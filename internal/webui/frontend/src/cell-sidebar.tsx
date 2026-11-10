import { useMemo, useState } from "react";
import type { InventoryResource, Row } from "./inventory";
import { workspaceLink } from "./routes";
import { StateBadge } from "./status";
import type { TerminalState } from "./terminal";

const groupingKey = "kukeon-cell-grouping";
const collapsedKey = "kukeon-cell-collapsed-groups";
const pageSize = 100;
const groupPageSize = 25;
const ungrouped = "Ungrouped";

function storedGrouping(): string {
  try { return localStorage.getItem(groupingKey) ?? ""; } catch { return ""; }
}
function storedCollapsed(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(collapsedKey) ?? "[]");
    return Array.isArray(value) && value.every(item => typeof item === "string") ? value : [];
  } catch { return []; }
}
function save(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* Preferences are optional. */ }
}
function groupId(key: string, value: string | null) { return JSON.stringify([key, value]); }

export function CellSidebar({ inventory, selectedScope, terminalStates }: {
  inventory: InventoryResource;
  selectedScope: string[];
  terminalStates: Record<string, TerminalState>;
}) {
  const [search, setSearch] = useState("");
  const [groupBy, setGroupBy] = useState(storedGrouping);
  const [collapsed, setCollapsed] = useState(storedCollapsed);
  const [limits, setLimits] = useState<Record<string, number>>({});
  const [groupLimit, setGroupLimit] = useState(groupPageSize);
  const cells = inventory.data?.rows[3] ?? [];
  const keys = useMemo(() => [...new Set(cells.flatMap(row => Object.keys(row.doc.metadata.labels ?? {})))].sort(), [cells]);
  const query = search.trim().toLocaleLowerCase();
  const filtered = useMemo(() => cells.filter(row => !query || row.scope.join(" / ").toLocaleLowerCase().includes(query)), [cells, query]);
  const groups = useMemo(() => {
    if (!groupBy) return [{ id: groupId("", "All cells"), label: "All cells", rows: filtered }];
    const found = new Map<string | null, Row[]>();
    for (const row of filtered) {
      const value = row.doc.metadata.labels?.[groupBy]?.trim() || null;
      const rows = found.get(value) ?? [];
      rows.push(row);
      found.set(value, rows);
    }
    return [...found].sort(([a], [b]) => a === null ? 1 : b === null ? -1 : a.localeCompare(b)).map(([value, rows]) => ({ id: groupId(groupBy, value), label: value === null ? ungrouped : value === ungrouped ? `${ungrouped} (label value)` : value, rows }));
  }, [filtered, groupBy]);
  const incomplete = !!inventory.error || !!inventory.data?.failures.length;
  function changeGrouping(value: string) {
    setGroupBy(value); setLimits({}); setGroupLimit(groupPageSize); save(groupingKey, value);
  }
  function toggleGroup(id: string) {
    const next = collapsed.includes(id) ? collapsed.filter(item => item !== id) : [...collapsed, id];
    setCollapsed(next); save(collapsedKey, JSON.stringify(next));
  }
  return <section className="cell-navigation" aria-label="All cells">
    <div className="cell-navigation-heading"><h2>Cells</h2><span>{cells.length}</span></div>
    <div className="cell-navigation-controls">
      <label>Search cells<input type="search" value={search} placeholder="Name or scope" onChange={event => { setSearch(event.target.value); setLimits({}); setGroupLimit(groupPageSize); }} /></label>
      <label>Group by<select value={groupBy} onChange={event => changeGrouping(event.target.value)}><option value="">None</option>{keys.map(key => <option key={key} value={key}>{key}</option>)}{groupBy && !keys.includes(groupBy) && <option value={groupBy}>{groupBy} (not in current inventory)</option>}</select></label>
    </div>
    <div className="cell-navigation-results" aria-live="polite">
      {inventory.loading && !inventory.data && <p role="status">Loading cells…</p>}
      {incomplete && <p className="cell-inventory-warning">{inventory.data ? "Inventory incomplete or stale. Refresh to retry." : "Cells unavailable. Refresh to retry."}</p>}
      {!inventory.loading && !incomplete && !cells.length && <p>No cells found.</p>}
      {!!cells.length && !filtered.length && <p>No matching cells.</p>}
      {!!filtered.length && <p className="cell-result-count">{filtered.length} {filtered.length === 1 ? "cell" : "cells"}{incomplete ? " in retained inventory" : ""}</p>}
    </div>
    <nav className="cell-navigation-list" aria-label="Cell navigation">
      {groups.slice(0, groupLimit).map(({ id, label, rows }) => {
        const isCollapsed = !!groupBy && collapsed.includes(id);
        const limit = limits[id] ?? pageSize;
        return <section className="cell-group" key={id} aria-label={groupBy ? `${label} group` : undefined}>
          {groupBy && <button className="cell-group-toggle" aria-expanded={!isCollapsed} onClick={() => toggleGroup(id)}><span aria-hidden="true">{isCollapsed ? "▸" : "▾"}</span><span>{label}</span><small>{rows.length}</small></button>}
          {!isCollapsed && <><ul>{rows.slice(0, limit).map(row => {
            const current = selectedScope.length === 4 && row.scope.every((part, i) => part === selectedScope[i]);
            const fullName = row.scope.join(" / ");
            const terminalState = terminalStates[JSON.stringify(row.scope)];
            const state = row.doc.status?.state;
            return <li key={JSON.stringify(row.scope)}><a className="cell-link" href={workspaceLink(row.scope)} aria-current={current ? "page" : undefined} aria-label={`Open ${fullName}`} title={fullName}><span className="cell-link-name">{row.doc.metadata.name}</span><span className="cell-link-scope">{row.scope.slice(0, 3).join(" / ")}</span><StateBadge state={state} />{terminalState && <span className={`cell-terminal-state ${terminalState}`}>Terminal {terminalState}</span>}</a></li>;
          })}</ul>{rows.length > limit && <button className="cell-more" onClick={() => setLimits(old => ({ ...old, [id]: limit + pageSize }))}>Show {Math.min(pageSize, rows.length - limit)} more cells</button>}</>}
        </section>;
      })}
      {groups.length > groupLimit && <button className="cell-more" onClick={() => setGroupLimit(value => value + groupPageSize)}>Show {Math.min(groupPageSize, groups.length - groupLimit)} more groups</button>}
    </nav>
  </section>;
}
