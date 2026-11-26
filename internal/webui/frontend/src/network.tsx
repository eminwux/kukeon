import { useResource, ErrorNotice, failureTitle, type Resource } from "./request";
import { StoredDisclosure, disclosedKinds } from "./disclosure";
import { apiPath, kinds } from "./routes";
import {
  formatBridge, formatConfigPath, formatContainerCNIPath, formatEgressDefault, formatFlag,
  formatIPv4, formatNetworkExists, formatNetworkMode, formatRulePorts, formatRuleTarget, formatSubnet, summarizeCellNetwork, summarizeContainerNetwork,
  type CellNetwork, type ContainerNetwork, type SpaceNetwork,
} from "./network-format.js";

// NetworkDetail mirrors kukeonv1.GetNetworkDetailResult (issue #1411).
export type NetworkDetail = { space?: SpaceNetwork; cell?: CellNetwork; container?: ContainerNetwork };

// networkKinds are the resource kinds with networking fields; realms and
// stacks have none.
export const networkKinds = ["space", "cell", "container"];

function SpaceSection({ space }: { space: SpaceNetwork }) {
  const rules = space.egress?.allow ?? [];
  return <>
    <h3>Space network</h3>
    <dl className="details-grid">
      <dt>Network name</dt><dd><code>{space.networkName}</code></dd>
      <dt>Bridge</dt><dd>{formatBridge(space.bridgeName)}</dd>
      <dt>Subnet</dt><dd>{formatSubnet(space)}</dd>
      <dt>CNI config path</dt><dd><code>{formatConfigPath(space)}</code></dd>
      <dt>CNI network present</dt><dd>{formatNetworkExists(space.cniNetworkExists)}</dd>
    </dl>
    <h3>Egress</h3>
    <dl className="details-grid"><dt>Default</dt><dd>{formatEgressDefault(space.egress)}</dd></dl>
    {rules.length
      ? <div className="table-scroll"><table className="network-rules"><thead><tr><th>Type</th><th>Destination</th><th>Ports</th></tr></thead><tbody>{rules.map((rule, i) => {
        const target = formatRuleTarget(rule);
        return <tr key={i}><td>{target.kind}</td><td><code>{target.value}</code></td><td>{formatRulePorts(rule.ports)}</td></tr>;
      })}</tbody></table></div>
      : <p className="network-empty">No allow rules.</p>}
  </>;
}

function CellSection({ cell }: { cell: CellNetwork }) {
  return <>
    <h3>Cell network</h3>
    <dl className="details-grid">
      <dt>Network mode</dt><dd>{formatNetworkMode(cell)}</dd>
      <dt>Bridge</dt><dd>{cell.hostNetwork ? "Host network (no bridge)" : formatBridge(cell.bridgeName)}</dd>
      <dt>IPv4</dt><dd>{formatIPv4(cell)}</dd>
      <dt>Network namespace</dt><dd>Owned by root container <code>{cell.rootContainer}</code>; other containers join it</dd>
    </dl>
  </>;
}

function ContainersSummary({ cell }: { cell: CellNetwork }) {
  if (!cell.containers?.length) return null;
  return <>
    <h3>Containers</h3>
    <div className="table-scroll"><table className="network-containers"><thead><tr><th>Container</th><th>Host network</th><th>Host PID</th></tr></thead><tbody>{cell.containers.map(c =>
      <tr key={c.id}><td>{c.id}{c.root && <small className="network-note"> (root)</small>}</td><td>{formatFlag(c.hostNetwork)}</td><td>{formatFlag(c.hostPID)}</td></tr>)}</tbody></table></div>
  </>;
}

function ContainerSection({ container, cell }: { container: ContainerNetwork; cell?: CellNetwork }) {
  return <>
    <h3>Container network</h3>
    <dl className="details-grid">
      <dt>Host network</dt><dd>{formatFlag(container.hostNetwork)}</dd>
      <dt>Host PID</dt><dd>{formatFlag(container.hostPID)}</dd>
      <dt>CNI config path</dt><dd><code>{formatContainerCNIPath(container)}</code></dd>
      <dt>Shared namespaces</dt><dd>{container.root ? "Owns the cell network namespace" : <>Joins the network namespace of root container <code>{cell?.rootContainer}</code></>}</dd>
    </dl>
  </>;
}

// NetworkPanel reads the scope's network detail itself, unless the page already read it and passes it in
// (the cell page shares one read with its overview's IP address row).
export function NetworkPanel({ scope, revision, detail: shared }: { scope: string[]; revision: number; detail?: Resource<NetworkDetail> }) {
  const kind = kinds[scope.length - 1];
  const own = useResource<NetworkDetail>(shared ? null : `${apiPath(scope)}/network`, revision);
  const detail = shared ?? own;
  const data = detail.data && !Array.isArray(detail.data) ? detail.data : undefined;
  const badge = <span className="badge">{detail.error ? "Stale values" : detail.loading ? "Refreshing values" : "Read by kukeond"}</span>;
  const body = <div className="panel-body">
    {detail.loading && !data && <p role="status">Loading network details…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={kind} />}
    {detail.error && data && <p className="stale">Stale network values — refresh failed.</p>}
    {data && <>
      {data.container && <ContainerSection container={data.container} cell={data.cell} />}
      {data.cell && <CellSection cell={data.cell} />}
      {kind === "cell" && data.cell && <ContainersSummary cell={data.cell} />}
      {data.space && <SpaceSection space={data.space} />}
    </>}
  </div>;
  if (!disclosedKinds.includes(kind)) return <section className="panel network-panel" aria-label={`${kind} network`}><div className="panel-heading"><h2>Network</h2>{badge}</div>{body}</section>;
  // Collapsed, the heading row still says what the section holds; a failed read stays visible there.
  const summary = data?.container ? summarizeContainerNetwork(data.container) : data?.cell ? summarizeCellNetwork(data.cell)
    : detail.error ? failureTitle(detail.error, kind) : detail.loading ? "Loading…" : "Not reported";
  return <StoredDisclosure id={`${kind}-network`} title="Network" summary={summary} badge={badge} className="network-panel" label={`${kind} network`}>{body}</StoredDisclosure>;
}
