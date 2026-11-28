import { useEffect } from "react";
import { ErrorNotice, useResource } from "./request";
import { connectorCatalogLink, identitiesLink } from "./connector-ui";
import { DaemonNav } from "./daemon";
import { imagesLink } from "./routes";
import { connectorLevels, defaultTargets, deliveryText, identitiesFor, isRegistry, listedConnectors, loginIdentitiesFor, loginMethods } from "./connectors.mjs";
import { connectorSetUp, notSetUpText } from "./onboarding.mjs";
import type { Connector } from "./connectors.mjs";

// The connector catalog (#1548, §7), the Daemon page's Connectors tab (#1790). It iterates
// GET /api/connectors: the read-only data embedded in kuke. Nothing is created, rotated or deleted here —
// each connector links to the Identities page filtered on it, where its identities are managed.

type Counts = { count: (connector: string) => number; setUp: (connector: Connector) => boolean };
// useIdentityCounts counts each connector's identities — its tagged Secrets and its login identities — and
// says whether one is set up.
function useIdentityCounts(revision: number): Counts | undefined {
  const secrets = useResource<unknown[]>("/catalog/secrets", revision);
  const logins = useResource<unknown[]>("/connectors/logins", revision);
  if (!secrets.data || !logins.data) return undefined;
  return {
    count: connector => identitiesFor(secrets.data, connector).length + loginIdentitiesFor(logins.data, connector).length,
    setUp: connector => connectorSetUp(connector, secrets.data, logins.data),
  };
}

function IdentityCount({ connector, counts }: { connector: Connector; counts: Counts | undefined }) {
  const title = connector.title || connector.name;
  if (!counts) return <>—</>;
  const n = counts.count(connector.name);
  return <><a href={identitiesLink(connector.name)} aria-label={`${n} ${title} ${n === 1 ? "identity" : "identities"}`}>{n}</a>{!counts.setUp(connector) && <> <span className="badge not-set-up" title={notSetUpText(connector)}>Not set up — recommended</span></>}</>;
}

// identityText says what fills a connector: the Secret types and fields it accepts, and a login identity
// for a method that keeps its login in the harness state directory.
function identityText(connector: Connector): string {
  const logins = loginMethods(connector);
  const secrets = isRegistry(connector) || defaultTargets(connector).length > 0
    ? `${connector.secretTypes.join(" or ")} Secret${connector.fields.length ? ` with ${connector.fields.map(field => field.name).join(", ")}` : ""}` : "";
  const login = logins.length ? `a login identity (a Volume) for the ${logins.map(method => method.name).join(", ")} method` : "";
  return [secrets, login].filter(Boolean).join(", or ") || "None";
}

function ConnectorList({ connectors, counts }: { connectors: Connector[]; counts: Counts | undefined }) {
  return <section className="panel" aria-label="Connectors list">
    <div className="table-scroll"><table><thead><tr><th>Connector</th><th>Delivered as</th><th>Identity</th><th>Default scopes</th><th>Harness</th><th>Identities</th></tr></thead><tbody>
      {connectors.map(connector => <tr key={connector.name}>
        <td><a className="resource-name" href={connectorCatalogLink(connector.name)}>{connector.title || connector.name}</a> <span className="badge">{connector.name}</span>{connector.description && <p className="description">{connector.description}</p>}</td>
        <td>{connector.methods.length ? connector.methods.map(method => <div key={method.name}><code>{method.name}</code> — {deliveryText(method)}</div>) : deliveryText({ delivery: connector.delivery ?? "" })}</td>
        <td>{identityText(connector)}</td>
        <td>{connectorLevels(connector).join(", ")}</td>
        <td>{connector.harness || <span className="description">—</span>}</td>
        <td><IdentityCount connector={connector} counts={counts} /></td>
      </tr>)}
    </tbody></table></div>
    {!connectors.length && <div className="empty-state"><h3>No connectors are defined</h3></div>}
  </section>;
}

function ConnectorDetail({ connector, counts }: { connector: Connector; counts: Counts | undefined }) {
  const title = connector.title || connector.name;
  return <>
    <div className="detail-actions"><a href={connectorCatalogLink()}>← Back to connectors</a></div>
    <section className="panel" aria-label={`${title} connector`}>
      <div className="panel-heading"><h2>{title}</h2><span className="badge">{connector.name}</span>{!isRegistry(connector) && <a className="document-action" href={identitiesLink(connector.name)}>Manage {title} identities</a>}</div>
      <div className="panel-body">
        {connector.description && <p>{connector.description}</p>}
        {counts && !counts.setUp(connector) && <p className="connector-not-set-up" role="note">{notSetUpText(connector)}</p>}
        <dl className="details-grid">
          <dt>Name</dt><dd><code>{connector.name}</code></dd>
          <dt>Delivered as</dt><dd>{connector.methods.length ? <ul className="connector-methods">{connector.methods.map(method => <li key={method.name}><code>{method.name}</code>{method.default && <> <span className="badge">default method</span></>} — {deliveryText(method)}{method.description && <span className="description"> {method.description}</span>}</li>)}</ul> : deliveryText({ delivery: connector.delivery ?? "" })}</dd>
          <dt>Identity</dt><dd>{identityText(connector)}</dd>
          <dt>Default scopes</dt><dd>{connectorLevels(connector).join(", ")}</dd>
          {connector.harness && <><dt>Harness</dt><dd>{connector.harness}</dd></>}
          {connector.group && <><dt>Method group</dt><dd>{connector.group} — a Config enables exactly one method</dd></>}
          {connector.optional && <><dt>Optional</dt><dd>A cell starts without it</dd></>}
          {!isRegistry(connector) && <><dt>Identities</dt><dd><IdentityCount connector={connector} counts={counts} /></dd></>}
        </dl>
      </div>
    </section>
    {isRegistry(connector) && <section className="panel connector-registry-notice" aria-label={`${title} on the Images page`}><div className="panel-body"><p role="note">A registry credential authenticates a Realm's image pulls; nothing of it is injected into a cell. Registry credentials are listed and added on the <a href={imagesLink()}>Images</a> page, per Realm.</p></div></section>}
  </>;
}

// ConnectorCatalogPage is #/daemon/connectors and #/daemon/connectors/<name>.
export function ConnectorCatalogPage({ name, revision, onTitle }: { name?: string; revision: number; onTitle?: (connector: { name: string; title: string }) => void }) {
  const catalog = useResource<Connector[]>("/connectors", revision);
  const counts = useIdentityCounts(revision);
  const connector = name ? catalog.data?.find(item => item.name === name) : undefined;
  // The page heading shows the connector's title, which only the catalog knows (#1568).
  const connectorName = connector?.name, connectorTitle = connector?.title;
  useEffect(() => { if (connectorName) onTitle?.({ name: connectorName, title: connectorTitle || connectorName }); }, [connectorName, connectorTitle, onTitle]);
  return <>
    <DaemonNav current="connectors" />
    {catalog.loading && !catalog.data && <p role="status">Loading connectors…</p>}
    {catalog.error && <ErrorNotice error={catalog.error} kind="Connector" />}
    {catalog.error && catalog.data && <p className="stale">Stale connector catalog — refresh failed.</p>}
    {!name && <p className="description connectors-intro">A connector is a kind of access a cell needs: the catalog is built into kuke and read-only here. An identity is the stored credential that fills it; identities are listed, created and set as defaults on the <a href={identitiesLink()}>Identities</a> page.</p>}
    {catalog.data && !name && <ConnectorList connectors={listedConnectors(catalog.data)} counts={counts} />}
    {catalog.data && name && !connector && <div className="error" role="alert"><strong>Unknown connector</strong><p>No connector named {name} is defined. <a href={connectorCatalogLink()}>Back to connectors</a></p></div>}
    {connector && <ConnectorDetail key={connector.name} connector={connector} counts={counts} />}
  </>;
}
