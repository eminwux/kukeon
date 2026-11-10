import { useEffect, useRef, useState } from "react";
import { availableLifecycleActions, canConfirmDestructiveAction } from "./lifecycle-policy.js";

export type LifecycleAction = "start" | "stop" | "restart" | "kill" | "delete" | "purge";
export type DestructiveAction = Extract<LifecycleAction, "kill" | "delete" | "purge">;
export type PurgeOptions = { force: boolean; cascade: boolean };
export type LifecycleFeedback = { action: LifecycleAction; phase: "pending" | "success" | "error"; message: string };

export function LifecycleNotice({ operation }: { operation?: LifecycleFeedback }) {
  if (!operation) return null;
  return <p className={`cell-lifecycle-feedback ${operation.phase}`} role={operation.phase === "error" ? "alert" : "status"} aria-live={operation.phase === "error" ? "assertive" : "polite"}>{operation.message}</p>;
}

function CellActionConfirmation({
  action,
  scope,
  onCancel,
  onConfirm,
}: {
  action: DestructiveAction;
  scope: string[];
  onCancel: () => void;
  onConfirm: (options?: PurgeOptions) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [typedScope, setTypedScope] = useState("");
  const [force, setForce] = useState(false);
  const [cascade, setCascade] = useState(false);
  const fullScope = scope.join(" / ");

  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    element.showModal();
    return () => { if (element.open) element.close(); };
  }, []);

  const title = action === "kill" ? "Kill cell" : action === "delete" ? "Delete cell" : "Purge cell";
  const consequence = action === "kill"
    ? "Kill immediately terminates all containers in this cell and sets its operational state to Stopped. The cell metadata and configuration remain, but running process work may be lost. This does not perform Delete or Purge cleanup."
    : action === "delete"
      ? "Delete removes the cell's containers, cgroup, and metadata when kukeond validation permits. It does not perform Purge's broader cleanup. A daemon rejection or partial result leaves the cell in the workspace until a fresh daemon read confirms removal."
      : "Purge requests the CLI's comprehensive cell cleanup, including cell removal and daemon-managed cleanup such as CNI resources and orphaned containers. Cleanup can be partial; the workspace will keep the cell until kukeond confirms its metadata is gone. Purge is destructive and may not be reversible.";
  const enabled = canConfirmDestructiveAction(action, acknowledged, typedScope, fullScope);

  return <dialog
    className={`cell-action-dialog ${action === "purge" ? "is-purge" : ""}`}
    ref={dialog}
    aria-labelledby="cell-action-dialog-title"
    aria-describedby="cell-action-dialog-description"
    onCancel={event => { event.preventDefault(); onCancel(); }}
    onClick={event => { if (event.target === event.currentTarget) onCancel(); }}
  >
    <form onSubmit={event => { event.preventDefault(); if (enabled) onConfirm(action === "purge" ? { force, cascade } : undefined); }}>
      <h2 id="cell-action-dialog-title">{title}</h2>
      <p id="cell-action-dialog-description">{consequence}</p>
      <dl className="cell-action-target"><dt>Exact target</dt><dd><code>{fullScope}</code></dd></dl>
      {action === "delete" && <label className="cell-action-acknowledgment">
        <input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />
        I understand Delete removes this cell but does not perform Purge cleanup.
      </label>}
      {action === "kill" && <label className="cell-action-acknowledgment">
        <input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />
        I understand this immediately terminates the cell's running containers.
      </label>}
      {action === "purge" && <>
        <label className="cell-action-option">
          <input type="checkbox" checked={force} onChange={event => setForce(event.target.checked)} />
          Pass <code>--force</code> (skip validation where supported)
        </label>
        <small className="cell-action-option-help">For cells, kukeond currently records this option but does not bypass controller validation.</small>
        <label className="cell-action-option">
          <input type="checkbox" checked={cascade} onChange={event => setCascade(event.target.checked)} />
          Pass <code>--cascade</code> (request recursive cleanup)
        </label>
        <small className="cell-action-option-help">Cells are leaf resources; the current cell purge operation accepts but does not apply cascade.</small>
        <label className="cell-action-scope-confirmation">Type the full target above to enable Purge
          <input autoComplete="off" spellCheck={false} value={typedScope} onChange={event => setTypedScope(event.target.value)} />
        </label>
      </>}
      <div className="cell-action-dialog-buttons">
        <button type="button" onClick={onCancel}>Cancel</button>
        <button className="danger-action" type="submit" disabled={!enabled}>
          {action === "kill" ? "Confirm Kill" : action === "delete" ? "Confirm Delete" : "Confirm Purge"}
        </button>
      </div>
    </form>
  </dialog>;
}

function AdvancedCellActions({
  scope,
  operation,
  onAction,
}: {
  scope: string[];
  operation?: LifecycleFeedback;
  onAction: (scope: string[], action: LifecycleAction, state?: string, options?: PurgeOptions) => void;
}) {
  const menu = useRef<HTMLDetailsElement>(null);
  const [confirming, setConfirming] = useState<DestructiveAction | null>(null);
  const pending = operation?.phase === "pending";

  function request(action: DestructiveAction) {
    if (pending) return;
    if (menu.current) menu.current.open = false;
    setConfirming(action);
  }

  return <>
    <details className="cell-advanced-actions" ref={menu}>
      <summary aria-label={`Advanced actions for ${scope.join(" / ")}`}>Advanced actions</summary>
      <div className="cell-advanced-menu">
        <section aria-labelledby="cell-advanced-operations">
          <h2 id="cell-advanced-operations">Operations</h2>
          <button type="button" disabled={pending} onClick={() => request("kill")}>Kill cell…</button>
        </section>
        <section className="cell-destructive-actions" aria-labelledby="cell-destructive-actions-title">
          <h2 id="cell-destructive-actions-title">Destructive actions</h2>
          <button type="button" disabled={pending} onClick={() => request("delete")}>Delete cell…</button>
          <button className="danger-action" type="button" disabled={pending} onClick={() => request("purge")}>Purge cell…</button>
        </section>
      </div>
    </details>
    {confirming && <CellActionConfirmation
      action={confirming}
      scope={scope}
      onCancel={() => setConfirming(null)}
      onConfirm={options => { setConfirming(null); onAction(scope, confirming, undefined, options); }}
    />}
  </>;
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
  onAction: (scope: string[], action: LifecycleAction, state?: string, options?: PurgeOptions) => void;
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
    <AdvancedCellActions scope={scope} operation={operation} onAction={onAction} />
    {showFeedback && <LifecycleNotice operation={operation} />}
  </div>;
}
