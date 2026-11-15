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
