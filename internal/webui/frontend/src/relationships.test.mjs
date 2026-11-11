import assert from "node:assert/strict";
import test from "node:test";
import { cellsForBlueprint, cellsForConfig, configsForBlueprint, sameScopedReference } from "./relationships.mjs";

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
