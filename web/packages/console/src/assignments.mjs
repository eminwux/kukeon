// The Identities page's Assignments tab (#1790): which identity each team and role authenticates a
// harness with. Rows are the TeamDefaults (All teams), then each team with its roles; columns are the
// connectors that authenticate a harness. A cell writes the existing fields and nothing new:
// TeamDefaults.spec.defaults.connectors.<name>, Team.spec.connectors.<name> or
// Team.spec.roles[].connectors.<name>. No DOM, no network — unit-tested with node --test.

import { deliveryStateVolume, loginMethods, refKey, secretRef } from "./connectors.mjs";

const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === "string" ? value.trim() : "";
const record = value => value && typeof value === "object" && !Array.isArray(value) ? value : {};

// The values of a cell's picker. An identity value is `id:` followed by its notation.
export const pickInherit = "inherit";
export const pickOwn = "own";
export const pickSystem = "system";
export const pickIdentity = "id:";
export const pickMethod = "method:";

// harnessConnectors are the columns: the connectors that authenticate a harness.
export function harnessConnectors(connectors) { return list(connectors).filter(connector => text(connector?.harness)); }

// defaultMethod is the method a choice without one selects: the connector's default, else its first.
export function defaultMethod(connector) {
  const methods = list(connector?.methods);
  return methods.find(method => method?.default) ?? methods[0];
}
export function methodOf(connector, name) { return name ? list(connector?.methods).find(method => method?.name === name) : defaultMethod(connector); }
// keepsLoginMethod reports whether a choice's method keeps its login in the state volume.
export function keepsLoginMethod(connector, name) { return methodOf(connector, name)?.delivery === deliveryStateVolume; }

// identityNotation writes a Volume or Secret reference the way a team's connector choice names it:
// `name@realm[/space[/stack]]` (config.ParseIdentity).
export function identityNotation(ref) {
  const value = secretRef(ref);
  if (!value) return "";
  return `${value.name}@${[value.realm, value.space, value.stack].filter(Boolean).join("/")}`;
}
// parseNotation reads `name[@realm[/space[/stack]]]`; a bare name is in realm.
export function parseNotation(value, realm = "default") {
  const raw = text(value);
  if (!raw) return undefined;
  const [name, scope] = raw.split("@");
  const [r, space, stack] = (scope ?? realm).split("/");
  return secretRef({ name, realm: r, space, stack });
}
const sameIdentity = (a, b, realm) => !!a && !!b && refKey(parseNotation(a, realm)) === refKey(parseNotation(b, realm));

// normalChoice keeps the fields of a connector choice that are set.
export function normalChoice(choice) {
  const value = record(choice);
  const out = {};
  for (const key of ["method", "identity", "state"]) if (text(value[key])) out[key] = text(value[key]);
  return out;
}
export function sameChoice(a, b) { return JSON.stringify(normalChoice(a)) === JSON.stringify(normalChoice(b)); }

// mergeChoice is compose's: a role entry that names a method replaces the team's choice whole; else it
// fills its empty fields from the team's.
export function mergeChoice(team, role) {
  const t = normalChoice(team), r = normalChoice(role);
  if (r.method) return r;
  return normalChoice({ method: t.method, identity: r.identity || t.identity, state: r.state || t.state });
}

// resolve computes the choice a role ends up with, the way pairPlan.choice does: the role entry over
// the team's choices, whose map — when the team declares none — is the TeamDefaults'. found says whether
// any level chose; without one the role keeps its own state volume and the scope defaults.
// team: { inherit: boolean, items: Record<name, choice> }; role: { set, method, identity, state } | undefined.
export function resolve({ name, defaults, team, role }) {
  const teamMap = team?.inherit ? record(defaults) : record(team?.items);
  const teamChoice = teamMap[name];
  if (role?.set) return { found: true, choice: teamChoice ? mergeChoice(teamChoice, role) : normalChoice(role), from: "role" };
  if (teamChoice) return { found: true, choice: normalChoice(teamChoice), from: team?.inherit ? "defaults" : "team" };
  return { found: false, choice: {}, from: "" };
}

// systemNotation is the System default of a connector's login method: the login identity of the
// default team's agent role (default-agent-<harness>-state), from GET /api/connectors/logins.
export function systemNotation(logins, connector) {
  const login = list(logins).find(item => item?.connector === connector?.name && list(item.usedBy).some(user => user?.role === "default/agent"));
  return login ? identityNotation(login.volume) : "";
}

// pickOf is the picker value of a choice: own per role (a login method with no identity), the System
// default, an identity, or a token method.
export function pickOf(choice, connector, system, realm = "default") {
  const value = normalChoice(choice);
  if (!keepsLoginMethod(connector, value.method)) return `${pickMethod}${value.method ?? defaultMethod(connector)?.name ?? ""}`;
  if (!value.identity) return pickOwn;
  if (system && sameIdentity(value.identity, system, realm)) return pickSystem;
  return `${pickIdentity}${value.identity}`;
}

// choiceOf is the connector choice a picker value writes; state is the token method's harness state.
export function choiceOf(pick, connector, system, state = "") {
  const login = loginMethods(connector)[0]?.name ?? "";
  if (pick === pickOwn) return { method: login };
  if (pick === pickSystem) return { method: login, identity: system };
  if (pick.startsWith(pickIdentity)) return { method: login, identity: pick.slice(pickIdentity.length) };
  if (pick.startsWith(pickMethod)) return normalChoice({ method: pick.slice(pickMethod.length), state });
  return {};
}

// cellText words a resolved choice for a cell: the login identity, own per role, or the method.
export function cellText(resolved, connector, system, realm = "default") {
  if (!resolved?.found) return "own per role";
  const pick = pickOf(resolved.choice, connector, system, realm);
  if (pick === pickOwn) return "own per role";
  if (pick === pickSystem) return "System default";
  if (pick.startsWith(pickIdentity)) return pick.slice(pickIdentity.length).split("@")[0];
  const state = resolved.choice.state ? ` · ${resolved.choice.state}` : "";
  return `${pick.slice(pickMethod.length)}${state}`;
}

// roleRuns reports whether a role runs a connector's harness: its pinned harness, else any of the team's.
export function roleRuns(role, teamHarnesses, connector) {
  const harness = text(connector?.harness);
  return text(role?.harness) ? text(role.harness) === harness : list(teamHarnesses).some(item => text(item?.name ?? item) === harness);
}

// gridRows builds the rows: All teams, then each team and its roles. defaults is the TeamDefaults'
// connector choices; teams are GET /api/teams/<name> views (with view and form).
export function gridRows(defaults, teams) {
  const rows = [{ key: "defaults", kind: "defaults", label: "All teams (defaults)" }];
  for (const team of list(teams)) {
    const name = text(team?.summary?.name);
    if (!name || !team.form) continue;
    rows.push({ key: `team:${name}`, kind: "team", team: name, label: name, system: !!team.summary.system });
    for (const role of list(team.view?.roles?.items)) {
      rows.push({ key: `role:${name}/${role.name}`, kind: "role", team: name, role: text(role.name), label: text(role.name), harness: text(role.harness) });
    }
  }
  return rows;
}

// formRoleChoice is a role's item for a connector in the team form, as the server sends it.
export function formRoleChoice(form, role, connector) {
  const item = list(form?.roles?.items).find(entry => entry?.name === role);
  return list(item?.harnessConnectors).find(entry => entry?.name === connector);
}

// cellState is what one cell shows: its own value (the picker's), and what it resolves to.
export function cellState({ row, connector, defaults, team, system }) {
  const name = connector.name;
  if (row.kind === "defaults") {
    const own = record(defaults)[name];
    return { own: own ? normalChoice(own) : undefined, resolved: { found: !!own, choice: own ? normalChoice(own) : {}, from: own ? "defaults" : "" }, pick: own ? pickOf(own, connector, system) : pickInherit };
  }
  const form = team?.form;
  const teamConnectors = { inherit: !!form?.connectors?.inherit, items: record(form?.connectors?.items) };
  if (row.kind === "team") {
    const own = teamConnectors.inherit ? undefined : teamConnectors.items[name];
    return { own: own ? normalChoice(own) : undefined, resolved: resolve({ name, defaults, team: teamConnectors }), pick: own ? pickOf(own, connector, system) : pickInherit };
  }
  const role = formRoleChoice(form, row.role, name);
  const set = role?.set ? normalChoice(role) : undefined;
  return { own: set, resolved: resolve({ name, defaults, team: teamConnectors, role }), pick: set ? pickOf(set, connector, system) : pickInherit };
}

// withDefaultsChoice is the body of POST /api/team-defaults for one cell of the All teams row: the
// connector's new choice, or null to remove it.
export function defaultsBody(name, choice) { return { connectors: { [name]: choice && Object.keys(normalChoice(choice)).length ? normalChoice(choice) : null } }; }

// nextDefaults is the TeamDefaults' connector map after one change.
export function nextDefaults(defaults, name, choice) {
  const out = { ...record(defaults) };
  if (choice) out[name] = normalChoice(choice); else delete out[name];
  return out;
}

const sameMap = (a, b) => {
  const keys = new Set([...Object.keys(record(a)), ...Object.keys(record(b))]);
  return [...keys].every(key => record(a)[key] && record(b)[key] && sameChoice(record(a)[key], record(b)[key]));
};

// withTeamChoice is the team form after one cell of a team row changes. A team that inherits the
// defaults' choices declares them once it picks — the defaults' entries are copied so nothing else
// changes — and returns to inheriting when its map equals the defaults' again. Inherit (choice
// undefined) puts back the defaults' entry for the connector.
export function withTeamChoice(form, name, choice, defaults) {
  const current = form.connectors.inherit ? { ...record(defaults) } : { ...record(form.connectors.items) };
  if (choice) current[name] = normalChoice(choice);
  else if (record(defaults)[name]) current[name] = normalChoice(record(defaults)[name]);
  else delete current[name];
  const inherit = sameMap(current, defaults);
  return { ...form, connectors: { inherit, items: inherit ? {} : current } };
}

// withRoleChoice is the team form after one cell of a role row changes: the role item's harness
// connector is set to the choice, or unset (Inherit). A team that inherits its roles declares the
// defaults' roles first (inheritedRoles, the defaults' role form items), so only this role changes.
export function withRoleChoice(form, role, name, choice, inheritedRoles) {
  const roles = form.roles.inherit ? { inherit: false, items: structuredClone(list(inheritedRoles)) } : { ...form.roles, items: list(form.roles.items) };
  const items = roles.items.map(item => {
    if (item?.name !== role) return item;
    const harness = list(item.harnessConnectors).some(entry => entry?.name === name) ? list(item.harnessConnectors) : [...list(item.harnessConnectors), { name, set: false }];
    return { ...item, harnessConnectors: harness.map(entry => entry?.name !== name ? entry : choice ? { name, set: true, ...normalChoice(choice) } : { name, set: false }) };
  });
  return { ...form, roles: { ...roles, items } };
}

// inheritingTeams are the teams a change of the defaults' connector choices recomposes: those that
// declare no connectors of their own.
export function inheritingTeams(teams) {
  return list(teams).filter(team => team?.form?.connectors?.inherit).map(team => text(team.summary?.name)).filter(Boolean);
}

// identityOptions are the identities a connector's cell offers: the login identities of a login method,
// by notation, with their status; the System default is offered separately.
export function identityOptions(logins, connector, system) {
  return list(logins).filter(item => item?.connector === connector?.name && secretRef(item.volume))
    .map(item => ({ value: identityNotation(item.volume), label: item.volume.name, state: text(item.login?.state) }))
    .filter(item => item.value !== system)
    .sort((a, b) => a.value.localeCompare(b.value));
}

// affectedRoles are the roles, `<team>/<role>`, a change hands a connector choice to: the role of a role
// row; the team's roles that run the harness and choose nothing themselves for a team row; those of every
// team inheriting the defaults' choices for the All teams row.
export function affectedRoles(teams, row, connector) {
  const out = [];
  for (const team of list(teams)) {
    const name = text(team?.summary?.name);
    if (!name || (row.kind !== "defaults" && name !== row.team)) continue;
    if (row.kind === "defaults" && !team.form?.connectors?.inherit) continue;
    for (const role of list(team.view?.roles?.items)) {
      if (row.kind === "role" && role.name !== row.role) continue;
      if (!roleRuns(role, team.view?.harnesses?.items, connector)) continue;
      if (row.kind !== "role" && formRoleChoice(team.form, role.name, connector.name)?.set) continue;
      out.push(`${name}/${role.name}`);
    }
  }
  return out;
}

// sharedRoles are the roles that would keep their harness state in one login identity after a change:
// the roles already using it (GET /api/connectors/logins) and the ones the change points at it.
export function sharedRoles(logins, notation, affected) {
  const ref = parseNotation(notation);
  if (!ref) return [];
  const login = list(logins).find(item => refKey(item?.volume) === refKey(ref));
  return [...new Set([...list(login?.usedBy).map(user => text(user?.role)), ...list(affected)].filter(Boolean))].sort();
}
