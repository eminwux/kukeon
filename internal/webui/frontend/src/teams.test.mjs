import assert from "node:assert/strict";
import test from "node:test";
import {
  configRoute, deleteSummary, editPayload, exportAPI, inheritingNote, isDefault, listOrigin, needsForce, originLabel, pairRows, planSections, refErrors, refLabel, refOf,
  repositoryFromSource, sourceErrors, sourceItems, sourcePayload, sourceTeams, sourcesAPI, sourcesRoute, taskLabel, teamConfigs, teamsAPI, teamsRoute, toggledDefaults, usesLabel, validName,
} from "./teams.mjs";

const defaults = {
  sources: [{ name: "kukeon", url: "https://github.com/eminwux/kukeon", branch: "main" }, { name: "agents", url: "https://example.test/agents", branch: "main" }],
  defaultRoles: [{ name: "dev", source: "agents", path: "dev" }],
  defaultHarnesses: [{ name: "claude", source: "kukeon", path: "team/harnesses/claude" }, { name: "codex", source: "kukeon", path: "team/harnesses/codex" }],
};

test("route and API paths encode the team and source names", () => {
  assert.equal(teamsRoute(), "/teams");
  assert.equal(teamsRoute("my team"), "/teams/my%20team");
  assert.equal(teamsRoute("web", "edit"), "/teams/web/edit");
  assert.equal(teamsRoute("web", "export"), "/teams/web/export");
  assert.equal(teamsRoute("web", "bogus"), "/teams/web");
  assert.equal(sourcesRoute(), "/sources");
  assert.equal(sourcesRoute("a b"), "/sources/a%20b");
  assert.equal(teamsAPI(), "/teams");
  assert.equal(teamsAPI("kuke", "reapply"), "/teams/kuke/reapply");
  assert.equal(sourcesAPI(), "/sources");
  assert.equal(sourcesAPI("a/b", "inspect"), "/sources/a%2Fb/inspect");
  assert.equal(exportAPI("web"), "/teams/web/export");
  assert.equal(exportAPI("web", { hostFields: true, explicit: true }), "/teams/web/export?hostFields=1&explicit=1");
  assert.equal(configRoute(["default", "web", "default"], "web-dev"), "/configs/default/web/default/web-dev");
});

test("refs read branch, tag or commit, in that order, and are checked as the server does", () => {
  assert.deepEqual(refOf({ branch: "main" }), { kind: "branch", value: "main" });
  assert.deepEqual(refOf({ tag: " v1 " }), { kind: "tag", value: "v1" });
  assert.deepEqual(refOf({}), { kind: "", value: "" });
  assert.equal(refLabel({ commit: "0123456789abcdef0123456789abcdef01234567" }), "commit 0123456789ab");
  assert.equal(refLabel({}), "default branch");
  assert.deepEqual(refErrors("commit", "abc"), ["A commit is a full commit SHA."]);
  assert.deepEqual(refErrors("commit", "0123456789abcdef0123456789abcdef01234567"), []);
  assert.equal(refErrors("branch", "a b").length, 1);
  assert.deepEqual(refErrors("branch", "feature/x"), []);
  assert.equal(validName("web.app-1"), true);
  assert.equal(validName("Bad_Name"), false);
});

test("an edit as YAML sends the whole declaration for the team", () => {
  assert.deepEqual(editPayload(" web ", "apiVersion: v1beta1\n"), { name: "web", declaration: "apiVersion: v1beta1\n" });
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

test("pair rows and task labels carry schedules", () => {
  const rows = pairRows({ pairs: [{ role: "dev", harness: "claude", config: "web-dev-claude", image: "img", components: ["github"], tasks: [{ name: "pick", config: "c", schedule: "@daily", suspend: true }, { name: "fix", config: "d" }] }] });
  assert.deepEqual(rows[0].tasks, ["pick (@daily, suspended)", "fix"]);
  assert.deepEqual(rows[0].components, ["github"]);
  assert.deepEqual(pairRows(undefined), []);
  assert.equal(taskLabel({ name: "sweep", schedule: "*/5 * * * *" }), "sweep (*/5 * * * *)");
});

test("toggling a default keeps the others by name and adds the new one by path", () => {
  assert.deepEqual(toggledDefaults(defaults, "role", { name: "pm", source: "agents", path: "pm" }, true), { roles: ["dev", "pm=agents:pm"] });
  assert.deepEqual(toggledDefaults(defaults, "role", { name: "dev", source: "agents", path: "dev" }, false), { roles: [] });
  assert.deepEqual(toggledDefaults(defaults, "harness", { name: "codex", source: "kukeon", path: "team/harnesses/codex" }, false), { harnesses: ["claude"] });
  assert.equal(isDefault(defaults, "role", "agents", "dev"), true);
  assert.equal(isDefault(defaults, "harness", "agents", "dev"), false);
});

test("offered items default their flags and lists; tasks carry their schedules", () => {
  const items = sourceItems({ roles: [{ name: "dev", path: "dev", tasks: [{ name: "pick", schedule: "@hourly", suspend: true }, "old"] }], harnesses: [{ name: "claude", path: "h", default: true }] });
  assert.deepEqual(items.roles[0], { name: "dev", path: "dev", harnesses: [], default: false, tasks: [{ name: "pick", schedule: "@hourly", suspend: true, args: "" }, { name: "old" }] });
  assert.equal(items.harnesses[0].default, true);
});

test("a source create sends kuke create source's flags, with its identity", () => {
  assert.deepEqual(sourcePayload({ name: "agents", url: "https://example.test/agents", refKind: "commit", ref: "0123456789abcdef0123456789abcdef01234567", identity: " git-ssh " }), {
    name: "agents", url: "https://example.test/agents", commit: "0123456789abcdef0123456789abcdef01234567", identity: "git-ssh",
  });
  assert.equal("clonePath" in sourcePayload({ name: "a", url: "u", clonePath: " /opt/a " }), false);
  assert.equal(sourcePayload({ name: "a", url: "u", force: true }).force, true);
  assert.deepEqual(sourceErrors({ name: "", url: "" }), ["Name is required.", "Address is required."]);
  assert.equal(sourceErrors({ name: "project", url: "u" }).length, 1);
  assert.deepEqual(sourceErrors({ name: "agents", url: "u", refKind: "branch", ref: "" }), []);
});

test("a Source's uses name the teams once, and the defaults", () => {
  const source = { usedBy: [{ team: "web", as: "project", name: "project" }, { team: "web", as: "role", name: "dev" }, { as: "default role", name: "dev" }, { team: "api", as: "declared", name: "tools" }] };
  assert.deepEqual(sourceTeams(source), ["api", "web"]);
  assert.equal(usesLabel(source.usedBy[1]), "team web (role dev)");
  assert.equal(usesLabel(source.usedBy[2]), "default role dev");
});

test("a New Cell repository copies a Source's name, address and ref", () => {
  assert.deepEqual(repositoryFromSource({ name: "agents", url: "github.com/a/agents", branch: "main" }), { name: "agents", url: "github.com/a/agents", branch: "main", ref: "" });
  assert.deepEqual(repositoryFromSource({ name: "kukeon", url: "github.com/e/kukeon", tag: "v1" }), { name: "kukeon", url: "github.com/e/kukeon", branch: "", ref: "v1" });
});

test("a list is declared or inherited", () => {
  assert.equal(listOrigin({ inherited: true }), "inherited");
  assert.equal(listOrigin({ inherited: false }), "declared");
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
