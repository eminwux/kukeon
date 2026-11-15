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
  document: <><path d="M14 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8.5Z" /><path d="M14 3.5v5h5" /><path d="M9 13h6M9 16.5h4" /></>,
} satisfies Record<string, ReactNode>;
export type IconName = keyof typeof glyphs;

export function Icon({ name }: { name: IconName }) {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">{glyphs[name]}</svg>;
}

// Quiet per-row document link, revealed on row hover and keyboard focus.
export function DocumentRowLink({ href, label, newTab = false }: { href: string; label: string; newTab?: boolean }) {
  return <a className="row-action" href={href} aria-label={`View document for ${label}`} title="View document" {...newTab ? { target: "_blank", rel: "noopener noreferrer" } : {}}><Icon name="document" /></a>;
}
