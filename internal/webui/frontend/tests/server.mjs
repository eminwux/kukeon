import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";

// Execute the packaged binary in an empty directory: no source assets available.
export async function startServer(endpoint) {
  const cwd = await mkdtemp(join(tmpdir(), "kuke-browser-"));
  const binary = resolve(process.env.KUKE_WEB_BINARY ?? "../../../kuke");
  const child = spawn(binary, ["--configuration", "/dev/null", "--host", endpoint ?? `unix://${cwd}/missing.sock`, "serve", "--listen", "127.0.0.1:0"], { cwd });
  let output = "";
  const exited = new Promise((resolve) => child.once("close", (code) => resolve(code)));
  async function stop() {
    if (child.exitCode === null) child.kill("SIGINT");
    const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
    try { await exited; } finally { clearTimeout(timer); await rm(cwd, { recursive: true, force: true }); }
  }
  try {
    const url = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`Server startup timeout: ${output}`)), 15000);
      child.once("error", (error) => { clearTimeout(timer); reject(error); });
      child.once("exit", (code) => { clearTimeout(timer); reject(new Error(`Server exited ${code}: ${output}`)); });
      child.stderr.on("data", (data) => { output += data; });
      child.stdout.on("data", (data) => {
        output += data;
        const match = output.match(/Listening on (http:\/\/\S+)/);
        if (match) { clearTimeout(timer); resolve(match[1]); }
      });
    });
    return { url, stop };
  } catch (error) { await stop(); throw error; }
}

// ---------------------------------------------------------------------------
// Mock connectors API (#1548). The browser tests run the packaged kuke binary
// against a missing daemon socket and answer the API with page.route; this is
// the stateful in-memory answer for the connector routes, so a POST has the
// consequence the daemon would give it (a default set on a Realm resolves the
// Configs that inherit it, a deleted identity leaves the list, …).
//
// Every connector here is test-only: the UI must render them from the catalog
// alone. `example-token` is the acceptance criterion's connector — env
// delivery and one readable field.
export const testConnectors = [
  {
    name: "example-token", title: "Example", description: "A test-only connector.", scopes: ["realm", "space", "stack"], secretTypes: ["opaque"],
    fields: [{ name: "account", type: "string", required: true, help: "Account the token belongs to." }],
    methods: [{ name: "token", component: "example-token", default: true, delivery: "env", slot: "example-token", envName: "EXAMPLE_TOKEN", description: "A token, delivered as EXAMPLE_TOKEN." }],
  },
  {
    name: "demo", title: "Demo", description: "How a demo harness authenticates.", group: "demo-auth", harness: "demo", scopes: ["realm", "space", "stack"], secretTypes: ["opaque"], fields: [],
    methods: [
      { name: "login", component: "demo-login", delivery: "stateVolume", description: "The login kept in the state volume." },
      { name: "key", component: "demo-key", default: true, delivery: "env", slot: "demo-key", envName: "DEMO_KEY", description: "An API key." },
    ],
  },
  {
    name: "repo", title: "Repo", description: "The identity a cell clones and signs with.", delivery: "git", optional: true, scopes: ["realm", "space", "stack"], secretTypes: ["ssh-key", "basic-auth", "opaque"], methods: [],
    fields: [
      { name: "author", type: "person", help: "Commit author, as Name <email>." },
      { name: "hosts", type: "list", required: true, secretTypes: ["ssh-key", "basic-auth"], help: "Hosts the secret authenticates." },
      { name: "sign", type: "list", enum: ["commits", "tags"], secretTypes: ["ssh-key"], help: "What to sign with the key." },
      { name: "allowedSignersRef", type: "secret", secretTypes: ["ssh-key"], help: "Secret holding the allowed-signers file." },
    ],
  },
  { name: "pulls", title: "Pulls", description: "The credential a Realm pulls private images with.", delivery: "registry", scopes: ["realm"], secretTypes: ["basic-auth"], fields: [], methods: [] },
];

// The blueprint `kuke team` would render for the demo harness: every connector
// component is optional, the demo methods share a group, and each carries a
// copy of its connector definition.
export const testBlueprint = {
  apiVersion: "v1beta1", kind: "CellBlueprint", metadata: { name: "demo", realm: "dev" },
  spec: {
    parameters: [{ name: "STATE_VOLUME", default: "demo-state" }],
    components: [
      { kind: "harness", name: "demo", modes: { interactive: {} } },
      ...testConnectors.flatMap(connector => connector.methods.length
        ? connector.methods.map(method => ({
          kind: "connector", name: method.component, optional: true, description: method.description, ...(connector.group ? { group: connector.group } : {}),
          connector: { name: connector.name, title: connector.title, method: method.name, delivery: method.delivery, secretTypes: connector.secretTypes, scopes: connector.scopes, fields: connector.fields },
          ...(method.slot ? { secrets: [{ name: method.slot, envName: method.envName, required: !connector.optional }] } : {}),
        }))
        : connector.delivery === "git" ? [{ kind: "connector", name: connector.name, optional: true, description: connector.description, connector: { name: connector.name, title: connector.title, delivery: "git", secretTypes: connector.secretTypes, scopes: connector.scopes, fields: connector.fields, optional: true } }] : []),
    ],
    cell: { containers: [{ id: "work", image: "example/demo:v1" }] },
  },
};

const levels = ["realm", "space", "stack"];
const refOf = value => value?.name ? { name: value.name, realm: value.realm, ...(value.space ? { space: value.space } : {}), ...(value.stack ? { stack: value.stack } : {}) } : undefined;
const refPath = ref => [ref.realm, ref.space, ref.stack, ref.name].filter(Boolean).join("/");
const scopePath = scope => [scope.realm, scope.space, scope.stack].filter(Boolean).join("/");
const targetKey = (connector, method) => `${connector.name}:${method?.name ?? ""}`;
const fail = (status, code, message) => ({ status, json: { error: { code, message } } });

// connectorWorld is the daemon state the mock answers from. Configs live in
// dev/shared/services and prod/shared/services, matching the spec's inventory
// fixture (realms dev and prod, space shared, stack services).
export function connectorWorld() {
  const config = (name, realm, components, extra = {}) => ({
    apiVersion: "v1beta1", kind: "CellConfig", metadata: { name, realm, space: "shared", stack: "services", ...(extra.labels ? { labels: extra.labels } : {}) },
    spec: { blueprint: { name: "demo", realm: "dev" }, components, ...(extra.secrets ? { secrets: extra.secrets } : {}), ...(extra.values ? { values: extra.values } : {}) },
  });
  return {
    connectors: structuredClone(testConnectors),
    blueprints: [structuredClone(testBlueprint)],
    secrets: [
      { name: "example-default", realm: "dev", connector: "example-token", identity: { account: "acme" } },
      { name: "example-other", realm: "dev", space: "shared", stack: "services", connector: "example-token", identity: { account: "beta" } },
      { name: "example-team", realm: "dev", connector: "example-token", identity: { account: "team" } },
      { name: "demo-key-1", realm: "dev", connector: "demo" },
      { name: "repo-key", realm: "dev", type: "ssh-key", connector: "repo", identity: { hosts: "example.com", author: "Dev <dev@example.com>" } },
      { name: "registry-ghcr.io", realm: "dev", type: "basic-auth", connector: "pulls" },
      { name: "untagged", realm: "dev" },
    ],
    // scope path → "<connector>:<method>" → Secret reference.
    defaults: {},
    configs: [
      // Requires Example and nothing fills it: no cell can be created from it.
      config("mine", "dev", ["example-token"]),
      // Team-owned: the team picked the Example identity; Demo uses its login.
      config("teams", "dev", ["demo-login", "example-token"], { labels: { "kukeon.io/team": "kukeon" }, secrets: { "example-token": { secretRef: { name: "example-team", realm: "dev" } } }, values: { STATE_VOLUME: "kukeon-dev-demo-state", STATE_VOLUME_REALM: "dev", STATE_VOLUME_SPACE: "shared", STATE_VOLUME_STACK: "services" } }),
      config("other", "prod", ["example-token"]),
    ],
    registry: { dev: [{ serverAddress: "ghcr.io", secretRef: { name: "registry-ghcr.io", realm: "dev" } }, { serverAddress: "legacy.example.com", username: "old", password: "" }] },
    // Every mutating request the page sent, in order.
    requests: [],
  };
}

function methodOf(world, component) {
  for (const connector of world.connectors) {
    const method = connector.methods.find(item => item.component === component);
    if (method) return { connector, method };
    if (!connector.methods.length && connector.name === component && connector.delivery === "git") return { connector, method: undefined };
  }
  return undefined;
}
function defaultAt(world, scope, key) {
  const paths = [[scope.realm], [scope.realm, scope.space], [scope.realm, scope.space, scope.stack]];
  const out = { levels: {} };
  paths.forEach((parts, index) => {
    if (parts.some(part => !part)) return;
    const ref = world.defaults[parts.join("/")]?.[key];
    if (ref) { out.levels[levels[index]] = ref; out.effective = ref; out.origin = levels[index]; }
  });
  return out;
}

// connectorStatuses resolves a Config's connectors the way the daemon does:
// cell override, then the Config's own fill, then the Stack, Space and Realm
// default; a named Secret must exist and carry the connector's tag.
export function connectorStatuses(world, config, overrides = {}) {
  const scope = { realm: config.metadata.realm, space: config.metadata.space || "default", stack: config.metadata.stack || "default" };
  return (config.spec.components ?? []).flatMap(component => {
    const found = methodOf(world, component);
    if (!found) return [];
    const { connector, method } = found;
    const delivery = method?.delivery ?? connector.delivery;
    const base = { connector: connector.name, title: connector.title, component, ...(method ? { method: method.name } : {}), delivery, ...(method?.slot ? { slot: method.slot } : {}) };
    if (delivery === "stateVolume") {
      const values = config.spec.values ?? {};
      const volumeRef = values.STATE_VOLUME && values.STATE_VOLUME_REALM ? { name: values.STATE_VOLUME, realm: values.STATE_VOLUME_REALM, ...(values.STATE_VOLUME_SPACE ? { space: values.STATE_VOLUME_SPACE } : {}), ...(values.STATE_VOLUME_STACK ? { stack: values.STATE_VOLUME_STACK } : {}) } : undefined;
      return [{ ...base, state: "resolved", volume: values.STATE_VOLUME ?? "", ...(volumeRef ? { volumeRef } : {}) }];
    }
    const required = !connector.optional;
    const fill = refOf(delivery === "git" ? config.spec.git?.identityRef : config.spec.secrets?.[method.slot]?.secretRef);
    const inherited = defaultAt(world, scope, targetKey(connector, method));
    const [identity, origin] = overrides[connector.name] ? [refOf(overrides[connector.name]), "cell"] : fill ? [fill, "config"] : [inherited.effective, inherited.origin];
    if (!identity) return [{ ...base, state: "unresolved", ...(required ? { required } : {}), message: method?.slot ? `no identity fills secret slot "${method.slot}" on the cell, the Config or a Realm, Space or Stack default` : "no git identity is set on the cell, the Config or a Realm, Space or Stack default" }];
    const secret = world.secrets.find(item => refPath(item) === refPath(identity));
    const problem = !secret ? `secret ${refPath(identity)} does not exist` : secret.connector !== connector.name ? `secret ${refPath(identity)}: it is not an identity of connector "${connector.name}"` : "";
    return [{ ...base, state: problem ? "unresolved" : "resolved", ...(required ? { required } : {}), identity, origin, ...(problem ? { message: problem } : {}) }];
  });
}
// withMethods applies a cell's per-cell method picks (#1551) the way the
// daemon does: the picked method's component replaces the Config's.
function withMethods(world, config, methods = {}) {
  const components = (config.spec.components ?? []).map(component => {
    const found = methodOf(world, component);
    const picked = found?.method ? methods[found.connector.name] : undefined;
    return picked ? found.connector.methods.find(method => method.name === picked)?.component ?? component : component;
  });
  return { ...config, spec: { ...config.spec, components } };
}
const blocks = status => status.state !== "resolved" && (status.required || status.identity);
const withStatus = (world, config) => ({ ...config, status: { connectors: connectorStatuses(world, config) } });
const secretDoc = secret => ({
  apiVersion: "v1beta1", kind: "Secret",
  metadata: { name: secret.name, realm: secret.realm, ...(secret.space ? { space: secret.space } : {}), ...(secret.stack ? { stack: secret.stack } : {}) },
  // Readable part only: type, connector tag and identity fields. The mock, like the server, keeps no material.
  spec: { ...(secret.type ? { type: secret.type } : {}), ...(secret.connector ? { connector: secret.connector } : {}), ...(secret.identity ? { identity: secret.identity } : {}) },
});
function renderFailure(statuses, name) {
  const broken = statuses.find(status => status.state !== "resolved" && status.identity);
  if (broken) return fail(400, "connector_invalid", `config "${name}", connector "${broken.connector}": the identity set at the ${broken.origin} level cannot be used: ${broken.message}`);
  const missing = statuses.find(blocks);
  if (missing) return fail(409, "connector_unresolved", `config "${name}" requires connector "${missing.connector}" (component "${missing.component}") and no identity fills it`);
  return undefined;
}

// mockConnectorAPI answers the connector, catalog and render routes from the
// world. Register it after the spec's inventory mock: the Realm document and
// the registry-credential route take precedence over it, everything else of
// /api/realms falls through.
export async function mockConnectorAPI(page, world) {
  const scoped = (docs, url) => docs.find(doc => doc.metadata.name === decodeURIComponent(url.pathname.split("/").filter(Boolean)[3])
    && ["realm", "space", "stack"].every(key => (doc.metadata[key] ?? "") === (url.searchParams.get(key) ?? "")));
  const record = request => {
    const url = new URL(request.url());
    const body = request.postDataJSON();
    world.requests.push({ path: url.pathname + url.search, body });
    return body;
  };
  const posts = path => world.requests.filter(request => request.path.split("?")[0] === path);
  world.posts = posts;

  await page.route(url => url.pathname === "/api/connectors" || url.pathname.startsWith("/api/connectors/"), async route => {
    const request = route.request();
    const url = new URL(request.url());
    const parts = url.pathname.split("/").filter(Boolean);
    if (request.method() === "GET" && parts.length === 2) return route.fulfill({ json: world.connectors });
    if (request.method() === "GET" && parts[2] === "status") {
      return route.fulfill({ json: world.connectors.map(connector => {
        const rows = world.configs.flatMap(config => connectorStatuses(world, config).filter(status => status.connector === connector.name).map(status => ({ config, status })));
        return { connector: connector.name, configs: rows.length, missing: rows.filter(row => blocks(row.status)).map(({ config, status }) => ({ config: { name: config.metadata.name, realm: config.metadata.realm, space: config.metadata.space, stack: config.metadata.stack }, component: status.component, method: status.method, slot: status.slot, delivery: status.delivery, message: status.message })) };
      }) });
    }
    if (request.method() === "GET" && parts[2] === "defaults") {
      const scope = Object.fromEntries(levels.map(level => [level, url.searchParams.get(level) ?? ""]));
      if (!scope.realm) return route.fulfill(fail(400, "invalid_request", "realm name is required"));
      return route.fulfill({ json: world.connectors.flatMap(connector => (connector.methods.length ? connector.methods.filter(method => method.slot) : connector.delivery === "git" ? [undefined] : []).map(method => ({
        connector: connector.name, ...(method ? { method: method.name } : {}), component: method?.component ?? connector.name, delivery: method?.delivery ?? connector.delivery, ...(method ? { slot: method.slot } : {}),
        ...defaultAt(world, scope, targetKey(connector, method)),
      }))) });
    }
    const connector = world.connectors.find(item => item.name === decodeURIComponent(parts[2]));
    if (request.method() !== "POST" || !connector) return route.fulfill(fail(400, "invalid_request", `unknown connector "${parts[2]}"`));
    const body = record(request);
    // The import of a git-delivered connector's identity from the server's
    // host (#1591): `.ssh/absent` stands for a key the server cannot read.
    if (parts[3] === "import") {
      if (connector.delivery !== "git" || connector.methods.length) return route.fulfill(fail(400, "invalid_request", `connector "${connector.name}" takes no import`));
      if (body.key === ".ssh/absent") return route.fulfill(fail(400, "invalid_request", "config error: import git identity: cannot read private key /home/operator/.ssh/absent; check its path and permissions"));
      const realm = body.realm || "default";
      const name = body.name || "git-ssh";
      const identity = { author: "Operator <operator@example.com>", committer: "Operator <operator@example.com>", hosts: (body.hosts ?? ["github.com"]).join(",") };
      const secret = { name, realm, type: "ssh-key", connector: connector.name, identity };
      const index = world.secrets.findIndex(item => refPath(item) === refPath(secret));
      if (index >= 0) world.secrets[index] = secret; else world.secrets.push(secret);
      let made;
      if (body.makeDefault) {
        const fills = world.defaults[realm] ??= {};
        const key = targetKey(connector, undefined);
        made = { realm, identity: { name, realm }, ...(fills[key] ? { previous: fills[key] } : {}), action: "updated" };
        fills[key] = refOf({ name, realm });
      }
      return route.fulfill({ json: {
        home: "/home/operator", name, realm, type: "ssh-key", identity, keyPath: "/home/operator/.ssh/id_ed25519",
        notes: ["git signing was not imported: the configured signing key is not the imported SSH key /home/operator/.ssh/id_ed25519 (a GPG key, or a different SSH key)."],
        secrets: [{ name, realm, action: index >= 0 ? "updated" : "created" }], ...(made ? { default: made } : {}),
      } });
    }
    const level = body.scope?.stack ? "stack" : body.scope?.space ? "space" : "realm";
    if (!connector.scopes.includes(level)) return route.fulfill(fail(400, "invalid_request", `connector "${connector.name}" is set at ${connector.scopes.join(" or ")} level, not ${level}`));
    if (parts[3] === "defaults") {
      const method = connector.methods.find(item => item.name === body.method) ?? (connector.methods.filter(item => item.slot).length === 1 ? connector.methods.find(item => item.slot) : undefined);
      const key = targetKey(connector, method);
      const fills = world.defaults[scopePath(body.scope)] ??= {};
      if (body.identity) fills[key] = refOf(body.identity); else delete fills[key];
      return route.fulfill({ json: { connector: connector.name, method: method?.name, scope: body.scope, level, identity: body.identity, action: "updated" } });
    }
    // identities: create, or rotate when the name exists in the scope.
    const type = body.type || connector.secretTypes[0];
    const missing = connector.fields.find(field => field.required && (!field.secretTypes || field.secretTypes.includes(type)) && !body.identity?.[field.name]);
    if (missing) return route.fulfill(fail(400, "invalid_request", `identity field "${missing.name}" is required`));
    const secret = { name: body.name, ...body.scope, ...(type !== "opaque" ? { type } : {}), connector: connector.name, ...(body.identity ? { identity: body.identity } : {}) };
    const index = world.secrets.findIndex(item => refPath(item) === refPath(secret));
    if (index >= 0) world.secrets[index] = secret; else world.secrets.push(secret);
    return route.fulfill({ json: { name: body.name, scope: body.scope, connector: connector.name, type, identity: body.identity, action: index >= 0 ? "updated" : "created" } });
  });

  await page.route("**/api/catalog/**", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const [, , kind, name, action] = url.pathname.split("/").filter(Boolean);
    const docs = kind === "secrets" ? world.secrets.map(secretDoc) : kind === "configs" ? world.configs.map(config => withStatus(world, config)) : kind === "blueprints" ? world.blueprints : [];
    if (!name) return route.fulfill({ json: docs });
    const found = scoped(docs, url);
    if (!found) return route.fulfill(fail(404, "not_found", `${kind} ${decodeURIComponent(name)} not found`));
    if (action !== "delete") return route.fulfill({ json: found });
    record(request);
    if (kind === "secrets") world.secrets = world.secrets.filter(secret => refPath(secret) !== refPath({ ...found.metadata }));
    if (kind === "configs") world.configs = world.configs.filter(config => config.metadata !== found.metadata && refPath(config.metadata) !== refPath(found.metadata));
    return route.fulfill({ json: { kind, name: found.metadata.name, deleted: true } });
  });

  // "Pick for this Config": a Config fill, refused for a team-owned Config.
  await page.route("**/api/configs/connector", async route => {
    const body = record(route.request());
    const config = world.configs.find(item => refPath(item.metadata) === refPath(body.config));
    if (!config) return route.fulfill(fail(404, "not_found", "config not found"));
    const team = config.metadata.labels?.["kukeon.io/team"];
    if (team) return route.fulfill(fail(400, "invalid_request", `config "${config.metadata.name}" is set by team "${team}"; choose the identity in the team definition or set a scope default`));
    const status = connectorStatuses(world, config).find(item => item.connector === body.connector);
    if (!status) return route.fulfill(fail(400, "invalid_request", `config "${config.metadata.name}" requires no connector "${body.connector}"`));
    if (status.delivery === "git") { if (body.identity) config.spec.git = { ...config.spec.git, identityRef: refOf(body.identity) }; else delete config.spec.git; }
    else {
      const secrets = { ...config.spec.secrets };
      if (body.identity) secrets[status.slot] = { secretRef: refOf(body.identity) }; else delete secrets[status.slot];
      if (Object.keys(secrets).length) config.spec.secrets = secrets; else delete config.spec.secrets;
    }
    await route.fulfill({ json: withStatus(world, config) });
  });

  await page.route("**/api/cells/from-config/*", async route => {
    const body = record(route.request());
    const config = world.configs.find(item => refPath(item.metadata) === refPath(body.config));
    if (!config) return route.fulfill(fail(404, "not_found", "config not found"));
    const statuses = connectorStatuses(world, withMethods(world, config, body.methods ?? {}), body.connectors ?? {});
    const failure = renderFailure(statuses, config.metadata.name);
    if (failure) return route.fulfill(failure);
    const document = { apiVersion: "v1beta1", kind: "Cell", metadata: { name: body.name || body.resolvedName || `${config.metadata.name}-abc123` }, spec: { realmId: config.metadata.realm, spaceId: config.metadata.space || "default", stackId: config.metadata.stack || "default", containers: [] } };
    if (route.request().url().endsWith("create")) return route.fulfill({ json: document });
    await route.fulfill({ json: { document, preview: "reviewed", digest: "sha256:render", values: [], connectors: statuses } });
  });

  await page.route("**/api/configs/from-blueprint/*", async route => {
    const body = record(route.request());
    const config = { apiVersion: "v1beta1", kind: "CellConfig", metadata: body.config, spec: body.spec };
    const statuses = connectorStatuses(world, config);
    const failure = renderFailure(statuses, config.metadata.name);
    if (failure) return route.fulfill(failure);
    if (route.request().url().endsWith("create")) { world.configs.push(config); return route.fulfill({ json: config }); }
    await route.fulfill({ json: { config, preview: "reviewed", digest: "sha256:render", document: { metadata: { name: `${config.metadata.name}-abc123` }, spec: { realmId: config.metadata.realm } }, values: [], connectors: statuses } });
  });

  // The Realm document carries the registry entries; saving one stores the
  // credential as a tagged Secret of the Realm and points the entry at it.
  await page.route(url => /^\/api\/realms\/[^/]+(\/registry-credentials)?$/.test(url.pathname), async route => {
    const request = route.request();
    const [, , realm, action] = new URL(request.url()).pathname.split("/").filter(Boolean).map(decodeURIComponent);
    if (!action) {
      if (request.method() !== "GET") return route.fallback();
      return route.fulfill({ json: { apiVersion: "v1beta1", kind: "Realm", metadata: { name: realm, labels: {} }, spec: { namespace: `${realm}.kukeon.io`, registryCredentials: world.registry[realm] ?? [] }, status: { state: "Ready" } } });
    }
    const body = record(request);
    const name = `registry-${body.server || "default"}`;
    const tag = world.connectors.find(connector => !connector.methods.length && connector.delivery === "registry")?.name;
    world.secrets = [...world.secrets.filter(secret => refPath(secret) !== `${realm}/${name}`), { name, realm, type: "basic-auth", connector: tag }];
    const entry = { ...(body.server ? { serverAddress: body.server } : {}), secretRef: { name, realm } };
    const entries = world.registry[realm] ?? [];
    const index = entries.findIndex(item => (item.serverAddress ?? "") === body.server);
    world.registry[realm] = index >= 0 ? entries.map((item, i) => i === index ? entry : item) : [...entries, entry];
    await route.fulfill({ json: { realm, server: body.server, username: body.username, secret: name, action: "updated" } });
  });
}
