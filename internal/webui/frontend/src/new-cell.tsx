import { useEffect, useMemo, useState } from "react";
import type { Doc, InventoryResource } from "./inventory";
import { useResource } from "./request";
import { cellAdvanced, containerFields } from "./creation-schema.mjs";
import type { Field } from "./creation-schema.mjs";

type ScopeMetadata = { name: string; realm?: string; space?: string; stack?: string };
type CatalogDoc = { metadata: ScopeMetadata; spec?: Record<string, unknown>; kind?: string };
type CellDoc = Doc & { spec: { realmId: string; spaceId: string; stackId: string; containers?: Container[]; provenance?: unknown } };
type Container = { id: string; image?: string; attachable?: boolean; root?: boolean; [key: string]: unknown };
type RenderedValue = { kind: string; key: string; value: string; origin: string };
type Preview = { document: CellDoc; preview: string; digest?: string; values?: RenderedValue[] };

// Shows each merged input of the daemon's render with the level that set it (#1418).
function MergedValues({ values, digest }: { values: RenderedValue[]; digest?: string }) {
  return <div className="merged-values"><h4>Merged values</h4>
    <p className="description">Realm, Space and Stack defaults merge under the Config, deeper levels winning per key. Scalar values are hidden in the browser; use <code>kuke create cell --from-config … --dry-run --explain</code> to see them.</p>
    {!values.length ? <p>No values, repositories, secrets or images were merged.</p> : <table aria-label="Merged values and their origin"><thead><tr><th scope="col">Kind</th><th scope="col">Key</th><th scope="col">Value</th><th scope="col">Origin</th></tr></thead><tbody>
      {values.map(value => <tr key={`${value.kind}:${value.key}`}><td>{value.kind}</td><td><code>{value.key}</code></td><td>{value.value ? <code>{value.value}</code> : <span className="description">hidden</span>}</td><td><span className="badge">{value.origin}</span></td></tr>)}
    </tbody></table>}
    {digest && <p className="description">Render digest: <code>{digest}</code></p>}
  </div>;
}
type FormValue = Record<string, unknown>;

function scopedName(metadata: ScopeMetadata): string[] {
  return [metadata.realm, metadata.space, metadata.stack, metadata.name].filter((value): value is string => !!value);
}
function catalogLink(kind: string, scope: string[], document = false): string { return `#/${kind}/` + scope.map(encodeURIComponent).join("/") + (document ? "?document=1" : ""); }
function scopeLabel(scope: string[]): string { return scope.join(" / "); }
function configAPI(scope: string[]): string {
  const params = new URLSearchParams();
  ["realm", "space", "stack"].forEach((key, i) => { if (scope[i]) params.set(key, scope[i]); });
  return `/api/catalog/configs/${encodeURIComponent(scope.at(-1) ?? "")}?${params}`;
}
function blueprintAPI(reference: FormValue): string | null {
  if (typeof reference.name !== "string" || typeof reference.realm !== "string") return null;
  const scope = [reference.realm, reference.space, reference.stack, reference.name].filter((value): value is string => typeof value === "string" && !!value);
  const params = new URLSearchParams();
  ["realm", "space", "stack"].forEach((key, i) => { if (scope[i]) params.set(key, scope[i]); });
  return `/api/catalog/blueprints/${encodeURIComponent(scope.at(-1) ?? "")}?${params}`;
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
    return <fieldset className="schema-array"><legend>{field.label}</legend>{field.help && <p className="description">{field.help}</p>}{items.map((item, index) => <section className="schema-array-item" key={`${path}.${index}`}><div className="schema-array-heading"><h4>{field.itemLabel ?? field.label.slice(0, -1)} {index + 1}</h4><button type="button" className="subtle-button" disabled={disabled} onClick={() => onChange(items.filter((_, current) => current !== index))}>Remove</button></div><div className="schema-fields">{(field.fields ?? []).map(child => <FieldEditor key={`${path}.${index}.${child.key}`} field={child} value={item[child.key]} path={`${path}.${index}.${child.key}`} errors={errors} disabled={disabled} onChange={next => onChange(items.map((current, n) => n === index ? setAt(current, child.key, next) : current))} />)}</div></section>)}<button type="button" disabled={disabled} onClick={() => onChange([...items, emptyObject(field.fields ?? [])])}>Add {field.itemLabel?.toLowerCase() ?? "item"}</button></fieldset>;
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

function ConfigCreation({ revision, inventory, configScope, onCreated }: { revision: number; inventory: InventoryResource; configScope?: string[]; onCreated: (cell: CellDoc, start: boolean) => void }) {
  const configs = useResource<CatalogDoc[]>("/api/catalog/configs", revision);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(configScope ? JSON.stringify(configScope) : "");
  const configRows = (configs.data ?? []).map(doc => ({ doc, scope: scopedName(doc.metadata) }));
  const filtered = configRows.filter(({ scope }) => scopeLabel(scope).toLowerCase().includes(query.toLowerCase()));
  const config = configRows.find(row => JSON.stringify(row.scope) === selected);
  const detail = useResource<CatalogDoc>(config ? configAPI(config.scope) : null, revision);
  const blueprintRef = detail.data?.spec?.blueprint as FormValue | undefined;
  const blueprintValid = !!blueprintRef && typeof blueprintRef.name === "string" && !!blueprintRef.name && typeof blueprintRef.realm === "string" && !!blueprintRef.realm && !(blueprintRef.stack && !blueprintRef.space);
  const blueprintPath = blueprintValid && blueprintRef ? blueprintAPI(blueprintRef) : null;
  const blueprint = useResource<CatalogDoc>(blueprintPath, revision);
  const blueprintScope = blueprintRef ? [blueprintRef.realm, blueprintRef.space, blueprintRef.stack, blueprintRef.name].filter((value): value is string => typeof value === "string" && !!value) : [];
  const [name, setName] = useState("");
  const [env, setEnv] = useState("");
  const [ignoreDiskPressure, setIgnoreDiskPressure] = useState(false);
  const [start, setStart] = useState(false);
  const [preview, setPreview] = useState<Preview>();
  const [pending, setPending] = useState(false);
  const [stage, setStage] = useState<"preview" | "create">("preview");
  const [error, setError] = useState("");
  const targetScope = config ? [config.doc.metadata.realm || "default", config.doc.metadata.space || "default", config.doc.metadata.stack || "default"] : [];
  const stackMissing = !!config && !!inventory.data && !inventory.data.rows[2].some(row => row.scope[0] === targetScope[0] && row.scope[1] === targetScope[1] && row.scope[2] === targetScope[2]);
  const supportsAttach = !!preview?.document.spec.containers?.some(container => container.attachable && !container.root);
  async function submit(create: boolean) {
    if (!config || pending) return;
    setPending(true); setStage(create ? "create" : "preview"); setError("");
    try {
      const body = { config: { name: config.doc.metadata.name, realm: config.doc.metadata.realm, space: config.doc.metadata.space, stack: config.doc.metadata.stack }, name: name.trim(), ...(create && preview ? { resolvedName: preview.document.metadata.name, preview: preview.preview } : {}), env: env.split("\n").filter(Boolean), ignoreDiskPressure };
      const response = await fetch(`/api/cells/from-config/${create ? "create" : "preview"}`, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(body) });
      const result = await response.json();
      if (!response.ok) {
        const message = result.error?.message ?? result.message ?? `Request failed (HTTP ${response.status})`;
        throw new Error(create && (result.error?.code === "unavailable" || response.status >= 500) ? `The creation outcome could not be confirmed. Refresh the Cell inventory before retrying. ${message}` : message);
      }
      if (create) onCreated(result as CellDoc, start);
      else setPreview(result as Preview);
    } catch (failure) {
      const message = failure instanceof Error ? failure.message : "The request could not reach the web server.";
      setError(create && failure instanceof TypeError ? `The creation outcome could not be confirmed. Refresh the Cell inventory before retrying. ${message}` : message);
    }
    finally { setPending(false); }
  }
  return <div className="creation-layout">
    <section className="panel"><div className="panel-heading"><h2>Choose a CellConfig</h2><span className="badge">Existing config</span></div><div className="panel-body creation-form">
      {configs.loading && <p role="status">Loading CellConfigs…</p>}{configs.error && <div className="error" role="alert"><strong>CellConfigs unavailable</strong><p>{configs.error.message}</p></div>}{configs.error && configs.data && <p className="stale">Showing stale CellConfigs.</p>}
      <label>Search CellConfigs<input type="search" value={query} placeholder="Name or full scope" onChange={event => setQuery(event.target.value)} /></label>
      <label>CellConfig<select value={selected} onChange={event => { setSelected(event.target.value); setPreview(undefined); setStart(false); setError(""); }}><option value="">Select a CellConfig…</option>{filtered.map(row => <option key={JSON.stringify(row.scope)} value={JSON.stringify(row.scope)}>{row.doc.metadata.name} — {scopeLabel(row.scope.slice(0, -1))}</option>)}</select></label>
      {!configs.loading && !configs.error && !configRows.length && <p className="description">No CellConfigs are available.</p>}
      {inventory.data?.failures.length ? <p className="stale">Inventory is incomplete. Target scope may be unavailable.</p> : null}
    </div></section>
    {config && <>
      <section className="panel"><div className="panel-heading"><h2>Config relationships and target</h2><span className="badge">{scopeLabel(config.scope)}</span></div><div className="panel-body">
        {detail.loading && <p role="status">{detail.data ? "Refreshing CellConfig details…" : "Loading CellConfig details…"}</p>}{detail.error && <div className="error" role="alert"><strong>CellConfig details unavailable</strong><p>{detail.error.message}</p></div>}{detail.error && detail.data && <p className="stale">Showing stale CellConfig details.</p>}
        {detail.data && <><p>Config: <a className="resource-name" href={catalogLink("configs", config.scope, true)}>{detail.data.metadata.name}</a> <span className="description">({scopeLabel(config.scope.slice(0, -1))})</span></p><p>Target: <strong>{detail.data.metadata.realm || "default"} / {detail.data.metadata.space || "default"} / {detail.data.metadata.stack || "default"}</strong></p>{stackMissing && <p className="error" role="alert">The target Stack is not present in the loaded inventory.</p>}
          <h3>Referenced Blueprint</h3>{!blueprintRef ? <p className="error" role="alert">This config does not contain a Blueprint reference.</p> : !blueprintValid ? <p className="error" role="alert">The Blueprint reference is incomplete: {scopeLabel(blueprintScope)}</p> : blueprint.loading ? <p role="status">Checking referenced Blueprint…</p> : blueprint.error ? <div className="error" role="alert"><strong>{blueprint.error.code === "not_found" ? "Blueprint reference is missing" : "Blueprint could not be loaded"}</strong><p>{blueprint.error.message}</p><p>Reference: {scopeLabel(blueprintScope)}</p></div> : blueprint.data ? <p><a className="resource-name" href={catalogLink("blueprints", blueprintScope)}>{blueprint.data.metadata.name}</a> <span className="description">{scopeLabel(blueprintScope.slice(0, -1))}</span></p> : <p className="description">Blueprint details are unavailable.</p>}
        </>}
      </div></section>
      <section className="panel"><div className="panel-heading"><h2>Create a stopped Cell</h2></div><form className="panel-body creation-form" onSubmit={event => { event.preventDefault(); void submit(false); }}>
        <label>Cell name (optional)<input value={name} disabled={pending} onChange={event => { setName(event.target.value); setPreview(undefined); setStart(false); }} /><small>Leave blank to let Kukeon choose a name using its existing naming algorithm.</small></label>
        <label>Environment overrides<textarea value={env} disabled={pending} placeholder="KEY=VALUE, one per line" onChange={event => { setEnv(event.target.value); setPreview(undefined); setStart(false); }} /></label>
        <label className="schema-check"><input type="checkbox" checked={ignoreDiskPressure} disabled={pending} onChange={event => { setIgnoreDiskPressure(event.target.checked); setPreview(undefined); setStart(false); }} /> Ignore disk pressure for this creation</label>
        <p className="description">The daemon renders the existing config and blueprint, merging the Realm, Space and Stack defaults. Environment values are redacted in the preview. Creation leaves the Cell stopped.</p>
        {error && <p className="error" role="alert">{error}</p>}{pending && <p role="status">{stage === "create" ? "Creating Cell…" : "Preparing preview…"}</p>}
        {preview && <div className="creation-review"><h3>Review Cell document</h3><p>Resolved Cell name: <strong>{preview.document.metadata.name}</strong></p><p className="description">The reviewed automatic name is sent back unchanged when creating, so preview and creation use the same name. Kukeon may add its default root container during materialization.</p>{preview.values && <MergedValues values={preview.values} digest={preview.digest} />}<pre tabIndex={0} aria-label="Complete Cell preview">{JSON.stringify(preview.document, null, 2)}</pre><label className="schema-check"><input type="checkbox" checked={start} disabled={pending} onChange={event => setStart(event.target.checked)} /> {supportsAttach ? "Start and attach after creation" : "Start after creation"}</label><button type="button" disabled={pending || stackMissing} onClick={() => void submit(true)}>Create Cell</button></div>}
        <button type="submit" disabled={pending || !detail.data || !blueprint.data || stackMissing}>{preview ? "Refresh preview" : "Preview Cell"}</button>
      </form></section>
    </>}
  </div>;
}

function ManualCreation({ inventory, onCreated }: { inventory: InventoryResource; onCreated: (cell: CellDoc, start: boolean) => void }) {
  const realms = inventory.data?.rows[0] ?? [];
  const [model, setModel] = useState<FormValue>(() => ({ name: "", realm: "", space: "", stack: "", labels: {}, annotations: {}, spec: { containers: [], autoDelete: false, nestedCgroupRuntime: false, ignoreDiskPressure: false } }));
  const [advanced, setAdvanced] = useState(false);
  const [containerAdvanced, setContainerAdvanced] = useState<Record<number, boolean>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<Preview>();
  const [pending, setPending] = useState(false);
  const [stage, setStage] = useState<"preview" | "create">("preview");
  const [start, setStart] = useState(false);
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
  const canPreview = !Object.keys(localErrors).length && !pending;
  function changeRoot(key: string, value: unknown) { setModel(old => setAt(old, key, value)); setPreview(undefined); setStart(false); setErrors({}); }
  function changeSpec(key: string, value: unknown) { setModel(old => setAt(old, "spec", setAt(old.spec as FormValue, key, value))); setPreview(undefined); setStart(false); setErrors({}); }
  function changeContainer(index: number, key: string, value: unknown) { changeSpec("containers", containers.map((container, i) => i === index ? setAt(container, key, value) : container)); }
  async function submit(create: boolean) {
    if (!create && !canPreview) { setErrors(localErrors); return; }
    if (pending) return;
    setPending(true); setStage(create ? "create" : "preview"); setError("");
    try {
      const payload = { name: String(model.name).trim(), realm, space, stack: String(model.stack ?? ""), labels: model.labels, annotations: model.annotations, spec };
      const response = await fetch(`/api/cells/manual/${create ? "create" : "preview"}`, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify({ ...payload, ...(create && preview ? { preview: preview.preview } : {}) }) });
      const result = await response.json();
      if (!response.ok) {
        const message = result.error?.message ?? result.message ?? `Request failed (HTTP ${response.status})`;
        throw new Error(create && (result.error?.code === "unavailable" || response.status >= 500) ? `The creation outcome could not be confirmed. Refresh the Cell inventory before retrying. ${message}` : message);
      }
      if (create) onCreated(result as CellDoc, start);
      else setPreview(result as Preview);
    } catch (failure) {
      const message = failure instanceof Error ? failure.message : "The request could not reach the web server.";
      setError(create && failure instanceof TypeError ? `The creation outcome could not be confirmed. Refresh the Cell inventory before retrying. ${message}` : message);
    }
    finally { setPending(false); }
  }
  const previewAttachable = !!preview?.document.spec.containers?.some(container => container.attachable && !container.root);
  return <section className="panel"><div className="panel-heading"><h2>Build a Cell specification</h2><span className="badge">No Config or Blueprint</span></div><form className="panel-body creation-form" onSubmit={event => { event.preventDefault(); void submit(false); }}>
    <div className="schema-section"><h3>Basic Cell configuration</h3><div className="schema-fields">
      <label className="schema-field" htmlFor="manual-name"><span>Cell name <b aria-label="required"> *</b></span><input id="manual-name" value={String(model.name)} disabled={pending} aria-invalid={!!errors["cell.name"]} aria-describedby={errors["cell.name"] ? "manual-name-error" : undefined} onChange={event => changeRoot("name", event.target.value)} />{errors["cell.name"] && <small id="manual-name-error" className="field-error">{errors["cell.name"]}</small>}</label>
      <label className="schema-field"><span>Realm <b aria-label="required"> *</b></span><select value={realm} disabled={pending} onChange={event => { changeRoot("realm", event.target.value); changeRoot("space", ""); changeRoot("stack", ""); }}><option value="">Select a Realm…</option>{realms.map(row => <option key={row.scope[0]}>{row.scope[0]}</option>)}</select>{errors["cell.realm"] && <small className="field-error">{errors["cell.realm"]}</small>}</label>
      <label className="schema-field"><span>Space <b aria-label="required"> *</b></span><select value={space} disabled={pending || !realm} onChange={event => { changeRoot("space", event.target.value); changeRoot("stack", ""); }}><option value="">Select a Space…</option>{spaces.map(row => <option key={row.scope[1]}>{row.scope[1]}</option>)}</select>{errors["cell.space"] && <small className="field-error">{errors["cell.space"]}</small>}</label>
      <label className="schema-field"><span>Stack <b aria-label="required"> *</b></span><select value={String(model.stack ?? "")} disabled={pending || !space} onChange={event => changeRoot("stack", event.target.value)}><option value="">Select a Stack…</option>{stacks.map(row => <option key={row.scope[2]}>{row.scope[2]}</option>)}</select>{errors["cell.stack"] && <small className="field-error">{errors["cell.stack"]}</small>}</label>
    </div></div>
    <div className="schema-section"><h3>Metadata</h3><div className="schema-fields">{(["labels", "annotations"] as const).map(key => <FieldEditor key={key} field={{ key, label: key === "labels" ? "Labels" : "Annotations", kind: "map" }} value={model[key]} path={`cell.${key}`} errors={errors} disabled={pending} onChange={value => changeRoot(key, value)} />)}</div></div>
    <div className="schema-section"><div className="schema-section-heading"><h3>Workload containers</h3><button type="button" disabled={pending} onClick={() => changeSpec("containers", [...containers, emptyObject(containerFields)])}>Add container</button></div><p className="description">Define workload containers only. Kukeon creates its default root container if none is explicitly marked as root.</p>
      {!containers.length && <p className="empty-state compact-empty">No workload containers added. Kukeon will add its default root container when the Cell is created.</p>}
      {containers.map((container, index) => <section className="container-editor" key={index}><div className="schema-array-heading"><h4>Container {index + 1}{container.id ? ` · ${String(container.id)}` : ""}</h4><button type="button" className="subtle-button" disabled={pending} onClick={() => changeSpec("containers", containers.filter((_, current) => current !== index))}>Remove container</button></div><div className="schema-fields">{containerFields.filter(field => !field.advanced).map(field => <FieldEditor key={field.key} field={field} value={container[field.key]} path={`cell.spec.containers.${index}.${field.key}`} errors={errors} disabled={pending} onChange={value => changeContainer(index, field.key, value)} />)}</div><button type="button" className="subtle-button" aria-expanded={!!containerAdvanced[index]} onClick={() => setContainerAdvanced(old => ({ ...old, [index]: !old[index] }))}>{containerAdvanced[index] ? "Hide" : "Show"} advanced container fields</button>{containerAdvanced[index] && <div className="schema-fields advanced-fields">{containerFields.filter(field => field.advanced).map(field => <FieldEditor key={field.key} field={field} value={container[field.key]} path={`cell.spec.containers.${index}.${field.key}`} errors={errors} disabled={pending} onChange={value => changeContainer(index, field.key, value)} />)}</div>}</section>)}
    </div>
    <div className="schema-section"><button type="button" className="subtle-button" aria-expanded={advanced} onClick={() => setAdvanced(value => !value)}>{advanced ? "Hide" : "Show"} advanced Cell configuration</button>{advanced && <div className="schema-fields advanced-fields">{cellAdvanced.map(field => <FieldEditor key={field.key} field={field} value={spec[field.key]} path={`cell.spec.${field.key}`} errors={errors} disabled={pending} onChange={value => changeSpec(field.key, value)} />)}{["autoDelete"].map(key => <FieldEditor key={key} field={{ key, label: "Delete Cell after root exits", kind: "boolean" }} value={spec[key]} path={`cell.spec.${key}`} errors={errors} disabled={pending} onChange={value => changeSpec(key, value)} />)}</div>}</div>
    <p className="description">Manual Cell input is validated by Kukeon's API conversion and daemon on preview and create. The daemon owns runtime status, IDs, provenance, and generated container fields.</p>
    {inventory.error && <p className="error" role="alert">Scope inventory could not be loaded: {inventory.error.message}</p>}{!!inventory.data?.failures.length && <p className="stale">Scope inventory is incomplete. Verify the selected target resources before creating.</p>}
    {error && <p className="error" role="alert">{error}</p>}{pending && <p role="status">{stage === "create" ? "Creating Cell…" : "Validating and preparing preview…"}</p>}
    {preview && <div className="creation-review"><h3>Review Cell document</h3><p>Cell scope: <strong>{scopeLabel([preview.document.spec.realmId, preview.document.spec.spaceId, preview.document.spec.stackId, preview.document.metadata.name])}</strong></p><p className="description">The preview contains your authorable specification. Kukeon adds its default root container during creation when you have not explicitly defined one; the returned Cell document will show the daemon’s final specification.</p><pre tabIndex={0} aria-label="Complete manual Cell preview">{JSON.stringify(preview.document, null, 2)}</pre><label className="schema-check"><input type="checkbox" checked={start} disabled={pending} onChange={event => setStart(event.target.checked)} /> {previewAttachable ? "Start and attach after creation" : "Start after creation"}</label><button type="button" disabled={pending} onClick={() => void submit(true)}>Create Cell</button></div>}
    <button type="submit" disabled={pending}>Validate and preview Cell</button>
    {Object.keys(errors).length > 0 && <p className="field-error" role="alert">Fix the highlighted fields before previewing.</p>}
  </form></section>;
}

export function NewCellPage({ revision, inventory, configScope, onCreated }: { revision: number; inventory: InventoryResource; configScope?: string[]; onCreated: (cell: CellDoc, start: boolean) => void }) {
  const [method, setMethod] = useState<"config" | "manual">("config");
  return <>
    <div className="creation-method-switch" role="group" aria-label="Cell creation method"><button type="button" aria-pressed={method === "config"} onClick={() => setMethod("config")}>From Config</button><button type="button" aria-pressed={method === "manual"} onClick={() => setMethod("manual")}>Manual</button></div>
    {method === "config" ? <ConfigCreation key={JSON.stringify(configScope ?? [])} revision={revision} inventory={inventory} configScope={configScope} onCreated={onCreated} /> : <ManualCreation inventory={inventory} onCreated={onCreated} />}
  </>;
}
