import { useMemo, useState } from "react";
import type { InventoryResource, Row } from "./inventory";
import { listLink, workspaceLink } from "./routes";
import { DriftBadge, RollupDot, ScopePath, StateBadge } from "@kukeon/ui";
import type { TerminalState } from "./terminal";
import type { CellFilter } from "./cell-filter";
import type { CellListStyle } from "./settings";
import { Icon } from "@kukeon/ui";
import { activeCells, agentGroups, cellMatches, filterAgents, groupCells, groupingKeys, instanceLabels, rollupState } from "./cells.mjs";
import { unreadAmount, unreadLabel } from "./unread.mjs";
import type { Unread } from "./use-unread";

const collapsedKey = "kukeon-cell-collapsed-groups";
const pageSize = 100;
const groupPageSize = 25;
// The paging id of the Other cells list, beside the group and agent ids.
const othersId = "others";

function storedCollapsed(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(collapsedKey) ?? "[]");
    return Array.isArray(value) && value.every(item => typeof item === "string") ? value : [];
  } catch { return []; }
}
function save(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* Preferences are optional. */ }
}
const sameScope = (a: string[], b: string[]) => b.length === 4 && a.every((part, i) => part === b[i]);

// UnreadMark is the unread-output mark (#1696): an accent dot and, with Show unread bytes on, the amount.
// It is decoration: the element that carries it puts the same in its accessible name (unreadLabel).
export function UnreadMark({ bytes, showBytes }: { bytes: number; showBytes: boolean }) {
  return <><span className="unread-dot" aria-hidden="true" />{showBytes && <span className="cell-unread-count" aria-hidden="true">{unreadAmount(bytes)}</span>}</>;
}

// unreadOf rolls the unread state of rows up: marked when any row is, the bytes summed over the marked rows.
export function unreadOf(rows: Row[], unread: Record<string, Unread>): number {
  return rows.reduce((sum, row) => sum + (unread[JSON.stringify(row.scope)]?.unreadBytes ?? 0), 0);
}

// StoppedCellsLink names the cells the Workbench hides and opens them in Manage → Cells (#1635).
export function StoppedCellsLink({ hidden }: { hidden: number }) {
  return hidden > 0 ? <a className="cell-stopped-link" href={listLink(3, [], "inactive")}>Stopped · {hidden}</a> : null;
}

type RowOptions = { selectedScope: string[]; terminalStates: Record<string, TerminalState>; showScope: boolean; showTerminalStatus: boolean; labels: Map<string, string>; unread: Record<string, Unread>; showUnreadBytes: boolean };

// One cell of the list: a group's row in default style, an agent's instance in agent style.
// An instance with a label (#1692) shows it, with the cell name as secondary text.
// A cell with unread terminal output (#1696) has has-unread, the mark after its name and the label suffix.
function CellRow({ row, selectedScope, terminalStates, showScope, showTerminalStatus, labels, unread, showUnreadBytes }: { row: Row } & RowOptions) {
  const fullName = row.scope.join(" / ");
  const terminalState = terminalStates[JSON.stringify(row.scope)];
  const label = labels.get(JSON.stringify(row.scope));
  const unreadBytes = unread[JSON.stringify(row.scope)]?.unreadBytes ?? 0;
  return <li><a className={`cell-link${unreadBytes ? " has-unread" : ""}`} href={workspaceLink(row.scope)} aria-current={sameScope(row.scope, selectedScope) ? "page" : undefined} aria-label={`Open ${label ? `${label} (${fullName})` : fullName}${unreadLabel(unreadBytes, showUnreadBytes)}`} title={fullName}><span className="cell-link-name">{label ?? row.doc.metadata.name}{unreadBytes > 0 && <UnreadMark bytes={unreadBytes} showBytes={showUnreadBytes} />}</span>{label && <span className="cell-link-cell mono-id">{row.doc.metadata.name}</span>}{showScope && <span className="cell-link-scope"><ScopePath parts={row.scope.slice(0, 3)} /></span>}<StateBadge state={row.doc.status?.state} /><DriftBadge status={row.doc.status} />{showTerminalStatus && terminalState && <span className={`cell-terminal-state ${terminalState}`}>Terminal {terminalState}</span>}</a></li>;
}

// The Workbench sidebar lists active cells only (#1635); the open cell stays listed whatever its state.
// listStyle, showSearch, showGroupBy, showScope and showTerminalStatus are the Profile settings (#1652,
// #1686). In agent style the cells are listed as agents (Configs) and their instances, and the hierarchy
// is the grouping, so no Group by is offered. A link's title, accessible name and search keep the full
// scope whatever is shown. unread marks the cells with unread terminal output (#1696); an agent row is
// marked when any of its instances is, collapsed or not.
export function CellSidebar({ inventory, selectedScope, terminalStates, filter, listStyle = "default", showSearch = true, showGroupBy = true, showScope = true, showTerminalStatus = true, unread = {}, showUnreadBytes = false }: {
  inventory: InventoryResource;
  selectedScope: string[];
  terminalStates: Record<string, TerminalState>;
  filter: CellFilter;
  listStyle?: CellListStyle;
  showSearch?: boolean;
  showGroupBy?: boolean;
  showScope?: boolean;
  showTerminalStatus?: boolean;
  unread?: Record<string, Unread>;
  showUnreadBytes?: boolean;
}) {
  const { search, setSearch, groupBy, setGroupBy } = filter;
  const agentStyle = listStyle === "agent";
  const [collapsed, setCollapsed] = useState(storedCollapsed);
  const [limits, setLimits] = useState<Record<string, number>>({});
  const [groupLimit, setGroupLimit] = useState(groupPageSize);
  const all = inventory.data?.rows[3] ?? [];
  const cells = useMemo(() => activeCells(all, selectedScope), [all, selectedScope]);
  const hidden = all.length - cells.length;
  const keys = useMemo(() => groupingKeys(cells, groupBy), [cells, groupBy]);
  const filtered = useMemo(() => cells.filter(row => cellMatches(row, search)), [cells, search]);
  const groups = useMemo(() => groupCells(filtered, groupBy), [filtered, groupBy]);
  const configs = inventory.data?.configs;
  // Agents are grouped over every cell and searched after, so an agent keeps its id while the user types (#1688).
  const allAgents = useMemo(() => agentGroups(cells, configs), [cells, configs]);
  const { agents, others } = useMemo(() => filterAgents(allAgents, row => cellMatches(row, search)), [allAgents, search]);
  // An agent is a single link or a toggle by its listed instances, not by how many match the search.
  const instanceCounts = useMemo(() => new Map(allAgents.agents.map(agent => [agent.id, agent.rows.length])), [allAgents]);
  // Instance labels are counted over every cell, so a search or a stopped cell never renumbers them.
  const labels = useMemo(() => agentStyle ? instanceLabels(all, configs) : new Map<string, string>(), [agentStyle, all, configs]);
  // When every listed cell shares one realm / space / stack, show it once instead of on every row.
  const sharedScope = filtered.length && filtered.every(row => row.scope.slice(0, 3).join(" / ") === filtered[0].scope.slice(0, 3).join(" / ")) ? filtered[0].scope.slice(0, 3).join(" / ") : "";
  const incomplete = !!inventory.error || !!inventory.data?.failures.length;
  const rowOptions: RowOptions = { selectedScope, terminalStates, showScope: showScope && !sharedScope, showTerminalStatus, labels, unread, showUnreadBytes };
  const groupByShown = showGroupBy && !agentStyle;
  function resetPaging() { setLimits({}); setGroupLimit(groupPageSize); }
  function toggleGroup(id: string) {
    const next = collapsed.includes(id) ? collapsed.filter(item => item !== id) : [...collapsed, id];
    setCollapsed(next); save(collapsedKey, JSON.stringify(next));
  }
  // The rows of one group, agent or Other cells list, 100 at a time.
  function rowList(id: string, rows: Row[], className?: string) {
    const limit = limits[id] ?? pageSize;
    return <><ul className={className}>{rows.slice(0, limit).map(row => <CellRow key={JSON.stringify(row.scope)} row={row} {...rowOptions} />)}</ul>{rows.length > limit && <button className="cell-more" onClick={() => setLimits(old => ({ ...old, [id]: limit + pageSize }))}>Show {Math.min(pageSize, rows.length - limit)} more cells</button>}</>;
  }
  // Agents count as groups for the 25-group paging; Other cells is the last one.
  const agentSections = [...agents.map(agent => ({ id: agent.id, agent })), ...(others.length ? [{ id: othersId, agent: undefined }] : [])];
  const shownSections = agentSections.slice(0, groupLimit);
  const sectionCount = agentStyle ? agentSections.length : groups.length;
  return <section className="cell-navigation" aria-label="All cells">
    <div className="cell-navigation-heading"><h2>Cells</h2><span>{cells.length}</span></div>
    {(showSearch || groupByShown) && <div className="cell-navigation-controls">
      {showSearch && <label>Search cells<input type="search" value={search} placeholder="Name or scope" onChange={event => { setSearch(event.target.value); resetPaging(); }} /></label>}
      {groupByShown && <label>Group by<select value={groupBy} onChange={event => { setGroupBy(event.target.value); resetPaging(); }}><option value="">None</option>{keys.map(key => <option key={key} value={key}>{key}</option>)}{groupBy && !keys.includes(groupBy) && <option value={groupBy}>{groupBy} (not in current inventory)</option>}</select></label>}
    </div>}
    <div className="cell-navigation-results" aria-live="polite">
      {inventory.loading && !inventory.data && <p role="status">Loading cells…</p>}
      {incomplete && <p className="cell-inventory-warning">{inventory.data ? "Inventory incomplete or stale. Refresh to retry." : "Cells unavailable. Refresh to retry."}</p>}
      {!inventory.loading && !incomplete && !cells.length && <p>{hidden ? "No active cells." : "No cells found."}</p>}
      {!!cells.length && !filtered.length && <p>No matching cells.</p>}
      {!!filtered.length && <p className="cell-result-count">{filtered.length} {filtered.length === 1 ? "cell" : "cells"}{incomplete ? " in retained inventory" : ""}</p>}
    </div>
    <nav className="cell-navigation-list" aria-label="Cell navigation">
      {showScope && sharedScope && <p className="cell-group-scope" title="Scope shared by every listed cell"><ScopePath parts={filtered[0].scope.slice(0, 3)} /></p>}
      {agentStyle ? <>
        {agents.length > 0 && <h3 className="cell-list-heading">Agents</h3>}
        {shownSections.map(({ id, agent }) => {
          if (!agent) return <section className="cell-group" key={id} aria-label="Other cells"><h3 className="cell-list-heading">Other cells</h3>{rowList(id, others)}</section>;
          const agentUnread = unreadOf(agent.rows, unread);
          const unreadSuffix = unreadLabel(agentUnread, showUnreadBytes);
          const unreadClass = agentUnread ? " has-unread" : "";
          const name = <span className="agent-name"><Icon name="agent" /><span>{agent.displayName}{agentUnread > 0 && <UnreadMark bytes={agentUnread} showBytes={showUnreadBytes} />}</span>{agent.named && <span className="agent-internal mono-id">{agent.name}</span>}{agent.context && <span className="agent-context">{agent.context}</span>}</span>;
          const count = <small>{agent.rows.length}<span className="sr-only"> {agent.rows.length === 1 ? "instance" : "instances"}</span></small>;
          const state = rollupState(agent.rows);
          if (instanceCounts.get(id) === 1) {
            // One instance: the agent row is the link to that cell.
            const row = agent.rows[0];
            const fullName = row.scope.join(" / ");
            const terminalState = terminalStates[JSON.stringify(row.scope)];
            return <section className="cell-group cell-agent" key={id} aria-label={`${agent.displayName} agent`}>
              <a className={`agent-row agent-link${unreadClass}`} href={workspaceLink(row.scope)} aria-current={sameScope(row.scope, selectedScope) ? "page" : undefined} aria-label={`${agent.displayName}: open ${fullName} (${state || "state not reported"})${unreadSuffix}`} title={fullName}>{name}{count}<RollupDot state={state} /><DriftBadge status={row.doc.status} />{showTerminalStatus && terminalState && <span className={`cell-terminal-state ${terminalState}`}>Terminal {terminalState}</span>}</a>
            </section>;
          }
          const isCollapsed = collapsed.includes(id);
          const holdsCurrent = isCollapsed && agent.rows.some(row => sameScope(row.scope, selectedScope));
          return <section className="cell-group cell-agent" key={id} aria-label={`${agent.displayName} agent`}>
            <button className={`cell-group-toggle agent-row${holdsCurrent ? " has-current" : ""}${unreadClass}`} aria-expanded={!isCollapsed} onClick={() => toggleGroup(id)}><span aria-hidden="true">{isCollapsed ? "▸" : "▾"}</span>{name}{holdsCurrent && <span className="sr-only">, contains the open cell</span>}{count}<RollupDot state={state} />{unreadSuffix && <span className="sr-only">{unreadSuffix}</span>}</button>
            {!isCollapsed && rowList(id, agent.rows, "agent-instances")}
          </section>;
        })}
      </> : groups.slice(0, groupLimit).map(({ id, label, rows }) => {
        const isCollapsed = !!groupBy && collapsed.includes(id);
        return <section className="cell-group" key={id} aria-label={groupBy ? `${label} group` : undefined}>
          {groupBy && <button className="cell-group-toggle" aria-expanded={!isCollapsed} onClick={() => toggleGroup(id)}><span aria-hidden="true">{isCollapsed ? "▸" : "▾"}</span><span>{label}</span><small>{rows.length}</small></button>}
          {!isCollapsed && rowList(id, rows)}
        </section>;
      })}
      {sectionCount > groupLimit && <button className="cell-more" onClick={() => setGroupLimit(value => value + groupPageSize)}>Show {Math.min(groupPageSize, sectionCount - groupLimit)} more groups</button>}
    </nav>
    <StoppedCellsLink hidden={hidden} />
  </section>;
}
