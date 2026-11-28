import assert from "node:assert/strict";
import test from "node:test";
import {
  affectedRoles, cellState, cellText, choiceOf, defaultsBody, gridRows, identityNotation, identityOptions, inheritingTeams, mergeChoice, nextDefaults,
  parseNotation, pickOf, pickOwn, pickSystem, resolve, roleRuns, sharedRoles, systemNotation, withRoleChoice, withTeamChoice,
} from "./assignments.mjs";

// A test-only harness connector with a login method and a token method — nothing the UI knows by name.
const agent = { name: "agent", title: "Agent", harness: "agent", methods: [
  { name: "login", delivery: "stateVolume", default: true },
  { name: "key", delivery: "env", slot: "AGENT_KEY" },
] };
const system = "default-agent-agent-state@default/default/default";
const logins = [
  { connector: "agent", volume: { name: "default-agent-agent-state", realm: "default", space: "default", stack: "default" }, login: { state: "empty" }, usedBy: [{ name: "default-agent-agent", realm: "default", role: "default/agent" }] },
  { connector: "agent", volume: { name: "work-max", realm: "default" }, login: { state: "logged in" }, usedBy: [] },
  { connector: "other", volume: { name: "x", realm: "default" }, usedBy: [] },
];
const form = (connectors, roles) => ({ name: "kukeon", sources: null, roles, harnesses: { inherit: true, items: [] }, connectors });
const role = (name, harnessConnectors = [{ name: "agent", set: false }]) => ({ name, source: "kukeon", path: `team/roles/${name}`, image: [], tasks: { all: true, items: [] }, connectors: [], harnessConnectors });

test("notations read and write name@realm/space/stack", () => {
  assert.equal(identityNotation({ name: "v", realm: "r", space: "s", stack: "k" }), "v@r/s/k");
  assert.equal(identityNotation({ name: "v", realm: "r" }), "v@r");
  assert.deepEqual(parseNotation("v@r/s"), { name: "v", realm: "r", space: "s" });
  assert.deepEqual(parseNotation("v", "dev"), { name: "v", realm: "dev" });
  assert.equal(parseNotation(""), undefined);
});

test("the System default is the default team's agent login identity", () => {
  assert.equal(systemNotation(logins, agent), system);
  assert.equal(systemNotation([], agent), "");
});

test("a choice maps to a picker value and back", () => {
  assert.equal(pickOf({ method: "login" }, agent, system), pickOwn);
  assert.equal(pickOf({}, agent, system), pickOwn);
  assert.equal(pickOf({ identity: system }, agent, system), pickSystem);
  assert.equal(pickOf({ identity: "work-max@default" }, agent, system), "id:work-max@default");
  assert.equal(pickOf({ method: "key", state: "ephemeral" }, agent, system), "method:key");
  assert.deepEqual(choiceOf(pickOwn, agent, system), { method: "login" });
  assert.deepEqual(choiceOf(pickSystem, agent, system), { method: "login", identity: system });
  assert.deepEqual(choiceOf("id:work-max@default", agent, system), { method: "login", identity: "work-max@default" });
  assert.deepEqual(choiceOf("method:key", agent, system, "ephemeral"), { method: "key", state: "ephemeral" });
});

test("resolution follows compose: role over team, a team that declares none reads the defaults, else own per role", () => {
  const defaults = { agent: { identity: system } };
  assert.deepEqual(resolve({ name: "agent", defaults, team: { inherit: true, items: {} } }), { found: true, choice: { identity: system }, from: "defaults" });
  // A team that declares its connectors does not read the defaults' entries.
  assert.equal(resolve({ name: "agent", defaults, team: { inherit: false, items: {} } }).found, false);
  // A role entry with a method replaces the team's choice whole: own per role.
  const own = resolve({ name: "agent", defaults, team: { inherit: false, items: { agent: { identity: "work-max@default" } } }, role: { name: "agent", set: true, method: "login" } });
  assert.deepEqual(own.choice, { method: "login" });
  assert.equal(cellText(own, agent, system), "own per role");
  // A role entry without a method fills from the team's.
  assert.deepEqual(mergeChoice({ method: "key", state: "ephemeral" }, { identity: "k" }), { method: "key", identity: "k", state: "ephemeral" });
  assert.equal(cellText(resolve({ name: "agent", defaults, team: { inherit: true, items: {} } }), agent, system), "System default");
  assert.equal(cellText({ found: false, choice: {}, from: "" }, agent, system), "own per role");
});

test("grid rows are All teams, then each team with its roles; a role runs a harness it pins or the team's", () => {
  const teams = [{ summary: { name: "kuke", system: true }, form: form({ inherit: true, items: {} }, { inherit: false, items: [role("majordomo")] }), view: { roles: { items: [{ name: "majordomo" }] } } }, { summary: { name: "broken" } }];
  assert.deepEqual(gridRows({}, teams).map(row => row.key), ["defaults", "team:kuke", "role:kuke/majordomo"]);
  assert.equal(roleRuns({ harness: "agent" }, [], agent), true);
  assert.equal(roleRuns({ harness: "other" }, [{ name: "agent" }], agent), false);
  assert.equal(roleRuns({}, [{ name: "agent" }], agent), true);
  assert.deepEqual(inheritingTeams(teams), ["kuke"]);
});

test("cell states show the cell's own value and what it resolves to", () => {
  const defaults = { agent: { identity: system } };
  const team = { summary: { name: "kukeon" }, form: form({ inherit: false, items: { agent: { identity: "work-max@default" } } }, { inherit: false, items: [role("dev"), role("pm", [{ name: "agent", set: true, method: "login" }])] }) };
  const all = cellState({ row: { kind: "defaults" }, connector: agent, defaults, team: undefined, system });
  assert.equal(all.pick, pickSystem);
  const teamCell = cellState({ row: { kind: "team", team: "kukeon" }, connector: agent, defaults, team, system });
  assert.equal(teamCell.pick, "id:work-max@default");
  const dev = cellState({ row: { kind: "role", team: "kukeon", role: "dev" }, connector: agent, defaults, team, system });
  assert.equal(dev.pick, "inherit");
  assert.equal(cellText(dev.resolved, agent, system), "work-max");
  const pm = cellState({ row: { kind: "role", team: "kukeon", role: "pm" }, connector: agent, defaults, team, system });
  assert.equal(pm.pick, pickOwn);
});

test("the All teams row writes connectors.<name> of the TeamDefaults and nothing else", () => {
  assert.deepEqual(defaultsBody("agent", { method: "login", identity: system }), { connectors: { agent: { method: "login", identity: system } } });
  assert.deepEqual(defaultsBody("agent", undefined), { connectors: { agent: null } });
  assert.deepEqual(nextDefaults({ other: { method: "x" } }, "agent", { identity: system }), { other: { method: "x" }, agent: { identity: system } });
  assert.deepEqual(nextDefaults({ agent: { identity: system } }, "agent", undefined), {});
});

test("a team row pick declares the defaults' choices once, and Inherit goes back to them", () => {
  const defaults = { other: { method: "x" } };
  const picked = withTeamChoice(form({ inherit: true, items: {} }, { inherit: true, items: [] }), "agent", { identity: "work-max@default" }, defaults);
  assert.deepEqual(picked.connectors, { inherit: false, items: { other: { method: "x" }, agent: { identity: "work-max@default" } } });
  const back = withTeamChoice(picked, "agent", undefined, defaults);
  assert.deepEqual(back.connectors, { inherit: true, items: {} });
});

test("a role row pick sets that role's item only, declaring inherited roles first", () => {
  const base = form({ inherit: true, items: {} }, { inherit: true, items: [] });
  const picked = withRoleChoice(base, "pm", "agent", { method: "login" }, [role("dev"), role("pm")]);
  assert.equal(picked.roles.inherit, false);
  assert.deepEqual(picked.roles.items.map(item => item.harnessConnectors), [[{ name: "agent", set: false }], [{ name: "agent", set: true, method: "login" }]]);
  const cleared = withRoleChoice(picked, "pm", "agent", undefined, []);
  assert.deepEqual(cleared.roles.items[1].harnessConnectors, [{ name: "agent", set: false }]);
  // A role item without the connector gets one.
  const added = withRoleChoice(form({ inherit: true, items: {} }, { inherit: false, items: [role("qa", [])] }), "qa", "agent", { method: "login" }, []);
  assert.deepEqual(added.roles.items[0].harnessConnectors, [{ name: "agent", set: true, method: "login" }]);
});

test("identity options are the connector's login identities, the System default apart", () => {
  assert.deepEqual(identityOptions(logins, agent, system), [{ value: "work-max@default", label: "work-max", state: "logged in" }]);
});

test("a change names the roles it points at an identity, and the roles that would share it", () => {
  const view = roles => ({ roles: { items: roles }, harnesses: { items: [{ name: "agent" }] } });
  const teams = [
    { summary: { name: "kuke" }, form: form({ inherit: true, items: {} }, { inherit: false, items: [role("majordomo")] }), view: view([{ name: "majordomo" }]) },
    { summary: { name: "kukeon" }, form: form({ inherit: false, items: {} }, { inherit: false, items: [role("dev"), role("pm", [{ name: "agent", set: true, method: "login" }]), role("ops")] }), view: view([{ name: "dev" }, { name: "pm" }, { name: "ops", harness: "other" }]) },
  ];
  assert.deepEqual(affectedRoles(teams, { kind: "defaults" }, agent), ["kuke/majordomo"]);
  assert.deepEqual(affectedRoles(teams, { kind: "team", team: "kukeon" }, agent), ["kukeon/dev"]);
  assert.deepEqual(affectedRoles(teams, { kind: "role", team: "kukeon", role: "pm" }, agent), ["kukeon/pm"]);
  const used = [{ connector: "agent", volume: { name: "work-max", realm: "default" }, usedBy: [{ name: "c", realm: "default", role: "kuke/majordomo" }] }];
  assert.deepEqual(sharedRoles(used, "work-max@default", ["kukeon/dev"]), ["kuke/majordomo", "kukeon/dev"]);
  assert.deepEqual(sharedRoles(used, "", ["kukeon/dev"]), []);
});
