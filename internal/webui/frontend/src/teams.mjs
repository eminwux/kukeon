// Pure logic of the Teams and Sources pages (#1618, #1644). The pages drive
// the same team operations as `kuke team` and `kuke create source`: the
// server composes, this module only shapes requests and reads responses. A
// declaration is a YAML string in both directions; nothing here parses it.
// The wizard's form logic is in team-form.mjs. No DOM, no network —
// unit-tested with node --test.

// The kuke system team: reset with `kuke team init --force`, never deleted.
export const systemTeam = "kuke";
// Ref kinds a repository or a source is read at.
export const refKinds = ["branch", "tag", "commit"];
// Error codes the teams API answers with that the pages branch on.
export const codeExists = "already_exists";
export const codeReserved = "reserved";
export const codeInUse = "in_use";

const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === "string" ? value : "";
const trimmed = value => text(value).trim();

// teamsLink opens the Teams list, one team's page, or its edit or export
// screen.
export function teamsLink(name, section) {
  if (!name) return "#/teams";
  return "#/teams/" + encodeURIComponent(name) + (section === "edit" || section === "export" ? "/" + section : "");
}

export const newTeamLink = "#/new-team";

export function sourcesLink(name) {
  return "#/sources" + (name ? "/" + encodeURIComponent(name) : "");
}

// teamsAPI is a teams route: the list, one team, or one team's action.
export function teamsAPI(name, action) {
  if (!name) return "/api/teams";
  return "/api/teams/" + encodeURIComponent(name) + (action ? "/" + action : "");
}

// exportAPI is kuke team export NAME with its --host-fields and
// --explicit-defaults options.
export function exportAPI(name, options = {}) {
  const query = [options.hostFields ? "hostFields=1" : "", options.explicit ? "explicit=1" : ""].filter(Boolean).join("&");
  return teamsAPI(name, "export") + (query ? "?" + query : "");
}

// sourcesAPI is a Sources route: the list (and create), the inspect of an
// unstored Source, or one stored Source's action.
export function sourcesAPI(name, action) {
  if (!name) return "/api/sources";
  return "/api/sources/" + encodeURIComponent(name) + (action ? "/" + action : "");
}
export const inspectAPI = "/api/sources/inspect";

export const defaultsAPI = "/api/team-defaults";

// refOf names the ref a source is read at: its branch, tag or commit.
export function refOf(source) {
  for (const kind of refKinds) {
    if (trimmed(source?.[kind])) return { kind, value: trimmed(source[kind]) };
  }
  return { kind: "", value: "" };
}

export function refLabel(source) {
  const ref = refOf(source);
  if (!ref.kind) return "default branch";
  return ref.kind === "commit" ? `commit ${ref.value.slice(0, 12)}` : `${ref.kind} ${ref.value}`;
}

// withRef sets one ref field from a picked kind and value, as the CLI's
// --branch, --tag and --commit flags do; an empty value asks the server to
// discover the default branch.
export function withRef(out, kind, value) {
  const ref = trimmed(value);
  if (ref && refKinds.includes(kind)) out[kind] = ref;
  return out;
}

// refErrors checks a ref kind and value the way the server will.
export function refErrors(kind, value) {
  const ref = trimmed(value);
  if (kind === "commit" && ref && !/^([0-9a-f]{40}|[0-9a-f]{64})$/i.test(ref)) return ["A commit is a full commit SHA."];
  if (ref && /[\s~^:?*[\\]|\.\.|@\{/.test(ref)) return ["A branch or tag name has no spaces or special characters."];
  return [];
}

// validName is a team or Source name: lowercase letters, digits, dots and
// hyphens.
export function validName(value) {
  return /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/.test(trimmed(value));
}

// editPayload is the body of an edit as YAML: the whole declaration, kept to
// the team being edited (kuke team apply -f).
export function editPayload(name, declaration) {
  return { name: trimmed(name), declaration: text(declaration) };
}

// needsForce reports an add refused because the team is stored: the page then
// asks before replacing its declaration (AC8a).
export function needsForce(failure) {
  return failure?.code === codeExists;
}

// planSections are the parts of a preview, in the order the CLI prints them,
// with only the ones that have content.
export function planSections(plan) {
  if (!plan) return [];
  const sections = [
    { key: "create", title: "Configs to create", items: list(plan.create) },
    { key: "rerender", title: "Configs to re-render", items: list(plan.rerender) },
    { key: "prune", title: "Configs to prune", items: list(plan.prune) },
    { key: "createSecrets", title: "Secrets to create", items: list(plan.createSecrets) },
    { key: "missingSecrets", title: "Missing secrets", items: list(plan.missingSecrets) },
    { key: "notes", title: "Notes", items: list(plan.notes) },
  ];
  return sections.filter(section => section.items.length);
}

// originLabel says where a plan's declaration came from (AC10).
export function originLabel(origin) {
  const kind = text(origin?.kind);
  if (!kind) return "";
  return trimmed(origin.detail) ? `${kind} (${trimmed(origin.detail)})` : kind;
}

// taskLabel names a task with its schedule: "pick (@hourly)", "sweep
// (@daily, suspended)".
export function taskLabel(task) {
  const name = text(task?.name);
  if (!trimmed(task?.schedule)) return name;
  return `${name} (${trimmed(task.schedule)}${task.suspend ? ", suspended" : ""})`;
}

// pairRows flattens a plan's role × harness pairs for a table.
export function pairRows(plan) {
  return list(plan?.pairs).map(pair => ({
    key: `${text(pair.role)}:${text(pair.harness)}`,
    role: text(pair.role), harness: text(pair.harness), config: text(pair.config), image: text(pair.image),
    components: list(pair.components),
    tasks: list(pair.tasks).map(taskLabel),
  }));
}

// sourceItems defaults the lists and flags of what a source offers.
export function sourceItems(contents) {
  return {
    roles: list(contents?.roles).map(role => ({
      ...role, harnesses: list(role.harnesses), default: !!role.default,
      tasks: list(role.tasks).map(task => typeof task === "string" ? { name: task } : { name: text(task?.name), schedule: text(task?.schedule), suspend: !!task?.suspend, args: text(task?.args) }),
    })),
    harnesses: list(contents?.harnesses).map(harness => ({ ...harness, default: !!harness.default })),
  };
}

// toggledDefaults is the defaults set body that turns one offered role or
// harness on or off, keeping the other defaults: the current defaults by
// name, the newly selected one as NAME=SOURCE:PATH.
export function toggledDefaults(current, kind, item, on) {
  const field = kind === "harness" ? "defaultHarnesses" : "defaultRoles";
  const name = trimmed(item?.name);
  const kept = list(current?.[field]).filter(entry => text(entry.name) !== name).map(entry => text(entry.name));
  const entries = on ? [...kept, `${name}=${trimmed(item?.source)}:${trimmed(item?.path)}`] : kept;
  return kind === "harness" ? { harnesses: entries } : { roles: entries };
}

// isDefault reports whether the defaults select a source's role or harness.
export function isDefault(current, kind, source, path) {
  const field = kind === "harness" ? "defaultHarnesses" : "defaultRoles";
  return list(current?.[field]).some(entry => entry.source === source && entry.path === path);
}

// sourcePayload is the POST /api/sources body: kuke create source NAME --url
// with its ref, --identity and --force. A Source has no clone path; a team's
// entry for it does (#1643).
export function sourcePayload(form) {
  const out = withRef({ name: trimmed(form?.name), url: trimmed(form?.url) }, form?.refKind, form?.ref);
  if (trimmed(form?.identity)) out.identity = trimmed(form.identity);
  if (form?.force) out.force = true;
  return out;
}

// sourceErrors lists what keeps a New source form from being sent.
export function sourceErrors(form) {
  const errors = [];
  if (!trimmed(form?.name)) errors.push("Name is required.");
  else if (!validName(form.name) || trimmed(form.name) === "project") errors.push("A Source name is lowercase letters, digits, dots and hyphens, and not project.");
  if (!trimmed(form?.url)) errors.push("Address is required.");
  return [...errors, ...refErrors(form?.refKind, form?.ref)];
}

// usesLabel says how one team or the defaults use a Source.
export function usesLabel(use) {
  const how = `${text(use?.as)} ${text(use?.name)}`.trim();
  return trimmed(use?.team) ? `team ${use.team} (${how})` : how;
}

// sourceTeams lists the teams that use a Source, once each.
export function sourceTeams(source) {
  return [...new Set(list(source?.usedBy).map(use => trimmed(use?.team)).filter(Boolean))].sort();
}

// inheritingNote tells the user which teams a defaults change reaches only
// once reapplied (AC14, G4).
export function inheritingNote(change) {
  const teams = list(change?.inheriting);
  if (!teams.length) return "No stored team inherits from the defaults.";
  return `Teams inheriting from the defaults: ${teams.join(", ")}. A change reaches each only when it is reapplied.`;
}

// deleteSummary describes what a delete removed and left (AC6).
export function deleteSummary(result) {
  const removed = list(result?.removed).map(item => `${text(item.kind)} ${text(item.name)}`);
  return {
    removed,
    liveCells: list(result?.liveCells),
    keptVolumes: list(result?.keptVolumes),
  };
}

// teamConfigs lists a team's Configs with their scopes, from the catalog of
// Configs, for links to their pages.
export function teamConfigs(configs, team) {
  return list(configs)
    .filter(doc => text(doc?.metadata?.labels?.["kukeon.io/team"]) === team)
    .map(doc => ({ name: text(doc.metadata.name), scope: [doc.metadata.realm || "default", doc.metadata.space || "default", doc.metadata.stack || "default"] }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

// listOrigin is the badge of a list on a team's page: the team declares it,
// or inherits it from the defaults.
export function listOrigin(list) {
  return list?.inherited ? "inherited" : "declared";
}

// configLink opens a Config's page.
export function configLink(scope, name) {
  return "#/configs/" + [...list(scope), name].map(encodeURIComponent).join("/");
}

// repositoryFromSource is the New Cell repository entry a stored Source
// fills (AC7): its name, address and ref — a branch, else a tag or commit as
// the entry's ref.
export function repositoryFromSource(source) {
  const out = { name: text(source?.name), url: text(source?.url), branch: "", ref: "" };
  const ref = refOf(source);
  if (ref.kind === "branch") out.branch = ref.value;
  else if (ref.kind) out.ref = ref.value;
  return out;
}
