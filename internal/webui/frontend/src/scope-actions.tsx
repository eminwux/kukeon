import { useRef, useState } from "react";
import { canConfirmDestructiveAction, disruptsDaemon } from "./lifecycle-policy.js";
import { DocumentRowLink, RowAction, RowActions } from "./icons";
import { kinds, resourceLink, titles } from "./routes";
import type { LifecycleFeedback, PurgeOptions } from "./lifecycle";
import { useModalDialog } from "./focus";

export type ParentAction = "delete" | "purge";

const childNoun = (depth: number, count: number) => {
  const title = titles[depth].toLowerCase();
  return count === 1 ? title.slice(0, -1) : title;
};

// Confirmation for deleting or purging a realm, space or stack (#1495), generalized from
// CellActionConfirmation. cascade and force map to the CLI flags and start off.
function ParentActionConfirmation({
  action,
  scope,
  counts,
  onCancel,
  onConfirm,
}: {
  action: ParentAction;
  scope: string[];
  counts: number[];
  onCancel: () => void;
  onConfirm: (options: PurgeOptions) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [typedScope, setTypedScope] = useState("");
  const [force, setForce] = useState(false);
  const [cascade, setCascade] = useState(false);
  const kind = kinds[scope.length - 1];
  const fullScope = scope.join(" / ");
  const verb = action === "delete" ? "Delete" : "Purge";
  const children = counts.map((count, index) => `${count} ${childNoun(scope.length + index, count)}`).join(", ");
  const hasChildren = counts.some(count => count > 0);

  useModalDialog(dialog);

  const consequence = action === "delete"
    ? `Delete removes this ${kind}'s metadata and cgroup when kukeond validation permits. It does not perform Purge's broader cleanup.`
    : `Purge requests the CLI's comprehensive ${kind} cleanup, including daemon-managed resources such as CNI networks and orphaned containers. Cleanup can be partial, and Purge may not be reversible.`;
  const enabled = canConfirmDestructiveAction(action, acknowledged, typedScope, fullScope);

  return <dialog
    className={`cell-action-dialog ${action === "purge" ? "is-purge" : ""}`}
    ref={dialog}
    aria-labelledby="parent-action-dialog-title"
    aria-describedby="parent-action-dialog-description"
    onCancel={event => { event.preventDefault(); onCancel(); }}
    onClick={event => { if (event.target === event.currentTarget) onCancel(); }}
  >
    <form onSubmit={event => { event.preventDefault(); if (enabled) onConfirm({ force, cascade }); }}>
      <h2 id="parent-action-dialog-title">{verb} {kind}</h2>
      <p id="parent-action-dialog-description">{consequence} The {kind} stays listed until a fresh daemon read confirms its removal.</p>
      <dl className="cell-action-target">
        <dt>Exact target</dt><dd><code>{fullScope}</code></dd>
        <dt>Contains (current inventory)</dt><dd>{children}</dd>
      </dl>
      {disruptsDaemon(scope) && <p className="scope-action-warning" role="note">
        This {kind} is inside the <code>kuke-system</code> realm, where kukeond runs. This action can stop kukeond and disconnect this UI. Recover with <code>kuke init</code>.
      </p>}
      {hasChildren && <p>Without <code>--cascade</code> or <code>--force</code>, kukeond refuses to {action} a {kind} that has children.</p>}
      <label className="cell-action-option">
        <input type="checkbox" checked={cascade} onChange={event => setCascade(event.target.checked)} />
        Pass <code>--cascade</code> ({action} child resources first)
      </label>
      <label className="cell-action-option">
        <input type="checkbox" checked={force} onChange={event => setForce(event.target.checked)} />
        Pass <code>--force</code> (skip validation)
      </label>
      {action === "delete" && <label className="cell-action-acknowledgment">
        <input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />
        I understand Delete removes this {kind} but does not perform Purge cleanup.
      </label>}
      {action === "purge" && <label className="cell-action-scope-confirmation">Type the full target above to enable Purge
        <input autoComplete="off" spellCheck={false} value={typedScope} onChange={event => setTypedScope(event.target.value)} />
      </label>}
      <div className="cell-action-dialog-buttons">
        <button type="button" onClick={onCancel}>Cancel</button>
        <button className="danger-action" type="submit" disabled={!enabled}>Confirm {verb}</button>
      </div>
    </form>
  </dialog>;
}

// Delete and Purge icons for one Realms, Spaces or Stacks row. Both always confirm first.
export function ParentRowActions({
  scope,
  counts,
  allowed,
  operation,
  onAction,
}: {
  scope: string[];
  counts: number[];
  allowed: boolean;
  operation?: LifecycleFeedback;
  onAction: (scope: string[], action: ParentAction, options: PurgeOptions) => void;
}) {
  const [confirming, setConfirming] = useState<ParentAction | null>(null);
  const pending = operation?.phase === "pending";
  const target = scope.join(" / ");
  const kind = kinds[scope.length - 1];
  return <>
    <RowActions busy={pending}>
      {allowed && (["delete", "purge"] as const).map(action => {
        const verb = action === "delete" ? "Delete" : "Purge";
        const busy = pending && operation?.action === action;
        return <RowAction key={action} icon={action} label={`${verb} ${kind} ${target}`} title={`${verb}…`} danger disabled={pending} busy={busy} onClick={() => { if (!pending) setConfirming(action); }} />;
      })}
      <DocumentRowLink href={resourceLink(scope, true)} label={target} />
    </RowActions>
    {confirming && <ParentActionConfirmation
      action={confirming}
      scope={scope}
      counts={counts}
      onCancel={() => setConfirming(null)}
      onConfirm={options => { setConfirming(null); onAction(scope, confirming, options); }}
    />}
  </>;
}
