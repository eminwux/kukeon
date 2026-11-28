import assert from "node:assert/strict";
import test from "node:test";
import { batchConcurrency, batchSummary, batchTargets, counted, headerState, offeredActions, pruneSelection, purgePhrase, runBatch, systemSkip, toggleAll, toggleKey } from "./bulk.mjs";

test("a row is selected and cleared by its key", () => {
  let selected = toggleKey([], "a", true);
  selected = toggleKey(selected, "b", true);
  selected = toggleKey(selected, "a", true);
  assert.deepEqual(selected, ["b", "a"]);
  assert.deepEqual(toggleKey(selected, "b", false), ["a"]);
});

test("rows that left the list are pruned; the others stay selected whatever the search", () => {
  const selected = ["a", "b", "c"];
  assert.deepEqual(pruneSelection(selected, ["a", "c", "d"]), ["a", "c"]);
  assert.equal(pruneSelection(selected, ["a", "b", "c", "d"]), selected, "an unchanged selection is the same array");
});

test("the header checkbox covers every matching row, on every page", () => {
  const matching = Array.from({ length: 60 }, (_, i) => `row-${i}`);
  assert.equal(headerState([], matching), "none");
  assert.equal(headerState(["row-3"], matching), "some");
  const all = toggleAll([], matching);
  assert.equal(all.length, 60, "not only the 25 on the page");
  assert.equal(headerState(all, matching), "all");
  // A row selected outside the filter is kept by both directions.
  const withOther = toggleAll(["other"], matching);
  assert.equal(withOther.length, 61);
  assert.deepEqual(toggleAll(withOther, matching), ["other"]);
  assert.equal(headerState(["other"], []), "none");
});

test("an action's targets skip the rows it does not apply to and every kuke-system row", () => {
  const rows = [
    { scope: ["dev", "s", "k", "a"], state: "Ready" },
    { scope: ["dev", "s", "k", "b"], state: "Stopped" },
    { scope: ["kuke-system", "kukeon", "kukeon", "kukeond"], state: "Ready" },
  ];
  const reason = (row, action) => action === "stop" && row.state !== "Ready" ? "not running" : "";
  const stop = batchTargets(rows, row => reason(row, "stop"));
  assert.deepEqual(stop.targets.map(row => row.scope[3]), ["a"]);
  assert.deepEqual(stop.skipped.map(item => [item.row.scope[3], item.reason]), [["b", "not running"], ["kukeond", systemSkip]]);
  const remove = batchTargets(rows, () => "");
  assert.deepEqual(remove.targets.map(row => row.scope[3]), ["a", "b"]);
  // The bar offers an action that applies to at least one selected row.
  assert.deepEqual(offeredActions(rows.slice(1), ["stop", "delete"], reason), ["delete"]);
  assert.deepEqual(offeredActions(rows.slice(2), ["stop", "delete"], reason), [], "a kuke-system row alone offers nothing");
});

test("a batch runs a few rows at a time, keeps going after a failure and reports each row in order", async () => {
  let running = 0, peak = 0;
  const items = Array.from({ length: 10 }, (_, i) => i);
  const results = await runBatch(items, async item => {
    running++; peak = Math.max(peak, running);
    await new Promise(resolve => setTimeout(resolve, 5 + (item % 3)));
    running--;
    if (item === 2) throw new Error("boom");
    return { ok: item !== 5, message: item === 5 ? "refused" : "" };
  });
  assert.equal(peak, batchConcurrency);
  assert.deepEqual(results.map(result => result.item), items);
  assert.deepEqual(results.filter(result => !result.ok).map(result => [result.item, result.message]), [[2, "boom"], [5, "refused"]]);
  assert.deepEqual(await runBatch([], async () => ({ ok: true })), []);
});

test("a batch is summed up in one toast, and a purge asks for the action and the count", () => {
  assert.deepEqual(batchSummary("Delete", "cells", [{ ok: true }, { ok: true }]), { tone: "success", message: "Delete: 2 of 2 cells done." });
  assert.deepEqual(batchSummary("Delete", "cells", [{ ok: true }, { ok: false }], 3), { tone: "error", message: "Delete: 1 of 2 cells done, 1 failed, 3 skipped." });
  assert.equal(purgePhrase(12, "cells"), "purge 12 cells");
  assert.equal(counted(1, "cell"), "1 cell");
  assert.equal(counted(2, "cell"), "2 cells");
  assert.equal(counted(2, "identity", "identities"), "2 identities");
});
