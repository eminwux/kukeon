import { useState } from "react";
import { ErrorNotice, useResource } from "./request";
import { catalogTitles, newCellLink, workspaceLink } from "./routes";
import { StateBadge } from "./status";
import type { InventoryResource, Row } from "./inventory";
import { cellsForBlueprint, cellsForConfig, configsForBlueprint, type ConfigDoc } from "./relationships.mjs";

type Metadata = { name: string; realm?: string; space?: string; stack?: string; cell?: string; labels?: Record<string, string>; annotations?: Record<string, string> };
type CatalogDoc = { metadata: Metadata; spec?: Record<string, unknown>; [key: string]: unknown };
type Reference = { kind: string; name: string; realm: string; space?: string; stack?: string; cell?: string };
const coordinates = ["realm", "space", "stack", "cell"] as const;
function scopeOf(metadata: Metadata): string[] { return coordinates.map(key => metadata[key]).filter((value): value is string => !!value); }
function catalogLink(kind: string, scope: string[], document = false): string { return `#/${kind}/` + scope.map(encodeURIComponent).join("/") + (document ? "?document=1" : ""); }
function catalogAPI(kind: string, scope: string[]): string {
  const params = new URLSearchParams();
  scope.slice(0, -1).forEach((value, i) => params.set(coordinates[i], value));
  return `/api/catalog/${kind}/${encodeURIComponent(scope.at(-1) ?? "")}?${params}`;
}
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
function RelatedCellList({ rows, inventory, configLabel }: { rows: Array<Row & { config?: RelatedCatalogDoc }>; inventory: InventoryResource; configLabel?: boolean }) {
  return <>
    {inventory.loading && !inventory.data && <p role="status">Loading shared Cell inventory…</p>}
    {inventory.loading && inventory.data && <p role="status">Refreshing Cell inventory; displayed Cells come from the last completed retrieval.</p>}
    {inventory.error && <p className="stale">Cell inventory could not be refreshed: {inventory.error.message}</p>}
    {!!inventory.data?.failures.length && <p className="stale">Cell inventory is incomplete; this relationship list may be partial.</p>}
    {!inventory.data && !inventory.loading && !inventory.error && <p className="empty-state">Cell inventory has not been retrieved.</p>}
    {rows.length > 0 && <ul className="state-list related-cell-list">{rows.map(row => {
      const scope = cellScope(row);
      return <li key={JSON.stringify(scope)}><div><a className="resource-name" href={workspaceLink(scope)}>{row.doc.metadata.name}</a><p className="description">{scopeLabel(scope.slice(0, -1))}</p>{configLabel && row.config && <p className="description">Config: <a href={relationResourceLink("configs", [...scopeOf(row.config.metadata), row.config.metadata.name])}>{row.config.metadata.name}</a> · {scopeLabel(scopeOf(row.config.metadata))}</p>}</div><StateBadge state={row.doc.status?.state} /><a className="document-action" href={workspaceLink(scope)}>Open Workspace</a></li>;
    })}</ul>}
    {inventory.data && !rows.length && !inventory.loading && !inventory.error && !inventory.data.failures.length && <p className="description">No related Cells found.</p>}
  </>;
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
function CatalogList({ kind, revision }: { kind: string; revision: number }) {
  const list = useResource<CatalogDoc[]>(`/api/catalog/${kind}`, revision);
  const [query, setQuery] = useState("");
  const [realm, setRealm] = useState("");
  const [page, setPage] = useState(0);
  const rows = (list.data ?? []).filter(doc => (!realm || doc.metadata.realm === realm) && [...scopeOf(doc.metadata), doc.metadata.name].join(" / ").toLowerCase().includes(query.toLowerCase())).sort((a, b) => [...scopeOf(a.metadata), a.metadata.name].join("/").localeCompare([...scopeOf(b.metadata), b.metadata.name].join("/")));
  const currentPage = Math.min(page, Math.max(0, Math.ceil(rows.length / 25) - 1));
  return <>
    {list.error && <ErrorNotice error={list.error} kind={catalogTitles[kind].replace(/s$/, "")} />}
    {list.error && list.data && <p className="stale">Stale {kind} list — refresh failed.</p>}
    {kind === "secrets" && <p className="description">Metadata only. Secret values are never returned by this browser.</p>}
    <section className="panel" aria-label={`${catalogTitles[kind]} list`} aria-busy={list.loading}>
      <div className="list-controls"><label className="search-field">Search<input type="search" value={query} placeholder={`Search ${kind}…`} onChange={e => { setQuery(e.target.value); setPage(0); }} /></label><label>Realm<select aria-label="Filter by realm" value={realm} onChange={e => { setRealm(e.target.value); setPage(0); }}><option value="">All realms</option>{[...new Set(list.data?.map(doc => doc.metadata.realm))].sort().map(name => <option key={name}>{name}</option>)}</select></label><span className="result-count">{rows.length} results</span></div>
      {list.loading && <p className="scope-filter" role="status">Loading {kind}…</p>}
      <div className="table-scroll"><table><thead><tr><th>Name</th><th>Scope</th><th>Type</th><th>Document</th></tr></thead><tbody>{rows.slice(currentPage * 25, (currentPage + 1) * 25).map(doc => { const scope = [...scopeOf(doc.metadata), doc.metadata.name]; return <tr key={JSON.stringify(scope)}><td><a className="resource-name" href={catalogLink(kind, scope)}>{doc.metadata.name}</a></td><td className="scope-cell">{scope.slice(0, -1).join(" / ")}</td><td>{doc.kind as string || catalogTitles[kind]}</td><td><a className="document-action" href={catalogLink(kind, scope, true)}>View document</a></td></tr>; })}</tbody></table></div>
      {!list.loading && !rows.length && <div className="empty-state"><h3>{list.error ? "Resources could not be loaded" : list.data?.length ? "No matching resources" : `No ${kind} found`}</h3></div>}
      <div className="pagination"><span>{rows.length ? `${currentPage * 25 + 1}–${Math.min((currentPage + 1) * 25, rows.length)} of ${rows.length}` : "0 results"}</span><button disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>Previous</button><button disabled={(currentPage + 1) * 25 >= rows.length} onClick={() => setPage(currentPage + 1)}>Next</button></div>
    </section>
  </>;
}
function CatalogDetails({ kind, scope, document, revision, inventory }: { kind: string; scope: string[]; document: boolean; revision: number; inventory: InventoryResource }) {
  const detail = useResource<CatalogDoc>(catalogAPI(kind, scope), revision);
  const doc = detail.data;
  return <>
    <div className="detail-actions"><a href={`#/${kind}`}>← Back to {kind}</a><a className="document-action" href={catalogLink(kind, scope, !document)}>{document ? "Show overview" : "View document"}</a></div>
    {detail.loading && <p role="status">Loading {kind} document…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={catalogTitles[kind].replace(/s$/, "")} />}
    {detail.error && doc && <p className="stale">Stale {kind} document — refresh failed.</p>}
    {kind === "secrets" && <p className="description">Secret values are redacted. Only daemon-exposed metadata is available.</p>}
    {doc && <section className="panel"><div className="panel-heading"><h2>{document ? "API document" : "Resource overview"}</h2><span className="badge">Read only</span></div>{document ? <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(doc, null, 2)}</pre> : <div className="panel-body"><dl className="details-grid"><dt>Name</dt><dd>{doc.metadata.name}</dd><dt>Scope</dt><dd>{scopeOf(doc.metadata).join(" / ")}</dd><dt>Kind</dt><dd>{doc.kind as string}</dd><dt>Labels</dt><dd>{Object.entries(doc.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(doc.metadata.labels ?? {}).length && "No labels"}</dd>{kind === "volumes" && <><dt>Reclaim policy</dt><dd>{doc.spec?.reclaimPolicy as string || "Default (Delete)"}</dd></>}</dl><p className="description">Use View document for every API-exposed field.</p></div>}</section>}
    {doc && kind === "configs" && !document && <a className="new-cell-action config-create-action" href={newCellLink(scope)}>Create Cell from this Config <span aria-hidden="true">→</span></a>}
    {doc && !document && <RelatedResources doc={doc} revision={revision} />}
    {doc && kind === "configs" && !document && <ConfigRelationships doc={doc} revision={revision} inventory={inventory} />}
    {doc && kind === "blueprints" && !document && <BlueprintRelationships doc={doc} revision={revision} inventory={inventory} />}
  </>;
}
export function CatalogPage(props: { kind: string; scope: string[]; document: boolean; revision: number; inventory: InventoryResource }) {
  return props.scope.length ? <CatalogDetails key={props.kind + JSON.stringify(props.scope)} {...props} /> : <CatalogList key={props.kind} kind={props.kind} revision={props.revision} />;
}
