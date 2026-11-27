import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { teamsLink } from "./teams.mjs";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import type { InventoryResource } from "./inventory";
import { workspaceLink } from "./routes";
import {
  allowedLevels, blockingConnectors, blocksCells, clearMaterial, connectorStatuses, connectorTitle, defaultTargets, emptyGitForm, emptyIdentityForm, fieldList, fieldPerson, fieldSecret,
  formatSecretReference, harnessEphemeral, identitiesFor, identityDefaultOn, identityErrors, identityFields, identityPayload, keepsLogin, linkedGitIdentity, loginBody, loginIdentitiesFor, loginLoggedIn, loginStateText, loginTone, originLabel, refKey, refLabel, refScope, sameRef, scopeAt, scopeAtLevel, scopeChoices, scopeLevel, scopeLevels, scopeParts, scopeQuery,
  secretBasicAuth, secretOpaque, secretRef, secretSSHKey, secretTypeChoices, secretTypesOf, sessionScope, staleGitNotice, stateResolved, stateText, statusIdentity, storesNoValue, takesIdentity, targetScope, teamOf, tokenForGit, tokenGitErrors, tokenGitPayload,
  joinPerson, splitPerson,
} from "./connectors.mjs";
import type { ConfigMetadata, Connector, ConnectorStatus, FormField, Identity, IdentityForm as IdentityFormState, LoginIdentity, LoginResult, Scope, SecretRef, VolumeRef } from "./connectors.mjs";

// Shared pieces of the connectors UI (#1548, §7). Nothing here names a
// connector: every label, field and choice comes from GET /api/connectors, the
// Secret catalog and a Config's status.connectors.

export function toFailure(caught: unknown): Failure {
  const failure = caught as Partial<Failure> | undefined;
  return { code: failure?.code ?? "unavailable", message: failure?.message ?? "Request failed. Try again." };
}
// sendJSON posts a JSON body and returns the decoded result, or throws the
// server's { code, message }. Secret material only ever travels in this body.
export async function sendJSON<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(path, { method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" }, body: typeof body === "string" ? body : JSON.stringify(body) });
  const result = await response.json().catch(() => ({}));
  if (!response.ok) throw result.error ?? { code: "http_error", message: `HTTP ${response.status}` };
  return result as T;
}
export function connectorAPI(connector: string, action: "defaults" | "identities" | "import" | "logins" | "login"): string {
  return `/api/connectors/${encodeURIComponent(connector)}/${action}`;
}
export function connectorsLink(connector?: string, scope?: Partial<Scope>): string {
  const parts = scopeParts(scope);
  return "#/connectors" + (connector ? `/${encodeURIComponent(connector)}` : "") + (connector && parts.length ? `?scope=${encodeURIComponent(JSON.stringify(parts))}` : "");
}
// newIdentityLink opens a connector's New identity page, storing on the given scope.
export function newIdentityLink(connector: string, scope?: Partial<Scope>): string {
  const parts = scopeParts(scope);
  return `#/connectors/${encodeURIComponent(connector)}/new-identity` + (parts.length ? `?scope=${encodeURIComponent(JSON.stringify(parts))}` : "");
}
// rotateIdentityLink opens the Rotate page of one stored identity; scope is the connector page's to return to.
export function rotateIdentityLink(connector: string, identity: Partial<SecretRef>, scope?: Partial<Scope>): string {
  const ref = secretRef(identity);
  const parts = scopeParts(scope);
  return `#/connectors/${encodeURIComponent(connector)}/rotate-identity` + (ref ? `?identity=${encodeURIComponent(JSON.stringify([...refScope(ref), ref.name]))}` : "") + (parts.length ? `${ref ? "&" : "?"}scope=${encodeURIComponent(JSON.stringify(parts))}` : "");
}
function levelTitle(level: string): string { return `${level[0].toUpperCase()}${level.slice(1)}`; }
export function levelLabel(level: string, scope: Partial<Scope>): string { return `${levelTitle(level)} — ${scopeParts(scopeAt(scope, level)).join(" / ")}`; }

// StatusBadge states how a connector resolves.
export function ConnectorStateBadge({ status }: { status: ConnectorStatus }) {
  const tone = status.state === stateResolved ? "ok" : blocksCells(status) ? "error" : "stopped";
  return <span className={`state-badge tone-${tone}`}><span className="state-dot" aria-hidden="true" />{status.state === stateResolved ? "Resolved" : blocksCells(status) ? "Unresolved" : "Not set"}</span>;
}

export function volumeLink(ref: VolumeRef): string { return "#/volumes/" + [ref.realm, ref.space, ref.stack, ref.name].filter(Boolean).map(value => encodeURIComponent(value as string)).join("/"); }
// StateVolume names a state volume: a link to the Volume when the Config names
// its scope, the bare name otherwise.
function StateVolume({ status }: { status: ConnectorStatus }) {
  if (status.volumeRef) return <a className="resource-name" href={volumeLink(status.volumeRef)}><code>{status.volumeRef.name}</code></a>;
  return status.volume ? <code>{status.volume}</code> : <>of the harness</>;
}
// LoginStateBadge says whether a login identity holds a login.
export function LoginStateBadge({ state }: { state: string | undefined }) {
  return <span className={`state-badge tone-${loginTone(state)}`}><span className="state-dot" aria-hidden="true" />{loginStateText(state)}</span>;
}
// HarnessState says where the harness of a token method keeps its state
// (#1685): its state volume, or ephemeral.
function HarnessState({ status }: { status: ConnectorStatus }) {
  if (!stateText(status)) return null;
  return <span className="connector-harness-state"> · State: {status.harnessState === harnessEphemeral ? <>ephemeral <span className="description">(no state volume)</span></> : <StateVolume status={status} />}</span>;
}

// StatusIdentity names the identity a connector resolved to and the level it
// came from: a Secret, or — for a method that keeps a login — the login
// identity (a Volume) with its login state. Only coordinates are shown, never
// material.
export function StatusIdentity({ status, team = "" }: { status: ConnectorStatus; team?: string }) {
  if (keepsLogin(status)) return <span>Login identity <StateVolume status={status} />{status.volumeRef && <> <span className="description">{refScope(status.volumeRef).join(" / ")}</span></>}{status.origin && <> <span className="badge">{originLabel(status.origin, team)}</span></>}{status.login && <> <LoginStateBadge state={status.login.state} /></>}</span>;
  if (!status.identity) return <span>No identity{status.required ? " — required" : ""}<HarnessState status={status} /></span>;
  return <span><code>{status.identity.name}</code> <span className="description">{refScope(status.identity).join(" / ")}</span>{status.origin && <> <span className="badge">{originLabel(status.origin, team)}</span></>}<HarnessState status={status} /></span>;
}

// startLogin makes sure a login identity exists and an identity cell is logging
// in to it, then opens that cell's terminal (#1685). A session already running
// is attached to instead of starting a second one. The identity cell mounts
// only the identity's Volume and deletes itself when the login ends.
export async function startLogin(connector: string, volume: Partial<SecretRef>, method?: string): Promise<LoginIdentity | undefined> {
  const result = await sendJSON<LoginResult>(connectorAPI(connector, "login"), loginBody(volume.name ?? "", volume, method));
  const [identity] = loginIdentitiesFor([result.identity], connector);
  if (identity?.session) window.location.hash = workspaceLink(sessionScope(identity.session));
  return identity;
}

// LoginButton logs in to one login identity: "Log in", "Log in again" once it
// holds a login, or "Open login session" while one is running. idle replaces
// "Log in" where the page words the offer itself (New agent's "Log in first").
export function LoginButton({ connector, title, volume, state, running = false, method, disabled = false, idle = "Log in", className = "subtle-button", onStarted }: {
  connector: string;
  title: string;
  volume: Partial<SecretRef>;
  state?: string;
  running?: boolean;
  method?: string;
  disabled?: boolean;
  idle?: string;
  className?: string;
  onStarted?: () => void;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const label = running ? "Open login session" : state === loginLoggedIn ? "Log in again" : idle;
  async function run() {
    if (pending) return;
    setPending(true); setError(undefined);
    try { await startLogin(connector, volume, method); onStarted?.(); }
    catch (caught) { setError(toFailure(caught)); }
    finally { setPending(false); }
  }
  return <>
    <button type="button" className={className} aria-label={`${label}: ${title} identity ${refLabel(volume)}`} disabled={disabled || pending} onClick={() => void run()}>{pending ? "Starting…" : label}</button>
    {error && <ErrorNotice error={error} kind="Identity" />}
  </>;
}

// LoginIdentitySelect picks a login identity of one connector: the Volumes
// tagged with it, plus the one already in use. The empty choice is the
// caller's "use the Config's".
export function LoginIdentitySelect({ label, identities, value, onChange, emptyLabel, extra = [], disabled = false, help }: {
  label: string;
  identities: LoginIdentity[];
  value: Partial<SecretRef> | undefined;
  onChange: (value: SecretRef | undefined) => void;
  emptyLabel: string;
  extra?: Array<SecretRef | undefined>;
  disabled?: boolean;
  help?: ReactNode;
}) {
  const id = useId();
  const options = identities.map(identity => ({ ref: identity.volume, text: `${refLabel(identity.volume)} — ${loginStateText(identity.state)}${identity.usedBy.length ? `, used by ${identity.usedBy[0].name}${identity.usedBy.length > 1 ? ` (+${identity.usedBy.length - 1})` : ""}` : ""}` }));
  for (const ref of extra) if (ref && !options.some(option => sameRef(option.ref, ref))) options.push({ ref, text: refLabel(ref) });
  const current = refKey(value);
  return <div className="schema-field secret-picker"><label htmlFor={id}><span>{label}</span></label>
    <select id={id} value={options.some(option => refKey(option.ref) === current) ? current : ""} disabled={disabled} onChange={event => onChange(options.find(option => refKey(option.ref) === event.target.value)?.ref)}>
      <option value="">{emptyLabel}</option>
      {options.map(option => <option key={refKey(option.ref)} value={refKey(option.ref)}>{option.text}</option>)}
    </select>
    {help && <small>{help}</small>}
  </div>;
}

// IdentitySelect picks an identity of one connector: the Secrets tagged with
// it, any extra reference the caller wants listed (the effective identity),
// and a by-name entry for a Secret that carries no tag. The empty choice is
// the caller's "Default"/"None".
export function IdentitySelect({ label, identities, value, onChange, emptyLabel, extra = [], disabled = false, required = false, help, realm = "", id: fixedId, invalid = false }: {
  id?: string;
  invalid?: boolean;
  label: string;
  identities: Identity[];
  value: Partial<SecretRef> | undefined;
  onChange: (value: Partial<SecretRef> | undefined) => void;
  emptyLabel: string;
  extra?: Array<SecretRef | undefined>;
  disabled?: boolean;
  required?: boolean;
  help?: ReactNode;
  realm?: string;
}) {
  const generatedId = useId();
  const id = fixedId ?? generatedId;
  const options = identities.map(identity => ({ ref: secretRef(identity)!, text: `${refLabel(identity)} (${identity.type})` }));
  for (const ref of extra) if (ref && !options.some(option => refKey(option.ref) === refKey(ref))) options.push({ ref, text: `${refLabel(ref)} (not tagged)` });
  const current = refKey(value);
  const listed = options.some(option => refKey(option.ref) === current);
  const [manual, setManual] = useState(false);
  const byName = manual || (!!current && !listed);
  const edit = (key: keyof SecretRef, next: string) => onChange({ ...value, [key]: next });
  return <div className="schema-field secret-picker"><label htmlFor={id}><span>{label}{required && <b aria-label="required"> *</b>}</span></label>
    <select id={id} value={byName ? "manual" : current} disabled={disabled} aria-invalid={invalid || undefined} onChange={event => {
      if (event.target.value === "manual") { setManual(true); onChange({ name: "", realm }); return; }
      setManual(false);
      onChange(options.find(option => refKey(option.ref) === event.target.value)?.ref);
    }}>
      <option value="">{emptyLabel}</option>
      {options.map(option => <option key={refKey(option.ref)} value={refKey(option.ref)}>{option.text}</option>)}
      <option value="manual">Name a secret…</option>
    </select>
    {byName && <div className="map-entry">
      <label>Secret name<input aria-label={`${label} secret name`} value={value?.name ?? ""} disabled={disabled} onChange={event => edit("name", event.target.value)} /></label>
      <label>Realm<input aria-label={`${label} secret realm`} value={value?.realm ?? ""} disabled={disabled} onChange={event => edit("realm", event.target.value)} /></label>
      <label>Space (optional)<input aria-label={`${label} secret space`} value={value?.space ?? ""} disabled={disabled} onChange={event => edit("space", event.target.value)} /></label>
      <label>Stack (optional)<input aria-label={`${label} secret stack`} value={value?.stack ?? ""} disabled={disabled} onChange={event => edit("stack", event.target.value)} /></label>
    </div>}
    {help && <small>{help}</small>}
  </div>;
}

// ScopeGroup puts a scope picker under a heading that says what its scope applies to (#1568): the connector
// page has one for where an identity is stored and one for whose defaults are shown.
export function ScopeGroup({ title, description, children }: { title: string; description: ReactNode; children: ReactNode }) {
  const id = useId();
  return <div className="connector-store" role="group" aria-labelledby={id}><h3 id={id}>{title}</h3><p className="description">{description}</p>{children}</div>;
}

// ScopePicker chooses a Realm, Space and Stack from the inventory, down to the
// deepest of the given levels.
export function ScopePicker({ label, inventory, levels, value, onChange, disabled = false }: { label: string; inventory: InventoryResource; levels: string[]; value: Scope; onChange: (scope: Scope) => void; disabled?: boolean }) {
  const rows = inventory.data?.rows ?? [[], [], [], []];
  const names = (items: string[], current?: string) => [...new Set([...items, ...(current ? [current] : [])])];
  const realms = names(rows[0].map(row => row.scope[0]), value.realm);
  const spaces = names(rows[1].filter(row => row.scope[0] === value.realm).map(row => row.scope[1]), value.space);
  const stacks = names(rows[2].filter(row => row.scope[0] === value.realm && row.scope[1] === value.space).map(row => row.scope[2]), value.stack);
  const [realmLevel, spaceLevel, stackLevel] = scopeLevels;
  const deeper = levels.includes(spaceLevel) || levels.includes(stackLevel);
  return <div className="schema-fields scope-picker">
    <label className="schema-field"><span>Realm</span><select aria-label={`${label} realm`} value={value.realm} disabled={disabled} onChange={event => onChange({ realm: event.target.value })}><option value="">Select a Realm…</option>{realms.map(name => <option key={name}>{name}</option>)}</select></label>
    {deeper && <label className="schema-field"><span>Space</span><select aria-label={`${label} space`} value={value.space ?? ""} disabled={disabled || !value.realm} onChange={event => onChange({ realm: value.realm, ...(event.target.value ? { space: event.target.value } : {}) })}><option value="">{levels.includes(realmLevel) ? "Realm level" : "Select a Space…"}</option>{spaces.map(name => <option key={name}>{name}</option>)}</select></label>}
    {levels.includes(stackLevel) && <label className="schema-field"><span>Stack</span><select aria-label={`${label} stack`} value={value.stack ?? ""} disabled={disabled || !value.space} onChange={event => onChange({ realm: value.realm, space: value.space, ...(event.target.value ? { stack: event.target.value } : {}) })}><option value="">{levels.includes(spaceLevel) ? "Space level" : "Select a Stack…"}</option>{stacks.map(name => <option key={name}>{name}</option>)}</select></label>}
  </div>;
}

// ScopeRadio asks "Where can cells use it?" (#1693): everywhere in a realm, or
// only in one Space or Stack of it — only the levels the connector accepts.
// The realm is picked only when the inventory has more than one; a narrower
// choice reveals its Space (and Stack) select, starting on `default`.
export function ScopeRadio({ label, connector, inventory, value, onChange, disabled = false }: { label: string; connector: Connector; inventory: InventoryResource; value: Scope; onChange: (scope: Scope) => void; disabled?: boolean }) {
  const name = useId();
  const rows = inventory.data?.rows ?? [[], [], [], []];
  const names = (items: string[], current?: string) => [...new Set([...items, ...(current ? [current] : [])])];
  const spacesOf = (realm: string) => rows[1].filter(row => row.scope[0] === realm).map(row => row.scope[1]);
  const stacksOf = (realm: string, space: string) => rows[2].filter(row => row.scope[0] === realm && row.scope[1] === space).map(row => row.scope[2]);
  const realms = names(rows[0].map(row => row.scope[0]), value.realm);
  const level = scopeLevel(value);
  const [, spaceLevel, stackLevel] = scopeLevels;
  return <fieldset className="connector-store scope-radio" disabled={disabled}>
    <legend>Where can cells use it?</legend>
    {realms.length > 1 && <label className="schema-field"><span>Realm</span><select aria-label={`${label} realm`} value={value.realm} onChange={event => onChange(scopeAtLevel({ realm: event.target.value }, level, spacesOf, stacksOf) ?? { realm: event.target.value })}><option value="">Select a Realm…</option>{realms.map(item => <option key={item}>{item}</option>)}</select></label>}
    {scopeChoices(connector, value.realm).map(choice => {
      const next = scopeAtLevel(value, choice.level, spacesOf, stacksOf);
      return <label className="schema-check" key={choice.level}><input type="radio" name={name} checked={level === choice.level} disabled={!next} onChange={() => next && onChange(next)} /><span>{choice.label}{choice.recommended && <span className="description"> (recommended)</span>}{!next && value.realm && <span className="description"> — none in {value.realm}</span>}</span></label>;
    })}
    {level !== scopeLevels[0] && <div className="schema-fields scope-picker">
      <label className="schema-field"><span>Space</span><select aria-label={`${label} space`} value={value.space ?? ""} onChange={event => onChange(scopeAtLevel({ realm: value.realm, space: event.target.value }, level, spacesOf, stacksOf) ?? { realm: value.realm, space: event.target.value })}>{names(spacesOf(value.realm), value.space).map(item => <option key={item}>{item}</option>)}</select></label>
      {level === stackLevel && <label className="schema-field"><span>Stack</span><select aria-label={`${label} stack`} value={value.stack ?? ""} onChange={event => onChange({ realm: value.realm, space: value.space, stack: event.target.value })}>{names(stacksOf(value.realm, value.space ?? ""), value.stack).map(item => <option key={item}>{item}</option>)}</select></label>}
    </div>}
    <p className="description">{level === scopeLevels[0] ? <>Every cell in {value.realm ? <code>{value.realm}</code> : "the realm"} can be given this identity.</> : level === spaceLevel ? <>Only cells in this Space can be given it.</> : <>Only cells in this Stack can be given it.</>}</p>
  </fieldset>;
}

// SecretTypeChoice picks the Secret type an identity is stored as, each with
// what it gives a cell.
function SecretTypeChoice({ connector, value, onChange, disabled }: { connector: Connector; value: string; onChange: (type: string) => void; disabled: boolean }) {
  const name = useId();
  return <fieldset className="schema-field secret-type-choice" disabled={disabled}>
    <legend>Secret type</legend>
    {secretTypeChoices(connector).map(choice => <label className="schema-check" key={choice.type}><input type="radio" name={name} value={choice.type} checked={value === choice.type} onChange={() => onChange(choice.type)} /><span>{choice.label} — {choice.description}</span></label>)}
  </fieldset>;
}

// PersonFields asks for a person — a commit author — as two boxes, Name and Email (#1745), in the markup
// of the Config form's IdentityFields. The caller joins them into the `Name <email>` string requests carry.
function PersonFields({ label, required = false, name, email, onChange, disabled, help }: { label: string; required?: boolean; name: string; email: string; onChange: (name: string, email: string) => void; disabled: boolean; help?: ReactNode }) {
  const mark = required && <b aria-label="required"> *</b>;
  return <div className="schema-field person-fields" role="group" aria-label={label}>
    <span>{label}{mark}</span>
    <div className="person-fields-inputs">
      <label><span>Name</span><input aria-label={`${label} name`} value={name} disabled={disabled} autoComplete="off" spellCheck={false} onChange={event => onChange(event.target.value, email)} /></label>
      <label><span>Email</span><input type="email" aria-label={`${label} email`} value={email} disabled={disabled} autoComplete="off" spellCheck={false} onChange={event => onChange(name, event.target.value)} /></label>
    </div>
    {help}
  </div>;
}

// PersonFieldInput keeps a schema person field's joined value in the form and its two halves as typed, so
// a space at the end of a name survives the round trip. A value set from outside (a prefill, a reset) is
// split again.
function PersonFieldInput({ field, value, onChange, disabled, help }: { field: FormField; value: string; onChange: (value: string) => void; disabled: boolean; help: ReactNode }) {
  const [parts, setParts] = useState(() => splitPerson(value));
  const sent = useRef(value);
  if (value !== sent.current) { sent.current = value; setParts(splitPerson(value)); }
  return <PersonFields label={field.name} required={field.required} name={parts.name} email={parts.email} disabled={disabled} help={help} onChange={(name, email) => {
    setParts({ name, email });
    sent.current = joinPerson(name, email);
    onChange(sent.current);
  }} />;
}

function IdentityFieldInput({ field, value, onChange, disabled, secrets, listId }: { field: FormField; value: string; onChange: (value: string) => void; disabled: boolean; secrets: unknown[] | undefined; listId: string }) {
  const label = <span>{field.name}{field.required && <b aria-label="required"> *</b>}</span>;
  const help = field.help && <small>{field.help}</small>;
  if (field.type === fieldList && field.enum.length) {
    const selected = value.split(",").map(item => item.trim()).filter(Boolean);
    return <div className="schema-field" role="group" aria-label={field.name}>{label}{field.enum.map(option => <label className="schema-check" key={option}><input type="checkbox" checked={selected.includes(option)} disabled={disabled} onChange={event => onChange(field.enum.filter(item => item === option ? event.target.checked : selected.includes(item)).join(","))} />{option}</label>)}{help}</div>;
  }
  if (field.enum.length) return <label className="schema-field">{label}<select value={value} disabled={disabled} onChange={event => onChange(event.target.value)}><option value="">{field.required ? "Select…" : "None"}</option>{field.enum.map(option => <option key={option}>{option}</option>)}</select>{help}</label>;
  if (field.type === fieldSecret) return <label className="schema-field">{label}<input value={value} list={listId} placeholder="name@realm[/space[/stack]]" disabled={disabled} autoComplete="off" spellCheck={false} onChange={event => onChange(event.target.value)} /><datalist id={listId}>{(secrets ?? []).map(doc => formatSecretReference((doc as { metadata?: SecretRef }).metadata)).filter(Boolean).map(reference => <option key={reference} value={reference} />)}</datalist>{help}</label>;
  if (field.type === fieldPerson) return <PersonFieldInput field={field} value={value} onChange={onChange} disabled={disabled} help={help} />;
  return <label className="schema-field">{label}<input value={value} placeholder={field.type === fieldList ? "Comma-separated" : undefined} disabled={disabled} autoComplete="off" spellCheck={false} onChange={event => onChange(event.target.value)} />{help}</label>;
}

// SecretMaterialFields are the write-only inputs of one Secret type, shared by
// the identity form and the New Secret / Replace value form. An author-only
// identity takes none.
export function SecretMaterialFields({ type, form, set, pending, authorOnly = false, valueLabel = "Value" }: { type: string; form: IdentityFormState; set: (next: Partial<IdentityFormState>) => void; pending: boolean; authorOnly?: boolean; valueLabel?: string }) {
  const secretInput = { autoComplete: "new-password", spellCheck: false, disabled: pending } as const;
  return <div className="schema-fields">
    {type === secretBasicAuth ? <>
      <label className="schema-field"><span>Username<b aria-label="required"> *</b></span><input value={form.username} autoComplete="off" spellCheck={false} disabled={pending} onChange={event => set({ username: event.target.value })} /></label>
      <label className="schema-field"><span>Password or token<b aria-label="required"> *</b></span><input type="password" value={form.password} {...secretInput} onChange={event => set({ password: event.target.value })} /></label>
    </> : type === secretSSHKey ? <>
      <label className="schema-field identity-key"><span>Private key<b aria-label="required"> *</b></span><textarea value={form.privateKey} placeholder="Paste the private key, armor lines included" {...secretInput} onChange={event => set({ privateKey: event.target.value })} /></label>
      <label className="schema-field"><span>Passphrase</span><input type="password" value={form.passphrase} {...secretInput} onChange={event => set({ passphrase: event.target.value })} /></label>
      <label className="schema-field"><span>Public key</span><input value={form.publicKey} placeholder="ssh-ed25519 AAAA…" autoComplete="off" spellCheck={false} disabled={pending} onChange={event => set({ publicKey: event.target.value })} /></label>
      <label className="schema-field"><span>Certificate</span><input value={form.certificate} autoComplete="off" spellCheck={false} disabled={pending} onChange={event => set({ certificate: event.target.value })} /></label>
      <label className="schema-field identity-key"><span>Known hosts</span><textarea value={form.knownHosts} placeholder="example.com ssh-ed25519 AAAA…" autoComplete="off" spellCheck={false} disabled={pending} onChange={event => set({ knownHosts: event.target.value })} /></label>
    </> : authorOnly ? <p className="description identity-no-value">An author-only identity: it stores no value, and gives cells the author and committer only — no credential and no signing.</p>
      : <label className="schema-field"><span>{valueLabel}<b aria-label="required"> *</b></span><input type="password" value={form.value} {...secretInput} onChange={event => set({ value: event.target.value })} /></label>}
  </div>;
}

// IdentityForm creates an identity of a connector — a tagged Secret — or, for
// an existing one, rotates it: same name and scope, new value. The form is
// generated from the connector's secretTypes and fields. Material is
// write-only: held only while typed, sent in the POST body, cleared on every
// submission and never rendered back.
type IdentityResult = { name: string; scope: Scope; action: string; git?: { name: string; scope: Scope; action: string }; defaults?: Array<{ connector: string }>; staleGit?: SecretRef };

// IdentityForm also offers, on a connector whose token can serve git (#1693),
// "Also use this token for git": the server stores the same token as a git
// identity limited to the advertised hosts. With offerDefault, "Make this the
// default" makes the stored identity (or both) the default at the same scope.
export function IdentityForm({ connector, scope, existing, secrets, onSaved, onCancel, offerDefault = true }: {
  connector: Connector;
  scope: Scope;
  existing?: Identity;
  secrets?: unknown[];
  onSaved: (ref: SecretRef, action: string) => void;
  onCancel?: () => void;
  offerDefault?: boolean;
}) {
  const offer = tokenForGit(connector);
  const linked = existing ? linkedGitIdentity(secrets, connector, existing) : undefined;
  const [form, setForm] = useState<IdentityFormState>(() => emptyIdentityForm(connector, existing));
  const [git, setGit] = useState(() => emptyGitForm(!!offer && (!existing || !!linked)));
  const targets = offerDefault ? defaultTargets(connector) : [];
  const [method, setMethod] = useState(() => (targets.find(item => connector.methods.find(m => m.name === item.method)?.default) ?? targets[0])?.method ?? "");
  const target = targets.find(item => item.method === method) ?? targets[0];
  const defaults = useResource<unknown[]>(target && scope.realm ? `/api/connectors/defaults?${scopeQuery(scope)}` : null, 0);
  // "Make this the default" follows the scope — on where no default resolves
  // yet, and for a new token with git — until the user sets it.
  const [makeDefault, setMakeDefault] = useState<boolean>();
  // Until the scope's defaults load, nothing is known to be missing: off.
  const defaultOn = makeDefault ?? (!!target && ((git.git && !existing) || (!!defaults.data && identityDefaultOn(defaults.data, connector, target))));
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const { notify } = useNotify();
  const listId = useId();
  const types = secretTypesOf(connector);
  const title = connector.title || connector.name;
  const where = scopeParts(scope).join(" / ");
  const withGit = !!offer && git.git && form.type === secretOpaque;
  const hosts = offer?.hosts.join(", ") ?? "";
  const set = (next: Partial<IdentityFormState>) => { setForm(old => ({ ...old, ...next })); setError(undefined); };

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const problems = !scope.realm ? ["Choose where the identity is stored."] : [...identityErrors(connector, form), ...(withGit ? tokenGitErrors(git) : [])];
    if (problems.length) { setError({ code: "invalid_request", message: problems.join(" ") }); return; }
    const body = JSON.stringify(identityPayload(connector, scope, form, { makeDefault: !!target && defaultOn, method: target?.method, git: withGit ? tokenGitPayload(git) : undefined }));
    // The material leaves the form before the request is even sent.
    setForm(old => clearMaterial(old));
    setPending(true); setError(undefined);
    try {
      const result = await sendJSON<IdentityResult>(connectorAPI(connector.name, "identities"), body);
      const ref = secretRef({ name: result.name, ...result.scope }) ?? secretRef({ name: form.name, ...scope })!;
      const place = refScope(ref).join(" / ");
      const verb = (action: string) => action === "updated" ? "rotated" : "created";
      const parts = [`${title} identity ${ref.name} ${verb(result.action)} in ${place}.`];
      if (result.git) parts.push(`Git identity ${result.git.name} ${verb(result.git.action)} for ${hosts}.`);
      if (result.defaults?.length) parts.push(result.defaults.length > 1 ? `Both are now the defaults in ${place}.` : `It is now the default in ${place}.`);
      // With git off, a git identity linked to the token keeps the previous one (#1707).
      const stale = staleGitNotice(result);
      if (stale) parts.push(stale);
      notify({ key: `identity:${connector.name}:${refKey(ref)}`, tone: stale ? "warning" : "success", message: parts.join(" ") });
      if (!existing) setForm(emptyIdentityForm(connector));
      onSaved(ref, result.action);
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(false); }
  }

  return <form className="creation-form identity-form" aria-label={existing ? `Rotate ${title} identity ${existing.name}` : `New ${title} identity`} method="post" autoComplete="off" onSubmit={submit}>
    <h3>{existing ? `Rotate ${existing.name}` : `New ${title} identity`}</h3>
    <p className="description">{existing ? <>Replaces the value stored under <code>{existing.name}</code> in {where}. Everything that uses this identity picks up the new value.</> : <>Stored as a Secret tagged <code>{connector.name}</code>{where && <> in {where}</>}.</>} Values are write-only: they are sent once and never shown again.</p>
    <div className="schema-fields">
      <label className="schema-field"><span>Identity name<b aria-label="required"> *</b></span><input value={form.name} disabled={pending || !!existing} autoComplete="off" spellCheck={false} onChange={event => set({ name: event.target.value })} /></label>
      {types.length > 1 && <SecretTypeChoice connector={connector} value={form.type} disabled={pending} onChange={type => set({ type })} />}
      {identityFields(connector, form.type).map(field => <IdentityFieldInput key={field.name} field={field} value={form.fields[field.name] ?? ""} disabled={pending} secrets={secrets} listId={`${listId}-${field.name}`} onChange={value => set({ fields: { ...form.fields, [field.name]: value } })} />)}
    </div>
    <SecretMaterialFields type={form.type} form={form} set={set} pending={pending} authorOnly={storesNoValue(connector, form.type)} valueLabel={offer ? "Token" : "Value"} />
    {offer && form.type === secretOpaque && <div className="schema-fields token-for-git" role="group" aria-label="Use for git">
      <label className="schema-check"><input type="checkbox" checked={git.git} disabled={pending} onChange={event => { setGit(old => ({ ...old, git: event.target.checked })); setError(undefined); }} />Also use this token for git on {hosts}</label>
      {git.git ? <>
        <p className="description">{linked ? <>Rotates the linked git identity <code>{linked.name}</code> too.</> : <>Also stored as the git identity <code>{offer.name}</code>: username and token, for HTTPS on {hosts} only.</>} Signing needs an SSH key: <a href={newIdentityLink(offer.connector, scope)}>Import from this machine</a>.</p>
        <PersonFields label="Commit author" name={git.gitAuthorName} email={git.gitAuthorEmail} disabled={pending} help={<small>Optional. The name and email of commits made with this identity.</small>} onChange={(gitAuthorName, gitAuthorEmail) => { setGit(old => ({ ...old, gitAuthorName, gitAuthorEmail })); setError(undefined); }} />
        <label className="schema-field"><span>Username</span><input value={git.gitUsername} placeholder={offer.username} disabled={pending} autoComplete="off" spellCheck={false} onChange={event => { setGit(old => ({ ...old, gitUsername: event.target.value })); setError(undefined); }} /><small>Optional. {title} accepts any non-empty username with a personal access token; empty stores <code>{offer.username}</code>.</small></label>
      </> : <p className="description">Git is unchanged: the token reaches cells as an API token only.{linked && <> The linked git identity <code>{linked.name}</code> keeps the previous token: check the box to rotate it too.</>}</p>}
    </div>}
    {target && <div className="schema-fields identity-default">
      <label className="schema-check"><input type="checkbox" checked={defaultOn} disabled={pending} onChange={event => setMakeDefault(event.target.checked)} />Make this the default{where && <> in {where}</>}</label>
      {targets.length > 1 && <label className="schema-field"><span>Default for</span><select aria-label={`${title} default method`} value={target.method} disabled={pending || !defaultOn} onChange={event => { setMethod(event.target.value); setMakeDefault(undefined); }}>{targets.map(item => <option key={item.method} value={item.method}>{item.method} method</option>)}</select></label>}
      <small className="description">{defaultOn ? withGit ? <>Cells here that pick no identity get this token for {title} and for git.</> : <>Cells here that pick no identity get this one.</> : <>Stored only: set a default later on the {title} page.</>}</small>
    </div>}
    <div className="connector-actions">
      <button type="submit" disabled={pending}>{pending ? "Saving…" : existing ? "Rotate identity" : "Create identity"}</button>
      {onCancel && <button type="button" className="subtle-button" disabled={pending} onClick={onCancel}>Cancel</button>}
    </div>
    {error && <ErrorNotice error={error} kind="Identity" />}
  </form>;
}

// SetDefault saves an identity as the connector's default at one level of a
// scope ("Set default"): pick an existing identity, or create one first.
function SetDefault({ status, connector, scope, identities, secrets, onChanged, onClose }: {
  status: ConnectorStatus;
  connector: Connector | undefined;
  scope: Scope;
  identities: Identity[];
  secrets: unknown[] | undefined;
  onChanged: () => void;
  onClose: () => void;
}) {
  const levels = allowedLevels(connector, scope);
  const [level, setLevel] = useState(levels[0] ?? scopeLevels[0]);
  const [choice, setChoice] = useState<Partial<SecretRef>>();
  const [creating, setCreating] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const { notify } = useNotify();
  const title = connectorTitle(status);
  const target = scopeAt(scope, level);
  const ref = secretRef(choice);
  async function save() {
    if (!ref || pending) return;
    setPending(true); setError(undefined);
    try {
      await sendJSON(connectorAPI(status.connector, "defaults"), { scope: target, ...(status.method ? { method: status.method } : {}), identity: ref });
      notify({ key: `connector-default:${status.connector}`, tone: "success", message: `${title} default set to ${ref.name} at ${levelLabel(level, scope)}.` });
      onChanged(); onClose();
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(false); }
  }
  return <div className="connector-fix" role="group" aria-label={`Set default for ${title}`}>
    <p className="description">A default fills {title} for every Config in the scope that picked no identity of its own. No Config is rewritten.</p>
    <div className="schema-fields">
      <label className="schema-field"><span>Level</span><select aria-label={`${title} default level`} value={level} disabled={pending} onChange={event => setLevel(event.target.value)}>{levels.map(item => <option key={item} value={item}>{levelLabel(item, scope)}</option>)}</select></label>
      <IdentitySelect label={`${title} default identity`} identities={identities} value={choice} extra={[ref]} emptyLabel="Select an identity…" disabled={pending} realm={scope.realm} onChange={setChoice} />
    </div>
    <div className="connector-actions">
      <button type="button" disabled={!ref || pending || !levels.length} onClick={() => void save()}>{pending ? "Saving…" : "Save default"}</button>
      {connector && <button type="button" className="subtle-button" aria-expanded={creating} disabled={pending} onClick={() => setCreating(value => !value)}>{creating ? "Hide new identity" : "Create a new identity…"}</button>}
      <button type="button" className="subtle-button" disabled={pending} onClick={onClose}>Cancel</button>
    </div>
    {creating && connector && <IdentityForm connector={connector} scope={target} secrets={secrets} offerDefault={false} onSaved={created => { setChoice(created); setCreating(false); onChanged(); }} onCancel={() => setCreating(false)} />}
    {error && <ErrorNotice error={error} kind="Scope" />}
  </div>;
}

// PickForConfig saves a Config fill ("Pick for this Config"): the identity
// wins over every scope default for that one Config.
function PickForConfig({ status, config, identities, logins = [], onChanged, onClose }: { status: ConnectorStatus; config: ConfigMetadata; identities: Identity[]; logins?: LoginIdentity[]; onChanged: () => void; onClose: () => void }) {
  const login = keepsLogin(status);
  const [choice, setChoice] = useState<Partial<SecretRef> | undefined>(login ? statusIdentity(status) : status.origin === "config" ? status.identity : undefined);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const { notify } = useNotify();
  const title = connectorTitle(status);
  const ref = secretRef(choice);
  async function save(identity: SecretRef | null) {
    if (pending) return;
    setPending(true); setError(undefined);
    try {
      await sendJSON("/api/configs/connector", { config: { name: config.name, realm: config.realm, ...(config.space ? { space: config.space } : {}), ...(config.stack ? { stack: config.stack } : {}) }, connector: status.connector, identity });
      notify({ key: `connector-pick:${status.connector}:${config.name}`, tone: "success", message: identity ? `Config ${config.name} now uses ${identity.name} for ${title}.` : `Config ${config.name} uses the ${title} default again.` });
      onChanged(); onClose();
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(false); }
  }
  return <div className="connector-fix" role="group" aria-label={`Pick ${title} identity for this Config`}>
    <p className="description">{login ? <>Saved in the Config as its state volume: every cell of this Config keeps its {title} login — and the rest of its harness state — there. One login identity serves one role.</> : <>Saved in the Config itself: it wins over the Realm, Space and Stack defaults for this Config only.</>}</p>
    <div className="schema-fields">{login
      ? <LoginIdentitySelect label={`${title} login identity for this Config`} identities={logins} value={choice} extra={[statusIdentity(status)]} emptyLabel="Select a login identity…" disabled={pending} onChange={setChoice} />
      : <IdentitySelect label={`${title} identity for this Config`} identities={identities} value={choice} extra={[status.identity]} emptyLabel="Select an identity…" disabled={pending} realm={config.realm} onChange={setChoice} />}</div>
    <div className="connector-actions">
      <button type="button" disabled={!ref || pending || (login && sameRef(ref, statusIdentity(status)))} onClick={() => void save(ref ?? null)}>{pending ? "Saving…" : "Save for this Config"}</button>
      {!login && status.origin === "config" && <button type="button" className="subtle-button" disabled={pending} onClick={() => void save(null)}>Use the default instead</button>}
      <button type="button" className="subtle-button" disabled={pending} onClick={onClose}>Cancel</button>
    </div>
    {error && <ErrorNotice error={error} kind="Config" />}
  </div>;
}

// ConnectorFixes offers the two ways to give a connector an identity: a scope
// default, or a pick saved in the Config. A team-owned Config offers only the
// default — applying the team re-renders its fills.
export function ConnectorFixes({ status, connector, config, identities, logins = [], secrets, onChanged }: {
  status: ConnectorStatus;
  connector: Connector | undefined;
  config: ConfigMetadata;
  identities: Identity[];
  logins?: LoginIdentity[];
  secrets: unknown[] | undefined;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState<"" | "default" | "pick">("");
  const title = connectorTitle(status);
  const team = teamOf({ metadata: config });
  // A login identity has no scope default: it is logged in to, and — on a
  // Config no team owns — picked.
  if (keepsLogin(status)) {
    const identity = statusIdentity(status);
    const known = logins.find(item => sameRef(item.volume, identity));
    return <div className="connector-fixes">
      <div className="connector-actions">
        {identity && <LoginButton connector={status.connector} title={title} volume={identity} state={known?.state ?? status.login?.state} running={!!known?.session} method={status.method} onStarted={onChanged} />}
        {!team && <button type="button" aria-expanded={open === "pick"} aria-label={`Pick ${title} login identity for this Config`} onClick={() => setOpen(open === "pick" ? "" : "pick")}>Pick a login identity</button>}
        <a className="document-action" href={connectorsLink(status.connector, targetScope(config))}>Manage {title} login identities</a>
      </div>
      {open === "pick" && !team && <PickForConfig status={status} config={config} identities={identities} logins={logins} onChanged={onChanged} onClose={() => setOpen("")} />}
    </div>;
  }
  if (!takesIdentity(status.delivery)) return null;
  return <div className="connector-fixes">
    <div className="connector-actions">
      <button type="button" aria-expanded={open === "default"} aria-label={`Set default for ${title}`} onClick={() => setOpen(open === "default" ? "" : "default")}>Set default</button>
      {!team && <button type="button" aria-expanded={open === "pick"} aria-label={`Pick ${title} identity for this Config`} onClick={() => setOpen(open === "pick" ? "" : "pick")}>Pick for this Config</button>}
    </div>
    {open === "default" && <SetDefault status={status} connector={connector} scope={targetScope(config)} identities={identities} secrets={secrets} onChanged={onChanged} onClose={() => setOpen("")} />}
    {open === "pick" && !team && <PickForConfig status={status} config={config} identities={identities} onChanged={onChanged} onClose={() => setOpen("")} />}
  </div>;
}

// NeedsConnectorBadge marks a Config no cell can be created from until the
// named connectors get an identity.
export function NeedsConnectorBadge({ config }: { config: unknown }) {
  const blocking = blockingConnectors(config);
  if (!blocking.length) return null;
  return <span className="badge needs-connector">Needs connector: {[...new Set(blocking.map(connectorTitle))].join(", ")}</span>;
}

type ConfigDoc = { metadata: ConfigMetadata; status?: unknown };

// ConnectorIdentities is the connector part of a render preview: the identity
// each connector uses in the cell and the level that decided it.
export function ConnectorIdentities({ statuses, team = "" }: { statuses: ConnectorStatus[]; team?: string }) {
  if (!statuses.length) return null;
  return <div className="merged-values connector-identities-preview"><h4>Connector identities</h4>
    <p className="description">The identity each connector uses in this cell, and the level that decided it. Only stored secrets are named — no material is shown.</p>
    <table aria-label="Connector identities"><thead><tr><th scope="col">Connector</th><th scope="col">Method</th><th scope="col">Identity</th><th scope="col">State</th></tr></thead><tbody>
      {statuses.map(status => <tr key={`${status.connector}:${status.component}`}><td>{connectorTitle(status)}</td><td>{status.method || "—"}</td><td><StatusIdentity status={status} team={team} />{status.state !== stateResolved && status.message && <p className="setup-error">{status.message}</p>}</td><td><ConnectorStateBadge status={status} /></td></tr>)}
    </tbody></table>
  </div>;
}

// ConfigConnectors is the connector section of a Config's detail: every
// connector it requires, the identity each resolves to and the level it comes
// from, and the fixes for one that does not resolve. A team-owned Config shows
// the same read-only, and offers only a scope default.
export function ConfigConnectors({ doc, revision, onChanged }: { doc: ConfigDoc; scope?: string[]; revision: number; onChanged: () => void }) {
  const statuses = connectorStatuses(doc);
  // Nothing is fetched for a Config that requires no connector.
  const catalog = useResource<Connector[]>(statuses.length ? "/api/connectors" : null, revision);
  const secrets = useResource<unknown[]>(statuses.some(status => takesIdentity(status.delivery)) ? "/api/catalog/secrets" : null, revision);
  const logins = useResource<unknown[]>(statuses.some(keepsLogin) ? "/api/connectors/logins" : null, revision);
  if (!statuses.length) return null;
  const team = teamOf(doc);
  const blocking = statuses.filter(blocksCells);
  return <section className="panel config-connectors" aria-label="Connectors">
    <div className="panel-heading"><h2>Connectors</h2>{blocking.length ? <NeedsConnectorBadge config={doc} /> : <span className="badge">All resolved</span>}</div>
    <div className="panel-body">
      <p className="description">What this Config needs access to, and the identity each connector uses for a cell created now. An identity comes from the Config's own pick, else the Stack, Space or Realm default.</p>
      {team && <p className="connector-team" role="note">Identities are set by team <a href={teamsLink(team)}><code>{team}</code></a>. They are read-only here because applying the team re-renders this Config; change them on the team's Edit team screen (Credentials), or set a scope default.</p>}
      {catalog.error && <p className="stale">The connector catalog could not be loaded: {catalog.error.message}</p>}
      {secrets.error && <p className="stale">Identities could not be listed: {secrets.error.message}. Name a secret by hand instead.</p>}
      <ul className="state-list connector-status-list">{statuses.map(status => <li key={`${status.connector}:${status.component}`} aria-label={`${connectorTitle(status)} connector`}>
        <div className="connector-status">
          <div><strong>{connectorTitle(status)}</strong> <span className="badge">{status.connector}</span>{status.method && <> <span className="badge">{status.method}</span></>} <ConnectorStateBadge status={status} /></div>
          <p className="description"><StatusIdentity status={status} team={team} /></p>
          {status.state !== stateResolved && status.message && <p className="setup-error">{status.message}</p>}
          <ConnectorFixes status={status} connector={catalog.data?.find(item => item.name === status.connector)} config={doc.metadata} identities={identitiesFor(secrets.data, status.connector)} logins={loginIdentitiesFor(logins.data, status.connector)} secrets={secrets.data} onChanged={onChanged} />
          {keepsLogin(status) && <p className="description">Log in opens a short-lived identity cell that mounts only this login identity and runs the {connectorTitle(status)} login; every cell of the same role then reuses it.</p>}
        </div>
      </li>)}</ul>
    </div>
  </section>;
}
