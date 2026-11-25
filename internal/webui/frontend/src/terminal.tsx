import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { checkSessionAfterSocketFailure } from "./auth";
import { createPortal } from "react-dom";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { ErrorNotice, useResource } from "./request";
import { apiPath } from "./routes";
import type { InventoryResource } from "./inventory";
import { Icon } from "./icons";
import { ContainerLog } from "./container-log";
import { availableLifecycleActions } from "./lifecycle-policy.js";
import type { LifecycleAction, LifecycleFeedback } from "./lifecycle";
import { darkAnsi, darkTerminalBackground, lightAnsi, lightTerminalBackground } from "./terminal-palette.js";

type Container = { id: string; attachable?: boolean; root?: boolean };
export type TerminalState = "connecting" | "attached" | "disconnected" | "interrupted";
export type Session = { id: string; scope: string[]; container: string; state: TerminalState; message: string; generation: number };
// A paste waiting for its session: delivered once, when the session is attached and on screen.
type PendingPaste = { text: string; nonce: number };
const maxConnections = 20;
// A queued paste waits until the output received so far has been parsed and has been quiet this
// long, so xterm has seen the program's bracketed-paste request (\x1b[?2004h) before it pastes.
const pasteSettleMs = 150;
// A program that prints nothing still gets its paste, this long after it became deliverable.
const pasteMaxWaitMs = 2000;
const maxSessions = 20;
export const cellId = (scope: string[]) => JSON.stringify(scope);
export const sessionId = (scope: string[], container: string) => JSON.stringify([...scope, container]);
export const unavailableStates = new Set(["Stopped", "Stopping", "Exited", "Deleted", "Failed", "Error", "Pending", "Unknown"]);
const terminalTheme = { cursorAccent: "#000000", selectionBackground: "#4d4d4d" };
export type Theme = "system" | "light" | "dark";
export function terminalThemeFor(theme: Theme) {
  const dark = theme === "dark" || theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches;
  return { ...terminalTheme, ...(dark ? darkAnsi : lightAnsi), background: dark ? darkTerminalBackground : lightTerminalBackground, foreground: dark ? "#e5e6ec" : "#20232b", cursor: dark ? "#e5e6ec" : "#20232b" };
}

// This hook lives in App, above both the cell terminal and Resources routes.
export function useTerminalSessions(inventory: InventoryResource) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selection, setSelection] = useState<Record<string, string>>({});
  const retainedCells = useRef(new Set<string>());
  const [pastes, setPastes] = useState<Record<string, PendingPaste>>({});
  const pasteNonce = useRef(0);
  // Queue text for a session's terminal. It is pasted, never typed: no Enter is sent, so the
  // program in the container receives it without submitting it.
  const paste = useCallback((id: string, text: string) => {
    pasteNonce.current += 1;
    const nonce = pasteNonce.current;
    setPastes(old => ({ ...old, [id]: { text, nonce } }));
  }, []);
  const pasteDelivered = useCallback((id: string, nonce: number) => {
    setPastes(old => {
      if (old[id]?.nonce !== nonce) return old;
      const next = { ...old };
      delete next[id];
      return next;
    });
  }, []);
  // The latest selection, written in step with the state: an effect scheduled by an earlier render
  // reads it to see a choice made since that render (#1675).
  const picked = useRef<Record<string, string>>({});
  const forget = (keep: (key: string) => boolean) => {
    const next = Object.fromEntries(Object.entries(picked.current).filter(([key]) => keep(key)));
    if (Object.keys(next).length !== Object.keys(picked.current).length) picked.current = next;
  };
  const select = useCallback((scope: string[], container: string) => {
    picked.current = { ...picked.current, [cellId(scope)]: container };
    setSelection(old => ({ ...old, [cellId(scope)]: container }));
  }, []);
  const attach = useCallback((scope: string[], container: string) => {
    const id = sessionId(scope, container);
    setSessions(old => {
      const existing = old.find(item => item.id === id);
      if (existing?.state === "attached" || existing?.state === "connecting") return old;
      if (old.filter(item => item.state === "attached" || item.state === "connecting").length >= maxConnections) return old;
      if (!existing && old.length >= maxSessions) return old;
      if (existing) return old.map(item => item.id === id ? { ...item, generation: item.generation + 1, state: "connecting", message: "Connecting…" } : item);
      return [...old, { id, scope: [...scope], container, state: "connecting", message: "Connecting…", generation: 0 }];
    });
    select(scope, container);
  }, [select]);
  // The effect-driven attach. `seen` is the selection of the render that scheduled the effect; a
  // different one now means a container was chosen since, so the effect neither opens a session for
  // its stale container nor undoes the choice. User actions call attach.
  const autoAttach = useCallback((scope: string[], container: string, seen: string | undefined) => {
    if (picked.current[cellId(scope)] !== seen) return;
    attach(scope, container);
  }, [attach]);
  const detach = useCallback((id: string) => setSessions(old => old.filter(item => item.id !== id)), []);
  const removeCell = useCallback((scope: string[]) => {
    const key = cellId(scope);
    retainedCells.current.delete(key);
    setSessions(old => old.filter(item => cellId(item.scope) !== key));
    forget(id => id !== key);
    setSelection(old => {
      if (!(key in old)) return old;
      const next = { ...old };
      delete next[key];
      return next;
    });
  }, []);
  const interruptCell = useCallback((scope: string[], message: string) => {
    const key = cellId(scope);
    setSessions(old => old.map(item => cellId(item.scope) === key
      ? { ...item, generation: item.generation + 1, state: "interrupted", message }
      : item));
  }, []);
  // After a successful start the previous sessions are gone for good; dropping them lets the auto-attach
  // effect open a fresh terminal once the cell is Ready. Detached containers stay detached.
  const dropInterrupted = useCallback((scope: string[]) => {
    const key = cellId(scope);
    setSessions(old => old.filter(item => cellId(item.scope) !== key || item.state !== "interrupted"));
  }, []);
  const holdCell = useCallback((scope: string[]) => { retainedCells.current.add(cellId(scope)); }, []);
  const releaseCell = useCallback((scope: string[]) => { retainedCells.current.delete(cellId(scope)); }, []);
  const update = useCallback((id: string, generation: number, state: TerminalState, message: string) => {
    setSessions(old => old.map(item => item.id === id && item.generation === generation ? { ...item, state, message } : item));
  }, []);

  // A partial/failed refresh must not discard an attachment whose cell was omitted.
  useEffect(() => {
    if (!inventory.data || inventory.loading || inventory.error || inventory.data.failures.length) return;
    const states = new Map(inventory.data.rows[3].map(row => [cellId(row.scope), row.doc.status?.state ?? ""]));
    setSessions(old => old.filter(item => {
      const key = cellId(item.scope);
      const state = states.get(key);
      if (state === undefined) return false;
      if (retainedCells.current.has(key)) return true;
      return !unavailableStates.has(state) || item.state === "interrupted";
    }));
    forget(key => states.has(key));
    setSelection(old => {
      let changed = false;
      const next = { ...old };
      for (const key of Object.keys(next)) {
        if (!states.has(key)) {
          delete next[key];
          changed = true;
        }
      }
      return changed ? next : old;
    });
  }, [inventory.data, inventory.loading, inventory.error]);

  const cellStates = useMemo(() => {
    const result: Record<string, TerminalState> = {};
    for (const session of sessions) {
      const key = cellId(session.scope);
      if (session.state === "attached" || (session.state === "connecting" && result[key] !== "attached") || !result[key]) result[key] = session.state;
    }
    return result;
  }, [sessions]);
  return { sessions, selection, select, attach, autoAttach, detach, removeCell, interruptCell, dropInterrupted, holdCell, releaseCell, update, cellStates, pastes, paste, pasteDelivered };
}

export type TerminalManager = ReturnType<typeof useTerminalSessions>;
type Manager = TerminalManager;

export function TerminalSession({ session, active, expanded, theme, update, pending, onPasted }: { session: Session; active: boolean; expanded: boolean; theme: Theme; update: Manager["update"]; pending?: PendingPaste; onPasted?: Manager["pasteDelivered"] }) {
  const mount = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const socket = useRef<WebSocket | null>(null);
  const fitNow = useRef<() => void>(() => {});
  // Only a real size change reaches the container; a repeated fit must not SIGWINCH its process again.
  const sentSize = useRef("");
  const activeNow = useRef(active);
  const wasInterrupted = useRef(false);
  // Output frames received on the current connection, and how many of them xterm has parsed.
  const frames = useRef({ received: 0, parsed: 0 });
  const [drained, setDrained] = useState(0); // bumps when xterm has parsed every frame received so far
  activeNow.current = active;

  useEffect(() => {
    if (!mount.current) return;
    const term = new Terminal({ cursorBlink: true, cursorStyle: "underline", cursorInactiveStyle: "outline", convertEol: false,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace", fontSize: 14, scrollback: 2000,
      theme: terminalThemeFor(theme) });
    const fit = new FitAddon();
    terminal.current = term;
    term.loadAddon(fit);
    term.open(mount.current);
    let resizeFrame = 0;
    const resize = () => {
      if (!activeNow.current) return;
      if (resizeFrame) cancelAnimationFrame(resizeFrame);
      resizeFrame = requestAnimationFrame(() => {
        resizeFrame = 0;
        if (!activeNow.current || !mount.current?.clientWidth || !mount.current.clientHeight) return;
        fit.fit();
        const size = `${term.cols}x${term.rows}`;
        if (socket.current?.readyState !== WebSocket.OPEN || size === sentSize.current) return;
        sentSize.current = size;
        socket.current.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      });
    };
    fitNow.current = resize;
    const observer = new ResizeObserver(resize);
    observer.observe(mount.current);
    if (mount.current.parentElement) observer.observe(mount.current.parentElement);
    window.addEventListener("resize", resize);
    window.visualViewport?.addEventListener("resize", resize);
    const input = term.onData(data => {
      if (activeNow.current && socket.current?.readyState === WebSocket.OPEN) socket.current.send(new TextEncoder().encode(data));
    });
    return () => {
      observer.disconnect(); window.removeEventListener("resize", resize);
      window.visualViewport?.removeEventListener("resize", resize);
      if (resizeFrame) cancelAnimationFrame(resizeFrame);
      input.dispose(); term.dispose(); terminal.current = null;
      fitNow.current = () => {};
    };
  }, []);

  useEffect(() => {
    const applyTheme = () => { if (terminal.current) terminal.current.options.theme = terminalThemeFor(theme); };
    applyTheme();
    if (theme !== "system") return;
    const colorScheme = window.matchMedia("(prefers-color-scheme: dark)");
    colorScheme.addEventListener("change", applyTheme);
    return () => colorScheme.removeEventListener("change", applyTheme);
  }, [theme]);

  useEffect(() => {
    if (session.state === "interrupted") {
      terminal.current?.reset();
      terminal.current?.write("\r\nPrevious terminal session ended. Reconnect to attach to the current process.\r\n");
      wasInterrupted.current = true;
    } else if (session.state === "connecting" && wasInterrupted.current) {
      terminal.current?.reset();
      wasInterrupted.current = false;
    }
  }, [session.state, session.generation]);

  useEffect(() => {
    if (session.state === "disconnected" || session.state === "interrupted") return;
    let connection: WebSocket;
    try {
      connection = new WebSocket(`${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}${apiPath(session.scope)}/containers/${encodeURIComponent(session.container)}/attach`);
    } catch {
      update(session.id, session.generation, "disconnected", "Terminal connection failed. Check the server and retry.");
      return;
    }
    socket.current = connection;
    frames.current = { received: 0, parsed: 0 };
    connection.binaryType = "arraybuffer";
    let failure = "";
    let ready = false;
    let receivedOutput = false;
    let opened = false;
    connection.onopen = () => {
      opened = true;
      sentSize.current = "";
      fitNow.current();
    };
    connection.onmessage = event => {
      if (event.data instanceof ArrayBuffer) {
        const counts = frames.current;
        counts.received += 1;
        terminal.current?.write(new Uint8Array(event.data), () => {
          counts.parsed += 1;
          if (frames.current === counts && counts.parsed === counts.received) setDrained(count => count + 1);
        });
        if (!receivedOutput) {
          receivedOutput = true;
          if (ready) update(session.id, session.generation, "attached", "Attached");
        }
      }
      else if (typeof event.data === "string") {
        try {
          const response = JSON.parse(event.data);
          if (response.type === "ready") {
            ready = true;
            update(session.id, session.generation, "attached", receivedOutput ? "Attached" : "Attached — waiting for container output. A prompt appears when the container runs an interactive shell.");
          }
          else if (response.type === "error") failure = typeof response.message === "string" ? response.message : "Terminal server error.";
          else failure = "Invalid terminal response.";
        } catch { failure = "Invalid terminal response."; }
        if (failure) { update(session.id, session.generation, "disconnected", failure); connection.close(); }
      }
    };
    // A refused upgrade hides its status code; ask the session route so a 401 signs the UI out (#1652).
    const checkSession = () => { if (!opened) { opened = true; checkSessionAfterSocketFailure(); } };
    connection.onerror = () => { checkSession(); failure ||= "Terminal connection failed. Check the server and retry."; update(session.id, session.generation, "disconnected", failure); connection.close(); };
    connection.onclose = () => { checkSession(); update(session.id, session.generation, "disconnected", failure || "Terminal connection closed. Reconnect to attach again."); };
    return () => {
      connection.onopen = connection.onmessage = connection.onerror = connection.onclose = null;
      connection.close();
      if (socket.current === connection) socket.current = null;
    };
  }, [session.id, session.generation, session.scope, session.container, update]);

  useEffect(() => {
    if (!active) { terminal.current?.blur(); return; }
    // A session activated from a tab list (the majordomo tabs) leaves focus on the tab, so the arrow keys
    // keep moving between tabs instead of typing into the terminal.
    const frame = requestAnimationFrame(() => { fitNow.current(); if (!document.activeElement?.closest('[role="tablist"]')) terminal.current?.focus(); });
    return () => cancelAnimationFrame(frame);
  }, [active]);

  // Deliver a queued paste once the session is attached and on screen. xterm wraps it in
  // bracketed-paste markers only when it has already parsed the program's request for them, and
  // the server sends "ready" before any output frame, so the paste also waits for output: until
  // every frame received so far is parsed and none has arrived for pasteSettleMs, or at most
  // pasteMaxWaitMs for a program that prints nothing. It is sent once, as one write, never submitted.
  const waitingSince = useRef<{ nonce: number; at: number } | null>(null);
  useEffect(() => {
    if (!pending || !active || session.state !== "attached" || socket.current?.readyState !== WebSocket.OPEN) return;
    if (waitingSince.current?.nonce !== pending.nonce) waitingSince.current = { nonce: pending.nonce, at: Date.now() };
    const { received, parsed } = frames.current;
    const settled = received > 0 && parsed === received;
    const remaining = Math.max(0, waitingSince.current.at + pasteMaxWaitMs - Date.now());
    const timer = setTimeout(() => {
      terminal.current?.paste(pending.text);
      terminal.current?.focus();
      onPasted?.(session.id, pending.nonce);
    }, settled ? Math.min(pasteSettleMs, remaining) : remaining);
    return () => clearTimeout(timer);
  }, [pending, active, session.state, session.id, onPasted, drained]);

  useEffect(() => {
    if (!active || !expanded) return;
    const frame = requestAnimationFrame(() => { fitNow.current(); terminal.current?.focus(); });
    return () => cancelAnimationFrame(frame);
  }, [active, expanded]);

  return <div className="terminal-session" hidden={!active} aria-hidden={!active}>
    <div className="terminal-screen" aria-label={`Terminal for ${session.scope.join(" / ")} / ${session.container}`}>
      {/* FitAddon sizes from the xterm parent's computed box; keep padding off it so rows/cols never spill under the panel border. */}
      <div ref={mount} className="terminal-mount" />
    </div>
  </div>;
}

// dockedCells are the cells whose terminals live in the majordomo panel, by cellId. Their sessions are
// mounted there and nowhere else, so this workspace neither renders nor attaches them.
export function TerminalWorkspace({ scope, revision, inventory, manager, theme, visible = true, controlsHost, onExpandedChange, dockedCells = [], onOpenDock, operation, onLifecycleAction }: { scope: string[] | null; revision: number; inventory: InventoryResource; manager: Manager; theme: Theme; visible?: boolean; controlsHost: HTMLDivElement | null; onExpandedChange: (expanded: boolean) => void; dockedCells?: string[]; onOpenDock?: () => void; operation?: LifecycleFeedback; onLifecycleAction?: (scope: string[], action: LifecycleAction, state?: string) => void }) {
  const [expanded, setExpanded] = useState(false);
  const [skipAutoAttachFor, setSkipAutoAttachFor] = useState("");
  const expandButton = useRef<HTMLButtonElement>(null);
  const screens = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!scope) {
      setExpanded(false);
      onExpandedChange(false);
    }
  }, [scope, onExpandedChange]);
  useEffect(() => {
    if (!visible && expanded) {
      setExpanded(false);
      onExpandedChange(false);
    }
  }, [visible, expanded, onExpandedChange]);
  useEffect(() => {
    if (!expanded) return;
    const escape = (event: KeyboardEvent) => {
      // An open disclosure (the Advanced actions menu above the terminal) takes its own Escape first.
      if (event.key !== "Escape" || screens.current?.contains(event.target as Node) || (event.target as Element | null)?.closest?.("details[open]")) return;
      event.preventDefault();
      event.stopPropagation();
      setExpanded(false);
      onExpandedChange(false);
      requestAnimationFrame(() => expandButton.current?.focus());
    };
    window.addEventListener("keydown", escape, true);
    return () => window.removeEventListener("keydown", escape, true);
  }, [expanded]);
  const path = scope?.length === 4 ? `${apiPath(scope)}/containers` : null;
  const containers = useResource<Container[]>(path, revision);
  const workloads = containers.data?.filter(container => !container.root) ?? [];
  const eligible = workloads.filter(container => container.attachable);
  // Containers without a terminal are still listed: their output shows in the log view.
  const logOnly = workloads.filter(container => !container.attachable);
  const key = scope ? cellId(scope) : "";
  const docked = !!key && dockedCells.includes(key);
  const openInCell = manager.sessions.filter(item => cellId(item.scope) === key);
  // Attachable first, then log-only, then retained sessions whose container is gone: the default stays the first attachable one.
  const options = [
    ...eligible.map(container => container.id),
    ...logOnly.map(container => container.id),
    ...openInCell.map(item => item.container).filter(id => !workloads.some(container => container.id === id)),
  ];
  const selected = options.includes(manager.selection[key]) ? manager.selection[key] : options[0] ?? "";
  const currentId = scope && selected ? sessionId(scope, selected) : "";
  const current = manager.sessions.find(item => item.id === currentId);
  const selectedEligible = eligible.some(container => container.id === selected);
  const connections = manager.sessions.filter(item => item.state === "attached" || item.state === "connecting").length;
  const cellState = inventory.data?.rows[3].find(row => cellId(row.scope) === key)?.doc.status?.state;
  const unavailable = cellState && unavailableStates.has(cellState);
  const selectedListed = workloads.some(container => container.id === selected);
  // A live terminal needs an attachable container in a live cell; everything else that is listed shows its log.
  const mode: "terminal" | "logs" = scope && !docked && selectedListed && (!selectedEligible || unavailable) ? "logs" : "terminal";
  const logs = mode === "logs";
  const atLimit = !current && (connections >= maxConnections || manager.sessions.length >= maxSessions);
  const reconnectLimited = (current?.state === "disconnected" || current?.state === "interrupted") && connections >= maxConnections;
  useEffect(() => { setSkipAutoAttachFor(""); }, [key]);
  useEffect(() => {
    // Wait for the inventory: it is what tells whether this cell's terminal is docked.
    // Only cellTerminal attaches: cellRead keeps this workspace mounted so existing sessions
    // survive, but inspecting a cell must not open a new terminal session (#1566).
    if (!visible || !scope || docked || logs || !inventory.data || containers.loading || containers.error || !selected || !selectedEligible || unavailable || atLimit || current || skipAutoAttachFor === currentId) return;
    manager.autoAttach(scope, selected, manager.selection[key]);
  }, [visible, key, docked, !inventory.data, selected, selectedEligible, containers.loading, containers.error, unavailable, atLimit, current, currentId, skipAutoAttachFor, logs, manager.autoAttach]);
  const headerState = logs ? "Logs" : current?.state === "attached" ? "Attached"
    : current?.message || (skipAutoAttachFor === currentId && currentId ? "Detached" : atLimit ? "Connection limit reached" : unavailable ? `Unavailable: ${cellState}` : containers.loading ? "Loading containers…" : containers.error ? "Container list unavailable" : !eligible.length ? "No attachable containers" : !selectedEligible ? "Container unavailable" : "Connecting…");
  // With nothing to attach to, the terminal area carries the only (neutral) message.
  const empty = !!scope && !containers.loading && !containers.error && !workloads.length && !options.length;
  const live = current?.state === "attached" || current?.state === "connecting";
  const limitMessage = "Terminal limit reached. Detach another terminal to free a connection or retained slot.";
  const staleContainers = containers.error && containers.data ? "Container list is stale. Refresh before attaching." : "";
  // The title row never carries these messages: the state dot's tooltip does, and the terminal area
  // shows an overlay (which cannot change the terminal's size) only while nothing is attached.
  const tooltip = logs ? unavailable ? `Cell is ${cellState}. Showing the last output of ${selected}.` : `Showing the output of ${selected}.`
    : atLimit || reconnectLimited ? limitMessage
    : unavailable && !live ? `Terminal unavailable: cell is ${cellState}.`
      : containers.error && !live ? `${headerState}: ${containers.error.message}`
        : staleContainers && !live ? staleContainers
          : current?.message || headerState;
  const overlay = !scope || empty || live || logs ? null
    : atLimit || reconnectLimited ? <p className="terminal-limit" role="status">{limitMessage}</p>
      : unavailable ? <p className="terminal-unavailable" role="status">Terminal unavailable: cell is {cellState}.</p>
        : containers.error ? <><ErrorNotice error={containers.error} kind="Containers" />{staleContainers && <p className="stale">{staleContainers}</p>}</>
          : containers.loading && !containers.data ? <p role="status">Loading containers…</p>
            : null;
  const headerTone = logs ? "logs" : current?.state ?? (skipAutoAttachFor === currentId && currentId || atLimit || unavailable || containers.error || !eligible.length || !selectedEligible ? "disconnected" : "connecting");
  const toggleExpanded = () => { const next = !expanded; setExpanded(next); onExpandedChange(next); };
  const resume = () => { setSkipAutoAttachFor(""); manager.attach(scope!, selected); };
  // Attach, Detach and Reconnect swap within one slot. A dropped session also keeps Detach so its retained slot can be released.
  const connectButton = (label: "Attach" | "Detach" | "Reconnect", disabled: boolean, onClick: () => void) =>
    <button type="button" className="icon-button" aria-label={label} data-tooltip={label} disabled={disabled} onClick={onClick}><Icon name={label === "Attach" ? "attach" : label === "Detach" ? "detach" : "reconnect"} /></button>;
  const connectSlot = current?.state === "attached" || current?.state === "connecting" ? connectButton("Detach", false, () => { setSkipAutoAttachFor(current.id); manager.detach(current.id); })
    : current ? <>{connectButton("Reconnect", reconnectLimited || !selectedEligible || !!unavailable || !!containers.error, resume)}{connectButton("Detach", false, () => { setSkipAutoAttachFor(current.id); manager.detach(current.id); })}</>
    : skipAutoAttachFor === currentId && currentId ? connectButton("Attach", !!atLimit || !selectedEligible || !!unavailable || !!containers.error, resume) : null;
  const optionLabel = (id: string) => eligible.some(container => container.id === id) ? id : logOnly.some(container => container.id === id) ? `${id} (logs)` : `${id} (unavailable)`;
  const controls = scope && !docked && <div className="terminal-workspace-tools">
    {!empty && <span className={`terminal-connection-state ${headerTone}`} role="status" tabIndex={0} title={tooltip} data-tooltip={tooltip}><span className="state-dot" aria-hidden="true" /><span className="sr-only">{headerState}</span></span>}
    <div className="terminal-controls">
      {!!options.length && <><label htmlFor="terminal-container" className="sr-only">Container</label><select id="terminal-container" className="mono-id" value={selected} onChange={event => manager.select(scope, event.target.value)}>{options.map(id => <option key={id} value={id}>{optionLabel(id)}</option>)}</select>
        {!logs && connectSlot}</>}
      {!empty && <button ref={expandButton} type="button" className="icon-button" aria-label={expanded ? "Collapse terminal" : "Expand terminal"} data-tooltip={expanded ? "Collapse terminal" : "Expand terminal"} aria-expanded={expanded} aria-controls="terminal-screens" onClick={toggleExpanded}><Icon name={expanded ? "collapse" : "expand"} /></button>}
    </div>
  </div>;
  return <>
    {controls && controlsHost && createPortal(controls, controlsHost)}
    <section className={`panel terminal-workspace${expanded ? " is-expanded" : ""}${empty ? " is-empty" : ""}`} id="cell-terminal" hidden={!scope} aria-label="Terminal workspace">
      {docked ? <div className="empty-state terminal-docked" role="status"><p>This cell's terminal is docked in the Majordomo panel.</p><button type="button" onClick={onOpenDock}>Open the Majordomo panel</button></div> : <>
        {empty && <p className="empty-state terminal-empty" role="status">No containers are available.</p>}
        {overlay && <div className="terminal-overlay">{overlay}</div>}
        {logs && unavailable && <div className="terminal-log-status" role="status">
          <span>Cell is {cellState}. Showing the last output of <code>{selected}</code>.</span>
          {availableLifecycleActions(cellState).includes("start") && <button type="button" disabled={operation?.phase === "pending"} onClick={() => onLifecycleAction?.(scope!, "start", cellState)}>Start</button>}
        </div>}
      </>}
      <div className="terminal-screens" id="terminal-screens" ref={screens}>
        {manager.sessions.filter(session => !dockedCells.includes(cellId(session.scope))).map(session => <TerminalSession key={session.id} session={session} active={!!scope && visible && !logs && session.id === currentId} expanded={expanded} theme={theme} update={manager.update} pending={manager.pastes[session.id]} onPasted={manager.pasteDelivered} />)}
        {/* Not mounting the log while hidden is what keeps cellRead free of log requests (#1566). */}
        {visible && logs && <ContainerLog key={sessionId(scope!, selected)} scope={scope!} container={selected} attachable={selectedEligible} live={!unavailable} cellState={cellState} revision={revision} theme={theme} expanded={expanded} />}
      </div>
    </section>
  </>;
}
