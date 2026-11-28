import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { availableLifecycleActions, canConfirmDestructiveAction } from "./lifecycle-policy.js";
import { DocumentRowLink, Icon, RowAction, RowActions } from "@kukeon/ui";
import { resourceLink, workspaceLink } from "./routes";
import { useModalDialog } from "@kukeon/ui";

export type LifecycleAction = "start" | "stop" | "restart" | "kill" | "delete" | "purge";
export type DestructiveAction = Extract<LifecycleAction, "kill" | "delete" | "purge">;
export type PurgeOptions = { force: boolean; cascade: boolean };
export type LifecycleFeedback = { action: LifecycleAction; phase: "pending" | "success" | "error"; message: string };

// A cell purge offers no --force or --cascade choice (#1568): the controller's PurgeCell only records both
// on its result (internal/controller/purge_cell.go) and the runner purge takes neither, so for a leaf cell
// they change nothing. The request still carries both, off, as it always did by default; realm, space and
// stack purges keep the options in scope-actions.tsx, where they do apply.
const cellPurgeOptions: PurgeOptions = { force: false, cascade: false };

function CellActionConfirmation({
  action,
  scope,
  returnFocus,
  onCancel,
  onConfirm,
}: {
  action: DestructiveAction;
  scope: string[];
  // Where focus goes when the dialog closes and the control that opened it is no longer rendered.
  returnFocus?: () => HTMLElement | null | undefined;
  onCancel: () => void;
  onConfirm: (options?: PurgeOptions) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [typedScope, setTypedScope] = useState("");
  const fullScope = scope.join(" / ");

  useModalDialog(dialog, returnFocus);

  const title = action === "kill" ? "Kill cell" : action === "delete" ? "Delete cell" : "Purge cell";
  const consequence = action === "kill"
    ? "Kill immediately terminates all containers in this cell and sets its operational state to Stopped. The cell metadata and configuration remain, but running process work may be lost. This does not perform Delete or Purge cleanup."
    : action === "delete"
      ? "Delete removes the cell's containers, cgroup, and metadata when kukeond validation permits. It does not perform Purge's broader cleanup. A daemon rejection or partial result leaves the cell listed until a fresh daemon read confirms removal."
      : "Purge requests the CLI's comprehensive cell cleanup, including cell removal and daemon-managed cleanup such as CNI resources and orphaned containers. Cleanup can be partial; the cell stays listed until kukeond confirms its metadata is gone. Purge is destructive and may not be reversible.";
  const enabled = canConfirmDestructiveAction(action, acknowledged, typedScope, fullScope);

  return <dialog
    className={`cell-action-dialog ${action === "purge" ? "is-purge" : ""}`}
    ref={dialog}
    aria-labelledby="cell-action-dialog-title"
    aria-describedby="cell-action-dialog-description"
    onCancel={event => { event.preventDefault(); onCancel(); }}
    onClick={event => { if (event.target === event.currentTarget) onCancel(); }}
  >
    <form onSubmit={event => { event.preventDefault(); if (enabled) onConfirm(action === "purge" ? cellPurgeOptions : undefined); }}>
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
  const [open, setOpen] = useState(false);
  const pending = operation?.phase === "pending";

  // The menu hangs from the summary's right edge and shifts sideways only as far as needed to stay
  // inside a 16px viewport gutter: on phones the title row wraps and the summary may sit anywhere.
  useLayoutEffect(() => {
    const details = menu.current;
    const panel = details?.querySelector<HTMLElement>(".cell-advanced-menu");
    if (!open || !details || !panel) return;
    const place = () => {
      const anchor = details.getBoundingClientRect();
      const width = panel.offsetWidth;
      const left = Math.max(16, Math.min(anchor.right - width, window.innerWidth - 16 - width));
      panel.style.right = `${Math.round(anchor.right - left - width)}px`;
    };
    place();
    window.addEventListener("resize", place);
    return () => { window.removeEventListener("resize", place); panel.style.removeProperty("right"); };
  }, [open]);

  // While open, Escape closes the menu and returns focus to its summary, and a pointer down outside
  // closes it where it is. Escape is taken in the capture phase so the expanded terminal keeps its size.
  // The listeners stay attached and read the element's own open state: the `open` React state trails
  // the asynchronous toggle event, so gating on it lost an Escape pressed right after opening and
  // swallowed the confirmation dialog's Escape right after an action closed the menu.
  useEffect(() => {
    const escape = (event: KeyboardEvent) => {
      const details = menu.current;
      if (event.key !== "Escape" || !details?.open) return;
      event.preventDefault();
      // The expanded terminal also listens on window in the capture phase; whichever registered first,
      // the menu's Escape ends here so the terminal keeps its size.
      event.stopImmediatePropagation();
      details.open = false;
      details.querySelector("summary")?.focus();
    };
    const outside = (event: PointerEvent) => {
      const details = menu.current;
      if (details?.open && !details.contains(event.target as Node | null)) details.open = false;
    };
    window.addEventListener("keydown", escape, true);
    document.addEventListener("pointerdown", outside, true);
    return () => { window.removeEventListener("keydown", escape, true); document.removeEventListener("pointerdown", outside, true); };
  }, []);

  function request(action: DestructiveAction) {
    if (pending) return;
    if (menu.current) menu.current.open = false;
    setConfirming(action);
  }

  return <>
    <details className="cell-advanced-actions" ref={menu} onToggle={event => setOpen(event.currentTarget.open)}>
      <summary aria-label={`Advanced actions for ${scope.join(" / ")}`}>Advanced actions</summary>
      <div className="cell-advanced-menu">
        <section aria-labelledby="cell-advanced-operations">
          <h2 id="cell-advanced-operations">Operations</h2>
          <button className="danger-action" type="button" disabled={pending} onClick={() => request("kill")}>Kill cell…</button>
        </section>
        <section className="cell-destructive-actions" aria-labelledby="cell-destructive-actions-title">
          <h2 id="cell-destructive-actions-title">Destructive actions</h2>
          <button className="danger-action" type="button" disabled={pending} onClick={() => request("delete")}>Delete cell…</button>
          <button className="danger-action" type="button" disabled={pending} onClick={() => request("purge")}>Purge cell…</button>
        </section>
      </div>
    </details>
    {confirming && <CellActionConfirmation
      action={confirming}
      scope={scope}
      returnFocus={() => menu.current?.querySelector("summary")}
      onCancel={() => setConfirming(null)}
      onConfirm={options => { setConfirming(null); onAction(scope, confirming, undefined, options); }}
    />}
  </>;
}

const actionTitle: Record<LifecycleAction, string> = { start: "Start", stop: "Stop", restart: "Restart", kill: "Kill", delete: "Delete", purge: "Purge" };

// Quick actions for one row of the Cells table (#1494). Start/Stop/Restart run immediately, as on the
// cell screen; Kill, Delete and Purge always open CellActionConfirmation first. Every action goes through
// the same onAction (runCellLifecycle) as the cell screen, and an in-flight action disables the row.
export function CellRowActions({
  scope,
  state,
  operation,
  onAction,
}: {
  scope: string[];
  state?: string;
  operation?: LifecycleFeedback;
  onAction: (scope: string[], action: LifecycleAction, state?: string, options?: PurgeOptions) => void;
}) {
  const [confirming, setConfirming] = useState<DestructiveAction | null>(null);
  const pending = operation?.phase === "pending";
  const target = scope.join(" / ");
  const button = (action: LifecycleAction, icon: LifecycleAction, run: () => void, danger = false) => {
    const busy = pending && operation?.action === action;
    return <RowAction key={action} icon={icon} label={`${actionTitle[action]} cell ${target}`} title={busy || danger ? `${actionTitle[action]}…` : actionTitle[action]} danger={danger} disabled={pending} busy={busy} onClick={run} />;
  };
  return <>
    <RowActions busy={pending}>
      {availableLifecycleActions(state).map(action => button(action, action, () => onAction(scope, action, state)))}
      <RowAction icon="terminal" href={workspaceLink(scope)} label={`Open terminal for ${target}`} title="Open terminal" />
      {(["kill", "delete", "purge"] as const).map(action => button(action, action, () => { if (!pending) setConfirming(action); }, true))}
      <DocumentRowLink href={resourceLink(scope, true)} label={target} />
    </RowActions>
    {confirming && <CellActionConfirmation
      action={confirming}
      scope={scope}
      onCancel={() => setConfirming(null)}
      onConfirm={options => { setConfirming(null); onAction(scope, confirming, undefined, options); }}
    />}
  </>;
}

// Start leads, Restart is secondary and Stop shares the danger style with the destructive menu actions.
const actionVariant: Record<LifecycleAction, string> = { start: "primary-action", restart: "", stop: "danger-action", kill: "danger-action", delete: "danger-action", purge: "danger-action" };

export function CellLifecycleControls({
  scope,
  state,
  operation,
  onAction,
  compact = false,
}: {
  scope: string[];
  state?: string;
  operation?: LifecycleFeedback;
  onAction: (scope: string[], action: LifecycleAction, state?: string, options?: PurgeOptions) => void;
  compact?: boolean;
}) {
  const actions = availableLifecycleActions(state);
  return <div className={`cell-lifecycle${compact ? " is-compact" : ""}`} aria-label={`Lifecycle actions for ${scope.join(" / ")}`}>
    <div className="cell-lifecycle-buttons">
      {actions.map(action => {
        const label = operation?.phase === "pending" && operation.action === action ? `${action[0].toUpperCase()}${action.slice(1)}…` : action[0].toUpperCase() + action.slice(1);
        return <button key={action} className={`icon-button ${actionVariant[action]}`} type="button" aria-label={label} data-tooltip={label} disabled={operation?.phase === "pending"} onClick={() => onAction(scope, action, state)}>
          <Icon name={action === "start" ? "start" : action === "stop" ? "stop" : "restart"} />
        </button>;
      })}
      {!actions.length && operation?.phase !== "pending" && <span className="cell-lifecycle-unavailable">No lifecycle action in this state</span>}
    </div>
    <AdvancedCellActions scope={scope} operation={operation} onAction={onAction} />
  </div>;
}
