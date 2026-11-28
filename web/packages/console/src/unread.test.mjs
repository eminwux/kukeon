import assert from "node:assert/strict";
import test from "node:test";
import { applyReadings, isMarked, nextMarker, parseActivity, parseMarkers, pruneMarkers, unreadBytes, unreadLabel, unreadQuietMs, unreadState } from "./unread.mjs";

const run1 = "2026-11-26T10:00:00Z";
const run2 = "2026-11-26T11:00:00Z";
const reading = (bytesOut, idleMs = 5000, startedAt = run1) => ({ startedAt, bytesOut, idleMs });
const key = cell => JSON.stringify(["default", "default", "default", cell]);
const none = new Set();

test("the quiet period is three seconds", () => {
  assert.equal(unreadQuietMs, 3000);
});

test("no stored marker: the reading becomes the marker and nothing is unread", () => {
  assert.deepEqual(nextMarker(undefined, reading(500), false), { startedAt: run1, bytes: 500 });
  assert.equal(unreadBytes(undefined, reading(500)), 0);
  const { markers, changed } = applyReadings({}, { [key("a")]: reading(500) }, none);
  assert.deepEqual(markers, { [key("a")]: { startedAt: run1, bytes: 500 } });
  assert.deepEqual(changed, [key("a")]);
  assert.deepEqual(unreadState({}, { [key("a")]: reading(500) }, none), {});
});

test("same startedAt: unread is the bytes past the marker, never negative", () => {
  const stored = { startedAt: run1, bytes: 100 };
  assert.equal(unreadBytes(stored, reading(350)), 250);
  assert.equal(unreadBytes(stored, reading(100)), 0);
  assert.equal(unreadBytes(stored, reading(40)), 0);
  // The marker of a cell not being read does not move.
  assert.equal(nextMarker(stored, reading(350), false), stored);
  assert.deepEqual(applyReadings({ [key("a")]: stored }, { [key("a")]: reading(350) }, none).changed, []);
});

test("different startedAt: the terminal restarted and all output of the new run is unread", () => {
  const stored = { startedAt: run1, bytes: 9000 };
  assert.deepEqual(nextMarker(stored, reading(120, 5000, run2), false), { startedAt: run2, bytes: 0 });
  assert.equal(unreadBytes(stored, reading(120, 5000, run2)), 120);
  assert.deepEqual(unreadState({ [key("a")]: stored }, { [key("a")]: reading(120, 5000, run2) }, none), { [key("a")]: { unreadBytes: 120, marked: true } });
  // A restart into a run with no output yet is not unread.
  assert.deepEqual(unreadState({ [key("a")]: stored }, { [key("a")]: reading(0, 0, run2) }, none), {});
});

test("a cell being read moves its marker to every reading and is never marked", () => {
  const stored = { startedAt: run1, bytes: 100 };
  const being = new Set([key("a")]);
  assert.deepEqual(nextMarker(stored, reading(900), true), { startedAt: run1, bytes: 900 });
  assert.deepEqual(nextMarker(stored, reading(30, 5000, run2), true), { startedAt: run2, bytes: 30 });
  assert.deepEqual(unreadState({ [key("a")]: stored }, { [key("a")]: reading(900) }, being), {});
  const { markers, changed } = applyReadings({ [key("a")]: stored }, { [key("a")]: reading(900) }, being);
  assert.deepEqual(markers[key("a")], { startedAt: run1, bytes: 900 });
  assert.deepEqual(changed, [key("a")]);
});

test("an unavailable cell is not marked and keeps its marker", () => {
  const stored = { startedAt: run1, bytes: 100 };
  for (const unavailable of ["not_found", "not_live", "unsupported", "error"]) {
    assert.equal(nextMarker(stored, { unavailable }, false), stored);
    assert.equal(nextMarker(stored, { unavailable }, true), stored);
    assert.equal(nextMarker(undefined, { unavailable }, false), undefined);
    const { markers, changed } = applyReadings({ [key("a")]: stored }, { [key("a")]: { unavailable } }, none);
    assert.deepEqual(markers, { [key("a")]: stored });
    assert.deepEqual(changed, []);
    assert.deepEqual(unreadState({ [key("a")]: stored }, { [key("a")]: { unavailable } }, none), {});
  }
});

test("quiet rule: marked only once the terminal has been quiet for the quiet period", () => {
  assert.equal(isMarked(10, 2999), false);
  assert.equal(isMarked(10, 3000), true);
  assert.equal(isMarked(0, 60000), false);
  assert.equal(isMarked(10, 0, 0), true);
  const stored = { [key("a")]: { startedAt: run1, bytes: 0 } };
  assert.deepEqual(unreadState(stored, { [key("a")]: reading(64, 1200) }, none), {});
  assert.deepEqual(unreadState(stored, { [key("a")]: reading(64, 3400) }, none), { [key("a")]: { unreadBytes: 64, marked: true } });
  assert.deepEqual(unreadState(stored, { [key("a")]: reading(64, 1200) }, none, 0), { [key("a")]: { unreadBytes: 64, marked: true } });
});

test("pruneMarkers keeps only the cells of the inventory", () => {
  const markers = { [key("a")]: { startedAt: run1, bytes: 1 }, [key("gone")]: { startedAt: run1, bytes: 2 } };
  assert.deepEqual(pruneMarkers(markers, [key("a"), key("b")]), { [key("a")]: { startedAt: run1, bytes: 1 } });
  assert.deepEqual(pruneMarkers(markers, []), {});
});

test("parseMarkers reads unreadable storage as empty", () => {
  assert.deepEqual(parseMarkers(null), {});
  assert.deepEqual(parseMarkers(undefined), {});
  assert.deepEqual(parseMarkers(""), {});
  assert.deepEqual(parseMarkers("{not json"), {});
  assert.deepEqual(parseMarkers("[1,2]"), {});
  assert.deepEqual(parseMarkers("null"), {});
  assert.deepEqual(parseMarkers("42"), {});
  const raw = JSON.stringify({
    [key("a")]: { startedAt: run1, bytes: 7, extra: true },
    [key("b")]: { startedAt: run1, bytes: -1 },
    [key("c")]: { startedAt: 5, bytes: 1 },
    [key("d")]: "x",
  });
  assert.deepEqual(parseMarkers(raw), { [key("a")]: { startedAt: run1, bytes: 7 } });
});

test("parseActivity reads live and unavailable cells and skips malformed ones", () => {
  const scope = ["default", "default", "default", "a"];
  const readings = parseActivity({ cells: [
    { scope, container: "main", startedAt: run1, bytesOut: 48213, bytesIn: 311, idleMs: 12840 },
    { scope: ["default", "default", "default", "b"], unavailable: "not_live" },
    { scope: ["default", "default", "c"], startedAt: run1, bytesOut: 1, idleMs: 0 },
    { scope: ["default", "default", "default", "d"], startedAt: run1, bytesOut: "1", idleMs: 0 },
    null,
  ] });
  assert.deepEqual(readings, {
    [key("a")]: { startedAt: run1, bytesOut: 48213, idleMs: 12840 },
    [key("b")]: { unavailable: "not_live" },
  });
  assert.deepEqual(parseActivity(null), {});
  assert.deepEqual(parseActivity({ cells: "x" }), {});
});

test("unreadLabel is the accessible-name suffix, with the amount only when asked", () => {
  assert.equal(unreadLabel(1229, false), ", unread output");
  assert.equal(unreadLabel(1229, true), ", 1.2 KiB unread output");
  assert.equal(unreadLabel(12, true), ", 12 B unread output");
  assert.equal(unreadLabel(0, true), "");
  assert.equal(unreadLabel(0, false), "");
});
