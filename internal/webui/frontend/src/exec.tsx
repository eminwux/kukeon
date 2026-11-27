import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { ErrorNotice, toFailure, type Failure } from "./request";
import { apiPath } from "./routes";
import { rememberFocus, useModalDialog } from "./focus";
import { Icon } from "./icons";
import { TerminalSession, sessionId, type Session, type TerminalState, type Theme } from "./terminal";
import { apiFetch } from "./transport";

// `kuke exec` in the browser (#1432): run one command, or open a shell, in a container of a running
// cell. Both run as the container's own user unless root is chosen, and root is chosen only through
// a confirmation; the server refuses a root request that does not carry it.

type Identity = "container" | "root";
type Mode = "run" | "terminal";
type RunResult = { stdout: string; stderr: string; exitCode: number; truncated: boolean; timedOut: boolean };
type ShellState = { generation: number; state: TerminalState; message: string };
const modes: { id: Mode; label: string }[] = [{ id: "run", label: "Run command" }, { id: "terminal", label: "Terminal" }];

// The Exec action of the selected container. A cell that is not running disables it and says why.
export function ExecButton({ container, open, reason, onToggle }: { container: string; open: boolean; reason: string; onToggle: () => void }) {
  const tooltip = reason || (open ? `Close Exec for ${container}` : `Exec: run a command or open a shell in ${container}`);
  return <button type="button" className="icon-button exec-button" aria-label="Exec" aria-expanded={open} title={tooltip} data-tooltip={tooltip} disabled={!!reason} onClick={onToggle}><Icon name="exec" /></button>;
}

function RootConfirmation({ target, onCancel, onConfirm }: { target: string; onCancel: () => void; onConfirm: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useModalDialog(dialog);
  return <dialog
    className="cell-action-dialog exec-root-dialog"
    ref={dialog}
    aria-labelledby="exec-root-dialog-title"
    aria-describedby="exec-root-dialog-description"
    onCancel={event => { event.preventDefault(); onCancel(); }}
    onClick={event => { if (event.target === event.currentTarget) onCancel(); }}
  >
    <form onSubmit={event => { event.preventDefault(); onConfirm(); }}>
      <h2 id="exec-root-dialog-title">Run as root</h2>
      <p id="exec-root-dialog-description">Commands and shells started from this panel will run as root (uid 0, gid 0) inside the container, whatever user the container itself runs as. kukeond records every root exec in its log. Changes made this way are lost when the cell is recreated.</p>
      <dl className="cell-action-target"><dt>Container</dt><dd><code>{target}</code></dd></dl>
      <div className="cell-action-dialog-buttons">
        <button type="button" onClick={onCancel}>Cancel</button>
        <button className="danger-action" type="submit">Run as root</button>
      </div>
    </form>
  </dialog>;
}

export function ExecPanel({ scope, container, theme, visible, expanded, onClose }: { scope: string[]; container: string; theme: Theme; visible: boolean; expanded: boolean; onClose: () => void }) {
  const id = useId();
  const target = [...scope, container].join(" / ");
  const base = `${apiPath(scope)}/containers/${encodeURIComponent(container)}/exec`;
  const panel = useRef<HTMLElement>(null);
  const commandInput = useRef<HTMLInputElement>(null);
  const [identity, setIdentity] = useState<Identity>("container");
  // Root is confirmed once per panel; until then no request names it.
  const [rootConfirmed, setRootConfirmed] = useState(false);
  const [askingRoot, setAskingRoot] = useState(false);
  const [mode, setMode] = useState<Mode>("run");
  const [command, setCommand] = useState("");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<RunResult>();
  const [failure, setFailure] = useState<Failure>();
  // The shell starts the first time the Terminal tab is shown and then stays mounted behind the Run tab.
  const [shellOpened, setShellOpened] = useState(false);
  const [shell, setShell] = useState<ShellState>({ generation: 0, state: "connecting", message: "Connecting…" });
  const asRoot = identity === "root" && rootConfirmed;

  // Focus enters the panel when it opens and returns to the Exec button when it closes.
  useEffect(() => {
    const restore = rememberFocus();
    commandInput.current?.focus();
    return restore;
  }, []);

  const choose = (next: Identity) => {
    if (next === identity) return;
    if (next === "root" && !rootConfirmed) { setAskingRoot(true); return; }
    setIdentity(next);
    restartShell();
  };
  const confirmRoot = () => { setAskingRoot(false); setRootConfirmed(true); setIdentity("root"); restartShell(); };
  // A new generation is a new process: the previous terminal unmounts, which closes its WebSocket.
  const restartShell = () => setShell(old => ({ generation: old.generation + 1, state: "connecting", message: "Connecting…" }));

  async function run(event: FormEvent) {
    event.preventDefault();
    if (running || !command.trim()) return;
    setRunning(true); setFailure(undefined);
    try {
      const response = await apiFetch(base, {
        method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
        body: JSON.stringify({ command, identity: asRoot ? "root" : "container", ...(asRoot ? { confirmRoot: true } : {}) }),
      });
      const body = await response.json();
      if (!response.ok) throw body.error ?? { code: "http_error", message: `HTTP ${response.status}` };
      setResult(body as RunResult);
    } catch (error) {
      setResult(undefined);
      setFailure(toFailure(error));
    } finally { setRunning(false); }
  }

  const scopeKey = JSON.stringify(scope);
  const session: Session = useMemo(
    () => ({ id: `exec:${sessionId(scope, container)}`, scope, container, state: shell.state, message: shell.message, generation: shell.generation }),
    [scopeKey, container, shell.state, shell.message, shell.generation],
  );
  const updateShell = useCallback((_id: string, generation: number, state: TerminalState, message: string) => {
    setShell(old => old.generation === generation ? { ...old, state, message } : old);
  }, []);
  const endpoint = useMemo(
    () => ({ path: `${base}/terminal?identity=${asRoot ? "root&confirmRoot=1" : "container"}`, label: `Exec terminal for ${target}` }),
    [base, asRoot, target],
  );

  const select = (next: Mode) => { setMode(next); if (next === "terminal") setShellOpened(true); };
  function tabKeys(event: KeyboardEvent<HTMLDivElement>) {
    const index = modes.findIndex(item => item.id === mode);
    const next = event.key === "ArrowRight" ? index + 1 : event.key === "ArrowLeft" ? index - 1 : event.key === "Home" ? 0 : event.key === "End" ? modes.length - 1 : null;
    if (next === null) return;
    event.preventDefault();
    select(modes[(next + modes.length) % modes.length].id);
    requestAnimationFrame(() => panel.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.focus());
  }
  // Escape closes the panel, except from the terminal, where it belongs to the shell.
  function escape(event: KeyboardEvent<HTMLElement>) {
    if (event.key !== "Escape" || (event.target as Element).closest(".terminal-screen, dialog")) return;
    event.preventDefault();
    event.stopPropagation();
    onClose();
  }

  const shellEnded = shell.state === "disconnected";
  return <section className={`exec-panel${expanded ? " is-expanded" : ""}`} ref={panel} role="group" aria-labelledby={`${id}-title`} onKeyDown={escape}>
    <header className="exec-panel-header">
      <h2 id={`${id}-title`}>Exec in <code>{container}</code></h2>
      <fieldset className="exec-identity">
        <legend>Run as</legend>
        <label><input type="radio" name={`${id}-identity`} checked={identity === "container"} onChange={() => choose("container")} />Container user</label>
        <label><input type="radio" name={`${id}-identity`} checked={identity === "root"} onChange={() => choose("root")} />root</label>
      </fieldset>
      <div className="exec-tabs" role="tablist" aria-label="Exec mode" onKeyDown={tabKeys}>
        {modes.map(item => <button key={item.id} type="button" role="tab" className="exec-tab" id={`${id}-tab-${item.id}`} aria-selected={mode === item.id} aria-controls={`${id}-panel-${item.id}`} tabIndex={mode === item.id ? 0 : -1} onClick={() => select(item.id)}>{item.label}</button>)}
      </div>
      <button type="button" className="exec-close" aria-label="Close Exec" onClick={onClose}>Close</button>
    </header>
    <div className="exec-run" role="tabpanel" id={`${id}-panel-run`} aria-labelledby={`${id}-tab-run`} hidden={mode !== "run"}>
      <form className="exec-run-form" onSubmit={run}>
        <label htmlFor={`${id}-command`}>Command <span className="exec-hint">one line, run as <code>sh -c</code>{asRoot ? " as root" : ""}</span></label>
        <div className="exec-run-line">
          <input ref={commandInput} id={`${id}-command`} className="mono-id" autoComplete="off" autoCapitalize="off" spellCheck={false} placeholder="id -u" value={command} onChange={event => setCommand(event.target.value)} />
          <button type="submit" className="primary-action" disabled={running || !command.trim()}>{running ? "Running…" : "Run"}</button>
        </div>
      </form>
      {failure && <ErrorNotice error={failure} kind="Container" />}
      {result && <div className="exec-result" role="status">
        <p className="exec-exit">{result.timedOut ? "Stopped after the time limit; no exit code." : <>Exit code <code>{result.exitCode}</code></>}</p>
        {result.timedOut && <p className="stale">The command ran longer than 60 seconds and was killed. The output up to that point is below.</p>}
        {result.truncated && <p className="stale">Output above 1 MiB per stream was dropped.</p>}
        <h3 id={`${id}-stdout`}>stdout</h3>
        <pre className="exec-output" aria-labelledby={`${id}-stdout`} tabIndex={0}>{result.stdout || <span className="exec-empty">(empty)</span>}</pre>
        <h3 id={`${id}-stderr`}>stderr</h3>
        <pre className="exec-output" aria-labelledby={`${id}-stderr`} tabIndex={0}>{result.stderr || <span className="exec-empty">(empty)</span>}</pre>
      </div>}
    </div>
    <div className="exec-terminal" role="tabpanel" id={`${id}-panel-terminal`} aria-labelledby={`${id}-tab-terminal`} hidden={mode !== "terminal"}>
      <div className="exec-terminal-status">
        <p role="status">{shell.message}{asRoot && !shellEnded ? " (root)" : ""}</p>
        {shellEnded && <button type="button" onClick={restartShell}>New shell</button>}
      </div>
      {shellOpened && <TerminalSession key={`${identity}:${shell.generation}`} session={session} active={visible && mode === "terminal"} expanded={expanded} theme={theme} update={updateShell} endpoint={endpoint} />}
    </div>
    {askingRoot && <RootConfirmation target={target} onCancel={() => setAskingRoot(false)} onConfirm={confirmRoot} />}
  </section>;
}
