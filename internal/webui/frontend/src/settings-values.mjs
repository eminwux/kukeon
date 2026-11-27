// Pure parsers of the per-browser settings (settings.ts, #1745) that do more
// than read a fixed word: the inventory refresh interval and the Get started
// switch, which also reads the three card modes it replaced. No DOM, no
// storage — unit-tested with node --test.

// The inventory refresh intervals the Profile offers, in seconds.
export const refreshIntervals = [5, 10, 30, 60];
export const defaultRefreshSeconds = 10;

// parseRefreshSeconds reads a stored interval: only an offered value counts.
export function parseRefreshSeconds(raw) {
  const text = typeof raw === "string" ? raw.trim() : "";
  if (!/^\d+$/.test(text)) return undefined;
  const value = Number(text);
  return refreshIntervals.includes(value) ? value : undefined;
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
