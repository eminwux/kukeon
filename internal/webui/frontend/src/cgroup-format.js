// Formatting for the Cgroup detail panel (issue #1410). Every unset state has
// its own label so an unreported value never renders as 0 or as Unlimited.
export const UNLIMITED = "Unlimited";
export const NOT_AVAILABLE = "Not available (controller not enabled)";
export const UNREADABLE = "Unreadable";
export const NOT_SET = "Not set";

const KiB = 1024;
const units = ["B", "KiB", "MiB", "GiB", "TiB"];

export function formatBytes(bytes) {
  let value = bytes;
  let unit = 0;
  while (Math.abs(value) >= KiB && unit < units.length - 1) { value /= KiB; unit++; }
  return unit === 0 ? `${value} B` : `${Number(value.toFixed(value >= 100 ? 0 : value >= 10 ? 1 : 2))} ${units[unit]}`;
}

function unsetLabel(value) {
  if (!value || value.state === "absent") return NOT_AVAILABLE;
  if (value.state === "unreadable") return UNREADABLE;
  if (value.state === "max") return UNLIMITED;
  return undefined;
}

// formatCgroupValue renders a CgroupValue as { text, raw }. kind is "bytes",
// "count", "weight" or "usec".
export function formatCgroupValue(value, kind = "count") {
  const unset = unsetLabel(value);
  if (unset) return { text: unset, raw: value?.raw };
  if (typeof value.value !== "number") return { text: value.raw || NOT_AVAILABLE, raw: value.raw };
  const n = value.value;
  if (kind === "bytes") return { text: formatBytes(n), raw: value.raw };
  if (kind === "weight") return { text: n === 100 ? "100 (default)" : String(n), raw: value.raw };
  if (kind === "usec") return { text: `${Number((n / 1e6).toFixed(n >= 1e8 ? 0 : 2))} s`, raw: value.raw };
  return { text: String(n), raw: value.raw };
}

// formatCPUMax renders cpu.max as a core count ("0.5 CPU") or Unlimited.
export function formatCPUMax(cpuMax) {
  const unset = unsetLabel(cpuMax);
  if (unset) return { text: unset, raw: cpuMax?.raw };
  if (typeof cpuMax.quotaMicros !== "number" || typeof cpuMax.periodMicros !== "number" || cpuMax.periodMicros <= 0) {
    return { text: cpuMax.raw || NOT_AVAILABLE, raw: cpuMax.raw };
  }
  const cores = cpuMax.quotaMicros / cpuMax.periodMicros;
  return { text: `${Number(cores.toFixed(2))} CPU (${Number((cores * 100).toFixed(1))}%)`, raw: cpuMax.raw };
}

export function formatCgroupList(list) {
  const unset = unsetLabel(list);
  if (unset) return unset;
  return list.values?.length ? list.values.join(" ") : "None";
}

export function formatIOMax(io) {
  const unset = unsetLabel(io);
  if (unset) return [unset];
  if (!io.entries?.length) return ["No device limits"];
  return io.entries.map(entry => `${entry.device} ${Object.entries(entry.limits ?? {}).map(([k, v]) => `${k}=${v === "max" ? UNLIMITED : v}`).join(" ")}`);
}

const sources = { container: "Container spec", "space-default": "Inherited space default" };

// formatDeclared renders a container spec.resources entry.
export function formatDeclared(entry) {
  if (!entry || entry.source === "unset" || typeof entry.value !== "number") return { text: NOT_SET, source: NOT_SET };
  const text = entry.field === "memoryLimitBytes" ? formatBytes(entry.value) : String(entry.value);
  return { text, source: sources[entry.source] ?? entry.source };
}
