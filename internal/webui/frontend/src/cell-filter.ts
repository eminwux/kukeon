import { useState } from "react";

const groupingKey = "kukeon-cell-grouping";

function storedGrouping(): string {
  try { return localStorage.getItem(groupingKey) ?? ""; } catch { return ""; }
}

export type CellFilter = { search: string; setSearch: (value: string) => void; groupBy: string; setGroupBy: (value: string) => void };

// The Workbench sidebar and the grid share one search and one group-by (#1635). The state lives in App so
// both read it; the group-by preference persists under its historical key.
export function useCellFilter(): CellFilter {
  const [search, setSearch] = useState("");
  const [groupBy, setGrouping] = useState(storedGrouping);
  function setGroupBy(value: string) {
    setGrouping(value);
    try { localStorage.setItem(groupingKey, value); } catch { /* Preferences are optional. */ }
  }
  return { search, setSearch, groupBy, setGroupBy };
}
