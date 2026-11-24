import assert from "node:assert/strict";
import test from "node:test";
import {
  availableSources, createBody, cronErrors, draftPayload, editBody, emptyProject, gitIdentities, initialPathValid, inspectRequest, projectErrors, rolesErrors, sourcesErrors, stepErrors, stepsFor,
  roleConnectorRows, roleHarnesses, suspendOf, switchOf, taskRows, tasksErrors, togglable, withAllTasks, withConnector, withConnectorsInherit, withHarness, withInherit, withProject, withRole,
  withRoleConnector, withRolePatch, withSource, withTask,
} from "./team-form.mjs";

// form is a team's form as GET /api/teams/{name} returns it.
const form = () => ({
  name: "web",
  project: { alias: "project", stored: "web", refKind: "branch", ref: "main", clonePath: "/home/user/web" },
  sources: null,
  roles: { inherit: false, items: [{ name: "dev", source: "agents", path: "dev", image: [], tasks: { all: true, items: [] } }] },
  harnesses: { inherit: true, items: [] },
  connectors: { inherit: true, items: {} },
});

test("a new team has six steps; an edit starts at its sources", () => {
  assert.deepEqual(stepsFor("new").map(step => step.title), ["Project", "Other sources", "Roles and harnesses", "Tasks and schedules", "Credentials", "Review"]);
  assert.deepEqual(stepsFor("edit").map(step => step.key), ["sources", "roles", "tasks", "credentials", "review"]);
});

test("the Project step reads an address, a stored Source or a pasted file", () => {
  const project = { ...emptyProject(), address: " github.com/acme/web ", refKind: "tag", ref: "v1", identity: "git-ssh", name: "web" };
  assert.deepEqual(draftPayload(project), { name: "web", source: "github.com/acme/web", tag: "v1", identity: "git-ssh", useRepoFile: true });
  assert.deepEqual(draftPayload({ ...project, mode: "stored", stored: "app", identity: "ignored", useRepoFile: false, ref: "" }), { name: "web", source: "app", useRepoFile: false });
  assert.deepEqual(draftPayload({ mode: "file", file: "kind: Team\n" }), { declaration: "kind: Team\n" });
  assert.deepEqual(projectErrors(emptyProject()), ["Repository address is required."]);
  assert.deepEqual(projectErrors({ mode: "file", file: "" }), ["Paste a kuketeam.yaml."]);
  assert.deepEqual(projectErrors({ mode: "stored", stored: "app", name: "Bad" }), ["A team name is lowercase letters, digits, dots and hyphens."]);
  assert.deepEqual(stepErrors("project", { project: { ...project } }), ["Read the repository first."]);
  assert.deepEqual(stepErrors("project", { project, draft: {} }), []);
});

test("sources: an unresolved conflict, a duplicate and a relative clone path block the step", () => {
  const draftSources = [{ alias: "shared", stored: "shared", state: "conflict", have: "github.com/other/shared" }];
  let next = withSource(form(), undefined, { alias: "shared", url: "github.com/acme/shared" });
  assert.equal(next.sources.length, 1, "the first entry declares the list");
  assert.match(sourcesErrors(next, draftSources)[0], /holds another repository/);
  next = withSource(next, 0, { stored: "acme-shared" });
  assert.deepEqual(sourcesErrors(next, draftSources), []);
  next = withSource(next, undefined, { alias: "shared", url: "", clonePath: "opt" });
  const errors = sourcesErrors(next, draftSources);
  assert.ok(errors.some(e => /listed twice/.test(e)) && errors.some(e => /needs an address/.test(e)) && errors.some(e => /absolute/.test(e)), errors.join("; "));
  assert.equal(withSource(next, 1, null).sources.length, 1);
  assert.equal(withProject(form(), { refKind: "tag", ref: "v2" }).project.ref, "v2");
});

test("available sources are the project, the entries, then the stored Sources not shadowed", () => {
  const stored = [{ name: "agents", url: "github.com/a/agents", branch: "main", identity: "git-ssh" }, { name: "kukeon", url: "github.com/e/kukeon", tag: "v1" }];
  const next = withSource(form(), undefined, { alias: "kukeon", stored: "kukeon-fork", refKind: "", ref: "" });
  delete next.sources[0].url;
  const list = availableSources(next, stored);
  assert.deepEqual(list.map(source => source.alias), ["project", "kukeon", "agents"]);
  assert.equal(list[2].declared, false);
  assert.deepEqual(inspectRequest(list[0]), { path: "/api/sources/web/inspect", body: {} });
  assert.deepEqual(inspectRequest(list[1]), { path: "/api/sources/kukeon-fork/inspect", body: {} });
  assert.deepEqual(inspectRequest({ alias: "x", url: "github.com/x/x", refKind: "branch", ref: "dev", identity: "git-ssh" }), { path: "/api/sources/inspect", body: { url: "github.com/x/x", branch: "dev", identity: "git-ssh" } });
});

test("roles: selecting from an inherited list starts from the defaults; dropping keeps the rest", () => {
  const inherited = form();
  inherited.roles = { inherit: true, items: [] };
  let next = withRole(inherited, { name: "pm", source: "agents", path: "pm" }, true, [{ name: "dev", source: "agents", path: "dev" }]);
  assert.equal(next.roles.inherit, false);
  assert.deepEqual(next.roles.items.map(role => role.name), ["dev", "pm"]);
  assert.deepEqual(next.roles.items[1].tasks, { all: true, items: [] });
  next = withRole(next, { name: "dev", source: "agents", path: "dev" }, false);
  assert.deepEqual(next.roles.items.map(role => role.name), ["pm"]);
  next = withRolePatch(next, 0, { harness: "claude", image: ["go"] });
  assert.equal(next.roles.items[0].harness, "claude");
  assert.deepEqual(withInherit(next, "roles", true).roles, { inherit: true, items: [] });
  next = withHarness(form(), { name: "claude", source: "kukeon", path: "h/claude" }, true, [{ name: "codex", source: "kukeon", path: "h/codex" }]);
  assert.deepEqual(next.harnesses.items.map(h => h.name), ["codex", "claude"]);
  assert.deepEqual(rolesErrors({ ...form(), roles: { inherit: false, items: [] } }), ["Select at least one role, or use the default roles."]);
});

test("a role's initial path follows config.validateInitialPath: $HOME, $HOME/<dir>, / or a clean absolute path", () => {
  for (const ok of ["", "$HOME", "$HOME/work", "$HOME/a/b-c_d.e", "/", "/workspace", "/home/user/web"]) assert.equal(initialPathValid(ok), true, ok);
  for (const bad of ["work", "~/work", "$HOME/", "$HOMEwork", "$HOME/../etc", "$HOME//a", "/a//b", "/a/./b", "/a/../b", "/a/", "/a b", "/a:b", "${HOME}"]) assert.equal(initialPathValid(bad), false, bad);
  const withPath = initialPath => ({ ...form(), roles: { inherit: false, items: [{ ...form().roles.items[0], name: "majordomo", initialPath }] } });
  assert.deepEqual(rolesErrors(withPath(" $HOME ")), []);
  assert.deepEqual(rolesErrors(withPath("work")), ["Role majordomo: the initial path is $HOME, $HOME/<directory> or a clean absolute path (letters, digits, _, ., / and -)."]);
});

test("a schedule switch is suspend: on false, off true, the role's null", () => {
  assert.equal(suspendOf("on"), false);
  assert.equal(suspendOf("off"), true);
  assert.equal(suspendOf("role"), null);
  assert.equal(switchOf({ suspend: true }), "off");
  assert.equal(switchOf({ suspend: null }), "role");
  assert.equal(switchOf(undefined), "role");
});

test("task rows list the role's tasks with its defaults as placeholders", () => {
  const role = form().roles.items[0];
  const rows = taskRows(role, [{ name: "pick", schedule: "@hourly", suspend: true, args: "--all" }], [{ task: "sweep", schedule: { cron: "@daily" } }]);
  assert.deepEqual(rows.map(row => [row.name, row.selected, row.switch, row.roleSchedule, row.roleSuspend]), [["pick", true, "role", "@hourly", true], ["sweep", true, "role", "@daily", false]]);
  assert.equal(rows[0].roleArgs, "--all");
});

test("switching one task of an all-tasks role lists every task, so the others stay selected", () => {
  let next = withTask(form(), 0, "pick", { switch: "off" }, ["pick", "sweep"]);
  assert.deepEqual(next.roles.items[0].tasks, { all: false, items: [{ name: "pick", suspend: true }, { name: "sweep", suspend: null }] });
  next = withTask(next, 0, "sweep", { schedule: "*/15 * * * *", args: "--bug" }, ["pick", "sweep"]);
  assert.deepEqual(next.roles.items[0].tasks.items[1], { name: "sweep", suspend: null, schedule: "*/15 * * * *", args: "--bug" });
  next = withTask(next, 0, "pick", { selected: false }, ["pick", "sweep"]);
  assert.deepEqual(next.roles.items[0].tasks.items.map(task => task.name), ["sweep"]);
  assert.deepEqual(withAllTasks(next, 0).roles.items[0].tasks, { all: true, items: [] });
  const none = withTask(withTask(form(), 0, "pick", { selected: false }, ["pick"]), 0, "x", { selected: false }, []);
  assert.deepEqual(none.roles.items[0].tasks, { all: false, items: [] }, "dropping every task is tasks: []");
});

test("a role's harnesses: the pin, else the team's, narrowed to what the role declares, plus every override", () => {
  const team = { ...form(), harnesses: { inherit: false, items: [{ name: "codex" }, { name: "claude" }, { name: "script" }] } };
  const role = team.roles.items[0];
  assert.deepEqual(roleHarnesses(team, role, undefined), ["codex", "claude", "script"]);
  assert.deepEqual(roleHarnesses(team, role, { harnesses: ["claude", "codex"] }), ["codex", "claude"]);
  assert.deepEqual(roleHarnesses(team, role, { harnesses: [] }, [], [{ role: "dev", harness: "claude" }, { role: "pm", harness: "codex" }]), ["claude"], "task Configs narrow when the role's list is unknown");
  assert.deepEqual(roleHarnesses(team, { ...role, harness: "claude" }, { harnesses: ["claude", "codex"] }), ["claude"]);
  assert.deepEqual(roleHarnesses(form(), role, undefined, [{ name: "codex" }, { name: "claude" }]), ["codex", "claude"], "inherited harnesses");
  const overridden = { ...role, harness: "claude", tasks: { all: false, items: [{ name: "sweep", suspend: null, harnesses: { codex: { suspend: false } } }] } };
  assert.deepEqual(roleHarnesses(team, overridden, undefined), ["claude", "codex"], "an override is never hidden");
});

test("per-harness switches: rows carry them, withTask sets and clears an override", () => {
  const role = { ...form().roles.items[0], tasks: { all: false, items: [{ name: "sweep", suspend: true, harnesses: { codex: { schedule: "@daily", suspend: null } } }] } };
  const rows = taskRows(role, [], [], ["claude", "codex"]);
  assert.deepEqual(rows[0].harnesses, [{ name: "claude", switch: "same", schedule: "" }, { name: "codex", switch: "same", schedule: "@daily" }]);
  const base = { ...form(), roles: { inherit: false, items: [role] } };
  let next = withTask(base, 0, "sweep", { harness: "claude", switch: "on" }, ["sweep"]);
  assert.deepEqual(next.roles.items[0].tasks.items[0], { name: "sweep", suspend: true, harnesses: { claude: { suspend: false }, codex: { schedule: "@daily", suspend: null } } }, "the entry's switch is untouched");
  next = withTask(next, 0, "sweep", { harness: "codex", switch: "off" }, ["sweep"]);
  assert.deepEqual(next.roles.items[0].tasks.items[0].harnesses.codex, { schedule: "@daily", suspend: true }, "a schedule-only override keeps its schedule");
  next = withTask(next, 0, "sweep", { harness: "codex", switch: "same" }, ["sweep"]);
  assert.deepEqual(next.roles.items[0].tasks.items[0].harnesses.codex, { schedule: "@daily", suspend: null });
  next = withTask(next, 0, "sweep", { harness: "claude", switch: "same" }, ["sweep"]);
  assert.deepEqual(Object.keys(next.roles.items[0].tasks.items[0].harnesses), ["codex"], "an override left with neither is removed");
  const plain = withTask(withTask(form(), 0, "pick", { harness: "claude", switch: "on" }, ["pick"]), 0, "pick", { harness: "claude", switch: "same" }, ["pick"]);
  assert.deepEqual(plain.roles.items[0].tasks.items[0], { name: "pick", suspend: null, harnesses: undefined });
  assert.deepEqual(JSON.parse(JSON.stringify(plain.roles.items[0].tasks.items[0])), { name: "pick", suspend: null }, "no harnesses key is sent");
  assert.equal(base.roles.items[0].tasks.items[0].harnesses.claude, undefined, "withTask does not change its input");
});

test("cron text is five fields or a named schedule", () => {
  assert.deepEqual(cronErrors(""), []);
  assert.deepEqual(cronErrors("@daily"), []);
  assert.deepEqual(cronErrors("*/15 * * * *"), []);
  assert.equal(cronErrors("every day").length, 1);
  assert.equal(cronErrors("* * *").length, 1);
  const next = withTask(form(), 0, "pick", { schedule: "nope" }, ["pick"]);
  assert.match(tasksErrors(next)[0], /^dev \/ pick:/);
  const role = { ...form().roles.items[0], tasks: { all: false, items: [{ name: "pick", suspend: null, harnesses: { claude: { schedule: "nope", suspend: null } } }] } };
  assert.match(tasksErrors({ ...form(), roles: { inherit: false, items: [role] } })[0], /^dev \/ pick on claude:/);
});

test("connectors: the first choice declares the list; clearing a choice drops it", () => {
  let next = withConnector(form(), "claude", { method: "api-key" });
  assert.deepEqual(next.connectors, { inherit: false, items: { claude: { method: "api-key", identity: "" } } });
  next = withConnector(next, "claude", { method: "" });
  assert.deepEqual(next.connectors, { inherit: false, items: {} });
  assert.deepEqual(withConnectorsInherit(next, true).connectors, { inherit: true, items: {} });
});

test("request bodies: a new team sends the Project inputs, the form and the draft's declaration unchanged", () => {
  const project = { ...emptyProject(), address: "github.com/acme/web", identity: "git-ssh" };
  const draft = { name: "web", declaration: "opaque: yes\n" };
  const body = createBody(project, draft, { ...form(), name: "" }, true);
  assert.deepEqual(Object.keys(body).sort(), ["declaration", "force", "form", "identity", "name", "source"]);
  assert.equal(body.name, "web");
  assert.equal(body.form.name, "web");
  assert.equal(body.declaration, "opaque: yes\n");
  assert.deepEqual(editBody(" web ", form()), { name: "web", form: form() });
});

test("git identities are the Secrets of realm default of the connectors delivered as git", () => {
  const secrets = [
    { metadata: { name: "b", realm: "default" }, spec: { connector: "git" } },
    { metadata: { name: "a", realm: "default" }, spec: { connector: "git" } },
    { metadata: { name: "c", realm: "other" }, spec: { connector: "git" } },
    { metadata: { name: "d", realm: "default", space: "s" }, spec: { connector: "git" } },
    { metadata: { name: "e", realm: "default" }, spec: { connector: "claude" } },
  ];
  const connectors = [{ name: "git", delivery: "git" }, { name: "claude", methods: [] }];
  assert.deepEqual(gitIdentities(secrets, connectors), ["a", "b"]);
  assert.deepEqual(gitIdentities(secrets, []), []);
});

// catalog is GET /api/connectors, as far as per-role access reads it.
const catalog = [
  { name: "claude", title: "Claude", harness: "claude", methods: [{ name: "login" }] },
  { name: "git", title: "Git", delivery: "git", methods: [] },
  { name: "github", title: "GitHub", methods: [{ name: "token", component: "github" }] },
  { name: "registry", title: "Registry", delivery: "registry", methods: [] },
];

test("per-role access offers the connectors a team can give a role (#1649 D3)", () => {
  assert.deepEqual(catalog.filter(togglable).map(c => c.name), ["git", "github"]);
  const role = { name: "majordomo", connectors: [{ name: "github", enabled: true, identity: "bot-token" }] };
  const view = [
    { role: "majordomo", config: "kuke-majordomo-claude", connector: "github", state: "unresolved", fill: "MISSING", setByTeam: true },
    { role: "majordomo", config: "kuke-majordomo-claude", connector: "git", state: "resolved", fill: "id (realm)", setByTeam: false },
    { role: "other", config: "x", connector: "github", state: "resolved", fill: "-", setByTeam: false },
  ];
  const rows = roleConnectorRows(role, catalog, view);
  assert.deepEqual(rows.map(row => [row.name, row.enabled, row.identity, row.required]), [["git", false, "", true], ["github", true, "bot-token", false]]);
  assert.deepEqual(rows[1].states, [{ config: "kuke-majordomo-claude", state: "unresolved", fill: "MISSING" }]);
  assert.deepEqual(roleConnectorRows({ name: "new" }, catalog).map(row => row.enabled), [false, false], "a new role has every switch off");
});

test("a per-role switch and identity change only that role's item", () => {
  const start = form();
  let next = withRoleConnector(start, 0, "github", { enabled: true });
  assert.deepEqual(next.roles.items[0].connectors, [{ name: "github", enabled: true, identity: "" }]);
  assert.equal(start.roles.items[0].connectors, undefined, "the reducer does not change its input");
  next = withRoleConnector(next, 0, "github", { identity: " bot-token " });
  assert.deepEqual(next.roles.items[0].connectors, [{ name: "github", enabled: true, identity: "bot-token" }]);
  next = withRoleConnector(next, 0, "github", { enabled: false });
  assert.deepEqual(next.roles.items[0].connectors, [{ name: "github", enabled: false, identity: "bot-token" }]);
  assert.equal(withRoleConnector(start, 5, "github", { enabled: true }), start, "an unknown role changes nothing");
  // The edit body is the form: the server applies the items that differ from the stored team.
  assert.deepEqual(editBody("web", next).form.roles.items[0].connectors, next.roles.items[0].connectors);
});

test("a role selected again keeps its connector switches", () => {
  const withSwitch = { ...form(), roles: { inherit: false, items: [{ ...form().roles.items[0], connectors: [{ name: "github", enabled: true }] }] } };
  const kept = withInherit(withSwitch, "roles", false);
  assert.deepEqual(kept.roles.items[0].connectors, [{ name: "github", enabled: true }]);
  const fromDefaults = withInherit({ ...form(), roles: { inherit: true, items: [] } }, "roles", false, [{ name: "majordomo", source: "kukeon", path: "team/roles/majordomo", connectors: [{ name: "github", enabled: true, identity: "x" }] }]);
  assert.deepEqual(fromDefaults.roles.items[0].connectors, [{ name: "github", enabled: true, identity: "x" }]);
});
