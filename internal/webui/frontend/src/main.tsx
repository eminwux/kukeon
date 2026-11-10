import { StrictMode, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";
import { useResource, ErrorNotice, type Resource } from "./request";
import { CatalogPage, RelatedResources } from "./catalog";
import { TerminalWorkspace, useTerminalSessions, type TerminalManager } from "./terminal";
import { CellSidebar } from "./cell-sidebar";
import { useInventory, type Inventory, type InventoryResource, type Doc, type RuntimeStatus } from "./inventory";
import { apiPath, catalogTitles, kinds, titles, readRoute, resourceLink, listLink, workspaceLink, type Route } from "./routes";
import { StateBadge } from "./status";

function validTime(value?: string): value is string {
  return !!value && !value.startsWith("0001-") && Number.isFinite(Date.parse(value));
}
function StatusSummary({ status, kind }: { status?: RuntimeStatus; kind: string }) {
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
      {(status?.outOfSync || status?.outOfSyncError) && <div className="error"><strong>{status.outOfSyncError ? "Configuration comparison failed" : "Configuration out of sync"}</strong><p>{status.outOfSyncError || status.outOfSyncReason || "The daemon reports a configuration mismatch."}</p></div>}
      {times.some(([, value]) => validTime(value)) && <dl className="status-times">{times.filter(([, value]) => validTime(value)).map(([label, value]) => <div key={label}><dt>{label}</dt><dd><time dateTime={value}>{new Date(value!).toLocaleString()}</time></dd></div>)}</dl>}
      {container && ([ ["Repository setup", status?.repos], ["Create stages", status?.stages] ] as const).map(([label, items]) => !!items?.length && <div className="setup-status" key={label}><h3>{label}</h3><ul className="state-list">{items.map((item, i) => <li key={i}><div><strong>{item.name ?? `Stage ${item.index ?? i}`}</strong>{item.error && <p className="setup-error">{item.error}</p>}</div><StateBadge state={item.state} /></li>)}</ul></div>)}
      {!status && <p className="description">The daemon has not reported status for this resource.</p>}
    </div>
  </section>;
}
function Dashboard({ inventory }: { inventory: Resource<Inventory> }) {
  const partial = inventory.error || inventory.data?.failures.length;
  const cells = inventory.data?.rows[3] ?? [];
  const states = new Map<string, number>();
  for (const row of cells) { const state = row.doc.status?.state || "Not reported"; states.set(state, (states.get(state) ?? 0) + 1); }
  return <>
    <div className="summary-grid">{titles.map((title, depth) => <a className="summary-card panel" key={title} href={listLink(depth)}><span>{title}</span><strong>{inventory.data ? inventory.data.rows[depth].length : "—"}</strong><small>{inventory.loading ? "Refreshing…" : partial ? "Incomplete or stale data" : "View all →"}</small></a>)}</div>
    <div className="dashboard-grid">
      <section className="panel"><div className="panel-heading"><h2>Cell states</h2><span className="badge">{partial ? "Incomplete" : "Reported by daemon"}</span></div><div className="panel-body">{states.size ? <ul className="state-list">{[...states].map(([state, count]) => <li key={state}><StateBadge state={state} /><strong>{count}</strong></li>)}</ul> : <p className="description">{inventory.loading ? "Loading cell states…" : partial ? "Cell states could not be fully loaded." : "No cells found."}</p>}</div></section>
      <section className="panel"><div className="panel-heading"><h2>Resource overview</h2></div><div className="panel-body"><p>Browse resources by type, across every scope.</p><p className="description">Use a list to find a resource, inspect its status, or narrow it to a realm. Complete API documents are available on demand.</p><a className="text-link" href={listLink(3)}>Explore cells →</a></div></section>
    </div>
  </>;
}
function ResourceList({ depth, inventory, parent }: { depth: number; inventory: Resource<Inventory>; parent: string[] }) {
  const [search, setSearch] = useState("");
  const [realm, setRealm] = useState("");
  const [page, setPage] = useState(0);
  const rows = inventory.data?.rows[depth] ?? [];
  const scoped = rows.filter(row => parent.every((name, i) => row.scope[i] === name));
  const filtered = scoped.filter(row => (!realm || row.scope[0] === realm) && row.scope.join(" / ").toLowerCase().includes(search.toLowerCase()));
  const pageSize = 25;
  const currentPage = Math.min(page, Math.max(0, Math.ceil(filtered.length / pageSize) - 1));
  return <section className="panel" aria-label={`${titles[depth]} list`}>
    <div className="list-controls"><label className="search-field">Search<input type="search" placeholder={`Search ${titles[depth].toLowerCase()}…`} value={search} onChange={e => { setSearch(e.target.value); setPage(0); }} /></label>{depth > 0 && <label>Realm<select aria-label="Filter by realm" value={realm} onChange={e => { setRealm(e.target.value); setPage(0); }}><option value="">All realms</option>{inventory.data?.rows[0].map(row => <option key={row.scope[0]}>{row.scope[0]}</option>)}</select></label>}<span className="result-count">{filtered.length} results</span></div>
    {parent.length > 0 && <p className="scope-filter">Scope: {parent.join(" / ")} <a href={listLink(depth)}>Clear scope</a></p>}
    <div className="table-scroll"><table><thead><tr><th>Name</th><th>State</th><th>Scope</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{filtered.slice(currentPage * pageSize, (currentPage + 1) * pageSize).map(row => <tr key={JSON.stringify(row.scope)}><td><a className="resource-name" href={resourceLink(row.scope)}>{row.doc.metadata.name}</a></td><td><StateBadge state={row.doc.status?.state} /></td><td className="scope-cell">{row.scope.slice(0, -1).join(" / ") || "Workspace"}</td><td><a className="document-action" href={resourceLink(row.scope, true)} aria-label={`View document for ${row.scope.join(" / ")}`}>View document</a></td></tr>)}</tbody></table></div>
    {!filtered.length && <div className="empty-state"><h3>{inventory.loading ? "Loading resources…" : inventory.error || inventory.data?.failures.length ? "Resources could not be fully loaded" : scoped.length ? "No matching resources" : `No ${titles[depth].toLowerCase()} found`}</h3><p>{search || realm ? "Try another search or clear the filters." : "Refresh to retrieve the latest resource state."}</p></div>}
    <div className="pagination"><span>{filtered.length ? `${currentPage * pageSize + 1}–${Math.min((currentPage + 1) * pageSize, filtered.length)} of ${filtered.length}` : "0 results"}</span><button disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>Previous</button><button disabled={(currentPage + 1) * pageSize >= filtered.length} onClick={() => setPage(currentPage + 1)}>Next</button></div>
  </section>;
}
function ResourceDetails({ route, revision }: { route: Route; revision: number }) {
  const depth = route.scope.length - 1;
  const kind = kinds[depth];
  const detail = useResource<Doc>(apiPath(route.scope), revision);
  const containers = useResource<{ id: string; attachable?: boolean; root?: boolean }[]>(depth === 3 ? `${apiPath(route.scope)}/containers` : null, revision);
  const status = detail.data && (depth === 3 || depth === 4) && !route.document && <StatusSummary status={detail.data.status} kind={kind} />;
  const overview = detail.data && <section className="panel resource-overview"><div className="panel-heading"><h2>{route.document ? "API document" : "Resource overview"}</h2><StateBadge state={detail.data.status?.state} /></div>{route.document ? <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(detail.data, null, 2)}</pre> : <div className="panel-body"><dl className="details-grid"><dt>Name</dt><dd>{detail.data.metadata.name}</dd><dt>Kind</dt><dd>{detail.data.kind ?? kind}</dd><dt>Scope</dt><dd>{route.scope.join(" / ")}</dd><dt>API version</dt><dd>{detail.data.apiVersion ?? "Not reported"}</dd><dt>Labels</dt><dd>{Object.entries(detail.data.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(detail.data.metadata.labels ?? {}).length && "No labels"}</dd></dl>{depth < 3 && <a className="text-link" href={listLink(depth + 1, route.scope)}>View {titles[depth + 1].toLowerCase()} in this {kind} →</a>}</div>}</section>;
  const related = detail.data && !route.document && <RelatedResources doc={detail.data} revision={revision} />;
  const containerPanel = depth === 3 && <section className="panel container-panel"><div className="panel-heading"><h2>Containers</h2><span className="badge">{detail.error ? "Stale observations" : detail.loading ? "Refreshing observations" : "Cell observations"}</span></div><div className="panel-body">{containers.loading && <p>Loading containers…</p>}{containers.error && <ErrorNotice error={containers.error} />}{containers.error && containers.data && <p className="stale">Stale container list — refresh failed.</p>}{!containers.loading && !containers.error && !containers.data?.length && <p>No containers found.</p>}<ul className="state-list">{containers.data?.map(container => <li key={container.id}><a className="text-link" href={resourceLink([...route.scope, container.id])}>{container.id}</a><StateBadge state={detail.data?.status?.containers?.find(item => item.name === container.id || item.id === container.id)?.state} /><a href={resourceLink([...route.scope, container.id], true)}>View document</a></li>)}</ul></div></section>;
  return <>
    <div className="detail-actions"><a href={listLink(Math.min(depth, 3))}>← Back to {titles[Math.min(depth, 3)].toLowerCase()}</a><a className="document-action" href={resourceLink(route.scope, !route.document)}>{route.document ? "Show overview" : "View document"}</a></div>
    {detail.loading && <p role="status">Loading {kind} details…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={kind} />}
    {detail.error && detail.data && <p className="stale">Stale resource details — refresh failed.</p>}
    {status}
    {overview}
    {related}
    {containerPanel}
  </>;
}

type CellTab = "terminal" | "overview";
const cellTabs: { id: CellTab; label: string }[] = [
  { id: "terminal", label: "Terminal" },
  { id: "overview", label: "Overview" },
];

function CellDetailsScreen({ scope, revision, inventory, manager }: {
  scope: string[] | null;
  revision: number;
  inventory: InventoryResource;
  manager: TerminalManager;
}) {
  const [activeTab, setActiveTab] = useState<CellTab>("terminal");
  const [terminalExpanded, setTerminalExpanded] = useState(false);
  const [terminalToolsHost, setTerminalToolsHost] = useState<HTMLDivElement | null>(null);
  const tablist = useRef<HTMLDivElement>(null);
  const scopeKey = JSON.stringify(scope ?? []);
  const detail = useResource<Doc>(scope ? apiPath(scope) : null, revision);
  const containers = useResource<{ id: string; attachable?: boolean; root?: boolean }[]>(scope ? `${apiPath(scope)}/containers` : null, revision);
  const status = detail.data && <StatusSummary status={detail.data.status} kind="cell" />;

  useEffect(() => { setActiveTab("terminal"); setTerminalExpanded(false); }, [scopeKey]);

  function moveTab(event: ReactKeyboardEvent<HTMLDivElement>) {
    const index = cellTabs.findIndex(tab => tab.id === activeTab);
    const next = event.key === "ArrowRight" ? (index + 1) % cellTabs.length
      : event.key === "ArrowLeft" ? (index - 1 + cellTabs.length) % cellTabs.length
      : event.key === "Home" ? 0 : event.key === "End" ? cellTabs.length - 1 : -1;
    if (next < 0) return;
    event.preventDefault();
    setActiveTab(cellTabs[next].id);
    tablist.current?.querySelectorAll<HTMLButtonElement>("[role=tab]")[next]?.focus();
  }

  const overview = detail.data && scope && <section className="panel resource-overview">
    <div className="panel-heading"><h2>Resource overview</h2><div className="cell-overview-actions"><StateBadge state={detail.data.status?.state} /><a className="document-action" href={resourceLink(scope, true)} target="_blank" rel="noopener noreferrer">View document</a></div></div>
    <div className="panel-body"><dl className="details-grid">
      <dt>Name</dt><dd>{detail.data.metadata.name}</dd>
      <dt>Kind</dt><dd>{detail.data.kind ?? "cell"}</dd>
      <dt>Scope</dt><dd>{scope.join(" / ")}</dd>
      <dt>API version</dt><dd>{detail.data.apiVersion ?? "Not reported"}</dd>
      <dt>Labels</dt><dd>{Object.entries(detail.data.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(detail.data.metadata.labels ?? {}).length && "No labels"}</dd>
    </dl></div>
  </section>;

  const containerPanel = <section className="panel container-panel">
    <div className="panel-heading"><h2>Containers</h2><span className="badge">{detail.error ? "Stale observations" : detail.loading ? "Refreshing observations" : "Cell observations"}</span></div>
    <div className="panel-body">
      {containers.loading && <p>Loading containers…</p>}
      {containers.error && <ErrorNotice error={containers.error} />}
      {containers.error && containers.data && <p className="stale">Stale container list — refresh failed.</p>}
      {!containers.loading && !containers.error && !containers.data?.length && <p>No containers found.</p>}
      <ul className="state-list">{containers.data?.map(container => <li key={container.id}><a className="text-link" href={resourceLink([...(scope ?? []), container.id])}>{container.id}</a><StateBadge state={detail.data?.status?.containers?.find(item => item.name === container.id || item.id === container.id)?.state} /><a href={resourceLink([...(scope ?? []), container.id], true)} target="_blank" rel="noopener noreferrer">View document</a></li>)}</ul>
    </div>
  </section>;

  return <div className="cell-detail-screen" hidden={!scope}>
    {detail.loading && scope && <p role="status">Loading cell details…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind="Cell" />}
    {detail.error && detail.data && <p className="stale">Stale resource details — refresh failed.</p>}
    <div className={`cell-detail-tabbar${terminalExpanded ? " is-terminal-expanded" : ""}`}>
      <div className="cell-detail-tabs" role="tablist" aria-label="Cell details" ref={tablist} onKeyDown={moveTab}>
        {cellTabs.map(tab => <button key={tab.id} id={`cell-tab-${tab.id}`} role="tab" type="button" aria-selected={activeTab === tab.id} aria-controls={`cell-panel-${tab.id}`} tabIndex={activeTab === tab.id ? 0 : -1} onClick={() => setActiveTab(tab.id)}>{tab.label}</button>)}
      </div>
      <div className="cell-terminal-tools" ref={setTerminalToolsHost} hidden={!scope || activeTab !== "terminal"} />
    </div>
    <section className="cell-tab-panel cell-terminal-panel" id="cell-panel-terminal" role="tabpanel" aria-labelledby="cell-tab-terminal" tabIndex={0} hidden={activeTab !== "terminal"}>
      <TerminalWorkspace scope={scope} revision={revision} inventory={inventory} manager={manager} visible={activeTab === "terminal" && !!scope} controlsHost={terminalToolsHost} onExpandedChange={setTerminalExpanded} />
    </section>
    <section className="cell-tab-panel cell-overview-content" id="cell-panel-overview" role="tabpanel" aria-labelledby="cell-tab-overview" tabIndex={0} hidden={activeTab !== "overview"}>{overview}{status}{containerPanel}</section>
  </div>;
}

type Theme = "system" | "light" | "dark";
function initialTheme(): Theme {
  try { const value = localStorage.getItem("kukeon-theme"); return value === "light" || value === "dark" ? value : "system"; }
  catch { return "system"; }
}
function ResourcesNavigation({ route }: { route: Route }) {
  const activeDepth = route.view === "resource" ? Math.min(route.scope.length - 1, 3) : titles.findIndex(title => title.toLowerCase() === route.view);
  return <nav className="resources-nav panel" aria-label="Resources navigation">
    <a href="#/resources" aria-current={route.view === "dashboard" ? "page" : undefined}>Overview</a>
    {titles.map((title, depth) => <a key={title} href={listLink(depth)} aria-current={activeDepth === depth ? "page" : undefined}>{title}</a>)}
    {Object.entries(catalogTitles).map(([kind, title]) => <a key={kind} href={`#/${kind}`} aria-current={route.view === kind ? "page" : undefined}>{title}</a>)}
  </nav>;
}
function WorkspaceLanding({ inventory }: { inventory: InventoryResource }) {
  const count = inventory.data?.rows[3].length ?? 0;
  return <section className="panel workspace-landing" aria-label="Workspace welcome">
    <h2>{count ? "Choose a cell" : inventory.loading ? "Loading cells…" : "No cells available"}</h2>
    <p>{count ? "Search or select a cell in the sidebar to open its terminal, status, and containers." : inventory.error || inventory.data?.failures.length ? "The inventory is incomplete. Refresh to retry unavailable scopes." : "Create a cell from an existing config or refresh the inventory."}</p>
    <a className="new-cell-action" href="#/configs">New Cell <span aria-hidden="true">→</span></a>
  </section>;
}
function App() {
  const [revision, setRevision] = useState(0);
  const [route, setRoute] = useState(readRoute);
  const [theme, setTheme] = useState<Theme>(initialTheme);
  const nextTheme: Theme = theme === "system" ? "light" : theme === "light" ? "dark" : "system";
  const [menuOpen, setMenuOpen] = useState(false);
  useEffect(() => {
    const changed = () => { setRoute(readRoute()); setMenuOpen(false); };
    const keydown = (event: KeyboardEvent) => { if (event.key === "Escape") setMenuOpen(false); };
    window.addEventListener("hashchange", changed);
    window.addEventListener("keydown", keydown);
    return () => { window.removeEventListener("hashchange", changed); window.removeEventListener("keydown", keydown); };
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try { localStorage.setItem("kukeon-theme", theme); } catch { /* Preferences are optional. */ }
  }, [theme]);
  const connection = useResource<{ endpoint: string; version: string }>("/api/connection", revision);
  const inventory = useInventory(revision);
  const terminals = useTerminalSessions(inventory);
  const isWorkspace = route.view === "workspace";
  const cellScreenScope = (isWorkspace || route.view === "resource" && !route.document) && route.scope.length === 4 ? route.scope : null;
  const depth = titles.findIndex(title => title.toLowerCase() === route.view);
  const catalog = Object.keys(catalogTitles).includes(route.view);
  const heading = isWorkspace ? route.scope.at(-1) ?? "Workspace" : catalog ? route.scope.at(-1) ?? catalogTitles[route.view] : route.view === "resource" ? route.scope.at(-1) : depth >= 0 ? titles[depth] : "Resources";
  const daemonStatus = inventory.daemon.error?.code === "permission_denied" ? "Permission denied" : inventory.daemon.error ? "Unavailable" : inventory.daemon.loading ? "Checking…" : "Connected";
  return <div className={`app-shell${cellScreenScope ? " cell-screen-active" : ""}`}>
    <aside className={`sidebar ${menuOpen ? "is-open" : ""}`} id="cell-sidebar" aria-label="Cell sidebar">
      <button className="sidebar-close" aria-label="Close sidebar" onClick={() => setMenuOpen(false)}>×</button>
      <a className="brand" href={workspaceLink()}><span className="mark" aria-hidden="true">K</span><span className="brand-title">Kukeon<small>LOCAL WORKSPACE</small></span>{connection.data?.version && <span className="brand-version">{connection.data.version.startsWith("v") ? connection.data.version : `v${connection.data.version}`}</span>}</a>
      <nav className="area-nav" aria-label="Areas"><a href={workspaceLink()} aria-current={isWorkspace ? "page" : undefined}>Workspace</a></nav>
      {!cellScreenScope && <a className="new-cell-action sidebar-new-cell" href="#/configs">+ New Cell</a>}
      <CellSidebar inventory={inventory} selectedScope={cellScreenScope ?? []} terminalStates={terminals.cellStates} />
      <section className="connection" aria-label="Daemon connection">
        <p className="status" role="status"><span className={`dot ${inventory.daemon.error ? "failed" : inventory.daemon.loading ? "checking" : ""}`} aria-hidden="true" />{daemonStatus}</p>
        <code>{connection.data?.endpoint ?? (connection.loading ? "Loading socket…" : "Socket unknown")}</code>
        {connection.error && <ErrorNotice error={connection.error} />}
      </section>
    </aside>
    <main className="workspace">
      <header className="toolbar">
        <button className="menu-toggle" aria-label="Toggle sidebar" aria-expanded={menuOpen} aria-controls="cell-sidebar" onClick={() => setMenuOpen(!menuOpen)}>☰</button>
        <nav className="toolbar-areas" aria-label="Areas"><a href={workspaceLink()} aria-current={isWorkspace ? "page" : undefined}>Workspace</a></nav>
        <span className="toolbar-connectivity" role="status" aria-label={`Daemon: ${daemonStatus}`} title={`Daemon: ${daemonStatus}`}><span className={`dot ${inventory.daemon.error ? "failed" : inventory.daemon.loading ? "checking" : ""}`} aria-hidden="true" /></span>
        <nav className="breadcrumbs" aria-label="Resource scope"><a href={isWorkspace ? workspaceLink() : "#/resources"}>{isWorkspace ? "Workspace" : "Resources"}</a>{isWorkspace ? route.scope.length > 0 && <span>{route.scope.join(" / ")}</span> : catalog ? <><a href={`#/${route.view}`}>{catalogTitles[route.view]}</a>{route.scope.slice(0, -1).map((name, i) => <a key={i} href={resourceLink(route.scope.slice(0, i + 1))}>{name}</a>)}{route.scope.length > 0 && <span>{route.scope.at(-1)}</span>}</> : route.view === "resource" ? route.scope.map((name, i) => <a key={i} aria-label={`${kinds[i]}: ${name}`} href={resourceLink(route.scope.slice(0, i + 1))}>{name}</a>) : <span>{heading}</span>}</nav>
        <div className="toolbar-actions">
          <a className="header-action" href="#/resources" aria-current={!isWorkspace ? "page" : undefined}>Resources</a>
          <button className="icon-button refresh-button" type="button" aria-label="Refresh" title="Refresh" onClick={() => setRevision(value => value + 1)}>
            <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M20 11a8.1 8.1 0 0 0-14.9-4L3 10" /><path d="M3 4v6h6" /><path d="M4 13a8.1 8.1 0 0 0 14.9 4L21 14" /><path d="M21 20v-6h-6" /></svg>
          </button>
          <button className="icon-button" type="button" aria-label={`Appearance: ${theme}; switch to ${nextTheme}`} title={`Appearance: ${theme} (switch to ${nextTheme})`} onClick={() => setTheme(nextTheme)}>
            <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
              {theme === "system" ? <><rect x="3.5" y="4.5" width="17" height="12" rx="2" /><path d="M8 20h8m-4-3.5V20" /></> : theme === "light" ? <><circle cx="12" cy="12" r="4" /><path d="M12 2v2m0 16v2M4.93 4.93l1.42 1.42m11.3 11.3 1.42 1.42M2 12h2m16 0h2M4.93 19.07l1.42-1.42m11.3-11.3 1.42-1.42" /></> : <path d="M20.1 15.7A8 8 0 0 1 8.3 3.9 8.2 8.2 0 1 0 20.1 15.7Z" />}
            </svg>
          </button>
        </div>
      </header>
      <div className={`workspace-content ${isWorkspace ? "cell-workspace-content" : ""}${cellScreenScope ? " cell-screen-content" : ""}`}>
        {!isWorkspace && !cellScreenScope && <ResourcesNavigation route={route} />}
        <div className="page-heading">{!cellScreenScope && <p className="eyebrow">{isWorkspace ? "Workspace" : "Resources"}</p>}<h1>{heading}</h1>{!cellScreenScope && <p className="description">{isWorkspace ? route.scope.length ? route.scope.join(" / ") : "Select a cell to work with its terminal and inspect its state." : route.view === "dashboard" ? "Browse and manage resources on this server." : route.view === "resource" || (catalog && route.scope.length > 0) ? route.scope.join(" / ") : `Browse ${heading?.toLowerCase()} across your infrastructure.`}</p>}{isWorkspace && !cellScreenScope && <a className="new-cell-action mobile-new-cell" href="#/configs">+ New Cell</a>}</div>
        {inventory.loading && <p className="description" role="status">Refreshing resource inventory…</p>}
        {inventory.error && <ErrorNotice error={inventory.error} />}
        {inventory.error && inventory.data && <p className="stale">Stale inventory — refresh failed. Counts and lists may be out of date.</p>}
        {!!inventory.data?.failures.length && <div className="error" role="alert"><strong>Incomplete inventory</strong><p>Some scopes could not be loaded. Counts and lists exclude unavailable resources.</p><ul>{inventory.data.failures.map(message => <li key={message}>{message}</li>)}</ul></div>}
        <CellDetailsScreen scope={cellScreenScope} revision={revision} inventory={inventory} manager={terminals} />
        {isWorkspace ? route.scope.length === 4 ? null : <WorkspaceLanding inventory={inventory} /> : catalog ? <CatalogPage kind={route.view} scope={route.scope} document={route.document} revision={revision} onCreated={() => setRevision(value => value + 1)} /> : route.view === "resource" ? route.scope.length === 4 && !route.document ? null : <ResourceDetails key={JSON.stringify(route.scope)} route={route} revision={revision} /> : depth >= 0 ? <ResourceList key={route.view + JSON.stringify(route.parent)} depth={depth} inventory={inventory} parent={route.parent} /> : <Dashboard inventory={inventory} />}
        <footer>Daemon inventory · {inventory.data ? <>Last refreshed <time dateTime={inventory.data.updatedAt}>{new Date(inventory.data.updatedAt).toLocaleTimeString()}</time></> : "Not refreshed yet"} · Refresh to retrieve the latest state.</footer>
      </div>
    </main>
  </div>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
createRoot(root).render(<StrictMode><App /></StrictMode>);
