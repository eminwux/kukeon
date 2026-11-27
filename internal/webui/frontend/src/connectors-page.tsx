import { useEffect, useState, type FormEvent } from "react";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import type { InventoryResource } from "./inventory";
import { imagesLink } from "./routes";
import { CatalogDeleteConfirmation } from "./catalog";
import { catalogDeleteAPI } from "./catalog-url.mjs";
import { IdentityForm, IdentitySelect, LoginButton, LoginStateBadge, ScopeGroup, ScopePicker, ScopeRadio, connectorAPI, connectorsLink, levelLabel, newIdentityLink, rotateIdentityLink, sendJSON, startLogin, toFailure, volumeLink } from "./connector-ui";
import { Disclosure } from "./disclosure";
import { connectorSetUp, notSetUpText } from "./onboarding.mjs";
import {
  allowedLevels, connectorLevels, defaultRow, defaultScopeName, defaultTargets, deliveryEnv, deliveryFile, deliveryGit, deliveryRegistry, deliveryStateVolume, identitiesFor, importPayload, importsFromHost,
  isRegistry, listedConnectors, loginBody, tokenConnectors, tokenForGit, loginIdentitiesFor, loginMethods, loginNameError, refKey, refLabel, refScope, scopeAt, scopeFromParts, scopeLevel, scopeLevels, scopeParts, scopeQuery, secretRef, targetScope, usageFor,
} from "./connectors.mjs";
import type { Connector, ConnectorDefault, ConnectorUsage, DefaultTarget, Identity, ImportForm, ImportResult, LoginIdentity, Scope, SecretRef } from "./connectors.mjs";

// The Connectors page (#1548, §7). It iterates GET /api/connectors: a
// connector added as one connector.yaml is listed, given identities and
// defaulted here with no change to this file.

type Delivered = { delivery: string; envName?: string; mountPath?: string };
// deliveryText describes a delivery handler — the closed set the UI may branch on.
function deliveryText(item: Delivered): string {
  return item.delivery === deliveryEnv ? `environment variable ${item.envName ?? ""}`.trim()
    : item.delivery === deliveryFile ? `file ${item.mountPath ?? ""}`.trim()
    : item.delivery === deliveryStateVolume ? "login kept in a login identity (a Volume mounted as the harness state directory)"
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

function ConnectorList({ connectors, usage, secrets, logins }: { connectors: Connector[]; usage: ConnectorUsage[] | undefined; secrets: unknown[] | undefined; logins: unknown[] | undefined }) {
  return <section className="panel" aria-label="Connectors list">
    <div className="table-scroll"><table><thead><tr><th>Connector</th><th>Delivered as</th><th>Identities</th><th>Configs requiring it</th><th>Configs without an identity</th></tr></thead><tbody>
      {connectors.map(connector => {
        const { configs, missing } = usageFor(usage, connector.name);
        return <tr key={connector.name}>
          <td><a className="resource-name" href={connectorsLink(connector.name)}>{connector.title || connector.name}</a> <span className="badge">{connector.name}</span>{connector.description && <p className="description">{connector.description}</p>}</td>
          <td>{connector.methods.length ? connector.methods.map(method => <div key={method.name}><code>{method.name}</code> — {deliveryText(method)}</div>) : deliveryText({ delivery: connector.delivery ?? "" })}</td>
          <td>{secrets ? identitiesFor(secrets, connector.name).length + loginIdentitiesFor(logins, connector.name).length : "—"}{secrets && logins && !connectorSetUp(connector, secrets, logins) && <> <span className="badge not-set-up" title={notSetUpText(connector)}>Not set up — recommended</span></>}</td>
          <td>{usage ? configs : "—"}</td>
          <td>{!usage ? "—" : missing.length ? <><strong>{missing.length}</strong> <span className="badge needs-connector">Needs a default</span> <a className="document-action" href={connectorsLink(connector.name, targetScope(missing[0].config))} aria-label={`Set a default for ${connector.title || connector.name}`}>Set a default</a></> : "0"}</td>
        </tr>;
      })}
    </tbody></table></div>
    {!connectors.length && <div className="empty-state"><h3>No connectors are defined</h3></div>}
  </section>;
}

// ImportFromHost runs the server's import of a git-delivered connector's
// identity (#1591): the SSH key and git configuration of the account running
// `kuke serve`, stored as one identity — the same import as
// `kuke connector import git`. Only paths and readable fields come back.
function ImportFromHost({ connector, inventory, initiallyOpen = false, onChanged }: { connector: Connector; inventory: InventoryResource; initiallyOpen?: boolean; onChanged: () => void }) {
  const title = connector.title || connector.name;
  const [open, setOpen] = useState(initiallyOpen);
  const [scope, setScope] = useScope(inventory, undefined);
  const [form, setForm] = useState<Omit<ImportForm, "realm">>({ hosts: "", name: "", key: "", passphraseFile: "", makeDefault: true });
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const [result, setResult] = useState<ImportResult>();
  const { notify } = useNotify();
  const set = (next: Partial<typeof form>) => { setForm(old => ({ ...old, ...next })); setError(undefined); };
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    setPending(true); setError(undefined); setResult(undefined);
    try {
      const imported = await sendJSON<ImportResult>(connectorAPI(connector.name, "import"), importPayload({ ...form, realm: scope.realm }));
      setResult(imported);
      const stored = imported.secrets.find(secret => secret.name === imported.name)?.action === "updated" ? "updated" : "imported";
      notify({ key: `identity-import:${connector.name}`, tone: "success", message: `${title} identity ${imported.name} ${stored} in ${imported.realm}${imported.default ? `; it is now the ${imported.realm} default` : ""}.` });
      onChanged();
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(false); }
  }
  return <div className="connector-import">
    <div className="connector-actions"><button type="button" className="subtle-button" aria-expanded={open} onClick={() => setOpen(value => !value)}>{open ? "Hide import" : "Import from host"}</button></div>
    {open && <form className="creation-form identity-form" aria-label={`Import ${title} identity from host`} method="post" autoComplete="off" onSubmit={submit}>
      <h3>Import from this machine</h3>
      <p className="description">Reads the SSH key and global git configuration of the account running <code>kuke serve</code> and stores them as one {title} identity: an SSH key identity when a key is found, else an author-only one. Rerunning updates it in place. Key material goes to the daemon only — it is never shown here.</p>
      <ScopePicker label="Import" inventory={inventory} levels={[scopeLevels[0]]} value={scope} disabled={pending} onChange={setScope} />
      <div className="schema-fields">
        <label className="schema-field"><span>Hosts</span><input value={form.hosts} placeholder="github.com" disabled={pending} autoComplete="off" spellCheck={false} onChange={event => set({ hosts: event.target.value })} /><small>Comma-separated hosts or URL prefixes the key authenticates.</small></label>
        <label className="schema-field"><span>Identity name</span><input value={form.name} placeholder="git-ssh, or git-author without a key" disabled={pending} autoComplete="off" spellCheck={false} onChange={event => set({ name: event.target.value })} /></label>
        <label className="schema-field"><span>Key</span><input value={form.key} placeholder=".ssh/id_ed25519" disabled={pending} autoComplete="off" spellCheck={false} onChange={event => set({ key: event.target.value })} /><small>A file in the server account's home directory. Empty: the key git signs with, else id_ed25519, id_ecdsa, id_rsa.</small></label>
        <label className="schema-field"><span>Passphrase file</span><input value={form.passphraseFile} placeholder=".ssh/passphrase" disabled={pending} autoComplete="off" spellCheck={false} onChange={event => set({ passphraseFile: event.target.value })} /><small>For a passphrase-protected key: a file in the server account's home directory.</small></label>
        <label className="schema-check"><input type="checkbox" checked={form.makeDefault} disabled={pending} onChange={event => set({ makeDefault: event.target.checked })} />Make it the Realm default</label>
      </div>
      <div className="connector-actions"><button type="submit" disabled={pending || !scope.realm}>{pending ? "Importing…" : "Import identity"}</button></div>
      {error && <ErrorNotice error={error} kind="Identity" />}
      {result && <div className="connector-import-result" role="status" aria-label="Imported identity">
        <dl className="details-grid">
          <dt>Identity</dt><dd><code>{result.name}</code> <span className="description">{result.realm}</span> <span className="badge">{result.type}</span></dd>
          <dt>Read from</dt><dd>{result.home}{result.keyPath ? <> — key <code>{result.keyPath}</code></> : <> — no SSH key: author-only</>}</dd>
          {Object.keys(result.identity).length > 0 && <><dt>Fields</dt><dd>{Object.entries(result.identity).map(([name, value]) => <span className="badge label-badge" key={name}>{name}: {value}</span>)}</dd></>}
          {result.default && <><dt>Realm default</dt><dd>{result.default.identity?.name}{result.default.previous && result.default.previous.name !== result.default.identity?.name && <> (replaces <code>{result.default.previous.name}</code>)</>}</dd></>}
        </dl>
        {!!result.notes?.length && <ul className="description">{result.notes.map(note => <li key={note}>{note}</li>)}</ul>}
      </div>}
    </form>}
  </div>;
}

// IdentitiesPanel lists a connector's identities — the Secrets tagged with it —
// and deletes them. Creating and rotating one are pages of their own.
function IdentitiesPanel({ connector, identities, secretsError, revision, inventory, target, onChanged }: {
  connector: Connector;
  identities: Identity[];
  secretsError: Failure | undefined;
  revision: number;
  inventory: InventoryResource;
  target: Scope | undefined;
  onChanged: () => void;
}) {
  const title = connector.title || connector.name;
  const registry = isRegistry(connector);
  const [confirming, setConfirming] = useState<Identity>();
  const [pending, setPending] = useState("");
  const [failure, setFailure] = useState<{ target: string; message: string }>();
  const { notify } = useNotify();
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
    <div className="panel-heading"><h2>Identities</h2><span className="badge">{identities.length} stored</span>{!registry && <a className="document-action" href={newIdentityLink(connector.name, target)}>New identity</a>}</div>
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
            <a className="subtle-button" href={rotateIdentityLink(connector.name, identity, target)} aria-label={`Rotate ${title} identity ${refLabel(identity)}`}>Rotate</a>
            <button type="button" className="subtle-button danger-action" aria-label={`Delete ${title} identity ${refLabel(identity)}`} disabled={!!pending} onClick={() => setConfirming(identity)}>{pending === refKey(identity) ? "Deleting…" : "Delete"}</button>
          </td>
        </tr>)}
      </tbody></table></div> : <p>No {title} identities are stored.{!registry && <> <a href={newIdentityLink(connector.name, target)}>Create one</a>.</>}</p>}
    </div>
    {confirming && <CatalogDeleteConfirmation kind="secrets" doc={{ metadata: { name: confirming.name, realm: confirming.realm, ...(confirming.space ? { space: confirming.space } : {}), ...(confirming.stack ? { stack: confirming.stack } : {}) } }} revision={revision} inventory={inventory} onCancel={() => setConfirming(undefined)} onConfirm={() => { const identity = confirming; setConfirming(undefined); void remove(identity); }} />}
  </section>;
}

// LoginIdentitiesPanel lists a connector's login identities (#1685) — the
// Volumes that hold a harness login — with whether each holds one and the
// Configs using it, creates one, and logs in to one: "Log in" starts an
// identity cell that mounts only that Volume and opens its terminal. A team's
// state volume is listed with the same action. Nothing of a login is shown.
function LoginIdentitiesPanel({ connector, logins, loginsError, inventory, target, onChanged }: {
  connector: Connector;
  logins: LoginIdentity[];
  loginsError: Failure | undefined;
  inventory: InventoryResource;
  target: Scope | undefined;
  onChanged: () => void;
}) {
  const title = connector.title || connector.name;
  const methods = loginMethods(connector);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [method, setMethod] = useState("");
  const [scope, setScope] = useScope(inventory, target && scopeAt(target, scopeLevels[0]));
  const [pending, setPending] = useState("");
  const [error, setError] = useState<Failure>();
  const { notify } = useNotify();
  const problem = loginNameError(name);
  const taken = logins.some(item => refKey(item.volume) === refKey({ name: name.trim(), ...scope }));
  async function create(event: FormEvent, login: boolean) {
    event.preventDefault();
    if (pending) return;
    if (problem || !scope.realm) { setError({ code: "invalid_request", message: problem || "Choose where the login identity is stored." }); return; }
    setPending(login ? "login" : "create"); setError(undefined);
    try {
      if (login) await startLogin(connector.name, { name: name.trim(), ...scope }, method || undefined);
      else {
        await sendJSON(connectorAPI(connector.name, "logins"), loginBody(name, scope, method || undefined));
        notify({ key: `login-identity:${connector.name}:${name.trim()}`, tone: "success", message: `${title} login identity ${name.trim()} created in ${scopeParts(scope).join(" / ")}.` });
        setName(""); setCreating(false);
      }
      onChanged();
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(""); }
  }
  return <section className="panel connector-logins" aria-label={`${title} login identities`}>
    <div className="panel-heading"><h2>Login identities</h2><span className="badge">{logins.length} stored</span><button type="button" className="document-action" aria-expanded={creating} onClick={() => { setCreating(value => !value); setError(undefined); }}>{creating ? "Hide new login identity" : "New login identity"}</button></div>
    <div className="panel-body">
      <p className="description">A login identity is a Volume tagged <code>{connector.name}</code>: the harness keeps its login in its state directory, and that directory is the Volume. <strong>Log in</strong> opens a short-lived identity cell that mounts only that Volume and runs the {title} login; the cell is deleted when the login ends. Only whether a login is present is shown — never the login itself. One login identity serves one role.</p>
      {loginsError && <p className="stale">Login identities could not be listed: {loginsError.message}</p>}
      {creating && <form className="creation-form identity-form" aria-label={`New ${title} login identity`} method="post" autoComplete="off" onSubmit={event => void create(event, false)}>
        <h3>New {title} login identity</h3>
        <ScopeGroup title="Login identity scope" description={<>Where the Volume is stored: a Realm, or a Space or Stack of it.</>}><ScopePicker label="Login identity" inventory={inventory} levels={scopeLevels} value={scope} disabled={!!pending} onChange={setScope} /></ScopeGroup>
        <div className="schema-fields">
          <label className="schema-field"><span>Login identity name<b aria-label="required"> *</b></span><input value={name} placeholder="personal" disabled={!!pending} autoComplete="off" spellCheck={false} aria-invalid={(!!name && !!problem) || undefined} onChange={event => { setName(event.target.value); setError(undefined); }} /><small>{taken ? "A login identity of this name already exists here: logging in logs in to it again." : "The Volume's name. It is created retained, so it outlives a purge of its scope."}</small></label>
          {methods.length > 1 && <label className="schema-field"><span>Login method</span><select aria-label={`${title} login method`} value={method} disabled={!!pending} onChange={event => setMethod(event.target.value)}><option value="">Default</option>{methods.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}</select></label>}
        </div>
        <div className="connector-actions">
          <button type="button" disabled={!!pending || !scope.realm || !name.trim()} onClick={event => void create(event, true)}>{pending === "login" ? "Starting…" : "Create and log in"}</button>
          <button type="submit" className="subtle-button" disabled={!!pending || !scope.realm || !name.trim()}>{pending === "create" ? "Creating…" : "Create without logging in"}</button>
          <button type="button" className="subtle-button" disabled={!!pending} onClick={() => setCreating(false)}>Cancel</button>
        </div>
        {error && <ErrorNotice error={error} kind="Identity" />}
      </form>}
      {logins.length ? <div className="table-scroll"><table aria-label={`${title} login identities`}><thead><tr><th scope="col">Name</th><th scope="col">Scope</th><th scope="col">Status</th><th scope="col">Used by</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {logins.map(login => <tr key={refKey(login.volume)}>
          <td>{login.exists ? <a className="resource-name" href={volumeLink(login.volume)}>{login.volume.name}</a> : <span className="resource-name">{login.volume.name}</span>}{!login.exists && <> <span className="badge">Not created yet</span></>}{login.session && <> <span className="badge">Login running</span></>}</td>
          <td className="scope-cell">{refScope(login.volume).join(" / ")}</td>
          <td><LoginStateBadge state={login.state} />{login.since && <span className="description"> since {new Date(login.since).toLocaleString()}</span>}</td>
          <td>{login.usedBy.length ? login.usedBy.map(config => <a className="badge label-badge" key={configLink(config)} href={configLink(config)}>{config.name}</a>) : <span className="description">—</span>}</td>
          <td className="connector-row-actions"><LoginButton connector={connector.name} title={title} volume={login.volume} state={login.state} running={!!login.session} method={login.method || undefined} onStarted={onChanged} /></td>
        </tr>)}
      </tbody></table></div> : <p>No {title} login identities are stored.{!creating && <> <button type="button" className="subtle-button" onClick={() => setCreating(true)}>Create one</button>.</>}</p>}
    </div>
  </section>;
}

// IdentityPage is #/connectors/<name>/new-identity — where the identity is
// stored, its form and, for a connector that imports from the host, the import
// — or #/connectors/<name>/rotate-identity, the rotate form of one identity.
// Saving or cancelling returns to the connector page.
// The ways the New identity page of a git-delivered connector starts (#1693).
const choiceImport = "import";
const choiceManual = "manual";

// IdentityChoices are the three ways to add an identity of a git-delivered
// connector, as cards: discover it on the server's host, use a token another
// connector can store for git (the GitHub token), or enter a key or password.
function IdentityChoices({ connector, tokenConnector, target, choice, onChoose }: { connector: Connector; tokenConnector: Connector | undefined; target: Scope | undefined; choice: string; onChoose: (choice: string) => void }) {
  const title = connector.title || connector.name;
  const tokenTitle = tokenConnector ? tokenConnector.title || tokenConnector.name : "";
  const card = (id: string, heading: string, text: string) => <button type="button" className={`identity-choice${choice === id ? " is-selected" : ""}`} aria-pressed={choice === id} onClick={() => onChoose(id)}><strong>{heading}</strong><span className="description">{text}</span></button>;
  return <div className="identity-choices" role="group" aria-label={`How to add a ${title} identity`}>
    {card(choiceImport, "Import from this machine", "The SSH key and git author of the account running kuke serve: clone, push and sign.")}
    {tokenConnector && <a className="identity-choice" href={newIdentityLink(tokenConnector.name, target)}><strong>Use a {tokenTitle} token</strong><span className="description">One personal access token for the {tokenTitle} API and for git over HTTPS on {tokenForGit(tokenConnector)?.hosts.join(", ")}. No signing.</span></a>}
    {card(choiceManual, "Enter a key or password", "Paste an SSH key, a username with a password or token, or store an author only.")}
  </div>;
}

function IdentityPage({ connector, connectors, action, identity, target, identities, secrets, secretsLoading, inventory, onChanged }: {
  connector: Connector;
  connectors: Connector[];
  action: string;
  identity: string[] | undefined;
  target: Scope | undefined;
  identities: Identity[];
  secrets: unknown[] | undefined;
  secretsLoading: boolean;
  inventory: InventoryResource;
  onChanged: () => void;
}) {
  const title = connector.title || connector.name;
  const back = connectorsLink(connector.name, target);
  const leave = () => { window.location.hash = back; };
  const saved = () => { onChanged(); leave(); };
  const levels = connectorLevels(connector);
  const [store, setStore] = useScope(inventory, target && scopeAt(target, levels.includes(scopeLevels[0]) ? scopeLevels[0] : levels[0]));
  const storable = !!store.realm && levels.includes(scopeLevel(store));
  const rotate = action === "rotate-identity";
  const wanted = identity ? refKey({ name: identity.at(-1) ?? "", ...scopeFromParts(identity.slice(0, -1)) }) : "";
  const existing = rotate ? identities.find(item => refKey(item) === wanted) : undefined;
  const choices = importsFromHost(connector);
  const [choice, setChoice] = useState(choices ? "" : choiceManual);
  return <>
    <div className="detail-actions"><a href={back}>← Back to {title}</a></div>
    <section className="panel connector-identity-page" aria-label={rotate ? `Rotate ${title} identity` : `New ${title} identity`}><div className="panel-body">
      {rotate ? existing ? <IdentityForm key={`rotate:${refKey(existing)}`} connector={connector} scope={secretRef(existing)!} existing={existing} secrets={secrets} onSaved={saved} onCancel={leave} />
        : secretsLoading ? <p role="status">Loading identities…</p>
          : <div className="error" role="alert"><strong>Identity not found</strong><p>No {title} identity {identity?.at(-1) ? <code>{identity.at(-1)}</code> : ""} is stored{identity && identity.length > 1 ? <> in {identity.slice(0, -1).join(" / ")}</> : ""}. <a href={back}>Back to {title}</a></p></div>
        : <>
          {choices && <IdentityChoices connector={connector} tokenConnector={tokenConnectors(connectors).find(item => tokenForGit(item)?.connector === connector.name)} target={target} choice={choice} onChoose={setChoice} />}
          {choice === choiceManual && <>
            <ScopeRadio label="Identity" connector={connector} inventory={inventory} value={store} onChange={setStore} />
            {store.realm && !storable && <p className="field-error" role="alert">{title} identities are stored at {levels.join(" or ")} level.</p>}
            <IdentityForm key={`new:${scopeParts(store).join("/")}`} connector={connector} scope={store} secrets={secrets} onSaved={saved} onCancel={leave} />
          </>}
          {choice === choiceImport && <ImportFromHost connector={connector} inventory={inventory} initiallyOpen onChanged={onChanged} />}
        </>}
    </div></section>
  </>;
}

// DefaultTargetEditor leads with the identity that resolves for one connector
// method in the chosen scope and the level it comes from, then lets the user
// choose another at any level the connector allows. The per-level table, with
// its Clear buttons, sits behind a disclosure.
function DefaultTargetEditor({ connector, target, scope, row, identities, onChanged }: { connector: Connector; target: DefaultTarget; scope: Scope; row: ConnectorDefault | undefined; identities: Identity[]; onChanged: () => void }) {
  const title = connector.title || connector.name;
  const name = target.method ? `${title} ${target.method}` : title;
  const allowed = allowedLevels(connector, scope);
  const shown = scopeLevels.filter(level => scopeLevel(scopeAt(scope, level)) === level);
  const [level, setLevel] = useState("");
  const [choice, setChoice] = useState<Partial<SecretRef>>();
  const [levelsOpen, setLevelsOpen] = useState(false);
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
  const setLevels = shown.filter(item => row?.levels?.[item]).length;
  return <div className="connector-default" role="group" aria-label={`${name} default`}>
    <h3>{target.method ? <><code>{target.method}</code> method</> : "Resolves to"}</h3>
    <p className={`connector-effective${row?.effective ? "" : " is-unresolved"}`} role="status">{row?.effective
      ? <><strong className="connector-effective-name">{row.effective.name}</strong> <span className="description">{refScope(row.effective).join(" / ")}</span> <span className="badge">{row.origin ? `${row.origin[0].toUpperCase()}${row.origin.slice(1)} default` : "Default"}</span>{row.origin && <span className="description"> — from the {levelLabel(row.origin, scope)} level</span>}</>
      : <><strong className="connector-effective-name">No default resolves in this scope.</strong> <span className="description">A Config here that picks no identity cannot create cells.</span></>}</p>
    <p className="description">Delivered as {deliveryText({ delivery: target.delivery, ...connector.methods.find(method => method.name === target.method) })}.{target.description && <> {target.description}</>}</p>
    <div className="schema-fields">
      <label className="schema-field"><span>Level</span><select aria-label={`${name} default level`} value={picked} disabled={pending || !allowed.length} onChange={event => setLevel(event.target.value)}>{allowed.map(item => <option key={item} value={item}>{levelLabel(item, scope)}</option>)}</select></label>
      {identities.length ? <IdentitySelect label={`${name} default identity`} identities={identities} value={choice} emptyLabel="Select an identity…" disabled={pending} realm={scope.realm} onChange={setChoice} />
        : <div className="schema-field"><span>Identity</span><p className="description">No {title} identity is stored. <a href={newIdentityLink(connector.name, scope)}>Create an identity</a> to set it as the default.</p></div>}
    </div>
    {identities.length > 0 && <div className="connector-actions"><button type="button" disabled={!ref || !picked || pending} onClick={() => void save(picked, ref ?? null)}>{pending ? "Saving…" : "Set default"}</button></div>}
    {error && <ErrorNotice error={error} kind="Scope" />}
    <Disclosure level={3} className="connector-default-levels" title="Defaults by level" summary={`${setLevels} of ${shown.length} set`} label={`${name} defaults by level`} open={levelsOpen} onToggle={setLevelsOpen}>
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
    </Disclosure>
  </div>;
}

function DefaultsPanel({ connector, identities, revision, inventory, initialScope, onChanged }: { connector: Connector; identities: Identity[]; revision: number; inventory: InventoryResource; initialScope: Scope | undefined; onChanged: () => void }) {
  const title = connector.title || connector.name;
  const targets = defaultTargets(connector);
  const [scope, setScope] = useScope(inventory, initialScope);
  const defaults = useResource<ConnectorDefault[]>(scope.realm && targets.length ? `/api/connectors/defaults?${scopeQuery(scope)}` : null, revision);
  if (!targets.length) return null;
  const levels = connectorLevels(connector);
  return <section className="panel connector-defaults" aria-label={`${title} default identity`}>
    <div className="panel-heading"><h2>Default identity</h2><span className="badge">{levels.join(" · ")}</span></div>
    <div className="panel-body">
      <p className="description">A default fills {title} for every Config in the scope that picked no identity. The deepest level wins: Stack over Space over Realm. Changing one takes effect at once — no Config is re-rendered.</p>
      <ScopeGroup title="Which cells?" description={<>Pick a Realm, or a Space or Stack in it, to see the identity its cells get when they pick none, and to change it.</>}><ScopePicker label="Default" inventory={inventory} levels={levels} value={scope} onChange={setScope} /></ScopeGroup>
      {!scope.realm ? <p className="description">Choose a Realm to see and set its defaults.</p> : <>
        {defaults.loading && !defaults.data && <p role="status">Loading defaults…</p>}
        {defaults.error && <ErrorNotice error={defaults.error} kind="Scope" />}
        {defaults.error && defaults.data && <p className="stale">Showing stale defaults — refresh failed.</p>}
        {targets.map(target => <DefaultTargetEditor key={`${target.method}:${scopeParts(scope).join("/")}`} connector={connector} target={target} scope={scope} row={defaultRow(defaults.data, { connector: connector.name, component: target.component, method: target.method })} identities={identities} onChanged={onChanged} />)}
      </>}
    </div>
  </section>;
}

function ConnectorDetail({ connector, connectors, usage, secrets, logins, loginsError, secretsError, secretsLoading, target, action, identity, revision, inventory, onChanged }: {
  connector: Connector;
  connectors: Connector[];
  usage: ConnectorUsage[] | undefined;
  secrets: unknown[] | undefined;
  logins: unknown[] | undefined;
  loginsError: Failure | undefined;
  secretsError: Failure | undefined;
  secretsLoading: boolean;
  action?: string;
  identity?: string[];
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
  const keepsLogins = loginMethods(connector).length > 0;
  const loginIdentities = loginIdentitiesFor(logins, connector.name);
  if (action && storesIdentity && !registry) return <IdentityPage connector={connector} connectors={connectors} action={action} identity={identity} target={target} identities={identities} secrets={secrets} secretsLoading={secretsLoading} inventory={inventory} onChanged={onChanged} />;
  return <>
    <div className="detail-actions"><a href={connectorsLink()}>← Back to connectors</a></div>
    <section className="panel" aria-label={`${title} connector`}>
      <div className="panel-heading"><h2>{title}</h2>{missing.length ? <span className="badge needs-connector">Needs a default</span> : <span className="badge">{connector.name}</span>}</div>
      <div className="panel-body">
        {connector.description && <p>{connector.description}</p>}
        {!registry && secrets && logins && !connectorSetUp(connector, secrets, logins) && <p className="connector-not-set-up" role="note">{notSetUpText(connector)}</p>}
        <dl className="details-grid">
          <dt>Name</dt><dd><code>{connector.name}</code></dd>
          <dt>Delivered as</dt><dd>{connector.methods.length ? <ul className="connector-methods">{connector.methods.map(method => <li key={method.name}><code>{method.name}</code>{method.default && <> <span className="badge">default method</span></>} — {deliveryText(method)}{method.description && <span className="description"> {method.description}</span>}</li>)}</ul> : deliveryText({ delivery: connector.delivery ?? "" })}</dd>
          <dt>Identity</dt><dd>{storesIdentity ? <>{connector.secretTypes.join(" or ")} Secret{connector.fields.length > 0 && <> with {connector.fields.map(field => field.name).join(", ")}</>}{keepsLogins && <>, or a login identity (a Volume) for the {loginMethods(connector).map(method => method.name).join(", ")} method</>}</> : keepsLogins ? "A login identity — a Volume holding the harness login" : "None"}</dd>
          <dt>Default scopes</dt><dd>{connectorLevels(connector).join(", ")}</dd>
          {connector.harness && <><dt>Harness</dt><dd>{connector.harness}</dd></>}
          {connector.group && <><dt>Method group</dt><dd>{connector.group} — a Config enables exactly one method</dd></>}
          <dt>Configs</dt><dd>{usage ? <>{configs} require it{connector.optional && " (a cell starts without it)"}</> : "Not loaded"}</dd>
        </dl>
        {keepsLogins && <p className="description">The {loginMethods(connector).map(method => method.name).join(", ")} method keeps its login in a login identity. Create one and log in below; every cell of the role that uses it then starts logged in.</p>}
      </div>
    </section>
    {missing.length > 0 && <section className="panel connector-missing" aria-label={`Configs without a ${title} identity`}>
      <div className="panel-heading"><h2>Needs a default</h2><span className="badge needs-connector">{missing.length} {missing.length === 1 ? "Config" : "Configs"} without an identity</span></div>
      <div className="panel-body">
        <p className="description">No cell can be created from these Configs until {title} has an identity. Set a default below for their scope, or pick one in the Config.</p>
        <ul className="state-list">{missing.map(item => <li key={`${configLink(item.config)}:${item.component}`}><div><a className="resource-name" href={configLink(item.config)}>{item.config.name}</a><p className="description">{[item.config.realm, item.config.space, item.config.stack].filter(Boolean).join(" / ")}{item.method && <> · {item.method} method</>}</p>{item.message && <p className="setup-error">{item.message}</p>}</div><a className="document-action" href={connectorsLink(connector.name, targetScope(item.config))} aria-label={`Set a ${title} default for the scope of ${item.config.name}`}>Set a default for its scope</a></li>)}</ul>
      </div>
    </section>}
    <DefaultsPanel key={`defaults:${scopeParts(target).join("/")}`} connector={connector} identities={identities} revision={revision} inventory={inventory} initialScope={target} onChanged={onChanged} />
    {registry && <section className="panel connector-registry-notice" aria-label={`${title} on the Images page`}><div className="panel-body"><p role="note">A registry credential authenticates a Realm's image pulls; nothing of it is injected into a cell. Registry credentials are listed and added on the <a href={imagesLink(target?.realm)}>Images</a> page, per Realm.</p></div></section>}
    {keepsLogins && <LoginIdentitiesPanel connector={connector} logins={loginIdentities} loginsError={loginsError} inventory={inventory} target={target} onChanged={onChanged} />}
    {storesIdentity && !registry && <IdentitiesPanel connector={connector} identities={identities} secretsError={secretsError} revision={revision} inventory={inventory} target={target} onChanged={onChanged} />}
  </>;
}

export function ConnectorsPage({ name, target, action, identity, revision, inventory, onChanged, onTitle }: { name?: string; target?: string[]; action?: string; identity?: string[]; revision: number; inventory: InventoryResource; onChanged: () => void; onTitle?: (connector: { name: string; title: string }) => void }) {
  const catalog = useResource<Connector[]>("/api/connectors", revision);
  const usage = useResource<ConnectorUsage[]>("/api/connectors/status", revision);
  const secrets = useResource<unknown[]>("/api/catalog/secrets", revision);
  // Login identities (#1685). While a login session runs the list is re-read
  // every few seconds, so a row's status follows the login as it ends.
  const [tick, setTick] = useState(0);
  const logins = useResource<unknown[]>("/api/connectors/logins", revision + tick);
  const running = (logins.data ?? []).some(item => !!(item as { session?: unknown } | null)?.session);
  useEffect(() => {
    if (!running) return undefined;
    const timer = window.setInterval(() => setTick(value => value + 1), 3000);
    return () => window.clearInterval(timer);
  }, [running]);
  const connector = name ? catalog.data?.find(item => item.name === name) : undefined;
  const scope: Scope | undefined = target?.length ? scopeFromParts(target) : undefined;
  // The page heading shows the connector's title, which only the catalog knows (#1568).
  const connectorName = connector?.name, connectorTitle = connector?.title;
  useEffect(() => { if (connectorName) onTitle?.({ name: connectorName, title: connectorTitle || connectorName }); }, [connectorName, connectorTitle, onTitle]);
  return <>
    {catalog.loading && !catalog.data && <p role="status">Loading connectors…</p>}
    {catalog.error && <ErrorNotice error={catalog.error} kind="Connector" />}
    {catalog.error && catalog.data && <p className="stale">Stale connector catalog — refresh failed.</p>}
    {usage.error && <p className="stale">Config usage could not be loaded: {usage.error.message}</p>}
    {!name && secrets.error && <p className="stale">Identities could not be listed: {secrets.error.message}</p>}
    {!name && <p className="description connectors-intro">A connector is a kind of access a cell needs. An identity is the stored credential that fills it — chosen per cell or per Config, or inherited from a Realm, Space or Stack default.</p>}
    {catalog.data && !name && <ConnectorList connectors={listedConnectors(catalog.data)} usage={usage.data} secrets={secrets.data} logins={logins.data} />}
    {catalog.data && name && !connector && <div className="error" role="alert"><strong>Unknown connector</strong><p>No connector named {name} is defined. <a href={connectorsLink()}>Back to connectors</a></p></div>}
    {connector && <ConnectorDetail key={connector.name} connector={connector} connectors={catalog.data ?? []} usage={usage.data} secrets={secrets.data} logins={logins.data} loginsError={logins.error} secretsError={secrets.error} secretsLoading={secrets.loading && !secrets.data} target={scope} action={action} identity={identity} revision={revision} inventory={inventory} onChanged={onChanged} />}
  </>;
}
