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
  const path = `${apiPath(parent)}/${kind}s`;
  const selected = scope[depth];
  const list = useResource<Entry[]>(path, revision);
  const detail = useResource<Realm>(selected === undefined ? null : apiPath(scope.slice(0, depth + 1)), revision);
  return <section aria-label={`${title} browser`}>
    <h2 className="scope-heading">{title}s in {parent.join(" / ")}</h2>
    <div className="browser">
      <section className="panel" aria-label={`${title} list`} aria-busy={list.loading}>
        <h2>{title}s</h2>
        {list.loading && <p role="status">Loading {kind}s…</p>}
        {list.error && <ErrorNotice error={list.error} kind={title} />}
        {list.error && list.data !== undefined && <p className="stale">Stale {kind} list — refresh failed.</p>}
        {!list.loading && !list.error && list.data?.length === 0 && <p>No {kind}s found.</p>}
        <ul>{list.data?.map((entry) => {
          const name = entry.metadata?.name ?? entry.id!;
          return <li key={name}><a className="realm" href={scopeLink([...parent, name])} aria-current={selected === name ? "page" : undefined}>{name}{entry.status?.state && <span>{entry.status.state}</span>}</a></li>;
        })}</ul>
      </section>
      <section className="document panel" aria-label={`${title} document`} aria-busy={selected !== undefined && detail.loading}>
        <h2>{selected ?? `${title} document`}</h2>
        {selected === undefined ? <p>Choose a {kind} from the list.</p> : <>
          <p className="description">Scope: {scope.slice(0, depth + 1).join(" / ")}</p>
          {detail.loading && <p role="status">Loading {kind} document…</p>}
          {detail.error && <ErrorNotice error={detail.error} kind={title} />}
          {detail.error && detail.data !== undefined && <p className="stale">Stale {kind} document — refresh failed.</p>}
          {detail.data !== undefined && <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(detail.data, null, 2)}</pre>}
        </>}
      </section>
    </div>
    {selected !== undefined && depth < 4 && <Children scope={scope} depth={depth + 1} revision={revision} />}
  </section>;
}

function App() {
  const [revision, setRevision] = useState(0);
  const [scope, setScope] = useState(readScope);
  useEffect(() => {
    const changed = () => setScope(readScope());
    window.addEventListener("hashchange", changed);
    return () => window.removeEventListener("hashchange", changed);
  }, []);
  const selected = scope[0] ?? null;
  const setSelected = (name: string) => { window.location.hash = scopeLink([name]); };
  const connection = useResource<{ endpoint: string }>("/api/connection", revision);
  const realms = useResource<Realm[]>("/api/realms", revision);
  const detail = useResource<Realm>(selected === null ? null : `/api/realms/${encodeURIComponent(selected)}`, revision);
  const status = realms.loading ? "Connecting…" : realms.error
    ? realms.error.code === "permission_denied" ? "Permission denied" : "Unavailable" : "Connected";
  return (
    <main>
      <header><span className="mark" aria-hidden="true">K</span><span>Kukeon</span><span className="local">Local workspace</span></header>
      <section className="connection" aria-label="Daemon connection">
        <div><p className="eyebrow">Daemon socket</p><code>{connection.data?.endpoint ?? (connection.loading ? "Loading socket…" : "Socket unknown")}</code></div>
        <p className="status" role="status">{status}</p>
      </section>
      {connection.error && <ErrorNotice error={connection.error} />}
      <div className="heading"><div><p className="eyebrow">Explore your workspace</p><h1>Realms</h1></div><button onClick={() => setRevision((value) => value + 1)}>Refresh</button></div>
      <p className="description">Select a realm to inspect its complete API document.</p>
      <div className="browser">
        <section className="realms panel" aria-label="Realm list" aria-busy={realms.loading}>
          <h2>All realms</h2>
          {realms.loading && <p role="status">Loading realms…</p>}
          {realms.error && <ErrorNotice error={realms.error} />}
          {realms.error && realms.data !== undefined && <p className="stale">Stale realm list — refresh failed.</p>}
          {!realms.loading && !realms.error && realms.data?.length === 0 && <p>No realms found.</p>}
          <ul>{realms.data?.map((realm) => <li key={realm.metadata.name}><button className="realm" aria-pressed={selected === realm.metadata.name} onClick={() => setSelected(realm.metadata.name)}>{realm.metadata.name}<span aria-hidden="true">→</span></button></li>)}</ul>
        </section>
        <section className="document panel" aria-label="Realm document" aria-busy={selected !== null && detail.loading}>
          <h2>{selected ?? "Realm document"}</h2>
          {selected === null ? <p>Choose a realm from the list.</p> : <>
            {detail.loading && <p role="status">Loading realm document…</p>}
            {detail.error && <ErrorNotice error={detail.error} />}
            {detail.error && detail.data !== undefined && <p className="stale">Stale realm document — refresh failed.</p>}
            {detail.data !== undefined && <pre tabIndex={0} aria-label="Complete realm JSON">{JSON.stringify(detail.data, null, 2)}</pre>}
          </>}
        </section>
      </div>
      <nav aria-label="Resource scope"><a href="#">Realms</a>{scope.map((name, i) => <a key={i} href={scopeLink(scope.slice(0, i + 1))}>{kinds[i]}: {name}</a>)}</nav>
      {selected !== null && <Children scope={scope} depth={1} revision={revision} />}
      <footer>Read-only view · Refresh to retrieve the latest data from kukeond.</footer>
    </main>
  );
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
createRoot(root).render(<StrictMode><App /></StrictMode>);
