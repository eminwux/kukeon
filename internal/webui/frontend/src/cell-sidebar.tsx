import { useMemo, useState } from "react";
import type { InventoryResource } from "./inventory";
import { listLink, workspaceLink } from "./routes";
import { DriftBadge, ScopePath, StateBadge } from "./status";
import type { TerminalState } from "./terminal";
import type { CellFilter } from "./cell-filter";
import { activeCells, cellMatches, groupCells, groupingKeys } from "./cells.mjs";

const collapsedKey = "kukeon-cell-collapsed-groups";
const pageSize = 100;
const groupPageSize = 25;

function storedCollapsed(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(collapsedKey) ?? "[]");
    return Array.isArray(value) && value.every(item => typeof item === "string") ? value : [];
  } catch { return []; }
}
function save(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* Preferences are optional. */ }
}

// StoppedCellsLink names the cells the Workbench hides and opens them in Manage → Cells (#1635).
export function StoppedCellsLink({ hidden }: { hidden: number }) {
  return hidden > 0 ? <a className="cell-stopped-link" href={listLink(3, [], "inactive")}>Stopped · {hidden}</a> : null;
}

// The Workbench sidebar lists active cells only (#1635); the open cell stays listed whatever its state.
export function CellSidebar({ inventory, selectedScope, terminalStates, filter }: {
  inventory: InventoryResource;
  selectedScope: string[];
  terminalStates: Record<string, TerminalState>;
  filter: CellFilter;
}) {
  const { search, setSearch, groupBy, setGroupBy } = filter;
  const [collapsed, setCollapsed] = useState(storedCollapsed);
  const [limits, setLimits] = useState<Record<string, number>>({});
  const [groupLimit, setGroupLimit] = useState(groupPageSize);
  const all = inventory.data?.rows[3] ?? [];
  const cells = useMemo(() => activeCells(all, selectedScope), [all, selectedScope]);
  const hidden = all.length - cells.length;
  const keys = useMemo(() => groupingKeys(cells, groupBy), [cells, groupBy]);
  const filtered = useMemo(() => cells.filter(row => cellMatches(row, search)), [cells, search]);
  const groups = useMemo(() => groupCells(filtered, groupBy), [filtered, groupBy]);
  // When every listed cell shares one realm / space / stack, show it once instead of on every row.
  const sharedScope = filtered.length && filtered.every(row => row.scope.slice(0, 3).join(" / ") === filtered[0].scope.slice(0, 3).join(" / ")) ? filtered[0].scope.slice(0, 3).join(" / ") : "";
  const incomplete = !!inventory.error || !!inventory.data?.failures.length;
  function changeGrouping(value: string) {
    setGroupBy(value); setLimits({}); setGroupLimit(groupPageSize);
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
      {!inventory.loading && !incomplete && !cells.length && <p>{hidden ? "No active cells." : "No cells found."}</p>}
      {!!cells.length && !filtered.length && <p>No matching cells.</p>}
      {!!filtered.length && <p className="cell-result-count">{filtered.length} {filtered.length === 1 ? "cell" : "cells"}{incomplete ? " in retained inventory" : ""}</p>}
    </div>
    <nav className="cell-navigation-list" aria-label="Cell navigation">
      {sharedScope && <p className="cell-group-scope" title="Scope shared by every listed cell"><ScopePath parts={filtered[0].scope.slice(0, 3)} /></p>}
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
            return <li key={JSON.stringify(row.scope)}><a className="cell-link" href={workspaceLink(row.scope)} aria-current={current ? "page" : undefined} aria-label={`Open ${fullName}`} title={fullName}><span className="cell-link-name">{row.doc.metadata.name}</span>{!sharedScope && <span className="cell-link-scope"><ScopePath parts={row.scope.slice(0, 3)} /></span>}<StateBadge state={state} /><DriftBadge status={row.doc.status} />{terminalState && <span className={`cell-terminal-state ${terminalState}`}>Terminal {terminalState}</span>}</a></li>;
          })}</ul>{rows.length > limit && <button className="cell-more" onClick={() => setLimits(old => ({ ...old, [id]: limit + pageSize }))}>Show {Math.min(pageSize, rows.length - limit)} more cells</button>}</>}
        </section>;
      })}
      {groups.length > groupLimit && <button className="cell-more" onClick={() => setGroupLimit(value => value + groupPageSize)}>Show {Math.min(groupPageSize, groups.length - groupLimit)} more groups</button>}
      <StoppedCellsLink hidden={hidden} />
    </nav>
  </section>;
}
