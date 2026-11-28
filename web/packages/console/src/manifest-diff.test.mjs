import assert from "node:assert/strict";
import test from "node:test";
import { compact, diffLines, expandFold, hasChanges, restartOutcome } from "./manifest-diff.mjs";

const doc = items => items.join("\n") + "\n";
const kinds = rows => rows.map(row => row.kind);

test("diffLines of identical documents is all same, with line numbers", () => {
  const rows = diffLines(doc(["a", "b", "c"]), doc(["a", "b", "c"]));
  assert.deepEqual(kinds(rows), ["same", "same", "same"]);
  assert.deepEqual(rows.map(row => [row.leftNo, row.rightNo]), [[1, 1], [2, 2], [3, 3]]);
  assert.equal(hasChanges(rows), false);
});

test("diffLines pairs a removed run with the following added run as changed", () => {
  const rows = diffLines(doc(["a", "x1", "x2", "b"]), doc(["a", "y1", "y2", "y3", "b"]));
  assert.deepEqual(kinds(rows), ["same", "changed", "changed", "added", "same"]);
  assert.deepEqual(rows[1], { kind: "changed", left: "x1", leftNo: 2, right: "y1", rightNo: 2 });
  assert.deepEqual(rows[3], { kind: "added", right: "y3", rightNo: 4 });
  assert.deepEqual([rows[4].leftNo, rows[4].rightNo], [4, 5]);
  assert.equal(hasChanges(rows), true);
});

test("diffLines reports lines on one side only as removed or added", () => {
  assert.deepEqual(kinds(diffLines(doc(["a", "gone", "b"]), doc(["a", "b"]))), ["same", "removed", "same"]);
  assert.deepEqual(kinds(diffLines(doc(["a", "b"]), doc(["a", "new", "b"]))), ["same", "added", "same"]);
  assert.deepEqual(kinds(diffLines("", doc(["a"]))), ["added"]);
  assert.deepEqual(kinds(diffLines(doc(["a"]), "")), ["removed"]);
  assert.deepEqual(diffLines("", ""), []);
});

test("diffLines keeps the common lines of a reordered env list", () => {
  const rows = diffLines(doc(["env:", "- A=+", "- B=+"]), doc(["env:", "- B=+", "- A=+"]));
  assert.equal(rows.filter(row => row.kind === "same").length, 2);
});

test("compact keeps three lines of context and folds the rest", () => {
  const left = Array.from({ length: 20 }, (_, i) => `line ${i}`);
  const right = [...left];
  right[10] = "changed";
  const rows = compact(diffLines(doc(left), doc(right)));
  assert.deepEqual(kinds(rows), ["fold", "same", "same", "same", "changed", "same", "same", "same", "fold"]);
  assert.equal(rows[0].count, 7);
  assert.equal(rows[8].count, 6);
  assert.deepEqual(rows[1].left, "line 7");
  assert.equal(rows[0].rows.length, 7);
});

test("compact folds between two distant changes and leaves short gaps alone", () => {
  const left = Array.from({ length: 30 }, (_, i) => `l${i}`);
  const right = [...left];
  right[2] = "x";
  right[20] = "y";
  const rows = compact(diffLines(doc(left), doc(right)), 3);
  const folds = rows.filter(row => row.kind === "fold");
  assert.deepEqual(folds.map(fold => fold.count), [11, 6]);
  const near = [...left];
  near[2] = "x";
  near[8] = "y";
  assert.equal(compact(diffLines(doc(left), doc(near)), 3).filter(row => row.kind === "fold").length, 1);
});

test("compact does not fold a single hidden line", () => {
  const left = Array.from({ length: 8 }, (_, i) => `l${i}`);
  const right = [...left];
  right[0] = "x";
  right[7] = "y";
  assert.ok(compact(diffLines(doc(left), doc(right))).every(row => row.kind !== "fold"));
});

test("compact folds an unchanged document into one row", () => {
  const rows = compact(diffLines(doc(["a", "b", "c"]), doc(["a", "b", "c"])));
  assert.deepEqual(rows.map(row => [row.kind, row.count]), [["fold", 3]]);
});

test("expandFold puts the hidden lines back", () => {
  const left = Array.from({ length: 20 }, (_, i) => `line ${i}`);
  const right = [...left];
  right[10] = "changed";
  const full = diffLines(doc(left), doc(right));
  let rows = compact(full);
  rows = expandFold(rows, rows.length - 1);
  rows = expandFold(rows, 0);
  assert.deepEqual(rows, full);
  assert.equal(expandFold(rows, 3), rows);
});

test("restartOutcome names what a restart does", () => {
  assert.equal(restartOutcome("breaking"), "Restart will recreate the cell");
  assert.equal(restartOutcome("compatible"), "Restart will update in place");
  assert.equal(restartOutcome("additive"), "Restart will update in place");
  assert.match(restartOutcome("none"), /spec does not change/);
  assert.equal(restartOutcome(undefined), "");
});
