import assert from "node:assert/strict";
import test from "node:test";
import {
  defaultGridScreenSeconds, defaultRefreshSeconds, defaultUnreadPollSeconds, defaultUnreadQuietSeconds, gridScreenIntervals,
  parseGridScreenSeconds, parseRefreshSeconds, parseShowGetStarted, parseSwitch, parseUnreadPollSeconds, parseUnreadQuietSeconds,
  refreshIntervals, unreadPollIntervals, unreadQuietOptions,
} from "./settings-values.mjs";

test("the refresh interval reads only the offered values", () => {
  assert.deepEqual(refreshIntervals, [5, 10, 30, 60]);
  assert.equal(defaultRefreshSeconds, 10);
  for (const value of refreshIntervals) assert.equal(parseRefreshSeconds(String(value)), value);
  for (const raw of ["", "0", "1", "15", "60000", "-10", "10.5", "1e1", "ten", " "]) assert.equal(parseRefreshSeconds(raw), undefined, raw);
});

// Each Workbench timer (#1770) reads only its own offered values; anything else reads as its default.
const garbage = ["", "ten", " ", "-1", "1.5", "1e1", "0x2", "null"];

test("the unread check interval reads only its offered values, 2 s by default", () => {
  assert.deepEqual(unreadPollIntervals, [1, 2, 5, 10, 30]);
  assert.equal(defaultUnreadPollSeconds, 2);
  for (const value of unreadPollIntervals) assert.equal(parseUnreadPollSeconds(String(value)), value);
  for (const raw of [...garbage, "0", "3", "60", "2000"]) assert.equal(parseUnreadPollSeconds(raw), undefined, raw);
});

test("the thumbnail refresh interval reads only its offered values, 5 s by default", () => {
  assert.deepEqual(gridScreenIntervals, [2, 5, 10, 30]);
  assert.equal(defaultGridScreenSeconds, 5);
  for (const value of gridScreenIntervals) assert.equal(parseGridScreenSeconds(String(value)), value);
  for (const raw of [...garbage, "0", "1", "60", "5000"]) assert.equal(parseGridScreenSeconds(raw), undefined, raw);
});

test("the quiet wait reads 0 as Off and only its offered values, Off by default", () => {
  assert.deepEqual(unreadQuietOptions, [0, 1, 3, 5, 10]);
  assert.equal(defaultUnreadQuietSeconds, 0);
  assert.equal(parseUnreadQuietSeconds("0"), 0);
  for (const value of unreadQuietOptions) assert.equal(parseUnreadQuietSeconds(String(value)), value);
  for (const raw of [...garbage, "2", "30", "3000"]) assert.equal(parseUnreadQuietSeconds(raw), undefined, raw);
});

test("Get started reads true and false, and the three card modes it replaced", () => {
  assert.equal(parseShowGetStarted("true"), true);
  assert.equal(parseShowGetStarted("false"), false);
  // #1694 stored a card mode under the same key.
  assert.equal(parseShowGetStarted("hidden"), false);
  assert.equal(parseShowGetStarted("auto"), true);
  assert.equal(parseShowGetStarted("shown"), true);
  assert.equal(parseShowGetStarted("maybe"), undefined);
});

test("a switch reads only true and false", () => {
  assert.equal(parseSwitch("true"), true);
  assert.equal(parseSwitch("false"), false);
  assert.equal(parseSwitch("on"), undefined);
});
