import type { ReactNode } from "react";

// Inline stroke glyphs drawn on the same 24px grid as the toolbar refresh and appearance icons.
const glyphs = {
  attach: <><path d="M9 15 15 9" /><path d="M11 6.5 13 4.5a4 4 0 0 1 5.66 5.66L16.5 12.3" /><path d="M13 17.5 11 19.5a4 4 0 0 1-5.66-5.66L7.5 11.7" /></>,
  detach: <><path d="M11 6.5 13 4.5a4 4 0 0 1 5.66 5.66L16.5 12.3" /><path d="M13 17.5 11 19.5a4 4 0 0 1-5.66-5.66L7.5 11.7" /><path d="M4 4l16 16" /></>,
  reconnect: <><path d="M20 11a8.1 8.1 0 0 0-14.9-4L3 10" /><path d="M3 4v6h6" /><path d="M9.5 14.5l5-5" /><path d="M13 16.5l-1.5 1.5a3 3 0 0 1-4.24-4.24L8.5 12.5" /></>,
  expand: <><path d="M14 4h6v6" /><path d="M20 4l-6.5 6.5" /><path d="M10 20H4v-6" /><path d="M4 20l6.5-6.5" /></>,
  collapse: <><path d="M20 10h-6V4" /><path d="M14 10l6.5-6.5" /><path d="M4 14h6v6" /><path d="M10 14l-6.5 6.5" /></>,
  start: <path d="M8 5.5v13l10.5-6.5Z" />,
  stop: <rect x="6" y="6" width="12" height="12" rx="1.5" />,
  restart: <><path d="M20 12a8 8 0 1 1-2.34-5.66" /><path d="M20 4v5h-5" /></>,
  // Tornado mark for the brand, collapsed rail and favicon (src/favicon.svg mirrors these strokes).
  tornado: <><path d="M3 4.5h18" /><path d="M5 8.5h14" /><path d="M7.5 12.5h9.5" /><path d="M9 16.5h6" /><path d="M10.5 20.5h3" /></>,
  sidebarCollapse: <><rect x="3.5" y="4.5" width="17" height="15" rx="2" /><path d="M9 4.5v15" /><path d="m15.5 10-2 2 2 2" /></>,
  sidebarExpand: <><rect x="3.5" y="4.5" width="17" height="15" rx="2" /><path d="M9 4.5v15" /><path d="m13.5 10 2 2-2 2" /></>,
  warning: <><path d="M10.3 4.2 2.8 17.5A2 2 0 0 0 4.5 20.5h15a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0Z" /><path d="M12 9.5v4.5" /><path d="M12 17.5h.01" /></>,
  info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v5" /><path d="M12 7.5h.01" /></>,
  document: <><path d="M14 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8.5Z" /><path d="M14 3.5v5h5" /><path d="M9 13h6M9 16.5h4" /></>,
  // Row quick-action glyphs (#1494).
  kill: <path d="M13 3 5.5 13.5h5.5L10 21l7.5-10.5H12Z" />,
  delete: <><path d="M4.5 7h15" /><path d="M9.5 7V4.5h5V7" /><path d="m6.5 7 1 12a1.5 1.5 0 0 0 1.5 1.5h6a1.5 1.5 0 0 0 1.5-1.5l1-12" /></>,
  purge: <><path d="M4.5 7h15" /><path d="M9.5 7V4.5h5V7" /><path d="m6.5 7 1 12a1.5 1.5 0 0 0 1.5 1.5h6a1.5 1.5 0 0 0 1.5-1.5l1-12" /><path d="m10 11.5 4 4m0-4-4 4" /></>,
  edit: <><path d="M4.5 19.5h4l10-10a2.83 2.83 0 0 0-4-4l-10 10Z" /><path d="m13 7 4 4" /></>,
  terminal: <><rect x="3" y="4.5" width="18" height="15" rx="2" /><path d="m7 9.5 3 2.5-3 2.5" /><path d="M12.5 15H17" /></>,
  create: <path d="M12 5v14M5 12h14" />,
  // Majordomo: a bell on its base.
  majordomo: <><path d="M5.5 16.5a6.5 6.5 0 0 1 13 0Z" /><path d="M12 10V7.5" /><path d="M10 7.5h4" /><path d="M3.5 19.5h17" /></>,
  pending: <path d="M12 3.5a8.5 8.5 0 1 0 8.5 8.5" />,
} satisfies Record<string, ReactNode>;
export type IconName = keyof typeof glyphs;

export function Icon({ name }: { name: IconName }) {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">{glyphs[name]}</svg>;
}

// One quiet per-row icon action, revealed on row hover and keyboard focus. A link when given href,
// otherwise a button. label is the accessible name (action and target); title is the short tooltip.
export function RowAction({ icon, label, title, href, onClick, danger = false, disabled = false, busy = false, newTab = false }: {
  icon: IconName;
  label: string;
  title: string;
  href?: string;
  onClick?: () => void;
  danger?: boolean;
  disabled?: boolean;
  busy?: boolean;
  newTab?: boolean;
}) {
  const className = `row-action${danger ? " is-danger" : ""}${busy ? " is-busy" : ""}`;
  const glyph = <Icon name={busy ? "pending" : icon} />;
  if (href !== undefined) return <a className={className} href={href} aria-label={label} title={title} {...newTab ? { target: "_blank", rel: "noopener noreferrer" } : {}}>{glyph}</a>;
  return <button className={className} type="button" aria-label={label} title={title} aria-busy={busy || undefined} disabled={disabled} onClick={onClick}>{glyph}</button>;
}

// Compact group of row actions for a table's td.row-actions cell.
export function RowActions({ children, busy = false }: { children: ReactNode; busy?: boolean }) {
  return <span className="row-action-group" aria-busy={busy || undefined}>{children}</span>;
}

// Quiet per-row document link, revealed on row hover and keyboard focus.
export function DocumentRowLink({ href, label, newTab = false }: { href: string; label: string; newTab?: boolean }) {
  return <RowAction icon="document" href={href} label={`View document for ${label}`} title="View document" newTab={newTab} />;
}
