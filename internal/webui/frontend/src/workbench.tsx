import { useEffect, useMemo, useRef, useState } from "react";
import type { InventoryResource, Row } from "./inventory";
import type { CellFilter } from "./cell-filter";
import { activeCells, agentGroups, attentionFirst, cellMatches, groupCells, groupingKeys, instanceLabels, rollupState, screenTail } from "./cells.mjs";
import type { CellListStyle } from "./settings";
import { Icon } from "./icons";
import { newAgentLink, newCellLink, workspaceLink } from "./routes";
import { DriftBadge, RollupDot, ScopePath, StateBadge } from "./status";
import { unavailableStates, type TerminalState } from "./terminal";
import { useCellScreens, type Screen } from "./screens";
import { StoppedCellsLink } from "./cell-sidebar";

const key = (scope: string[]) => JSON.stringify(scope);

function CellTile({ row, label, screen, docked, terminalState, onOpenDock }: { row: Row; label?: string; screen?: Screen; docked: boolean; terminalState?: TerminalState; onOpenDock: () => void }) {
  const state = row.doc.status?.state;
  const fullName = row.scope.join(" / ");
  // A cell with no live terminal never gets a screenshot request; it and an unavailable screen read as state only.
  const text = !unavailableStates.has(state ?? "") && !screen?.unavailable ? screenTail(screen?.text ?? "") : "";
  const body = <>
    <span className="cell-tile-head"><strong className="cell-tile-name">{label ?? row.doc.metadata.name}</strong>{label && <span className="cell-tile-cell mono-id">{row.doc.metadata.name}</span>}<StateBadge state={state} /><DriftBadge status={row.doc.status} /></span>
    <span className="cell-tile-scope mono-id"><ScopePath parts={row.scope.slice(0, 3)} /></span>
    {/* Terminal text is rendered as a text node, never as HTML. */}
    {text ? <pre className="cell-tile-screen" aria-hidden="true">{text}</pre> : <span className="cell-tile-screen cell-tile-empty">{screen || unavailableStates.has(state ?? "") ? "No terminal screen" : "Reading terminal screen…"}</span>}
    {(screen?.stale || terminalState) && <span className="cell-tile-foot">{screen?.stale && <span className="badge cell-tile-stale" title="The last screen refresh failed">Stale</span>}{terminalState && <span className={`cell-terminal-state ${terminalState}`}>Terminal {terminalState}</span>}</span>}
  </>;
  // A cell docked in the majordomo panel opens the panel instead of the cell screen.
  return docked
    ? <button type="button" className="cell-tile panel" data-scope={key(row.scope)} aria-label={`Open ${fullName} in the majordomo panel`} title={fullName} onClick={onOpenDock}>{body}</button>
    : <a className="cell-tile panel" data-scope={key(row.scope)} href={workspaceLink(row.scope)} aria-label={`Open ${label ? `${label} (${fullName})` : fullName}`} title={fullName}>{body}</a>;
}

// The grid's own search field and Group by select (#1686), separate from the sidebar's. Each is rendered
// only while its Profile switch is on, and the row only when it has a control. Their names differ from the
// sidebar's "Search cells" and "Group by", so the two pairs can be told apart.
function GridControls({ filter, keys, showSearch, showGroupBy }: { filter: CellFilter; keys: string[]; showSearch: boolean; showGroupBy: boolean }) {
  if (!showSearch && !showGroupBy) return null;
  return <div className="cell-navigation-controls grid-controls">
    {showSearch && <label>Search grid<input type="search" value={filter.search} placeholder="Name or scope" onChange={event => filter.setSearch(event.target.value)} /></label>}
    {showGroupBy && <label>Group grid by<select value={filter.groupBy} onChange={event => filter.setGroupBy(event.target.value)}><option value="">None</option>{keys.map(key => <option key={key} value={key}>{key}</option>)}{filter.groupBy && !keys.includes(filter.groupBy) && <option value={filter.groupBy}>{filter.groupBy} (not in current inventory)</option>}</select></label>}
  </div>;
}

// The Workbench home (#1635): one tile per active cell with a text thumbnail of its terminal screen,
// filtered and grouped by the grid's own search and Group by (#1686), attention first inside each group.
// In agent style the sections are the agents (Configs) with their instance tiles, Other cells last, and
// no Group by is offered.
export function WorkbenchGrid({ inventory, filter, dockedCells, onOpenDock, terminalStates, listStyle = "default", showSearch = true, showGroupBy = true }: {
  inventory: InventoryResource;
  filter: CellFilter;
  dockedCells: string[];
  onOpenDock: () => void;
  terminalStates: Record<string, TerminalState>;
  listStyle?: CellListStyle;
  showSearch?: boolean;
  showGroupBy?: boolean;
}) {
  const agentStyle = listStyle === "agent";
  const all = inventory.data?.rows[3] ?? [];
  const cells = useMemo(() => activeCells(all, []), [all]);
  const hidden = all.length - cells.length;
  const keys = useMemo(() => groupingKeys(cells, filter.groupBy), [cells, filter.groupBy]);
  const filtered = useMemo(() => cells.filter(row => cellMatches(row, filter.search)), [cells, filter.search]);
  const configs = inventory.data?.configs;
  // Instance labels are counted over every cell, so a search or a stopped cell never renumbers them.
  const labels = useMemo(() => agentStyle ? instanceLabels(all, configs) : new Map<string, string>(), [agentStyle, all, configs]);
  const sections = useMemo(() => {
    if (!agentStyle) return groupCells(filtered, filter.groupBy).map(group => ({ id: group.id, name: `${group.label} group`, heading: filter.groupBy ? <>{group.label}</> : null, state: undefined as string | undefined, rollup: false, rows: attentionFirst(group.rows) }));
    const { agents, others } = agentGroups(filtered, configs);
    return [
      ...agents.map(agent => ({ id: agent.id, name: `${agent.displayName} agent`, heading: <><Icon name="agent" /><span>{agent.displayName}</span>{agent.named && <span className="agent-internal mono-id">{agent.name}</span>}{agent.context && <span className="agent-context">{agent.context}</span>}</>, state: rollupState(agent.rows), rollup: true, rows: attentionFirst(agent.rows) })),
      { id: "others", name: "Other cells", heading: <>Other cells</>, state: undefined, rollup: false, rows: attentionFirst(others) },
    ];
  }, [agentStyle, filtered, filter.groupBy, configs]);
  const incomplete = !!inventory.error || !!inventory.data?.failures.length;

  // Which tiles are on screen: only those are polled.
  const grid = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState<string[]>([]);
  const tileKeys = filtered.map(row => key(row.scope)).join("\n");
  useEffect(() => {
    const host = grid.current;
    if (!host) { setVisible([]); return; }
    const shown = new Set<string>();
    const observer = new IntersectionObserver(entries => {
      for (const entry of entries) {
        const id = (entry.target as HTMLElement).dataset.scope;
        if (!id) continue;
        if (entry.isIntersecting) shown.add(id); else shown.delete(id);
      }
      setVisible([...shown].sort());
    });
    host.querySelectorAll<HTMLElement>(".cell-tile[data-scope]").forEach(tile => observer.observe(tile));
    return () => observer.disconnect();
  }, [tileKeys, filter.groupBy, listStyle]);
  const polled = useMemo(() => filtered.filter(row => visible.includes(key(row.scope)) && !unavailableStates.has(row.doc.status?.state ?? "")).map(row => row.scope), [filtered, visible]);
  // Mounted only on the workbench view, so polling stops on every other page.
  const screens = useCellScreens(polled, true);

  return <section className="workbench" aria-label="Cell terminals">
    {!!cells.length && <GridControls filter={filter} keys={keys} showSearch={showSearch} showGroupBy={showGroupBy && !agentStyle} />}
    <div className="workbench-status" aria-live="polite">
      {inventory.loading && !inventory.data && <p role="status">Loading cells…</p>}
      {incomplete && <p className="cell-inventory-warning">{inventory.data ? "Inventory incomplete or stale. Refresh to retry." : "Cells unavailable. Refresh to retry."}</p>}
      {!!cells.length && !filtered.length && <p>No matching cells.</p>}
    </div>
    {inventory.data && !cells.length && <div className="empty-state"><h3>No active cells</h3><p>{hidden ? "Every cell is stopped or exited." : "Create a cell to see its terminal here."}</p><div className="empty-state-actions"><a className="header-action" href={newAgentLink()}>New agent</a><a className="header-action" href={newCellLink()}>New Cell</a></div></div>}
    <div ref={grid} className="cell-grid-groups">
      {sections.filter(section => section.rows.length).map(section => <section className="cell-grid-group" key={section.id} aria-label={section.heading ? section.name : undefined}>
        {section.heading && <h2 className="cell-grid-heading">{section.heading} <small>{section.rows.length}{section.rollup && <span className="sr-only"> {section.rows.length === 1 ? "instance" : "instances"}</span>}</small>{section.rollup && <RollupDot state={section.state} />}</h2>}
        <ul className="cell-grid">{section.rows.map(row => <li key={key(row.scope)}><CellTile row={row} label={labels.get(key(row.scope))} screen={screens[key(row.scope)]} docked={dockedCells.includes(key(row.scope))} terminalState={terminalStates[key(row.scope)]} onOpenDock={onOpenDock} /></li>)}</ul>
      </section>)}
    </div>
    <StoppedCellsLink hidden={hidden} />
  </section>;
}
