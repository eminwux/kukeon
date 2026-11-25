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

// agentGroups lists cells as agents and their instances (#1686). An agent is one Config: the cell's
// spec.provenance binding when it has one, otherwise its realm plus its kukeon.io/config label value (the
// label stores only the Config name). A label-only cell joins the provenance agent of the same realm and
// name when there is one, so both ways of naming one Config land in one agent. Cells with neither go to
// `others`. Agents are sorted by name, then realm; rows keep inventory order. `context` is what tells two
// agents of one name apart: the realm, or the full Config scope when the realm is the same too.
export function agentGroups(rows) {
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
    const agent = found.get(key) ?? { id: groupId("agent", key), name: parts[3], realm: parts[0], scope: parts.slice(0, 3).filter(Boolean), rows: [] };
    agent.rows.push(row);
    found.set(key, agent);
  }
  const agents = [...found.values()].sort((a, b) => a.name.localeCompare(b.name) || a.realm.localeCompare(b.realm) || a.id.localeCompare(b.id));
  for (const agent of agents) {
    const sameName = agents.filter(other => other.name === agent.name);
    agent.showRealm = sameName.some(other => other.realm !== agent.realm);
    agent.context = sameName.some(other => other !== agent && other.realm === agent.realm) ? agent.scope.join(" / ") : agent.showRealm ? agent.realm : "";
  }
  return { agents, others };
}

// rollupState is the one state an agent row shows: the state of the first instance that needs
// attention, otherwise the state of the first instance.
export function rollupState(rows) {
  return (rows.find(row => needsAttention(row.doc.status)) ?? rows[0])?.doc.status?.state;
}
