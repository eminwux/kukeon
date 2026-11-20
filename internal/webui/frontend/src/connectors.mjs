// Pure logic of the connectors UI (#1548, §7). A connector is data: every
// function here works on whatever GET /api/connectors, the Secret catalog and a
// Config's status.connectors return, and none names a connector. The only
// closed sets the UI may branch on live here as constants — the delivery
// handlers, the identity field types and the Secret types — so no other module
// spells them. No DOM, no network — unit-tested with node --test.

// Delivery handlers: how an identity reaches a cell.
export const deliveryEnv = "env";
export const deliveryFile = "file";
export const deliveryStateVolume = "stateVolume";
export const deliveryGit = "git";
export const deliveryRegistry = "registry";

// Identity field types: how the string in Secret.spec.identity is read.
export const fieldString = "string";
export const fieldList = "list";
export const fieldPerson = "person";
export const fieldSecret = "secret";

// Secret types an identity is stored as.
export const secretOpaque = "opaque";
export const secretBasicAuth = "basic-auth";
export const secretSSHKey = "ssh-key";

// Levels a default may be set at, shallowest first: a deeper level wins.
export const scopeLevels = ["realm", "space", "stack"];
export const stateResolved = "resolved";
// The label `kuke team` stamps on the Configs it renders.
export const teamLabel = "kukeon.io/team";
// An open Config coordinate lands in the scope of this name.
export const defaultScopeName = "default";

const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === "string" ? value : "";
const trimmed = value => text(value).trim();

// takesIdentity reports whether a delivery is filled by a stored identity a
// default, a Config or a cell can choose. A login kept in the state volume has
// none, and a registry credential is bound per server on the Realm.
export function takesIdentity(delivery) {
  return delivery === deliveryEnv || delivery === deliveryFile || delivery === deliveryGit;
}

// secretRef normalizes a Secret reference to the coordinates the API accepts
// (name and realm, space and stack optional); undefined when it names nothing.
export function secretRef(value) {
  const name = trimmed(value?.name);
  const realm = trimmed(value?.realm);
  if (!name || !realm) return undefined;
  const out = { name, realm };
  if (trimmed(value.space)) out.space = trimmed(value.space);
  if (out.space && trimmed(value.stack)) out.stack = trimmed(value.stack);
  return out;
}
export function refKey(value) {
  const ref = secretRef(value);
  return ref ? JSON.stringify([ref.realm, ref.space ?? "", ref.stack ?? "", ref.name]) : "";
}
export function sameRef(a, b) { return refKey(a) === refKey(b); }
export function refScope(value) {
  const ref = secretRef(value);
  return ref ? [ref.realm, ref.space, ref.stack].filter(Boolean) : [];
}
// refLabel is how an identity is named in text: "name — realm / space / stack".
export function refLabel(value) {
  const ref = secretRef(value);
  return ref ? `${ref.name} — ${refScope(ref).join(" / ")}` : "";
}
// formatSecretReference writes a reference the way a `secret` identity field
// stores it: name[@realm[/space[/stack]]].
export function formatSecretReference(value) {
  const ref = secretRef(value);
  return ref ? `${ref.name}@${refScope(ref).join("/")}` : "";
}

// identitiesFor lists the identities of one connector: the Secrets tagged with
// its name. Only the readable part of a Secret is ever looked at.
export function identitiesFor(secrets, connector) {
  return list(secrets)
    .filter(doc => trimmed(doc?.metadata?.name) && trimmed(doc.metadata.realm) && !trimmed(doc.metadata.cell) && !!connector && text(doc.spec?.connector) === connector)
    .map(doc => ({
      name: doc.metadata.name, realm: doc.metadata.realm, space: text(doc.metadata.space), stack: text(doc.metadata.stack),
      type: text(doc.spec?.type) || secretOpaque,
      identity: Object.fromEntries(Object.entries(doc.spec?.identity ?? {}).filter(([, value]) => typeof value === "string")),
    }))
    .sort((a, b) => [a.realm, a.space, a.stack, a.name].join("/").localeCompare([b.realm, b.space, b.stack, b.name].join("/")));
}

// connectorStatuses is what a Config (or a preview) reports for the connectors
// it requires.
export function connectorStatuses(config) { return list(config?.status?.connectors).filter(status => text(status?.connector)); }
// blocksCells mirrors the daemon: a cell cannot be created while a required
// connector has no identity, or while a named identity cannot be used.
export function blocksCells(status) {
  return !!status && status.state !== stateResolved && (status.required === true || !!status.identity);
}
export function blockingConnectors(config) { return connectorStatuses(config).filter(blocksCells); }
export function connectorTitle(status) { return text(status?.title) || text(status?.connector); }
// originLabel says where an identity comes from: a Realm, Space or Stack
// default, the Config's own pick (a team's, for a team-owned Config) or the
// pick made for one cell.
export function originLabel(origin, team = "") {
  const level = text(origin);
  if (scopeLevels.includes(level)) return `${level[0].toUpperCase()}${level.slice(1)} default`;
  if (level === "config") return team ? `set by team ${team}` : "picked for this Config";
  if (level === "cell") return "picked for this cell";
  return level;
}
// teamOf names the team that owns a Config; `kuke team add` re-renders such a
// Config, so its identities are shown read-only.
export function teamOf(config) { return text(config?.metadata?.labels?.[teamLabel]); }

// targetScope is where cells of a Config land, and so the scope whose defaults
// it inherits: an open coordinate means the default space or stack.
export function targetScope(metadata) {
  return { realm: trimmed(metadata?.realm), space: trimmed(metadata?.space) || defaultScopeName, stack: trimmed(metadata?.stack) || defaultScopeName };
}
// scopeAt cuts a scope down to one level.
export function scopeAt(scope, level) {
  const out = { realm: trimmed(scope?.realm) };
  if (level !== scopeLevels[0] && trimmed(scope?.space)) out.space = trimmed(scope.space);
  if (level === scopeLevels[2] && out.space && trimmed(scope?.stack)) out.stack = trimmed(scope.stack);
  return out;
}
export function scopeLevel(scope) { return trimmed(scope?.stack) ? scopeLevels[2] : trimmed(scope?.space) ? scopeLevels[1] : scopeLevels[0]; }
export function scopeParts(scope) { return [trimmed(scope?.realm), trimmed(scope?.space), trimmed(scope?.stack)].filter(Boolean); }
// scopeFromParts reads a [realm, space, stack] path.
export function scopeFromParts(parts) {
  const [realm, space, stack] = list(parts).map(trimmed);
  return scopeAt({ realm, space, stack }, scopeLevels[2]);
}
// connectorLevels are the levels a connector accepts a default (and stores an
// identity) at; a connector that names none accepts every level.
export function connectorLevels(connector) {
  const accepted = list(connector?.scopes).length ? connector.scopes : scopeLevels;
  return scopeLevels.filter(level => accepted.includes(level));
}
// allowedLevels are the levels a default of the connector may be set at within
// a scope: the connector's own `scopes`, limited to the coordinates the scope
// names.
export function allowedLevels(connector, scope) {
  return connectorLevels(connector).filter(level => scopeLevel(scopeAt(scope, level)) === level);
}
export function scopeQuery(scope) {
  const params = new URLSearchParams();
  scopeLevels.forEach(level => { if (trimmed(scope?.[level])) params.set(level, trimmed(scope[level])); });
  return params.toString();
}

// defaultTargets lists the places a connector keeps a default identity: one
// per method delivered through a secret slot, one for a git-delivered
// connector. A state-volume login has none.
export function defaultTargets(connector) {
  const methods = list(connector?.methods);
  if (!methods.length) return connector?.delivery === deliveryGit ? [{ method: "", component: text(connector.name), delivery: deliveryGit, slot: "", description: "" }] : [];
  return methods.filter(method => (method?.delivery === deliveryEnv || method?.delivery === deliveryFile) && text(method.slot))
    .map(method => ({ method: text(method.name), component: text(method.component), delivery: method.delivery, slot: method.slot, description: text(method.description) }));
}
// defaultRow finds the row of GET /api/connectors/defaults for one target.
export function defaultRow(defaults, target) {
  return list(defaults).find(row => row?.connector === target?.connector && (target.component ? row.component === target.component : text(row.method) === text(target.method)));
}
// loginMethods are the methods that keep a login in the state volume.
export function loginMethods(connector) { return list(connector?.methods).filter(method => method?.delivery === deliveryStateVolume); }
// isRegistry reports a connector bound per registry server on the Realm.
export function isRegistry(connector) { return !list(connector?.methods).length && connector?.delivery === deliveryRegistry; }

// usageFor reads one connector's row of GET /api/connectors/status.
export function usageFor(usage, connector) {
  const row = list(usage).find(item => item?.connector === connector);
  return { configs: Number(row?.configs) || 0, missing: list(row?.missing) };
}

// groupComponents splits a blueprint form's optional components into the
// mutually exclusive groups (exactly one member is enabled, shown as a single
// choice) and the ones toggled on their own.
export function groupComponents(components) {
  const groups = [];
  const ungrouped = [];
  for (const component of list(components)) {
    if (!component?.optional) continue;
    if (!text(component.group)) { ungrouped.push(component); continue; }
    let group = groups.find(entry => entry.group === component.group);
    if (!group) groups.push(group = { group: component.group, members: [] });
    group.members.push(component);
  }
  return { groups, ungrouped };
}
// defaultGroupChoices picks the member each group starts on: the method the
// connector catalog marks default, else the first member.
export function defaultGroupChoices(components, connectors) {
  const out = {};
  for (const { group, members } of groupComponents(components).groups) {
    const preferred = members.find(member => {
      const catalog = list(connectors).find(connector => connector?.name === member.connector?.name);
      return list(catalog?.methods).some(method => method?.default === true && (method.component === member.name || (text(method.name) && method.name === member.connector?.method)));
    });
    out[group] = (preferred ?? members[0]).name;
  }
  return out;
}

// pickableStatuses are the connectors of a Config whose identity can be chosen.
export function pickableStatuses(statuses) { return list(statuses).filter(status => takesIdentity(status?.delivery)); }
// changedPicks keeps only the picks that differ from the identity the Config
// resolves on its own: those are the per-cell overrides a create sends.
export function changedPicks(statuses, picks) {
  const out = {};
  for (const status of pickableStatuses(statuses)) {
    const ref = secretRef(picks?.[status.connector]);
    if (ref && !sameRef(ref, status.identity)) out[status.connector] = ref;
  }
  return out;
}
// unresolvedConnectors are the connectors that still keep a cell from being
// created once the picks are applied: a blocking one nothing was picked for.
export function unresolvedConnectors(statuses, picks) {
  const changed = changedPicks(statuses, picks);
  return list(statuses).filter(status => blocksCells(status) && !changed[status.connector]);
}

// identityFields are the readable fields an identity of the given Secret type
// carries: a field limited to some types shows only for those.
export function identityFields(connector, type) {
  return list(connector?.fields).filter(field => text(field?.name) && (!list(field.secretTypes).length || field.secretTypes.includes(type)))
    .map(field => ({ name: field.name, type: [fieldList, fieldPerson, fieldSecret].includes(field.type) ? field.type : fieldString, required: field.required === true, help: text(field.help), enum: list(field.enum).filter(value => typeof value === "string") }));
}
export function secretTypesOf(connector) {
  const types = list(connector?.secretTypes).filter(type => [secretOpaque, secretBasicAuth, secretSSHKey].includes(type));
  return types.length ? types : [secretOpaque];
}
// emptyIdentityForm is a blank create form; given an existing identity it is
// the rotate form: same name, type and readable fields, material blank.
export function emptyIdentityForm(connector, existing) {
  const types = secretTypesOf(connector);
  return {
    name: text(existing?.name), type: types.includes(existing?.type) ? existing.type : types[0], fields: { ...(existing?.identity ?? {}) },
    value: "", username: "", password: "", privateKey: "", passphrase: "", publicKey: "", certificate: "", knownHosts: "",
  };
}
// parseList reads a comma-separated list field.
export function parseList(value) { return text(value).split(",").map(item => item.trim()).filter(Boolean); }
const personPattern = /^[^<>]+<[^<>\s@]+@[^<>\s@]+>$/;

// identityErrors names what still keeps an identity form from being sent. The
// daemon validates again; this only spares a round trip.
export function identityErrors(connector, form) {
  const errors = [];
  if (!trimmed(form?.name)) errors.push("Name is required.");
  for (const field of identityFields(connector, form?.type)) {
    const value = trimmed(form?.fields?.[field.name]);
    if (!value) { if (field.required) errors.push(`${field.name} is required.`); continue; }
    if (field.type === fieldPerson && !personPattern.test(value)) errors.push(`${field.name} must be written Name <email>.`);
    const values = field.type === fieldList ? parseList(value) : [value];
    if (field.enum.length && values.some(item => !field.enum.includes(item))) errors.push(`${field.name} must be one of: ${field.enum.join(", ")}.`);
  }
  if (form?.type === secretBasicAuth) { if (!trimmed(form.username)) errors.push("Username is required."); if (!text(form.password)) errors.push("Password or token is required."); }
  else if (form?.type === secretSSHKey) { if (!trimmed(form.privateKey)) errors.push("Private key is required."); }
  else if (!text(form?.value)) errors.push("Value is required.");
  return errors;
}
// identityPayload builds the body of POST /api/connectors/{name}/identities.
// Material is only ever placed here — never in a URL, a log or the page.
export function identityPayload(connector, scope, form) {
  const type = secretTypesOf(connector).includes(form?.type) ? form.type : secretTypesOf(connector)[0];
  const body = { scope: scopeAt(scope, scopeLevel(scope)), name: trimmed(form?.name), type };
  const identity = {};
  for (const field of identityFields(connector, type)) {
    const value = field.type === fieldList ? parseList(form?.fields?.[field.name]).join(",") : trimmed(form?.fields?.[field.name]);
    if (value) identity[field.name] = value;
  }
  if (Object.keys(identity).length) body.identity = identity;
  if (type === secretBasicAuth) { body.username = trimmed(form.username); body.password = text(form.password); }
  else if (type === secretSSHKey) {
    body.privateKey = text(form.privateKey);
    for (const key of ["passphrase", "publicKey", "certificate", "knownHosts"]) if (text(form[key])) body[key] = text(form[key]);
  } else body.value = text(form?.value);
  return body;
}
// clearMaterial blanks every write-only input of a form once it was sent.
export function clearMaterial(form) {
  return { ...form, value: "", password: "", privateKey: "", passphrase: "", publicKey: "", certificate: "", knownHosts: "" };
}

// registryEntries reads a Realm's registry credentials: a Secret-backed entry
// names its Secret, a legacy inline entry its username. A password is never
// part of either.
export function registryEntries(realm) {
  return list(realm?.spec?.registryCredentials).map(entry => {
    const ref = entry?.secretRef && trimmed(entry.secretRef.name) ? { name: trimmed(entry.secretRef.name), realm: trimmed(entry.secretRef.realm) || trimmed(realm?.metadata?.name) } : undefined;
    return { server: text(entry?.serverAddress), username: text(entry?.username), ...(ref ? { secret: ref } : {}) };
  });
}
