// Side panel membership (#1777): which cells the docked panel holds, kept per browser. Every interactive
// majordomo cell is in the panel by default; the operator adds any other cell and removes any cell,
// majordomo cells included. The stored value is { added, removed }: the cells added, in the order added,
// and the majordomo cells taken out. Keys are cellId (JSON.stringify(scope)).
// No DOM, no storage access — unit-tested with node --test.

// The localStorage key of the membership.
export const membershipKey = "kukeon-panel-cells";

const keyList = value => Array.isArray(value) ? [...new Set(value.filter(key => typeof key === "string" && key))] : [];

// parseMembership reads the stored value; a missing or unreadable value reads as the default (nothing
// added, nothing removed), and an unreadable entry is left out.
export function parseMembership(raw) {
  if (typeof raw !== "string") return { added: [], removed: [] };
  let value;
  try { value = JSON.parse(raw); } catch { return { added: [], removed: [] }; }
  if (!value || typeof value !== "object" || Array.isArray(value)) return { added: [], removed: [] };
  return { added: keyList(value.added), removed: keyList(value.removed) };
}

// panelKeys are the panel's tabs in order: the majordomo cells (majordomo, in their own order) not
// removed, then the added cells that exist (existing) and are not majordomo cells, in the order added.
export function panelKeys(majordomo, existing, membership) {
  const removed = new Set(membership.removed);
  const majors = new Set(majordomo);
  const present = new Set(existing);
  return [...majordomo.filter(key => !removed.has(key)), ...membership.added.filter(key => present.has(key) && !majors.has(key))];
}

// addToPanel puts a cell in the panel: a majordomo cell by no longer removing it, any other cell by
// adding it last.
export function addToPanel(membership, key, isMajordomo) {
  const removed = membership.removed.filter(item => item !== key);
  if (isMajordomo) return { added: membership.added.filter(item => item !== key), removed };
  return { added: membership.added.includes(key) ? membership.added : [...membership.added, key], removed };
}

// removeFromPanel takes a cell out of the panel; a removed majordomo cell stays out until it is added back.
export function removeFromPanel(membership, key, isMajordomo) {
  const added = membership.added.filter(item => item !== key);
  if (!isMajordomo) return { added, removed: membership.removed.filter(item => item !== key) };
  return { added, removed: membership.removed.includes(key) ? membership.removed : [...membership.removed, key] };
}

// pruneMembership keeps the entries of the cells in cellKeys (a complete inventory).
export function pruneMembership(membership, cellKeys) {
  const keep = new Set(cellKeys);
  return { added: membership.added.filter(key => keep.has(key)), removed: membership.removed.filter(key => keep.has(key)) };
}

// sameMembership compares two memberships entry by entry, in order.
export function sameMembership(a, b) {
  return a.added.length === b.added.length && a.removed.length === b.removed.length &&
    a.added.every((key, index) => key === b.added[index]) && a.removed.every((key, index) => key === b.removed[index]);
}
