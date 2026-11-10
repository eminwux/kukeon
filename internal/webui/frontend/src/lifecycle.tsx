export type LifecycleAction = "start" | "stop" | "restart";
export type LifecycleFeedback = { action: LifecycleAction; phase: "pending" | "success" | "error"; message: string };

export function availableLifecycleActions(state?: string): LifecycleAction[] {
  if (state === "Ready") return ["stop", "restart"];
  return ["start"];
}

export function LifecycleNotice({ operation }: { operation?: LifecycleFeedback }) {
  if (!operation) return null;
  return <p className={`cell-lifecycle-feedback ${operation.phase}`} role={operation.phase === "error" ? "alert" : "status"} aria-live={operation.phase === "error" ? "assertive" : "polite"}>{operation.message}</p>;
}

export function CellLifecycleControls({
  scope,
  state,
  operation,
  onAction,
  compact = false,
  showFeedback = true,
}: {
  scope: string[];
  state?: string;
  operation?: LifecycleFeedback;
  onAction: (scope: string[], action: LifecycleAction, state?: string) => void;
  compact?: boolean;
  showFeedback?: boolean;
}) {
  const actions = availableLifecycleActions(state);
  return <div className={`cell-lifecycle${compact ? " is-compact" : ""}`} aria-label={`Lifecycle actions for ${scope.join(" / ")}`}>
    <div className="cell-lifecycle-buttons">
      {actions.map(action => <button key={action} type="button" disabled={operation?.phase === "pending"} onClick={() => onAction(scope, action, state)}>
        {operation?.phase === "pending" && operation.action === action ? `${action[0].toUpperCase()}${action.slice(1)}…` : action[0].toUpperCase() + action.slice(1)}
      </button>)}
      {!actions.length && operation?.phase !== "pending" && <span className="cell-lifecycle-unavailable">No lifecycle action in this state</span>}
    </div>
    {showFeedback && <LifecycleNotice operation={operation} />}
  </div>;
}
