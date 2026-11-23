import assert from "node:assert/strict";
import test from "node:test";
import {
  addErrors, addPayload, deleteSummary, editPayload, inheritingNote, needsForce, originLabel, pairRows, pickerOptions, planSections, refLabel, refOf,
  selectionEntry, sourceItems, sourcePayload, sourcesAPI, teamConfigs, teamsAPI, teamsLink, toggledDefaults,
} from "./teams.mjs";

const defaults = {
  sources: [{ name: "kukeon", url: "https://github.com/eminwux/kukeon", branch: "main" }, { name: "agents", url: "https://example.test/agents", branch: "main", clonePath: "/home/user/agents" }],
  defaultRoles: [{ name: "dev", source: "agents", path: "dev" }],
  defaultHarnesses: [{ name: "claude", source: "kukeon", path: "team/harnesses/claude" }, { name: "codex", source: "kukeon", path: "team/harnesses/codex" }],
};

test("links and API paths encode the team and source names", () => {
  assert.equal(teamsLink(), "#/teams");
  assert.equal(teamsLink("my team"), "#/teams/my%20team");
  assert.equal(teamsAPI(), "/api/teams");
  assert.equal(teamsAPI("kuke"), "/api/teams/kuke");
  assert.equal(teamsAPI("kuke", "reapply"), "/api/teams/kuke/reapply");
  assert.equal(sourcesAPI(), "/api/team-sources");
  assert.equal(sourcesAPI("a/b", "inspect"), "/api/team-sources/a%2Fb/inspect");
});

test("refs read branch, tag or commit, in that order", () => {
  assert.deepEqual(refOf({ branch: "main" }), { kind: "branch", value: "main" });
  assert.deepEqual(refOf({ tag: " v1 " }), { kind: "tag", value: "v1" });
  assert.deepEqual(refOf({}), { kind: "", value: "" });
  assert.equal(refLabel({ commit: "0123456789abcdef0123456789abcdef01234567" }), "commit 0123456789ab");
  assert.equal(refLabel({}), "default branch");
});

test("a selection is a default's name or NAME=SOURCE:PATH", () => {
  assert.equal(selectionEntry({ name: "dev", source: "agents", path: "dev", default: true }), "dev");
  assert.equal(selectionEntry({ name: "reviewer", source: "agents", path: "pr-reviewer", default: false }), "reviewer=agents:pr-reviewer");
  assert.equal(selectionEntry({ name: "", source: "agents", path: "pm" }), "agents:pm");
});

test("an add sends the CLI's inputs; an untouched picker keeps the declaration's list", () => {
  assert.deepEqual(addPayload({ name: "web", source: " https://example.test/web ", refKind: "tag", ref: "v2", useRepoFile: true, roles: null, harnesses: null, declaration: "" }), {
    name: "web", source: "https://example.test/web", tag: "v2", useRepoFile: true, roles: null, harnesses: null,
  });
  const picked = addPayload({
    source: "https://example.test/web", refKind: "branch", ref: "", useRepoFile: false,
    roles: [{ name: "dev", source: "agents", path: "dev", default: true }, { name: "pm", source: "agents", path: "pm", default: false }],
    harnesses: [], declaration: "kind: Team\n", force: true,
  });
  assert.equal(picked.branch, undefined, "an empty ref discovers the default branch");
  assert.equal(picked.useRepoFile, false);
  assert.deepEqual(picked.roles, ["dev", "pm=agents:pm"]);
  assert.deepEqual(picked.harnesses, [], "a touched empty picker replaces the list with nothing");
  assert.equal(picked.declaration, "kind: Team\n");
  assert.equal(picked.force, true);
});

test("an edit sends the whole declaration for the team", () => {
  assert.deepEqual(editPayload(" web ", "apiVersion: v1beta1\n"), { name: "web", declaration: "apiVersion: v1beta1\n" });
});

test("add form errors", () => {
  assert.deepEqual(addErrors({ source: "" }), ["Repository URL is required."]);
  assert.deepEqual(addErrors({ source: "x", refKind: "commit", ref: "abc" }), ["A commit is a full 40-character SHA."]);
  assert.deepEqual(addErrors({ source: "x", name: "Bad_Name" }), ["A team name is lowercase letters, digits, dots and hyphens."]);
  assert.deepEqual(addErrors({ source: "x", name: "web.app-1" }), []);
});

test("a stored team asks before replacing its declaration", () => {
  assert.equal(needsForce({ code: "already_exists", message: "team already exists" }), true);
  assert.equal(needsForce({ code: "team_failed" }), false);
  assert.equal(needsForce(undefined), false);
});

test("a plan shows the sections that have content, in the CLI's order", () => {
  const plan = { create: ["web-dev-claude"], rerender: [], prune: ["web-old"], createSecrets: null, missingSecrets: ["gh-token"], notes: ["note"] };
  assert.deepEqual(planSections(plan).map(section => section.key), ["create", "prune", "missingSecrets", "notes"]);
  assert.deepEqual(planSections(undefined), []);
  assert.equal(originLabel({ kind: "repository file", detail: "kuketeam.yaml at 0123abc" }), "repository file (kuketeam.yaml at 0123abc)");
  assert.equal(originLabel({ kind: "defaults" }), "defaults");
  assert.equal(originLabel(undefined), "");
});

test("pair rows carry tasks with their schedules", () => {
  const rows = pairRows({ pairs: [{ role: "dev", harness: "claude", config: "web-dev-claude", image: "img", components: ["github"], tasks: [{ name: "pick", config: "c", schedule: "@daily", suspend: true }, { name: "fix", config: "d" }] }] });
  assert.deepEqual(rows[0].tasks, ["pick (@daily, suspended)", "fix"]);
  assert.deepEqual(rows[0].components, ["github"]);
  assert.deepEqual(pairRows(undefined), []);
});

test("toggling a default keeps the others by name and adds the new one by path", () => {
  assert.deepEqual(toggledDefaults(defaults, "role", { name: "pm", source: "agents", path: "pm" }, true), { roles: ["dev", "pm=agents:pm"] });
  assert.deepEqual(toggledDefaults(defaults, "role", { name: "dev", source: "agents", path: "dev" }, false), { roles: [] });
  assert.deepEqual(toggledDefaults(defaults, "harness", { name: "codex", source: "kukeon", path: "team/harnesses/codex" }, false), { harnesses: ["claude"] });
});

test("picker options list the defaults, then what inspected sources offer", () => {
  const inspected = { agents: { roles: [{ name: "dev", path: "dev" }, { name: "pm", path: "pm" }], harnesses: [] } };
  const options = pickerOptions(defaults, inspected, "role");
  assert.deepEqual(options.map(option => [option.name, option.default]), [["dev", true], ["pm", false]]);
  assert.equal(pickerOptions(defaults, undefined, "harness").length, 2);
});

test("offered items default their flags and lists", () => {
  const items = sourceItems({ roles: [{ name: "dev", path: "dev" }], harnesses: [{ name: "claude", path: "h", default: true }] });
  assert.deepEqual(items.roles[0], { name: "dev", path: "dev", harnesses: [], tasks: [], default: false });
  assert.equal(items.harnesses[0].default, true);
});

test("a source add sends kuke team source add's flags", () => {
  assert.deepEqual(sourcePayload({ name: "agents", url: "https://example.test/agents", refKind: "commit", ref: "0123456789abcdef0123456789abcdef01234567", clonePath: "" }), {
    name: "agents", url: "https://example.test/agents", commit: "0123456789abcdef0123456789abcdef01234567",
  });
  assert.equal(sourcePayload({ name: "a", url: "u", clonePath: " /opt/a " }).clonePath, "/opt/a");
});

test("a defaults change names the teams that need a reapply", () => {
  assert.equal(inheritingNote({ inheriting: [] }), "No stored team inherits from the defaults.");
  assert.match(inheritingNote({ inheriting: ["kuke", "web"] }), /kuke, web/);
});

test("a delete reports what it removed and left", () => {
  assert.deepEqual(deleteSummary({ deleted: true, removed: [{ kind: "CellConfig", name: "web-dev-claude", action: "deleted" }], liveCells: ["default/default/default/c1"], keptVolumes: ["default/default/default/web-dev-claude-state"] }), {
    removed: ["CellConfig web-dev-claude"], liveCells: ["default/default/default/c1"], keptVolumes: ["default/default/default/web-dev-claude-state"],
  });
  assert.deepEqual(deleteSummary(undefined), { removed: [], liveCells: [], keptVolumes: [] });
});

test("a team's Configs are found by label, with their scopes", () => {
  const configs = [
    { metadata: { name: "b", realm: "default", labels: { "kukeon.io/team": "web" } } },
    { metadata: { name: "a", realm: "default", space: "s", stack: "t", labels: { "kukeon.io/team": "web" } } },
    { metadata: { name: "c", labels: { "kukeon.io/team": "kuke" } } },
  ];
  assert.deepEqual(teamConfigs(configs, "web"), [{ name: "a", scope: ["default", "s", "t"] }, { name: "b", scope: ["default", "default", "default"] }]);
  assert.deepEqual(teamConfigs(undefined, "web"), []);
});
