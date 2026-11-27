import { agentTemplateAnnotation, configLabel } from "./cells.mjs";
import { identitiesFor, importsFromHost, loginEmpty, loginIdentitiesFor, loginLoggedIn, tokenForGit } from "./connectors.mjs";
import { configForCell } from "./relationships.mjs";
import { systemTeam } from "./teams.mjs";

// Pure logic of first-run onboarding (#1694): the Get started checklist of
// the Workbench, when its card shows, and the wording of a connector that is
// not set up. Every state is derived from data the web UI already reads —
// GET /api/teams, the connector catalog, the Secret catalog, the login
// identities, the Config catalog and the cell inventory — and nothing is
// stored in kukeond. No connector is named here: a harness connector is one
// with `harness`, a git connector one delivered by git or whose token can
// serve git. No DOM, no network — unit-tested with node --test.

// The system team that holds the agent templates (#1692).
export const defaultTeam = "default";

// Checklist steps, in order.
export const stepInitialize = "initialize";
export const stepLogin = "login";
export const stepGit = "git-identity";
export const stepMajordomo = "majordomo";
export const stepAgent = "agent";

// What a step asks of the user: a required step keeps the checklist open;
// a recommended or an optional one never does.
export const kindRequired = "required";
export const kindRecommended = "recommended";
export const kindOptional = "optional";

// How the Get started card is shown, kept per browser: `auto` while a
// required step is open, `hidden` once dismissed or completed, `shown` when
// reopened from the user menu.
export const cardAuto = "auto";
export const cardHidden = "hidden";
export const cardShown = "shown";
export const cardModes = [cardAuto, cardHidden, cardShown];

const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === "string" ? value.trim() : "";
const titleOf = connector => text(connector?.title) || text(connector?.name);

// teamsInitialized reports what `kuke team init` stores: the kuke team and
// the system default team.
export function teamsInitialized(teams) {
  const names = list(teams).map(team => text(team?.name));
  return names.includes(systemTeam) && names.includes(defaultTeam);
}

// harnessConnectors are the connectors a harness authenticates with.
export function harnessConnectors(connectors) { return list(connectors).filter(connector => !!text(connector?.harness)); }
// gitConnectors are the connectors that give a cell git: the git-delivered
// one, and one whose token can also be stored as a git identity (#1693).
export function gitConnectors(connectors) { return list(connectors).filter(connector => importsFromHost(connector) || !!tokenForGit(connector)); }
// gitIdentityConnector is the connector whose New identity page offers every
// way to set git up: the git-delivered one.
export function gitIdentityConnector(connectors) { return gitConnectors(connectors).find(importsFromHost); }

// connectorSetUp reports a connector that has an identity to use: a stored
// Secret tagged with it, or a login identity that exists and is not empty. A
// team's state volume that no cell created yet, or one nobody logged in to,
// is not one.
export function connectorSetUp(connector, secrets, logins) {
  const name = text(connector?.name);
  if (!name) return false;
  return identitiesFor(secrets, name).length > 0 || loginIdentitiesFor(logins, name).some(login => login.exists && login.state !== loginEmpty);
}

// recommendedFor words what a connector that is not set up is recommended
// for, from its catalog description: the sentence without its full stop, its
// first word lowered unless that word has capitals of its own (GitHub).
export function recommendedFor(description) {
  const sentence = text(description).replace(/\.$/, "");
  return /^[A-Z][a-z]*(\s|$)/.test(sentence) ? sentence[0].toLowerCase() + sentence.slice(1) : sentence;
}
// notSetUpText is the note of a connector without identities.
export function notSetUpText(connector) {
  const purpose = recommendedFor(connector?.description);
  return purpose ? `Not set up — recommended for ${purpose}.` : "Not set up — recommended.";
}

// harnessLoggedIn reports a harness connector a cell can authenticate with:
// one of its login identities holds a login, or a token is stored for it.
export function harnessLoggedIn(connector, secrets, logins) {
  const name = text(connector?.name);
  if (!name) return false;
  return loginIdentitiesFor(logins, name).some(login => login.state === loginLoggedIn) || identitiesFor(secrets, name).length > 0;
}

// configLogin is the login identity a Config logs in through for a connector:
// the one whose usedBy names the Config. Undefined when the Config uses a
// token method, or is not stored.
export function configLogin(logins, connector, config) {
  const name = text(connector?.name);
  const wanted = text(config?.name);
  if (!name || !wanted) return undefined;
  return loginIdentitiesFor(logins, name).find(login => login.usedBy.some(user => text(user.name) === wanted && (!text(config.realm) || !text(user.realm) || user.realm === config.realm)));
}

// harnessConnector is the connector of one harness.
export function harnessConnector(connectors, harness) {
  return text(harness) ? harnessConnectors(connectors).find(connector => connector.harness === harness) : undefined;
}

// templateLogins pairs each harness connector with the login identity its
// agent template logs in through (templates: GET /api/agents/templates), so
// the checklist's Log in reaches the identity New agent uses.
export function templateLogins(connectors, logins, templates) {
  return harnessConnectors(connectors).map(connector => {
    const template = list(templates).find(item => item?.harness === connector.harness && item.exists === true);
    return { connector, login: template ? configLogin(logins, connector, template) : undefined };
  });
}

// hasAgentCell reports a cell of an agent: a Config copied from an agent
// template (kukeon.io/agent-template). Rows are inventory cell rows; configs
// is the Config catalog. A cell names its Config in spec.provenance, else in
// its kukeon.io/config label within the Config's realm.
export function hasAgentCell(rows, configs) {
  const agents = list(configs).filter(config => text(config?.metadata?.annotations?.[agentTemplateAnnotation]) && text(config.metadata.name));
  if (!agents.length) return false;
  return list(rows).some(row => {
    const ref = configForCell(row?.doc);
    const realm = text(ref?.realm) || text(row?.scope?.[0]);
    const name = ref ? text(ref.name) : text(row?.doc?.metadata?.labels?.[configLabel]);
    return !!name && agents.some(config => config.metadata.name === name && (text(config.metadata.realm) || realm) === realm);
  });
}

const joined = (titles, word) => titles.length > 1 ? `${titles.slice(0, -1).join(", ")} ${word} ${titles.at(-1)}` : titles[0] ?? "";

// checklist builds the Get started items from what the page read. An input
// that is not loaded yet reads as not done.
export function checklist({ teams, connectors, secrets, logins, cells, configs, majordomoCells = 0 } = {}) {
  const harnesses = harnessConnectors(connectors);
  const git = gitConnectors(connectors);
  return [
    { id: stepInitialize, kind: kindRequired, title: "Initialize", done: teamsInitialized(teams),
      description: "Store the kuke team (majordomo) and the default team (the agent templates)." },
    { id: stepLogin, kind: kindRequired, title: harnesses.length ? `Log in to ${joined(harnesses.map(titleOf), "or")}` : "Log in to a harness", done: harnesses.some(connector => harnessLoggedIn(connector, secrets, logins)),
      description: "An agent needs a login, or a token, for its harness." },
    { id: stepGit, kind: kindRecommended, title: git.length ? `Set up ${joined(git.map(titleOf), "and")}` : "Set up git", done: git.some(connector => identitiesFor(secrets, connector.name).length > 0),
      description: "Give cells an identity to clone, push and sign with." },
    { id: stepMajordomo, kind: kindOptional, title: "Enable majordomo", done: majordomoCells > 0,
      description: "Majordomo is kukeon's own assistant, docked beside every page." },
    { id: stepAgent, kind: kindRequired, title: "Launch your first agent", done: hasAgentCell(cells, configs),
      description: "A Claude or Codex cell with a terminal of its own." },
  ];
}

// requiredOpen are the required steps still to do.
export function requiredOpen(items) { return list(items).filter(item => item.kind === kindRequired && !item.done); }
// progress counts the required steps: the card's "N of M done".
export function progress(items) {
  const required = list(items).filter(item => item.kind === kindRequired);
  return { done: required.filter(item => item.done).length, total: required.length };
}
// stepState words a step for its badge: Done, or what it asks.
export function stepState(item) {
  return item?.done ? "Done" : item?.kind === kindRecommended ? "Recommended" : item?.kind === kindOptional ? "Optional" : "To do";
}
// cardVisible decides whether the Get started card shows: always when
// reopened, never when hidden, and in auto mode while a required step is open.
export function cardVisible(mode, items) {
  if (mode === cardShown) return true;
  if (mode === cardHidden) return false;
  return requiredOpen(items).length > 0;
}
