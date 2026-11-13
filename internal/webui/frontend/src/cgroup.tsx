import { useResource, ErrorNotice } from "./request";
import { apiPath, kinds } from "./routes";
import { StateBadge } from "./status";
import {
  formatCPUMax, formatCgroupList, formatCgroupValue, formatDeclared, formatIOMax,
  type CgroupCPUMax, type CgroupDeclared, type CgroupIOMax, type CgroupList, type CgroupValue, type Formatted,
} from "./cgroup-format.js";

// CgroupDetail mirrors kukeonv1.GetCgroupDetailResult (issue #1410).
type CgroupDetail = {
  cgroupPath?: string;
  absolutePath?: string;
  exists?: boolean;
  ready?: boolean;
  reason?: string;
  subtreeControllers?: string[];
  nestedCgroupRuntime?: boolean;
  hostCgroup?: boolean;
  declared?: CgroupDeclared[];
  live?: {
    controllers?: CgroupList;
    subtreeControl?: CgroupList;
    populated?: CgroupValue;
    memory?: Record<"max" | "high" | "swapMax" | "current" | "peak" | "oom" | "oomKill", CgroupValue | undefined>;
    cpu?: { max?: CgroupCPUMax; weight?: CgroupValue; usageUsec?: CgroupValue; userUsec?: CgroupValue; systemUsec?: CgroupValue };
    pids?: { max?: CgroupValue; current?: CgroupValue };
    io?: CgroupIOMax;
  };
};

const declaredLabels: Record<string, { label: string; live: (live: NonNullable<CgroupDetail["live"]>) => Formatted }> = {
  memoryLimitBytes: { label: "Memory limit", live: live => formatCgroupValue(live.memory?.max, "bytes") },
  cpuShares: { label: "CPU shares", live: live => formatCgroupValue(live.cpu?.weight, "weight") },
  pidsLimit: { label: "PIDs limit", live: live => formatCgroupValue(live.pids?.max) },
};

function Value({ value }: { value: Formatted }) {
  return <span title={value.raw ? `Raw: ${value.raw}` : undefined}>{value.text}{value.raw && value.raw !== value.text && <small className="cgroup-raw"> ({value.raw})</small>}</span>;
}

function Row({ label, value }: { label: string; value: Formatted }) {
  return <><dt>{label}</dt><dd><Value value={value} /></dd></>;
}

function LiveValues({ live }: { live: NonNullable<CgroupDetail["live"]> }) {
  const memory = live.memory;
  const peak = formatCgroupValue(memory?.peak, "bytes");
  return <div className="cgroup-live">
    <h3>Memory</h3>
    <dl className="details-grid">
      <Row label="max" value={formatCgroupValue(memory?.max, "bytes")} />
      <Row label="high" value={formatCgroupValue(memory?.high, "bytes")} />
      <Row label="swap.max" value={formatCgroupValue(memory?.swapMax, "bytes")} />
      <Row label="current" value={formatCgroupValue(memory?.current, "bytes")} />
      {memory?.peak?.state !== "absent" && <Row label="peak" value={peak} />}
      <Row label="oom events" value={formatCgroupValue(memory?.oom)} />
      <Row label="oom kills" value={formatCgroupValue(memory?.oomKill)} />
    </dl>
    <h3>CPU</h3>
    <dl className="details-grid">
      <Row label="max" value={formatCPUMax(live.cpu?.max)} />
      <Row label="weight" value={formatCgroupValue(live.cpu?.weight, "weight")} />
      <Row label="usage" value={formatCgroupValue(live.cpu?.usageUsec, "usec")} />
      <Row label="user" value={formatCgroupValue(live.cpu?.userUsec, "usec")} />
      <Row label="system" value={formatCgroupValue(live.cpu?.systemUsec, "usec")} />
    </dl>
    <h3>PIDs</h3>
    <dl className="details-grid">
      <Row label="max" value={formatCgroupValue(live.pids?.max)} />
      <Row label="current" value={formatCgroupValue(live.pids?.current)} />
    </dl>
    <h3>IO</h3>
    <ul className="cgroup-io">{formatIOMax(live.io).map(line => <li key={line}>{line}</li>)}</ul>
  </div>;
}

export function CgroupPanel({ scope, revision }: { scope: string[]; revision: number }) {
  const kind = kinds[scope.length - 1];
  const detail = useResource<CgroupDetail>(`${apiPath(scope)}/cgroup`, revision);
  const data = detail.data && !Array.isArray(detail.data) ? detail.data : undefined;
  const live = data?.exists ? data.live : undefined;
  return <section className="panel cgroup-panel" aria-label={`${kind} cgroup`}>
    <div className="panel-heading"><h2>Cgroup</h2><span className="badge">{detail.error ? "Stale values" : detail.loading ? "Refreshing values" : "Read by kukeond"}</span></div>
    <div className="panel-body">
      {detail.loading && !data && <p role="status">Loading cgroup details…</p>}
      {detail.error && <ErrorNotice error={detail.error} kind={kind} />}
      {detail.error && data && <p className="stale">Stale cgroup values — refresh failed.</p>}
      {data && <>
        <h3>Configuration</h3>
        <dl className="details-grid">
          <dt>Path</dt><dd><code>{data.absolutePath || data.cgroupPath || "Not recorded"}</code></dd>
          <dt>Readiness</dt><dd><StateBadge state={data.ready ? "Ready" : "Not ready"} />{data.reason && !data.ready && <small> {data.reason}</small>}</dd>
          {kind !== "container" && <><dt>Delegated controllers</dt><dd>{data.subtreeControllers?.length ? data.subtreeControllers.join(" ") : "None"}</dd></>}
          {data.nestedCgroupRuntime !== undefined && <><dt>Nested cgroup runtime</dt><dd>{data.nestedCgroupRuntime ? "Enabled" : "Disabled"}</dd></>}
          {data.hostCgroup !== undefined && <><dt>Host cgroup namespace</dt><dd>{data.hostCgroup ? "Shared with parent" : "Private"}</dd></>}
          {live && <><dt>Controllers</dt><dd>{formatCgroupList(live.controllers)}</dd><dt>Subtree control</dt><dd>{formatCgroupList(live.subtreeControl)}</dd><dt>Populated</dt><dd>{live.populated?.state === "set" ? live.populated.value ? "Yes" : "No" : formatCgroupValue(live.populated).text}</dd></>}
        </dl>
        {!!data.declared?.length && <>
          <h3>Declared resources</h3>
          <div className="table-scroll"><table className="cgroup-declared"><thead><tr><th>Field</th><th>Declared</th><th>Source</th><th>Live effective</th></tr></thead><tbody>{data.declared.map(entry => {
            const declared = formatDeclared(entry);
            const meta = declaredLabels[entry.field];
            return <tr key={entry.field}><td>{meta?.label ?? entry.field}</td><td>{declared.text}</td><td>{declared.source}</td><td>{live && meta ? <Value value={meta.live(live)} /> : "Cgroup not present"}</td></tr>;
          })}</tbody></table></div>
        </>}
        <h3>Limits and usage (live)</h3>
        {live ? <LiveValues live={live} /> : <div className="status-message"><strong>Cgroup not present</strong><p>{data.cgroupPath ? `kukeond found no cgroup at this path${data.reason ? ` (${data.reason})` : ""}. Stopped containers and cells without a cgroup have no live values.` : "No cgroup path is recorded for this resource yet."}</p></div>}
      </>}
    </div>
  </section>;
}
