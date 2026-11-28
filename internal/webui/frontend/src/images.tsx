import { useResource, ErrorNotice } from "./request";
import { imagesLink } from "./routes";

type RealmImage = { name: string; ref: string; digest?: string; built?: boolean };

// Lists the realm's spec.images with the reference and digest the daemon resolves (#1418).
// The Realm's registry credentials are managed on the Images page; this panel counts them and links there.
export function RealmImagesPanel({ realm, revision, credentials }: { realm: string; revision: number; credentials: number }) {
  const images = useResource<RealmImage[]>(`/realms/${encodeURIComponent(realm)}/images`, revision);
  return <section className="panel realm-images" aria-label="Realm images">
    <div className="panel-heading"><h2>Images</h2><span className="badge">{images.data ? `${images.data.length} declared` : "Realm images"}</span></div>
    <div className="panel-body">
      {images.loading && !images.data && <p>Loading images…</p>}
      {images.error && <ErrorNotice error={images.error} />}
      {!images.loading && !images.error && !images.data?.length && <p>This realm declares no images. Add <code>spec.images</code> to the realm to name images Configs can use.</p>}
      <p className="realm-registry-link">Registry credentials: {credentials} configured — <a href={imagesLink(realm)}>manage in Images</a></p>
      {!!images.data?.length && <table className="data-table"><thead><tr><th scope="col">Name</th><th scope="col">Reference</th><th scope="col">Source</th><th scope="col">Digest</th></tr></thead><tbody>
        {images.data.map(image => <tr key={image.name}><td>{image.name}</td><td><code>{image.ref}</code></td><td>{image.built ? "Dockerfile" : "Reference"}</td><td>{image.digest ? <code title={image.digest}>{image.digest.slice(0, 19)}…</code> : <span className="description">{image.built ? "Not built yet" : "Not pulled yet"}</span>}</td></tr>)}
      </tbody></table>}
    </div>
  </section>;
}
