import assert from "node:assert/strict";
import test from "node:test";
import {
  IDLE_LIMIT, RUN_PAUSE, addLines, createLineSplitter, createRecording, duration, exportCast, opsDue,
  parseLine, parseSize, positionOf, runMarks, sizeAt, wallClockAt, writesBetween,
} from "./recording.mjs";

const encoder = new TextEncoder();
const header = (timestamp, width = 80, height = 24) => JSON.stringify({ version: 2, width, height, timestamp });
const event = (time, code, data) => JSON.stringify([time, code, data]);

// Two runs: the first resizes and idles for a minute, the second starts an hour later. The second
// segment of the first run repeats its header with the size in effect.
const twoRuns = [
  header(1700000000),
  event(0.5, "o", "$ "),
  event(1, "r", "100x30"),
  event(1.5, "o", "ls\r\n"),
  header(1700000000, 100, 30),
  event(61.5, "o", "after idle ─\r\n"),
  header(1700003600, 90, 20),
  event(0.25, "o", "second run\r\n"),
];

function recordingOf(lines) {
  const recording = createRecording();
  addLines(recording, lines);
  return recording;
}

test("lines are split across chunk boundaries, multi-byte characters included", () => {
  const bytes = encoder.encode(`${event(0.1, "o", "é─")}\n${event(0.2, "o", "x")}\npartial`);
  for (const size of [1, 2, 3, 7, 1000]) {
    const splitter = createLineSplitter();
    const lines = [];
    for (let i = 0; i < bytes.length; i += size) lines.push(...splitter.push(bytes.subarray(i, i + size)));
    assert.deepEqual(lines, [event(0.1, "o", "é─"), event(0.2, "o", "x")], `chunks of ${size}`);
    assert.equal(splitter.pending, "partial".length);
  }
});

test("headers and events parse; malformed lines are skipped", () => {
  assert.deepEqual(parseLine(header(5, 90, 20)), { header: { width: 90, height: 20, timestamp: 5 } });
  assert.deepEqual(parseLine(event(1.5, "o", "hi")), { event: { time: 1.5, code: "o", data: "hi" } });
  for (const line of ["", "  ", "{", "[1,2]", '[1,"o",3]', '{"version":1}', "plain text", '["x","o","y"]']) {
    assert.equal(parseLine(line), null, line);
  }
  assert.deepEqual(parseSize("120x40"), { cols: 120, rows: 40 });
  assert.equal(parseSize("0x40"), null);
  assert.equal(parseSize("wide"), null);
});

test("runs, sizes and wall-clock time follow the headers and resize events", () => {
  const recording = recordingOf(twoRuns);
  assert.equal(recording.runs.length, 2, "the repeated header continues the first run");
  assert.deepEqual(recording.runs.map(run => run.timestamp), [1700000000, 1700003600]);
  // The size at each step: launch size, the resize, then the second run's size.
  const sizes = recording.ops.map((_, i) => sizeAt(recording, i + 1));
  assert.deepEqual(sizes.at(0), { cols: 80, rows: 24 });
  assert.deepEqual(sizeAt(recording, opsDue(recording, false, 1.5)), { cols: 100, rows: 30 });
  assert.deepEqual(sizes.at(-1), { cols: 90, rows: 20 });
  // Wall clock: run timestamp plus event time.
  const atLs = positionOf(recording, false, recording.ops.findIndex(op => op.data === "ls\r\n") + 1);
  assert.equal(wallClockAt(recording, false, atLs), 1700000001.5);
  const end = duration(recording, false);
  assert.equal(wallClockAt(recording, false, end), 1700003600.25);
  // Halfway through the idle minute the clock is interpolated.
  assert.ok(Math.abs(wallClockAt(recording, false, atLs + 30) - 1700000031.5) < 1e-9);
  const marks = runMarks(recording, false);
  assert.equal(marks.length, 2);
  assert.equal(marks[0].position, 0);
  assert.equal(marks[1].position, 61.5 + RUN_PAUSE, "the gap between runs plays as a short pause");
});

test("idle skipping shortens long gaps and keeps the order", () => {
  const recording = recordingOf(twoRuns);
  const full = duration(recording, false);
  const idle = duration(recording, true);
  assert.equal(full, 61.5 + RUN_PAUSE + 0.25);
  assert.equal(idle, 1.5 + IDLE_LIMIT + RUN_PAUSE + 0.25);
  for (const mode of [true, false]) {
    const play = recording.play[mode ? "idle" : "full"];
    for (let i = 1; i < play.length; i++) assert.ok(play[i] >= play[i - 1]);
  }
  // Every op is due at the end, none before the start.
  assert.equal(opsDue(recording, true, idle), recording.ops.length);
  assert.equal(opsDue(recording, true, -1), 0);
});

test("writes up to a position equal the output played from the start", () => {
  const recording = recordingOf(twoRuns);
  const due = opsDue(recording, false, 2);
  const writes = writesBetween(recording, 0, due);
  assert.deepEqual(writes, [{ cols: 80, rows: 24 }, { data: "$ " }, { cols: 100, rows: 30 }, { data: "ls\r\n" }]);
  // Played in two steps, the writes join to the same output.
  const split = opsDue(recording, false, 1.2);
  const joined = [...writesBetween(recording, 0, split), ...writesBetween(recording, split, recording.ops.length)];
  const all = writesBetween(recording, 0, recording.ops.length);
  assert.equal(joined.filter(w => w.data).map(w => w.data).join(""), all.filter(w => w.data).map(w => w.data).join(""));
  // maxChars bounds one write.
  const long = recordingOf([header(1), ...Array.from({ length: 10 }, (_, i) => event(i, "o", "0123456789"))]);
  assert.ok(writesBetween(long, 0, long.ops.length, 25).every(w => !w.data || w.data.length <= 30));
});

test("a recording fed while it rotates holds every event once", () => {
  // The live segment is read in two pieces, then re-read whole after rotation, skipping the lines
  // already held: the result equals reading it once.
  const segment = encoder.encode([header(10), event(1, "o", "a"), event(2, "o", "b"), event(3, "o", "c")].join("\n") + "\n");
  const once = recordingOf(createLineSplitter().push(segment));

  const followed = createRecording();
  const live = createLineSplitter();
  let held = 0;
  for (const piece of [segment.subarray(0, 50), segment.subarray(50, 70)]) {
    const lines = live.push(piece);
    held += lines.length;
    addLines(followed, lines);
  }
  const reread = createLineSplitter().push(segment).slice(held);
  addLines(followed, reread);
  assert.deepEqual(followed.ops, once.ops);
});

test("export is one asciicast file with a single header and non-decreasing times", () => {
  const recording = recordingOf(twoRuns);
  const lines = exportCast(recording).trimEnd().split("\n");
  const records = lines.map(parseLine);
  assert.deepEqual(records[0], { header: { width: 80, height: 24, timestamp: 1700000000 } });
  assert.equal(records.filter(r => r?.header).length, 1);
  const events = records.slice(1).map(r => r.event);
  assert.deepEqual(events.filter(e => e.code === "o").map(e => e.data), ["$ ", "ls\r\n", "after idle ─\r\n", "second run\r\n"]);
  assert.deepEqual(events.filter(e => e.code === "r").map(e => e.data), ["100x30", "90x20"]);
  for (let i = 1; i < events.length; i++) assert.ok(events[i].time >= events[i - 1].time);
  // Played back, the export holds the same output and sizes.
  const again = recordingOf(lines);
  assert.deepEqual(writesBetween(again, 0, again.ops.length), writesBetween(recording, 0, recording.ops.length));
});

test("an event before any header still plays at a default size", () => {
  const recording = recordingOf([event(0.1, "o", "x"), event(0.05, "o", "y")]);
  assert.deepEqual(sizeAt(recording, 1), { cols: 80, rows: 24 });
  assert.equal(recording.ops.at(-1).t, 0.1, "a time that goes backwards is clamped");
});
