import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";

type Realm = { metadata: { name: string }; [key: string]: unknown };
type Failure = { code: string; message: string };
type Resource<T> = { data?: T; loading: boolean; error?: Failure };

// Abort replaced requests and ignore their results, including on realm switches.
function useResource<T>(path: string | null, revision: number): Resource<T> {
  const [state, setState] = useState<Resource<T>>({ loading: true });
  const [previousPath, setPreviousPath] = useState(path);
  // Clear the previous realm before painting a different selection.
  if (previousPath !== path) {
    setPreviousPath(path);
    setState({ loading: path !== null });
  }
  useEffect(() => {
    if (path === null) return;
    const controller = new AbortController();
    setState((old) => ({ data: old.data, loading: true }));
    void (async () => {
      try {
        const response = await fetch(path, { signal: controller.signal });
        const body = await response.json();
        if (!response.ok) throw body.error ?? { code: "http_error", message: `HTTP ${response.status}` };
        if (!controller.signal.aborted) setState({ data: body, loading: false });
      } catch (error) {
        if (controller.signal.aborted) return;
        const failure = error as Partial<Failure>;
        setState((old) => ({ data: old.data, loading: false,
          error: { code: failure.code ?? "unavailable", message: failure.message ?? "Request failed. Try refreshing." },
        }));
      }
    })();
    return () => controller.abort();
  }, [path, revision]);
  return state;
}

function ErrorNotice({ error, kind = "Realm" }: { error: Failure; kind?: string }) {
  const title = error.code === "permission_denied" ? "Permission denied"
    : error.code === "unavailable" ? "Daemon unavailable"
    : error.code === "not_found" ? `${kind} no longer exists` : "Request failed";
  return <div className="error" role="alert"><strong>{title}</strong><p>{error.message}</p></div>;
}

const kinds = ["realm", "space", "stack", "cell", "container"];
function readScope(): string[] {
  try { return window.location.hash.slice(1).split("/").filter(Boolean).map(decodeURIComponent).slice(0, 5); }
  catch { return []; }
}
function scopeLink(scope: string[]): string {
  return "#" + scope.map(encodeURIComponent).join("/");
}
function apiPath(scope: string[]): string {
  return "/api/" + scope.map((name, i) => `${kinds[i]}s/${encodeURIComponent(name)}`).join("/");
}
type Entry = { metadata?: { name: string }; id?: string; status?: { state?: string } };

function Children({ scope, depth, revision }: { scope: string[]; depth: number; revision: number }) {
  const kind = kinds[depth];
  const title = kind[0].toUpperCase() + kind.slice(1);
  const parent = scope.slice(0, depth);
  const list = useResource<Entry[]>(`${apiPath(parent)}/${kind}s`, revision);
  return <>
    <section className="nav-group" aria-label={`${title} list`} aria-busy={list.loading}>
      <h2>{title}s <span>{list.data?.length ?? "—"}</span></h2>
      {list.loading && <p role="status">Loading {kind}s…</p>}
      {list.error && <ErrorNotice error={list.error} kind={title} />}
      {list.error && list.data !== undefined && <p className="stale">Stale {kind} list — refresh failed.</p>}
      {!list.loading && !list.error && list.data?.length === 0 && <p>No {kind}s found.</p>}
      <ul>{list.data?.map(entry => {
        const name = entry.metadata?.name ?? entry.id!;
        return <li key={name}><a className="resource-link" href={scopeLink([...parent, name])} aria-current={scope[depth] === name ? "page" : undefined}><span className="resource-icon" aria-hidden="true">◇</span><span>{name}</span>{entry.status?.state && <small>{entry.status.state}</small>}</a></li>;
      })}</ul>
    </section>
    {scope[depth] !== undefined && depth < 4 && <Children scope={scope} depth={depth + 1} revision={revision} />}
  </>;
}

type Theme = "system" | "light" | "dark";
function initialTheme(): Theme {
  try { const value = localStorage.getItem("kukeon-theme"); return value === "light" || value === "dark" ? value : "system"; }
  catch { return "system"; }
}
function App() {
  const [revision, setRevision] = useState(0);
  const [scope, setScope] = useState(readScope);
  const [theme, setTheme] = useState<Theme>(initialTheme);
  const [menuOpen, setMenuOpen] = useState(false);
  useEffect(() => {
    const changed = () => { setScope(readScope()); setMenuOpen(false); };
    window.addEventListener("hashchange", changed);
    return () => window.removeEventListener("hashchange", changed);
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try { localStorage.setItem("kukeon-theme", theme); } catch { /* Preferences are optional. */ }
  }, [theme]);
  const connection = useResource<{ endpoint: string }>("/api/connection", revision);
  const realms = useResource<Realm[]>("/api/realms", revision);
  const kind = kinds[Math.max(scope.length - 1, 0)];
  const title = kind[0].toUpperCase() + kind.slice(1);
  const selected = scope.at(-1);
  const detail = useResource<Realm>(scope.length ? apiPath(scope) : null, revision);
  const status = realms.loading ? "Connecting…" : realms.error
    ? realms.error.code === "permission_denied" ? "Permission denied" : "Unavailable" : "Connected";
  return <div className="app-shell">
    <aside className={`sidebar ${menuOpen ? "is-open" : ""}`} id="resource-sidebar" aria-label="Workspace sidebar">
      <button className="sidebar-close" aria-label="Close sidebar" onClick={() => setMenuOpen(false)}>×</button>
      <a className="brand" href="#"><span className="mark" aria-hidden="true">K</span><span>Kukeon<small>LOCAL WORKSPACE</small></span></a>
      <div className="sidebar-scroll">
        <a className="workspace-link" href="#"><span aria-hidden="true">▦</span> Resource explorer</a>
        <section className="nav-group" aria-label="Realm list" aria-busy={realms.loading}>
          <h2>Realms <span>{realms.data?.length ?? "—"}</span></h2>
          {realms.loading && <p role="status">Loading realms…</p>}
          {realms.error && <ErrorNotice error={realms.error} />}
          {realms.error && realms.data !== undefined && <p className="stale">Stale realm list — refresh failed.</p>}
          {!realms.loading && !realms.error && realms.data?.length === 0 && <p>No realms found.</p>}
          <ul>{realms.data?.map(realm => <li key={realm.metadata.name}><button className="resource-link" aria-pressed={scope[0] === realm.metadata.name} onClick={() => { window.location.hash = scopeLink([realm.metadata.name]); setMenuOpen(false); }}><span className="resource-icon" aria-hidden="true">◈</span><span>{realm.metadata.name}</span></button></li>)}</ul>
        </section>
        {scope.length > 0 && <Children scope={scope} depth={1} revision={revision} />}
      </div>
      <section className="connection" aria-label="Daemon connection">
        <p className="status" role="status"><span className={`dot ${realms.error ? "failed" : ""}`} aria-hidden="true" />{status}</p>
        <code>{connection.data?.endpoint ?? (connection.loading ? "Loading socket…" : "Socket unknown")}</code>
        {connection.error && <ErrorNotice error={connection.error} />}
        <label className="theme-control">Appearance<select aria-label="Appearance" value={theme} onChange={event => setTheme(event.target.value as Theme)}><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select></label>
      </section>
    </aside>
    <main className="workspace">
      <header className="toolbar">
        <button className="menu-toggle" aria-label="Toggle sidebar" aria-expanded={menuOpen} aria-controls="resource-sidebar" onClick={() => setMenuOpen(!menuOpen)}>☰</button>
        <nav aria-label="Resource scope"><a href="#">Resources</a>{scope.map((name, i) => <a key={i} aria-label={`${kinds[i]}: ${name}`} href={scopeLink(scope.slice(0, i + 1))} aria-current={i === scope.length - 1 ? "page" : undefined}><span className="crumb-kind">{kinds[i]}: </span>{name}</a>)}</nav>
        <button className="refresh" onClick={() => setRevision(value => value + 1)}>Refresh</button>
      </header>
      <div className="workspace-content">
        <div className="page-heading"><p className="eyebrow">{selected ? `${title} details` : "Your infrastructure"}</p><h1>{selected ?? "Resource explorer"}</h1><p className="description">{selected ? scope.join(" / ") : "Browse your local workspace. Select a realm in the sidebar to get started."}</p></div>
        <section className="document panel" aria-label={`${title} document`} aria-busy={selected !== undefined && detail.loading}>
          <div className="panel-heading"><h2>{selected ? "API document" : "Workspace resources"}</h2><span className="badge">Read only</span></div>
          {selected === undefined ? <div className="empty-state"><span aria-hidden="true">▦</span><h3>Everything in its place.</h3><p>Explore realms, spaces, stacks, cells, and containers from one workspace.</p><p>Choose a realm from the list.</p></div> : <>
            {detail.loading && <p role="status">Loading {kind} document…</p>}
            {detail.error && <ErrorNotice error={detail.error} kind={title} />}
            {detail.error && detail.data !== undefined && <p className="stale">Stale {kind} document — refresh failed.</p>}
            {detail.data !== undefined && <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(detail.data, null, 2)}</pre>}
          </>}
        </section>
        <footer>Live data from kukeond · Refresh to retrieve the latest state.</footer>
      </div>
    </main>
  </div>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
createRoot(root).render(<StrictMode><App /></StrictMode>);
