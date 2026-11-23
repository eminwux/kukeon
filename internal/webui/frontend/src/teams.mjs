// Pure logic of the Teams page (#1618, AC16 and AC17). The page drives the
// same team operations as `kuke team`: the server composes, this module only
// shapes requests and reads responses. A declaration is a YAML string in both
// directions; nothing here parses it. No DOM, no network — unit-tested with
// node --test.

// The kuke system team: reset with `kuke team init --force`, never deleted.
export const systemTeam = "kuke";
// Ref kinds a repository or a source is read at.
export const refKinds = ["branch", "tag", "commit"];
// Error codes the teams API answers with that the page branches on.
export const codeExists = "already_exists";
export const codeReserved = "reserved";

const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === "string" ? value : "";
const trimmed = value => text(value).trim();

export function teamsLink(name) {
  return "#/teams" + (name ? "/" + encodeURIComponent(name) : "");
}

// teamsAPI is a teams route: the list, one team, or one team's action.
export function teamsAPI(name, action) {
  if (!name) return "/api/teams";
  return "/api/teams/" + encodeURIComponent(name) + (action ? "/" + action : "");
}

export function sourcesAPI(name, action) {
  if (!name) return "/api/team-sources";
  return "/api/team-sources/" + encodeURIComponent(name) + (action ? "/" + action : "");
}

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
function withRef(out, kind, value) {
  const ref = trimmed(value);
  if (ref && refKinds.includes(kind)) out[kind] = ref;
  return out;
}

// selectionEntry is one --role or --harness value: a default's bare name, or
// NAME=SOURCE:PATH for anything a registered source offers.
export function selectionEntry(item) {
  const name = trimmed(item?.name);
  if (item?.default || !trimmed(item?.source)) return name;
  const target = `${trimmed(item.source)}:${trimmed(item.path)}`;
  return name ? `${name}=${target}` : target;
}

// addPayload is the POST /api/teams{,/preview} body of an add: kuke team add
// NAME --source URL with its flags. A picker that was not touched sends null,
// which keeps the declaration's list; a touched one replaces it, even empty.
export function addPayload(form) {
  const out = withRef({ name: trimmed(form?.name), source: trimmed(form?.source) }, form?.refKind, form?.ref);
  out.useRepoFile = form?.useRepoFile !== false;
  out.roles = Array.isArray(form?.roles) ? form.roles.map(selectionEntry).filter(Boolean) : null;
  out.harnesses = Array.isArray(form?.harnesses) ? form.harnesses.map(selectionEntry).filter(Boolean) : null;
  if (trimmed(form?.declaration)) out.declaration = text(form.declaration);
  if (form?.force) out.force = true;
  return out;
}

// editPayload is the body of an edit: the whole declaration, kept to the team
// being edited (kuke team apply -f).
export function editPayload(name, declaration) {
  return { name: trimmed(name), declaration: text(declaration) };
}

// addErrors lists what keeps an add form from being sent.
export function addErrors(form) {
  const errors = [];
  if (!trimmed(form?.source)) errors.push("Repository URL is required.");
  if (form?.refKind === "commit" && trimmed(form?.ref) && !/^[0-9a-f]{40}$/.test(trimmed(form.ref))) {
    errors.push("A commit is a full 40-character SHA.");
  }
  if (trimmed(form?.name) && !/^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/.test(trimmed(form.name))) {
    errors.push("A team name is lowercase letters, digits, dots and hyphens.");
  }
  return errors;
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

// pairRows flattens a plan's role × harness pairs for a table.
export function pairRows(plan) {
  return list(plan?.pairs).map(pair => ({
    key: `${text(pair.role)}:${text(pair.harness)}`,
    role: text(pair.role), harness: text(pair.harness), config: text(pair.config), image: text(pair.image),
    components: list(pair.components),
    tasks: list(pair.tasks).map(task => task.schedule ? `${text(task.name)} (${text(task.schedule)}${task.suspend ? ", suspended" : ""})` : text(task.name)),
  }));
}

// sourceItems marks each role and harness a source offers with whether it is
// a default, for the source's default toggles.
export function sourceItems(contents) {
  return {
    roles: list(contents?.roles).map(role => ({ ...role, harnesses: list(role.harnesses), tasks: list(role.tasks), default: !!role.default })),
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

// pickerOptions are what the add form's role or harness picker offers: the
// defaults first, then every offered item of the inspected sources that is
// not already a default.
export function pickerOptions(defaults, inspected, kind) {
  const field = kind === "harness" ? "defaultHarnesses" : "defaultRoles";
  const offered = kind === "harness" ? "harnesses" : "roles";
  const out = list(defaults?.[field]).map(entry => ({ name: text(entry.name), source: text(entry.source), path: text(entry.path), default: true }));
  for (const [source, contents] of Object.entries(inspected ?? {})) {
    for (const item of list(contents?.[offered])) {
      if (out.some(option => option.source === source && option.path === item.path)) continue;
      out.push({ name: text(item.name), source, path: text(item.path), default: false });
    }
  }
  return out;
}

export function optionKey(option) {
  return `${text(option?.source)}:${text(option?.path)}:${text(option?.name)}`;
}

// sourcePayload is the POST /api/team-sources body: kuke team source add.
export function sourcePayload(form) {
  const out = withRef({ name: trimmed(form?.name), url: trimmed(form?.url) }, form?.refKind, form?.ref);
  if (trimmed(form?.clonePath)) out.clonePath = trimmed(form.clonePath);
  return out;
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
