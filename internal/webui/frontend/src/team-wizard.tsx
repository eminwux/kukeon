import { useState, type ReactNode } from "react";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import { sendJSON, toFailure } from "./connector-ui";
import { identitiesFor } from "./connectors.mjs";
import type { Connector } from "./connectors.mjs";
import { GitIdentitySelect, OutcomeNotice, PlanView, RefFields } from "./team-plan";
import {
  availableSources, createBody, draftPayload, editBody, emptyProject, emptySource, imageList, inspectRequest, projectAlias, projectErrors, selectedIndex, stateLabel, stepErrors,
  roleConnectorRows, roleHarnesses, stepsFor, taskRows, withAllTasks, withConnector, withConnectorsInherit, withHarness, withInherit, withProject, withRole, withRoleConnector, withRolePatch, withSource, withTask,
} from "./team-form.mjs";
import type { Available, ProjectInput } from "./team-form.mjs";
import { editPayload, needsForce, originLabel, refLabel, sourcesLink, teamsAPI, teamsLink } from "./teams.mjs";
import type { Draft, FormRole, RoleConnectorView, Selection, SourceContents, Sources, TeamForm, TeamOutcome, TeamView, ViewTask } from "./teams.mjs";

// The team wizard (#1644, AC2 and AC4): one component for New team
// (#/new-team) and Edit team (#/teams/<name>/edit). Its state is the server's
// form plus, for a new team, the draft's declaration, sent back unchanged.
// Every request is the CLI's: the draft reads what kuke create team would,
// the preview and the apply run create team or team apply -f.

type Mode = "new" | "edit";
type Inspected = Record<string, { contents?: SourceContents; error?: Failure; loading?: boolean }>;

function Errors({ errors }: { errors: string[] }) {
  if (!errors.length) return null;
  return <ul className="setup-error" aria-label="Step problems">{errors.map(message => <li key={message}>{message}</li>)}</ul>;
}

function ProjectStep({ project, sources, draft, pending, error, onChange, onRead }: {
  project: ProjectInput; sources: Sources | undefined; draft: Draft | undefined; pending: boolean; error?: Failure;
  onChange: (next: Partial<ProjectInput>) => void; onRead: () => void;
}) {
  const stored = sources?.sources.find(source => source.name === project.stored);
  const errors = projectErrors(project);
  return <div className="wizard-step">
    <fieldset className="schema-field wizard-choice"><legend>The project</legend>
      <label className="schema-check"><input type="radio" name="project-mode" checked={project.mode === "address"} disabled={pending} onChange={() => onChange({ mode: "address" })} />A repository address</label>
      <label className="schema-check"><input type="radio" name="project-mode" checked={project.mode === "stored"} disabled={pending} onChange={() => onChange({ mode: "stored" })} />A stored Source</label>
      <label className="schema-check"><input type="radio" name="project-mode" checked={project.mode === "file"} disabled={pending} onChange={() => onChange({ mode: "file" })} /><span>A pasted <code>kuketeam.yaml</code></span></label>
    </fieldset>
    <div className="schema-fields">
      {project.mode === "address" && <label className="schema-field"><span>Repository address<b aria-label="required"> *</b></span><input value={project.address} disabled={pending} placeholder="github.com/owner/repo" spellCheck={false} onChange={event => onChange({ address: event.target.value })} /></label>}
      {project.mode === "stored" && <label className="schema-field"><span>Stored Source<b aria-label="required"> *</b></span><select value={project.stored} disabled={pending} onChange={event => onChange({ stored: event.target.value })}><option value="">Choose…</option>{sources?.sources.map(source => <option key={source.name} value={source.name}>{source.name} — {source.url}</option>)}</select>{stored && <small>Identity: {stored.identity || "Default"}</small>}</label>}
      {project.mode === "file" && <label className="schema-field team-yaml"><span>kuketeam.yaml<b aria-label="required"> *</b></span><textarea value={project.file} disabled={pending} spellCheck={false} placeholder={"apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: …\nspec:\n  projectSource: {url: github.com/owner/repo}"} onChange={event => onChange({ file: event.target.value })} /><small>Its <code>spec.projectSource.url</code> names the project.</small></label>}
      {project.mode !== "file" && <RefFields kind={project.refKind} value={project.ref} disabled={pending} onChange={(refKind, ref) => onChange({ refKind, ref })} />}
      {project.mode === "address" && <GitIdentitySelect value={project.identity} disabled={pending} onChange={identity => onChange({ identity })} />}
      <label className="schema-field"><span>Team name</span><input value={project.name} disabled={pending} placeholder="the file's name, else the repository's" spellCheck={false} onChange={event => onChange({ name: event.target.value })} /></label>
      {project.mode !== "file" && <label className="schema-check"><input type="checkbox" checked={project.useRepoFile} disabled={pending} onChange={event => onChange({ useRepoFile: event.target.checked })} /><span>Use the repository's <code>kuketeam.yaml</code></span></label>}
    </div>
    {(project.address || project.stored || project.file) && <Errors errors={errors} />}
    <div className="connector-actions"><button type="button" disabled={pending || !!errors.length} onClick={onRead}>{pending ? "Reading…" : "Read repository"}</button></div>
    {error && <ErrorNotice error={error} kind="Repository" />}
    {draft && <div className="health-sweep-started" role="status" aria-label="Repository read">
      <p>{draft.origin.kind === "defaults" ? <>No <code>kuketeam.yaml</code> is read: the team takes the default roles and harnesses.</> : <>Declared by {originLabel(draft.origin)}: {draft.form.roles.inherit ? "the default roles" : `${draft.form.roles.items.length} roles`}, {draft.form.harnesses.inherit ? "the default harnesses" : `${draft.form.harnesses.items.length} harnesses`}, {draft.sources.length - 1} other sources.</>}</p>
      <p>Team <code>{draft.name}</code>.</p>
      {!!draft.notes.length && <ul className="description">{draft.notes.map(note => <li key={note}>{note}</li>)}</ul>}
    </div>}
  </div>;
}

function SourcesStep({ mode, form, draft, sources, disabled, onForm }: { mode: Mode; form: TeamForm; draft?: Draft; sources: Sources | undefined; disabled: boolean; onForm: (form: TeamForm) => void }) {
  const [adding, setAdding] = useState("");
  const listed = form.sources ?? [];
  const drafted = (alias: string) => draft?.sources.find(source => source.alias === alias);
  return <div className="wizard-step">
    {mode === "edit" && form.project && <fieldset className="schema-object"><legend>Project</legend>
      <p className="description">{form.project.url || form.project.stored} — the team keeps its project; only its ref changes here.</p>
      <RefFields label="Project ref" allowNone kind={form.project.refKind ?? ""} value={form.project.ref ?? ""} disabled={disabled} onChange={(refKind, ref) => onForm(withProject(form, { refKind, ref }))} />
    </fieldset>}
    {mode === "new" && form.project && <p className="description">Project: <code>{form.project.url || form.project.stored}</code> {drafted(projectAlias) && <span className="badge">{stateLabel(drafted(projectAlias)!.state)}</span>}</p>}
    <p className="description">Roles and harnesses can also name any stored Source by its name without listing it here. List a source to give it a clone path, its own ref, or to define one by address.</p>
    {listed.map((source, index) => {
      const state = drafted(source.alias);
      return <fieldset key={index} className="schema-object wizard-source" aria-label={`Source ${source.alias || index + 1}`}>
        <legend>{source.alias || `Source ${index + 1}`} {state && <span className={`badge team-state-${state.state}`}>{stateLabel(state.state)}</span>}</legend>
        {state?.state === "conflict" && <p className="field-error">A stored Source {state.stored} holds another repository ({state.have}). Store this one under another name.</p>}
        <div className="schema-fields">
          <label className="schema-field"><span>Name</span><input value={source.alias} disabled={disabled} spellCheck={false} onChange={event => onForm(withSource(form, index, { alias: event.target.value }))} /></label>
          {source.url !== undefined ? <>
            <label className="schema-field"><span>Address</span><input value={source.url} disabled={disabled} spellCheck={false} onChange={event => onForm(withSource(form, index, { url: event.target.value }))} /></label>
            <label className="schema-field"><span>Stored as</span><input value={source.stored ?? ""} disabled={disabled} placeholder={source.alias} spellCheck={false} onChange={event => onForm(withSource(form, index, { stored: event.target.value }))} /></label>
            <GitIdentitySelect value={source.identity ?? ""} disabled={disabled} onChange={identity => onForm(withSource(form, index, { identity }))} />
          </> : <label className="schema-field"><span>Stored Source</span><input value={source.stored ?? ""} disabled={disabled} placeholder={source.alias} spellCheck={false} onChange={event => onForm(withSource(form, index, { stored: event.target.value }))} /><small>{(() => { const backing = sources?.sources.find(item => item.name === (source.stored || source.alias)); return backing ? <>{backing.url}, identity {backing.identity || "Default"} — <a href={sourcesLink(backing.name)}>Source page</a></> : "Not stored."; })()}</small></label>}
          <RefFields allowNone kind={source.refKind ?? ""} value={source.ref ?? ""} disabled={disabled} onChange={(refKind, ref) => onForm(withSource(form, index, { refKind, ref }))} />
          <label className="schema-field"><span>Clone path</span><input value={source.clonePath ?? ""} disabled={disabled} placeholder={`/home/user/${source.alias || "name"}`} spellCheck={false} onChange={event => onForm(withSource(form, index, { clonePath: event.target.value }))} /></label>
        </div>
        <button type="button" className="subtle-button" disabled={disabled} onClick={() => onForm(withSource(form, index, null))}>Remove source</button>
      </fieldset>;
    })}
    {!listed.length && <p className="description">The team lists no other source.</p>}
    <div className="connector-actions">
      <label className="schema-field"><span>Add a stored Source</span><select aria-label="Add a stored Source" value={adding} disabled={disabled} onChange={event => setAdding(event.target.value)}><option value="">Choose…</option>{sources?.sources.filter(source => !listed.some(item => item.alias === source.name)).map(source => <option key={source.name} value={source.name}>{source.name}</option>)}</select></label>
      <button type="button" disabled={disabled || !adding} onClick={() => { onForm(withSource(form, undefined, emptySource({ name: adding }))); setAdding(""); }}>Add</button>
      <button type="button" disabled={disabled} onClick={() => onForm(withSource(form, undefined, {}))}>Add by address</button>
    </div>
  </div>;
}

// Offered lists one source's roles or harnesses as checkboxes, fetched on
// demand.
function SourceGroup({ source, inspected, onInspect, children }: { source: Available; inspected: Inspected; onInspect: (source: Available) => void; children: (contents: SourceContents) => ReactNode }) {
  const state = inspected[source.alias];
  return <section className="wizard-group" aria-label={`Source ${source.alias}`}>
    <div className="schema-array-heading"><h4><code>{source.alias}</code> <span className="description">{source.url} {source.stored && source.stored !== source.alias ? `(Source ${source.stored})` : ""}</span></h4>
      <button type="button" className="subtle-button" disabled={state?.loading} onClick={() => onInspect(source)}>{state?.loading ? "Fetching…" : state?.contents ? "Refresh" : "Show contents"}</button></div>
    {state?.error && <ErrorNotice error={state.error} kind="Source" />}
    {state?.contents && children(state.contents)}
  </section>;
}

function RolesStep({ form, available, inspected, inheritedRoles, inheritedHarnesses, disabled, onInspect, onForm }: {
  form: TeamForm; available: Available[]; inspected: Inspected; inheritedRoles: unknown[]; inheritedHarnesses: Selection[]; disabled: boolean;
  onInspect: (source: Available) => void; onForm: (form: TeamForm) => void;
}) {
  const offeredRole = (role: FormRole) => inspected[role.source]?.contents?.roles.find(item => item.path === role.path);
  return <div className="wizard-step">
    <fieldset className="schema-object" aria-label="Roles"><legend>Roles</legend>
      <label className="schema-check"><input type="checkbox" checked={form.roles.inherit} disabled={disabled} onChange={event => onForm(withInherit(form, "roles", event.target.checked, inheritedRoles))} />Use the default roles</label>
      {form.roles.inherit ? <p className="description">{(inheritedRoles as Selection[]).map(role => role.name).join(", ") || "The defaults have no roles."}</p> : <>
        {form.roles.items.map((role, index) => {
          const harnesses = offeredRole(role)?.harnesses ?? [];
          return <div key={`${role.source}:${role.path}`} className="wizard-role" aria-label={`Role ${role.name}`}>
            <div className="schema-array-heading"><h4>{role.name} <span className="description"><code>{role.source}</code>:{role.path}</span></h4><button type="button" className="subtle-button" disabled={disabled} onClick={() => onForm(withRole(form, role, false))}>Remove</button></div>
            <div className="schema-fields">
              <label className="schema-field"><span>Name</span><input value={role.name} disabled={disabled} spellCheck={false} onChange={event => onForm(withRolePatch(form, index, { name: event.target.value }))} /></label>
              <label className="schema-field"><span>Harness</span><select value={role.harness ?? ""} disabled={disabled} onChange={event => onForm(withRolePatch(form, index, { harness: event.target.value }))}><option value="">Any the role declares</option>{[...new Set([...harnesses, ...(role.harness ? [role.harness] : [])])].map(name => <option key={name} value={name}>{name}</option>)}</select></label>
              <label className="schema-field"><span>Image needs</span><input value={role.image.join(", ")} disabled={disabled} placeholder="go, node" spellCheck={false} onChange={event => onForm(withRolePatch(form, index, { image: imageList(event.target.value) }))} /><small>Capabilities the cell image adds, comma separated.</small></label>
              <label className="schema-field"><span>Initial path</span><input value={role.initialPath ?? ""} disabled={disabled} placeholder="the project's clone" spellCheck={false} onChange={event => onForm(withRolePatch(form, index, { initialPath: event.target.value }))} /></label>
            </div>
          </div>;
        })}
        {!form.roles.items.length && <p className="description">No role is selected.</p>}
        <h4>Add roles</h4>
        {available.map(source => <SourceGroup key={source.alias} source={source} inspected={inspected} onInspect={onInspect}>{contents => contents.roles.length ? <div className="team-picker">{contents.roles.map(role => <label key={role.path} className="schema-check"><input type="checkbox" aria-label={`Role ${role.name} from ${source.alias}`} checked={selectedIndex(form.roles.items, source.alias, role.path) >= 0} disabled={disabled} onChange={event => onForm(withRole(form, { name: role.name, source: source.alias, path: role.path }, event.target.checked))} /><span>{role.name} <span className="description">{role.path}{role.harnesses.length ? ` — ${role.harnesses.join(", ")}` : ""}</span></span></label>)}</div> : <p className="description">No role.</p>}</SourceGroup>)}
      </>}
    </fieldset>
    <fieldset className="schema-object" aria-label="Harnesses"><legend>Harnesses</legend>
      <label className="schema-check"><input type="checkbox" checked={form.harnesses.inherit} disabled={disabled} onChange={event => onForm(withInherit(form, "harnesses", event.target.checked, inheritedHarnesses))} />Use the default harnesses</label>
      {form.harnesses.inherit ? <p className="description">{inheritedHarnesses.map(h => h.name).join(", ") || "The defaults have no harnesses."}</p> : <>
        <ul className="state-list">{form.harnesses.items.map(h => <li key={`${h.source}:${h.path}`}>{h.name} <span className="description"><code>{h.source}</code>:{h.path}</span> <button type="button" className="text-button" disabled={disabled} onClick={() => onForm(withHarness(form, h, false))}>Remove</button></li>)}</ul>
        {available.map(source => <SourceGroup key={source.alias} source={source} inspected={inspected} onInspect={onInspect}>{contents => contents.harnesses.length ? <div className="team-picker">{contents.harnesses.map(h => <label key={h.path} className="schema-check"><input type="checkbox" aria-label={`Harness ${h.name} from ${source.alias}`} checked={selectedIndex(form.harnesses.items, source.alias, h.path) >= 0} disabled={disabled} onChange={event => onForm(withHarness(form, { name: h.name, source: source.alias, path: h.path }, event.target.checked))} /><span>{h.name} <span className="description">{h.path}</span></span></label>)}</div> : <p className="description">No harness.</p>}</SourceGroup>)}
      </>}
    </fieldset>
  </div>;
}

function TasksStep({ form, available, inspected, configured, inheritedHarnesses, disabled, onInspect, onForm }: {
  form: TeamForm; available: Available[]; inspected: Inspected; configured: ViewTask[]; inheritedHarnesses: Selection[]; disabled: boolean;
  onInspect: (source: Available) => void; onForm: (form: TeamForm) => void;
}) {
  if (form.roles.inherit) return <div className="wizard-step"><p className="description">The team uses the default roles, with every task each declares. To choose tasks and schedules, select the roles in Roles and harnesses.</p></div>;
  return <div className="wizard-step">{form.roles.items.map((role, index) => {
    const offeredRole = inspected[role.source]?.contents?.roles.find(item => item.path === role.path);
    const offered = offeredRole?.tasks;
    const mine = configured.filter(task => task.role === role.name);
    // One switch per harness only when the role's tasks render on two or more (#1668).
    const harnesses = roleHarnesses(form, role, offeredRole, inheritedHarnesses, mine);
    const rows = taskRows(role, offered, mine, harnesses.length > 1 ? harnesses : []);
    const known = rows.map(row => row.name);
    const source = available.find(item => item.alias === role.source);
    return <fieldset key={`${role.source}:${role.path}`} className="schema-object" aria-label={`Tasks of ${role.name}`}><legend>{role.name}</legend>
      <label className="schema-check"><input type="checkbox" checked={role.tasks.all} disabled={disabled} onChange={event => onForm(event.target.checked ? withAllTasks(form, index) : withRolePatch(form, index, { tasks: { all: false, items: known.map(name => ({ name, suspend: null })) } }))} />Every task the role declares, with the role's schedules</label>
      {!offered && source && <div className="connector-actions"><button type="button" className="subtle-button" disabled={disabled || inspected[role.source]?.loading} onClick={() => onInspect(source)}>{inspected[role.source]?.loading ? "Fetching…" : "Show the role's tasks"}</button></div>}
      {rows.length ? <div className="table-scroll"><table aria-label={`Tasks of ${role.name} table`}><thead><tr><th>Task</th><th>Selected</th><th>Schedule</th><th>Cron</th><th>Arguments</th></tr></thead><tbody>
        {rows.map(row => <tr key={row.name}>
          <td>{row.name}</td>
          <td><input type="checkbox" aria-label={`Select ${role.name} task ${row.name}`} checked={row.selected} disabled={disabled} onChange={event => onForm(withTask(form, index, row.name, { selected: event.target.checked }, known))} /></td>
          <td><select aria-label={`Schedule of ${role.name} task ${row.name}`} value={row.switch} disabled={disabled || !row.selected} onChange={event => onForm(withTask(form, index, row.name, { switch: event.target.value }, known))}>
            <option value="role">The role's ({row.roleSchedule ? row.roleSuspend ? "off" : "on" : "none"})</option><option value="on">On</option><option value="off">Off</option>
          </select>
          {row.harnesses.length > 0 && <ul className="state-list" aria-label={`Schedule of ${role.name} task ${row.name} per harness`}>{row.harnesses.map(h => <li key={h.name}><label>{h.name}{" "}
            <select aria-label={`Schedule of ${role.name} task ${row.name} on ${h.name}`} value={h.switch} disabled={disabled || !row.selected} onChange={event => onForm(withTask(form, index, row.name, { harness: h.name, switch: event.target.value }, known))}>
              <option value="same">Same as the task</option><option value="on">On</option><option value="off">Off</option>
            </select></label>{h.schedule && <span className="description"> {h.schedule}</span>}</li>)}</ul>}</td>
          <td><input aria-label={`Cron of ${role.name} task ${row.name}`} value={row.schedule} disabled={disabled || !row.selected} placeholder={row.roleSchedule || "@daily"} spellCheck={false} onChange={event => onForm(withTask(form, index, row.name, { schedule: event.target.value }, known))} /></td>
          <td><input aria-label={`Arguments of ${role.name} task ${row.name}`} value={row.args} disabled={disabled || !row.selected} placeholder={row.roleArgs} spellCheck={false} onChange={event => onForm(withTask(form, index, row.name, { args: event.target.value }, known))} /></td>
        </tr>)}
      </tbody></table></div> : <p className="description">No task is known for this role yet{offered ? ": it declares none" : ""}.</p>}
    </fieldset>;
  })}</div>;
}

// identityNames are the stored identities of a connector a team can name (realm default, no space), plus the
// current choice when it names another.
function identityNames(secrets: unknown[] | undefined, connector: string, current: string): string[] {
  const identities = identitiesFor(secrets, connector).filter(identity => identity.realm === "default" && !identity.space);
  return [...new Set([...identities.map(identity => identity.name), ...(current ? [current] : [])])];
}

// RoleAccess is the Credentials step's per-role connector switches (#1649, AC6): one row per selected role and
// connector a team can give a role. While the role list is inherited the rows show the inherited roles, disabled.
function RoleAccess({ form, connectors, secrets, inheritedRoles, viewRows, disabled, onForm }: {
  form: TeamForm; connectors: Connector[] | undefined; secrets: unknown[] | undefined; inheritedRoles: Selection[]; viewRows: RoleConnectorView[]; disabled: boolean; onForm: (form: TeamForm) => void;
}) {
  const inherited = form.roles.inherit;
  const roles: { name: string; connectors?: FormRole["connectors"] }[] = inherited ? inheritedRoles.map(role => ({ name: role.name })) : form.roles.items;
  return <fieldset className="schema-object" aria-label="Per-role access">
    <legend>Per-role access</legend>
    <p className="description">A connector a role's cells get although the role does not ask for it, such as GitHub access for majordomo's health sweep. Turning one on adds it to the role's entry in the team; a running cell keeps what it started with until it is restarted.</p>
    {inherited && <p className="description" role="note">Declare the team's roles to change per-role access.</p>}
    {!roles.length ? <p className="description">No role is selected.</p> : <div className="table-scroll"><table aria-label="Per-role connectors"><thead><tr><th>Role</th><th>Connector</th><th>Enabled</th><th>Identity</th><th>Resolves to</th></tr></thead><tbody>
      {roles.flatMap((role, index) => roleConnectorRows(role, connectors, viewRows).map(row => {
        const off = disabled || inherited;
        return <tr key={`${role.name}/${row.name}`}>
          <td><code>{role.name}</code></td>
          <td>{row.title}</td>
          <td>{row.required ? <span className="description">Required by the role</span> : <label className="schema-check"><input type="checkbox" aria-label={`${row.title} for ${role.name}`} checked={row.enabled} disabled={off} onChange={event => onForm(withRoleConnector(form, index, row.name, { enabled: event.target.checked }))} />{row.enabled ? "On" : "Off"}</label>}</td>
          <td>{row.required ? "—" : <select aria-label={`Identity of ${row.name} for ${role.name}`} value={row.identity} disabled={off || !row.enabled} onChange={event => onForm(withRoleConnector(form, index, row.name, { identity: event.target.value }))}><option value="">Default</option>{identityNames(secrets, row.name, row.identity).map(name => <option key={name} value={name}>{name}</option>)}</select>}</td>
          <td>{row.states.length ? <ul className="state-list">{row.states.map(state => <li key={state.config}><span className="mono-id">{state.config}</span>: {state.fill}</li>)}</ul> : <span className="description">Not rendered</span>}</td>
        </tr>;
      }))}
    </tbody></table></div>}
  </fieldset>;
}

function CredentialsStep({ form, missing, inheritedRoles, viewRows, disabled, onForm }: { form: TeamForm; missing: string[]; inheritedRoles: Selection[]; viewRows: RoleConnectorView[]; disabled: boolean; onForm: (form: TeamForm) => void }) {
  const connectors = useResource<Connector[]>("/api/connectors", 0);
  const secrets = useResource<unknown[]>("/api/catalog/secrets", 0);
  return <div className="wizard-step">
    <label className="schema-check"><input type="checkbox" checked={form.connectors.inherit} disabled={disabled} onChange={event => onForm(withConnectorsInherit(form, event.target.checked))} />Use the defaults' connector choices</label>
    {!form.connectors.inherit && <>
      {connectors.error && <ErrorNotice error={connectors.error} kind="Connectors" />}
      <div className="table-scroll"><table aria-label="Connector choices"><thead><tr><th>Connector</th><th>Method</th><th>Identity</th></tr></thead><tbody>
        {(connectors.data ?? []).map(connector => {
          const choice = form.connectors.items[connector.name] ?? {};
          const identities = identitiesFor(secrets.data, connector.name).filter(identity => identity.realm === "default" && !identity.space);
          return <tr key={connector.name}>
            <td>{connector.title || connector.name}</td>
            <td><select aria-label={`Method of ${connector.name}`} value={choice.method ?? ""} disabled={disabled} onChange={event => onForm(withConnector(form, connector.name, { method: event.target.value }))}><option value="">Default</option>{connector.methods.map(method => <option key={method.name} value={method.name}>{method.name}</option>)}</select></td>
            <td><select aria-label={`Identity of ${connector.name}`} value={choice.identity ?? ""} disabled={disabled} onChange={event => onForm(withConnector(form, connector.name, { identity: event.target.value }))}><option value="">From the scope defaults</option>{[...new Set([...identities.map(identity => identity.name), ...(choice.identity ? [choice.identity] : [])])].map(name => <option key={name} value={name}>{name}</option>)}</select></td>
          </tr>;
        })}
      </tbody></table></div>
    </>}
    <RoleAccess form={form} connectors={connectors.data} secrets={secrets.data} inheritedRoles={inheritedRoles} viewRows={viewRows} disabled={disabled} onForm={onForm} />
    <p className="description">Secret bindings (<code>spec.secrets</code>) and a role's <code>needs.secrets</code> are kept as they are; edit them as YAML.</p>
    {!!missing.length && <><h3>Missing secrets in the last preview</h3><ul className="state-list" aria-label="Missing secrets">{missing.map(item => <li key={item} className="mono-id">{item}</li>)}</ul></>}
  </div>;
}

function YamlTab({ name, document, onChanged }: { name: string; document: string; onChanged: () => void }) {
  const [draft, setDraft] = useState(document);
  const [pending, setPending] = useState("");
  const [error, setError] = useState<Failure>();
  const [preview, setPreview] = useState<TeamOutcome>();
  const { notify } = useNotify();
  async function run(action: "preview" | "apply") {
    if (pending) return;
    setPending(action); setError(undefined);
    try {
      const outcome = await sendJSON<TeamOutcome>(action === "preview" ? teamsAPI("preview") : teamsAPI(), editPayload(name, draft));
      if (action === "preview") { setPreview(outcome); return; }
      notify({ key: `team-apply:${name}`, tone: "success", message: `Team ${name} applied.` });
      onChanged();
      window.location.hash = teamsLink(name);
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(""); }
  }
  return <section className="panel" aria-label="Team declaration">
    <div className="panel-heading"><h2>Declaration</h2>{draft !== document && <span className="badge">Edited</span>}</div>
    <div className="panel-body creation-form">
      <label className="schema-field team-yaml"><span>Team document</span><textarea aria-label="Team declaration YAML" value={draft} spellCheck={false} disabled={!!pending} onChange={event => { setDraft(event.target.value); setPreview(undefined); }} /><small>The whole declaration, as <code>kuke team apply -f</code>: secret bindings, a role's connectors, a source's git block.</small></label>
      <div className="connector-actions">
        <button type="button" disabled={!!pending} onClick={() => void run("preview")}>{pending === "preview" ? "Previewing…" : "Preview"}</button>
        <button type="button" className="primary-action" disabled={!!pending || !preview} onClick={() => void run("apply")}>{pending === "apply" ? "Applying…" : "Apply"}</button>
      </div>
      {error && <ErrorNotice error={error} kind="Team" />}
      {preview && <PlanView plan={preview.plan} title="Preview" />}
    </div>
  </section>;
}

export function TeamWizard({ mode, team, onChanged }: { mode: Mode; team?: TeamView; onChanged: () => void }) {
  const steps = stepsFor(mode);
  const name = team?.summary.name ?? "";
  const sources = useResource<Sources>("/api/sources", 0);
  const [tab, setTab] = useState<"form" | "yaml">("form");
  const [step, setStep] = useState(0);
  const [project, setProject] = useState<ProjectInput>(emptyProject());
  const [draft, setDraft] = useState<Draft>();
  const [form, setForm] = useState<TeamForm | undefined>(team?.form);
  const [inspected, setInspected] = useState<Inspected>({});
  const [pending, setPending] = useState("");
  const [error, setError] = useState<Failure>();
  const [preview, setPreview] = useState<TeamOutcome>();
  const [replace, setReplace] = useState(false);
  const { notify } = useNotify();
  const key = steps[step].key;
  const errors = stepErrors(key, { project, draft, form });
  const available = availableSources(form, sources.data?.sources);
  const inheritedRoles = team?.view?.roles.inherited ? team.view.roles.items : sources.data?.defaultRoles ?? [];
  const inheritedHarnesses = team?.view?.harnesses.inherited ? team.view.harnesses.items : sources.data?.defaultHarnesses ?? [];
  const configured = team?.view?.tasks ?? [];
  const changeForm = (next: TeamForm) => { setForm(next); setPreview(undefined); setReplace(false); };

  async function readRepository() {
    setPending("draft"); setError(undefined);
    try {
      const read = await sendJSON<Draft>(teamsAPI("draft"), draftPayload(project));
      setDraft(read); setForm(read.form); setPreview(undefined);
      const seeded: Inspected = {};
      for (const source of read.sources) if (source.contents) seeded[source.alias] = { contents: source.contents };
      setInspected(seeded);
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(""); }
  }
  async function inspect(source: Available) {
    setInspected(old => ({ ...old, [source.alias]: { ...old[source.alias], loading: true, error: undefined } }));
    const request = inspectRequest(source);
    try {
      const contents = await sendJSON<SourceContents>(request.path, request.body);
      setInspected(old => ({ ...old, [source.alias]: { contents } }));
    } catch (caught) { setInspected(old => ({ ...old, [source.alias]: { error: toFailure(caught) } })); }
  }
  function body(force = false) {
    return mode === "new" ? createBody(project, draft, form!, force) : editBody(name, form!);
  }
  async function send(dryRun: boolean, force = false) {
    if (!form || pending) return;
    setPending(dryRun ? "preview" : "apply"); setError(undefined);
    try {
      const outcome = await sendJSON<TeamOutcome>(dryRun ? teamsAPI("preview") : teamsAPI(), body(force));
      if (dryRun) { setPreview(outcome); return; }
      const added = outcome.result?.team ?? name;
      notify({ key: `team-${mode}:${added}`, tone: "success", message: mode === "new" ? `Team ${added} added.` : `Team ${added} applied.` });
      onChanged();
      window.location.hash = teamsLink(added);
    } catch (caught) {
      const failure = toFailure(caught);
      if (mode === "new" && needsForce(failure)) setReplace(true);
      setError(failure);
    } finally { setPending(""); }
  }

  const disabled = !!pending;
  const stepNav = <ol className="wizard-steps" aria-label="Steps">{steps.map((item, i) => <li key={item.key}><button type="button" className="text-button" aria-current={i === step ? "step" : undefined} disabled={disabled || (i > step && (!form || i > step + 1 || !!errors.length))} onClick={() => setStep(i)}>{i + 1}. {item.title}</button></li>)}</ol>;
  return <>
    {mode === "edit" && <nav className="daemon-tabs" aria-label="Edit team">
      <a href={teamsLink(name, "edit")} aria-current={tab === "form" ? "page" : undefined} onClick={event => { event.preventDefault(); setTab("form"); }}>Form</a>
      <a href={teamsLink(name, "edit")} aria-current={tab === "yaml" ? "page" : undefined} onClick={event => { event.preventDefault(); setTab("yaml"); }}>Edit as YAML</a>
    </nav>}
    {mode === "edit" && tab === "yaml" ? <YamlTab name={name} document={team?.document ?? ""} onChanged={onChanged} /> : <section className="panel team-wizard" aria-label={mode === "new" ? "New team wizard" : "Edit team form"}>
      <div className="panel-heading"><h2>{steps[step].title}</h2><span className="badge">Step {step + 1} of {steps.length}</span></div>
      <div className="panel-body creation-form">
        {stepNav}
        {mode === "edit" && !form && <ErrorNotice error={{ code: "invalid", message: team?.problem ?? "The team's form could not be read; edit it as YAML." }} kind="Team" />}
        {key === "project" && <ProjectStep project={project} sources={sources.data} draft={draft} pending={pending === "draft"} error={pending ? undefined : error} onChange={next => { setProject(old => ({ ...old, ...next })); setDraft(undefined); setForm(undefined); setError(undefined); }} onRead={() => void readRepository()} />}
        {form && key === "sources" && <SourcesStep mode={mode} form={form} draft={draft} sources={sources.data} disabled={disabled} onForm={changeForm} />}
        {form && key === "roles" && <RolesStep form={form} available={available} inspected={inspected} inheritedRoles={inheritedRoles} inheritedHarnesses={inheritedHarnesses} disabled={disabled} onInspect={source => void inspect(source)} onForm={changeForm} />}
        {form && key === "tasks" && <TasksStep form={form} available={available} inspected={inspected} configured={configured} inheritedHarnesses={inheritedHarnesses} disabled={disabled} onInspect={source => void inspect(source)} onForm={changeForm} />}
        {form && key === "credentials" && <CredentialsStep form={form} missing={preview?.plan.missingSecrets ?? []} inheritedRoles={inheritedRoles} viewRows={team?.view?.connectors.roles ?? []} disabled={disabled} onForm={changeForm} />}
        {form && key === "review" && <div className="wizard-step">
          <p className="description">{mode === "new" ? <>Adds team <code>{form.name || draft?.name}</code> from <code>{form.project?.url || form.project?.stored}</code> ({refLabel({ [form.project?.refKind ?? "branch"]: form.project?.ref })}).</> : <>Applies the form to team <code>{name}</code>.</>} The apply composes from scratch again: a branch may have moved since the preview, and the apply result is authoritative.</p>
          <div className="connector-actions">
            <button type="button" disabled={disabled} onClick={() => void send(true)}>{pending === "preview" ? "Previewing…" : preview ? "Preview again" : "Preview"}</button>
            <button type="button" className="primary-action" disabled={disabled || !preview} onClick={() => void send(false)}>{pending === "apply" ? mode === "new" ? "Adding…" : "Applying…" : mode === "new" ? "Add team" : "Apply"}</button>
          </div>
          {error && <ErrorNotice error={error} kind="Team" />}
          {replace && <div className="error" role="alert"><p>A team of this name is stored. Reapply it from its team page to recompose it with its stored declaration, or replace the declaration with this one.</p><button type="button" className="danger-action" disabled={disabled} onClick={() => void send(false, true)}>Replace the stored declaration</button></div>}
          {preview && <OutcomeNotice outcome={preview} />}
          {preview && <PlanView plan={preview.plan} title="Preview" />}
        </div>}
        {key !== "review" && key !== "project" && error && <ErrorNotice error={error} kind="Team" />}
        <Errors errors={key === "project" ? [] : errors} />
        <div className="form-actions">
          <button type="button" disabled={disabled || step === 0} onClick={() => setStep(step - 1)}>Back</button>
          {step < steps.length - 1 && <button type="button" className="primary-action" disabled={disabled || !!errors.length || !form} onClick={() => setStep(step + 1)}>Next</button>}
          {mode === "edit" && <a className="document-action" href={teamsLink(name)}>Cancel</a>}
        </div>
      </div>
    </section>}
  </>;
}
