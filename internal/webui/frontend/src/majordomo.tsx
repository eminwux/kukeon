import { createContext, useContext, useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { useResource, ErrorNotice, type Resource } from "./request";
import type { InventoryResource, Row, Doc, RuntimeStatus } from "./inventory";
import { apiPath } from "./routes";
import { StateBadge } from "./status";
import { Icon } from "./icons";
import { TerminalSession, cellId, sessionId, unavailableStates, type TerminalManager, type Theme } from "./terminal";

// Majordomo is found by label, never by name: its configs and cells carry the kuke team and the
// majordomo role, and a task config additionally names its task.
const teamLabel = "kukeon.io/team";
const roleLabel = "kukeon.io/role";
const taskLabel = "kukeon.io/task";
const team = "kuke";
const role = "majordomo";
export const sweepTask = "health-sweep";
// The one interactive cell the web UI creates.
const cellName = "majordomo";

export type ScheduleStatus = { lastScheduleTime?: string; nextScheduleTime?: string; lastCell?: string; activeCells?: string[]; skippedFires?: number; lastError?: string };
export type ConfigDoc = {
  metadata: { name: string; realm?: string; space?: string; stack?: string; labels?: Record<string, string> };
  spec?: { blueprint?: { name?: string }; schedule?: { cron?: string; timezone?: string; suspend?: boolean }; [key: string]: unknown };
  status?: { schedule?: ScheduleStatus };
};
type Labels = Record<string, string> | undefined;

function isMajordomo(labels: Labels): boolean {
  return labels?.[teamLabel] === team && labels?.[roleLabel] === role;
}
export function isMajordomoConfig(doc: ConfigDoc): boolean {
  return isMajordomo(doc.metadata.labels) && !doc.metadata.labels?.[taskLabel];
}
export function isSweepConfig(doc: ConfigDoc): boolean {
  return isMajordomo(doc.metadata.labels) && doc.metadata.labels?.[taskLabel] === sweepTask;
}
export function isMajordomoCell(doc: Doc): boolean {
  return isMajordomo(doc.metadata.labels) && !doc.metadata.labels?.[taskLabel];
}
export function configScope(doc: ConfigDoc): string[] {
  return [doc.metadata.realm || "default", doc.metadata.space || "default", doc.metadata.stack || "default"];
}

export type Majordomo = {
  configs: Resource<ConfigDoc[]>;
  // Interactive configs, one per harness.
  pairs: ConfigDoc[];
  // health-sweep task configs.
  sweeps: ConfigDoc[];
  // The interactive cell, when one exists.
  cell?: Row;
};

export function useMajordomo(inventory: InventoryResource, revision: number): Majordomo {
  const configs = useResource<ConfigDoc[]>("/api/catalog/configs", revision);
  return useMemo(() => {
    const all = configs.data ?? [];
    const cells = (inventory.data?.rows[3] ?? []).filter(row => isMajordomoCell(row.doc));
    // More than one can only come from the CLI; the panel then follows a running one.
    const cell = cells.find(row => row.doc.status?.state === "Ready") ?? cells[0];
    return { configs, pairs: all.filter(isMajordomoConfig), sweeps: all.filter(isSweepConfig), cell };
  }, [configs, inventory.data]);
}

// Text bound for majordomo's terminal travels as one line: a newline inside a paste could submit it
// in a program that did not ask for bracketed paste.
export function oneLine(text: string): string {
  return text.replace(/[\u0000-\u001f\u007f]+/g, " ").replace(/\s+/g, " ").trim();
}
function statusText(status?: RuntimeStatus): string {
  const parts = [`state ${status?.state || "not reported"}`];
  if (status?.reason) parts.push(`reason: ${status.reason}`);
  if (status?.message) parts.push(`message: ${status.message}`);
  if (status?.outOfSyncError) parts.push(`config comparison failed: ${status.outOfSyncError}`);
  else if (status?.outOfSync) parts.push(`out of sync with its config: ${status.outOfSyncReason || "reason not reported"}`);
  return parts.join("; ");
}
export function cellQuestion(scope: string[], status?: RuntimeStatus): string {
  return oneLine(`About the cell ${scope[3]} in realm ${scope[0]}, space ${scope[1]}, stack ${scope[2]} (${statusText(status)}): what is going on with it, and what should I do?`);
}
export function catalogQuestion(kind: string, scope: string[], state: string): string {
  const [realm, space, stack] = scope.length === 4 ? scope : [scope[0], scope[1], scope[2]];
  const name = scope.at(-1);
  const where = [`realm ${realm}`, space && scope.length > 2 ? `space ${space}` : "", stack && scope.length > 3 ? `stack ${stack}` : ""].filter(Boolean).join(", ");
  return oneLine(`About the ${kind} ${name} in ${where} (${state}): explain what it is for and check whether anything is wrong with it.`);
}

type Ask = (question: string) => void;
const AskContext = createContext<Ask | null>(null);
export const AskProvider = AskContext.Provider;

// Opens the majordomo panel and pastes the question into its terminal without submitting it.
export function AskMajordomo({ question, subject, prominent = false }: { question: string; subject: string; prominent?: boolean }) {
  const ask = useContext(AskContext);
  if (!ask) return null;
  return <button type="button" className={`ask-majordomo${prominent ? " is-prominent" : ""}`} aria-label={`Ask majordomo about ${subject}`} title="Opens the majordomo panel and pastes this question without sending it" onClick={() => ask(question)}>
    <Icon name="majordomo" /><span>Ask majordomo</span>
  </button>;
}

const openKey = "kukeon-majordomo-open";
const widthKey = "kukeon-majordomo-width";
export const dockWidth = { min: 320, max: 960, initial: 480, step: 24 };
function clampWidth(value: number): number {
  return Math.round(Math.min(dockWidth.max, Math.max(dockWidth.min, value)));
}
function stored(key: string): string | null {
  try { return localStorage.getItem(key); } catch { return null; }
}
function save(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* Preferences are optional. */ }
}

// The phone breakpoint shared with style.css's max-width: 760px blocks.
export const mobileMaxWidth = 760;
// The narrowest main column the docked panel may leave: an 80-column cell terminal at 1440 with the
// default sidebar, after the cell screen's and the terminal screen's padding.
export const contentMinWidth = 800;

// window.innerWidth, the width the CSS media queries see, updated at most once a frame.
export function useViewportWidth(): number {
  const [width, setWidth] = useState(() => window.innerWidth);
  useEffect(() => {
    let frame = 0;
    const update = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(() => setWidth(window.innerWidth)); };
    window.addEventListener("resize", update);
    return () => { cancelAnimationFrame(frame); window.removeEventListener("resize", update); };
  }, []);
  return width;
}

// Panel visibility and width, kept per browser. question is the text waiting to be pasted.
// width is the stored preference; effectiveWidth is what the viewport allows next to the sidebar
// (sidebarWidth, the rendered width: 0 when the sidebar is a drawer). When the panel cannot dock
// without squeezing main below contentMinWidth, it overlays the content instead.
export function useMajordomoDock(viewport: number, sidebarWidth: number) {
  const [open, setOpen] = useState(() => stored(openKey) === "true");
  const [width, setWidth] = useState(() => {
    const value = Number(stored(widthKey));
    return Number.isFinite(value) && value > 0 ? clampWidth(value) : dockWidth.initial;
  });
  const [question, setQuestion] = useState("");
  useEffect(() => { save(openKey, String(open)); }, [open]);
  useEffect(() => { save(widthKey, String(width)); }, [width]);
  const available = viewport - sidebarWidth - contentMinWidth;
  const overlay = viewport <= mobileMaxWidth || available < dockWidth.min;
  const maxWidth = Math.max(dockWidth.min, Math.min(dockWidth.max, overlay ? viewport : available));
  const effectiveWidth = Math.min(width, maxWidth);
  return {
    open, setOpen, width, effectiveWidth, maxWidth, overlay, setWidth: (value: number) => setWidth(clampWidth(value)), question,
    ask: (text: string) => { setQuestion(oneLine(text)); setOpen(true); },
    clearQuestion: () => setQuestion(""),
  };
}
export type MajordomoDock = ReturnType<typeof useMajordomoDock>;

function entryState(major: Majordomo): { label: string; state?: string } {
  if (major.cell) return { label: major.cell.doc.status?.state || "State not reported", state: major.cell.doc.status?.state };
  if (major.configs.loading && !major.configs.data) return { label: "Checking…" };
  if (major.configs.error && !major.configs.data) return { label: "Unavailable" };
  return { label: major.pairs.length ? "Not enabled" : "Needs kuke team init" };
}

// Permanent sidebar entry: always present, whatever state majordomo is in.
export function MajordomoSidebarEntry({ major, dock }: { major: Majordomo; dock: MajordomoDock }) {
  const { label, state } = entryState(major);
  return <section className="majordomo-entry" aria-label="Majordomo">
    <button type="button" className="majordomo-entry-button" aria-expanded={dock.open} aria-controls="majordomo-panel" onClick={() => dock.setOpen(true)}>
      <Icon name="majordomo" /><span className="majordomo-entry-name">Majordomo</span>
      {state ? <StateBadge state={state} /> : <span className="badge">{label}</span>}
    </button>
  </section>;
}
export function MajordomoRailButton({ major, dock }: { major: Majordomo; dock: MajordomoDock }) {
  const label = `Majordomo (${entryState(major).label})`;
  return <button type="button" className="icon-button rail-majordomo" aria-label={label} title={label} aria-expanded={dock.open} aria-controls="majordomo-panel" onClick={() => dock.setOpen(true)}><Icon name="majordomo" /></button>;
}
export function MajordomoToolbarButton({ dock }: { dock: MajordomoDock }) {
  const label = dock.open ? "Hide majordomo panel" : "Show majordomo panel";
  return <button type="button" className="icon-button majordomo-toolbar-button" aria-label={label} title={label} aria-pressed={dock.open} aria-controls="majordomo-panel" onClick={() => dock.setOpen(!dock.open)}><Icon name="majordomo" /></button>;
}

async function postJSON(path: string, body?: unknown): Promise<{ ok: boolean; status: number; body: { error?: { code?: string; message?: string }; [key: string]: unknown } }> {
  const response = await fetch(path, {
    method: "POST",
    headers: body === undefined ? { Accept: "application/json" } : { Accept: "application/json", "Content-Type": "application/json" },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  let parsed = {};
  try { parsed = await response.json(); } catch { /* Use the HTTP status. */ }
  return { ok: response.ok, status: response.status, body: parsed };
}

// width is the rendered width and max what the viewport allows; onResize stores the preference, which
// keeps its own 320-960 bounds.
function Resizer({ width, max, onResize }: { width: number; max: number; onResize: (value: number) => void }) {
  const origin = useRef<{ x: number; width: number } | null>(null);
  function down(event: PointerEvent<HTMLDivElement>) {
    if (event.button !== 0) return;
    event.preventDefault();
    origin.current = { x: event.clientX, width };
    event.currentTarget.setPointerCapture(event.pointerId);
  }
  // The panel is on the right, so dragging left widens it.
  function move(event: PointerEvent<HTMLDivElement>) {
    if (origin.current) onResize(origin.current.width - (event.clientX - origin.current.x));
  }
  function up(event: PointerEvent<HTMLDivElement>) {
    origin.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  }
  function key(event: KeyboardEvent<HTMLDivElement>) {
    const next = event.key === "ArrowLeft" ? width + dockWidth.step
      : event.key === "ArrowRight" ? width - dockWidth.step
        : event.key === "Home" ? dockWidth.max
          : event.key === "End" ? dockWidth.min : null;
    if (next === null) return;
    event.preventDefault();
    onResize(next);
  }
  return <div className="majordomo-resizer" role="separator" aria-orientation="vertical" aria-label="Resize majordomo panel" aria-controls="majordomo-panel"
    aria-valuenow={width} aria-valuemin={dockWidth.min} aria-valuemax={max} tabIndex={0}
    onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerCancel={up} onKeyDown={key} />;
}

type Container = { id: string; attachable?: boolean; root?: boolean };

// The docked panel. It stays mounted while collapsed, so collapsing never detaches the session.
export function MajordomoPanel({ major, dock, inventory, manager, theme, revision, onChanged }: {
  major: Majordomo;
  dock: MajordomoDock;
  inventory: InventoryResource;
  manager: TerminalManager;
  theme: Theme;
  revision: number;
  onChanged: () => void;
}) {
  const scope = major.cell?.scope ?? null;
  const key = scope ? cellId(scope) : "";
  const cellState = major.cell?.doc.status?.state;
  const running = !!scope && !!cellState && !unavailableStates.has(cellState);
  const containers = useResource<Container[]>(scope ? `${apiPath(scope)}/containers` : null, revision);
  const eligible = containers.data?.filter(container => container.attachable && !container.root) ?? [];
  const selected = eligible.some(container => container.id === manager.selection[key]) ? manager.selection[key] : eligible[0]?.id ?? "";
  const currentId = scope && selected ? sessionId(scope, selected) : "";
  const sessions = manager.sessions.filter(item => cellId(item.scope) === key);
  const current = sessions.find(item => item.id === currentId);
  const [detached, setDetached] = useState(false);
  const [chosen, setChosen] = useState("");
  const [pending, setPending] = useState<"" | "enable" | "start">("");
  const [failure, setFailure] = useState("");
  const config = major.pairs.find(doc => JSON.stringify([...configScope(doc), doc.metadata.name]) === chosen) ?? major.pairs[0];

  useEffect(() => { setDetached(false); }, [key]);
  // Attach when the panel is opened on a running cell; a deliberate Detach holds until Reconnect.
  useEffect(() => {
    if (!dock.open || !scope || !running || !selected || current || detached || containers.loading) return;
    manager.attach(scope, selected);
  }, [dock.open, key, running, selected, current, detached, containers.loading, manager.attach]);
  // A waiting question goes to the terminal as soon as there is a session to take it.
  useEffect(() => {
    if (!dock.question || !current || current.state !== "attached") return;
    manager.paste(current.id, dock.question);
    dock.clearQuestion();
  }, [dock.question, current?.id, current?.state]);

  async function start(target: string[]) {
    const started = await postJSON(`${apiPath(target)}/start`);
    if (!started.ok) throw new Error(started.body.error?.message ?? `HTTP ${started.status}`);
  }
  async function enable() {
    if (!config || pending || major.cell) return;
    setPending("enable"); setFailure("");
    try {
      const request = { config: { name: config.metadata.name, realm: config.metadata.realm, space: config.metadata.space, stack: config.metadata.stack }, name: cellName, env: [], ignoreDiskPressure: false };
      const preview = await postJSON("/api/cells/from-config/preview", request);
      if (!preview.ok) throw new Error(preview.body.error?.message ?? `HTTP ${preview.status}`);
      const document = preview.body.document as Doc | undefined;
      const created = await postJSON("/api/cells/from-config/create", { ...request, resolvedName: document?.metadata.name, preview: preview.body.preview });
      if (!created.ok) throw new Error(created.body.error?.message ?? `HTTP ${created.status}`);
      const cell = created.body as unknown as Doc;
      inventory.upsertCell(cell);
      await start([...configScope(config), cell.metadata.name]);
    } catch (error) {
      setFailure(error instanceof Error ? error.message : "The request could not reach the web server.");
    } finally { setPending(""); onChanged(); }
  }
  async function startCell() {
    if (!scope || pending) return;
    setPending("start"); setFailure("");
    try { await start(scope); }
    catch (error) { setFailure(error instanceof Error ? error.message : "The request could not reach the web server."); }
    finally { setPending(""); onChanged(); }
  }

  const live = current?.state === "attached" || current?.state === "connecting";
  const connection = !scope ? { tone: "disconnected", label: "No majordomo cell" }
    : !running ? { tone: "disconnected", label: `Cell is ${cellState || "in an unknown state"}` }
      : current ? { tone: current.state, label: current.state === "attached" ? "Attached" : current.state === "connecting" ? "Connecting…" : current.message || "Disconnected" }
        : detached ? { tone: "disconnected", label: "Detached" }
          : !containers.loading && !eligible.length ? { tone: "disconnected", label: "No attachable container" }
            : { tone: "connecting", label: "Connecting…" };
  const canReconnect = !!scope && running && !!selected && !live;
  const reconnect = () => { if (scope && selected) { setDetached(false); manager.attach(scope, selected); } };
  const setup = !scope ? <div className="majordomo-setup">
    {major.configs.loading && !major.configs.data ? <p role="status">Looking for majordomo configs…</p>
      : major.configs.error && !major.configs.data ? <ErrorNotice error={major.configs.error} kind="Configs" />
        : !major.pairs.length ? <div className="majordomo-missing" role="status"><strong>Majordomo is not installed on this host.</strong><p>Run <code>kuke team init</code> on the host to apply its configs, then refresh.</p></div>
          : <>
            <p>Majordomo is kukeon's own assistant: ask it for help, and it changes things only when you ask.</p>
            <label>Harness<select value={config ? JSON.stringify([...configScope(config), config.metadata.name]) : ""} disabled={!!pending} onChange={event => setChosen(event.target.value)}>{major.pairs.map(doc => { const id = JSON.stringify([...configScope(doc), doc.metadata.name]); return <option key={id} value={id}>{doc.metadata.name}</option>; })}</select></label>
            <button type="button" className="majordomo-primary" disabled={!!pending} aria-busy={pending === "enable" || undefined} onClick={enable}>{pending === "enable" ? "Enabling majordomo…" : "Enable majordomo"}</button>
            <small>Creates and starts one cell named <code>{cellName}</code> from the selected config.</small>
          </>}
  </div> : !running ? <div className="majordomo-setup">
    <p role="status">The majordomo cell <code>{scope.join(" / ")}</code> is <StateBadge state={cellState} />.</p>
    <button type="button" className="majordomo-primary" disabled={!!pending} aria-busy={pending === "start" || undefined} onClick={startCell}>{pending === "start" ? "Starting…" : "Start"}</button>
  </div> : null;

  return <aside className="majordomo-panel" id="majordomo-panel" aria-label="Majordomo panel" hidden={!dock.open} style={{ width: dock.effectiveWidth }}>
    <Resizer width={dock.effectiveWidth} max={dock.maxWidth} onResize={dock.setWidth} />
    <header className="majordomo-header">
      <h2><Icon name="majordomo" />Majordomo</h2>
      <span className={`terminal-connection-state ${connection.tone}`} role="status" title={connection.label}><span className="state-dot" aria-hidden="true" /><span className="majordomo-connection-label">{connection.label}</span></span>
      <div className="majordomo-header-actions">
        {canReconnect && <button type="button" className="icon-button" aria-label="Reconnect majordomo" title="Reconnect" onClick={reconnect}><Icon name="reconnect" /></button>}
        {live && current && <button type="button" className="icon-button" aria-label="Detach majordomo" title="Detach" onClick={() => { setDetached(true); manager.detach(current.id); }}><Icon name="detach" /></button>}
        <button type="button" className="icon-button" aria-label="Collapse majordomo panel" title="Collapse (the session stays attached)" onClick={() => dock.setOpen(false)}><Icon name="sidebarExpand" /></button>
      </div>
    </header>
    {dock.question && <p className="majordomo-question" role="status"><strong>Waiting to paste:</strong> {dock.question} <button type="button" className="text-button" onClick={dock.clearQuestion}>Discard</button></p>}
    {failure && <div className="error" role="alert"><strong>{scope ? "Majordomo did not start" : "Majordomo could not be enabled"}</strong><p>{failure}</p></div>}
    {setup}
    {scope && running && containers.error && !live && <ErrorNotice error={containers.error} kind="Containers" />}
    <div className="majordomo-screens" hidden={!scope || !running}>
      {sessions.map(session => <TerminalSession key={session.id} session={session} active={dock.open && session.id === currentId} expanded={false} theme={theme} update={manager.update} pending={manager.pastes[session.id]} onPasted={manager.pasteDelivered} />)}
    </div>
  </aside>;
}
