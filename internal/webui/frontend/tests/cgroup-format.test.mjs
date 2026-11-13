import assert from "node:assert/strict";
import test from "node:test";
import { NOT_AVAILABLE, NOT_SET, UNLIMITED, formatBytes, formatCPUMax, formatCgroupList, formatCgroupValue, formatDeclared, formatIOMax } from "../src/cgroup-format.js";

test("kernel max renders as Unlimited, keeping the raw value", () => {
  assert.deepEqual(formatCgroupValue({ state: "max", raw: "max" }, "bytes"), { text: UNLIMITED, raw: "max" });
  assert.equal(formatCPUMax({ state: "max", raw: "max 100000", periodMicros: 100000 }).text, UNLIMITED);
});

test("absent interface files render as Not available, never 0 or Unlimited", () => {
  for (const value of [{ state: "absent" }, undefined]) {
    const text = formatCgroupValue(value, "bytes").text;
    assert.equal(text, NOT_AVAILABLE);
    assert.notEqual(text, "0 B");
  }
  assert.equal(formatCPUMax({ state: "absent" }).text, NOT_AVAILABLE);
  assert.equal(formatCgroupList({ state: "absent" }), NOT_AVAILABLE);
  assert.deepEqual(formatIOMax({ state: "absent" }), [NOT_AVAILABLE]);
});

test("a reported 0 stays 0", () => {
  assert.equal(formatCgroupValue({ state: "set", raw: "0", value: 0 }).text, "0");
});

test("human-readable units", () => {
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(536870912), "512 MiB");
  assert.equal(formatBytes(1610612736), "1.5 GiB");
  assert.equal(formatCPUMax({ state: "set", raw: "50000 100000", quotaMicros: 50000, periodMicros: 100000 }).text, "0.5 CPU (50%)");
  assert.equal(formatCgroupValue({ state: "set", raw: "100", value: 100 }, "weight").text, "100 (default)");
  assert.equal(formatCgroupValue({ state: "set", raw: "200", value: 200 }, "weight").text, "200");
});

test("io.max entries and empty file", () => {
  assert.deepEqual(formatIOMax({ state: "set", entries: [] }), ["No device limits"]);
  assert.deepEqual(formatIOMax({ state: "set", entries: [{ device: "8:0", limits: { rbps: "1048576", wbps: "max" } }] }), [`8:0 rbps=1048576 wbps=${UNLIMITED}`]);
});

test("declared resources label their source or Not set", () => {
  assert.deepEqual(formatDeclared({ field: "memoryLimitBytes", source: "unset" }), { text: NOT_SET, source: NOT_SET });
  assert.deepEqual(formatDeclared({ field: "memoryLimitBytes", value: 1073741824, source: "space-default" }), { text: "1 GiB", source: "Inherited space default" });
  assert.deepEqual(formatDeclared({ field: "pidsLimit", value: 64, source: "container" }), { text: "64", source: "Container spec" });
});
