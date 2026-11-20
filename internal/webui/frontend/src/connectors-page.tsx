import { useEffect, useState } from "react";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import type { Doc, InventoryResource } from "./inventory";
import { CatalogDeleteConfirmation } from "./catalog";
import { catalogDeleteAPI } from "./catalog-url.mjs";
import { RegistryCredentialsPanel } from "./registry-credentials";
import { IdentityForm, IdentitySelect, ScopePicker, connectorAPI, connectorsLink, levelLabel, sendJSON, toFailure } from "./connector-ui";
import {
  allowedLevels, connectorLevels, defaultRow, defaultScopeName, defaultTargets, deliveryEnv, deliveryFile, deliveryGit, deliveryRegistry, deliveryStateVolume, identitiesFor, isRegistry,
  loginMethods, refKey, refLabel, refScope, registryEntries, scopeAt, scopeFromParts, scopeLevel, scopeLevels, scopeParts, scopeQuery, secretRef, targetScope, usageFor,
} from "./connectors.mjs";
import type { Connector, ConnectorDefault, ConnectorUsage, DefaultTarget, Identity, Scope, SecretRef } from "./connectors.mjs";

// The Connectors page (#1548, §7). It iterates GET /api/connectors: a
// connector added as one connector.yaml is listed, given identities and
// defaulted here with no change to this file.

type Delivered = { delivery: string; envName?: string; mountPath?: string };
// deliveryText describes a delivery handler — the closed set the UI may branch on.
function deliveryText(item: Delivered): string {
  return item.delivery === deliveryEnv ? `environment variable ${item.envName ?? ""}`.trim()
    : item.delivery === deliveryFile ? `file ${item.mountPath ?? ""}`.trim()
    : item.delivery === deliveryStateVolume ? "login kept in the harness state volume (no identity)"
    : item.delivery === deliveryGit ? "the cell's git identity (author, committer, credentials, signing)"
    : item.delivery === deliveryRegistry ? "image pulls of a Realm"
    : item.delivery;
}
function identityLink(identity: SecretRef): string { return "#/secrets/" + [...refScope(identity), identity.name].map(encodeURIComponent).join("/"); }
function configLink(config: { name: string; realm?: string; space?: string; stack?: string }): string {
  return "#/configs/" + [config.realm, config.space, config.stack, config.name].filter((value): value is string => !!value).map(encodeURIComponent).join("/");
}
function firstRealm(inventory: InventoryResource): string {
  const realms = inventory.data?.rows[0] ?? [];
  return (realms.find(row => row.scope[0] === defaultScopeName) ?? realms[0])?.scope[0] ?? "";
}
// useScope holds a scope picker's value, starting on the linked scope or, once
// the inventory arrives, on a Realm.
function useScope(inventory: InventoryResource, initial: Scope | undefined): [Scope, (scope: Scope) => void] {
  const [scope, setScope] = useState<Scope>(initial ?? { realm: "" });
  const fallback = firstRealm(inventory);
  useEffect(() => { if (!scope.realm && fallback) setScope({ realm: fallback }); }, [scope.realm, fallback]);
  return [scope, setScope];
}

function ConnectorList({ connectors, usage, secrets }: { connectors: Connector[]; usage: ConnectorUsage[] | undefined; secrets: unknown[] | undefined }) {
  return <section className="panel" aria-label="Connectors list">
    <div className="table-scroll"><table><thead><tr><th>Connector</th><th>Delivered as</th><th>Identities</th><th>Configs requiring it</th><th>Configs without an identity</th></tr></thead><tbody>
      {connectors.map(connector => {
        const { configs, missing } = usageFor(usage, connector.name);
        return <tr key={connector.name}>
          <td><a className="resource-name" href={connectorsLink(connector.name)}>{connector.title || connector.name}</a> <span className="badge">{connector.name}</span>{connector.description && <p className="description">{connector.description}</p>}</td>
          <td>{connector.methods.length ? connector.methods.map(method => <div key={method.name}><code>{method.name}</code> — {deliveryText(method)}</div>) : deliveryText({ delivery: connector.delivery ?? "" })}</td>
          <td>{secrets ? identitiesFor(secrets, connector.name).length : "—"}</td>
          <td>{usage ? configs : "—"}</td>
          <td>{!usage ? "—" : missing.length ? <><strong>{missing.length}</strong> <span className="badge needs-connector">Needs a default</span> <a className="document-action" href={connectorsLink(connector.name, targetScope(missing[0].config))} aria-label={`Set a default for ${connector.title || connector.name}`}>Set a default</a></> : "0"}</td>
        </tr>;
      })}
    </tbody></table></div>
    {!connectors.length && <div className="empty-state"><h3>No connectors are defined</h3></div>}
  </section>;
}

// IdentitiesPanel lists a connector's identities — the Secrets tagged with it —
// and creates, rotates and deletes them.
function IdentitiesPanel({ connector, identities, secrets, secretsError, revision, inventory, initialScope, onChanged }: {
  connector: Connector;
  identities: Identity[];
  secrets: unknown[] | undefined;
  secretsError: Failure | undefined;
  revision: number;
  inventory: InventoryResource;
  initialScope: Scope | undefined;
  onChanged: () => void;
}) {
  const title = connector.title || connector.name;
  const registry = isRegistry(connector);
  const [store, setStore] = useScope(inventory, initialScope && scopeAt(initialScope, scopeLevels[0]));
  const [rotating, setRotating] = useState<Identity>();
  const [confirming, setConfirming] = useState<Identity>();
  const [pending, setPending] = useState("");
  const [failure, setFailure] = useState<{ target: string; message: string }>();
  const { notify } = useNotify();
  const levels = connectorLevels(connector);
  const storable = !!store.realm && levels.includes(scopeLevel(store));
  const fieldNames = connector.fields.map(field => field.name);

  async function remove(identity: Identity) {
    const ref = secretRef(identity)!;
    const key = refKey(ref);
    if (pending) return;
    setPending(key); setFailure(undefined);
    try {
      await sendJSON(catalogDeleteAPI("secrets", [...refScope(ref), ref.name]), "{}");
      notify({ key: `identity-delete:${key}`, tone: "success", message: `${title} identity ${ref.name} deleted.` });
    } catch (caught) { setFailure({ target: refLabel(ref), message: toFailure(caught).message }); }
    finally { setPending(""); onChanged(); }
  }

  return <section className="panel connector-identities" aria-label={`${title} identities`}>
    <div className="panel-heading"><h2>Identities</h2><span className="badge">{identities.length} stored</span></div>
    <div className="panel-body">
      <p className="description">An identity is a Secret tagged <code>{connector.name}</code>. Only its name, scope, type and readable fields are shown: values are write-only and never returned to the browser.</p>
      {secretsError && <p className="stale">Identities could not be listed: {secretsError.message}</p>}
      {failure && <div className="error" role="alert"><strong>Delete failed: {failure.target}</strong><p>{failure.message}</p></div>}
      {identities.length ? <div className="table-scroll"><table aria-label={`${title} identities`}><thead><tr><th scope="col">Name</th><th scope="col">Scope</th><th scope="col">Type</th><th scope="col">Identity</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {identities.map(identity => <tr key={refKey(identity)}>
          <td><a className="resource-name" href={identityLink(identity)}>{identity.name}</a></td>
          <td className="scope-cell">{refScope(identity).join(" / ")}</td>
          <td>{identity.type}</td>
          <td>{[...fieldNames.filter(name => name in identity.identity), ...Object.keys(identity.identity).filter(name => !fieldNames.includes(name))].map(name => <span className="badge label-badge" key={name}>{name}: {identity.identity[name]}</span>)}{!Object.keys(identity.identity).length && <span className="description">—</span>}</td>
          <td className="connector-row-actions">
            <button type="button" className="subtle-button" aria-label={`Rotate ${title} identity ${refLabel(identity)}`} disabled={!!pending} onClick={() => setRotating(identity)}>Rotate</button>
            <button type="button" className="subtle-button danger-action" aria-label={`Delete ${title} identity ${refLabel(identity)}`} disabled={!!pending} onClick={() => setConfirming(identity)}>{pending === refKey(identity) ? "Deleting…" : "Delete"}</button>
          </td>
        </tr>)}
      </tbody></table></div> : <p>No {title} identities are stored.</p>}
      {rotating && <IdentityForm key={`rotate:${refKey(rotating)}`} connector={connector} scope={secretRef(rotating)!} existing={rotating} secrets={secrets} onSaved={() => { setRotating(undefined); onChanged(); }} onCancel={() => setRotating(undefined)} />}
      {registry ? <p className="description">A registry identity is created together with the registry entry that uses it, below.</p> : <>
        <div className="connector-store"><h3>Where to store a new identity</h3><ScopePicker label="Identity" inventory={inventory} levels={levels} value={store} onChange={setStore} />
          {store.realm && !storable && <p className="field-error" role="alert">{title} identities are stored at {levels.join(" or ")} level.</p>}</div>
        <IdentityForm key={`new:${scopeParts(store).join("/")}`} connector={connector} scope={store} secrets={secrets} onSaved={() => onChanged()} />
      </>}
    </div>
    {confirming && <CatalogDeleteConfirmation kind="secrets" doc={{ metadata: { name: confirming.name, realm: confirming.realm, ...(confirming.space ? { space: confirming.space } : {}), ...(confirming.stack ? { stack: confirming.stack } : {}) } }} revision={revision} inventory={inventory} onCancel={() => setConfirming(undefined)} onConfirm={() => { const identity = confirming; setConfirming(undefined); void remove(identity); }} />}
  </section>;
}

// DefaultTargetEditor shows what each level of a scope sets as the default of
// one connector method, which one wins, and sets or clears a level.
function DefaultTargetEditor({ connector, target, scope, row, identities, onChanged }: { connector: Connector; target: DefaultTarget; scope: Scope; row: ConnectorDefault | undefined; identities: Identity[]; onChanged: () => void }) {
  const title = connector.title || connector.name;
  const name = target.method ? `${title} ${target.method}` : title;
  const allowed = allowedLevels(connector, scope);
  const shown = scopeLevels.filter(level => scopeLevel(scopeAt(scope, level)) === level);
  const [level, setLevel] = useState("");
  const [choice, setChoice] = useState<Partial<SecretRef>>();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const { notify } = useNotify();
  // Default to the deepest level the scope names: the one the picker points at.
  const picked = allowed.includes(level) ? level : allowed.at(-1) ?? "";
  const ref = secretRef(choice);
  async function save(at: string, identity: SecretRef | null) {
    if (pending || !at) return;
    setPending(true); setError(undefined);
    try {
      await sendJSON(connectorAPI(connector.name, "defaults"), { scope: scopeAt(scope, at), ...(target.method ? { method: target.method } : {}), identity });
      notify({ key: `connector-default:${connector.name}:${target.method}`, tone: "success", message: identity ? `${name} default set to ${identity.name} at ${levelLabel(at, scope)}.` : `${name} default cleared at ${levelLabel(at, scope)}.` });
      setChoice(undefined);
      onChanged();
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(false); }
  }
  return <div className="connector-default" role="group" aria-label={`${name} default`}>
    <h3>{target.method ? <><code>{target.method}</code> method</> : "Default identity"}</h3>
    <p className="description">Delivered as {deliveryText({ delivery: target.delivery, ...connector.methods.find(method => method.name === target.method) })}.{target.description && <> {target.description}</>}</p>
    <div className="table-scroll"><table aria-label={`${name} defaults by level`}><thead><tr><th scope="col">Level</th><th scope="col">Scope</th><th scope="col">Default identity</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead><tbody>
      {shown.map(item => {
        const set = row?.levels?.[item];
        return <tr key={item}>
          <td>{item}</td>
          <td className="scope-cell">{scopeParts(scopeAt(scope, item)).join(" / ")}</td>
          <td>{set ? <><code>{set.name}</code> <span className="description">{refScope(set).join(" / ")}</span></> : allowed.includes(item) ? <span className="description">Not set</span> : <span className="description">Not available: {title} defaults are set at {connectorLevels(connector).join(" or ")} level</span>}{row?.origin === item && <> <span className="badge">Effective</span></>}</td>
          <td className="connector-row-actions">{set && allowed.includes(item) && <button type="button" className="subtle-button" aria-label={`Clear ${name} default at ${item} level`} disabled={pending} onClick={() => void save(item, null)}>Clear</button>}</td>
        </tr>;
      })}
    </tbody></table></div>
    <p className="connector-effective" role="status">{row?.effective ? <>Effective: <code>{row.effective.name}</code> <span className="description">{refScope(row.effective).join(" / ")}</span> — from the {row.origin} level.</> : <>No default in this scope. A Config here that picks no identity cannot create cells.</>}</p>
    <div className="schema-fields">
      <label className="schema-field"><span>Level</span><select aria-label={`${name} default level`} value={picked} disabled={pending || !allowed.length} onChange={event => setLevel(event.target.value)}>{allowed.map(item => <option key={item} value={item}>{levelLabel(item, scope)}</option>)}</select></label>
      <IdentitySelect label={`${name} default identity`} identities={identities} value={choice} emptyLabel="Select an identity…" disabled={pending} realm={scope.realm} onChange={setChoice} />
    </div>
    <div className="connector-actions"><button type="button" disabled={!ref || !picked || pending} onClick={() => void save(picked, ref ?? null)}>{pending ? "Saving…" : "Set default"}</button></div>
    {error && <ErrorNotice error={error} kind="Scope" />}
  </div>;
}

function DefaultsPanel({ connector, identities, revision, inventory, initialScope, onChanged }: { connector: Connector; identities: Identity[]; revision: number; inventory: InventoryResource; initialScope: Scope | undefined; onChanged: () => void }) {
  const title = connector.title || connector.name;
  const targets = defaultTargets(connector);
  const [scope, setScope] = useScope(inventory, initialScope);
  const defaults = useResource<ConnectorDefault[]>(scope.realm && targets.length ? `/api/connectors/defaults?${scopeQuery(scope)}` : null, revision);
  if (!targets.length) return null;
  const levels = connectorLevels(connector);
  return <section className="panel connector-defaults" aria-label={`${title} defaults`}>
    <div className="panel-heading"><h2>Defaults</h2><span className="badge">{levels.join(" · ")}</span></div>
    <div className="panel-body">
      <p className="description">A default fills {title} for every Config in the scope that picked no identity. The deepest level wins: Stack over Space over Realm. Changing one takes effect at once — no Config is re-rendered.</p>
      <ScopePicker label="Default" inventory={inventory} levels={levels} value={scope} onChange={setScope} />
      {!scope.realm ? <p className="description">Choose a Realm to see and set its defaults.</p> : <>
        {defaults.loading && !defaults.data && <p role="status">Loading defaults…</p>}
        {defaults.error && <ErrorNotice error={defaults.error} kind="Scope" />}
        {defaults.error && defaults.data && <p className="stale">Showing stale defaults — refresh failed.</p>}
        {targets.map(target => <DefaultTargetEditor key={`${target.method}:${scopeParts(scope).join("/")}`} connector={connector} target={target} scope={scope} row={defaultRow(defaults.data, { connector: connector.name, component: target.component, method: target.method })} identities={identities} onChanged={onChanged} />)}
      </>}
    </div>
  </section>;
}

// RegistryPanel is the registry-delivered connector: Realm only. It lists the
// Realm's registry entries and saves Secret-backed ones through the same form
// the Realm page uses.
function RegistryPanel({ connector, revision, inventory, initialScope, onChanged }: { connector: Connector; revision: number; inventory: InventoryResource; initialScope: Scope | undefined; onChanged: () => void }) {
  const title = connector.title || connector.name;
  const [scope, setScope] = useScope(inventory, initialScope && scopeAt(initialScope, scopeLevels[0]));
  const realm = useResource<Doc>(scope.realm ? `/api/realms/${encodeURIComponent(scope.realm)}` : null, revision);
  return <>
    <section className="panel" aria-label={`${title} realm`}>
      <div className="panel-heading"><h2>Realm</h2><span className="badge">Realm only</span></div>
      <div className="panel-body">
        <p className="description">A registry credential belongs to one Realm: it authenticates that Realm's image pulls and is bound to a registry server. It is never part of a Config or a cell.</p>
        <ScopePicker label="Registry" inventory={inventory} levels={[scopeLevels[0]]} value={scope} onChange={setScope} />
        {realm.loading && !realm.data && scope.realm && <p role="status">Loading the Realm's registry entries…</p>}
        {realm.error && <ErrorNotice error={realm.error} />}
      </div>
    </section>
    {scope.realm && realm.data && <RegistryCredentialsPanel realm={scope.realm} credentials={registryEntries(realm.data).map(entry => ({ serverAddress: entry.server || undefined, username: entry.username || undefined, secretRef: entry.secret }))} onSaved={onChanged} />}
  </>;
}

function ConnectorDetail({ connector, usage, secrets, secretsError, target, revision, inventory, onChanged }: {
  connector: Connector;
  usage: ConnectorUsage[] | undefined;
  secrets: unknown[] | undefined;
  secretsError: Failure | undefined;
  target: Scope | undefined;
  revision: number;
  inventory: InventoryResource;
  onChanged: () => void;
}) {
  const title = connector.title || connector.name;
  const { configs, missing } = usageFor(usage, connector.name);
  const identities = identitiesFor(secrets, connector.name);
  const registry = isRegistry(connector);
  const storesIdentity = registry || defaultTargets(connector).length > 0;
  const logins = loginMethods(connector);
  return <>
    <div className="detail-actions"><a href={connectorsLink()}>← Back to connectors</a></div>
    <section className="panel" aria-label={`${title} connector`}>
      <div className="panel-heading"><h2>{title}</h2>{missing.length ? <span className="badge needs-connector">Needs a default</span> : <span className="badge">{connector.name}</span>}</div>
      <div className="panel-body">
        {connector.description && <p>{connector.description}</p>}
        <dl className="details-grid">
          <dt>Name</dt><dd><code>{connector.name}</code></dd>
          <dt>Delivered as</dt><dd>{connector.methods.length ? <ul className="connector-methods">{connector.methods.map(method => <li key={method.name}><code>{method.name}</code>{method.default && <> <span className="badge">default method</span></>} — {deliveryText(method)}{method.description && <span className="description"> {method.description}</span>}</li>)}</ul> : deliveryText({ delivery: connector.delivery ?? "" })}</dd>
          <dt>Identity</dt><dd>{storesIdentity ? <>{connector.secretTypes.join(" or ")} Secret{connector.fields.length > 0 && <> with {connector.fields.map(field => field.name).join(", ")}</>}</> : "None — the login is kept in the harness state volume"}</dd>
          <dt>Default scopes</dt><dd>{connectorLevels(connector).join(", ")}</dd>
          {connector.harness && <><dt>Harness</dt><dd>{connector.harness}</dd></>}
          {connector.group && <><dt>Method group</dt><dd>{connector.group} — a Config enables exactly one method</dd></>}
          <dt>Configs</dt><dd>{usage ? <>{configs} require it{connector.optional && " (a cell starts without it)"}</> : "Not loaded"}</dd>
        </dl>
        {logins.length > 0 && <p className="description">The {logins.map(method => method.name).join(", ")} method has no identity: log in once from an interactive cell and the login stays in that role's state volume.</p>}
      </div>
    </section>
    {missing.length > 0 && <section className="panel connector-missing" aria-label={`Configs without a ${title} identity`}>
      <div className="panel-heading"><h2>Needs a default</h2><span className="badge needs-connector">{missing.length} {missing.length === 1 ? "Config" : "Configs"} without an identity</span></div>
      <div className="panel-body">
        <p className="description">No cell can be created from these Configs until {title} has an identity. Set a default below for their scope, or pick one in the Config.</p>
        <ul className="state-list">{missing.map(item => <li key={`${configLink(item.config)}:${item.component}`}><div><a className="resource-name" href={configLink(item.config)}>{item.config.name}</a><p className="description">{[item.config.realm, item.config.space, item.config.stack].filter(Boolean).join(" / ")}{item.method && <> · {item.method} method</>}</p>{item.message && <p className="setup-error">{item.message}</p>}</div><a className="document-action" href={connectorsLink(connector.name, targetScope(item.config))} aria-label={`Set a ${title} default for the scope of ${item.config.name}`}>Set a default for its scope</a></li>)}</ul>
      </div>
    </section>}
    {storesIdentity && <IdentitiesPanel connector={connector} identities={identities} secrets={secrets} secretsError={secretsError} revision={revision} inventory={inventory} initialScope={target} onChanged={onChanged} />}
    <DefaultsPanel key={`defaults:${scopeParts(target).join("/")}`} connector={connector} identities={identities} revision={revision} inventory={inventory} initialScope={target} onChanged={onChanged} />
    {registry && <RegistryPanel connector={connector} revision={revision} inventory={inventory} initialScope={target} onChanged={onChanged} />}
  </>;
}

export function ConnectorsPage({ name, target, revision, inventory, onChanged }: { name?: string; target?: string[]; revision: number; inventory: InventoryResource; onChanged: () => void }) {
  const catalog = useResource<Connector[]>("/api/connectors", revision);
  const usage = useResource<ConnectorUsage[]>("/api/connectors/status", revision);
  const secrets = useResource<unknown[]>("/api/catalog/secrets", revision);
  const connector = name ? catalog.data?.find(item => item.name === name) : undefined;
  const scope: Scope | undefined = target?.length ? scopeFromParts(target) : undefined;
  return <>
    {catalog.loading && !catalog.data && <p role="status">Loading connectors…</p>}
    {catalog.error && <ErrorNotice error={catalog.error} kind="Connector" />}
    {catalog.error && catalog.data && <p className="stale">Stale connector catalog — refresh failed.</p>}
    {usage.error && <p className="stale">Config usage could not be loaded: {usage.error.message}</p>}
    {!name && secrets.error && <p className="stale">Identities could not be listed: {secrets.error.message}</p>}
    {!name && <p className="description connectors-intro">A connector is a kind of access a cell needs. An identity is the stored credential that fills it — chosen per cell or per Config, or inherited from a Realm, Space or Stack default.</p>}
    {catalog.data && !name && <ConnectorList connectors={catalog.data} usage={usage.data} secrets={secrets.data} />}
    {catalog.data && name && !connector && <div className="error" role="alert"><strong>Unknown connector</strong><p>No connector named {name} is defined. <a href={connectorsLink()}>Back to connectors</a></p></div>}
    {connector && <ConnectorDetail key={connector.name} connector={connector} usage={usage.data} secrets={secrets.data} secretsError={secrets.error} target={scope} revision={revision} inventory={inventory} onChanged={onChanged} />}
  </>;
}
