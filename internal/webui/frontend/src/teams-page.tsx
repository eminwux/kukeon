import { useRef, useState, type FormEvent } from "react";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import { useModalDialog } from "./focus";
import { sendJSON, toFailure } from "./connector-ui";
import {
  addErrors, addPayload, defaultsAPI, deleteSummary, editPayload, inheritingNote, needsForce, optionKey, originLabel, pairRows, pickerOptions, planSections, refKinds, refLabel,
  sourceItems, sourcePayload, sourcesAPI, systemTeam, teamConfigs, teamsAPI, teamsLink, toggledDefaults,
} from "./teams.mjs";
import type { AddForm, DefaultsChange, DeleteResult, PickerOption, Plan, SourceContents, SourceForm, Sources, TeamOutcome, TeamSummary, TeamView } from "./teams.mjs";

// The Teams page (#1618, AC16 and AC17). Every action posts to the teams API,
// which runs the same code as `kuke team`: the server fetches the sources,
// composes and applies, as the account running `kuke serve`. A declaration is
// edited as YAML and checked by the preview endpoint; only roles and harnesses
// have pickers.

type CatalogConfig = { metadata: { name: string; realm?: string; space?: string; stack?: string; labels?: Record<string, string> } };

function configLink(scope: string[], name: string): string {
  return "#/configs/" + [...scope, name].map(encodeURIComponent).join("/");
}

function PlanView({ plan, title }: { plan: Plan; title: string }) {
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

function OutcomeNotice({ outcome }: { outcome: TeamOutcome }) {
  const result = outcome.result;
  if (!result) return null;
  return <div className="health-sweep-started" role="status">
    <p>Team <code>{result.team}</code> applied: {result.resources} resources{result.pruned.length ? `; pruned ${result.pruned.join(", ")}` : ""}.</p>
    {!!result.missingSecrets.length && <p>Missing secrets: {result.missingSecrets.join(", ")}.</p>}
    {!!result.notes.length && <ul className="description">{result.notes.map(note => <li key={note}>{note}</li>)}</ul>}
  </div>;
}

function Picker({ label, options, value, disabled, onChange }: { label: string; options: PickerOption[]; value: PickerOption[] | null; disabled: boolean; onChange: (value: PickerOption[] | null) => void }) {
  const selected = new Set((value ?? []).map(optionKey));
  return <fieldset className="schema-field team-picker" disabled={disabled}>
    <legend>{label}</legend>
    <label className="schema-check"><input type="checkbox" checked={value === null} onChange={event => onChange(event.target.checked ? null : options.filter(option => option.default))} />Keep the declaration's list</label>
    {value !== null && <>
      {options.map(option => <label className="schema-check" key={optionKey(option)}><input type="checkbox" checked={selected.has(optionKey(option))} onChange={event => onChange(event.target.checked ? [...(value ?? []), option] : (value ?? []).filter(item => optionKey(item) !== optionKey(option)))} />{option.name} <span className="description">{option.source}:{option.path}{option.default ? " — default" : ""}</span></label>)}
      {!options.length && <small>No default and no inspected source offers one. Register a source below and show it.</small>}
    </>}
  </fieldset>;
}

const emptyAdd: AddForm = { name: "", source: "", refKind: "branch", ref: "", useRepoFile: true, roles: null, harnesses: null, declaration: "" };

function RefFields({ kind, value, disabled, onChange }: { kind: string; value: string; disabled: boolean; onChange: (kind: string, value: string) => void }) {
  return <label className="schema-field"><span>Ref</span>
    <span className="team-ref">
      <select value={kind} disabled={disabled} aria-label="Ref kind" onChange={event => onChange(event.target.value, value)}>{refKinds.map(item => <option key={item} value={item}>{item}</option>)}</select>
      <input value={value} disabled={disabled} placeholder={kind === "branch" ? "default branch" : kind === "tag" ? "v1.2.3" : "full commit SHA"} spellCheck={false} autoComplete="off" aria-label="Ref" onChange={event => onChange(kind, event.target.value)} />
    </span>
  </label>;
}

// AddTeam is kuke team add NAME --source URL: preview first, then add. An
// existing team is replaced only after the user confirms (AC8a).
function AddTeam({ sources, inspected, onChanged }: { sources: Sources | undefined; inspected: Record<string, SourceContents | undefined>; onChanged: () => void }) {
  const [form, setForm] = useState<AddForm>(emptyAdd);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const [preview, setPreview] = useState<TeamOutcome>();
  const [replace, setReplace] = useState(false);
  const { notify } = useNotify();
  const set = (next: Partial<AddForm>) => { setForm(old => ({ ...old, ...next })); setError(undefined); setPreview(undefined); setReplace(false); };
  const errors = addErrors(form);
  async function send(dryRun: boolean, force = false) {
    if (pending) return;
    setPending(true); setError(undefined);
    try {
      const outcome = await sendJSON<TeamOutcome>(dryRun ? teamsAPI("preview") : teamsAPI(), addPayload({ ...form, force }));
      if (dryRun) { setPreview(outcome); return; }
      notify({ key: `team-add:${outcome.result?.team}`, tone: "success", message: `Team ${outcome.result?.team} added.` });
      setForm(emptyAdd); setPreview(undefined); setReplace(false);
      onChanged();
      if (outcome.result?.team) window.location.hash = teamsLink(outcome.result.team);
    } catch (caught) {
      const failure = toFailure(caught);
      if (needsForce(failure)) setReplace(true);
      setError(failure);
    } finally { setPending(false); }
  }
  function submit(event: FormEvent) { event.preventDefault(); void send(true); }
  return <section className="panel" aria-label="Add team">
    <div className="panel-heading"><h2>Add team</h2></div>
    <div className="panel-body">
      <form className="creation-form" aria-label="Add team form" method="post" autoComplete="off" onSubmit={submit}>
        <p className="description">Fetches the repository as the account running <code>kuke serve</code>. Without a <code>kuketeam.yaml</code> in it, the team takes the default roles and harnesses.</p>
        <div className="schema-fields">
          <label className="schema-field"><span>Repository URL<b aria-label="required"> *</b></span><input value={form.source} disabled={pending} placeholder="https://github.com/owner/repo" spellCheck={false} onChange={event => set({ source: event.target.value })} /></label>
          <label className="schema-field"><span>Name</span><input value={form.name} disabled={pending} placeholder="the repository file's name" spellCheck={false} onChange={event => set({ name: event.target.value })} /></label>
          <RefFields kind={form.refKind} value={form.ref} disabled={pending} onChange={(refKind, ref) => set({ refKind, ref })} />
          <label className="schema-check"><input type="checkbox" checked={form.useRepoFile} disabled={pending} onChange={event => set({ useRepoFile: event.target.checked })} />Use the repository's <code>kuketeam.yaml</code></label>
          <Picker label="Roles" options={pickerOptions(sources, inspected, "role")} value={form.roles} disabled={pending} onChange={roles => set({ roles })} />
          <Picker label="Harnesses" options={pickerOptions(sources, inspected, "harness")} value={form.harnesses} disabled={pending} onChange={harnesses => set({ harnesses })} />
          <label className="schema-field team-yaml"><span>Declaration (optional)</span><textarea value={form.declaration} disabled={pending} spellCheck={false} placeholder={"apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: …\nspec:\n  roles: […]"} onChange={event => set({ declaration: event.target.value })} /><small>A whole Team document replaces the declaration, as <code>kuke team add -f</code>. Tasks, schedules, needs, connectors and sources are edited here.</small></label>
        </div>
        {!!errors.length && form.source && <ul className="setup-error">{errors.map(message => <li key={message}>{message}</li>)}</ul>}
        <div className="connector-actions">
          <button type="submit" disabled={pending || !!errors.length}>{pending && !preview ? "Previewing…" : "Preview"}</button>
          <button type="button" disabled={pending || !!errors.length || !preview} onClick={() => void send(false)}>{pending && preview ? "Adding…" : "Add team"}</button>
        </div>
        {error && <ErrorNotice error={error} kind="Team" />}
        {replace && <div className="error" role="alert"><p>A team of this name is stored. Reapply it from its team page to recompose it with its stored declaration, or replace the declaration with this one.</p><button type="button" className="danger-action" disabled={pending} onClick={() => void send(false, true)}>Replace the stored declaration</button></div>}
      </form>
      {preview && <PlanView plan={preview.plan} title="Preview" />}
    </div>
  </section>;
}

function TeamList({ teams }: { teams: TeamSummary[] }) {
  return <section className="panel" aria-label="Teams list">
    <div className="table-scroll"><table><thead><tr><th>Team</th><th>Project</th><th>Roles</th><th>Harnesses</th><th>Configs</th></tr></thead><tbody>
      {teams.map(team => <tr key={team.name}>
        <td><a className="resource-name" href={teamsLink(team.name)}>{team.name}</a>{team.system && <> <span className="badge">system</span></>}</td>
        <td>{team.projectUrl ? <>{team.projectUrl} <span className="description">{team.projectRef}</span></> : "—"}</td>
        <td>{team.rolesInherited ? <span className="description">defaults</span> : team.roles.join(", ") || "none"}</td>
        <td>{team.harnessesInherited ? <span className="description">defaults</span> : team.harnesses.join(", ") || "none"}</td>
        <td>{team.configs.length}</td>
      </tr>)}
    </tbody></table></div>
    {!teams.length && <div className="empty-state"><h3>No team is stored</h3><p>Run <code>kuke team init</code> for the kuke team, or add one below.</p></div>}
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

// TeamDetail is one stored team: its summary and Configs, its declaration
// edited as YAML with a preview before apply, reapply and delete.
function TeamDetail({ name, revision, onChanged }: { name: string; revision: number; onChanged: () => void }) {
  const team = useResource<TeamView>(teamsAPI(name), revision);
  const configs = useResource<CatalogConfig[]>("/api/catalog/configs", revision);
  const [draft, setDraft] = useState<string>();
  const [pending, setPending] = useState("");
  const [error, setError] = useState<Failure>();
  const [preview, setPreview] = useState<TeamOutcome>();
  const [applied, setApplied] = useState<TeamOutcome>();
  const [confirming, setConfirming] = useState(false);
  const [deleted, setDeleted] = useState<DeleteResult>();
  const { notify } = useNotify();
  if (team.error) return <ErrorNotice error={team.error} kind="Team" />;
  if (!team.data) return <p role="status">Loading team…</p>;
  const summary = team.data.summary;
  const declaration = draft ?? team.data.document;
  const located = teamConfigs(configs.data, name);
  async function run(action: string, path: string, body: unknown) {
    if (pending) return;
    setPending(action); setError(undefined);
    try {
      const outcome = await sendJSON<TeamOutcome>(path, body);
      if (action === "preview") { setPreview(outcome); return; }
      setApplied(outcome); setPreview(undefined); setDraft(undefined);
      notify({ key: `team-${action}:${name}`, tone: "success", message: `Team ${name} ${action === "reapply" ? "reapplied" : "applied"}.` });
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
  return <>
    <section className="panel" aria-label="Team overview">
      <div className="panel-heading"><h2>Overview</h2><div className="connector-actions">
        <button type="button" disabled={!!pending} onClick={() => void run("reapply", teamsAPI(name, "reapply"), {})}>{pending === "reapply" ? "Reapplying…" : "Reapply"}</button>
        {!summary.system && <button type="button" className="danger-action" disabled={!!pending} onClick={() => setConfirming(true)}>Delete</button>}
      </div></div>
      <div className="panel-body"><dl className="details-grid">
        <dt>Realm</dt><dd>{summary.realm}</dd>
        <dt>Project</dt><dd>{summary.projectUrl ? <>{summary.projectUrl} <span className="description">{summary.projectRef}</span></> : "—"}</dd>
        <dt>Roles</dt><dd>{summary.roles.join(", ") || "none"}{summary.rolesInherited && <span className="description"> (the defaults)</span>}</dd>
        <dt>Harnesses</dt><dd>{summary.harnesses.join(", ") || "none"}{summary.harnessesInherited && <span className="description"> (the defaults)</span>}</dd>
        <dt>Configs</dt><dd>{summary.configs.length ? summary.configs.map(config => { const found = located.find(item => item.name === config); return <div key={config}>{found ? <a className="resource-name" href={configLink(found.scope, config)}>{config}</a> : <span className="mono-id">{config}</span>}</div>; }) : "none"}</dd>
      </dl>
      {summary.system && <p className="description">The kuke system team cannot be deleted; reset it with <code>kuke team init --force</code>.</p>}
      </div>
    </section>
    {error && <ErrorNotice error={error} kind="Team" />}
    {applied && <OutcomeNotice outcome={applied} />}
    <section className="panel" aria-label="Team declaration">
      <div className="panel-heading"><h2>Declaration</h2>{draft !== undefined && <span className="badge">Edited</span>}</div>
      <div className="panel-body creation-form">
        <label className="schema-field team-yaml"><span>Team document</span><textarea aria-label="Team declaration YAML" value={declaration} spellCheck={false} disabled={!!pending} onChange={event => { setDraft(event.target.value); setPreview(undefined); }} /></label>
        <div className="connector-actions">
          <button type="button" disabled={!!pending} onClick={() => void run("preview", teamsAPI("preview"), editPayload(name, declaration))}>{pending === "preview" ? "Previewing…" : "Preview"}</button>
          <button type="button" disabled={!!pending || !preview} onClick={() => void run("apply", teamsAPI(), editPayload(name, declaration))}>{pending === "apply" ? "Applying…" : "Apply"}</button>
          {draft !== undefined && <button type="button" className="subtle-button" disabled={!!pending} onClick={() => { setDraft(undefined); setPreview(undefined); }}>Discard edits</button>}
        </div>
        <p className="description">Apply composes from scratch again: a branch may have moved since the preview, and the apply result is authoritative.</p>
      </div>
    </section>
    {preview && <PlanView plan={preview.plan} title="Preview" />}
    {applied && <PlanView plan={applied.plan} title="Applied" />}
    {confirming && <DeleteTeamDialog team={summary} pending={pending === "delete"} onCancel={() => setConfirming(false)} onConfirm={() => void remove()} />}
  </>;
}

// SourceDetail is one registered source: what it offers, with the default
// toggles, and its removal.
function SourceDetail({ name, sources, contents, onInspected, onChanged }: { name: string; sources: Sources; contents: SourceContents | undefined; onInspected: (contents: SourceContents) => void; onChanged: (change: DefaultsChange) => void }) {
  const [pending, setPending] = useState("");
  const [error, setError] = useState<Failure>();
  async function act<T>(action: string, path: string, body: unknown, done: (value: T) => void) {
    if (pending) return;
    setPending(action); setError(undefined);
    try { done(await sendJSON<T>(path, body)); } catch (caught) { setError(toFailure(caught)); } finally { setPending(""); }
  }
  const items = sourceItems(contents);
  const isDefault = (kind: "role" | "harness", path: string) => (kind === "role" ? sources.defaultRoles : sources.defaultHarnesses).some(item => item.source === name && item.path === path);
  return <div className="team-source">
    <div className="connector-actions">
      <button type="button" disabled={!!pending} onClick={() => void act<SourceContents>("inspect", sourcesAPI(name, "inspect"), {}, onInspected)}>{pending === "inspect" ? "Fetching…" : contents ? "Refresh" : "Show contents"}</button>
      <button type="button" className="danger-action" disabled={!!pending} onClick={() => void act<DefaultsChange>("remove", sourcesAPI(name, "delete"), {}, onChanged)}>{pending === "remove" ? "Removing…" : "Remove"}</button>
    </div>
    {error && <ErrorNotice error={error} kind="Source" />}
    {contents && <>
      {contents.commit && <p className="description">At commit <code>{contents.commit.slice(0, 12)}</code>.</p>}
      <table aria-label={`Roles in ${name}`}><thead><tr><th>Role</th><th>Path</th><th>Harnesses</th><th>Tasks</th><th>Default</th></tr></thead><tbody>
        {items.roles.map(role => <tr key={role.path}><td>{role.name}</td><td className="mono-id">{role.path}</td><td>{role.harnesses.join(", ") || "any"}</td><td>{role.tasks.join(", ") || "—"}</td>
          <td><input type="checkbox" aria-label={`Default role ${role.name}`} checked={isDefault("role", role.path)} disabled={!!pending} onChange={event => void act<DefaultsChange>("toggle", defaultsAPI, toggledDefaults(sources, "role", { name: role.name, source: name, path: role.path }, event.target.checked), onChanged)} /></td></tr>)}
      </tbody></table>
      {!items.roles.length && <p className="description">No role.</p>}
      <table aria-label={`Harnesses in ${name}`}><thead><tr><th>Harness</th><th>Path</th><th>Default</th></tr></thead><tbody>
        {items.harnesses.map(harness => <tr key={harness.path}><td>{harness.name}</td><td className="mono-id">{harness.path}</td>
          <td><input type="checkbox" aria-label={`Default harness ${harness.name}`} checked={isDefault("harness", harness.path)} disabled={!!pending} onChange={event => void act<DefaultsChange>("toggle", defaultsAPI, toggledDefaults(sources, "harness", { name: harness.name, source: name, path: harness.path }, event.target.checked), onChanged)} /></td></tr>)}
      </tbody></table>
      {!items.harnesses.length && <p className="description">No harness.</p>}
    </>}
  </div>;
}

const emptySource: SourceForm = { name: "", url: "", refKind: "branch", ref: "", clonePath: "" };

// SourcesPanel is kuke team source add|list|show|remove and the default
// toggles of kuke team defaults set (AC17).
function SourcesPanel({ sources, inspected, onInspected, onChanged }: { sources: Sources; inspected: Record<string, SourceContents | undefined>; onInspected: (name: string, contents: SourceContents) => void; onChanged: () => void }) {
  const [form, setForm] = useState<SourceForm>(emptySource);
  const [open, setOpen] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const [change, setChange] = useState<DefaultsChange>();
  const set = (next: Partial<SourceForm>) => { setForm(old => ({ ...old, ...next })); setError(undefined); };
  const changed = (next: DefaultsChange) => { setChange(next); onChanged(); };
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    setPending(true); setError(undefined);
    try {
      const result = await sendJSON<DefaultsChange>(sourcesAPI(), sourcePayload(form));
      if (result.discovered) onInspected(form.name.trim(), result.discovered);
      setForm(emptySource); changed(result);
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(false); }
  }
  return <section className="panel" aria-label="Team sources">
    <div className="panel-heading"><h2>Sources</h2><span className="badge">{sources.sources.length} registered</span></div>
    <div className="panel-body">
      <p className="description">Where roles and harnesses come from. A team that declares no sources, roles or harnesses takes the defaults here; changing them reaches a team when it is reapplied.</p>
      {change && <div className="health-sweep-started" role="status"><p>Defaults {change.action}. {inheritingNote(change)}</p></div>}
      <ul className="state-list">{sources.sources.map(source => <li key={source.name} aria-label={`Source ${source.name}`}>
        <button type="button" className="text-button" aria-expanded={open === source.name} onClick={() => setOpen(open === source.name ? "" : source.name)}><code>{source.name}</code></button> {source.url} <span className="description">{refLabel(source)}{source.clonePath ? ` · cloned at ${source.clonePath}` : ""}</span>
        {open === source.name && <SourceDetail name={source.name} sources={sources} contents={inspected[source.name]} onInspected={contents => onInspected(source.name, contents)} onChanged={changed} />}
      </li>)}</ul>
      {!sources.sources.length && <p className="description">No source is registered.</p>}
      <form className="creation-form" aria-label="Register source" method="post" autoComplete="off" onSubmit={submit}>
        <h3>Register a source</h3>
        <div className="schema-fields">
          <label className="schema-field"><span>Name<b aria-label="required"> *</b></span><input value={form.name} disabled={pending} placeholder="agents" spellCheck={false} onChange={event => set({ name: event.target.value })} /></label>
          <label className="schema-field"><span>URL<b aria-label="required"> *</b></span><input value={form.url} disabled={pending} placeholder="https://github.com/owner/repo" spellCheck={false} onChange={event => set({ url: event.target.value })} /></label>
          <RefFields kind={form.refKind} value={form.ref} disabled={pending} onChange={(refKind, ref) => set({ refKind, ref })} />
          <label className="schema-field"><span>Clone path</span><input value={form.clonePath} disabled={pending} placeholder="/home/user/<name> when the source has roles" spellCheck={false} onChange={event => set({ clonePath: event.target.value })} /></label>
        </div>
        <div className="connector-actions"><button type="submit" disabled={pending || !form.name.trim() || !form.url.trim()}>{pending ? "Registering…" : "Register source"}</button></div>
        {error && <ErrorNotice error={error} kind="Source" />}
      </form>
    </div>
  </section>;
}

export function TeamsPage({ name, revision, onChanged }: { name?: string; revision: number; onChanged: () => void }) {
  const [reads, setReads] = useState(0);
  const [inspected, setInspected] = useState<Record<string, SourceContents | undefined>>({});
  const refresh = () => { setReads(value => value + 1); onChanged(); };
  const teams = useResource<{ teams: TeamSummary[] }>(name ? null : teamsAPI(), revision + reads);
  const sources = useResource<Sources>(name ? null : sourcesAPI(), revision + reads);
  if (name) return <TeamDetail key={name} name={name} revision={revision + reads} onChanged={refresh} />;
  return <>
    {teams.error && <ErrorNotice error={teams.error} kind="Teams" />}
    {teams.loading && !teams.data && <p role="status">Loading teams…</p>}
    {teams.data && <TeamList teams={teams.data.teams} />}
    {teams.data && !teams.data.teams.some(team => team.name === systemTeam) && <p className="description" role="note">The kuke team is not stored. Run <code>kuke team init</code> on the host to create it and the initial defaults.</p>}
    <AddTeam sources={sources.data} inspected={inspected} onChanged={refresh} />
    {sources.error && <ErrorNotice error={sources.error} kind="Sources" />}
    {sources.data && <SourcesPanel sources={sources.data} inspected={inspected} onInspected={(source, contents) => setInspected(old => ({ ...old, [source]: contents }))} onChanged={refresh} />}
  </>;
}
