import { useRef, useState } from "react";
import { teamsLink } from "./teams.mjs";
import { secretTypeOf, teamOf } from "./connectors.mjs";
import { ErrorNotice, useResource } from "./request";
import { catalogPageLink, catalogTitles, newCellLink, newConfigLink, newSecretLink, resourceLink, workspaceLink } from "./routes";
import { ScopePath, StateBadge } from "./status";
import type { InventoryResource, Row } from "./inventory";
import { cellsForBlueprint, cellsForConfig, configsForBlueprint, configsForStateVolume, volumeMounts, type ConfigDoc } from "./relationships.mjs";
import { DocumentRowLink, RowAction, RowActions } from "./icons";
import { catalogAPI, catalogDeleteAPI } from "./catalog-url.mjs";
import { useNotify } from "./notifications";
import { useModalDialog } from "./focus";
import { AskMajordomo, catalogQuestion } from "./majordomo";
import { ConfigConnectors, LoginButton, LoginStateBadge, NeedsConnectorBadge, connectorsLink } from "./connector-ui";
import { SecretForm } from "./secret-form";
import { ScheduleDetails, cellLinker } from "./schedule";
import { nextColumn, scheduleColumn, validTime, type Schedule, type ScheduleStatus } from "./schedule-format.mjs";

type Metadata = { name: string; realm?: string; space?: string; stack?: string; cell?: string; labels?: Record<string, string>; annotations?: Record<string, string> };
type CatalogDoc = { metadata: Metadata; spec?: Record<string, unknown>; [key: string]: unknown };
type Reference = { kind: string; name: string; realm: string; space?: string; stack?: string; cell?: string };
const coordinates = ["realm", "space", "stack", "cell"] as const;
function scopeOf(metadata: Metadata): string[] { return coordinates.map(key => metadata[key]).filter((value): value is string => !!value); }
export function catalogLink(kind: string, scope: string[], document = false): string { return `#/${kind}/` + scope.map(encodeURIComponent).join("/") + (document ? "?document=1" : ""); }
function referenceScope(ref: Reference): string[] { return [...scopeOf(ref), ref.name]; }
function references(doc: { [key: string]: unknown }): Reference[] {
  const result: Reference[] = [];
  const visit = (value: unknown, key: string) => {
    if (!value || typeof value !== "object") return;
    if (Array.isArray(value)) { value.forEach(item => visit(item, key)); return; }
    const object = value as Record<string, unknown>;
    const kind = key === "blueprint" ? "blueprints" : key === "secretRef" ? "secrets" : key === "volumeRef" ? "volumes" : undefined;
    if (kind && typeof object.name === "string" && typeof object.realm === "string") {
      const ref = { kind, name: object.name, realm: object.realm, space: typeof object.space === "string" ? object.space : undefined, stack: typeof object.stack === "string" ? object.stack : undefined, cell: typeof object.cell === "string" ? object.cell : undefined };
      if (!result.some(existing => JSON.stringify(existing) === JSON.stringify(ref))) result.push(ref);
    }
    Object.entries(object).forEach(([childKey, child]) => visit(child, childKey));
  };
  visit(doc.spec, "spec");
  return result;
}
function ReferenceRow({ reference, revision }: { reference: Reference; revision: number }) {
  const scope = referenceScope(reference);
  const templated = scope.some(value => value.includes("${"));
  const invalid = !reference.name || !reference.realm || (!!reference.stack && !reference.space) || (!!reference.cell && (!reference.stack || reference.kind !== "secrets"));
  const target = useResource<CatalogDoc>(templated || invalid ? null : catalogAPI(reference.kind, scope), revision);
  return <li><div><span className="badge">{catalogTitles[reference.kind]}</span> {templated || invalid ? reference.name : <a className="resource-name" href={catalogLink(reference.kind, scope)}>{reference.name}</a>}<p className="description">{scope.slice(0, -1).join(" / ")}</p></div><div>{invalid ? <span>Invalid reference scope</span> : templated ? <span>Template reference — resolved when materialized</span> : target.loading ? <span>Checking reference…</span> : target.error ? <div className="error" role="alert"><strong>{target.error.code === "not_found" ? "Missing reference" : "Reference retrieval failed"}</strong><p>{target.error.message}</p></div> : <span className="badge">Available</span>}</div></li>;
}
function scopedVolumeSources(doc: { [key: string]: unknown }): string[] {
  const found = new Set<string>();
  const visit = (value: unknown) => {
    if (!value || typeof value !== "object") return;
    if (Array.isArray(value)) { value.forEach(visit); return; }
    const object = value as Record<string, unknown>;
    if (object.kind === "volume" && typeof object.source === "string" && object.source) found.add(object.source);
    Object.values(object).forEach(visit);
  };
  visit(doc.spec);
  return [...found];
}

type RelatedCatalogDoc = CatalogDoc & ConfigDoc;
function lineageReference(metadata: Metadata): Reference {
  return { kind: "configs", name: metadata.name, realm: metadata.realm ?? "", space: metadata.space, stack: metadata.stack };
}
function relationResourceLink(kind: string, scope: string[]): string { return catalogLink(kind, scope); }
function scopeLabel(scope: string[]): string { return scope.join(" / "); }
function cellScope(row: Row): string[] { return row.scope.length === 4 ? row.scope : [...row.scope.slice(0, 3), row.doc.metadata.name]; }
// InventoryNotices says how far a list derived from the shared Cell inventory can be trusted.
function InventoryNotices({ inventory }: { inventory: InventoryResource }) {
  return <>
    {inventory.loading && !inventory.data && <p role="status">Loading shared Cell inventory…</p>}
    {inventory.loading && inventory.data && <p role="status">Refreshing Cell inventory; displayed Cells come from the last completed retrieval.</p>}
    {inventory.error && <p className="stale">Cell inventory could not be refreshed: {inventory.error.message}</p>}
    {!!inventory.data?.failures.length && <p className="stale">Cell inventory is incomplete; this relationship list may be partial.</p>}
    {!inventory.data && !inventory.loading && !inventory.error && <p className="empty-state">Cell inventory has not been retrieved.</p>}
  </>;
}
function RelatedCellList({ rows, inventory, configLabel }: { rows: Array<Row & { config?: RelatedCatalogDoc }>; inventory: InventoryResource; configLabel?: boolean }) {
  return <>
    <InventoryNotices inventory={inventory} />
    {rows.length > 0 && <ul className="state-list related-cell-list">{rows.map(row => {
      const scope = cellScope(row);
      return <li key={JSON.stringify(scope)}><div><a className="resource-name" href={workspaceLink(scope)}>{row.doc.metadata.name}</a><p className="description">{scopeLabel(scope.slice(0, -1))}</p>{configLabel && row.config && <p className="description">Config: <a href={relationResourceLink("configs", [...scopeOf(row.config.metadata), row.config.metadata.name])}>{row.config.metadata.name}</a> · {scopeLabel(scopeOf(row.config.metadata))}</p>}</div><StateBadge state={row.doc.status?.state} /><a className="document-action" href={workspaceLink(scope)}>Open terminal</a></li>;
    })}</ul>}
    {inventory.data && !rows.length && !inventory.loading && !inventory.error && !inventory.data.failures.length && <p className="description">No related Cells found.</p>}
  </>;
}
// VolumeMountTable lists the cells that mount a Volume: one row per mount, so a cell mounting it in two
// containers shows twice.
function VolumeMountTable({ mounts, inventory }: { mounts: ReturnType<typeof volumeMounts<Row>>; inventory: InventoryResource }) {
  return <>
    <InventoryNotices inventory={inventory} />
    {mounts.length > 0 && <div className="table-scroll"><table className="stackable volume-mounts" aria-label="Cells mounting this Volume"><thead><tr><th>Cell</th><th>State</th><th>Container</th><th>Mount path</th><th>Access</th><th>Created with the cell</th></tr></thead><tbody>{mounts.map((use, i) => <tr key={i}>
      <td data-label="Cell"><a className="resource-name" href={resourceLink(use.row.scope)}>{use.row.doc.metadata.name}</a><p className="description">{scopeLabel(use.row.scope.slice(0, -1))}{use.by === "source" ? " · by name" : ""}</p></td>
      <td data-label="State"><StateBadge state={use.row.doc.status?.state} /></td>
      <td data-label="Container"><code>{use.container || "—"}</code></td>
      <td data-label="Mount path"><code>{use.target || "—"}</code></td>
      <td data-label="Access">{use.readOnly ? "Read-only" : "Read-write"}</td>
      <td data-label="Created with the cell">{use.ensure ? "Yes (ensure)" : "No"}</td>
    </tr>)}</tbody></table></div>}
    {inventory.data && !mounts.length && !inventory.loading && !inventory.error && !inventory.data.failures.length && <p className="description">No cell in the inventory mounts this Volume.</p>}
  </>;
}
function VolumeRelationships({ doc, revision, inventory }: { doc: CatalogDoc; revision: number; inventory: InventoryResource }) {
  const volumes = useResource<CatalogDoc[]>("/api/catalog/volumes", revision);
  const configs = useResource<RelatedCatalogDoc[]>("/api/catalog/configs", revision);
  const mounts = inventory.data && volumes.data ? volumeMounts(inventory.data.rows[3], volumes.data, doc) : [];
  const logins = configsForStateVolume(configs.data, doc);
  // A Volume tagged with a connector is a login identity (#1685).
  const spec = doc.spec as { connector?: unknown; method?: unknown } | undefined;
  const tag = { connector: typeof spec?.connector === "string" ? spec.connector : "", method: typeof spec?.method === "string" ? spec.method : "" };
  const login = (doc.status as { login?: { state?: string; since?: string } } | undefined)?.login;
  return <div className="relationship-grid">
    <section className="panel related-resources volume-mounted-by" aria-label="Mounted by"><div className="panel-heading"><h2>Mounted by</h2><span className="badge">{inventory.data && volumes.data ? `${mounts.length} mount${mounts.length === 1 ? "" : "s"}` : "Resolving…"}</span></div><div className="panel-body">
      <p className="description">Every cell in the inventory whose spec mounts this Volume, whatever its state. A mount by name resolves like kukeond does: the Volume of that name in the cell's Stack, else its Space, else its Realm.</p>
      {volumes.error && <ErrorNotice error={volumes.error} kind="Volume" />}
      <VolumeMountTable mounts={mounts} inventory={inventory} />
    </div></section>
    {tag.connector && <section className="panel related-resources volume-login-identity" aria-label="Login identity"><div className="panel-heading"><h2>Login identity</h2><span className="badge">{tag.connector}</span></div><div className="panel-body">
      <p className="description">This Volume is a login identity of the <a href={connectorsLink(tag.connector)}><code>{tag.connector}</code></a> connector{tag.method && <> (<code>{tag.method}</code> method)</>}: the harness keeps its login here. Only whether a login is present is shown — never the login itself.</p>
      <dl className="details-grid"><dt>Login</dt><dd><LoginStateBadge state={login?.state} />{login?.since && <span className="description"> since {new Date(login.since).toLocaleString()}</span>}</dd></dl>
      <div className="connector-actions"><LoginButton connector={tag.connector} title={tag.connector} volume={{ name: doc.metadata.name, realm: doc.metadata.realm ?? "", space: doc.metadata.space, stack: doc.metadata.stack }} state={login?.state} method={tag.method || undefined} /></div>
    </div></section>}
    <section className="panel related-resources" aria-label="Harness state for"><div className="panel-heading"><h2>Harness state for</h2></div><div className="panel-body">
      <p className="description">Configs whose harness keeps its state in this Volume: the login of a connector's login method, or the sessions and settings of a token method.</p>
      {configs.loading && !configs.data && <p role="status">Loading CellConfigs…</p>}{configs.error && <ErrorNotice error={configs.error} kind="CellConfig" />}
      {logins.length > 0 && <ul className="state-list">{logins.map(({ config, connectors }) => { const scope = [...scopeOf(config.metadata), config.metadata.name]; return <li key={JSON.stringify(scope)}><div><a className="resource-name" href={catalogLink("configs", scope)}>{config.metadata.name}</a><p className="description">{scopeLabel(scope.slice(0, -1))} · {connectors.join(", ")}</p></div></li>; })}</ul>}
      {configs.data && !logins.length && !configs.error && <p className="description">No Config keeps harness state in this Volume.</p>}
    </div></section>
  </div>;
}
function ConfigRelationships({ doc, revision, inventory }: { doc: CatalogDoc; revision: number; inventory: InventoryResource }) {
  const ref = doc.spec?.blueprint as Reference | undefined;
  const valid = !!ref && !!ref.name && !!ref.realm && !(ref.stack && !ref.space);
  const scope = ref ? referenceScope(ref) : [];
  const target = useResource<CatalogDoc>(valid ? catalogAPI("blueprints", scope) : null, revision);
  const cells = inventory.data ? cellsForConfig(inventory.data.rows[3], lineageReference(doc.metadata)) as Row[] : [];
  return <div className="relationship-grid">
    <section className="panel related-resources"><div className="panel-heading"><h2>Referenced Blueprint</h2></div><div className="panel-body">
      {!ref ? <div className="error" role="alert"><strong>Blueprint reference missing</strong><p>This CellConfig has no Blueprint reference.</p></div> : !valid ? <div className="error" role="alert"><strong>Blueprint reference is incomplete</strong><p>Reference: {scopeLabel(scope)}</p></div> : target.loading && !target.data ? <p role="status">Loading referenced Blueprint…</p> : target.error ? <><ErrorNotice error={target.error} kind="Blueprint" />{target.data && <p className="stale">Showing stale Blueprint details.</p>}</> : target.data ? <p><a className="resource-name" href={catalogLink("blueprints", scope)}>{target.data.metadata.name}</a><span className="description"> {scopeLabel(scope.slice(0, -1))}</span></p> : <p className="description">Blueprint details are unavailable.</p>}
    </div></section>
    <section className="panel related-resources"><div className="panel-heading"><h2>Cells using this Config</h2><span className="badge">Lineage</span></div><div className="panel-body"><p className="description">Resolved from each Cell’s scoped Config provenance.</p><RelatedCellList rows={cells} inventory={inventory} /></div></section>
  </div>;
}
function BlueprintRelationships({ doc, revision, inventory }: { doc: CatalogDoc; revision: number; inventory: InventoryResource }) {
  const configs = useResource<RelatedCatalogDoc[]>("/api/catalog/configs", revision);
  const blueprint: Reference = { kind: "blueprints", name: doc.metadata.name, realm: doc.metadata.realm ?? "", space: doc.metadata.space, stack: doc.metadata.stack };
  const matches = configs.data ? configsForBlueprint(configs.data, blueprint) : [];
  const cellRows = inventory.data ? cellsForBlueprint(inventory.data.rows[3], matches) as Array<Row & { config: RelatedCatalogDoc }> : [];
  return <div className="relationship-grid">
    <section className="panel related-resources"><div className="panel-heading"><h2>Configs using this Blueprint</h2><span className="badge">Scoped references</span></div><div className="panel-body">
      {configs.loading && <p role="status">{configs.data ? "Refreshing CellConfigs…" : "Loading CellConfigs…"}</p>}{configs.error && <ErrorNotice error={configs.error} kind="CellConfig" />}{configs.error && configs.data && <p className="stale">Showing stale CellConfig results.</p>}
      {configs.data && matches.length > 0 && <ul className="state-list">{matches.map(config => { const scope = [...scopeOf(config.metadata), config.metadata.name]; return <li key={JSON.stringify(scope)}><div><a className="resource-name" href={catalogLink("configs", scope)}>{config.metadata.name}</a><p className="description">{scopeLabel(scope.slice(0, -1))}</p></div><a className="document-action" href={catalogLink("configs", scope)}>Open Config</a></li>; })}</ul>}
      {configs.data && !matches.length && !configs.error && <p className="description">No CellConfigs reference this Blueprint.</p>}
    </div></section>
    <section className="panel related-resources"><div className="panel-heading"><h2>Cells using this Blueprint through Configs</h2><span className="badge">Lineage</span></div><div className="panel-body"><p className="description">Cells are matched by their scoped Config provenance, not by generated names.</p>{configs.error && !configs.data ? <p className="error" role="alert">Cannot resolve indirect Cell relationships because the CellConfig inventory failed.</p> : <RelatedCellList rows={cellRows} inventory={inventory} configLabel />}</div></section>
  </div>;
}
export function RelatedResources({ doc, revision }: { doc: { [key: string]: unknown }; revision: number }) {
  const refs = references(doc);
  const sources = scopedVolumeSources(doc);
  if (!refs.length && !sources.length) return null;
  return <section className="panel related-resources" aria-label="Resource references"><div className="panel-heading"><h2>References</h2></div><div className="panel-body"><p className="description">Explicit references from this document. References describe usage, not ownership.</p><ul className="state-list">{refs.map(ref => <ReferenceRow key={JSON.stringify(ref)} reference={ref} revision={revision} />)}{sources.map(source => <li key={source}><div><span className="badge">Volume source</span> {source}<p className="description">Scope is resolved by the daemon at create/start; no ownership is inferred.</p></div></li>)}</ul></div></section>;
}
const singular: Record<string, string> = { blueprints: "Blueprint", configs: "Config", volumes: "Volume", secrets: "Secret" };
const deleteConsequence: Record<string, string> = {
  configs: "Delete removes this CellConfig document from kukeond. Cells created from it are not deleted; they keep running and lose only their Config lineage.",
  blueprints: "Delete removes this CellBlueprint document from kukeond. Configs created from it are independent copies and are not deleted.",
  volumes: "Delete removes this Volume and its daemon-provisioned directory, including the data stored in it. kukeond refuses while a Ready or Degraded cell mounts the Volume.",
  secrets: "Delete removes this Secret's daemon-stored value. kukeond refuses while a cell references it through secretRef. The value is never shown here.",
};
// Dependents the catalog already loads elsewhere: Cells by Config lineage, and Configs (plus their Cells)
// by Blueprint reference. Nothing else is inferred.
function DeleteDependents({ kind, doc, revision, inventory }: { kind: string; doc: CatalogDoc; revision: number; inventory: InventoryResource }) {
  const configs = useResource<RelatedCatalogDoc[]>(kind === "blueprints" ? "/api/catalog/configs" : null, revision);
  const volumes = useResource<CatalogDoc[]>(kind === "volumes" ? "/api/catalog/volumes" : null, revision);
  if (kind === "volumes") {
    const mounts = inventory.data && volumes.data ? volumeMounts(inventory.data.rows[3], volumes.data, doc) : [];
    return <section className="catalog-delete-dependents" aria-label="Known related resources"><h3>Cells mounting this Volume</h3>
      <p className="description">kukeond refuses the delete while a Ready or Degraded cell mounts the Volume. Stopped cells keep their mount in the spec and fail to start once it is gone.</p>
      {volumes.error && <ErrorNotice error={volumes.error} kind="Volume" />}
      <VolumeMountTable mounts={mounts} inventory={inventory} />
    </section>;
  }
  if (kind === "configs") {
    const cells = inventory.data ? cellsForConfig(inventory.data.rows[3], lineageReference(doc.metadata)) as Row[] : [];
    return <section className="catalog-delete-dependents" aria-label="Known related resources"><h3>Cells created from this Config</h3><RelatedCellList rows={cells} inventory={inventory} /></section>;
  }
  if (kind !== "blueprints") return null;
  const blueprint: Reference = { kind: "blueprints", name: doc.metadata.name, realm: doc.metadata.realm ?? "", space: doc.metadata.space, stack: doc.metadata.stack };
  const matches = configs.data ? configsForBlueprint(configs.data, blueprint) : [];
  const cellRows = inventory.data ? cellsForBlueprint(inventory.data.rows[3], matches) as Array<Row & { config: RelatedCatalogDoc }> : [];
  return <section className="catalog-delete-dependents" aria-label="Known related resources">
    <h3>Configs created from this Blueprint</h3>
    {configs.loading && <p role="status">Loading CellConfigs…</p>}
    {configs.error && <ErrorNotice error={configs.error} kind="CellConfig" />}
    {configs.data && matches.length > 0 && <ul className="state-list">{matches.map(config => { const scope = [...scopeOf(config.metadata), config.metadata.name]; return <li key={JSON.stringify(scope)}><div><a className="resource-name" href={catalogLink("configs", scope)}>{config.metadata.name}</a><p className="description">{scopeLabel(scope.slice(0, -1))}</p></div></li>; })}</ul>}
    {configs.data && !matches.length && !configs.error && <p className="description">No CellConfigs reference this Blueprint.</p>}
    {matches.length > 0 && <><h3>Cells created through those Configs</h3><RelatedCellList rows={cellRows} inventory={inventory} configLabel /></>}
  </section>;
}
export function CatalogDeleteConfirmation({ kind, doc, revision, inventory, onCancel, onConfirm }: { kind: string; doc: CatalogDoc; revision: number; inventory: InventoryResource; onCancel: () => void; onConfirm: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const scope = [...scopeOf(doc.metadata), doc.metadata.name];
  useModalDialog(dialog);
  return <dialog className="cell-action-dialog catalog-delete-dialog" ref={dialog} aria-labelledby="catalog-delete-title" aria-describedby="catalog-delete-description" onCancel={event => { event.preventDefault(); onCancel(); }} onClick={event => { if (event.target === event.currentTarget) onCancel(); }}>
    <form onSubmit={event => { event.preventDefault(); if (acknowledged) onConfirm(); }}>
      <h2 id="catalog-delete-title">Delete {singular[kind].toLowerCase()}</h2>
      <p id="catalog-delete-description">{deleteConsequence[kind]}</p>
      <dl className="cell-action-target"><dt>Kind</dt><dd>{singular[kind]}</dd><dt>Exact target</dt><dd><code>{scope.join(" / ")}</code></dd></dl>
      <DeleteDependents kind={kind} doc={doc} revision={revision} inventory={inventory} />
      <label className="cell-action-acknowledgment">
        <input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />
        I understand this permanently deletes the {singular[kind].toLowerCase()} {kind === "volumes" ? "and its data" : "document"}.
      </label>
      <div className="cell-action-dialog-buttons">
        <button type="button" onClick={onCancel}>Cancel</button>
        <button className="danger-action" type="submit" disabled={!acknowledged}>Confirm Delete</button>
      </div>
    </form>
  </dialog>;
}
// Asks kukeond whether the document still exists: true, false, or null when the read itself failed.
async function readCatalogPresence(kind: string, scope: string[]): Promise<boolean | null> {
  try {
    const response = await fetch(catalogAPI(kind, scope), { headers: { Accept: "application/json" } });
    if (response.ok) return true;
    const body = await response.json().catch(() => ({}));
    return body.error?.code === "not_found" ? false : null;
  } catch { return null; }
}
function CatalogList({ kind, revision, inventory, onChanged }: { kind: string; revision: number; inventory: InventoryResource; onChanged: () => void }) {
  const list = useResource<CatalogDoc[]>(`/api/catalog/${kind}`, revision);
  // A mount count is shown only from a complete inventory: a partial one would undercount.
  const mountedBy = (doc: CatalogDoc) => kind !== "volumes" || !inventory.data || inventory.data.failures.length || !list.data ? "—" : String(volumeMounts(inventory.data.rows[3], list.data, doc).length);
  const { notify } = useNotify();
  const [confirming, setConfirming] = useState<CatalogDoc | null>(null);
  const [pending, setPending] = useState<Set<string>>(() => new Set());
  const [removed, setRemoved] = useState<Set<string>>(() => new Set());
  const [failure, setFailure] = useState<{ target: string; message: string } | null>(null);
  async function runDelete(scope: string[]) {
    const key = JSON.stringify(scope);
    const target = scope.join(" / ");
    if (pending.has(key)) return;
    setPending(old => new Set(old).add(key));
    setFailure(null);
    notify({ key: `catalog-delete:${kind}:${key}`, tone: "pending", title: target, message: `Deleting ${singular[kind].toLowerCase()}…` });
    let message: string | null = null;
    try {
      const response = await fetch(catalogDeleteAPI(kind, scope), { method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" }, body: "{}" });
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        message = body.error?.message ?? `HTTP ${response.status}`;
      }
    } catch (error) { message = error instanceof Error ? error.message : "Request failed"; }
    // Only a fresh read decides whether the row goes: a refusal or a lost response may still have deleted it.
    const present = await readCatalogPresence(kind, scope);
    if (present === false) {
      setRemoved(old => new Set(old).add(key));
      notify({ key: `catalog-delete:${kind}:${key}`, tone: "success", title: target, message: `${singular[kind]} deleted.` });
    } else {
      const shown = message ?? (present === null ? "Deleted, but the follow-up read failed; refresh to confirm." : "kukeond reported success, but the resource is still listed.");
      setFailure({ target, message: shown });
      notify({ key: `catalog-delete:${kind}:${key}`, tone: message ? "error" : "warning", title: target, message: shown });
    }
    setPending(old => { const next = new Set(old); next.delete(key); return next; });
    onChanged();
  }
  const [query, setQuery] = useState("");
  const [realm, setRealm] = useState("");
  const [page, setPage] = useState(0);
  const rows = (list.data ?? []).filter(doc => !removed.has(JSON.stringify([...scopeOf(doc.metadata), doc.metadata.name]))).filter(doc => (!realm || doc.metadata.realm === realm) && [...scopeOf(doc.metadata), doc.metadata.name].join(" / ").toLowerCase().includes(query.toLowerCase())).sort((a, b) => [...scopeOf(a.metadata), a.metadata.name].join("/").localeCompare([...scopeOf(b.metadata), b.metadata.name].join("/")));
  const currentPage = Math.min(page, Math.max(0, Math.ceil(rows.length / 25) - 1));
  return <>
    {list.error && <ErrorNotice error={list.error} kind={catalogTitles[kind].replace(/s$/, "")} />}
    {list.error && list.data && <p className="stale">Stale {kind} list — refresh failed.</p>}
    {kind === "secrets" && <p className="description">Metadata only. Secret values are never returned by this browser.</p>}
    {failure && <div className="error" role="alert"><strong>Delete failed: {failure.target}</strong><p>{failure.message}</p></div>}
    <section className="panel" aria-label={`${catalogTitles[kind]} list`} aria-busy={list.loading}>
      <div className="list-controls"><label className="search-field">Search<input type="search" value={query} placeholder={`Search ${kind}…`} onChange={e => { setQuery(e.target.value); setPage(0); }} /></label><label>Realm<select aria-label="Filter by realm" value={realm} onChange={e => { setRealm(e.target.value); setPage(0); }}><option value="">All realms</option>{[...new Set(list.data?.map(doc => doc.metadata.realm))].sort().map(name => <option key={name}>{name}</option>)}</select></label><span className="result-count">{rows.length} results</span>{(kind === "blueprints" || kind === "configs") && <a className="document-action" href={newConfigLink()}>New Config</a>}{kind === "secrets" && <a className="document-action" href={newSecretLink()}>New Secret</a>}</div>
      {list.loading && <p className="scope-filter" role="status">Loading {kind}…</p>}
      <div className="table-scroll"><table className="stackable"><thead><tr><th>Name</th><th>Scope</th>{kind === "volumes" ? <><th>Reclaim policy</th><th>Mounted by</th></> : kind === "configs" ? <><th>Schedule</th><th>Next fire</th></> : <th>Type</th>}<th><span className="sr-only">Actions</span></th></tr></thead><tbody>{rows.slice(currentPage * 25, (currentPage + 1) * 25).map(doc => { const scope = [...scopeOf(doc.metadata), doc.metadata.name]; return <tr key={JSON.stringify(scope)}><td data-label="Name"><a className="resource-name" href={catalogLink(kind, scope)}>{doc.metadata.name}</a>{kind === "configs" && <> <NeedsConnectorBadge config={doc} /></>}</td><td className="scope-cell" data-label="Scope"><ScopePath parts={scope.slice(0, -1)} /></td>{kind === "volumes" ? <><td data-label="Reclaim policy">{doc.spec?.reclaimPolicy as string || "Delete"}</td><td data-label="Mounted by" title="Cells in the inventory that mount this Volume">{mountedBy(doc)}</td></> : kind === "configs" ? <><td className="mono-id" data-label="Schedule">{scheduleColumn(doc) || "—"}</td><td className="mono-id" data-label="Next fire">{nextColumn(doc) === "suspended" ? "suspended" : nextColumn(doc) ? <time dateTime={nextColumn(doc)} title={validTime(nextColumn(doc)) ? new Date(nextColumn(doc)).toLocaleString() : undefined}>{nextColumn(doc)}</time> : "—"}</td></> : <td data-label="Type">{doc.kind as string || catalogTitles[kind]}</td>}<td className="row-actions"><RowActions>{kind === "configs" && <RowAction icon="create" href={newCellLink(scope)} label={`New cell from config ${scope.join(" / ")}`} title="New cell from this config" />}{kind === "blueprints" && <RowAction icon="create" href={newConfigLink(scope)} label={`New config from blueprint ${scope.join(" / ")}`} title="New config from this blueprint" />}{kind === "secrets" && !secretConnector(doc) && <RowAction icon="edit" href={catalogPageLink(kind, scope, { edit: true })} label={`Replace value of secret ${scope.join(" / ")}`} title="Replace value…" />}<DocumentRowLink href={catalogLink(kind, scope, true)} label={scope.join(" / ")} /><RowAction icon="delete" danger label={`Delete ${singular[kind].toLowerCase()} ${scope.join(" / ")}`} title="Delete…" disabled={pending.has(JSON.stringify(scope))} busy={pending.has(JSON.stringify(scope))} onClick={() => setConfirming(doc)} /></RowActions></td></tr>; })}</tbody></table></div>
      {!list.loading && !rows.length && <div className="empty-state"><h3>{list.error ? "Resources could not be loaded" : list.data?.length ? "No matching resources" : `No ${kind} found`}</h3></div>}
      <div className="pagination"><span>{rows.length ? `${currentPage * 25 + 1}–${Math.min((currentPage + 1) * 25, rows.length)} of ${rows.length}` : "0 results"}</span><button disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>Previous</button><button disabled={(currentPage + 1) * 25 >= rows.length} onClick={() => setPage(currentPage + 1)}>Next</button></div>
    </section>
    {confirming && <CatalogDeleteConfirmation kind={kind} doc={confirming} revision={revision} inventory={inventory} onCancel={() => setConfirming(null)} onConfirm={() => { const scope = [...scopeOf(confirming.metadata), confirming.metadata.name]; setConfirming(null); void runDelete(scope); }} />}
  </>;
}
// secretConnector names the connector a Secret is an identity of: such a Secret
// is rotated on the connector's page, never replaced from the catalog.
function secretConnector(doc: CatalogDoc): string { return typeof doc.spec?.connector === "string" ? doc.spec.connector : ""; }
// SecretValue is a Secret page's write side: Replace value for an untagged
// Secret, a link to its connector for an identity.
function SecretValue({ doc, scope, edit, onChanged }: { doc: CatalogDoc; scope: string[]; edit: boolean; onChanged: () => void }) {
  const connector = secretConnector(doc);
  const plain = catalogPageLink("secrets", scope);
  if (connector) return <section className="panel secret-value" aria-label="Secret value"><div className="panel-heading"><h2>Value</h2></div><div className="panel-body"><p className="description">This Secret is an identity of connector <a className="resource-name" href={connectorsLink(connector)}><code>{connector}</code></a>. Rotate it on the connector's page, which checks it against the connector; it is not replaced here.</p></div></section>;
  return <section className="panel secret-value" aria-label="Secret value"><div className="panel-heading"><h2>Value</h2>{!edit && <a className="document-action" href={catalogPageLink("secrets", scope, { edit: true })}>Replace value</a>}</div><div className="panel-body">
    {edit ? <SecretForm scope={scope.slice(0, -1)} existing={doc as { metadata: Metadata; spec?: { type?: string } }} onSaved={() => { onChanged(); window.location.hash = plain; }} onCancel={() => { window.location.hash = plain; }} />
      : <p className="description">The value is write-only and never shown. Replace value stores new material under the same name, scope and type.</p>}
  </div></section>;
}
// ConfigSchedule is a scheduled Config's Schedule panel: what spec.schedule defines and what kukeond's
// scheduler reports. The schedule is edited in the Config document, not here.
function ConfigSchedule({ doc, inventory }: { doc: CatalogDoc; inventory: InventoryResource }) {
  const schedule = doc.spec?.schedule as Schedule;
  const status = (doc as { status?: { schedule?: ScheduleStatus } }).status?.schedule;
  return <section className="panel config-schedule" aria-label="Schedule"><div className="panel-heading"><h2>Schedule</h2><span className="badge">From spec.schedule</span></div><div className="panel-body">
    <p className="description">Each fire creates and starts the cell <code>{doc.metadata.name}-&lt;unix time&gt;</code>, which deletes itself when it ends; only this status remains after a run. The schedule is edited in the Config document, not here.</p>
    {!status && <p className="description" role="status">kukeond's scheduler has not visited this Config yet; it fills the status within a minute of the apply.</p>}
    <ScheduleDetails schedule={schedule} status={status} cellLink={cellLinker(inventory.data, doc)} />
  </div></section>;
}
// The catalog kinds majordomo can be asked about, and what each is called in a question.
const askKinds: Record<string, string> = { configs: "CellConfig", blueprints: "CellBlueprint", volumes: "volume" };
function catalogState(kind: string, doc: CatalogDoc): string {
  const spec = (doc.spec ?? {}) as { blueprint?: { name?: string }; schedule?: { cron?: string; suspend?: boolean }; reclaimPolicy?: string };
  const status = (doc as { status?: { schedule?: { lastError?: string } } }).status;
  if (kind === "configs") return [`stored, blueprint ${spec.blueprint?.name ?? "not set"}`, spec.schedule ? `schedule ${spec.schedule.cron}${spec.schedule.suspend ? " (suspended)" : ""}` : "", status?.schedule?.lastError ? `last schedule error: ${status.schedule.lastError}` : ""].filter(Boolean).join(", ");
  if (kind === "volumes") return `stored${spec.reclaimPolicy ? `, reclaim policy ${spec.reclaimPolicy}` : ""}`;
  return "stored";
}
function CatalogDetails({ kind, scope, document, edit = false, revision, inventory, onChanged }: { kind: string; scope: string[]; document: boolean; edit?: boolean; revision: number; inventory: InventoryResource; onChanged: () => void }) {
  const detail = useResource<CatalogDoc>(catalogAPI(kind, scope), revision);
  const doc = detail.data;
  return <>
    {/* The page's actions share one header row; creating from this resource is its primary action, last on the right (#1603). */}
    <div className="detail-actions"><a href={`#/${kind}`}>← Back to {kind}</a><div>{doc && askKinds[kind] && <AskMajordomo question={catalogQuestion(askKinds[kind], scope, catalogState(kind, doc))} subject={`${askKinds[kind]} ${scope.join(" / ")}`} />}<a className="document-action" href={catalogLink(kind, scope, !document)}>{document ? "Show overview" : "View document"}</a>
      {doc && kind === "configs" && !document && <a className="new-cell-action" href={newCellLink(scope)}>Create Cell from this Config <span aria-hidden="true">→</span></a>}
      {doc && kind === "blueprints" && !document && <a className="new-cell-action" href={newConfigLink(scope)}>Create Config from this Blueprint <span aria-hidden="true">→</span></a>}</div></div>
    {detail.loading && <p role="status">Loading {kind} document…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={catalogTitles[kind].replace(/s$/, "")} />}
    {detail.error && doc && <p className="stale">Stale {kind} document — refresh failed.</p>}
    {kind === "secrets" && <p className="description">Secret values are redacted. Only daemon-exposed metadata is available.</p>}
    {doc && <section className="panel"><div className="panel-heading"><h2>{document ? "API document" : "Resource overview"}</h2>{kind !== "secrets" && <span className="badge" title={`Edits are not supported here. Delete ${kind} from the list.`}>No edits — delete from list</span>}</div>{document ? <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(doc, null, 2)}</pre> : <div className="panel-body"><dl className="details-grid"><dt>Name</dt><dd>{doc.metadata.name}</dd><dt>Scope</dt><dd>{scopeOf(doc.metadata).join(" / ")}</dd><dt>Kind</dt><dd>{doc.kind as string}</dd>{kind === "secrets" && <><dt>Type</dt><dd>{secretTypeOf(doc)}</dd>{secretConnector(doc) && <><dt>Identity of connector</dt><dd><a className="resource-name" href={connectorsLink(secretConnector(doc))}><code>{secretConnector(doc)}</code></a></dd></>}</>}{kind !== "volumes" && <><dt>Labels</dt><dd>{Object.entries(doc.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(doc.metadata.labels ?? {}).length && "No labels"}</dd></>}{kind === "configs" && teamOf(doc) && <><dt>Team</dt><dd><a className="resource-name" href={teamsLink(teamOf(doc))}>{teamOf(doc)}</a> <span className="description">Rendered by the team; edit it on the Teams page.</span></dd></>}{kind === "configs" && !doc.spec?.schedule && <><dt>Schedule</dt><dd>None</dd></>}{kind === "volumes" && <><dt>Reclaim policy</dt><dd>{doc.spec?.reclaimPolicy as string || "Default (Delete)"}</dd></>}</dl><p className="description">Use View document for every API-exposed field.</p></div>}</section>}
    {doc && kind === "secrets" && !document && <SecretValue doc={doc} scope={scope} edit={edit} onChanged={onChanged} />}
    {doc && kind === "configs" && !document && !!doc.spec?.schedule && <ConfigSchedule doc={doc} inventory={inventory} />}
    {doc && kind === "configs" && !document && <ConfigConnectors doc={doc} scope={scope} revision={revision} onChanged={onChanged} />}
    {doc && !document && <RelatedResources doc={doc} revision={revision} />}
    {doc && kind === "configs" && !document && <ConfigRelationships doc={doc} revision={revision} inventory={inventory} />}
    {doc && kind === "blueprints" && !document && <BlueprintRelationships doc={doc} revision={revision} inventory={inventory} />}
    {doc && kind === "volumes" && !document && <VolumeRelationships doc={doc} revision={revision} inventory={inventory} />}
  </>;
}
export function CatalogPage(props: { kind: string; scope: string[]; document: boolean; edit?: boolean; revision: number; inventory: InventoryResource; onChanged: () => void }) {
  return props.scope.length ? <CatalogDetails key={props.kind + JSON.stringify(props.scope)} {...props} /> : <CatalogList key={props.kind} kind={props.kind} revision={props.revision} inventory={props.inventory} onChanged={props.onChanged} />;
}
