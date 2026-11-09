export function StateBadge({ state }: { state?: string }) {
  const tone = ["Ready", "Running", "done", "cloned", "fetched"].includes(state ?? "") ? "success"
    : ["Failed", "Error", "failed"].includes(state ?? "") ? "danger"
    : ["Pending", "Creating", "Pausing", "Degraded", "Not ready"].includes(state ?? "") ? "warning" : "neutral";
  return <span className={`state-badge tone-${tone}`}><span className="state-dot" aria-hidden="true" />{state || "Not reported"}</span>;
}
