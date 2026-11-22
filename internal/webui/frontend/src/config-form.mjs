// Pure logic of the "new config from a blueprint" form (#1472, step 16). A
// blueprint is a form and a config is the answers: everything here derives the
// form from the CellBlueprint document alone and turns the operator's answers
// into a CellConfig spec. No DOM, no network — unit-tested with node --test.

import { deliveryEnv, deliveryFile, deliveryGit } from "./connectors.mjs";

const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === "string" ? value : "";
const trimmed = value => text(value).trim();
const unique = values => [...new Set(values.map(trimmed).filter(Boolean))];

function parameter(raw) {
  return {
    name: text(raw?.name),
    description: text(raw?.description),
    hasDefault: typeof raw?.default === "string",
    default: text(raw?.default),
    required: raw?.required === true,
    type: ["enum", "path", "image", "bool"].includes(raw?.type) ? raw.type : "string",
    enum: list(raw?.enum).filter(value => typeof value === "string"),
    group: text(raw?.group),
  };
}
function parameters(raw) { return list(raw).map(parameter).filter(item => item.name); }
function secretSlots(raw) {
  return list(raw).filter(slot => text(slot?.name)).map(slot => ({ name: slot.name, envName: text(slot.envName), required: slot.required === true }));
}
// connectorOf reads the connector definition a connector component carries
// (#1548): which connector it delivers, by which method and handler.
function connectorOf(raw) {
  if (!raw || typeof raw !== "object" || !text(raw.name)) return null;
  return {
    name: raw.name, title: text(raw.title), method: text(raw.method), delivery: text(raw.delivery), optional: raw.optional === true,
    secretTypes: list(raw.secretTypes).filter(value => typeof value === "string"),
    scopes: list(raw.scopes).filter(value => typeof value === "string"),
  };
}

// blueprintForm reads the form contract out of a CellBlueprint document:
// typed parameters, requires, components with their modes, and the repo and
// secret slots of the cell template. A repo that carries its own url is not a
// slot.
export function blueprintForm(blueprint) {
  const spec = blueprint?.spec ?? {};
  const components = list(spec.components).filter(component => text(component?.name)).map(component => ({
    kind: text(component.kind),
    name: component.name,
    description: text(component.description),
    optional: component.optional === true,
    group: text(component.group),
    connector: connectorOf(component.connector),
    requires: unique(list(component.requires)),
    parameters: parameters(component.parameters),
    secrets: secretSlots(component.secrets),
    modes: Object.keys(component.modes ?? {}).sort().map(name => ({
      name,
      description: text(component.modes[name]?.description),
      parameters: parameters(component.modes[name]?.parameters),
      secrets: secretSlots(component.modes[name]?.secrets),
    })),
  }));
  const repoSlots = [];
  const containerSecrets = [];
  for (const container of list(spec.cell?.containers)) {
    for (const repo of list(container?.repos)) {
      if (!text(repo?.name) || text(repo.url)) continue;
      const known = repoSlots.find(slot => slot.name === repo.name);
      if (known) known.required ||= repo.required === true;
      else repoSlots.push({ name: repo.name, target: text(repo.target), required: repo.required === true });
    }
    for (const slot of secretSlots(container?.secrets)) {
      const known = containerSecrets.find(item => item.name === slot.name);
      if (known) known.required ||= slot.required;
      else containerSecrets.push(slot);
    }
  }
  return {
    requires: unique(list(spec.requires)),
    parameters: parameters(spec.parameters),
    components,
    modes: unique(components.flatMap(component => component.modes.map(mode => mode.name))).sort(),
    repoSlots,
    secretSlots: containerSecrets,
  };
}

// emptyAnswers is a blank form for a blueprint: nothing optional enabled, every
// mode allowed, every group on its first member and every connector on its
// default identity.
export function emptyAnswers(form) {
  return { values: {}, components: [], groups: {}, connectors: {}, requires: "", prefix: "", modes: { enabled: [...form.modes], default: "" }, repos: {}, secrets: {}, git: null };
}

// groupChoice is the member of a mutually exclusive group the answers enable:
// exactly one, the first when nothing was chosen.
export function groupChoice(form, answers, group) {
  const members = form.components.filter(component => component.optional && component.group === group);
  const chosen = answers?.groups?.[group];
  return members.some(component => component.name === chosen) ? chosen : members[0]?.name ?? "";
}

function enabledModes(form, answers) {
  const enabled = list(answers?.modes?.enabled).filter(name => form.modes.includes(name));
  return enabled.length ? form.modes.filter(name => enabled.includes(name)) : [...form.modes];
}

// previewMode picks the mode a preview renders when the operator named none,
// in the daemon's order: the config default, the only enabled mode,
// "interactive", then the first by name.
export function previewMode(form, answers, requested = "") {
  const enabled = enabledModes(form, answers);
  if (!enabled.length) return "";
  if (enabled.includes(requested)) return requested;
  const fallback = trimmed(answers?.modes?.default);
  if (enabled.includes(fallback)) return fallback;
  if (enabled.length === 1 || !enabled.includes("interactive")) return enabled[0];
  return "interactive";
}

function activeComponents(form, answers) {
  const enabled = list(answers?.components);
  return form.components.filter(component => !component.optional
    || (component.group ? groupChoice(form, answers, component.group) === component.name : enabled.includes(component.name)));
}
// slotDelivery reports a connector whose identity fills a secret slot. The
// handler names are spelled once, in connectors.mjs.
function slotDelivery(connector) { return connector?.delivery === deliveryEnv || connector?.delivery === deliveryFile; }

// formSections lists what the form shows for the current answers: the
// blueprint's own parameters grouped by `group`, then one section per active
// component (its parameters and secret slots), and the picked mode's own
// parameters and secret slots under the component that declares it.
export function formSections(form, answers, mode = "") {
  const sections = [];
  const groups = [];
  for (const item of form.parameters) {
    const name = item.group || "general";
    let group = groups.find(entry => entry.name === name);
    if (!group) groups.push(group = { name, parameters: [] });
    group.parameters.push(item);
  }
  for (const group of groups) sections.push({ key: `group:${group.name}`, title: group.name, kind: "group", parameters: group.parameters, secrets: [] });
  if (form.secretSlots.length) sections.push({ key: "secrets", title: "secrets", kind: "group", parameters: [], secrets: form.secretSlots });
  const picked = previewMode(form, answers, mode);
  for (const component of activeComponents(form, answers)) {
    const active = component.modes.find(entry => entry.name === picked);
    if (component.parameters.length || component.secrets.length || component.connector) sections.push({ key: `component:${component.name}`, title: component.name, kind: component.kind || "component", description: component.description, parameters: component.parameters, secrets: component.secrets, ...(component.connector ? { connector: component.connector, component: component.name } : {}) });
    if (active && (active.parameters.length || active.secrets.length)) sections.push({ key: `mode:${component.name}:${active.name}`, title: `${component.name} · ${active.name} mode`, kind: "mode", description: active.description, parameters: active.parameters, secrets: active.secrets });
  }
  return sections;
}

// requiredCapabilities is what an image must provide: the blueprint's
// requires ∪ the enabled components' ∪ the config's own.
export function requiredCapabilities(form, answers) {
  return unique([
    ...form.requires,
    ...activeComponents(form, answers).flatMap(component => component.requires),
    ...text(answers?.requires).split(/[\s,]+/),
  ]).sort();
}

// imageChoices filters a realm's image inventory to the images a cell of this
// config can run on. An image whose label lists every required capability is a
// match; an unlabeled image stays selectable but is marked unknown, since the
// daemon cannot check it either; a labeled image missing a capability is left
// out.
export function imageChoices(images, required) {
  const choices = [];
  for (const image of list(images)) {
    if (!text(image?.name)) continue;
    const capabilities = list(image.capabilities).filter(value => typeof value === "string");
    if (!capabilities.length) choices.push({ name: image.name, capabilities, status: "unknown" });
    else if (required.every(need => capabilities.includes(need))) choices.push({ name: image.name, capabilities, status: "match" });
  }
  return choices.sort((a, b) => a.status === b.status ? a.name.localeCompare(b.name) : a.status === "match" ? -1 : 1);
}

// imageStatus explains a typed image reference against the inventory.
export function imageStatus(images, name, required) {
  const image = list(images).find(item => item?.name === trimmed(name));
  if (!trimmed(name) || !image) return { status: "absent", missing: [] };
  const capabilities = list(image.capabilities);
  if (!capabilities.length) return { status: "unknown", missing: [] };
  const missing = required.filter(need => !capabilities.includes(need));
  return { status: missing.length ? "missing" : "match", missing };
}

function secretRef(ref) {
  const name = trimmed(ref?.name);
  const realm = trimmed(ref?.realm);
  if (!name || !realm) return undefined;
  const out = { name, realm };
  for (const key of ["space", "stack", "cell"]) if (trimmed(ref[key])) out[key] = trimmed(ref[key]);
  return out;
}
function identity(raw) {
  const name = trimmed(raw?.name);
  const email = trimmed(raw?.email);
  return name || email ? { name, email } : undefined;
}
function signing(raw) {
  const ref = secretRef(raw?.secretRef);
  if (!ref) return undefined;
  const allowed = secretRef(raw?.allowedSignersRef);
  return allowed ? { secretRef: ref, allowedSignersRef: allowed } : { secretRef: ref };
}
function credentialOptions(raw) {
  const out = {};
  if (trimmed(raw?.user)) out.user = trimmed(raw.user);
  const port = Number(raw?.port);
  if (Number.isInteger(port) && port > 0) out.port = port;
  if (raw?.hostKeyPolicy === "strict" || raw?.hostKeyPolicy === "accept-new") out.hostKeyPolicy = raw.hostKeyPolicy;
  return out;
}
function signSelection(raw) { return ["commits", "tags"].filter(value => list(raw).includes(value)); }

// repoGit is the git identity one source states for itself; undefined when the
// operator stated none, so the source uses the inherited default.
function repoGit(raw) {
  if (!raw || raw.enabled === false) return undefined;
  const out = {};
  if (identity(raw.author)) out.author = identity(raw.author);
  if (identity(raw.committer)) out.committer = identity(raw.committer);
  const ref = secretRef(raw.credential?.secretRef);
  if (ref) out.credential = { secretRef: ref, ...credentialOptions(raw.credential) };
  if (signing(raw.signing)) {
    out.signing = signing(raw.signing);
    if (signSelection(raw.sign).length) out.sign = signSelection(raw.sign);
  }
  return Object.keys(out).length ? out : undefined;
}

// defaultGit is the config's override of the inherited default identity.
function defaultGit(raw) {
  if (!raw || raw.enabled === false) return undefined;
  const out = {};
  if (identity(raw.author)) out.author = identity(raw.author);
  if (identity(raw.committer)) out.committer = identity(raw.committer);
  const credentials = list(raw.credentials).map(entry => {
    const ref = secretRef(entry?.secretRef);
    return trimmed(entry?.host) && ref ? { host: trimmed(entry.host), secretRef: ref, ...credentialOptions(entry) } : undefined;
  }).filter(Boolean);
  if (credentials.length) out.credentials = credentials;
  if (signing(raw.signing)) {
    out.signing = signing(raw.signing);
    if (signSelection(raw.sign).length) out.sign = signSelection(raw.sign);
  }
  return Object.keys(out).length ? out : undefined;
}

// buildSpec turns the answers into the CellConfig spec the daemon stores. Only
// what the operator answered is written: an untouched parameter keeps its
// blueprint default, an unfilled optional slot is dropped, and answers that
// belong to a component the config leaves off are not carried.
export function buildSpec(blueprintRef, form, answers) {
  const spec = { blueprint: {} };
  for (const key of ["name", "realm", "space", "stack"]) if (trimmed(blueprintRef?.[key])) spec.blueprint[key] = trimmed(blueprintRef[key]);
  if (trimmed(answers.prefix)) spec.prefix = trimmed(answers.prefix);

  const components = activeComponents(form, answers);
  const modes = enabledModes(form, answers);
  const visible = [
    ...form.parameters,
    ...components.flatMap(component => [...component.parameters, ...component.modes.filter(mode => modes.includes(mode.name)).flatMap(mode => mode.parameters)]),
  ];
  const values = {};
  for (const item of visible) {
    const value = answers.values?.[item.name];
    if (item.type === "bool") { if (value === true || value === false) values[item.name] = String(value); continue; }
    if (typeof value === "string" && value !== "") values[item.name] = value;
  }
  if (Object.keys(values).length) spec.values = values;

  const repos = {};
  for (const slot of form.repoSlots) {
    const answer = answers.repos?.[slot.name];
    if (!trimmed(answer?.url)) continue;
    const fill = { url: trimmed(answer.url) };
    if (answer.revision === "ref") { if (trimmed(answer.ref)) fill.ref = trimmed(answer.ref); }
    else if (trimmed(answer.branch)) fill.branch = trimmed(answer.branch);
    const git = repoGit(answer.git);
    if (git) fill.git = git;
    repos[slot.name] = fill;
  }
  if (Object.keys(repos).length) spec.repos = repos;

  const slots = [
    ...form.secretSlots,
    ...components.flatMap(component => [...component.secrets, ...component.modes.filter(mode => modes.includes(mode.name)).flatMap(mode => mode.secrets)]),
  ];
  const secrets = {};
  for (const slot of slots) {
    const ref = secretRef(answers.secrets?.[slot.name]);
    if (ref) secrets[slot.name] = { secretRef: ref };
  }
  // A connector's identity pick is a fill of its slot; "Default" writes none,
  // so the Config keeps inheriting the Realm, Space and Stack default.
  let gitIdentity;
  for (const component of components) {
    const ref = secretRef(answers.connectors?.[component.name]);
    if (!component.connector || !ref) continue;
    delete ref.cell;
    if (slotDelivery(component.connector) && component.secrets[0]) secrets[component.secrets[0].name] = { secretRef: ref };
    else if (component.connector.delivery === deliveryGit) gitIdentity = ref;
  }
  if (Object.keys(secrets).length) spec.secrets = secrets;

  const requires = unique(text(answers.requires).split(/[\s,]+/));
  if (requires.length) spec.requires = requires;
  const enabled = components.filter(component => component.optional).map(component => component.name);
  if (enabled.length) spec.components = enabled;
  if (form.modes.length) {
    const fallback = modes.includes(trimmed(answers.modes?.default)) ? trimmed(answers.modes.default) : "";
    if (modes.length < form.modes.length || fallback) spec.modes = { ...(modes.length < form.modes.length ? { enabled: modes } : {}), ...(fallback ? { default: fallback } : {}) };
  }
  const git = defaultGit(answers.git);
  if (git || gitIdentity) spec.git = { ...(gitIdentity ? { identityRef: gitIdentity } : {}), ...git };
  return spec;
}

// missingAnswers names the required answers still blank, so the form can say
// what blocks a preview before asking the daemon.
export function missingAnswers(form, answers, mode = "") {
  const missing = [];
  for (const section of formSections(form, answers, mode)) {
    for (const item of section.parameters) {
      const value = answers.values?.[item.name];
      if (item.required && !item.hasDefault && item.type !== "bool" && !(typeof value === "string" && value !== "")) missing.push(item.name);
    }
    // A connector's slot may resolve from a scope default; connectorsWithoutIdentity reports those.
    if (section.connector && slotDelivery(section.connector)) continue;
    for (const slot of section.secrets) if (slot.required && !secretRef(answers.secrets?.[slot.name])) missing.push(slot.name);
  }
  for (const slot of form.repoSlots) if (slot.required && !trimmed(answers.repos?.[slot.name]?.url)) missing.push(slot.name);
  return missing;
}

const identityFields = ["author", "committer", "credential", "signing"];

// groupGitValues splits a render's merge explanation into the git identity
// each repo ends up with — author, committer, credential and signing, each
// with the level that decided it — the default identity the hierarchy merges
// to (`default.author`, `default.credential[<host>]`, …), and every other
// merged value. Every repo is listed with a `<repo>.url` entry, which is what
// tells a repo's own key from a default one: neither a repo name nor a
// credential host is split on the dots it contains.
export function groupGitValues(values) {
  const repos = [];
  const defaults = [];
  const rest = [];
  const git = [];
  for (const value of list(values)) (value?.kind === "git" ? git : rest).push(value);
  const names = git.map(value => text(value.key)).filter(key => key.endsWith(".url")).map(key => key.slice(0, -".url".length))
    .sort((a, b) => b.length - a.length);
  const repoOf = name => {
    let repo = repos.find(entry => entry.repo === name);
    if (!repo) repos.push(repo = { repo: name, url: "", fields: [] });
    return repo;
  };
  for (const value of git) {
    const key = text(value.key);
    const entry = { value: text(value.value), origin: text(value.origin) };
    // Only the default identity keys a credential by host, so that form is a
    // default even beside a repo that happens to be called "default".
    const name = /^default\.credential\[.*\]$/.test(key) ? undefined : names.find(candidate => key.startsWith(`${candidate}.`));
    if (name !== undefined) {
      const field = key.slice(name.length + 1);
      if (field === "url") repoOf(name).url = entry.value;
      else repoOf(name).fields.push({ field, ...entry });
      continue;
    }
    if (key.startsWith("default.")) {
      const field = key.slice("default.".length);
      const host = /^credential\[(.*)\]$/.exec(field);
      defaults.push(host ? { field: "credential", host: host[1], ...entry } : { field, host: "", ...entry });
      continue;
    }
    const cut = key.lastIndexOf(".");
    repoOf(cut > 0 ? key.slice(0, cut) : key).fields.push({ field: cut > 0 ? key.slice(cut + 1) : "", ...entry });
  }
  const order = entry => identityFields.indexOf(entry.field);
  for (const repo of repos) repo.fields.sort((a, b) => order(a) - order(b));
  defaults.sort((a, b) => order(a) - order(b) || a.host.localeCompare(b.host));
  return { repos, defaults, rest };
}

// valueSources is the order the New Cell preview lists what each source of a
// render contributes (#1603): what the Blueprint and Config say, the
// connectors, then the Realm, Space and Stack defaults and the cell's own picks.
const valueSources = ["blueprint", "config", "connectors", "realm", "space", "stack", "cell"];

// groupValuesBySource splits a preview's merged values by the source that set
// them, in valueSources order. The connectors group holds the connector
// statuses. Empty groups are dropped; a value from a source outside the list
// lands in a last "other" group, so every value is listed exactly once.
export function groupValuesBySource(values, connectors) {
  const groups = new Map(valueSources.map(source => [source, []]));
  const other = [];
  for (const value of list(values)) {
    const rows = value && value.origin !== "connectors" ? groups.get(text(value.origin)) : undefined;
    (rows ?? other).push(value);
  }
  groups.set("connectors", list(connectors).slice());
  const out = valueSources.map(source => ({ source, rows: groups.get(source) })).filter(group => group.rows.length > 0);
  if (other.length) out.push({ source: "other", rows: other });
  return out;
}

// secretChoices narrows the secret catalog to the types a slot accepts. An
// untyped secret is opaque.
export function secretChoices(secrets, types) {
  return list(secrets).filter(doc => text(doc?.metadata?.name) && text(doc.metadata.realm))
    .map(doc => ({ name: doc.metadata.name, realm: doc.metadata.realm, space: text(doc.metadata.space), stack: text(doc.metadata.stack), cell: text(doc.metadata.cell), type: text(doc.spec?.type) || "opaque" }))
    .filter(choice => !types?.length || types.includes(choice.type))
    .sort((a, b) => [a.realm, a.space, a.stack, a.cell, a.name].join("/").localeCompare([b.realm, b.space, b.stack, b.cell, b.name].join("/")));
}

// connectorSections are the sections of a form that carry a connector, i.e.
// the connector components the answers enable.
export function connectorSections(form, answers, mode = "") {
  return formSections(form, answers, mode).filter(section => section.connector);
}

// connectorsWithoutIdentity names the required connectors of the form that
// nothing fills: no identity picked for the Config and, per the scope defaults
// read for its target (GET /api/connectors/defaults), no default either.
// Unknown defaults (not loaded) report nothing.
export function connectorsWithoutIdentity(form, answers, defaults, mode = "") {
  if (!Array.isArray(defaults)) return [];
  const missing = [];
  for (const section of connectorSections(form, answers, mode)) {
    const { connector } = section;
    const name = section.component;
    if (connector.optional || !(slotDelivery(connector) || connector.delivery === deliveryGit)) continue;
    if (secretRef(answers.connectors?.[name])) continue;
    const row = defaults.find(item => item?.connector === connector.name && item.component === name);
    if (!row?.effective) missing.push(connector.title || connector.name);
  }
  return missing;
}
