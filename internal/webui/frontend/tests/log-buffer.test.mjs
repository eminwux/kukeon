import assert from "node:assert/strict";
import test from "node:test";
import { applyChunk, emptyLogBuffer, healthLabel, healthTone } from "../src/log-buffer.js";

test("a tail read replaces the buffer and records omitted earlier output", () => {
  const old = applyChunk(emptyLogBuffer(), { data: "stale\n", next: 6 });
  const tail = applyChunk(old, { data: "a\nb\n", start: 10, next: 14, size: 14, truncated: true, source: "/log" }, { replace: true });
  assert.deepEqual(tail.lines, ["a", "b"]);
  assert.equal(tail.omitted, true);
  assert.equal(tail.next, 14);
  assert.equal(tail.source, "/log");
});

test("forward reads append verbatim lines without parsing fields", () => {
  let buffer = applyChunk(emptyLogBuffer(), { data: "time=1 level=INFO msg=a\n", next: 24 }, { replace: true });
  buffer = applyChunk(buffer, { data: "plain text line\n", next: 40 });
  assert.deepEqual(buffer.lines, ["time=1 level=INFO msg=a", "plain text line"]);
  assert.equal(buffer.next, 40);
  assert.equal(buffer.omitted, false);
});

test("an empty forward read keeps the cursor and lines", () => {
  const buffer = applyChunk(emptyLogBuffer(), { data: "x\n", next: 2 }, { replace: true });
  const same = applyChunk(buffer, { data: "", next: 2 });
  assert.deepEqual(same.lines, ["x"]);
  assert.equal(same.next, 2);
});

test("pieces of an over-long line are joined across chunks", () => {
  let buffer = applyChunk(emptyLogBuffer(), { data: "abcd", next: 4 }, { replace: true });
  buffer = applyChunk(buffer, { data: "ef\nnext\n", next: 12 });
  assert.deepEqual(buffer.lines, ["abcdef", "next"]);
  assert.equal(buffer.open, false);
});

test("a daemon cursor reset starts over", () => {
  const buffer = applyChunk(emptyLogBuffer(), { data: "old\n", next: 4 }, { replace: true });
  const reset = applyChunk(buffer, { data: "new\n", next: 4, reset: true });
  assert.deepEqual(reset.lines, ["new"]);
});

test("retention is bounded by lines and characters", () => {
  const many = applyChunk(emptyLogBuffer(), { data: "1\n2\n3\n4\n5\n", next: 10 }, { replace: true, maxLines: 3 });
  assert.deepEqual(many.lines, ["3", "4", "5"]);
  assert.equal(many.omitted, true);
  const wide = applyChunk(emptyLogBuffer(), { data: "aaaa\nbbbb\ncccc\n", next: 15 }, { replace: true, maxChars: 11 });
  assert.deepEqual(wide.lines, ["bbbb", "cccc"]);
  // The newest line is always kept even when it alone exceeds the bound.
  const huge = applyChunk(emptyLogBuffer(), { data: "0123456789\n", next: 11 }, { replace: true, maxChars: 4 });
  assert.deepEqual(huge.lines, ["0123456789"]);
});

test("health statuses have distinct labels and tones", () => {
  assert.equal(healthLabel("reachable"), "Reachable");
  assert.equal(healthLabel("permission_denied"), "Permission denied");
  assert.equal(healthLabel("unavailable"), "Unavailable");
  assert.equal(healthLabel("failed"), "Check failed");
  assert.equal(healthLabel(undefined), "Checking…");
  assert.equal(healthTone("reachable"), "ok");
  assert.equal(healthTone("daemon_outdated"), "warning");
  assert.equal(healthTone("unavailable"), "error");
  assert.equal(healthTone(undefined), "pending");
});
