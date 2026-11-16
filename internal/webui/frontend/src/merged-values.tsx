import { groupGitValues, type RenderedValue } from "./config-form.mjs";

export type { RenderedValue };

// Shows the git identity and credential each repository of a rendered cell
// ends up with, and the level that decided each field (#1472).
export function SourceIdentities({ values }: { values: RenderedValue[] }) {
  const { repos } = groupGitValues(values);
  if (!repos.length) return null;
  return <div className="merged-values source-identities"><h4>Git identity per repository</h4>
    <p className="description">Each repository clones, pushes and signs with the identity below. A field the source does not state itself falls back to the default inherited from the Realm, Space, Stack or Config; a repository with no credential uses anonymous access. Only stored secrets are named — no key material is shown.</p>
    {repos.map(repo => <section className="source-identity" key={repo.repo} aria-label={`Git identity for ${repo.repo}`}>
      <h5><code>{repo.repo}</code>{repo.url && <span className="description"> {repo.url}</span>}</h5>
      {!repo.fields.length ? <p className="description">No identity or credential: commits use the image's git configuration and the clone is anonymous.</p> : <table aria-label={`Git identity for ${repo.repo}`}><thead><tr><th scope="col">Field</th><th scope="col">Value</th><th scope="col">Decided by</th></tr></thead><tbody>
        {repo.fields.map(field => <tr key={field.field}><td>{field.field}</td><td><code>{field.value}</code></td><td><span className="badge">{field.origin}</span></td></tr>)}
      </tbody></table>}
    </section>)}
  </div>;
}

// Shows each merged input of the daemon's render with the level that set it
// (#1418). Git values are shown per repository by SourceIdentities instead.
export function MergedValues({ values, digest }: { values: RenderedValue[]; digest?: string }) {
  const { rest } = groupGitValues(values);
  return <><SourceIdentities values={values} /><div className="merged-values"><h4>Merged values</h4>
    <p className="description">Realm, Space and Stack defaults merge under the Config, deeper levels winning per key. Scalar values are hidden in the browser; use <code>kuke create cell --from-config … --dry-run --explain</code> to see them.</p>
    {!rest.length ? <p>No values, repositories, secrets or images were merged.</p> : <table aria-label="Merged values and their origin"><thead><tr><th scope="col">Kind</th><th scope="col">Key</th><th scope="col">Value</th><th scope="col">Origin</th></tr></thead><tbody>
      {rest.map(value => <tr key={`${value.kind}:${value.key}`}><td>{value.kind}</td><td><code>{value.key}</code></td><td>{value.value ? <code>{value.value}</code> : <span className="description">hidden</span>}</td><td><span className="badge">{value.origin}</span></td></tr>)}
    </tbody></table>}
    {digest && <p className="description">Render digest: <code>{digest}</code></p>}
  </div></>;
}
