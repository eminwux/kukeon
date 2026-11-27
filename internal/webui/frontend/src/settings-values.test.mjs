import assert from "node:assert/strict";
import test from "node:test";
import { defaultRefreshSeconds, parseRefreshSeconds, parseShowGetStarted, parseSwitch, refreshIntervals } from "./settings-values.mjs";

test("the refresh interval reads only the offered values", () => {
  assert.deepEqual(refreshIntervals, [5, 10, 30, 60]);
  assert.equal(defaultRefreshSeconds, 10);
  for (const value of refreshIntervals) assert.equal(parseRefreshSeconds(String(value)), value);
  for (const raw of ["", "0", "1", "15", "60000", "-10", "10.5", "1e1", "ten", " "]) assert.equal(parseRefreshSeconds(raw), undefined, raw);
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
