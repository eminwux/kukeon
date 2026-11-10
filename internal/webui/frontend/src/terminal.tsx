import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { ErrorNotice, useResource } from "./request";
import { apiPath } from "./routes";
import type { InventoryResource } from "./inventory";

type Container = { id: string; attachable?: boolean; root?: boolean };
export type TerminalState = "connecting" | "attached" | "disconnected" | "interrupted";
type Session = { id: string; scope: string[]; container: string; state: TerminalState; message: string; generation: number };
const maxConnections = 20;
const maxSessions = 20;
const cellId = (scope: string[]) => JSON.stringify(scope);
const sessionId = (scope: string[], container: string) => JSON.stringify([...scope, container]);
const unavailableStates = new Set(["Stopped", "Stopping", "Exited", "Deleted", "Failed", "Error", "Pending", "Unknown"]);
const terminalTheme = {
  background: "#000000", foreground: "#ffffff", cursor: "#ffffff", cursorAccent: "#000000", selectionBackground: "#4d4d4d",
  black: "#000000", red: "#cd0000", green: "#00cd00", yellow: "#cdcd00", blue: "#0000ee", magenta: "#cd00cd", cyan: "#00cdcd", white: "#e5e5e5",
  brightBlack: "#7f7f7f", brightRed: "#ff0000", brightGreen: "#00ff00", brightYellow: "#ffff00", brightBlue: "#5c5cff", brightMagenta: "#ff00ff", brightCyan: "#00ffff", brightWhite: "#ffffff",
};
type Theme = "system" | "light" | "dark";
function terminalThemeFor(theme: Theme) {
  const dark = theme === "dark" || theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches;
  return { ...terminalTheme, background: dark ? "#0d1421" : "#f8fafc", foreground: dark ? "#e5e6ec" : "#20232b", cursor: dark ? "#e5e6ec" : "#20232b" };
}

// This hook lives in App, above both Workspace and Resources routes.
export function useTerminalSessions(inventory: InventoryResource) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selection, setSelection] = useState<Record<string, string>>({});
  const retainedCells = useRef(new Set<string>());
  const select = useCallback((scope: string[], container: string) => {
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
  const detach = useCallback((id: string) => setSessions(old => old.filter(item => item.id !== id)), []);
  const interruptCell = useCallback((scope: string[], message: string) => {
    const key = cellId(scope);
    setSessions(old => old.map(item => cellId(item.scope) === key
      ? { ...item, generation: item.generation + 1, state: "interrupted", message }
      : item));
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
  }, [inventory.data, inventory.loading, inventory.error]);

  const cellStates = useMemo(() => {
    const result: Record<string, TerminalState> = {};
    for (const session of sessions) {
      const key = cellId(session.scope);
      if (session.state === "attached" || (session.state === "connecting" && result[key] !== "attached") || !result[key]) result[key] = session.state;
    }
    return result;
  }, [sessions]);
  return { sessions, selection, select, attach, detach, interruptCell, holdCell, releaseCell, update, cellStates };
}

export type TerminalManager = ReturnType<typeof useTerminalSessions>;
type Manager = TerminalManager;

function TerminalSession({ session, active, expanded, theme, update }: { session: Session; active: boolean; expanded: boolean; theme: Theme; update: Manager["update"] }) {
  const mount = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const socket = useRef<WebSocket | null>(null);
  const fitNow = useRef<() => void>(() => {});
  const activeNow = useRef(active);
  const wasInterrupted = useRef(false);
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
        if (socket.current?.readyState === WebSocket.OPEN) socket.current.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
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
    connection.binaryType = "arraybuffer";
    let failure = "";
    let ready = false;
    let receivedOutput = false;
    connection.onopen = () => {
      fitNow.current();
    };
    connection.onmessage = event => {
      if (event.data instanceof ArrayBuffer) {
        terminal.current?.write(new Uint8Array(event.data));
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
    connection.onerror = () => { failure ||= "Terminal connection failed. Check the server and retry."; update(session.id, session.generation, "disconnected", failure); connection.close(); };
    connection.onclose = () => update(session.id, session.generation, "disconnected", failure || "Terminal connection closed. Reconnect to attach again.");
    return () => {
      connection.onopen = connection.onmessage = connection.onerror = connection.onclose = null;
      connection.close();
      if (socket.current === connection) socket.current = null;
    };
  }, [session.id, session.generation, session.scope, session.container, update]);

  useEffect(() => {
    if (!active) { terminal.current?.blur(); return; }
    const frame = requestAnimationFrame(() => { fitNow.current(); terminal.current?.focus(); });
    return () => cancelAnimationFrame(frame);
  }, [active]);

  useEffect(() => {
    if (!active || !expanded) return;
    const frame = requestAnimationFrame(() => { fitNow.current(); terminal.current?.focus(); });
    return () => cancelAnimationFrame(frame);
  }, [active, expanded]);

  return <div className="terminal-session" hidden={!active} aria-hidden={!active}>
    <div ref={mount} className="terminal-screen" aria-label={`Terminal for ${session.scope.join(" / ")} / ${session.container}`} />
  </div>;
}

export function TerminalWorkspace({ scope, revision, inventory, manager, theme, visible = true, controlsHost, onExpandedChange }: { scope: string[] | null; revision: number; inventory: InventoryResource; manager: Manager; theme: Theme; visible?: boolean; controlsHost: HTMLDivElement | null; onExpandedChange: (expanded: boolean) => void }) {
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
      if (event.key !== "Escape" || screens.current?.contains(event.target as Node)) return;
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
  const eligible = containers.data?.filter(container => container.attachable && !container.root) ?? [];
  const key = scope ? cellId(scope) : "";
  const openInCell = manager.sessions.filter(item => cellId(item.scope) === key);
  const options = [...eligible.map(container => container.id), ...openInCell.map(item => item.container).filter(id => !eligible.some(container => container.id === id))];
  const selected = options.includes(manager.selection[key]) ? manager.selection[key] : options[0] ?? "";
  const currentId = scope && selected ? sessionId(scope, selected) : "";
  const current = manager.sessions.find(item => item.id === currentId);
  const selectedEligible = eligible.some(container => container.id === selected);
  const connections = manager.sessions.filter(item => item.state === "attached" || item.state === "connecting").length;
  const cellState = inventory.data?.rows[3].find(row => cellId(row.scope) === key)?.doc.status?.state;
  const unavailable = cellState && unavailableStates.has(cellState);
  const atLimit = !current && (connections >= maxConnections || manager.sessions.length >= maxSessions);
  const reconnectLimited = (current?.state === "disconnected" || current?.state === "interrupted") && connections >= maxConnections;
  useEffect(() => { setSkipAutoAttachFor(""); }, [key]);
  useEffect(() => {
    if (!scope || containers.loading || containers.error || !selected || !selectedEligible || unavailable || atLimit || current || skipAutoAttachFor === currentId) return;
    manager.attach(scope, selected);
  }, [key, selected, selectedEligible, containers.loading, containers.error, unavailable, atLimit, current, currentId, skipAutoAttachFor, manager.attach]);
  const headerState = current?.state === "attached" ? "Attached"
    : current?.message || (skipAutoAttachFor === currentId && currentId ? "Detached" : atLimit ? "Connection limit reached" : unavailable ? `Unavailable: ${cellState}` : containers.loading ? "Loading containers…" : containers.error ? "Container list unavailable" : !eligible.length ? "No attachable containers" : !selectedEligible ? "Container unavailable" : "Connecting…");
  const headerTone = current?.state ?? (skipAutoAttachFor === currentId && currentId || atLimit || unavailable || containers.error || !eligible.length || !selectedEligible ? "disconnected" : "connecting");
  const controls = scope && <div className="terminal-workspace-tools">
    <div className="terminal-tool-status"><span className={`terminal-connection-state ${headerTone}`} role="status" title={current?.message}>{headerState}</span><button ref={expandButton} type="button" aria-expanded={expanded} aria-controls="terminal-screens" onClick={() => { const next = !expanded; setExpanded(next); onExpandedChange(next); }}>{expanded ? "Collapse terminal" : "Expand terminal"}</button></div>
    <div className="terminal-controls">
      {containers.loading && <p role="status">Loading containers…</p>}
      {containers.error && <ErrorNotice error={containers.error} kind="Containers" />}
      {containers.error && containers.data && <p className="stale">Container list is stale. Refresh before attaching.</p>}
      {!containers.loading && !containers.error && !eligible.length && <p>No attachable containers are available.</p>}
      {!!options.length && <><label htmlFor="terminal-container">Container</label><select id="terminal-container" value={selected} onChange={event => manager.select(scope, event.target.value)}>{options.map(id => <option key={id} value={id}>{id}{eligible.some(container => container.id === id) ? "" : " (unavailable)"}</option>)}</select>
        {current?.state === "attached" || current?.state === "connecting" ? <button onClick={() => { setSkipAutoAttachFor(current.id); manager.detach(current.id); }}>Detach</button> : current ? <><button disabled={reconnectLimited || !selectedEligible || !!unavailable || !!containers.error} onClick={() => { setSkipAutoAttachFor(""); manager.attach(scope, selected); }}>Reconnect</button><button onClick={() => { setSkipAutoAttachFor(current.id); manager.detach(current.id); }}>Detach</button></> : skipAutoAttachFor === currentId && currentId ? <button disabled={!!atLimit || !selectedEligible || !!unavailable || !!containers.error} onClick={() => { setSkipAutoAttachFor(""); manager.attach(scope, selected); }}>Attach</button> : null}</>}
      {unavailable && <p className="terminal-unavailable" role="status">Terminal unavailable: cell is {cellState}.</p>}
      {(atLimit || reconnectLimited) && <p className="terminal-limit" role="status">Terminal limit reached. Detach another terminal to free a connection or retained slot.</p>}
    </div>
  </div>;
  return <>
    {controls && controlsHost && createPortal(controls, controlsHost)}
    <section className={`panel terminal-workspace${expanded ? " is-expanded" : ""}`} id="cell-terminal" hidden={!scope} aria-label="Terminal workspace">
      <div className="terminal-screens" id="terminal-screens" ref={screens}>{manager.sessions.map(session => <TerminalSession key={session.id} session={session} active={!!scope && visible && session.id === currentId} expanded={expanded} theme={theme} update={manager.update} />)}</div>
    </section>
  </>;
}
