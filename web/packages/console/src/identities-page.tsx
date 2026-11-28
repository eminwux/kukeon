import { useEffect, useMemo, useState, type FormEvent } from "react";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import type { InventoryResource } from "./inventory";
import { CatalogDeleteConfirmation } from "./catalog";
import { catalogDeleteAPI } from "./catalog-url.mjs";
import { IdentityForm, IdentitySelect, LoginButton, LoginStateBadge, ScopeGroup, ScopePicker, ScopeRadio, connectorAPI, connectorCatalogLink, identitiesLink, identityPageLink, levelLabel, newIdentityLink, newLoginIdentityLink, rotateIdentityLink, sendJSON, startLogin, toFailure } from "./connector-ui";
import { Disclosure } from "@kukeon/ui";
import { connectorSetUp, notSetUpText } from "./onboarding.mjs";
import {
  allowedLevels, connectorLevels, defaultRow, defaultScopeName, defaultTargets, deliveryText, identitiesFor, importPayload, importsFromHost,
  listedConnectors, loginBody, tokenConnectors, tokenForGit, loginIdentitiesFor, loginMethods, loginNameError, refKey, refLabel, refScope, scopeAt, scopeFromParts, scopeLevel, scopeLevels, scopeParts, scopeQuery, secretRef, sharedLoginNote, targetScope, usageFor,
} from "./connectors.mjs";
import type { Connector, ConnectorDefault, ConnectorUsage, DefaultTarget, Identity, ImportForm, ImportResult, Scope, SecretRef } from "./connectors.mjs";
import { BulkActions, SelectAllBox, SelectRowBox, countNoun, useSelection, type BulkAction, type BulkRow } from "./bulk-ui";
import { navigate, routeHref } from "./router";
import { defaultRole, defaultTeam, filterRows, findRow, identityRows, kindLogin, loginMethodOf, markDefaults, pageKind, pageLogin, pageSecret, pinnedFor, scopeLabel, systemDefaults, usedByCount } from "./identities.mjs";
import type { IdentityRow, SystemDefault } from "./identities.mjs";
import { AssignmentsTab } from "./assignments-tab";
import { IdentityDetailPage } from "./identity-page";

// The Identities page (#1790): every credential a cell uses, from the connector catalog
// (GET /api/connectors), the tagged Secrets and the login identities. A connector added as one
// connector.yaml is listed, given identities and defaulted here with no change to this file. The
// catalog itself is read-only on the Daemon page's Connectors tab.

function configLink(config: { name: string; realm?: string; space?: string; stack?: string }): string {
  return routeHref("/configs/" + [config.realm, config.space, config.stack, config.name].filter((value): value is string => !!value).map(encodeURIComponent).join("/"));
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

const identityNoun = countNoun("identity", "identities");
function deleteKind(row: IdentityRow): "secrets" | "volumes" { return row.kind === kindLogin ? "volumes" : "secrets"; }
function rowNoun(row: IdentityRow): string { return `${row.title} ${row.kind === kindLogin ? "login identity" : "identity"}`; }

// UsedBy names the Configs using an identity — linked, with their team role — and the scope defaults
// naming it; an identity nothing uses says Unused.
function UsedBy({ row }: { row: IdentityRow }) {
  const { configs, teams } = usedByCount(row);
  if (row.unused) return <span className="badge identity-unused" title="No Config and no default uses it: it can be deleted.">Unused</span>;
  return <div className="identity-used-by">
    {configs > 0 && <span className="description">{configs} {configs === 1 ? "Config" : "Configs"}{teams.length > 0 && <> · {teams.length} {teams.length === 1 ? "team" : "teams"}</>}</span>}
    {row.usedBy.map(config => <a className="badge label-badge" key={configLink(config)} href={configLink(config)} title={config.role ? `Role ${config.role}` : undefined}>{config.name}</a>)}
    {row.defaults.map(item => <span className="badge" key={`${item.level}:${item.scope.join("/")}:${item.slot}`}>{`${item.level[0].toUpperCase()}${item.level.slice(1)} default`} {item.scope.join(" / ")}</span>)}
  </div>;
}

function Roles({ roles }: { roles: string[] }) {
  return roles.length ? <>{roles.map(role => <span className="badge label-badge" key={role}>{role}</span>)}</> : <span className="description">—</span>;
}

// IdentityCells are the cells of one identity's row after its name: connector, method or type, role,
// scope, status and users.
function IdentityCells({ row }: { row: IdentityRow }) {
  return <>
    <td><a href={identitiesLink(row.connector)}>{row.title}</a></td>
    <td><code>{row.kind === kindLogin ? row.method : row.type}</code>{Object.entries(row.fields).flatMap(([name, value]) => [" ", <span className="badge label-badge" key={name}>{name}: {value}</span>])}</td>
    <td><Roles roles={row.roles} /></td>
    <td className="scope-cell">{scopeLabel(row)}</td>
    <td>{row.kind === kindLogin ? <><LoginStateBadge state={row.status} />{row.since && <span className="description"> since {new Date(row.since).toLocaleString()}</span>}</> : <span className="description">—</span>}</td>
    <td><UsedBy row={row} /></td>
  </>;
}

// IdentityName links to the identity's page (#1805), a login identity not created yet included.
function IdentityName({ row }: { row: IdentityRow }) {
  return <><a className="resource-name" href={identityPageLink(row.connector, pageKind(row), row.ref)}>{row.name}</a>
    {row.kind === kindLogin && !row.exists && <> <span className="badge">Not created yet</span></>}
    {row.session && <> <span className="badge">Login running</span></>}</>;
}

function RowActions({ row, target, pending, onDelete, onChanged }: { row: IdentityRow; target: Scope | undefined; pending: boolean; onDelete: (row: IdentityRow) => void; onChanged: () => void }) {
  const label = `${rowNoun(row)} ${refLabel(row.ref)}`;
  return <td className="connector-row-actions">
    {row.kind === kindLogin ? <LoginButton connector={row.connector} title={row.title} volume={row.ref} state={row.status} running={!!row.session} method={row.method || undefined} onStarted={onChanged} />
      : <a className="subtle-button" href={rotateIdentityLink(row.connector, row.ref, target)} aria-label={`Rotate ${label}`}>Rotate</a>}
    {row.exists && <button type="button" className="subtle-button danger-action" aria-label={`Delete ${label}`} disabled={pending} onClick={() => onDelete(row)}>{pending ? "Deleting…" : "Delete"}</button>}
  </td>;
}

// SystemDefaultRow is a pinned row: the identity a connector × method falls back to on a fresh realm —
// the default team's agent login identity, or the default realm's Realm default — or Not set up.
function SystemDefaultRow({ item, connector, target, pending, onDelete, onChanged }: { item: SystemDefault; connector: Connector | undefined; target: Scope | undefined; pending: (row: IdentityRow) => boolean; onDelete: (row: IdentityRow) => void; onChanged: () => void }) {
  const row = item.row;
  const label = `System default for ${item.title}${item.method ? ` ${item.method}` : ""}`;
  if (row) return <tr className="system-default" aria-label={label}>
    <td className="bulk-column" />
    <td><IdentityName row={row} /> <span className="badge system-default-badge">System default</span></td>
    <IdentityCells row={row} />
    <RowActions row={row} target={target} pending={pending(row)} onDelete={onDelete} onChanged={onChanged} />
  </tr>;
  const login = item.kind === kindLogin;
  return <tr className="system-default" aria-label={label}>
    <td className="bulk-column" />
    <td>{login && connector?.harness ? <span className="resource-name">{`${defaultTeam}-${defaultRole}-${connector.harness}-state`}</span> : <span className="description">Not set up</span>} <span className="badge system-default-badge">System default</span></td>
    <td><a href={identitiesLink(item.connector)}>{item.title}</a></td>
    <td><code>{item.method || (login && loginMethodOf(connector)) || "—"}</code></td>
    <td>{login ? <span className="badge label-badge">{`${defaultTeam}/${defaultRole}`}</span> : <span className="description">—</span>}</td>
    <td className="scope-cell">{defaultScopeName}</td>
    <td>{login ? <span className="description">The default team is not applied: <code>kuke team init</code> applies it.</span> : <span className="description">No Realm default</span>}</td>
    <td><span className="description">—</span></td>
    <td className="connector-row-actions">{!login && <a className="subtle-button" href={newIdentityLink(item.connector, { realm: defaultScopeName })} aria-label={`Set up the ${label}`}>Create one</a>}</td>
  </tr>;
}

// IdentitiesTable is every identity in one table — the tagged Secrets and the login identities — with
// the system defaults pinned first. Rotate, Log in, Delete and bulk delete are row actions; creating one
// is a page of its own.
function IdentitiesTable({ label, rows, pinned, connectors, target, error, revision, inventory, onChanged }: {
  label: string;
  rows: IdentityRow[];
  pinned: SystemDefault[];
  connectors: Connector[];
  target: Scope | undefined;
  error: string;
  revision: number;
  inventory: InventoryResource;
  onChanged: () => void;
}) {
  const [confirming, setConfirming] = useState<IdentityRow>();
  // The identities being deleted, and the failed deletes still shown: a batch deletes several at once (#1745).
  const [pending, setPending] = useState<Set<string>>(() => new Set());
  const [failures, setFailures] = useState<{ target: string; message: string }[]>([]);
  const { notify } = useNotify();
  // remove deletes one identity and resolves to its outcome; quiet leaves the toast and the failure notice
  // to a batch, which reports every row once.
  async function remove(row: IdentityRow, quiet = false): Promise<{ ok: boolean; message: string }> {
    const target = `${rowNoun(row)} ${refLabel(row.ref)}`;
    if (pending.has(row.key)) return { ok: false, message: "A delete of it is still running." };
    setPending(old => new Set(old).add(row.key));
    setFailures(old => old.filter(item => item.target !== target));
    try {
      await sendJSON(catalogDeleteAPI(deleteKind(row), [...refScope(row.ref), row.ref.name]), "{}");
      if (!quiet) notify({ key: `identity-delete:${row.key}`, tone: "success", message: `${rowNoun(row)} ${row.name} deleted.` });
      return { ok: true, message: "" };
    } catch (caught) {
      const message = toFailure(caught).message;
      if (!quiet) setFailures(old => [...old, { target, message }]);
      return { ok: false, message };
    } finally {
      setPending(old => { const next = new Set(old); next.delete(row.key); return next; });
      onChanged();
    }
  }
  // Row selection and bulk delete (#1745); a login identity not created yet has nothing to delete.
  const deletable = rows.filter(row => row.exists);
  const byKey = new Map(deletable.map(row => [row.key, row]));
  const selection = useSelection([...byKey.keys()], !error);
  type SelectedRow = BulkRow & { row: IdentityRow };
  const selectedRows: SelectedRow[] = deletable.filter(row => selection.has(row.key)).map(row => ({ key: row.key, scope: [...row.scope, row.name], label: refLabel(row.ref), row }));
  const bulkDelete: BulkAction<SelectedRow>[] = [{ name: "delete", verb: "Delete", danger: true,
    consequence: "Deletes each identity: a Secret, or a login identity's Volume with its login. A cell or team that uses one loses its credential.",
    reasonOf: item => pending.has(item.key) ? "a delete of it is still running" : "",
    acknowledgment: "I understand this permanently deletes every identity listed." }];
  const find = (name: string) => connectors.find(item => item.name === name);
  return <section className="panel connector-identities" aria-label={label}>
    <div className="panel-heading"><h2>{label}</h2><span className="badge">{rows.length} stored</span></div>
    <div className="panel-body">
      <p className="description">An identity is a Secret tagged with a connector, or a login identity — a Volume the harness keeps its login in. Only names, scopes, types, readable fields and whether a login is present are shown: values and logins are never returned to the browser. {sharedLoginNote}</p>
      {error && <p className="stale">{error}</p>}
      {failures.map(failure => <div key={failure.target} className="error" role="alert"><strong>Delete failed: {failure.target}</strong><p>{failure.message}</p></div>)}
      <BulkActions rows={selectedRows} actions={bulkDelete} noun={identityNoun} selection={selection} run={item => remove(item.row, true)} />
      {rows.length || pinned.length ? <div className="table-scroll"><table aria-label={label}><thead><tr><th scope="col" className="bulk-column"><SelectAllBox selection={selection} matching={[...byKey.keys()]} label={`Select all ${identityNoun(byKey.size)}`} /></th><th scope="col">Name</th><th scope="col">Connector</th><th scope="col">Method / type</th><th scope="col">Role</th><th scope="col">Scope</th><th scope="col">Status</th><th scope="col">Used by</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
        {pinned.length > 0 && <tbody className="system-defaults" aria-label="System defaults">{pinned.map(item => <SystemDefaultRow key={item.key} item={item} connector={find(item.connector)} target={target} pending={row => pending.has(row.key)} onDelete={setConfirming} onChanged={onChanged} />)}</tbody>}
        <tbody>{rows.map(row => <tr key={row.key}>
          <td className="bulk-column">{row.exists && <SelectRowBox selection={selection} rowKey={row.key} label={`Select ${rowNoun(row)} ${refLabel(row.ref)}`} />}</td>
          <td><IdentityName row={row} /></td>
          <IdentityCells row={row} />
          <RowActions row={row} target={target} pending={pending.has(row.key)} onDelete={setConfirming} onChanged={onChanged} />
        </tr>)}</tbody>
      </table></div> : <p>No identities are stored.</p>}
    </div>
    {confirming && <CatalogDeleteConfirmation kind={deleteKind(confirming)} doc={{ metadata: { name: confirming.ref.name, realm: confirming.ref.realm, ...(confirming.ref.space ? { space: confirming.ref.space } : {}), ...(confirming.ref.stack ? { stack: confirming.ref.stack } : {}) } }} revision={revision} inventory={inventory} onCancel={() => setConfirming(undefined)} onConfirm={() => { const row = confirming; setConfirming(undefined); void remove(row); }} />}
  </section>;
}

// NewLoginIdentityPage is #/identities/<name>/new-login (#1685, #1790): a name and a scope, with Create and
// log in — the identity cell's terminal — or Create without logging in. It returns to Identities.
function NewLoginIdentityPage({ connector, logins, target, inventory, onChanged }: { connector: Connector; logins: unknown[] | undefined; target: Scope | undefined; inventory: InventoryResource; onChanged: () => void }) {
  const title = connector.title || connector.name;
  const methods = loginMethods(connector);
  const back = identitiesLink(connector.name, target);
  const [name, setName] = useState("");
  const [method, setMethod] = useState("");
  const [scope, setScope] = useScope(inventory, target && scopeAt(target, scopeLevels[0]));
  const [pending, setPending] = useState("");
  const [error, setError] = useState<Failure>();
  const { notify } = useNotify();
  const problem = loginNameError(name);
  const taken = loginIdentitiesFor(logins, connector.name).some(item => refKey(item.volume) === refKey({ name: name.trim(), ...scope }));
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
        navigate(back);
      }
      onChanged();
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(""); }
  }
  return <>
    <div className="detail-actions"><a href={back}>← Back to {title}</a></div>
    <section className="panel connector-identity-page" aria-label={`New ${title} login identity`}><div className="panel-body">
      <form className="creation-form identity-form" aria-label={`New ${title} login identity`} method="post" autoComplete="off" onSubmit={event => void create(event, false)}>
        <p className="description">A login identity is a Volume tagged <code>{connector.name}</code>: the harness keeps its login in its state directory, and that directory is the Volume. <strong>Create and log in</strong> opens a short-lived identity cell that mounts only that Volume and runs the {title} login; the cell is deleted when the login ends. Only whether a login is present is shown — never the login itself. {sharedLoginNote}</p>
        <ScopeGroup title="Login identity scope" description={<>Where the Volume is stored: a Realm, or a Space or Stack of it.</>}><ScopePicker label="Login identity" inventory={inventory} levels={scopeLevels} value={scope} disabled={!!pending} onChange={setScope} /></ScopeGroup>
        <div className="schema-fields">
          <label className="schema-field"><span>Login identity name<b aria-label="required"> *</b></span><input value={name} placeholder="personal" disabled={!!pending} autoComplete="off" spellCheck={false} aria-invalid={(!!name && !!problem) || undefined} onChange={event => { setName(event.target.value); setError(undefined); }} /><small>{taken ? "A login identity of this name already exists here: logging in logs in to it again." : "The Volume's name. It is created retained, so it outlives a purge of its scope."}</small></label>
          {methods.length > 1 && <label className="schema-field"><span>Login method</span><select aria-label={`${title} login method`} value={method} disabled={!!pending} onChange={event => setMethod(event.target.value)}><option value="">Default</option>{methods.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}</select></label>}
        </div>
        <div className="connector-actions">
          <button type="button" disabled={!!pending || !scope.realm || !name.trim()} onClick={event => void create(event, true)}>{pending === "login" ? "Starting…" : "Create and log in"}</button>
          <button type="submit" className="subtle-button" disabled={!!pending || !scope.realm || !name.trim()}>{pending === "create" ? "Creating…" : "Create without logging in"}</button>
          <a className="subtle-button" href={back}>Cancel</a>
        </div>
        {error && <ErrorNotice error={error} kind="Identity" />}
      </form>
    </div></section>
  </>;
}

// newIdentityChoices are the ways to add an identity of a connector: a Secret for a connector with a
// default target (a token, a key, a git identity), a login identity for one with a login method.
function newIdentityChoices(connector: Connector): { label: string; href: (scope?: Partial<Scope>) => string }[] {
  const title = connector.title || connector.name;
  return [
    ...(defaultTargets(connector).length ? [{ label: loginMethods(connector).length ? `${title} token or key` : `${title} identity`, href: (scope?: Partial<Scope>) => newIdentityLink(connector.name, scope) }] : []),
    ...(loginMethods(connector).length ? [{ label: `${title} login identity`, href: (scope?: Partial<Scope>) => newLoginIdentityLink(connector.name, scope) }] : []),
  ];
}

// NewIdentity asks for the connector first, then opens today's forms: the identity form (with the git
// cards for a git-delivered connector) or New login identity. Filtered on a connector it links them.
function NewIdentity({ connectors, connector, target }: { connectors: Connector[]; connector: Connector | undefined; target: Scope | undefined }) {
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState("");
  if (connector) {
    const choices = newIdentityChoices(connector);
    return <div className="connector-actions">{choices.map((choice, i) => <a key={choice.label} className="document-action" href={choice.href(target)}>{i === 0 && defaultTargets(connector).length ? "New identity" : "New login identity"}</a>)}</div>;
  }
  const choices = connectors.flatMap(item => newIdentityChoices(item).map(choice => ({ ...choice, key: `${item.name}:${choice.label}` })));
  const chosen = choices.find(choice => choice.key === picked);
  return <div className="connector-actions new-identity">
    <button type="button" className="document-action" aria-expanded={open} onClick={() => setOpen(value => !value)}>New identity</button>
    {open && <form className="creation-form" aria-label="New identity" onSubmit={event => { event.preventDefault(); if (chosen) navigate(chosen.href(target)); }}>
      <label className="schema-field"><span>Connector</span><select aria-label="New identity connector" value={picked} onChange={event => setPicked(event.target.value)}><option value="">Choose a connector…</option>{choices.map(choice => <option key={choice.key} value={choice.key}>{choice.label}</option>)}</select></label>
      <div className="connector-actions"><button type="submit" disabled={!chosen}>Continue</button><button type="button" className="subtle-button" onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
  </div>;
}

// IdentityPage is #/identities/<name>/new-identity — where the identity is
// stored, its form and, for a connector that imports from the host, the import
// — or #/identities/<name>/rotate-identity, the rotate form of one identity.
// Saving or cancelling returns to the Identities page filtered on the connector.
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
  const back = identitiesLink(connector.name, target);
  const leave = () => { navigate(back); };
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
  const defaults = useResource<ConnectorDefault[]>(scope.realm && targets.length ? `/connectors/defaults?${scopeQuery(scope)}` : null, revision);
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

// NeedsDefault lists the Configs of a connector that have no identity, each with a link that opens the
// connector's scope-default editor on the Config's scope.
function NeedsDefault({ connector, missing }: { connector: Connector; missing: ConnectorUsage["missing"] }) {
  const title = connector.title || connector.name;
  if (!missing.length) return null;
  return <section className="panel connector-missing" aria-label={`Configs without a ${title} identity`}>
    <div className="panel-heading"><h2>Needs a default</h2><span className="badge needs-connector">{missing.length} {missing.length === 1 ? "Config" : "Configs"} without a {title} identity</span></div>
    <div className="panel-body">
      <p className="description">No cell can be created from these Configs until {title} has an identity. Set a default for their scope, or pick one in the Config.</p>
      <ul className="state-list">{missing.map(item => <li key={`${configLink(item.config)}:${item.component}`}><div><a className="resource-name" href={configLink(item.config)}>{item.config.name}</a><p className="description">{[item.config.realm, item.config.space, item.config.stack].filter(Boolean).join(" / ")}{item.method && <> · {item.method} method</>}</p>{item.message && <p className="setup-error">{item.message}</p>}</div><a className="document-action" href={identitiesLink(connector.name, targetScope(item.config))} aria-label={`Set a ${title} default for the scope of ${item.config.name}`}>Set a default for its scope</a></li>)}</ul>
    </div>
  </section>;
}

// IdentitiesNav is the Identities page's tabs (#1790): every identity, and the Assignments of teams and
// roles to identities.
function IdentitiesNav({ current }: { current: "identities" | "assignments" }) {
  return <nav className="daemon-tabs" aria-label="Identities sections">
    <a href={identitiesLink()} aria-current={current === "identities" ? "page" : undefined}>Identities</a>
    <a href={routeHref("/identities/assignments")} aria-current={current === "assignments" ? "page" : undefined}>Assignments</a>
  </nav>;
}

// IdentitiesPage is #/identities, filtered on a connector with ?connector=, its Assignments tab
// #/identities/assignments, the identity forms #/identities/<name>/new-identity, …/new-login and
// …/rotate-identity, and the page of one identity …/identity and …/login (#1805).
export function IdentitiesPage({ connector: filter, section, target, action, identity, revision, inventory, onChanged, onTitle }: { connector?: string; section?: string; target?: string[]; action?: string; identity?: string[]; revision: number; inventory: InventoryResource; onChanged: () => void; onTitle?: (connector: { name: string; title: string }) => void }) {
  const catalog = useResource<Connector[]>("/connectors", revision);
  const usage = useResource<ConnectorUsage[]>("/connectors/status", revision);
  const secrets = useResource<unknown[]>("/catalog/secrets", revision);
  // Login identities (#1685). While a login session runs the list is re-read
  // every few seconds, so a row's status follows the login as it ends.
  const [tick, setTick] = useState(0);
  const logins = useResource<unknown[]>("/connectors/logins", revision + tick);
  const running = (logins.data ?? []).some(item => !!(item as { session?: unknown } | null)?.session);
  useEffect(() => {
    if (!running) return undefined;
    const timer = window.setInterval(() => setTick(value => value + 1), 3000);
    return () => window.clearInterval(timer);
  }, [running]);
  // The system defaults are the default realm's: kuke init always creates it.
  const hasDefaultRealm = (inventory.data?.rows[0] ?? []).some(row => row.scope[0] === defaultScopeName);
  const realmDefaults = useResource<unknown[]>(hasDefaultRealm ? `/connectors/defaults?realm=${encodeURIComponent(defaultScopeName)}` : null, revision);
  const connectors = listedConnectors(catalog.data ?? []);
  const connector = filter ? connectors.find(item => item.name === filter) : undefined;
  const scope: Scope | undefined = target?.length ? scopeFromParts(target) : undefined;
  // An identity form's heading shows the connector's title, which only the catalog knows (#1568).
  const connectorName = connector?.name, connectorTitle = connector?.title;
  useEffect(() => { if (connectorName) onTitle?.({ name: connectorName, title: connectorTitle || connectorName }); }, [connectorName, connectorTitle, onTitle]);
  const scopes = inventory.data?.rows.slice(0, 3);
  const configs = inventory.data?.configs;
  const rows = useMemo(() => markDefaults(identityRows({ connectors, secrets: secrets.data, logins: logins.data, configs, scopes }), realmDefaults.data), [connectors, secrets.data, logins.data, configs, scopes, realmDefaults.data]);
  const unknown = catalog.data && filter && !connector;
  if (section === "assignments") return <><IdentitiesNav current="assignments" /><AssignmentsTab revision={revision} onChanged={onChanged} /></>;
  if (action === pageSecret || action === pageLogin) {
    const row = findRow(rows, action, filter, identity);
    const loading = (catalog.loading && !catalog.data) || (secrets.loading && !secrets.data) || (logins.loading && !logins.data) || (inventory.loading && !inventory.data);
    const pinned = hasDefaultRealm ? pinnedFor(systemDefaults({ connectors, rows, realmDefaults: realmDefaults.data }), row) : [];
    return <IdentityDetailPage key={`${action}:${filter}:${JSON.stringify(identity ?? [])}`} connector={connector} row={row} pinned={pinned} loading={!!loading} wanted={identity} revision={revision} inventory={inventory} onChanged={onChanged} />;
  }
  if (action === "new-login" && connector && loginMethods(connector).length) return <NewLoginIdentityPage key={connector.name} connector={connector} logins={logins.data} target={scope} inventory={inventory} onChanged={onChanged} />;
  if (action && action !== "new-login" && connector && defaultTargets(connector).length > 0) return <IdentityPage key={`${connector.name}:${action}`} connector={connector} connectors={catalog.data ?? []} action={action} identity={identity} target={scope} identities={identitiesFor(secrets.data, connector.name)} secrets={secrets.data} secretsLoading={secrets.loading && !secrets.data} inventory={inventory} onChanged={onChanged} />;
  const shown = connector ? [connector] : filter ? [] : connectors;
  const pinned = hasDefaultRealm ? systemDefaults({ connectors: shown, rows, realmDefaults: realmDefaults.data }) : [];
  const needing = shown.map(item => ({ connector: item, missing: usageFor(usage.data, item.name).missing })).filter(item => item.missing.length);
  const errors = [secrets.error && `Identities could not be listed: ${secrets.error.message}`, logins.error && `Login identities could not be listed: ${logins.error.message}`].filter(Boolean).join(" ");
  const title = connector ? connector.title || connector.name : "";
  return <>
    <IdentitiesNav current="identities" />
    {catalog.loading && !catalog.data && <p role="status">Loading identities…</p>}
    {catalog.error && <ErrorNotice error={catalog.error} kind="Connector" />}
    {catalog.error && catalog.data && <p className="stale">Stale connector catalog — refresh failed.</p>}
    {usage.error && <p className="stale">Config usage could not be loaded: {usage.error.message}</p>}
    <p className="description connectors-intro">An identity is a stored credential a cell uses: a Secret tagged with a connector, or a login identity — the Volume a harness keeps its login in. A Config picks one, or inherits a Realm, Space or Stack default. What each connector accepts is listed in the <a href={connectorCatalogLink()}>connector catalog</a>.</p>
    {catalog.data && <div className="list-controls identities-controls">
      <label className="schema-field"><span>Connector</span><select aria-label="Filter identities by connector" value={filter ?? ""} onChange={event => navigate(identitiesLink(event.target.value || undefined))}><option value="">All connectors</option>{connectors.map(item => <option key={item.name} value={item.name}>{item.title || item.name}</option>)}{unknown && <option value={filter}>{filter}</option>}</select></label>
      <NewIdentity connectors={connectors} connector={connector} target={scope} />
    </div>}
    {unknown && <div className="error" role="alert"><strong>Unknown connector</strong><p>No connector named {filter} is defined. <a href={identitiesLink()}>Show every identity</a></p></div>}
    {connector && secrets.data && logins.data && !connectorSetUp(connector, secrets.data, logins.data) && <p className="connector-not-set-up" role="note">{notSetUpText(connector)}</p>}
    {catalog.data && !unknown && <IdentitiesTable label={connector ? `${title} identities` : "Identities"} rows={filterRows(rows, connector?.name)} pinned={pinned} connectors={connectors} target={scope} error={errors} revision={revision} inventory={inventory} onChanged={onChanged} />}
    {connector ? <>
      <NeedsDefault connector={connector} missing={usageFor(usage.data, connector.name).missing} />
      <DefaultsPanel key={`defaults:${scopeParts(scope).join("/")}`} connector={connector} identities={identitiesFor(secrets.data, connector.name)} revision={revision} inventory={inventory} initialScope={scope} onChanged={onChanged} />
    </> : needing.length > 0 && <section className="panel connector-missing" aria-label="Connectors without a default">
      <div className="panel-heading"><h2>Needs a default</h2><span className="badge needs-connector">{needing.length} {needing.length === 1 ? "connector" : "connectors"}</span></div>
      <div className="panel-body">
        <p className="description">Configs that require these connectors resolve to no identity, so no cell can be created from them. Set a default for their scope, or pick one in the Config.</p>
        <ul className="state-list">{needing.map(({ connector: item, missing }) => <li key={item.name}><div><a className="resource-name" href={identitiesLink(item.name)}>{item.title || item.name}</a> <span className="badge needs-connector">Needs a default</span><p className="description">{missing.length} {missing.length === 1 ? "Config" : "Configs"} without an identity</p></div><a className="document-action" href={identitiesLink(item.name, targetScope(missing[0].config))} aria-label={`Set a default for ${item.title || item.name}`}>Set a default</a></li>)}</ul>
      </div>
    </section>}
  </>;
}
