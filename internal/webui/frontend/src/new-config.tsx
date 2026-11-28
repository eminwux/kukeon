import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { InventoryResource } from "./inventory";
import { ErrorNotice, useResource } from "./request";
import { Disclosure } from "./disclosure";
import { InitializePrompt } from "./initialize";
import { newCellLink } from "./routes";
import { MergedValues } from "./merged-values";
import { catalogAPI } from "./catalog-url.mjs";
import { blueprintForm, buildSpec, componentsWithoutIdentity, connectorsWithoutIdentity, emptyAnswers, formSections, groupChoice, imageChoices, imageStatus, missingAnswers, offersState, previewMode, requiredCapabilities, secretChoices, stateChoice } from "./config-form.mjs";
import type { Answers, BlueprintForm, CredentialAnswer, DefaultGitAnswer, FormSection, Identity, InventoryImage, Parameter, RenderedValue, RepoAnswer, RepoGitAnswer, RepoSlot, SecretChoice, SecretRef, SecretSlot, SigningAnswer } from "./config-form.mjs";

import { ConnectorIdentities, IdentitySelect, LoginIdentitySelect, connectorsLink } from "./connector-ui";
import { defaultGroupChoices, defaultRow, deliveryStateVolume, groupComponents, harnessEphemeral, harnessPersistent, harnessStates, identitiesFor, loginIdentitiesFor, originLabel, refLabel, scopeParts, scopeQuery, secretRef as identityRef, takesIdentity, targetScope } from "./connectors.mjs";
import type { Connector, ConnectorDefault, ConnectorStatus } from "./connectors.mjs";
import { apiFetch } from "./transport";
import { routeHref } from "./router";

type ScopeMetadata = { name: string; realm?: string; space?: string; stack?: string };
type CatalogDoc = { metadata: ScopeMetadata; spec?: Record<string, unknown>; kind?: string };
type Preview = { config: CatalogDoc; document: unknown; preview: string; digest?: string; values?: RenderedValue[]; connectors?: ConnectorStatus[] };
type ImageList = { images: InventoryImage[] };

// One collapsible section of the form: `set` counts its answers, `invalid` lists the DOM ids of its fields
// with an unmet required answer, and `essential` (it holds a required answer) sorts it first.
type FormBlock = { key: string; title: string; badge?: string; essential: boolean; invalid: string[]; warning: boolean; set: number; body: ReactNode };
const requiredMessage = "This answer is required.";

const gitTypes = ["basic-auth", "ssh-key"];
const signingTypes = ["ssh-key"];

function scopedName(metadata: ScopeMetadata): string[] {
  return [metadata.realm, metadata.space, metadata.stack, metadata.name].filter((value): value is string => !!value);
}
function catalogLink(kind: string, scope: string[]): string { return routeHref(`/${kind}/` + scope.map(encodeURIComponent).join("/")); }
function fieldId(...parts: string[]): string { return "config-" + parts.join("-").replace(/[^a-zA-Z0-9_-]/g, "-"); }
function choiceKey(ref: Partial<SecretRef> | undefined): string {
  return ref?.name ? JSON.stringify([ref.realm ?? "", ref.space ?? "", ref.stack ?? "", ref.cell ?? "", ref.name]) : "";
}

// Picks a stored secret by scope and name. A slot only offers the secret types
// it accepts; a secret that does not exist yet can still be named by hand.
function SecretPicker({ label, value, onChange, choices, disabled, required, help, invalid }: { label: string; value: Partial<SecretRef> | undefined; onChange: (value: Partial<SecretRef> | undefined) => void; choices: SecretChoice[]; disabled: boolean; required?: boolean; help?: string; invalid?: boolean }) {
  const current = choiceKey(value);
  const listed = choices.some(choice => choiceKey(choice) === current);
  const [manual, setManual] = useState(!!current && !listed);
  const id = fieldId("secret", label);
  return <div className="schema-field secret-picker"><label htmlFor={id}><span>{label}{required && <b aria-label="required"> *</b>}</span></label>
    <select id={id} value={manual ? "manual" : current} disabled={disabled} aria-invalid={invalid || undefined} onChange={event => {
      if (event.target.value === "manual") { setManual(true); onChange({ name: "", realm: "default" }); return; }
      setManual(false);
      const choice = choices.find(item => choiceKey(item) === event.target.value);
      onChange(choice ? { name: choice.name, realm: choice.realm, space: choice.space, stack: choice.stack, cell: choice.cell } : undefined);
    }}>
      <option value="">{required ? "Select a secret…" : "None"}</option>
      {choices.map(choice => <option key={choiceKey(choice)} value={choiceKey(choice)}>{choice.name} — {[choice.realm, choice.space, choice.stack, choice.cell].filter(Boolean).join(" / ")} ({choice.type})</option>)}
      <option value="manual">Name a secret…</option>
    </select>
    {manual && <div className="map-entry"><label>Secret name<input value={value?.name ?? ""} disabled={disabled} onChange={event => onChange({ ...value, name: event.target.value })} /></label><label>Realm<input value={value?.realm ?? ""} disabled={disabled} onChange={event => onChange({ ...value, realm: event.target.value })} /></label></div>}
    {help && <small>{help}</small>}
    {invalid && <small className="field-error">{requiredMessage}</small>}
  </div>;
}

function IdentityFields({ label, value, onChange, disabled }: { label: string; value: Identity | undefined; onChange: (value: Identity) => void; disabled: boolean }) {
  return <><label className="schema-field"><span>{label} name</span><input value={value?.name ?? ""} disabled={disabled} onChange={event => onChange({ ...value, name: event.target.value })} /></label>
    <label className="schema-field"><span>{label} email</span><input type="email" value={value?.email ?? ""} disabled={disabled} onChange={event => onChange({ ...value, email: event.target.value })} /></label></>;
}

function CredentialOptions({ value, onChange, disabled }: { value: CredentialAnswer | undefined; onChange: (value: CredentialAnswer) => void; disabled: boolean }) {
  return <><label className="schema-field"><span>SSH user</span><input value={value?.user ?? ""} placeholder="git" disabled={disabled} onChange={event => onChange({ ...value, user: event.target.value })} /></label>
    <label className="schema-field"><span>SSH port</span><input type="number" min={1} max={65535} value={value?.port ?? ""} placeholder="22" disabled={disabled} onChange={event => onChange({ ...value, port: event.target.value })} /></label>
    <label className="schema-field"><span>Host key policy</span><select value={value?.hostKeyPolicy ?? ""} disabled={disabled} onChange={event => onChange({ ...value, hostKeyPolicy: event.target.value })}><option value="">Strict (pinned or built-in host keys)</option><option value="accept-new">Accept new host keys</option></select></label></>;
}

function SigningFields({ value, sign, onChange, secrets, disabled }: { value: SigningAnswer | undefined; sign: string[] | undefined; onChange: (signing: SigningAnswer | undefined, sign: string[]) => void; secrets: unknown[] | undefined; disabled: boolean }) {
  const selected = sign ?? [];
  return <><SecretPicker label="Signing key secret" value={value?.secretRef} choices={secretChoices(secrets, signingTypes)} disabled={disabled} help="An ssh-key secret. Commits and tags are signed with SSH signing." onChange={ref => onChange(ref ? { ...value, secretRef: ref } : undefined, selected)} />
    {value?.secretRef && <div className="schema-field"><span>Sign</span>{["commits", "tags"].map(kind => <label className="schema-check" key={kind}><input type="checkbox" checked={!selected.length || selected.includes(kind)} disabled={disabled} onChange={event => { const all = selected.length ? selected : ["commits", "tags"]; onChange(value, event.target.checked ? [...new Set([...all, kind])] : all.filter(item => item !== kind)); }} />{kind}</label>)}<small>With neither selected, both are signed.</small></div>}</>;
}

function ParameterField({ item, value, onChange, disabled, images, required, invalid }: { item: Parameter; value: string | boolean | undefined; onChange: (value: string | boolean) => void; disabled: boolean; images: InventoryImage[] | undefined; required: string[]; invalid?: boolean }) {
  const id = fieldId("param", item.name);
  const helpId = `${id}-help`;
  const must = item.required && !item.hasDefault;
  const help = <><small id={helpId}>{item.description}{item.description && " "}{item.hasDefault && item.type !== "bool" && <>Default: <code>{item.default || "(empty)"}</code></>}</small>{invalid && <small className="field-error">{requiredMessage}</small>}</>;
  if (item.type === "bool") return <label className="schema-check"><input id={id} type="checkbox" checked={typeof value === "boolean" ? value : item.default === "true"} disabled={disabled} aria-describedby={helpId} onChange={event => onChange(event.target.checked)} />{item.name}{help}</label>;
  const text = typeof value === "string" ? value : "";
  if (item.type === "enum") return <label className="schema-field" htmlFor={id}><span>{item.name}{must && <b aria-label="required"> *</b>}</span><select id={id} value={text} disabled={disabled} aria-invalid={invalid || undefined} aria-describedby={helpId} onChange={event => onChange(event.target.value)}><option value="">{item.hasDefault ? `Default (${item.default})` : "Select…"}</option>{item.enum.map(option => <option key={option}>{option}</option>)}</select>{help}</label>;
  if (item.type === "image") {
    const choices = imageChoices(images, required);
    const status = imageStatus(images, text || item.default, required);
    return <div className="schema-field image-parameter"><label htmlFor={id}><span>{item.name}{must && <b aria-label="required"> *</b>}</span></label>
      <input id={id} value={text} placeholder={item.default} disabled={disabled} aria-invalid={invalid || undefined} aria-describedby={helpId} onChange={event => onChange(event.target.value)} />
      <select aria-label={`${item.name} from realm images`} value={choices.some(choice => choice.name === text) ? text : ""} disabled={disabled || !choices.length} onChange={event => { if (event.target.value) onChange(event.target.value); }}>
        <option value="">{choices.length ? "Choose from the realm's images…" : "No image in this realm provides the required capabilities"}</option>
        {choices.map(choice => <option key={choice.name} value={choice.name}>{choice.name}{choice.status === "unknown" ? " — capabilities unknown" : ""}</option>)}
      </select>
      {help}
      <small>Required capabilities: {required.length ? required.map(need => <code key={need}>{need} </code>) : "none"}. Images labelled without one of them are not offered; unlabelled images are offered as “capabilities unknown”. Any reference can still be typed.</small>
      {status.status === "missing" && <small className="field-error" role="alert">This image does not provide: {status.missing.join(", ")}. The daemon will refuse to create a cell from it.</small>}
      {status.status === "unknown" && <small>This image declares no capabilities; the daemon cannot check it.</small>}
    </div>;
  }
  return <label className="schema-field" htmlFor={id}><span>{item.name}{must && <b aria-label="required"> *</b>}</span><input id={id} value={text} placeholder={item.default} disabled={disabled} aria-invalid={invalid || undefined} aria-describedby={helpId} onChange={event => onChange(event.target.value)} />{help}</label>;
}

function RepoSlotEditor({ slot, value, onChange, secrets, disabled, invalid }: { slot: RepoSlot; value: RepoAnswer | undefined; onChange: (value: RepoAnswer) => void; secrets: unknown[] | undefined; disabled: boolean; invalid?: boolean }) {
  const answer = value ?? {};
  const git: RepoGitAnswer = answer.git ?? { enabled: false };
  const revision = answer.revision ?? "branch";
  const setGit = (next: RepoGitAnswer) => onChange({ ...answer, git: next });
  return <fieldset className="schema-object"><legend>Repository: {slot.name}{slot.required && <b aria-label="required"> *</b>}</legend>
    <p className="description">{slot.required ? "Required." : "Optional — left blank, the repository is not cloned."}{slot.target && <> Cloned to <code>{slot.target}</code>.</>}</p>
    <div className="schema-fields">
      <label className="schema-field"><span>{slot.name} URL{slot.required && <b aria-label="required"> *</b>}</span><input id={fieldId("repo", slot.name, "url")} value={answer.url ?? ""} placeholder="git@github.com:owner/repo.git or https://…" disabled={disabled} aria-invalid={invalid || undefined} onChange={event => onChange({ ...answer, url: event.target.value })} /><small>The transport — SSH or HTTPS — follows the credential bound to this repository.</small>{invalid && <small className="field-error">{requiredMessage}</small>}</label>
      <div className="schema-field"><label htmlFor={fieldId("repo", slot.name, "revision")}><span>{slot.name} revision</span></label><div className="map-entry"><select id={fieldId("repo", slot.name, "revision")} value={revision} disabled={disabled} onChange={event => onChange({ ...answer, revision: event.target.value === "ref" ? "ref" : "branch" })}><option value="branch">Branch</option><option value="ref">Tag or commit</option></select><input aria-label={`${slot.name} ${revision === "ref" ? "tag or commit" : "branch"}`} value={(revision === "ref" ? answer.ref : answer.branch) ?? ""} placeholder={revision === "ref" ? "v1.2.3 or a full commit SHA" : "Remote default branch"} disabled={disabled} onChange={event => onChange(revision === "ref" ? { ...answer, ref: event.target.value } : { ...answer, branch: event.target.value })} /></div></div>
    </div>
    <label className="schema-check"><input type="checkbox" checked={git.enabled === true} disabled={disabled} onChange={event => setGit({ ...git, enabled: event.target.checked })} />{slot.name} uses its own git identity<small>Otherwise it uses the default identity inherited from the Realm, Space, Stack or this Config. Unset fields below fall back to that default.</small></label>
    {git.enabled && <div className="schema-fields advanced-fields">
      <IdentityFields label="Author" value={git.author} disabled={disabled} onChange={author => setGit({ ...git, author })} />
      <IdentityFields label="Committer" value={git.committer} disabled={disabled} onChange={committer => setGit({ ...git, committer })} />
      <SecretPicker label={`${slot.name} credential secret`} value={git.credential?.secretRef} choices={secretChoices(secrets, gitTypes)} disabled={disabled} help="A basic-auth secret (HTTPS) or an ssh-key secret (SSH). API tokens are never git credentials." onChange={ref => setGit({ ...git, credential: ref ? { ...git.credential, secretRef: ref } : undefined })} />
      {git.credential?.secretRef && <CredentialOptions value={git.credential} disabled={disabled} onChange={credential => setGit({ ...git, credential })} />}
      <SigningFields value={git.signing} sign={git.sign} secrets={secrets} disabled={disabled} onChange={(signing, sign) => setGit({ ...git, signing, sign })} />
    </div>}
  </fieldset>;
}

function DefaultGitEditor({ value, onChange, secrets, disabled }: { value: DefaultGitAnswer | null; onChange: (value: DefaultGitAnswer) => void; secrets: unknown[] | undefined; disabled: boolean }) {
  const git: DefaultGitAnswer = value ?? { enabled: false };
  const credentials = git.credentials ?? [];
  return <fieldset className="schema-object"><legend>Default git identity</legend>
    <p className="description">Cells inherit their git identity, credentials and signing from the Realm, Space and Stack defaults. Override them here only when this Config needs something different: identity and signing replace the inherited value, credentials merge by host.</p>
    <label className="schema-check"><input type="checkbox" checked={git.enabled === true} disabled={disabled} onChange={event => onChange({ ...git, enabled: event.target.checked })} />Override the inherited default identity</label>
    {git.enabled && <><div className="schema-fields advanced-fields">
      <IdentityFields label="Default author" value={git.author} disabled={disabled} onChange={author => onChange({ ...git, author })} />
      <IdentityFields label="Default committer" value={git.committer} disabled={disabled} onChange={committer => onChange({ ...git, committer })} />
      <SigningFields value={git.signing} sign={git.sign} secrets={secrets} disabled={disabled} onChange={(signing, sign) => onChange({ ...git, signing, sign })} />
    </div>
    {credentials.map((credential, index) => <section className="schema-array-item" key={index}><div className="schema-array-heading"><h4>Credential {index + 1}</h4><button type="button" className="subtle-button" disabled={disabled} onClick={() => onChange({ ...git, credentials: credentials.filter((_, current) => current !== index) })}>Remove</button></div><div className="schema-fields">
      <label className="schema-field"><span>Host</span><input value={credential.host ?? ""} placeholder="github.com or github.com/owner" disabled={disabled} onChange={event => onChange({ ...git, credentials: credentials.map((item, current) => current === index ? { ...item, host: event.target.value } : item) })} /></label>
      <SecretPicker label={`Credential ${index + 1} secret`} value={credential.secretRef} choices={secretChoices(secrets, gitTypes)} disabled={disabled} onChange={ref => onChange({ ...git, credentials: credentials.map((item, current) => current === index ? { ...item, secretRef: ref } : item) })} />
      <CredentialOptions value={credential} disabled={disabled} onChange={next => onChange({ ...git, credentials: credentials.map((item, current) => current === index ? { ...item, ...next } : item) })} />
    </div></section>)}
    <button type="button" disabled={disabled} onClick={() => onChange({ ...git, credentials: [...credentials, { host: "" }] })}>Add credential</button></>}
  </fieldset>;
}

function ConfigForm({ blueprint, blueprintScope, form, revision, inventory, onCreated }: { blueprint: CatalogDoc; blueprintScope: string[]; form: BlueprintForm; revision: number; inventory: InventoryResource; onCreated: () => void }) {
  const realms = inventory.data?.rows[0] ?? [];
  const [name, setName] = useState("");
  const [realm, setRealm] = useState("");
  const [space, setSpace] = useState("");
  const [stack, setStack] = useState("");
  const [answers, setAnswers] = useState<Answers>(() => emptyAnswers(form));
  const [requestedMode, setRequestedMode] = useState("");
  const [preview, setPreview] = useState<Preview>();
  const [created, setCreated] = useState<string[]>();
  const [pending, setPending] = useState(false);
  const [stage, setStage] = useState<"preview" | "create">("preview");
  const [error, setError] = useState("");
  const [opened, setOpened] = useState<Record<string, boolean>>({});
  const [showInvalid, setShowInvalid] = useState(false);
  const [focusRequest, setFocusRequest] = useState<{ id?: string; element?: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement }>();
  const autoOpened = useRef(new Set<string>());
  const review = useRef<HTMLDivElement>(null);
  // Runs after the sections holding the field have opened. A natively invalid field also shows the
  // browser's own message (a malformed email, a port out of range).
  useEffect(() => {
    if (focusRequest?.id) document.getElementById(focusRequest.id)?.focus();
    if (focusRequest?.element) { focusRequest.element.focus(); focusRequest.element.reportValidity(); }
  }, [focusRequest]);
  useEffect(() => { if (preview) review.current?.scrollIntoView({ block: "nearest" }); }, [preview]);
  const spaces = (inventory.data?.rows[1] ?? []).filter(row => row.scope[0] === realm);
  const stacks = (inventory.data?.rows[2] ?? []).filter(row => row.scope[0] === realm && row.scope[1] === space);
  // Default to the scope cells land in when a coordinate is left open.
  useEffect(() => {
    if (!inventory.data || realm) return;
    const first = realms.find(row => row.scope[0] === "default") ?? realms[0];
    if (!first) return;
    const hasSpace = inventory.data.rows[1].some(row => row.scope[0] === first.scope[0] && row.scope[1] === "default");
    const hasStack = hasSpace && inventory.data.rows[2].some(row => row.scope[0] === first.scope[0] && row.scope[1] === "default" && row.scope[2] === "default");
    setRealm(first.scope[0]); setSpace(hasSpace ? "default" : ""); setStack(hasStack ? "default" : "");
  }, [inventory.data, realm, realms]);
  const secrets = useResource<unknown[]>("/catalog/secrets", revision);
  const logins = useResource<unknown[]>("/connectors/logins", revision);
  const images = useResource<ImageList>(realm ? `/realms/${encodeURIComponent(realm)}/image-inventory` : null, revision);
  // Connector components (#1548): one method per group, and an identity picker per connector that
  // shows the default the Config's scope would inherit. Nothing is fetched for a blueprint without them.
  const hasConnectors = form.components.some(component => component.connector);
  const { groups, ungrouped: optional } = useMemo(() => groupComponents(form.components), [form]);
  const connectors = useResource<Connector[]>(hasConnectors ? "/connectors" : null, revision);
  const target = targetScope({ realm, space, stack });
  const defaults = useResource<ConnectorDefault[]>(hasConnectors && realm ? `/connectors/defaults?${scopeQuery(target)}` : null, revision);
  // A group starts on the method the catalog marks default until the operator chooses one.
  const effective = useMemo<Answers>(() => ({ ...answers, groups: { ...defaultGroupChoices(form.components, connectors.data), ...answers.groups } }), [answers, form, connectors.data]);
  const mode = previewMode(form, effective, requestedMode);
  const sections = formSections(form, effective, mode);
  const required = requiredCapabilities(form, effective);
  const missing = [...missingAnswers(form, effective, mode), ...connectorsWithoutIdentity(form, effective, defaults.data, mode).map(title => `${title} identity`)];
  const enabledModes = form.modes.filter(item => !answers.modes.enabled.length || answers.modes.enabled.includes(item));
  const blueprintRef = { name: blueprint.metadata.name, realm: blueprint.metadata.realm, space: blueprint.metadata.space, stack: blueprint.metadata.stack };
  function reviewChanged() { setPreview(undefined); setCreated(undefined); setError(""); }
  function change(next: Partial<Answers>) { setAnswers(old => ({ ...old, ...next })); reviewChanged(); }
  async function submit(create: boolean) {
    if (pending) return;
    setPending(true); setStage(create ? "create" : "preview"); setError("");
    const scope = [realm, space, stack, name.trim()].filter(Boolean);
    try {
      const body = { config: { name: name.trim(), realm, ...(space ? { space } : {}), ...(stack ? { stack } : {}) }, spec: buildSpec(blueprintRef, form, effective), ...(mode ? { mode } : {}), ...(create && preview ? { preview: preview.preview } : {}) };
      const response = await apiFetch(`/configs/from-blueprint/${create ? "create" : "preview"}`, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(body) });
      const result = await response.json();
      if (!response.ok) {
        const message = result.error?.message ?? result.message ?? `Request failed (HTTP ${response.status})`;
        throw new Error(create && (result.error?.code === "unavailable" || response.status >= 500) ? `The creation outcome could not be confirmed. Refresh the Configs list before retrying. ${message}` : message);
      }
      if (create) { setCreated(scope); setPreview(undefined); onCreated(); }
      else setPreview(result as Preview);
    } catch (failure) {
      const message = failure instanceof Error ? failure.message : "The request could not reach the web server.";
      setError(create && failure instanceof TypeError ? `The creation outcome could not be confirmed. Refresh the Configs list before retrying. ${message}` : message);
    }
    finally { setPending(false); }
  }
  // Progressive disclosure (#1568). The Config section stays open; every other section is a disclosure that
  // says how many answers it holds. Sections that hold a required answer come first, and a section opens by
  // itself the first time it has an unmet required answer or an image that lacks a required capability.
  const missingNames = new Set(missingAnswers(form, effective, mode));
  // Keyed on the component: two components may bind one connector and resolve differently.
  const unresolvedComponents = componentsWithoutIdentity(form, effective, defaults.data, mode);
  const answered = (value: string | boolean | undefined) => typeof value === "boolean" || (typeof value === "string" && value !== "");
  const needed = (item: Parameter) => item.required && !item.hasDefault && item.type !== "bool";
  const takesPick = (section: FormSection) => !!section.connector && !!section.component && takesIdentity(section.connector.delivery);
  // The DOM ids of a section's fields with an unmet required answer, in the order the fields render:
  // parameters, the connector identity, then secret slots.
  const sectionInvalid = (section: FormSection) => [
    ...section.parameters.filter(item => needed(item) && missingNames.has(item.name)).map(item => fieldId("param", item.name)),
    ...(takesPick(section) && !identityRef(answers.connectors[section.component!]) && unresolvedComponents.includes(section.component!) ? [fieldId("identity", section.component!)] : []),
    ...(takesPick(section) ? [] : section.secrets.filter(slot => slot.required && missingNames.has(slot.name)).map(slot => fieldId("secret", `${slot.name} secret`))),
  ];
  const repoInvalid = form.repoSlots.filter(slot => slot.required && missingNames.has(slot.name)).map(slot => fieldId("repo", slot.name, "url"));
  const invalidIds = new Set([...sections.flatMap(sectionInvalid), ...repoInvalid]);
  // Fields are marked only after a submit attempt, and only while their answer is still missing.
  const flagged = (id: string) => showInvalid && invalidIds.has(id);
  const blocks: FormBlock[] = [];
  if (optional.length > 0 || groups.length > 0) blocks.push({ key: "components", title: "Components", essential: false, invalid: [], warning: false, set: answers.components.length + Object.keys(answers.groups).length,
    body: <><p className="description">Optional components bundle the parameters, secrets and image capabilities of one concern. Their fields appear once enabled.</p>
      {groups.length > 0 && <div className="schema-fields">{groups.map(({ group, members }) => {
        const chosen = members.find(member => member.name === groupChoice(form, effective, group));
        const titles = [...new Set(members.map(member => member.connector?.title || member.connector?.name || ""))];
        const label = `${titles.length === 1 && titles[0] ? titles[0] : group} method`;
        return <label className="schema-field" key={group}><span>{label}<b aria-label="required"> *</b></span><select aria-label={label} value={chosen?.name ?? ""} disabled={pending} onChange={event => change({ groups: { ...answers.groups, [group]: event.target.value } })}>{members.map(member => <option key={member.name} value={member.name}>{member.connector?.method || member.name}</option>)}</select><small>{chosen?.description}{chosen?.description && " "}The methods of the <code>{group}</code> group are alternatives: a Config enables exactly one.</small></label>;
      })}</div>}
      {optional.map(component => <label className="schema-check" key={component.name}><input type="checkbox" checked={answers.components.includes(component.name)} disabled={pending} onChange={event => change({ components: event.target.checked ? [...answers.components, component.name] : answers.components.filter(item => item !== component.name) })} /><span>{component.name} <span className="badge">{component.kind}</span></span><small>{component.description}{component.requires.length > 0 && <> Requires: {component.requires.join(", ")}.</>}</small></label>)}</> });
  if (form.modes.length > 0) blocks.push({ key: "modes", title: "Modes", essential: false, invalid: [], warning: false, set: (enabledModes.length < form.modes.length ? 1 : 0) + (answers.modes.default ? 1 : 0),
    body: <><p className="description">A harness mode is picked per cell at create time, so one Config serves both an attached session and a dispatched run. Choose which modes this Config allows and its default.</p>
      {form.modes.map(item => <label className="schema-check" key={item}><input type="checkbox" checked={enabledModes.includes(item)} disabled={pending || (enabledModes.length === 1 && enabledModes[0] === item)} onChange={event => change({ modes: { ...answers.modes, enabled: form.modes.filter(candidate => candidate === item ? event.target.checked : enabledModes.includes(candidate)) } })} />Allow {item} mode</label>)}
      <div className="schema-fields">
        <label className="schema-field"><span>Default mode</span><select value={enabledModes.includes(answers.modes.default) ? answers.modes.default : ""} disabled={pending} onChange={event => change({ modes: { ...answers.modes, default: event.target.value } })}><option value="">Blueprint default</option>{enabledModes.map(item => <option key={item}>{item}</option>)}</select></label>
        <label className="schema-field"><span>Preview as mode</span><select value={mode} disabled={pending} onChange={event => { setRequestedMode(event.target.value); reviewChanged(); }}>{enabledModes.map(item => <option key={item}>{item}</option>)}</select><small>Shows that mode's own parameters and secrets and renders the preview cell in it.</small></label>
      </div></> });
  for (const section of sections) {
    const identity = takesPick(section);
    const picked = identity && !!identityRef(answers.connectors[section.component!]);
    blocks.push({
      key: section.key, title: section.kind === "group" ? section.title[0].toUpperCase() + section.title.slice(1) : section.title, badge: section.kind !== "group" ? section.kind : undefined,
      essential: section.parameters.some(needed) || (identity ? !section.connector!.optional : section.secrets.some(slot => slot.required)),
      invalid: sectionInvalid(section),
      warning: section.parameters.some(item => { const value = answers.values[item.name]; return item.type === "image" && imageStatus(images.data?.images, (typeof value === "string" && value) || item.default, required).status === "missing"; }),
      set: section.parameters.filter(item => answered(answers.values[item.name])).length + (picked ? 1 : 0) + (identity ? 0 : section.secrets.filter(slot => !!answers.secrets[slot.name]).length),
      body: <>{section.description && <p className="description">{section.description}</p>}{section.kind === "mode" && <p className="description">Values entered here are saved in the Config as that mode's default; a cell can still set them with <code>--param</code>.</p>}
      <div className="schema-fields">
        {section.parameters.map(item => <ParameterField key={item.name} item={item} invalid={flagged(fieldId("param", item.name))} value={answers.values[item.name]} disabled={pending} images={images.data?.images} required={required} onChange={value => change({ values: { ...answers.values, [item.name]: value } })} />)}
        {section.connector && section.component && takesIdentity(section.connector.delivery) && (() => {
          const { connector, component } = section;
          const title = connector.title || connector.name;
          const row = defaultRow(defaults.data, { connector: connector.name, component });
          const picked = identityRef(answers.connectors[component]);
          const none = !!defaults.data && !row?.effective;
          const emptyLabel = !defaults.data ? "Default (inherited from the Realm, Space or Stack)" : row?.effective ? `Default: ${refLabel(row.effective)} (${originLabel(row.origin)})` : connector.optional ? "No default" : "No default — required";
          const envName = section.secrets[0]?.envName;
          return <IdentitySelect key={`connector:${component}`} id={fieldId("identity", component)} invalid={flagged(fieldId("identity", component))} label={`${title} identity`} identities={identitiesFor(secrets.data, connector.name)} value={answers.connectors[component]} emptyLabel={emptyLabel} disabled={pending} required={!connector.optional} realm={realm} onChange={ref => { const next = { ...answers.connectors }; if (ref) next[component] = ref; else delete next[component]; change({ connectors: next }); }}
            help={<>{envName && <>Delivered as {envName}. </>}Choosing the default saves no fill: the Config keeps inheriting it from {scopeParts(target).join(" / ")}.{none && !picked && !connector.optional && <span className="field-error"> Nothing resolves yet — pick an identity or <a href={connectorsLink(connector.name, target)}>set a default</a>.</span>}</>} />;
        })()}
        {section.connector?.delivery === deliveryStateVolume && section.component && (() => {
          const { connector, component } = section;
          const title = connector.title || connector.name;
          const picked = identityRef(answers.connectors[component]);
          return <LoginIdentitySelect key={`login:${component}`} label={`${title} login identity`} identities={loginIdentitiesFor(logins.data, connector.name)} value={answers.connectors[component]} emptyLabel="The state volume named in this form" disabled={pending} onChange={ref => { const next = { ...answers.connectors }; if (ref) next[component] = ref; else delete next[component]; change({ connectors: next }); }}
            help={<>The login is kept in a login identity — a Volume mounted as the harness state directory. {picked ? <>Cells of this Config keep their state in <code>{refLabel(picked)}</code>; the state volume values below are set from it.</> : <>Pick one to reuse a login, or leave it and name a state volume of the Config's own below.</>} One login identity serves one role. <a href={connectorsLink(connector.name, target)}>Create or log in to a {title} login identity</a>.</>} />;
        })()}
        {section.connector && section.connector.harness && section.connector.delivery !== deliveryStateVolume && offersState(form, effective) && <label className="schema-field"><span>Harness state</span><select aria-label={`${section.connector.title || section.connector.name} harness state`} value={stateChoice(form, effective) || harnessPersistent} disabled={pending} onChange={event => change({ state: event.target.value === harnessEphemeral ? harnessEphemeral : "" })}>{harnessStates.map(item => <option key={item} value={item}>{item}{item === harnessPersistent ? " (default)" : ""}</option>)}</select><small>{stateChoice(form, effective) === harnessEphemeral ? "Ephemeral: cells mount no state volume; the harness state directory lives and dies with each cell, which authenticates from its Secret." : "Persistent: cells mount the state volume named in this form, so sessions and settings survive a cell."}</small></label>}
        {!(section.connector && takesIdentity(section.connector.delivery)) && section.secrets.map((slot: SecretSlot) => <SecretPicker key={slot.name} label={`${slot.name} secret`} invalid={flagged(fieldId("secret", `${slot.name} secret`))} required={slot.required} value={answers.secrets[slot.name]} choices={secretChoices(secrets.data, ["opaque"])} disabled={pending} help={slot.envName ? `Delivered as ${slot.envName}.` : undefined} onChange={ref => { const next = { ...answers.secrets }; if (ref) next[slot.name] = ref; else delete next[slot.name]; change({ secrets: next }); }} />)}
      </div></>,
    });
  }
  blocks.push({ key: "capabilities", title: "Image capabilities", essential: false, invalid: [], warning: false, set: answers.requires.trim() ? 1 : 0,
    body: <><div className="schema-fields"><label className="schema-field"><span>Additional required capabilities</span><input value={answers.requires} placeholder="go, node" disabled={pending} onChange={event => change({ requires: event.target.value })} /><small>Added to the blueprint's and the enabled components'. Currently required: {required.length ? required.join(", ") : "none"}.</small></label></div>
      {images.error && <p className="stale">Realm images could not be loaded: {images.error.message}. Image references can still be typed.</p>}</> });
  if (form.repoSlots.length > 0) blocks.push({ key: "repositories", title: "Repositories", essential: form.repoSlots.some(slot => slot.required), warning: false,
    invalid: repoInvalid,
    set: form.repoSlots.filter(slot => !!answers.repos[slot.name]?.url?.trim()).length,
    body: <><p className="description">Each source is cloned by the cell with stored git credentials — nothing from the host is mounted. A source may state its own identity; otherwise it uses the default.</p>
      {form.repoSlots.map(slot => <RepoSlotEditor key={slot.name} slot={slot} invalid={flagged(fieldId("repo", slot.name, "url"))} value={answers.repos[slot.name]} secrets={secrets.data} disabled={pending} onChange={value => change({ repos: { ...answers.repos, [slot.name]: value } })} />)}</> });
  blocks.push({ key: "git", title: "Default git identity", essential: false, invalid: [], warning: false, set: answers.git?.enabled ? 1 : 0,
    body: <><DefaultGitEditor value={answers.git} secrets={secrets.data} disabled={pending} onChange={git => change({ git })} /></> });
  const ordered = [...blocks.filter(block => block.essential), ...blocks.filter(block => !block.essential)];
  const attention = blocks.filter(block => block.invalid.length > 0 || block.warning).map(block => block.key).join("\n");
  useLayoutEffect(() => {
    const fresh = attention.split("\n").filter(key => key && !autoOpened.current.has(key));
    if (!fresh.length) return;
    for (const key of fresh) autoOpened.current.add(key);
    setOpened(old => ({ ...Object.fromEntries(fresh.map(key => [key, true])), ...old }));
  }, [attention]);
  // Submitting with an unmet required answer sends nothing: it opens every section that holds one and puts
  // the cursor on the first.
  function previewOrFocus(target: HTMLFormElement) {
    const invalid = ordered.flatMap(block => block.invalid.map(id => ({ key: block.key, id })));
    if (invalid.length) {
      setShowInvalid(true);
      setOpened(old => ({ ...old, ...Object.fromEntries(invalid.map(item => [item.key, true])) }));
      setFocusRequest({ id: invalid[0].id });
      return;
    }
    // The form is noValidate because the browser cannot focus an invalid field inside a collapsed section;
    // its typed inputs (an email, a port) are checked here instead, with the section opened first.
    const native = target.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>("input:invalid, select:invalid, textarea:invalid");
    if (native) {
      const key = native.closest<HTMLElement>("[data-section]")?.dataset.section;
      if (key) setOpened(old => ({ ...old, [key]: true }));
      setFocusRequest({ element: native });
      return;
    }
    void submit(false);
  }
  if (created) return <section className="panel" aria-label="Config created"><div className="panel-heading"><h2>Config created</h2><span className="badge">{created.slice(0, -1).join(" / ")}</span></div><div className="panel-body creation-form">
    <p className="creation-success" role="status"><span>CellConfig <strong>{created.at(-1)}</strong> was saved. It answers the <strong>{blueprint.metadata.name}</strong> blueprint's form; no cell was created.</span></p>
    <p><a className="resource-name" href={catalogLink("configs", created)}>Open Config {created.at(-1)}</a></p>
    <a className="new-cell-action config-create-action" href={newCellLink(created)}>Create a Cell from it <span aria-hidden="true">→</span></a>
    <button type="button" className="subtle-button" onClick={() => { setCreated(undefined); setName(""); }}>Create another Config from this Blueprint</button>
  </div></section>;
  return <section className="panel"><div className="panel-heading"><h2>Answer the {blueprint.metadata.name} form</h2><span className="badge">{blueprintScope.slice(0, -1).join(" / ")}</span></div><form className="panel-body creation-form" noValidate onSubmit={event => { event.preventDefault(); previewOrFocus(event.currentTarget); }}>
    <p className="description">A blueprint is a form; a config is the answers. Every field below comes from the blueprint document — its parameters, components, modes and repository and secret slots. Security posture (root, host mounts) is fixed by the blueprint and cannot be changed here.</p>
    <div className="schema-section"><h3>Config</h3><div className="schema-fields">
      <label className="schema-field" htmlFor="config-name"><span>Config name <b aria-label="required"> *</b></span><input id="config-name" value={name} disabled={pending} onChange={event => { setName(event.target.value); reviewChanged(); }} /></label>
      <label className="schema-field"><span>Cell name prefix</span><input value={answers.prefix} placeholder={name || "Config name"} disabled={pending} onChange={event => change({ prefix: event.target.value })} /><small>Cells created without a name are called <code>&lt;prefix&gt;-&lt;6hex&gt;</code>.</small></label>
      <label className="schema-field"><span>Realm <b aria-label="required"> *</b></span><select aria-label="Config realm" value={realm} disabled={pending} onChange={event => { setRealm(event.target.value); setSpace(""); setStack(""); reviewChanged(); }}><option value="">Select a Realm…</option>{realms.map(row => <option key={row.scope[0]}>{row.scope[0]}</option>)}</select></label>
      <label className="schema-field"><span>Space</span><select aria-label="Config space" value={space} disabled={pending || !realm} onChange={event => { setSpace(event.target.value); setStack(""); reviewChanged(); }}><option value="">Realm-scoped</option>{spaces.map(row => <option key={row.scope[1]}>{row.scope[1]}</option>)}</select></label>
      <label className="schema-field"><span>Stack</span><select aria-label="Config stack" value={stack} disabled={pending || !space} onChange={event => { setStack(event.target.value); reviewChanged(); }}><option value="">{space ? "Space-scoped" : "Realm-scoped"}</option>{stacks.map(row => <option key={row.scope[2]}>{row.scope[2]}</option>)}</select><small>Cells land in the Config's scope; an open coordinate means <code>default</code>.</small></label>
    </div></div>

    {ordered.map(block => <Disclosure key={block.key} level={3} className="schema-disclosure" title={block.title} open={opened[block.key] ?? false} onToggle={open => setOpened(old => ({ ...old, [block.key]: open }))}
      badge={block.badge && <span className="badge">{block.badge}</span>}
      summary={<>{block.set ? `${block.set} set` : "Defaults"}{block.invalid.length > 0 && <span className="disclosure-required"> · {block.invalid.length} required</span>}</>}>
      <div className="schema-section" data-section={block.key}>{block.body}</div>
    </Disclosure>)}

    {secrets.error && <p className="stale">Secrets could not be listed: {secrets.error.message}. Name a secret by hand instead.</p>}
    {defaults.error && <p className="stale">Connector defaults could not be loaded for {scopeParts(target).join(" / ")}: {defaults.error.message}. A picker's Default still inherits whatever the scope sets.</p>}

    {missing.length > 0 && <p className="description" role="note">Still required: {missing.join(", ")}.</p>}
    {error && <p className="error" role="alert">{error}</p>}{pending && <p role="status">{stage === "create" ? "Saving Config…" : "Preparing preview…"}</p>}
    <button type="submit" className={preview ? undefined : "primary-action"} disabled={pending || !name.trim() || !realm}>{preview ? "Refresh preview" : "Preview Config"}</button>
    {preview && <div className="creation-review" ref={review}><h3>Review Config</h3>
      <p className="description">The daemon rendered a cell from these answers{mode && <> in <strong>{mode}</strong> mode</>} without saving anything. Creating saves the Config below; no cell is created.</p>
      {preview.connectors && <ConnectorIdentities statuses={preview.connectors} />}
      {preview.values && <MergedValues values={preview.values} digest={preview.digest} />}
      <h4>CellConfig to save</h4><pre tabIndex={0} aria-label="CellConfig to save">{JSON.stringify(preview.config, null, 2)}</pre>
      <details><summary>Cell this Config renders</summary><pre tabIndex={0} aria-label="Cell preview">{JSON.stringify(preview.document, null, 2)}</pre></details>
      <button type="button" className="primary-action" disabled={pending} onClick={() => void submit(true)}>Create Config</button>
    </div>}
  </form></section>;
}

// NewConfigPage creates a CellConfig from a blueprint using only the form the
// blueprint document describes (#1472, step 16).
export function NewConfigPage({ revision, inventory, blueprintScope, onCreated }: { revision: number; inventory: InventoryResource; blueprintScope?: string[]; onCreated: () => void }) {
  const blueprints = useResource<CatalogDoc[]>("/catalog/blueprints", revision);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(blueprintScope?.length ? JSON.stringify(blueprintScope) : "");
  const rows = (blueprints.data ?? []).map(doc => ({ doc, scope: scopedName(doc.metadata) }));
  const filtered = rows.filter(({ scope }) => scope.join(" / ").toLowerCase().includes(query.toLowerCase()) || JSON.stringify(scope) === selected);
  const scope = useMemo(() => { try { const parsed: unknown = JSON.parse(selected || "[]"); return Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === "string") : []; } catch { return []; } }, [selected]);
  const detail = useResource<CatalogDoc>(scope.length >= 2 ? catalogAPI("blueprints", scope) : null, revision);
  const form = useMemo(() => detail.data ? blueprintForm(detail.data) : undefined, [detail.data]);
  return <div className="creation-layout">
    <section className="panel"><div className="panel-heading"><h2>Choose a Blueprint</h2><span className="badge">Form source</span></div><div className="panel-body creation-form">
      {blueprints.loading && <p role="status">Loading Blueprints…</p>}{blueprints.error && <ErrorNotice error={blueprints.error} kind="Blueprint" />}{blueprints.error && blueprints.data && <p className="stale">Showing stale Blueprints.</p>}
      <label>Search Blueprints<input type="search" value={query} placeholder="Name or full scope" onChange={event => setQuery(event.target.value)} /></label>
      <label>Blueprint<select value={selected} onChange={event => setSelected(event.target.value)}><option value="">Select a Blueprint…</option>{filtered.map(row => <option key={JSON.stringify(row.scope)} value={JSON.stringify(row.scope)}>{row.doc.metadata.name} — {row.scope.slice(0, -1).join(" / ")}</option>)}{selected && !rows.some(row => JSON.stringify(row.scope) === selected) && <option value={selected}>{scope.at(-1)} — {scope.slice(0, -1).join(" / ")}</option>}</select></label>
      {!blueprints.loading && !blueprints.error && !rows.length && <div className="initialize-empty" role="note"><p className="description">No Blueprints are available. Initialize stores the ones kukeon ships, or apply one with <code>kuke apply</code>.</p><InitializePrompt onChanged={onCreated} subject="the Blueprints and Configs of the kuke and default teams" /></div>}
      {scope.length >= 2 && detail.loading && !detail.data && <p role="status">Loading the Blueprint's form…</p>}
      {detail.error && <ErrorNotice error={detail.error} kind="Blueprint" />}
      {detail.data && <p className="description">Form of <a className="resource-name" href={catalogLink("blueprints", scope)}>{detail.data.metadata.name}</a>{form && <>: {form.parameters.length} parameters, {form.components.length} components, {form.repoSlots.length} repository slots.</>}</p>}
      {inventory.data?.failures.length ? <p className="stale">Inventory is incomplete. Target scopes may be missing.</p> : null}
    </div></section>
    {detail.data && form && <ConfigForm key={selected} blueprint={detail.data} blueprintScope={scope} form={form} revision={revision} inventory={inventory} onCreated={onCreated} />}
  </div>;
}
