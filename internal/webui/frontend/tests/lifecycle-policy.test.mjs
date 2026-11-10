import assert from "node:assert/strict";
import test from "node:test";
import {
  availableLifecycleActions,
  canConfirmDestructiveAction,
  cellRemovalConfirmed,
  deleteOperationComplete,
  purgeOperationComplete,
  requestedPurgeOptions,
} from "../src/lifecycle-policy.js";

test("primary lifecycle actions follow the reported operational state", () => {
  assert.deepEqual(availableLifecycleActions("Ready"), ["stop", "restart"]);
  assert.deepEqual(availableLifecycleActions("Stopped"), ["start"]);
  assert.deepEqual(availableLifecycleActions(undefined), ["start"]);
});

test("destructive actions require acknowledgment or the exact full purge scope", () => {
  assert.equal(canConfirmDestructiveAction("kill", false, "", "r / s / t / c"), false);
  assert.equal(canConfirmDestructiveAction("kill", true, "", "r / s / t / c"), true);
  assert.equal(canConfirmDestructiveAction("delete", false, "", "r / s / t / c"), false);
  assert.equal(canConfirmDestructiveAction("delete", true, "", "r / s / t / c"), true);
  assert.equal(canConfirmDestructiveAction("purge", true, "r / s / t", "r / s / t / c"), false);
  assert.equal(canConfirmDestructiveAction("purge", false, "r / s / t / c", "r / s / t / c"), true);
});

test("purge flags remain opt-in", () => {
  assert.deepEqual(requestedPurgeOptions(), { force: false, cascade: false });
  assert.deepEqual(requestedPurgeOptions(true, false), { force: true, cascade: false });
  assert.deepEqual(requestedPurgeOptions(false, true), { force: false, cascade: true });
});

test("cell removal requires daemon metadata deletion or a confirming fresh read", () => {
  assert.equal(cellRemovalConfirmed(true, true), true);
  assert.equal(cellRemovalConfirmed(false, false), true);
  assert.equal(cellRemovalConfirmed(false, true), false);
  assert.equal(cellRemovalConfirmed(false, null), false);
});

test("partial Delete and Purge results are not presented as complete", () => {
  assert.equal(deleteOperationComplete({ metadataDeleted: true, cgroupDeleted: true }), true);
  assert.equal(deleteOperationComplete({ metadataDeleted: false, cgroupDeleted: true }), false);
  assert.equal(deleteOperationComplete({ metadataDeleted: true, cgroupDeleted: false }), false);
  assert.equal(purgeOperationComplete({ purgeSucceeded: true, metadataDeleted: true, cgroupDeleted: true }), true);
  assert.equal(purgeOperationComplete({ purgeSucceeded: false, metadataDeleted: true, cgroupDeleted: true }), false);
  assert.equal(purgeOperationComplete({ purgeSucceeded: true, metadataDeleted: false, cgroupDeleted: true }), false);
});
