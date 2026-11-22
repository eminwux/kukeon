import assert from "node:assert/strict";
import test from "node:test";
import { blueprintForm, buildSpec, emptyAnswers, formSections, groupGitValues, groupValuesBySource, imageChoices, imageStatus, missingAnswers, previewMode, requiredCapabilities, secretChoices } from "./config-form.mjs";

// The shape `kuke team` renders: one shared blueprint per harness × profile.
const blueprint = {
  metadata: { name: "claude", realm: "default" },
  spec: {
    requires: ["git"],
    parameters: [
      { name: "IMAGE", type: "image", default: "ghcr.io/eminwux/kukeon/claude:v1", group: "image" },
      { name: "ROLE_PATH", type: "path", required: true, group: "role" },
      { name: "INITIAL_PATH", type: "path", default: "/home/user", group: "role" },
      { name: "VERBOSE", type: "bool" },
    ],
    components: [
      {
        kind: "harness", name: "claude", requires: ["claude"],
        parameters: [{ name: "ANTHROPIC_MODEL", type: "enum", enum: ["opus", "sonnet"], default: "opus", group: "harness" }],
        secrets: [{ name: "claude-code-oauth-token", envName: "CLAUDE_CODE_OAUTH_TOKEN" }],
        modes: {
          interactive: { description: "Attachable session." },
          headless: {
            parameters: [{ name: "PROMPT", required: true }],
            secrets: [{ name: "anthropic-api-key", envName: "ANTHROPIC_API_KEY" }],
          },
        },
      },
      { kind: "connector", name: "github", optional: true, requires: ["gh"], secrets: [{ name: "github-token", envName: "GH_TOKEN", required: true }] },
      { kind: "forge", name: "github-issues", optional: true, requires: ["gh"] },
    ],
    cell: {
      containers: [{
        id: "work",
        repos: [
          { name: "role-source", target: "${ROLE_SOURCE_DIR}", required: true },
          { name: "project", target: "${PROJECT_DIR}" },
          { name: "pinned", url: "https://example.com/pinned.git", target: "/pinned" },
        ],
        secrets: [{ name: "extra-token", envName: "EXTRA_TOKEN" }],
      }],
    },
  },
};
const ref = { name: "claude", realm: "default" };

test("the form is derived from the blueprint document alone", () => {
  const form = blueprintForm(blueprint);
  assert.deepEqual(form.requires, ["git"]);
  assert.deepEqual(form.parameters.map(item => [item.name, item.type, item.required, item.hasDefault]), [
    ["IMAGE", "image", false, true], ["ROLE_PATH", "path", true, false], ["INITIAL_PATH", "path", false, true], ["VERBOSE", "bool", false, false],
  ]);
  assert.deepEqual(form.components.map(component => [component.name, component.kind, component.optional]), [["claude", "harness", false], ["github", "connector", true], ["github-issues", "forge", true]]);
  assert.deepEqual(form.modes, ["headless", "interactive"]);
  // A repo with an inline url is a value, not a slot.
  assert.deepEqual(form.repoSlots, [{ name: "role-source", target: "${ROLE_SOURCE_DIR}", required: true }, { name: "project", target: "${PROJECT_DIR}", required: false }]);
  assert.deepEqual(form.secretSlots.map(slot => slot.name), ["extra-token"]);
  assert.deepEqual(blueprintForm({}).parameters, []);
  assert.equal(blueprintForm({ spec: { parameters: [{ name: "X", type: "future" }] } }).parameters[0].type, "string");
});

test("sections group parameters by group and component and follow toggles and the picked mode", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  assert.deepEqual(formSections(form, answers).map(section => section.key), ["group:image", "group:role", "group:general", "secrets", "component:claude"]);
  assert.deepEqual(formSections(form, answers).find(section => section.key === "group:role").parameters.map(item => item.name), ["ROLE_PATH", "INITIAL_PATH"]);

  // An optional component shows nothing until the config enables it.
  answers.components = ["github"];
  assert.ok(formSections(form, answers).some(section => section.key === "component:github" && section.secrets[0].name === "github-token"));

  // A mode's parameters and secrets appear only for that mode.
  const headless = formSections(form, answers, "headless").find(section => section.kind === "mode");
  assert.deepEqual([headless.key, headless.parameters.map(item => item.name), headless.secrets.map(slot => slot.name)], ["mode:claude:headless", ["PROMPT"], ["anthropic-api-key"]]);
  assert.ok(!formSections(form, answers, "interactive").some(section => section.kind === "mode"));
});

test("the preview mode follows the daemon's pick order", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  assert.equal(previewMode(form, answers), "interactive");
  assert.equal(previewMode(form, answers, "headless"), "headless");
  answers.modes.default = "headless";
  assert.equal(previewMode(form, answers), "headless");
  answers.modes = { enabled: ["headless"], default: "" };
  assert.equal(previewMode(form, answers, "interactive"), "headless");
  assert.equal(previewMode(blueprintForm({}), emptyAnswers(blueprintForm({}))), "");
});

test("required capabilities are blueprint ∪ enabled components ∪ the config's own", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  assert.deepEqual(requiredCapabilities(form, answers), ["claude", "git"]);
  answers.components = ["github", "github-issues"];
  answers.requires = "go, node  go";
  assert.deepEqual(requiredCapabilities(form, answers), ["claude", "gh", "git", "go", "node"]);
});

test("the image picker keeps matching and unlabeled images and drops labeled images missing a capability", () => {
  const images = [
    { name: "z/unlabeled:1", capabilities: [] },
    { name: "b/claude:1", capabilities: ["claude", "git", "gh"] },
    { name: "a/claude-min:1", capabilities: ["claude", "git"] },
    { name: "c/codex:1", capabilities: ["codex", "git", "gh"] },
    { name: "d/no-field:1" },
  ];
  assert.deepEqual(imageChoices(images, ["claude", "gh", "git"]).map(choice => [choice.name, choice.status]), [["b/claude:1", "match"], ["d/no-field:1", "unknown"], ["z/unlabeled:1", "unknown"]]);
  assert.deepEqual(imageChoices(images, []).map(choice => choice.status), ["match", "match", "match", "unknown", "unknown"]);
  assert.deepEqual(imageChoices(undefined, ["git"]), []);
  assert.deepEqual(imageStatus(images, "a/claude-min:1", ["claude", "gh"]), { status: "missing", missing: ["gh"] });
  assert.equal(imageStatus(images, "z/unlabeled:1", ["gh"]).status, "unknown");
  assert.equal(imageStatus(images, "b/claude:1", ["gh"]).status, "match");
  assert.equal(imageStatus(images, "registry.example/other:1", ["gh"]).status, "absent");
});

test("the spec carries only what was answered", () => {
  const form = blueprintForm(blueprint);
  assert.deepEqual(buildSpec(ref, form, emptyAnswers(form)), { blueprint: { name: "claude", realm: "default" } });

  const answers = emptyAnswers(form);
  answers.values = { ROLE_PATH: "dev", IMAGE: "", VERBOSE: true, ANTHROPIC_MODEL: "sonnet", PROMPT: "review", UNDECLARED: "x" };
  answers.components = ["github"];
  answers.requires = "go";
  answers.prefix = " dev ";
  answers.modes = { enabled: ["interactive", "headless"], default: "interactive" };
  answers.repos = {
    "role-source": { url: " git@github.com:o/agents.git ", revision: "branch", branch: "main", ref: "ignored" },
    project: {
      url: "git@github.com:o/project.git", revision: "ref", ref: "v1.2.3", branch: "ignored",
      git: {
        enabled: true, author: { name: "bot", email: "bot@example.com" }, committer: {},
        credential: { secretRef: { name: "deploy", realm: "default" }, user: "git", port: "2222", hostKeyPolicy: "accept-new" },
        signing: { secretRef: { name: "deploy", realm: "default" } }, sign: ["tags", "commits", "other"],
      },
    },
    unknown: { url: "https://example.com/x.git" },
  };
  answers.secrets = { "github-token": { name: "gh-token", realm: "default" }, "anthropic-api-key": { name: "api", realm: "default", space: "s" }, "extra-token": { name: "", realm: "default" }, stray: { name: "x", realm: "default" } };
  assert.deepEqual(buildSpec(ref, form, answers), {
    blueprint: { name: "claude", realm: "default" },
    prefix: "dev",
    values: { ROLE_PATH: "dev", VERBOSE: "true", ANTHROPIC_MODEL: "sonnet", PROMPT: "review" },
    repos: {
      "role-source": { url: "git@github.com:o/agents.git", branch: "main" },
      project: {
        url: "git@github.com:o/project.git", ref: "v1.2.3",
        git: {
          author: { name: "bot", email: "bot@example.com" },
          credential: { secretRef: { name: "deploy", realm: "default" }, user: "git", port: 2222, hostKeyPolicy: "accept-new" },
          signing: { secretRef: { name: "deploy", realm: "default" } }, sign: ["commits", "tags"],
        },
      },
    },
    secrets: { "github-token": { secretRef: { name: "gh-token", realm: "default" } }, "anthropic-api-key": { secretRef: { name: "api", realm: "default", space: "s" } } },
    requires: ["go"],
    components: ["github"],
    modes: { default: "interactive" },
  });
});

test("answers of a disabled component or mode are not carried", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  answers.values = { PROMPT: "review" };
  answers.secrets = { "github-token": { name: "gh-token", realm: "default" }, "anthropic-api-key": { name: "api", realm: "default" } };
  answers.modes = { enabled: ["interactive"], default: "headless" };
  assert.deepEqual(buildSpec(ref, form, answers), { blueprint: { name: "claude", realm: "default" }, modes: { enabled: ["interactive"] } });
});

test("a source without its own identity and an untouched default override write no git block", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  answers.repos = { project: { url: "https://example.com/p.git", git: { enabled: false, author: { name: "x", email: "y" } } } };
  answers.git = { enabled: true, author: {}, credentials: [{ host: "", secretRef: { name: "k", realm: "default" } }] };
  assert.deepEqual(buildSpec(ref, form, answers).repos, { project: { url: "https://example.com/p.git" } });
  assert.equal(buildSpec(ref, form, answers).git, undefined);
  answers.git = { enabled: true, committer: { name: "c", email: "c@example.com" }, credentials: [{ host: "github.com", secretRef: { name: "k", realm: "default" } }], signing: { secretRef: { name: "k", realm: "default" }, allowedSignersRef: { name: "signers", realm: "default" } } };
  assert.deepEqual(buildSpec(ref, form, answers).git, {
    committer: { name: "c", email: "c@example.com" },
    credentials: [{ host: "github.com", secretRef: { name: "k", realm: "default" } }],
    signing: { secretRef: { name: "k", realm: "default" }, allowedSignersRef: { name: "signers", realm: "default" } },
  });
});

test("missing answers name required parameters, slots and repos of what is shown", () => {
  const form = blueprintForm(blueprint);
  const answers = emptyAnswers(form);
  assert.deepEqual(missingAnswers(form, answers), ["ROLE_PATH", "role-source"]);
  answers.components = ["github"];
  assert.deepEqual(missingAnswers(form, answers, "headless"), ["ROLE_PATH", "PROMPT", "github-token", "role-source"]);
  answers.values = { ROLE_PATH: "dev", PROMPT: "p" };
  answers.secrets = { "github-token": { name: "gh-token", realm: "default" } };
  answers.repos = { "role-source": { url: "https://example.com/r.git" } };
  assert.deepEqual(missingAnswers(form, answers, "headless"), []);
});

test("git values are grouped per repo with the level that decided each field", () => {
  const { repos, rest } = groupGitValues([
    { kind: "value", key: "TAG", value: "", origin: "config" },
    { kind: "git", key: "project.url", value: "git@github.com:o/p.git", origin: "config" },
    { kind: "git", key: "project.signing", value: "deploy (ssh-key)", origin: "config" },
    { kind: "git", key: "project.author", value: "bot <bot@example.com>", origin: "config" },
    { kind: "git", key: "role.source.credential", value: "git-ssh (ssh-key)", origin: "realm" },
    { kind: "git", key: "role.source.url", value: "git@github.com:o/a.git", origin: "config" },
  ]);
  assert.deepEqual(rest, [{ kind: "value", key: "TAG", value: "", origin: "config" }]);
  assert.deepEqual(repos, [
    { repo: "project", url: "git@github.com:o/p.git", fields: [{ field: "author", value: "bot <bot@example.com>", origin: "config" }, { field: "signing", value: "deploy (ssh-key)", origin: "config" }] },
    { repo: "role.source", url: "git@github.com:o/a.git", fields: [{ field: "credential", value: "git-ssh (ssh-key)", origin: "realm" }] },
  ]);
  assert.deepEqual(groupGitValues(undefined), { repos: [], defaults: [], rest: [] });
});

test("the default git identity is its own block, not a repo named default", () => {
  const { repos, defaults, rest } = groupGitValues([
    { kind: "git", key: "default.credential[github.com]", value: "git-ssh (ssh-key)", origin: "stack" },
    { kind: "git", key: "default.signing", value: "git-ssh (ssh-key)", origin: "realm" },
    { kind: "git", key: "default.credential[git.example.com/team]", value: "team (basic-auth)", origin: "config" },
    { kind: "git", key: "default.author", value: "Dev <dev@example.com>", origin: "stack" },
    { kind: "git", key: "project.author", value: "Dev <dev@example.com>", origin: "stack" },
    { kind: "git", key: "project.url", value: "git@github.com:o/p.git", origin: "config" },
  ]);
  assert.deepEqual(rest, []);
  assert.deepEqual(repos, [{ repo: "project", url: "git@github.com:o/p.git", fields: [{ field: "author", value: "Dev <dev@example.com>", origin: "stack" }] }]);
  assert.deepEqual(defaults, [
    { field: "author", host: "", value: "Dev <dev@example.com>", origin: "stack" },
    { field: "credential", host: "git.example.com/team", value: "team (basic-auth)", origin: "config" },
    { field: "credential", host: "github.com", value: "git-ssh (ssh-key)", origin: "stack" },
    { field: "signing", host: "", value: "git-ssh (ssh-key)", origin: "realm" },
  ]);
  // A repo that really is called "default" keeps its own fields.
  const named = groupGitValues([
    { kind: "git", key: "default.url", value: "https://example.com/d.git", origin: "config" },
    { kind: "git", key: "default.author", value: "Bot <bot@example.com>", origin: "config" },
    { kind: "git", key: "default.credential[example.com]", value: "c (basic-auth)", origin: "realm" },
  ]);
  assert.deepEqual(named.repos.map(repo => [repo.repo, repo.fields.map(field => field.field)]), [["default", ["author"]]]);
  assert.deepEqual(named.defaults.map(field => [field.field, field.host]), [["credential", "example.com"]]);
});

test("secret pickers are limited to the types a slot accepts", () => {
  const secrets = [
    { metadata: { name: "gh-token", realm: "default" }, spec: {} },
    { metadata: { name: "git-ssh", realm: "default" }, spec: { type: "ssh-key" } },
    { metadata: { name: "git-https", realm: "default", space: "s", stack: "t" }, spec: { type: "basic-auth" } },
    { metadata: { name: "no-realm" } },
  ];
  assert.deepEqual(secretChoices(secrets).map(choice => [choice.name, choice.type]), [["gh-token", "opaque"], ["git-ssh", "ssh-key"], ["git-https", "basic-auth"]]);
  assert.deepEqual(secretChoices(secrets, ["basic-auth", "ssh-key"]).map(choice => choice.name), ["git-ssh", "git-https"]);
  assert.deepEqual(secretChoices(secrets, ["ssh-key"]).map(choice => choice.name), ["git-ssh"]);
});

test("groupValuesBySource lists each source in order and drops empty ones (#1603)", () => {
  const values = [
    { kind: "value", key: "REGION", value: "", origin: "stack" },
    { kind: "component", key: "github", value: "", origin: "config" },
    { kind: "mode", key: "interactive", value: "", origin: "blueprint" },
    { kind: "secret", key: "token", value: "", origin: "realm" },
    { kind: "value", key: "PROMPT", value: "", origin: "cell" },
  ];
  const connectors = [{ connector: "git", method: "ssh", origin: "space" }];
  const groups = groupValuesBySource(values, connectors);
  assert.deepEqual(groups.map(group => group.source), ["blueprint", "config", "connectors", "realm", "stack", "cell"]);
  assert.deepEqual(groups.find(group => group.source === "connectors").rows, connectors);
  assert.deepEqual(groupValuesBySource([], []), []);
  assert.deepEqual(groupValuesBySource(undefined, undefined), []);
  assert.deepEqual(groupValuesBySource(values, []).map(group => group.source), ["blueprint", "config", "realm", "stack", "cell"]);
});

test("groupValuesBySource keeps git values in their origin group and an unknown origin in other (#1603)", () => {
  const values = [
    { kind: "git", key: "project.url", value: "git@example.com:a.git", origin: "config" },
    { kind: "git", key: "default.author", value: "Ada", origin: "space" },
    { kind: "image", key: "base", value: "ref", origin: "realm" },
    { kind: "value", key: "ODD", value: "", origin: "elsewhere" },
    { kind: "value", key: "NONE", value: "" },
  ];
  const groups = groupValuesBySource(values, undefined);
  assert.deepEqual(groups.map(group => group.source), ["config", "realm", "space", "other"]);
  assert.deepEqual(groups.find(group => group.source === "space").rows, [values[1]]);
  assert.deepEqual(groups.at(-1).rows.map(value => value.key), ["ODD", "NONE"]);
  // Every value appears exactly once across the groups.
  const listed = groups.flatMap(group => group.rows);
  assert.equal(listed.length, values.length);
  for (const value of values) assert.equal(listed.filter(row => row === value).length, 1);
});
