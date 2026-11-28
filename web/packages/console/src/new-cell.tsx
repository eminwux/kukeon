import { useEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import type { Doc, InventoryResource } from "./inventory";
import { useResource } from "./request";
import { cellAdvanced, containerFields } from "./creation-schema.mjs";
import type { Field } from "./creation-schema.mjs";
import { SourceGroups, SourceIdentities, type RenderedValue } from "./merged-values";
import { groupGitValues } from "./config-form.mjs";
import { Disclosure } from "@kukeon/ui";
import { catalogAPI, referenceScope } from "./catalog-url.mjs";
import { ConnectorFixes, ConnectorStateBadge, IdentitySelect, LoginIdentitySelect, StatusIdentity } from "./connector-ui";
import { blocksCells, changedMethods, changedPicks, connectorStatuses, connectorTitle, harnessEphemeral, harnessPersistent, harnessStates, identitiesFor, keepsLogin, loginIdentitiesFor, methodChoices, methodStatuses, stateResolved, statusIdentity, takesIdentity, teamOf, unresolvedConnectors } from "./connectors.mjs";
import type { Connector, ConnectorStatus, SecretRef } from "./connectors.mjs";
import { repositoryFromSource, sourcesAPI } from "./teams.mjs";
import type { StoredSource } from "./teams.mjs";
import { apiFetch } from "@kukeon/api-client";
import { routeHref } from "./router";

type ScopeMetadata = { name: string; realm?: string; space?: string; stack?: string; labels?: Record<string, string> };
type CatalogDoc = { metadata: ScopeMetadata; spec?: Record<string, unknown>; kind?: string; status?: unknown };
type CellDoc = Doc & { spec: { realmId: string; spaceId: string; stackId: string; containers?: Container[]; provenance?: unknown } };
type Container = { id: string; image?: string; attachable?: boolean; root?: boolean; [key: string]: unknown };
type Preview = { document: CellDoc; preview: string; digest?: string; values?: RenderedValue[]; connectors?: ConnectorStatus[] };
type FormValue = Record<string, unknown>;

function scopedName(metadata: ScopeMetadata): string[] {
  return [metadata.realm, metadata.space, metadata.stack, metadata.name].filter((value): value is string => !!value);
}
function catalogLink(kind: string, scope: string[], document = false): string { return routeHref(`/${kind}/` + scope.map(encodeURIComponent).join("/") + (document ? "?document=1" : "")); }
function scopeLabel(scope: string[]): string { return scope.join(" / "); }
function blueprintAPI(reference: FormValue): string | null {
  if (typeof reference.name !== "string" || typeof reference.realm !== "string") return null;
  return catalogAPI("blueprints", referenceScope(reference));
}
function setAt(value: FormValue, key: string, next: unknown): FormValue { return { ...value, [key]: next }; }
function emptyObject(fields: Field[]): FormValue {
  return Object.fromEntries(fields.filter(field => field.kind !== "objects").map(field => [field.key, field.kind === "boolean" ? false : field.kind === "strings" || field.kind === "env" ? [] : field.kind === "map" ? {} : field.kind === "number" ? undefined : field.kind === "object" ? undefined : ""]));
}
function fieldErrors(value: FormValue, fields: Field[], prefix: string): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const field of fields) {
    const path = `${prefix}.${field.key}`;
    const item = value[field.key];
    if (field.required && (item === undefined || item === null || String(item).trim() === "")) errors[path] = "This field is required.";
    if (field.kind === "object" && field.fields && item && typeof item === "object") Object.assign(errors, fieldErrors(item as FormValue, field.fields, path));
    if (field.kind === "objects" && field.fields && Array.isArray(item)) item.forEach((entry, index) => Object.assign(errors, fieldErrors(entry as FormValue, field.fields!, `${path}.${index}`)));
  }
  return errors;
}

// The preview leads with a short summary (#1568): what will be created and where. The complete document
// stays one click away in a collapsed disclosure, wrapped so it never widens the page.
function PreviewSummary({ document, connectors }: { document: CellDoc; connectors?: ConnectorStatus[] }) {
  const containers = document.spec.containers ?? [];
  const images = [...new Set(containers.map(container => container.image).filter((image): image is string => !!image))];
  return <dl className="details-grid creation-summary">
    <dt>Name</dt><dd>{document.metadata.name}</dd>
    <dt>Scope</dt><dd className="mono-id">{scopeLabel([document.spec.realmId, document.spec.spaceId, document.spec.stackId])}</dd>
    <dt>Containers</dt><dd>{containers.length ? `${containers.length} (${containers.map(container => container.id).join(", ")})` : "None declared"}</dd>
    <dt>Images</dt><dd>{images.length ? images.map(image => <code className="creation-summary-image" key={image}>{image}</code>) : "None declared"}</dd>
    <dt>Connectors</dt><dd>{connectors?.length ? connectors.map(status => `${connectorTitle(status)}${status.method ? ` (${status.method})` : ""}`).join(", ") : "None"}</dd>
  </dl>;
}
function PreviewDocument({ document, label }: { document: CellDoc; label: string }) {
  return <details className="creation-json"><summary>Full preview (JSON)</summary><pre tabIndex={0} aria-label={label}>{JSON.stringify(document, null, 2)}</pre></details>;
}
// A fresh preview scrolls its review into view, so the summary shows without hunting below the form.
function useReviewScroll(preview: Preview | undefined) {
  const review = useRef<HTMLElement>(null);
  useEffect(() => { if (preview) review.current?.scrollIntoView({ block: "nearest" }); }, [preview]);
  return review;
}

// What happens after Create (#1603): the cell is started and a terminal attached unless the operator opts out.
export type CreatedOptions = { start: boolean; attach: boolean };

// PreviewPanel holds only preview output, set apart from the form (#1603): its own heading, a "Not created
// yet" badge and an accent border, and a control to hide it. Hiding it makes Create render afresh.
const PreviewPanel = ({ review, onHide, children }: { review: RefObject<HTMLElement | null>; onHide: () => void; children: ReactNode }) =>
  <section className="panel preview-panel" aria-label="Preview" ref={review}>
    <div className="panel-heading"><h2>Preview</h2><span className="badge">Not created yet</span><button type="button" className="subtle-button" onClick={onHide}>Hide preview</button></div>
    <div className="panel-body creation-review">{children}</div>
  </section>;

// CreationActions is the one action row at the end of a New Cell form (#1603): what happens after creation,
// then Preview (secondary) and Create (the primary action). Create needs no preview.
function CreationActions({ start, attach, onStart, onAttach, pending, hasPreview, previewLabel, canPreview, canCreate, onPreview, onCreate }: {
  start: boolean; attach: boolean; onStart: (value: boolean) => void; onAttach: (value: boolean) => void; pending: boolean;
  hasPreview: boolean; previewLabel: string; canPreview: boolean; canCreate: boolean; onPreview: () => void; onCreate: () => void;
}) {
  return <div className="form-actions">
    <label className="schema-check"><input type="checkbox" checked={start} disabled={pending} onChange={event => onStart(event.target.checked)} /> Start the cell</label>
    <label className="schema-check"><input type="checkbox" checked={start && attach} disabled={pending || !start} onChange={event => onAttach(event.target.checked)} /> Attach a terminal</label>
    <button type="button" disabled={!canPreview} onClick={onPreview}>{hasPreview ? "Refresh preview" : previewLabel}</button>
    <button type="button" className="primary-action" disabled={!canCreate} onClick={onCreate}>Create Cell</button>
  </div>;
}

// TargetSelect picks the space or stack a cell lands in. A coordinate the Config pins is shown, disabled.
function TargetSelect({ label, value, options, pinned, disabled, onChange }: { label: string; value: string; options: string[]; pinned: boolean; disabled: boolean; onChange: (value: string) => void }) {
  const choices = options.includes(value) ? options : [value, ...options];
  return <label className="schema-field">{label}<select value={value} disabled={disabled || pinned} onChange={event => onChange(event.target.value)}>{choices.map(option => <option key={option} value={option}>{option}</option>)}</select>{pinned && <small>Set by the Config's scope.</small>}</label>;
}
// initialTarget is where a target select starts (#1603): the Config's own coordinate, else "default" when it
// is listed, else the first listed one.
function initialTarget(own: string | undefined, options: string[]): string {
  if (own) return own;
  return options.includes("default") || !options.length ? "default" : options[0];
}

// RepositorySourcePicker fills a repository entry from a stored Source (#1644, AC7): its name, address and
// ref. It copies: the cell does not follow the Source afterwards. The Source's git identity is shown by
// name; the cell's identity comes from its own git settings.
function RepositorySourcePicker({ label, disabled, onPick }: { label: string; disabled: boolean; onPick: (patch: FormValue) => void }) {
  const sources = useResource<{ sources: StoredSource[] }>(sourcesAPI(), 0);
  const [picked, setPicked] = useState("");
  const source = sources.data?.sources.find(item => item.name === picked);
  return <div className="schema-field repository-source"><label><span>From source</span>
    <select aria-label={`From source for ${label}`} value={picked} disabled={disabled || !sources.data?.sources.length} onChange={event => {
      setPicked(event.target.value);
      const chosen = sources.data?.sources.find(item => item.name === event.target.value);
      if (chosen) onPick(repositoryFromSource(chosen));
    }}><option value="">{sources.data?.sources.length ? "Choose a stored Source…" : "No Source is stored"}</option>{sources.data?.sources.map(item => <option key={item.name} value={item.name}>{item.name} — {item.url}</option>)}</select></label>
    {source && <small>Identity: {source.identity || "Default"}. The fields are copied; the cell does not follow the Source afterwards.</small>}
  </div>;
}

function FieldEditor({ field, value, onChange, path, errors, disabled = false }: { field: Field; value: unknown; onChange: (value: unknown) => void; path: string; errors: Record<string, string>; disabled?: boolean }) {
  const error = errors[path];
  const id = `field-${path.replace(/[^a-zA-Z0-9_-]/g, "-")}`;
  const helpId = field.help ? `${id}-help` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [helpId, errorId].filter(Boolean).join(" ") || undefined;
  if (field.kind === "boolean") return <label className="schema-check"><input id={id} type="checkbox" checked={value === true} disabled={disabled} onChange={event => onChange(event.target.checked)} aria-describedby={describedBy} />{field.label}{field.help && <small id={helpId}>{field.help}</small>}{error && <small className="field-error" id={errorId}>{error}</small>}</label>;
  if (field.kind === "object") {
    const object = value && typeof value === "object" ? value as FormValue : undefined;
    return <fieldset className="schema-object"><legend>{field.label}</legend>{field.help && <p className="description">{field.help}</p>}{!object ? <button type="button" disabled={disabled} onClick={() => onChange(emptyObject(field.fields ?? []))}>Add {field.label.toLowerCase()}</button> : <><div className="schema-fields">{(field.fields ?? []).map(child => <FieldEditor key={`${path}.${child.key}`} field={child} value={object[child.key]} path={`${path}.${child.key}`} errors={errors} disabled={disabled} onChange={next => onChange(setAt(object, child.key, next))} />)}</div><button type="button" className="subtle-button" disabled={disabled} onClick={() => onChange(undefined)}>Remove {field.label.toLowerCase()}</button></>}</fieldset>;
  }
  if (field.kind === "objects") {
    const items = Array.isArray(value) ? value as FormValue[] : [];
    return <fieldset className="schema-array"><legend>{field.label}</legend>{field.help && <p className="description">{field.help}</p>}{items.map((item, index) => <section className="schema-array-item" key={`${path}.${index}`}><div className="schema-array-heading"><h4>{field.itemLabel ?? field.label.slice(0, -1)} {index + 1}</h4><button type="button" className="subtle-button" disabled={disabled} onClick={() => onChange(items.filter((_, current) => current !== index))}>Remove</button></div>{field.sourcePicker && <RepositorySourcePicker label={`${field.itemLabel ?? "Item"} ${index + 1}`} disabled={disabled} onPick={patch => onChange(items.map((current, n) => n === index ? { ...current, ...patch } : current))} />}<div className="schema-fields">{(field.fields ?? []).map(child => <FieldEditor key={`${path}.${index}.${child.key}`} field={child} value={item[child.key]} path={`${path}.${index}.${child.key}`} errors={errors} disabled={disabled} onChange={next => onChange(items.map((current, n) => n === index ? setAt(current, child.key, next) : current))} />)}</div></section>)}<button type="button" disabled={disabled} onClick={() => onChange([...items, emptyObject(field.fields ?? [])])}>Add {field.itemLabel?.toLowerCase() ?? "item"}</button></fieldset>;
  }
  if (field.kind === "map") {
    const entries = Object.entries((value && typeof value === "object" ? value : {}) as Record<string, string>);
    const changeEntry = (index: number, key: string, next: string) => {
      const result = { ...Object.fromEntries(entries) };
      const oldKey = entries[index]?.[0];
      delete result[oldKey];
      if (key.trim()) result[key] = next;
      onChange(result);
    };
    return <fieldset className="schema-array"><legend>{field.label}</legend>{entries.map(([key, item], index) => <div className="map-entry" key={`${path}.${index}`}><label>Key<input value={key} disabled={disabled} onChange={event => changeEntry(index, event.target.value, item)} /></label><label>Value<input value={item} disabled={disabled} onChange={event => changeEntry(index, key, event.target.value)} /></label><button type="button" className="subtle-button" disabled={disabled} onClick={() => onChange(Object.fromEntries(entries.filter((_, n) => n !== index)))}>Remove</button></div>)}<button type="button" disabled={disabled} onClick={() => onChange({ ...Object.fromEntries(entries), [`key-${entries.length + 1}`]: "" })}>Add key/value</button></fieldset>;
  }
  if (field.kind === "strings" || field.kind === "env") {
    const items = Array.isArray(value) ? value as string[] : [];
    const update = (index: number, next: string) => onChange(items.map((entry, n) => n === index ? next : entry));
    if (field.kind === "env") return <fieldset className="schema-array"><legend>{field.label}</legend>{field.help && <p className="description">{field.help}</p>}{items.map((entry, index) => { const [key, ...parts] = entry.split("="); const itemValue = parts.join("="); return <div className="map-entry" key={`${path}.${index}`}><label>Variable<input value={key} disabled={disabled} onChange={event => update(index, `${event.target.value}=${itemValue}`)} /></label><label>Value<input value={itemValue} disabled={disabled} onChange={event => update(index, `${key}=${event.target.value}`)} /></label><button type="button" className="subtle-button" disabled={disabled} onClick={() => onChange(items.filter((_, n) => n !== index))}>Remove</button></div>; })}<button type="button" disabled={disabled} onClick={() => onChange([...items, "="])}>Add environment variable</button></fieldset>;
    return <fieldset className="schema-array"><legend>{field.label}</legend>{field.help && <p className="description">{field.help}</p>}{items.map((item, index) => <div className="string-entry" key={`${path}.${index}`}><input aria-label={`${field.label} ${index + 1}`} value={item} disabled={disabled} onChange={event => update(index, event.target.value)} /><button type="button" className="subtle-button" disabled={disabled} onClick={() => onChange(items.filter((_, n) => n !== index))}>Remove</button></div>)}<button type="button" disabled={disabled} onClick={() => onChange([...items, ""])}>Add {field.label.toLowerCase().replace(/s$/, "")}</button></fieldset>;
  }
  const input = field.kind === "enum" ? <select id={id} value={typeof value === "string" ? value : ""} disabled={disabled} onChange={event => onChange(event.target.value)} aria-invalid={!!error} aria-describedby={describedBy}><option value="">Select…</option>{(field.options ?? []).filter(option => option !== "").map(option => <option key={option}>{option}</option>)}</select>
    : <input id={id} type={field.kind === "number" ? "number" : "text"} min={field.kind === "number" ? 0 : undefined} value={value === undefined || value === null ? "" : String(value)} disabled={disabled} aria-invalid={!!error} aria-describedby={describedBy} onChange={event => onChange(field.kind === "number" ? event.target.value === "" ? undefined : Number(event.target.value) : event.target.value)} />;
  return <label className="schema-field" htmlFor={id}><span>{field.label}{field.required && <b aria-label="required"> *</b>}</span>{input}{field.help && <small id={helpId}>{field.help}</small>}{error && <small className="field-error" id={errorId}>{error}</small>}</label>;
}

function ConfigCreation({ revision, inventory, configScope, onCreated }: { revision: number; inventory: InventoryResource; configScope?: string[]; onCreated: (cell: CellDoc, options: CreatedOptions) => void }) {
  const configs = useResource<CatalogDoc[]>("/catalog/configs", revision);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(configScope ? JSON.stringify(configScope) : "");
  // Opened from a Config, the form leads with a one-line summary of it; "Change" reveals the chooser (#1603).
  const [choosing, setChoosing] = useState(!configScope);
  const configRows = (configs.data ?? []).map(doc => ({ doc, scope: scopedName(doc.metadata) }));
  const filtered = configRows.filter(({ scope }) => scopeLabel(scope).toLowerCase().includes(query.toLowerCase()));
  const config = configRows.find(row => JSON.stringify(row.scope) === selected);
  const showChooser = choosing || (!configs.loading && !config);
  // A saved default or Config pick re-reads the Config, so its connector status follows at once.
  const [saved, setSaved] = useState(0);
  const detail = useResource<CatalogDoc>(config ? catalogAPI("configs", config.scope) : null, revision + saved);
  // Each connector the Config requires starts on the identity it resolves to; a pick is for this cell only.
  const configStatuses = connectorStatuses(detail.data);
  const connectors = useResource<Connector[]>(configStatuses.length ? "/connectors" : null, revision);
  // A connector with several methods may use another one for this cell only (#1551); its identity then
  // fills that method's slot, so the statuses below are the Config's with the method picks applied.
  const [methodPicks, setMethodPicks] = useState<Record<string, string | undefined>>({});
  const methods = changedMethods(configStatuses, methodPicks);
  const statuses = methodStatuses(configStatuses, connectors.data, methodPicks);
  const secrets = useResource<unknown[]>(statuses.some(status => takesIdentity(status.delivery)) ? "/catalog/secrets" : null, revision + saved);
  const logins = useResource<unknown[]>(statuses.some(keepsLogin) ? "/connectors/logins" : null, revision + saved);
  const [picks, setPicks] = useState<Record<string, Partial<SecretRef> | undefined>>({});
  // Harness state for this cell only (#1685): persistent or ephemeral, empty for the Config's. A cell
  // whose method keeps a login always has persistent state, so the choice is offered for token methods.
  const [stateChoice, setStateChoice] = useState("");
  const configState = configStatuses.find(status => !!status.harnessState)?.harnessState ?? "";
  const keepsLoginNow = statuses.some(keepsLogin);
  const offersState = !!configState && !keepsLoginNow;
  const state = offersState && stateChoice !== configState ? stateChoice : "";
  const overrides = changedPicks(statuses, picks);
  const unresolved = unresolvedConnectors(statuses, picks);
  const team = teamOf(detail.data);
  const blueprintRef = detail.data?.spec?.blueprint as FormValue | undefined;
  const blueprintValid = !!blueprintRef && typeof blueprintRef.name === "string" && !!blueprintRef.name && typeof blueprintRef.realm === "string" && !!blueprintRef.realm && !(blueprintRef.stack && !blueprintRef.space);
  const blueprintPath = blueprintValid && blueprintRef ? blueprintAPI(blueprintRef) : null;
  const blueprint = useResource<CatalogDoc>(blueprintPath, revision);
  const blueprintScope = referenceScope(blueprintRef);
  const [name, setName] = useState("");
  const [env, setEnv] = useState("");
  const [ignoreDiskPressure, setIgnoreDiskPressure] = useState(false);
  // Create starts the cell and attaches a terminal by default (#1603); input changes leave both alone.
  const [start, setStart] = useState(true);
  const [attach, setAttach] = useState(true);
  const [preview, setPreview] = useState<Preview>();
  const [pending, setPending] = useState(false);
  const [stage, setStage] = useState<"preview" | "create">("preview");
  const [error, setError] = useState("");
  const [connectorsOpen, setConnectorsOpen] = useState(false);
  const [envOpen, setEnvOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [gitOpen, setGitOpen] = useState(false);
  // A required connector without an identity opens its section by itself, so the fix is in view.
  const needsConnector = unresolved.length > 0;
  useEffect(() => { if (needsConnector) setConnectorsOpen(true); }, [needsConnector]);
  // The target (#1603): the Config's realm, and a space and stack at or below its scope. A coordinate the
  // Config sets is pinned; an open one starts on "default" when listed.
  const ownRealm = config?.doc.metadata.realm || "default";
  const ownSpace = config?.doc.metadata.space || "";
  const ownStack = config?.doc.metadata.stack || "";
  const [pickedSpace, setPickedSpace] = useState<string>();
  const [pickedStack, setPickedStack] = useState<string>();
  const spaceOptions = (inventory.data?.rows[1] ?? []).filter(row => row.scope[0] === ownRealm).map(row => row.scope[1]);
  const targetSpace = ownSpace || pickedSpace || initialTarget("", spaceOptions);
  const stackOptions = (inventory.data?.rows[2] ?? []).filter(row => row.scope[0] === ownRealm && row.scope[1] === targetSpace).map(row => row.scope[2]);
  const targetStack = ownStack || pickedStack || initialTarget("", stackOptions);
  const stackMissing = !!config && !!inventory.data && !inventory.data.rows[2].some(row => row.scope[0] === ownRealm && row.scope[1] === targetSpace && row.scope[2] === targetStack);
  const review = useReviewScroll(preview);
  function changed() { setPreview(undefined); }
  async function submit(create: boolean) {
    if (!config || pending) return;
    setPending(true); setStage(create ? "create" : "preview"); setError("");
    try {
      // Only the picks that differ from the Config's own identity are sent, as per-cell overrides. A create
      // after a preview sends its token and name, so what was reviewed is what gets created.
      const body = { config: { name: config.doc.metadata.name, realm: config.doc.metadata.realm, space: config.doc.metadata.space, stack: config.doc.metadata.stack }, name: name.trim(), ...(create && preview ? { resolvedName: preview.document.metadata.name, preview: preview.preview } : {}), env: env.split("\n").filter(Boolean), ignoreDiskPressure, targetSpace, targetStack, ...(Object.keys(overrides).length ? { connectors: overrides } : {}), ...(Object.keys(methods).length ? { methods } : {}), ...(state ? { state } : {}) };
      const response = await apiFetch(`/cells/from-config/${create ? "create" : "preview"}`, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(body) });
      const result = await response.json();
      if (!response.ok) {
        const message = result.error?.message ?? result.message ?? `Request failed (HTTP ${response.status})`;
        // A connector the daemon could not resolve: re-read the Config so the flag and its fixes show.
        if (result.error?.code === "connector_unresolved" || result.error?.code === "connector_invalid") setSaved(value => value + 1);
        throw new Error(create && (result.error?.code === "unavailable" || response.status >= 500) ? `The creation outcome could not be confirmed. Refresh the Cell inventory before retrying. ${message}` : message);
      }
      if (create) onCreated(result as CellDoc, { start, attach: start && attach });
      else setPreview(result as Preview);
    } catch (failure) {
      const message = failure instanceof Error ? failure.message : "The request could not reach the web server.";
      setError(create && failure instanceof TypeError ? `The creation outcome could not be confirmed. Refresh the Cell inventory before retrying. ${message}` : message);
    }
    finally { setPending(false); }
  }
  const previewBlocked = (preview?.connectors ?? []).some(blocksCells);
  const ready = !!detail.data && !!blueprint.data && !stackMissing && unresolved.length === 0;
  const canCreate = !pending && ready && !previewBlocked;
  const pickedCount = Object.keys(overrides).length + Object.keys(methods).length + (state ? 1 : 0);
  const envCount = env.split("\n").filter(line => line.trim()).length;
  const blueprintStatus = !blueprintRef ? <p className="error" role="alert">This config does not contain a Blueprint reference.</p> : !blueprintValid ? <p className="error" role="alert">The Blueprint reference is incomplete: {scopeLabel(blueprintScope)}</p> : blueprint.loading ? <p role="status">Checking referenced Blueprint…</p> : blueprint.error ? <div className="error" role="alert"><strong>{blueprint.error.code === "not_found" ? "Blueprint reference is missing" : "Blueprint could not be loaded"}</strong><p>{blueprint.error.message}</p><p>Reference: {scopeLabel(blueprintScope)}</p></div> : null;
  const connectorPanel = config && detail.data && statuses.length > 0 && <Disclosure className="cell-connectors" label="Connectors" title="Connectors" open={connectorsOpen} onToggle={setConnectorsOpen}
    summary={unresolved.length ? undefined : `${statuses.length} resolved${pickedCount ? `, ${pickedCount} picked for this cell` : ""}`}
    badge={unresolved.length ? <span className="badge needs-connector">Needs connector: {[...new Set(unresolved.map(connectorTitle))].join(", ")}</span> : undefined}><div className="panel-body">
    <p className="description">A method or identity picked here applies to this cell only.</p>
    {team && <p className="connector-team" role="note">This Config's identities are set by team <code>{team}</code>. A pick here still applies to this cell only.</p>}
    {secrets.error && <p className="stale">Identities could not be listed: {secrets.error.message}. Name a secret by hand instead.</p>}
    <ul className="state-list connector-status-list">{statuses.map(status => {
      const title = connectorTitle(status);
      const picked = overrides[status.connector];
      const methodPicked = methods[status.connector];
      const configStatus = configStatuses.find(item => item.connector === status.connector) ?? status;
      const choices = configStatus.method ? methodChoices(connectors.data?.find(item => item.name === status.connector)) : [];
      const flagged = unresolved.includes(status);
      return <li key={`${status.connector}:${status.component}`} aria-label={`${title} connector`}><div className="connector-status">
        <div><strong>{title}</strong> <span className="badge">{status.connector}</span>{status.method && <> <span className="badge">{status.method}</span></>} {picked || methodPicked ? <span className="badge">Picked for this cell</span> : <ConnectorStateBadge status={status} />}</div>
        {choices.length > 0 && <label className="schema-field">Method<select aria-label={`${title} method`} value={methodPicked ?? configStatus.method} disabled={pending} onChange={event => { const next = event.target.value; setMethodPicks(old => ({ ...old, [status.connector]: next === configStatus.method ? undefined : next })); setPicks(old => ({ ...old, [status.connector]: undefined })); changed(); }}>{choices.map(method => <option key={method.name} value={method.name}>{method.name}{method.name === configStatus.method ? " (the Config's)" : ""}</option>)}</select><small>{methodPicked ? `${choices.find(method => method.name === methodPicked)?.description ?? ""} Used for this cell only; the Config uses ${configStatus.method}.` : choices.find(method => method.name === configStatus.method)?.description}</small></label>}
        {takesIdentity(status.delivery) ? <IdentitySelect label={`${title} identity`} identities={identitiesFor(secrets.data, status.connector)} value={picks[status.connector] ?? status.identity} extra={[status.identity]} emptyLabel={methodPicked ? "Use the default identity for this method" : status.identity ? "Use the Config's identity" : status.required ? "No identity — required" : "No identity"} disabled={pending} required={status.required} realm={config.doc.metadata.realm} onChange={ref => { setPicks(old => ({ ...old, [status.connector]: ref })); changed(); }} help={methodPicked ? <>The {methodPicked} method resolves its identity from the Realm, Space or Stack defaults unless one is picked here; the preview shows which.</> : <>The Config resolves to: <StatusIdentity status={status} team={team} /></>} /> : keepsLogin(status) ? <LoginIdentitySelect label={`${title} login identity`} identities={loginIdentitiesFor(logins.data, status.connector)} value={picks[status.connector] ?? statusIdentity(status)} extra={[statusIdentity(status)]} emptyLabel="Use the Config's login identity" disabled={pending} onChange={ref => { setPicks(old => ({ ...old, [status.connector]: ref })); changed(); }} help={<>The Config resolves to: <StatusIdentity status={status} team={team} />. A login identity picked here becomes this cell's state volume; one login identity serves one role.</>} /> : <p className="description"><StatusIdentity status={status} team={team} /></p>}
        {flagged && <p className="field-error" role="alert">{title} has no usable identity{status.message ? `: ${status.message}` : ""}. Pick one for this cell above, set a default, or pick one for the Config.</p>}
        {!methodPicked && status.state !== stateResolved && <ConnectorFixes status={status} connector={connectors.data?.find(item => item.name === status.connector)} config={detail.data!.metadata} identities={identitiesFor(secrets.data, status.connector)} logins={loginIdentitiesFor(logins.data, status.connector)} secrets={secrets.data} onChanged={() => { setSaved(value => value + 1); changed(); }} />}
      </div></li>;
    })}</ul>
    {offersState && <label className="schema-field">Harness state<select aria-label="Harness state" value={stateChoice || configState} disabled={pending} onChange={event => { setStateChoice(event.target.value); changed(); }}>{harnessStates.map(item => <option key={item} value={item}>{item}{item === configState ? " (the Config's)" : ""}</option>)}</select><small>{(stateChoice || configState) === harnessEphemeral ? "Ephemeral: this cell mounts no state volume; the harness state directory lives and dies with the cell, and the cell authenticates from its Secret." : "Persistent: this cell mounts the Config's state volume, so sessions and settings survive the cell."}{state ? ` Used for this cell only; the Config uses ${configState || harnessPersistent}.` : ""}</small></label>}
    {!!configState && keepsLoginNow && <p className="description">Harness state is persistent: a method that keeps its login in the state volume cannot use ephemeral state.</p>}
  </div></Disclosure>;
  const git = groupGitValues(preview?.values);
  return <div className="creation-layout">
    <section className="panel" aria-label="Config"><div className="panel-heading"><h2>Config</h2>{config && !showChooser && <button type="button" className="subtle-button" onClick={() => setChoosing(true)}>Change</button>}</div><div className="panel-body creation-form">
      {showChooser ? <>
        {configs.loading && <p role="status">Loading CellConfigs…</p>}{configs.error && <div className="error" role="alert"><strong>CellConfigs unavailable</strong><p>{configs.error.message}</p></div>}{configs.error && configs.data && <p className="stale">Showing stale CellConfigs.</p>}
        <label>Search CellConfigs<input type="search" value={query} placeholder="Name or full scope" onChange={event => setQuery(event.target.value)} /></label>
        <label>CellConfig<select value={selected} onChange={event => { setSelected(event.target.value); setPicks({}); setMethodPicks({}); setStateChoice(""); setPickedSpace(undefined); setPickedStack(undefined); setPreview(undefined); setError(""); }}><option value="">Select a CellConfig…</option>{filtered.map(row => <option key={JSON.stringify(row.scope)} value={JSON.stringify(row.scope)}>{row.doc.metadata.name} — {scopeLabel(row.scope.slice(0, -1))}</option>)}</select></label>
        {!configs.loading && !configs.error && !configRows.length && <p className="description">No CellConfigs are available.</p>}
      </> : !config ? <p role="status">Loading CellConfigs…</p> : null}
      {config && <>
        {detail.loading && <p role="status">{detail.data ? "Refreshing CellConfig details…" : "Loading CellConfig details…"}</p>}{detail.error && <div className="error" role="alert"><strong>CellConfig details unavailable</strong><p>{detail.error.message}</p></div>}{detail.error && detail.data && <p className="stale">Showing stale CellConfig details.</p>}
        {detail.data && <p className="config-summary">Config <a className="resource-name" href={catalogLink("configs", config.scope, true)}>{detail.data.metadata.name}</a> <span className="description">{scopeLabel(config.scope.slice(0, -1))}</span>{blueprint.data && blueprintValid && <> · Blueprint <a className="resource-name" href={catalogLink("blueprints", blueprintScope)}>{blueprint.data.metadata.name}</a> <span className="description">{scopeLabel(blueprintScope.slice(0, -1))}</span></>}</p>}
        {detail.data && blueprintStatus}
      </>}
    </div></section>
    {config && <>
      <section className="panel" aria-label="Cell"><div className="panel-heading"><h2>Cell</h2></div><form className="panel-body creation-form" onSubmit={event => { event.preventDefault(); if (ready) void submit(false); }}>
        <label>Cell name (optional)<input value={name} disabled={pending} onChange={event => { setName(event.target.value); changed(); }} /><small>Leave blank to let kukeon pick a name.</small></label>
        <div className="schema-fields">
          <p className="schema-field">Realm<strong>{ownRealm}</strong></p>
          <TargetSelect label="Target space" value={targetSpace} options={spaceOptions} pinned={!!ownSpace} disabled={pending} onChange={value => { setPickedSpace(value); setPickedStack(undefined); changed(); }} />
          <TargetSelect label="Target stack" value={targetStack} options={stackOptions} pinned={!!ownStack} disabled={pending} onChange={value => { setPickedStack(value); changed(); }} />
        </div>
        {stackMissing && <p className="error" role="alert">The target Stack is not present in the loaded inventory.</p>}
        {inventory.data?.failures.length ? <p className="stale">Inventory is incomplete. Target scope may be unavailable.</p> : null}
      </form></section>
      {connectorPanel}
      <Disclosure title="Environment overrides" level={3} summary={envCount ? `${envCount} set` : "None"} open={envOpen} onToggle={setEnvOpen}><div className="panel-body creation-form">
        <label>Environment overrides<textarea value={env} disabled={pending} placeholder="KEY=VALUE, one per line" onChange={event => { setEnv(event.target.value); changed(); }} /><small>Values are redacted in the preview.</small></label>
      </div></Disclosure>
      <Disclosure title="Advanced" level={3} summary={ignoreDiskPressure ? "Ignoring disk pressure" : "Defaults"} open={advancedOpen} onToggle={setAdvancedOpen}><div className="panel-body creation-form">
        <label className="schema-check"><input type="checkbox" checked={ignoreDiskPressure} disabled={pending} onChange={event => { setIgnoreDiskPressure(event.target.checked); changed(); }} /> Ignore disk pressure for this creation</label>
      </div></Disclosure>
      {error && <p className="error" role="alert">{error}</p>}{pending && <p role="status">{stage === "create" ? "Creating Cell…" : "Preparing preview…"}</p>}
      {preview && <PreviewPanel review={review} onHide={() => setPreview(undefined)}>
        <PreviewSummary document={preview.document} connectors={preview.connectors} />
        <SourceGroups values={preview.values} connectors={preview.connectors} team={team} sources={{
          blueprint: blueprint.data && blueprintValid ? <a className="resource-name" href={catalogLink("blueprints", blueprintScope)}>{blueprint.data.metadata.name}</a> : undefined,
          config: <a className="resource-name" href={catalogLink("configs", config.scope, true)}>{config.doc.metadata.name}</a>,
          realm: preview.document.spec.realmId, space: preview.document.spec.spaceId, stack: preview.document.spec.stackId,
        }} />
        {(git.repos.length > 0 || git.defaults.length > 0) && <Disclosure title="Git identity per repository" level={3} open={gitOpen} onToggle={setGitOpen}><div className="panel-body"><SourceIdentities values={preview.values ?? []} /></div></Disclosure>}
        <PreviewDocument document={preview.document} label="Complete Cell preview" />
        {preview.digest && <p className="description">Render digest: <code>{preview.digest}</code></p>}
      </PreviewPanel>}
      {unresolved.length > 0 && <p className="field-error" role="alert">Create is disabled until every required connector resolves: {[...new Set(unresolved.map(connectorTitle))].join(", ")}.</p>}
      <CreationActions start={start} attach={attach} onStart={setStart} onAttach={setAttach} pending={pending} hasPreview={!!preview} previewLabel="Preview Cell" canPreview={!pending && ready} canCreate={canCreate} onPreview={() => void submit(false)} onCreate={() => void submit(true)} />
    </>}
  </div>;
}

function ManualCreation({ inventory, onCreated }: { inventory: InventoryResource; onCreated: (cell: CellDoc, options: CreatedOptions) => void }) {
  const realms = inventory.data?.rows[0] ?? [];
  const [model, setModel] = useState<FormValue>(() => ({ name: "", realm: "", space: "", stack: "", labels: {}, annotations: {}, spec: { containers: [], autoDelete: false, nestedCgroupRuntime: false, ignoreDiskPressure: false } }));
  const [advanced, setAdvanced] = useState(false);
  const [containerAdvanced, setContainerAdvanced] = useState<Record<number, boolean>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<Preview>();
  const [pending, setPending] = useState(false);
  const [stage, setStage] = useState<"preview" | "create">("preview");
  const [start, setStart] = useState(true);
  const [attach, setAttach] = useState(true);
  const [error, setError] = useState("");
  const realm = String(model.realm ?? "");
  const spaces = (inventory.data?.rows[1] ?? []).filter(row => row.scope[0] === realm);
  const space = String(model.space ?? "");
  const stacks = (inventory.data?.rows[2] ?? []).filter(row => row.scope[0] === realm && row.scope[1] === space);
  const spec = model.spec as FormValue;
  const containers = (spec.containers as FormValue[] | undefined) ?? [];
  useEffect(() => {
    if (!inventory.data || model.realm) return;
    const defaultRealm = realms.find(row => row.doc.metadata.name === "default") ?? realms[0];
    if (!defaultRealm) return;
    const defaultSpace = inventory.data.rows[1].find(row => row.scope[0] === defaultRealm.scope[0] && row.doc.metadata.name === "default")
      ?? inventory.data.rows[1].find(row => row.scope[0] === defaultRealm.scope[0]);
    const defaultStack = defaultSpace && (inventory.data.rows[2].find(row => row.scope[0] === defaultSpace.scope[0] && row.scope[1] === defaultSpace.scope[1] && row.doc.metadata.name === "default")
      ?? inventory.data.rows[2].find(row => row.scope[0] === defaultSpace.scope[0] && row.scope[1] === defaultSpace.scope[1]));
    setModel(old => old.realm ? old : { ...old, realm: defaultRealm.scope[0], space: defaultSpace?.scope[1] ?? "", stack: defaultStack?.scope[2] ?? "" });
  }, [inventory.data, model.realm, realms]);
  const localErrors = useMemo(() => ({ ...fieldErrors({ name: model.name }, [{ key: "name", label: "Cell name", kind: "text", required: true }], "cell"), ...fieldErrors({ realm, space, stack: model.stack }, ["realm", "space", "stack"].map(key => ({ key, label: key, kind: "text" as const, required: true })), "cell"), ...fieldErrors({ containers }, [{ key: "containers", label: "Containers", kind: "objects", fields: containerFields.filter(field => field.required) }], "cell.spec") }), [model, realm, space, spec, containers]);
  function changeRoot(key: string, value: unknown) { setModel(old => setAt(old, key, value)); setPreview(undefined); setErrors({}); }
  function changeSpec(key: string, value: unknown) { setModel(old => setAt(old, "spec", setAt(old.spec as FormValue, key, value))); setPreview(undefined); setErrors({}); }
  function changeContainer(index: number, key: string, value: unknown) { changeSpec("containers", containers.map((container, i) => i === index ? setAt(container, key, value) : container)); }
  async function submit(create: boolean) {
    if (pending) return;
    // A create without a preview runs the same local validation a preview does (#1603).
    if ((!create || !preview) && Object.keys(localErrors).length) { setErrors(localErrors); return; }
    setPending(true); setStage(create ? "create" : "preview"); setError("");
    try {
      const payload = { name: String(model.name).trim(), realm, space, stack: String(model.stack ?? ""), labels: model.labels, annotations: model.annotations, spec };
      const response = await apiFetch(`/cells/manual/${create ? "create" : "preview"}`, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify({ ...payload, ...(create && preview ? { preview: preview.preview } : {}) }) });
      const result = await response.json();
      if (!response.ok) {
        const message = result.error?.message ?? result.message ?? `Request failed (HTTP ${response.status})`;
        throw new Error(create && (result.error?.code === "unavailable" || response.status >= 500) ? `The creation outcome could not be confirmed. Refresh the Cell inventory before retrying. ${message}` : message);
      }
      if (create) onCreated(result as CellDoc, { start, attach: start && attach });
      else setPreview(result as Preview);
    } catch (failure) {
      const message = failure instanceof Error ? failure.message : "The request could not reach the web server.";
      setError(create && failure instanceof TypeError ? `The creation outcome could not be confirmed. Refresh the Cell inventory before retrying. ${message}` : message);
    }
    finally { setPending(false); }
  }
  const review = useReviewScroll(preview);
  return <div className="creation-layout">
    <section className="panel"><div className="panel-heading"><h2>Build a Cell specification</h2><span className="badge">No Config or Blueprint</span></div><form className="panel-body creation-form" onSubmit={event => { event.preventDefault(); void submit(false); }}>
      <div className="schema-section"><h3>Basic Cell configuration</h3><div className="schema-fields">
        <label className="schema-field" htmlFor="manual-name"><span>Cell name <b aria-label="required"> *</b></span><input id="manual-name" value={String(model.name)} disabled={pending} aria-invalid={!!errors["cell.name"]} aria-describedby={errors["cell.name"] ? "manual-name-error" : undefined} onChange={event => changeRoot("name", event.target.value)} />{errors["cell.name"] && <small id="manual-name-error" className="field-error">{errors["cell.name"]}</small>}</label>
        <label className="schema-field"><span>Realm <b aria-label="required"> *</b></span><select value={realm} disabled={pending} onChange={event => { changeRoot("realm", event.target.value); changeRoot("space", ""); changeRoot("stack", ""); }}><option value="">Select a Realm…</option>{realms.map(row => <option key={row.scope[0]}>{row.scope[0]}</option>)}</select>{errors["cell.realm"] && <small className="field-error">{errors["cell.realm"]}</small>}</label>
        <label className="schema-field"><span>Space <b aria-label="required"> *</b></span><select value={space} disabled={pending || !realm} onChange={event => { changeRoot("space", event.target.value); changeRoot("stack", ""); }}><option value="">Select a Space…</option>{spaces.map(row => <option key={row.scope[1]}>{row.scope[1]}</option>)}</select>{errors["cell.space"] && <small className="field-error">{errors["cell.space"]}</small>}</label>
        <label className="schema-field"><span>Stack <b aria-label="required"> *</b></span><select value={String(model.stack ?? "")} disabled={pending || !space} onChange={event => changeRoot("stack", event.target.value)}><option value="">Select a Stack…</option>{stacks.map(row => <option key={row.scope[2]}>{row.scope[2]}</option>)}</select>{errors["cell.stack"] && <small className="field-error">{errors["cell.stack"]}</small>}</label>
      </div></div>
      <div className="schema-section"><h3>Metadata</h3><div className="schema-fields">{(["labels", "annotations"] as const).map(key => <FieldEditor key={key} field={{ key, label: key === "labels" ? "Labels" : "Annotations", kind: "map" }} value={model[key]} path={`cell.${key}`} errors={errors} disabled={pending} onChange={value => changeRoot(key, value)} />)}</div></div>
      <div className="schema-section"><div className="schema-section-heading"><h3>Workload containers</h3><button type="button" disabled={pending} onClick={() => changeSpec("containers", [...containers, emptyObject(containerFields)])}>Add container</button></div><p className="description">Define workload containers only; kukeon adds its default root container unless one is marked as root.</p>
        {!containers.length && <p className="empty-state compact-empty">No workload containers added. kukeon will add its default root container when the Cell is created.</p>}
        {containers.map((container, index) => <section className="container-editor" key={index}><div className="schema-array-heading"><h4>Container {index + 1}{container.id ? ` · ${String(container.id)}` : ""}</h4><button type="button" className="subtle-button" disabled={pending} onClick={() => changeSpec("containers", containers.filter((_, current) => current !== index))}>Remove container</button></div><div className="schema-fields">{containerFields.filter(field => !field.advanced).map(field => <FieldEditor key={field.key} field={field} value={container[field.key]} path={`cell.spec.containers.${index}.${field.key}`} errors={errors} disabled={pending} onChange={value => changeContainer(index, field.key, value)} />)}</div><button type="button" className="subtle-button" aria-expanded={!!containerAdvanced[index]} onClick={() => setContainerAdvanced(old => ({ ...old, [index]: !old[index] }))}>{containerAdvanced[index] ? "Hide" : "Show"} advanced container fields</button>{containerAdvanced[index] && <div className="schema-fields advanced-fields">{containerFields.filter(field => field.advanced).map(field => <FieldEditor key={field.key} field={field} value={container[field.key]} path={`cell.spec.containers.${index}.${field.key}`} errors={errors} disabled={pending} onChange={value => changeContainer(index, field.key, value)} />)}</div>}</section>)}
      </div>
      <div className="schema-section"><button type="button" className="subtle-button" aria-expanded={advanced} onClick={() => setAdvanced(value => !value)}>{advanced ? "Hide" : "Show"} advanced Cell configuration</button>{advanced && <div className="schema-fields advanced-fields">{cellAdvanced.map(field => <FieldEditor key={field.key} field={field} value={spec[field.key]} path={`cell.spec.${field.key}`} errors={errors} disabled={pending} onChange={value => changeSpec(field.key, value)} />)}{["autoDelete"].map(key => <FieldEditor key={key} field={{ key, label: "Delete Cell after root exits", kind: "boolean" }} value={spec[key]} path={`cell.spec.${key}`} errors={errors} disabled={pending} onChange={value => changeSpec(key, value)} />)}</div>}</div>
      {inventory.error && <p className="error" role="alert">Scope inventory could not be loaded: {inventory.error.message}</p>}{!!inventory.data?.failures.length && <p className="stale">Scope inventory is incomplete. Verify the selected target resources before creating.</p>}
      {Object.keys(errors).length > 0 && <p className="field-error" role="alert">Fix the highlighted fields before previewing or creating.</p>}
    </form></section>
    {error && <p className="error" role="alert">{error}</p>}{pending && <p role="status">{stage === "create" ? "Creating Cell…" : "Validating and preparing preview…"}</p>}
    {preview && <PreviewPanel review={review} onHide={() => setPreview(undefined)}>
      <PreviewSummary document={preview.document} />
      <p className="description">kukeon adds its default root container on creation unless one is defined; the created Cell shows the final specification.</p>
      <PreviewDocument document={preview.document} label="Complete manual Cell preview" />
    </PreviewPanel>}
    <CreationActions start={start} attach={attach} onStart={setStart} onAttach={setAttach} pending={pending} hasPreview={!!preview} previewLabel="Validate and preview Cell" canPreview={!pending} canCreate={!pending} onPreview={() => void submit(false)} onCreate={() => void submit(true)} />
  </div>;
}

export function NewCellPage({ revision, inventory, configScope, onCreated }: { revision: number; inventory: InventoryResource; configScope?: string[]; onCreated: (cell: CellDoc, options: CreatedOptions) => void }) {
  const [method, setMethod] = useState<"config" | "manual">("config");
  return <>
    <div className="creation-method-switch" role="group" aria-label="Cell creation method"><button type="button" aria-pressed={method === "config"} onClick={() => setMethod("config")}>From Config</button><button type="button" aria-pressed={method === "manual"} onClick={() => setMethod("manual")}>Manual</button></div>
    {method === "config" ? <ConfigCreation key={JSON.stringify(configScope ?? [])} revision={revision} inventory={inventory} configScope={configScope} onCreated={onCreated} /> : <ManualCreation inventory={inventory} onCreated={onCreated} />}
  </>;
}
