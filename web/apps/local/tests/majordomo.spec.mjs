import { test, expect } from "@playwright/test";
import { startServer, signIn } from "./server.mjs";

// Majordomo in the web UI (#1509): the permanent sidebar entry and enable flow, the docked panel,
// the "Ask majordomo" actions and the Health page. kuke serve runs for real with no daemon; every
// API call and the attach WebSocket are answered here.

let server;
test.beforeEach(async ({ page }) => { server = await startServer(); await signIn(page); });
test.afterEach(async () => { await server?.stop(); });

const scope = ["default", "default", "default"];
const majordomoLabels = { "kukeon.io/team": "kuke", "kukeon.io/role": "majordomo" };
const sweepLabels = { ...majordomoLabels, "kukeon.io/task": "health-sweep" };

function config(name, labels, extra = {}) {
  return { apiVersion: "v1beta1", kind: "CellConfig", metadata: { name, realm: scope[0], space: scope[1], stack: scope[2], labels }, spec: { blueprint: { name: "claude-host", realm: "default" }, ...extra.spec }, ...(extra.status ? { status: extra.status } : {}) };
}
function cell(name, state, labels = {}, status = {}) {
  return {
    apiVersion: "v1beta1", kind: "Cell", metadata: { name, labels },
    spec: { realmId: scope[0], spaceId: scope[1], stackId: scope[2], containers: [{ id: "agent", attachable: true }, { id: "pause", root: true, attachable: true }], tty: { default: "agent" } },
    status: { state, ...status },
  };
}
// Configs as `kuke team init` applies them, plus three that must not be mistaken for majordomo: one
// that only has the name, one of a project team named kukeon (the init team's pre-#1527 name), and a
// sweep task config (same team and role, but a task).
function teamConfigs({ suspend = true, status } = {}) {
  return [
    config("kuke-majordomo-claude", majordomoLabels),
    config("assistant-b", majordomoLabels),
    config("kuke-majordomo-lookalike", { "kukeon.io/team": "other" }),
    config("kukeon-majordomo-claude", { "kukeon.io/team": "kukeon", "kukeon.io/role": "majordomo" }),
    config("kuke-majordomo-claude-health-sweep", { ...sweepLabels, "kukeon.io/harness": "claude" }, { spec: { schedule: { cron: "@hourly", suspend } }, status }),
  ];
}

// One mutable host: its cells and configs, the requests the page made, and the attach sockets.
// attach shapes the attach socket: promptDelay holds the prompt frame back that many ms after
// "ready"; silent sends only "ready", like a program that prints nothing.
// initConfigs is what Initialize (POST /api/teams/init, #1694) stores; logins answers GET
// /api/connectors/logins and takes the login an identity cell is started for.
async function world(page, { cells = [], configs = [], reports = [], createError, daemon, attach = {}, initConfigs, logins = [] } = {}) {
  const state = { cells: [...cells], configs, reports, requests: [], sockets: [], messages: [], createError, sweeps: [], logins, loginPosts: [] };
  const named = name => state.cells.find(item => item.metadata.name === name);
  await page.route("**/api/realms**", async route => {
    const request = route.request();
    const parts = new URL(request.url()).pathname.split("/").filter(Boolean).slice(1);
    if (request.method() === "POST") {
      state.requests.push(`POST ${parts.slice(6).join("/")}`);
      const target = named(decodeURIComponent(parts[7]));
      if (parts[8] === "start" && target) { target.status = { state: "Ready" }; return route.fulfill({ json: { action: "start", cell: target.metadata.name } }); }
      if (parts[8] === "stop" && target) { target.status = { state: "Stopped" }; return route.fulfill({ json: { action: "stop", cell: target.metadata.name } }); }
      return route.fulfill({ status: 404, json: { error: { code: "not_found", message: "cell not found" } } });
    }
    const leaf = body => route.fulfill({ json: body });
    if (parts.length === 1) return leaf([{ metadata: { name: "default" }, status: { state: "Ready" } }]);
    if (parts.length === 3) return leaf([{ metadata: { name: "default" }, status: { state: "Ready" } }]);
    if (parts.length === 5) return leaf([{ metadata: { name: "default" }, status: { state: "Ready" } }]);
    if (parts.length === 7) return leaf(state.cells);
    if (parts.length === 8) { const found = named(decodeURIComponent(parts[7])); return found ? leaf(found) : route.fulfill({ status: 404, json: { error: { code: "not_found", message: "cell not found" } } }); }
    // The Workbench grid's thumbnails (#1635): no screen to read.
    if (parts.length === 9 && parts[8] === "screenshot") return route.fulfill({ status: 404, json: { error: { code: "screen_unavailable", message: "no screen" } } });
    if (parts.length === 9 && parts[8] === "containers") return leaf([{ id: "agent", attachable: true }, { id: "pause", root: true, attachable: true }]);
    return leaf({ metadata: { name: parts.at(-1) }, status: { state: "Ready" } });
  });
  await page.route("**/api/catalog/**", async route => {
    const url = new URL(route.request().url());
    const [, , , kind, name] = url.pathname.split("/");
    const list = kind === "configs" ? state.configs
      : kind === "blueprints" ? [{ apiVersion: "v1beta1", kind: "CellBlueprint", metadata: { name: "claude-host", realm: "default" }, spec: {} }]
        : kind === "volumes" ? [{ apiVersion: "v1beta1", kind: "Volume", metadata: { name: "kuke-majordomo-claude", realm: "default", space: "default", stack: "default" }, spec: { reclaimPolicy: "Retain" } }] : [];
    if (!name) return route.fulfill({ json: list });
    const found = list.find(item => item.metadata.name === decodeURIComponent(name));
    return found ? route.fulfill({ json: found }) : route.fulfill({ status: 404, json: { error: { code: "not_found", message: "not found" } } });
  });
  await page.route("**/api/cells/from-config/*", async route => {
    const body = route.request().postDataJSON();
    const action = new URL(route.request().url()).pathname.split("/").at(-1);
    state.requests.push(`${action} ${body.config.name} as ${body.name}`);
    if (state.createError) return route.fulfill({ status: 400, json: { error: { code: "invalid_request", message: state.createError } } });
    const created = cell(body.name, "Stopped", { ...majordomoLabels, "kukeon.io/config": body.config.name });
    if (action === "preview") return route.fulfill({ json: { document: created, preview: "token-1", digest: "d1", values: {} } });
    if (body.preview !== "token-1" || named(body.name)) return route.fulfill({ status: 409, json: { error: { code: "already_exists", message: "cell already exists" } } });
    state.cells.push(created);
    return route.fulfill({ json: created });
  });
  await page.route("**/api/health/**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === "POST") { state.sweeps.push(request.postDataJSON()); return route.fulfill({ json: { cell: { metadata: { name: "kuke-majordomo-claude-health-sweep-1a2b3c" } } } }); }
    if (path === "/api/health/reports") return route.fulfill({ json: state.reports.map(report => ({ id: report.id, createdAt: report.createdAt, since: report.since, until: report.until, summary: report.summary, findings: report.findings.length, recurring: report.findings.filter(f => f.recurring).length, filed: report.findings.filter(f => f.issueUrl).length })) });
    const id = decodeURIComponent(path.split("/").at(-1));
    const found = id === "latest" ? state.reports[0] : state.reports.find(report => report.id === id);
    return found ? route.fulfill({ json: found }) : route.fulfill({ status: 404, json: { error: { code: "not_found", message: "health report not found" } } });
  });
  if (daemon) await page.route("**/api/daemon/health", route => route.fulfill({ json: daemon }));
  await page.route("**/api/teams/init", route => {
    state.requests.push("POST teams/init");
    state.configs = initConfigs ?? teamConfigs();
    return route.fulfill({ json: { teams: [{ team: "kuke", resources: 5, configs: ["kuke-majordomo-claude"], blueprints: ["claude-host"], missingSecrets: [], notes: [] }], notes: [] } });
  });
  await page.route(url => url.pathname === "/api/connectors/logins", route => route.fulfill({ json: state.logins }));
  // "Log in": the identity cell of the named state volume starts.
  await page.route(url => /^\/api\/connectors\/[^/]+\/login$/.test(url.pathname), route => {
    const body = route.request().postDataJSON();
    state.loginPosts.push({ connector: new URL(route.request().url()).pathname.split("/")[3], ...body });
    const item = state.logins.find(entry => entry.volume.name === body.name);
    item.session = { realm: scope[0], space: scope[1], stack: scope[2], cell: `login-${body.name}`, container: "login" };
    state.cells.push({ apiVersion: "v1beta1", kind: "Cell", metadata: { name: `login-${body.name}` }, spec: { realmId: scope[0], spaceId: scope[1], stackId: scope[2], containers: [{ id: "login", attachable: true }], tty: { default: "login" } }, status: { state: "Ready" } });
    return route.fulfill({ json: { identity: item, created: false } });
  });
  await page.routeWebSocket("**/attach", socket => {
    const entry = { url: new URL(socket.url()).pathname, closed: false, messages: [], send: data => socket.send(data), close: () => socket.close() };
    state.sockets.push(entry);
    socket.onMessage(message => { state.messages.push(message); entry.messages.push(message); });
    socket.onClose(() => { entry.closed = true; });
    socket.send(JSON.stringify({ type: "ready" }));
    if (attach.silent) return;
    // The harness asks for bracketed paste before it draws its prompt.
    const prompt = () => { if (!entry.closed) socket.send(Buffer.from("\x1b[?2004hmajordomo> ")); };
    if (attach.promptDelay) setTimeout(prompt, attach.promptDelay);
    else prompt();
  });
  return state;
}

const panel = page => page.getByRole("complementary", { name: "Majordomo panel" });
const entryRegion = page => page.getByRole("region", { name: "Majordomo", exact: true });
const entry = page => entryRegion(page).locator(".majordomo-entry-button");
const connection = page => panel(page).locator(".terminal-connection-state");
const pasted = state => state.messages.filter(message => Buffer.isBuffer(message)).map(message => message.toString());
const runningMajordomo = () => cell("majordomo", "Ready", { ...majordomoLabels, "kukeon.io/config": "kuke-majordomo-claude" });

test("the Majordomo sidebar entry is permanent and offers Initialize when kuke team init is needed", async ({ page }) => {
  // The only config carries the name but not the labels, so majordomo is not installed.
  const state = await world(page, { configs: [config("kuke-majordomo-claude", { team: "platform" })] });
  await page.goto(`${server.url}#/resources`);
  await expect(entry(page)).toContainText("Majordomo");
  await expect(entry(page)).toContainText("Not initialized");
  // The entry carries the button itself (#1694); the command stays in the panel as secondary text.
  await expect(entryRegion(page).getByRole("button", { name: "Initialize", exact: true })).toBeVisible();
  await expect(entryRegion(page)).not.toContainText("kuke team init");
  await page.goto(`${server.url}#/configs`);
  await expect(entry(page)).toBeVisible();
  await entry(page).click();
  await expect(panel(page)).toContainText("Majordomo is not installed on this host.");
  await expect(panel(page).locator(".initialize-cli")).toHaveText("Or run kuke team init on the host.");
  await expect(panel(page).getByRole("button", { name: "Enable majordomo" })).toHaveCount(0);
  expect(state.requests).toEqual([]);
  // Initialize from the panel applies the configs: the entry and the panel follow without a reload.
  await panel(page).getByRole("button", { name: "Initialize", exact: true }).click();
  await expect(entry(page)).toContainText("Not enabled");
  await expect(entryRegion(page).getByRole("button", { name: "Initialize", exact: true })).toHaveCount(0);
  await expect(panel(page).getByRole("button", { name: "Enable majordomo" })).toBeVisible();
  await expect(panel(page).getByLabel("Harness").locator("option")).toHaveText(["kuke-majordomo-claude", "assistant-b"]);
  expect(state.requests).toEqual(["POST teams/init"]);
});

// The majordomo Config as kukeond reports it since #1685: its harness keeps its login in a state volume,
// and status.connectors says whether that volume holds one.
const majordomoVolume = { name: "kuke-majordomo-claude", realm: scope[0], space: scope[1], stack: scope[2] };
const loginStatus = state => ({ connectors: [{ connector: "claude", title: "Claude", component: "claude-login", method: "login", delivery: "stateVolume", state: "resolved", volume: majordomoVolume.name, volumeRef: majordomoVolume, login: { state } }] });
const withLogin = state => [config("kuke-majordomo-claude", majordomoLabels, { status: loginStatus(state) }), config("assistant-b", majordomoLabels)];
const majordomoLogin = (state = "empty") => ({ connector: "claude", method: "login", volume: { ...majordomoVolume }, exists: true, tagged: true, login: { state }, usedBy: [{ name: "kuke-majordomo-claude", realm: scope[0], space: scope[1], stack: scope[2] }] });
const loginLine = page => panel(page).getByRole("status", { name: "Claude login" });

test("the majordomo panel says whether its harness is logged in and logs in through the identity cell", async ({ page }) => {
  const state = await world(page, { cells: [runningMajordomo()], configs: withLogin("empty"), logins: [majordomoLogin()] });
  await page.goto(`${server.url}#/resources`);
  await entry(page).click();
  await expect(connection(page)).toHaveText("Attached");
  await expect(loginLine(page)).toHaveText("Claude: not logged in");
  const logIn = panel(page).getByRole("button", { name: "Log in to Claude for majordomo" });
  await logIn.click();
  // Log in starts the identity cell of the majordomo state volume and opens its terminal beside the panel.
  await expect(page).toHaveURL(/#\/workspace\/default\/default\/default\/login-kuke-majordomo-claude$/);
  expect(state.loginPosts).toEqual([{ connector: "claude", name: "kuke-majordomo-claude", scope: { realm: "default", space: "default", stack: "default" }, method: "login" }]);
  await expect(panel(page)).toBeVisible();
  await expect(panel(page).getByRole("button", { name: "Log in to Claude for majordomo" })).toHaveText("Open login session");
  await expect(loginLine(page)).toHaveText("Claude: not logged in");
  // The identity cell ends: the volume holds a login, and the line follows without a refresh.
  delete state.logins[0].session;
  state.logins[0].login = { state: "logged in" };
  state.cells = state.cells.filter(item => !item.metadata.name.startsWith("login-"));
  state.configs = withLogin("logged in");
  await expect(loginLine(page)).toHaveText("Claude: logged in", { timeout: 10000 });
  await expect(panel(page).getByRole("button", { name: "Log in to Claude for majordomo" })).toHaveCount(0);
  // The majordomo session was never detached.
  await expect(connection(page)).toHaveText("Attached");
});

test("Enable majordomo asks to log in first while its harness has no login, with Continue anyway", async ({ page }) => {
  const state = await world(page, { configs: withLogin("empty"), logins: [majordomoLogin()] });
  await page.goto(`${server.url}#/resources`);
  await entry(page).click();
  // The line shows before anything is enabled, for the harness the form selects.
  await expect(loginLine(page)).toHaveText("Claude: not logged in");
  const prompt = panel(page).getByRole("group", { name: "Log in first" });
  await expect(prompt).toHaveCount(0);
  await panel(page).getByRole("button", { name: "Enable majordomo" }).click();
  await expect(prompt).toContainText("Majordomo starts without a login");
  await expect(prompt.getByRole("button", { name: "Log in to Claude for majordomo" })).toBeVisible();
  // Nothing was created yet.
  expect(state.requests).toEqual([]);
  await prompt.getByRole("button", { name: "Continue anyway" }).click();
  await expect(connection(page)).toHaveText("Attached");
  expect(state.requests).toEqual(["preview kuke-majordomo-claude as majordomo", "create kuke-majordomo-claude as majordomo", "POST cells/majordomo/start"]);
  await expect(prompt).toHaveCount(0);
});

test("Enable majordomo does not ask when the harness is logged in or reports no login", async ({ page }) => {
  const state = await world(page, { configs: withLogin("logged in"), logins: [majordomoLogin("logged in")] });
  await page.goto(`${server.url}#/resources`);
  await entry(page).click();
  await expect(loginLine(page)).toHaveText("Claude: logged in");
  await expect(panel(page).getByRole("button", { name: "Log in to Claude for majordomo" })).toHaveCount(0);
  // A Config that reports no login state volume shows no line and enables at once.
  await panel(page).getByLabel("Harness").selectOption({ label: "assistant-b" });
  await expect(loginLine(page)).toHaveCount(0);
  await panel(page).getByRole("button", { name: "Enable majordomo" }).click();
  await expect(connection(page)).toHaveText("Attached");
  expect(state.requests).toEqual(["preview assistant-b as majordomo", "create assistant-b as majordomo", "POST cells/majordomo/start"]);
});

test("Enable majordomo picks a labelled harness config, creates and starts one cell named majordomo", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs() });
  await page.goto(`${server.url}#/resources`);
  await expect(entry(page)).toContainText("Not enabled");
  await entry(page).click();
  // Configs are found by label: the lookalike name and the sweep task config are not offered.
  const harness = panel(page).getByLabel("Harness");
  await expect(harness.locator("option")).toHaveText(["kuke-majordomo-claude", "assistant-b"]);
  await harness.selectOption({ label: "assistant-b" });
  await panel(page).getByRole("button", { name: "Enable majordomo" }).click();
  await expect(connection(page)).toContainText("Attached");
  expect(state.requests).toEqual(["preview assistant-b as majordomo", "create assistant-b as majordomo", "POST cells/majordomo/start"]);
  expect(state.cells.map(item => item.metadata.name)).toEqual(["majordomo"]);
  // With a cell present there is nothing left to enable, so a second cell cannot be created.
  await expect(panel(page).getByRole("button", { name: "Enable majordomo" })).toHaveCount(0);
  await expect(entry(page)).toContainText("Ready");
  expect(state.sockets.map(socket => socket.url)).toEqual(["/api/realms/default/spaces/default/stacks/default/cells/majordomo/containers/agent/attach"]);
});

test("a creation error shows the daemon's message with the missing prerequisite", async ({ page }) => {
  const message = "config kuke-majordomo-claude: secret slot \"claude-code-oauth-token\" references secret default/claude-code-oauth-token, which does not exist";
  const state = await world(page, { configs: teamConfigs(), createError: message });
  await page.goto(`${server.url}#/resources`);
  await entry(page).click();
  await panel(page).getByRole("button", { name: "Enable majordomo" }).click();
  await expect(panel(page).getByRole("alert")).toContainText("Majordomo could not be enabled");
  await expect(panel(page).getByRole("alert")).toContainText(message);
  expect(state.cells).toEqual([]);
  await expect(panel(page).getByRole("button", { name: "Enable majordomo" })).toBeEnabled();
});

test("a stopped majordomo cell offers Start and a running one opens", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), cells: [cell("butler", "Stopped", majordomoLabels)] });
  await page.goto(`${server.url}#/resources`);
  // The cell is recognized by its labels, whatever it is called.
  await expect(entry(page)).toContainText("Stopped");
  await entry(page).click();
  await expect(panel(page)).toContainText("default / default / default / butler");
  await expect(panel(page).getByRole("button", { name: "Enable majordomo" })).toHaveCount(0);
  expect(state.sockets).toEqual([]);
  await panel(page).getByRole("button", { name: "Start" }).click();
  await expect(connection(page)).toContainText("Attached");
  expect(state.requests).toEqual(["POST cells/butler/start"]);
  expect(state.cells).toHaveLength(1);
});

test("the docked panel opens from the sidebar and the header, collapses without detaching, resizes and keeps its session across navigation", async ({ page }) => {
  // Wide enough that the stepped widths below all fit next to the 800px main column floor.
  await page.setViewportSize({ width: 1920, height: 1080 });
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Ready")] });
  await page.goto(`${server.url}#/resources`);
  await expect(panel(page)).toBeHidden();
  // Nothing attaches until the panel is opened.
  expect(state.sockets).toEqual([]);

  const toggle = page.locator(".toolbar").getByRole("button", { name: "Show majordomo panel" });
  await toggle.click();
  await expect(panel(page)).toBeVisible();
  await expect(connection(page)).toContainText("Attached");
  await expect(panel(page).locator(".xterm-rows")).toContainText("majordomo>");
  expect(state.sockets).toHaveLength(1);

  // Collapse from the panel, reopen from the sidebar entry: the same socket stays open.
  await panel(page).getByRole("button", { name: "Collapse majordomo panel" }).click();
  await expect(panel(page)).toBeHidden();
  expect(state.sockets[0].closed).toBe(false);
  await entry(page).click();
  await expect(panel(page)).toBeVisible();
  await expect(panel(page).locator(".xterm-rows")).toContainText("majordomo>");

  // Resize from the keyboard; the width is kept.
  const resizer = panel(page).getByRole("separator", { name: "Resize majordomo panel" });
  const before = Number(await resizer.getAttribute("aria-valuenow"));
  await resizer.focus();
  await page.keyboard.press("ArrowLeft");
  await page.keyboard.press("ArrowLeft");
  await expect(resizer).toHaveAttribute("aria-valuenow", String(before + 48));
  await expect.poll(async () => Math.round((await panel(page).boundingBox()).width)).toBe(before + 48);
  await page.keyboard.press("End");
  await expect(resizer).toHaveAttribute("aria-valuenow", "320");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("kukeon-majordomo-width"))).toBe("320");

  // Every page keeps the panel and the one session: lists, a catalog page, the daemon page, a
  // cell terminal and a cell's details.
  for (const hash of ["#/cells", "#/configs", "#/daemon", "#/health", "#/workspace/default/default/default/api", "#/resource/default/default/default/api", "#/resources"]) {
    await page.goto(`${server.url}${hash}`);
    await expect(panel(page)).toBeVisible();
    await expect(connection(page)).toContainText("Attached");
  }
  const majordomoSockets = state.sockets.filter(socket => socket.url.includes("/cells/majordomo/"));
  expect(majordomoSockets).toHaveLength(1);
  expect(majordomoSockets[0].closed).toBe(false);

  // The session takes input in the panel: harness login and permission prompts are answered here.
  await panel(page).locator(".terminal-session:not([hidden]) .xterm-helper-textarea").press("y");
  await expect.poll(() => pasted(state)).toContain("y");

  // A dropped connection is visible and one click reconnects.
  majordomoSockets[0].close();
  await expect(connection(page)).toContainText("Terminal connection closed");
  await panel(page).getByRole("button", { name: "Reconnect majordomo" }).click();
  await expect(connection(page)).toContainText("Attached");
  expect(state.sockets.filter(socket => socket.url.includes("/cells/majordomo/"))).toHaveLength(2);
  // No request ever stopped the cell.
  expect(state.requests).toEqual([]);
});

test("Majordomo dock never squeezes main below its floor and overlays the content when it cannot dock", async ({ page }) => {
  // No majordomo cell: the panel shows its setup, so the only terminal is the api cell's.
  const state = await world(page, { configs: teamConfigs(), cells: [cell("api", "Ready")] });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => { if (!sessionStorage.getItem("seeded")) { sessionStorage.setItem("seeded", "1"); localStorage.setItem("kukeon-majordomo-open", "true"); } });
  await page.goto(`${server.url}#/workspace/default/default/default/api`);
  const shell = page.locator(".app-shell");
  const resizer = panel(page).getByRole("separator", { name: "Resize majordomo panel" });
  const width = async selector => Math.round((await page.locator(selector).first().boundingBox()).width);
  const cols = () => state.messages.filter(message => typeof message === "string" && JSON.parse(message).type === "resize").map(message => JSON.parse(message).cols).at(-1);
  await expect(panel(page)).toBeVisible();
  await expect(page.locator(".terminal-connection-state").first()).toContainText("Attached");

  // 1440 with the default 480 preference: the panel docks at what leaves main its 800px floor, and
  // the cell terminal gets 80 columns. The stored preference is untouched.
  await expect(shell).not.toHaveClass(/majordomo-overlay/);
  await expect.poll(() => width(".majordomo-panel")).toBe(1440 - 264 - 800);
  expect(await width("main")).toBeGreaterThanOrEqual(800);
  await expect.poll(cols).toBeGreaterThanOrEqual(80);
  await expect(resizer).toHaveAttribute("aria-valuemax", String(1440 - 264 - 800));
  expect(await page.evaluate(() => localStorage.getItem("kukeon-majordomo-width"))).toBe("480");
  // Docked, the expanded terminal and the toasts keep clear of the panel.
  await page.locator(".cell-terminal-tools").getByRole("button", { name: "Expand terminal" }).click();
  await expect.poll(async () => { const box = await page.locator(".terminal-workspace.is-expanded").boundingBox(); return Math.round(box.x + box.width); }).toBe(1440 - 376 - 12);
  expect(await page.locator(".toast-stack").evaluate(el => getComputedStyle(el).right)).toBe(`${376 + 16}px`);
  await page.locator(".cell-terminal-tools").getByRole("button", { name: "Collapse terminal" }).click();
  await expect(page.locator(".terminal-workspace.is-expanded")).toHaveCount(0);

  // A stored width wider than the viewport allows is clamped on load and on window resize, and stays stored.
  await page.evaluate(() => localStorage.setItem("kukeon-majordomo-width", "960"));
  await page.reload();
  await expect(panel(page)).toBeVisible();
  await expect.poll(() => width(".majordomo-panel")).toBe(1440 - 264 - 800);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await expect.poll(() => width(".majordomo-panel")).toBe(1920 - 264 - 800);
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect.poll(() => width(".majordomo-panel")).toBe(1440 - 264 - 800);
  expect(await page.evaluate(() => localStorage.getItem("kukeon-majordomo-width"))).toBe("960");

  // 1024 and 768 cannot dock next to the floor: the panel overlays, main keeps the full width beside
  // the sidebar, and nothing is offset for the panel.
  for (const [viewport, height] of [[1024, 768], [768, 1024]]) {
    await page.setViewportSize({ width: viewport, height });
    await expect(shell).toHaveClass(/majordomo-overlay/);
    await expect.poll(() => width(".workspace")).toBe(viewport - 264);
    expect(await shell.evaluate(el => getComputedStyle(el).paddingRight)).toBe("0px");
    expect(await page.locator(".toast-stack").evaluate(el => getComputedStyle(el).right)).toBe("16px");
    expect(await page.locator(".cell-page-title-row h1").evaluate(el => el.getBoundingClientRect().height)).toBeLessThan(40);
    // The overlaid panel covers the title row's right end, so its controls are used from the keyboard here.
    await page.locator(".cell-terminal-tools").getByRole("button", { name: "Expand terminal" }).press("Enter");
    await expect.poll(async () => { const box = await page.locator(".terminal-workspace.is-expanded").boundingBox(); return Math.round(box.x + box.width); }).toBe(viewport - 12);
    await page.locator(".cell-terminal-tools").getByRole("button", { name: "Collapse terminal" }).press("Enter");
    await page.goto(`${server.url}#/cells`);
    await expect(page.locator(".workspace table")).toBeVisible();
    expect(await width(".workspace table")).toBeGreaterThan(400);
    await page.goto(`${server.url}#/workspace/default/default/default/api`);
  }
  expect(await page.evaluate(() => localStorage.getItem("kukeon-majordomo-width"))).toBe("960");
});

// #1777, A: a cell in the panel also opens in the main view, each view over its own WebSocket.
const resizes = socket => socket.messages.filter(message => typeof message === "string" && JSON.parse(message).type === "resize");
const mainTools = page => page.locator(".cell-terminal-tools");
test("majordomo's own cell screen attaches it too: two sessions, each detached, reconnected and sized on its own", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem("kukeon-majordomo-open", "true"));
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo()] });
  await page.goto(`${server.url}#/workspace/default/default/default/majordomo`);
  const workspace = page.getByRole("region", { name: "Terminal workspace" });
  await expect(connection(page)).toContainText("Attached");
  await expect(mainTools(page).locator(".terminal-connection-state")).toHaveClass(/attached/);
  await expect(workspace).not.toContainText("docked");
  await expect(workspace.locator(".xterm")).toBeVisible();
  await expect.poll(() => state.sockets.map(socket => socket.url)).toEqual([attachPath("majordomo"), attachPath("majordomo")]);
  // The cell is in the panel already: its screen offers no Open in side panel, and keeps Exec and its lifecycle controls.
  await expect(mainTools(page).getByRole("button", { name: "Open in side panel" })).toHaveCount(0);
  await expect(mainTools(page).getByRole("button", { name: "Exec", exact: true })).toBeVisible();
  // Each view sends its size when it attaches.
  await expect.poll(() => state.sockets.every(socket => resizes(socket).length > 0)).toBe(true);

  // Detach in the panel closes the panel's session only.
  await panel(page).getByRole("button", { name: "Detach majordomo" }).click();
  await expect(connection(page)).toContainText("Detached");
  await expect.poll(() => state.sockets.filter(socket => socket.closed)).toHaveLength(1);
  const main = state.sockets.find(socket => !socket.closed);
  await expect(mainTools(page).locator(".terminal-connection-state")).toHaveClass(/attached/);
  await panel(page).getByRole("button", { name: "Reconnect majordomo" }).click();
  await expect(connection(page)).toContainText("Attached");
  await expect.poll(() => state.sockets).toHaveLength(3);
  const side = state.sockets[2];
  expect(main.closed).toBe(false);
  // Detach in the main view leaves the panel's session alone.
  await mainTools(page).getByRole("button", { name: "Detach" }).click();
  await expect.poll(() => main.closed).toBe(true);
  expect(side.closed).toBe(false);
  await expect(connection(page)).toContainText("Attached");
  await mainTools(page).getByRole("button", { name: "Attach" }).click();
  await expect.poll(() => state.sockets).toHaveLength(4);
  const mainAgain = state.sockets[3];
  await expect(mainTools(page).locator(".terminal-connection-state")).toHaveClass(/attached/);

  // Taking focus sends the size again even when it did not change, so the view in use sets it; the
  // other view keeps working and shows no error.
  await expect.poll(() => resizes(side).length).toBeGreaterThan(0);
  const sideBefore = resizes(side).length;
  await panel(page).locator(".xterm").click();
  await expect.poll(() => resizes(side).length).toBeGreaterThan(sideBefore);
  await expect.poll(() => resizes(mainAgain).length).toBeGreaterThan(0);
  const mainBefore = resizes(mainAgain).length;
  await workspace.locator(".xterm").click();
  await expect.poll(() => resizes(mainAgain).length).toBeGreaterThan(mainBefore);
  expect(JSON.parse(resizes(mainAgain).at(-1))).toEqual(expect.objectContaining({ cols: expect.any(Number), rows: expect.any(Number) }));
  // Opening the panel again makes its terminal the shown one: it sends its size too.
  const reopened = resizes(side).length;
  await panel(page).getByRole("button", { name: "Collapse majordomo panel" }).click();
  await page.getByRole("button", { name: "Show majordomo panel" }).click();
  await expect.poll(() => resizes(side).length).toBeGreaterThan(reopened);
  await expect(connection(page)).toContainText("Attached");
  await expect(panel(page).getByRole("alert")).toHaveCount(0);
  await expect(workspace.getByRole("alert")).toHaveCount(0);

  // A lifecycle action interrupts the cell's sessions in both views.
  await page.locator(".cell-page-title-row").getByRole("button", { name: "Stop", exact: true }).click();
  await expect.poll(() => [side.closed, mainAgain.closed]).toEqual([true, true]);
  await expect(connection(page)).toContainText("Cell is Stopped");
  await expect(workspace).toContainText("Cell is Stopped");
});

test("Ask majordomo pastes one unsubmitted bracketed paste carrying the cell's scoped identity and state", async ({ page }) => {
  const failed = cell("api", "Failed", {}, { reason: "CreateFailed", message: "cni attach failed:\n netns missing" });
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), failed] });
  await page.goto(`${server.url}#/resource/default/default/default/api`);
  // On a failure state the action is a visible button, not a menu item, in the title row and in
  // the status panel.
  const ask = page.locator(".cell-page-title-row").getByRole("button", { name: "Ask majordomo about cell default / default / default / api" });
  await expect(ask).toBeVisible();
  await expect(ask).toHaveClass(/is-prominent/);
  await expect(page.locator(".status-ask").getByRole("button", { name: /Ask majordomo/ })).toBeVisible();
  await expect(panel(page)).toBeHidden();

  await ask.click();
  await expect(panel(page)).toBeVisible();
  await expect(connection(page)).toContainText("Attached");
  await expect.poll(() => pasted(state).filter(message => message.includes("\x1b[200~"))).toHaveLength(1);
  const paste = pasted(state).find(message => message.includes("\x1b[200~"));
  expect(paste.startsWith("\x1b[200~")).toBe(true);
  expect(paste.endsWith("\x1b[201~")).toBe(true);
  const text = paste.slice(6, -6);
  expect(text).toBe("About the cell api in realm default, space default, stack default (state Failed; reason: CreateFailed; message: cni attach failed: netns missing): what is going on with it, and what should I do?");
  // Nothing submitted it: no carriage return or newline was ever sent.
  expect(pasted(state).join("")).not.toMatch(/[\r\n]/);
  await expect(panel(page).locator(".majordomo-question")).toHaveCount(0);
});

const askAbout = async (page, name) => {
  await page.goto(`${server.url}#/resource/default/default/default/${name}`);
  await page.locator(".cell-page-title-row").getByRole("button", { name: `Ask majordomo about cell default / default / default / ${name}` }).click();
  await expect(panel(page)).toBeVisible();
  await expect(connection(page)).toContainText("Attached");
};
const question = name => `About the cell ${name} in realm default, space default, stack default (state Failed): what is going on with it, and what should I do?`;

test("Ask majordomo waits for a prompt that arrives after ready and pastes it bracketed", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Failed")], attach: { promptDelay: 300 } });
  await askAbout(page, "api");
  await expect.poll(() => pasted(state)).toHaveLength(1);
  expect(pasted(state)[0]).toBe(`\x1b[200~${question("api")}\x1b[201~`);
  await page.waitForTimeout(500);
  expect(pasted(state)).toHaveLength(1);
});

test("Ask majordomo still pastes, unbracketed, into a program that prints nothing", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Failed")], attach: { silent: true } });
  await askAbout(page, "api");
  const attachedAt = Date.now();
  await page.waitForTimeout(1000);
  expect(pasted(state)).toHaveLength(0);
  await expect.poll(() => pasted(state), { timeout: 5000 }).toHaveLength(1);
  expect(Date.now() - attachedAt).toBeGreaterThanOrEqual(1000);
  expect(pasted(state)[0]).toBe(question("api"));
  await page.waitForTimeout(300);
  expect(pasted(state)).toHaveLength(1);
});

test("a second Ask on an attached, idle majordomo session pastes once, bracketed", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Failed"), cell("web", "Failed")] });
  await askAbout(page, "api");
  await expect.poll(() => pasted(state)).toHaveLength(1);
  await askAbout(page, "web");
  await expect.poll(() => pasted(state)).toHaveLength(2);
  expect(pasted(state)[1]).toBe(`\x1b[200~${question("web")}\x1b[201~`);
  expect(state.sockets).toHaveLength(1);
  await page.waitForTimeout(500);
  expect(pasted(state)).toHaveLength(2);
});

test("Ask majordomo is on the cell list's failed rows and on config, blueprint, volume and daemon views", async ({ page }) => {
  // Docked, not overlaid: below 1384px the open panel would cover the next view's Ask button.
  await page.setViewportSize({ width: 1920, height: 1080 });
  const daemon = { status: "unavailable", endpoint: "unix:///run/kukeon/kukeond.sock", serverVersion: "v0.11.0", checkedAt: "2026-11-17T10:00:00Z", latencyMs: 3, error: { code: "unavailable", message: "dial unix /run/kukeon/kukeond.sock: connect: connection refused" } };
  const state = await world(page, { configs: teamConfigs(), daemon, cells: [runningMajordomo(), cell("api", "Error"), cell("web", "Degraded"), cell("ok", "Ready")] });
  const question = async () => { await expect.poll(() => pasted(state).filter(message => message.includes("\x1b[200~")).length).toBeGreaterThan(0); const last = pasted(state).filter(message => message.includes("\x1b[200~")).at(-1); state.messages.length = 0; return last.slice(6, -6); };

  await page.goto(`${server.url}#/cells`);
  const rows = page.getByRole("region", { name: "Cells list" });
  await expect(rows.getByRole("button", { name: /Ask majordomo about cell/ })).toHaveCount(2);
  await rows.getByRole("button", { name: "Ask majordomo about cell default / default / default / web" }).click();
  expect(await question()).toContain("About the cell web in realm default, space default, stack default (state Degraded)");

  await page.goto(`${server.url}#/configs/default/default/default/kuke-majordomo-claude-health-sweep`);
  await page.getByRole("button", { name: "Ask majordomo about CellConfig default / default / default / kuke-majordomo-claude-health-sweep" }).click();
  expect(await question()).toBe("About the CellConfig kuke-majordomo-claude-health-sweep in realm default, space default, stack default (stored, blueprint claude-host, schedule @hourly (suspended)): explain what it is for and check whether anything is wrong with it.");

  await page.goto(`${server.url}#/blueprints/default/claude-host`);
  await page.getByRole("button", { name: "Ask majordomo about CellBlueprint default / claude-host" }).click();
  expect(await question()).toBe("About the CellBlueprint claude-host in realm default (stored): explain what it is for and check whether anything is wrong with it.");

  await page.goto(`${server.url}#/volumes/default/default/default/kuke-majordomo-claude`);
  await page.getByRole("button", { name: "Ask majordomo about volume default / default / default / kuke-majordomo-claude" }).click();
  expect(await question()).toContain("About the volume kuke-majordomo-claude in realm default, space default, stack default (stored, reclaim policy Retain)");

  await page.goto(`${server.url}#/daemon`);
  const daemonAsk = page.getByRole("region", { name: "Daemon connectivity" }).getByRole("button", { name: "Ask majordomo about the kukeond daemon" });
  await expect(daemonAsk).toHaveClass(/is-prominent/);
  await daemonAsk.click();
  const asked = await question();
  expect(asked).toContain("About the kukeond daemon at unix:///run/kukeon/kukeond.sock (connectivity");
  expect(asked).toContain("error: dial unix /run/kukeon/kukeond.sock: connect: connection refused");
});

test("a question asked before majordomo runs waits in the panel and can be discarded", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), cells: [cell("api", "Failed")] });
  await page.goto(`${server.url}#/resource/default/default/default/api`);
  await page.locator(".cell-page-title-row").getByRole("button", { name: /Ask majordomo/ }).click();
  await expect(panel(page).locator(".majordomo-question")).toContainText("Waiting to paste: About the cell api");
  await expect(panel(page).getByRole("button", { name: "Enable majordomo" })).toBeVisible();
  await panel(page).getByRole("button", { name: "Discard" }).click();
  await expect(panel(page).locator(".majordomo-question")).toHaveCount(0);
  expect(state.messages).toEqual([]);
});

const reports = [
  {
    id: "20261117T100000.000000000Z", createdAt: "2026-11-17T10:00:00Z", since: "2026-11-17T09:00:00Z", until: "2026-11-17T10:00:00Z",
    cell: { realm: "default", space: "default", stack: "default", name: "kuke-majordomo-claude-health-sweep-aa11bb" },
    summary: "Three findings, one kukeon defect.", body: "# Health sweep\n\nThe api cell cannot attach to the network.",
    findings: [
      { signature: "cell default/default/default/api Failed", classification: "kukeon", severity: "error", recurring: true, title: "api fails to start: CNI attach error", evidence: ["status.reason: CreateFailed"], suggestedCommands: ["kuke restart api --realm default --space default --stack default"], issueUrl: "https://github.com/acme/ops/issues/7" },
      { signature: "cell default/default/default/job Error", classification: "workload", severity: "warning", recurring: false, evidence: ["exit code 3"] },
      { signature: "check state/run-dir", classification: "unclear", severity: "info", recurring: false, evidence: ["orphan entries: x.sock"] },
    ],
  },
  {
    id: "20261117T090000.000000000Z", createdAt: "2026-11-17T09:00:00Z", since: "2026-11-17T08:00:00Z", until: "2026-11-17T09:00:00Z",
    cell: { realm: "default", space: "default", stack: "default", name: "sweep-old" }, summary: "One kukeon defect.",
    findings: [{ signature: "cell default/default/default/api Failed", classification: "kukeon", severity: "error", recurring: false, evidence: ["status.reason: CreateFailed"] }],
  },
];

// #1630, item H: every majordomo cell is in the panel by default, one tab each; #1777: any other cell can
// be added, any cell removed.
const attachPath = name => `/api/realms/default/spaces/default/stacks/default/cells/${name}/containers/agent/attach`;
const majordomoCell = (name, configName, createdAt) => cell(name, "Ready", { ...majordomoLabels, "kukeon.io/config": configName }, createdAt ? { createdAt } : {});
const tabs = page => panel(page).getByRole("tablist", { name: "Panel cells" }).getByRole("tab");
const bracketed = socket => socket.messages.filter(message => Buffer.isBuffer(message) && message.toString().includes("\x1b[200~"));

test("each majordomo cell is a tab ordered by creation; switching keeps every session and Ask pastes into the active one", async ({ page }) => {
  const failed = cell("api", "Failed");
  // majordomo sorts first by name, but assistant-b was created first.
  const state = await world(page, { configs: teamConfigs(), cells: [majordomoCell("majordomo", "kuke-majordomo-claude", "2026-11-02T10:00:00Z"), majordomoCell("assistant-b", "assistant-b", "2026-11-01T10:00:00Z"), failed] });
  await page.goto(`${server.url}#/resources`);
  await expect(page.getByRole("region", { name: "Majordomo", exact: true }).locator(".majordomo-count")).toHaveText("2");
  await entry(page).click();
  await expect(tabs(page)).toHaveText([/^assistant-b/, /^kuke-majordomo-claude/]);
  await expect(tabs(page).nth(0)).toHaveAttribute("aria-selected", "true");
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "false");
  await expect(panel(page).locator("#majordomo-screens")).toHaveAttribute("role", "tabpanel");
  await expect(connection(page)).toContainText("Attached");
  expect(state.sockets.map(socket => socket.url)).toEqual([attachPath("assistant-b")]);

  // Switching attaches the other cell and keeps the first session open behind it.
  await tabs(page).nth(1).click();
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(connection(page)).toContainText("Attached");
  await expect.poll(() => state.sockets.map(socket => socket.url)).toEqual([attachPath("assistant-b"), attachPath("majordomo")]);
  // The keyboard moves between tabs; going back re-attaches nothing.
  await tabs(page).nth(1).press("ArrowLeft");
  await expect(tabs(page).nth(0)).toHaveAttribute("aria-selected", "true");
  await expect(tabs(page).nth(0)).toBeFocused();
  await tabs(page).nth(0).press("End");
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "true");
  await tabs(page).nth(1).press("Home");
  await expect(tabs(page).nth(0)).toHaveAttribute("aria-selected", "true");
  expect(state.sockets).toHaveLength(2);
  expect(state.sockets.map(socket => socket.closed)).toEqual([false, false]);

  // A majordomo cell that appears later, even one that sorts first, does not take the panel.
  state.cells.push(majordomoCell("early", "kuke-majordomo-claude", "2026-10-01T10:00:00Z"));
  // The overlaid panel covers the toolbar at this width, so Refresh is used from the keyboard.
  await page.getByRole("button", { name: "Refresh" }).press("Enter");
  // Two cells of one Config are labelled by cell name.
  await expect(tabs(page)).toHaveText([/^early/, /^assistant-b/, /^majordomo/]);
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(tabs(page).nth(1)).toContainText("assistant-b");

  // Ask pastes into the active tab's session only.
  await page.goto(`${server.url}#/resource/default/default/default/api`);
  await page.locator(".cell-page-title-row").getByRole("button", { name: "Ask majordomo about cell default / default / default / api" }).click();
  await expect.poll(() => bracketed(state.sockets[0]).length).toBe(1);
  expect(bracketed(state.sockets[1])).toHaveLength(0);
  expect(state.sockets).toHaveLength(2);

  // A panel cell's own screen opens a second terminal for it, over its own WebSocket (#1777); the
  // panel's sessions stay open.
  for (const [index, name] of ["assistant-b", "majordomo"].entries()) {
    await page.goto(`${server.url}#/workspace/default/default/default/${name}`);
    await expect(page.locator(".cell-terminal-tools .terminal-connection-state")).toHaveClass(/attached/);
    await expect.poll(() => state.sockets.length).toBe(3 + index);
    expect(state.sockets.at(-1).url).toBe(attachPath(name));
  }
  expect(state.sockets.map(socket => socket.closed)).toEqual([false, false, false, false]);

  // The choice survives a reload.
  await page.reload();
  await entry(page).click();
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(tabs(page).nth(1)).toContainText("assistant-b");
});

test("Add majordomo offers only the harness configs no cell was created from and opens the new cell in its own tab", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), cells: [majordomoCell("majordomo", "kuke-majordomo-claude", "2026-11-01T10:00:00Z")] });
  await page.goto(`${server.url}#/resources`);
  await entry(page).click();
  await expect(connection(page)).toContainText("Attached");
  // One cell: no tab strip.
  await expect(panel(page).getByRole("tablist")).toHaveCount(0);
  await panel(page).getByRole("button", { name: "Add majordomo" }).click();
  const add = panel(page).getByRole("group", { name: "Add majordomo" });
  await expect(add.getByLabel("Harness").locator("option")).toHaveText(["assistant-b"]);
  await add.getByRole("button", { name: "Add majordomo" }).click();
  await expect.poll(() => state.requests).toEqual(["preview assistant-b as assistant-b", "create assistant-b as assistant-b", "POST cells/assistant-b/start"]);
  await expect(tabs(page)).toHaveText([/^kuke-majordomo-claude/, /^assistant-b/]);
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(connection(page)).toContainText("Attached");
  await expect.poll(() => state.sockets.map(socket => socket.url)).toEqual([attachPath("majordomo"), attachPath("assistant-b")]);
  expect(state.sockets[0].closed).toBe(false);
  // Every harness config now has a cell: nothing is left to add.
  await expect(panel(page).getByRole("button", { name: "Add majordomo" })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Majordomo", exact: true }).locator(".majordomo-count")).toHaveText("2");
});

const cellKey = name => JSON.stringify([...scope, name]);
const membership = value => ({ key: "kukeon-panel-cells", value: JSON.stringify(value) });
for (const [width, height] of [[1440, 900], [390, 844]]) test(`four tabs, two majordomo and two other cells, fit the panel at ${width}×${height} without scrolling the page sideways`, async ({ page }) => {
  await page.setViewportSize({ width, height });
  const stored = membership({ added: [cellKey("api"), cellKey("frontend-with-a-rather-long-cell-name")], removed: [] });
  await page.addInitScript(item => { localStorage.setItem("kukeon-majordomo-open", "true"); localStorage.setItem(item.key, item.value); }, stored);
  await world(page, { configs: teamConfigs(), cells: [
    majordomoCell("majordomo", "kuke-majordomo-claude", "2026-11-01T10:00:00Z"),
    majordomoCell("assistant-b", "assistant-b", "2026-11-02T10:00:00Z"),
    cell("api", "Ready"),
    cell("frontend-with-a-rather-long-cell-name", "Ready"),
  ] });
  await page.goto(`${server.url}#/resources`);
  await expect(panel(page)).toBeVisible();
  // Majordomo cells first, by Config; then the added cells, by name, in the order added.
  await expect(tabs(page)).toHaveText([/^kuke-majordomo-claude/, /^assistant-b/, /^api/, /^frontend-with-a-rather-long-cell-name/]);
  await expect(tabs(page).nth(3)).toHaveAttribute("title", /^default \/ default \/ default \/ frontend-with-a-rather-long-cell-name/);
  const box = await panel(page).boundingBox();
  for (let i = 0; i < 4; i++) {
    await expect(tabs(page).nth(i)).toBeVisible();
    const tab = await tabs(page).nth(i).boundingBox();
    expect(tab.x).toBeGreaterThanOrEqual(box.x - 1);
    expect(tab.x + tab.width).toBeLessThanOrEqual(box.x + box.width + 1);
    expect(tab.height).toBeGreaterThanOrEqual(24);
  }
  // Every header control fits the panel too.
  const controls = panel(page).locator(".majordomo-header button");
  for (let i = 0; i < await controls.count(); i++) {
    const control = await controls.nth(i).boundingBox();
    expect(control.x).toBeGreaterThanOrEqual(box.x - 1);
    expect(control.x + control.width).toBeLessThanOrEqual(box.x + box.width + 1);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
});

// #1777, B: panel membership, kept per browser.
test("Add cell adds any cell, Remove takes any out, majordomo included, and the choice survives a reload", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Ready"), cell("web", "Stopped")] });
  await page.goto(`${server.url}#/resources`);
  await entry(page).click();
  await expect(connection(page)).toContainText("Attached");
  await expect(panel(page).getByRole("tablist")).toHaveCount(0);
  // Add cell and Add majordomo are separate controls.
  await expect(panel(page).getByRole("button", { name: "Add majordomo", exact: true })).toBeVisible();
  await panel(page).getByRole("button", { name: "Add cell", exact: true }).click();
  const picker = panel(page).getByRole("group", { name: "Add cell to the panel" });
  await expect(picker.getByLabel("Cell").locator("option")).toHaveText(["default / default / default / api", "default / default / default / web"]);
  await picker.getByLabel("Cell").selectOption({ label: "default / default / default / api" });
  await picker.getByRole("button", { name: "Add to panel" }).click();
  // The added cell's tab shows its name and the full scope as its title, and is the active one.
  await expect(tabs(page)).toHaveText([/^kuke-majordomo-claude/, /^api/]);
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(tabs(page).nth(1)).toHaveAttribute("title", /^default \/ default \/ default \/ api/);
  await expect(connection(page)).toContainText("Attached");
  await expect.poll(() => state.sockets.map(socket => socket.url)).toEqual([attachPath("majordomo"), attachPath("api")]);
  await expect(panel(page).getByRole("button", { name: "Detach api" })).toBeVisible();

  // A stopped cell shows its state and Start, and the texts do not call it majordomo.
  await panel(page).getByRole("button", { name: "Add cell", exact: true }).click();
  await expect(picker.getByLabel("Cell").locator("option")).toHaveText(["default / default / default / web"]);
  await picker.getByRole("button", { name: "Add to panel" }).click();
  await expect(tabs(page)).toHaveText([/^kuke-majordomo-claude/, /^api/, /^web/]);
  await expect(panel(page)).toContainText("The cell default / default / default / web is");
  await expect(panel(page)).not.toContainText("The majordomo cell");
  await expect(panel(page).getByRole("button", { name: "Start" })).toBeVisible();
  // Every cell is offered now: no Add cell.
  await expect(panel(page).getByRole("button", { name: "Add cell", exact: true })).toHaveCount(0);

  // Removing a cell closes the panel's session only.
  await tabs(page).nth(1).click();
  await panel(page).getByRole("button", { name: "Remove api from the side panel" }).click();
  await expect(tabs(page)).toHaveText([/^kuke-majordomo-claude/, /^web/]);
  await expect.poll(() => state.sockets[1].closed).toBe(true);
  expect(state.sockets[0].closed).toBe(false);
  // The majordomo cell can be removed too, and the last cell as well.
  await tabs(page).nth(0).click();
  await panel(page).getByRole("button", { name: "Remove majordomo from the side panel" }).click();
  await expect.poll(() => state.sockets[0].closed).toBe(true);
  await expect(tabs(page)).toHaveText([/^web/]);
  await panel(page).getByRole("button", { name: "Remove web from the side panel" }).click();
  await expect(panel(page)).toContainText("The side panel holds no cells.");
  await expect(connection(page)).toContainText("No cell in the panel");
  // Removing majordomo never offers its Config again; the sidebar entry still reports it.
  await panel(page).getByRole("button", { name: "Add majordomo", exact: true }).click();
  await expect(panel(page).getByRole("group", { name: "Add majordomo" }).getByLabel("Harness").locator("option")).toHaveText(["assistant-b"]);
  await panel(page).getByRole("group", { name: "Add majordomo" }).getByRole("button", { name: "Cancel" }).click();
  await expect(entry(page)).toContainText("Ready");

  // The choice survives a reload, and is stored per browser.
  await page.reload();
  await expect(panel(page)).toContainText("The side panel holds no cells.");
  expect(JSON.parse(await page.evaluate(() => localStorage.getItem("kukeon-panel-cells")))).toEqual({ added: [], removed: [cellKey("majordomo")] });
  // The empty state's Add cell brings majordomo back.
  await panel(page).getByRole("button", { name: "Add cell", exact: true }).click();
  await picker.getByLabel("Cell").selectOption({ label: "default / default / default / majordomo" });
  await picker.getByRole("button", { name: "Add to panel" }).click();
  await expect(connection(page)).toContainText("Attached");
  await expect(panel(page).getByRole("tablist")).toHaveCount(0);
  await page.reload();
  await expect(connection(page)).toContainText("Attached");
  expect(JSON.parse(await page.evaluate(() => localStorage.getItem("kukeon-panel-cells")))).toEqual({ added: [], removed: [] });
});

test("entries for cells a complete inventory no longer has are pruned", async ({ page }) => {
  const stored = membership({ added: [cellKey("api"), cellKey("gone")], removed: [cellKey("old")] });
  await page.addInitScript(item => { if (!sessionStorage.getItem("seeded")) { sessionStorage.setItem("seeded", "1"); localStorage.setItem(item.key, item.value); } }, stored);
  await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Ready")] });
  await page.goto(`${server.url}#/resources`);
  await expect.poll(async () => JSON.parse(await page.evaluate(() => localStorage.getItem("kukeon-panel-cells")))).toEqual({ added: [cellKey("api")], removed: [] });
});

test("Open in side panel adds a cell from its screen; Ask majordomo pastes only into majordomo, adding it back when removed", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Ready")] });
  const socketsOf = name => state.sockets.filter(socket => socket.url === attachPath(name));
  await page.goto(`${server.url}#/workspace/default/default/default/api`);
  await expect(mainTools(page).locator(".terminal-connection-state")).toHaveClass(/attached/);
  await expect(panel(page)).toBeHidden();
  await mainTools(page).getByRole("button", { name: "Open in side panel" }).click();
  await expect(panel(page)).toBeVisible();
  await expect(tabs(page)).toHaveText([/^kuke-majordomo-claude/, /^api/]);
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(connection(page)).toContainText("Attached");
  await expect.poll(() => socketsOf("api")).toHaveLength(2);
  await expect(mainTools(page).getByRole("button", { name: "Open in side panel" })).toHaveCount(0);

  // With another cell's tab active, Ask switches to the majordomo tab and pastes there only.
  await page.goto(`${server.url}#/resource/default/default/default/api`);
  const ask = page.locator(".cell-page-title-row").getByRole("button", { name: "Ask majordomo about cell default / default / default / api" });
  await ask.click();
  await expect(tabs(page).nth(0)).toHaveAttribute("aria-selected", "true");
  await expect.poll(() => socketsOf("majordomo").flatMap(bracketed)).toHaveLength(1);
  expect(socketsOf("api").flatMap(bracketed)).toHaveLength(0);

  // A majordomo cell removed from the panel is added back first.
  await panel(page).getByRole("button", { name: "Remove majordomo from the side panel" }).click();
  await expect(tabs(page)).toHaveText([/^api/]);
  await expect(panel(page).getByRole("status").filter({ hasText: "Waiting to paste" })).toHaveCount(0);
  await ask.click();
  await expect(tabs(page)).toHaveText([/^kuke-majordomo-claude/, /^api/]);
  await expect(tabs(page).nth(0)).toHaveAttribute("aria-selected", "true");
  await expect.poll(() => socketsOf("majordomo").at(-1) && bracketed(socketsOf("majordomo").at(-1)).length).toBe(1);
  expect(socketsOf("api").flatMap(bracketed)).toHaveLength(0);
  // The sidebar entry and rail button still count majordomo cells only.
  await expect(entry(page)).toContainText("Ready");
  await expect(entryRegion(page).locator(".majordomo-count")).toHaveCount(0);
});

// #1777, C: reading a cell in the panel counts as reading it.
test("the majordomo cell's unread mark and byte count clear within one poll while it is shown in the panel", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.clock.install();
  await page.addInitScript(() => localStorage.setItem("kukeon-show-unread-bytes", "true"));
  const counts = { "default/default/default/majordomo": { container: "agent", startedAt: "2026-11-26T10:00:00Z", bytesOut: 100, bytesIn: 0, idleMs: 60_000 } };
  const asked = [];
  let answered = 0;
  await page.route("**/api/terminal-activity**", async route => {
    const cells = new URL(route.request().url()).searchParams.getAll("cell");
    asked.push(cells);
    await route.fulfill({ json: { cells: cells.map(item => ({ scope: item.split("/"), ...(counts[item] ?? { unavailable: "not_found" }) })) } });
    answered++;
  });
  const tick = async () => { const count = answered; await page.clock.fastForward(5000); await expect.poll(() => answered).toBeGreaterThan(count); await page.clock.fastForward(100); };
  await world(page, { configs: teamConfigs(), cells: [runningMajordomo()] });
  await page.goto(server.url);
  // The majordomo cell is listed under its Config, as an agent instance.
  const row = page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: /open default \/ default \/ default \/ majordomo/ });
  await expect.poll(() => answered).toBeGreaterThan(0);
  counts["default/default/default/majordomo"] = { ...counts["default/default/default/majordomo"], bytesOut: 48_228 };
  await tick();
  await expect(row).toHaveClass(/has-unread/);
  await expect(row.locator(".cell-unread-count")).toHaveText("47 KiB");

  // Shown and attached in the panel: cleared within one poll, and the marker moves to every reading.
  await entry(page).click();
  await expect(connection(page)).toContainText("Attached");
  await tick();
  await expect(row).not.toHaveClass(/has-unread/);
  await expect(row.locator(".cell-unread-count")).toHaveCount(0);
  await expect.poll(async () => JSON.parse(await page.evaluate(() => localStorage.getItem("kukeon-terminal-seen")))[cellKey("majordomo")]).toEqual({ startedAt: "2026-11-26T10:00:00Z", bytes: 48_228 });

  // Closing the panel takes one last reading of the cell left.
  counts["default/default/default/majordomo"] = { ...counts["default/default/default/majordomo"], bytesOut: 50_000, idleMs: 4000 };
  const before = asked.length;
  await panel(page).getByRole("button", { name: "Collapse majordomo panel" }).click();
  await expect.poll(() => asked.length).toBeGreaterThan(before);
  expect(asked.at(-1)).toEqual(["default/default/default/majordomo"]);
  await expect.poll(async () => JSON.parse(await page.evaluate(() => localStorage.getItem("kukeon-terminal-seen")))[cellKey("majordomo")]).toEqual({ startedAt: "2026-11-26T10:00:00Z", bytes: 50_000 });
  await expect(row).not.toHaveClass(/has-unread/);
});

test("the Health page shows the latest report, its findings and the history", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), reports, cells: [runningMajordomo()] });
  await page.goto(`${server.url}#/resources`);
  // Health is the Daemon page's second tab (#1635).
  await page.getByRole("navigation", { name: "Manage navigation" }).getByRole("link", { name: "Daemon" }).click();
  const tabs = page.getByRole("navigation", { name: "Daemon sections" });
  await tabs.getByRole("link", { name: "Health" }).click();
  await expect(page).toHaveURL(/#\/daemon\/health$/);
  await expect(page.getByRole("heading", { level: 1, name: "Health" })).toBeVisible();
  await expect(tabs.getByRole("link")).toHaveText(["Status", "Health", "Logs", "Connectors"]);
  await expect(tabs.locator('[aria-current="page"]')).toHaveText("Health");

  const report = page.getByRole("region", { name: "Health report" });
  await expect(report.getByRole("heading", { name: "Latest report" })).toBeVisible();
  await expect(report).toContainText("Three findings, one kukeon defect.");
  await expect(report).toContainText("kuke-majordomo-claude-health-sweep-aa11bb");
  const findings = report.getByRole("list", { name: "Findings" }).locator("> li");
  await expect(findings).toHaveCount(3);
  await expect(findings.nth(0)).toContainText("api fails to start: CNI attach error");
  await expect(findings.nth(0).getByTitle("Classification")).toHaveText("kukeon");
  await expect(findings.nth(0).getByTitle("Recurrence")).toHaveText("Recurring");
  await expect(findings.nth(0).getByRole("link", { name: "Filed issue" })).toHaveAttribute("href", "https://github.com/acme/ops/issues/7");
  await expect(findings.nth(0)).toContainText("kuke restart api --realm default --space default --stack default");
  await expect(findings.nth(1).getByTitle("Classification")).toHaveText("workload");
  await expect(findings.nth(1).getByTitle("Recurrence")).toHaveText("New");
  await expect(findings.nth(1).getByRole("link", { name: "Filed issue" })).toHaveCount(0);
  await expect(findings.nth(2).getByTitle("Classification")).toHaveText("unclear");

  const history = page.getByRole("region", { name: "Report history" });
  await expect(history.locator("tbody tr")).toHaveCount(2);
  await expect(history.locator("tbody tr").nth(0)).toContainText("Three findings, one kukeon defect.");
  await history.getByRole("button", { name: "Show report 20261117T090000.000000000Z" }).click();
  await expect(report.getByRole("heading", { name: "Report", exact: true })).toBeVisible();
  await expect(report).toContainText("One kukeon defect.");
  await expect(report.getByRole("list", { name: "Findings" }).locator("> li")).toHaveCount(1);

  // "Ask majordomo about this report" pastes a question naming the report shown.
  await report.getByRole("button", { name: "Ask majordomo about health report 20261117T090000.000000000Z" }).click();
  await expect.poll(() => pasted(state).filter(message => message.includes("\x1b[200~"))).toHaveLength(1);
  expect(pasted(state).find(message => message.includes("\x1b[200~"))).toContain('kuke health report 20261117T090000.000000000Z');
});

test("the Health page shows the sweep schedule from the task config, runs a sweep now and links the kuke team while suspended", async ({ page }) => {
  const status = { schedule: { lastScheduleTime: "2026-11-17T09:00:00Z", nextScheduleTime: "2026-11-17T10:00:00Z", lastCell: "kuke-majordomo-claude-health-sweep-aa11bb", lastError: "create cell: disk pressure" } };
  const state = await world(page, { configs: teamConfigs({ suspend: true, status }), reports: [] });
  await page.goto(`${server.url}#/health`);
  const schedule = page.getByRole("region", { name: "Sweep schedule" });
  // Only the labelled sweep task config is listed.
  await expect(schedule.locator(".health-sweep")).toHaveCount(1);
  await expect(schedule.getByRole("link", { name: "kuke-majordomo-claude-health-sweep" })).toHaveAttribute("href", "#/configs/default/default/default/kuke-majordomo-claude-health-sweep");
  const rows = schedule.locator(".health-sweep dl");
  await expect(rows).toContainText("@hourly (UTC)");
  // The entry's badge carries the state; the list does not repeat it. A suspended schedule has no next fire.
  await expect(schedule.locator(".health-sweep .state-badge")).toHaveText("Suspended");
  await expect(rows.locator("dt", { hasText: /^State$/ })).toHaveCount(0);
  await expect(rows.locator("dt", { hasText: "Next fire" }).locator("+ dd")).toHaveText("— (suspended)");
  await expect(rows.locator("time")).toHaveCount(1);
  await expect(rows.locator("time").nth(0)).toHaveAttribute("datetime", "2026-11-17T09:00:00Z");
  await expect(rows).toContainText("kuke-majordomo-claude-health-sweep-aa11bb");
  await expect(rows).toContainText("create cell: disk pressure");

  // Suspended: the edit and a link to the kuke team are shown, and nothing on the page edits the schedule.
  const enable = schedule.getByRole("note");
  await expect(enable).toHaveCount(1);
  await expect(enable).toContainText("Sweeps are suspended.");
  await expect(enable).toContainText("kuke-majordomo-claude-health-sweep ships switched off");
  await expect(enable.locator("pre")).toHaveText("kuke team task enable kuke health-sweep --role majordomo --harness claude");
  await expect(enable.getByRole("link", { name: "kuke team" })).toHaveAttribute("href", "#/teams/kuke/edit");
  await expect(enable).not.toContainText("kuketeam");
  await expect(schedule.locator("input, select, textarea")).toHaveCount(0);
  await expect(schedule.getByRole("button")).toHaveCount(1);

  await schedule.getByRole("button", { name: "Run sweep now with kuke-majordomo-claude-health-sweep" }).click();
  await expect(schedule.getByRole("status").filter({ hasText: "Sweep started" })).toContainText("kuke-majordomo-claude-health-sweep-1a2b3c");
  expect(state.sweeps).toEqual([{ config: { name: "kuke-majordomo-claude-health-sweep", realm: "default", space: "default", stack: "default" } }]);

  await expect(page.getByRole("region", { name: "Health report" })).toContainText("No health report is stored.");
});

// Issue #1568, item F: the suspended notice is shown once, however many sweep configs are suspended. Since #1668
// it shows only while every sweep is suspended, with one kuke team task enable line per harness.
test("the suspended notice names every suspended sweep with a command per harness, and hides once one sweep is on", async ({ page }) => {
  const sweep = (name, harness, suspend) => config(name, harness ? { ...sweepLabels, "kukeon.io/harness": harness } : sweepLabels, { spec: { schedule: { cron: "@hourly", suspend } } });
  await world(page, { configs: [...teamConfigs({ suspend: true }), sweep("kuke-majordomo-codex-health-sweep", "codex", true)] });
  await page.goto(`${server.url}#/health`);
  let schedule = page.getByRole("region", { name: "Sweep schedule" });
  await expect(schedule.locator(".health-sweep")).toHaveCount(2);
  await expect(schedule.locator(".health-sweep .state-badge")).toHaveText(["Suspended", "Suspended"]);
  const notice = schedule.getByRole("note");
  await expect(notice).toHaveCount(1);
  await expect(notice).toContainText("Sweeps are suspended.");
  await expect(notice.getByRole("list", { name: "Suspended sweeps" }).getByRole("listitem")).toHaveText(["kuke-majordomo-claude-health-sweep", "kuke-majordomo-codex-health-sweep"]);
  await expect(notice.locator("pre")).toHaveText("kuke team task enable kuke health-sweep --role majordomo --harness claude\nkuke team task enable kuke health-sweep --role majordomo --harness codex");
  await expect(notice).not.toContainText("suspend: false");
  // The notice sits above the sweep list instead of inside every suspended entry.
  await expect(schedule.locator(".health-sweep").getByRole("note")).toHaveCount(0);
  expect(await notice.evaluate(el => el.compareDocumentPosition(document.querySelector(".health-sweeps")) & Node.DOCUMENT_POSITION_FOLLOWING)).toBeTruthy();
  await expect(schedule.getByRole("button", { name: /^Run sweep now with / })).toHaveCount(2);

  // One sweep on and one suspended: no notice.
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await signIn(page);
  await world(page, { configs: [...teamConfigs({ suspend: true }), sweep("kuke-majordomo-codex-health-sweep", "codex", false)] });
  await page.reload();
  schedule = page.getByRole("region", { name: "Sweep schedule" });
  await expect(schedule.locator(".health-sweep .state-badge")).toHaveText(["Suspended", "Scheduled"]);
  await expect(schedule.getByRole("note")).toHaveCount(0);

  // A sweep Config rendered before the harness label existed: the all-harness command.
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await signIn(page);
  await world(page, { configs: [...teamConfigs({ suspend: true }), sweep("kuke-majordomo-codex-health-sweep", "", true)] });
  await page.reload();
  schedule = page.getByRole("region", { name: "Sweep schedule" });
  await expect(schedule.getByRole("note").locator("pre")).toHaveText("kuke team task enable kuke health-sweep --role majordomo");
});

test("an enabled sweep shows no reapply instructions, and a host without majordomo says so", async ({ page }) => {
  await world(page, { configs: teamConfigs({ suspend: false }) });
  await page.goto(`${server.url}#/health`);
  const schedule = page.getByRole("region", { name: "Sweep schedule" });
  await expect(schedule.locator(".health-sweep .state-badge")).toHaveText("Scheduled");
  await expect(schedule.locator(".health-sweep dl dt", { hasText: "Next fire" }).locator("+ dd")).toHaveText("—");
  await expect(schedule.getByRole("note")).toHaveCount(0);

  await page.unrouteAll({ behavior: "ignoreErrors" });
  await signIn(page);
  await world(page, { configs: [] });
  await page.reload();
  await expect(page.getByRole("region", { name: "Sweep schedule" })).toContainText("No health-sweep config found.");
});

// #1635: the Workbench grid and the Manage sidebar with majordomo docked.
test("a grid tile opens the cell screen, also for a cell in the side panel", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Ready")] });
  await page.goto(server.url);
  const grid = page.getByRole("region", { name: "Cell terminals" });
  await expect(grid.getByRole("button")).toHaveCount(0);
  const tile = grid.getByRole("link", { name: "Open default / default / default / majordomo", exact: true });
  await expect(tile).toHaveAttribute("href", "#/workspace/default/default/default/majordomo");
  await expect(grid.getByRole("link", { name: "Open default / default / default / api", exact: true })).toHaveAttribute("href", "#/workspace/default/default/default/api");
  await tile.click();
  await expect(page).toHaveURL(/#\/workspace\/default\/default\/default\/majordomo$/);
  await expect(mainTools(page).locator(".terminal-connection-state")).toHaveClass(/attached/);
  await expect(panel(page)).toBeHidden();
});

test("with the majordomo panel docked at 1440×900 the Manage sidebar and a list page do not overflow", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await world(page, { configs: teamConfigs(), cells: [runningMajordomo(), cell("api", "Ready")] });
  await page.addInitScript(() => localStorage.setItem("kukeon-majordomo-open", "true"));
  for (const hash of ["#/resources", "#/cells", "#/configs", "#/teams", "#/daemon"]) {
    await page.goto(`${server.url}${hash}`);
    await expect(panel(page), hash).toBeVisible();
    const manage = page.getByRole("navigation", { name: "Manage navigation" });
    await expect(manage.getByRole("link"), hash).toHaveCount(14);
    // One vertical list: every link is on its own line and inside the sidebar.
    const boxes = await manage.getByRole("link").evaluateAll(links => links.map(link => link.getBoundingClientRect()).map(box => ({ left: Math.round(box.left), right: Math.round(box.right), top: Math.round(box.top) })));
    expect(new Set(boxes.map(box => box.left)).size, hash).toBe(1);
    expect(new Set(boxes.map(box => box.top)).size, hash).toBe(14);
    const sidebar = await page.locator("#cell-sidebar").boundingBox();
    for (const box of boxes) expect(box.right, hash).toBeLessThanOrEqual(Math.round(sidebar.x + sidebar.width));
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), hash).toBe(true);
    expect(await page.locator("main").evaluate(el => el.scrollWidth <= el.clientWidth + 1), hash).toBe(true);
  }
});

// Issue #1649, AC9: each sweep Config says whether issue filing has a token. GitHub access is off until the
// Config renders the github component; on, it is missing until the connector resolves; resolved, it names
// the identity and its level. /api/connectors is the real server's catalog.
test("the Health page says whether a sweep can file issues: GitHub access off, token missing, or the token", async ({ page }) => {
  const sweep = (components, connectors) => config("kuke-majordomo-claude-health-sweep", sweepLabels, {
    spec: { schedule: { cron: "@hourly", suspend: false }, components }, status: { connectors },
  });
  const state = await world(page, { configs: [sweep([], [])] });
  await page.goto(`${server.url}#/health`);
  const filing = page.getByRole("region", { name: "Sweep schedule" }).locator(".health-filing");
  await expect(filing).toContainText("GitHub access is off.");
  await expect(filing.getByRole("link", { name: "kuke team's Edit team screen" })).toHaveAttribute("href", "#/teams/kuke/edit");
  await expect(filing).toContainText("kuke team connector enable kuke github --role majordomo");
  await expect(filing).toContainText("--file-issues <owner>/<repo>");

  state.configs = [sweep(["github"], [{ connector: "github", component: "github", method: "token", delivery: "env", state: "unresolved", required: true }])];
  await page.reload();
  await expect(filing).toContainText("Token missing.");
  await expect(filing.getByRole("link", { name: "Add or choose a GitHub identity" })).toHaveAttribute("href", "#/identities?connector=github");

  state.configs = [sweep(["github"], [{ connector: "github", component: "github", method: "token", delivery: "env", state: "resolved", identity: { name: "gh-token", realm: "default" }, origin: "realm" }])];
  await page.reload();
  await expect(filing).toContainText("Issue filing has a token: gh-token (realm).");
  await expect(filing).not.toContainText("Token missing");
});
