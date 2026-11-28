// The Identities page's rows (#1790): every credential a cell uses, in one list — the Secrets tagged
// with a connector and the login identities (the Volumes a harness keeps its login in) — with the roles
// and Configs using each, the scope defaults naming it, and the system default of each connector ×
// method pinned first. No DOM, no network — unit-tested with node --test.

import { defaultScopeName, defaultTargets, identitiesFor, isRegistry, loginIdentitiesFor, loginMethods, refKey, refScope, secretRef } from "./connectors.mjs";

const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === "string" ? value : "";

export const kindSecret = "secret";
export const kindLogin = "login";
// The system team whose agent role holds the default login identities (#1692).
export const defaultTeam = "default";
export const defaultRole = "agent";

// configRef names a Config by its scope and name.
function configRef(doc) {
  const md = doc?.metadata ?? {};
  return { name: text(md.name), realm: text(md.realm), ...(text(md.space) ? { space: md.space } : {}), ...(text(md.stack) ? { stack: md.stack } : {}) };
}
// roleOf is a Config's team role the way the server names it (apply.RoleName): `<team>/<role>`, the
// role alone outside a team, empty without a role. A New agent copy names its team in an annotation.
export function roleOf(doc) {
  const labels = doc?.metadata?.labels ?? {};
  const role = text(labels["kukeon.io/role"]).trim();
  if (!role) return "";
  const team = text(labels["kukeon.io/team"]).trim() || text(doc?.metadata?.annotations?.["kukeon.io/agent-team"]).trim();
  return team ? `${team}/${role}` : role;
}
const configKey = config => [config.realm, config.space ?? "", config.stack ?? "", config.name].join("/");
const uniqueSorted = values => [...new Set(values.filter(Boolean))].sort();

// secretUsers lists, per Secret key, the Configs whose status resolves a connector to it.
function secretUsers(configs) {
  const out = new Map();
  for (const doc of list(configs)) {
    for (const status of list(doc?.status?.connectors)) {
      const ref = secretRef(status?.identity);
      if (!ref) continue;
      const key = refKey(ref);
      const users = out.get(key) ?? [];
      const user = { ...configRef(doc), role: roleOf(doc) };
      if (!users.some(item => configKey(item) === configKey(user))) users.push(user);
      out.set(key, users);
    }
  }
  return out;
}

// scopeDefaults lists every identity a Realm, Space or Stack default names: the secret fills of its
// defaults and its git identity. scopes are the inventory rows of realms, spaces and stacks.
export function scopeDefaults(scopes) {
  const out = [];
  list(scopes).forEach((rows, depth) => {
    for (const row of list(rows)) {
      const defaults = row?.doc?.spec?.defaults;
      if (!defaults) continue;
      const scope = list(row.scope).slice(0, depth + 1);
      const at = { level: ["realm", "space", "stack"][depth], scope };
      for (const [slot, fill] of Object.entries(defaults.secrets ?? {})) {
        const ref = secretRef(fill?.secretRef);
        if (ref) out.push({ ...at, slot, ref });
      }
      const git = secretRef(defaults.git?.identityRef);
      if (git) out.push({ ...at, slot: "", git: true, ref: git });
    }
  });
  return out;
}

// identityRows builds one row per identity. connectors is the catalog; secrets the Secret catalog;
// logins GET /api/connectors/logins; configs the Config catalog (for the users of a Secret); scopes the
// inventory's realm, space and stack rows (for the defaults naming an identity).
export function identityRows({ connectors, secrets, logins, configs, scopes }) {
  const users = secretUsers(configs);
  const defaults = scopeDefaults(scopes);
  const rows = [];
  for (const connector of list(connectors)) {
    if (isRegistry(connector)) continue;
    const title = text(connector.title) || text(connector.name);
    for (const identity of identitiesFor(secrets, connector.name)) {
      const ref = secretRef(identity);
      const key = refKey(ref);
      const usedBy = (users.get(key) ?? []).slice().sort((a, b) => configKey(a).localeCompare(configKey(b)));
      const named = defaults.filter(item => refKey(item.ref) === key);
      rows.push({
        key: `${kindSecret}:${key}`, kind: kindSecret, connector: connector.name, title, ref, name: identity.name, scope: refScope(ref),
        method: "", type: identity.type, fields: identity.identity, status: "", exists: true,
        usedBy, roles: uniqueSorted(usedBy.map(user => user.role)), defaults: named,
        unused: !usedBy.length && !named.length,
      });
    }
    for (const login of loginIdentitiesFor(logins, connector.name)) {
      const ref = login.volume;
      const usedBy = login.usedBy.map(user => ({ ...user, role: text(user.role) }));
      rows.push({
        key: `${kindLogin}:${refKey(ref)}`, kind: kindLogin, connector: connector.name, title, ref, name: ref.name, scope: refScope(ref),
        method: login.method || "login", type: kindLogin, fields: {}, status: login.state, since: login.since, exists: login.exists, session: login.session,
        usedBy, roles: uniqueSorted(usedBy.map(user => user.role)), defaults: [],
        unused: !usedBy.length,
      });
    }
  }
  return rows;
}

// systemDefaults are the pinned rows, one per connector × method: for a login method, the login identity
// of the `default` team's `agent` role for that connector (default-agent-<harness>-state); for a token or
// key method — and a git-delivered connector — the Realm default of the default realm, or none (row and
// ref absent: "Not set up"). rows are identityRows' result; realmDefaults is GET
// /api/connectors/defaults?realm=default, undefined while it loads or when the realm is absent.
export function systemDefaults({ connectors, rows, realmDefaults }) {
  const out = [];
  for (const connector of list(connectors)) {
    if (isRegistry(connector)) continue;
    const title = text(connector.title) || text(connector.name);
    for (const method of loginMethods(connector)) {
      const row = list(rows).find(item => item.kind === kindLogin && item.connector === connector.name && item.roles.includes(`${defaultTeam}/${defaultRole}`));
      out.push({ key: `system:${connector.name}:${method.name}`, connector: connector.name, title, method: method.name, kind: kindLogin, row });
    }
    for (const target of defaultTargets(connector)) {
      const set = list(realmDefaults).find(item => item?.connector === connector.name && text(item.method) === target.method);
      const ref = secretRef(set?.levels?.realm);
      const row = ref ? list(rows).find(item => item.kind === kindSecret && item.connector === connector.name && refKey(item.ref) === refKey(ref)) : undefined;
      out.push({ key: `system:${connector.name}:${target.method}`, connector: connector.name, title, method: target.method, kind: kindSecret, row, ...(ref ? { ref } : {}) });
    }
  }
  return out;
}

// markDefaults adds the defaults of realmDefaults (GET /api/connectors/defaults for one realm) to the
// rows they name, so an identity only a Realm default uses is not Unused.
export function markDefaults(rows, realmDefaults, realm = defaultScopeName) {
  const named = list(realmDefaults).flatMap(item => {
    const ref = secretRef(item?.levels?.realm);
    return ref ? [{ level: "realm", scope: [realm], slot: text(item.slot), ref }] : [];
  });
  return list(rows).map(row => {
    if (row.kind !== kindSecret) return row;
    const more = named.filter(item => refKey(item.ref) === refKey(row.ref) && !row.defaults.some(have => have.level === item.level && have.scope.join("/") === item.scope.join("/") && refKey(have.ref) === refKey(item.ref)));
    if (!more.length) return row;
    const defaults = [...row.defaults, ...more];
    return { ...row, defaults, unused: !row.usedBy.length && !defaults.length };
  });
}

// filterRows keeps the rows of one connector; an empty filter keeps every row.
export function filterRows(rows, connector) {
  return connector ? list(rows).filter(row => row.connector === connector) : list(rows);
}

// usedByCount counts a row's users: its Configs, and the teams they belong to.
export function usedByCount(row) {
  const teams = uniqueSorted(list(row?.roles).map(role => role.includes("/") ? role.split("/")[0] : ""));
  return { configs: list(row?.usedBy).length, teams };
}

// scopeLabel words a row's scope as realm / space / stack.
export function scopeLabel(row) { return list(row?.scope).join(" / "); }
