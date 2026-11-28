import { createContext, useContext, useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { useResource, ErrorNotice, type Resource } from "./request";
import type { InventoryResource, Row, Doc, RuntimeStatus } from "./inventory";
import { apiPath } from "./routes";
import { StateBadge } from "@kukeon/ui";
import { Icon } from "@kukeon/ui";
import type { Schedule, ScheduleStatus } from "./schedule-format.mjs";
import { TerminalSession, cellId, sessionId, unavailableStates, useReattach, type TerminalManager, type Theme } from "./terminal";
import { useSettings } from "./settings";
import { configForCell, sameScopedReference } from "./relationships.mjs";
import { configLabel } from "./cells.mjs";
import { InitializePrompt } from "./initialize";
import { startLogin } from "./connector-ui";
import { connectorStatuses, connectorTitle, keepsLogin, loginEmpty, loginIdentitiesFor, loginLoggedIn, sameRef, type ConnectorStatus } from "./connectors.mjs";
import { apiFetch } from "@kukeon/api-client";

// Majordomo is found by label, never by name: its configs and cells carry the kuke team and the
// majordomo role, and a task config additionally names its task.
const teamLabel = "kukeon.io/team";
const roleLabel = "kukeon.io/role";
const taskLabel = "kukeon.io/task";
// The harness a team Config runs on (#1668); a Config rendered before the label existed has none.
export const harnessLabel = "kukeon.io/harness";
const team = "kuke";
export const majordomoRole = "majordomo";
const role = majordomoRole;
export const sweepTask = "health-sweep";
// The first interactive cell the web UI creates; a further one is named after its Config (#1630).
const cellName = "majordomo";
// The active docked cell, by cellId, kept per browser.
const activeKey = "kukeon-majordomo-active";

export type { ScheduleStatus } from "./schedule-format.mjs";
export type ConfigDoc = {
  metadata: { name: string; realm?: string; space?: string; stack?: string; labels?: Record<string, string> };
  spec?: { blueprint?: { name?: string }; schedule?: Schedule; [key: string]: unknown };
  status?: { schedule?: ScheduleStatus; connectors?: ConnectorStatus[] };
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
  // Every interactive majordomo cell, oldest first: each is docked in the panel, one tab each.
  cells: Row[];
  // The active one, when any exists.
  cell?: Row;
  // Interactive configs no majordomo cell was created from: what Add majordomo offers.
  unused: ConfigDoc[];
  // select makes a docked cell, by cellId, the active one.
  select: (key: string) => void;
};

function configRef(doc: ConfigDoc) {
  return { name: doc.metadata.name, realm: doc.metadata.realm, space: doc.metadata.space, stack: doc.metadata.stack };
}
// The lineage label kukeond stamps on a cell created from a Config.
// fromConfig reports whether a cell was created from a Config: by its spec.provenance, else by its lineage
// label within the Config's Realm.
function fromConfig(row: Row, doc: ConfigDoc): boolean {
  const ref = configForCell(row.doc);
  if (ref) return sameScopedReference(configRef(doc), ref);
  return row.doc.metadata.labels?.[configLabel] === doc.metadata.name && row.scope[0] === configScope(doc)[0];
}
// cellConfig is the Config a majordomo cell was created from.
function cellConfig(row: Row, configs: ConfigDoc[]): ConfigDoc | undefined {
  return configs.find(doc => fromConfig(row, doc));
}
function createdAt(row: Row): number {
  const value = Date.parse(row.doc.status?.createdAt ?? "");
  return Number.isFinite(value) && !(row.doc.status?.createdAt ?? "").startsWith("0001-") ? value : Number.MAX_SAFE_INTEGER;
}

export function useMajordomo(inventory: InventoryResource, revision: number): Majordomo {
  const configs = useResource<ConfigDoc[]>("/catalog/configs", revision);
  // The choice survives inventory refreshes: only the user, or the loss of the chosen cell, changes it.
  const [chosen, setChosen] = useState(() => stored(activeKey) ?? "");
  const select = useMemo(() => (key: string) => { setChosen(key); save(activeKey, key); }, []);
  const result = useMemo(() => {
    const all = configs.data ?? [];
    // Ordered by creation, then scope: a cell that appears or sorts first by name never moves the others.
    const cells = (inventory.data?.rows[3] ?? []).filter(row => isMajordomoCell(row.doc))
      .sort((a, b) => createdAt(a) - createdAt(b) || a.scope.join("/").localeCompare(b.scope.join("/")));
    const cell = cells.find(row => cellId(row.scope) === chosen) ?? cells.find(row => row.doc.status?.state === "Ready") ?? cells[0];
    const pairs = all.filter(isMajordomoConfig);
    const unused = pairs.filter(doc => !cells.some(row => fromConfig(row, doc)));
    return { configs, pairs, sweeps: all.filter(isSweepConfig), cells, cell, unused, select };
  }, [configs, inventory.data, chosen, select]);
  // Pin the fallback once a complete inventory shows the chosen cell is gone (or none was chosen), so a
  // cell that starts or appears later does not take the panel.
  const fallback = result.cell ? cellId(result.cell.scope) : "";
  const complete = !!inventory.data && !inventory.data.failures.length;
  useEffect(() => { if (complete && fallback && fallback !== chosen && !result.cells.some(row => cellId(row.scope) === chosen)) select(fallback); }, [complete, fallback, chosen, result.cells, select]);
  return result;
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
  if (major.cell) return { label: `${major.cell.doc.status?.state || "State not reported"}${major.cells.length > 1 ? `, ${major.cells.length} cells` : ""}`, state: major.cell.doc.status?.state };
  if (major.configs.loading && !major.configs.data) return { label: "Checking…" };
  if (major.configs.error && !major.configs.data) return { label: "Unavailable" };
  return { label: major.pairs.length ? "Not enabled" : "Not initialized" };
}
// needsInitialize reports a host whose Config catalog has no majordomo Config: `kuke team init` has not run.
export function needsInitialize(major: Majordomo): boolean {
  return !major.cell && !!major.configs.data && !major.pairs.length;
}

// Permanent sidebar entry: always present, whatever state majordomo is in.
export function MajordomoSidebarEntry({ major, dock, onChanged }: { major: Majordomo; dock: MajordomoDock; onChanged: () => void }) {
  const { label, state } = entryState(major);
  return <section className="majordomo-entry" aria-label="Majordomo">
    <button type="button" className="majordomo-entry-button" aria-expanded={dock.open} aria-controls="majordomo-panel" onClick={() => dock.setOpen(true)}>
      <Icon name="majordomo" /><span className="majordomo-entry-name">Majordomo</span>
      {state ? <StateBadge state={state} /> : <span className="badge">{label}</span>}
      {major.cells.length > 1 && <span className="badge majordomo-count" aria-label={`${major.cells.length} majordomo cells`} title={`${major.cells.length} majordomo cells`}>{major.cells.length}</span>}
    </button>
    {needsInitialize(major) && <InitializePrompt compact onChanged={onChanged} subject="majordomo and the agent templates" />}
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
  const response = await apiFetch(path, {
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

// harnessLogin is the connector status of the login a majordomo Config's harness keeps in its state volume
// (#1685), or undefined for a Config on a token method or one that reports none.
function harnessLogin(doc: ConfigDoc | undefined): ConnectorStatus | undefined {
  return connectorStatuses(doc).find(status => keepsLogin(status) && !!status.login);
}

// MajordomoLogin is the panel's status line for the harness login (#1694): "logged in" or "not logged in",
// and Log in while it is empty. Log in starts the identity cell of the majordomo state volume and opens its
// terminal; while that session runs the login identities are polled, and the Configs are re-read when it
// ends so the line follows the login. asking is the prompt Enable majordomo shows first while the login is
// empty: the same line and Log in, with Continue anyway.
function MajordomoLogin({ status, asking = false, busy = false, onContinue, onChanged }: { status: ConnectorStatus; asking?: boolean; busy?: boolean; onContinue?: () => void; onChanged: () => void }) {
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState("");
  const [watching, setWatching] = useState(false);
  const [tick, setTick] = useState(0);
  const logins = useResource<unknown[]>(watching ? "/connectors/logins" : null, tick);
  const seen = useRef(false);
  const misses = useRef(0);
  useEffect(() => {
    if (!watching) return undefined;
    const timer = window.setInterval(() => setTick(value => value + 1), 3000);
    return () => window.clearInterval(timer);
  }, [watching]);
  useEffect(() => {
    if (!watching || logins.loading || !logins.data) return;
    const running = loginIdentitiesFor(logins.data, status.connector).some(item => sameRef(item.volume, status.volumeRef) && !!item.session);
    if (running) { seen.current = true; misses.current = 0; return; }
    misses.current += 1;
    // The session ended, or never showed up in three reads: re-read the Configs either way.
    if (seen.current || misses.current >= 3) { setWatching(false); onChanged(); }
  }, [watching, logins.data, logins.loading]);
  const title = connectorTitle(status);
  const state = status.login?.state;
  async function logIn() {
    if (pending || !status.volumeRef) return;
    setPending(true); setFailure("");
    try {
      await startLogin(status.connector, status.volumeRef, status.method);
      seen.current = false; misses.current = 0;
      setWatching(true);
    } catch (error) { setFailure((error as { message?: string } | undefined)?.message ?? "The login could not be started."); }
    finally { setPending(false); }
  }
  if (state !== loginLoggedIn && state !== loginEmpty) return null;
  const prompt = asking && state === loginEmpty;
  return <div className={`majordomo-login${prompt ? " is-asking" : ""}`} role={prompt ? "group" : undefined} aria-label={prompt ? "Log in first" : undefined}>
    <p role="status" aria-label={`${title} login`}><span className={`state-dot tone-${state === loginLoggedIn ? "ok" : "stopped"}`} aria-hidden="true" />{title}: {state === loginLoggedIn ? "logged in" : "not logged in"}</p>
    {prompt && <p className="majordomo-login-ask">Majordomo starts without a login, and {title} then asks for one in its terminal. Log in first, or continue.</p>}
    {state === loginEmpty && status.volumeRef && <button type="button" className="text-button" disabled={pending || busy} aria-label={`Log in to ${title} for majordomo`} onClick={() => void logIn()}>{pending ? "Starting…" : watching ? "Open login session" : "Log in"}</button>}
    {prompt && <button type="button" className="text-button" disabled={busy} onClick={onContinue}>Continue anyway</button>}
    {failure && <p className="error" role="alert">{failure}</p>}
  </div>;
}

// MajordomoTabs switches the active docked cell (#1630): one tab per cell, labelled with the Config it came
// from, with its state. Switching only changes which session is shown; none is detached.
function MajordomoTabs({ major }: { major: Majordomo }) {
  const list = useRef<HTMLDivElement>(null);
  const all = major.configs.data ?? [];
  const activeId = major.cell ? cellId(major.cell.scope) : "";
  function key(event: KeyboardEvent<HTMLDivElement>) {
    const index = major.cells.findIndex(row => cellId(row.scope) === activeId);
    const next = event.key === "ArrowRight" ? index + 1 : event.key === "ArrowLeft" ? index - 1 : event.key === "Home" ? 0 : event.key === "End" ? major.cells.length - 1 : null;
    if (next === null) return;
    event.preventDefault();
    const row = major.cells[(next + major.cells.length) % major.cells.length];
    major.select(cellId(row.scope));
    requestAnimationFrame(() => list.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.focus());
  }
  const configName = (row: Row) => cellConfig(row, all)?.metadata.name ?? configForCell(row.doc)?.name ?? row.doc.metadata.labels?.[configLabel] ?? row.doc.metadata.name;
  const names = major.cells.map(configName);
  return <div className="majordomo-tabs" role="tablist" aria-label="Majordomo cells" ref={list} onKeyDown={key}>
    {major.cells.map((row, index) => {
      const id = cellId(row.scope);
      const selected = id === activeId;
      // Labelled by Config; two cells of one Config are told apart by cell name.
      const label = names.indexOf(names[index]) === names.lastIndexOf(names[index]) ? names[index] : row.doc.metadata.name;
      const state = row.doc.status?.state;
      const tone = state === "Ready" ? "ok" : state && unavailableStates.has(state) ? "stopped" : "pending";
      return <button key={id} type="button" role="tab" className="majordomo-tab" aria-selected={selected} aria-controls="majordomo-screens" tabIndex={selected ? 0 : -1} title={`${row.scope.join(" / ")} (${state || "state not reported"})`} onClick={() => major.select(id)}>
        <span className={`state-dot tone-${tone}`} aria-hidden="true" /><span className="majordomo-tab-label">{label}</span><span className="sr-only">, {state || "state not reported"}</span>
      </button>;
    })}
  </div>;
}

// The docked panel. It stays mounted while collapsed, so collapsing never detaches a session. Every
// majordomo cell is docked here; the attach, reconnect and paste logic follows the active one, and the
// sessions of the others stay mounted behind it.
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
  const docked = major.cells.map(row => cellId(row.scope));
  const sessions = manager.sessions.filter(item => docked.includes(cellId(item.scope)));
  const current = sessions.find(item => item.id === currentId);
  // A deliberate Detach holds per cell until Reconnect, across tab switches.
  const [detached, setDetached] = useState<Record<string, boolean>>({});
  const [chosen, setChosen] = useState("");
  const [adding, setAdding] = useState(false);
  const [pending, setPending] = useState<"" | "enable" | "start">("");
  const [failure, setFailure] = useState("");
  // Enabling a majordomo whose harness is not logged in asks first (#1694); Continue anyway goes on.
  const [askLogin, setAskLogin] = useState(false);
  // The setup form offers every interactive Config for the first cell, and only the unused ones after.
  const offered = major.cell ? major.unused : major.pairs;
  const config = offered.find(doc => JSON.stringify([...configScope(doc), doc.metadata.name]) === chosen) ?? offered[0];
  // The login line follows the Config on screen: the setup form's choice, else the active cell's.
  const formShown = !scope || adding;
  const login = harnessLogin(formShown ? config : major.cell ? cellConfig(major.cell, major.configs.data ?? []) : undefined);
  const notLoggedIn = formShown && login?.login?.state === loginEmpty;
  useEffect(() => { if (!notLoggedIn) setAskLogin(false); }, [notLoggedIn]);
  const containersReady = !containers.loading && !!containers.data;

  // With Auto-attach terminals on (#1745), attach when the panel shows a running cell — on the first
  // activation of its tab — unless detached, and attach again when its connection drops, like the cell
  // terminal does.
  const [{ autoAttach }] = useSettings();
  const canAutoAttach = autoAttach && dock.open && !!scope && running && !!selected && !detached[key] && containersReady;
  useEffect(() => {
    if (!canAutoAttach || current) return;
    manager.attach(scope!, selected);
  }, [canAutoAttach, key, selected, current, manager.attach]);
  const forgetReattach = useReattach(current, canAutoAttach, session => manager.attach(session.scope, session.container));
  // A waiting question goes to the active tab's terminal as soon as it has a session to take it.
  useEffect(() => {
    if (!dock.question || !current || current.state !== "attached") return;
    manager.paste(current.id, dock.question);
    dock.clearQuestion();
  }, [dock.question, current?.id, current?.state]);
  useEffect(() => { if (!offered.length) setAdding(false); }, [offered.length]);

  async function start(target: string[]) {
    const started = await postJSON(`${apiPath(target)}/start`);
    if (!started.ok) throw new Error(started.body.error?.message ?? `HTTP ${started.status}`);
  }
  async function enable(anyway = false) {
    if (!config || pending || (major.cell && !adding)) return;
    if (notLoggedIn && !anyway) { setAskLogin(true); return; }
    setAskLogin(false);
    setPending("enable"); setFailure("");
    // The first cell is `majordomo`; a further one is named after its Config.
    const name = major.cells.length ? config.metadata.name : cellName;
    try {
      const request = { config: { name: config.metadata.name, realm: config.metadata.realm, space: config.metadata.space, stack: config.metadata.stack }, name, env: [], ignoreDiskPressure: false };
      const preview = await postJSON("/cells/from-config/preview", request);
      if (!preview.ok) throw new Error(preview.body.error?.message ?? `HTTP ${preview.status}`);
      const document = preview.body.document as Doc | undefined;
      const created = await postJSON("/cells/from-config/create", { ...request, resolvedName: document?.metadata.name, preview: preview.body.preview });
      if (!created.ok) throw new Error(created.body.error?.message ?? `HTTP ${created.status}`);
      const cell = created.body as unknown as Doc;
      inventory.upsertCell(cell);
      const target = [...configScope(config), cell.metadata.name];
      major.select(cellId(target));
      setAdding(false);
      await start(target);
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
        : detached[key] ? { tone: "disconnected", label: "Detached" }
          : !containers.loading && !eligible.length ? { tone: "disconnected", label: "No attachable container" }
            : !autoAttach ? { tone: "disconnected", label: "Detached" }
              : { tone: "connecting", label: "Connecting…" };
  const canReconnect = !!scope && running && !!selected && !live;
  const reconnect = () => { if (scope && selected) { setDetached(old => ({ ...old, [key]: false })); forgetReattach(currentId); manager.attach(scope, selected); } };
  const form = (title: string, note: string) => <>
    <p>{title}</p>
    <label>Harness<select value={config ? JSON.stringify([...configScope(config), config.metadata.name]) : ""} disabled={!!pending} onChange={event => setChosen(event.target.value)}>{offered.map(doc => { const id = JSON.stringify([...configScope(doc), doc.metadata.name]); return <option key={id} value={id}>{doc.metadata.name}</option>; })}</select></label>
    <button type="button" className="majordomo-primary" disabled={!!pending || !config} aria-busy={pending === "enable" || undefined} onClick={() => void enable()}>{pending === "enable" ? (major.cell ? "Adding majordomo…" : "Enabling majordomo…") : major.cell ? "Add majordomo" : "Enable majordomo"}</button>
    <small>{note}</small>
  </>;
  const setup = !scope ? <div className="majordomo-setup">
    {major.configs.loading && !major.configs.data ? <p role="status">Looking for majordomo configs…</p>
      : major.configs.error && !major.configs.data ? <ErrorNotice error={major.configs.error} kind="Configs" />
        : !major.pairs.length ? <div className="majordomo-missing"><p role="status"><strong>Majordomo is not installed on this host.</strong> Initialize applies its configs.</p><InitializePrompt onChanged={onChanged} subject="majordomo and the agent templates" /></div>
          : form("Majordomo is kukeon's own assistant: ask it for help, and it changes things only when you ask.", `Creates and starts one cell named ${cellName} from the selected config.`)}
  </div> : adding ? <div className="majordomo-setup majordomo-add" role="group" aria-label="Add majordomo">
    {form("Add another majordomo with a different harness. Each runs in its own cell, in its own tab.", `Creates and starts a cell named after the selected config.`)}
    <button type="button" className="text-button" disabled={!!pending} onClick={() => setAdding(false)}>Cancel</button>
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
        {!!major.cell && major.unused.length > 0 && <button type="button" className="icon-button" aria-label="Add majordomo" title="Add majordomo with another harness" aria-expanded={adding} onClick={() => setAdding(value => !value)}><Icon name="create" /></button>}
        {canReconnect && <button type="button" className="icon-button" aria-label="Reconnect majordomo" title="Reconnect" onClick={reconnect}><Icon name="reconnect" /></button>}
        {live && current && <button type="button" className="icon-button" aria-label="Detach majordomo" title="Detach" onClick={() => { setDetached(old => ({ ...old, [key]: true })); manager.detach(current.id); }}><Icon name="detach" /></button>}
        <button type="button" className="icon-button" aria-label="Collapse majordomo panel" title="Collapse (the session stays attached)" onClick={() => dock.setOpen(false)}><Icon name="sidebarExpand" /></button>
      </div>
    </header>
    {major.cells.length > 1 && <MajordomoTabs major={major} />}
    {dock.question && <p className="majordomo-question" role="status"><strong>Waiting to paste:</strong> {dock.question} <button type="button" className="text-button" onClick={dock.clearQuestion}>Discard</button></p>}
    {login && <MajordomoLogin key={`${login.connector}/${JSON.stringify(login.volumeRef ?? null)}`} status={login} asking={askLogin} busy={!!pending} onContinue={() => void enable(true)} onChanged={onChanged} />}
    {failure && <div className="error" role="alert"><strong>{scope && !adding ? "Majordomo did not start" : major.cell ? "Majordomo could not be added" : "Majordomo could not be enabled"}</strong><p>{failure}</p></div>}
    {setup}
    {scope && running && !adding && containers.error && !live && <ErrorNotice error={containers.error} kind="Containers" />}
    <div className="majordomo-screens" id="majordomo-screens" role={major.cells.length > 1 ? "tabpanel" : undefined} aria-label={major.cell ? `Terminal of ${major.cell.doc.metadata.name}` : undefined} hidden={!scope || !running || adding}>
      {sessions.map(session => <TerminalSession key={session.id} session={session} active={dock.open && !adding && session.id === currentId} expanded={false} theme={theme} update={manager.update} pending={manager.pastes[session.id]} onPasted={manager.pasteDelivered} />)}
    </div>
  </aside>;
}
