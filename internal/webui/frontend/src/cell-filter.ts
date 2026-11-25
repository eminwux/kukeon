import { useEffect, useState, useSyncExternalStore } from "react";

// The Workbench sidebar and the grid each have their own search and Group by (#1686): typing in one list
// never filters the other. A list's Group by choice persists under its own key; the sidebar keeps the
// historical one, so a choice stored before the split still applies to it.
export const sidebarGroupingKey = "kukeon-cell-grouping";
export const gridGroupingKey = "kukeon-grid-grouping";

// The stored choice per key: a label key, "" for an explicit None, or null when the list has no choice of
// its own and follows the Default Group by setting. A failed write keeps the value for this page only.
const choices = new Map<string, string | null>();
const listeners = new Set<() => void>();

function choice(key: string): string | null {
  if (!choices.has(key)) {
    let stored: string | null = null;
    try { stored = localStorage.getItem(key); } catch { /* Preferences are optional. */ }
    choices.set(key, stored);
  }
  return choices.get(key) ?? null;
}
function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
function setChoice(key: string, value: string) {
  choices.set(key, value);
  try { localStorage.setItem(key, value); } catch { /* Preferences are optional. */ }
  for (const listener of listeners) listener();
}

// clearGroupings drops both lists' own choices, so a new Default Group by shows in both at once.
export function clearGroupings() {
  for (const key of [sidebarGroupingKey, gridGroupingKey]) {
    choices.set(key, null);
    try { localStorage.removeItem(key); } catch { /* Preferences are optional. */ }
  }
  for (const listener of listeners) listener();
}

export type CellFilter = { search: string; setSearch: (value: string) => void; groupBy: string; setGroupBy: (value: string) => void };

// useCellFilter is one list's search and Group by. groupBy is the list's stored choice when it has one
// (None counts), otherwise defaultGroupBy. While the list's search field is hidden (searchShown false)
// no search applies, and hiding it clears the typed query.
export function useCellFilter(storageKey: string, defaultGroupBy: string, searchShown: boolean): CellFilter {
  const [search, setSearch] = useState("");
  const stored = useSyncExternalStore(subscribe, () => choice(storageKey));
  useEffect(() => { if (!searchShown) setSearch(""); }, [searchShown]);
  return { search: searchShown ? search : "", setSearch, groupBy: stored ?? defaultGroupBy, setGroupBy: value => setChoice(storageKey, value) };
}
