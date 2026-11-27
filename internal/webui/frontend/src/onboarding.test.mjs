import assert from "node:assert/strict";
import test from "node:test";
import {
  cardAuto, cardHidden, cardShown, cardVisible, checklist, configLogin, connectorSetUp, gitConnectors, gitIdentityConnector, harnessConnector, harnessConnectors, harnessLoggedIn, hasAgentCell,
  kindOptional, kindRecommended, kindRequired, notSetUpText, progress, recommendedFor, requiredOpen, stepAgent, stepGit, stepInitialize, stepLogin, stepMajordomo, stepState, teamsInitialized, templateLogins,
} from "./onboarding.mjs";

// Test-only connectors, as in connectors.test.mjs: the checklist must read
// them from the catalog alone. `demo` and `other` are harness connectors,
// `repo` is delivered by git, and `forge`'s token can also serve git.
const demo = {
  name: "demo", title: "Demo", description: "How a demo harness authenticates.", harness: "demo", scopes: ["realm"], secretTypes: ["opaque"], fields: [],
  methods: [{ name: "login", component: "demo-login", delivery: "stateVolume", default: true }, { name: "key", component: "demo-key", delivery: "env", slot: "demo-key", envName: "DEMO_KEY" }],
};
const other = { name: "other", title: "Other", description: "How another harness authenticates.", harness: "other", scopes: ["realm"], secretTypes: ["opaque"], fields: [], methods: [{ name: "login", component: "other-login", delivery: "stateVolume", default: true }] };
const repo = { name: "repo", title: "Repo", description: "The identity a cell clones and signs with.", delivery: "git", optional: true, scopes: ["realm"], secretTypes: ["ssh-key"], fields: [], methods: [] };
const forge = {
  name: "forge", title: "ForgeHub", description: "ForgeHub API access for the fh CLI.", scopes: ["realm"], secretTypes: ["opaque"], fields: [],
  methods: [{ name: "token", component: "forge", delivery: "env", slot: "forge-token", envName: "FH_TOKEN", default: true }],
  tokenForGit: { connector: "repo", hosts: ["forge.test"], username: "x-access-token", name: "forge-git", tokenName: "forge-token" },
};
const plain = { name: "plain", title: "Plain", description: "A test-only connector.", scopes: ["realm"], secretTypes: ["opaque"], fields: [], methods: [{ name: "token", component: "plain", delivery: "env", slot: "plain", envName: "PLAIN", default: true }] };
const connectors = [demo, other, repo, forge, plain];

const secret = (name, connector) => ({ metadata: { name, realm: "default" }, spec: { connector, type: "opaque" } });
const login = (connector, name, state, { exists = true, usedBy = [], session } = {}) => ({
  connector, method: "login", volume: { name, realm: "default", space: "default", stack: "default" }, exists, tagged: exists,
  ...(state ? { login: { state } } : {}), usedBy, ...(session ? { session } : {}),
});
const systemTeams = [{ name: "kuke" }, { name: "default" }];
const config = (name, annotations) => ({ metadata: { name, realm: "default", space: "default", stack: "default", ...(annotations ? { annotations } : {}) } });
const cell = (name, { provenance, label, realm = "default" } = {}) => ({
  scope: [realm, "default", "default", name],
  doc: { metadata: { name, ...(label ? { labels: { "kukeon.io/config": label } } : {}) }, spec: provenance ? { provenance: { bindingKind: "config", bindingRef: provenance } } : {} },
});
const step = (items, id) => items.find(item => item.id === id);

test("a fresh host has every step open, titled from the connector catalog", () => {
  const items = checklist({ teams: [], connectors, secrets: [], logins: [], cells: [], configs: [] });
  assert.deepEqual(items.map(item => item.id), [stepInitialize, stepLogin, stepGit, stepMajordomo, stepAgent]);
  assert.deepEqual(items.map(item => item.done), [false, false, false, false, false]);
  assert.deepEqual(items.map(item => item.kind), [kindRequired, kindRequired, kindRecommended, kindOptional, kindRequired]);
  assert.equal(step(items, stepLogin).title, "Log in to Demo or Other");
  assert.equal(step(items, stepGit).title, "Set up Repo and ForgeHub");
  assert.deepEqual(items.map(stepState), ["To do", "To do", "Recommended", "Optional", "To do"]);
  // Nothing loaded yet reads the same, with titles that name no connector.
  const empty = checklist();
  assert.deepEqual(empty.map(item => item.done), [false, false, false, false, false]);
  assert.equal(step(empty, stepLogin).title, "Log in to a harness");
  assert.equal(step(empty, stepGit).title, "Set up git");
});

test("Initialize is done only with both system teams", () => {
  assert.equal(teamsInitialized(undefined), false);
  assert.equal(teamsInitialized([{ name: "kuke" }]), false);
  assert.equal(teamsInitialized([{ name: "default" }, { name: "proj" }]), false);
  assert.equal(teamsInitialized([{ name: "proj" }, ...systemTeams]), true);
  assert.equal(step(checklist({ teams: systemTeams }), stepInitialize).done, true);
});

test("Log in is done by a logged-in login identity or a stored token of a harness connector", () => {
  const done = input => step(checklist({ connectors, ...input }), stepLogin).done;
  assert.equal(done({ secrets: [], logins: [login("demo", "agent-demo", "empty")] }), false);
  assert.equal(done({ secrets: [], logins: [login("demo", "agent-demo", "logged in")] }), true);
  assert.equal(done({ secrets: [], logins: [login("other", "agent-other", "logged in")] }), true);
  assert.equal(done({ secrets: [secret("demo-key", "demo")], logins: [] }), true);
  // A login or a token of a connector no harness uses does not count.
  assert.equal(done({ secrets: [secret("plain-token", "plain")], logins: [login("plain", "x", "logged in")] }), false);
  assert.deepEqual(harnessConnectors(connectors).map(item => item.name), ["demo", "other"]);
  assert.equal(harnessConnector(connectors, "other"), other);
  assert.equal(harnessConnector(connectors, ""), undefined);
  assert.equal(harnessLoggedIn(undefined, [], []), false);
});

test("git is done by an identity of the git connector or of a connector whose token serves git", () => {
  const done = secrets => step(checklist({ connectors, secrets }), stepGit).done;
  assert.equal(done([]), false);
  assert.equal(done([secret("plain-token", "plain")]), false);
  assert.equal(done([secret("key", "repo")]), true);
  assert.equal(done([secret("forge-token", "forge")]), true);
  assert.deepEqual(gitConnectors(connectors).map(item => item.name), ["repo", "forge"]);
  assert.equal(gitIdentityConnector(connectors), repo);
  assert.equal(gitIdentityConnector([forge]), undefined);
});

test("majordomo is done once a majordomo cell exists", () => {
  assert.equal(step(checklist({ majordomoCells: 0 }), stepMajordomo).done, false);
  assert.equal(step(checklist({ majordomoCells: 2 }), stepMajordomo).done, true);
});

test("the first agent is a cell of a Config copied from an agent template", () => {
  const copy = config("agent-1a2b", { "kukeon.io/agent-template": "default-agent-demo" });
  const configs = [config("default-agent-demo"), config("kuke-majordomo-demo"), copy];
  assert.equal(hasAgentCell([], configs), false);
  // A cell of the template itself, of majordomo, or hand-built is not an agent.
  assert.equal(hasAgentCell([cell("t", { label: "default-agent-demo" }), cell("m", { label: "kuke-majordomo-demo" }), cell("plain")], configs), false);
  assert.equal(hasAgentCell([cell("a", { provenance: { name: "agent-1a2b", realm: "default", space: "default", stack: "default" } })], configs), true);
  assert.equal(hasAgentCell([cell("a", { label: "agent-1a2b" })], configs), true);
  // The label names a Config of the cell's own realm only.
  assert.equal(hasAgentCell([cell("a", { label: "agent-1a2b", realm: "elsewhere" })], configs), false);
  assert.equal(hasAgentCell([cell("a", { label: "agent-1a2b" })], undefined), false);
  assert.equal(step(checklist({ cells: [cell("a", { label: "agent-1a2b" })], configs }), stepAgent).done, true);
});

test("recommended and optional steps never keep the checklist open", () => {
  const input = { teams: systemTeams, connectors, secrets: [], logins: [login("demo", "agent-demo", "logged in")], configs: [config("agent-1", { "kukeon.io/agent-template": "default-agent-demo" })], cells: [cell("a", { label: "agent-1" })] };
  const items = checklist(input);
  assert.deepEqual(requiredOpen(items), []);
  assert.deepEqual(progress(items), { done: 3, total: 3 });
  assert.deepEqual([step(items, stepGit).done, step(items, stepMajordomo).done], [false, false]);
  assert.deepEqual([stepState(step(items, stepGit)), stepState(step(items, stepMajordomo)), stepState(step(items, stepAgent))], ["Recommended", "Optional", "Done"]);
  // One required step open keeps it open.
  const open = checklist({ ...input, cells: [] });
  assert.deepEqual(requiredOpen(open).map(item => item.id), [stepAgent]);
  assert.deepEqual(progress(open), { done: 2, total: 3 });
});

test("the card shows while a required step is open, until dismissed, and again when reopened", () => {
  const open = checklist({ teams: [] });
  const done = [{ id: "a", kind: kindRequired, done: true }, { id: "b", kind: kindRecommended, done: false }, { id: "c", kind: kindOptional, done: false }];
  assert.equal(cardVisible(cardAuto, open), true);
  assert.equal(cardVisible(cardAuto, done), false);
  assert.equal(cardVisible(cardHidden, open), false);
  assert.equal(cardVisible(cardShown, done), true);
  assert.equal(cardVisible(cardShown, open), true);
});

test("a connector without identities is not set up, worded from its description", () => {
  assert.equal(connectorSetUp(demo, [], []), false);
  // A team state volume no cell created yet, and one nobody logged in to, are not identities.
  assert.equal(connectorSetUp(demo, [], [login("demo", "agent-demo", "", { exists: false })]), false);
  assert.equal(connectorSetUp(demo, [], [login("demo", "agent-demo", "empty")]), false);
  assert.equal(connectorSetUp(demo, [], [login("demo", "agent-demo", "logged in")]), true);
  // A login identity whose connector declares no marker has an unknown state: it exists.
  assert.equal(connectorSetUp(demo, [], [login("demo", "agent-demo", "")]), true);
  assert.equal(connectorSetUp(demo, [secret("demo-key", "demo")], []), true);
  assert.equal(connectorSetUp(repo, [secret("demo-key", "demo")], []), false);
  assert.equal(connectorSetUp(undefined, [], []), false);
  assert.equal(recommendedFor("How a demo harness authenticates."), "how a demo harness authenticates");
  assert.equal(recommendedFor("The identity a cell clones and signs with."), "the identity a cell clones and signs with");
  assert.equal(recommendedFor("ForgeHub API access for the fh CLI."), "ForgeHub API access for the fh CLI");
  assert.equal(recommendedFor("A test-only connector."), "a test-only connector");
  assert.equal(recommendedFor(undefined), "");
  assert.equal(notSetUpText(repo), "Not set up — recommended for the identity a cell clones and signs with.");
  assert.equal(notSetUpText({}), "Not set up — recommended.");
});

test("a Config's login identity is the one that names it", () => {
  const logins = [
    login("demo", "kuke-majordomo-demo", "empty", { usedBy: [{ name: "kuke-majordomo-demo", realm: "default" }] }),
    login("demo", "default-agent-demo", "logged in", { usedBy: [{ name: "default-agent-demo", realm: "default" }, { name: "agent-1", realm: "default" }] }),
    login("other", "default-agent-other", "empty", { exists: false, usedBy: [{ name: "default-agent-other", realm: "default" }] }),
  ];
  assert.equal(configLogin(logins, demo, { name: "kuke-majordomo-demo", realm: "default" }).volume.name, "kuke-majordomo-demo");
  assert.equal(configLogin(logins, demo, { name: "agent-1" }).volume.name, "default-agent-demo");
  assert.equal(configLogin(logins, demo, { name: "agent-1", realm: "elsewhere" }), undefined);
  assert.equal(configLogin(logins, other, { name: "default-agent-demo" }), undefined);
  assert.equal(configLogin(logins, demo, undefined), undefined);
  const templates = [{ harness: "demo", name: "default-agent-demo", realm: "default", exists: true }, { harness: "other", name: "default-agent-other", realm: "default", exists: false }];
  const pairs = templateLogins([demo, other, repo], logins, templates);
  assert.deepEqual(pairs.map(pair => [pair.connector.name, pair.login?.volume.name, pair.login?.state]), [["demo", "default-agent-demo", "logged in"], ["other", undefined, undefined]]);
  // Before Initialize there is no template, so no login to reach.
  assert.deepEqual(templateLogins([demo], [], []).map(pair => pair.login), [undefined]);
});
