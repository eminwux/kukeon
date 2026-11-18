import { test, expect } from "@playwright/test";
import { startServer } from "./server.mjs";

// Majordomo in the web UI (#1509): the permanent sidebar entry and enable flow, the docked panel,
// the "Ask majordomo" actions and the Health page. kuke serve runs for real with no daemon; every
// API call and the attach WebSocket are answered here.

let server;
test.beforeEach(async () => { server = await startServer(); });
test.afterEach(async () => { await server?.stop(); });

const scope = ["default", "default", "default"];
const majordomoLabels = { "kukeon.io/team": "kukeon", "kukeon.io/role": "majordomo" };
const sweepLabels = { ...majordomoLabels, "kukeon.io/task": "health-sweep" };

function config(name, labels, extra = {}) {
  return { apiVersion: "kukeon.io/v1beta1", kind: "CellConfig", metadata: { name, realm: scope[0], space: scope[1], stack: scope[2], labels }, spec: { blueprint: { name: "claude-host", realm: "default" }, ...extra.spec }, ...(extra.status ? { status: extra.status } : {}) };
}
function cell(name, state, labels = {}, status = {}) {
  return {
    apiVersion: "kukeon.io/v1beta1", kind: "Cell", metadata: { name, labels },
    spec: { realmId: scope[0], spaceId: scope[1], stackId: scope[2], containers: [{ id: "agent", attachable: true }, { id: "pause", root: true, attachable: true }], tty: { default: "agent" } },
    status: { state, ...status },
  };
}
// Configs as `kuke team init` applies them, plus two that must not be mistaken for majordomo: one
// that only has the name, and a sweep task config (same team and role, but a task).
function teamConfigs({ suspend = true, status } = {}) {
  return [
    config("kukeon-majordomo-claude", majordomoLabels),
    config("assistant-b", majordomoLabels),
    config("kukeon-majordomo-lookalike", { "kukeon.io/team": "other" }),
    config("kukeon-majordomo-claude-health-sweep", sweepLabels, { spec: { schedule: { cron: "@hourly", suspend } }, status }),
  ];
}

// One mutable host: its cells and configs, the requests the page made, and the attach sockets.
async function world(page, { cells = [], configs = [], reports = [], createError, daemon } = {}) {
  const state = { cells: [...cells], configs, reports, requests: [], sockets: [], messages: [], createError, sweeps: [] };
  const named = name => state.cells.find(item => item.metadata.name === name);
  await page.route("**/api/realms**", async route => {
    const request = route.request();
    const parts = new URL(request.url()).pathname.split("/").filter(Boolean).slice(1);
    if (request.method() === "POST") {
      state.requests.push(`POST ${parts.slice(6).join("/")}`);
      const target = named(decodeURIComponent(parts[7]));
      if (parts[8] === "start" && target) { target.status = { state: "Ready" }; return route.fulfill({ json: { action: "start", cell: target.metadata.name } }); }
      return route.fulfill({ status: 404, json: { error: { code: "not_found", message: "cell not found" } } });
    }
    const leaf = body => route.fulfill({ json: body });
    if (parts.length === 1) return leaf([{ metadata: { name: "default" }, status: { state: "Ready" } }]);
    if (parts.length === 3) return leaf([{ metadata: { name: "default" }, status: { state: "Ready" } }]);
    if (parts.length === 5) return leaf([{ metadata: { name: "default" }, status: { state: "Ready" } }]);
    if (parts.length === 7) return leaf(state.cells);
    if (parts.length === 8) { const found = named(decodeURIComponent(parts[7])); return found ? leaf(found) : route.fulfill({ status: 404, json: { error: { code: "not_found", message: "cell not found" } } }); }
    if (parts.length === 9 && parts[8] === "containers") return leaf([{ id: "agent", attachable: true }, { id: "pause", root: true, attachable: true }]);
    return leaf({ metadata: { name: parts.at(-1) }, status: { state: "Ready" } });
  });
  await page.route("**/api/catalog/**", async route => {
    const url = new URL(route.request().url());
    const [, , , kind, name] = url.pathname.split("/");
    const list = kind === "configs" ? state.configs
      : kind === "blueprints" ? [{ apiVersion: "kukeon.io/v1beta1", kind: "CellBlueprint", metadata: { name: "claude-host", realm: "default" }, spec: {} }]
        : kind === "volumes" ? [{ apiVersion: "kukeon.io/v1beta1", kind: "Volume", metadata: { name: "kukeon-majordomo-claude", realm: "default", space: "default", stack: "default" }, spec: { reclaimPolicy: "Retain" } }] : [];
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
    if (request.method() === "POST") { state.sweeps.push(request.postDataJSON()); return route.fulfill({ json: { cell: { metadata: { name: "kukeon-majordomo-claude-health-sweep-1a2b3c" } } } }); }
    if (path === "/api/health/reports") return route.fulfill({ json: state.reports.map(report => ({ id: report.id, createdAt: report.createdAt, since: report.since, until: report.until, summary: report.summary, findings: report.findings.length, recurring: report.findings.filter(f => f.recurring).length, filed: report.findings.filter(f => f.issueUrl).length })) });
    const id = decodeURIComponent(path.split("/").at(-1));
    const found = id === "latest" ? state.reports[0] : state.reports.find(report => report.id === id);
    return found ? route.fulfill({ json: found }) : route.fulfill({ status: 404, json: { error: { code: "not_found", message: "health report not found" } } });
  });
  if (daemon) await page.route("**/api/daemon/health", route => route.fulfill({ json: daemon }));
  await page.routeWebSocket("**/attach", socket => {
    const entry = { url: new URL(socket.url()).pathname, closed: false, send: data => socket.send(data), close: () => socket.close() };
    state.sockets.push(entry);
    socket.onMessage(message => { state.messages.push(message); });
    socket.onClose(() => { entry.closed = true; });
    socket.send(JSON.stringify({ type: "ready" }));
    // The harness asks for bracketed paste before it draws its prompt.
    socket.send(Buffer.from("\x1b[?2004hmajordomo> "));
  });
  return state;
}

const panel = page => page.getByRole("complementary", { name: "Majordomo panel" });
const entry = page => page.getByRole("region", { name: "Majordomo", exact: true }).getByRole("button");
const connection = page => panel(page).locator(".terminal-connection-state");
const pasted = state => state.messages.filter(message => Buffer.isBuffer(message)).map(message => message.toString());
const runningMajordomo = () => cell("majordomo", "Ready", { ...majordomoLabels, "kukeon.io/config": "kukeon-majordomo-claude" });

test("the Majordomo sidebar entry is permanent and says when kuke team init is needed", async ({ page }) => {
  // The only config carries the name but not the labels, so majordomo is not installed.
  const state = await world(page, { configs: [config("kukeon-majordomo-claude", { team: "platform" })] });
  await page.goto(`${server.url}#/resources`);
  await expect(entry(page)).toContainText("Majordomo");
  await expect(entry(page)).toContainText("Needs kuke team init");
  await page.goto(`${server.url}#/configs`);
  await expect(entry(page)).toBeVisible();
  await entry(page).click();
  await expect(panel(page)).toContainText("Majordomo is not installed on this host.");
  await expect(panel(page)).toContainText("kuke team init");
  await expect(panel(page).getByRole("button", { name: "Enable majordomo" })).toHaveCount(0);
  expect(state.requests).toEqual([]);
});

test("Enable majordomo picks a labelled harness config, creates and starts one cell named majordomo", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs() });
  await page.goto(`${server.url}#/resources`);
  await expect(entry(page)).toContainText("Not enabled");
  await entry(page).click();
  // Configs are found by label: the lookalike name and the sweep task config are not offered.
  const harness = panel(page).getByLabel("Harness");
  await expect(harness.locator("option")).toHaveText(["kukeon-majordomo-claude", "assistant-b"]);
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
  const message = "config kukeon-majordomo-claude: secret slot \"claude-code-oauth-token\" references secret default/claude-code-oauth-token, which does not exist";
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

test("majordomo's own cell screen points at the panel instead of opening a second terminal", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), cells: [runningMajordomo()] });
  await page.goto(`${server.url}#/workspace/default/default/default/majordomo`);
  const workspace = page.getByRole("region", { name: "Terminal workspace" });
  await expect(workspace).toContainText("This cell's terminal is docked in the Majordomo panel.");
  expect(state.sockets).toEqual([]);
  await workspace.getByRole("button", { name: "Open the Majordomo panel" }).click();
  await expect(connection(page)).toContainText("Attached");
  expect(state.sockets).toHaveLength(1);
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

test("Ask majordomo is on the cell list's failed rows and on config, blueprint, volume and daemon views", async ({ page }) => {
  const daemon = { status: "unavailable", endpoint: "unix:///run/kukeon/kukeond.sock", serverVersion: "v0.11.0", checkedAt: "2026-11-17T10:00:00Z", latencyMs: 3, error: { code: "unavailable", message: "dial unix /run/kukeon/kukeond.sock: connect: connection refused" } };
  const state = await world(page, { configs: teamConfigs(), daemon, cells: [runningMajordomo(), cell("api", "Error"), cell("web", "Degraded"), cell("ok", "Ready")] });
  const question = async () => { await expect.poll(() => pasted(state).filter(message => message.includes("\x1b[200~")).length).toBeGreaterThan(0); const last = pasted(state).filter(message => message.includes("\x1b[200~")).at(-1); state.messages.length = 0; return last.slice(6, -6); };

  await page.goto(`${server.url}#/cells`);
  const rows = page.getByRole("region", { name: "Cells list" });
  await expect(rows.getByRole("button", { name: /Ask majordomo about cell/ })).toHaveCount(2);
  await rows.getByRole("button", { name: "Ask majordomo about cell default / default / default / web" }).click();
  expect(await question()).toContain("About the cell web in realm default, space default, stack default (state Degraded)");

  await page.goto(`${server.url}#/configs/default/default/default/kukeon-majordomo-claude-health-sweep`);
  await page.getByRole("button", { name: "Ask majordomo about CellConfig default / default / default / kukeon-majordomo-claude-health-sweep" }).click();
  expect(await question()).toBe("About the CellConfig kukeon-majordomo-claude-health-sweep in realm default, space default, stack default (stored, blueprint claude-host, schedule @hourly (suspended)): explain what it is for and check whether anything is wrong with it.");

  await page.goto(`${server.url}#/blueprints/default/claude-host`);
  await page.getByRole("button", { name: "Ask majordomo about CellBlueprint default / claude-host" }).click();
  expect(await question()).toBe("About the CellBlueprint claude-host in realm default (stored): explain what it is for and check whether anything is wrong with it.");

  await page.goto(`${server.url}#/volumes/default/default/default/kukeon-majordomo-claude`);
  await page.getByRole("button", { name: "Ask majordomo about volume default / default / default / kukeon-majordomo-claude" }).click();
  expect(await question()).toContain("About the volume kukeon-majordomo-claude in realm default, space default, stack default (stored, reclaim policy Retain)");

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
    cell: { realm: "default", space: "default", stack: "default", name: "kukeon-majordomo-claude-health-sweep-aa11bb" },
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

test("the Health page shows the latest report, its findings and the history", async ({ page }) => {
  const state = await world(page, { configs: teamConfigs(), reports, cells: [runningMajordomo()] });
  await page.goto(`${server.url}#/resources`);
  await page.getByRole("navigation", { name: "Resources navigation" }).getByRole("link", { name: "Health" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Health" })).toBeVisible();

  const report = page.getByRole("region", { name: "Health report" });
  await expect(report.getByRole("heading", { name: "Latest report" })).toBeVisible();
  await expect(report).toContainText("Three findings, one kukeon defect.");
  await expect(report).toContainText("kukeon-majordomo-claude-health-sweep-aa11bb");
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

test("the Health page shows the sweep schedule from the task config, runs a sweep now and explains --reapply while suspended", async ({ page }) => {
  const status = { schedule: { lastScheduleTime: "2026-11-17T09:00:00Z", nextScheduleTime: "2026-11-17T10:00:00Z", lastCell: "kukeon-majordomo-claude-health-sweep-aa11bb", lastError: "create cell: disk pressure" } };
  const state = await world(page, { configs: teamConfigs({ suspend: true, status }), reports: [] });
  await page.goto(`${server.url}#/health`);
  const schedule = page.getByRole("region", { name: "Sweep schedule" });
  // Only the labelled sweep task config is listed.
  await expect(schedule.locator(".health-sweep")).toHaveCount(1);
  await expect(schedule.getByRole("link", { name: "kukeon-majordomo-claude-health-sweep" })).toHaveAttribute("href", "#/configs/default/default/default/kukeon-majordomo-claude-health-sweep");
  const rows = schedule.locator(".health-sweep dl");
  await expect(rows).toContainText("@hourly (UTC)");
  await expect(rows.locator("dd").nth(1)).toHaveText("Yes");
  await expect(rows.locator("time").nth(0)).toHaveAttribute("datetime", "2026-11-17T10:00:00Z");
  await expect(rows.locator("time").nth(1)).toHaveAttribute("datetime", "2026-11-17T09:00:00Z");
  await expect(rows).toContainText("kukeon-majordomo-claude-health-sweep-aa11bb");
  await expect(rows).toContainText("create cell: disk pressure");

  // Suspended: the edit and the command are shown, and nothing on the page edits the schedule.
  const enable = schedule.getByRole("note");
  await expect(enable).toContainText("Sweeps are suspended.");
  await expect(enable).toContainText("{ name: health-sweep, suspend: false }");
  await expect(enable).toContainText("kuke team init --reapply");
  await expect(schedule.locator("input, select, textarea")).toHaveCount(0);
  await expect(schedule.getByRole("button")).toHaveCount(1);

  await schedule.getByRole("button", { name: "Run sweep now with kukeon-majordomo-claude-health-sweep" }).click();
  await expect(schedule.getByRole("status").filter({ hasText: "Sweep started" })).toContainText("kukeon-majordomo-claude-health-sweep-1a2b3c");
  expect(state.sweeps).toEqual([{ config: { name: "kukeon-majordomo-claude-health-sweep", realm: "default", space: "default", stack: "default" } }]);

  await expect(page.getByRole("region", { name: "Health report" })).toContainText("No health report is stored.");
});

test("an enabled sweep shows no reapply instructions, and a host without majordomo says so", async ({ page }) => {
  await world(page, { configs: teamConfigs({ suspend: false }) });
  await page.goto(`${server.url}#/health`);
  const schedule = page.getByRole("region", { name: "Sweep schedule" });
  await expect(schedule.locator(".health-sweep .state-badge")).toHaveText("Scheduled");
  await expect(schedule.locator(".health-sweep dl dd").nth(1)).toHaveText("No");
  await expect(schedule.getByRole("note")).toHaveCount(0);

  await page.unrouteAll({ behavior: "ignoreErrors" });
  await world(page, { configs: [] });
  await page.reload();
  await expect(page.getByRole("region", { name: "Sweep schedule" })).toContainText("No health-sweep config found.");
});
