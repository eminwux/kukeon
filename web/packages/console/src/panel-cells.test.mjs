import assert from "node:assert/strict";
import test from "node:test";
import { addToPanel, parseMembership, panelKeys, pruneMembership, removeFromPanel, sameMembership } from "./panel-cells.mjs";

const key = cell => JSON.stringify(["default", "default", "default", cell]);
const none = { added: [], removed: [] };

test("parseMembership reads unreadable storage as the default and drops unreadable entries", () => {
  for (const raw of [null, undefined, "", "{", "[]", "null", "3", '"x"']) assert.deepEqual(parseMembership(raw), none, String(raw));
  assert.deepEqual(parseMembership(JSON.stringify({ added: [key("a"), 3, "", key("a"), key("b")], removed: "x" })), { added: [key("a"), key("b")], removed: [] });
});

test("by default the panel holds every majordomo cell, in their order", () => {
  assert.deepEqual(panelKeys([key("m1"), key("m2")], [key("m1"), key("m2"), key("api")], none), [key("m1"), key("m2")]);
});

test("added cells follow the majordomo cells in the order added; a missing one is skipped", () => {
  let membership = addToPanel(none, key("web"), false);
  membership = addToPanel(membership, key("api"), false);
  membership = addToPanel(membership, key("web"), false);
  assert.deepEqual(membership.added, [key("web"), key("api")]);
  assert.deepEqual(panelKeys([key("m1")], [key("m1"), key("api"), key("web")], membership), [key("m1"), key("web"), key("api")]);
  assert.deepEqual(panelKeys([key("m1")], [key("m1"), key("api")], membership), [key("m1"), key("api")]);
});

test("any cell can be removed, also the only one; a removed majordomo cell stays out until added back", () => {
  let membership = removeFromPanel(none, key("m1"), true);
  assert.deepEqual(panelKeys([key("m1")], [key("m1")], membership), []);
  membership = addToPanel(membership, key("api"), false);
  membership = removeFromPanel(membership, key("api"), false);
  assert.deepEqual(membership, { added: [], removed: [key("m1")] });
  // Added back, the majordomo cell takes its place among the majordomo cells, not after the others.
  membership = addToPanel(addToPanel(membership, key("api"), false), key("m1"), true);
  assert.deepEqual(panelKeys([key("m1"), key("m2")], [key("m1"), key("m2"), key("api")], membership), [key("m1"), key("m2"), key("api")]);
  assert.deepEqual(membership.removed, []);
});

test("pruneMembership keeps only the cells of the inventory", () => {
  const membership = { added: [key("api"), key("gone")], removed: [key("m1"), key("old")] };
  assert.deepEqual(pruneMembership(membership, [key("api"), key("m1")]), { added: [key("api")], removed: [key("m1")] });
  assert.equal(sameMembership(pruneMembership(membership, [key("api"), key("gone"), key("m1"), key("old")]), membership), true);
  assert.equal(sameMembership(membership, { added: [key("gone"), key("api")], removed: membership.removed }), false);
});
