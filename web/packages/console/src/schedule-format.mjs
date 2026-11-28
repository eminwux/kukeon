// Pure formatting of a CellConfig schedule (#1630), shared by the Configs table, a Config's Schedule panel,
// a cell's overview and the Health page. No DOM, no network — unit-tested with node --test.

const text = value => typeof value === "string" ? value.trim() : "";

// validTime reports a timestamp worth showing: unset and Go's zero time (0001-…) are not.
export function validTime(value) {
  return typeof value === "string" && !!value && !value.startsWith("0001-") && Number.isFinite(Date.parse(value));
}
// scheduleColumn is `kuke get config`'s SCHEDULE column: the cron expression, then its time zone when one
// is set. Empty for an unscheduled Config.
export function scheduleColumn(config) {
  const schedule = config?.spec?.schedule;
  if (!schedule) return "";
  const tz = text(schedule.timezone);
  return tz ? `${text(schedule.cron)} (${tz})` : text(schedule.cron);
}
// nextColumn is `kuke get config`'s NEXT column: "suspended" while suspended, else the next fire in UTC
// (RFC 3339, seconds precision). Empty when unscheduled or before the scheduler's first pass on the Config.
export function nextColumn(config) {
  const schedule = config?.spec?.schedule;
  if (!schedule) return "";
  if (schedule.suspend) return "suspended";
  const next = config?.status?.schedule?.nextScheduleTime;
  return validTime(next) ? new Date(next).toISOString().replace(/\.\d{3}Z$/, "Z") : "";
}
// scheduleText is how a schedule reads in prose: the cron expression with its time zone, UTC when unset.
export function scheduleText(schedule) {
  if (!schedule) return "";
  return `${text(schedule.cron)} (${text(schedule.timezone) || "UTC"})`;
}
// concurrencyText says what a fire does while an earlier one still runs; unset means Forbid.
export function concurrencyText(schedule) {
  const policy = text(schedule?.concurrencyPolicy) || "Forbid";
  return policy === "Forbid" ? "Forbid — a fire is skipped while a cell from an earlier one runs" : policy;
}
// deadlineText describes startingDeadlineSeconds, or is empty when unset.
export function deadlineText(schedule) {
  const seconds = schedule?.startingDeadlineSeconds;
  if (typeof seconds !== "number" || !Number.isFinite(seconds)) return "";
  return `${seconds} second${seconds === 1 ? "" : "s"} — a fire missed by up to this much still runs`;
}
