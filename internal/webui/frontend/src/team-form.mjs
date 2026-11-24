// Pure logic of the team wizard and the Edit team screen (#1644). The state
// is the server's `form` (internal/webserver/team_form.go) plus, for a new
// team, the draft's declaration, which this module never reads: it is sent
// back unchanged (D3). Every list that can inherit carries its flag; the
// reducers here keep them, so nothing turns an inherited list into an empty
// one by accident. No DOM, no network — unit-tested with node --test.

import { deliveryGit } from "./connectors.mjs";
import { inspectAPI, refErrors, refOf, sourcesAPI, validName, withRef } from "./teams.mjs";

const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === "string" ? value : "";
const trimmed = value => text(value).trim();

export const projectAlias = "project";

const steps = {
  project: { key: "project", title: "Project" },
  sources: { key: "sources", title: "Other sources" },
  roles: { key: "roles", title: "Roles and harnesses" },
  tasks: { key: "tasks", title: "Tasks and schedules" },
  credentials: { key: "credentials", title: "Credentials" },
  review: { key: "review", title: "Review" },
};

// stepsFor lists a mode's steps: a new team starts at its project; an edit
// keeps its project and starts at the other sources, with the project ref
// at the top of that step.
export function stepsFor(mode) {
  const keys = mode === "edit" ? ["sources", "roles", "tasks", "credentials", "review"] : ["project", "sources", "roles", "tasks", "credentials", "review"];
  return keys.map(key => steps[key]);
}

// The Project step's ways to name the project.
export const projectModes = ["address", "stored", "file"];

export function emptyProject() {
  return { mode: "address", address: "", stored: "", file: "", refKind: "branch", ref: "", identity: "", name: "", useRepoFile: true };
}

// draftPayload is the POST /api/teams/draft body: create team's inputs.
export function draftPayload(project) {
  const mode = projectModes.includes(project?.mode) ? project.mode : "address";
  const out = {};
  if (trimmed(project?.name)) out.name = trimmed(project.name);
  if (mode === "file") {
    out.declaration = text(project?.file);
  } else {
    out.source = mode === "stored" ? trimmed(project?.stored) : trimmed(project?.address);
    withRef(out, project?.refKind, project?.ref);
    if (mode === "address" && trimmed(project?.identity)) out.identity = trimmed(project.identity);
    out.useRepoFile = project?.useRepoFile !== false;
  }
  return out;
}

// projectErrors lists what keeps the Project step from being read.
export function projectErrors(project) {
  const errors = [];
  const mode = project?.mode;
  if (mode === "file" && !trimmed(project?.file)) errors.push("Paste a kuketeam.yaml.");
  if (mode === "stored" && !trimmed(project?.stored)) errors.push("Choose a stored Source.");
  if ((mode === "address" || !mode) && !trimmed(project?.address)) errors.push("Repository address is required.");
  if (mode !== "file") errors.push(...refErrors(project?.refKind, project?.ref));
  if (trimmed(project?.name) && !validName(project.name)) errors.push("A team name is lowercase letters, digits, dots and hyphens.");
  return errors;
}

// sourceRef is a form source's ref as the server shows it ("branch main").
export function sourceRef(source) {
  return trimmed(source?.refKind) && trimmed(source?.ref) ? `${source.refKind} ${trimmed(source.ref)}` : "";
}

// sourceErrors lists the problems of the Other sources step: names, clone
// paths, and conflicts the draft found that the user has not resolved by
// storing the definition under another name.
export function sourcesErrors(form, draftSources) {
  const errors = [];
  const seen = new Set([projectAlias]);
  list(form?.sources).forEach((source, i) => {
    const alias = trimmed(source.alias);
    const label = alias || `source ${i + 1}`;
    if (!alias) errors.push(`Source ${i + 1} needs a name.`);
    else if (!validName(alias)) errors.push(`Source ${label}: a name is lowercase letters, digits, dots and hyphens.`);
    else if (seen.has(alias)) errors.push(`Source ${label} is listed twice.`);
    seen.add(alias);
    if (source.url !== undefined && !trimmed(source.url)) errors.push(`Source ${label} needs an address.`);
    if (trimmed(source.clonePath) && !trimmed(source.clonePath).startsWith("/")) errors.push(`Source ${label}: a clone path is absolute.`);
    if (trimmed(source.stored) && !validName(source.stored)) errors.push(`Source ${label}: the stored name is lowercase letters, digits, dots and hyphens.`);
    errors.push(...refErrors(source.refKind, source.ref).map(message => `Source ${label}: ${message}`));
    const drafted = list(draftSources).find(item => item.alias === alias);
    if (drafted?.state === "conflict" && trimmed(source.url) && storedName(source) === drafted.stored) {
      errors.push(`Source ${label}: a stored Source ${drafted.stored} holds another repository (${drafted.have}); store this one under another name.`);
    }
  });
  return errors;
}

// storedName is the stored Source an entry is, or will be stored as.
export function storedName(source) {
  return trimmed(source?.stored) || trimmed(source?.alias);
}

// emptySource is a new entry: an address, or a stored Source by its name.
export function emptySource(stored) {
  return stored ? { alias: trimmed(stored.name), stored: "", refKind: "", ref: "", clonePath: "" }
    : { alias: "", stored: "", url: "", refKind: "branch", ref: "", clonePath: "", identity: "" };
}

// withSource adds (index undefined), changes or (patch null) removes a
// source entry. The first change of an undeclared list declares it.
export function withSource(form, index, patch) {
  const sources = [...list(form?.sources)];
  if (index === undefined) sources.push({ ...emptySource(), ...patch });
  else if (patch === null) sources.splice(index, 1);
  else sources[index] = { ...sources[index], ...patch };
  return { ...form, sources };
}

// withProject changes the project entry (an edit's ref).
export function withProject(form, patch) {
  return { ...form, project: { ...(form?.project ?? { alias: projectAlias }), ...patch } };
}

// available lists the sources a role or harness can name: the project, the
// team's entries, then every stored Source no entry shadows.
export function availableSources(form, stored) {
  const out = [];
  const taken = new Set();
  if (form?.project) {
    out.push({ alias: projectAlias, stored: trimmed(form.project.stored), url: trimmed(form.project.url), refKind: text(form.project.refKind), ref: text(form.project.ref), identity: text(form.project.identity), declared: true });
    taken.add(projectAlias);
  }
  for (const source of list(form?.sources)) {
    const alias = trimmed(source.alias);
    if (!alias || taken.has(alias)) continue;
    taken.add(alias);
    const backing = list(stored).find(item => item.name === storedName(source));
    out.push({ alias, stored: trimmed(source.url) ? "" : storedName(source), url: trimmed(source.url) || text(backing?.url), refKind: text(source.refKind), ref: text(source.ref), identity: trimmed(source.url) ? text(source.identity) : text(backing?.identity), declared: true });
  }
  for (const source of list(stored)) {
    if (taken.has(source.name)) continue;
    taken.add(source.name);
    const ref = refOf(source);
    out.push({ alias: source.name, stored: source.name, url: text(source.url), refKind: ref.kind, ref: ref.value, identity: text(source.identity), declared: false });
  }
  return out;
}

// inspectRequest is how a source's contents are read: a stored Source by its
// name, an entry with an address by that address, its ref and identity.
export function inspectRequest(source) {
  if (!trimmed(source?.stored) && trimmed(source?.url)) {
    const body = withRef({ url: trimmed(source.url) }, source.refKind, source.ref);
    if (trimmed(source.identity)) body.identity = trimmed(source.identity);
    return { path: inspectAPI, body };
  }
  return { path: sourcesAPI(trimmed(source?.stored) || trimmed(source?.alias), "inspect"), body: {} };
}

// selected reports whether the form selects a role or harness of a source.
export function selectedIndex(items, source, path) {
  return list(items).findIndex(item => item.source === source && item.path === path);
}

// withInherit switches a list between inheriting the defaults and listing
// its own items. Turning inheritance off starts from the inherited items, so
// nothing changes until the user picks.
export function withInherit(form, field, inherit, inherited = []) {
  const current = form?.[field] ?? { inherit: true, items: [] };
  if (inherit) return { ...form, [field]: { inherit: true, items: [] } };
  const items = current.inherit ? list(inherited).map(item => field === "roles" ? roleItem(item) : { name: text(item.name), source: text(item.source), path: text(item.path) }) : list(current.items);
  return { ...form, [field]: { inherit: false, items } };
}

function roleItem(role) {
  return {
    name: text(role?.name), source: text(role?.source), path: text(role?.path), harness: text(role?.harness),
    initialPath: text(role?.initialPath), image: list(role?.image), tasks: role?.tasks ?? { all: true, items: [] },
  };
}

// withRole selects or drops one offered role. A selected role starts with
// every task it declares.
export function withRole(form, role, on, inherited = []) {
  const next = withInherit(form, "roles", false, inherited);
  const items = [...next.roles.items];
  const at = selectedIndex(items, role.source, role.path);
  if (on && at < 0) items.push(roleItem({ name: role.name, source: role.source, path: role.path }));
  if (!on && at >= 0) items.splice(at, 1);
  return { ...next, roles: { inherit: false, items } };
}

// withRolePatch changes one selected role's fields.
export function withRolePatch(form, index, patch) {
  const items = list(form?.roles?.items).map((role, i) => i === index ? { ...role, ...patch } : role);
  return { ...form, roles: { ...form.roles, items } };
}

// withHarness selects or drops one offered harness.
export function withHarness(form, harness, on, inherited = []) {
  const next = withInherit(form, "harnesses", false, inherited);
  const items = [...next.harnesses.items];
  const at = selectedIndex(items, harness.source, harness.path);
  if (on && at < 0) items.push({ name: harness.name, source: harness.source, path: harness.path });
  if (!on && at >= 0) items.splice(at, 1);
  return { ...next, harnesses: { inherit: false, items } };
}

// imageList reads a comma list of image capabilities.
export function imageList(value) {
  return text(value).split(",").map(item => item.trim()).filter(Boolean);
}

// Schedule switch states: on (suspend false), off (suspend true), or the
// role's own (suspend null).
export function switchOf(task) {
  if (task?.suspend === true) return "off";
  if (task?.suspend === false) return "on";
  return "role";
}
export function suspendOf(state) {
  if (state === "off") return true;
  if (state === "on") return false;
  return null;
}

// taskRows lists a role's tasks for the Tasks step: every task the role
// declares (from an inspected source, else the team's task Configs), and any
// the form selects that neither names. Each row carries the role's defaults
// as placeholders.
export function taskRows(role, offered, configured) {
  const names = [];
  const add = name => { if (name && !names.includes(name)) names.push(name); };
  list(offered).forEach(task => add(text(task?.name)));
  list(configured).forEach(task => add(text(task?.task ?? task?.name)));
  const items = list(role?.tasks?.items);
  items.forEach(task => add(text(task.name)));
  const all = role?.tasks?.all !== false;
  return names.map(name => {
    const defaults = list(offered).find(task => task.name === name) ?? {};
    const configuredTask = list(configured).find(task => text(task?.task ?? task?.name) === name);
    const item = items.find(task => task.name === name);
    return {
      name, selected: all || !!item,
      schedule: text(item?.schedule), switch: switchOf(item), args: text(item?.args),
      roleSchedule: text(defaults.schedule) || text(configuredTask?.schedule?.cron), roleSuspend: defaults.schedule ? !!defaults.suspend : !!configuredTask?.schedule?.suspend,
      roleArgs: text(defaults.args),
    };
  });
}

// withTask changes one task of a role: patch.selected selects or drops it,
// other fields set its overrides. A role that selected every task lists
// them all first (known), so changing one keeps the others selected.
export function withTask(form, roleIndex, name, patch, known) {
  return withRolePatch(form, roleIndex, { tasks: taskSelection(form.roles.items[roleIndex].tasks, name, patch, known) });
}

function taskSelection(tasks, name, patch, known) {
  let items = tasks?.all !== false ? list(known).map(task => ({ name: text(task), suspend: null })) : list(tasks?.items).map(task => ({ ...task }));
  const at = items.findIndex(task => task.name === name);
  if (patch.selected === false) {
    items = items.filter(task => task.name !== name);
    return { all: false, items };
  }
  const current = at >= 0 ? items[at] : { name, suspend: null };
  const next = { ...current };
  if ("schedule" in patch) next.schedule = text(patch.schedule);
  if ("args" in patch) next.args = text(patch.args);
  if ("switch" in patch) next.suspend = suspendOf(patch.switch);
  if (at >= 0) items[at] = next; else items.push(next);
  return { all: false, items };
}

// withAllTasks selects every task the role declares again.
export function withAllTasks(form, roleIndex) {
  return withRolePatch(form, roleIndex, { tasks: { all: true, items: [] } });
}

const cronWords = ["@hourly", "@daily", "@weekly", "@monthly"];
// cronErrors checks a schedule the way kukeond reads one: five fields, or
// @hourly, @daily, @weekly, @monthly.
export function cronErrors(value) {
  const cron = trimmed(value);
  if (!cron || cronWords.includes(cron)) return [];
  const fields = cron.split(/\s+/);
  if (fields.length !== 5 || !fields.every(field => /^[0-9*,/\-A-Za-z?]+$/.test(field))) return [`"${cron}" is not a five-field cron expression or @hourly, @daily, @weekly, @monthly.`];
  return [];
}

// rolesErrors lists the problems of the Roles step.
export function rolesErrors(form) {
  const errors = [];
  if (form?.roles && !form.roles.inherit && !list(form.roles.items).length) errors.push("Select at least one role, or use the default roles.");
  if (form?.harnesses && !form.harnesses.inherit && !list(form.harnesses.items).length) errors.push("Select at least one harness, or use the default harnesses.");
  const names = new Set();
  for (const role of list(form?.roles?.items)) {
    if (!validName(role.name)) errors.push(`Role ${role.name || role.path}: a name is lowercase letters, digits, dots and hyphens.`);
    else if (names.has(role.name)) errors.push(`Role ${role.name} is selected twice; rename one.`);
    names.add(role.name);
    if (trimmed(role.initialPath) && !trimmed(role.initialPath).startsWith("/")) errors.push(`Role ${role.name}: the initial path is absolute.`);
  }
  return errors;
}

// tasksErrors lists the problems of the Tasks step.
export function tasksErrors(form) {
  const errors = [];
  for (const role of list(form?.roles?.items)) {
    for (const task of list(role.tasks?.items)) errors.push(...cronErrors(task.schedule).map(message => `${role.name} / ${task.name}: ${message}`));
  }
  return errors;
}

// stepErrors lists what keeps a step's Next disabled.
export function stepErrors(key, state) {
  switch (key) {
    case "project": return [...projectErrors(state?.project), ...(state?.draft ? [] : ["Read the repository first."])];
    case "sources": return [...(state?.form?.project ? refErrors(state.form.project.refKind, state.form.project.ref) : []), ...sourcesErrors(state?.form, state?.draft?.sources)];
    case "roles": return rolesErrors(state?.form);
    case "tasks": return tasksErrors(state?.form);
    default: return [];
  }
}

// withConnector sets one connector choice; an empty method and identity
// drop it. The first change declares the list.
export function withConnector(form, name, patch, inherited = {}) {
  const current = form?.connectors ?? { inherit: true, items: {} };
  const items = { ...(current.inherit ? inherited : current.items ?? {}) };
  const next = { ...(items[name] ?? {}), ...patch };
  if (!trimmed(next.method) && !trimmed(next.identity)) delete items[name];
  else items[name] = { method: trimmed(next.method), identity: trimmed(next.identity) };
  return { ...form, connectors: { inherit: false, items } };
}

export function withConnectorsInherit(form, inherit) {
  return { ...form, connectors: inherit ? { inherit: true, items: {} } : { inherit: false, items: { ...(form?.connectors?.items ?? {}) } } };
}

// createBody is the POST /api/teams{,/preview} body of a new team: the
// Project step's inputs, the form, and the draft's declaration unchanged.
export function createBody(project, draft, form, force = false) {
  const out = draftPayload(project);
  delete out.useRepoFile;
  delete out.declaration;
  out.name = trimmed(form?.name) || trimmed(draft?.name);
  out.form = { ...form, name: out.name };
  out.declaration = text(draft?.declaration);
  if (force) out.force = true;
  return out;
}

// editBody is the body of an edit through the form (kuke team apply -f).
export function editBody(name, form) {
  return { name: trimmed(name), form: { ...form, name: trimmed(name) } };
}

// gitIdentities lists the stored git identities a source can be fetched
// with: in realm default, by name, the Secrets of the connectors the catalog
// delivers as git.
export function gitIdentities(secrets, connectors) {
  const git = list(connectors).filter(connector => connector?.delivery === deliveryGit).map(connector => text(connector.name));
  return list(secrets)
    .filter(doc => git.includes(text(doc?.spec?.connector)) && text(doc?.metadata?.realm) === "default" && !trimmed(doc?.metadata?.space) && !trimmed(doc?.metadata?.cell) && trimmed(doc?.metadata?.name))
    .map(doc => doc.metadata.name)
    .sort();
}

// stateLabel is a draft source's state as a badge.
export function stateLabel(state) {
  return { stored: "stored", new: "new Source", conflict: "conflict", missing: "not stored" }[state] ?? text(state);
}
