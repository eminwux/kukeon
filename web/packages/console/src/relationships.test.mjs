import assert from "node:assert/strict";
import test from "node:test";
import { cellsForBlueprint, cellsForConfig, configForCell, configsForBlueprint, configsForStateVolume, sameScopedReference, scheduledRun, volumeMounts } from "./relationships.mjs";

const blueprint = { name: "base", realm: "r1", space: "shared", stack: "images" };
const configA = { metadata: { name: "app", realm: "r1", space: "team-a", stack: "dev" }, spec: { blueprint } };
const configB = { metadata: { name: "app", realm: "r2", space: "team-a", stack: "dev" }, spec: { blueprint: { ...blueprint, realm: "r2" } } };
const cell = (realm, name, bindingRef, state = "Ready") => ({ scope: [realm, "team-a", "dev", name], doc: { metadata: { name }, spec: { realmId: realm, spaceId: "team-a", stackId: "dev", provenance: { bindingKind: "config", bindingRef } }, status: { state } } });

test("Blueprint references resolve by full scope, including duplicate names", () => {
  assert.deepEqual(configsForBlueprint([configA, configB], blueprint), [configA]);
  assert.equal(sameScopedReference(blueprint, { ...blueprint, realm: "r2" }), false);
});

test("Config lineage resolves Cells by authoritative scoped provenance", () => {
  const cells = [cell("r1", "app-123", configA.metadata), cell("r1", "app-other", { ...configA.metadata, stack: "prod" }), cell("r1", "app-by-name-only", undefined)];
  assert.deepEqual(cellsForConfig(cells, configA.metadata), [cells[0]]);
  assert.deepEqual(cellsForConfig([cells[0], { ...cells[0] }], configA.metadata), [cells[0]]);
});

test("Blueprint lineage follows its scoped Configs and deduplicates full Cell identity", () => {
  const first = cell("r1", "same-cell", configA.metadata);
  const second = cell("r2", "same-cell", configB.metadata);
  const duplicate = { ...first, scope: [...first.scope] };
  const result = cellsForBlueprint([first, second, duplicate], [configA, configB]);
  assert.equal(result.length, 2);
  assert.equal(result[0].config, configA);
  assert.equal(result[1].config, configB);
});

test("A Cell resolves its source Config from the same scoped provenance, and only a Config binding counts", () => {
  const fromConfig = cell("r1", "app-123", configA.metadata);
  assert.deepEqual(configForCell(fromConfig.doc), configA.metadata);
  // The inverse of cellsForConfig: the reference it returns selects the same Cell.
  assert.deepEqual(cellsForConfig([fromConfig], configForCell(fromConfig.doc)), [fromConfig]);
  // A realm-scoped Config has no space or stack; absent coordinates are left out, not blanked.
  assert.deepEqual(configForCell({ spec: { provenance: { bindingKind: "config", bindingRef: { name: "base", realm: "r1" } } } }), { name: "base", realm: "r1" });
  assert.equal(configForCell({ spec: { provenance: { bindingKind: "blueprint", bindingRef: blueprint } } }), null);
  assert.equal(configForCell({ spec: { provenance: { bindingKind: "config", bindingRef: { realm: "r1" } } } }), null);
  assert.equal(configForCell({ spec: {} }), null);
  assert.equal(configForCell(undefined), null);
});

// #1630: which cells mount a Volume, resolving a bare source the way kukeond does.
const vol = (name, realm, space, stack) => ({ metadata: { name, realm, ...(space ? { space } : {}), ...(stack ? { stack } : {}) } });
const mounting = (scope, containers, state = "Ready") => ({ scope, doc: { metadata: { name: scope[3] }, spec: { containers }, status: { state } } });
test("volumeMounts resolves explicit references and bare sources deepest first", () => {
  const realmData = vol("data", "r1");
  const spaceData = vol("data", "r1", "s1");
  const stackData = vol("data", "r1", "s1", "k1");
  const otherRealm = vol("data", "r2");
  const volumes = [realmData, spaceData, stackData, otherRealm];
  const byRef = mounting(["r1", "s9", "k9", "by-ref"], [{ id: "app", volumes: [{ kind: "volume", volumeRef: { name: "data", realm: "r1", space: "s1" }, target: "/data", readOnly: true }] }]);
  const inStack = mounting(["r1", "s1", "k1", "in-stack"], [{ id: "app", volumes: [{ kind: "volume", source: "data", target: "/a" }] }]);
  const inSpace = mounting(["r1", "s1", "k2", "in-space"], [{ id: "app", volumes: [{ kind: "volume", source: "data", target: "/b", ensure: true }] }]);
  const inRealm = mounting(["r1", "s2", "k1", "in-realm"], [{ id: "app", volumes: [{ kind: "volume", source: "data", target: "/c" }] }, { id: "side", volumes: [{ kind: "volume", source: "data", target: "/d" }, { kind: "bind", source: "data", target: "/e" }] }], "Stopped");
  const elsewhere = mounting(["r2", "s1", "k1", "elsewhere"], [{ id: "app", volumes: [{ kind: "volume", source: "data", target: "/f" }] }]);
  const template = mounting(["r1", "s1", "k1", "template"], [{ id: "app", volumes: [{ kind: "volume", source: "data-${CELL_NAME}", target: "/g", ensure: true }, { kind: "volume", volumeRef: { name: "${CELL_NAME}", realm: "r1" }, target: "/h" }] }]);
  const cells = [byRef, inStack, inSpace, inRealm, elsewhere, template];

  assert.deepEqual(volumeMounts(cells, volumes, stackData).map(use => [use.row.scope[3], use.container, use.target, use.by]), [["in-stack", "app", "/a", "source"]], "the stack Volume shadows the space and realm ones");
  assert.deepEqual(volumeMounts(cells, volumes, spaceData).map(use => [use.row.scope[3], use.target, use.readOnly, use.ensure, use.by]),
    [["by-ref", "/data", true, false, "volumeRef"], ["in-space", "/b", false, true, "source"]]);
  assert.deepEqual(volumeMounts(cells, volumes, realmData).map(use => [use.row.scope[3], use.container, use.target]), [["in-realm", "app", "/c"], ["in-realm", "side", "/d"]], "two containers of one cell, and a bind mount is not a Volume");
  assert.deepEqual(volumeMounts(cells, volumes, otherRealm).map(use => use.row.scope[3]), ["elsewhere"]);
  assert.deepEqual(volumeMounts(cells, [realmData], stackData), [], "a Volume that does not exist resolves nothing");
  assert.deepEqual(volumeMounts(cells, [realmData], realmData).map(use => use.row.scope[3]), ["in-stack", "in-space", "in-realm", "in-realm"], "without deeper Volumes, a bare source falls back to the realm");
  assert.deepEqual(volumeMounts(undefined, volumes, realmData), []);
});

test("configsForStateVolume finds the Configs keeping a login in a Volume", () => {
  const login = vol("login", "r1", "s1");
  const configs = [
    { metadata: { name: "a", realm: "r1" }, status: { connectors: [{ connector: "harness", title: "Harness", delivery: "stateVolume", volumeRef: { name: "login", realm: "r1", space: "s1" } }] } },
    { metadata: { name: "b", realm: "r1" }, status: { connectors: [{ connector: "harness", delivery: "stateVolume", volumeRef: { name: "login", realm: "r1" } }] } },
    { metadata: { name: "c", realm: "r1" } },
  ];
  assert.deepEqual(configsForStateVolume(configs, login), [{ config: configs[0], connectors: ["Harness"] }]);
  assert.deepEqual(configsForStateVolume(undefined, login), []);
});

// #1630: a scheduled cell's fire time is in its name only.
test("scheduledRun reads the Config and the fire time of a scheduled cell", () => {
  const labels = { "kukeon.io/schedule": "health-sweep" };
  assert.deepEqual(scheduledRun({ metadata: { name: "health-sweep-1763892000", labels } }), { config: "health-sweep", firedAt: "2025-11-23T10:00:00.000Z" });
  assert.deepEqual(scheduledRun({ metadata: { name: "renamed-by-hand", labels } }), { config: "health-sweep", firedAt: undefined }, "the label alone still says it is a scheduled run");
  assert.deepEqual(scheduledRun({ metadata: { name: "health-sweep-12", labels } }), { config: "health-sweep", firedAt: undefined }, "not a plausible unix time");
  assert.equal(scheduledRun({ metadata: { name: "health-sweep-1763892000" } }), null, "no label");
  assert.equal(scheduledRun(undefined), null);
});
