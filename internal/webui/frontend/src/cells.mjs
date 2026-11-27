import { configForCell } from "./relationships.mjs";

// Pure cell-list logic shared by the Workbench sidebar, rail and grid and by the Cells table (#1635).
// Rows are inventory rows: { doc, scope } with scope [realm, space, stack, cell]. No DOM, no network —
// unit-tested with node --test.

// A cell is active unless it is Stopped or Exited. Failed, Error, Degraded, Pending and Unknown stay
// listed because they need a look.
const inactive = new Set(["Stopped", "Exited"]);
export const isActiveCell = state => !inactive.has(state ?? "");

const sameScope = (a, b) => a.length === b.length && a.every((part, i) => part === b[i]);

// activeCells keeps the active rows, plus the open cell (openScope) whatever its state.
export function activeCells(rows, openScope = []) {
  return rows.filter(row => isActiveCell(row.doc.status?.state) || (openScope.length === 4 && sameScope(row.scope, openScope)));
}

// A cell needs attention when it is not Ready or drifts from its Config; the Overview's rule.
export const needsAttention = status => status?.state !== "Ready" || !!status?.outOfSync || !!status?.outOfSyncError;

// attentionFirst moves the rows that need attention to the front; each half keeps its order.
export function attentionFirst(rows) {
  return [...rows.filter(row => needsAttention(row.doc.status)), ...rows.filter(row => !needsAttention(row.doc.status))];
}

// State filter values of the Cells table: "" (all), "active", "inactive", or an exact state.
export function matchesState(state, filter) {
  if (!filter) return true;
  if (filter === "active") return isActiveCell(state);
  if (filter === "inactive") return !isActiveCell(state);
  return (state ?? "") === filter;
}

// screenTail returns the last `rows` lines of a terminal screen: line ends trimmed, trailing blank
// lines dropped. Lines are never wrapped; the tile clips them on the right.
export function screenTail(text, rows = 12) {
  const lines = (text ?? "").split(/\r?\n/).map(line => line.trimEnd());
  while (lines.length && !lines[lines.length - 1]) lines.pop();
  return lines.slice(-rows).join("\n");
}

// cellMatches applies a list's search: a case-insensitive substring of "realm / space / stack / cell".
export function cellMatches(row, search) {
  const query = (search ?? "").trim().toLocaleLowerCase();
  return !query || row.scope.join(" / ").toLocaleLowerCase().includes(query);
}

export const ungrouped = "Ungrouped";
// Controller-managed identity labels (realm.kukeon.io, cell.kukeon.io, …) repeat the scope or are unique per
// cell, so Group by leaves them out. Same ".kukeon.io" suffix as managedLabelSuffix in
// internal/controller/runner/labels.go.
const managedLabelKey = /^[^/]*\.kukeon\.io(\/|$)/;

// groupingKeys lists the label keys Group by offers, sorted; the current choice is always kept.
export function groupingKeys(rows, groupBy) {
  return [...new Set(rows.flatMap(row => Object.keys(row.doc.metadata.labels ?? {})))].filter(key => key === groupBy || !managedLabelKey.test(key)).sort();
}

export const groupId = (key, value) => JSON.stringify([key, value]);

// groupCells groups rows by the value of the groupBy label, values in order and the unlabelled group
// last. Without groupBy every row is in one "All cells" group.
export function groupCells(rows, groupBy) {
  if (!groupBy) return [{ id: groupId("", "All cells"), label: "All cells", rows }];
  const found = new Map();
  for (const row of rows) {
    const value = row.doc.metadata.labels?.[groupBy]?.trim() || null;
    const items = found.get(value) ?? [];
    items.push(row);
    found.set(value, items);
  }
  return [...found].sort(([a], [b]) => a === null ? 1 : b === null ? -1 : a.localeCompare(b))
    .map(([value, items]) => ({ id: groupId(groupBy, value), label: value === null ? ungrouped : value === ungrouped ? `${ungrouped} (label value)` : value, rows: items }));
}

// The label kukeond stamps on every cell created from a Config, holding the Config's name only
// (lookupLineageConfig in internal/controller/reconcile_outofsync.go).
export const configLabel = "kukeon.io/config";

// Well-known keys of the New agent flow (#1692). A template Config (kukeon.io/template: "true") is a
// hidden agent template of the default team; an agent copy names the template it was copied from in
// kukeon.io/agent-template; kukeon.io/display-name, on a Config or a cell, is the name the user chose.
export const templateLabel = "kukeon.io/template";
export const harnessLabel = "kukeon.io/harness";
export const displayNameAnnotation = "kukeon.io/display-name";
export const agentTemplateAnnotation = "kukeon.io/agent-template";

const text = value => typeof value === "string" ? value.trim() : "";
// isTemplate is true for a template Config, or a cell made from one (cells carry their Config's labels).
export const isTemplate = doc => text(doc?.metadata?.labels?.[templateLabel]) === "true";
// The display name a Config or a cell carries itself, or "".
export const ownDisplayName = doc => text(doc?.metadata?.annotations?.[displayNameAnnotation]);
const harnessTitle = harness => harness ? harness[0].toLocaleUpperCase() + harness.slice(1) : "";

// displayNameError checks a display name the way the server does (#1692): trimmed, at most 63
// characters, no control characters. Empty is valid: it clears the name. Returns "" when valid.
export function displayNameError(value) {
  const name = text(value);
  if ([...name].length > 63) return "A display name has at most 63 characters.";
  if (/[\u0000-\u001f\u007f-\u009f]/.test(name)) return "A display name cannot contain control characters.";
  return "";
}

// configDisplayName is the name an agent (one Config) is shown with: its kukeon.io/display-name, else
// "Claude agent" / "Codex agent" for an agent copy, else "" (the caller shows the Config name). `named`
// says whether the agent has a name of its own, so its instances are labelled by ordinal.
export function configDisplayName(config) {
  const own = ownDisplayName(config);
  if (own) return { name: own, named: true };
  if (text(config?.metadata?.annotations?.[agentTemplateAnnotation])) {
    const harness = harnessTitle(text(config.metadata.labels?.[harnessLabel]));
    return { name: harness ? `${harness} agent` : "Agent", named: true };
  }
  return { name: "", named: false };
}

// findConfig returns the Config doc of an agent: the exact scoped Config, or for a label-only agent (no
// space/stack known) the only Config of that realm and name.
function findConfig(configs, realm, space, stack, name) {
  const list = Array.isArray(configs) ? configs : [];
  const exact = list.find(config => text(config?.metadata?.realm) === realm && text(config?.metadata?.space) === space && text(config?.metadata?.stack) === stack && text(config?.metadata?.name) === name);
  if (exact || space || stack) return exact;
  const candidates = list.filter(config => text(config?.metadata?.realm) === realm && text(config?.metadata?.name) === name);
  return candidates.length === 1 ? candidates[0] : undefined;
}

// agentGroups lists cells as agents and their instances (#1686). An agent is one Config: the cell's
// spec.provenance binding when it has one, otherwise its realm plus its kukeon.io/config label value (the
// label stores only the Config name). A label-only cell joins the provenance agent of the same realm and
// name when there is one, so both ways of naming one Config land in one agent. Cells with neither go to
// `others`. Cells of a template Config (#1692) are neither agents nor others. Agents are sorted by name,
// then realm; rows keep inventory order. `context` is what tells two agents of one name apart: the realm,
// or the full Config scope when the realm is the same too. `configs` (the catalog list, optional) gives
// each agent its displayName — the Config's display name, the agent-copy fallback or the Config name —
// and `named` when the name is the agent's own rather than the Config name.
export function agentGroups(rows, configs = []) {
  rows = rows.filter(row => !isTemplate(row.doc));
  const keyOf = (realm, space, stack, name) => JSON.stringify([realm, space, stack, name]);
  const bound = new Map();
  for (const row of rows) {
    const ref = configForCell(row.doc);
    if (!ref) continue;
    const parts = [ref.realm || row.scope[0], ref.space ?? "", ref.stack ?? "", ref.name];
    const key = keyOf(...parts);
    const pair = JSON.stringify([parts[0], parts[3]]);
    if (!bound.has(pair) || key < bound.get(pair)) bound.set(pair, key);
  }
  const found = new Map();
  const others = [];
  for (const row of rows) {
    const ref = configForCell(row.doc);
    const label = row.doc.metadata.labels?.[configLabel]?.trim();
    let parts;
    if (ref) parts = [ref.realm || row.scope[0], ref.space ?? "", ref.stack ?? "", ref.name];
    else if (label) parts = JSON.parse(bound.get(JSON.stringify([row.scope[0], label])) ?? keyOf(row.scope[0], "", "", label));
    else { others.push(row); continue; }
    const key = keyOf(...parts);
    let agent = found.get(key);
    if (!agent) {
      const config = findConfig(configs, ...parts);
      if (config && isTemplate(config)) continue;
      const display = configDisplayName(config);
      agent = { id: groupId("agent", key), name: parts[3], displayName: display.name || parts[3], named: display.named, realm: parts[0], scope: parts.slice(0, 3).filter(Boolean), rows: [] };
    }
    agent.rows.push(row);
    found.set(key, agent);
  }
  const agents = [...found.values()].sort((a, b) => a.displayName.localeCompare(b.displayName) || a.name.localeCompare(b.name) || a.realm.localeCompare(b.realm) || a.id.localeCompare(b.id));
  for (const agent of agents) {
    const sameName = agents.filter(other => other.displayName === agent.displayName);
    agent.showRealm = sameName.some(other => other.realm !== agent.realm);
    agent.context = sameName.some(other => other !== agent && other.realm === agent.realm) ? agent.scope.join(" / ") : agent.showRealm ? agent.realm : "";
  }
  return { agents, others };
}

// filterAgents narrows the result of agentGroups to the rows `match` keeps, dropping agents left with no
// rows (#1688). Group every cell first and filter after: a label-only cell joins its provenance sibling's
// agent only when the sibling is in the grouped rows, so grouping an already searched list would move it to
// another agent id while the user types. Ids, names and context stay what the full list gave them.
export function filterAgents({ agents, others }, match) {
  return {
    agents: agents.map(agent => ({ ...agent, rows: agent.rows.filter(match) })).filter(agent => agent.rows.length),
    others: others.filter(match),
  };
}

// The creation time of a cell in milliseconds, or NaN when the daemon reports none (a zero time).
function createdAt(row) {
  const value = row.doc.status?.createdAt;
  return typeof value === "string" && !value.startsWith("0001-") ? Date.parse(value) : NaN;
}

// instanceLabels maps each cell (by JSON scope) to the label it is listed with (#1692): its own
// kukeon.io/display-name, else, for an agent with a name of its own, "<agent display name> #N" where N is
// its ordinal by creation time among the agent's instances (name order breaks ties and stands in for a
// missing time). A cell of an agent without a name of its own keeps its cell name, so it has no entry
// unless it carries a display name. Pass every cell, not a filtered list, so ordinals stay stable.
export function instanceLabels(rows, configs = []) {
  const labels = new Map();
  for (const row of rows) {
    const own = ownDisplayName(row.doc);
    if (own) labels.set(JSON.stringify(row.scope), own);
  }
  for (const agent of agentGroups(rows, configs).agents) {
    if (!agent.named) continue;
    const ordered = [...agent.rows].sort((a, b) => {
      const at = createdAt(a), bt = createdAt(b);
      if (Number.isFinite(at) && Number.isFinite(bt) && at !== bt) return at - bt;
      if (Number.isFinite(at) !== Number.isFinite(bt)) return Number.isFinite(at) ? -1 : 1;
      return a.scope.join("/").localeCompare(b.scope.join("/"));
    });
    ordered.forEach((row, i) => {
      const key = JSON.stringify(row.scope);
      if (!labels.has(key)) labels.set(key, `${agent.displayName} #${i + 1}`);
    });
  }
  return labels;
}

// rollupState is the one state an agent row shows: the state of the first instance that needs
// attention, otherwise the state of the first instance.
export function rollupState(rows) {
  return (rows.find(row => needsAttention(row.doc.status)) ?? rows[0])?.doc.status?.state;
}
