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

function ErrorNotice({ error }: { error: Failure }) {
  const title = error.code === "permission_denied" ? "Permission denied"
    : error.code === "unavailable" ? "Daemon unavailable"
    : error.code === "not_found" ? "Realm no longer exists" : "Request failed";
  return <div className="error" role="alert"><strong>{title}</strong><p>{error.message}</p></div>;
}

function App() {
  const [revision, setRevision] = useState(0);
  const [selected, setSelected] = useState<string | null>(null);
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
          {realms.data?.length === 0 && <p>No realms found.</p>}
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
      <footer>Read-only view · Refresh to retrieve the latest data from kukeond.</footer>
    </main>
  );
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
createRoot(root).render(<StrictMode><App /></StrictMode>);
