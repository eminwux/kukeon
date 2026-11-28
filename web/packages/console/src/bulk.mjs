// Pure logic of the list tables' row selection and bulk actions (#1745): the
// selection over row keys, the header checkbox's state, which rows an action
// applies to, and a runner that sends the existing per-row requests a few at
// a time and collects one result per row. No DOM, no network — unit-tested
// with node --test.

import { disruptsDaemon } from "./lifecycle-policy.js";

// How many per-row requests a batch has in flight at once.
export const batchConcurrency = 4;

const list = value => Array.isArray(value) ? value : [];

// toggleKey selects or clears one row.
export function toggleKey(selected, key, on) {
  const keys = list(selected).filter(item => item !== key);
  return on ? [...keys, key] : keys;
}

// pruneSelection drops the keys of rows that left the list. Rows outside the
// current search, filters or page stay selected.
export function pruneSelection(selected, keys) {
  const present = new Set(list(keys));
  const kept = list(selected).filter(key => present.has(key));
  return kept.length === list(selected).length ? selected : kept;
}

// headerState is the header checkbox over the rows that match the current
// search and filters, on every page: none, some or all of them selected.
export function headerState(selected, matching) {
  const chosen = new Set(list(selected));
  const count = list(matching).filter(key => chosen.has(key)).length;
  return !count ? "none" : count === list(matching).length ? "all" : "some";
}

// toggleAll is the header checkbox: with every matching row selected it
// clears them, otherwise it selects them all. Selected rows that do not
// match stay as they are.
export function toggleAll(selected, matching) {
  const keys = list(matching);
  if (headerState(selected, keys) === "all") {
    const drop = new Set(keys);
    return list(selected).filter(key => !drop.has(key));
  }
  return [...new Set([...list(selected), ...keys])];
}

// systemSkip is why a target inside kuke-system is never part of a batch: it
// can stop kukeond and disconnect the UI.
export const systemSkip = "inside kuke-system, where kukeond runs: act on it on its own";

// batchTargets splits the selected rows for one action into the targets and
// the skipped ones. reasonOf(row) returns why the action does not apply to a
// row, or "" when it does; a row inside kuke-system is always skipped.
export function batchTargets(rows, reasonOf) {
  const targets = [], skipped = [];
  for (const row of list(rows)) {
    const reason = disruptsDaemon(list(row?.scope)) ? systemSkip : reasonOf(row) || "";
    if (reason) skipped.push({ row, reason }); else targets.push(row);
  }
  return { targets, skipped };
}

// offeredActions are the actions the bulk bar offers: each one that applies
// to at least one selected row, in the given order.
export function offeredActions(rows, actions, reasonOf) {
  return list(actions).filter(action => batchTargets(rows, row => reasonOf(row, action)).targets.length > 0);
}

// runBatch runs one per-row request for every item, at most limit at once,
// and returns one result per item in the items' order. A request that throws
// is a failed row; it never stops the rest.
export async function runBatch(items, run, limit = batchConcurrency) {
  const all = list(items);
  const results = new Array(all.length);
  let cursor = 0;
  const worker = async () => {
    while (cursor < all.length) {
      const index = cursor++;
      try {
        const result = await run(all[index]);
        results[index] = { item: all[index], ok: result?.ok === true, message: result?.message ?? "" };
      } catch (error) {
        results[index] = { item: all[index], ok: false, message: error instanceof Error ? error.message : String(error) };
      }
    }
  };
  await Promise.all(Array.from({ length: Math.max(1, Math.min(limit, all.length)) }, worker));
  return results;
}

// batchSummary words a finished batch for its one toast.
export function batchSummary(verb, noun, results, skipped = 0) {
  const done = list(results).filter(result => result.ok).length;
  const failed = list(results).length - done;
  const parts = [`${verb}: ${done} of ${list(results).length} ${noun} done`];
  if (failed) parts.push(`${failed} failed`);
  if (skipped) parts.push(`${skipped} skipped`);
  return { tone: failed ? "error" : "success", message: `${parts.join(", ")}.` };
}

// purgePhrase is what a batch purge asks the user to type: the action and the
// count, in place of one target's full scope.
export function purgePhrase(count, noun) {
  return `purge ${count} ${noun}`;
}

// counted names a count of rows: 1 cell, 2 cells.
export function counted(count, singular, plural = `${singular}s`) {
  return `${count} ${count === 1 ? singular : plural}`;
}
