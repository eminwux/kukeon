import { useEffect, useState } from "react";
import { LoginButton } from "./connector-ui";
import { loginEmpty, type Connector } from "./connectors.mjs";
import { configLogin, harnessConnector } from "./onboarding.mjs";
import { displayNameError } from "./cells.mjs";
import { ErrorNotice, useResource } from "./request";
import { catalogPageLink, teamsLink } from "./routes";
import { InitializePrompt } from "./initialize";
import { apiFetch } from "@kukeon/api-client";

// The New agent page (#1692): pick Claude or Codex, optionally name the agent, and kukeon copies that
// harness's template Config (default-agent-<harness>, from the default team) into a new Config and
// starts its first cell. The page then opens the cell's terminal.

export type AgentTemplate = { harness: string; name: string; realm: string; space?: string; stack?: string; exists: boolean };
type Coordinates = { name: string; realm: string; space?: string; stack?: string };
export type CreatedAgent = { config: Coordinates & { labels?: Record<string, string>; annotations?: Record<string, string> }; cell: Coordinates };
type AgentFailure = { code?: string; message?: string; step?: string; config?: Coordinates; cell?: Coordinates };

const harnessTitles: Record<string, string> = { claude: "Claude", codex: "Codex" };
const titleOf = (harness: string) => harnessTitles[harness] ?? harness;
const scopeOf = (c: Coordinates) => [c.realm, c.space, c.stack, c.name].filter((part): part is string => !!part);
const stepText: Record<string, string> = { "create-cell": "creating its first cell", "start-cell": "starting its first cell" };

export function NewAgentPage({ revision, onCreated, onChanged }: { revision: number; onCreated: (agent: CreatedAgent) => void; onChanged: () => void }) {
  const templates = useResource<{ templates: AgentTemplate[] }>("/agents/templates", revision);
  const list = templates.data?.templates ?? [];
  const available = list.filter(template => template.exists);
  const [picked, setPicked] = useState("");
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<AgentFailure>();
  const harness = available.some(template => template.harness === picked) ? picked : available[0]?.harness ?? "";
  // The login the chosen harness's template logs in through (#1694): while it is empty, Log in first is
  // offered next to Create. The list is re-read while a login session runs.
  const catalog = useResource<Connector[]>("/connectors", revision);
  const [tick, setTick] = useState(0);
  const logins = useResource<unknown[]>("/connectors/logins", revision + tick);
  const loginRunning = (logins.data ?? []).some(item => !!(item as { session?: unknown } | null)?.session);
  useEffect(() => {
    if (!loginRunning) return undefined;
    const timer = window.setInterval(() => setTick(value => value + 1), 3000);
    return () => window.clearInterval(timer);
  }, [loginRunning]);
  const connector = harnessConnector(catalog.data, harness);
  const login = configLogin(logins.data, connector, available.find(template => template.harness === harness));
  const loginFirst = connector && login && (login.state === loginEmpty || login.session) ? login : undefined;
  const nameError = displayNameError(name);
  async function create() {
    if (!harness || pending || nameError) return;
    setPending(true); setFailure(undefined);
    try {
      const response = await apiFetch("/agents", { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify({ harness, ...(name.trim() ? { displayName: name.trim() } : {}) }) });
      let body: { error?: AgentFailure } & Partial<CreatedAgent> = {};
      try { body = await response.json(); } catch { /* Use the HTTP status below. */ }
      if (!response.ok) { setFailure(body.error ?? { message: `Request failed (HTTP ${response.status})` }); return; }
      if (!body.config || !body.cell) { setFailure({ message: "The server created the agent but returned no cell. Refresh the inventory to find it." }); return; }
      onCreated(body as CreatedAgent);
    } catch (error) {
      setFailure({ message: `The creation outcome could not be confirmed. Refresh the inventory before retrying. ${error instanceof Error ? error.message : ""}`.trim() });
    } finally { setPending(false); }
  }
  return <section className="new-agent" aria-label="New agent">
    <p className="description">An agent is a Claude or Codex cell that knows it runs in kukeon. It has no project checkout; clone one in its terminal when you need it.</p>
    {templates.loading && !templates.data && <p role="status">Loading agent templates…</p>}
    {templates.error && <ErrorNotice error={templates.error} kind="Agent template" />}
    {templates.data && !available.length && <div className="empty-state new-agent-missing" role="note"><h3>No agent templates</h3><p>Agents are copied from the templates of the <code>default</code> team, and none is stored yet. Initialize stores them; see also <a href={teamsLink()}>Teams</a>.</p><InitializePrompt onChanged={onChanged} subject="the agent templates of the default team" /></div>}
    {list.length > 0 && <fieldset className="agent-harnesses" disabled={pending}>
      <legend>Harness</legend>
      <div className="agent-cards">{list.map(template => {
        const selected = template.exists && template.harness === harness;
        return <label key={template.harness} className={`agent-card panel${selected ? " is-selected" : ""}${template.exists ? "" : " is-missing"}`}>
          <input type="radio" name="agent-harness" value={template.harness} checked={selected} disabled={!template.exists} onChange={() => setPicked(template.harness)} />
          <strong>{titleOf(template.harness)}</strong>
          <span className="description mono-id">{template.name}</span>
          <span className={`badge${template.exists ? "" : " needs-connector"}`}>{template.exists ? "Template ready" : "Template missing"}</span>
        </label>;
      })}</div>
    </fieldset>}
    {available.length > 0 && <form className="new-agent-form" onSubmit={event => { event.preventDefault(); void create(); }}>
      <label>Name <span className="description">(optional)</span><input type="text" value={name} maxLength={63} placeholder={`${titleOf(harness)} agent`} disabled={pending} onChange={event => setName(event.target.value)} /></label>
      {nameError && <p className="error" role="alert">{nameError}</p>}
      <div className="form-actions">
        <button type="submit" className="primary-action" disabled={!harness || pending || !!nameError}>{pending ? "Creating…" : "Create"}</button>
        {connector && loginFirst && <LoginButton connector={connector.name} title={connector.title || connector.name} volume={loginFirst.volume} state={loginFirst.state} running={!!loginFirst.session} method={loginFirst.method || undefined} idle="Log in first" disabled={pending} />}
      </div>
      {connector && loginFirst && <p className="description new-agent-login" role="note">{connector.title || connector.name} is not logged in for agents yet. Log in first, or create the agent and log in from its terminal.</p>}
    </form>}
    {failure && <div className="error" role="alert">
      <strong>{failure.step && failure.config ? "Agent stored, but not started" : failure.code === "template_not_found" ? "Template missing" : "Agent not created"}</strong>
      <p>{failure.step && failure.config ? <>Config <a className="resource-name" href={catalogPageLink("configs", scopeOf(failure.config))}>{failure.config.name}</a> was stored; {stepText[failure.step] ?? failure.step} failed: {failure.message}</> : failure.message}</p>
    </div>}
  </section>;
}
