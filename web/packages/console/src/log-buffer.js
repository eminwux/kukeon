// Browser-side retention for daemon log chunks (issue #1386, step 8). Lines are
// kept verbatim from /api/daemon/logs: nothing here parses or invents
// timestamps or levels. Retention is bounded by line count and characters.
export const MAX_LINES = 2000;
export const MAX_CHARS = 1_000_000;

export function emptyLogBuffer() {
  return { lines: [], open: false, omitted: false, next: null, size: 0, source: "" };
}

// applyChunk folds one chunk into the buffer. A tail read (replace) or a
// daemon-reported cursor reset starts over; a forward read appends. A chunk
// that ends without a newline (a piece of an over-long line) leaves the last
// line open so the next chunk continues it.
export function applyChunk(buffer, chunk, { replace = false, maxLines = MAX_LINES, maxChars = MAX_CHARS } = {}) {
  const restart = replace || chunk.reset;
  const lines = restart ? [] : [...buffer.lines];
  let open = restart ? false : buffer.open;
  let omitted = restart ? !!chunk.truncated : buffer.omitted;
  const data = chunk.data ?? "";
  if (data) {
    const parts = data.split("\n");
    const endsOpen = !data.endsWith("\n");
    if (!endsOpen) parts.pop();
    if (open && lines.length && parts.length) lines[lines.length - 1] += parts.shift();
    lines.push(...parts);
    open = endsOpen;
  }
  let drop = Math.max(0, lines.length - maxLines);
  let chars = 0;
  for (let i = lines.length - 1; i >= drop; i--) {
    chars += lines[i].length + 1;
    if (chars > maxChars && i < lines.length - 1) { drop = i + 1; break; }
  }
  if (drop > 0) { lines.splice(0, drop); omitted = true; }
  return { lines, open, omitted, next: chunk.next ?? buffer.next, size: chunk.size ?? buffer.size, source: chunk.source || buffer.source };
}

// healthLabel names a /api/daemon/health status for display.
export function healthLabel(status) {
  return status === "reachable" ? "Reachable"
    : status === "permission_denied" ? "Permission denied"
    : status === "unavailable" ? "Unavailable"
    : status === "daemon_outdated" ? "Daemon outdated"
    : status === "failed" ? "Check failed" : "Checking…";
}
export function healthTone(status) {
  return status === "reachable" ? "ok" : status === "daemon_outdated" ? "warning" : status ? "error" : "pending";
}
