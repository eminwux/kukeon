import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";
import { useResource, ErrorNotice, type Resource } from "./request";
import { CatalogPage, RelatedResources } from "./catalog";
import { AttachTerminal } from "./terminal";
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
function ResourceDetails({ route, revision, workspace = false }: { route: Route; revision: number; workspace?: boolean }) {
  const depth = route.scope.length - 1;
  const kind = kinds[depth];
  const detail = useResource<Doc>(apiPath(route.scope), revision);
  const containers = useResource<{ id: string; attachable?: boolean; root?: boolean }[]>(depth === 3 ? `${apiPath(route.scope)}/containers` : null, revision);
  const terminal = depth === 3 && detail.data && containers.data && !containers.error && <div id="cell-terminal"><AttachTerminal key={JSON.stringify(route.scope)} path={apiPath(route.scope)} containers={containers.data} /></div>;
  const status = detail.data && (depth === 3 || depth === 4) && !route.document && <StatusSummary status={detail.data.status} kind={kind} />;
  const overview = detail.data && <section className="panel resource-overview"><div className="panel-heading"><h2>{route.document ? "API document" : "Resource overview"}</h2><StateBadge state={detail.data.status?.state} /></div>{route.document ? <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(detail.data, null, 2)}</pre> : <div className="panel-body"><dl className="details-grid"><dt>Name</dt><dd>{detail.data.metadata.name}</dd><dt>Kind</dt><dd>{detail.data.kind ?? kind}</dd><dt>Scope</dt><dd>{route.scope.join(" / ")}</dd><dt>API version</dt><dd>{detail.data.apiVersion ?? "Not reported"}</dd><dt>Labels</dt><dd>{Object.entries(detail.data.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(detail.data.metadata.labels ?? {}).length && "No labels"}</dd></dl>{depth < 3 && <a className="text-link" href={listLink(depth + 1, route.scope)}>View {titles[depth + 1].toLowerCase()} in this {kind} →</a>}</div>}</section>;
  const related = detail.data && !route.document && <RelatedResources doc={detail.data} revision={revision} />;
  const containerPanel = depth === 3 && <section className="panel container-panel"><div className="panel-heading"><h2>Containers</h2><span className="badge">{detail.error ? "Stale observations" : detail.loading ? "Refreshing observations" : "Cell observations"}</span></div><div className="panel-body">{containers.loading && <p>Loading containers…</p>}{containers.error && <ErrorNotice error={containers.error} />}{containers.error && containers.data && <p className="stale">Stale container list — refresh failed.</p>}{!containers.loading && !containers.error && !containers.data?.length && <p>No containers found.</p>}<ul className="state-list">{containers.data?.map(container => <li key={container.id}><a className="text-link" href={resourceLink([...route.scope, container.id])}>{container.id}</a><StateBadge state={detail.data?.status?.containers?.find(item => item.name === container.id || item.id === container.id)?.state} /><a href={resourceLink([...route.scope, container.id], true)}>View document</a></li>)}</ul></div></section>;
  return <>
    <div className="detail-actions"><a href={workspace ? resourceLink(route.scope) : listLink(Math.min(depth, 3))}>{workspace ? "View in Administration" : `← Back to ${titles[Math.min(depth, 3)].toLowerCase()}`}</a><a className="document-action" href={resourceLink(route.scope, !route.document)}>{route.document ? "Show overview" : "View document"}</a></div>
    {detail.loading && <p role="status">Loading {kind} details…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={kind} />}
    {detail.error && detail.data && <p className="stale">Stale resource details — refresh failed.</p>}
    {workspace && <p className="description terminal-note">Switching cells closes the current terminal attachment. The cell keeps running.</p>}
    {workspace && terminal}
    {status}
    {overview}
    {related}
    {!workspace && !route.document && terminal}
    {containerPanel}
  </>;
}

type Theme = "system" | "light" | "dark";
function initialTheme(): Theme {
  try { const value = localStorage.getItem("kukeon-theme"); return value === "light" || value === "dark" ? value : "system"; }
  catch { return "system"; }
}
function AdministrationNavigation({ route }: { route: Route }) {
  const activeDepth = route.view === "resource" ? Math.min(route.scope.length - 1, 3) : titles.findIndex(title => title.toLowerCase() === route.view);
  return <nav className="administration-nav panel" aria-label="Administration resources">
    <a href="#/administration" aria-current={route.view === "dashboard" ? "page" : undefined}>Overview</a>
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
  const connection = useResource<{ endpoint: string }>("/api/connection", revision);
  const inventory = useInventory(revision);
  const isWorkspace = route.view === "workspace";
  const depth = titles.findIndex(title => title.toLowerCase() === route.view);
  const catalog = Object.keys(catalogTitles).includes(route.view);
  const heading = isWorkspace ? route.scope.at(-1) ?? "Workspace" : catalog ? route.scope.at(-1) ?? catalogTitles[route.view] : route.view === "resource" ? route.scope.at(-1) : depth >= 0 ? titles[depth] : "Administration";
  const daemonStatus = inventory.daemon.error?.code === "permission_denied" ? "Permission denied" : inventory.daemon.error ? "Unavailable" : inventory.daemon.loading ? "Checking…" : "Connected";
  return <div className="app-shell">
    <aside className={`sidebar ${menuOpen ? "is-open" : ""}`} id="cell-sidebar" aria-label="Cell sidebar">
      <button className="sidebar-close" aria-label="Close sidebar" onClick={() => setMenuOpen(false)}>×</button>
      <a className="brand" href={workspaceLink()}><span className="mark" aria-hidden="true">K</span><span>Kukeon<small>LOCAL WORKSPACE</small></span></a>
      <nav className="area-nav" aria-label="Areas"><a href={workspaceLink()} aria-current={isWorkspace ? "page" : undefined}>Workspace</a><a href="#/administration" aria-current={!isWorkspace ? "page" : undefined}>Administration</a></nav>
      <a className="new-cell-action sidebar-new-cell" href="#/configs">+ New Cell</a>
      <CellSidebar inventory={inventory} selectedScope={isWorkspace ? route.scope : []} />
      <section className="connection" aria-label="Daemon connection">
        <p className="status" role="status"><span className={`dot ${inventory.daemon.error ? "failed" : inventory.daemon.loading ? "checking" : ""}`} aria-hidden="true" />{daemonStatus}</p>
        <code>{connection.data?.endpoint ?? (connection.loading ? "Loading socket…" : "Socket unknown")}</code>
        {connection.error && <ErrorNotice error={connection.error} />}
        <label className="theme-control">Appearance<select aria-label="Appearance" value={theme} onChange={event => setTheme(event.target.value as Theme)}><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select></label>
      </section>
    </aside>
    <main className="workspace">
      <header className="toolbar">
        <button className="menu-toggle" aria-label="Toggle sidebar" aria-expanded={menuOpen} aria-controls="cell-sidebar" onClick={() => setMenuOpen(!menuOpen)}>☰</button>
        <nav className="toolbar-areas" aria-label="Areas"><a href={workspaceLink()} aria-current={isWorkspace ? "page" : undefined}>Workspace</a><a href="#/administration" aria-current={!isWorkspace ? "page" : undefined}>Administration</a></nav>
        <span className="toolbar-connectivity" role="status" aria-label={`Daemon: ${daemonStatus}`} title={`Daemon: ${daemonStatus}`}><span className={`dot ${inventory.daemon.error ? "failed" : inventory.daemon.loading ? "checking" : ""}`} aria-hidden="true" /></span>
        <nav className="breadcrumbs" aria-label="Resource scope"><a href={isWorkspace ? workspaceLink() : "#/administration"}>{isWorkspace ? "Workspace" : "Administration"}</a>{isWorkspace ? route.scope.length > 0 && <span>{route.scope.join(" / ")}</span> : catalog ? <><a href={`#/${route.view}`}>{catalogTitles[route.view]}</a>{route.scope.slice(0, -1).map((name, i) => <a key={i} href={resourceLink(route.scope.slice(0, i + 1))}>{name}</a>)}{route.scope.length > 0 && <span>{route.scope.at(-1)}</span>}</> : route.view === "resource" ? route.scope.map((name, i) => <a key={i} aria-label={`${kinds[i]}: ${name}`} href={resourceLink(route.scope.slice(0, i + 1))}>{name}</a>) : <span>{heading}</span>}</nav>
        <button className="refresh" onClick={() => setRevision(value => value + 1)}>Refresh</button>
      </header>
      <div className={`workspace-content ${isWorkspace ? "cell-workspace-content" : ""}`}>
        {!isWorkspace && <AdministrationNavigation route={route} />}
        <div className="page-heading"><p className="eyebrow">{isWorkspace ? "Workspace" : "Administration"}</p><h1>{heading}</h1><p className="description">{isWorkspace ? route.scope.length ? route.scope.join(" / ") : "Select a cell to work with its terminal and inspect its state." : route.view === "dashboard" ? "Your local infrastructure at a glance." : route.view === "resource" || (catalog && route.scope.length > 0) ? route.scope.join(" / ") : `Browse ${heading?.toLowerCase()} across your infrastructure.`}</p>{isWorkspace && <a className="new-cell-action mobile-new-cell" href="#/configs">+ New Cell</a>}</div>
        {inventory.loading && <p className="description" role="status">Refreshing resource inventory…</p>}
        {inventory.error && <ErrorNotice error={inventory.error} />}
        {inventory.error && inventory.data && <p className="stale">Stale inventory — refresh failed. Counts and lists may be out of date.</p>}
        {!!inventory.data?.failures.length && <div className="error" role="alert"><strong>Incomplete inventory</strong><p>Some scopes could not be loaded. Counts and lists exclude unavailable resources.</p><ul>{inventory.data.failures.map(message => <li key={message}>{message}</li>)}</ul></div>}
        {isWorkspace ? route.scope.length === 4 ? <ResourceDetails key={JSON.stringify(route.scope)} route={route} revision={revision} workspace /> : <WorkspaceLanding inventory={inventory} /> : catalog ? <CatalogPage kind={route.view} scope={route.scope} document={route.document} revision={revision} onCreated={() => setRevision(value => value + 1)} /> : route.view === "resource" ? <ResourceDetails key={JSON.stringify(route.scope)} route={route} revision={revision} /> : depth >= 0 ? <ResourceList key={route.view + JSON.stringify(route.parent)} depth={depth} inventory={inventory} parent={route.parent} /> : <Dashboard inventory={inventory} />}
        <footer>Daemon inventory · {inventory.data ? <>Last refreshed <time dateTime={inventory.data.updatedAt}>{new Date(inventory.data.updatedAt).toLocaleTimeString()}</time></> : "Not refreshed yet"} · Refresh to retrieve the latest state.</footer>
      </div>
    </main>
  </div>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
createRoot(root).render(<StrictMode><App /></StrictMode>);
