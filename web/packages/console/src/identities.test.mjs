import assert from "node:assert/strict";
import test from "node:test";
import { filterRows, identityRows, loginMethodOf, markDefaults, roleOf, scopeDefaults, systemDefaults, usedByCount } from "./identities.mjs";

// A test-only catalog: a harness connector with a login and a token method, a git-delivered one and a
// registry-delivered one — nothing here is a connector the UI knows by name.
const connectors = [
  { name: "agent", title: "Agent", harness: "agent", scopes: ["realm", "space", "stack"], secretTypes: ["opaque"], fields: [], methods: [
    { name: "login", component: "agent-login", delivery: "stateVolume", default: true },
    { name: "key", component: "agent-key", delivery: "env", slot: "AGENT_KEY", envName: "AGENT_KEY" },
  ] },
  { name: "repo", title: "Repo", delivery: "git", scopes: ["realm"], secretTypes: ["ssh-key"], fields: [], methods: [] },
  { name: "pulls", title: "Pulls", delivery: "registry", scopes: ["realm"], secretTypes: ["basic-auth"], fields: [], methods: [] },
];
const secret = (name, realm, connector, extra = {}) => ({ metadata: { name, realm, ...extra }, spec: { connector, type: "opaque", identity: { account: "a" } } });
const config = (name, labels, connectorStatuses, annotations) => ({ metadata: { name, realm: "default", labels, ...(annotations ? { annotations } : {}) }, status: { connectors: connectorStatuses } });
const secrets = [secret("agent-key", "default", "agent"), secret("spare", "default", "agent"), secret("git-ssh", "default", "repo"), secret("registry-ghcr", "default", "pulls"), secret("plain", "default", "")];
const shared = { name: "work-max", realm: "default" };
const logins = [
  { connector: "agent", method: "login", volume: { name: "default-agent-agent-state", realm: "default", space: "default", stack: "default" }, exists: false, tagged: false, login: { state: "empty" }, usedBy: [{ name: "default-agent-agent", realm: "default", role: "default/agent" }] },
  { connector: "agent", method: "login", volume: shared, exists: true, tagged: true, login: { state: "logged in" }, usedBy: [{ name: "kukeon-dev", realm: "default", role: "kukeon/dev" }, { name: "kuke-majordomo", realm: "default", role: "kuke/majordomo" }] },
  { connector: "agent", method: "login", volume: { name: "old-pm-state", realm: "default" }, exists: true, tagged: true, login: { state: "empty" }, usedBy: [] },
];
const configs = [
  config("kukeon-pm", { "kukeon.io/team": "kukeon", "kukeon.io/role": "pm" }, [{ connector: "agent", method: "key", delivery: "env", identity: { name: "agent-key", realm: "default" } }]),
  config("my-agent", { "kukeon.io/role": "agent" }, [{ connector: "agent", method: "key", delivery: "env", identity: { name: "agent-key", realm: "default" } }], { "kukeon.io/agent-team": "default" }),
];
const scopes = [[{ doc: { metadata: { name: "default" }, spec: { defaults: { git: { identityRef: { name: "git-ssh", realm: "default" } } } } }, scope: ["default"] }], [], []];
const rows = identityRows({ connectors, secrets, logins, configs, scopes });
const row = name => rows.find(item => item.name === name);

test("roleOf names a Config's role as the server does", () => {
  assert.equal(roleOf({ metadata: { labels: { "kukeon.io/team": "t", "kukeon.io/role": "r" } } }), "t/r");
  assert.equal(roleOf({ metadata: { labels: { "kukeon.io/role": "r" }, annotations: { "kukeon.io/agent-team": "default" } } }), "default/r");
  assert.equal(roleOf({ metadata: { labels: { "kukeon.io/role": "r" } } }), "r");
  assert.equal(roleOf({ metadata: { labels: { "kukeon.io/team": "t" } } }), "");
  assert.equal(roleOf(undefined), "");
});

test("identityRows merges the tagged Secrets and the login identities, leaving out registry credentials and untagged Secrets", () => {
  assert.deepEqual(rows.map(item => `${item.kind}:${item.name}`), ["secret:agent-key", "secret:spare", "login:default-agent-agent-state", "login:work-max", "login:old-pm-state", "secret:git-ssh"]);
  assert.equal(row("agent-key").type, "opaque");
  assert.equal(row("work-max").type, "login");
  assert.equal(row("work-max").status, "logged in");
  assert.equal(row("default-agent-agent-state").exists, false);
  assert.deepEqual(row("agent-key").scope, ["default"]);
});

test("identityRows names the roles using an identity: both roles of a shared login identity, the Configs of a Secret", () => {
  assert.deepEqual(row("work-max").roles, ["kuke/majordomo", "kukeon/dev"]);
  assert.deepEqual(row("agent-key").roles, ["default/agent", "kukeon/pm"]);
  assert.deepEqual(row("agent-key").usedBy.map(user => user.name), ["kukeon-pm", "my-agent"]);
  assert.deepEqual(usedByCount(row("work-max")), { configs: 2, teams: ["kuke", "kukeon"] });
});

test("an identity no Config and no default uses is Unused", () => {
  assert.equal(row("spare").unused, true);
  assert.equal(row("old-pm-state").unused, true);
  assert.equal(row("work-max").unused, false);
  assert.equal(row("agent-key").unused, false);
  // Only a Realm default names it: used.
  assert.equal(row("git-ssh").unused, false);
  assert.deepEqual(row("git-ssh").defaults.map(item => `${item.level}:${item.scope.join("/")}`), ["realm:default"]);
  // A default the defaults API reports names an identity too.
  const marked = markDefaults(rows, [{ connector: "agent", method: "key", slot: "AGENT_KEY", levels: { realm: { name: "spare", realm: "default" } } }]);
  assert.equal(marked.find(item => item.name === "spare").unused, false);
  assert.equal(marked.find(item => item.name === "old-pm-state").unused, true);
});

test("scopeDefaults reads the secret fills and the git identity of every Realm, Space and Stack", () => {
  const found = scopeDefaults([
    [{ doc: { spec: { defaults: { secrets: { AGENT_KEY: { secretRef: { name: "k", realm: "r" } } } } } }, scope: ["r"] }],
    [{ doc: { spec: { defaults: { git: { identityRef: { name: "g", realm: "r" } } } } }, scope: ["r", "s"] }],
    [{ doc: { spec: {} }, scope: ["r", "s", "k"] }],
  ]);
  assert.deepEqual(found.map(item => `${item.level}:${item.scope.join("/")}:${item.slot}:${item.ref.name}`), ["realm:r:AGENT_KEY:k", "space:r/s::g"]);
  assert.deepEqual(scopeDefaults(undefined), []);
});

test("systemDefaults pins one row per connector × method: the default team's agent login, else the Realm default or none", () => {
  const pinned = systemDefaults({ connectors, rows, realmDefaults: [{ connector: "repo", component: "repo", levels: { realm: { name: "git-ssh", realm: "default" } } }, { connector: "agent", method: "key", slot: "AGENT_KEY", levels: {} }] });
  assert.deepEqual(pinned.map(item => `${item.connector}:${item.method}:${item.kind}:${item.row?.name ?? "-"}`), ["agent:login:login:default-agent-agent-state", "agent:key:secret:-", "repo::secret:git-ssh"]);
  // Without the default team, a login method has no system default row to point at.
  const bare = systemDefaults({ connectors, rows: rows.filter(item => !item.roles.includes("default/agent")), realmDefaults: undefined });
  assert.equal(bare[0].row, undefined);
});

test("filterRows narrows to one connector", () => {
  assert.deepEqual(filterRows(rows, "repo").map(item => item.name), ["git-ssh"]);
  assert.equal(filterRows(rows, "").length, rows.length);
});

test("a login identity with no method tag takes the connector's login method from the catalog", () => {
  const renamed = [{ ...connectors[0], methods: [{ name: "key", delivery: "env" }, { name: "manual", delivery: "stateVolume" }, { name: "browser", delivery: "stateVolume", default: true }] }];
  assert.equal(loginMethodOf(renamed[0]), "browser");
  assert.equal(loginMethodOf({ ...renamed[0], methods: renamed[0].methods.slice(0, 2) }), "manual");
  assert.equal(loginMethodOf(connectors[1]), "");
  const untagged = [{ connector: "agent", volume: { name: "dev-state", realm: "default" }, exists: true, tagged: false, login: { state: "empty" }, usedBy: [] }];
  const [got] = identityRows({ connectors: renamed, secrets: [], logins: untagged, configs: [], scopes: [[], [], []] });
  assert.equal(got.method, "browser");
});
