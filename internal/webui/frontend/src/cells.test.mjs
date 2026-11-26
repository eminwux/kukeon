import assert from "node:assert/strict";
import test from "node:test";
import { activeCells, agentGroups, attentionFirst, cellMatches, configDisplayName, displayNameError, groupCells, groupingKeys, instanceLabels, isActiveCell, matchesState, needsAttention, rollupState, screenTail } from "./cells.mjs";

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

// Display names (#1692). A Config in realm dev / s / k; `copy` makes it an agent copy of a template.
const config = (name, { displayName, harness, copy = false, template = false } = {}) => ({
  metadata: {
    name, realm: "dev", space: "s", stack: "k",
    labels: { ...(harness ? { "kukeon.io/harness": harness, "kukeon.io/role": "agent" } : {}), ...(template ? { "kukeon.io/template": "true" } : {}) },
    annotations: { ...(displayName !== undefined ? { "kukeon.io/display-name": displayName } : {}), ...(copy ? { "kukeon.io/agent-template": `default-agent-${harness}` } : {}) },
  },
});
const bound = (name, configName, { createdAt, displayName, labels } = {}) => {
  const row = cell("dev", name, { binding: { realm: "dev", space: "s", stack: "k", name: configName }, config: configName, status: createdAt ? { createdAt } : {} });
  if (displayName) row.doc.metadata.annotations = { "kukeon.io/display-name": displayName };
  if (labels) row.doc.metadata.labels = { ...row.doc.metadata.labels, ...labels };
  return row;
};

test("configDisplayName prefers the annotation, then the agent-copy fallback, else none", () => {
  assert.deepEqual(configDisplayName(config("c1", { displayName: " Reviewer ", harness: "claude", copy: true })), { name: "Reviewer", named: true });
  assert.deepEqual(configDisplayName(config("c2", { harness: "claude", copy: true })), { name: "Claude agent", named: true });
  assert.deepEqual(configDisplayName(config("c3", { harness: "codex", copy: true })), { name: "Codex agent", named: true });
  assert.deepEqual(configDisplayName(config("c4", { displayName: "  ", harness: "codex", copy: true })), { name: "Codex agent", named: true });
  assert.deepEqual(configDisplayName(config("pm")), { name: "", named: false });
  assert.deepEqual(configDisplayName(undefined), { name: "", named: false });
});

test("agentGroups names agents from their Config and falls back to the Config name", () => {
  const configs = [config("default-agent-codex-a1b2c3", { displayName: "Docs bot", harness: "codex", copy: true }), config("default-agent-claude-d4e5f6", { harness: "claude", copy: true }), config("pm")];
  const rows = [bound("x", "default-agent-codex-a1b2c3"), bound("y", "default-agent-claude-d4e5f6"), bound("z", "pm"), cell("dev", "w", { config: "pm" })];
  const { agents } = agentGroups(rows, configs);
  assert.deepEqual(agents.map(agent => [agent.displayName, agent.name, agent.named, names(agent.rows)]), [
    ["Claude agent", "default-agent-claude-d4e5f6", true, ["y"]],
    ["Docs bot", "default-agent-codex-a1b2c3", true, ["x"]],
    ["pm", "pm", false, ["z", "w"]],
  ]);
  // Without the Config list every agent is its Config name.
  assert.deepEqual(agentGroups(rows).agents.map(agent => [agent.displayName, agent.named]), [["default-agent-claude-d4e5f6", false], ["default-agent-codex-a1b2c3", false], ["pm", false]]);
});

test("agentGroups leaves template cells out of agents and others", () => {
  const configs = [config("default-agent-claude", { harness: "claude", template: true })];
  const rows = [bound("t1", "default-agent-claude"), cell("dev", "t2", { config: "default-agent-claude" }), cell("dev", "loose")];
  rows[1].doc.metadata.labels["kukeon.io/template"] = "true";
  assert.deepEqual(shape(agentGroups(rows, configs)), { agents: [], others: ["loose"] });
});

test("instanceLabels numbers a named agent's instances by creation time and honours a cell's own name", () => {
  const configs = [config("default-agent-codex-a1b2c3", { displayName: "Docs bot", harness: "codex", copy: true }), config("pm")];
  const rows = [
    bound("b", "default-agent-codex-a1b2c3", { createdAt: "2026-11-02T00:00:00Z" }),
    bound("a", "default-agent-codex-a1b2c3", { createdAt: "2026-11-03T00:00:00Z" }),
    bound("c", "default-agent-codex-a1b2c3", { createdAt: "2026-11-01T00:00:00Z", displayName: "Night shift" }),
    bound("d", "default-agent-codex-a1b2c3", { createdAt: "0001-01-01T00:00:00Z" }),
    bound("p", "pm"),
    bound("q", "pm", { displayName: "Planner" }),
  ];
  const labels = instanceLabels(rows, configs);
  const label = name => labels.get(JSON.stringify(["dev", "s", "k", name]));
  // c is the first instance and keeps its own name; b and a follow; d has no creation time and comes last.
  assert.equal(label("c"), "Night shift");
  assert.equal(label("b"), "Docs bot #2");
  assert.equal(label("a"), "Docs bot #3");
  assert.equal(label("d"), "Docs bot #4");
  // An agent without a name of its own keeps cell names, except for a cell that carries one.
  assert.equal(label("p"), undefined);
  assert.equal(label("q"), "Planner");
});

test("instanceLabels falls back to name order when no instance has a creation time", () => {
  const configs = [config("default-agent-claude-x", { harness: "claude", copy: true })];
  const labels = instanceLabels([bound("beta", "default-agent-claude-x"), bound("alpha", "default-agent-claude-x")], configs);
  assert.deepEqual([...labels.entries()].sort(), [[JSON.stringify(["dev", "s", "k", "alpha"]), "Claude agent #1"], [JSON.stringify(["dev", "s", "k", "beta"]), "Claude agent #2"]]);
});

test("displayNameError allows empty and up to 63 characters without control characters", () => {
  assert.equal(displayNameError(""), "");
  assert.equal(displayNameError("  Docs bot  "), "");
  assert.equal(displayNameError("é".repeat(63)), "");
  assert.match(displayNameError("x".repeat(64)), /63/);
  assert.match(displayNameError("a\u0007b"), /control/);
});
