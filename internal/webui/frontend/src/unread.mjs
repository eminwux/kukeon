// Unread terminal output (#1696): the read markers this browser keeps per cell, compared with the
// terminal counters GET /api/terminal-activity reports. A marker is the terminal run (startedAt) and
// how many of its output bytes the operator has seen. A cell is marked once its terminal produced
// bytes past the marker and has then been quiet for unreadQuietMs.
// No DOM, no network — unit-tested with node --test.
import { formatBytes } from "./cgroup-format.js";

// How long a terminal must have produced no output before its unread output marks the cell. An agent
// TUI redraws while it works, so marking on any new byte would light every busy cell; 0 does that.
export const unreadQuietMs = 3000;

// The localStorage key of the markers: { [cellKey]: { startedAt, bytes } }, cellKey JSON.stringify(scope).
export const markersKey = "kukeon-terminal-seen";

const isMarker = value => !!value && typeof value === "object" && typeof value.startedAt === "string" &&
  Number.isFinite(value.bytes) && value.bytes >= 0;

// parseMarkers reads the stored value; a missing or unreadable value, or entry, reads as empty.
export function parseMarkers(raw) {
  if (typeof raw !== "string") return {};
  let value;
  try { value = JSON.parse(raw); } catch { return {}; }
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const markers = {};
  for (const [key, marker] of Object.entries(value)) {
    if (isMarker(marker)) markers[key] = { startedAt: marker.startedAt, bytes: marker.bytes };
  }
  return markers;
}

// parseActivity turns a response body into readings by cell key: { startedAt, bytesOut, idleMs } for a
// terminal that answered, { unavailable } for any other cell. A cell entry it cannot read is left out.
export function parseActivity(body) {
  const readings = {};
  const cells = body && typeof body === "object" && Array.isArray(body.cells) ? body.cells : [];
  for (const cell of cells) {
    if (!cell || !Array.isArray(cell.scope) || cell.scope.length !== 4 || !cell.scope.every(part => typeof part === "string")) continue;
    const key = JSON.stringify(cell.scope);
    if (typeof cell.unavailable === "string") readings[key] = { unavailable: cell.unavailable };
    else if (typeof cell.startedAt === "string" && Number.isFinite(cell.bytesOut) && cell.bytesOut >= 0 && Number.isFinite(cell.idleMs)) {
      readings[key] = { startedAt: cell.startedAt, bytesOut: cell.bytesOut, idleMs: cell.idleMs };
    }
  }
  return readings;
}

const live = reading => !!reading && !reading.unavailable;

// nextMarker is the marker to keep after a reading. A cell being read has seen everything. A cell this
// browser has never observed starts as read, so the first load marks nothing. A new startedAt means
// the terminal restarted: every byte of the new run is unread. An unavailable reading keeps the marker.
export function nextMarker(stored, reading, isBeingRead) {
  if (!live(reading)) return stored;
  if (isBeingRead || !stored) return { startedAt: reading.startedAt, bytes: reading.bytesOut };
  if (stored.startedAt === reading.startedAt) return stored;
  return { startedAt: reading.startedAt, bytes: 0 };
}

// unreadBytes counts the output bytes of the reading's terminal run past the marker.
export function unreadBytes(stored, reading) {
  if (!live(reading) || !stored) return 0;
  if (stored.startedAt !== reading.startedAt) return reading.bytesOut;
  return Math.max(0, reading.bytesOut - stored.bytes);
}

// isMarked: unread output, and the terminal has been quiet for quietMs.
export function isMarked(unread, idleMs, quietMs = unreadQuietMs) {
  return unread > 0 && idleMs >= quietMs;
}

// pruneMarkers keeps the markers of the cells in cellKeys (the inventory).
export function pruneMarkers(markers, cellKeys) {
  const keep = new Set(cellKeys);
  const next = {};
  for (const [key, marker] of Object.entries(markers)) if (keep.has(key)) next[key] = marker;
  return next;
}

// applyReadings returns the markers after readings, and the keys whose marker changed. beingRead holds
// the keys of the cells whose output the operator is looking at.
export function applyReadings(markers, readings, beingRead) {
  const next = { ...markers };
  const changed = [];
  for (const [key, reading] of Object.entries(readings)) {
    const marker = nextMarker(markers[key], reading, beingRead.has(key));
    if (!marker || (markers[key] && marker.startedAt === markers[key].startedAt && marker.bytes === markers[key].bytes)) continue;
    next[key] = marker;
    changed.push(key);
  }
  return { markers: next, changed };
}

// unreadState is what each surface shows per cell: the unread bytes and whether the cell is marked. A
// cell being read shows nothing, whatever its last reading said.
export function unreadState(markers, readings, beingRead, quietMs = unreadQuietMs) {
  const state = {};
  for (const [key, reading] of Object.entries(readings)) {
    if (!live(reading) || beingRead.has(key)) continue;
    const bytes = unreadBytes(nextMarker(markers[key], reading, false), reading);
    const marked = isMarked(bytes, reading.idleMs, quietMs);
    if (marked) state[key] = { unreadBytes: bytes, marked };
  }
  return state;
}

// unreadLabel is the accessible-name suffix of a marked cell, so every surface says the same: the
// amount only with Show unread bytes on. An unmarked cell (no unread bytes) has none.
export function unreadLabel(bytes, showBytes) {
  if (!(bytes > 0)) return "";
  return showBytes ? `, ${formatBytes(bytes)} unread output` : ", unread output";
}

// unreadAmount is the visible count of a marked cell with Show unread bytes on.
export function unreadAmount(bytes) {
  return formatBytes(bytes);
}
