import { StrictMode, Suspense, lazy, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";
import { useResource, ErrorNotice, failureTitle, type Resource } from "./request";
import { CatalogPage, RelatedResources, catalogLink } from "./catalog";
import { configForCell, scheduledRun } from "./relationships.mjs";
import { catalogAPI, referenceScope } from "./catalog-url.mjs";
import { scheduleText, type Schedule, type ScheduleStatus } from "./schedule-format.mjs";
import { when } from "./schedule";
import { TerminalWorkspace, cellId, useTerminalSessions, type TerminalManager } from "./terminal";
import { CellSidebar } from "./cell-sidebar";
import { Login, useSession } from "./auth";
import { useSettings, type Theme } from "./settings";
import { UserMenu } from "./user-menu";
import { gridGroupingKey, sidebarGroupingKey, useCellFilter } from "./cell-filter";
import { SettingsNav } from "./settings-nav";
import { SettingsPage } from "./settings-page";
import { activeCells, configDisplayName, instanceLabels, matchesState, ownDisplayName } from "./cells.mjs";
import { useTabVisible, useUnread } from "./use-unread";
import { RenameControl, renameCell, renameConfig } from "./display-name";
import { useInventory, type Inventory, type InventoryResource, type Doc, type RuntimeStatus } from "./inventory";
import { apiPath, catalogTitles, daemonLink, healthLink, homeLink, imagesLink, kinds, overviewLink, titles, readRoute, resourceLink, listLink, newAgentLink, newCellLink, settingsLink, workspaceLink, type Route } from "./routes";
import type { CreatedAgent } from "./new-agent";
import { manageAttention, viewOf } from "./views.mjs";
import { ManageNav } from "./manage-nav";
import { WorkbenchGrid } from "./workbench";
import { GetStarted } from "./get-started";
import { BrandMark, SidebarRail, SidebarResizer, SidebarToggle, VersionRows, railWidth, useSidebarLayout, versionInfo } from "./sidebar-shell";
import { DriftBadge, ScopePath, SocketPath, StateBadge, driftReason, hasDrift } from "./status";
import { DocumentRowLink, Icon } from "./icons";
import { CgroupPanel } from "./cgroup";
import { RealmImagesPanel } from "./images";
import { registryEntries } from "./connectors.mjs";
import { ImagesPage } from "./image-inventory";
import { NetworkPanel, networkKinds, type NetworkDetail } from "./network";
import { formatIPv4, formatSubnet, type SpaceNetwork } from "./network-format.js";
import { useSpaceSubnets } from "./space-subnets";
import { DaemonHealthPage, DaemonLogsPage } from "./daemon";
import { CellLifecycleControls, CellRowActions, type LifecycleAction, type LifecycleFeedback, type PurgeOptions } from "./lifecycle";
import { InventoryIndicator, NotificationProvider, useNotify } from "./notifications";
import { cellRemovalConfirmed, cellsWithin, deleteOperationComplete, descendantCounts, parentActionsAllowed, purgeOperationComplete, requestedPurgeOptions } from "./lifecycle-policy.js";
import { ParentRowActions, type ParentAction } from "./scope-actions";
import { AskMajordomo, AskProvider, MajordomoPanel, MajordomoRailButton, MajordomoSidebarEntry, MajordomoToolbarButton, cellQuestion, mobileMaxWidth, useMajordomo, useMajordomoDock, useViewportWidth } from "./majordomo";
import { HealthPage, storedHealthSeen, type Summary } from "./health";
import { connectorsLink } from "./connector-ui";
import { newTeamLink, sourcesLink, teamsLink } from "./teams.mjs";
import { NewSecretPage } from "./secret-form";

const failureStates = ["Failed", "Error", "Degraded"];

const NewCellPage = lazy(async () => ({ default: (await import("./new-cell")).NewCellPage }));
const NewAgentPage = lazy(async () => ({ default: (await import("./new-agent")).NewAgentPage }));
const NewConfigPage = lazy(async () => ({ default: (await import("./new-config")).NewConfigPage }));
const ConnectorsPage = lazy(async () => ({ default: (await import("./connectors-page")).ConnectorsPage }));
const TeamsPage = lazy(async () => ({ default: (await import("./teams-page")).TeamsPage }));
const NewTeamPage = lazy(async () => ({ default: (await import("./teams-page")).NewTeamPage }));
const SourcesPage = lazy(async () => ({ default: (await import("./sources-page")).SourcesPage }));

function validTime(value?: string): value is string {
  return !!value && !value.startsWith("0001-") && Number.isFinite(Date.parse(value));
}
function StatusSummary({ status, kind, scope }: { status?: RuntimeStatus; kind: string; scope?: string[] }) {
  const container = kind === "container";
  const observations = status?.containers ?? [];
  const finished = validTime(status?.finishTime) && ["Stopped", "Failed", "Exited", "Error"].includes(status?.state ?? "");
  const times = container ? [["Started", status?.startTime], ["Finished", status?.finishTime], ["Last restart", status?.restartTime]] : [["Created", status?.createdAt], ["Updated", status?.updatedAt]];
  return <section className="panel runtime-status" aria-label={`${container ? "Container" : "Cell"} status`}>
    <div className="panel-heading"><h2>Runtime status</h2><span className="badge">Daemon observation</span></div>
    <div className="panel-body">
      <div className="status-grid">
        <div className="status-card"><span>State</span><StateBadge state={status?.state} /></div>
        {container ? <div className="status-card"><span>Restarts</span><strong>{status?.restartCount ?? "Not reported"}</strong></div> : <>
          <div className="status-card"><span>Cgroup readiness</span><StateBadge state={status?.cgroupReady === undefined ? undefined : status.cgroupReady ? "Ready" : "Not ready"} /></div>
          <div className="status-card"><span>Container observations</span><strong>{status?.containers ? observations.length : "Not reported"}</strong>{status?.containers && <small>{observations.filter(item => ["Ready", "Running"].includes(item.state ?? "")).length} ready · {observations.filter(item => ["Failed", "Error"].includes(item.state ?? "")).length} failed</small>}</div>
        </>}
        {finished && status?.exitCode !== undefined && <div className="status-card"><span>Exit code</span><strong>{status.exitCode}</strong>{status.exitSignal && <small>Signal: {status.exitSignal}</small>}</div>}
      </div>
      {(status?.reason || status?.message) && <div className="status-message"><strong>{status.reason || "Status message"}</strong>{status.message && <p>{status.message}</p>}</div>}
      {scope?.length === 4 && failureStates.includes(status?.state ?? "") && <div className="status-ask"><span>This cell is {status?.state}.</span><AskMajordomo prominent question={cellQuestion(scope, status)} subject={`cell ${scope.join(" / ")}`} /></div>}
      {(status?.outOfSync || status?.outOfSyncError) && <div className="error"><strong>{status.outOfSyncError ? "Configuration comparison failed" : "Configuration out of sync"}</strong><p>{status.outOfSyncError || status.outOfSyncReason || "The daemon reports a configuration mismatch."}</p></div>}
      {times.some(([, value]) => validTime(value)) && <dl className="status-times">{times.filter(([, value]) => validTime(value)).map(([label, value]) => <div key={label}><dt>{label}</dt><dd><time dateTime={value}>{new Date(value!).toLocaleString()}</time></dd></div>)}</dl>}
      {container && ([ ["Repository setup", status?.repos], ["Create stages", status?.stages] ] as const).map(([label, items]) => !!items?.length && <div className="setup-status" key={label}><h3>{label}</h3><ul className="state-list">{items.map((item, i) => <li key={i}><div><strong>{item.name ?? `Stage ${item.index ?? i}`}</strong>{item.error && <p className="setup-error">{item.error}</p>}</div><StateBadge state={item.state} /></li>)}</ul></div>)}
      {!status && <p className="description">The daemon has not reported status for this resource.</p>}
    </div>
  </section>;
}
// The Overview leads with what an operator acts on (#1568): the cells needing attention and the cell
// summary come before the realm, space and stack counts.
// The Health card (#1635): the newest stored sweep report and its findings, on the Daemon page's Health tab.
function HealthCard({ reports }: { reports: Resource<Summary[]> }) {
  const latest = reports.data?.[0];
  return <a className="summary-card panel summary-card-health" href={healthLink()}>
    <span>Health</span>
    <strong>{latest ? `${latest.findings} ${latest.findings === 1 ? "finding" : "findings"}` : "—"}</strong>
    <small>{latest ? <>Latest report {when(latest.createdAt)}</> : reports.error ? "Unavailable" : reports.data ? "No report stored" : "Loading…"}</small>
  </a>;
}
function Dashboard({ inventory, reports }: { inventory: Resource<Inventory>; reports: Resource<Summary[]> }) {
  const partial = inventory.error || inventory.data?.failures.length;
  const cells = inventory.data?.rows[3] ?? [];
  const states = new Map<string, number>();
  for (const row of cells) { const state = row.doc.status?.state || "Not reported"; states.set(state, (states.get(state) ?? 0) + 1); }
  // A Ready cell that is out of sync with its Config, or whose comparison failed, needs attention too.
  const attention = cells.filter(row => row.doc.status?.state !== "Ready" || hasDrift(row.doc.status));
  const cardNote = inventory.loading ? "Refreshing…" : partial ? "Incomplete or stale data" : "View all →";
  const card = (depth: number) => <a className={`summary-card panel${depth === 3 ? " summary-card-cells" : ""}`} key={titles[depth]} href={listLink(depth)}><span>{titles[depth]}</span><strong>{inventory.data ? inventory.data.rows[depth].length : "—"}</strong>{depth === 3 && states.size > 0 && <ul className="summary-states" aria-label="Cell states">{[...states].map(([state, count]) => <li key={state}><StateBadge state={state} /><span>{count}</span></li>)}</ul>}<small>{cardNote}</small></a>;
  return <>
    <section className="panel attention-panel" aria-label="Cells needing attention">
      <div className="panel-heading"><h2>Needs attention</h2><span className="badge">{partial ? "Incomplete" : `${attention.length} of ${cells.length} cells`}</span></div>
      {attention.length ? <ul className="attention-list">{attention.map(row => <li key={JSON.stringify(row.scope)}><a className="resource-name" href={resourceLink(row.scope)}>{row.doc.metadata.name}</a><span className="mono-id">{row.scope.slice(0, 3).join(" / ")}</span><span className="attention-badges"><StateBadge state={row.doc.status?.state} /><DriftBadge status={row.doc.status} /></span>{hasDrift(row.doc.status) && <p className="attention-reason">{driftReason(row.doc.status)}</p>}</li>)}</ul>
        : <p className="attention-empty" role="status">{inventory.loading && !inventory.data ? "Loading cell states…" : partial ? "Cell states could not be fully loaded." : cells.length ? <><StateBadge state="Ready" /> All cells Ready</> : "No cells found."}</p>}
    </section>
    <div className="summary-grid">{card(3)}{[0, 1, 2].map(card)}<HealthCard reports={reports} /></div>
  </>;
}
// SpaceCIDR renders one Space's subnet: allocated, legacy conflist and unallocated read differently, and a
// failed read says so without breaking the row.
function SpaceCIDR({ subnet }: { subnet?: Resource<SpaceNetwork> }) {
  if (subnet?.data) return <span className={subnet.data.subnetSource === "allocator" && subnet.data.subnetCIDR ? undefined : "description"}>{formatSubnet(subnet.data)}</span>;
  if (subnet?.error) return <span className="cidr-error" title={subnet.error.message}>Unavailable</span>;
  return <span className="description" aria-label="Loading subnet">…</span>;
}
function ResourceList({ depth, inventory, parent, state: initialState, operations, onLifecycleAction, onParentAction }: {
  depth: number;
  inventory: Resource<Inventory>;
  parent: string[];
  // The Cells table's State filter, preselected by ?state= (#1635).
  state?: string;
  operations: Record<string, LifecycleFeedback>;
  onLifecycleAction: (scope: string[], action: LifecycleAction, state?: string, options?: PurgeOptions) => void;
  onParentAction: (scope: string[], action: ParentAction, options: PurgeOptions) => void;
}) {
  const [search, setSearch] = useState("");
  const [realm, setRealm] = useState("");
  const [state, setState] = useState(depth === 3 ? initialState ?? "" : "");
  const [page, setPage] = useState(0);
  const rows = inventory.data?.rows[depth] ?? [];
  const scoped = rows.filter(row => parent.every((name, i) => row.scope[i] === name));
  const filtered = scoped.filter(row => (!realm || row.scope[0] === realm) && (depth !== 3 || matchesState(row.doc.status?.state, state)) && row.scope.join(" / ").toLowerCase().includes(search.toLowerCase()));
  // All states, Active, Stopped or exited, then each state present in the rows (and a preselected one).
  const states = [...new Set([...rows.map(row => row.doc.status?.state || ""), ...(state && !["active", "inactive"].includes(state) ? [state] : [])].filter(Boolean))].sort();
  const pageSize = 25;
  const currentPage = Math.min(page, Math.max(0, Math.ceil(filtered.length / pageSize) - 1));
  const shown = filtered.slice(currentPage * pageSize, (currentPage + 1) * pageSize);
  // The Spaces table reads each visible Space's subnet: a Space document carries no CIDR.
  const subnets = useSpaceSubnets(depth === 1 ? shown.map(row => row.scope) : [], inventory.data?.updatedAt ?? "");
  return <section className="panel" aria-label={`${titles[depth]} list`}>
    <div className="list-controls"><label className="search-field">Search<input type="search" placeholder={`Search ${titles[depth].toLowerCase()}…`} value={search} onChange={e => { setSearch(e.target.value); setPage(0); }} /></label>{depth > 0 && <label>Realm<select aria-label="Filter by realm" value={realm} onChange={e => { setRealm(e.target.value); setPage(0); }}><option value="">All realms</option>{inventory.data?.rows[0].map(row => <option key={row.scope[0]}>{row.scope[0]}</option>)}</select></label>}{depth === 3 && <label>State<select aria-label="Filter by state" value={state} onChange={e => { setState(e.target.value); setPage(0); }}><option value="">All states</option><option value="active">Active</option><option value="inactive">Stopped or exited</option>{states.map(value => <option key={value} value={value}>{value}</option>)}</select></label>}<span className="result-count">{filtered.length} results</span></div>
    {parent.length > 0 && <p className="scope-filter">Scope: {parent.join(" / ")} <a href={listLink(depth)}>Clear scope</a></p>}
    <div className="table-scroll"><table className="stackable"><thead><tr><th>Name</th><th>State</th>{depth === 1 && <th>CIDR</th>}{depth > 0 && <th>Scope</th>}<th><span className="sr-only">Actions</span></th></tr></thead><tbody>{shown.map(row => <tr key={JSON.stringify(row.scope)}><td data-label="Name"><a className="resource-name" href={resourceLink(row.scope)}>{row.doc.metadata.name}</a></td><td data-label="State"><StateBadge state={row.doc.status?.state} />{depth === 3 && <DriftBadge status={row.doc.status} />}{depth === 3 && scheduledRun(row.doc) && <span className="badge scheduled-badge" title={`Scheduled run of Config ${scheduledRun(row.doc)!.config}`}>Scheduled</span>}{depth === 3 && failureStates.includes(row.doc.status?.state ?? "") && <AskMajordomo question={cellQuestion(row.scope, row.doc.status)} subject={`cell ${row.scope.join(" / ")}`} />}</td>{depth === 1 && <td className="space-cidr mono-id" data-label="CIDR"><SpaceCIDR subnet={subnets[JSON.stringify(row.scope)]} /></td>}{depth > 0 && <td className="scope-cell" data-label="Scope"><ScopePath parts={row.scope.slice(0, -1)} /></td>}<td className="row-actions">{depth === 3 ? <CellRowActions scope={row.scope} state={row.doc.status?.state} operation={operations[JSON.stringify(row.scope)]} onAction={onLifecycleAction} /> : <ParentRowActions scope={row.scope} counts={descendantCounts(inventory.data?.rows ?? [], row.scope)} allowed={parentActionsAllowed(row.scope)} operation={operations[JSON.stringify(row.scope)]} onAction={onParentAction} />}</td></tr>)}</tbody></table></div>
    {!filtered.length && <div className="empty-state"><h3>{inventory.loading ? "Loading resources…" : inventory.error || inventory.data?.failures.length ? "Resources could not be fully loaded" : scoped.length ? "No matching resources" : `No ${titles[depth].toLowerCase()} found`}</h3><p>{search || realm || state ? "Try another search or clear the filters." : "Refresh to retrieve the latest resource state."}</p></div>}
    <div className="pagination"><span>{filtered.length ? `${currentPage * pageSize + 1}–${Math.min((currentPage + 1) * pageSize, filtered.length)} of ${filtered.length}` : "0 results"}</span><button disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>Previous</button><button disabled={(currentPage + 1) * pageSize >= filtered.length} onClick={() => setPage(currentPage + 1)}>Next</button></div>
  </section>;
}
function ResourceDetails({ route, revision }: { route: Route; revision: number }) {
  const depth = route.scope.length - 1;
  const kind = kinds[depth];
  const detail = useResource<Doc>(apiPath(route.scope), revision);
  const containers = useResource<{ id: string; attachable?: boolean; root?: boolean }[]>(depth === 3 ? `${apiPath(route.scope)}/containers` : null, revision);
  const status = detail.data && (depth === 3 || depth === 4) && !route.document && <StatusSummary status={detail.data.status} kind={kind} />;
  const overview = detail.data && <section className="panel resource-overview"><div className="panel-heading"><h2>{route.document ? "API document" : "Resource overview"}</h2><StateBadge state={detail.data.status?.state} /></div>{route.document ? <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(detail.data, null, 2)}</pre> : <div className="panel-body"><dl className="details-grid"><dt>Name</dt><dd>{detail.data.metadata.name}</dd><dt>Kind</dt><dd>{detail.data.kind ?? kind}</dd><dt>Scope</dt><dd className="mono-id">{route.scope.join(" / ")}</dd><dt>API version</dt><dd>{detail.data.apiVersion ?? "Not reported"}</dd><dt>Labels</dt><dd>{Object.entries(detail.data.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(detail.data.metadata.labels ?? {}).length && "No labels"}</dd></dl>{depth < 3 && <a className="text-link" href={listLink(depth + 1, route.scope)}>View {titles[depth + 1].toLowerCase()} in this {kind} →</a>}</div>}</section>;
  const related = detail.data && !route.document && <RelatedResources doc={detail.data} revision={revision} />;
  const containerPanel = depth === 3 && <section className="panel container-panel"><div className="panel-heading"><h2>Containers</h2><span className="badge">{detail.error ? "Stale observations" : detail.loading ? "Refreshing observations" : "Cell observations"}</span></div><div className="panel-body">{containers.loading && <p>Loading containers…</p>}{containers.error && <ErrorNotice error={containers.error} />}{containers.error && containers.data && <p className="stale">Stale container list — refresh failed.</p>}{!containers.loading && !containers.error && !containers.data?.length && <p>No containers found.</p>}<ul className="state-list">{containers.data?.map(container => <li key={container.id}><a className="text-link" href={resourceLink([...route.scope, container.id])}>{container.id}</a><StateBadge state={detail.data?.status?.containers?.find(item => item.name === container.id || item.id === container.id)?.state} /><DocumentRowLink href={resourceLink([...route.scope, container.id], true)} label={container.id} /></li>)}</ul></div></section>;
  return <>
    <div className="detail-actions"><a href={listLink(Math.min(depth, 3))}>← Back to {titles[Math.min(depth, 3)].toLowerCase()}</a><a className="document-action" href={resourceLink(route.scope, !route.document)}>{route.document ? "Show overview" : "View document"}</a></div>
    {detail.loading && <p role="status">Loading {kind} details…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={kind} />}
    {detail.error && detail.data && <p className="stale">Stale resource details — refresh failed.</p>}
    {status}
    {overview}
    {related}
    {containerPanel}
    {depth === 0 && detail.data && !route.document && <RealmImagesPanel realm={route.scope[0]} revision={revision} credentials={registryEntries(detail.data).length} />}
    {detail.data && !route.document && networkKinds.includes(kind) && <NetworkPanel scope={route.scope} revision={revision} />}
    {detail.data && !route.document && <CgroupPanel scope={route.scope} revision={revision} />}
  </>;
}

type CellView = "terminal" | "read";
type LifecycleResponse = {
  error?: { code?: string; message?: string; phase?: string; stoppedFirst?: boolean };
  killed?: boolean;
  containersDeleted?: boolean;
  cgroupDeleted?: boolean;
  metadataDeleted?: boolean;
  purgeSucceeded?: boolean;
  force?: boolean;
  cascade?: boolean;
  deleted?: string[];
  purged?: string[];
};

type ParentActionResponse = {
  error?: { code?: string; message?: string };
  metadataDeleted?: boolean;
  purgeSucceeded?: boolean;
  force?: boolean;
  cascade?: boolean;
  deleted?: string[];
  purged?: string[];
};

async function readCellPresence(scope: string[]): Promise<boolean | null> {
  try {
    const response = await fetch(apiPath(scope), { headers: { Accept: "application/json" } });
    if (response.status === 404) return false;
    return response.ok ? true : null;
  } catch {
    return null;
  }
}

// cellTerminal (#/workspace/...) and cellRead (#/resource/...) share one mounted screen so switching
// between them keeps the TerminalWorkspace, and therefore every attached session, alive.
function CellDetailsScreen({ title, view, scope, revision, inventory, manager, theme, operations, onLifecycleAction, dockedCells, onOpenDock, onChanged }: {
  onChanged: () => void;
  dockedCells: string[];
  onOpenDock: () => void;
  title: string;
  view: CellView;
  scope: string[] | null;
  revision: number;
  inventory: InventoryResource;
  manager: TerminalManager;
  theme: Theme;
  operations: Record<string, LifecycleFeedback>;
  onLifecycleAction: (scope: string[], action: LifecycleAction, state?: string, options?: PurgeOptions) => void;
}) {
  const [terminalExpanded, setTerminalExpanded] = useState(false);
  const [terminalToolsHost, setTerminalToolsHost] = useState<HTMLDivElement | null>(null);
  const screen = useRef<HTMLDivElement>(null);
  const terminalBar = useRef<HTMLDivElement>(null);
  const scopeKey = JSON.stringify(scope ?? []);
  const detail = useResource<Doc>(scope ? apiPath(scope) : null, revision);
  const containers = useResource<{ id: string; attachable?: boolean; root?: boolean }[]>(scope ? `${apiPath(scope)}/containers` : null, revision);
  const inventoryState = scope ? inventory.data?.rows[3].find(row => JSON.stringify(row.scope) === scopeKey)?.doc.status?.state : undefined;
  const cellState = detail.data?.status?.state ?? inventoryState;
  const operation = operations[scopeKey];
  const terminalView = view === "terminal";
  const status = detail.data && <StatusSummary status={detail.data.status} kind="cell" scope={scope ?? undefined} />;
  // Detail loading and failures live in a fixed-size badge in the title row, so nothing renders between
  // the row and the terminal and the row's height never changes.
  const detailState = !scope ? null
    : detail.error ? detail.data ? { tone: "stale", label: "Stale cell details — refresh failed." } : { tone: "failed", label: `${failureTitle(detail.error, "Cell")}: ${detail.error.message}` }
      : detail.loading && !detail.data ? { tone: "checking", label: "Loading cell details…" } : null;

  useEffect(() => { setTerminalExpanded(false); }, [scopeKey, view]);

  // Stack the fixed terminal bar below the toolbar (whose actions sit above it) and the expanded terminal below
  // the bar; both heights change when they wrap at narrow or zoomed widths.
  useLayoutEffect(() => {
    const host = screen.current, bar = terminalBar.current;
    if (!terminalExpanded || !host || !bar) return;
    const toolbar = host.closest(".workspace")?.querySelector<HTMLElement>(".toolbar");
    const place = () => {
      if (toolbar) host.style.setProperty("--terminal-expanded-bar-top", `${Math.ceil(toolbar.getBoundingClientRect().bottom) + 8}px`);
      host.style.setProperty("--terminal-expanded-top", `${Math.ceil(bar.getBoundingClientRect().bottom) + 8}px`);
    };
    place();
    const observer = new ResizeObserver(place);
    observer.observe(bar);
    if (toolbar) observer.observe(toolbar);
    window.addEventListener("resize", place);
    return () => {
      observer.disconnect(); window.removeEventListener("resize", place);
      host.style.removeProperty("--terminal-expanded-bar-top"); host.style.removeProperty("--terminal-expanded-top");
    };
  }, [terminalExpanded]);

  // The Config this cell was created from, read from spec.provenance like the Config page's "Cells using
  // this Config" panel reads it the other way.
  const sourceConfig = detail.data ? configForCell(detail.data) : null;
  const sourceConfigScope = sourceConfig ? referenceScope(sourceConfig) : [];
  // Display names (#1692): the instance label the agent lists show, the cell's own name and its agent's.
  const allCells = inventory.data?.rows[3];
  const configs = inventory.data?.configs;
  const labels = useMemo(() => instanceLabels(allCells ?? [], configs), [allCells, configs]);
  const instanceLabel = scope ? labels.get(scopeKey) : undefined;
  const inventoryDoc = scope ? allCells?.find(row => JSON.stringify(row.scope) === scopeKey)?.doc : undefined;
  const cellDisplayName = ownDisplayName(inventoryDoc ?? detail.data);
  const agentConfig = sourceConfig ? configs?.find(doc => doc.metadata.name === sourceConfig.name && (doc.metadata.realm ?? "") === (sourceConfig.realm ?? "") && (doc.metadata.space ?? "") === (sourceConfig.space ?? "") && (doc.metadata.stack ?? "") === (sourceConfig.stack ?? "")) : undefined;
  const agentName = configDisplayName(agentConfig).name;
  // A scheduled run names its Config by label and its fire time by name; the Config itself is read only
  // on cellRead, for its schedule.
  const run = detail.data ? scheduledRun(detail.data) : null;
  // One network read per refresh on cellRead, shared by the IP address row and the Network section;
  // cellTerminal reads none.
  const network = useResource<NetworkDetail>(scope && !terminalView ? `${apiPath(scope)}/network` : null, revision);
  const cellNetwork = network.data && !Array.isArray(network.data) ? network.data.cell : undefined;
  // A failed read wins over the last value: the row never shows an address the daemon could not confirm.
  const ipRow = <><dt>IP address</dt><dd className="cell-ip">{network.error ? <span title={network.error.message}>Unavailable</span> : cellNetwork ? <>{cellNetwork.ipv4State === "assigned" ? <code>{formatIPv4(cellNetwork)}</code> : formatIPv4(cellNetwork)}{cellNetwork.ipv4State === "assigned" && cellNetwork.rootContainer && <span className="description"> · root container <code>{cellNetwork.rootContainer}</code></span>}</>
    : network.loading ? "…" : "Not reported"}</dd></>;
  const sourceConfigDoc = useResource<{ spec?: { schedule?: Schedule }; status?: { schedule?: ScheduleStatus } }>(sourceConfig && !terminalView ? catalogAPI("configs", sourceConfigScope) : null, revision);
  const sourceSchedule = sourceConfigDoc.data?.spec?.schedule;
  const scheduleRow = (run || sourceSchedule) && <><dt>Schedule</dt><dd className="cell-schedule">
    {run && <p>Scheduled run of {sourceConfig ? <a className="resource-name" href={catalogLink("configs", sourceConfigScope)}>{run.config}</a> : <code>{run.config}</code>}, fired {run.firedAt ? when(run.firedAt) : "at an unknown time"}.</p>}
    {sourceSchedule && <p><a className="resource-name" href={catalogLink("configs", sourceConfigScope)}>{sourceConfigScope.at(-1)}</a> runs <code>{scheduleText(sourceSchedule)}</code>{sourceSchedule.suspend ? " (suspended)" : ""} · next {sourceSchedule.suspend ? "—" : when(sourceConfigDoc.data?.status?.schedule?.nextScheduleTime)} · last {when(sourceConfigDoc.data?.status?.schedule?.lastScheduleTime)}</p>}
    {run && sourceConfigDoc.error?.code === "not_found" && <p className="description">The Config no longer exists.</p>}
  </dd></>;
  const overview = detail.data && scope && <section className="panel resource-overview">
    <div className="panel-heading"><h2>Resource overview</h2><div className="cell-overview-actions"><StateBadge state={detail.data.status?.state} /><a className="document-action" href={resourceLink(scope, true)} target="_blank" rel="noopener noreferrer">View document</a></div></div>
    <div className="panel-body"><dl className="details-grid">
      {/* The agent's and the instance's display names (#1692) ride on the Config and Name rows, so the
          overview keeps its height. */}
      <dt>Config</dt><dd className={sourceConfig ? "display-name-row" : undefined}>{sourceConfig ? <><a className="resource-name" href={catalogLink("configs", sourceConfigScope)}>{sourceConfigScope.join(" / ")}</a>{agentName && <span className="badge agent-display-name">Agent: {agentName}</span>}<RenameControl subject={`agent ${sourceConfigScope.join(" / ")}`} current={ownDisplayName(agentConfig)} onSave={async value => { const ref = { name: sourceConfig.name ?? "", realm: sourceConfig.realm ?? scope[0], space: sourceConfig.space, stack: sourceConfig.stack }; inventory.setConfigAnnotations(ref, await renameConfig(ref, value)); onChanged(); }} /></> : "Not created from a Config"}</dd>
      {ipRow}
      {scheduleRow}
      <dt>Labels</dt><dd>{Object.entries(detail.data.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(detail.data.metadata.labels ?? {}).length && "No labels"}</dd>
      <dt>Name</dt><dd className="display-name-row"><span>{detail.data.metadata.name}</span>{cellDisplayName ? <span className="badge instance-display-name">Display name: {cellDisplayName}</span> : instanceLabel && <span className="badge instance-display-name">Listed as {instanceLabel}</span>}<RenameControl subject={`instance ${scope.join(" / ")}`} current={cellDisplayName} onSave={async value => { inventory.setCellAnnotations(scope, await renameCell(scope, value)); onChanged(); }} /></dd>
      <dt>Kind</dt><dd>{detail.data.kind ?? "cell"}</dd>
      <dt>Scope</dt><dd className="mono-id">{scope.join(" / ")}</dd>
      <dt>API version</dt><dd>{detail.data.apiVersion ?? "Not reported"}</dd>
    </dl></div>
  </section>;

  const containerPanel = <section className="panel container-panel">
    <div className="panel-heading"><h2>Containers</h2><span className="badge">{detail.error ? "Stale observations" : detail.loading ? "Refreshing observations" : "Cell observations"}</span></div>
    <div className="panel-body">
      {containers.loading && <p>Loading containers…</p>}
      {containers.error && <ErrorNotice error={containers.error} />}
      {containers.error && containers.data && <p className="stale">Stale container list — refresh failed.</p>}
      {!containers.loading && !containers.error && !containers.data?.length && <p>No containers found.</p>}
      <ul className="state-list">{containers.data?.map(container => <li key={container.id}><a className="text-link" href={resourceLink([...(scope ?? []), container.id])}>{container.id}</a><StateBadge state={detail.data?.status?.containers?.find(item => item.name === container.id || item.id === container.id)?.state} /><DocumentRowLink href={resourceLink([...(scope ?? []), container.id], true)} label={container.id} newTab /></li>)}</ul>
    </div>
  </section>;

  const switchLabel = scope ? `${terminalView ? "View details for" : "Open terminal for"} ${scope.join(" / ")}` : "";
  return <div className="cell-detail-screen" ref={screen} hidden={!scope}>
    {scope && <div className="page-heading cell-page-heading">
      {/* cellTerminal keeps one action row: title, view switch, terminal tools, then lifecycle actions. */}
      <div ref={terminalBar} className={`cell-page-title-row${terminalView ? " cell-terminal-bar" : ""}${terminalExpanded ? " is-terminal-expanded" : ""}`}>
        <h1 tabIndex={-1} title={instanceLabel ? scope.join(" / ") : title}>{instanceLabel ?? title}</h1>
        {instanceLabel && <span className="cell-page-name mono-id" title={scope.join(" / ")}>{scope[3]}</span>}
        <span className={`cell-detail-state ${detailState?.tone ?? "is-idle"}`} aria-hidden={!detailState} tabIndex={detailState ? 0 : -1} aria-label={detailState ? `Cell details: ${detailState.label}` : undefined} data-tooltip={detailState?.label}><span className="dot" aria-hidden="true" /></span>
        <a className="icon-button cell-view-switch" href={terminalView ? resourceLink(scope) : workspaceLink(scope)} aria-label={switchLabel} title={terminalView ? "View cell details" : "Open cell terminal"}>
          <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
            {terminalView ? <><circle cx="12" cy="12" r="9" /><path d="M12 11v5" /><path d="M12 7.5h.01" /></> : <><rect x="3" y="4.5" width="18" height="15" rx="2" /><path d="m7 9.5 3 2.5-3 2.5" /><path d="M12.5 15H17" /></>}
          </svg>
        </a>
        <div className="cell-terminal-tools" ref={setTerminalToolsHost} hidden={!terminalView} />
        <AskMajordomo prominent={failureStates.includes(cellState ?? "")} question={cellQuestion(scope, detail.data?.status ?? { state: cellState })} subject={`cell ${scope.join(" / ")}`} />
        <CellLifecycleControls scope={scope} state={cellState} operation={operation} onAction={onLifecycleAction} />
      </div>
    </div>}
    <section className="cell-terminal-view" aria-label="Cell terminal" hidden={!terminalView}>
      <TerminalWorkspace scope={scope} revision={revision} inventory={inventory} manager={manager} theme={theme} visible={terminalView && !!scope} controlsHost={terminalToolsHost} onExpandedChange={setTerminalExpanded} dockedCells={dockedCells} onOpenDock={onOpenDock} operation={operation} onLifecycleAction={onLifecycleAction} />
    </section>
    <section className="cell-overview-content" aria-label="Cell details" hidden={terminalView}>{detail.error && <ErrorNotice error={detail.error} kind="Cell" />}{detail.error && detail.data && <p className="stale">Stale resource details — refresh failed.</p>}{status}{containerPanel}{overview}{scope && !terminalView && <NetworkPanel scope={scope} revision={revision} detail={network} />}{scope && !terminalView && <CgroupPanel scope={scope} revision={revision} />}</section>
  </div>;
}

function App({ user, onSignOut }: { user: string; onSignOut: () => void }) {
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision(value => value + 1), []);
  const [operations, setOperations] = useState<Record<string, LifecycleFeedback>>({});
  const { notify } = useNotify();
  // The cell on screen when a lifecycle result arrives; toasts for any other cell name its scope.
  const screenCell = useRef("");
  const pendingOperations = useRef(new Set<string>());
  const [route, setRoute] = useState(readRoute);
  const [settings, setSetting] = useSettings();
  const theme = settings.theme;
  const [menuOpen, setMenuOpen] = useState(false);
  // The open connector's display title, reported by the Connectors page once its catalog has loaded.
  const [connectorTitle, setConnectorTitle] = useState<{ name: string; title: string }>();
  const sidebar = useSidebarLayout();
  // The sidebar and the grid filter and group independently (#1686).
  const sidebarFilter = useCellFilter(sidebarGroupingKey, settings.defaultGroupBy, settings.showSidebarSearch);
  const gridFilter = useCellFilter(gridGroupingKey, settings.defaultGroupBy, settings.showGridSearch);
  const menuToggle = useRef<HTMLButtonElement>(null);
  const sidebarClose = useRef<HTMLButtonElement>(null);
  const restoreMenuFocus = useRef(false);
  // Closing the drawer from its scrim, close button or Escape returns focus to the menu toggle once
  // main is no longer inert; choosing a link closes it and leaves focus to the new page.
  const closeMenu = () => { restoreMenuFocus.current = true; setMenuOpen(false); };
  useEffect(() => {
    const changed = () => { setRoute(readRoute()); setMenuOpen(false); };
    window.addEventListener("hashchange", changed);
    return () => window.removeEventListener("hashchange", changed);
  }, []);
  useEffect(() => {
    if (!menuOpen) {
      if (restoreMenuFocus.current) menuToggle.current?.focus();
      restoreMenuFocus.current = false;
      return;
    }
    sidebarClose.current?.focus();
    const keydown = (event: KeyboardEvent) => { if (event.key === "Escape") closeMenu(); };
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, [menuOpen]);
  const connection = useResource<{ endpoint: string; version: string; daemonVersion?: string }>("/api/connection", revision);
  const inventory = useInventory(revision);
  // The newest health report feeds the Overview's Health card and the Manage dot (#1635).
  const [reportReads, setReportReads] = useState(0);
  const reports = useResource<Summary[]>("/api/health/reports", revision + reportReads);
  const [seenId, setSeenId] = useState(storedHealthSeen);
  const latestReport = useRef<string | undefined>(undefined);
  latestReport.current = reports.data?.[0]?.id;
  // The Health tab reads its own list; a report it shows that this list has not seen yet re-reads it.
  const markReportSeen = useCallback((id: string) => {
    setSeenId(id);
    if (latestReport.current !== id) setReportReads(value => value + 1);
  }, []);
  const terminals = useTerminalSessions(inventory);
  const major = useMajordomo(inventory, revision);
  const viewport = useViewportWidth();
  const mobile = viewport <= mobileMaxWidth;
  // The sidebar's rendered width: the resizable column, the icon rail, or nothing when it is a drawer.
  const sidebarWidth = mobile ? 0 : sidebar.collapsed ? railWidth : sidebar.width;
  const dock = useMajordomoDock(viewport, sidebarWidth);
  // The toast stack is a sibling of the shell, so the rendered dock width is published on the root.
  useLayoutEffect(() => { document.documentElement.style.setProperty("--majordomo-width", `${dock.effectiveWidth}px`); }, [dock.effectiveWidth]);
  // Every majordomo cell is docked in the panel; their terminals never open in the cell screen.
  const dockedCells = major.cells.map(row => JSON.stringify(row.scope));
  function recordOperation(scope: string[], operation: LifecycleFeedback) {
    const key = JSON.stringify(scope);
    setOperations(old => ({ ...old, [key]: operation }));
    // Delete and Purge navigate away from the cell, so their results always name it.
    const named = screenCell.current !== key || operation.action === "delete" || operation.action === "purge";
    notify({ key: `lifecycle:${key}`, tone: operation.phase, title: named ? scope.join(" / ") : undefined, message: operation.message });
  }
  // A confirmed removal closes the cell's terminals. Only the removed cell's own screen navigates home;
  // a removal started from the Cells table keeps the table, where the row drops on the next inventory read.
  function leaveRemovedCell(scope: string[]) {
    terminals.removeCell(scope);
    if (screenCell.current === JSON.stringify(scope)) window.location.hash = homeLink;
  }
  async function runCellLifecycle(scope: string[], action: LifecycleAction, state?: string, options?: PurgeOptions) {
    const key = JSON.stringify(scope);
    if (pendingOperations.current.has(key)) return;
    pendingOperations.current.add(key);
    terminals.holdCell(scope);
    const pendingMessage = action === "restart"
      ? state === "Ready" ? "Restarting cell: stopping, then starting…" : "Restarting cell: starting or recovering…"
      : action === "kill" ? "Killing cell containers…"
        : action === "delete" ? "Deleting cell resources…"
          : action === "purge" ? "Purging cell resources…"
            : `${action === "start" ? "Starting" : "Stopping"} cell…`;
    recordOperation(scope, { action, phase: "pending", message: pendingMessage });
    try {
      const response = await fetch(`${apiPath(scope)}/${action}`, {
        method: "POST",
        headers: action === "purge" ? { Accept: "application/json", "Content-Type": "application/json" } : { Accept: "application/json" },
        ...(action === "purge" ? { body: JSON.stringify(requestedPurgeOptions(options?.force, options?.cascade)) } : {}),
      });
      let body: LifecycleResponse = {};
      try { body = await response.json(); } catch { /* Use the HTTP status below. */ }
      if (!response.ok) {
        const failure = body.error ?? { code: "unavailable", message: `HTTP ${response.status}` };
        const phase = failure.phase;
        if (action === "restart" && (phase === "stopping" || phase === "starting")) {
          terminals.interruptCell(scope, phase === "stopping"
            ? "Restart failed during stopping. The previous terminal connection may have ended."
            : failure.stoppedFirst
              ? "The cell stopped, but restart failed during starting. Reconnect when the cell is Ready to attach to its current process."
              : "Restart failed during starting. Reconnect when the cell is Ready to attach to its current process.");
        } else if (action === "start") {
          terminals.interruptCell(scope, "Cell start failed. Reconnect when the cell is Ready to attach to its current process.");
        }

        let presence: boolean | null = null;
        if (action === "delete" || action === "purge") {
          presence = await readCellPresence(scope);
          if (presence === false) {
            leaveRemovedCell(scope);
          } else if (failure.code === "unavailable") {
            terminals.interruptCell(scope, "The destructive action did not return a result. This cell's terminal may have been affected; refresh before reconnecting.");
          }
        } else if (action === "kill" && failure.code === "unavailable") {
          terminals.interruptCell(scope, "The kill result was not received. This cell's terminal may have ended; refresh before reconnecting.");
        }

        const reason = failure.code === "permission_denied" ? "Permission denied"
          : failure.code === "unavailable" ? "Daemon unavailable"
          : failure.code === "invalid_state" ? "Invalid cell state"
          : failure.code === "operation_in_progress" ? "Another operation is in progress"
          : failure.code === "not_found" ? "Cell no longer exists"
          : failure.code === "invalid_request" ? "Invalid lifecycle request"
          : "Request failed";
        const title = action === "restart" && phase === "stopping" ? "Restart failed while stopping"
          : action === "restart" && phase === "starting" ? "Restart failed while starting"
          : action === "restart" && phase === "validation" ? "Restart is unavailable"
          : `${action[0].toUpperCase()}${action.slice(1)} failed`;
        const reconcile = action === "delete" || action === "purge"
          ? presence === false ? " A fresh daemon read confirms the cell is gone; its terminal tabs were closed."
            : presence === true ? " A fresh daemon read confirms the cell still exists; it remains listed in the sidebar."
              : " The final state is unknown; the cell and terminal tabs were retained. Refresh before retrying."
          : "";
        recordOperation(scope, { action, phase: "error", message: `${title}. ${reason}: ${failure.message ?? "The daemon rejected the request."}${reconcile}` });
        return;
      }

      if (action === "stop" || action === "restart") {
        terminals.interruptCell(scope, action === "stop"
          ? "Cell stopped. The previous terminal session ended. Reconnect after the cell starts."
          : "Cell restarted. The previous terminal session ended; reconnect to attach to the new process.");
      }
      if (action === "kill") {
        if (body.killed) {
          terminals.interruptCell(scope, "Cell killed. The cell remains; its terminal session ended and can be reconnected when appropriate.");
          recordOperation(scope, { action, phase: "success", message: "Cell killed. Its containers were terminated; the cell definition remains and its terminal session ended." });
        } else {
          recordOperation(scope, { action, phase: "error", message: "The daemon did not confirm a kill. The terminal connection was left intact; refresh the cell state." });
        }
        return;
      }
      if (action === "delete") {
        const presence = body.metadataDeleted ? false : await readCellPresence(scope);
        if (cellRemovalConfirmed(body.metadataDeleted, presence)) leaveRemovedCell(scope);
        const complete = deleteOperationComplete(body);
        const outcome = body.metadataDeleted
          ? "kukeond reports that cell metadata was deleted."
          : presence === false ? "A fresh daemon read confirms the cell is gone."
          : presence === true ? "The cell still exists and remains listed in the sidebar."
            : "The final state could not be confirmed; the cell and terminal tabs were retained.";
        const effects = `Containers deleted: ${body.containersDeleted === true ? "yes" : "not reported"}; cgroup deleted: ${body.cgroupDeleted === true ? "yes" : "not reported"}; metadata deleted: ${body.metadataDeleted === true ? "yes" : "not confirmed"}.`;
        if (!complete && presence !== false) {
          terminals.interruptCell(scope, "Delete was incomplete. The cell may still exist, but its runtime resources may have changed.");
        }
        recordOperation(scope, { action, phase: complete ? "success" : "error", message: complete ? `Cell deleted. ${effects}` : `Delete incomplete. ${effects} ${outcome}` });
        return;
      }
      if (action === "purge") {
        const presence = body.metadataDeleted ? false : await readCellPresence(scope);
        if (cellRemovalConfirmed(body.metadataDeleted, presence)) leaveRemovedCell(scope);
        const complete = purgeOperationComplete(body);
        const outcome = body.metadataDeleted
          ? "kukeond reports that cell metadata was deleted."
          : presence === false ? "A fresh daemon read confirms the cell is gone."
          : presence === true ? "The cell still exists and remains listed in the sidebar."
            : "The final state could not be confirmed; the cell and terminal tabs were retained.";
        const effects = `Containers deleted: ${body.containersDeleted === true ? "yes" : "not reported"}; cgroup deleted: ${body.cgroupDeleted === true ? "yes" : "not reported"}; metadata deleted: ${body.metadataDeleted === true ? "yes" : "not confirmed"}; comprehensive purge: ${body.purgeSucceeded === true ? "complete" : "incomplete"}.`;
        const details = body.purgeSucceeded
          ? ` Deleted: ${body.deleted?.join(", ") || "none reported"}. Purged: ${body.purged?.join(", ") || "none reported"}.`
          : ` Daemon-reported cleanup targets (individual completion unconfirmed): ${body.deleted?.join(", ") || "none reported"}. Purge detail: ${body.purged?.join(", ") || "none reported"}.`;
        if (!complete && presence !== false) {
          terminals.interruptCell(scope, "Purge was incomplete. The cell may still exist, but its runtime resources may have changed.");
        }
        recordOperation(scope, { action, phase: complete ? "success" : "error", message: complete ? `Cell purged. ${effects}${details}` : `Purge incomplete. ${effects}${details} ${outcome}` });
        return;
      }
      // The interrupted sessions belong to the stopped process; dropping them lets the terminal reattach on Ready.
      if (action === "start") terminals.dropInterrupted(scope);
      const message = action === "start" ? "Cell started."
        : action === "stop" ? "Cell stopped. The previous terminal session ended."
        : "Cell restarted. The previous terminal session ended; reconnect to attach to the new process.";
      recordOperation(scope, { action, phase: "success", message });
    } catch (error) {
      if (action === "stop" || action === "restart") {
        terminals.interruptCell(scope, "The lifecycle request lost its connection. The terminal session may have ended; refresh the cell before reconnecting.");
      }
      const message = error instanceof Error ? error.message : "The request could not reach the web server.";
      let outcome = "";
      if (action === "kill") {
        terminals.interruptCell(scope, "The kill result was not received. This cell's terminal may have ended; refresh before reconnecting.");
        outcome = " The cell state is being refreshed; the kill result remains unknown.";
      } else if (action === "delete" || action === "purge") {
        const presence = await readCellPresence(scope);
        if (presence === false) {
          leaveRemovedCell(scope);
          outcome = " A fresh daemon read confirms the cell is gone; its terminal tabs were closed.";
        } else if (presence === true) {
          terminals.interruptCell(scope, "The destructive action response was lost. The cell still exists, but its runtime may have changed; refresh before reconnecting.");
          outcome = " A fresh daemon read confirms the cell still exists; the operation's runtime effects are unknown.";
        } else {
          terminals.interruptCell(scope, "The destructive action response was lost. This cell's terminal may have been affected; refresh before reconnecting.");
          outcome = " The final state is unknown; the cell remains visible until a daemon refresh confirms removal.";
        }
      }
      recordOperation(scope, { action, phase: "error", message: `${action[0].toUpperCase()}${action.slice(1)} outcome unknown. Daemon unavailable: ${message}.${outcome}` });
    } finally {
      terminals.releaseCell(scope);
      pendingOperations.current.delete(key);
      setRevision(value => value + 1);
    }
  }
  // Delete or purge a realm, space or stack (#1495). Rows, their descendants and sidebar cells drop only
  // when the follow-up inventory read omits them; terminals close once a fresh read confirms removal.
  async function runParentAction(scope: string[], action: ParentAction, options: PurgeOptions) {
    const key = JSON.stringify(scope);
    if (pendingOperations.current.has(key)) return;
    pendingOperations.current.add(key);
    const kind = kinds[scope.length - 1];
    const verb = action === "delete" ? "Delete" : "Purge";
    const cells = cellsWithin(inventory.data?.rows ?? [], scope);
    recordOperation(scope, { action, phase: "pending", message: `${action === "delete" ? "Deleting" : "Purging"} ${kind}…` });
    const confirmRemoval = async () => {
      const presence = await readCellPresence(scope);
      if (presence === false) {
        for (const cell of cells) terminals.removeCell(cell);
        const current = readRoute();
        if (current.scope.length >= scope.length && scope.every((part, index) => current.scope[index] === part)) window.location.hash = homeLink;
      }
      const text = presence === false ? ` A fresh daemon read confirms the ${kind} is gone.`
        : presence === true ? ` A fresh daemon read confirms the ${kind} still exists.`
          : " The final state could not be confirmed; refresh before retrying.";
      return { removed: presence === false, text };
    };
    try {
      const response = await fetch(`${apiPath(scope)}/${action}`, {
        method: "POST",
        headers: { Accept: "application/json", "Content-Type": "application/json" },
        body: JSON.stringify(requestedPurgeOptions(options.force, options.cascade)),
      });
      let body: ParentActionResponse = {};
      try { body = await response.json(); } catch { /* Use the HTTP status below. */ }
      if (!response.ok) {
        const failure = body.error ?? { code: "unavailable", message: `HTTP ${response.status}` };
        const message = failure.message ?? "The daemon rejected the request.";
        if (failure.code === "has_dependencies") {
          recordOperation(scope, { action, phase: "error", message: `${verb} refused. ${message} Enable cascade to ${action} its children first.` });
          return;
        }
        const reason = failure.code === "permission_denied" ? "Permission denied"
          : failure.code === "unavailable" ? "Daemon unavailable"
          : failure.code === "operation_in_progress" ? "Another operation is in progress"
          : failure.code === "not_found" ? `The ${kind} no longer exists`
          : failure.code === "invalid_request" ? "Invalid request"
          : "Request failed";
        const outcome = failure.code === "unavailable" || failure.code === "not_found" ? (await confirmRemoval()).text : "";
        recordOperation(scope, { action, phase: "error", message: `${verb} failed. ${reason}: ${message}${outcome}` });
        return;
      }
      const { removed, text: outcome } = await confirmRemoval();
      const complete = removed && body.purgeSucceeded !== false && body.metadataDeleted !== false;
      const effects = [
        body.deleted?.length ? `Deleted: ${body.deleted.join(", ")}.` : "",
        body.purged?.length ? `Purged: ${body.purged.join(", ")}.` : "",
        body.purgeSucceeded === false ? "Comprehensive purge: incomplete." : "",
        `Options passed: --force ${body.force ? "enabled" : "disabled"}, --cascade ${body.cascade ? "enabled" : "disabled"}.`,
      ].filter(Boolean).join(" ");
      recordOperation(scope, { action, phase: complete ? "success" : "error", message: complete ? `${kind[0].toUpperCase()}${kind.slice(1)} ${action}d. ${effects}${outcome}` : `${verb} incomplete. ${effects}${outcome}` });
    } catch (error) {
      const message = error instanceof Error ? error.message : "The request could not reach the web server.";
      recordOperation(scope, { action, phase: "error", message: `${verb} outcome unknown. Daemon unavailable: ${message}.${(await confirmRemoval()).text}` });
    } finally {
      pendingOperations.current.delete(key);
      setRevision(value => value + 1);
    }
  }
  // After creation (#1603) the UI lands on cellTerminal only when the cell is started with a terminal attached
  // and has a workload to attach to; otherwise on cellRead, which never opens a terminal session.
  async function handleCellCreated(cell: Doc, options: { start: boolean; attach: boolean }) {
    const start = options.start;
    const spec = cell.spec as { realmId?: unknown; spaceId?: unknown; stackId?: unknown; containers?: { id?: unknown; attachable?: unknown; root?: unknown }[]; tty?: { default?: unknown } } | undefined;
    if (typeof spec?.realmId !== "string" || typeof spec.spaceId !== "string" || typeof spec.stackId !== "string") {
      notify({ key: "create", tone: "error", message: "The daemon created a Cell but returned an incomplete scope. Refresh the inventory to locate it." });
      setRevision(value => value + 1);
      return;
    }
    const scope = [spec.realmId, spec.spaceId, spec.stackId, cell.metadata.name];
    const attachable = spec.containers?.filter(container => container.attachable === true && container.root !== true) ?? [];
    const preferred = typeof spec.tty?.default === "string" ? attachable.find(container => container.id === spec.tty?.default) : undefined;
    const selected = preferred ?? attachable[0];
    if (typeof selected?.id === "string") terminals.select(scope, selected.id);
    inventory.upsertCell(cell);
    setRevision(value => value + 1);
    const openTerminal = options.start && options.attach && typeof selected?.id === "string";
    window.location.hash = openTerminal ? workspaceLink(scope) : resourceLink(scope);
    const key = `create:${JSON.stringify(scope)}`;
    const link = { href: resourceLink(scope), label: "View cell details" };
    notify({ key, tone: start ? "pending" : "success", message: start ? `Cell ${scope.join(" / ")} created. Starting it…` : `Cell ${scope.join(" / ")} created and left stopped.`, link });
    if (!start) return;
    try {
      const response = await fetch(`${apiPath(scope)}/start`, { method: "POST", headers: { Accept: "application/json" } });
      let body: { error?: { message?: string } } = {};
      try { body = await response.json(); } catch { /* Use the HTTP status below. */ }
      if (!response.ok) throw new Error(body.error?.message ?? `HTTP ${response.status}`);
      notify({ key, tone: "success", message: `Cell ${scope.join(" / ")} started.${!selected ? " No attachable workload was defined." : openTerminal ? " The terminal will attach to its selected workload when ready." : ""}`, link });
    } catch (error) {
      notify({ key, tone: "error", message: `Cell ${scope.join(" / ")} was created stopped, but start failed: ${error instanceof Error ? error.message : "The daemon request failed."}`, link });
    } finally { setRevision(value => value + 1); }
  }
  // A new agent (#1692) is stored and started by the server; the page then opens its cell's terminal.
  function handleAgentCreated(agent: CreatedAgent) {
    const { cell, config } = agent;
    const scope = [cell.realm, cell.space ?? "", cell.stack ?? "", cell.name];
    setRevision(value => value + 1);
    const named = config.annotations?.["kukeon.io/display-name"];
    notify({ key: `agent:${JSON.stringify(scope)}`, tone: "success", message: `Agent ${named ? `${named} ` : ""}created: cell ${scope.join(" / ")} started.`, link: { href: resourceLink(scope), label: "View cell details" } });
    window.location.hash = workspaceLink(scope);
  }
  // Workbench (terminals) or Manage (resources), derived from the route and never stored (#1635).
  const view = viewOf(route);
  const isWorkspace = route.view === "workspace";
  const cellScreenScope = (isWorkspace || route.view === "resource" && !route.document) && route.scope.length === 4 ? route.scope : null;
  // cellRead flows as an ordinary scrolling page; only cellTerminal pins the shell to the viewport.
  const cellView: CellView = isWorkspace ? "terminal" : "read";
  const cellTerminalActive = !!cellScreenScope && cellView === "terminal";
  screenCell.current = cellScreenScope ? JSON.stringify(cellScreenScope) : "";
  // Unread terminal output (#1696). The cell being read is the open cell while its terminal screen is
  // shown, the session on it is attached and the tab is visible; the polled cells are the ones the
  // Workbench sidebar lists.
  const tabVisible = useTabVisible();
  const openCell = cellTerminalActive && tabVisible && cellScreenScope ? cellId(cellScreenScope) : "";
  const shownContainer = openCell ? terminals.selection[openCell] : undefined;
  const shownSession = openCell && !dockedCells.includes(openCell)
    ? terminals.sessions.find(session => cellId(session.scope) === openCell && (!shownContainer || session.container === shownContainer))
    : undefined;
  const readingCell = shownSession?.state === "attached" ? openCell : null;
  const allCells = inventory.data?.rows[3];
  const unreadScopes = useMemo(() => activeCells(allCells ?? [], cellScreenScope ?? undefined).map(row => row.scope), [allCells, cellScreenScope?.join("\n")]);
  const inventoryKeys = useMemo(() => inventory.data && !inventory.error && !inventory.data.failures.length ? (allCells ?? []).map(row => cellId(row.scope)) : null, [inventory.data, inventory.error, allCells]);
  const unread = useUnread(unreadScopes, readingCell, view === "workbench", inventoryKeys);
  const depth = titles.findIndex(title => title.toLowerCase() === route.view);
  const catalog = Object.keys(catalogTitles).includes(route.view);
  const images = route.view === "images";
  const daemonView = route.view === "daemon" || route.view === "daemon-logs";
  const connectorsView = route.view === "connectors";
  const teamsView = route.view === "teams";
  const sourcesView = route.view === "sources";
  // A team's Edit team and Export screens title the screen; the team is the context.
  const teamAction = teamsView && route.scope[0] && route.action ? route.action === "edit" ? "Edit team" : "Export" : undefined;
  // A connector page is headed by its title, with the slug as secondary text; until the catalog loads, or
  // for an unknown connector, the slug stands in.
  const connectorHeading = connectorsView && route.scope[0] ? connectorTitle?.name === route.scope[0] ? connectorTitle.title : route.scope[0] : undefined;
  // The identity pages of a connector: #/connectors/<name>/new-identity and …/rotate-identity.
  const identityAction = connectorsView && route.scope[0] && route.action ? route.action === "rotate-identity" ? "Rotate identity" : "New identity" : undefined;
  const heading = route.view === "settings" ? "Settings" : connectorsView ? identityAction ?? connectorHeading ?? "Connectors" : teamsView ? teamAction ?? route.scope[0] ?? "Teams" : route.view === "new-team" ? "New team" : sourcesView ? route.scope[0] ?? "Sources" : route.view === "health" ? "Health" : route.view === "daemon" ? "Daemon" : route.view === "daemon-logs" ? "Daemon logs" : isWorkspace ? route.scope.at(-1) ?? "Cell" : route.view === "new-cell" ? "New Cell" : route.view === "new-agent" ? "New agent" : route.view === "new-config" ? "New Config" : route.view === "new-secret" ? "New Secret" : images ? route.scope[1] ?? "Images" : catalog ? route.scope.at(-1) ?? catalogTitles[route.view] : route.view === "resource" ? route.scope.at(-1) : depth >= 0 ? titles[depth] : route.view === "workbench" ? "Workbench" : "Overview";
  // Only a scope path or realm qualifies the title; generic subtitles were dropped.
  const headingContext = teamAction ? route.scope[0] : connectorsView ? identityAction ? connectorHeading ?? "" : connectorHeading && connectorHeading !== route.scope[0] ? route.scope[0] : "" : isWorkspace ? route.scope.join(" / ") : images ? route.scope[1] ? route.scope[0] : "" : route.view === "resource" || (catalog && route.scope.length > 0) ? route.scope.join(" / ") : "";
  const daemonStatus = inventory.daemon.error?.code === "permission_denied" ? "Permission denied" : inventory.daemon.error ? "Unavailable" : inventory.daemon.loading ? "Checking…" : "Connected";
  const daemonTone = inventory.daemon.error ? "failed" : inventory.daemon.loading ? "checking" : "";
  const versions = versionInfo(connection.data?.version, connection.data?.daemonVersion);
  // Why the Manage side of the switch shows a dot; empty means no dot.
  const attention = manageAttention({ daemonError: inventory.daemon.error, versionMismatch: versions.mismatch, reports: reports.data, seenId });
  // The breadcrumb root names the view: Workbench pages start at the grid, Manage pages at the Overview.
  const crumbRoot = view === "workbench" ? <a href={homeLink}>Workbench</a> : <a href={overviewLink}>Overview</a>;
  const shellStyle = { "--sidebar-width": `${sidebar.width}px` } as CSSProperties;
  const pageTitle = heading ? `${heading} · kukeon` : "kukeon";
  useEffect(() => { document.title = pageTitle; }, [pageTitle]);
  // A route change moves focus to the new page's h1 so keyboard and screen-reader users start at the
  // new content. Not on first load, not when only ?document=1 toggles, and not while a form control
  // drives the route (the Images realm select). The terminal view focuses its terminal itself.
  const routeKey = JSON.stringify({ ...route, document: undefined });
  const shownRoute = useRef(routeKey);
  useEffect(() => {
    if (shownRoute.current === routeKey) return;
    shownRoute.current = routeKey;
    if (isWorkspace || document.activeElement?.matches("input, select, textarea")) return;
    document.querySelector<HTMLElement>("main .page-heading h1")?.focus();
  }, [routeKey, isWorkspace]);
  return <AskProvider value={dock.ask}><div className={`app-shell${cellScreenScope ? " cell-screen" : ""}${cellTerminalActive ? " cell-screen-active" : ""}${sidebar.collapsed ? " sidebar-collapsed" : ""}${dock.open ? " majordomo-open" : ""}${dock.open && dock.overlay ? " majordomo-overlay" : ""}`} style={shellStyle}>
    <a className="skip-link" href="#main-content" onClick={event => { event.preventDefault(); document.getElementById("main-content")?.focus(); }}>Skip to content</a>
    <aside className={`sidebar ${menuOpen ? "is-open" : ""}`} id="cell-sidebar" aria-label={view === "workbench" ? "Cell sidebar" : view === "settings" ? "Settings sidebar" : "Manage sidebar"}>
      <button ref={sidebarClose} className="sidebar-close" aria-label="Close sidebar" onClick={closeMenu}>×</button>
      <div className="sidebar-panel">
        <div className="sidebar-header">
          <a className="brand" href={homeLink}><BrandMark /><span className="brand-title">kukeon</span></a>
          <SidebarToggle collapsed={false} onToggle={() => sidebar.setCollapsed(true)} />
        </div>
        <MajordomoSidebarEntry major={major} dock={dock} onChanged={refresh} />
        {view === "workbench" ? <CellSidebar inventory={inventory} selectedScope={cellScreenScope ?? []} terminalStates={terminals.cellStates} filter={sidebarFilter} listStyle={settings.cellListStyle} showSearch={settings.showSidebarSearch} showGroupBy={settings.showSidebarGroupBy} showScope={settings.showCellScope} showTerminalStatus={settings.showTerminalStatus} unread={unread} showUnreadBytes={settings.showUnreadBytes} /> : view === "settings" ? <SettingsNav onNavigate={() => setMenuOpen(false)} /> : <ManageNav route={route} />}
        <section className="connection" aria-label="Daemon connection">
          <div className="connection-heading">
            <p className="status" role="status"><span className={`dot ${daemonTone}`} aria-hidden="true" />{daemonStatus}</p>
            <a className="connection-action" href={daemonLink()} aria-label="Daemon details" title="Daemon details"><Icon name="info" /></a>
          </div>
          <code>{connection.data?.endpoint ? <SocketPath path={connection.data.endpoint} /> : connection.loading ? "Loading socket…" : "Socket unknown"}</code>
          {connection.data && <VersionRows info={versions} />}
          {connection.error && <ErrorNotice error={connection.error} />}
        </section>
      </div>
      {sidebar.collapsed && <SidebarRail view={view} listStyle={settings.cellListStyle} unread={unread} showUnreadBytes={settings.showUnreadBytes} inventory={inventory} selectedScope={cellScreenScope ?? []} connection={{ status: daemonStatus, tone: daemonTone, info: versions }} onExpand={() => sidebar.setCollapsed(false)} majordomo={<MajordomoRailButton major={major} dock={dock} />} />}
      {!sidebar.collapsed && <SidebarResizer width={sidebar.width} onResize={sidebar.setWidth} />}
    </aside>
    <div className="sidebar-scrim" aria-hidden="true" hidden={!menuOpen} onClick={closeMenu} />
    {/* While the phone drawer is open, the page behind it leaves the Tab order and the accessibility tree. */}
    <main className="workspace" id="main-content" tabIndex={-1} inert={menuOpen && mobile}>
      <header className="toolbar">
        <button ref={menuToggle} className="menu-toggle" aria-label="Toggle sidebar" aria-expanded={menuOpen} aria-controls="cell-sidebar" onClick={() => setMenuOpen(!menuOpen)}>☰</button>
        <span className="toolbar-connectivity" role="status" aria-label={`Daemon: ${daemonStatus}`} title={`Daemon: ${daemonStatus}`}><span className={`dot ${daemonTone}`} aria-hidden="true" /></span>
        <InventoryIndicator inventory={inventory} />
        <nav className="view-switch" aria-label="View">
          <a href={homeLink} aria-current={view === "workbench" ? "true" : undefined}><Icon name="terminal" /><span className="view-switch-label">Workbench</span></a>
          <a href={overviewLink} aria-current={view === "manage" ? "true" : undefined} aria-label={attention.length ? `Manage: ${attention.join("; ")}` : undefined} title={attention.length ? attention.join("; ") : undefined}><Icon name="manage" /><span className="view-switch-label">Manage</span>{attention.length > 0 && <span className="view-switch-dot" aria-hidden="true" />}</a>
        </nav>
        <nav className="breadcrumbs" aria-label="Resource scope">{route.view === "settings" ? <><a href={settingsLink()}>Settings</a><span>Profile</span></> : isWorkspace || route.view === "resource" ? <>{crumbRoot}{route.scope.map((name, i) => <a key={i} aria-label={`${kinds[i]}: ${name}`} href={resourceLink(route.scope.slice(0, i + 1))}>{name}</a>)}</> : <>{crumbRoot}{connectorsView ? route.scope[0] ? <><a href={connectorsLink()}>Connectors</a>{identityAction ? <><a href={connectorsLink(route.scope[0], route.target ? { realm: route.target[0], space: route.target[1], stack: route.target[2] } : undefined)}>{connectorHeading}</a><span>{identityAction}</span></> : <span>{route.scope[0]}</span>}</> : <span>Connectors</span> : teamsView ? route.scope[0] ? <><a href={teamsLink()}>Teams</a>{teamAction ? <><a href={teamsLink(route.scope[0])}>{route.scope[0]}</a><span>{teamAction}</span></> : <span>{route.scope[0]}</span>}</> : <span>Teams</span> : route.view === "new-team" ? <><a href={teamsLink()}>Teams</a><span>New team</span></> : sourcesView ? route.scope[0] ? <><a href={sourcesLink()}>Sources</a><span>{route.scope[0]}</span></> : <span>Sources</span> : route.view === "health" ? <><a href={daemonLink()}>Daemon</a><span>Health</span></> : daemonView ? route.view === "daemon" ? <span>Daemon</span> : <><a href={daemonLink()}>Daemon</a><span>Logs</span></> : images ? <><a href={imagesLink()}>Images</a>{route.scope[0] && (route.scope[1] ? <a href={imagesLink(route.scope[0])}>{route.scope[0]}</a> : <span>{route.scope[0]}</span>)}{route.scope[1] && <span>{route.scope[1]}</span>}</> : catalog ? <><a href={`#/${route.view}`}>{catalogTitles[route.view]}</a>{route.scope.slice(0, -1).map((name, i) => <a key={i} href={resourceLink(route.scope.slice(0, i + 1))}>{name}</a>)}{route.scope.length > 0 && <span>{route.scope.at(-1)}</span>}</> : route.view !== "dashboard" && route.view !== "workbench" && <span>{heading}</span>}</>}</nav>
        <div className="toolbar-actions">
          <a className="header-action new-cell-toolbar-action" href={newCellLink()}>New Cell</a>
          <MajordomoToolbarButton dock={dock} />
          <button className="icon-button refresh-button" type="button" aria-label="Refresh" title={inventory.loading ? "Refreshing…" : "Refresh"} aria-busy={inventory.loading} onClick={() => setRevision(value => value + 1)}>
            <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M20 11a8.1 8.1 0 0 0-14.9-4L3 10" /><path d="M3 4v6h6" /><path d="M4 13a8.1 8.1 0 0 0 14.9 4L21 14" /><path d="M21 20v-6h-6" /></svg>
          </button>
          <UserMenu user={user} onGetStarted={() => setSetting("showGetStarted", true)} onSignOut={onSignOut} />
        </div>
      </header>
      <div className={`workspace-content ${isWorkspace ? "cell-workspace-content" : ""}${cellTerminalActive ? " cell-screen-content" : ""}`}>
        {!cellScreenScope && <div className="page-heading"><h1 tabIndex={-1}>{heading}</h1>{headingContext && <p className="page-context mono-id">{headingContext}</p>}{route.view === "workbench" && <a className="header-action page-heading-action" href={newAgentLink()}>New agent</a>}</div>}
        <CellDetailsScreen title={heading ?? "Cell"} view={cellView} scope={cellScreenScope} revision={revision} inventory={inventory} manager={terminals} theme={theme} operations={operations} onLifecycleAction={runCellLifecycle} dockedCells={dockedCells} onOpenDock={() => dock.setOpen(true)} onChanged={() => setRevision(value => value + 1)} />
        {isWorkspace ? null : route.view === "settings" ? <SettingsPage inventory={inventory} /> : route.view === "new-cell" ? <Suspense fallback={<p role="status">Loading Cell creation form…</p>}><NewCellPage key={JSON.stringify(route.config ?? [])} revision={revision} inventory={inventory} configScope={route.config} onCreated={handleCellCreated} /></Suspense> : route.view === "new-agent" ? <Suspense fallback={<p role="status">Loading the New agent page…</p>}><NewAgentPage revision={revision} onCreated={handleAgentCreated} onChanged={refresh} /></Suspense> : route.view === "new-config" ? <Suspense fallback={<p role="status">Loading Config creation form…</p>}><NewConfigPage key={JSON.stringify(route.blueprint ?? [])} revision={revision} inventory={inventory} blueprintScope={route.blueprint} onCreated={() => setRevision(value => value + 1)} /></Suspense> : route.view === "new-secret" ? <NewSecretPage inventory={inventory} onChanged={() => setRevision(value => value + 1)} /> : connectorsView ? <Suspense fallback={<p role="status">Loading connectors…</p>}><ConnectorsPage name={route.scope[0]} target={route.target} action={route.action} identity={route.identity} revision={revision} inventory={inventory} onChanged={() => setRevision(value => value + 1)} onTitle={setConnectorTitle} /></Suspense> : teamsView ? <Suspense fallback={<p role="status">Loading teams…</p>}><TeamsPage name={route.scope[0]} section={route.action} revision={revision} onChanged={() => setRevision(value => value + 1)} /></Suspense> : route.view === "new-team" ? <Suspense fallback={<p role="status">Loading the team wizard…</p>}><NewTeamPage key={newTeamLink} onChanged={() => setRevision(value => value + 1)} /></Suspense> : sourcesView ? <Suspense fallback={<p role="status">Loading sources…</p>}><SourcesPage name={route.scope[0]} revision={revision} onChanged={() => setRevision(value => value + 1)} /></Suspense> : route.view === "health" ? <HealthPage revision={revision} major={major} inventory={inventory} onChanged={() => setRevision(value => value + 1)} onSeen={markReportSeen} /> : route.view === "daemon" ? <DaemonHealthPage revision={revision} inventory={inventory} /> : route.view === "daemon-logs" ? <DaemonLogsPage revision={revision} /> : images ? <ImagesPage scope={route.scope} revision={revision} /> : catalog ? <CatalogPage kind={route.view} scope={route.scope} document={route.document} edit={!!route.edit} revision={revision} inventory={inventory} onChanged={() => setRevision(value => value + 1)} /> : route.view === "resource" ? route.scope.length === 4 && !route.document ? null : <ResourceDetails key={JSON.stringify(route.scope)} route={route} revision={revision} /> : route.view === "workbench" ? <><GetStarted revision={revision} inventory={inventory} configs={major.configs.data} majordomoCells={major.cells.length} onOpenMajordomo={() => dock.setOpen(true)} onChanged={refresh} /><WorkbenchGrid inventory={inventory} filter={gridFilter} listStyle={settings.cellListStyle} showSearch={settings.showGridSearch} showGroupBy={settings.showGridGroupBy} dockedCells={dockedCells} onOpenDock={() => dock.setOpen(true)} terminalStates={terminals.cellStates} unread={unread} showUnreadBytes={settings.showUnreadBytes} /></> : depth >= 0 ? <ResourceList key={route.view + JSON.stringify(route.parent) + (route.state ?? "")} depth={depth} inventory={inventory} parent={route.parent} state={route.state} operations={operations} onLifecycleAction={runCellLifecycle} onParentAction={runParentAction} /> : <Dashboard inventory={inventory} reports={reports} />}
        <footer>Daemon inventory · {inventory.data ? <>Last refreshed <time dateTime={inventory.data.updatedAt}>{new Date(inventory.data.updatedAt).toLocaleTimeString()}</time></> : "Not refreshed yet"} · Refresh to retrieve the latest state.</footer>
      </div>
    </main>
    <MajordomoPanel major={major} dock={dock} inventory={inventory} manager={terminals} theme={theme} revision={revision} onChanged={() => setRevision(value => value + 1)} />
  </div></AskProvider>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
// Root shows the login screen until the session is known and valid (#1652), so no inventory,
// screenshot or terminal request runs before sign-in; signing out unmounts the app and with it
// every terminal connection. The theme applies to the login screen too.
function Root() {
  const [settings] = useSettings();
  const { session, retry, signedIn, signOut } = useSession();
  useEffect(() => { document.documentElement.dataset.theme = settings.theme; }, [settings.theme]);
  if (session.phase !== "signed-in") {
    return <Login checking={session.phase === "checking"} notice={session.phase === "unavailable" ? session.error : undefined} onRetry={() => void retry()} onSignedIn={signedIn} />;
  }
  return <NotificationProvider><App user={session.user} onSignOut={() => void signOut()} /></NotificationProvider>;
}

createRoot(root).render(<StrictMode><Root /></StrictMode>);
