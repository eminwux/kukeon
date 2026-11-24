import { useResource } from "./request";
import { gitIdentities } from "./team-form.mjs";
import { originLabel, pairRows, planSections, refKinds } from "./teams.mjs";
import type { Plan, TeamOutcome } from "./teams.mjs";

// Pieces the team pages share (#1644): a plan as the CLI prints it, the
// result of an apply, a ref field and the git identity select.

export function PlanView({ plan, title }: { plan: Plan; title: string }) {
  const rows = pairRows(plan);
  return <section className="panel team-plan" aria-label={title}>
    <div className="panel-heading"><h2>{title}</h2><span className="badge">{originLabel(plan.origin)}</span></div>
    <div className="panel-body">
      {!!plan.sources?.length && <><h3>Sources</h3><ul className="state-list">{plan.sources.map(source => <li key={source.name}><code>{source.name}</code> {source.url} <span className="description">{source.ref}{source.commit ? ` at ${source.commit.slice(0, 12)}` : ""}</span></li>)}</ul></>}
      <h3>Role × harness</h3>
      {rows.length ? <div className="table-scroll"><table aria-label="Role and harness pairs"><thead><tr><th>Role</th><th>Harness</th><th>Config</th><th>Image</th><th>Tasks</th><th>Connectors and components</th></tr></thead><tbody>
        {rows.map(row => <tr key={row.key}><td>{row.role}</td><td>{row.harness}</td><td className="mono-id">{row.config}</td><td className="mono-id">{row.image}</td><td>{row.tasks.join(", ") || "—"}</td><td>{row.components.join(", ") || "—"}</td></tr>)}
      </tbody></table></div> : <p className="description">No role × harness pair.</p>}
      {planSections(plan).map(section => <div key={section.key}><h3>{section.title}</h3><ul className="state-list" aria-label={section.title}>{section.items.map(item => <li key={item} className="mono-id">{item}</li>)}</ul></div>)}
      {plan.declaration && <details className="health-body"><summary>Declaration</summary><pre tabIndex={0} aria-label="Composed declaration">{plan.declaration}</pre></details>}
    </div>
  </section>;
}

export function OutcomeNotice({ outcome }: { outcome: TeamOutcome }) {
  const result = outcome.result;
  if (!result) return null;
  return <div className="health-sweep-started" role="status">
    <p>Team <code>{result.team}</code> applied: {result.resources} resources{result.pruned.length ? `; pruned ${result.pruned.join(", ")}` : ""}.</p>
    {!!result.missingSecrets.length && <p>Missing secrets: {result.missingSecrets.join(", ")}.</p>}
    {!!result.notes.length && <ul className="description">{result.notes.map(note => <li key={note}>{note}</li>)}</ul>}
  </div>;
}

export function RefFields({ kind, value, disabled, label = "Ref", allowNone = false, onChange }: { kind: string; value: string; disabled: boolean; label?: string; allowNone?: boolean; onChange: (kind: string, value: string) => void }) {
  const kinds = allowNone ? ["", ...refKinds] : refKinds;
  return <label className="schema-field"><span>{label}</span>
    <span className="team-ref">
      <select value={kind} disabled={disabled} aria-label={`${label} kind`} onChange={event => onChange(event.target.value, event.target.value ? value : "")}>{kinds.map(item => <option key={item} value={item}>{item || "the Source's"}</option>)}</select>
      <input value={value} disabled={disabled || !kind} placeholder={!kind ? "the stored Source's ref" : kind === "branch" ? "default branch" : kind === "tag" ? "v1.2.3" : "full commit SHA"} spellCheck={false} autoComplete="off" aria-label={label} onChange={event => onChange(kind, event.target.value)} />
    </span>
  </label>;
}

// GitIdentitySelect lists the stored git identities by name (names only: no
// route returns secret data), plus "Default", the account's or the realm's
// git default for the host.
export function GitIdentitySelect({ value, disabled, label = "Git identity", onChange }: { value: string; disabled: boolean; label?: string; onChange: (value: string) => void }) {
  const secrets = useResource<unknown[]>("/api/catalog/secrets", 0);
  const connectors = useResource<unknown[]>("/api/connectors", 0);
  const names = gitIdentities(secrets.data, connectors.data);
  const choices = value && !names.includes(value) ? [value, ...names] : names;
  return <label className="schema-field"><span>{label}</span>
    <select value={value} disabled={disabled} onChange={event => onChange(event.target.value)}>
      <option value="">Default</option>
      {choices.map(name => <option key={name} value={name}>{name}</option>)}
    </select>
    <small>A stored git identity of realm default fetches the repository; Default uses the realm's git default for the host, else the account running <code>kuke serve</code>.</small>
  </label>;
}
