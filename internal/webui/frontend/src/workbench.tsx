import { useEffect, useMemo, useRef, useState } from "react";
import type { InventoryResource, Row } from "./inventory";
import type { CellFilter } from "./cell-filter";
import { activeCells, attentionFirst, cellMatches, groupCells, screenTail } from "./cells.mjs";
import { newCellLink, workspaceLink } from "./routes";
import { DriftBadge, ScopePath, StateBadge } from "./status";
import { unavailableStates, type TerminalState } from "./terminal";
import { useCellScreens, type Screen } from "./screens";
import { StoppedCellsLink } from "./cell-sidebar";

const key = (scope: string[]) => JSON.stringify(scope);

function CellTile({ row, screen, docked, terminalState, onOpenDock }: { row: Row; screen?: Screen; docked: boolean; terminalState?: TerminalState; onOpenDock: () => void }) {
  const state = row.doc.status?.state;
  const fullName = row.scope.join(" / ");
  // A cell with no live terminal never gets a screenshot request; it and an unavailable screen read as state only.
  const text = !unavailableStates.has(state ?? "") && !screen?.unavailable ? screenTail(screen?.text ?? "") : "";
  const body = <>
    <span className="cell-tile-head"><strong className="cell-tile-name">{row.doc.metadata.name}</strong><StateBadge state={state} /><DriftBadge status={row.doc.status} /></span>
    <span className="cell-tile-scope mono-id"><ScopePath parts={row.scope.slice(0, 3)} /></span>
    {/* Terminal text is rendered as a text node, never as HTML. */}
    {text ? <pre className="cell-tile-screen" aria-hidden="true">{text}</pre> : <span className="cell-tile-screen cell-tile-empty">{screen || unavailableStates.has(state ?? "") ? "No terminal screen" : "Reading terminal screen…"}</span>}
    {(screen?.stale || terminalState) && <span className="cell-tile-foot">{screen?.stale && <span className="badge cell-tile-stale" title="The last screen refresh failed">Stale</span>}{terminalState && <span className={`cell-terminal-state ${terminalState}`}>Terminal {terminalState}</span>}</span>}
  </>;
  // A cell docked in the majordomo panel opens the panel instead of the cell screen.
  return docked
    ? <button type="button" className="cell-tile panel" data-scope={key(row.scope)} aria-label={`Open ${fullName} in the majordomo panel`} onClick={onOpenDock}>{body}</button>
    : <a className="cell-tile panel" data-scope={key(row.scope)} href={workspaceLink(row.scope)} aria-label={`Open ${fullName}`}>{body}</a>;
}

// The Workbench home (#1635): one tile per active cell with a text thumbnail of its terminal screen,
// filtered and grouped by the sidebar's shared search and group-by, attention first inside each group.
export function WorkbenchGrid({ inventory, filter, dockedCells, onOpenDock, terminalStates }: {
  inventory: InventoryResource;
  filter: CellFilter;
  dockedCells: string[];
  onOpenDock: () => void;
  terminalStates: Record<string, TerminalState>;
}) {
  const all = inventory.data?.rows[3] ?? [];
  const cells = useMemo(() => activeCells(all, []), [all]);
  const hidden = all.length - cells.length;
  const filtered = useMemo(() => cells.filter(row => cellMatches(row, filter.search)), [cells, filter.search]);
  const groups = useMemo(() => groupCells(filtered, filter.groupBy).map(group => ({ ...group, rows: attentionFirst(group.rows) })), [filtered, filter.groupBy]);
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
  }, [tileKeys, filter.groupBy]);
  const polled = useMemo(() => filtered.filter(row => visible.includes(key(row.scope)) && !unavailableStates.has(row.doc.status?.state ?? "")).map(row => row.scope), [filtered, visible]);
  // Mounted only on the workbench view, so polling stops on every other page.
  const screens = useCellScreens(polled, true);

  return <section className="workbench" aria-label="Cell terminals">
    <div className="workbench-status" aria-live="polite">
      {inventory.loading && !inventory.data && <p role="status">Loading cells…</p>}
      {incomplete && <p className="cell-inventory-warning">{inventory.data ? "Inventory incomplete or stale. Refresh to retry." : "Cells unavailable. Refresh to retry."}</p>}
      {!!cells.length && !filtered.length && <p>No matching cells.</p>}
    </div>
    {inventory.data && !cells.length && <div className="empty-state"><h3>No active cells</h3><p>{hidden ? "Every cell is stopped or exited." : "Create a cell to see its terminal here."}</p><a className="header-action" href={newCellLink()}>New Cell</a></div>}
    <div ref={grid} className="cell-grid-groups">
      {groups.filter(group => group.rows.length).map(group => <section className="cell-grid-group" key={group.id} aria-label={filter.groupBy ? `${group.label} group` : undefined}>
        {filter.groupBy && <h2 className="cell-grid-heading">{group.label} <small>{group.rows.length}</small></h2>}
        <ul className="cell-grid">{group.rows.map(row => <li key={key(row.scope)}><CellTile row={row} screen={screens[key(row.scope)]} docked={dockedCells.includes(key(row.scope))} terminalState={terminalStates[key(row.scope)]} onOpenDock={onOpenDock} /></li>)}</ul>
      </section>)}
    </div>
    <StoppedCellsLink hidden={hidden} />
  </section>;
}
