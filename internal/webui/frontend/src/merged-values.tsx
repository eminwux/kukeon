import type { ReactNode } from "react";
import { groupGitValues, groupValuesBySource, type RenderedValue } from "./config-form.mjs";
import { StatusIdentity } from "./connector-ui";
import { connectorTitle, originLabel, refScope } from "./connectors.mjs";
import type { ConnectorStatus } from "./connectors.mjs";

export type { RenderedValue };

// Shows the git identity and credential each repository of a rendered cell
// ends up with, and the level that decided each field (#1472).
export function SourceIdentities({ values }: { values: RenderedValue[] }) {
  const { repos, defaults } = groupGitValues(values);
  if (!repos.length && !defaults.length) return null;
  return <div className="merged-values source-identities">{defaults.length > 0 && <section className="source-identity" aria-label="Default git identity">
      <h4>Default git identity</h4>
      <p className="description">What the Realm, Space, Stack and Config merge to. Every repository uses it unless it states its own; a credential applies to the repositories on its host.</p>
      <table aria-label="Default git identity"><thead><tr><th scope="col">Field</th><th scope="col">Value</th><th scope="col">Decided by</th></tr></thead><tbody>
        {defaults.map(field => <tr key={`${field.field}:${field.host}`}><td>{field.field}{field.host && <> for <code>{field.host}</code></>}</td><td><code>{field.value}</code></td><td><span className="badge">{field.origin}</span></td></tr>)}
      </tbody></table>
    </section>}
    {repos.length > 0 && <h4>Git identity per repository</h4>}
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
// (#1418). Git values are shown by SourceIdentities instead: the default
// identity first, then each repository.
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

// What each source of a preview is called, and the source it names (#1603).
const sourceHeadings: Record<string, string> = {
  blueprint: "From the Blueprint", config: "From the Config", connectors: "Connectors", realm: "Realm defaults",
  space: "Space defaults", stack: "Stack defaults", cell: "Set for this cell", other: "Other",
};
export type PreviewSources = { blueprint?: ReactNode; config?: ReactNode; realm?: string; space?: string; stack?: string };

// SourceGroups lists what each source of a Config render contributes (#1603): the Blueprint, the Config,
// the connectors, the Realm, Space and Stack defaults and this cell's own picks, in that order. Scalar
// values stay hidden in the browser, as in MergedValues.
export function SourceGroups({ values, connectors, sources = {}, team = "" }: { values?: RenderedValue[]; connectors?: ConnectorStatus[]; sources?: PreviewSources; team?: string }) {
  const groups = groupValuesBySource(values, connectors);
  if (!groups.length) return <p className="description">No values, repositories, secrets or images were merged.</p>;
  const named: Record<string, ReactNode> = { blueprint: sources.blueprint, config: sources.config, realm: sources.realm, space: sources.space, stack: sources.stack };
  return <div className="merged-values source-groups" role="group" aria-label="Preview sources">{groups.map(group => {
    const heading = sourceHeadings[group.source];
    // A group, not a region: the form's Connectors section is the page's one "Connectors" region.
    return <div role="group" key={group.source} aria-label={heading}><h4>{heading}{named[group.source] && <> <span className="description">{named[group.source]}</span></>}</h4>
      {group.source === "connectors" ? <table aria-label={heading}><thead><tr><th scope="col">Connector</th><th scope="col">Method</th><th scope="col">Identity</th><th scope="col">Decided by</th></tr></thead><tbody>
        {(group.rows as ConnectorStatus[]).map(status => <tr key={`${status.connector}:${status.component}`}><td>{connectorTitle(status)}</td><td>{status.method || <span className="description">—</span>}</td><td>{status.identity ? <><code>{status.identity.name}</code> <span className="description">{refScope(status.identity).join(" / ")}</span></> : <StatusIdentity status={status} team={team} />}</td><td>{status.origin ? <span className="badge">{originLabel(status.origin, team)}</span> : <span className="description">—</span>}</td></tr>)}
      </tbody></table> : <table aria-label={heading}><thead><tr><th scope="col">Kind</th><th scope="col">Key</th><th scope="col">Value</th></tr></thead><tbody>
        {(group.rows as RenderedValue[]).map(value => <tr key={`${value.kind}:${value.key}`}><td>{value.kind}</td><td><code>{value.key}</code></td><td>{value.value ? <code>{value.value}</code> : <span className="description">hidden</span>}</td></tr>)}
      </tbody></table>}
    </div>;
  })}</div>;
}
