import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { ErrorNotice, toFailure, useResource, type Failure } from "./request";
import { daemonLink, healthLink, overviewLink } from "./routes";
import type { InventoryResource } from "./inventory";
import { MAX_CHARS, MAX_LINES, applyChunk, emptyLogBuffer, healthLabel, healthTone, type LogBuffer, type LogChunk } from "./log-buffer.js";
import { AskMajordomo, oneLine } from "./majordomo";
import { apiFetch } from "./transport";

// DaemonHealth mirrors GET /api/daemon/health (issue #1386, step 6). It only
// reports whether kukeond answered a Ping over the configured socket.
export type DaemonHealth = {
  status: string;
  endpoint: string;
  daemonVersion?: string;
  serverVersion: string;
  checkedAt: string;
  latencyMs: number;
  error?: Failure;
};

const TAIL_BYTES = 64 * 1024;
const FOLLOW_INTERVAL_MS = 2000;
// Drain at most this many bounded chunks per follow tick so a burst of output
// cannot monopolize the page.
const FOLLOW_CHUNKS_PER_TICK = 8;

function displayVersion(version?: string): string {
  if (!version) return "Not reported";
  return version.startsWith("v") ? version : `v${version}`;
}

export function HealthBadge({ status }: { status?: string }) {
  return <span className={`state-badge tone-${healthTone(status)}`}><span className="state-dot" aria-hidden="true" />{healthLabel(status)}</span>;
}

// The Daemon page's tabs (#1635): Status (the connectivity check), Health (the sweep reports) and Logs.
export function DaemonNav({ current }: { current: "status" | "health" | "logs" }) {
  return <nav className="daemon-tabs" aria-label="Daemon sections">
    <a href={daemonLink()} aria-current={current === "status" ? "page" : undefined}>Status</a>
    <a href={healthLink()} aria-current={current === "health" ? "page" : undefined}>Health</a>
    <a href={daemonLink("logs")} aria-current={current === "logs" ? "page" : undefined}>Logs</a>
  </nav>;
}

function daemonQuestion(data: DaemonHealth): string {
  const state = `connectivity ${healthLabel(data.status)}, kukeond version ${data.daemonVersion || "unknown"}, kuke serve version ${data.serverVersion}, response time ${data.latencyMs} ms${data.error ? `, error: ${data.error.message}` : ""}`;
  return oneLine(`About the kukeond daemon at ${data.endpoint} (${state}): diagnose its health with "kuke health" and tell me what needs attention.`);
}

export function DaemonHealthPage({ revision, inventory }: { revision: number; inventory: InventoryResource }) {
  const [checks, setChecks] = useState(0);
  const health = useResource<DaemonHealth>("/daemon/health", revision + checks);
  const data = health.data;
  const versionsDiffer = !!data?.daemonVersion && displayVersion(data.daemonVersion) !== displayVersion(data.serverVersion);
  return <>
    <DaemonNav current="status" />
    {health.error && <ErrorNotice error={health.error} kind="Daemon" />}
    {health.error && data && <p className="stale">Stale health result — the web server could not be reached.</p>}
    <section className="panel daemon-health" aria-label="Daemon connectivity" aria-busy={health.loading}>
      <div className="panel-heading"><h2>Connectivity</h2><div className="health-report-actions">{data && <AskMajordomo prominent={!!data.error} question={daemonQuestion(data)} subject="the kukeond daemon" />}<button type="button" onClick={() => setChecks(n => n + 1)} disabled={health.loading}>{health.loading ? "Checking…" : "Check again"}</button></div></div>
      <div className="panel-body">
        <div className="status-grid">
          <div className="status-card"><span>Connectivity</span><HealthBadge status={health.loading && !data ? undefined : data?.status ?? (health.error?.code === "unavailable" ? "unavailable" : undefined)} /></div>
          <div className="status-card"><span>kukeond version</span><strong className="mono-id">{data?.status === "reachable" ? displayVersion(data.daemonVersion) : "Unknown"}</strong>{versionsDiffer && <small>Differs from kuke serve</small>}</div>
          <div className="status-card"><span>kuke serve version</span><strong className="mono-id">{displayVersion(data?.serverVersion)}</strong></div>
          <div className="status-card"><span>Response time</span><strong>{data ? `${data.latencyMs} ms` : "—"}</strong></div>
        </div>
        <dl className="details-grid daemon-details">
          <dt>Socket</dt><dd><code>{data?.endpoint ?? "Loading…"}</code></dd>
          <dt>Last checked</dt><dd>{data?.checkedAt ? <time dateTime={data.checkedAt}>{new Date(data.checkedAt).toLocaleString()}</time> : "Not checked yet"}</dd>
          <dt>Inventory refreshed</dt><dd>{inventory.data ? <time dateTime={inventory.data.updatedAt}>{new Date(inventory.data.updatedAt).toLocaleString()}</time> : inventory.loading ? "Refreshing…" : "Not refreshed"}{(inventory.error || !!inventory.data?.failures.length) && <span className="stale"> · incomplete or stale</span>}</dd>
        </dl>
        {data?.error && <div className="error" role="alert"><strong>{healthLabel(data.status)}</strong><p>{data.error.message}</p><p>{data.status === "permission_denied" ? "Run kuke serve as a user that can open the daemon socket (for example, a member of the kukeon group)." : data.status === "daemon_outdated" ? "Upgrade kukeond to match this kuke serve build." : data.status === "unavailable" ? "Check that kukeond is running (kuke daemon start) and that the selected socket is correct." : "kukeond answered but the check did not succeed; see the daemon logs."}</p></div>}
      </div>
    </section>
    <section className="panel" aria-label="Health scope">
      <div className="panel-heading"><h2>What this check covers</h2></div>
      <div className="panel-body"><p className="description">Connectivity means kukeond answered a ping on the selected socket using this server's permissions. It does not report the health of cells, containers, cgroups, networking or other subsystems. Review cell states in <a className="text-link" href={overviewLink()}>Overview</a>, read the <a className="text-link" href={daemonLink("logs")}>daemon logs</a>, or run <code>kuke status</code> for host pre-flight and state-consistency checks.</p></div>
    </section>
  </>;
}

async function readChunk(offset: number, signal: AbortSignal): Promise<LogChunk> {
  const response = await apiFetch(`/daemon/logs?offset=${offset}&maxBytes=${TAIL_BYTES}`, { signal, headers: { Accept: "application/json" } });
  let body: { error?: Failure } & LogChunk = {};
  try { body = await response.json(); } catch { /* Use the HTTP status below. */ }
  if (!response.ok) throw body.error ?? { code: "http_error", message: `HTTP ${response.status}` };
  return body;
}

export function DaemonLogsPage({ revision }: { revision: number }) {
  const [buffer, setBuffer] = useState<LogBuffer>(emptyLogBuffer);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Failure>();
  const [following, setFollowing] = useState(false);
  const [notice, setNotice] = useState<string>();
  const [copyState, setCopyState] = useState<string>();
  const [reloads, setReloads] = useState(0);
  const bufferRef = useRef(buffer);
  // busy holds the token of the read in flight (null when idle); generation
  // bumps on every tail reload so a follow read issued before it is dropped.
  const busy = useRef<number | null>(null);
  const tokens = useRef(0);
  const generation = useRef(0);
  const output = useRef<HTMLPreElement>(null);
  const stickToBottom = useRef(true);

  const commit = useCallback((next: LogBuffer) => { bufferRef.current = next; setBuffer(next); }, []);

  // A tail read on mount, Reload, or the global Refresh replaces the buffer.
  useEffect(() => {
    const controller = new AbortController();
    const token = ++tokens.current;
    generation.current++;
    busy.current = token;
    setLoading(true);
    const release = () => { if (busy.current === token) busy.current = null; };
    void (async () => {
      try {
        const chunk = await readChunk(-1, controller.signal);
        if (controller.signal.aborted) return;
        stickToBottom.current = true;
        commit(applyChunk(bufferRef.current, chunk, { replace: true }));
        setError(undefined);
        setNotice(undefined);
      } catch (failure) {
        if (!controller.signal.aborted) setError(toFailure(failure));
      } finally {
        if (!controller.signal.aborted) { release(); setLoading(false); }
      }
    })();
    return () => { controller.abort(); release(); };
  }, [revision, reloads, commit]);

  // Follow mode polls forward from the last cursor; Pause stops polling.
  useEffect(() => {
    if (!following) return;
    const controller = new AbortController();
    let active: number | null = null;
    const tick = async () => {
      if (busy.current !== null || document.visibilityState !== "visible") return;
      const token = ++tokens.current;
      const gen = generation.current;
      const stale = () => controller.signal.aborted || generation.current !== gen;
      busy.current = active = token;
      try {
        for (let i = 0; i < FOLLOW_CHUNKS_PER_TICK; i++) {
          const cursor = bufferRef.current.next;
          const chunk = await readChunk(cursor ?? -1, controller.signal);
          // A tail reload that started meanwhile owns the buffer now.
          if (stale()) return;
          commit(applyChunk(bufferRef.current, chunk, { replace: cursor === null }));
          if (chunk.reset) setNotice("The daemon log was truncated or replaced since the last read; showing its latest output.");
          setError(undefined);
          if (!chunk.more) break;
        }
      } catch (failure) {
        if (!stale()) setError(toFailure(failure));
      } finally {
        if (busy.current === token) busy.current = null;
        if (active === token) active = null;
      }
    };
    void tick();
    const timer = window.setInterval(() => { void tick(); }, FOLLOW_INTERVAL_MS);
    return () => {
      window.clearInterval(timer);
      controller.abort();
      if (active !== null && busy.current === active) busy.current = null;
    };
  }, [following, commit]);

  useLayoutEffect(() => {
    const node = output.current;
    if (node && stickToBottom.current) node.scrollTop = node.scrollHeight;
  }, [buffer]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(buffer.lines.join("\n"));
      setCopyState(`Copied ${buffer.lines.length} lines.`);
    } catch {
      setCopyState("Copy failed: the browser denied clipboard access. Select the output and copy it manually.");
    }
  }

  const lines = buffer.lines;
  return <>
    <DaemonNav current="logs" />
    <section className="panel daemon-logs" aria-label="Daemon logs" aria-busy={loading}>
      <div className="panel-heading"><h2>kukeond output</h2><span className="badge" role="status">{following ? "Following" : "Paused"}</span></div>
      <div className="list-controls log-controls">
        <button type="button" aria-pressed={following} onClick={() => setFollowing(value => !value)}>{following ? "Pause" : "Follow"}</button>
        <button type="button" onClick={() => setReloads(n => n + 1)} disabled={loading}>Reload latest</button>
        <button type="button" onClick={() => void copy()} disabled={!lines.length}>Copy</button>
        <span className="result-count">{lines.length} lines</span>
      </div>
      <p className="description log-meta">
        Source {buffer.source ? <code>{buffer.source}</code> : "not resolved"} · read through kukeond · verbatim output in arrival order · this page keeps at most {MAX_LINES.toLocaleString()} lines ({(MAX_CHARS / 1_000_000).toLocaleString()} MB).
      </p>
      {copyState && <p className="description" role="status">{copyState}</p>}
      {buffer.omitted && lines.length > 0 && <p className="description log-omitted">Earlier output is not shown.</p>}
      {notice && <p className="stale" role="status">{notice}</p>}
      {error && <ErrorNotice error={error} kind="Daemon log" />}
      {error && lines.length > 0 && <p className="stale">{following ? "Following is retrying; the output below may be out of date." : "The output below may be out of date."}</p>}
      {loading && !lines.length && <p className="scope-filter" role="status">Loading daemon logs…</p>}
      {!loading && !error && !lines.length && <div className="empty-state"><h3>No log output yet</h3><p>kukeond has not written any complete lines. Follow to watch for new output.</p></div>}
      {lines.length > 0 && <pre ref={output} className="log-output" tabIndex={0} aria-label="kukeond log output" onScroll={event => {
        const node = event.currentTarget;
        stickToBottom.current = node.scrollHeight - node.scrollTop - node.clientHeight < 24;
      }}>{lines.join("\n")}</pre>}
    </section>
  </>;
}
