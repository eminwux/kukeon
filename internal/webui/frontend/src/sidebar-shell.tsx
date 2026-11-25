import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import type { InventoryResource } from "./inventory";
import { homeLink, workspaceLink } from "./routes";
import { stateTone } from "./status";
import { Icon } from "./icons";
import type { View } from "./views.mjs";
import { activeCells, agentGroups, rollupState } from "./cells.mjs";
import type { Row } from "./inventory";
import type { CellListStyle } from "./settings";

const collapsedKey = "kukeon-sidebar-collapsed";
const widthKey = "kukeon-sidebar-width";
export const sidebarWidth = { min: 208, max: 480, initial: 264, step: 16 };
// The collapsed icon rail, the --rail-width token in style.css.
export const railWidth = 56;

function clampWidth(value: number): number {
  return Math.round(Math.min(sidebarWidth.max, Math.max(sidebarWidth.min, value)));
}
function storedCollapsed(): boolean {
  try { return localStorage.getItem(collapsedKey) === "true"; } catch { return false; }
}
function storedWidth(): number {
  try {
    const value = Number(localStorage.getItem(widthKey));
    return Number.isFinite(value) && value > 0 ? clampWidth(value) : sidebarWidth.initial;
  } catch { return sidebarWidth.initial; }
}
function save(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* Preferences are optional. */ }
}

// Sidebar collapse and width preferences, persisted per browser.
export function useSidebarLayout() {
  const [collapsed, setCollapsed] = useState(storedCollapsed);
  const [width, setWidth] = useState(storedWidth);
  useEffect(() => { save(collapsedKey, String(collapsed)); }, [collapsed]);
  useEffect(() => { save(widthKey, String(width)); }, [width]);
  return { collapsed, setCollapsed, width, setWidth: (value: number) => setWidth(clampWidth(value)) };
}

export function BrandMark() {
  return <span className="mark" aria-hidden="true"><Icon name="tornado" /></span>;
}

export function SidebarToggle({ collapsed, onToggle }: { collapsed: boolean; onToggle: () => void }) {
  const label = collapsed ? "Expand sidebar" : "Collapse sidebar";
  return <button type="button" className="icon-button sidebar-toggle" aria-label={label} title={label} aria-expanded={!collapsed} aria-controls="cell-sidebar" onClick={onToggle}>
    <Icon name={collapsed ? "sidebarExpand" : "sidebarCollapse"} />
  </button>;
}

// Pointer and keyboard resize for the sidebar's right edge.
export function SidebarResizer({ width, onResize }: { width: number; onResize: (value: number) => void }) {
  const origin = useRef<{ x: number; width: number } | null>(null);
  function down(event: PointerEvent<HTMLDivElement>) {
    if (event.button !== 0) return;
    event.preventDefault();
    origin.current = { x: event.clientX, width };
    event.currentTarget.setPointerCapture(event.pointerId);
  }
  function move(event: PointerEvent<HTMLDivElement>) {
    if (origin.current) onResize(origin.current.width + event.clientX - origin.current.x);
  }
  function up(event: PointerEvent<HTMLDivElement>) {
    origin.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  }
  function key(event: KeyboardEvent<HTMLDivElement>) {
    const next = event.key === "ArrowLeft" ? width - sidebarWidth.step
      : event.key === "ArrowRight" ? width + sidebarWidth.step
        : event.key === "Home" ? sidebarWidth.min
          : event.key === "End" ? sidebarWidth.max : null;
    if (next === null) return;
    event.preventDefault();
    onResize(next);
  }
  return <div className="sidebar-resizer" role="separator" aria-orientation="vertical" aria-label="Resize sidebar" aria-controls="cell-sidebar"
    aria-valuenow={width} aria-valuemin={sidebarWidth.min} aria-valuemax={sidebarWidth.max} tabIndex={0}
    onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerCancel={up} onKeyDown={key} />;
}

function displayVersion(value?: string): string | undefined {
  return value ? value.startsWith("v") ? value : `v${value}` : undefined;
}
export function versionInfo(web?: string, daemon?: string) {
  const webVersion = displayVersion(web);
  const daemonVersion = displayVersion(daemon);
  const mismatch = !!webVersion && !!daemonVersion && webVersion !== daemonVersion;
  return {
    web: webVersion, daemon: daemonVersion, mismatch,
    summary: `web ${webVersion ?? "unknown"} · daemon ${daemonVersion ?? "unknown"}`,
    warning: mismatch ? `Version mismatch: web ${webVersion} ≠ daemon ${daemonVersion}` : "",
  };
}

export function VersionWarning({ label }: { label: string }) {
  return <span className="version-warning" role="img" aria-label={label} title={label}><Icon name="warning" /></span>;
}

// Rows shown in the expanded Connected section.
export function VersionRows({ info }: { info: ReturnType<typeof versionInfo> }) {
  return <div className="connection-versions">
    <dl aria-label="Versions">
      <div><dt>web</dt><dd>{info.web ?? "unknown"}</dd></div>
      <div><dt>daemon</dt><dd>{info.daemon ?? "unknown"}</dd></div>
    </dl>
    {info.mismatch && <VersionWarning label={info.warning} />}
  </div>;
}

// Collapsed icon rail: brand mark, expand button, majordomo, then in Workbench one state dot per cell,
// then the connection dot. In Manage and Settings the rail carries no per-kind icons (#1635): collapsing
// there is only a way to get the width back. In agent style (#1686) each agent is its glyph, toned by the
// roll-up state, followed by one dot per instance; a single-instance agent is only its glyph, which links
// to that cell. Dividers separate the agents, and the dots of the other cells come last.
export function SidebarRail({ view, inventory, selectedScope, connection, onExpand, majordomo, listStyle = "default" }: {
  view: View;
  listStyle?: CellListStyle;
  majordomo?: ReactNode;
  inventory: InventoryResource;
  selectedScope: string[];
  connection: { status: string; tone: string; info: ReturnType<typeof versionInfo> };
  onExpand: () => void;
}): ReactNode {
  // Like the Workbench sidebar, the rail lists active cells and the open one (#1635).
  const cells = activeCells(inventory.data?.rows[3] ?? [], selectedScope);
  const { agents, others } = agentGroups(cells);
  const tooltip = `Daemon: ${connection.status} · ${connection.info.summary}`;
  const isCurrent = (row: Row) => selectedScope.length === 4 && row.scope.every((part, i) => part === selectedScope[i]);
  const dot = (row: Row) => {
    const state = row.doc.status?.state;
    const label = `Open ${row.scope.join(" / ")} (${state || "state not reported"})`;
    return <li key={JSON.stringify(row.scope)}><a className={`rail-cell tone-${stateTone(state)}`} href={workspaceLink(row.scope)} aria-label={label} title={label} aria-current={isCurrent(row) ? "page" : undefined}><span className="state-dot" aria-hidden="true" /></a></li>;
  };
  return <div className="sidebar-rail">
    <a className="rail-brand" href={homeLink} aria-label="kukeon home" title="kukeon"><BrandMark /></a>
    <SidebarToggle collapsed onToggle={onExpand} />
    {majordomo}
    {view !== "workbench" ? <div className="rail-spacer" /> : <nav className="rail-cells" aria-label="Cell shortcuts">
      {listStyle === "agent" ? <ul>
        {agents.map(agent => {
          const count = `${agent.rows.length} ${agent.rows.length === 1 ? "instance" : "instances"}`;
          const named = `Agent ${agent.name}${agent.context ? ` (${agent.context})` : ""}, ${count}`;
          const tone = `rail-agent-glyph tone-${stateTone(rollupState(agent.rows))}`;
          if (agent.rows.length === 1) {
            const row = agent.rows[0];
            const label = `${named}: open ${row.scope.join(" / ")} (${row.doc.status?.state || "state not reported"})`;
            return <li className="rail-agent" key={agent.id}><a className={tone} href={workspaceLink(row.scope)} aria-label={label} title={label} aria-current={isCurrent(row) ? "page" : undefined}><Icon name="agent" /></a></li>;
          }
          return <li className="rail-agent" key={agent.id}><span className={tone} role="img" aria-label={named} title={named}><Icon name="agent" /></span><ul>{agent.rows.map(dot)}</ul></li>;
        })}
        {others.length > 0 && <li className="rail-others"><ul aria-label="Other cells">{others.map(dot)}</ul></li>}
      </ul> : <ul>{cells.map(dot)}</ul>}
    </nav>}
    <div className="rail-connection">
      <span className="rail-connection-dot" role="status" aria-label={tooltip} title={tooltip}><span className={`dot ${connection.tone}`} aria-hidden="true" /></span>
      {connection.info.mismatch && <VersionWarning label={connection.info.warning} />}
    </div>
  </div>;
}
