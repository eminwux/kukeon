import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useResource } from "./request";
import type { InventoryResource } from "./inventory";
import type { Connector } from "./connectors.mjs";
import { LoginButton, connectorsLink, newIdentityLink } from "./connector-ui";
import { InitializePrompt } from "./initialize";
import { newAgentLink } from "./routes";
import { useSettings } from "./settings";
import type { TeamSummary } from "./teams.mjs";
import { checklist, gitIdentityConnector, progress, requiredOpen, stepAgent, stepGit, stepInitialize, stepLogin, stepMajordomo, stepState, templateLogins, type AgentTemplateRef, type Step } from "./onboarding.mjs";

// The Get started card of the Workbench (#1694): the first-run checklist. Its
// five steps are derived from data the UI already reads (onboarding.mjs) and
// nothing is stored in kukeond; only whether the card shows is kept, in this
// browser (showGetStarted, #1745). It shows until it is dismissed, also once
// every required step is done; the Profile switch and the user menu turn it
// back on.

function StepRow({ step, children }: { step: Step; children?: ReactNode }) {
  const tone = step.done ? "ok" : step.kind === "required" ? "pending" : "stopped";
  return <li className={`get-started-step${step.done ? " is-done" : ""}`} data-step={step.id} aria-label={`${step.title}: ${stepState(step)}`}>
    <span className={`state-badge tone-${tone} get-started-state`}><span className="state-dot" aria-hidden="true" />{stepState(step)}</span>
    <div className="get-started-text"><strong>{step.title}</strong><p className="description">{step.description}</p></div>
    {!step.done && children && <div className="get-started-actions">{children}</div>}
  </li>;
}

export function GetStarted({ revision, inventory, configs, majordomoCells, onOpenMajordomo, onChanged }: {
  revision: number;
  inventory: InventoryResource;
  // The Config catalog the app already reads for majordomo: undefined until it loads.
  configs: unknown[] | undefined;
  majordomoCells: number;
  onOpenMajordomo: () => void;
  onChanged: () => void;
}) {
  const [settings, set] = useSettings();
  const active = settings.showGetStarted;
  const teams = useResource<{ teams: TeamSummary[] }>(active ? "/api/teams" : null, revision);
  const catalog = useResource<Connector[]>(active ? "/api/connectors" : null, revision);
  const secrets = useResource<unknown[]>(active ? "/api/catalog/secrets" : null, revision);
  const templates = useResource<{ templates: AgentTemplateRef[] }>(active ? "/api/agents/templates" : null, revision);
  // While a login session runs the login identities are re-read, so the Log in
  // step completes in place when the identity cell ends.
  const [tick, setTick] = useState(0);
  const logins = useResource<unknown[]>(active ? "/api/connectors/logins" : null, revision + tick);
  const loginRunning = (logins.data ?? []).some(item => !!(item as { session?: unknown } | null)?.session);
  useEffect(() => {
    if (!loginRunning) return undefined;
    const timer = window.setInterval(() => setTick(value => value + 1), 3000);
    return () => window.clearInterval(timer);
  }, [loginRunning]);
  const cells = inventory.data?.rows[3];
  const items = useMemo(() => checklist({ teams: teams.data?.teams, connectors: catalog.data, secrets: secrets.data, logins: logins.data, cells, configs, majordomoCells }),
    [teams.data, catalog.data, secrets.data, logins.data, cells, configs, majordomoCells]);
  // Every input answered, with data or an error: only then is "all required done" a fact.
  const settled = [catalog, secrets, templates, logins].every(resource => !!resource.data || !!resource.error) && !!inventory.data && !!configs;
  const complete = !!teams.data && settled && !requiredOpen(items).length;
  if (!active || !teams.data) return null;
  const initialized = items.find(item => item.id === stepInitialize)?.done ?? false;
  // An initialized host waits for the rest of its data, so the card does not flash steps it is about to tick.
  if (initialized && !settled) return null;
  const step = (id: string) => items.find(item => item.id === id) as Step;
  const count = progress(items);
  const harnesses = templateLogins(catalog.data, logins.data, templates.data?.templates);
  const git = gitIdentityConnector(catalog.data);
  return <section className="panel get-started" aria-label="Get started">
    <div className="panel-heading"><h2>Get started</h2><span className="badge">{count.done} of {count.total} done</span><button type="button" className="subtle-button" onClick={() => set("showGetStarted", false)}>Dismiss</button></div>
    <div className="panel-body">
      {complete && <p className="get-started-complete" role="status">You are set up. Recommended steps stay listed until you set them up or dismiss this card.</p>}
      <ol className="get-started-list">
        <StepRow step={step(stepInitialize)}><InitializePrompt onChanged={onChanged} /></StepRow>
        <StepRow step={step(stepLogin)}>
          {harnesses.map(({ connector, login }) => { const title = connector.title || connector.name; return <div className="get-started-harness" key={connector.name} role="group" aria-label={title}>
            <span className="get-started-harness-name">{title}</span>
            {login ? <LoginButton connector={connector.name} title={title} volume={login.volume} state={login.state} running={!!login.session} method={login.method || undefined} onStarted={onChanged} /> : <span className="description">{initialized ? "uses a token" : "after Initialize"}</span>}
            <a className="document-action" href={connectorsLink(connector.name)} aria-label={`Use a token for ${title}`}>Use a token</a>
          </div>; })}
        </StepRow>
        <StepRow step={step(stepGit)}>{git ? <a className="document-action" href={newIdentityLink(git.name)}>New {git.title || git.name} identity</a> : <a className="document-action" href={connectorsLink()}>Connectors</a>}</StepRow>
        <StepRow step={step(stepMajordomo)}><button type="button" onClick={onOpenMajordomo}>Enable majordomo</button></StepRow>
        <StepRow step={step(stepAgent)}><a className="header-action" href={newAgentLink()}>New agent</a></StepRow>
      </ol>
    </div>
  </section>;
}
