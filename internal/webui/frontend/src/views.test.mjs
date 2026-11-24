import assert from "node:assert/strict";
import test from "node:test";
import { manageAttention, viewOf } from "./views.mjs";

const route = (view, scope = []) => ({ view, scope });

test("viewOf puts terminals, cell pages and New Cell in Workbench", () => {
  assert.equal(viewOf(route("workbench")), "workbench");
  assert.equal(viewOf(route("workspace", ["r", "s", "k", "c"])), "workbench");
  assert.equal(viewOf(route("new-cell")), "workbench");
  assert.equal(viewOf(route("resource", ["r", "s", "k", "c"])), "workbench");
  assert.equal(viewOf(route("resource", ["r", "s", "k", "c", "shell"])), "workbench");
  // ?document=1 does not change the view: it looks at the scope length only.
  assert.equal(viewOf({ ...route("resource", ["r", "s", "k", "c"]), document: true }), "workbench");
});

test("viewOf puts every other route in Manage", () => {
  for (const view of ["dashboard", "realms", "spaces", "stacks", "cells", "blueprints", "configs", "volumes", "secrets", "connectors", "teams", "new-team", "sources", "images", "daemon", "daemon-logs", "health", "new-config", "new-secret"]) {
    assert.equal(viewOf(route(view)), "manage", view);
  }
  assert.equal(viewOf(route("resource", ["r"])), "manage");
  assert.equal(viewOf(route("resource", ["r", "s"])), "manage");
  assert.equal(viewOf(route("resource", ["r", "s", "k"])), "manage");
});

const reports = findings => [{ id: "r2", findings }, { id: "r1", findings: 5 }];

test("manageAttention is empty when nothing needs a look", () => {
  assert.deepEqual(manageAttention({}), []);
  assert.deepEqual(manageAttention({ reports: [] }), []);
});

test("manageAttention names daemon failures", () => {
  assert.deepEqual(manageAttention({ daemonError: { code: "permission_denied", message: "denied" } }), ["Daemon: permission denied"]);
  assert.deepEqual(manageAttention({ daemonError: { code: "unavailable", message: "down" } }), ["Daemon unavailable"]);
});

test("manageAttention names a version mismatch", () => {
  assert.deepEqual(manageAttention({ versionMismatch: true }), ["Version mismatch"]);
});

test("manageAttention flags only an unseen latest report with findings", () => {
  assert.deepEqual(manageAttention({ reports: reports(2), seenId: "r1" }), ["2 findings in the latest health report"]);
  assert.deepEqual(manageAttention({ reports: reports(1) }), ["1 finding in the latest health report"]);
  assert.deepEqual(manageAttention({ reports: reports(2), seenId: "r2" }), []);
  assert.deepEqual(manageAttention({ reports: reports(0) }), []);
  assert.deepEqual(manageAttention({ reports: undefined, seenId: "r0" }), []);
});

test("manageAttention keeps every reason in order", () => {
  assert.deepEqual(manageAttention({ daemonError: { code: "unavailable" }, versionMismatch: true, reports: reports(3) }),
    ["Daemon unavailable", "Version mismatch", "3 findings in the latest health report"]);
});
