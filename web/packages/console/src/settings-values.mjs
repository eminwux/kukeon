// Pure parsers of the per-browser settings (settings.ts, #1745) that do more
// than read a fixed word: the Workbench timers (#1770) and the Get started
// switch, which also reads the three card modes it replaced. No DOM, no
// storage — unit-tested with node --test.

// parseOffered reads a stored whole number of seconds: only a value in offered counts.
function parseOffered(raw, offered) {
  const text = typeof raw === "string" ? raw.trim() : "";
  if (!/^\d+$/.test(text)) return undefined;
  const value = Number(text);
  return offered.includes(value) ? value : undefined;
}

// The inventory refresh intervals the Profile offers, in seconds.
export const refreshIntervals = [5, 10, 30, 60];
export const defaultRefreshSeconds = 10;

// parseRefreshSeconds reads a stored interval: only an offered value counts.
export function parseRefreshSeconds(raw) {
  return parseOffered(raw, refreshIntervals);
}

// How often GET /api/terminal-activity is read for the unread marks, in seconds (#1770).
export const unreadPollIntervals = [1, 2, 5, 10, 30];
export const defaultUnreadPollSeconds = 2;

export function parseUnreadPollSeconds(raw) {
  return parseOffered(raw, unreadPollIntervals);
}

// How often the grid tiles' terminal thumbnails are read, in seconds (#1770).
export const gridScreenIntervals = [2, 5, 10, 30];
export const defaultGridScreenSeconds = 5;

export function parseGridScreenSeconds(raw) {
  return parseOffered(raw, gridScreenIntervals);
}

// How long a terminal must be quiet before its new output marks the cell, in seconds (#1770).
// 0 is Off: the first new byte marks the cell.
export const unreadQuietOptions = [0, 1, 3, 5, 10];
export const defaultUnreadQuietSeconds = 0;

export function parseUnreadQuietSeconds(raw) {
  return parseOffered(raw, unreadQuietOptions);
}

// parseShowGetStarted reads the Get started switch. The key once held a card
// mode (#1694): `hidden` reads as Off, `auto` and `shown` as On.
export function parseShowGetStarted(raw) {
  if (raw === "true" || raw === "auto" || raw === "shown") return true;
  if (raw === "false" || raw === "hidden") return false;
  return undefined;
}

// parseSwitch reads an On/Off setting.
export function parseSwitch(raw) {
  return raw === "true" ? true : raw === "false" ? false : undefined;
}
