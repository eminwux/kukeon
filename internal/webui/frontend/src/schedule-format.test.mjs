import assert from "node:assert/strict";
import test from "node:test";
import { concurrencyText, deadlineText, nextColumn, scheduleColumn, scheduleText, validTime } from "./schedule-format.mjs";

// #1630: the Configs table shows what `kuke get config` prints in SCHEDULE and NEXT.
test("schedule columns match kuke get config", () => {
  const scheduled = { spec: { schedule: { cron: "*/15 * * * *" } }, status: { schedule: { nextScheduleTime: "2026-11-23T10:15:00.000Z" } } };
  assert.equal(scheduleColumn(scheduled), "*/15 * * * *", "UTC is not spelled out in the column");
  assert.equal(scheduleColumn({ spec: { schedule: { cron: "0 9 * * 1", timezone: "Europe/Madrid" } } }), "0 9 * * 1 (Europe/Madrid)");
  assert.equal(nextColumn(scheduled), "2026-11-23T10:15:00Z");
  assert.equal(nextColumn({ spec: { schedule: { cron: "@daily", suspend: true } }, status: { schedule: { nextScheduleTime: "2026-11-23T10:15:00Z" } } }), "suspended");
  assert.equal(nextColumn({ spec: { schedule: { cron: "@daily" } } }), "", "before the scheduler's first pass");
  assert.equal(nextColumn({ spec: { schedule: { cron: "@daily" } }, status: { schedule: { nextScheduleTime: "0001-01-01T00:00:00Z" } } }), "");
  assert.equal(scheduleColumn({ spec: {} }), ""); assert.equal(nextColumn(undefined), "");
});

test("schedule prose names the time zone, the policy and the deadline", () => {
  assert.equal(scheduleText({ cron: "@hourly" }), "@hourly (UTC)");
  assert.equal(scheduleText({ cron: "0 3 * * *", timezone: "America/New_York" }), "0 3 * * * (America/New_York)");
  assert.equal(scheduleText(undefined), "");
  assert.match(concurrencyText({}), /^Forbid/, "unset means Forbid");
  assert.equal(deadlineText({}), "");
  assert.match(deadlineText({ startingDeadlineSeconds: 300 }), /^300 seconds/);
  assert.equal(validTime("0001-01-01T00:00:00Z"), false);
  assert.equal(validTime("not a time"), false);
  assert.equal(validTime("2026-11-23T10:15:00Z"), true);
});
