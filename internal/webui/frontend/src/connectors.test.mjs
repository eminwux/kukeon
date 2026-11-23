import assert from "node:assert/strict";
import test from "node:test";
import {
  allowedLevels, blockingConnectors, blocksCells, changedMethods, changedPicks, clearMaterial, connectorLevels, defaultGroupChoices, defaultRow, defaultTargets, emptyIdentityForm, formatSecretReference,
  groupComponents, identitiesFor, listedConnectors, identityErrors, identityFields, identityPayload, importPayload, importsFromHost, isRegistry, loginMethods, methodChoices, methodStatuses, originLabel, refKey, refLabel,
  emptySecretForm, registryEntries, sameRef, scopeAt, scopeFromParts, scopeLevel, scopeQuery, secretErrors, secretPayload, secretRef, secretTypeOf, storesNoValue, takesIdentity, targetScope, teamOf, unresolvedConnectors, usageFor,
} from "./connectors.mjs";
import { blueprintForm, buildSpec, componentsWithoutIdentity, connectorsWithoutIdentity, emptyAnswers, formSections, groupChoice, missingAnswers } from "./config-form.mjs";

// Test-only connectors: nothing in the UI knows these names. `example-token`
// is the one the acceptance criterion describes — env delivery, one readable
// field; the others exercise the remaining delivery handlers.
const example = {
  name: "example-token", title: "Example", description: "A test-only connector.", scopes: ["realm", "space", "stack"], secretTypes: ["opaque"],
  fields: [{ name: "account", type: "string", required: true, help: "Account the token belongs to." }],
  methods: [{ name: "token", component: "example-token", default: true, delivery: "env", slot: "example-token", envName: "EXAMPLE_TOKEN" }],
};
const demo = {
  name: "demo", title: "Demo", group: "demo-auth", harness: "demo", scopes: ["realm", "space", "stack"], secretTypes: ["opaque"], fields: [],
  methods: [
    { name: "login", component: "demo-login", delivery: "stateVolume" },
    { name: "key", component: "demo-key", default: true, delivery: "env", slot: "demo-key", envName: "DEMO_KEY" },
    { name: "cert", component: "demo-cert", delivery: "file", slot: "demo-cert", mountPath: "/run/demo/cert" },
  ],
};
const repo = {
  name: "repo", title: "Repo", delivery: "git", optional: true, scopes: ["realm", "space", "stack"], secretTypes: ["ssh-key", "basic-auth"], methods: [],
  fields: [
    { name: "author", type: "person" }, { name: "hosts", type: "list", required: true },
    { name: "sign", type: "list", enum: ["commits", "tags"], secretTypes: ["ssh-key"] }, { name: "allowedSignersRef", type: "secret", secretTypes: ["ssh-key"] },
  ],
};
const pulls = { name: "pulls", title: "Pulls", delivery: "registry", scopes: ["realm"], secretTypes: ["basic-auth"], fields: [], methods: [] };
const secrets = [
  { metadata: { name: "example-default", realm: "dev" }, spec: { connector: "example-token", identity: { account: "acme" } } },
  { metadata: { name: "example-other", realm: "dev", space: "shared", stack: "services" }, spec: { type: "opaque", connector: "example-token", identity: { account: "beta", ignored: 3 } } },
  { metadata: { name: "in-cell", realm: "dev", space: "shared", stack: "services", cell: "api" }, spec: { connector: "example-token" } },
  { metadata: { name: "repo-key", realm: "dev" }, spec: { type: "ssh-key", connector: "repo", identity: { hosts: "example.com" } } },
  { metadata: { name: "untagged", realm: "dev" }, spec: {} },
];
const status = (overrides = {}) => ({ connector: "example-token", title: "Example", component: "example-token", method: "token", delivery: "env", slot: "example-token", state: "resolved", required: true, identity: { name: "example-default", realm: "dev" }, origin: "realm", ...overrides });

test("identities are the Secrets tagged with the connector, never a cell's own or an untagged one", () => {
  assert.deepEqual(identitiesFor(secrets, "example-token"), [
    { name: "example-default", realm: "dev", space: "", stack: "", type: "opaque", identity: { account: "acme" } },
    { name: "example-other", realm: "dev", space: "shared", stack: "services", type: "opaque", identity: { account: "beta" } },
  ]);
  assert.deepEqual(identitiesFor(secrets, "repo").map(identity => [identity.name, identity.type]), [["repo-key", "ssh-key"]]);
  assert.deepEqual(identitiesFor(secrets, "unknown"), []);
  assert.deepEqual(identitiesFor(secrets, ""), []);
  assert.deepEqual(identitiesFor(undefined, "example-token"), []);
});

test("secret references normalize, compare and print by scope", () => {
  assert.deepEqual(secretRef({ name: " a ", realm: "dev", space: "", stack: "ignored-without-space", cell: "x" }), { name: "a", realm: "dev" });
  assert.equal(secretRef({ name: "a" }), undefined);
  assert.equal(secretRef(undefined), undefined);
  assert.ok(sameRef({ name: "a", realm: "dev" }, { name: "a", realm: "dev", space: "" }));
  assert.ok(!sameRef({ name: "a", realm: "dev" }, { name: "a", realm: "dev", space: "s" }));
  assert.ok(!sameRef(undefined, { name: "a", realm: "dev" }));
  assert.equal(refKey(undefined), "");
  assert.equal(refLabel({ name: "a", realm: "dev", space: "s", stack: "t" }), "a — dev / s / t");
  assert.equal(formatSecretReference({ name: "a", realm: "dev", space: "s" }), "a@dev/s");
});

test("a status blocks cells when it is required and unfilled, or names an identity that cannot be used", () => {
  assert.ok(!blocksCells(status()));
  assert.ok(blocksCells(status({ state: "unresolved", identity: undefined })));
  assert.ok(!blocksCells(status({ state: "unresolved", identity: undefined, required: false })));
  assert.ok(blocksCells(status({ state: "unresolved", required: false })), "a named identity that fails blocks even an optional connector");
  assert.ok(!blocksCells(undefined));
  const config = { metadata: { name: "c", realm: "dev", labels: { "kukeon.io/team": "kukeon" } }, status: { connectors: [status(), status({ connector: "demo", title: "Demo", state: "unresolved", identity: undefined })] } };
  assert.deepEqual(blockingConnectors(config).map(item => item.connector), ["demo"]);
  assert.deepEqual(blockingConnectors({ metadata: { name: "plain" } }), []);
  assert.equal(teamOf(config), "kukeon");
  assert.equal(teamOf({ metadata: { name: "plain" } }), "");
});

test("origins read as the level that decided an identity", () => {
  assert.equal(originLabel("realm"), "Realm default");
  assert.equal(originLabel("stack"), "Stack default");
  assert.equal(originLabel("config"), "picked for this Config");
  assert.equal(originLabel("config", "kukeon"), "set by team kukeon");
  assert.equal(originLabel("cell"), "picked for this cell");
  assert.equal(originLabel(undefined), "");
});

test("scopes cut to a level, default open coordinates and respect the connector's scopes", () => {
  assert.deepEqual(targetScope({ realm: "dev" }), { realm: "dev", space: "default", stack: "default" });
  assert.deepEqual(targetScope({ realm: "dev", space: "shared", stack: "services" }), { realm: "dev", space: "shared", stack: "services" });
  const scope = { realm: "dev", space: "shared", stack: "services" };
  assert.deepEqual(scopeAt(scope, "realm"), { realm: "dev" });
  assert.deepEqual(scopeAt(scope, "space"), { realm: "dev", space: "shared" });
  assert.deepEqual(scopeAt(scope, "stack"), scope);
  assert.deepEqual(scopeFromParts(["dev", "shared"]), { realm: "dev", space: "shared" });
  assert.equal(scopeLevel({ realm: "dev", space: "shared" }), "space");
  assert.equal(scopeQuery(scope), "realm=dev&space=shared&stack=services");
  assert.equal(scopeQuery({ realm: "dev" }), "realm=dev");
  assert.deepEqual(allowedLevels(example, scope), ["realm", "space", "stack"]);
  assert.deepEqual(allowedLevels(example, { realm: "dev", space: "shared" }), ["realm", "space"]);
  assert.deepEqual(allowedLevels(pulls, scope), ["realm"]);
  assert.deepEqual(allowedLevels(undefined, { realm: "dev" }), ["realm"]);
  assert.deepEqual(connectorLevels({ scopes: ["stack", "realm"] }), ["realm", "stack"]);
  assert.deepEqual(connectorLevels({ scopes: [] }), ["realm", "space", "stack"]);
});

test("default targets follow the delivery handler: a slot per env or file method, one for git, none for a login or a registry", () => {
  assert.deepEqual(defaultTargets(example).map(target => [target.method, target.component, target.slot]), [["token", "example-token", "example-token"]]);
  assert.deepEqual(defaultTargets(demo).map(target => [target.method, target.delivery]), [["key", "env"], ["cert", "file"]]);
  assert.deepEqual(loginMethods(demo).map(method => method.name), ["login"]);
  assert.deepEqual(defaultTargets(repo), [{ method: "", component: "repo", delivery: "git", slot: "", description: "" }]);
  assert.deepEqual(defaultTargets(pulls), []);
  assert.ok(isRegistry(pulls));
  assert.ok(!isRegistry(repo) && !isRegistry(example));
  assert.ok(takesIdentity("env") && takesIdentity("file") && takesIdentity("git"));
  assert.ok(!takesIdentity("stateVolume") && !takesIdentity("registry") && !takesIdentity(undefined));
  const rows = [
    { connector: "demo", method: "key", component: "demo-key", delivery: "env", slot: "demo-key", levels: {} },
    { connector: "demo", method: "cert", component: "demo-cert", delivery: "file", slot: "demo-cert", levels: { realm: { name: "c", realm: "dev" } }, effective: { name: "c", realm: "dev" }, origin: "realm" },
    { connector: "repo", component: "repo", delivery: "git", levels: {} },
  ];
  assert.equal(defaultRow(rows, { connector: "demo", component: "demo-cert" }).origin, "realm");
  assert.equal(defaultRow(rows, { connector: "demo", method: "key" }).slot, "demo-key");
  assert.equal(defaultRow(rows, { connector: "repo", method: "" }).delivery, "git");
  assert.equal(defaultRow(rows, { connector: "example-token", component: "example-token" }), undefined);
  assert.equal(defaultRow(undefined, { connector: "demo", method: "key" }), undefined);
});

test("usage reads the count and the Configs missing an identity", () => {
  const usage = [{ connector: "example-token", configs: 3, missing: [{ config: { name: "a", realm: "dev" }, component: "example-token", delivery: "env" }] }, { connector: "demo", configs: 0, missing: [] }];
  assert.deepEqual(usageFor(usage, "example-token"), { configs: 3, missing: usage[0].missing });
  assert.deepEqual(usageFor(usage, "repo"), { configs: 0, missing: [] });
  assert.deepEqual(usageFor(undefined, "demo"), { configs: 0, missing: [] });
});

// A blueprint as `kuke team` renders it: every connector component is optional,
// the methods of one harness share a group, each carries its connector's copy.
const blueprint = {
  metadata: { name: "demo", realm: "dev" },
  spec: {
    components: [
      { kind: "harness", name: "demo", parameters: [{ name: "MODEL" }] },
      { kind: "connector", name: "demo-login", optional: true, group: "demo-auth", description: "Login.", connector: { name: "demo", title: "Demo", method: "login", delivery: "stateVolume" } },
      { kind: "connector", name: "demo-key", optional: true, group: "demo-auth", description: "Key.", connector: { name: "demo", title: "Demo", method: "key", delivery: "env", secretTypes: ["opaque"], scopes: ["realm"] }, secrets: [{ name: "demo-key", envName: "DEMO_KEY", required: true }] },
      { kind: "connector", name: "example-token", optional: true, connector: { name: "example-token", title: "Example", method: "token", delivery: "env" }, secrets: [{ name: "example-token", envName: "EXAMPLE_TOKEN", required: true }] },
      { kind: "connector", name: "repo", optional: true, connector: { name: "repo", title: "Repo", delivery: "git", optional: true } },
      { kind: "connector", name: "legacy", optional: true, secrets: [{ name: "legacy-token", required: true }] },
    ],
  },
};
const blueprintRef = { name: "demo", realm: "dev" };

test("components sharing a group become one choice; the rest toggle on their own", () => {
  const form = blueprintForm(blueprint);
  const { groups, ungrouped } = groupComponents(form.components);
  assert.deepEqual(groups.map(entry => [entry.group, entry.members.map(member => member.name)]), [["demo-auth", ["demo-login", "demo-key"]]]);
  assert.deepEqual(ungrouped.map(component => component.name), ["example-token", "repo", "legacy"]);
  assert.deepEqual(groupComponents(undefined), { groups: [], ungrouped: [] });
  // The catalog's default method starts the choice; without a catalog, the first member.
  assert.deepEqual(defaultGroupChoices(form.components, [demo, example]), { "demo-auth": "demo-key" });
  assert.deepEqual(defaultGroupChoices(form.components, undefined), { "demo-auth": "demo-login" });
  assert.deepEqual(defaultGroupChoices(form.components, [{ ...demo, methods: demo.methods.map(method => ({ ...method, default: false })) }]), { "demo-auth": "demo-login" });
});

test("a Config enables exactly one member of a group", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  assert.equal(groupChoice(form, answers, "demo-auth"), "demo-login");
  assert.deepEqual(buildSpec(blueprintRef, form, answers).components, ["demo-login"]);
  assert.ok(formSections(form, answers).some(section => section.key === "component:demo-login" && section.connector.delivery === "stateVolume"));
  assert.ok(!formSections(form, answers).some(section => section.key === "component:demo-key"));

  answers.groups = { "demo-auth": "demo-key" };
  answers.components = ["example-token", "demo-login"];
  assert.deepEqual(buildSpec(blueprintRef, form, answers).components, ["demo-key", "example-token"], "a grouped member is enabled by the group choice only");
  answers.groups = { "demo-auth": "not-a-member" };
  assert.equal(groupChoice(form, answers, "demo-auth"), "demo-login");
});

test("an identity pick becomes a fill of the connector's slot or the git identity reference; Default writes none", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  answers.groups = { "demo-auth": "demo-key" };
  answers.components = ["example-token", "repo"];
  assert.deepEqual(buildSpec(blueprintRef, form, answers), { blueprint: blueprintRef, components: ["demo-key", "example-token", "repo"] });

  answers.connectors = { "example-token": { name: "example-other", realm: "dev", space: "shared", stack: "services" }, "demo-key": { name: "demo", realm: "dev" }, repo: { name: "repo-key", realm: "dev" }, "demo-login": { name: "ignored", realm: "dev" } };
  const spec = buildSpec(blueprintRef, form, answers);
  assert.deepEqual(spec.secrets, { "demo-key": { secretRef: { name: "demo", realm: "dev" } }, "example-token": { secretRef: { name: "example-other", realm: "dev", space: "shared", stack: "services" } } });
  assert.deepEqual(spec.git, { identityRef: { name: "repo-key", realm: "dev" } });
  // An explicit git override sits next to the reference.
  answers.git = { enabled: true, author: { name: "bot", email: "bot@example.com" } };
  assert.deepEqual(buildSpec(blueprintRef, form, answers).git, { identityRef: { name: "repo-key", realm: "dev" }, author: { name: "bot", email: "bot@example.com" } });
  // A pick for a component the Config leaves off is not carried.
  answers.components = [];
  assert.equal(buildSpec(blueprintRef, form, answers).secrets["example-token"], undefined);
});

test("a connector slot is required only when neither a pick nor a scope default fills it", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  answers.groups = { "demo-auth": "demo-key" };
  answers.components = ["example-token", "repo", "legacy"];
  // A component without a connector copy keeps the plain required-slot rule.
  assert.deepEqual(missingAnswers(form, answers), ["legacy-token"]);
  assert.deepEqual(connectorsWithoutIdentity(form, answers, undefined), [], "unknown defaults report nothing");
  const defaults = [
    { connector: "demo", component: "demo-key", levels: {} },
    { connector: "example-token", component: "example-token", levels: { realm: { name: "example-default", realm: "dev" } }, effective: { name: "example-default", realm: "dev" }, origin: "realm" },
    { connector: "repo", component: "repo", levels: {} },
  ];
  assert.deepEqual(connectorsWithoutIdentity(form, answers, defaults), ["Demo"], "the optional git-delivered connector is never required");
  answers.connectors = { "demo-key": { name: "demo", realm: "dev" } };
  assert.deepEqual(connectorsWithoutIdentity(form, answers, defaults), []);
  assert.deepEqual(connectorsWithoutIdentity(form, { ...answers, connectors: {} }, []), ["Demo", "Example"]);
});

test("the missing-identity check is per component when two components bind one connector", () => {
  // A test-only connector with two components enabled together (no group); only the first has a
  // scope default, so only the second is unresolved.
  const twin = { name: "twin", title: "Twin", method: "token", delivery: "env" };
  const form = blueprintForm({
    metadata: { name: "twins", realm: "dev" },
    spec: {
      components: [
        { kind: "connector", name: "twin-a", optional: true, connector: twin, secrets: [{ name: "twin-a", envName: "TWIN_A", required: true }] },
        { kind: "connector", name: "twin-b", optional: true, connector: twin, secrets: [{ name: "twin-b", envName: "TWIN_B", required: true }] },
      ],
    },
  });
  const answers = emptyAnswers(form);
  answers.components = ["twin-a", "twin-b"];
  assert.deepEqual(formSections(form, answers).filter(section => section.connector).map(section => section.component), ["twin-a", "twin-b"], "both components are enabled on their own");
  const defaults = [
    { connector: "twin", component: "twin-a", levels: { realm: { name: "twin-default", realm: "dev" } }, effective: { name: "twin-default", realm: "dev" }, origin: "realm" },
    { connector: "twin", component: "twin-b", levels: {} },
  ];
  assert.deepEqual(componentsWithoutIdentity(form, answers, defaults), ["twin-b"]);
  assert.deepEqual(connectorsWithoutIdentity(form, answers, defaults), ["Twin"], "the title is reported once, as before");
  assert.deepEqual(componentsWithoutIdentity(form, answers, undefined), [], "unknown defaults report nothing");
  answers.connectors = { "twin-b": { name: "twin-other", realm: "dev" } };
  assert.deepEqual(componentsWithoutIdentity(form, answers, defaults), []);
});

test("only the picks that differ from the effective identity are sent as overrides", () => {
  const statuses = [
    status(),
    status({ connector: "demo", title: "Demo", component: "demo-login", method: "login", delivery: "stateVolume", slot: "", identity: undefined, required: false, volume: "demo-state" }),
    status({ connector: "repo", title: "Repo", component: "repo", method: "", delivery: "git", slot: "", required: false, identity: { name: "repo-key", realm: "dev" } }),
  ];
  assert.deepEqual(changedPicks(statuses, {}), {});
  assert.deepEqual(changedPicks(statuses, { "example-token": { name: "example-default", realm: "dev" }, repo: { name: "repo-key", realm: "dev", space: "" } }), {}, "re-picking the effective identity is no override");
  assert.deepEqual(changedPicks(statuses, { "example-token": { name: "example-other", realm: "dev", space: "shared", stack: "services" }, repo: undefined }), { "example-token": { name: "example-other", realm: "dev", space: "shared", stack: "services" } });
  assert.deepEqual(changedPicks(statuses, { "example-token": { name: "", realm: "dev" } }), {}, "a half-typed name is not sent");
  assert.deepEqual(changedPicks(statuses, { demo: { name: "x", realm: "dev" }, other: { name: "y", realm: "dev" } }), {}, "a login and an unknown connector take no identity");
  assert.deepEqual(changedPicks(undefined, { a: { name: "x", realm: "dev" } }), {});
});

test("Create stays blocked until every blocking connector has a pick", () => {
  const blocked = status({ state: "unresolved", identity: undefined, message: "no identity fills secret slot" });
  const optional = status({ connector: "repo", title: "Repo", delivery: "git", state: "unresolved", identity: undefined, required: false });
  assert.deepEqual(unresolvedConnectors([status(), optional], {}), []);
  assert.deepEqual(unresolvedConnectors([blocked, optional], {}).map(item => item.connector), ["example-token"]);
  assert.deepEqual(unresolvedConnectors([blocked], { "example-token": { name: "", realm: "dev" } }).length, 1);
  assert.deepEqual(unresolvedConnectors([blocked], { "example-token": { name: "example-other", realm: "dev" } }), []);
  // A named identity that cannot be used blocks until another one is picked.
  const broken = status({ state: "unresolved", message: "secret dev/example-default does not exist" });
  assert.equal(unresolvedConnectors([broken], { "example-token": { name: "example-default", realm: "dev" } }).length, 1);
  assert.deepEqual(unresolvedConnectors([broken], { "example-token": { name: "example-other", realm: "dev" } }), []);
});

test("the identity form is generated from the connector's types and fields", () => {
  assert.deepEqual(identityFields(example, "opaque"), [{ name: "account", type: "string", required: true, help: "Account the token belongs to.", enum: [] }]);
  assert.deepEqual(identityFields(repo, "ssh-key").map(field => [field.name, field.type]), [["author", "person"], ["hosts", "list"], ["sign", "list"], ["allowedSignersRef", "secret"]]);
  assert.deepEqual(identityFields(repo, "basic-auth").map(field => field.name), ["author", "hosts"], "a field limited to some secret types shows only for those");
  assert.deepEqual(identityFields({ fields: [{ name: "x", type: "future" }, { type: "string" }] }, "opaque").map(field => [field.name, field.type]), [["x", "string"]]);
  assert.equal(emptyIdentityForm(repo).type, "ssh-key");
  assert.equal(emptyIdentityForm({}).type, "opaque");
  // Rotation starts from the existing identity: name, type and readable fields, never material.
  const rotate = emptyIdentityForm(repo, { name: "repo-key", type: "basic-auth", identity: { hosts: "example.com" } });
  assert.deepEqual([rotate.name, rotate.type, rotate.fields, rotate.password, rotate.privateKey], ["repo-key", "basic-auth", { hosts: "example.com" }, "", ""]);
});

test("identity payloads carry the readable fields and the material of the chosen type only", () => {
  const scope = { realm: "dev", space: "shared" };
  const form = { ...emptyIdentityForm(example), name: " example-new ", fields: { account: " acme ", stray: "x" }, value: "s3cret-value", password: "stray", privateKey: "stray" };
  assert.deepEqual(identityErrors(example, form), []);
  assert.deepEqual(identityPayload(example, scope, form), { scope: { realm: "dev", space: "shared" }, name: "example-new", type: "opaque", identity: { account: "acme" }, value: "s3cret-value" });

  const key = { ...emptyIdentityForm(repo), name: "repo-key", fields: { author: "A <a@example.com>", hosts: " example.com , example.org/group ", sign: "commits,tags", allowedSignersRef: "signers@dev" }, privateKey: "-----BEGIN KEY-----\nabc\n", passphrase: "pp", knownHosts: "example.com ssh-ed25519 AAAA" };
  assert.deepEqual(identityErrors(repo, key), []);
  assert.deepEqual(identityPayload(repo, { realm: "dev" }, key), {
    scope: { realm: "dev" }, name: "repo-key", type: "ssh-key",
    identity: { author: "A <a@example.com>", hosts: "example.com,example.org/group", sign: "commits,tags", allowedSignersRef: "signers@dev" },
    privateKey: "-----BEGIN KEY-----\nabc\n", passphrase: "pp", knownHosts: "example.com ssh-ed25519 AAAA",
  });

  const basic = { ...key, type: "basic-auth", username: " bot ", password: "tok en" };
  assert.deepEqual(identityPayload(repo, { realm: "dev" }, basic), { scope: { realm: "dev" }, name: "repo-key", type: "basic-auth", identity: { author: "A <a@example.com>", hosts: "example.com,example.org/group" }, username: "bot", password: "tok en" });
  assert.deepEqual(clearMaterial(basic), { ...basic, value: "", password: "", privateKey: "", passphrase: "", publicKey: "", certificate: "", knownHosts: "" });
});

test("identity errors name what is missing or malformed without quoting material", () => {
  assert.deepEqual(identityErrors(example, emptyIdentityForm(example)), ["Name is required.", "account is required.", "Value is required."]);
  const bad = { ...emptyIdentityForm(repo), name: "k", fields: { author: "no-email", hosts: "example.com", sign: "commits,merges" }, privateKey: "" };
  assert.deepEqual(identityErrors(repo, bad), ["author must be written Name <email>.", "sign must be one of: commits, tags.", "Private key is required."]);
  assert.deepEqual(identityErrors(pulls, { ...emptyIdentityForm(pulls), name: "p" }), ["Username is required.", "Password or token is required."]);
  const secret = "s3cret-value";
  assert.ok(!identityErrors(example, { ...emptyIdentityForm(example), value: secret }).join(" ").includes(secret));
});

test("registry entries name the Secret of a Secret-backed entry and the username of a legacy one", () => {
  const realm = { metadata: { name: "dev" }, spec: { registryCredentials: [
    { serverAddress: "ghcr.io", secretRef: { name: "registry-ghcr.io", realm: "dev" } },
    { serverAddress: "legacy.example.com", username: "old", password: "" },
    { secretRef: { name: "registry-default" } },
  ] } };
  assert.deepEqual(registryEntries(realm), [
    { server: "ghcr.io", username: "", secret: { name: "registry-ghcr.io", realm: "dev" } },
    { server: "legacy.example.com", username: "old" },
    { server: "", username: "", secret: { name: "registry-default", realm: "dev" } },
  ]);
  assert.deepEqual(registryEntries({}), []);
});

test("a cell's method pick swaps the connector's delivery and is sent only when it differs (#1551)", () => {
  assert.deepEqual(methodChoices(demo).map(method => method.name), ["login", "key", "cert"]);
  assert.deepEqual(methodChoices(example), [], "a single method is no choice");
  assert.deepEqual(methodChoices(repo), [], "a connector without methods is no choice");

  const statuses = [
    { connector: "demo", title: "Demo", component: "demo-login", method: "login", delivery: "stateVolume", state: "resolved", volume: "dev-demo" },
    { connector: "repo", component: "repo", delivery: "git", state: "resolved" },
  ];
  assert.deepEqual(changedMethods(statuses, {}), {});
  assert.deepEqual(changedMethods(statuses, { demo: "login" }), {}, "re-picking the Config's method is no override");
  assert.deepEqual(changedMethods(statuses, { demo: "key", repo: "ssh" }), { demo: "key" }, "a connector without a method takes no method pick");

  assert.equal(methodStatuses(statuses, [demo, repo], {})[0], statuses[0], "no pick keeps the Config's status");
  const [picked, untouched] = methodStatuses(statuses, [demo, repo], { demo: "key" });
  assert.deepEqual(picked, { connector: "demo", title: "Demo", component: "demo-key", method: "key", delivery: "env", slot: "demo-key", state: "pending" });
  assert.equal(untouched, statuses[1]);
  assert.equal(takesIdentity(picked.delivery), true, "the picked method's identity can be chosen");
  assert.equal(blocksCells(picked), false, "a picked method resolves in the preview, which fails by itself when nothing fills it");
  assert.deepEqual(changedPicks([picked], { demo: { name: "demo-key-1", realm: "dev" } }), { demo: { name: "demo-key-1", realm: "dev" } });
  assert.equal(methodStatuses(statuses, [demo], { demo: "oauth" })[0], statuses[0], "an unknown method is left to the daemon to refuse");
});

// #1591: an opaque identity of a git-delivered connector is author-only — it
// stores no value — and such a connector can import its identity from the host.
test("a git-delivered connector's opaque identity is author-only and importable", () => {
  const authorOnly = { ...repo, secretTypes: ["ssh-key", "basic-auth", "opaque"], fields: repo.fields.map(field => field.name === "hosts" ? { ...field, secretTypes: ["ssh-key", "basic-auth"] } : field) };
  assert.equal(storesNoValue(authorOnly, "opaque"), true);
  assert.equal(storesNoValue(authorOnly, "ssh-key"), false);
  assert.equal(storesNoValue(example, "opaque"), false, "an env-delivered opaque identity is a token");
  assert.equal(emptyIdentityForm(authorOnly).type, "ssh-key", "the first type stays the default");
  const form = { ...emptyIdentityForm(authorOnly), name: "me", type: "opaque", fields: { author: "Me <me@example.com>", hosts: "github.com" }, value: "ignored" };
  assert.deepEqual(identityFields(authorOnly, "opaque").map(field => field.name), ["author"], "hosts and signing are for a key");
  assert.deepEqual(identityErrors(authorOnly, form), []);
  assert.deepEqual(identityPayload(authorOnly, { realm: "dev" }, form), { scope: { realm: "dev" }, name: "me", type: "opaque", identity: { author: "Me <me@example.com>" } });

  assert.equal(importsFromHost(repo), true);
  for (const other of [example, demo, pulls]) assert.equal(importsFromHost(other), false, other.name);
  assert.deepEqual(importPayload({ realm: " dev ", hosts: "github.com, gitlab.com/group", name: "", key: " .ssh/id_work ", passphraseFile: "", makeDefault: true }),
    { realm: "dev", makeDefault: true, hosts: ["github.com", "gitlab.com/group"], key: ".ssh/id_work" });
  assert.deepEqual(importPayload({}), { realm: "default", makeDefault: false });
});

// #1630: New Secret and Replace value write untagged Secrets of the three types.
test("the secret form validates and builds untagged Secret writes", () => {
  assert.equal(secretTypeOf({ spec: { type: "" } }), "opaque", "an opaque Secret reads back with an empty type");
  assert.equal(secretTypeOf({ spec: { type: "ssh-key" } }), "ssh-key");
  assert.equal(secretTypeOf(undefined), "opaque");
  const blank = emptySecretForm();
  assert.equal(blank.name, ""); assert.equal(blank.type, "opaque");
  const replaceForm = emptySecretForm({ metadata: { name: "login" }, spec: { type: "basic-auth" } });
  assert.equal(replaceForm.name, "login"); assert.equal(replaceForm.type, "basic-auth"); assert.equal(replaceForm.password, "");

  assert.deepEqual(secretErrors(blank), ["Name is required.", "Value is required."]);
  assert.deepEqual(secretErrors({ ...blank, name: ".types", value: "x" }), ["A name cannot start with a dot."]);
  assert.deepEqual(secretErrors({ ...blank, name: "t", type: "tls", value: "v" }), ["Type must be one of: opaque, basic-auth, ssh-key."]);
  assert.deepEqual(secretErrors({ ...blank, name: "t", value: "   " }), ["Value is required."]);
  assert.deepEqual(secretErrors({ ...replaceForm, username: "bot" }, true), ["Password or token is required."], "replace asks for no name");
  assert.deepEqual(secretErrors({ ...blank, name: "k", type: "ssh-key" }), ["Private key is required."]);

  assert.deepEqual(secretPayload({ ...blank, name: "t", value: "v\n" }), { type: "opaque", value: "v\n" }, "the name and scope travel in the URL");
  assert.deepEqual(secretPayload({ ...replaceForm, username: " bot ", password: "p w" }, true), { type: "basic-auth", replace: true, username: "bot", password: "p w" });
  assert.deepEqual(secretPayload({ ...blank, type: "ssh-key", privateKey: "KEY", knownHosts: "h k" }), { type: "ssh-key", privateKey: "KEY", knownHosts: "h k" });
  for (const body of [secretPayload({ ...blank, value: "v" }), secretPayload({ ...replaceForm, username: "u", password: "p" }, true)]) {
    assert.equal("connector" in body, false); assert.equal("identity" in body, false); assert.equal("name" in body, false);
  }
});

// #1630: registry credentials live on the Images page; the list filters by delivery handler only.
test("listedConnectors leaves out registry-delivered connectors, whatever they are called", () => {
  const renamed = { ...pulls, name: "image-pull-secrets", title: "Image pulls" };
  const withMethods = { ...pulls, name: "methods-win", methods: [{ name: "token", component: "x", delivery: "env", slot: "x" }] };
  assert.deepEqual(listedConnectors([example, pulls, repo, renamed, withMethods]).map(connector => connector.name), ["example-token", repo.name, "methods-win"]);
  assert.deepEqual(listedConnectors(undefined), []);
});
