import { CreateCell } from "./create";
import { useState } from "react";
import { ErrorNotice, useResource } from "./request";

export const catalogTitles: Record<string, string> = { blueprints: "Blueprints", configs: "Configs", volumes: "Volumes", secrets: "Secrets" };
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
function CatalogDetails({ kind, scope, document, revision }: { kind: string; scope: string[]; document: boolean; revision: number }) {
  const detail = useResource<CatalogDoc>(catalogAPI(kind, scope), revision);
  const doc = detail.data;
  return <>
    <div className="detail-actions"><a href={`#/${kind}`}>← Back to {kind}</a><a className="document-action" href={catalogLink(kind, scope, !document)}>{document ? "Show overview" : "View document"}</a></div>
    {detail.loading && <p role="status">Loading {kind} document…</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={catalogTitles[kind].replace(/s$/, "")} />}
    {detail.error && doc && <p className="stale">Stale {kind} document — refresh failed.</p>}
    {kind === "secrets" && <p className="description">Secret values are redacted. Only daemon-exposed metadata is available.</p>}
    {doc && <section className="panel"><div className="panel-heading"><h2>{document ? "API document" : "Resource overview"}</h2><span className="badge">Read only</span></div>{document ? <pre tabIndex={0} aria-label={`Complete ${kind} JSON`}>{JSON.stringify(doc, null, 2)}</pre> : <div className="panel-body"><dl className="details-grid"><dt>Name</dt><dd>{doc.metadata.name}</dd><dt>Scope</dt><dd>{scopeOf(doc.metadata).join(" / ")}</dd><dt>Kind</dt><dd>{doc.kind as string}</dd><dt>Labels</dt><dd>{Object.entries(doc.metadata.labels ?? {}).map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}{!Object.keys(doc.metadata.labels ?? {}).length && "No labels"}</dd>{kind === "volumes" && <><dt>Reclaim policy</dt><dd>{doc.spec?.reclaimPolicy as string || "Default (Delete)"}</dd></>}</dl><p className="description">Use View document for every API-exposed field.</p></div>}</section>}
    {doc && kind === "configs" && !document && <CreateCell config={doc.metadata} />}
    {doc && !document && <RelatedResources doc={doc} revision={revision} />}
  </>;
}
export function CatalogPage(props: { kind: string; scope: string[]; document: boolean; revision: number }) {
  return props.scope.length ? <CatalogDetails key={props.kind + JSON.stringify(props.scope)} {...props} /> : <CatalogList key={props.kind} kind={props.kind} revision={props.revision} />;
}
