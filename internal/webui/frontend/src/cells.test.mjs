import assert from "node:assert/strict";
import test from "node:test";
import { activeCells, agentGroups, attentionFirst, cellMatches, groupCells, groupingKeys, isActiveCell, matchesState, needsAttention, rollupState, screenTail } from "./cells.mjs";

const row = (name, state, extra = {}) => ({ scope: ["r", "s", "k", name], doc: { metadata: { name, labels: extra.labels }, status: { state, ...extra.status } } });
const names = rows => rows.map(item => item.doc.metadata.name);

test("isActiveCell hides only Stopped and Exited", () => {
  const expected = { Pending: true, Ready: true, Stopped: false, Failed: true, Unknown: true, Exited: false, Error: true, Degraded: true };
  for (const [state, active] of Object.entries(expected)) assert.equal(isActiveCell(state), active, state);
  assert.equal(isActiveCell(undefined), true);
});

test("activeCells keeps the open cell whatever its state", () => {
  const rows = [row("a", "Ready"), row("b", "Stopped"), row("c", "Exited"), row("d", "Failed")];
  assert.deepEqual(names(activeCells(rows)), ["a", "d"]);
  assert.deepEqual(names(activeCells(rows, ["r", "s", "k", "b"])), ["a", "b", "d"]);
  assert.deepEqual(names(activeCells(rows, ["r", "s", "k"])), ["a", "d"]);
});

test("needsAttention follows the Overview rule", () => {
  assert.equal(needsAttention({ state: "Ready" }), false);
  assert.equal(needsAttention({ state: "Ready", outOfSync: true }), true);
  assert.equal(needsAttention({ state: "Ready", outOfSyncError: "compare failed" }), true);
  assert.equal(needsAttention({ state: "Failed" }), true);
  assert.equal(needsAttention(undefined), true);
});

test("attentionFirst keeps scope order inside each half", () => {
  const rows = [row("a", "Ready"), row("b", "Failed"), row("c", "Ready"), row("d", "Ready", { status: { outOfSync: true } }), row("e", "Pending")];
  assert.deepEqual(names(attentionFirst(rows)), ["b", "d", "e", "a", "c"]);
});

test("matchesState handles all, active, inactive and exact filters", () => {
  assert.equal(matchesState("Stopped", ""), true);
  assert.equal(matchesState("Ready", "active"), true);
  assert.equal(matchesState("Exited", "active"), false);
  assert.equal(matchesState("Exited", "inactive"), true);
  assert.equal(matchesState("Failed", "inactive"), false);
  assert.equal(matchesState("Failed", "Failed"), true);
  assert.equal(matchesState("Ready", "Failed"), false);
  assert.equal(matchesState(undefined, "Ready"), false);
});

test("screenTail on an empty string", () => {
  assert.equal(screenTail(""), "");
  assert.equal(screenTail("   \n  \n"), "");
});

test("screenTail drops trailing blank rows of a full screen", () => {
  const screen = Array.from({ length: 24 }, (_, i) => i < 15 ? `line ${i}   ` : "      ").join("\n");
  const tail = screenTail(screen).split("\n");
  assert.equal(tail.length, 12);
  assert.equal(tail[0], "line 3");
  assert.equal(tail.at(-1), "line 14");
});

test("screenTail keeps every row when there are fewer than the limit", () => {
  assert.equal(screenTail("$ ls\r\n\r\nREADME  \n"), "$ ls\n\nREADME");
  assert.equal(screenTail("a\nb\nc", 2), "b\nc");
});

test("cellMatches searches the joined scope", () => {
  assert.equal(cellMatches(row("api", "Ready"), " R / S "), true);
  assert.equal(cellMatches(row("api", "Ready"), "web"), false);
  assert.equal(cellMatches(row("api", "Ready"), ""), true);
});

test("groupCells orders values and puts unlabelled cells last", () => {
  const rows = [row("a", "Ready", { labels: { team: "web" } }), row("b", "Ready"), row("c", "Ready", { labels: { team: "api" } })];
  assert.deepEqual(groupCells(rows, "team").map(group => [group.label, names(group.rows)]), [["api", ["c"]], ["web", ["a"]], ["Ungrouped", ["b"]]]);
  assert.deepEqual(groupCells(rows, "").map(group => group.label), ["All cells"]);
  assert.deepEqual(groupingKeys([row("a", "Ready", { labels: { team: "x", "cell.kukeon.io/name": "a" } })], ""), ["team"]);
});

// A cell in `realm`: `config` is its kukeon.io/config label, `binding` its spec.provenance Config reference.
const cell = (realm, name, { config, binding, state = "Ready", status = {} } = {}) => ({
  scope: [realm, "s", "k", name],
  doc: { metadata: { name, labels: config === undefined ? undefined : { "kukeon.io/config": config } }, spec: binding ? { provenance: { bindingKind: "config", bindingRef: binding } } : {}, status: { state, ...status } },
});
const shape = ({ agents, others }) => ({ agents: agents.map(agent => [agent.name, agent.realm, names(agent.rows)]), others: names(others) });

test("agentGroups puts provenance and label-only cells of one Config in one agent", () => {
  const rows = [
    cell("dev", "a", { config: "claude" }),
    cell("dev", "b", { binding: { realm: "dev", name: "claude" }, config: "claude" }),
    cell("dev", "c", { binding: { realm: "dev", space: "s", name: "codex" } }),
    cell("dev", "d", { config: "codex" }),
  ];
  const result = agentGroups(rows);
  assert.deepEqual(shape(result), { agents: [["claude", "dev", ["a", "b"]], ["codex", "dev", ["c", "d"]]], others: [] });
  assert.deepEqual(result.agents.map(agent => [agent.showRealm, agent.context]), [[false, ""], [false, ""]]);
  assert.notEqual(result.agents[0].id, result.agents[1].id);
});

test("agentGroups keeps the same Config name in two realms apart and shows the realm", () => {
  const result = agentGroups([cell("prod", "a", { config: "claude" }), cell("dev", "b", { config: "claude" }), cell("dev", "c", { config: "solo" })]);
  assert.deepEqual(shape(result), { agents: [["claude", "dev", ["b"]], ["claude", "prod", ["a"]], ["solo", "dev", ["c"]]], others: [] });
  assert.deepEqual(result.agents.map(agent => [agent.showRealm, agent.context]), [[true, "dev"], [true, "prod"], [false, ""]]);
});

test("agentGroups tells two Configs of one name in one realm apart by their scope", () => {
  const result = agentGroups([cell("dev", "a", { binding: { realm: "dev", space: "s1", name: "claude" } }), cell("dev", "b", { binding: { realm: "dev", space: "s2", name: "claude" } })]);
  assert.deepEqual(result.agents.map(agent => [names(agent.rows), agent.showRealm, agent.context]), [[["a"], false, "dev / s1"], [["b"], false, "dev / s2"]]);
});

test("agentGroups lists cells without a Config under others, a whitespace-only label included", () => {
  assert.deepEqual(shape(agentGroups([cell("dev", "a", { config: "  " }), cell("dev", "b"), cell("dev", "c", { config: " claude " })])), { agents: [["claude", "dev", ["c"]]], others: ["a", "b"] });
  assert.deepEqual(shape(agentGroups([cell("dev", "a"), cell("dev", "b")])), { agents: [], others: ["a", "b"] });
  assert.deepEqual(shape(agentGroups([])), { agents: [], others: [] });
});

test("agentGroups with every cell labelled has no others and keeps a single-instance agent", () => {
  const result = agentGroups([cell("dev", "b", { config: "pm" }), cell("dev", "a", { config: "dev" }), cell("dev", "c", { config: "dev" })]);
  assert.deepEqual(shape(result), { agents: [["dev", "dev", ["a", "c"]], ["pm", "dev", ["b"]]], others: [] });
  assert.equal(result.agents[1].rows.length, 1);
});

test("rollupState is the first instance that needs attention, else the first instance", () => {
  assert.equal(rollupState([cell("dev", "a"), cell("dev", "b", { state: "Failed" }), cell("dev", "c", { state: "Pending" })]), "Failed");
  assert.equal(rollupState([cell("dev", "a", { state: "Ready" }), cell("dev", "b", { state: "Ready" })]), "Ready");
  assert.equal(rollupState([cell("dev", "a"), cell("dev", "b", { status: { outOfSync: true } })]), "Ready");
  assert.equal(rollupState([]), undefined);
});
