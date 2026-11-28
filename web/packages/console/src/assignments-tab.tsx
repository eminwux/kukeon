import { useEffect, useMemo, useState } from "react";
import { ErrorNotice, useResource, type Failure } from "./request";
import { useNotify } from "./notifications";
import { apiFetch } from "@kukeon/api-client";
import { identitiesLink, sendJSON, toFailure } from "./connector-ui";
import { PlanView } from "./team-plan";
import { editBody } from "./team-form.mjs";
import { defaultsAPI, teamsAPI } from "./teams.mjs";
import type { Sources, TeamForm, TeamOutcome, TeamSummary, TeamView } from "./teams.mjs";
import { sharedLoginNote } from "./connectors.mjs";
import type { Connector } from "./connectors.mjs";
import {
  affectedRoles, cellState, cellText, choiceOf, defaultsBody, gridRows, harnessConnectors, identityOptions, inheritingTeams, keepsLoginMethod,
  nextDefaults, pickIdentity, pickInherit, pickMethod, pickOwn, pickSystem, resolve, roleRuns, sharedRoles, systemNotation, withRoleChoice, withTeamChoice,
} from "./assignments.mjs";
import type { Choice, GridRow } from "./assignments.mjs";

// The Assignments tab of the Identities page (#1790): which identity each team and role authenticates a
// harness with. Rows are All teams (the TeamDefaults), then each team and its roles; columns are the
// connectors that authenticate a harness. A cell writes the existing connector fields only, and a change
// is previewed — every team it recomposes — before it is applied.

type Change = { row: GridRow; connector: Connector; pick: string; choice: Choice | undefined; text: string };
type PreviewItem = { team: string; form?: TeamForm; outcome?: TeamOutcome; error?: Failure };

// useTeams reads every stored team's view and form: GET /api/teams, then GET /api/teams/<name>.
function useTeams(revision: number): { teams?: TeamView[]; error?: Failure } {
  const list = useResource<{ teams: TeamSummary[] }>("/teams", revision);
  const [state, setState] = useState<{ teams?: TeamView[]; error?: Failure }>({});
  const listed = list.data;
  useEffect(() => {
    if (!listed) return undefined;
    let live = true;
    void Promise.all(listed.teams.map(async summary => {
      const response = await apiFetch(`${teamsAPI(summary.name)}`, { headers: { Accept: "application/json" } });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw body.error ?? { code: "http_error", message: `HTTP ${response.status}` };
      return body as TeamView;
    })).then(teams => { if (live) setState({ teams }); }, caught => { if (live) setState({ error: toFailure(caught) }); });
    return () => { live = false; };
  }, [listed]);
  return list.error ? { error: list.error } : state;
}

function rowName(row: GridRow): string { return row.kind === "defaults" ? "All teams" : row.kind === "team" ? row.label : `${row.team}/${row.role}`; }

function Cell({ row, connector, defaults, team, system, logins, disabled, onPick }: {
  row: GridRow; connector: Connector; defaults: Record<string, Choice>; team: TeamView | undefined; system: string; logins: unknown[] | undefined; disabled: boolean;
  onPick: (change: Change) => void;
}) {
  const title = connector.title || connector.name;
  if (row.kind === "role") {
    const role = team?.view?.roles.items.find(item => item.name === row.role);
    if (!roleRuns(role, team?.view?.harnesses.items, connector)) return <td className="assignment-cell"><span className="description">—</span></td>;
  }
  const state = cellState({ row, connector, defaults, team, system });
  // What Inherit means here: the level above's choice, as compose resolves it.
  const above = row.kind === "team" ? resolve({ name: connector.name, defaults, team: { inherit: true, items: {} } })
    : row.kind === "role" ? resolve({ name: connector.name, defaults, team: { inherit: !!team?.form?.connectors.inherit, items: team?.form?.connectors.items ?? {} } }) : undefined;
  const inheritLabel = row.kind === "defaults" ? "Not set — own per role" : `Inherit → ${cellText(above, connector, system)}`;
  const tokenMethods = connector.methods.filter(method => !keepsLoginMethod(connector, method.name));
  const options = identityOptions(logins, connector, system);
  const pickedState = state.own?.state ?? "";
  const label = `${rowName(row)} ${title}`;
  const describe = (pick: string, choice: Choice | undefined) => pick === pickInherit ? inheritLabel : cellText({ found: true, choice: choice ?? {}, from: "" }, connector, system);
  const pick = (value: string, stateValue = pickedState) => {
    const choice = value === pickInherit ? undefined : choiceOf(value, connector, system, stateValue);
    onPick({ row, connector, pick: value, choice, text: describe(value, choice) });
  };
  return <td className="assignment-cell">
    <select aria-label={label} value={state.pick} disabled={disabled} onChange={event => pick(event.target.value, "")}>
      <option value={pickInherit}>{inheritLabel}</option>
      {system && <option value={pickSystem}>System default</option>}
      <option value={pickOwn}>Own per role</option>
      {options.map(item => <option key={item.value} value={`${pickIdentity}${item.value}`}>{item.label}{item.state ? ` — ${item.state}` : ""}</option>)}
      {state.pick.startsWith(pickIdentity) && !options.some(item => `${pickIdentity}${item.value}` === state.pick) && <option value={state.pick}>{state.pick.slice(pickIdentity.length)}</option>}
      {tokenMethods.map(method => <option key={method.name} value={`${pickMethod}${method.name}`}>{method.name} method</option>)}
    </select>
    {state.pick.startsWith(pickMethod) && <select aria-label={`${label} harness state`} value={pickedState} disabled={disabled} onChange={event => pick(state.pick, event.target.value)}>
      <option value="">persistent</option><option value="ephemeral">ephemeral</option>
    </select>}
    {row.kind !== "defaults" && state.pick !== pickInherit && <span className="description assignment-resolved"> → {cellText(state.resolved, connector, system)}</span>}
  </td>;
}

export function AssignmentsTab({ revision, onChanged }: { revision: number; onChanged: () => void }) {
  const [reads, setReads] = useState(0);
  const catalog = useResource<Connector[]>("/connectors", revision);
  const logins = useResource<unknown[]>("/connectors/logins", revision + reads);
  const sources = useResource<Sources>("/sources", revision + reads);
  const { teams, error } = useTeams(revision + reads);
  const [change, setChange] = useState<Change>();
  const [preview, setPreview] = useState<PreviewItem[]>();
  const [pending, setPending] = useState("");
  const [failure, setFailure] = useState<Failure>();
  const [warnings, setWarnings] = useState<string[]>([]);
  const { notify } = useNotify();
  const columns = harnessConnectors(catalog.data);
  const defaults = (sources.data?.defaultForm?.connectors ?? {}) as Record<string, Choice>;
  const rows = useMemo(() => gridRows(defaults, teams), [defaults, teams]);
  const byName = new Map((teams ?? []).map(team => [team.summary.name, team]));
  const reset = () => { setChange(undefined); setPreview(undefined); setFailure(undefined); };

  // The teams a change recomposes, each with the form it is previewed and applied with.
  function planned(next: Change): PreviewItem[] {
    if (next.row.kind === "defaults") {
      const items = nextDefaults(defaults, next.connector.name, next.choice);
      return inheritingTeams(teams).map(name => ({ team: name, form: { ...byName.get(name)!.form!, connectors: { inherit: false, items } } }));
    }
    const team = byName.get(next.row.team ?? "");
    if (!team?.form) return [];
    const form = next.row.kind === "team" ? withTeamChoice(team.form, next.connector.name, next.choice, defaults)
      : withRoleChoice(team.form, next.row.role ?? "", next.connector.name, next.choice, sources.data?.defaultForm?.roles);
    return [{ team: team.summary.name, form }];
  }
  async function runPreview() {
    if (!change || pending) return;
    setPending("preview"); setFailure(undefined);
    const items = planned(change);
    const out = await Promise.all(items.map(async item => {
      try { return { ...item, outcome: await sendJSON<TeamOutcome>(teamsAPI("preview"), editBody(item.team, item.form!)) }; }
      catch (caught) { return { ...item, error: toFailure(caught) }; }
    }));
    setPreview(out);
    setPending("");
  }
  async function apply() {
    if (!change || !preview || pending) return;
    setPending("apply"); setFailure(undefined);
    const found: string[] = [];
    try {
      if (change.row.kind === "defaults") {
        await sendJSON(defaultsAPI, defaultsBody(change.connector.name, change.choice));
        for (const item of preview) {
          const outcome = await sendJSON<TeamOutcome>(teamsAPI(item.team, "reapply"), {});
          found.push(...(outcome.result?.warnings ?? []));
        }
      } else {
        for (const item of preview) {
          const outcome = await sendJSON<TeamOutcome>(teamsAPI(), editBody(item.team, item.form!));
          found.push(...(outcome.result?.warnings ?? []));
        }
      }
      notify({ key: `assignment:${change.row.key}:${change.connector.name}`, tone: "success", message: `${change.connector.title || change.connector.name} for ${rowName(change.row)} set to ${change.text}${preview.length ? `; recomposed ${preview.map(item => item.team).join(", ")}` : ""}.` });
      setWarnings(found);
      reset();
      setReads(value => value + 1);
      onChanged();
    } catch (caught) { setFailure(toFailure(caught)); }
    finally { setPending(""); }
  }

  const system = (connector: Connector) => systemNotation(logins.data, connector);
  const affected = change ? affectedRoles(teams, change.row, change.connector) : [];
  const identity = change?.choice?.identity ?? "";
  const shared = change && identity ? sharedRoles(logins.data, identity, affected) : [];
  const serverWarnings = (preview ?? []).flatMap(item => (item.outcome?.connectors ?? []).map(row => row.warning ?? "").filter(Boolean));
  const loading = catalog.loading || (!teams && !error) || (sources.loading && !sources.data);
  return <section className="panel identity-assignments" aria-label="Assignments">
    <div className="panel-heading"><h2>Assignments</h2><span className="badge">{teams?.length ?? 0} teams</span></div>
    <div className="panel-body">
      <p className="description">Which identity each team and role authenticates a harness with. A team that sets nothing keeps <code>{"<team>-<role>-<harness>-state"}</code> for each role (<strong>Own per role</strong>); <strong>Inherit</strong> takes the level above — All teams for a team, the team for a role. <strong>System default</strong> is the login identity of the <code>default</code> team's <code>agent</code> role. Login identities are listed on the <a href={identitiesLink()}>Identities</a> tab. Running cells keep their identity until they are restarted.</p>
      {catalog.error && <ErrorNotice error={catalog.error} kind="Connector" />}
      {error && <ErrorNotice error={error} kind="Team" />}
      {sources.error && <p className="stale">The defaults could not be read: {sources.error.message}</p>}
      {loading && <p role="status">Loading assignments…</p>}
      {warnings.map(item => <p key={item} className="connector-warning" role="note">{item}</p>)}
      {teams && catalog.data && (columns.length ? <div className="table-scroll"><table aria-label="Team identity assignments"><thead><tr><th scope="col">Team / role</th>{columns.map(connector => <th scope="col" key={connector.name}>{connector.title || connector.name}</th>)}</tr></thead><tbody>
        {rows.map(row => <tr key={row.key} className={`assignment-${row.kind}`} aria-label={rowName(row)}>
          <th scope="row">{row.kind === "role" ? <span className="assignment-role">{row.label}</span> : <strong>{row.label}</strong>}{row.system && <> <span className="badge">system</span></>}</th>
          {columns.map(connector => <Cell key={connector.name} row={row} connector={connector} defaults={defaults} team={row.team ? byName.get(row.team) : undefined} system={system(connector)} logins={logins.data} disabled={!!pending} onPick={next => { setChange(next); setPreview(undefined); setFailure(undefined); }} />)}
        </tr>)}
      </tbody></table></div> : <p>No connector authenticates a harness.</p>)}
      {change && <div className="assignment-change" role="region" aria-label="Pending change">
        <p><strong>{change.connector.title || change.connector.name}</strong> for <strong>{rowName(change.row)}</strong> → {change.text}.{" "}
          {change.row.kind === "defaults" ? <>Writes <code>connectors.{change.connector.name}</code> of the TeamDefaults and recomposes the teams that inherit it: {inheritingTeams(teams).join(", ") || "none"}.</> : change.row.kind === "team" ? <>Writes <code>spec.connectors.{change.connector.name}</code> of team {change.row.team}.</> : <>Writes <code>connectors.{change.connector.name}</code> of role {change.row.role} in team {change.row.team}.</>}</p>
        {shared.length > 1 && <p className="connector-warning" role="note">Login identity <code>{identity.split("@")[0]}</code> would be shared by roles {shared.join(", ")}. {sharedLoginNote}</p>}
        <div className="connector-actions">
          <button type="button" disabled={!!pending} onClick={() => void runPreview()}>{pending === "preview" ? "Previewing…" : "Preview"}</button>
          <button type="button" className="primary-action" disabled={!!pending || !preview || preview.some(item => item.error)} onClick={() => void apply()}>{pending === "apply" ? "Applying…" : "Apply"}</button>
          <button type="button" className="subtle-button" disabled={!!pending} onClick={reset}>Cancel</button>
        </div>
        {failure && <ErrorNotice error={failure} kind="Team" />}
        {preview && <>
          {!preview.length && <p className="description" role="status">No team inherits it: only the defaults change.</p>}
          {serverWarnings.map(item => <p key={item} className="connector-warning" role="note">{item}</p>)}
          {preview.map(item => item.error ? <div key={item.team} className="error" role="alert"><strong>Preview failed: {item.team}</strong><p>{item.error.message}</p></div>
            : item.outcome && <PlanView key={item.team} plan={item.outcome.plan} title={`Preview: ${item.team}`} />)}
        </>}
      </div>}
    </div>
  </section>;
}
