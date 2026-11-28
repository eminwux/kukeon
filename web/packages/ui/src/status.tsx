import { Fragment } from "react";
// Every state badge maps onto the shared --status-* tokens defined in style.css.
export type StatusTone = "ok" | "stopped" | "warning" | "error" | "pending" | "neutral";
export function stateTone(state?: string): StatusTone {
  return ["Ready", "Running", "done", "cloned", "fetched"].includes(state ?? "") ? "ok"
    : ["Failed", "Error", "failed"].includes(state ?? "") ? "error"
    : ["Degraded", "Not ready", "Pausing"].includes(state ?? "") ? "warning"
    : ["Pending", "Creating", "Starting", "Stopping"].includes(state ?? "") ? "pending"
    : ["Stopped", "Exited", "Paused"].includes(state ?? "") ? "stopped" : "neutral";
}
export function StateBadge({ state }: { state?: string }) {
  return <span className={`state-badge tone-${stateTone(state)}`}><span className="state-dot" aria-hidden="true" />{state || "Not reported"}</span>;
}
// The one state dot of an agent row or heading (#1686): the roll-up state as a toned dot, named for
// assistive technology and in its tooltip, never colour alone.
export function RollupDot({ state }: { state?: string }) {
  const label = state || "State not reported";
  return <span className={`rollup-dot tone-${stateTone(state)}`} title={label}><span className="state-dot" aria-hidden="true" /><span className="sr-only">{label}</span></span>;
}

// Config drift (#1568). A Ready cell can still be out of sync with its Config, or the daemon may have failed
// to compare them. The badge carries a text label, never colour alone; the reason is in its tooltip and,
// through the visually hidden text, in its accessible name.
export type DriftStatus = { outOfSync?: boolean; outOfSyncReason?: string; outOfSyncError?: string };
export function hasDrift(status?: DriftStatus): boolean {
  return !!status?.outOfSync || !!status?.outOfSyncError;
}
export function driftReason(status?: DriftStatus): string {
  return status?.outOfSyncError ? `Configuration comparison failed: ${status.outOfSyncError}`
    : `Configuration out of sync: ${status?.outOfSyncReason || "reason not reported"}`;
}
export function DriftBadge({ status }: { status?: DriftStatus }) {
  if (!hasDrift(status)) return null;
  const reason = driftReason(status);
  return <span className="state-badge tone-warning drift-badge" title={reason}><span className="state-dot" aria-hidden="true" />{status?.outOfSyncError ? "Drift unknown" : "Drift"}<span className="sr-only">. {reason}</span></span>;
}

// Scope and socket paths wrap only at their separators (#1567). Each segment, with the separator that
// ends it, is an inline-block, so lines break between segments; a segment breaks inside only when it
// alone is wider than the line.
export function PathText({ segments, joiner = "" }: { segments: string[]; joiner?: string }) {
  return <>{segments.map((segment, i) => <Fragment key={i}>{i > 0 && joiner}<span className="path-segment">{segment}</span></Fragment>)}</>;
}
// realm / space / stack, breaking only after a " /". The space inside a segment does not break: the
// inline-block keeps its max-content width whenever that fits on the line.
export function ScopePath({ parts, empty = "—" }: { parts: string[]; empty?: string }) {
  return parts.length ? <PathText segments={parts.map((part, i) => i < parts.length - 1 ? `${part} /` : part)} joiner=" " /> : <>{empty}</>;
}
// A socket endpoint such as unix:///run/kukeon/kukeond.sock, breaking only after a "/".
export function SocketPath({ path }: { path: string }) {
  return <PathText segments={path.split(/(?<=\/)/)} />;
}
