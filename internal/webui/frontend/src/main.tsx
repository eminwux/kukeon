import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";
import { useResource, ErrorNotice, type Resource, type Failure } from "./request";
import { CatalogPage, catalogTitles, RelatedResources } from "./catalog";

type SetupStatus = { name?: string; index?: number; state: string; error?: string };
type RuntimeStatus = {
  state?: string; name?: string; id?: string; cgroupReady?: boolean;
  reason?: string; message?: string; outOfSync?: boolean; outOfSyncReason?: string; outOfSyncError?: string;
  restartCount?: number; exitCode?: number; exitSignal?: string;
  createdAt?: string; updatedAt?: string; startTime?: string; finishTime?: string; restartTime?: string;
  containers?: RuntimeStatus[]; repos?: SetupStatus[]; stages?: SetupStatus[];
};
type Doc = { metadata: { name: string; labels?: Record<string, string> }; status?: RuntimeStatus; apiVersion?: string; kind?: string; [key: string]: unknown };
const kinds = ["realm", "space", "stack", "cell", "container"];
const titles = ["Realms", "Spaces", "Stacks", "Cells"];
type Row = { doc: Doc; scope: string[] };
type Inventory = { rows: Row[][]; failures: string[]; updatedAt: string };
type Route = { view: string; scope: string[]; parent: string[]; document: boolean };
function readRoute(): Route {
  try {
    const raw = window.location.hash.slice(1);
    const [path, query] = raw.split("?");
    const params = new URLSearchParams(query);
    if (!path || path === "/dashboard") return { view: "dashboard", scope: [], parent: [], document: false };
    const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
    if (Object.keys(catalogTitles).includes(parts[0])) return { view: parts[0], scope: parts.slice(1, 6), parent: [], document: params.has("document") };
    if (parts[0] === "resource") return { view: "resource", scope: parts.slice(1, 6), parent: [], document: params.has("document") };
    if (titles.some(title => title.toLowerCase() === parts[0])) {
      const parent: unknown = JSON.parse(params.get("parent") ?? "[]");
      return { view: parts[0], scope: [], parent: Array.isArray(parent) && parent.every(p => typeof p === "string") ? parent.slice(0, 4) : [], document: false };
    }
    // Keep previously bookmarked hierarchy links working.
    return { view: "resource", scope: parts.slice(0, 5), parent: [], document: false };
  } catch { return { view: "dashboard", scope: [], parent: [], document: false }; }
}
function resourceLink(scope: string[], document = false): string {
  return "#/resource/" + scope.map(encodeURIComponent).join("/") + (document ? "?document=1" : "");
}
function listLink(depth: number, parent: string[] = []): string {
  return `#/${titles[depth].toLowerCase()}` + (parent.length ? `?parent=${encodeURIComponent(JSON.stringify(parent))}` : "");
}
function apiPath(scope: string[]): string {
  return "/api/" + scope.map((name, i) => `${kinds[i]}s/${encodeURIComponent(name)}`).join("/");
}
async function readJSON<T>(path: string, signal: AbortSignal): Promise<T> {
  const response = await fetch(path, { signal });
  const body = await response.json();
  if (!response.ok) throw body.error ?? { code: "unavailable", message: `HTTP ${response.status}` };
  return body;
}
// Limit fan-out across scopes so large workspaces do not overwhelm the daemon.
async function eachScope(rows: Row[], read: (row: Row) => Promise<void>) {
  let cursor = 0;
  await Promise.all(Array.from({ length: Math.min(6, rows.length) }, async () => {
    while (cursor < rows.length) await read(rows[cursor++]);
  }));
}
function useInventory(revision: number): Resource<Inventory> {
  const [state, setState] = useState<Resource<Inventory>>({ loading: true });
  useEffect(() => {
    const controller = new AbortController();
    setState(old => ({ data: old.data, loading: true }));
    void (async () => {
      try {
        const realms = await readJSON<Doc[]>("/api/realms", controller.signal);
        const result: Inventory = { rows: [realms.map(doc => ({ doc, scope: [doc.metadata.name] })), [], [], []], failures: [], updatedAt: "" };
        for (let depth = 1; depth < 4; depth++) {
          await eachScope(result.rows[depth - 1], async parent => {
            try {
              const docs = await readJSON<Doc[]>(`${apiPath(parent.scope)}/${kinds[depth]}s`, controller.signal);
              result.rows[depth].push(...docs.map(doc => ({ doc, scope: [...parent.scope, doc.metadata.name] })));
            } catch (error) {
              if (controller.signal.aborted) throw error;
              result.failures.push(`${titles[depth]} in ${parent.scope.join(" / ")}: ${(error as Failure).message ?? "Request failed"}`);
            }
          });
          result.rows[depth].sort((a, b) => a.scope.join("/").localeCompare(b.scope.join("/")));
        }
        if (controller.signal.aborted) return;
        result.updatedAt = new Date().toISOString();
        setState(old => result.failures.length && old.data
          ? { data: old.data, loading: false, error: { code: "partial", message: result.failures.join("; ") } }
          : { data: result, loading: false });
      } catch (error) {
        if (controller.signal.aborted) return;
        const failure = error as Failure;
        setState(old => ({ data: old.data, loading: false, error: { code: failure.code ?? "unavailable", message: failure.message ?? "Request failed" } }));
      }
    })();
    return () => controller.abort();
  }, [revision]);
  return state;
}
function StateBadge({ state }: { state?: string }) {
  const tone = ["Ready", "Running", "done", "cloned", "fetched"].includes(state ?? "") ? "success"
    : ["Failed", "Error", "failed"].includes(state ?? "") ? "danger"
    : ["Pending", "Creating", "Pausing", "Degraded", "Not ready"].includes(state ?? "") ? "warning" : "neutral";
  return <span className={`state-badge tone-${tone}`}><span className="state-dot" aria-hidden="true" />{state || "Not reported"}</span>;
}
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
      <section className="panel"><div className="panel-heading"><h2>Workspace overview</h2></div><div className="panel-body"><p>Browse resources by type, across every scope.</p><p className="description">Use a list to find a resource, inspect its status, or narrow it to a realm. Complete API documents are available on demand.</p><a className="text-link" href={listLink(3)}>Explore cells →</a></div></section>
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
  const containers = useResource<{ id: string }[]>(depth === 3 ? `${apiPath(route.scope)}/containers` : null, revision);
  return <>
    <div className="detail-actions"><a href={listLink(Math.min(depth, 3))}>← Back to {titles[Math.min(depth, 3)].toLowerCase()}</a><a className="document-action" href={resourceLink(route.scope, !route.document)}>{route.document ? "Show overview" : "View document"}</a></div>
    {detail.loading && <p role="status">Loading {kind} details…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={kind} />}
    {detail.error && detail.data && <p className="stale">Stale resource details — refresh failed.</p>}
    {detail.data && (depth === 3 || depth === 4) && !route.document && <StatusSummary status={detail.data.status} kind={kind} />}
    {detail.data && <section className="panel"><div className="panel-heading"><h2>{route.document ? "API document" : "Resource overview"}</h2><StateBadge state={detail.data.status?.state} /></div>{route.document ? <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(detail.data, null, 2)}</pre> : <div className="panel-body"><dl className="details-grid"><dt>Name</dt><dd>{detail.data.metadata.name}</dd><dt>Kind</dt><dd>{detail.data.kind ?? kind}</dd><dt>Scope</dt><dd>{route.scope.join(" / ")}</dd><dt>API version</dt><dd>{detail.data.apiVersion ?? "Not reported"}</dd><dt>Labels</dt><dd>{Object.entries(detail.data.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(detail.data.metadata.labels ?? {}).length && "No labels"}</dd></dl>{depth < 3 && <a className="text-link" href={listLink(depth + 1, route.scope)}>View {titles[depth + 1].toLowerCase()} in this {kind} →</a>}</div>}</section>}
    {detail.data && !route.document && <RelatedResources doc={detail.data} revision={revision} />}
    {depth === 3 && <section className="panel container-panel"><div className="panel-heading"><h2>Containers</h2><span className="badge">{detail.error ? "Stale observations" : detail.loading ? "Refreshing observations" : "Cell observations"}</span></div><div className="panel-body">{containers.loading && <p>Loading containers…</p>}{containers.error && <ErrorNotice error={containers.error} />}{containers.error && containers.data && <p className="stale">Stale container list — refresh failed.</p>}{!containers.loading && !containers.error && !containers.data?.length && <p>No containers found.</p>}<ul className="state-list">{containers.data?.map(container => <li key={container.id}><a className="text-link" href={resourceLink([...route.scope, container.id])}>{container.id}</a><StateBadge state={detail.data?.status?.containers?.find(item => item.name === container.id || item.id === container.id)?.state} /><a href={resourceLink([...route.scope, container.id], true)}>View document</a></li>)}</ul></div></section>}
  </>;
}

type Theme = "system" | "light" | "dark";
function initialTheme(): Theme {
  try { const value = localStorage.getItem("kukeon-theme"); return value === "light" || value === "dark" ? value : "system"; }
  catch { return "system"; }
}
function App() {
  const [revision, setRevision] = useState(0);
  const [route, setRoute] = useState(readRoute);
  const [theme, setTheme] = useState<Theme>(initialTheme);
  const [menuOpen, setMenuOpen] = useState(false);
  useEffect(() => {
    const changed = () => { setRoute(readRoute()); setMenuOpen(false); };
    window.addEventListener("hashchange", changed);
    return () => window.removeEventListener("hashchange", changed);
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try { localStorage.setItem("kukeon-theme", theme); } catch { /* Preferences are optional. */ }
  }, [theme]);
  const connection = useResource<{ endpoint: string }>("/api/connection", revision);
  const inventory = useInventory(revision);
  const depth = titles.findIndex(title => title.toLowerCase() === route.view);
  const activeDepth = route.view === "resource" ? Math.min(route.scope.length - 1, 3) : depth;
  const catalog = Object.keys(catalogTitles).includes(route.view);
  const heading = catalog ? (route.scope.at(-1) ?? catalogTitles[route.view]) : route.view === "resource" ? route.scope.at(-1) : depth >= 0 ? titles[depth] : "Dashboard";
  const status = inventory.loading ? "Refreshing…" : inventory.error || inventory.data?.failures.length ? "Needs attention" : "Connected";
  return <div className="app-shell">
    <aside className={`sidebar ${menuOpen ? "is-open" : ""}`} id="resource-sidebar" aria-label="Workspace sidebar">
      <button className="sidebar-close" aria-label="Close sidebar" onClick={() => setMenuOpen(false)}>×</button>
      <a className="brand" href="#"><span className="mark" aria-hidden="true">K</span><span>Kukeon<small>LOCAL WORKSPACE</small></span></a>
      <nav className="sidebar-scroll primary-nav" aria-label="Main navigation">
        <a className="resource-link" href="#/dashboard" aria-current={route.view === "dashboard" ? "page" : undefined}><span aria-hidden="true">▦</span> Dashboard</a>
        <p className="nav-caption">Resources</p>
        {titles.map((title, i) => <a key={title} className="resource-link" href={listLink(i)} aria-current={activeDepth === i ? "page" : undefined}><span aria-hidden="true">◇</span>{title}</a>)}
        <p className="nav-caption">Configuration &amp; storage</p>
        {Object.entries(catalogTitles).map(([kind, title]) => <a key={kind} className="resource-link" href={`#/${kind}`} aria-current={route.view === kind ? "page" : undefined}><span aria-hidden="true">◇</span>{title}</a>)}
      </nav>
      <section className="connection" aria-label="Daemon connection">
        <p className="status" role="status"><span className={`dot ${inventory.error || inventory.data?.failures.length ? "failed" : ""}`} aria-hidden="true" />{status}</p>
        <code>{connection.data?.endpoint ?? (connection.loading ? "Loading socket…" : "Socket unknown")}</code>
        {connection.error && <ErrorNotice error={connection.error} />}
        <label className="theme-control">Appearance<select aria-label="Appearance" value={theme} onChange={event => setTheme(event.target.value as Theme)}><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select></label>
      </section>
    </aside>
    <main className="workspace">
      <header className="toolbar">
        <button className="menu-toggle" aria-label="Toggle sidebar" aria-expanded={menuOpen} aria-controls="resource-sidebar" onClick={() => setMenuOpen(!menuOpen)}>☰</button>
        <nav aria-label="Resource scope"><a href="#/dashboard">Workspace</a>{catalog ? <><a href={`#/${route.view}`}>{catalogTitles[route.view]}</a>{route.scope.slice(0, -1).map((name, i) => <a key={i} href={resourceLink(route.scope.slice(0, i + 1))}>{name}</a>)}{route.scope.length > 0 && <span>{route.scope.at(-1)}</span>}</> : route.view === "resource" ? route.scope.map((name, i) => <a key={i} aria-label={`${kinds[i]}: ${name}`} href={resourceLink(route.scope.slice(0, i + 1))}>{name}</a>) : <span>{heading}</span>}</nav>
        <button className="refresh" onClick={() => setRevision(value => value + 1)}>Refresh</button>
      </header>
      <div className="workspace-content">
        <div className="page-heading"><p className="eyebrow">{route.view === "resource" || (catalog && route.scope.length > 0) ? "Resource details" : "Workspace"}</p><h1>{heading}</h1><p className="description">{route.view === "dashboard" ? "Your local infrastructure at a glance." : route.view === "resource" || (catalog && route.scope.length > 0) ? route.scope.join(" / ") : `Browse ${heading?.toLowerCase()} across your workspace.`}</p></div>
        {inventory.loading && <p className="description" role="status">Refreshing resource inventory…</p>}
        {inventory.error && <ErrorNotice error={inventory.error} />}
        {inventory.error && inventory.data && <p className="stale">Stale inventory — refresh failed. Counts and lists may be out of date.</p>}
        {!!inventory.data?.failures.length && <div className="error" role="alert"><strong>Incomplete inventory</strong><p>Some scopes could not be loaded. Counts and lists exclude unavailable resources.</p><ul>{inventory.data.failures.map(message => <li key={message}>{message}</li>)}</ul></div>}
        {catalog ? <CatalogPage kind={route.view} scope={route.scope} document={route.document} revision={revision} /> : route.view === "resource" ? <ResourceDetails key={JSON.stringify(route.scope)} route={route} revision={revision} /> : depth >= 0 ? <ResourceList key={route.view + JSON.stringify(route.parent)} depth={depth} inventory={inventory} parent={route.parent} /> : <Dashboard inventory={inventory} />}
        <footer>Daemon inventory · {inventory.data ? <>Last refreshed <time dateTime={inventory.data.updatedAt}>{new Date(inventory.data.updatedAt).toLocaleTimeString()}</time></> : "Not refreshed yet"} · Refresh to retrieve the latest state.</footer>
      </div>
    </main>
  </div>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
createRoot(root).render(<StrictMode><App /></StrictMode>);
