import { useState } from "react";
import { ErrorNotice, useResource } from "./request";
import { imagesLink } from "./routes";
import { UNAVAILABLE, formatImageCreated, formatImageSize, formatImageText, shortDigest, sortedLabels } from "./image-format.js";

// InventoryImage mirrors the kuke serve image-inventory response (#1406).
// size and createdAt are null when containerd cannot resolve them.
export type InventoryImage = { name: string; size: number | null; createdAt: string | null; digest: string; mediaType: string; labels: Record<string, string> };
type ImageList = { realm: string; namespace: string; images: InventoryImage[] };
type ImageDetail = { realm: string; namespace: string; image: InventoryImage };
type RealmDoc = { metadata: { name: string } };

const pageSize = 25;
function inventoryAPI(realm: string, ref?: string): string {
  return `/api/realms/${encodeURIComponent(realm)}/image-inventory` + (ref ? "/" + encodeURIComponent(ref) : "");
}
function Labels({ labels }: { labels: Record<string, string> }) {
  const entries = sortedLabels(labels);
  return entries.length ? <>{entries.map(([key, value]) => <span className="badge label-badge" key={key}>{key}: {value}</span>)}</> : <>No labels</>;
}

function RealmPicker({ realm, revision }: { realm: string; revision: number }) {
  const realms = useResource<RealmDoc[]>("/api/realms", revision);
  const names = [...new Set([...(realms.data ?? []).map(doc => doc.metadata.name), realm].filter(Boolean))].sort();
  return <label>Realm<select aria-label="Realm" value={realm} disabled={!names.length} onChange={e => { window.location.hash = imagesLink(e.target.value); }}>
    {!names.length && <option value="">{realms.loading ? "Loading realms…" : "No realms"}</option>}
    {names.map(name => <option key={name} value={name}>{name}</option>)}
  </select></label>;
}

function ImageListView({ realm, revision }: { realm: string; revision: number }) {
  const list = useResource<ImageList>(inventoryAPI(realm), revision);
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const images = list.data?.images ?? [];
  const rows = images.filter(image => image.name.toLowerCase().includes(query.toLowerCase())).sort((a, b) => a.name.localeCompare(b.name));
  const currentPage = Math.min(page, Math.max(0, Math.ceil(rows.length / pageSize) - 1));
  return <>
    {list.error && <ErrorNotice error={list.error} />}
    {list.error && list.data && <p className="stale">Stale image list — refresh failed.</p>}
    <section className="panel" aria-label="Images list" aria-busy={list.loading}>
      <div className="list-controls">
        <RealmPicker realm={realm} revision={revision} />
        <label className="search-field">Search<input type="search" value={query} placeholder="Search image references…" onChange={e => { setQuery(e.target.value); setPage(0); }} /></label>
        <span className="result-count">{rows.length} results</span>
      </div>
      {list.data && <p className="scope-filter description">Containerd namespace <code>{list.data.namespace}</code></p>}
      {list.loading && <p className="scope-filter" role="status">{list.data ? "Refreshing images…" : "Loading images…"}</p>}
      {rows.length > 0 && <div className="table-scroll"><table><thead><tr><th scope="col">Reference</th><th scope="col">Size</th><th scope="col">Created</th><th scope="col">Digest</th><th scope="col">Media type</th><th scope="col">Labels</th></tr></thead><tbody>
        {rows.slice(currentPage * pageSize, (currentPage + 1) * pageSize).map(image => <tr key={image.name}>
          <td><a className="resource-name" href={imagesLink(realm, image.name)}>{image.name}</a></td>
          <td>{formatImageSize(image.size)}</td>
          <td>{formatImageCreated(image.createdAt)}</td>
          <td>{image.digest ? <code title={image.digest}>{shortDigest(image.digest)}</code> : UNAVAILABLE}</td>
          <td>{formatImageText(image.mediaType)}</td>
          <td>{sortedLabels(image.labels).length || "None"}</td>
        </tr>)}
      </tbody></table></div>}
      {!list.loading && !rows.length && <div className="empty-state"><h3>{list.error ? "Images could not be loaded" : images.length ? "No matching images" : "No images found"}</h3><p>{list.error ? "Refresh to retry." : images.length ? "Try another search." : `No images are installed in realm ${realm}'s containerd namespace.`}</p></div>}
      <div className="pagination"><span>{rows.length ? `${currentPage * pageSize + 1}–${Math.min((currentPage + 1) * pageSize, rows.length)} of ${rows.length}` : "0 results"}</span><button disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>Previous</button><button disabled={(currentPage + 1) * pageSize >= rows.length} onClick={() => setPage(currentPage + 1)}>Next</button></div>
    </section>
  </>;
}

function ImageDetailView({ realm, reference, revision }: { realm: string; reference: string; revision: number }) {
  const detail = useResource<ImageDetail>(inventoryAPI(realm, reference), revision);
  const image = detail.data?.image;
  return <>
    <div className="detail-actions"><a href={imagesLink(realm)}>← Back to images in {realm}</a></div>
    {detail.loading && <p role="status">{image ? "Refreshing image…" : "Loading image…"}</p>}
    {detail.error && <ErrorNotice error={detail.error} kind={/realm not found/i.test(detail.error.message) ? "Realm" : "Image"} />}
    {detail.error && image && <p className="stale">Stale image details — refresh failed.</p>}
    {image && <section className="panel" aria-label="Image details"><div className="panel-heading"><h2>Image</h2><span className="badge">Read only</span></div><div className="panel-body"><dl className="details-grid">
      <dt>Reference</dt><dd><code>{image.name}</code></dd>
      <dt>Realm</dt><dd>{detail.data?.realm}</dd>
      <dt>Namespace</dt><dd><code>{detail.data?.namespace}</code></dd>
      <dt>Size</dt><dd>{formatImageSize(image.size)}</dd>
      <dt>Created</dt><dd>{formatImageCreated(image.createdAt)}</dd>
      <dt>Digest</dt><dd>{image.digest ? <code>{image.digest}</code> : UNAVAILABLE}</dd>
      <dt>Media type</dt><dd>{formatImageText(image.mediaType)}</dd>
      <dt>Labels</dt><dd><Labels labels={image.labels} /></dd>
    </dl></div></section>}
  </>;
}

// ImagesPage browses a realm's containerd image inventory through kukeond
// (#1406). Read-only: loading, deleting, pruning and building stay on the CLI.
export function ImagesPage({ scope, revision }: { scope: string[]; revision: number }) {
  const realms = useResource<RealmDoc[]>(scope[0] ? null : "/api/realms", revision);
  const names = (realms.data ?? []).map(doc => doc.metadata.name);
  const realm = scope[0] || (names.includes("default") ? "default" : names.sort()[0] ?? "");
  if (!realm) {
    if (realms.loading) return <p role="status">Loading realms…</p>;
    if (realms.error) return <ErrorNotice error={realms.error} />;
    return <div className="empty-state"><h3>No realms found</h3><p>Run <code>kuke init</code> to create the default realms.</p></div>;
  }
  return scope[1] ? <ImageDetailView key={realm + "\u0000" + scope[1]} realm={realm} reference={scope[1]} revision={revision} /> : <ImageListView key={realm} realm={realm} revision={revision} />;
}
