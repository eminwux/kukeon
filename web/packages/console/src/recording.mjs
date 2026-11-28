// Asciicast v2 recordings for the cell terminal's Replay view (issue #1783). Pure: no DOM, no
// network. recording-source.ts feeds it the lines of every retained segment, oldest first, and
// replay.tsx plays it.
//
// sbsh's timing rule: an event's wall-clock time is the timestamp of the nearest preceding header
// plus the event's time. Every segment starts with a header; a header whose timestamp differs from
// the current run's starts a new run (the terminal restarted), one with the same timestamp continues
// it and carries the size in effect.

// An idle gap longer than this plays as this long when idle skipping is on (seconds).
export const IDLE_LIMIT = 2;
// The gap between two runs always plays as this long (seconds): their wall-clock gap is downtime.
export const RUN_PAUSE = 1;

const NEWLINE = 10;

// Splits a byte stream into lines across chunk boundaries. A line is returned once its newline has
// arrived; the bytes after the last newline wait for the next push.
export function createLineSplitter() {
  const decoder = new TextDecoder();
  let pending = [];
  let pendingLength = 0;
  return {
    push(bytes) {
      const lines = [];
      let start = 0;
      for (let i = bytes.indexOf(NEWLINE); i !== -1; i = bytes.indexOf(NEWLINE, start)) {
        let line = bytes.subarray(start, i);
        if (pendingLength) {
          const joined = new Uint8Array(pendingLength + line.length);
          let at = 0;
          for (const piece of pending) { joined.set(piece, at); at += piece.length; }
          joined.set(line, at);
          line = joined;
          pending = [];
          pendingLength = 0;
        }
        lines.push(decoder.decode(line));
        start = i + 1;
      }
      if (start < bytes.length) {
        pending.push(bytes.slice(start));
        pendingLength += bytes.length - start;
      }
      return lines;
    },
    get pending() { return pendingLength; },
  };
}

// One line of a recording: { header } or { event }, or null for a blank or malformed line.
export function parseLine(line) {
  const text = line.trim();
  if (!text) return null;
  try {
    if (text[0] === "{") {
      const header = JSON.parse(text);
      if (header?.version !== 2) return null;
      return { header: { width: positive(header.width, 80), height: positive(header.height, 24), timestamp: Number(header.timestamp) || 0 } };
    }
    if (text[0] === "[") {
      const [time, code, data] = JSON.parse(text);
      if (typeof time !== "number" || !Number.isFinite(time) || typeof code !== "string" || typeof data !== "string") return null;
      return { event: { time, code, data } };
    }
  } catch { /* Malformed: skipped, as sbsh's decoder does. */ }
  return null;
}

const positive = (value, fallback) => Number.isInteger(value) && value > 0 ? value : fallback;

// "<cols>x<rows>" of a resize event, or null.
export function parseSize(data) {
  const match = /^(\d+)x(\d+)$/.exec(data);
  if (!match) return null;
  const cols = Number(match[1]), rows = Number(match[2]);
  return cols > 0 && rows > 0 ? { cols, rows } : null;
}

// An empty recording. ops are the playable steps in order: { kind: "o", data } output or
// { kind: "size", cols, rows } a terminal size, each with its run and its time t in the run.
// play[mode][i] is op i's position on the playback clock, with ("idle") and without ("full") idle
// skipping.
export function createRecording() {
  return { runs: [], ops: [], play: { idle: [], full: [] }, cols: 0, rows: 0, lastT: 0 };
}

function pushOp(recording, op) {
  const { ops, play } = recording;
  const previous = ops.at(-1);
  if (!previous) { play.idle.push(0); play.full.push(0); }
  else if (previous.run !== op.run) {
    play.idle.push(play.idle.at(-1) + RUN_PAUSE);
    play.full.push(play.full.at(-1) + RUN_PAUSE);
  } else {
    const gap = Math.max(0, op.t - previous.t);
    play.idle.push(play.idle.at(-1) + Math.min(gap, IDLE_LIMIT));
    play.full.push(play.full.at(-1) + gap);
  }
  ops.push(op);
}

function setSize(recording, cols, rows, t) {
  if (cols === recording.cols && rows === recording.rows) return;
  recording.cols = cols;
  recording.rows = rows;
  pushOp(recording, { kind: "size", run: recording.runs.length - 1, t, cols, rows });
}

// Adds one parsed line (parseLine's result) to the recording.
export function addRecord(recording, record) {
  if (!record) return;
  const run = recording.runs.at(-1);
  if (record.header) {
    const { width, height, timestamp } = record.header;
    if (!run || run.timestamp !== timestamp) {
      recording.runs.push({ timestamp, firstOp: recording.ops.length });
      recording.lastT = 0;
      // A new run always gets a size op, so it has a first step and a mark on the timeline.
      recording.cols = 0;
      setSize(recording, width, height, 0);
    } else {
      setSize(recording, width, height, recording.lastT);
    }
    return;
  }
  const { event } = record;
  if (!run) {
    // Output before any header: sbsh writes a header first, so this is a damaged recording.
    recording.runs.push({ timestamp: 0, firstOp: recording.ops.length });
    recording.cols = 0;
    setSize(recording, 80, 24, 0);
  }
  // Times within a run never decrease; a damaged line that does is clamped.
  const t = Math.max(event.time, recording.lastT);
  recording.lastT = t;
  if (event.code === "o") {
    pushOp(recording, { kind: "o", run: recording.runs.length - 1, t, data: event.data });
  } else if (event.code === "r") {
    const size = parseSize(event.data);
    if (size) setSize(recording, size.cols, size.rows, t);
  }
}

export function addLines(recording, lines) {
  for (const line of lines) addRecord(recording, parseLine(line));
}

const mode = idleSkip => idleSkip ? "idle" : "full";

// The length of the recording on the playback clock.
export function duration(recording, idleSkip) {
  return recording.play[mode(idleSkip)].at(-1) ?? 0;
}

// How many ops are due at playback position pos: those at or before it.
export function opsDue(recording, idleSkip, pos) {
  const play = recording.play[mode(idleSkip)];
  let low = 0, high = play.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (play[mid] <= pos) low = mid + 1; else high = mid;
  }
  return low;
}

// The playback position of op index i (the position of the last op written when i ops are).
export function positionOf(recording, idleSkip, count) {
  return count > 0 ? recording.play[mode(idleSkip)][count - 1] : 0;
}

// The terminal size once count ops have been written.
export function sizeAt(recording, count) {
  for (let i = Math.min(count, recording.ops.length) - 1; i >= 0; i--) {
    const op = recording.ops[i];
    if (op.kind === "size") return { cols: op.cols, rows: op.rows };
  }
  const first = recording.ops.find(op => op.kind === "size");
  return first ? { cols: first.cols, rows: first.rows } : { cols: 80, rows: 24 };
}

// The wall-clock time (unix seconds) at playback position pos, interpolated between the ops around it.
export function wallClockAt(recording, idleSkip, pos) {
  const { ops, runs } = recording;
  if (!ops.length) return null;
  const play = recording.play[mode(idleSkip)];
  const due = Math.max(1, opsDue(recording, idleSkip, pos));
  const op = ops[due - 1];
  let t = op.t;
  const next = ops[due];
  if (next && next.run === op.run && play[due] > play[due - 1]) {
    t += (next.t - op.t) * Math.min(1, Math.max(0, (pos - play[due - 1]) / (play[due] - play[due - 1])));
  }
  return runs[op.run].timestamp + t;
}

// Where each run starts on the playback clock, with its wall-clock start time.
export function runMarks(recording, idleSkip) {
  const play = recording.play[mode(idleSkip)];
  return recording.runs.map((run, index) => ({ index, position: play[run.firstOp] ?? 0, timestamp: run.timestamp }));
}

// The writes that take the terminal from from to to ops: output runs joined into one string, each
// size change apart, in order. maxChars splits long output so a caller can bound one write.
export function writesBetween(recording, from, to, maxChars = Infinity) {
  const writes = [];
  let data = "";
  for (let i = from; i < to; i++) {
    const op = recording.ops[i];
    if (op.kind === "size") {
      if (data) { writes.push({ data }); data = ""; }
      writes.push({ cols: op.cols, rows: op.rows });
      continue;
    }
    data += op.data;
    if (data.length >= maxChars) { writes.push({ data }); data = ""; }
  }
  if (data) writes.push({ data });
  return writes;
}

const roundTime = seconds => Math.round(seconds * 1e6) / 1e6;

// The recording as one asciicast v2 file: a single header (the first run's size and start time), then
// every retained output and size change as "o" and "r" events, timed by the playback clock without
// idle skipping so times never decrease across runs.
export function exportCast(recording) {
  const first = recording.ops.find(op => op.kind === "size") ?? { cols: 80, rows: 24 };
  const header = { version: 2, width: first.cols, height: first.rows };
  if (recording.runs[0]?.timestamp) header.timestamp = recording.runs[0].timestamp;
  const lines = [JSON.stringify(header)];
  let cols = first.cols, rows = first.rows;
  recording.ops.forEach((op, i) => {
    const t = roundTime(recording.play.full[i]);
    if (op.kind === "o") { lines.push(JSON.stringify([t, "o", op.data])); return; }
    if (op.cols === cols && op.rows === rows) return;
    cols = op.cols; rows = op.rows;
    lines.push(JSON.stringify([t, "r", `${cols}x${rows}`]));
  });
  return lines.join("\n") + "\n";
}
