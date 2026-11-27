import { useEffect, useMemo, useRef, useState } from "react";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import { useModalDialog } from "./focus";
import { connectorsLink, sendJSON, toFailure } from "./connector-ui";
import { stateResolved } from "./connectors.mjs";
import { ScheduleDetails, when } from "./schedule";
import { OutcomeNotice, PlanView } from "./team-plan";
import { TeamWizard } from "./team-wizard";
import { InitializePrompt } from "./initialize";
import { configLink, deleteSummary, exportAPI, listOrigin, newTeamLink, sourcesLink, systemTeam, taskLabel, teamsAPI, teamsLink } from "./teams.mjs";
import type { DeleteResult, RoleConnectorView, TeamDetail, TeamOutcome, TeamSummary, TeamView, ViewSource } from "./teams.mjs";

// The Teams pages (#1618, #1644). The list opens the New team wizard; a
// team's page shows the whole setup as it composes, with Edit, Export,
// Reapply and Delete. Every action posts to the teams API, which runs the
// same code as `kuke team`: the server fetches, composes and applies, as the
// account running `kuke serve`.

// RoleConnectors is what each Config of the team resolves per connector (#1649, AC8), the rows of kuke team
// connector list: the method, the identity and the level it comes from, and whether the team's role entry or
// the role's definition asks for it.
function RoleConnectors({ rows, configs }: { rows: RoleConnectorView[]; configs: TeamDetail["configs"] }) {
  if (!rows.length) return <p className="description">No Config of the team reports its connectors yet.</p>;
  const scopeOf = (name: string) => configs.find(config => config.name === name)?.scope;
  return <div className="table-scroll"><table aria-label="Connectors per role"><thead><tr><th>Role</th><th>Config</th><th>Connector</th><th>Method</th><th>Identity</th><th>From</th><th>Asked by</th></tr></thead><tbody>
    {rows.map(row => { const scope = scopeOf(row.config); const unresolved = row.state !== stateResolved; return <tr key={`${row.config}/${row.connector}`}>
      <td><code>{row.role}</code></td>
      <td>{scope ? <a className="resource-name mono-id" href={configLink(scope, row.config)}>{row.config}</a> : <span className="mono-id">{row.config}</span>}</td>
      <td><a className="resource-name" href={connectorsLink(row.connector)}>{row.connector}</a></td>
      <td>{row.method || "—"}</td>
      <td>{unresolved ? <a className="badge needs-connector" href={connectorsLink(row.connector)}>Needs identity</a> : <span className="mono-id">{row.identity || "—"}</span>}</td>
      <td>{row.origin || "—"}</td>
      <td>{row.setByTeam ? "Team" : "Role definition"}</td>
    </tr>; })}
  </tbody></table></div>;
}

function TeamList({ teams, onChanged }: { teams: TeamSummary[]; onChanged: () => void }) {
  return <section className="panel" aria-label="Teams list">
    <div className="panel-heading"><h2>Teams</h2><a className="document-action" href={newTeamLink}>New team</a></div>
    <div className="table-scroll"><table><thead><tr><th>Team</th><th>Project</th><th>Roles</th><th>Harnesses</th><th>Configs</th></tr></thead><tbody>
      {teams.map(team => <tr key={team.name}>
        <td><a className="resource-name" href={teamsLink(team.name)}>{team.name}</a>{team.system && <> <span className="badge">system</span></>}</td>
        <td>{team.projectUrl ? <>{team.projectUrl} <span className="description">{team.projectRef}</span></> : "—"}</td>
        <td>{team.rolesInherited ? <span className="description">defaults</span> : team.roles.join(", ") || "none"}</td>
        <td>{team.harnessesInherited ? <span className="description">defaults</span> : team.harnesses.join(", ") || "none"}</td>
        <td>{team.configs.length}</td>
      </tr>)}
    </tbody></table></div>
    {!teams.length && <div className="empty-state"><h3>No team is stored</h3><p>Initialize stores the kuke team and the default team, or add one with New team.</p><InitializePrompt onChanged={onChanged} /></div>}
  </section>;
}

function DeleteTeamDialog({ team, pending, onCancel, onConfirm }: { team: TeamSummary; pending: boolean; onCancel: () => void; onConfirm: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  useModalDialog(dialog);
  return <dialog className="cell-action-dialog" ref={dialog} aria-labelledby="team-delete-title" aria-describedby="team-delete-description" onCancel={event => { event.preventDefault(); onCancel(); }}>
    <form onSubmit={event => { event.preventDefault(); if (acknowledged) onConfirm(); }}>
      <h2 id="team-delete-title">Delete team</h2>
      <p id="team-delete-description">Deletes the team's {team.configs.length} Configs, the shared blueprints no other Config uses, and the team. Cells and volumes stay; the result lists the cells still running and the state volumes kept.</p>
      <dl className="cell-action-target"><dt>Team</dt><dd><code>{team.name}</code></dd></dl>
      <label className="cell-action-acknowledgment"><input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />I understand this deletes team {team.name} and its Configs.</label>
      <div className="cell-action-dialog-buttons">
        <button type="button" onClick={onCancel}>Cancel</button>
        <button className="danger-action" type="submit" disabled={!acknowledged || pending}>{pending ? "Deleting…" : "Confirm Delete"}</button>
      </div>
    </form>
  </dialog>;
}

// Origin is a list's badge: the team declares it, or inherits it from the
// defaults.
function Origin({ list }: { list: { inherited: boolean } }) {
  const origin = listOrigin(list);
  return <span className={`badge team-origin team-origin-${origin}`}>{origin}</span>;
}

function SourceLink({ source }: { source: ViewSource }) {
  return source.stored ? <a className="resource-name" href={sourcesLink(source.stored)}>{source.stored}</a> : <span className="description">defined by the team</span>;
}

function SourcesTable({ items, label }: { items: ViewSource[]; label: string }) {
  return <div className="table-scroll"><table aria-label={label}><thead><tr><th>Name</th><th>Stored Source</th><th>Address</th><th>Ref</th><th>Clone path</th><th>Git identity</th></tr></thead><tbody>
    {items.map(source => <tr key={source.alias}>
      <td><code>{source.alias}</code>{!source.declared && <> <span className="description">(stored, not listed)</span></>}</td>
      <td><SourceLink source={source} /></td>
      <td className="mono-id">{source.url || "—"}</td><td>{source.ref || "the Source's"}</td>
      <td className="mono-id">{source.clonePath || "—"}</td><td>{source.identity || "Default"}</td>
    </tr>)}
  </tbody></table></div>;
}

// TeamOverview is the team page (AC3): the project, the sources, the roles,
// the tasks with their schedules, the harnesses, the connectors and the
// Configs, each list marked declared or inherited.
function TeamOverview({ name, data, onChanged }: { name: string; data: TeamView; onChanged: () => void }) {
  const [pending, setPending] = useState("");
  const [error, setError] = useState<Failure>();
  const [applied, setApplied] = useState<TeamOutcome>();
  const [confirming, setConfirming] = useState(false);
  const [deleted, setDeleted] = useState<DeleteResult>();
  const { notify } = useNotify();
  const summary = data.summary;
  const view: TeamDetail | undefined = data.view;
  async function reapply() {
    if (pending) return;
    setPending("reapply"); setError(undefined);
    try {
      const outcome = await sendJSON<TeamOutcome>(teamsAPI(name, "reapply"), {});
      setApplied(outcome);
      notify({ key: `team-reapply:${name}`, tone: "success", message: `Team ${name} reapplied.` });
      onChanged();
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(""); }
  }
  async function remove() {
    setPending("delete"); setError(undefined);
    try {
      const result = await sendJSON<DeleteResult>(teamsAPI(name, "delete"), {});
      setConfirming(false); setDeleted(result);
      notify({ key: `team-delete:${name}`, tone: "success", message: `Team ${name} deleted.` });
      onChanged();
    } catch (caught) { setConfirming(false); setError(toFailure(caught)); }
    finally { setPending(""); }
  }
  if (deleted) {
    const done = deleteSummary(deleted);
    return <section className="panel" aria-label="Deleted team"><div className="panel-body">
      <p role="status">Team <code>{name}</code> deleted.</p>
      {!!done.removed.length && <><h3>Removed</h3><ul className="state-list">{done.removed.map(item => <li key={item} className="mono-id">{item}</li>)}</ul></>}
      {!!done.liveCells.length && <><h3>Cells still running</h3><ul className="state-list">{done.liveCells.map(item => <li key={item} className="mono-id">{item}</li>)}</ul></>}
      {!!done.keptVolumes.length && <><h3>State volumes kept</h3><ul className="state-list">{done.keptVolumes.map(item => <li key={item} className="mono-id">{item}</li>)}</ul><p className="description">Delete them with <code>kuke delete volume</code> when no longer needed.</p></>}
      <a href={teamsLink()}>Back to teams</a>
    </div></section>;
  }
  const configScope = (config: string) => view?.configs.find(item => item.name === config)?.scope;
  return <>
    <section className="panel" aria-label="Team overview">
      <div className="panel-heading"><h2>Overview</h2><div className="connector-actions">
        <a className="document-action" href={teamsLink(name, "edit")}>Edit</a>
        <a className="document-action" href={teamsLink(name, "export")}>Export</a>
        <button type="button" disabled={!!pending} onClick={() => void reapply()}>{pending === "reapply" ? "Reapplying…" : "Reapply"}</button>
        {!summary.system && <button type="button" className="danger-action" disabled={!!pending} onClick={() => setConfirming(true)}>Delete</button>}
      </div></div>
      <div className="panel-body">
        <dl className="details-grid">
          <dt>Realm</dt><dd>{summary.realm}</dd>
          <dt>Configs</dt><dd>{summary.configs.length}</dd>
        </dl>
        {summary.system && <p className="description">The {summary.name} system team cannot be deleted; reset it with <code>kuke team init --force</code>.</p>}
        {view?.unresolved && <div className="error" role="alert"><strong>The team does not resolve</strong><p>{view.unresolved}</p></div>}
        {data.problem && <div className="error" role="alert"><strong>The team's setup could not be read</strong><p>{data.problem}</p></div>}
      </div>
    </section>
    {error && <ErrorNotice error={error} kind="Team" />}
    {applied && <OutcomeNotice outcome={applied} />}
    {view && <>
      <section className="panel" aria-label="Project">
        <div className="panel-heading"><h2>Project</h2></div>
        <div className="panel-body">{view.project ? <SourcesTable items={[view.project]} label="Project source" /> : <p className="description">The team has no project.</p>}</div>
      </section>
      <section className="panel" aria-label="Sources">
        <div className="panel-heading"><h2>Sources</h2><Origin list={view.sources} /></div>
        <div className="panel-body">{view.sources.items.length ? <SourcesTable items={view.sources.items} label="Team sources" /> : <p className="description">No source besides the project.</p>}
          {view.sources.inherited && <p className="description">The team lists no sources: its roles and harnesses read stored Sources by name.</p>}</div>
      </section>
      <section className="panel" aria-label="Roles">
        <div className="panel-heading"><h2>Roles</h2><Origin list={view.roles} /></div>
        <div className="panel-body">{view.roles.items.length ? <div className="table-scroll"><table aria-label="Team roles"><thead><tr><th>Role</th><th>Source</th><th>Path</th><th>Harness</th><th>Image needs</th><th>Tasks</th></tr></thead><tbody>
          {view.roles.items.map(role => <tr key={role.name}>
            <td>{role.name}</td><td><code>{role.source}</code></td><td className="mono-id">{role.path}</td><td>{role.harness || "any"}</td>
            <td>{role.image.join(", ") || "—"}</td><td>{role.allTasks ? <span className="description">all it declares</span> : role.selected.map(taskLabel).join(", ") || "none"}</td>
          </tr>)}
        </tbody></table></div> : <p className="description">No role.</p>}</div>
      </section>
      <section className="panel" aria-label="Tasks">
        <div className="panel-heading"><h2>Tasks</h2><span className="badge">{view.tasks.length}</span></div>
        <div className="panel-body">{view.tasks.length ? <div className="team-tasks">{view.tasks.map(task => <section key={task.config} className="team-task" aria-label={`Task ${task.role} ${task.task}${task.harness ? ` on ${task.harness}` : ""}`}>
          <h3>{task.role} / {task.task}{task.harness ? ` on ${task.harness}` : ""} <a className="resource-name mono-id" href={configLink(task.scope, task.config)}>{task.config}</a></h3>
          {task.schedule ? <ScheduleDetails schedule={task.schedule} status={task.status} cellLink={() => undefined} /> : <p className="description">No schedule: the task runs when started by hand.{task.status?.lastScheduleTime ? <> Last run {when(task.status.lastScheduleTime)}.</> : null}</p>}
        </section>)}</div> : <p className="description">The team renders no task Config.</p>}</div>
      </section>
      <section className="panel" aria-label="Harnesses">
        <div className="panel-heading"><h2>Harnesses</h2><Origin list={view.harnesses} /></div>
        <div className="panel-body">{view.harnesses.items.length ? <ul className="state-list">{view.harnesses.items.map(h => <li key={h.name}>{h.name} <span className="description"><code>{h.source}</code>:{h.path}</span></li>)}</ul> : <p className="description">No harness.</p>}</div>
      </section>
      <section className="panel" aria-label="Connectors">
        <div className="panel-heading"><h2>Connectors</h2><Origin list={view.connectors} /></div>
        <div className="panel-body">{view.connectors.items.length ? <ul className="state-list">{view.connectors.items.map(c => <li key={c.name}><code>{c.name}</code> <span className="description">{c.method || "default method"}, {c.identity || "identity from the scope defaults"}</span></li>)}</ul> : <p className="description">No connector choice: every connector uses its default method and the scope defaults' identity.</p>}
          <RoleConnectors rows={view.connectors.roles ?? []} configs={view.configs} />
        </div>
      </section>
    </>}
    <section className="panel" aria-label="Configs">
      <div className="panel-heading"><h2>Configs</h2><span className="badge">{summary.configs.length}</span></div>
      <div className="panel-body">{summary.configs.length ? <ul className="state-list">{summary.configs.map(config => { const scope = configScope(config); return <li key={config}>{scope ? <a className="resource-name" href={configLink(scope, config)}>{config}</a> : <span className="mono-id">{config}</span>}</li>; })}</ul> : <p className="description">none</p>}</div>
    </section>
    {applied && <PlanView plan={applied.plan} title="Applied" />}
    {confirming && <DeleteTeamDialog team={summary} pending={pending === "delete"} onCancel={() => setConfirming(false)} onConfirm={() => void remove()} />}
  </>;
}

// ExportPage is kuke team export NAME (AC5): the team as a kuketeam.yaml,
// with the command's two options, Copy and Download.
function ExportPage({ name, revision }: { name: string; revision: number }) {
  const [hostFields, setHostFields] = useState(false);
  const [explicit, setExplicit] = useState(false);
  const exported = useResource<{ document: string }>(exportAPI(name, { hostFields, explicit }), revision);
  const [copied, setCopied] = useState(false);
  const document = exported.data?.document ?? "";
  const href = useMemo(() => document ? URL.createObjectURL(new Blob([document], { type: "application/yaml" })) : "", [document]);
  useEffect(() => () => { if (href) URL.revokeObjectURL(href); }, [href]);
  async function copy() {
    try { await navigator.clipboard.writeText(document); setCopied(true); } catch { setCopied(false); }
  }
  return <section className="panel" aria-label="Export">
    <div className="panel-heading"><h2>kuketeam.yaml</h2><div className="connector-actions">
      <button type="button" disabled={!document} onClick={() => void copy()}>{copied ? "Copied" : "Copy"}</button>
      {href ? <a className="document-action" href={href} download="kuketeam.yaml">Download</a> : <span className="document-action" aria-disabled="true">Download</span>}
    </div></div>
    <div className="panel-body">
      <p className="description">The team as a repository's <code>kuketeam.yaml</code>, the output of <code>kuke team export {name}</code>. Commit it to the project repository to create the team from it again.</p>
      <div className="connector-actions">
        <label className="schema-check"><input type="checkbox" checked={hostFields} onChange={event => { setHostFields(event.target.checked); setCopied(false); }} /><span>Keep host fields (<code>--host-fields</code>): realm, stored Source names, refs, git identities and secret bindings</span></label>
        <label className="schema-check"><input type="checkbox" checked={explicit} onChange={event => { setExplicit(event.target.checked); setCopied(false); }} /><span>Write inherited lists out (<code>--explicit-defaults</code>)</span></label>
      </div>
      {exported.error && <ErrorNotice error={exported.error} kind="Team" />}
      {exported.loading && !exported.data && <p role="status">Exporting…</p>}
      {document && <pre className="team-export" tabIndex={0} aria-label="Exported kuketeam.yaml">{document}</pre>}
    </div>
  </section>;
}

function TeamPage({ name, section, revision, onChanged }: { name: string; section?: string; revision: number; onChanged: () => void }) {
  const team = useResource<TeamView>(teamsAPI(name), revision);
  if (section === "export") return <ExportPage name={name} revision={revision} />;
  if (team.error) return <ErrorNotice error={team.error} kind="Team" />;
  if (!team.data) return <p role="status">Loading team…</p>;
  if (section === "edit") return <TeamWizard mode="edit" team={team.data} onChanged={onChanged} />;
  return <TeamOverview name={name} data={team.data} onChanged={onChanged} />;
}

export function TeamsPage({ name, section, revision, onChanged }: { name?: string; section?: string; revision: number; onChanged: () => void }) {
  const [reads, setReads] = useState(0);
  const refresh = () => { setReads(value => value + 1); onChanged(); };
  const teams = useResource<{ teams: TeamSummary[] }>(name ? null : teamsAPI(), revision + reads);
  if (name) return <TeamPage key={`${name}/${section ?? ""}`} name={name} section={section} revision={revision + reads} onChanged={refresh} />;
  return <>
    {teams.error && <ErrorNotice error={teams.error} kind="Teams" />}
    {teams.loading && !teams.data && <p role="status">Loading teams…</p>}
    {teams.data && <TeamList teams={teams.data.teams} onChanged={refresh} />}
    {teams.data && teams.data.teams.length > 0 && !teams.data.teams.some(team => team.name === systemTeam) && <div className="initialize-empty" role="note"><p className="description">The kuke team is not stored. Initialize creates it, the default team and the initial defaults.</p><InitializePrompt onChanged={refresh} /></div>}
    <p className="description">Where roles and harnesses come from is on the <a href={sourcesLink()}>Sources</a> page.</p>
  </>;
}

export function NewTeamPage({ onChanged }: { onChanged: () => void }) {
  return <TeamWizard mode="new" onChanged={onChanged} />;
}
