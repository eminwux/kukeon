// Formatting for the Administration Images view (issue #1406). The server
// sends size and createdAt as null when containerd cannot resolve them, so
// an unknown value renders as "Unavailable" — never as "0 B" or 1970.
export const UNAVAILABLE = "Unavailable";

// formatImageSize mirrors `kuke get image`'s binary-unit rendering.
export function formatImageSize(size) {
  if (typeof size !== "number" || !Number.isFinite(size) || size < 0) return UNAVAILABLE;
  if (size < 1024) return `${size} B`;
  let value = size / 1024;
  let unit = 0;
  while (value >= 1024 && unit < 5) { value /= 1024; unit++; }
  return `${value.toFixed(1)} ${"KMGTPE"[unit]}iB`;
}

export function formatImageCreated(createdAt) {
  if (!createdAt || createdAt.startsWith("0001-")) return UNAVAILABLE;
  const time = Date.parse(createdAt);
  return Number.isFinite(time) ? new Date(time).toISOString().replace(".000Z", "Z") : UNAVAILABLE;
}

export function formatImageText(value) {
  return value ? value : UNAVAILABLE;
}

// shortDigest keeps the algorithm prefix and the first 12 hex digits.
export function shortDigest(digest) {
  if (!digest) return UNAVAILABLE;
  const [algorithm, hex] = digest.includes(":") ? digest.split(":", 2) : ["", digest];
  const short = hex.length > 12 ? `${hex.slice(0, 12)}…` : hex;
  return algorithm ? `${algorithm}:${short}` : short;
}

// sortedLabels returns label entries ordered by key for stable rendering.
export function sortedLabels(labels) {
  return Object.entries(labels ?? {}).sort(([a], [b]) => a.localeCompare(b));
}
