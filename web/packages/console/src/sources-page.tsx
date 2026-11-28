import { useState, type FormEvent } from "react";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import { sendJSON, toFailure } from "./connector-ui";
import { GitIdentitySelect, RefFields } from "./team-plan";
import { InitializePrompt } from "./initialize";
import { defaultsAPI, inheritingNote, isDefault, refLabel, sourceErrors, sourceItems, sourcePayload, sourcesAPI, sourceTeams, taskLabel, toggledDefaults, usesLabel } from "./teams.mjs";
import type { DefaultsChange, SourceChange, SourceContents, SourceForm, Sources, StoredSource } from "./teams.mjs";
import { navigate } from "./router";
import { sourcesLink, teamsLink } from "./routes";

// The Sources page (#1644, AC6), a Manage entry of its own: a stored Source
// belongs to no team. It is kuke get sources with the teams that use each,
// kuke create source, kuke source show with the default toggles of kuke team
// defaults set, and kuke delete source.

const emptySource: SourceForm = { name: "", url: "", refKind: "branch", ref: "", identity: "" };

function NewSource({ onCreated }: { onCreated: (change: SourceChange) => void }) {
  const [form, setForm] = useState<SourceForm>(emptySource);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const set = (next: Partial<SourceForm>) => { setForm(old => ({ ...old, ...next })); setError(undefined); };
  const errors = sourceErrors(form);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending || errors.length) return;
    setPending(true); setError(undefined);
    try {
      const change = await sendJSON<SourceChange>(sourcesAPI(), sourcePayload(form));
      setForm(emptySource);
      onCreated(change);
    } catch (caught) { setError(toFailure(caught)); }
    finally { setPending(false); }
  }
  return <section className="panel" aria-label="New source">
    <div className="panel-heading"><h2>New source</h2></div>
    <div className="panel-body">
      <form className="creation-form" aria-label="New source form" method="post" autoComplete="off" onSubmit={submit}>
        <p className="description">Fetches the repository with the git identity, as the account running <code>kuke serve</code>, and stores it: <code>kuke create source</code>. Without a ref the remote's default branch is stored.</p>
        <div className="schema-fields">
          <label className="schema-field"><span>Name<b aria-label="required"> *</b></span><input value={form.name} disabled={pending} placeholder="agents" spellCheck={false} onChange={event => set({ name: event.target.value })} /></label>
          <label className="schema-field"><span>Address<b aria-label="required"> *</b></span><input value={form.url} disabled={pending} placeholder="github.com/owner/repo" spellCheck={false} onChange={event => set({ url: event.target.value })} /></label>
          <RefFields kind={form.refKind} value={form.ref} disabled={pending} onChange={(refKind, ref) => set({ refKind, ref })} />
          <GitIdentitySelect value={form.identity} disabled={pending} onChange={identity => set({ identity })} />
        </div>
        {(form.name || form.url) && !!errors.length && <ul className="setup-error">{errors.map(message => <li key={message}>{message}</li>)}</ul>}
        <div className="connector-actions"><button type="submit" disabled={pending || !!errors.length}>{pending ? "Storing…" : "Store source"}</button></div>
        {error && <ErrorNotice error={error} kind="Source" />}
      </form>
    </div>
  </section>;
}

function SourceList({ sources, onChanged }: { sources: StoredSource[]; onChanged: () => void }) {
  return <section className="panel" aria-label="Sources list">
    <div className="panel-heading"><h2>Sources</h2><span className="badge">{sources.length} stored</span></div>
    <div className="table-scroll"><table><thead><tr><th>Source</th><th>Address</th><th>Ref</th><th>Git identity</th><th>Used by</th></tr></thead><tbody>
      {sources.map(source => <tr key={source.name}>
        <td><a className="resource-name" href={sourcesLink(source.name)}>{source.name}</a></td>
        <td className="mono-id">{source.url}</td><td>{refLabel(source)}</td><td>{source.identity || "Default"}</td>
        <td>{sourceTeams(source).map((team, i) => <span key={team}>{i > 0 && ", "}<a href={teamsLink(team)}>{team}</a></span>)}{source.usedBy.some(use => !use.team) && <>{sourceTeams(source).length ? ", " : ""}<span className="description">the defaults</span></>}{!source.usedBy.length && <span className="description">nothing</span>}</td>
      </tr>)}
    </tbody></table></div>
    {!sources.length && <div className="empty-state"><h3>No Source is stored</h3><p>Store one below, or initialize for the kukeon Source.</p><InitializePrompt onChanged={onChanged} subject="the kukeon Source with the kuke and default teams" /></div>}
  </section>;
}

// SourceDetail is one stored Source: what it offers, with the default
// toggles, the teams that use it, and Delete.
function SourceDetail({ name, sources, onChanged }: { name: string; sources: Sources; onChanged: () => void }) {
  const source = sources.sources.find(item => item.name === name);
  const [contents, setContents] = useState<SourceContents>();
  const [pending, setPending] = useState("");
  const [error, setError] = useState<Failure>();
  const [change, setChange] = useState<DefaultsChange>();
  const { notify } = useNotify();
  async function act<T>(action: string, path: string, body: unknown, done: (value: T) => void) {
    if (pending) return;
    setPending(action); setError(undefined);
    try { done(await sendJSON<T>(path, body)); } catch (caught) { setError(toFailure(caught)); } finally { setPending(""); }
  }
  if (!source) return <ErrorNotice error={{ code: "not_found", message: `No Source ${name} is stored.` }} kind="Source" />;
  const items = sourceItems(contents);
  const toggled = (next: DefaultsChange) => { setChange(next); onChanged(); };
  return <>
    <section className="panel" aria-label="Source overview">
      <div className="panel-heading"><h2>Overview</h2><div className="connector-actions">
        <button type="button" disabled={!!pending} onClick={() => void act<SourceContents>("inspect", sourcesAPI(name, "inspect"), {}, setContents)}>{pending === "inspect" ? "Fetching…" : contents ? "Refresh" : "Show contents"}</button>
        <button type="button" className="danger-action" disabled={!!pending} onClick={() => void act<{ deleted: boolean }>("delete", sourcesAPI(name, "delete"), {}, () => { notify({ key: `source-delete:${name}`, tone: "success", message: `Source ${name} deleted.` }); onChanged(); navigate(sourcesLink()); })}>{pending === "delete" ? "Deleting…" : "Delete"}</button>
      </div></div>
      <div className="panel-body"><dl className="details-grid">
        <dt>Address</dt><dd className="mono-id">{source.url}</dd>
        <dt>Ref</dt><dd>{refLabel(source)}</dd>
        <dt>Git identity</dt><dd>{source.identity || "Default"}</dd>
        <dt>Used by</dt><dd>{source.usedBy.length ? <ul className="state-list" aria-label="Used by">{source.usedBy.map(use => <li key={usesLabel(use)}>{use.team ? <><a href={teamsLink(use.team)}>team {use.team}</a> <span className="description">({use.as} {use.name})</span></> : usesLabel(use)}</li>)}</ul> : "nothing"}</dd>
      </dl></div>
    </section>
    {error && <ErrorNotice error={error} kind="Source" />}
    {change && <div className="health-sweep-started" role="status"><p>Defaults {change.action}. {inheritingNote(change)}</p></div>}
    {contents && <section className="panel" aria-label="Source contents">
      <div className="panel-heading"><h2>Contents</h2>{contents.commit && <span className="badge">at {contents.commit.slice(0, 12)}</span>}</div>
      <div className="panel-body">
        <div className="table-scroll"><table aria-label={`Roles in ${name}`}><thead><tr><th>Role</th><th>Path</th><th>Harnesses</th><th>Tasks</th><th>Default</th></tr></thead><tbody>
          {items.roles.map(role => <tr key={role.path}><td>{role.name}</td><td className="mono-id">{role.path}</td><td>{role.harnesses.join(", ") || "any"}</td><td>{role.tasks.map(taskLabel).join(", ") || "—"}</td>
            <td><input type="checkbox" aria-label={`Default role ${role.name}`} checked={isDefault(sources, "role", name, role.path)} disabled={!!pending} onChange={event => void act<DefaultsChange>("toggle", defaultsAPI, toggledDefaults(sources, "role", { name: role.name, source: name, path: role.path }, event.target.checked), toggled)} /></td></tr>)}
        </tbody></table></div>
        {!items.roles.length && <p className="description">No role.</p>}
        <div className="table-scroll"><table aria-label={`Harnesses in ${name}`}><thead><tr><th>Harness</th><th>Path</th><th>Default</th></tr></thead><tbody>
          {items.harnesses.map(harness => <tr key={harness.path}><td>{harness.name}</td><td className="mono-id">{harness.path}</td>
            <td><input type="checkbox" aria-label={`Default harness ${harness.name}`} checked={isDefault(sources, "harness", name, harness.path)} disabled={!!pending} onChange={event => void act<DefaultsChange>("toggle", defaultsAPI, toggledDefaults(sources, "harness", { name: harness.name, source: name, path: harness.path }, event.target.checked), toggled)} /></td></tr>)}
        </tbody></table></div>
        {!items.harnesses.length && <p className="description">No harness.</p>}
      </div>
    </section>}
  </>;
}

export function SourcesPage({ name, revision, onChanged }: { name?: string; revision: number; onChanged: () => void }) {
  const [reads, setReads] = useState(0);
  const [created, setCreated] = useState<SourceChange>();
  const refresh = () => { setReads(value => value + 1); onChanged(); };
  const sources = useResource<Sources>(sourcesAPI(), revision + reads);
  if (sources.error) return <ErrorNotice error={sources.error} kind="Sources" />;
  if (!sources.data) return <p role="status">Loading sources…</p>;
  if (name) return <SourceDetail key={name} name={name} sources={sources.data} onChanged={refresh} />;
  return <>
    <SourceList sources={sources.data.sources} onChanged={refresh} />
    {created && <div className="health-sweep-started" role="status"><p>Source <a href={sourcesLink(created.source.name)}>{created.source.name}</a> {created.action}: {created.source.url}, {refLabel(created.source)}; offers {created.discovered?.roles.length ?? 0} roles and {created.discovered?.harnesses.length ?? 0} harnesses.</p></div>}
    <NewSource onCreated={change => { setCreated(change); refresh(); }} />
  </>;
}
