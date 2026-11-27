import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useModalDialog } from "./focus";
import { useNotify } from "./notifications";
import { batchSummary, batchTargets, counted, headerState, offeredActions, pruneSelection, purgePhrase, runBatch, toggleAll, toggleKey } from "./bulk.mjs";
import type { PurgeOptions } from "./lifecycle";

// Row selection and bulk actions of the list tables (#1745). A table keeps its selection by row key across
// pages and searches (useSelection), renders a checkbox per row and one in the header, and hands the selected
// rows to BulkActions: the bar, one confirmation for the whole batch, the run through the table's own
// per-row request, and the report of the rows that failed or were skipped.

export type BulkRow = { key: string; scope: string[]; label: string };
export type BulkAction<R extends BulkRow> = {
  name: string;
  // The verb on the bar and the confirm button: Delete, Restart.
  verb: string;
  danger?: boolean;
  // Why the action does not apply to a row; "" when it does.
  reasonOf?: (row: R) => string;
  consequence: string;
  // The single action's acknowledgment, worded for the batch.
  acknowledgment?: string;
  // A purge asks for a typed phrase with the count instead of one full target.
  purge?: boolean;
  // Realm, space and stack removals take one set of --force and --cascade for the batch.
  options?: boolean;
};
export type BulkOutcome = { ok: boolean; message?: string } | undefined;

// useSelection keeps the selected row keys. keys are every row of the table (not only the matching ones):
// once ready, a key that leaves them is pruned.
export function useSelection(keys: string[], ready: boolean) {
  const [selected, setSelected] = useState<string[]>([]);
  const signature = keys.join("\n");
  useEffect(() => { if (ready) setSelected(old => pruneSelection(old, keys)); }, [signature, ready]);
  return {
    selected,
    has: (key: string) => selected.includes(key),
    toggle: (key: string, on: boolean) => setSelected(old => toggleKey(old, key, on)),
    toggleAll: (matching: string[]) => setSelected(old => toggleAll(old, matching)),
    state: (matching: string[]) => headerState(selected, matching),
    clear: () => setSelected([]),
  };
}
export type Selection = ReturnType<typeof useSelection>;

// SelectAllBox is the header checkbox: it selects every row that matches the search and filters, on every page.
export function SelectAllBox({ selection, matching, label }: { selection: Selection; matching: string[]; label: string }) {
  const box = useRef<HTMLInputElement>(null);
  const state = selection.state(matching);
  useLayoutEffect(() => { if (box.current) box.current.indeterminate = state === "some"; }, [state]);
  return <input ref={box} type="checkbox" className="bulk-check" aria-label={label} checked={state === "all"} disabled={!matching.length} onChange={() => selection.toggleAll(matching)} />;
}

export function SelectRowBox({ selection, rowKey, label }: { selection: Selection; rowKey: string; label: string }) {
  return <input type="checkbox" className="bulk-check" aria-label={label} checked={selection.has(rowKey)} onChange={event => selection.toggle(rowKey, event.target.checked)} />;
}

type Skipped<R> = { row: R; reason: string };
// pluralOf is a noun's plural word: cells.
const pluralOf = (noun: (count: number) => string) => noun(2).replace(/^2 /, "");
type Report<R> = { verb: string; failed: { row: R; message: string }[]; skipped: Skipped<R>[] };

function BulkConfirmation<R extends BulkRow>({ action, targets, skipped, noun, onCancel, onConfirm }: {
  action: BulkAction<R>; targets: R[]; skipped: Skipped<R>[]; noun: (count: number) => string;
  onCancel: () => void; onConfirm: (options: PurgeOptions) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [typed, setTyped] = useState("");
  const [force, setForce] = useState(false);
  const [cascade, setCascade] = useState(false);
  useModalDialog(dialog);
  const what = noun(targets.length);
  // purge 12 cells, purge 1 realm.
  const phrase = purgePhrase(targets.length, what.replace(/^\d+ /, ""));
  const enabled = action.purge ? typed.trim() === phrase : !action.acknowledgment || acknowledged;
  return <dialog
    className={`cell-action-dialog bulk-dialog${action.purge ? " is-purge" : ""}`}
    ref={dialog}
    aria-labelledby="bulk-dialog-title"
    aria-describedby="bulk-dialog-description"
    onCancel={event => { event.preventDefault(); onCancel(); }}
    onClick={event => { if (event.target === event.currentTarget) onCancel(); }}
  >
    <form onSubmit={event => { event.preventDefault(); if (enabled) onConfirm({ force, cascade }); }}>
      <h2 id="bulk-dialog-title">{action.verb} {what}</h2>
      <p id="bulk-dialog-description">{action.consequence} Each one runs through its own request; a failure is listed and does not stop the rest.</p>
      <dl className="cell-action-target">
        <dt>Targets ({targets.length})</dt>
        <dd><ul className="bulk-targets" tabIndex={0} aria-label="Targets">{targets.map(row => <li key={row.key}><code>{row.label}</code></li>)}</ul></dd>
        {!!skipped.length && <><dt>Skipped ({skipped.length})</dt>
          <dd><ul className="bulk-targets" tabIndex={0} aria-label="Skipped">{skipped.map(item => <li key={item.row.key}><code>{item.row.label}</code> — {item.reason}</li>)}</ul></dd></>}
      </dl>
      {action.options && <>
        <label className="cell-action-option"><input type="checkbox" checked={cascade} onChange={event => setCascade(event.target.checked)} />Pass <code>--cascade</code> to each ({action.name} child resources first)</label>
        <label className="cell-action-option"><input type="checkbox" checked={force} onChange={event => setForce(event.target.checked)} />Pass <code>--force</code> to each (skip validation)</label>
      </>}
      {action.acknowledgment && !action.purge && <label className="cell-action-acknowledgment">
        <input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />
        {action.acknowledgment}
      </label>}
      {action.purge && <label className="cell-action-scope-confirmation">Type <code>{phrase}</code> to enable Purge
        <input autoComplete="off" spellCheck={false} value={typed} onChange={event => setTyped(event.target.value)} />
      </label>}
      <div className="cell-action-dialog-buttons">
        <button type="button" onClick={onCancel}>Cancel</button>
        <button className={action.danger ? "danger-action" : "primary-action"} type="submit" disabled={!enabled}>{action.verb} {what}</button>
      </div>
    </form>
  </dialog>;
}

// BulkActions is the bar over a table with selected rows. run is the table's own per-row request; it
// reports quietly, so the batch ends in one summary toast.
export function BulkActions<R extends BulkRow>({ rows, actions, noun, selection, run, extra }: {
  rows: R[];
  actions: BulkAction<R>[];
  // A count of rows: 1 cell, 2 cells.
  noun: (count: number) => string;
  selection: Selection;
  run: (row: R, action: BulkAction<R>, options: PurgeOptions) => Promise<BulkOutcome>;
  extra?: ReactNode;
}) {
  const { notify } = useNotify();
  const [confirming, setConfirming] = useState<{ action: BulkAction<R>; targets: R[]; skipped: Skipped<R>[] }>();
  const [running, setRunning] = useState("");
  const [report, setReport] = useState<Report<R>>();
  const reasonOf = (row: R, action: BulkAction<R>) => action.reasonOf?.(row) ?? "";
  const offered = offeredActions(rows, actions, reasonOf);
  async function start(action: BulkAction<R>, targets: R[], skipped: Skipped<R>[], options: PurgeOptions) {
    setConfirming(undefined);
    setRunning(action.verb);
    setReport(undefined);
    const results = await runBatch(targets, row => run(row, action, options));
    const failed = results.filter(result => !result.ok).map(result => ({ row: result.item, message: result.message || "The request failed." }));
    const summary = batchSummary(action.verb, pluralOf(noun), results, skipped.length);
    notify({ key: `bulk:${noun(2)}`, tone: summary.tone, message: summary.message });
    setReport(failed.length || skipped.length ? { verb: action.verb, failed, skipped } : undefined);
    setRunning("");
    selection.clear();
  }
  if (!rows.length && !report && !running) return null;
  return <>
    {(!!rows.length || running) && <div className="bulk-bar" role="region" aria-label="Bulk actions">
      <span className="bulk-count" role="status">{running ? `${running}: ${noun(rows.length)}…` : `${noun(rows.length)} selected`}</span>
      {offered.map(action => <button key={action.name} type="button" className={action.danger ? "danger-action" : undefined} disabled={!!running} onClick={() => {
        const { targets, skipped } = batchTargets(rows, row => reasonOf(row, action));
        setConfirming({ action, targets, skipped });
      }}>{action.verb}…</button>)}
      {!offered.length && <span className="description">No action applies to every selected row; each one is skipped.</span>}
      {extra}
      <button type="button" className="subtle-button" disabled={!!running} onClick={selection.clear}>Clear</button>
    </div>}
    {report && <div className={report.failed.length ? "error bulk-report" : "bulk-report"} role={report.failed.length ? "alert" : "status"} aria-label={`${report.verb} report`}>
      <strong>{report.verb}: {report.failed.length ? `${report.failed.length} failed` : "done"}{report.skipped.length ? `, ${report.skipped.length} skipped` : ""}</strong>
      {!!report.failed.length && <ul aria-label="Failed">{report.failed.map(item => <li key={item.row.key}><code>{item.row.label}</code>: {item.message}</li>)}</ul>}
      {!!report.skipped.length && <ul aria-label="Skipped">{report.skipped.map(item => <li key={item.row.key}><code>{item.row.label}</code> — {item.reason}</li>)}</ul>}
      <button type="button" className="subtle-button" onClick={() => setReport(undefined)}>Dismiss</button>
    </div>}
    {confirming && <BulkConfirmation action={confirming.action} targets={confirming.targets} skipped={confirming.skipped} noun={noun} onCancel={() => setConfirming(undefined)} onConfirm={options => void start(confirming.action, confirming.targets, confirming.skipped, options)} />}
  </>;
}

// countNoun builds a table's noun from its singular and plural words.
export function countNoun(singular: string, plural = `${singular}s`) {
  return (count: number) => counted(count, singular, plural);
}
