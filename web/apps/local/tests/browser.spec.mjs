import { test, expect } from "@playwright/test";
import { startServer, connectorWorld, mockConnectorAPI, signIn, testTokenConnector } from "./server.mjs";

let server;
test.beforeEach(async ({ page }) => { server = await startServer(); await signIn(page); });
test.afterEach(async () => { await server?.stop(); });
const kinds = ["Realm", "Space", "Stack", "Cell", "Container"];
function doc(scope) {
  return { apiVersion: "v1beta1", kind: kinds[scope.length - 1], metadata: { name: scope.at(-1), labels: { team: "platform" } }, spec: { scope, futureField: { nested: true } }, status: { state: scope[0] === "prod" ? "Running" : "Ready" } };
}
function fixture(path, cellCount = 1) {
  const parts = path.split("/").filter(Boolean).slice(1);
  const scope = parts.filter((_, i) => i % 2 === 1).map(decodeURIComponent);
  if (parts.length % 2 === 0) return doc(scope);
  const depth = Math.floor(parts.length / 2);
  if (depth === 0) return [doc(["dev"]), doc(["prod"])];
  if (depth === 4) return [{ id: "web" }];
  const names = depth === 3 ? Array.from({ length: cellCount }, (_, i) => cellCount === 1 ? "api" : `api-${String(i).padStart(2, "0")}`) : [depth === 1 ? "shared" : "services"];
  return names.map(name => doc([...scope, name]));
}
// A container with no stream file yet: the log view's neutral empty state.
const noLog = { status: 404, json: { error: { code: "log_unavailable", message: "container log stream unavailable" } } };
// A cell with no terminal screen to read: the Workbench grid's state-only tile (#1635).
const noScreen = { status: 404, json: { error: { code: "screen_unavailable", message: "no screen" } } };
async function mock(page, handler = () => undefined) {
  await page.route("**/api/realms**", async route => {
    const path = new URL(route.request().url()).pathname;
    const result = await handler(path, route.request());
    await route.fulfill(result ?? (path.endsWith("/log") ? noLog : path.endsWith("/screenshot") ? noScreen : { json: fixture(path) }));
  });
}
// One GET …/containers/{name}/log reply: text starting at byte `start`, ending at the stream's end.
const logChunk = (text, start = 0) => ({ json: { source: "/mock/log", data: Buffer.from(text).toString("base64"), start, next: start + Buffer.byteLength(text), size: start + Buffer.byteLength(text), truncated: false, more: false } });
const failure = { status: 503, json: { error: { code: "unavailable", message: "Scoped read failed" } } };
const nav = page => page.getByRole("navigation", { name: "Manage navigation" });
const listSearch = page => page.locator(".list-controls").getByRole("searchbox");
const terminalTools = page => page.locator(".cell-terminal-tools");
const toasts = page => page.getByRole("region", { name: "Notifications" }).locator(".toast");
const content = page => page.locator(".workspace-content");
// Cell and container pages keep Network and Cgroup collapsed (#1568); a test opens one by its region name.
const expand = (page, name) => page.getByRole("region", { name }).locator("summary").click();
// Appearance and the Profile settings live on the Settings page, linked from the user menu (#1686).
const userButton = page => page.locator(".toolbar").getByRole("button", { name: /^User menu/ });
const settingsPage = page => content(page).locator(".settings-page");
const settingsNav = page => page.getByRole("navigation", { name: "Settings navigation" });
// Every setting is on the one Profile section (#1745), which #/settings opens.
async function openSettings(page) {
  await userButton(page).click();
  await page.getByRole("menuitem", { name: "Settings" }).click();
  await expect(page).toHaveURL(/#\/settings$/);
  await expect(settingsPage(page).getByRole("heading", { name: "Profile", level: 2 })).toBeVisible();
  return settingsPage(page);
}
// Sets the appearance on the Settings page and returns to the page the test was on. From a URL with a
// hash both moves change only the hash, so the app and its terminals stay mounted.
async function setAppearance(page, label) {
  const from = page.url();
  const settings = await openSettings(page);
  await settings.getByRole("radio", { name: label }).check();
  await page.goto(from);
}
// seed stores preferences before the app first loads. It writes once per tab, so a later reload keeps
// whatever the test changed.
async function seed(page, values) {
  await page.addInitScript(entries => {
    try {
      if (sessionStorage.getItem("kukeon-test-seeded")) return;
      for (const [key, value] of Object.entries(entries)) localStorage.setItem(key, value);
      sessionStorage.setItem("kukeon-test-seeded", "1");
    } catch { /* about:blank has no storage. */ }
  }, values);
}
// The lists of #1635: Default cell list style, no default grouping, and the sidebar's Search and Group by
// shown. Tests of the flat and label-grouped lists start from here; every other test runs on the
// defaults of #1686 (agent style, sidebar controls hidden).
const defaultStyle = (page, extra = {}) => seed(page, { "kukeon-cell-list-style": "default", "kukeon-default-group-by": "", "kukeon-sidebar-show-search": "true", "kukeon-sidebar-show-group-by": "true", ...extra });
// An inventory for the agent style: in dev two instances of the Config claude (one by its provenance
// binding, one by its label alone, Failed), one instance of pm and one cell created from no Config; in
// prod one instance of another Config named claude.
const agentCell = (scope, name, { config, binding, state = "Ready" } = {}) => ({ ...doc([...scope, name]), metadata: { name, labels: config ? { "kukeon.io/config": config, team: "platform" } : { team: "platform" } }, spec: binding ? { provenance: { bindingKind: "config", bindingRef: binding } } : {}, status: { state } });
function agentInventory(path) {
  if (!path.endsWith("/cells")) return;
  const scope = [path.split("/")[3], "shared", "services"];
  return { json: scope[0] === "dev"
    ? [agentCell(scope, "dev-0", { config: "claude", binding: { realm: "dev", name: "claude" } }), agentCell(scope, "dev-1", { config: "claude", state: "Failed" }), agentCell(scope, "pm-0", { config: "pm" }), agentCell(scope, "scratch")]
    : [agentCell(scope, "dev-9", { config: "claude" })] };
}
const inventoryIndicator = (page, state) => page.locator(".toolbar").getByRole("button", { name: `Inventory ${state}: show details` });

test("terminal selects eligible containers, forwards input and resize, and detaches", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [
    { id: "worker", attachable: true }, { id: "shell", attachable: true },
    { id: "pause", root: true, attachable: true }, { id: "no-tty" },
  ] } : undefined);
  const messages = [];
  // Every attach path in order: the automatic attach may open worker before the pick opens shell.
  const paths = [];
  let closed = false;
  let sendReady = () => {};
  let sendOutput = () => {};
  await page.routeWebSocket("**/attach", socket => {
    paths.push(new URL(socket.url()).pathname);
    socket.onMessage(message => { messages.push(message); });
    socket.onClose(() => { closed = true; });
    sendReady = () => socket.send(JSON.stringify({ type: "ready" }));
    sendOutput = () => socket.send(Buffer.from("\x1b[95;49mYOLOmo\x1b[0m $ "));
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  // Every non-root container is listed; one without a terminal is marked as a log view.
  await expect(terminalTools(page).locator("option")).toHaveText(["worker", "shell", "no-tty (logs)"]);
  await terminalTools(page).getByLabel("Container").selectOption("shell");
  await expect.poll(() => paths.at(-1) ?? "").toContain("/containers/shell/attach");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Connecting…");
  sendReady();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveAttribute("title", /waiting for container output/);
  sendOutput();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  // The page follows the (light) system appearance, so bright magenta uses the light ANSI palette.
  await expect.poll(() => panel.locator(".xterm-fg-13").evaluate(el => getComputedStyle(el).color)).toBe("rgb(166, 38, 164)");
  await expect.poll(() => panel.locator(".xterm-rows").first().evaluate(el => getComputedStyle(el).fontFamily)).toContain("monospace");
  await expect(panel.locator(".xterm-cursor.xterm-cursor-blink")).toHaveClass(/xterm-cursor-underline/);
  await expect.poll(() => panel.locator(".xterm-cursor.xterm-cursor-blink").evaluate(el => getComputedStyle(el).animationName)).not.toBe("none");
  await expect.poll(() => messages.some(message => typeof message === "string" && JSON.parse(message).type === "resize")).toBe(true);
  await panel.locator(".terminal-session:not([hidden]) .xterm-helper-textarea").press("a");
  await expect.poll(() => messages.some(message => Buffer.isBuffer(message) && message.toString() === "a")).toBe(true);
  await terminalTools(page).getByRole("button", { name: "Detach" }).click();
  await expect.poll(() => closed).toBe(true);
});

// A choice made after the options render but before the automatic-attach effect has run (#1675):
// the effect still holds the first container, and must neither open it nor undo the choice.
test("a container picked before the automatic attach stays selected", async ({ page }) => {
  // The container list is held until the inventory is on screen, so the render that shows the
  // options is also the one whose effect attaches the first container.
  let releaseContainers = () => {};
  const containersHeld = new Promise(resolve => { releaseContainers = resolve; });
  await mock(page, async path => {
    if (!path.endsWith("/containers")) return undefined;
    await containersHeld;
    return { json: [{ id: "worker", attachable: true }, { id: "shell", attachable: true }] };
  });
  const paths = [];
  await page.routeWebSocket("**/attach", socket => { paths.push(new URL(socket.url()).pathname); });
  // A MutationObserver callback is a microtask: it runs right after React commits the options,
  // before React runs the passive effect that attaches the first container.
  await page.addInitScript(() => {
    new MutationObserver(() => {
      const select = document.querySelector("#terminal-container");
      if (!select || select.dataset.picked || !select.querySelector('option[value="shell"]')) return;
      select.dataset.picked = "1";
      select.value = "shell";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    }).observe(document, { childList: true, subtree: true });
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(page.getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
  releaseContainers();
  await expect.poll(() => paths.at(-1) ?? "").toContain("/containers/shell/attach");
  await expect(terminalTools(page).getByLabel("Container")).toHaveValue("shell");
  expect(paths.filter(path => path.includes("/containers/worker/attach"))).toEqual([]);
});

test("refreshing keeps the cell terminal size and sends no resize", async ({ page }) => {
  let hold = null;
  await mock(page, async path => {
    if (hold) await hold.promise;
    return path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined;
  });
  const messages = [];
  await page.routeWebSocket("**/attach", socket => {
    socket.onMessage(message => { messages.push(message); });
    socket.send(JSON.stringify({ type: "ready" }));
  });
  const resizes = () => messages.filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length;
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await expect.poll(resizes).toBeGreaterThan(0);
  const screen = page.getByRole("region", { name: "Terminal workspace" }).locator(".terminal-screen");
  const before = await screen.boundingBox();
  const sent = resizes();
  let release;
  hold = { promise: new Promise(resolve => { release = resolve; }) };
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.locator(".toolbar-connectivity .dot")).toHaveClass(/checking/);
  await expect(page.getByRole("button", { name: "Refresh" })).toHaveAttribute("aria-busy", "true");
  await expect(page.getByText("Refreshing resource inventory…").filter({ visible: true })).toHaveCount(0);
  await expect(page.getByText("Loading cell details…").filter({ visible: true })).toHaveCount(0);
  await expect(page.getByText("Loading containers…").filter({ visible: true })).toHaveCount(0);
  expect(await screen.boundingBox()).toEqual(before);
  hold = null;
  release();
  await expect(page.locator(".toolbar-connectivity .dot")).not.toHaveClass(/checking/);
  await expect(page.getByRole("button", { name: "Refresh" })).toHaveAttribute("aria-busy", "false");
  await page.waitForTimeout(200);
  expect(await screen.boundingBox()).toEqual(before);
  expect(resizes()).toBe(sent);
});

test("expanded terminal keeps its session, resizes, and follows the appearance setting", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  let connections = 0;
  const messages = [];
  let sendOutput = () => {};
  await page.routeWebSocket("**/attach", socket => {
    connections++;
    socket.onMessage(message => { messages.push(message); });
    socket.send(JSON.stringify({ type: "ready" }));
    sendOutput = text => socket.send(Buffer.from(text));
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  // ANSI white and bright yellow follow the theme: readable greys/amber on the light background,
  // the unchanged dark palette after switching to dark (asserted below).
  sendOutput("\x1b[37mwhite\x1b[93mbright-yellow\x1b[0m $ ");
  const ansiColor = index => panel.locator(`.terminal-session:not([hidden]) .xterm-fg-${index}`).first().evaluate(el => getComputedStyle(el).color);
  await expect.poll(() => ansiColor(7)).toBe("rgb(110, 119, 129)");
  await expect.poll(() => ansiColor(11)).toBe("rgb(154, 103, 0)");
  const screen = panel.locator(".terminal-screen");
  const screenHandle = await screen.elementHandle();
  await page.getByRole("link", { name: "View details for dev / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/resource\/dev\/shared\/services\/api$/);
  await expect(panel).toBeHidden();
  expect(connections).toBe(1);
  await page.getByRole("link", { name: "Open terminal for dev / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  await expect(panel).toBeVisible();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  expect(await screenHandle.evaluate(el => el.isConnected)).toBe(true);
  const initial = await screen.boundingBox();
  const initialResizeCount = messages.filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length;
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  await expect(panel).toHaveClass(/is-expanded/);
  await expect(terminalTools(page).getByRole("button", { name: "Collapse terminal" })).toHaveAttribute("aria-expanded", "true");
  await expect(panel.locator(".terminal-session:not([hidden]) .xterm-helper-textarea")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(panel).toHaveClass(/is-expanded/);
  await expect.poll(() => messages.some(message => Buffer.isBuffer(message) && message.includes(0x1b))).toBe(true);
  await expect.poll(async () => (await screen.boundingBox())?.height).toBeGreaterThan(initial.height);
  await expect.poll(() => messages.filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length).toBeGreaterThan(initialResizeCount);
  expect(connections).toBe(1);
  // Appearance is set on the Settings page (#1686); leaving the cell screen ends the expansion, not the session.
  await setAppearance(page, "Dark");
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  await expect(panel).toHaveClass(/is-expanded/);
  await expect.poll(() => screen.evaluate(el => getComputedStyle(el).backgroundColor)).toBe("rgb(13, 20, 33)");
  await expect.poll(() => ansiColor(7)).toBe("rgb(229, 229, 229)");
  await expect.poll(() => ansiColor(11)).toBe("rgb(255, 255, 0)");
  await terminalTools(page).getByRole("button", { name: "Collapse terminal" }).focus();
  await page.keyboard.press("Escape");
  await expect(panel).not.toHaveClass(/is-expanded/);
  await expect(terminalTools(page).getByRole("button", { name: "Expand terminal" })).toBeFocused();
  expect(connections).toBe(1);
  await page.setViewportSize({ width: 390, height: 844 });
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  await expect(page.getByRole("button", { name: "Toggle sidebar" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await expect(page.getByRole("complementary", { name: "Cell sidebar" })).toBeVisible();
  expect(connections).toBe(1);
});

test("terminal grid stays inside the padded screen across browser zoom levels", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  // Browser zoom shrinks the CSS viewport and raises devicePixelRatio together.
  const cdp = await page.context().newCDPSession(page);
  const overflow = () => page.evaluate(() => {
    const screen = document.querySelector(".terminal-session:not([hidden]) .terminal-screen");
    const style = getComputedStyle(screen);
    const box = screen.getBoundingClientRect();
    const grid = screen.querySelector(".xterm-screen").getBoundingClientRect();
    return Math.max(grid.right - (box.right - parseFloat(style.paddingRight)), grid.bottom - (box.bottom - parseFloat(style.paddingBottom)));
  });
  for (const zoom of [1, 1.1, 1.25, 1.5, 2, 0.8, 0.5]) {
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: Math.round(1400 / zoom), height: Math.round(900 / zoom), deviceScaleFactor: zoom, mobile: false });
    await expect.poll(overflow, { message: `zoom ${zoom}` }).toBeLessThanOrEqual(0.5);
  }
});

test("expanded terminal starts below a wrapped terminal bar", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  // 933px is a 1400px window at 150% zoom: the expanded terminal bar wraps its terminal controls onto a second row.
  await page.setViewportSize({ width: 933, height: 600 });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(panel).toHaveClass(/is-expanded/);
  const bar = page.locator(".cell-terminal-bar");
  await expect.poll(async () => (await panel.boundingBox()).y - ((await bar.boundingBox()).y + (await bar.boundingBox()).height)).toBeGreaterThanOrEqual(0);
  await page.setViewportSize({ width: 1400, height: 900 });
  await expect.poll(async () => (await panel.boundingBox()).y - ((await bar.boundingBox()).y + (await bar.boundingBox()).height)).toBeGreaterThanOrEqual(0);
  await terminalTools(page).getByRole("button", { name: "Collapse terminal" }).click();
  await expect(page.locator(".cell-detail-screen")).not.toHaveAttribute("style", /--terminal-expanded-top/);
});

test("expanded terminal controls stay clear of the toolbar actions", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  const topmost = locator => locator.evaluate(el => { const box = el.getBoundingClientRect(); return el.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)); });
  for (const width of [1400, 933]) {
    await page.setViewportSize({ width, height: 900 });
    await expect.poll(() => topmost(terminalTools(page).getByRole("button", { name: "Detach" }))).toBe(true);
    await expect.poll(() => topmost(terminalTools(page).getByLabel("Container"))).toBe(true);
    await expect.poll(() => topmost(page.getByRole("button", { name: "Refresh" }))).toBe(true);
  }
});

test("Workspace retains independent scoped terminals, focus and resize while switching cells", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  const sockets = new Map();
  const messages = new Map();
  const closed = [];
  await page.routeWebSocket("**/attach", socket => {
    const path = new URL(socket.url()).pathname;
    sockets.set(path, socket);
    messages.set(path, []);
    socket.onMessage(message => messages.get(path).push(message));
    socket.onClose(() => closed.push(path));
    socket.send(JSON.stringify({ type: "ready" }));
    socket.send(Buffer.from(`output for ${path}`));
  });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await page.goto(server.url);
  await cells.getByRole("link", { name: "Open dev / shared / services / api" }).click();
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  const devPath = "/api/realms/dev/spaces/shared/stacks/services/cells/api/containers/shell/attach";
  const prodPath = "/api/realms/prod/spaces/shared/stacks/services/cells/api/containers/shell/attach";
  const devScreen = await page.locator('.terminal-screen[aria-label="Terminal for dev / shared / services / api / shell"]').elementHandle();
  await cells.getByRole("link", { name: "Open prod / shared / services / api" }).click();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  await expect.poll(() => sockets.size).toBe(2);
  expect(closed).toEqual([]);
  expect(await devScreen.evaluate(el => el.isConnected)).toBe(true);
  // Wait for the target cell's session to be the visible one: "Attached" was already true for the session being left.
  const visibleSession = scope => panel.locator(".terminal-session:not([hidden])").filter({ has: page.locator(`.terminal-screen[aria-label="Terminal for ${scope} / shell"]`) });
  await expect(visibleSession("prod / shared / services / api")).toBeVisible();
  await visibleSession("prod / shared / services / api").locator(".xterm-helper-textarea").press("p");
  await expect.poll(() => messages.get(prodPath).some(message => Buffer.isBuffer(message) && message.toString() === "p")).toBe(true);
  expect(messages.get(devPath).some(message => Buffer.isBuffer(message) && message.toString() === "p")).toBe(false);
  await cells.getByRole("link", { name: "Open dev / shared / services / api" }).click();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  expect(await devScreen.evaluate(el => el === document.querySelector('.terminal-screen[aria-label="Terminal for dev / shared / services / api / shell"]'))).toBe(true);
  await expect(visibleSession("dev / shared / services / api")).toBeVisible();
  await visibleSession("dev / shared / services / api").locator(".xterm-helper-textarea").press("d");
  await expect.poll(() => messages.get(devPath).some(message => Buffer.isBuffer(message) && message.toString() === "d")).toBe(true);
  // Reactivating at an unchanged size sends nothing new; only a real size change below does.
  await expect.poll(() => messages.get(devPath).filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length).toBeGreaterThanOrEqual(1);
  const resizedBefore = messages.get(devPath).filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length;
  await page.setViewportSize({ width: 900, height: 700 });
  await expect.poll(() => messages.get(devPath).filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length).toBeGreaterThan(resizedBefore);
  await page.locator(".breadcrumbs").getByRole("link", { name: "Workbench", exact: true }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  expect(closed).toEqual([]);
  await cells.getByRole("link", { name: "Open dev / shared / services / api" }).click();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toContainText("Terminal attached");
});

test("terminal container selection auto-attaches, enforces connection limits, and detaches explicitly", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: Array.from({ length: 21 }, (_, i) => ({ id: `shell-${i}`, attachable: true })) } : undefined);
  const closed = [];
  await page.routeWebSocket("**/attach", socket => {
    socket.onClose(() => closed.push(new URL(socket.url()).pathname));
    socket.send(JSON.stringify({ type: "ready" }));
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  for (let i = 1; i < 20; i++) {
    await terminalTools(page).locator(".terminal-controls select").selectOption(`shell-${i}`);
    await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  }
  await terminalTools(page).locator(".terminal-controls select").selectOption("shell-20");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Connection limit reached");
  await expect(panel.getByText("Terminal limit reached.", { exact: false })).toBeVisible();
  await expect(terminalTools(page).getByText("Terminal limit reached.", { exact: false })).toHaveCount(0);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveAttribute("data-tooltip", /Terminal limit reached/);
  await expect(terminalTools(page).getByRole("button", { name: "Attach" })).toHaveCount(0);
  await terminalTools(page).locator(".terminal-controls select").selectOption("shell-1");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  await expect(panel.getByRole("tab", { name: /shell-1/ })).toHaveCount(0);
  await terminalTools(page).locator(".terminal-controls select").selectOption("shell-0");
  await expect(terminalTools(page).locator(".terminal-controls select")).toHaveValue("shell-0");
  await terminalTools(page).getByRole("button", { name: "Detach" }).click();
  await expect.poll(() => closed.length).toBe(1);
  await terminalTools(page).locator(".terminal-controls select").selectOption("shell-20");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open prod / shared / services / api" }).click();
  await page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" }).click();
  await expect(terminalTools(page).locator(".terminal-controls select")).toHaveValue("shell-20");
});

test("terminal error offers a new attachment and stopped cells release sessions", async ({ page }) => {
  let stopped = false;
  let failInventory = false;
  await mock(page, path => {
    if (path.endsWith("/containers")) return { json: [{ id: "shell", attachable: true }] };
    if (path === "/api/realms" && failInventory) return failure;
    if (path.endsWith("/cells") && path.includes("/dev/")) return { json: fixture(path).map(cell => ({ ...cell, status: { state: stopped ? "Stopped" : "Ready" } })) };
  });
  let attempts = 0;
  let closed = 0;
  await page.routeWebSocket("**/attach", socket => {
    attempts++;
    socket.onClose(() => closed++);
    if (attempts === 1) { socket.send(JSON.stringify({ type: "error", message: "permission denied by attach socket" })); socket.close(); }
    else socket.send(JSON.stringify({ type: "ready" }));
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("permission denied by attach socket");
  const screen = await panel.locator(".terminal-screen").elementHandle();
  await terminalTools(page).getByRole("button", { name: "Reconnect" }).click();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  expect(await screen.evaluate(el => el.isConnected)).toBe(true);
  const closedBefore = closed;
  failInventory = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(inventoryIndicator(page, "stale")).toBeVisible();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  expect(closed).toBe(closedBefore);
  failInventory = false;
  stopped = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(panel.locator(".terminal-log-status")).toContainText("Cell is Stopped. Showing the last output of shell.");
  await expect(panel.getByText("Terminal unavailable", { exact: false })).toHaveCount(0);
  await expect(terminalTools(page).getByText("Terminal unavailable", { exact: false })).toHaveCount(0);
  await expect(terminalTools(page).getByRole("button", { name: "Reconnect" })).toHaveCount(0);
  await expect.poll(() => closed).toBeGreaterThan(closedBefore);
});

test("the Workbench grid is home and the Overview keeps resource summaries and the Manage navigation", async ({ page }) => {
  await mock(page);
  await page.goto(server.url);
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  // The document title names the page (#1567).
  await expect(page).toHaveTitle("Workbench · kukeon");
  await expect(page.locator('link[rel="icon"][type="image/svg+xml"]')).toHaveAttribute("href", /^\/assets\/.+\.svg$/);
  const brand = page.locator("#cell-sidebar").getByRole("link", { name: "kukeon", exact: true });
  await expect(brand).toHaveAttribute("href", "#/");
  await expect(brand.locator(".mark svg")).toHaveCount(1);
  await expect(page.getByText("LOCAL WORKSPACE")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Workspace", exact: true })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Workspace welcome" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "+ New Cell" })).toHaveCount(0);
  expect(await page.evaluate(() => document.body.innerText)).not.toMatch(/Kukeon|Workspace/);
  for (const hash of ["#/", "", "#/workspace", "#/workspace/dev", "#/%E0%A4%A"]) {
    await page.goto(`${server.url}${hash}`);
    await expect(page.locator(".page-heading h1"), hash).toHaveText("Workbench");
    await expect(page.getByRole("region", { name: "Cell terminals" }).locator(".cell-tile"), hash).toHaveCount(2);
  }
  for (const hash of ["#/resources", "#/administration", "#/dashboard"]) {
    await page.goto(`${server.url}${hash}`);
    await expect(page.locator(".page-heading h1"), hash).toHaveText("Overview");
    await expect(page.locator(".summary-card").filter({ hasText: "Cells" }).locator("strong"), hash).toHaveText("2");
  }
  await expect(page).toHaveTitle("Overview · kukeon");
  // The logo opens the grid from Manage too.
  const manageBrand = page.getByRole("complementary", { name: "Manage sidebar" }).getByRole("link", { name: "kukeon", exact: true });
  await expect(manageBrand).toHaveAttribute("href", "#/");
  await page.goto(`${server.url}#/cells`);
  await manageBrand.click();
  await expect(page).toHaveURL(/#\/$/);
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  await page.goto(`${server.url}#/resources`);
  for (const type of ["Realms", "Spaces", "Stacks", "Cells"]) {
    await expect(page.locator(".summary-card").filter({ hasText: type }).locator("strong")).toHaveText("2");
    await expect(nav(page).getByRole("link", { name: type, exact: true })).toBeVisible();
  }
  // One flat list, no group headings (#1635).
  await expect(nav(page).getByRole("link")).toHaveText(["Overview", "Realms", "Spaces", "Stacks", "Cells", "Blueprints", "Configs", "Volumes", "Secrets", "Connectors", "Teams", "Sources", "Images", "Daemon"]);
  await expect(nav(page).getByRole("group")).toHaveCount(0);
  await expect(nav(page).getByRole("heading")).toHaveCount(0);
  await expect(page.getByText(/^(Operate|Configure)$/i)).toHaveCount(0);
  await expect(nav(page).locator('[aria-current="page"]')).toHaveText(["Overview"]);
  await expect(page.locator(".resources-nav")).toHaveCount(0);
  // The toolbar no longer repeats the breadcrumb root, which leads to the Overview on Manage pages.
  await expect(page.locator(".toolbar-actions").getByRole("link", { name: "Overview" })).toHaveCount(0);
  await expect(page.locator(".toolbar-actions").getByRole("link", { name: "New Cell", exact: true })).toBeVisible();
  await expect(page.locator(".breadcrumbs").getByRole("link", { name: "Overview", exact: true })).toHaveAttribute("href", "#/resources");
  await expect(page.locator(".summary-card").filter({ hasText: "Cells" }).getByRole("list", { name: "Cell states" })).toContainText("Running");
  const attention = page.getByRole("region", { name: "Cells needing attention" });
  await expect(attention.getByRole("link", { name: "api", exact: true })).toHaveAttribute("href", "#/resource/prod/shared/services/api");
  await expect(attention.getByRole("listitem")).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "Resource overview" })).toHaveCount(0);
  await expect(page.locator("pre")).toHaveCount(0);
  // A Manage page has no cell list; the Workbench sidebar does.
  await expect(page.getByRole("navigation", { name: "Cell navigation" })).toHaveCount(0);
  await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
});

test("Resources cells open cellRead and the sidebar opens cellTerminal, each linking to the other", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  let opened = 0;
  let closed = 0;
  await page.routeWebSocket("**/attach", socket => { opened++; socket.onClose(() => { closed++; }); socket.send(JSON.stringify({ type: "ready" })); });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  const sidebarCell = cells.getByRole("link", { name: "Open dev / shared / services / api" });
  const connectionState = terminalTools(page).locator(".terminal-connection-state");
  await page.goto(`${server.url}#/cells`);
  await page.getByRole("row").filter({ hasText: "dev / shared / services" }).getByRole("link", { name: "api", exact: true }).click();
  await expect(page).toHaveURL(/#\/resource\/dev\/shared\/services\/api$/);
  await expect(page.getByRole("region", { name: "Cell details" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Resource overview" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Cell terminal" })).toBeHidden();
  await expect(page.getByRole("tablist")).toHaveCount(0);
  await expect(sidebarCell).toHaveAttribute("aria-current", "page");
  // Opening cellRead from Resources inspects the cell without attaching to it.
  await expect(page.locator(".container-panel li").filter({ hasText: "shell" })).toBeVisible();
  await page.waitForTimeout(500);
  expect(opened).toBe(0);
  await expect(sidebarCell.locator(".cell-terminal-state")).toHaveCount(0);
  const toTerminal = page.getByRole("link", { name: "Open terminal for dev / shared / services / api" });
  await expect(toTerminal).toHaveAttribute("title", "Open cell terminal");
  await toTerminal.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  await expect(page.getByRole("region", { name: "Terminal workspace" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Cell details" })).toBeHidden();
  await expect(page.getByRole("tablist")).toHaveCount(0);
  await expect(sidebarCell).toHaveAttribute("aria-current", "page");
  await expect(connectionState).toContainText("Attached");
  expect(opened).toBe(1);

  await page.goto(`${server.url}#/workspace`);
  await sidebarCell.click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  await expect(page.getByRole("region", { name: "Terminal workspace" })).toBeVisible();
  await expect(page.getByRole("tablist")).toHaveCount(0);
  const toDetails = page.getByRole("link", { name: "View details for dev / shared / services / api" });
  await expect(toDetails).toHaveAttribute("title", "View cell details");
  await toDetails.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/#\/resource\/dev\/shared\/services\/api$/);
  await expect(page.getByRole("heading", { name: "Runtime status" })).toBeVisible();
  const containerRow = page.locator(".container-panel li").filter({ hasText: "shell" });
  await expect(containerRow.getByRole("link", { name: "shell", exact: true })).toHaveAttribute("href", "#/resource/dev/shared/services/api/shell");
  await expect(containerRow.getByRole("link", { name: "View document" })).toHaveAttribute("href", "#/resource/dev/shared/services/api/shell?document=1");

  // The session attached on cellTerminal survives cellTerminal → cellRead → cellTerminal on the same socket.
  await page.getByRole("link", { name: "Open terminal for dev / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  await expect(connectionState).toContainText("Attached");
  expect(opened).toBe(1);
  expect(closed).toBe(0);

  // A terminal detached on cellTerminal stays detached through cellRead → cellTerminal.
  await terminalTools(page).getByRole("button", { name: "Detach" }).click();
  await expect.poll(() => closed).toBe(1);
  await expect(connectionState).toContainText("Detached");
  await page.getByRole("link", { name: "View details for dev / shared / services / api" }).click();
  await expect(page.getByRole("heading", { name: "Runtime status" })).toBeVisible();
  await page.getByRole("link", { name: "Open terminal for dev / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  await expect(connectionState).toContainText("Detached");
  await page.waitForTimeout(500);
  expect(opened).toBe(1);
});

test("a cold load of cellRead never opens a terminal session", async ({ page }) => {
  let logReads = 0;
  await mock(page, path => {
    if (path.endsWith("/log")) logReads++;
    return path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }, { id: "web" }] } : undefined;
  });
  let opened = 0;
  await page.routeWebSocket("**/attach", socket => { opened++; socket.send(JSON.stringify({ type: "ready" })); });
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  await expect(page.getByRole("heading", { name: "Runtime status" })).toBeVisible();
  await expect(page.locator(".container-panel li").filter({ hasText: "shell" })).toBeVisible();
  // Give a would-be auto-attach every chance to fire: the inventory and container list have loaded.
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
  await page.waitForTimeout(1000);
  expect(opened).toBe(0);
  expect(logReads).toBe(0);
  await expect(page.locator(".cell-terminal-state")).toHaveCount(0);
  await page.getByRole("link", { name: "Open terminal for dev / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  await expect.poll(() => opened).toBe(1);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  // The log view reads only once it is on screen.
  expect(logReads).toBe(0);
  await terminalTools(page).getByLabel("Container").selectOption("web");
  await expect.poll(() => logReads).toBeGreaterThan(0);
});

// Serves `text` as a container's whole stream: the tail read returns it, a forward read from the end returns nothing new.
const logStream = (request, text) => {
  const offset = Number(new URL(request.url()).searchParams.get("offset"));
  return offset === -1 ? logChunk(text) : logChunk("", offset);
};
const logScreen = page => page.locator("#cell-terminal .terminal-log .xterm-rows");

test("a live cell with no attachable container follows its log", async ({ page }) => {
  const offsets = [];
  await mock(page, (path, request) => {
    if (!path.endsWith("/containers/web/log")) return;
    const offset = Number(new URL(request.url()).searchParams.get("offset"));
    offsets.push(offset);
    if (offset === -1) return logChunk("listening on :8080\n");
    if (offset === 19) return logChunk("GET /healthz 200\n", 19);
    return logChunk("", offset);
  });
  let opened = 0;
  await page.routeWebSocket("**/attach", () => { opened++; });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator("option")).toHaveText(["web (logs)"]);
  await expect(logScreen(page)).toContainText("listening on :8080");
  // The follow timer reads forward from the cursor the tail read returned.
  await expect(logScreen(page)).toContainText("GET /healthz 200", { timeout: 10000 });
  expect(offsets[0]).toBe(-1);
  expect(offsets).toContain(19);
  expect(opened).toBe(0);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Logs");
  for (const name of ["Attach", "Detach", "Reconnect"]) await expect(terminalTools(page).getByRole("button", { name })).toHaveCount(0);
  await expect(page.locator("#cell-terminal .terminal-log-status")).toHaveCount(0);
  await expect(page.locator("#cell-terminal .terminal-overlay")).toHaveCount(0);
  // Read-only: typing into the log xterm sends nothing anywhere.
  const requests = [];
  page.on("request", request => { if (!request.url().endsWith("/log") && !request.url().includes("/log?")) requests.push(request.url()); });
  await page.locator("#cell-terminal .terminal-log .xterm-helper-textarea").focus();
  await page.keyboard.type("ls\n");
  expect(requests.filter(url => url.includes("/containers/web/"))).toEqual([]);
  expect(opened).toBe(0);
  // Expand resizes the log view with the panel.
  const before = await page.locator("#cell-terminal .terminal-log .terminal-screen").boundingBox();
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  await expect(page.locator("#cell-terminal")).toHaveClass(/is-expanded/);
  await expect.poll(async () => (await page.locator("#cell-terminal .terminal-log .terminal-screen").boundingBox())?.height).toBeGreaterThan(before.height);
  await expect(logScreen(page)).toContainText("listening on :8080");
});

test("the selector switches between a terminal and a log without reattaching", async ({ page }) => {
  await mock(page, (path, request) => {
    if (path.endsWith("/containers")) return { json: [{ id: "shell", attachable: true }, { id: "web" }] };
    if (path.endsWith("/containers/web/log")) return logStream(request, "served /index.html\n");
  });
  let opened = 0;
  let closed = 0;
  await page.routeWebSocket("**/attach", socket => { opened++; socket.onClose(() => closed++); socket.send(JSON.stringify({ type: "ready" })); });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const select = terminalTools(page).getByLabel("Container");
  await expect(terminalTools(page).locator("option")).toHaveText(["shell", "web (logs)"]);
  await expect(select).toHaveValue("shell");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  expect(opened).toBe(1);
  await select.selectOption("web");
  await expect(logScreen(page)).toContainText("served /index.html");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Logs");
  await expect(page.locator("#cell-terminal .terminal-session:not([hidden]):not(.terminal-log)")).toHaveCount(0);
  await select.selectOption("shell");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await expect(page.locator("#cell-terminal .terminal-log")).toHaveCount(0);
  await page.waitForTimeout(300);
  expect(opened).toBe(1);
  expect(closed).toBe(0);
});

test("a dead cell shows its last output and Start brings the terminal back", async ({ page }) => {
  let started = false;
  let hold = null;
  const cell = "/api/realms/dev/spaces/shared/stacks/services/cells/api";
  await mock(page, async (path, request) => {
    if (request.method() === "POST" && path === `${cell}/start`) {
      if (hold) await hold.promise;
      started = true;
      return { json: {} };
    }
    const state = started ? "Ready" : "Exited";
    if (path.endsWith("/containers")) return { json: [{ id: "shell", attachable: true }] };
    if (path.endsWith("/containers/shell/log")) return logStream(request, "\x1b[32mdone\x1b[0m\r\n$ exit\r\n");
    if (path === cell) return { json: { ...doc(["dev", "shared", "services", "api"]), status: { state } } };
    if (path.endsWith("/cells") && path.includes("/dev/")) return { json: fixture(path).map(item => ({ ...item, status: { state } })) };
  });
  let opened = 0;
  await page.routeWebSocket("**/attach", socket => { opened++; socket.send(JSON.stringify({ type: "ready" })); });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const panel = page.locator("#cell-terminal");
  await expect(logScreen(page)).toContainText("done");
  await expect(logScreen(page)).toContainText("$ exit");
  // The capture is replayed as terminal bytes, so ANSI colors survive.
  await expect(panel.locator(".terminal-log .xterm-fg-2")).toHaveText("done");
  await expect(panel.getByText("Terminal unavailable", { exact: false })).toHaveCount(0);
  await expect(panel.locator(".terminal-log-status")).toContainText("Cell is Exited. Showing the last output of shell.");
  expect(opened).toBe(0);
  let release;
  hold = { promise: new Promise(resolve => { release = resolve; }) };
  await panel.getByRole("button", { name: "Start" }).click();
  // The strip's Start shares the title row's pending lock.
  await expect(panel.getByRole("button", { name: "Start" })).toBeDisabled();
  await expect(page.locator(".cell-page-title-row").getByRole("button", { name: "Start…", exact: true })).toBeDisabled();
  hold = null;
  release();
  await expect.poll(() => opened).toBe(1);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await expect(panel.locator(".terminal-log")).toHaveCount(0);
  await expect(panel.locator(".terminal-log-status")).toHaveCount(0);
});

test("a container with no output yet shows a neutral message", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(page.locator("#cell-terminal").getByText("No output yet.")).toBeVisible();
  await expect(page.locator("#cell-terminal [role=\"alert\"]")).toHaveCount(0);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Logs");
});

for (const theme of ["light", "dark"]) {
  test(`the log view follows the ${theme} theme`, async ({ page }) => {
    await page.addInitScript(value => localStorage.setItem("kukeon-theme", value), theme);
    await mock(page, (path, request) => path.endsWith("/containers/web/log") ? logStream(request, "ready\n") : undefined);
    await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
    await expect(logScreen(page)).toContainText("ready");
    await expect.poll(() => logScreen(page).evaluate(el => getComputedStyle(el).color)).toBe(theme === "dark" ? "rgb(229, 230, 236)" : "rgb(32, 35, 43)");
  });
}

for (const [width, height] of [[1440, 900], [390, 844]]) {
  for (const theme of ["light", "dark"]) {
    test(`cellRead panels render at full content height and the page scrolls at ${width}x${height} (${theme})`, async ({ page }) => {
      await page.addInitScript(value => localStorage.setItem("kukeon-theme", value), theme);
      await page.setViewportSize({ width, height });
      await mock(page, path => path.endsWith("/cells/api/cgroup") ? { json: { cgroupPath: "/kukeon/dev/shared/services/api", absolutePath: "/sys/fs/cgroup/kukeon/dev/shared/services/api", exists: true, ready: true, nestedCgroupRuntime: true, subtreeControllers: ["cpu", "io", "memory", "pids"], live: { memory: { max: { state: "set", raw: "1073741824", value: 1073741824 } }, io: { state: "set", entries: [] } } } }
        : path.endsWith("/containers") ? { json: [{ id: "root", root: true }, { id: "web" }, { id: "shell", attachable: true }] } : undefined);
      await page.goto(`${server.url}#/resource/dev/shared/services/api`);
      const panels = page.locator(".cell-overview-content > .panel");
      await expect(panels).toHaveCount(5);
      // Network and Cgroup start collapsed (#1568); this test measures the page with both open.
      await expect(page.getByRole("region", { name: "cell cgroup" }).getByText("cpu io memory pids", { exact: true })).toBeHidden();
      await expand(page, "cell network");
      await expand(page, "cell cgroup");
      await expect(page.getByRole("region", { name: "cell cgroup" }).getByText("cpu io memory pids", { exact: true })).toBeVisible();
      await expect(page.getByRole("region", { name: "cell network" })).toBeVisible();
      const sizes = await panels.evaluateAll(items => items.map(item => ({ client: item.clientHeight, scroll: item.scrollHeight })));
      for (const size of sizes) expect(size.client).toBeGreaterThanOrEqual(size.scroll);
      expect(await page.evaluate(() => document.documentElement.dataset.theme)).toBe(theme);
      expect(await page.evaluate(() => document.scrollingElement.scrollHeight > innerHeight)).toBe(true);
      await page.evaluate(() => window.scrollTo(0, document.scrollingElement.scrollHeight));
      expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
      await expect(page.getByRole("region", { name: "cell cgroup" })).toBeInViewport();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    });
  }
}

// Issue #1568, item C: cellRead leads with status, containers and the source Config; runtime internals are disclosures.
test("cellRead keeps Network and Cgroup collapsed with a summary line, opens them by keyboard and remembers the choice", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const network = { cell: { bridgeName: "k-0123abcd", hostNetwork: false, ipv4: "10.88.1.5", ipv4State: "assigned", rootContainer: "root", containers: [{ id: "root", root: true }, { id: "web" }] } };
  const cgroup = { cgroupPath: "/kukeon/dev/shared/services/api", absolutePath: "/sys/fs/cgroup/kukeon/dev/shared/services/api", exists: true, ready: true, subtreeControllers: ["cpu", "memory"], live: { memory: { current: { state: "set", raw: "327155712", value: 327155712 }, max: { state: "set", raw: "1073741824", value: 1073741824 } }, pids: { current: { state: "set", raw: "12", value: 12 } } } };
  const reads = { network: 0, cgroup: 0 };
  await mock(page, path => {
    if (path.endsWith("/cells/api/network")) { reads.network++; return { json: network }; }
    if (path.endsWith("/cells/api/cgroup")) { reads.cgroup++; return { json: cgroup }; }
    if (path.endsWith("/containers")) return { json: [{ id: "root", root: true }, { id: "web", attachable: true }] };
    if (path.endsWith("/cells/api")) return { json: { ...doc(["dev", "shared", "services", "api"]), spec: { realmId: "dev", spaceId: "shared", stackId: "services", provenance: { bindingKind: "config", bindingRef: { name: "app", realm: "dev", space: "shared" } } } } };
  });
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const details = page.getByRole("region", { name: "Cell details" });
  const networkPanel = page.getByRole("region", { name: "cell network" });
  const cgroupPanel = page.getByRole("region", { name: "cell cgroup" });
  await expect(details.locator("details")).toHaveCount(2);
  await expect(networkPanel.locator("summary")).toContainText("Bridged (space network) · 10.88.1.5 · 2 containers");
  await expect(cgroupPanel.locator("summary")).toContainText("Ready · memory 312 MiB / 1 GiB · 12 PIDs");
  for (const panel of [networkPanel, cgroupPanel]) {
    await expect(panel.locator("details")).not.toHaveAttribute("open");
    await expect(panel.locator(".panel-body")).toBeHidden();
  }
  // Status, containers, then the overview with the source Config: all above the collapsed internals and
  // inside the first 1440x900 viewport, where the open sections used to push them 2,600px apart.
  await expect(details.locator(":scope > .panel h2")).toHaveText(["Runtime status", "Containers", "Resource overview", "Network", "Cgroup"]);
  const config = details.locator(".resource-overview dt:text-is('Config') + dd").getByRole("link");
  await expect(config).toHaveText("dev / shared / app");
  await expect(config).toHaveAttribute("href", "#/configs/dev/shared/app");
  for (const visible of [page.getByRole("heading", { name: "Runtime status" }), details.locator(".container-panel li").filter({ hasText: "web" }), config, networkPanel.locator("summary")]) await expect(visible).toBeInViewport({ ratio: 1 });
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
  expect(await page.evaluate(() => document.scrollingElement.scrollHeight)).toBeLessThan(1100);
  // Every overview field shown before is still there.
  // …and the IP address row (#1630), from the same network read as the Network section.
  await expect(details.locator(".resource-overview dt")).toHaveText(["Config", "IP address", "Labels", "Name", "Kind", "Scope", "API version"]);
  await expect(details.locator(".resource-overview dt:text-is('IP address') + dd")).toHaveText("10.88.1.5 · root container root");

  // Each summary is keyboard-operable: Enter opens Network, Space opens Cgroup.
  await networkPanel.locator("summary").focus();
  await page.keyboard.press("Enter");
  await expect(networkPanel.locator("details")).toHaveAttribute("open");
  await expect(networkPanel.getByText("k-0123abcd")).toBeVisible();
  await expect(networkPanel.locator("table.network-containers tr").filter({ hasText: "web" })).toBeVisible();
  await cgroupPanel.locator("summary").focus();
  await page.keyboard.press("Space");
  await expect(cgroupPanel.getByText("/sys/fs/cgroup/kukeon/dev/shared/services/api")).toBeVisible();
  await expect(cgroupPanel.locator(".cgroup-live")).toBeVisible();
  // One read per section: the summary line and the body share it.
  expect(reads).toEqual({ network: 1, cgroup: 1 });
  await page.keyboard.press("Space");
  await expect(cgroupPanel.locator(".panel-body")).toBeHidden();

  // The choice is remembered per section across a reload.
  await page.reload();
  await expect(networkPanel.locator("details")).toHaveAttribute("open");
  await expect(networkPanel.getByText("k-0123abcd")).toBeVisible();
  await expect(cgroupPanel.locator("details")).not.toHaveAttribute("open");
});

test("a cell not created from a Config says so, and a failed internal read stays visible on the collapsed row", async ({ page }) => {
  await mock(page, path => path.endsWith("/cells/api/network") ? { status: 403, json: { error: { code: "permission_denied", message: "network read denied" } } } : undefined);
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const details = page.getByRole("region", { name: "Cell details" });
  await expect(details.locator(".resource-overview dt:text-is('Config') + dd")).toHaveText("Not created from a Config");
  await expect(details.locator(".resource-overview").getByRole("link", { name: /Config/ })).toHaveCount(0);
  const networkPanel = page.getByRole("region", { name: "cell network" });
  await expect(networkPanel.locator("summary")).toContainText("Permission denied");
  await expect(networkPanel.locator("summary")).toContainText("Stale values");
  await expand(page, "cell network");
  await expect(networkPanel.getByRole("alert")).toContainText("network read denied");
});

test("the container page collapses Network and Cgroup the same way, while realm, space and stack pages keep them open", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers/web/network") ? { json: { cell: { hostNetwork: false, ipv4State: "assigned", ipv4: "10.88.1.5", rootContainer: "root" }, container: { id: "web" } } }
    : path.endsWith("/containers/web/cgroup") ? { json: { cgroupPath: "/kukeon/web", exists: false, ready: false } } : undefined);
  await page.goto(`${server.url}#/resource/prod/shared/services/api/web`);
  const networkPanel = page.getByRole("region", { name: "container network" });
  const cgroupPanel = page.getByRole("region", { name: "container cgroup" });
  await expect(networkPanel.locator("summary")).toContainText("Cell network namespace");
  await expect(networkPanel.locator("summary")).not.toContainText("declared");
  await expect(cgroupPanel.locator("summary")).toContainText("Cgroup not present");
  await expect(networkPanel.locator(".panel-body")).toBeHidden();
  await expect(cgroupPanel.locator(".panel-body")).toBeHidden();
  await expand(page, "container network");
  await expect(networkPanel.locator("dt:text-is('Host network') + dd")).toHaveText("No (default)");
  await expand(page, "container cgroup");
  await expect(cgroupPanel.getByText("kukeond found no cgroup at this path")).toBeVisible();
  // A realm, space or stack page has no disclosure: its sections render open as before.
  await page.goto(`${server.url}#/resource/dev/shared`);
  await expect(page.getByRole("region", { name: "space network" }).locator(".panel-body")).toBeVisible();
  await expect(page.getByRole("region", { name: "space cgroup" }).locator(".panel-body")).toBeVisible();
  await expect(content(page).locator("details.disclosure, .disclosure details")).toHaveCount(0);
});

test("cell sidebar uses full scope for duplicate names and switching reuses inventory", async ({ page }) => {
  let inventoryReads = 0;
  await mock(page, path => {
    if (path === "/api/realms" || path.endsWith("/spaces") || path.endsWith("/stacks") || path.endsWith("/cells")) inventoryReads++;
  });
  await page.goto(server.url);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(cells.getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toBeVisible();
  const initialReads = inventoryReads;
  await cells.getByRole("link", { name: "Open dev / shared / services / api" }).focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  // The default container has no terminal and no output yet: its log view shows a neutral message.
  await expect(page.locator("#cell-terminal .terminal-empty")).toHaveText("No output yet.");
  await expect(page.locator("#cell-terminal [role=\"alert\"]")).toHaveCount(0);
  await page.getByRole("link", { name: "View details for dev / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/resource\/dev\/shared\/services\/api$/);
  await expect(cells.getByRole("link", { name: "Open dev / shared / services / api" })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { name: "Runtime status" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Containers" })).toBeVisible();
  await expect(page.locator("#cell-terminal")).toHaveCount(1);
  await expect(page.getByRole("link", { name: "+ New Cell" })).toHaveCount(0);
  await expect(page.locator(".toolbar").getByRole("link", { name: "New Cell", exact: true })).toHaveAttribute("href", "#/new-cell");
  await cells.getByRole("link", { name: "Open prod / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/workspace\/prod\/shared\/services\/api$/);
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toHaveAttribute("aria-current", "page");
  expect(inventoryReads).toBe(initialReads);
  await page.goBack();
  await expect(page).toHaveURL(/#\/resource\/dev\/shared\/services\/api$/);
  await page.goBack();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
});

test("sidebar grouping, missing labels, search, collapse, and preference persistence", async ({ page }) => {
  await mock(page, path => {
    if (!path.endsWith("/cells")) return;
    const prod = path.includes("/prod/");
    return { json: fixture(path).map(cell => ({ ...cell, metadata: { ...cell.metadata, labels: prod ? {} : { team: "platform" } }, status: { state: prod ? "Degraded" : "Failed" } })) };
  });
  await defaultStyle(page);
  await page.goto(server.url);
  const sidebar = page.getByRole("region", { name: "All cells" });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(sidebar.getByLabel("Group by")).toHaveValue("");
  await expect(cells.getByRole("link")).toHaveCount(2);
  await expect(cells.getByRole("link", { name: "Open dev / shared / services / api" })).toContainText("Failed");
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toContainText("Degraded");
  await sidebar.getByLabel("Group by").selectOption("team");
  await expect(cells.getByRole("region", { name: "platform group" })).toBeVisible();
  await expect(cells.getByRole("region", { name: "Ungrouped group" })).toBeVisible();
  const ungrouped = cells.getByRole("region", { name: "Ungrouped group" });
  await ungrouped.getByRole("button", { name: /Ungrouped/ }).focus();
  await page.keyboard.press("Enter");
  await expect(ungrouped.getByRole("link")).toHaveCount(0);
  await page.reload();
  await expect(sidebar.getByLabel("Group by")).toHaveValue("team");
  await expect(ungrouped.getByRole("button", { name: /Ungrouped/ })).toHaveAttribute("aria-expanded", "false");
  await ungrouped.getByRole("button", { name: /Ungrouped/ }).click();
  await sidebar.getByRole("searchbox").fill("prod / shared");
  await expect(cells.getByRole("link")).toHaveCount(1);
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toBeVisible();
  await sidebar.getByRole("searchbox").fill("missing");
  await expect(sidebar.getByText("No matching cells.")).toBeVisible();
  await sidebar.getByLabel("Group by").selectOption("");
  await page.reload();
  await expect(sidebar.getByLabel("Group by")).toHaveValue("");
});

test("a literal Ungrouped label stays separate from missing labels", async ({ page }) => {
  await mock(page, path => {
    if (!path.endsWith("/cells")) return;
    const prod = path.includes("/prod/");
    return { json: fixture(path).map(cell => ({ ...cell, metadata: { ...cell.metadata, labels: prod ? {} : { team: "Ungrouped" } } })) };
  });
  await defaultStyle(page);
  await page.goto(server.url);
  await page.getByRole("region", { name: "All cells" }).getByLabel("Group by").selectOption("team");
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(cells.getByRole("region", { name: "Ungrouped (label value) group" }).getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
  await expect(cells.getByRole("region", { name: "Ungrouped group", exact: true }).getByRole("link", { name: "Open prod / shared / services / api" })).toBeVisible();
});

test("large inventories limit initial sidebar DOM and reveal more cells on demand", async ({ page }) => {
  await mock(page, path => {
    const result = fixture(path, 61);
    return { json: path.endsWith("/cells") ? result.map(cell => ({ ...cell, metadata: { ...cell.metadata, labels: { team: cell.metadata.name } } })) : result };
  });
  await defaultStyle(page);
  await page.goto(server.url);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(cells.getByRole("link")).toHaveCount(100);
  await cells.getByRole("button", { name: "Show 22 more cells" }).click();
  await expect(cells.getByRole("link")).toHaveCount(122);
  await page.getByRole("region", { name: "All cells" }).getByLabel("Group by").selectOption("team");
  await expect(cells.getByRole("region", { name: /group$/ })).toHaveCount(25);
  await cells.getByRole("button", { name: "Show 25 more groups" }).click();
  await expect(cells.getByRole("region", { name: /group$/ })).toHaveCount(50);
});

test("failed inventory refresh retains cells while daemon connectivity changes independently", async ({ page }) => {
  let fail = false;
  await mock(page, path => fail && path === "/api/realms" ? failure : undefined);
  await page.goto(server.url);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(cells.getByRole("link")).toHaveCount(2);
  fail = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Unavailable");
  await expect(inventoryIndicator(page, "stale")).toBeVisible();
  await expect(cells.getByRole("link")).toHaveCount(2);
  await expect(page.getByRole("region", { name: "All cells" })).toContainText("Inventory incomplete or stale");
  fail = false;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Connected");
  await expect(page.locator(".stale")).toHaveCount(0);
  await expect(page.locator(".inventory-indicator")).toHaveCount(0);
});

test("periodic refresh waits for a slow inventory traversal", async ({ page }) => {
  await page.clock.install();
  let realmReads = 0;
  let slowScopeReads = 0;
  let releaseScope;
  const slowScope = new Promise(resolve => { releaseScope = resolve; });
  await mock(page, async path => {
    if (path === "/api/realms") realmReads++;
    if (path === "/api/realms/dev/spaces") {
      slowScopeReads++;
      await slowScope;
    }
  });
  await page.goto(server.url);
  try {
    await expect.poll(() => slowScopeReads).toBeGreaterThan(0);
    const initialReads = realmReads;
    await page.clock.fastForward(60_001);
    expect(realmReads).toBe(initialReads);
    releaseScope();
    await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link")).toHaveCount(2);
    expect(realmReads).toBe(initialReads);
    await page.clock.fastForward(60_000);
    await expect.poll(() => realmReads).toBeGreaterThan(initialReads);
  } finally {
    releaseScope();
  }
});

test("the inventory refreshes at the Profile's interval, 10 s by default", async ({ page }) => {
  await page.clock.install();
  let realmReads = 0;
  await mock(page, path => { if (path === "/api/realms") realmReads++; });
  await page.goto(server.url);
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link")).toHaveCount(2);
  await expect(page.locator("main footer")).toContainText("Refreshes every 10 s.");
  const after = async (ms, reads) => { await page.clock.fastForward(ms); await expect.poll(() => realmReads).toBe(reads); };
  const start = realmReads;
  await after(9_000, start);
  await after(1_100, start + 1);
  // 30 s from the Profile: the new interval applies at once, without a reload.
  const settings = await openSettings(page);
  await settings.getByLabel("Refresh interval", { exact: true }).selectOption("30");
  await expect(page.locator("main footer")).toContainText("Refreshes every 30 s.");
  const before = realmReads;
  await after(20_000, before);
  await after(10_100, before + 1);
});

test("the unread check and the grid thumbnails poll at their own Profile intervals, 2 s and 5 s by default", async ({ page }) => {
  await page.clock.install();
  let screenReads = 0;
  await mock(page, path => { if (path.endsWith("/screenshot")) { screenReads++; return screen("$ "); } });
  const activity = await mockActivity(page, { "dev/shared/services/api": counters(1), "prod/shared/services/api": counters(1) });
  await page.goto(server.url);
  for (const scope of ["dev / shared / services / api", "prod / shared / services / api"]) await expect(tile(page, scope).locator("pre")).toHaveText("$");
  await expect.poll(() => activity.answered()).toBeGreaterThan(0);
  const reads = () => ({ activity: activity.answered(), screens: screenReads });
  const after = async (ms, expected) => { await page.clock.fastForward(ms); await expect.poll(reads).toEqual(expected); };
  let start = reads();
  // Defaults: the unread check every 2 s, a pass of both thumbnails every 5 s.
  await after(2_100, { activity: start.activity + 1, screens: start.screens });
  await after(2_100, { activity: start.activity + 2, screens: start.screens });
  await after(2_100, { activity: start.activity + 3, screens: start.screens + 2 });
  // 10 s and 30 s from the Profile: back on the Workbench, both follow the new intervals.
  const settings = await openSettings(page);
  await settings.getByLabel("Unread check interval").selectOption("10");
  await settings.getByLabel("Thumbnail refresh interval").selectOption("30");
  await page.goto(`${server.url}#/`);
  await expect.poll(() => screenReads).toBeGreaterThanOrEqual(start.screens + 4);
  await expect.poll(() => activity.answered()).toBeGreaterThan(start.activity + 3);
  start = reads();
  await after(8_500, start);
  await after(1_600, { activity: start.activity + 1, screens: start.screens });
  await page.clock.fastForward(18_500);
  await expect.poll(() => screenReads).toBe(start.screens);
  await page.clock.fastForward(2_000);
  await expect.poll(() => screenReads).toBe(start.screens + 2);
});

test("initial inventory failure is distinct from an empty inventory", async ({ page }) => {
  await mock(page, () => failure);
  await page.goto(server.url);
  await expect(page.getByRole("region", { name: "All cells" })).toContainText("Cells unavailable");
  await expect(page.getByRole("region", { name: "All cells" })).not.toContainText("No cells found");
  await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Unavailable");
});

test("empty inventory keeps the toolbar New Cell entry point without inventing cells", async ({ page }) => {
  await mockCatalog(page);
  await page.unroute("**/api/realms**");
  await page.route("**/api/realms**", route => route.fulfill({ json: [] }));
  await page.goto(server.url);
  await expect(page.getByRole("region", { name: "All cells" })).toContainText("No cells found");
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  // The grid's empty state offers New Cell too (#1635).
  const grid = page.getByRole("region", { name: "Cell terminals" });
  await expect(grid.getByRole("heading", { name: "No active cells" })).toBeVisible();
  await expect(grid.getByRole("link", { name: "New Cell", exact: true })).toHaveAttribute("href", "#/new-cell");
  await expect(grid.locator(".cell-tile")).toHaveCount(0);
  await page.locator(".toolbar").getByRole("link", { name: "New Cell", exact: true }).click();
  await expect(page).toHaveURL(/#\/new-cell$/);
  await expect(page.locator(".page-heading h1")).toHaveText("New Cell");
});

for (const type of ["Realms", "Spaces", "Stacks", "Cells"]) {
  test(`${type} list shows entries and scope, with complete documents only on request`, async ({ page }) => {
    await mock(page);
    await page.goto(`${server.url}#/${type.toLowerCase()}`);
    await expect(page.locator(".page-heading h1")).toHaveText(type);
    await expect(page.getByRole("row")).toHaveCount(3);
    await expect(page.locator("pre")).toHaveCount(0);
    const first = page.getByRole("row").nth(1);
    // The checkbox column leads (#1745), then Name and State.
    await expect(first.getByRole("cell").nth(2)).toContainText("Ready");
    await first.getByRole("link", { name: /View document/ }).click();
    await expect(page.locator("pre")).toContainText('"futureField"');
    await page.getByRole("link", { name: "Show overview" }).click();
    await expect(page.locator("pre")).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Resource overview" })).toBeVisible();
  });
}

// #1745: row selection and bulk actions on the list tables.
test("the Cells table selects every matching row across pages and deletes them in one confirmed batch, four at a time, listing the one that failed", async ({ page }) => {
  const deleted = new Set();
  const posts = [];
  let inFlight = 0, peak = 0;
  const cellsPath = /\/cells$/;
  await mock(page, async (path, request) => {
    if (request.method() === "POST") {
      posts.push(path);
      inFlight++; peak = Math.max(peak, inFlight);
      await new Promise(resolve => setTimeout(resolve, 40));
      inFlight--;
      if (path === "/api/realms/dev/spaces/shared/stacks/services/cells/api-08/delete") return { status: 409, json: { error: { code: "invalid_state", message: "cell is busy" } } };
      deleted.add(path.replace(/\/delete$/, ""));
      return { json: { action: "delete", metadataDeleted: true, cgroupDeleted: true, containersDeleted: true } };
    }
    // Even cells are Stopped, odd ones Ready: 30 stopped cells over two realms, two pages of them.
    if (cellsPath.test(path)) return { json: fixture(path, 30).filter(cell => !deleted.has(`${path}/${cell.metadata.name}`)).map(cell => ({ ...cell, status: { state: Number(cell.metadata.name.slice(-2)) % 2 ? "Ready" : "Stopped" } })) };
    if (deleted.has(path)) return { status: 404, json: { error: { code: "not_found", message: "cell not found" } } };
  });
  await page.goto(`${server.url}#/cells`);
  const list = page.getByRole("region", { name: "Cells list" });
  await expect(list.locator(".result-count")).toHaveText("60 results");
  await list.getByLabel("Filter by state").selectOption("inactive");
  await expect(list.locator(".result-count")).toHaveText("30 results");
  const bar = list.getByRole("region", { name: "Bulk actions" });
  await expect(bar).toHaveCount(0);
  // The header box takes every matching row, not only the 25 on this page.
  await list.getByRole("checkbox", { name: "Select all 30 cells matching" }).check();
  await expect(bar.getByRole("status")).toHaveText("30 cells selected");
  await expect(list.locator("tbody .bulk-check:checked")).toHaveCount(25);
  await list.getByRole("button", { name: "Next" }).click();
  await expect(list.locator("tbody .bulk-check:checked")).toHaveCount(5);
  // Stopped cells: Start, Kill, Delete and Purge apply; Stop and Restart need a Ready cell.
  await expect(bar.getByRole("button")).toHaveText(["Start…", "Kill…", "Delete…", "Purge…", "Clear"]);
  await bar.getByRole("button", { name: "Delete…" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete 30 cells" });
  await expect(dialog.getByRole("list", { name: "Targets" }).getByRole("listitem")).toHaveCount(30);
  const confirm = dialog.getByRole("button", { name: "Delete 30 cells" });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole("checkbox", { name: /I understand Delete removes these cells/ }).check();
  await confirm.click();
  await expect(toasts(page).filter({ hasText: "Delete: 29 of 30 cells done, 1 failed." })).toBeVisible();
  // One summary toast for the batch, not one per cell.
  await expect(toasts(page)).toHaveCount(1);
  expect(posts).toHaveLength(30);
  expect(peak).toBe(4);
  const report = list.getByRole("alert", { name: "Delete report" });
  await expect(report.getByRole("list", { name: "Failed" })).toContainText("dev / shared / services / api-08");
  await expect(report.getByRole("list", { name: "Failed" })).toContainText("cell is busy");
  await expect(bar).toHaveCount(0);
  await expect(list.locator(".result-count")).toHaveText("1 results");
});

test("every list table selects all its matching rows, and a batch skips each kuke-system target and asks for a typed purge phrase", async ({ page }) => {
  // A kuke-system realm next to dev: each of its rows is skipped, whatever the action.
  await mockCatalog(page);
  await page.route("**/api/realms", route => route.fulfill({ json: [doc(["dev"]), doc(["kuke-system"])] }));
  for (const [type, noun] of [["Realms", "realms"], ["Spaces", "spaces"], ["Stacks", "stacks"], ["Cells", "cells"]]) {
    await page.goto(`${server.url}#/${type.toLowerCase()}`);
    const list = page.getByRole("region", { name: `${type} list` });
    await expect(list.locator(".result-count")).toHaveText("2 results");
    await list.getByRole("checkbox", { name: `Select all 2 ${noun} matching` }).check();
    const bar = list.getByRole("region", { name: "Bulk actions" });
    await expect(bar.getByRole("status"), type).toHaveText(`2 ${noun} selected`);
    await bar.getByRole("button", { name: "Purge…" }).click();
    const one = noun.replace(/s$/, "");
    const dialog = page.getByRole("dialog", { name: `Purge 1 ${one}` });
    await expect(dialog.getByRole("list", { name: "Targets" }), type).toHaveText(type === "Realms" ? "dev" : new RegExp(`^dev / `));
    await expect(dialog.getByRole("list", { name: "Skipped" }), type).toContainText("kuke-system");
    await expect(dialog.getByRole("list", { name: "Skipped" }), type).toContainText("inside kuke-system, where kukeond runs");
    const confirm = dialog.getByRole("button", { name: `Purge 1 ${one}` });
    await expect(confirm).toBeDisabled();
    await dialog.getByRole("textbox").fill(`purge 1 ${one}`);
    await expect(confirm, type).toBeEnabled();
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toHaveCount(0);
    // Clear empties the selection; the header box clears it too.
    await bar.getByRole("button", { name: "Clear" }).click();
    await expect(bar).toHaveCount(0);
  }
  // The catalog lists: Delete only.
  await page.goto(`${server.url}#/configs`);
  const configs = page.getByRole("region", { name: "Configs list" });
  await configs.getByRole("checkbox", { name: "Select all 4 configs matching" }).check();
  const bar = configs.getByRole("region", { name: "Bulk actions" });
  await expect(bar.getByRole("status")).toHaveText("4 configs selected");
  await expect(bar.getByRole("button")).toHaveText(["Delete…", "Clear"]);
  await configs.getByRole("checkbox", { name: "Select all 4 configs matching" }).uncheck();
  await expect(bar).toHaveCount(0);
});

test("lists filter by name and realm and retain complete scope for duplicate names", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/cells`);
  await expect(page.getByRole("row")).toHaveCount(3);
  await page.getByLabel("Filter by realm").selectOption("prod");
  await expect(page.getByRole("row")).toHaveCount(2);
  await expect(page.getByRole("row").nth(1)).toContainText("prod / shared / services");
  await listSearch(page).fill("missing");
  await expect(page.getByText("No matching resources")).toBeVisible();
  await listSearch(page).fill("api");
  await page.getByRole("link", { name: "api", exact: true }).click();
  await expect(page).toHaveURL(/#\/resource\/prod\/shared\/services\/api$/);
  await expect(page.locator("pre")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Containers" })).toBeVisible();
  await expect(page.getByRole("link", { name: "View document", exact: true }).first()).toHaveAttribute("target", "_blank");
  await page.getByRole("link", { name: "web", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "View document", exact: true }).click();
  await expect(page.getByLabel("Complete container JSON")).toHaveText(JSON.stringify(doc(["prod", "shared", "services", "api", "web"]), null, 2));
});

test("scope shortcuts and browser history retain navigation", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/resource/prod`);
  await page.getByRole("link", { name: "View spaces in this realm" }).click();
  await expect(page.getByRole("row")).toHaveCount(2);
  await expect(page.getByText("Scope: prod")).toBeVisible();
  await page.getByRole("link", { name: "Clear scope" }).click();
  await expect(page.getByRole("row")).toHaveCount(3);
  await page.goBack();
  await expect(page.getByText("Scope: prod")).toBeVisible();
  await page.reload();
  await expect(page.getByRole("row")).toHaveCount(2);
});

test("large lists paginate and search resets the current page", async ({ page }) => {
  await mock(page, path => ({ json: fixture(path, 16) }));
  await page.goto(`${server.url}#/cells`);
  await expect(page.getByText("1–25 of 32")).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect(page.getByText("26–32 of 32")).toBeVisible();
  await listSearch(page).fill("api-00");
  await expect(page.getByText("1–2 of 2")).toBeVisible();
  await expect(page.getByRole("button", { name: "Previous" })).toBeDisabled();
});

test("refresh failures mark retained inventory and documents stale, retry recovers", async ({ page }) => {
  let fail = false;
  await mock(page, () => fail ? failure : undefined);
  await page.goto(`${server.url}#/resource/prod/shared/services/api?document=1`);
  await expect(page.getByLabel("Complete cell JSON")).toBeVisible();
  await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Connected");
  fail = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(inventoryIndicator(page, "stale")).toBeVisible();
  await expect(page.getByText("Stale resource details — refresh failed.")).toBeVisible();
  await expect(page.getByLabel("Complete cell JSON")).toContainText('"api"');
  fail = false;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.locator(".stale")).toHaveCount(0);
  await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Connected");
});

test("partial scope failures never present counts as complete", async ({ page }) => {
  await mock(page, path => path === "/api/realms/prod/spaces/shared/stacks" ? failure : undefined);
  await page.goto(`${server.url}#/administration`);
  await inventoryIndicator(page, "incomplete").click();
  const popover = page.getByRole("dialog", { name: "Inventory status" });
  await expect(popover).toContainText("Inventory incomplete");
  await expect(popover.getByRole("listitem")).toContainText(["Stacks in prod / shared: Scoped read failed"]);
  await expect(popover).toContainText("Last successful refresh:");
  await expect(content(page).getByText("Incomplete inventory")).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(popover).toBeHidden();
  await expect(page.locator(".summary-card").filter({ hasText: "Cells" })).toContainText("Incomplete or stale data");
  await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Connected");
});

test("empty inventory and failed retrieval have different messages", async ({ page }) => {
  await mock(page, () => ({ json: [] }));
  await page.goto(`${server.url}#/realms`);
  await expect(page.getByText("No realms found")).toBeVisible();
  await page.unroute("**/api/realms**");
  await page.route("**/api/realms**", route => route.fulfill(failure));
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(inventoryIndicator(page, "stale")).toBeVisible();
  await expect(page.getByText("Resources could not be fully loaded")).toBeVisible();
  await expect(page.getByText("No realms found")).toHaveCount(0);
});

test("old resource bookmarks work and replaced requests cannot overwrite another scope", async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await mock(page, async path => { if (path === "/api/realms/dev/spaces/shared") await gate; });
  await page.goto(`${server.url}#dev/shared`);
  await expect(page.getByText("Loading space details…")).toBeVisible();
  await page.goto(`${server.url}#/resource/prod/shared?document=1`);
  await expect(page.getByLabel("Complete space JSON")).toHaveText(JSON.stringify(doc(["prod", "shared"]), null, 2));
  release();
  await expect(page.getByLabel("Complete space JSON")).not.toContainText('"dev"');
});

test("Settings switches the appearance between system, light, and dark preferences", async ({ page }) => {
  await mock(page);
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(server.url);
  const scheme = () => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme);
  await expect.poll(scheme).toBe("dark");
  // The appearance control moved out of the top bar.
  await expect(page.locator(".toolbar").getByRole("button", { name: /Appearance/ })).toHaveCount(0);
  // Settings is a page, not a dialog (#1686).
  const settings = await openSettings(page);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.locator(".page-heading h1")).toHaveText("Settings");
  await expect(settings).toContainText("Stored in this browser only.");
  await expect(settings.getByRole("radio", { name: "System" })).toBeChecked();
  await settings.getByRole("radio", { name: "Light" }).check();
  await expect.poll(scheme).toBe("light");
  await page.reload();
  await expect.poll(scheme).toBe("light");
  await expect(settings.getByRole("radio", { name: "Light" })).toBeChecked();
  await settings.getByRole("radio", { name: "Dark" }).check();
  await expect.poll(scheme).toBe("dark");
  await page.emulateMedia({ colorScheme: "light" });
  await settings.getByRole("radio", { name: "System" }).check();
  await expect.poll(scheme).toBe("light");
  expect(await page.evaluate(() => localStorage.getItem("kukeon-theme"))).toBe("system");
});

test("without a session only the login screen renders and no inventory, screenshot or terminal request is made", async ({ page }) => {
  await mock(page);
  const requests = [];
  page.on("request", request => { const path = new URL(request.url()).pathname; if (path.startsWith("/api/")) requests.push(`${request.method()} ${path}`); });
  let signedIn = false;
  await page.route("**/api/session", route => route.fulfill(signedIn ? { json: { user: "root" } } : { status: 401, json: { error: { code: "unauthenticated", message: "sign in to use the kukeon web UI" } } }));
  await page.route("**/api/login", async route => {
    const body = route.request().postDataJSON();
    if (route.request().headers()["content-type"] !== "application/json") return route.fulfill({ status: 400, json: { error: { code: "invalid_request", message: "JSON request required" } } });
    if (body.username === "root" && body.password === "s3cret") { signedIn = true; return route.fulfill({ json: { user: "root" } }); }
    return route.fulfill({ status: 401, json: { error: { code: "invalid_credentials", message: "invalid user or password" } } });
  });
  await page.goto(`${server.url}#/realms`);
  const login = page.getByRole("form", { name: "Sign in" });
  await expect(login).toBeVisible();
  await expect(page.locator(".app-shell")).toHaveCount(0);
  await expect(login.getByLabel("User")).toHaveValue("root");
  await expect(login).toContainText("kuke get secret web-ui-admin --realm kuke-system -o yaml");
  await login.getByLabel("User").fill("admin");
  await login.getByLabel("Password").fill("wrong");
  await login.getByRole("button", { name: "Sign in" }).click();
  await expect(login.getByRole("alert")).toHaveText("invalid user or password");
  await expect(login.getByLabel("User")).toHaveValue("admin");
  expect(requests.every(request => request === "GET /api/session" || request === "POST /api/login"), requests.join("\n")).toBe(true);
  await login.getByLabel("User").fill("root");
  await login.getByLabel("Password").fill("s3cret");
  await login.getByRole("button", { name: "Sign in" }).click();
  // The page the URL hash names opens.
  await expect(page.locator(".page-heading h1")).toHaveText("Realms");
  await expect(page).toHaveURL(/#\/realms$/);
});

test("a 401 during a session returns to the login screen and closes the terminals", async ({ page }) => {
  let expired = false;
  await mock(page, path => expired ? { status: 401, json: { error: { code: "unauthenticated", message: "sign in to use the kukeon web UI" } } } : path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  const closed = [];
  await page.routeWebSocket("**/attach", socket => {
    socket.onClose(() => closed.push(new URL(socket.url()).pathname));
    socket.send(JSON.stringify({ type: "ready" }));
    socket.send(Buffer.from("output"));
  });
  await page.goto(server.url);
  await page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" }).click();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  expired = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("form", { name: "Sign in" })).toBeVisible();
  await expect(page.locator(".app-shell")).toHaveCount(0);
  await expect.poll(() => closed.length).toBe(1);
});

test("a refused terminal upgrade checks the session and signs out on 401", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  let signedIn = true;
  await page.route("**/api/session", route => route.fulfill(signedIn ? { json: { user: "root" } } : { status: 401, json: { error: { code: "unauthenticated", message: "sign in" } } }));
  await page.routeWebSocket("**/attach", socket => { signedIn = false; socket.close(); });
  await page.goto(server.url);
  await page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" }).click();
  await expect(page.getByRole("form", { name: "Sign in" })).toBeVisible();
});

test("the daemon-unavailable session check shows the login screen with a Retry", async ({ page }) => {
  let unavailable = true;
  await page.route("**/api/session", route => route.fulfill(unavailable ? { status: 503, json: { error: { code: "unavailable", message: "kukeond transport/connection failed" } } } : { json: { user: "root" } }));
  await mock(page);
  await page.goto(server.url);
  const login = page.getByRole("form", { name: "Sign in" });
  await expect(login.getByRole("alert")).toContainText("Daemon unavailable");
  unavailable = false;
  await login.getByRole("button", { name: "Retry" }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
});

test("the user menu names the user, opens Settings and signs out from the keyboard", async ({ page }) => {
  await mock(page);
  let loggedOut = 0;
  await page.route("**/api/logout", route => {
    if (route.request().method() === "POST" && route.request().headers()["content-type"] === "application/json") loggedOut += 1;
    return route.fulfill({ status: 204 });
  });
  await page.goto(server.url);
  const toolbar = page.locator(".toolbar-actions");
  const buttons = toolbar.getByRole("button");
  const names = await buttons.evaluateAll(list => list.map(button => button.getAttribute("aria-label") ?? button.textContent));
  expect(names.indexOf("User menu: root")).toBe(names.indexOf("Refresh") + 1);
  const button = userButton(page);
  await button.focus();
  await page.keyboard.press("Enter");
  const menu = page.getByRole("menu", { name: "User" });
  await expect(menu).toContainText("root");
  await expect(menu.getByRole("menuitem", { name: "Settings" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  await expect(button).toBeFocused();
  await page.keyboard.press("Space");
  await expect(menu).toBeVisible();
  // Settings is a link to the Settings page; Enter and Space both follow it and no dialog opens.
  const settingsItem = menu.getByRole("menuitem", { name: "Settings" });
  await expect(settingsItem).toHaveAttribute("href", "#/settings");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/#\/settings$/);
  await expect(menu).toHaveCount(0);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.locator(".page-heading h1")).toBeFocused();
  await page.goBack();
  await button.focus();
  await page.keyboard.press("Enter");
  await expect(settingsItem).toBeFocused();
  await page.keyboard.press("Space");
  await expect(page).toHaveURL(/#\/settings$/);
  await button.click();
  await menu.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page.getByRole("form", { name: "Sign in" })).toBeVisible();
  expect(loggedOut).toBe(1);
});

test("Settings sidebar switches hide the cell scope and terminal status and persist", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); socket.send(Buffer.from("output")); });
  await seed(page, { "kukeon-sidebar-show-search": "true" });
  await page.goto(server.url);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  const link = cells.getByRole("link", { name: "Open dev / shared / services / api" });
  await link.click();
  await expect(link).toContainText("Terminal attached");
  await expect(cells.locator(".cell-link-scope").first()).toBeVisible();
  const cell = page.url();
  const settings = await openSettings(page);
  const scopeSwitch = settings.getByRole("switch", { name: "Show cell scope in sidebar" });
  const statusSwitch = settings.getByRole("switch", { name: "Show terminal status in sidebar" });
  await expect(scopeSwitch).toHaveAttribute("aria-checked", "true");
  await expect(statusSwitch).toHaveAttribute("aria-checked", "true");
  await scopeSwitch.click();
  await statusSwitch.click();
  await expect(scopeSwitch).toHaveAttribute("aria-checked", "false");
  await page.goto(cell);
  await expect(link).toBeVisible();
  await expect(cells.locator(".cell-link-scope, .cell-group-scope")).toHaveCount(0);
  await expect(cells.locator(".cell-terminal-state")).toHaveCount(0);
  // The link keeps its full scope for the tooltip, the accessible name and search; badges stay.
  await expect(link).toHaveAttribute("title", "dev / shared / services / api");
  await expect(link.locator(".state-badge")).toHaveCount(1);
  await page.getByRole("searchbox", { name: "Search cells" }).fill("prod");
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toBeVisible();
  await expect(link).toHaveCount(0);
  await page.reload();
  await expect(cells.getByRole("link").first()).toBeVisible();
  await expect(cells.locator(".cell-link-scope, .cell-group-scope")).toHaveCount(0);
  const reopened = await openSettings(page);
  await expect(reopened.getByRole("switch", { name: "Show cell scope in sidebar" })).toHaveAttribute("aria-checked", "false");
  expect(await page.evaluate(() => [localStorage.getItem("kukeon-sidebar-show-scope"), localStorage.getItem("kukeon-sidebar-show-terminal-status")])).toEqual(["false", "false"]);
});

test("mobile cell navigation closes after selection and the Workbench fits the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  // A collapsed desktop preference must not leak into the mobile drawer.
  await page.addInitScript(() => localStorage.setItem("kukeon-sidebar-collapsed", "true"));
  await mock(page);
  await page.goto(server.url);
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  await expect(page.locator(".toolbar").getByRole("link", { name: "New Cell", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Expand sidebar" })).toHaveCount(0);
  await expect(page.getByRole("separator", { name: "Resize sidebar" })).toBeHidden();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" }).click();
  await expect(page.getByRole("complementary")).toBeHidden();
  await expect(page.locator(".page-heading h1")).toHaveText("api");
  await expect(page.getByRole("link", { name: "+ New Cell" })).toHaveCount(0);
  await expect(page.getByRole("status", { name: "Daemon: Connected" })).toBeVisible();
  // Phones hide the breadcrumbs, so the way home from a cell is the drawer's brand link.
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await page.getByRole("complementary", { name: "Cell sidebar" }).getByRole("link", { name: "kukeon", exact: true }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
});

// #1635: on phones the drawer shows the sidebar of the current view; Manage is one vertical list, so no
// page scrolls sideways and every Manage page is one tap away.
test("phone drawer shows the Manage navigation on Manage pages and reaches every page", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await mockDaemon(page);
  await page.goto(`${server.url}#/resources`);
  const fits = async () => expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await fits();
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  const drawer = page.getByRole("complementary", { name: "Manage sidebar" });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole("navigation", { name: "Cell navigation" })).toHaveCount(0);
  await expect(nav(page).getByRole("link")).toHaveCount(14);
  for (const link of await nav(page).getByRole("link").all()) await expect(link).toBeVisible();
  await nav(page).getByRole("link", { name: "Daemon" }).click();
  await expect(drawer).toBeHidden();
  await expect(page.locator(".page-heading h1")).toHaveText("Daemon");
  await fits();
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await expect(nav(page).getByRole("link", { name: "Daemon" })).toHaveAttribute("aria-current", "page");
  await nav(page).getByRole("link", { name: "Secrets" }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Secrets");
  await fits();
  // On a Workbench page the same drawer lists the cells.
  await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await expect(page.getByRole("complementary", { name: "Cell sidebar" }).getByRole("navigation", { name: "Cell navigation" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Manage navigation" })).toHaveCount(0);
});

test("cell status cards and container observations expose reported runtime state", async ({ page }) => {
  let fail = false;
  await mock(page, path => {
    if (path === "/api/realms/prod/spaces/shared/stacks/services/cells/api") {
      return fail ? failure : { json: { ...doc(["prod", "shared", "services", "api"]), status: { state: "Degraded", cgroupReady: true, reason: "WorkloadDown", message: "web exited unexpectedly", containers: [{ name: "web", state: "Failed" }] } } };
    }
  });
  await page.goto(`${server.url}#/resource/prod/shared/services/api`);
  const status = page.getByRole("region", { name: "Cell status" });
  await expect(status.getByText("Degraded", { exact: true })).toBeVisible();
  await expect(status.getByText("Ready", { exact: true })).toBeVisible();
  await expect(status.getByText("0 ready · 1 failed")).toBeVisible();
  await expect(status.getByText("web exited unexpectedly")).toBeVisible();
  await expect(page.locator(".container-panel").getByText("Failed", { exact: true })).toBeVisible();
  fail = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("Stale resource details — refresh failed.")).toBeVisible();
  await expect(page.getByText("Stale observations")).toBeVisible();
  await expect(status.getByText("Degraded", { exact: true })).toBeVisible();
});

// Issue #1768: the drift diff of a Config-made cell, the reported case. A blueprint content change alters
// containers[work].env; the masked env lines and the blueprint-revision annotation show as changed.
const driftManifest = (revision, mode) => [
  "apiVersion: v1beta1", "kind: Cell", "metadata:", "  name: api", "  labels:", "    kukeon.io/config: claude",
  "  annotations:", `    kukeon.io/render-blueprint-revision: claude-host@${revision}`, "    kukeon.io/render-digest: sha256:live",
  "spec:", "  realmId: prod", "  spaceId: shared", "  stackId: services", "  containers:",
  ...Array.from({ length: 4 }, (_, i) => [`    - id: side-${i}`, `      image: side:${i}`]).flat(),
  "    - id: work", "      image: claude:1", "      env:", `        - MODE=${mode}`, "        - KEEP=++++++++",
  ...Array.from({ length: 12 }, (_, i) => `      # filler ${i}`),
].join("\n") + "\n";
const driftBody = { live: driftManifest("aaa", "++++++++"), desired: driftManifest("bbb", "++++++++++++"), configFound: true, reasons: ["blueprint claude-host changed", "spec differs at containers[work].env"], paths: ["containers[work].env"], changeType: "compatible" };
const driftedCell = (status) => ({ json: { ...doc(["prod", "shared", "services", "api"]), metadata: { name: "api", labels: { "kukeon.io/config": "claude" } }, status: { state: "Ready", ...status } } });
const cellPath = "/api/realms/prod/spaces/shared/stacks/services/cells/api";

test("an out-of-sync cell shows a side-by-side manifest diff with masked env", async ({ page }) => {
  let driftReads = 0;
  await mock(page, path => {
    if (path === cellPath) return driftedCell({ outOfSync: true, outOfSyncReason: "blueprint claude-host changed; spec differs at containers[work].env" });
    if (path === `${cellPath}/drift`) { driftReads++; return { json: driftBody }; }
  });
  await page.goto(`${server.url}#/resource/prod/shared/services/api`);
  const status = page.getByRole("region", { name: "Cell status" });
  await expect(status.getByText("Configuration out of sync")).toBeVisible();
  const toggle = status.locator(".drift-diff summary");
  await expect(toggle).toHaveText(/Show diff/);
  // The diff loads only when opened.
  expect(driftReads).toBe(0);
  await toggle.click();
  const grid = status.getByLabel("Manifest diff");
  await expect(grid.locator(".drift-head")).toHaveText(["Running cell", "From Config"]);
  expect(driftReads).toBe(1);
  await expect(status.getByText("Restart will update in place")).toBeVisible();

  // Changed lines carry -/+ markers, not colour alone.
  const removed = grid.locator(".drift-left.drift-removed");
  const added = grid.locator(".drift-right.drift-added");
  await expect(removed).toHaveCount(2);
  await expect(added).toHaveCount(2);
  await expect(removed.locator(".drift-mark")).toHaveText(["-", "-"]);
  await expect(added.locator(".drift-mark")).toHaveText(["+", "+"]);
  await expect(removed.locator("code")).toHaveText(["    kukeon.io/render-blueprint-revision: claude-host@aaa", "        - MODE=++++++++"]);
  await expect(added.locator("code")).toHaveText(["    kukeon.io/render-blueprint-revision: claude-host@bbb", "        - MODE=++++++++++++"]);
  // Equal env values mask the same on both sides and are not marked.
  await expect(grid.locator(".drift-left:not(.drift-removed) code", { hasText: "KEEP=++++++++" })).toHaveCount(1);

  // Collapsed by default: unchanged runs fold, a fold opens on click, Full manifest shows everything.
  const folds = grid.locator(".drift-fold.drift-left");
  await expect(folds).toHaveText(["4 unchanged lines", "11 unchanged lines", "10 unchanged lines"]);
  await folds.nth(1).click();
  await expect(folds).toHaveText(["4 unchanged lines", "10 unchanged lines"]);
  await expect(grid.locator(".drift-left code", { hasText: "image: side:2" })).toHaveCount(1);
  const full = status.getByRole("switch", { name: "Full manifest" });
  await expect(full).toHaveAttribute("aria-checked", "false");
  await full.click();
  await expect(full).toHaveAttribute("aria-checked", "true");
  await expect(folds).toHaveCount(0);
  await expect(grid.locator(".drift-cell.drift-left")).toHaveCount(driftBody.live.split("\n").length - 1);
  // Back to the compact view, the fold opened by hand stays open.
  await full.click();
  await expect(folds).toHaveText(["4 unchanged lines", "10 unchanged lines"]);

  // At phone width the two columns stack, left above right, with no horizontal page scroll.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(grid.locator(".drift-head.drift-right")).toBeVisible();
  const layout = await page.evaluate(() => {
    const grid = document.querySelector(".drift-grid");
    const top = selector => grid.querySelector(selector).getBoundingClientRect().top;
    const lefts = [...grid.querySelectorAll(".drift-cell.drift-left")].map(el => el.getBoundingClientRect().bottom);
    return { overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth, rightHead: top(".drift-head.drift-right"), lastLeft: Math.max(...lefts), leftHead: top(".drift-head.drift-left") };
  });
  expect(layout.overflow).toBeLessThanOrEqual(0);
  expect(layout.rightHead).toBeGreaterThanOrEqual(layout.lastLeft - 1);
  expect(layout.leftHead).toBeLessThan(layout.rightHead);
});

test("the drift diff stays away from cells it cannot compare", async ({ page }) => {
  let cellStatus = { outOfSyncError: "resolve blueprint \"claude-host\": blueprint not found" };
  let driftReply = { json: driftBody };
  await mock(page, path => {
    if (path === cellPath) return driftedCell(cellStatus);
    if (path === `${cellPath}/drift`) return driftReply;
  });
  // A render failure shows the comparison error and no diff toggle.
  await page.goto(`${server.url}#/resource/prod/shared/services/api`);
  const status = page.getByRole("region", { name: "Cell status" });
  await expect(status.getByText("Configuration comparison failed")).toBeVisible();
  await expect(status.getByText('resolve blueprint "claude-host": blueprint not found')).toBeVisible();
  await expect(status.locator(".drift-diff")).toHaveCount(0);

  // A deleted Config shows its reason and no diff toggle.
  cellStatus = { outOfSync: true, outOfSyncReason: "lineage Config deleted" };
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(status.getByText("lineage Config deleted")).toBeVisible();
  await expect(status.locator(".drift-diff")).toHaveCount(0);

  // A cell the daemon finds is not from a Config, or whose Config went away since the last tick, says so.
  cellStatus = { outOfSync: true, outOfSyncReason: "config changed" };
  driftReply = { status: 409, json: { error: { code: "not_from_config", message: "cell \"api\": cell was not created from a config" } } };
  await page.getByRole("button", { name: "Refresh" }).click();
  await status.locator(".drift-diff summary").click();
  await expect(status.getByRole("alert").filter({ hasText: "cell was not created from a config" })).toBeVisible();
  await expect(status.getByLabel("Manifest diff")).toHaveCount(0);
  driftReply = { json: { live: driftBody.live, configFound: false, reasons: ["lineage Config deleted"], paths: [] } };
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(status.locator(".drift-body")).toHaveText("lineage Config deleted");
  await expect(status.getByLabel("Manifest diff")).toHaveCount(0);
});

test("container status shows restarts, terminal exit and setup failures without JSON", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers/web") ? { json: { ...doc(["prod", "shared", "services", "api", "web"]), status: { state: "Stopped", restartCount: 3, exitCode: 7, finishTime: "2026-01-02T03:04:05Z", repos: [{ name: "source", state: "failed", error: "Repository unavailable" }], stages: [{ index: 0, state: "done" }] } } } : undefined);
  await page.goto(`${server.url}#/resource/prod/shared/services/api/web`);
  const status = page.getByRole("region", { name: "Container status" });
  await expect(status.getByText("Stopped", { exact: true })).toBeVisible();
  await expect(status.locator(".status-card").filter({ hasText: "Restarts" })).toContainText("3");
  await expect(status.locator(".status-card").filter({ hasText: "Exit code" })).toContainText("7");
  await expect(status.locator("time")).toHaveAttribute("datetime", "2026-01-02T03:04:05Z");
  await expect(status.getByText("Repository unavailable")).toBeVisible();
  await expect(status.getByText("Stage 0", { exact: true })).toBeVisible();
  await expect(page.locator("pre")).toHaveCount(0);
});

test("running and unreported statuses never imply a successful exit or readiness", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers/web") ? { json: { ...doc(["prod", "shared", "services", "api", "web"]), status: { state: "Ready", restartCount: 0, exitCode: 0, finishTime: "0001-01-01T00:00:00Z" } } } : undefined);
  await page.goto(`${server.url}#/resource/prod/shared/services/api/web`);
  const status = page.getByRole("region", { name: "Container status" });
  await expect(status.getByText("Ready", { exact: true })).toBeVisible();
  await expect(status.getByText("Exit code", { exact: true })).toHaveCount(0);
  await expect(status.locator("time")).toHaveCount(0);
  await page.goto(`${server.url}#/resource/prod/shared/services/api`);
  await expect(page.getByRole("region", { name: "Cell status" }).getByText("Not reported", { exact: true })).toHaveCount(2);
  await expect(page.locator(".container-panel").getByText("Not reported", { exact: true })).toBeVisible();
});

function catalogDoc(kind, scope) {
  const metadata = { name: "shared-name", realm: scope[0], ...(scope[1] ? { space: scope[1] } : {}), ...(scope[2] ? { stack: scope[2] } : {}), ...(scope[3] ? { cell: scope[3] } : {}) };
  const spec = kind === "configs" ? { blueprint: { name: "template", realm: "library" }, values: { PORT: "8080" } }
    : kind === "blueprints" ? { prefix: "worker", cell: { containers: [{ id: "web", image: "nginx" }] } }
    : kind === "volumes" ? { reclaimPolicy: "Retain" } : {};
  return { apiVersion: "v1beta1", kind: { configs: "CellConfig", blueprints: "CellBlueprint", volumes: "Volume", secrets: "Secret" }[kind], metadata, spec };
}
async function mockCatalog(page, handler) {
  await mock(page);
  await page.route("**/api/catalog/**", async route => {
    const url = new URL(route.request().url());
    const parts = url.pathname.split("/").filter(Boolean);
    const kind = parts[2];
    const custom = await handler?.(url);
    if (custom) return route.fulfill(custom);
    if (!parts[3]) {
      const scopes = [["dev"], ["prod"], ["prod", "shared"], ["prod", "shared", "services"], ...(kind === "secrets" ? [["prod", "shared", "services", "api"]] : [])];
      return route.fulfill({ json: scopes.map(scope => catalogDoc(kind, scope)) });
    }
    const scope = ["realm", "space", "stack", "cell"].map(key => url.searchParams.get(key)).filter(Boolean);
    const document = catalogDoc(kind, scope);
    document.metadata.name = decodeURIComponent(parts[3]);
    await route.fulfill({ json: document });
  });
}
for (const kind of ["blueprints", "configs", "volumes", "secrets"]) {
  test(`${kind} browse all applicable scopes with complete documents on demand`, async ({ page }) => {
    await mockCatalog(page);
    await page.goto(`${server.url}#/${kind}`);
    const count = kind === "secrets" ? 5 : 4;
    await expect(page.getByRole("row")).toHaveCount(count + 1);
    await expect(page.locator("pre")).toHaveCount(0);
    await page.getByLabel("Filter by realm").selectOption("prod");
    await expect(page.getByRole("row")).toHaveCount(count);
    await listSearch(page).fill("missing");
    await expect(page.getByText("No matching resources")).toBeVisible();
    await listSearch(page).fill(kind === "secrets" ? "prod / shared / services / api" : "prod / shared / services");
    await expect(page.getByRole("row")).toHaveCount(2);
    await page.getByRole("link", { name: "shared-name", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Resource overview" })).toBeVisible();
    await expect(page.locator("pre")).toHaveCount(0);
    await page.getByRole("link", { name: "View document", exact: true }).click();
    const scope = kind === "secrets" ? ["prod", "shared", "services", "api"] : ["prod", "shared", "services"];
    await expect(page.getByLabel(`Complete ${kind} JSON`)).toHaveText(JSON.stringify(catalogDoc(kind, scope), null, 2));
    await page.reload();
    await expect(page.getByLabel(`Complete ${kind} JSON`)).toBeVisible();
    if (kind === "secrets") await expect(page.getByText(/Secret values are redacted/)).toBeVisible();
  });
}

test("config references navigate across realms and missing targets report errors", async ({ page }) => {
  let missing = true;
  await mockCatalog(page, url => url.pathname.endsWith("/blueprints/template") && missing ? { status: 404, json: { error: { code: "not_found", message: "Blueprint is missing" } } } : undefined);
  await page.goto(`${server.url}#/configs/prod/shared/services/shared-name`);
  await expect(page.getByRole("region", { name: "Resource references" }).getByRole("alert")).toContainText("Missing reference");
  await page.getByRole("link", { name: "template", exact: true }).click();
  await expect(page).toHaveURL(/#\/blueprints\/library\/template$/);
  await expect(page.locator(".page-heading h1")).toHaveText("template");
  await expect(content(page).getByRole("alert")).toContainText("Blueprint is missing");
  missing = false;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("heading", { name: "Resource overview" })).toBeVisible();
  await page.getByRole("link", { name: "View document", exact: true }).click();
  await expect(page.getByLabel("Complete blueprints JSON")).toContainText('"realm": "library"');
});

test("config lineage lists Cells with an Open terminal action and no Workspace copy", async ({ page }) => {
  await mockCatalog(page);
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells", route => {
    const cell = doc(["prod", "shared", "services", "api"]);
    cell.spec.provenance = { bindingKind: "config", bindingRef: { name: "shared-name", realm: "prod", space: "shared", stack: "services" } };
    return route.fulfill({ json: [cell] });
  });
  await page.goto(`${server.url}#/configs/prod/shared/services/shared-name`);
  const lineage = page.locator(".related-resources").filter({ has: page.getByRole("heading", { name: "Cells using this Config" }) });
  await expect(lineage.getByRole("link", { name: "Open terminal", exact: true })).toHaveAttribute("href", "#/workspace/prod/shared/services/api");
  expect(await page.evaluate(() => document.body.innerText)).not.toMatch(/Kukeon|Workspace/);
});

test("explicit secret and volume references show usage and errors without ownership assumptions", async ({ page }) => {
  await mockCatalog(page, url => {
    if (url.pathname.endsWith("/blueprints/shared-name")) {
      const document = catalogDoc("blueprints", ["prod"]);
      document.spec.cell.containers[0].secrets = [{ secretRef: { name: "credential", realm: "library", space: "secure", stack: "data", cell: "source" } }];
      document.spec.cell.containers[0].volumes = [{ kind: "volume", volumeRef: { name: "cache", realm: "store" }, target: "/cache" }];
      return { json: document };
    }
    if (url.pathname.endsWith("/volumes/cache")) return { status: 503, json: { error: { code: "unavailable", message: "Volume lookup failed" } } };
  });
  await page.goto(`${server.url}#/blueprints/prod/shared-name`);
  const refs = page.getByRole("region", { name: "Resource references" });
  await expect(refs.getByText(/not ownership/)).toBeVisible();
  await expect(refs.getByRole("alert")).toContainText("Reference retrieval failed");
  await expect(refs.getByRole("link", { name: "credential" })).toHaveAttribute("href", "#/secrets/library/secure/data/source/credential");
  await expect(refs.getByRole("link", { name: "cache" })).toHaveAttribute("href", "#/volumes/store/cache");
});

test("catalog refresh retains stale list and document with visible failure and recovers", async ({ page }) => {
  let fail = false;
  await mockCatalog(page, () => fail ? failure : undefined);
  await page.goto(`${server.url}#/secrets`);
  await expect(page.getByRole("row")).toHaveCount(6);
  fail = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("Stale secrets list — refresh failed.")).toBeVisible();
  await expect(page.getByRole("row")).toHaveCount(6);
  fail = false;
  await page.goto(`${server.url}#/secrets/prod/shared-name?document=1`);
  await expect(page.getByLabel("Complete secrets JSON")).toBeVisible();
  fail = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("Stale secrets document — refresh failed.")).toBeVisible();
  fail = false;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.locator(".stale")).toHaveCount(0);
});

test("catalog empty results are distinct from retrieval failures", async ({ page }) => {
  let fail = false;
  await mockCatalog(page, () => fail ? failure : { json: [] });
  await page.goto(`${server.url}#/volumes`);
  await expect(page.getByText("No volumes found")).toBeVisible();
  fail = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("Resources could not be loaded")).toBeVisible();
  await expect(page.getByText("No volumes found")).toHaveCount(0);
});

// New Cell's optional sections are collapsed disclosures (#1603); a test opens one by its heading.
const newCellSection = (page, title) => content(page).locator(".disclosure").filter({ has: page.locator("summary", { has: page.getByRole("heading", { name: title, exact: true }) }) });
async function openNewCellSection(page, title) {
  const section = newCellSection(page, title);
  if (await section.locator("details").getAttribute("open") === null) await section.locator("summary").click();
  await expect(section.locator("details")).toHaveAttribute("open");
}
const previewPanel = page => page.getByRole("region", { name: "Preview", exact: true });

test("create from config previews, prevents duplicate submits and opens the scoped cell", async ({ page }) => {
  await mockCatalog(page);
  let creates = 0;
  let started = 0;
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells", route => route.fulfill({ json: [...fixture("/api/realms/prod/spaces/shared/stacks/services/cells"), ...(creates ? [doc(["prod", "shared", "services", "new-cell"])] : [])] }));
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells/new-cell/start", route => { started++; return route.fulfill({ json: {} }); });
  await page.route("**/api/cells/from-config/*", async route => {
    const input = route.request().postDataJSON();
    const document = { metadata: { name: input.name }, spec: { realmId: "prod", spaceId: "shared", stackId: "services" } };
    if (route.request().url().endsWith("create")) {
      creates++;
      expect(input.preview).toBe("reviewed");
      await route.fulfill({ json: document });
    } else await route.fulfill({ json: { document, preview: "reviewed" } });
  });
  await page.goto(`${server.url}#/configs/prod/shared/services/shared-name`);
  await page.getByRole("link", { name: /Create Cell from this Config/ }).click();
  await expect(page).toHaveURL(/#\/new-cell\?config=/);
  await page.getByLabel("Cell name (optional)").fill("new-cell");
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await expect(previewPanel(page)).toBeVisible();
  expect(creates).toBe(0);
  // An input change drops the preview; Create stays on screen, since it needs no preview (#1603).
  await openNewCellSection(page, "Environment overrides");
  await page.getByLabel("Environment overrides").fill("APP=production");
  await expect(previewPanel(page)).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Create Cell", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await expect(previewPanel(page)).toBeVisible();
  await page.getByRole("button", { name: "Create Cell", exact: true }).click();
  // The mock cell has no attachable container, so the default start-and-attach lands on cellRead.
  await expect(page).toHaveURL(/#\/resource\/prod\/shared\/services\/new-cell$/);
  expect(creates).toBe(1);
  await expect.poll(() => started).toBe(1);
  await expect(toasts(page)).toContainText("No attachable workload was defined.");
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open prod / shared / services / new-cell" })).toBeVisible();
});

// Issue #1568, item D: the preview is a short summary, the JSON is one click away and Create Cell is the
// one primary action. #1603: Create needs no preview, the actions sit bottom right and the preview is its own panel.
test("New Cell from a Config summarizes the preview, keeps the JSON collapsed and wrapped, and makes Create the only primary action", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockCatalog(page);
  const requests = [];
  const cellDocument = name => ({ apiVersion: "v1beta1", kind: "Cell", metadata: { name, labels: { note: "x".repeat(400) } }, spec: { realmId: "prod", spaceId: "shared", stackId: "services", containers: [
    { id: "work", image: "ghcr.io/example/app:v1", attachable: true, env: Array.from({ length: 80 }, (_, i) => `VAR_${i}=<redacted>`) }, { id: "sidecar", image: "ghcr.io/example/sidecar:v2" }] } });
  await page.route("**/api/cells/from-config/*", async route => {
    const input = route.request().postDataJSON();
    const create = route.request().url().endsWith("create");
    requests.push({ step: create ? "create" : "preview", input });
    await route.fulfill({ json: create ? cellDocument(input.resolvedName) : { document: cellDocument(input.name || "worker-7f3a"), preview: "reviewed" } });
  });
  let started = 0;
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells/worker-7f3a/**", route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/start")) { started++; return route.fulfill({ json: {} }); }
    if (path.endsWith("/containers")) return route.fulfill({ json: [{ id: "work", attachable: true }] });
    return route.fallback();
  });
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  await page.goto(`${server.url}#/new-cell?config=${encodeURIComponent(JSON.stringify(["prod", "shared", "services", "shared-name"]))}`);
  // The mode switch is a segmented control that exposes its selected state.
  const modes = page.getByRole("group", { name: "Cell creation method" });
  await expect(modes.getByRole("button", { name: "From Config" })).toHaveAttribute("aria-pressed", "true");
  await expect(modes.getByRole("button", { name: "Manual" })).toHaveAttribute("aria-pressed", "false");
  const primary = content(page).locator("button.primary-action:visible");
  await expect(page.getByRole("button", { name: "Preview Cell", exact: true })).toBeEnabled();
  // Create Cell is the only primary-styled button, before and after a preview (#1603).
  await expect(primary).toHaveText(["Create Cell"]);
  await expect(page.getByRole("button", { name: "Preview Cell", exact: true })).not.toHaveClass(/primary-action/);
  // The form above the preview does not move or change when the preview appears.
  const formBox = () => page.getByRole("region", { name: "Cell", exact: true }).evaluate(el => { const box = el.getBoundingClientRect(); return { top: box.top + scrollY, height: box.height, text: el.textContent }; });
  const formBefore = await formBox();
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();

  const review = previewPanel(page);
  await expect(review.getByRole("heading", { name: "Preview", exact: true })).toBeVisible();
  await expect(review.locator(".badge", { hasText: "Not created yet" })).toBeVisible();
  await expect(review.locator(".creation-summary dt")).toHaveText(["Name", "Scope", "Containers", "Images", "Connectors"]);
  await expect(review.locator(".creation-summary dd")).toHaveText(["worker-7f3a", "prod / shared / services", "2 (work, sidecar)", "ghcr.io/example/app:v1ghcr.io/example/sidecar:v2", "None"]);
  // The summary is on screen without scrolling by hand, and the page is no longer thousands of pixels of JSON.
  await expect(review.locator(".creation-summary")).toBeInViewport({ ratio: 1 });
  expect(await formBox()).toEqual(formBefore);
  const collapsedHeight = await page.evaluate(() => document.scrollingElement.scrollHeight);
  await expect(primary).toHaveText(["Create Cell"]);
  const refresh = page.getByRole("button", { name: "Refresh preview", exact: true });
  await expect(refresh).not.toHaveClass(/primary-action/);
  expect((await refresh.boundingBox()).width).toBeLessThan(200);
  // One action row at the bottom right, below the preview: the options, then Refresh preview, then Create.
  const actions = content(page).locator(".form-actions");
  const create = actions.getByRole("button", { name: "Create Cell", exact: true });
  const [createBox, refreshBox, actionsBox, reviewBox] = [await create.boundingBox(), await refresh.boundingBox(), await actions.boundingBox(), await review.boundingBox()];
  expect(Math.abs((createBox.x + createBox.width) - (actionsBox.x + actionsBox.width))).toBeLessThan(2);
  expect(Math.abs((createBox.y + createBox.height / 2) - (refreshBox.y + refreshBox.height / 2))).toBeLessThan(12);
  expect(refreshBox.x + refreshBox.width).toBeLessThan(createBox.x);
  expect(actionsBox.y).toBeGreaterThan(reviewBox.y + reviewBox.height - 1);
  await expect(actions.getByLabel("Start the cell")).toBeChecked();
  await expect(actions.getByLabel("Attach a terminal")).toBeChecked();

  // The full JSON is a collapsed disclosure that wraps instead of widening the page.
  const json = review.locator("details.creation-json");
  const pre = page.getByLabel("Complete Cell preview");
  await expect(json).not.toHaveAttribute("open");
  await expect(pre).toBeHidden();
  await json.locator("summary").focus();
  await page.keyboard.press("Enter");
  await expect(pre).toContainText('"id": "sidecar"');
  expect(await pre.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  expect(await page.evaluate(() => document.scrollingElement.scrollHeight)).toBeGreaterThan(collapsedHeight);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await pre.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1440, height: 900 });

  // Hide preview removes the panel and nothing else.
  await review.getByRole("button", { name: "Hide preview" }).click();
  await expect(review).toHaveCount(0);
  expect(await formBox()).toEqual(formBefore);
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await expect(review).toBeVisible();

  // Create with the defaults: the reviewed name and preview token go back unchanged, the cell starts and attaches.
  await create.click();
  await expect(page).toHaveURL(/#\/workspace\/prod\/shared\/services\/worker-7f3a$/);
  await expect.poll(() => started).toBe(1);
  expect(requests.map(request => request.step)).toEqual(["preview", "preview", "create"]);
  expect(requests[2].input).toEqual({ config: { name: "shared-name", realm: "prod", space: "shared", stack: "services" }, name: "", resolvedName: "worker-7f3a", preview: "reviewed", env: [], ignoreDiskPressure: false, targetSpace: "shared", targetStack: "services" });
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
});

test("manual New Cell keeps validation inline, summarizes the preview and sends the previewed specification unchanged", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mock(page);
  const requests = [];
  await page.route("**/api/cells/manual/*", async route => {
    const input = route.request().postDataJSON();
    const create = route.request().url().endsWith("create");
    requests.push({ step: create ? "create" : "preview", input });
    const document = { apiVersion: "v1beta1", kind: "Cell", metadata: { name: input.name, annotations: { note: "y".repeat(600) } }, spec: { realmId: input.realm, spaceId: input.space, stackId: input.stack, containers: input.spec.containers } };
    await route.fulfill({ json: create ? document : { document, preview: "manual-reviewed" } });
  });
  await page.goto(`${server.url}#/new-cell`);
  const modes = page.getByRole("group", { name: "Cell creation method" });
  await modes.getByRole("button", { name: "Manual" }).click();
  await expect(modes.getByRole("button", { name: "Manual" })).toHaveAttribute("aria-pressed", "true");
  await expect(modes.getByRole("button", { name: "From Config" })).toHaveAttribute("aria-pressed", "false");
  const primary = content(page).locator("button.primary-action:visible");
  const validate = page.getByRole("button", { name: "Validate and preview Cell", exact: true });
  // Create Cell is the one primary action in both methods (#1603); Validate and preview is secondary.
  await expect(primary).toHaveText(["Create Cell"]);
  await expect(validate).not.toHaveClass(/primary-action/);
  // Validation errors stay inline, beside the field, and nothing is sent.
  await validate.click();
  await expect(page.locator("#manual-name-error")).toHaveText("This field is required.");
  await expect(page.getByText("Fix the highlighted fields before previewing or creating.")).toBeVisible();
  // A create without a preview runs the same validation (#1603).
  await page.locator("#manual-name").fill("x");
  await page.locator("#manual-name").fill("");
  await expect(page.locator("#manual-name-error")).toHaveCount(0);
  await primary.click();
  await expect(page.locator("#manual-name-error")).toHaveText("This field is required.");
  expect(requests).toEqual([]);
  await page.locator("#manual-name").fill("manual-cell");
  await page.getByRole("button", { name: "Add container" }).click();
  await page.getByLabel(/^Container ID/).fill("app");
  await page.getByLabel(/^OCI image/).fill("ghcr.io/example/app:latest");
  await page.getByLabel("Attachable terminal").check();
  await validate.click();

  const review = previewPanel(page);
  await expect(review.locator(".creation-summary dd")).toHaveText(["manual-cell", "dev / shared / services", "1 (app)", "ghcr.io/example/app:latest", "None"]);
  await expect(review.locator(".creation-summary")).toBeInViewport({ ratio: 1 });
  await expect(primary).toHaveText(["Create Cell"]);
  await expect(page.getByRole("button", { name: "Refresh preview", exact: true })).not.toHaveClass(/primary-action/);
  const pre = page.getByLabel("Complete manual Cell preview");
  await expect(pre).toBeHidden();
  await review.locator("details.creation-json summary").click();
  await expect(pre).toContainText('"image": "ghcr.io/example/app:latest"');
  // The raw preview used to be thousands of pixels wide; it now wraps, also on a phone.
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await pre.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1440, height: 900 });

  // The same action row as From Config, at the bottom right, starting and attaching by default.
  const actions = content(page).locator(".form-actions");
  await expect(actions.getByLabel("Start the cell")).toBeChecked();
  await expect(actions.getByLabel("Attach a terminal")).toBeChecked();
  await actions.getByRole("button", { name: "Create Cell", exact: true }).click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/manual-cell$/);
  expect(requests.map(request => request.step)).toEqual(["preview", "create"]);
  expect(requests[1].input).toEqual({ ...requests[0].input, preview: "manual-reviewed" });
  expect(requests[0].input).toMatchObject({ name: "manual-cell", realm: "dev", space: "shared", stack: "services", labels: {}, annotations: {}, spec: { containers: [{ id: "app", image: "ghcr.io/example/app:latest", attachable: true }], autoDelete: false, nestedCgroupRuntime: false, ignoreDiskPressure: false } });
});

// A shared blueprint as `kuke team` renders it: the form below is derived from
// this document alone.
const formBlueprint = {
  apiVersion: "v1beta1", kind: "CellBlueprint", metadata: { name: "claude", realm: "prod" },
  spec: {
    requires: ["git"],
    parameters: [
      { name: "IMAGE", type: "image", default: "ghcr.io/example/claude:v1", group: "image" },
      { name: "ROLE_PATH", type: "path", required: true, group: "role", description: "Role directory." },
    ],
    components: [
      { kind: "harness", name: "claude", requires: ["claude"], parameters: [{ name: "ANTHROPIC_MODEL", type: "enum", enum: ["opus", "sonnet"], default: "opus" }], modes: { interactive: {}, headless: { parameters: [{ name: "PROMPT", required: true }], secrets: [{ name: "anthropic-api-key", envName: "ANTHROPIC_API_KEY" }] } } },
      { kind: "connector", name: "github", optional: true, requires: ["gh"], description: "GitHub API access.", secrets: [{ name: "github-token", envName: "GH_TOKEN", required: true }] },
    ],
    cell: { containers: [{ id: "work", image: "${IMAGE}", repos: [{ name: "role-source", target: "/home/user/agents", required: true }, { name: "project", target: "/home/user/project" }] }] },
  },
};
// New Config sections after Config are disclosures (#1568); a test opens one by its heading unless it is open.
const configSection = (page, title) => page.locator(".schema-disclosure").filter({ has: page.locator("summary h3", { hasText: new RegExp(`^${title}$`) }) });
async function openSection(page, title) {
  const section = configSection(page, title);
  if (await section.locator("details").getAttribute("open") === null) await section.locator("summary").click();
  await expect(section.locator("details")).toHaveAttribute("open");
}
test("a config is created from the form a blueprint describes, and the preview shows each repo's git identity", async ({ page }) => {
  await mock(page, path => path === "/api/realms/prod/image-inventory" ? { json: { realm: "prod", namespace: "prod.kukeon.io", images: [
    { name: "ghcr.io/example/claude-gh:v1", capabilities: ["claude", "git", "gh"], labels: {} },
    { name: "ghcr.io/example/claude:v1", capabilities: ["claude", "git"], labels: {} },
    { name: "ghcr.io/example/codex:v1", capabilities: ["codex", "git", "gh"], labels: {} },
    { name: "docker.io/library/alpine:3", capabilities: [], labels: {} },
  ] } } : path === "/api/realms" ? { json: [doc(["prod"])] } : undefined);
  await page.route("**/api/catalog/**", async route => {
    const url = new URL(route.request().url());
    const [, , kind, name] = url.pathname.split("/").filter(Boolean);
    if (kind === "blueprints") return route.fulfill({ json: name ? formBlueprint : [{ kind: "CellBlueprint", metadata: formBlueprint.metadata }] });
    if (kind === "secrets") return route.fulfill({ json: [
      { kind: "Secret", metadata: { name: "gh-token", realm: "prod" }, spec: {} },
      { kind: "Secret", metadata: { name: "git-ssh", realm: "prod" }, spec: { type: "ssh-key" } },
      { kind: "Secret", metadata: { name: "git-https", realm: "prod" }, spec: { type: "basic-auth" } },
    ] });
    return route.fulfill({ json: name ? { kind: "CellConfig", metadata: { name, realm: "prod", space: "shared", stack: "services" }, spec: { blueprint: { name: "claude", realm: "prod" } } } : [] });
  });
  const requests = [];
  await page.route("**/api/configs/from-blueprint/*", async route => {
    const input = route.request().postDataJSON();
    requests.push({ action: route.request().url().split("/").at(-1), input });
    const config = { apiVersion: "v1beta1", kind: "CellConfig", metadata: input.config, spec: input.spec };
    if (route.request().url().endsWith("create")) return route.fulfill({ json: config });
    await route.fulfill({ json: { config, preview: "reviewed", digest: "sha256:render", document: { metadata: { name: "dev-abc123" }, spec: { realmId: "prod" } }, values: [
      { kind: "value", key: "ROLE_PATH", value: "", origin: "config" },
      { kind: "git", key: "project.author", value: "bot <bot@example.com>", origin: "config" },
      { kind: "git", key: "project.credential", value: "git-ssh (ssh-key)", origin: "config" },
      { kind: "git", key: "project.url", value: "git@github.com:o/project.git", origin: "config" },
      { kind: "git", key: "role-source.author", value: "dev <dev@example.com>", origin: "stack" },
      { kind: "git", key: "role-source.credential", value: "git-ssh (ssh-key)", origin: "realm" },
      { kind: "git", key: "role-source.url", value: "git@github.com:o/agents.git", origin: "config" },
    ] } });
  });
  await page.goto(`${server.url}#/blueprints/prod/claude`);
  await page.getByRole("link", { name: /Create Config from this Blueprint/ }).click();
  await expect(page).toHaveURL(/#\/new-config\?blueprint=/);
  await expect(page.getByRole("heading", { name: "Answer the claude form" })).toBeVisible();

  // Typed inputs come from the blueprint: an enum select, a required path.
  await expect(page.getByLabel("ANTHROPIC_MODEL")).toHaveValue("");
  await openSection(page, "claude");
  await page.getByLabel("ANTHROPIC_MODEL").selectOption("sonnet");
  await page.getByLabel("Config name").fill("dev");
  // The config's scope comes from the inventory; open coordinates stay realm-scoped.
  await expect(page.getByLabel("Config realm")).toHaveValue("prod");
  await page.getByLabel("Config space").selectOption("shared");
  await page.getByLabel("Config stack").selectOption("services");
  await page.getByLabel("ROLE_PATH").fill("dev");
  await expect(page.getByRole("note")).toContainText("role-source");

  // An optional component's slot appears only once enabled, and narrows the image picker.
  const picker = page.getByLabel("IMAGE from realm images");
  await expect(picker.getByRole("option")).toHaveText(["Choose from the realm's images…", "ghcr.io/example/claude-gh:v1", "ghcr.io/example/claude:v1", "docker.io/library/alpine:3 — capabilities unknown"]);
  await expect(page.getByLabel("github-token secret")).toHaveCount(0);
  await openSection(page, "Components");
  await page.getByLabel(/^github connector/).check();
  await expect(picker.getByRole("option")).toHaveText(["Choose from the realm's images…", "ghcr.io/example/claude-gh:v1", "docker.io/library/alpine:3 — capabilities unknown"]);
  await expect(page.getByText("This image does not provide: gh.")).toBeVisible();
  await picker.selectOption("ghcr.io/example/claude-gh:v1");
  await expect(page.getByLabel("IMAGE", { exact: true })).toHaveValue("ghcr.io/example/claude-gh:v1");
  await page.getByLabel("github-token secret").selectOption({ label: "gh-token — prod (opaque)" });

  // A mode's own parameters show only when previewing that mode.
  await expect(page.getByLabel("PROMPT")).toHaveCount(0);
  await openSection(page, "Modes");
  await page.getByLabel("Preview as mode").selectOption("headless");
  await expect(page.getByLabel("PROMPT")).toBeVisible();
  await page.getByLabel("Preview as mode").selectOption("interactive");

  // Repo slots, one with its own identity limited to git credential secrets.
  await page.getByLabel("role-source URL").fill("git@github.com:o/agents.git");
  await page.getByLabel("role-source branch").fill("main");
  await page.getByLabel("project URL").fill("git@github.com:o/project.git");
  await page.getByLabel("project uses its own git identity").check();
  await page.getByLabel("Author name").fill("bot");
  await page.getByLabel("Author email").fill("bot@example.com");
  await expect(page.getByLabel("project credential secret").getByRole("option")).toHaveText(["None", "git-https — prod (basic-auth)", "git-ssh — prod (ssh-key)", "Name a secret…"]);
  await page.getByLabel("project credential secret").selectOption({ label: "git-ssh — prod (ssh-key)" });
  await expect(page.getByLabel("Signing key secret").getByRole("option")).toHaveText(["None", "git-ssh — prod (ssh-key)", "Name a secret…"]);

  await page.getByRole("button", { name: "Preview Config", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Review Config" })).toBeVisible();
  const project = page.getByRole("table", { name: "Git identity for project" });
  await expect(project.getByRole("row").nth(1)).toHaveText(/author.*bot <bot@example.com>.*config/);
  await expect(project.getByRole("row").nth(2)).toHaveText(/credential.*git-ssh \(ssh-key\).*config/);
  await expect(page.getByRole("table", { name: "Git identity for role-source" }).getByRole("row").nth(2)).toHaveText(/credential.*git-ssh \(ssh-key\).*realm/);
  await expect(page.getByRole("table", { name: "Merged values and their origin" }).getByRole("row")).toHaveCount(2);
  expect(requests).toHaveLength(1);
  expect(requests[0].input).toEqual({
    config: { name: "dev", realm: "prod", space: "shared", stack: "services" }, mode: "interactive",
    spec: {
      blueprint: { name: "claude", realm: "prod" },
      values: { IMAGE: "ghcr.io/example/claude-gh:v1", ROLE_PATH: "dev", ANTHROPIC_MODEL: "sonnet" },
      repos: {
        "role-source": { url: "git@github.com:o/agents.git", branch: "main" },
        project: { url: "git@github.com:o/project.git", git: { author: { name: "bot", email: "bot@example.com" }, credential: { secretRef: { name: "git-ssh", realm: "prod" } } } },
      },
      secrets: { "github-token": { secretRef: { name: "gh-token", realm: "prod" } } },
      components: ["github"],
    },
  });

  // Changing an answer invalidates the review; the create sends the token.
  await page.getByLabel("ROLE_PATH").fill("pm");
  await expect(page.getByRole("button", { name: "Create Config", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Preview Config", exact: true }).click();
  await page.getByRole("button", { name: "Create Config", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Config created" })).toBeVisible();
  expect(requests.at(-1).action).toBe("create");
  expect(requests.at(-1).input.preview).toBe("reviewed");
  expect(requests.at(-1).input.spec.values.ROLE_PATH).toBe("pm");
  await expect(page.getByRole("link", { name: "Open Config dev" })).toHaveAttribute("href", "#/configs/prod/shared/services/dev");
  await page.getByRole("link", { name: /Create a Cell from it/ }).click();
  await expect(page).toHaveURL(/#\/new-cell\?config=/);
});

test("the config preview of an existing config shows each repo's git identity", async ({ page }) => {
  await mockCatalog(page);
  await page.route("**/api/cells/from-config/*", route => route.fulfill({ json: { document: { metadata: { name: "new-cell" }, spec: { realmId: "prod", spaceId: "shared", stackId: "services" } }, preview: "reviewed", values: [
    { kind: "repo", key: "project", value: "git@github.com:o/project.git", origin: "config" },
    { kind: "git", key: "project.signing", value: "git-ssh (ssh-key)", origin: "space" },
    { kind: "git", key: "project.url", value: "git@github.com:o/project.git", origin: "config" },
  ] } }));
  await page.goto(`${server.url}#/configs/prod/shared/services/shared-name`);
  await page.getByRole("link", { name: /Create Cell from this Config/ }).click();
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  // Each value is listed under the source that set it (#1603), git values included.
  const sources = previewPanel(page).getByRole("group", { name: "Preview sources" });
  await expect(sources.locator(":scope > [role=group]")).toHaveCount(2);
  await expect(sources.getByRole("table", { name: "From the Config" }).getByRole("row")).toHaveCount(3);
  await expect(sources.getByRole("table", { name: "Space defaults" }).getByRole("row").nth(1)).toHaveText(/git.*project\.signing.*git-ssh \(ssh-key\)/);
  // The per-repository identity table sits in a collapsed disclosure.
  const identity = page.getByRole("table", { name: "Git identity for project" });
  await expect(identity).toBeHidden();
  await openNewCellSection(page, "Git identity per repository");
  await expect(identity.getByRole("row").nth(1)).toHaveText(/signing.*git-ssh \(ssh-key\).*space/);
});

// kuke team writes realm-only blueprint refs from stack-scoped configs; the
// mock 404s any lookup whose scope slots do not match exactly, so a name that
// leaks into the space or stack slot fails the test.
// Issue #1568, item E: the New Config form leads with the essentials and discloses the rest on demand.
async function mockConfigForm(page) {
  await mock(page, path => path === "/api/realms" ? { json: [doc(["prod"])] } : path === "/api/realms/prod/image-inventory" ? { json: { realm: "prod", namespace: "prod.kukeon.io", images: [
    { name: "ghcr.io/example/claude-gh:v1", capabilities: ["claude", "git", "gh"], labels: {} }, { name: "ghcr.io/example/claude:v1", capabilities: ["claude", "git"], labels: {} }] } } : undefined);
  await page.route("**/api/catalog/**", async route => {
    const [, , kind, name] = new URL(route.request().url()).pathname.split("/").filter(Boolean);
    if (kind === "blueprints") return route.fulfill({ json: name ? formBlueprint : [{ kind: "CellBlueprint", metadata: formBlueprint.metadata }] });
    return route.fulfill({ json: kind === "secrets" ? [{ kind: "Secret", metadata: { name: "gh-token", realm: "prod" }, spec: {} }] : [] });
  });
  const requests = [];
  await page.route("**/api/configs/from-blueprint/*", async route => {
    const input = route.request().postDataJSON();
    requests.push({ action: route.request().url().split("/").at(-1), input });
    const config = { apiVersion: "v1beta1", kind: "CellConfig", metadata: input.config, spec: input.spec };
    await route.fulfill({ json: route.request().url().endsWith("create") ? config : { config, preview: "reviewed", document: { metadata: { name: "dev-abc123" }, spec: { realmId: "prod" } }, values: [] } });
  });
  await page.goto(`${server.url}#/new-config?blueprint=${encodeURIComponent(JSON.stringify(["prod", "claude"]))}`);
  await expect(page.getByRole("heading", { name: "Answer the claude form" })).toBeVisible();
  return requests;
}

test("New Config leads with the essential fields and keeps optional sections collapsed with a count of set answers", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockConfigForm(page);
  const headings = page.locator(".schema-disclosure > details > summary h3");
  // Sections holding a required answer come first and open by themselves; the rest are collapsed rows.
  await expect(headings).toHaveText(["Role", "Repositories", "Components", "Modes", "Image", "claude", "Image capabilities", "Default git identity"]);
  await expect(page.locator(".schema-disclosure > details[open] > summary h3")).toHaveText(["Role", "Repositories"]);
  await expect(configSection(page, "Role").locator(".disclosure-summary")).toHaveText("Defaults · 1 required");
  await expect(configSection(page, "Repositories").locator(".disclosure-summary")).toHaveText("Defaults · 1 required");
  for (const title of ["Components", "Modes", "Image", "claude", "Image capabilities", "Default git identity"]) await expect(configSection(page, title).locator(".disclosure-summary")).toHaveText("Defaults");
  // Above the fold at 1440x900 are the Config essentials only; the required answers follow, open, and no
  // optional field shows anywhere until its section is opened.
  for (const essential of [page.getByLabel("Config name"), page.getByLabel("Config realm"), page.getByLabel("Config space"), page.getByLabel("Config stack")]) await expect(essential).toBeInViewport({ ratio: 1 });
  for (const required of [page.getByLabel("ROLE_PATH"), page.getByLabel("role-source URL")]) await expect(required).toBeVisible();
  for (const optional of [page.getByLabel("IMAGE", { exact: true }), page.getByLabel("ANTHROPIC_MODEL"), page.getByLabel("Additional required capabilities"), page.getByLabel("Preview as mode"), page.getByLabel(/^github connector/), page.getByLabel("Override the inherited default identity")]) await expect(optional).toBeHidden();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);

  // A section opens from the keyboard, and its row counts the answers set inside it.
  await configSection(page, "claude").locator("summary").focus();
  await page.keyboard.press("Enter");
  await page.getByLabel("ANTHROPIC_MODEL").selectOption("sonnet");
  await expect(configSection(page, "claude").locator(".disclosure-summary")).toHaveText("1 set");
  await page.getByLabel("ROLE_PATH").fill("dev");
  await expect(configSection(page, "Role").locator(".disclosure-summary")).toHaveText("1 set");
  await openSection(page, "Modes");
  await page.getByLabel("Default mode").selectOption("headless");
  await expect(configSection(page, "Modes").locator(".disclosure-summary")).toHaveText("1 set");
  // Enabling a component brings its own section, already open because its secret is required, and opens
  // the Image section whose default image no longer provides every required capability.
  await openSection(page, "Components");
  await page.getByLabel(/^github connector/).check();
  await expect(configSection(page, "Components").locator(".disclosure-summary")).toHaveText("1 set");
  await expect(configSection(page, "github").locator("details")).toHaveAttribute("open");
  await expect(configSection(page, "github").locator(".disclosure-summary")).toHaveText("Defaults · 1 required");
  await expect(configSection(page, "Image").locator("details")).toHaveAttribute("open");
  await expect(page.getByText("This image does not provide: gh.")).toBeVisible();
  // The one primary action is the preview; the review then makes Create Config primary.
  await expect(content(page).locator("button.primary-action:visible")).toHaveText(["Preview Config"]);
});

test("submitting New Config with an unmet required answer opens its collapsed section and focuses the first invalid field", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const requests = await mockConfigForm(page);
  await page.getByLabel("Config name").fill("dev");
  await openSection(page, "Components");
  await page.getByLabel(/^github connector/).check();
  // Collapse every section that holds a required answer, as an operator skimming the form might.
  for (const title of ["Role", "github", "Repositories"]) {
    await configSection(page, title).locator("summary").click();
    await expect(configSection(page, title).locator("details")).not.toHaveAttribute("open");
  }
  await expect(page.getByLabel("ROLE_PATH")).toBeHidden();
  const requiredErrors = page.locator(".field-error").filter({ hasText: "This answer is required." });
  await expect(requiredErrors).toHaveCount(0);
  const preview = page.getByRole("button", { name: "Preview Config", exact: true });
  await preview.click();
  // Nothing is sent; each section with a missing answer opens and the first invalid field takes focus.
  for (const title of ["Role", "github", "Repositories"]) await expect(configSection(page, title).locator("details")).toHaveAttribute("open");
  await expect(page.getByLabel("ROLE_PATH")).toBeFocused();
  await expect(page.getByLabel("ROLE_PATH")).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByLabel("github-token secret")).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByLabel("role-source URL")).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByLabel("project URL")).not.toHaveAttribute("aria-invalid");
  await expect(requiredErrors).toHaveCount(3);
  expect(requests).toEqual([]);

  // An answered field drops its mark at once; the next submit moves focus to the next missing one.
  await page.getByLabel("ROLE_PATH").fill("dev");
  await expect(page.getByLabel("ROLE_PATH")).not.toHaveAttribute("aria-invalid");
  await preview.click();
  await expect(page.getByLabel("github-token secret")).toBeFocused();
  await page.getByLabel("github-token secret").selectOption({ label: "gh-token — prod (opaque)" });
  await configSection(page, "Repositories").locator("summary").click();
  await preview.click();
  await expect(configSection(page, "Repositories").locator("details")).toHaveAttribute("open");
  await expect(page.getByLabel("role-source URL")).toBeFocused();
  expect(requests).toEqual([]);
  await page.getByLabel("role-source URL").fill("git@github.com:o/agents.git");
  await expect(requiredErrors).toHaveCount(0);
  // A typed input the browser itself rejects (an email) gets the same treatment from a collapsed section.
  await openSection(page, "Default git identity");
  await page.getByLabel("Override the inherited default identity").check();
  await page.getByLabel("Default author email").fill("not-an-email");
  await configSection(page, "Default git identity").locator("summary").click();
  await expect(page.getByLabel("Default author email")).toBeHidden();
  await preview.click();
  await expect(configSection(page, "Default git identity").locator("details")).toHaveAttribute("open");
  await expect(page.getByLabel("Default author email")).toBeFocused();
  expect(requests).toEqual([]);
  await page.getByLabel("Override the inherited default identity").uncheck();
  await preview.click();
  await expect(page.getByRole("heading", { name: "Review Config" })).toBeVisible();
  expect(requests).toHaveLength(1);
  expect(requests[0].input).toEqual({ config: { name: "dev", realm: "prod" }, mode: "interactive", spec: { blueprint: { name: "claude", realm: "prod" }, values: { ROLE_PATH: "dev" }, repos: { "role-source": { url: "git@github.com:o/agents.git" } }, secrets: { "github-token": { secretRef: { name: "gh-token", realm: "prod" } } }, components: ["github"] } });
  // The review keeps the button hierarchy of New Cell: Create Config is the only primary action.
  await expect(content(page).locator("button.primary-action:visible")).toHaveText(["Create Config"]);
  await expect(page.getByRole("button", { name: "Refresh preview", exact: true })).not.toHaveClass(/primary-action/);
  await expect(page.locator(".creation-review details").filter({ hasText: "Cell this Config renders" })).not.toHaveAttribute("open");
});

test("create from config resolves configs and blueprint refs scoped at realm, space and stack", async ({ page }) => {
  const scopedMetadata = scope => ({ name: scope.at(-1), realm: scope[0], ...(scope.length > 2 ? { space: scope[1] } : {}), ...(scope.length > 3 ? { stack: scope[2] } : {}) });
  const cases = [
    { config: ["prod", "agent"], blueprint: ["prod", "claude"] },
    { config: ["prod", "shared", "agent"], blueprint: ["prod", "shared", "claude"] },
    { config: ["prod", "shared", "services", "agent"], blueprint: ["prod", "shared", "services", "claude"] },
    { config: ["prod", "shared", "services", "team"], blueprint: ["prod", "claude"] },
  ];
  const configs = cases.map(({ config, blueprint }) => ({ kind: "CellConfig", metadata: scopedMetadata(config), spec: { blueprint: scopedMetadata(blueprint) } }));
  const blueprints = cases.map(({ blueprint }) => ({ kind: "CellBlueprint", metadata: scopedMetadata(blueprint), spec: {} }));
  const lookups = [];
  await mock(page);
  await page.route("**/api/catalog/**", route => {
    const url = new URL(route.request().url());
    const [, , kind, name] = url.pathname.split("/").filter(Boolean);
    const docs = kind === "configs" ? configs : kind === "blueprints" ? blueprints : [];
    if (!name) return route.fulfill({ json: docs });
    const scope = [...["realm", "space", "stack"].map(key => url.searchParams.get(key)).filter(Boolean), decodeURIComponent(name)];
    lookups.push({ kind, scope, params: [...url.searchParams.values()] });
    const match = docs.find(doc => JSON.stringify([doc.metadata.realm, doc.metadata.space, doc.metadata.stack, doc.metadata.name].filter(Boolean)) === JSON.stringify(scope));
    return route.fulfill(match ? { json: match } : { status: 404, json: { error: { code: "not_found", message: `${kind} ${scope.join(" / ")} not found` } } });
  });
  let created;
  await page.route("**/api/cells/from-config/*", async route => {
    const input = route.request().postDataJSON();
    const document = { metadata: { name: "team-1" }, spec: { realmId: "prod", spaceId: "shared", stackId: "services" } };
    if (route.request().url().endsWith("create")) { created = input; await route.fulfill({ json: document }); }
    else await route.fulfill({ json: { document, preview: "reviewed" } });
  });
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells/team-1/start", route => route.fulfill({ json: {} }));
  for (const { config, blueprint } of cases) {
    await page.goto(`${server.url}#/new-cell?config=${encodeURIComponent(JSON.stringify(config))}`);
    await expect(page.getByRole("link", { name: "claude", exact: true })).toHaveAttribute("href", `#/blueprints/${blueprint.join("/")}`);
    await expect(page.getByText("Blueprint reference is missing")).toHaveCount(0);
    // A coordinate the Config is scoped to pins its target select (#1603).
    const [space, stack] = [page.getByLabel("Target space"), page.getByLabel("Target stack")];
    if (config.length > 2) { await expect(space).toBeDisabled(); await expect(space).toHaveValue(config[1]); } else await expect(space).toBeEnabled();
    if (config.length > 3) { await expect(stack).toBeDisabled(); await expect(stack).toHaveValue(config[2]); } else await expect(stack).toBeEnabled();
  }
  await expect(page.getByRole("button", { name: "Preview Cell", exact: true })).toBeEnabled();
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await expect(page.getByLabel("Start the cell")).toBeChecked();
  await page.getByRole("button", { name: "Create Cell", exact: true }).click();
  await expect.poll(() => created?.config).toEqual({ name: "team", realm: "prod", space: "shared", stack: "services" });
  for (const lookup of lookups) expect(lookup.params).not.toContain(lookup.scope.at(-1));
});

// #1603: the create action of a Config or Blueprint page is the header row's last, primary item, and
// nothing sits between the overview and the next panel.
test("Config and Blueprint pages put their create action in the header row", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockCatalog(page);
  for (const [kind, name] of [["configs", /Create Cell from this Config/], ["blueprints", /Create Config from this Blueprint/]]) {
    await page.goto(`${server.url}#/${kind}/prod/shared/services/shared-name`);
    const actions = content(page).locator(".detail-actions");
    const link = actions.getByRole("link", { name });
    await expect(link).toBeVisible();
    await expect(actions.locator("a, button").last()).toHaveText(name);
    const [linkBox, rowBox] = [await link.boundingBox(), await actions.boundingBox()];
    expect(Math.abs((linkBox.x + linkBox.width) - (rowBox.x + rowBox.width))).toBeLessThan(2);
    // The overview panel is followed directly by another panel, not by a stray link.
    const overview = content(page).locator("section.panel").filter({ has: page.getByRole("heading", { name: "Resource overview" }) });
    await expect(overview).toBeVisible();
    expect(await overview.evaluate(el => el.nextElementSibling?.tagName)).not.toBe("A");
    await expect(content(page).locator(":scope > a.new-cell-action, .workspace-content > a.new-cell-action")).toHaveCount(0);
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${server.url}#/configs/prod/shared/services/shared-name`);
  await expect(content(page).locator(".detail-actions").getByRole("link", { name: /Create Cell from this Config/ })).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

// A realm-scoped Config's cells may land in any space and stack of its realm (#1603). The inventory
// below gives the realm a default space with a default stack, and a space "a" with stacks "b" and "c".
async function mockTargets(page) {
  await mockCatalog(page);
  await page.route("**/api/realms/prod/spaces", route => route.fulfill({ json: [doc(["prod", "default"]), doc(["prod", "shared"]), doc(["prod", "a"])] }));
  await page.route("**/api/realms/prod/spaces/default/stacks", route => route.fulfill({ json: [doc(["prod", "default", "default"])] }));
  await page.route("**/api/realms/prod/spaces/a/stacks", route => route.fulfill({ json: [doc(["prod", "a", "b"]), doc(["prod", "a", "c"])] }));
  const requests = [];
  const starts = [];
  await page.route("**/api/cells/from-config/*", async route => {
    const input = route.request().postDataJSON();
    const create = route.request().url().endsWith("create");
    requests.push({ step: create ? "create" : "preview", input });
    const document = { apiVersion: "v1beta1", kind: "Cell", metadata: { name: input.resolvedName || input.name || "worker-1a2b3c" }, spec: { realmId: "prod", spaceId: input.targetSpace, stackId: input.targetStack, containers: [{ id: "work", attachable: true }] } };
    await route.fulfill({ json: create ? document : { document, preview: "reviewed", values: [] } });
  });
  await page.route("**/api/realms/prod/spaces/*/stacks/*/cells/worker-1a2b3c/**", route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/start")) { starts.push(path); return route.fulfill({ json: {} }); }
    if (path.endsWith("/containers")) return route.fulfill({ json: [{ id: "work", attachable: true }] });
    return route.fallback();
  });
  await page.goto(`${server.url}#/new-cell?config=${encodeURIComponent(JSON.stringify(["prod", "shared-name"]))}`);
  return { requests, starts };
}

test("New Cell creates, starts and attaches in one click, with no preview", async ({ page }) => {
  const { requests, starts } = await mockTargets(page);
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  // Only the essentials are open: the Config summary, the cell name and the target.
  await expect(page.getByRole("region", { name: "Config", exact: true }).locator(".config-summary")).toContainText("Config shared-name prod");
  await expect(page.getByLabel("Cell name (optional)")).toBeVisible();
  await expect(newCellSection(page, "Environment overrides").locator("details")).not.toHaveAttribute("open");
  await expect(newCellSection(page, "Advanced").locator("details")).not.toHaveAttribute("open");
  await expect(page.getByLabel("Start the cell")).toBeChecked();
  await expect(page.getByLabel("Attach a terminal")).toBeChecked();
  await page.getByRole("button", { name: "Create Cell", exact: true }).click();
  await expect(page).toHaveURL(/#\/workspace\/prod\/default\/default\/worker-1a2b3c$/);
  expect(requests.map(request => request.step)).toEqual(["create"]);
  expect(requests[0].input).not.toHaveProperty("preview");
  expect(requests[0].input).not.toHaveProperty("resolvedName");
  expect(requests[0].input).toMatchObject({ config: { name: "shared-name", realm: "prod" }, targetSpace: "default", targetStack: "default" });
  await expect.poll(() => starts.length).toBe(1);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
});

test("New Cell targets a space and stack below a realm-scoped Config, and Attach unticked lands on cellRead", async ({ page }) => {
  const { requests, starts } = await mockTargets(page);
  let attaches = 0;
  await page.routeWebSocket("**/attach", socket => { attaches++; socket.send(JSON.stringify({ type: "ready" })); });
  const [space, stack] = [page.getByLabel("Target space"), page.getByLabel("Target stack")];
  await expect(space).toHaveValue("default");
  await expect(stack).toHaveValue("default");
  await expect(space.getByRole("option")).toHaveText(["a", "default", "shared"]);
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await expect(previewPanel(page)).toBeVisible();
  // Changing the space drops the preview and resets the stack by the same rule: no default here, so the first.
  await space.selectOption("a");
  await expect(previewPanel(page)).toHaveCount(0);
  await expect(stack).toHaveValue("b");
  await expect(stack.getByRole("option")).toHaveText(["b", "c"]);
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await expect(previewPanel(page).locator(".creation-summary dd").nth(1)).toHaveText("prod / a / b");
  expect(requests.at(-1).input).toMatchObject({ targetSpace: "a", targetStack: "b" });
  await stack.selectOption("c");
  await expect(previewPanel(page)).toHaveCount(0);
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await expect(previewPanel(page).locator(".creation-summary dd").nth(1)).toHaveText("prod / a / c");

  // Start without a terminal: the cell starts and the UI opens its read page, with no attach session.
  await page.getByLabel("Attach a terminal").uncheck();
  await expect(page.getByLabel("Start the cell")).toBeChecked();
  await page.getByRole("button", { name: "Create Cell", exact: true }).click();
  await expect(page).toHaveURL(/#\/resource\/prod\/a\/c\/worker-1a2b3c$/);
  expect(requests.at(-1)).toMatchObject({ step: "create", input: { targetSpace: "a", targetStack: "c", preview: "reviewed", resolvedName: "worker-1a2b3c" } });
  await expect.poll(() => starts.length).toBe(1);
  await expect(toasts(page)).toContainText("started.");
  await page.waitForTimeout(300);
  expect(attaches).toBe(0);
});

test("the New Cell preview lists what each source contributes, in source order", async ({ page }) => {
  await mockCatalog(page);
  await page.route("**/api/cells/from-config/*", route => route.fulfill({ json: { document: { metadata: { name: "new-cell" }, spec: { realmId: "prod", spaceId: "shared", stackId: "services" } }, preview: "reviewed", values: [
    { kind: "value", key: "REGION", value: "", origin: "stack" },
    { kind: "component", key: "github", value: "", origin: "config" },
    { kind: "mode", key: "interactive", value: "", origin: "blueprint" },
    { kind: "image", key: "base", value: "ghcr.io/example/base:v1@sha256:abc", origin: "realm" },
    { kind: "secret", key: "token", value: "", origin: "space" },
    { kind: "value", key: "PROMPT", value: "", origin: "cell" },
  ], connectors: [{ connector: "github", title: "GitHub", component: "github", method: "token", delivery: "env", state: "resolved", identity: { name: "gh-token", realm: "prod" }, origin: "space" }] } }));
  await page.goto(`${server.url}#/new-cell?config=${encodeURIComponent(JSON.stringify(["prod", "shared", "services", "shared-name"]))}`);
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  const sources = previewPanel(page).getByRole("group", { name: "Preview sources" });
  await expect(sources.locator(":scope > [role=group] > h4")).toHaveText([/^From the Blueprint/, /^From the Config/, /^Connectors/, /^Realm defaults prod/, /^Space defaults shared/, /^Stack defaults services/, /^Set for this cell/]);
  await expect(sources.getByRole("link", { name: "shared-name" })).toHaveAttribute("href", /#\/configs\/prod\/shared\/services\/shared-name/);
  const connector = sources.getByRole("table", { name: "Connectors", exact: true }).getByRole("row").nth(1);
  await expect(connector).toHaveText(/GitHub.*token.*gh-token.*prod.*Space default/);
  await expect(sources.getByRole("table", { name: "Realm defaults" })).toContainText("ghcr.io/example/base:v1@sha256:abc");
  await expect(sources.getByRole("table", { name: "Stack defaults" })).toContainText("hidden");
});

test("kill and purge require explicit confirmation, and the cell Purge dialog offers no no-op options", async ({ page }) => {
  const submitted = [];
  await mock(page, (path, request) => {
    if (request.method() !== "POST") return undefined;
    if (path.endsWith("/kill")) return { json: { action: "kill", killed: true } };
    if (path.endsWith("/purge")) {
      const options = request.postDataJSON();
      submitted.push(options);
      return { json: {
        action: "purge", force: options.force, cascade: options.cascade,
        purgeSucceeded: true, metadataDeleted: true, cgroupDeleted: true,
        containersDeleted: true, deleted: ["containers", "cgroup", "metadata"], purged: ["cni-resources"],
      } };
    }
    return undefined;
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await page.getByText("Advanced actions", { exact: true }).click();
  await page.getByRole("button", { name: "Kill cell…" }).click();
  let dialog = page.getByRole("dialog");
  await expect(dialog.getByText("dev / shared / services / api", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Confirm Kill" })).toBeDisabled();
  await dialog.getByLabel("I understand this immediately terminates the cell's running containers.").check();
  await dialog.getByRole("button", { name: "Confirm Kill" }).click();
  await expect(toasts(page)).toContainText(["Cell killed"]);
  await expect(page.getByRole("button", { name: "Purge cell…" })).toHaveCount(0);

  await page.getByText("Advanced actions", { exact: true }).click();
  await page.getByRole("button", { name: "Purge cell…" }).click();
  dialog = page.getByRole("dialog");
  // --force and --cascade change nothing for a cell (kukeond only records them), so the dialog no longer
  // offers them (#1568). The request body is the one the dialog always sent by default.
  await expect(dialog.getByRole("checkbox")).toHaveCount(0);
  await expect(dialog).not.toContainText("--force");
  await expect(dialog).not.toContainText("--cascade");
  await expect(dialog.getByRole("button", { name: "Confirm Purge" })).toBeDisabled();
  await dialog.getByLabel("Type the full target above to enable Purge").fill("dev / shared / services / api");
  await dialog.getByRole("button", { name: "Confirm Purge" }).click();
  await expect(toasts(page)).toHaveCount(1);
  await expect(toasts(page)).toContainText("Cell purged");
  await expect(toasts(page)).not.toContainText("--force");
  await expect(toasts(page).locator(".toast-title")).toHaveText("dev / shared / services / api");
  expect(submitted).toEqual([{ force: false, cascade: false }]);
});

test("partial purge reports incomplete cleanup instead of success", async ({ page }) => {
  await mock(page, (path, request) => {
    if (request.method() !== "POST" || !path.endsWith("/purge")) return undefined;
    return { json: {
      action: "purge", purgeSucceeded: false, metadataDeleted: false,
      cgroupDeleted: true, containersDeleted: true,
      deleted: ["containers", "cgroup"],
      purged: ["cni-resources-incomplete", "purge-error:CNI DEL failed"],
    } };
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await page.getByText("Advanced actions", { exact: true }).click();
  await page.getByRole("button", { name: "Purge cell…" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Type the full target above to enable Purge").fill("dev / shared / services / api");
  await dialog.getByRole("button", { name: "Confirm Purge" }).click();
  const feedback = toasts(page);
  await expect(feedback).toContainText("Purge incomplete.");
  await expect(feedback).not.toContainText("Cell purged.");
  await expect(feedback).toContainText("cni-resources-incomplete");
});

test("confirmed deletion closes only that cell terminal and removes only that cell", async ({ page }) => {
  let deletedDev = false;
  const openSockets = new Set();
  await mock(page, (path, request) => {
    if (request.method() === "POST" && path.endsWith("/delete")) {
      deletedDev = path.includes("/realms/dev/");
      return { json: { action: "delete", metadataDeleted: true, cgroupDeleted: true, containersDeleted: true } };
    }
    if (path.endsWith("/cells") && path.includes("/realms/dev/") && deletedDev) return { json: [] };
    if (path.endsWith("/containers")) return { json: [{ id: "web", attachable: true }] };
    return undefined;
  });
  await page.routeWebSocket("**/attach", socket => {
    const path = new URL(socket.url()).pathname;
    openSockets.add(path);
    socket.onClose(() => openSockets.delete(path));
    socket.send(JSON.stringify({ type: "ready" }));
  });

  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect.poll(() => openSockets.size).toBe(1);
  await page.getByRole("link", { name: "Open prod / shared / services / api" }).click();
  await expect.poll(() => openSockets.size).toBe(2);
  await page.getByRole("link", { name: "Open dev / shared / services / api" }).click();
  await page.getByText("Advanced actions", { exact: true }).click();
  await page.getByRole("button", { name: "Delete cell…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("dev / shared / services / api", { exact: true })).toBeVisible();
  await dialog.getByLabel("I understand Delete removes this cell but does not perform Purge cleanup.").check();
  await dialog.getByRole("button", { name: "Confirm Delete" }).click();
  await expect(page).toHaveURL(/#\/$/);
  await expect.poll(() => openSockets.size).toBe(1);
  expect([...openSockets].every(path => path.includes("/realms/prod/"))).toBeTruthy();
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(cells.getByRole("link", { name: "Open dev / shared / services / api" })).toHaveCount(0);
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toBeVisible();
});

test("a rejected delete keeps the cell and its terminal session", async ({ page }) => {
  let closed = false;
  let connected = false;
  await mock(page, (path, request) => request.method() === "POST" && path.endsWith("/delete")
    ? { status: 403, json: { error: { code: "permission_denied", message: "delete permission denied" } } }
    : path.endsWith("/containers") ? { json: [{ id: "web", attachable: true }] } : undefined);
  await page.routeWebSocket("**/attach", socket => {
    connected = true;
    socket.onClose(() => { closed = true; });
    socket.send(JSON.stringify({ type: "ready" }));
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect.poll(() => connected).toBe(true);
  expect(closed).toBe(false);
  await page.getByText("Advanced actions", { exact: true }).click();
  await page.getByRole("button", { name: "Delete cell…" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("I understand Delete removes this cell but does not perform Purge cleanup.").check();
  await dialog.getByRole("button", { name: "Confirm Delete" }).click();
  await expect(toasts(page)).toContainText(["Permission denied"]);
  await expect(page.getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
  expect(closed).toBe(false);
});

test("an unavailable delete response reconciles the cell before reporting its outcome", async ({ page }) => {
  let closed = false;
  let connected = false;
  await mock(page, (path, request) => request.method() === "POST" && path.endsWith("/delete")
    ? { status: 503, json: { error: { code: "unavailable", message: "daemon response timed out" } } }
    : path.endsWith("/containers") ? { json: [{ id: "web", attachable: true }] } : undefined);
  await page.routeWebSocket("**/attach", socket => {
    connected = true;
    socket.onClose(() => { closed = true; });
    socket.send(JSON.stringify({ type: "ready" }));
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect.poll(() => connected).toBe(true);
  await page.getByText("Advanced actions", { exact: true }).click();
  await page.getByRole("button", { name: "Delete cell…" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("I understand Delete removes this cell but does not perform Purge cleanup.").check();
  await dialog.getByRole("button", { name: "Confirm Delete" }).click();
  await expect(toasts(page)).toContainText(["Daemon unavailable"]);
  await expect(toasts(page)).toContainText(["fresh daemon read confirms the cell still exists"]);
  await expect(page.getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
  await expect.poll(() => closed).toBe(true);
});

test("cells table row actions run lifecycle verbs, confirm destructive ones, and lock the row while in flight", async ({ page }) => {
  const posts = [];
  let releaseStop;
  let deleted = false;
  await mock(page, async (path, request) => {
    if (request.method() === "POST") {
      posts.push(path);
      if (path.endsWith("/stop")) { await new Promise(resolve => { releaseStop = resolve; }); return { json: { action: "stop" } }; }
      if (path.endsWith("/start")) return { status: 409, json: { error: { code: "operation_in_progress", message: "another lifecycle operation is running for this cell" } } };
      if (path.endsWith("/delete")) { deleted = true; return { json: { action: "delete", metadataDeleted: true, cgroupDeleted: true, containersDeleted: true } }; }
    }
    if (deleted && path === "/api/realms/dev/spaces/shared/stacks/services/cells") return { json: [] };
    return undefined;
  });
  await page.goto(`${server.url}#/cells`);
  const cellRow = prefix => page.locator("tbody tr").filter({ has: page.locator(".scope-cell", { hasText: prefix }) });
  const devRow = cellRow("dev / shared / services");
  const prodRow = cellRow("prod / shared / services");
  const dev = "dev / shared / services / api";
  // Ready rows offer Stop and Restart; any other state offers Start.
  await expect(devRow.getByRole("button", { name: `Stop cell ${dev}` })).toHaveAttribute("title", "Stop");
  await expect(devRow.getByRole("button", { name: `Restart cell ${dev}` })).toBeVisible();
  await expect(devRow.getByRole("button", { name: /^Start cell / })).toHaveCount(0);
  await expect(prodRow.getByRole("button", { name: "Start cell prod / shared / services / api" })).toBeVisible();
  await expect(prodRow.getByRole("button", { name: /^Stop cell / })).toHaveCount(0);
  await expect(devRow.getByRole("link", { name: `Open terminal for ${dev}` })).toHaveAttribute("href", "#/workspace/dev/shared/services/api");
  await expect(devRow.getByRole("link", { name: `View document for ${dev}` })).toBeVisible();
  for (const name of ["Kill", "Delete", "Purge"]) await expect(devRow.getByRole("button", { name: `${name} cell ${dev}` })).toHaveAttribute("title", `${name}…`);

  // Stop runs without confirmation; while it is in flight every row icon is disabled with a progress state.
  await devRow.getByRole("button", { name: `Stop cell ${dev}` }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(devRow.getByRole("button", { name: `Stop cell ${dev}` })).toHaveAttribute("aria-busy", "true");
  for (const button of await devRow.getByRole("button").all()) await expect(button).toBeDisabled();
  await expect(prodRow.getByRole("button", { name: /^Start cell / })).toBeEnabled();
  expect(await devRow.locator(".row-action.is-busy").evaluate(el => getComputedStyle(el).opacity)).toBe("1");
  releaseStop();
  await expect(toasts(page)).toContainText("Cell stopped");
  await expect(devRow.getByRole("button", { name: `Delete cell ${dev}` })).toBeEnabled();
  expect(posts).toEqual(["/api/realms/dev/spaces/shared/stacks/services/cells/api/stop"]);

  // A concurrent action on the same cell surfaces the daemon's per-cell lock error.
  await prodRow.getByRole("button", { name: /^Start cell / }).click();
  await expect(toasts(page).filter({ hasText: "Start failed" })).toContainText("Another operation is in progress");

  // Delete opens the confirmation; dismissing it sends nothing.
  await devRow.getByRole("button", { name: `Delete cell ${dev}` }).click();
  let dialog = page.getByRole("dialog");
  await expect(dialog.getByText(dev, { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(posts.filter(path => path.endsWith("/delete"))).toHaveLength(0);

  // Purge keeps its typed-scope confirmation.
  await devRow.getByRole("button", { name: `Purge cell ${dev}` }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Confirm Purge" })).toBeDisabled();
  // The dialog lives inside the nowrap, right-aligned actions cell; its copy must still wrap left-aligned at phone width.
  const viewport = page.viewportSize();
  await page.setViewportSize({ width: 390, height: 844 });
  const description = dialog.locator("#cell-action-dialog-description");
  const layout = await description.evaluate(el => { const style = getComputedStyle(el); return { height: el.getBoundingClientRect().height, lineHeight: parseFloat(style.lineHeight), whiteSpace: style.whiteSpace, textAlign: style.textAlign, overflow: el.scrollWidth - el.clientWidth }; });
  expect(layout.whiteSpace).toBe("normal");
  expect(layout.textAlign).toBe("left");
  expect(layout.height).toBeGreaterThan(layout.lineHeight * 1.5);
  expect(layout.overflow).toBeLessThanOrEqual(0);
  expect(await dialog.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
  await page.setViewportSize(viewport);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  // A confirmed delete stays on the Cells table; the row drops once a fresh read omits it.
  await devRow.getByRole("button", { name: `Delete cell ${dev}` }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByLabel("I understand Delete removes this cell but does not perform Purge cleanup.").check();
  await dialog.getByRole("button", { name: "Confirm Delete" }).click();
  await expect(toasts(page).filter({ hasText: "Cell deleted" })).toHaveCount(1);
  await expect(page).toHaveURL(/#\/cells$/);
  await expect(devRow).toHaveCount(0);
  await expect(prodRow).toBeVisible();
  expect(posts.filter(path => /\/(purge|kill)$/.test(path))).toHaveLength(0);
});

test("space row Delete and Purge confirm the exact scope, surface child refusal and drop the row after a fresh read", async ({ page }) => {
  const posts = [];
  let purged = false;
  await mock(page, async (path, request) => {
    if (request.method() === "POST") {
      const options = JSON.parse(request.postData() ?? "{}");
      posts.push({ path, options });
      if (!options.cascade) return { status: 409, json: { error: { code: "has_dependencies", message: 'resource has child resources: space "shared" has 1 stack(s). Use --cascade to delete them or --force to skip validation' } } };
      purged = true;
      return { json: { action: "purge", purgeSucceeded: true, metadataDeleted: true, force: options.force, cascade: options.cascade, deleted: ["stack:services"], purged: [] } };
    }
    if (purged && path === "/api/realms/dev/spaces/shared") return { status: 404, json: { error: { code: "not_found", message: "space not found" } } };
    if (purged && path === "/api/realms/dev/spaces") return { json: [] };
    return undefined;
  });
  await page.goto(`${server.url}#/spaces`);
  const row = page.locator("tbody tr").filter({ has: page.getByRole("link", { name: "shared" }) }).filter({ has: page.locator(".scope-cell", { hasText: /^dev$/ }) });
  const target = "dev / shared";
  for (const name of ["Delete", "Purge"]) await expect(row.getByRole("button", { name: `${name} space ${target}` })).toHaveAttribute("title", `${name}…`);
  await expect(row.getByRole("link", { name: `View document for ${target}` })).toBeVisible();

  // Delete confirms the exact scope and child counts; cancelling sends nothing.
  await row.getByRole("button", { name: `Delete space ${target}` }).click();
  let dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Delete space" })).toBeVisible();
  await expect(dialog.getByText(target, { exact: true })).toBeVisible();
  await expect(dialog).toContainText("1 stack, 1 cell");
  await expect(dialog.getByRole("checkbox", { name: /--cascade/ })).not.toBeChecked();
  await expect(dialog.getByRole("checkbox", { name: /--force/ })).not.toBeChecked();
  await expect(dialog.getByRole("button", { name: "Confirm Delete" })).toBeDisabled();
  await expect(dialog.locator(".scope-action-warning")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(posts).toHaveLength(0);

  // Purge requires the typed full scope; without cascade the daemon refusal is shown and the row stays.
  await row.getByRole("button", { name: `Purge space ${target}` }).click();
  dialog = page.getByRole("dialog");
  const confirm = dialog.getByRole("button", { name: "Confirm Purge" });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel("Type the full target above to enable Purge").fill("dev / share");
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel("Type the full target above to enable Purge").fill(target);
  await confirm.click();
  const refusal = toasts(page).filter({ hasText: "Purge refused" });
  await expect(refusal).toContainText('space "shared" has 1 stack(s)');
  await expect(refusal).toContainText("Enable cascade");
  await expect(row).toBeVisible();
  expect(posts).toEqual([{ path: "/api/realms/dev/spaces/shared/purge", options: { force: false, cascade: false } }]);

  // With cascade the purge succeeds and the row drops once the inventory read omits it.
  await row.getByRole("button", { name: `Purge space ${target}` }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByRole("checkbox", { name: /--cascade/ }).check();
  await dialog.getByLabel("Type the full target above to enable Purge").fill(target);
  await dialog.getByRole("button", { name: "Confirm Purge" }).click();
  await expect(toasts(page).filter({ hasText: "Space purged" })).toContainText("fresh daemon read confirms the space is gone");
  await expect(row).toHaveCount(0);
  await expect(page.locator("tbody tr").filter({ has: page.locator(".scope-cell", { hasText: /^prod$/ }) })).toBeVisible();
  expect(posts.at(-1)).toEqual({ path: "/api/realms/dev/spaces/shared/purge", options: { force: false, cascade: true } });
});

test("the kuke-system realm row has no Delete or Purge, and targets inside it warn about kukeond", async ({ page }) => {
  await mock(page, path => path === "/api/realms" ? { json: [doc(["default"]), doc(["kuke-system"])] } : undefined);
  await page.goto(`${server.url}#/realms`);
  const realmRow = name => page.locator("tbody tr").filter({ has: page.getByRole("link", { name, exact: true }) });
  await expect(realmRow("kuke-system").getByRole("link", { name: "View document for kuke-system" })).toBeVisible();
  await expect(realmRow("kuke-system").getByRole("button")).toHaveCount(0);
  await expect(realmRow("default").getByRole("button", { name: "Delete realm default" })).toBeVisible();
  await expect(realmRow("default").getByRole("button", { name: "Purge realm default" })).toBeVisible();

  await page.goto(`${server.url}#/stacks`);
  const stackRow = page.locator("tbody tr").filter({ has: page.locator(".scope-cell", { hasText: "kuke-system / shared" }) });
  await stackRow.getByRole("button", { name: "Delete stack kuke-system / shared / services" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.locator(".scope-action-warning")).toContainText("can stop kukeond and disconnect this UI");
  await expect(dialog.locator(".scope-action-warning")).toContainText("kuke init");
  await dialog.getByRole("button", { name: "Cancel" }).click();

  await page.goto(`${server.url}#/stacks`);
  const defaultStack = page.locator("tbody tr").filter({ has: page.locator(".scope-cell", { hasText: "default / shared" }) });
  await defaultStack.getByRole("button", { name: "Delete stack default / shared / services" }).click();
  await expect(page.getByRole("dialog").locator(".scope-action-warning")).toHaveCount(0);
});

test("config and blueprint rows link to the pre-filled creation pages", async ({ page }) => {
  await mockCatalog(page);
  await page.goto(`${server.url}#/configs`);
  const scope = ["prod", "shared", "services", "shared-name"];
  const catalogRow = () => page.locator("tbody tr").filter({ has: page.locator(".scope-cell", { hasText: /^prod \/ shared \/ services$/ }) });
  const configRow = catalogRow();
  const shortcut = configRow.getByRole("link", { name: `New cell from config ${scope.join(" / ")}` });
  await expect(shortcut).toHaveAttribute("title", "New cell from this config");
  await shortcut.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/#\/new-cell\?config=/);
  // Opened from a Config, the form shows a one-line summary of it; Change reveals the chooser (#1603).
  const configPanel = page.getByRole("region", { name: "Config", exact: true });
  await expect(configPanel.locator(".config-summary")).toContainText("Config shared-name prod / shared / services");
  await expect(page.getByRole("combobox", { name: /^CellConfig/ })).toHaveCount(0);
  await configPanel.getByRole("button", { name: "Change" }).click();
  await expect(page.getByRole("combobox", { name: /^CellConfig/ })).toHaveValue(JSON.stringify(scope));

  await page.goto(`${server.url}#/blueprints`);
  const blueprint = catalogRow().getByRole("link", { name: `New config from blueprint ${scope.join(" / ")}` });
  await expect(blueprint).toHaveAttribute("href", `#/new-config?blueprint=${encodeURIComponent(JSON.stringify(scope))}`);
  await expect(catalogRow().getByRole("link", { name: /^New cell from/ })).toHaveCount(0);
});

test("deleting a config confirms the full scope and known Cells, shows refusals verbatim and drops the row only after a fresh read", async ({ page }) => {
  const target = ["prod", "shared", "services"];
  const isTarget = url => target.every((value, i) => url.searchParams.get(["realm", "space", "stack"][i]) === value);
  const posts = [];
  let deleted = false;
  let refuse = true;
  await mockCatalog(page, url => {
    if (url.pathname === "/api/catalog/configs/shared-name/delete") {
      posts.push({ scope: Object.fromEntries(url.searchParams) });
      if (refuse) return { status: 503, json: { error: { code: "unavailable", message: "failed to delete config document: read-only file system" } } };
      deleted = true;
      return { json: { kind: "configs", name: "shared-name", scope: [...target, ""], deleted: true } };
    }
    if (deleted && url.pathname === "/api/catalog/configs/shared-name" && isTarget(url)) return { status: 404, json: { error: { code: "not_found", message: "config not found" } } };
    if (deleted && url.pathname === "/api/catalog/configs") {
      const scopes = [["dev"], ["prod"], ["prod", "shared"]];
      return { json: scopes.map(scope => catalogDoc("configs", scope)) };
    }
    if (url.pathname === "/api/catalog/secrets") {
      const secret = catalogDoc("secrets", ["prod"]);
      secret.spec = { type: "Opaque", data: "must-never-leak" };
      return { json: [secret] };
    }
  });
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells", route => {
    const cell = doc(["prod", "shared", "services", "api"]);
    cell.spec.provenance = { bindingKind: "config", bindingRef: { name: "shared-name", realm: "prod", space: "shared", stack: "services" } };
    return route.fulfill({ json: [cell] });
  });
  page.on("request", request => {
    if (request.url().includes("/delete")) expect(request.headers()["content-type"]).toBe("application/json");
  });
  await page.goto(`${server.url}#/configs`);
  const row = () => page.locator("tbody tr").filter({ has: page.locator(".scope-cell", { hasText: /^prod \/ shared \/ services$/ }) });
  const deleteIcon = row().getByRole("button", { name: "Delete config prod / shared / services / shared-name" });
  await expect(deleteIcon).toHaveClass(/is-danger/);
  await expect(deleteIcon).toHaveAttribute("title", "Delete…");

  await deleteIcon.click();
  const dialog = page.getByRole("dialog", { name: "Delete config" });
  await expect(dialog.locator(".cell-action-target")).toContainText("Config");
  await expect(dialog.locator(".cell-action-target code")).toHaveText("prod / shared / services / shared-name");
  await expect(dialog.getByRole("region", { name: "Known related resources" }).getByRole("link", { name: "api", exact: true })).toHaveAttribute("href", "#/workspace/prod/shared/services/api");
  await expect(dialog.getByRole("button", { name: "Confirm Delete" })).toBeDisabled();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  expect(posts).toHaveLength(0);

  await deleteIcon.click();
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Confirm Delete" }).click();
  await expect(content(page).getByRole("alert")).toContainText("failed to delete config document: read-only file system");
  await expect(row()).toHaveCount(1);
  expect(posts).toEqual([{ scope: { realm: "prod", space: "shared", stack: "services" } }]);

  refuse = false;
  await deleteIcon.click();
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Confirm Delete" }).click();
  await expect(row()).toHaveCount(0);
  await expect(toasts(page).filter({ hasText: "Config deleted." })).toBeVisible();
  await expect(page.locator("tbody tr")).toHaveCount(3);
  expect(posts).toHaveLength(2);

  await page.goto(`${server.url}#/secrets`);
  await page.locator("tbody tr").first().getByRole("button", { name: /^Delete secret / }).click();
  const secretDialog = page.getByRole("dialog", { name: "Delete secret" });
  await expect(secretDialog).toContainText("The value is never shown here.");
  await expect(secretDialog.getByRole("region", { name: "Known related resources" })).toHaveCount(0);
  expect(await page.evaluate(() => document.body.innerText)).not.toContain("must-never-leak");
});

test("lifecycle toasts update in place, auto-dismiss after a hover pause, and errors persist until dismissed", async ({ page }) => {
  await page.clock.install();
  const posts = [];
  let release = () => {};
  let fail = false;
  await mock(page, async (path, request) => {
    if (request.method() !== "POST") return undefined;
    posts.push(path);
    await new Promise(resolve => { release = resolve; });
    return fail ? { status: 409, json: { error: { code: "invalid_state", message: "cell is busy" } } } : { json: {} };
  });
  const polite = page.locator('[role="status"][aria-live="polite"]');
  const assertive = page.locator('[role="alert"][aria-live="assertive"]');
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const row = page.locator(".cell-page-title-row");
  await row.getByRole("button", { name: "Stop", exact: true }).click();
  await expect.poll(() => posts.length).toBe(1);
  await expect(toasts(page)).toHaveCount(1);
  await expect(toasts(page)).toHaveClass(/pending/);
  await expect(toasts(page)).toHaveText(/Stopping cell…/);
  await expect(toasts(page).locator("[role]")).toHaveCount(0);
  await expect(polite).toHaveText("Stopping cell…");
  await expect(row.getByRole("button", { name: "Stop…", exact: true })).toBeDisabled();
  release();
  await expect(toasts(page)).toHaveClass(/success/);
  await expect(toasts(page)).toHaveCount(1);
  await expect(toasts(page)).toContainText("Cell stopped.");
  await expect(polite).toContainText("Cell stopped.");
  await expect(assertive).toHaveText("");
  await toasts(page).hover();
  await page.clock.fastForward(6000);
  await expect(toasts(page)).toHaveCount(1);
  await page.mouse.move(700, 600);
  await page.clock.fastForward(4000);
  await expect(toasts(page)).toHaveCount(1);
  await page.clock.fastForward(1100);
  await expect(toasts(page)).toHaveCount(0);

  fail = true;
  await row.getByRole("button", { name: "Restart", exact: true }).click();
  await expect.poll(() => posts.length).toBe(2);
  await expect(toasts(page)).toHaveClass(/pending/);
  await page.goto(`${server.url}#/resources`);
  release();
  await expect(toasts(page)).toHaveClass(/error/);
  await expect(toasts(page)).toContainText("Restart failed. Invalid cell state: cell is busy");
  await expect(toasts(page).locator(".toast-title")).toHaveText("dev / shared / services / api");
  await expect(assertive).toContainText("Restart failed");
  await expect(polite).toHaveText("");
  await page.clock.fastForward(30_000);
  await expect(toasts(page)).toHaveCount(1);
  await toasts(page).getByRole("button", { name: "Dismiss notification" }).focus();
  await page.keyboard.press("Escape");
  await expect(toasts(page)).toHaveCount(0);

  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  await row.getByRole("button", { name: "Restart", exact: true }).click();
  await expect.poll(() => posts.length).toBe(3);
  release();
  await expect(toasts(page)).toHaveClass(/error/);
  await expect(toasts(page).locator(".toast-title")).toHaveCount(0);
  await toasts(page).getByRole("button", { name: "Dismiss notification" }).click();
  await expect(toasts(page)).toHaveCount(0);
});

test("Start, Stop, Restart and Refresh on cellTerminal never resize the attached terminal", async ({ page }) => {
  let state = "Ready";
  let hold = null;
  const posts = [];
  const cell = "/api/realms/dev/spaces/shared/stacks/services/cells/api";
  await mock(page, async (path, request) => {
    if (request.method() === "POST") {
      posts.push(path);
      if (hold) await hold.promise;
      state = path.endsWith("/stop") ? "Stopped" : "Ready";
      return { json: {} };
    }
    if (path.endsWith("/containers")) return { json: [{ id: "shell", attachable: true }] };
    if (path === cell) return { json: { ...doc(["dev", "shared", "services", "api"]), status: { state } } };
    if (path.endsWith("/cells") && path.includes("/dev/")) return { json: fixture(path).map(item => ({ ...item, status: { state } })) };
  });
  // Messages from the first attachment only: the session Start reattaches is a new process and sizes itself once.
  const messages = [];
  let sockets = 0;
  await page.routeWebSocket("**/attach", socket => {
    const first = ++sockets === 1;
    socket.onMessage(message => { if (first) messages.push(message); });
    socket.send(JSON.stringify({ type: "ready" }));
  });
  const resizes = () => messages.filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length;
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await expect.poll(resizes).toBeGreaterThan(0);
  await page.waitForTimeout(200);
  const terminal = page.locator("#cell-terminal");
  const before = await terminal.boundingBox();
  const sent = resizes();
  const row = page.locator(".cell-page-title-row");
  async function act(name) {
    let release;
    hold = { promise: new Promise(resolve => { release = resolve; }) };
    const count = posts.length;
    await row.getByRole("button", { name, exact: true }).click();
    await expect.poll(() => posts.length).toBe(count + 1);
    await expect(toasts(page).first()).toHaveClass(/pending/);
    expect(await terminal.boundingBox()).toEqual(before);
    hold = null;
    release();
    await expect(toasts(page).first()).toHaveClass(/success/);
    await expect(page.getByRole("button", { name: "Refresh" })).toHaveAttribute("aria-busy", "false");
    await page.waitForTimeout(200);
    expect(await terminal.boundingBox()).toEqual(before);
    // On desktop the toast may briefly cover the title row's lifecycle icons; dismiss it as a user would.
    await toasts(page).first().getByRole("button", { name: "Dismiss notification" }).click();
    await expect(toasts(page)).toHaveCount(0);
  }
  await act("Restart");
  await act("Stop");
  await expect(page.locator("#cell-terminal .terminal-log-status")).toContainText("Cell is Stopped.");
  expect(await terminal.boundingBox()).toEqual(before);
  const socketsBeforeStart = sockets;
  await act("Start");
  // Start drops the interrupted session, so the Ready cell reattaches with no further click.
  await expect.poll(() => sockets).toBe(socketsBeforeStart + 1);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("button", { name: "Refresh" })).toHaveAttribute("aria-busy", "false");
  await page.waitForTimeout(200);
  expect(await terminal.boundingBox()).toEqual(before);
  expect(resizes()).toBe(sent);
  await expect(page.locator(".cell-detail-screen > :not(.page-heading):not(.cell-terminal-view):not(.cell-overview-content)")).toHaveCount(0);
});

test("create-and-start reports through one toast that links to cellRead without resizing the terminal", async ({ page }) => {
  await mockCatalog(page);
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells", route => route.fulfill({ json: [...fixture("/api/realms/prod/spaces/shared/stacks/services/cells"), doc(["prod", "shared", "services", "new-cell"])] }));
  let releaseStart;
  const startHeld = new Promise(resolve => { releaseStart = resolve; });
  let started = false;
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells/new-cell/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/start")) { started = true; await startHeld; return route.fulfill({ json: {} }); }
    if (path.endsWith("/containers")) return route.fulfill({ json: [{ id: "shell", attachable: true }] });
    return route.fallback();
  });
  await page.route("**/api/cells/from-config/*", async route => {
    const input = route.request().postDataJSON();
    const document = { metadata: { name: input.name }, spec: { realmId: "prod", spaceId: "shared", stackId: "services", containers: [{ id: "shell", attachable: true }] } };
    await route.fulfill({ json: route.request().url().endsWith("create") ? document : { document, preview: "reviewed" } });
  });
  const messages = [];
  await page.routeWebSocket("**/attach", socket => {
    socket.onMessage(message => { messages.push(message); });
    socket.send(JSON.stringify({ type: "ready" }));
  });
  const resizes = () => messages.filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length;
  await page.goto(`${server.url}#/configs/prod/shared/services/shared-name`);
  await page.getByRole("link", { name: /Create Cell from this Config/ }).click();
  await page.getByLabel("Cell name (optional)").fill("new-cell");
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  // Start and attach are the defaults (#1603).
  await page.getByRole("button", { name: "Create Cell", exact: true }).click();
  await expect(page).toHaveURL(/#\/workspace\/prod\/shared\/services\/new-cell$/);
  await expect.poll(() => started).toBe(true);
  await expect(toasts(page)).toHaveCount(1);
  await expect(toasts(page)).toHaveClass(/pending/);
  await expect(toasts(page)).toContainText("Cell prod / shared / services / new-cell created. Starting it…");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await expect.poll(resizes).toBeGreaterThan(0);
  await page.waitForTimeout(200);
  const terminal = page.locator("#cell-terminal");
  const before = await terminal.boundingBox();
  const sent = resizes();
  releaseStart();
  await expect(toasts(page)).toHaveClass(/success/);
  await expect(toasts(page)).toHaveCount(1);
  await expect(toasts(page)).toContainText("Cell prod / shared / services / new-cell started.");
  await page.waitForTimeout(200);
  expect(await terminal.boundingBox()).toEqual(before);
  expect(resizes()).toBe(sent);
  await expect(page.getByText("View Cell document returned by kukeon")).toHaveCount(0);
  await toasts(page).getByRole("link", { name: "View cell details" }).click();
  await expect(page).toHaveURL(/#\/resource\/prod\/shared\/services\/new-cell$/);
});

test("stale and incomplete inventory use a toolbar indicator with a failure popover instead of page banners", async ({ page }) => {
  let hold = null;
  let failStacks = false;
  await mock(page, async path => {
    if (hold) await hold.promise;
    if (failStacks && path === "/api/realms/prod/spaces/shared/stacks") return failure;
  });
  await page.goto(`${server.url}#/resources`);
  await expect(page.locator(".summary-card").first()).toContainText("View all");
  await expect(page.locator(".inventory-indicator")).toHaveCount(0);
  let release;
  hold = { promise: new Promise(resolve => { release = resolve; }) };
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("button", { name: "Refresh" })).toHaveAttribute("aria-busy", "true");
  await expect(page.getByText("Refreshing resource inventory…")).toHaveCount(0);
  hold = null;
  failStacks = true;
  release();
  await expect(page.getByRole("button", { name: "Refresh" })).toHaveAttribute("aria-busy", "false");
  await expect(inventoryIndicator(page, "stale")).toBeVisible();
  await expect(content(page).getByText(/Stale inventory|Incomplete inventory/)).toHaveCount(0);
  await expect(content(page).getByRole("alert")).toHaveCount(0);
  await inventoryIndicator(page, "stale").click();
  const popover = page.getByRole("dialog", { name: "Inventory status" });
  await expect(popover).toBeVisible();
  await expect(popover.getByRole("listitem")).toHaveText(["Stacks in prod / shared: Scoped read failed"]);
  await expect(popover.locator("time")).toHaveAttribute("datetime", /\d{4}-\d{2}-\d{2}T/);
  const [button, box] = [await inventoryIndicator(page, "stale").boundingBox(), await popover.boundingBox()];
  expect(box.y).toBeGreaterThanOrEqual(button.y + button.height);
  expect(box.x).toBeGreaterThanOrEqual(16);
  expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize().width - 16);
  await page.keyboard.press("Escape");
  await expect(popover).toBeHidden();
  failStacks = false;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.locator(".inventory-indicator")).toHaveCount(0);
});

test("toasts stay under the toolbar at desktop and mobile widths in light and dark themes", async ({ page }) => {
  await mock(page, async (path, request) => {
    if (request.method() === "POST") await new Promise(() => {});
  });
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  await page.locator(".cell-page-title-row").getByRole("button", { name: "Stop", exact: true }).click();
  await expect(toasts(page)).toHaveClass(/pending/);
  const backgrounds = new Set();
  for (const [width, height] of [[1440, 900], [390, 844]]) {
    await page.setViewportSize({ width, height });
    for (const theme of ["light", "dark"]) {
      await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
      const toolbar = await page.locator(".toolbar").boundingBox();
      await expect.poll(async () => (await toasts(page).boundingBox()).y).toBeGreaterThanOrEqual(toolbar.y + toolbar.height);
      const box = await toasts(page).boundingBox();
      if (width === 1440) {
        expect(box.width).toBeLessThanOrEqual(360);
        expect(Math.round(box.x + box.width)).toBe(width - 16);
      } else {
        expect(Math.round(box.x)).toBe(16);
        expect(Math.round(box.width)).toBe(width - 32);
      }
      backgrounds.add(await toasts(page).evaluate(el => getComputedStyle(el).backgroundColor));
    }
  }
  expect(backgrounds.size).toBe(2);
});

test("terminal unavailable and limit messages leave the cellTerminal title row height unchanged", async ({ page }) => {
  let state = "Ready";
  await mock(page, path => {
    if (path.endsWith("/containers")) return { json: [{ id: "shell", attachable: true }] };
    if (path === "/api/realms/dev/spaces/shared/stacks/services/cells/api") return { json: { ...doc(["dev", "shared", "services", "api"]), status: { state } } };
    if (path.endsWith("/cells") && path.includes("/dev/")) return { json: fixture(path).map(item => ({ ...item, status: { state } })) };
  });
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  const row = page.locator(".cell-page-title-row");
  const heights = async () => {
    const result = [];
    for (const [width, height] of [[1440, 900], [390, 844]]) {
      await page.setViewportSize({ width, height });
      await page.waitForTimeout(100);
      result.push(Math.round((await row.boundingBox()).height));
    }
    return result;
  };
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  const ready = await heights();
  state = "Stopped";
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.locator("#cell-terminal .terminal-log-status")).toContainText("Cell is Stopped.");
  expect(await heights()).toEqual(ready);
  await page.setViewportSize({ width: 1440, height: 900 });
  const centers = await page.locator(".cell-page-title-row > *:not([hidden])").evaluateAll(els => new Set(els.map(el => Math.round(el.getBoundingClientRect().top + el.getBoundingClientRect().height / 2))).size);
  expect(centers).toBe(1);
  await expect(row.getByText(/Terminal unavailable|Terminal limit|Loading containers/)).toHaveCount(0);
});

test("container cgroup section distinguishes Unlimited, Not available and Not set and refreshes live values", async ({ page }) => {
  let reads = 0;
  const cgroup = current => ({ json: {
    cgroupPath: "/kukeon/prod/shared/services/api/prod_shared_services_api_web", absolutePath: "/sys/fs/cgroup/kukeon/prod/shared/services/api/prod_shared_services_api_web",
    exists: true, ready: true, hostCgroup: false,
    declared: [{ field: "memoryLimitBytes", value: 536870912, source: "space-default" }, { field: "cpuShares", source: "unset" }, { field: "pidsLimit", value: 64, source: "container" }],
    live: {
      controllers: { state: "set", values: ["cpu", "memory", "pids"] }, subtreeControl: { state: "set" }, populated: { state: "set", raw: "1", value: 1 },
      memory: { max: { state: "max", raw: "max" }, high: { state: "max", raw: "max" }, swapMax: { state: "absent" }, current: { state: "set", raw: String(current), value: current }, peak: { state: "absent" }, oom: { state: "set", raw: "0", value: 0 }, oomKill: { state: "set", raw: "0", value: 0 } },
      cpu: { max: { state: "max", raw: "max 100000", periodMicros: 100000 }, weight: { state: "set", raw: "100", value: 100 }, usageUsec: { state: "set", raw: "2500000", value: 2500000 }, userUsec: { state: "absent" }, systemUsec: { state: "absent" } },
      pids: { max: { state: "set", raw: "64", value: 64 }, current: { state: "set", raw: "0", value: 0 } },
      io: { state: "absent" },
    },
  } });
  await mock(page, path => path.endsWith("/containers/web/cgroup") ? cgroup(reads++ === 0 ? 1048576 : 2097152) : undefined);
  await page.goto(`${server.url}#/resource/prod/shared/services/api/web`);
  const panel = page.getByRole("region", { name: "container cgroup" });
  await expand(page, "container cgroup");
  await expect(panel.getByText("/sys/fs/cgroup/kukeon/prod/shared/services/api/prod_shared_services_api_web")).toBeVisible();
  const declared = panel.locator("table.cgroup-declared");
  await expect(declared.locator("tr").filter({ hasText: "Memory limit" })).toContainText("512 MiB");
  await expect(declared.locator("tr").filter({ hasText: "Memory limit" })).toContainText("Inherited space default");
  await expect(declared.locator("tr").filter({ hasText: "Memory limit" })).toContainText("Unlimited");
  await expect(declared.locator("tr").filter({ hasText: "CPU shares" })).toContainText("Not set");
  await expect(declared.locator("tr").filter({ hasText: "CPU shares" })).toContainText("100 (default)");
  await expect(declared.locator("tr").filter({ hasText: "PIDs limit" })).toContainText("Container spec");
  const live = panel.locator(".cgroup-live");
  await expect(live.locator("dd").filter({ hasText: "Not available (controller not enabled)" }).first()).toBeVisible();
  await expect(live.getByText("1 MiB", { exact: false })).toBeVisible();
  await expect(live.getByText("peak")).toHaveCount(0);
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(live.getByText("2 MiB", { exact: false })).toBeVisible();
});

test("a missing cgroup reports Cgroup not present, and a failed read uses the error pattern", async ({ page }) => {
  await mock(page, path => path === "/api/realms/dev/cgroup" ? { json: { cgroupPath: "/kukeon/dev", absolutePath: "/sys/fs/cgroup/kukeon/dev", exists: false, ready: false, reason: "CgroupMissing", subtreeControllers: ["cpu", "memory"] } }
    : path === "/api/realms/dev/spaces/shared/cgroup" ? { status: 403, json: { error: { code: "permission_denied", message: "cgroup read denied" } } } : undefined);
  await page.goto(`${server.url}#/resource/dev`);
  const realm = page.getByRole("region", { name: "realm cgroup" });
  await expect(realm.getByText("Cgroup not present")).toBeVisible();
  await expect(realm.getByText("cpu memory", { exact: true })).toBeVisible();
  await expect(realm.locator(".cgroup-live")).toHaveCount(0);
  await page.goto(`${server.url}#/resource/dev/shared`);
  const space = page.getByRole("region", { name: "space cgroup" });
  await expect(space.getByRole("alert")).toContainText("Permission denied");
});

test("a daemon without the cgroup RPC reports Daemon outdated, not Daemon unavailable", async ({ page }) => {
  await mock(page, path => path === "/api/realms/dev/cgroup" ? { status: 501, json: { error: { code: "daemon_outdated", message: "kukeond does not support this request; upgrade kukeond to match the client: KukeonV1.GetCgroupDetail" } } } : undefined);
  await page.goto(`${server.url}#/resource/dev`);
  const alert = page.getByRole("region", { name: "realm cgroup" }).getByRole("alert");
  await expect(alert).toContainText("Daemon outdated");
  await expect(alert).toContainText("upgrade kukeond");
  await expect(alert).not.toContainText("Daemon unavailable");
});

test("cellRead shows the cell cgroup with nested runtime and delegated controllers", async ({ page }) => {
  await mock(page, path => path.endsWith("/cells/api/cgroup") ? { json: { cgroupPath: "/kukeon/dev/shared/services/api", absolutePath: "/sys/fs/cgroup/kukeon/dev/shared/services/api", exists: true, ready: true, nestedCgroupRuntime: true, subtreeControllers: ["cpu", "io", "memory", "pids"], live: { memory: { max: { state: "set", raw: "1073741824", value: 1073741824 } }, io: { state: "set", entries: [] } } } } : undefined);
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "cell cgroup" });
  await expand(page, "cell cgroup");
  await expect(panel.getByText("cpu io memory pids", { exact: true })).toBeVisible();
  await expect(panel.getByText("Enabled", { exact: true })).toBeVisible();
  await expect(panel.locator(".panel-body").getByText("1 GiB", { exact: false })).toBeVisible();
  await expect(panel.getByText("No device limits")).toBeVisible();
});

test("space network section shows the subnet and admit-all default egress, and refreshes runtime values", async ({ page }) => {
  let reads = 0;
  await mock(page, path => path === "/api/realms/dev/spaces/shared/network" ? { json: { space: {
    networkName: "dev-shared", bridgeName: "k-0123abcd", subnetCIDR: reads++ === 0 ? "10.88.1.0/24" : "10.88.2.0/24", subnetSource: "allocator", parentCIDR: "10.88.0.0/16",
    configPath: "/opt/kukeon/data/dev/shared/network.conflist", configPathSource: "default", cniNetworkExists: true,
    egress: { default: "allow", defaultSource: "default" },
  } } } : undefined);
  await page.goto(`${server.url}#/resource/dev/shared`);
  const panel = page.getByRole("region", { name: "space network" });
  await expect(panel.getByText("dev-shared", { exact: true })).toBeVisible();
  await expect(panel.getByText("k-0123abcd")).toBeVisible();
  await expect(panel.getByText("10.88.1.0/24")).toBeVisible();
  await expect(panel.getByText("/opt/kukeon/data/dev/shared/network.conflist (default)")).toBeVisible();
  await expect(panel.getByText("Allow (default, not set)")).toBeVisible();
  await expect(panel.getByText("No allow rules.")).toBeVisible();
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(panel.getByText("10.88.2.0/24")).toBeVisible();
});

test("space network section renders deny rules with Any port and an unallocated subnet", async ({ page }) => {
  await mock(page, path => path === "/api/realms/dev/spaces/shared/network" ? { json: { space: {
    networkName: "dev-shared", subnetSource: "unallocated", parentCIDR: "10.88.0.0/16", configPath: "/etc/cni/net.d/custom.conflist", configPathSource: "spec", cniNetworkExists: false,
    egress: { default: "deny", defaultSource: "spec", allow: [{ host: "example.com", ports: [443] }, { cidr: "10.0.0.0/8" }] },
  } } } : undefined);
  await page.goto(`${server.url}#/resource/dev/shared`);
  const panel = page.getByRole("region", { name: "space network" });
  await expect(panel.getByText("Not allocated (allocated from 10.88.0.0/16)")).toBeVisible();
  await expect(panel.locator("dd").filter({ hasText: /^Not allocated$/ })).toBeVisible();
  await expect(panel.getByText("No (conflist not present)")).toBeVisible();
  await expect(panel.getByText("Deny", { exact: true })).toBeVisible();
  const rules = panel.locator("table.network-rules");
  await expect(rules.locator("tr").filter({ hasText: "example.com" })).toContainText("443");
  await expect(rules.locator("tr").filter({ hasText: "10.0.0.0/8" })).toContainText("Any port");
});

test("cellRead network section shows Not running for a stopped cell and the per-container summary", async ({ page }) => {
  await mock(page, path => path.endsWith("/cells/api/network") ? { json: {
    space: { networkName: "dev-shared", subnetCIDR: "10.88.1.0/24", subnetSource: "allocator", configPath: "/c.conflist", configPathSource: "default", cniNetworkExists: true, egress: { default: "allow", defaultSource: "default" } },
    cell: { bridgeName: "k-0123abcd", hostNetwork: false, ipv4State: "not-running", rootContainer: "root", containers: [{ id: "root", root: true }, { id: "web" }] },
  } } : undefined);
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "cell network" });
  await expand(page, "cell network");
  await expect(panel.locator("summary")).toContainText("Bridged (space network) · Not running · 2 containers");
  await expect(panel.locator(".panel-body").getByText("Bridged (space network)")).toBeVisible();
  await expect(panel.getByText("Not running", { exact: true })).toBeVisible();
  const containers = panel.locator("table.network-containers");
  await expect(containers.locator("tr").filter({ hasText: "web" })).toContainText("No (default)");
  await expect(containers.locator("th")).toHaveText(["Container", "Host network", "Host PID"]);
});

test("container network section labels defaults and the inherited CNI path, with no declared-list rows", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers/web/network") ? { json: {
    space: { networkName: "prod-shared", subnetCIDR: "10.88.1.0/24", subnetSource: "allocator", configPath: "/s.conflist", configPathSource: "default", cniNetworkExists: true, egress: { default: "allow", defaultSource: "default" } },
    cell: { hostNetwork: true, ipv4State: "host-network", rootContainer: "root" },
    container: { id: "web", hostNetwork: true, hostPID: false, cniConfigPath: "/s.conflist", cniConfigPathSource: "space" },
  } } : undefined);
  await page.goto(`${server.url}#/resource/prod/shared/services/api/web`);
  const panel = page.getByRole("region", { name: "container network" });
  await expand(page, "container network");
  await expect(panel.locator("dt:text-is('Host network') + dd")).toHaveText("Yes");
  await expect(panel.locator("dt:text-is('Host PID') + dd")).toHaveText("No (default)");
  await expect(panel.getByText("Inherited from space (/s.conflist)")).toBeVisible();
  await expect(panel.getByText("Joins the network namespace of root container")).toBeVisible();
  await expect(panel.getByText("Host", { exact: true })).toBeVisible();
  await expect(panel.getByText("Host network (no CNI attachment)").first()).toBeVisible();
  for (const label of ["Ports", "Networks", "Network aliases"]) await expect(panel.locator(`dt:text-is('${label}')`)).toHaveCount(0);
});

test("a failed network read uses the resource error pattern", async ({ page }) => {
  await mock(page, path => path === "/api/realms/dev/spaces/shared/network" ? { status: 403, json: { error: { code: "permission_denied", message: "network read denied" } } } : undefined);
  await page.goto(`${server.url}#/resource/dev/shared`);
  await expect(page.getByRole("region", { name: "space network" }).getByRole("alert")).toContainText("Permission denied");
});

const inventoryImages = [
  { name: "docker.io/library/alpine:3.19", size: 3400000, createdAt: "2026-01-02T03:04:05Z", digest: "sha256:0123456789abcdef0123", mediaType: "application/vnd.oci.image.index.v1+json", labels: { "io.kukeon/built": "true", team: "platform" } },
  { name: "kukeon.internal/unsized:dev", size: null, createdAt: null, digest: "sha256:fedcba9876543210", mediaType: "", labels: {} },
];
function imageInventory(path) {
  const match = path.match(/^\/api\/realms\/([^/]+)\/image-inventory(?:\/(.+))?$/);
  if (!match) return undefined;
  const realm = decodeURIComponent(match[1]);
  const namespace = `${realm}.kukeon.io`;
  if (!match[2]) return { json: { realm, namespace, images: realm === "prod" ? [] : inventoryImages } };
  const image = inventoryImages.find(item => item.name === decodeURIComponent(match[2]));
  return image ? { json: { realm, namespace, image } } : { status: 404, json: { error: { code: "not_found", message: `image not found: ${decodeURIComponent(match[2])}` } } };
}

test("Administration Images lists a realm's inventory, switches realm scope and opens a deep-linked image", async ({ page }) => {
  const requested = [];
  await mock(page, path => { if (path.includes("/image-inventory")) requested.push(path); return imageInventory(path); });
  await page.goto(`${server.url}#/resources`);
  await nav(page).getByRole("link", { name: "Images" }).click();
  await expect(page).toHaveURL(/#\/images$/);
  const list = page.getByRole("region", { name: "Images list" });
  await expect(list.getByText("dev.kukeon.io")).toBeVisible();
  const sized = list.getByRole("row", { name: /alpine/ });
  await expect(sized).toContainText("3.2 MiB");
  await expect(sized).toContainText("2026-01-02T03:04:05Z");
  await expect(sized).toContainText("sha256:0123456789ab…");
  await expect(sized).toContainText("application/vnd.oci.image.index.v1+json");
  const unsized = list.getByRole("row", { name: /unsized/ });
  await expect(unsized).toContainText("Unavailable");
  await expect(unsized).not.toContainText("0 B");
  expect(requested).toContain("/api/realms/dev/image-inventory");

  await list.getByRole("combobox", { name: "Realm" }).selectOption("prod");
  await expect(page).toHaveURL(/#\/images\/prod$/);
  await expect(list.getByText("prod.kukeon.io")).toBeVisible();
  await expect(list.getByRole("heading", { name: "No images found" })).toBeVisible();

  await page.goto(`${server.url}#/images/dev`);
  await list.getByRole("link", { name: "docker.io/library/alpine:3.19" }).click();
  await expect(page).toHaveURL(/#\/images\/dev\/docker\.io%2Flibrary%2Falpine%3A3\.19$/);
  const detail = page.getByRole("region", { name: "Image details" });
  await expect(detail).toContainText("sha256:0123456789abcdef0123");
  await expect(detail).toContainText("io.kukeon/built: true");
  await expect(detail).toContainText("team: platform");
  await expect(detail).toContainText("dev.kukeon.io");

  await page.reload();
  await expect(page.getByRole("region", { name: "Image details" })).toContainText("3.2 MiB");
  await page.goto(`${server.url}#/images/dev/${encodeURIComponent("kukeon.internal/unsized:dev")}`);
  const unsizedDetail = page.getByRole("region", { name: "Image details" });
  await expect(unsizedDetail.getByText("Unavailable")).toHaveCount(3);
});

test("Images view distinguishes loading, missing image, permission, outdated daemon and failed refresh", async ({ page }) => {
  let mode = "ok";
  let release;
  await mock(page, async path => {
    if (!path.includes("/image-inventory")) return undefined;
    if (mode === "slow") await new Promise(resolve => { release = resolve; });
    if (mode === "permission") return { status: 403, json: { error: { code: "permission_denied", message: "dial kukeond: permission denied" } } };
    if (mode === "outdated") return { status: 501, json: { error: { code: "daemon_outdated", message: "upgrade kukeond to match the client: KukeonV1.ListImages" } } };
    if (mode === "fail") return failure;
    return imageInventory(path);
  });
  mode = "slow";
  await page.goto(`${server.url}#/images/dev`);
  const list = page.getByRole("region", { name: "Images list" });
  await expect(list.getByRole("status")).toHaveText("Loading images…");
  release();
  await expect(list.getByRole("row", { name: /alpine/ })).toBeVisible();

  mode = "fail";
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("Stale image list — refresh failed.")).toBeVisible();
  await expect(content(page).getByRole("alert")).toContainText("Daemon unavailable");
  await expect(list.getByRole("row", { name: /alpine/ })).toBeVisible();
  mode = "ok";
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("Stale image list — refresh failed.")).toHaveCount(0);

  mode = "permission";
  await page.goto(`${server.url}#/images/prod`);
  await expect(content(page).getByRole("alert")).toContainText("Permission denied");
  await expect(list.getByRole("heading", { name: "Images could not be loaded" })).toBeVisible();

  mode = "outdated";
  await page.goto(`${server.url}#/images/dev`);
  await expect(content(page).getByRole("alert")).toContainText("Daemon outdated");

  mode = "ok";
  await page.goto(`${server.url}#/images/dev/${encodeURIComponent("missing:1")}`);
  await expect(content(page).getByRole("alert")).toContainText("Image no longer exists");
});

test("realm registry credentials list server and username on the Images page, keep the token write-only and refresh the list", async ({ page }) => {
  const token = "s3cr3t-registry-token";
  let credentials = [{ serverAddress: "ghcr.io", username: "bot", password: "" }, { username: "fallback", password: "" }];
  const posts = [];
  let mode = "ok";
  await mock(page, (path, request) => {
    if (path === "/api/realms/dev/registry-credentials") {
      posts.push({ url: request.url(), body: request.postDataJSON(), headers: request.headers() });
      if (mode === "invalid") return { status: 400, json: { error: { code: "invalid_request", message: "configuration error: server must be a registry host such as ghcr.io" } } };
      if (mode === "permission") return { status: 403, json: { error: { code: "permission_denied", message: "dial kukeond: permission denied" } } };
      const { server, username } = request.postDataJSON();
      const entry = { ...(server ? { serverAddress: server } : {}), username, password: "" };
      const index = credentials.findIndex(cred => (cred.serverAddress ?? "") === server);
      credentials = index >= 0 ? credentials.map((cred, i) => i === index ? entry : cred) : [...credentials, entry];
      return { json: { realm: "dev", server, username, action: "updated" } };
    }
    if (path === "/api/realms/dev") return { json: { ...doc(["dev"]), spec: { namespace: "dev.kukeon.io", registryCredentials: credentials } } };
    return undefined;
  });
  // #1630: the Realm page counts the credentials and links to Images, where the form lives.
  await page.goto(`${server.url}#/resource/dev`);
  const realmImages = page.getByRole("region", { name: "Realm images" });
  await expect(realmImages).toContainText("Registry credentials: 2 configured");
  await expect(page.getByRole("form", { name: "Add or replace registry credential" })).toHaveCount(0);
  await realmImages.getByRole("link", { name: "manage in Images" }).click();
  await expect(page).toHaveURL(/#\/images\/dev$/);
  const panel = page.getByRole("region", { name: "Registry credentials" });
  await expect(panel.getByRole("row", { name: /ghcr\.io/ })).toContainText("bot");
  await expect(panel.getByRole("row", { name: /fallback/ })).toContainText("Default (registry from image reference)");

  const form = panel.getByRole("form", { name: "Add or replace registry credential" });
  await panel.getByRole("button", { name: "Replace credential for ghcr.io" }).click();
  await expect(form.getByLabel("Server")).toHaveValue("ghcr.io");
  await form.getByLabel("Username").fill("robot");
  await expect(form.getByLabel("Token")).toHaveAttribute("type", "password");
  await form.getByLabel("Token").fill(token);
  await form.getByRole("button", { name: "Replace credential" }).click();
  await expect(toasts(page)).toContainText(["Saved credential for ghcr.io (user robot)"]);
  await expect(panel.getByText("Saved credential", { exact: false })).toHaveCount(0);
  await expect(panel.getByRole("row", { name: /ghcr\.io/ })).toContainText("robot");
  await expect(panel.getByRole("row")).toHaveCount(3);
  expect(posts).toHaveLength(1);
  expect(posts[0].body).toEqual({ server: "ghcr.io", username: "robot", password: token });
  expect(posts[0].url).not.toContain(token);
  expect(posts[0].headers["content-type"]).toBe("application/json");
  await expect(form.getByLabel("Token")).toHaveValue("");
  expect(await page.content()).not.toContain(token);
  expect(page.url()).not.toContain(token);
  expect(await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))).not.toContain(token);

  await form.getByLabel("Username").fill("default-user");
  await form.getByLabel("Token").fill(token);
  await form.getByRole("button", { name: "Replace credential" }).click();
  await expect(panel.getByRole("row", { name: /Default/ })).toContainText("default-user");
  expect(posts[1].body.server).toBe("");

  mode = "invalid";
  await form.getByLabel("Server").fill("ghcr.io/org");
  await form.getByLabel("Username").fill("u");
  await form.getByLabel("Token").fill(token);
  await form.getByRole("button", { name: "Add credential" }).click();
  await expect(panel.getByRole("alert")).toContainText("server must be a registry host");
  await expect(form.getByLabel("Token")).toHaveValue("");
  mode = "permission";
  await form.getByLabel("Token").fill(token);
  await form.getByRole("button", { name: "Add credential" }).click();
  await expect(panel.getByRole("alert")).toContainText("Permission denied");
  expect(await page.content()).not.toContain(token);

  // Switching the Realm switches the image list and the credentials together.
  await page.getByRole("combobox", { name: "Realm", exact: true }).selectOption("prod");
  await expect(page).toHaveURL(/#\/images\/prod$/);
  await expect(panel).toContainText("No registry credentials are configured for this realm.");
  await expect(panel.getByRole("row", { name: /ghcr\.io/ })).toHaveCount(0);

  await page.goto(`${server.url}#/resource/dev?document=1`);
  await expect(page.getByLabel("Complete realm JSON")).toContainText("ghcr.io");
  await expect(page.getByLabel("Complete realm JSON")).not.toContainText(token);
});

test("realm scope, breadcrumbs and narrow lists stay within the page", async ({ page }) => {
  await mock(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${server.url}#/realms`);
  await expect(page.getByRole("row")).toHaveCount(3);
  // Realms have no parent scope, so the list omits the Scope column (#1567).
  await expect(page.locator(".scope-cell")).toHaveCount(0);
  await page.goto(`${server.url}#/cells`);
  await expect(page.getByRole("row")).toHaveCount(3);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${server.url}#/resources`);
  await expect(page.locator(".breadcrumbs > *")).toHaveCount(1);
  await page.goto(`${server.url}#/images/dev`);
  expect(await page.locator(".breadcrumbs > span").evaluate(el => getComputedStyle(el, "::before").content)).toBe('"/"');
});

test("operator density: compact rows and header, quiet row actions, icon terminal toolbar and shared status tokens", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] }
    : /\/stacks\/[^/]+\/cells$/.test(path) ? { json: fixture(path, 12) } : undefined);
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${server.url}#/cells`);
  const rows = page.locator("tbody tr");
  await expect(rows).toHaveCount(24);
  for (const box of await rows.evaluateAll(els => els.map(el => el.getBoundingClientRect().height))) expect(box).toBeLessThanOrEqual(44);
  expect(await rows.evaluateAll(els => els.filter(el => el.getBoundingClientRect().bottom <= innerHeight).length)).toBeGreaterThanOrEqual(15);
  expect((await page.locator(".page-heading").boundingBox()).height).toBeLessThanOrEqual(100);
  await expect(page.locator(".eyebrow")).toHaveCount(0);
  // Row quick actions are icon-only (#1494): no table row carries a text button.
  expect(await page.locator("tbody button").evaluateAll(els => els.filter(el => el.textContent.trim()).length)).toBe(0);
  const quick = rows.first().locator(".row-actions");
  for (const name of ["Kill", "Delete", "Purge"]) await expect(quick.getByRole("button", { name: new RegExp(`^${name} cell `) })).toHaveClass(/is-danger/);
  await page.evaluate(() => { document.documentElement.dataset.theme = "dark"; });
  expect(await quick.getByRole("button", { name: /^Delete cell / }).evaluate(el => getComputedStyle(el).color)).toBe(await page.evaluate(() => { const probe = document.createElement("span"); probe.style.color = "var(--status-error)"; document.body.append(probe); const color = getComputedStyle(probe).color; probe.remove(); return color; }));
  for (const box of await rows.evaluateAll(els => els.map(el => el.getBoundingClientRect().height))) expect(box).toBeLessThanOrEqual(44);
  await page.evaluate(() => { document.documentElement.dataset.theme = "light"; });
  await expect(page.locator("tbody").getByText("View document")).toHaveCount(0);
  expect(await page.locator(".scope-cell").first().evaluate(el => getComputedStyle(el).fontFamily)).toContain("monospace");
  const action = rows.first().getByRole("link", { name: /^View document for / });
  expect(await action.evaluate(el => getComputedStyle(el).opacity)).toBe("0");
  await action.focus();
  await expect.poll(() => action.evaluate(el => getComputedStyle(el).opacity)).toBe("1");
  await page.keyboard.press("Enter");
  await expect(page.locator("pre")).toContainText('"futureField"');

  await page.goto(`${server.url}#/workspace/dev/shared/services/api-00`);
  const row = page.locator(".cell-page-title-row");
  await expect(row.locator(".terminal-connection-state")).toHaveText("Attached");
  for (const name of ["Detach", "Expand terminal", "Stop", "Restart"]) {
    const button = row.getByRole("button", { name, exact: true });
    await expect(button).toHaveAttribute("data-tooltip", name);
    await expect(button).toHaveText("");
  }
  const [top] = await page.locator(".cell-page-title-row > *:not([hidden])").evaluateAll(els => [new Set(els.map(el => Math.round(el.getBoundingClientRect().top + el.getBoundingClientRect().height / 2))).size]);
  expect(top).toBe(1);
  await row.getByRole("button", { name: "Detach", exact: true }).click();
  await expect(row.getByRole("button", { name: "Attach", exact: true })).toBeVisible();
  await expect(row.getByRole("button", { name: "Detach", exact: true })).toHaveCount(0);
  await expect(row.getByRole("button", { name: "Stop", exact: true })).toHaveClass(/danger-action/);
  await page.getByText("Advanced actions", { exact: true }).click();
  for (const name of ["Kill cell…", "Delete cell…", "Purge cell…"]) await expect(page.getByRole("button", { name })).toHaveClass(/danger-action/);

  const tokens = () => page.evaluate(() => ["ok", "stopped", "warning", "error", "pending"].map(tone => getComputedStyle(document.documentElement).getPropertyValue(`--status-${tone}`).trim()));
  const light = await tokens();
  expect(light.every(Boolean)).toBe(true);
  expect(await page.locator(".state-badge.tone-ok").first().evaluate(el => getComputedStyle(el).color)).toBe(await page.evaluate(() => { const probe = document.createElement("span"); probe.style.color = "var(--status-ok)"; document.body.append(probe); const color = getComputedStyle(probe).color; probe.remove(); return color; }));
  await page.evaluate(() => { document.documentElement.dataset.theme = "dark"; });
  const dark = await tokens();
  expect(dark.every(Boolean)).toBe(true);
  expect(dark).not.toEqual(light);
});

test("Overview shows an explicit all-Ready state when no cell needs attention", async ({ page }) => {
  await mock(page, path => /\/cells$/.test(path) ? { json: fixture(path).map(cell => ({ ...cell, status: { state: "Ready" } })) } : undefined);
  await page.goto(`${server.url}#/resources`);
  const attention = page.getByRole("region", { name: "Cells needing attention" });
  await expect(attention).toContainText("All cells Ready");
  await expect(attention.getByRole("link")).toHaveCount(0);
  await expect(attention.locator(".badge")).toHaveText("0 of 2 cells");
  await expect(page.locator(".drift-badge")).toHaveCount(0);
});

// Issue #1568, item B: a Ready cell that is out of sync with its Config no longer looks healthy.
test("config drift shows in Needs attention, the Cells table and the sidebar, and the Overview leads with cells", async ({ page }) => {
  const statuses = { "dev/api-00": { state: "Ready", outOfSync: true, outOfSyncReason: "image changed" }, "prod/api-00": { state: "Ready", outOfSyncError: "config demo not found" } };
  await mock(page, path => /\/cells$/.test(path) ? { json: fixture(path, 2).map(cell => ({ ...cell, status: statuses[`${path.split("/")[3]}/${cell.metadata.name}`] ?? { state: "Ready" } })) } : undefined);
  await page.goto(`${server.url}#/resources`);
  const attention = page.getByRole("region", { name: "Cells needing attention" });
  await expect(attention.locator(".panel-heading .badge")).toHaveText("2 of 4 cells");
  const items = attention.getByRole("listitem");
  await expect(items).toHaveCount(2);
  const drifted = items.filter({ hasText: "dev / shared / services" });
  await expect(drifted.getByRole("link", { name: "api-00", exact: true })).toHaveAttribute("href", "#/resource/dev/shared/services/api-00");
  await expect(drifted.locator(".state-badge")).toHaveText(["Ready", /^Drift\b/]);
  await expect(drifted.locator(".attention-reason")).toHaveText("Configuration out of sync: image changed");
  // The badge is a text label whose accessible text and tooltip carry the reason.
  await expect(drifted.locator(".drift-badge")).toHaveAttribute("title", "Configuration out of sync: image changed");
  await expect(drifted.locator(".drift-badge")).toContainText("Configuration out of sync: image changed");
  const failed = items.filter({ hasText: "prod / shared / services" });
  await expect(failed.locator(".state-badge")).toHaveText(["Ready", /^Drift unknown/]);
  await expect(failed.locator(".attention-reason")).toHaveText("Configuration comparison failed: config demo not found");
  // The attention panel and the cell summary come before the realm, space and stack cards.
  await expect(page.locator(".summary-card > span")).toHaveText(["Cells", "Realms", "Spaces", "Stacks", "Health"]);
  const tops = await page.evaluate(() => [".attention-panel", ".summary-card-cells", ".summary-card:not(.summary-card-cells)"].map(selector => document.querySelector(selector).getBoundingClientRect().top));
  expect(tops[0]).toBeLessThan(tops[1]);
  expect(tops[1]).toBeLessThan(tops[2]);
  await expect(page.locator(".summary-card-cells").getByRole("list", { name: "Cell states" })).toContainText("Ready");

  await page.goto(`${server.url}#/cells`);
  const rows = content(page).locator("tbody tr");
  await expect(rows).toHaveCount(4);
  await expect(rows.filter({ hasText: "dev / shared / services" }).filter({ hasText: "api-00" }).locator(".drift-badge")).toContainText("Drift");
  await expect(rows.filter({ hasText: "prod / shared / services" }).filter({ hasText: "api-00" }).locator(".drift-badge")).toContainText("Drift unknown");
  await expect(content(page).locator(".drift-badge")).toHaveCount(2);

  // The Workbench sidebar and grid carry the same badges.
  await page.goto(server.url);
  await expect(page.getByRole("region", { name: "Cell terminals" }).locator(".drift-badge")).toHaveCount(2);
  const sidebar = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(sidebar.getByRole("link", { name: "Open dev / shared / services / api-00" }).locator(".drift-badge")).toContainText("Drift");
  await expect(sidebar.getByRole("link", { name: "Open prod / shared / services / api-00" }).locator(".drift-badge")).toContainText("Drift unknown");
  await expect(sidebar.getByRole("link", { name: "Open dev / shared / services / api-01" }).locator(".drift-badge")).toHaveCount(0);
  await expect(sidebar.locator(".drift-badge")).toHaveCount(2);
  // The sidebar row keeps the state badge, the drift badge and the name apart at the narrowest sidebar.
  const overlap = await sidebar.getByRole("link", { name: "Open dev / shared / services / api-00" }).evaluate(link => {
    const [state, drift] = [...link.querySelectorAll(".state-badge")].map(el => el.getBoundingClientRect());
    return !(state.right <= drift.left || drift.right <= state.left || state.bottom <= drift.top || drift.bottom <= state.top);
  });
  expect(overlap).toBe(false);
});

test("Group by leaves out controller-managed label keys unless one is the stored selection", async ({ page }) => {
  await mock(page, path => /\/cells$/.test(path) ? { json: fixture(path).map(cell => ({ ...cell, metadata: { ...cell.metadata, labels: { team: "platform", "kukeon.io/config": "app", "cell.kukeon.io": cell.metadata.name, "realm.kukeon.io/name": "x" } } })) } : undefined);
  await defaultStyle(page);
  await page.goto(server.url);
  const groupBy = page.getByRole("region", { name: "All cells" }).getByLabel("Group by");
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link")).toHaveCount(2);
  await expect(groupBy.locator("option")).toHaveText(["None", "kukeon.io/config", "team"]);
  await page.evaluate(() => localStorage.setItem("kukeon-cell-grouping", "cell.kukeon.io"));
  await page.reload();
  await expect(groupBy).toHaveValue("cell.kukeon.io");
  await expect(groupBy.locator("option")).toHaveText(["None", "cell.kukeon.io", "kukeon.io/config", "team"]);
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("region", { name: "api group" })).toBeVisible();
  // Default Group by in Profile lists None and the same keys, and always kukeon.io/config and its value.
  await page.goto(`${server.url}#/settings/profile`);
  const defaultGroupBy = settingsPage(page).getByLabel("Default Group by");
  await expect(defaultGroupBy).toHaveValue("");
  await expect(defaultGroupBy.locator("option")).toHaveText(["None", "kukeon.io/config", "team"]);
  await page.evaluate(() => localStorage.setItem("kukeon-default-group-by", "cell.kukeon.io"));
  await page.reload();
  await expect(defaultGroupBy).toHaveValue("cell.kukeon.io");
  await expect(defaultGroupBy.locator("option")).toHaveText(["None", "cell.kukeon.io", "kukeon.io/config", "team"]);
});

// Administration → Daemon (issue #1386, steps 6 and 8).
const healthBody = (overrides = {}) => ({ status: "reachable", endpoint: "unix:///run/kukeon/kukeond.sock", daemonVersion: "v1.2.3", serverVersion: "v1.2.3", checkedAt: "2026-11-15T10:00:00Z", latencyMs: 3, ...overrides });
async function mockDaemon(page, { health = () => ({ json: healthBody() }), logs = () => ({ json: { source: "/opt/kukeon/log", data: "", start: 0, next: 0, size: 0 } }) } = {}) {
  await page.route("**/api/daemon/**", async route => {
    const url = new URL(route.request().url());
    await route.fulfill(await (url.pathname === "/api/daemon/health" ? health(url) : logs(url)));
  });
}

test("Administration Daemon shows connectivity, daemon version, socket and check time separately from workloads", async ({ page }) => {
  let checks = 0;
  await mock(page);
  await mockDaemon(page, { health: () => { checks++; return { json: healthBody({ daemonVersion: "v1.2.4" }) }; } });
  await page.goto(`${server.url}#/resources`);
  await expect(page.getByLabel("Daemon connection").getByRole("link", { name: "Daemon details" })).toHaveAttribute("href", "#/daemon");
  await nav(page).getByRole("link", { name: "Daemon" }).click();
  await expect(page).toHaveURL(/#\/daemon$/);
  await expect(page.locator(".page-heading h1")).toHaveText("Daemon");
  const panel = page.getByRole("region", { name: "Daemon connectivity" });
  await expect(panel.locator(".state-badge")).toHaveText("Reachable");
  await expect(panel).toContainText("v1.2.4");
  await expect(panel).toContainText("Differs from kuke serve");
  await expect(panel).toContainText("unix:///run/kukeon/kukeond.sock");
  await expect(panel.locator("time").first()).toHaveAttribute("dateTime", "2026-11-15T10:00:00Z");
  await expect(panel).toContainText("Inventory refreshed");
  await expect(page.getByRole("region", { name: "Health scope" })).toContainText("does not report the health of cells");
  const before = checks;
  await panel.getByRole("button", { name: "Check again" }).click();
  await expect.poll(() => checks).toBeGreaterThan(before);
  await page.reload();
  await expect(page.getByRole("region", { name: "Daemon connectivity" }).locator(".state-badge")).toHaveText("Reachable");
});

test("Daemon health distinguishes permission-denied, unavailable, outdated and failed checks", async ({ page }) => {
  let mode = "permission_denied";
  await mock(page);
  await mockDaemon(page, { health: () => ({ json: healthBody({ status: mode, daemonVersion: undefined, error: { code: mode, message: `check: ${mode}` } }) }) });
  await page.goto(`${server.url}#/daemon`);
  const panel = page.getByRole("region", { name: "Daemon connectivity" });
  for (const [status, label, hint] of [["permission_denied", "Permission denied", "kukeon group"], ["unavailable", "Unavailable", "kuke daemon start"], ["daemon_outdated", "Daemon outdated", "Upgrade kukeond"], ["failed", "Check failed", "daemon logs"]]) {
    mode = status;
    await panel.getByRole("button", { name: "Check again" }).click();
    await expect(panel.locator(".state-badge")).toHaveText(label);
    await expect(panel.getByRole("alert")).toContainText(`check: ${status}`);
    await expect(panel.getByRole("alert")).toContainText(hint);
    await expect(panel).toContainText("Unknown");
  }
});

test("Daemon health against the real server reports an unavailable socket", async ({ page }) => {
  // The real server checks the session against the web-ui-admin secret through kukeond (#1652), so with the
  // daemon socket missing it refuses the request with 503 unavailable, naming the socket, never a success.
  await page.goto(`${server.url}#/daemon`);
  const panel = page.getByRole("region", { name: "Daemon connectivity" });
  await expect(panel.locator(".state-badge")).toHaveText("Unavailable");
  await expect(content(page).getByRole("alert").first()).toContainText("Daemon unavailable");
  await expect(content(page).getByRole("alert").first()).toContainText("missing.sock");
  await expect(page.getByLabel("Daemon connection").getByRole("alert")).toContainText("Daemon unavailable");
});

test("Daemon logs read the latest bounded output, follow and pause explicitly, and copy", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const requests = [];
  let appended = "";
  await mock(page);
  await mockDaemon(page, { logs: url => {
    const offset = Number(url.searchParams.get("offset"));
    requests.push(offset);
    if (offset < 0) return { json: { source: "/opt/kukeon/kuke-system/kukeon/kukeon/kukeond/kukeond/log", data: "time=t1 level=INFO msg=started\nplain line\n", start: 100, next: 140, size: 140, truncated: true } };
    const data = offset === 140 ? appended : "";
    return { json: { source: "/opt/kukeon/log", data, start: offset, next: offset + data.length, size: offset + data.length } };
  } });
  await page.goto(`${server.url}#/resources`);
  await nav(page).getByRole("link", { name: "Daemon" }).click();
  await page.getByRole("navigation", { name: "Daemon sections" }).getByRole("link", { name: "Logs" }).click();
  await expect(page).toHaveURL(/#\/daemon\/logs$/);
  const logs = page.getByRole("region", { name: "Daemon logs" });
  const output = logs.getByLabel("kukeond log output");
  await expect(output).toHaveText("time=t1 level=INFO msg=started\nplain line");
  await expect(logs).toContainText("/opt/kukeon/kuke-system/kukeon/kukeon/kukeond/kukeond/log");
  await expect(logs).toContainText("Earlier output is not shown.");
  await expect(logs).toContainText("at most 2,000 lines");
  // Paused by default, like `kuke daemon logs` without -f: no polling.
  await expect(logs.locator(".badge")).toHaveText("Paused");
  await page.waitForTimeout(2500);
  expect(requests).toEqual([-1]);

  appended = "followed line\n";
  await logs.getByRole("button", { name: "Follow" }).click();
  await expect(logs.getByRole("button", { name: "Pause" })).toHaveAttribute("aria-pressed", "true");
  await expect(output).toContainText("followed line");
  expect(requests).toContain(140);
  await logs.getByRole("button", { name: "Pause" }).click();
  await expect(logs.locator(".badge")).toHaveText("Paused");
  const paused = requests.length;
  await page.waitForTimeout(2500);
  expect(requests.length).toBe(paused);

  await logs.getByRole("button", { name: "Copy" }).click();
  await expect(logs.getByRole("status").filter({ hasText: "Copied 3 lines." })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("time=t1 level=INFO msg=started\nplain line\nfollowed line");

  // Reload latest replaces the retained output with a fresh tail read.
  await logs.getByRole("button", { name: "Reload latest" }).click();
  await expect(output).toHaveText("time=t1 level=INFO msg=started\nplain line");
});

test("Daemon logs drop a follow read that is still in flight when Reload latest replaces the buffer", async ({ page }) => {
  await mock(page);
  let tails = 0;
  let release;
  const held = new Promise(resolve => { release = resolve; });
  const forward = [];
  await mockDaemon(page, { logs: async url => {
    const offset = Number(url.searchParams.get("offset"));
    if (offset < 0) {
      tails++;
      return { json: tails === 1 ? { source: "/log", data: "a\n", start: 0, next: 2, size: 2 } : { source: "/log", data: "a\nb\n", start: 0, next: 4, size: 4 } };
    }
    forward.push(offset);
    if (offset === 2) { await held; return { json: { source: "/log", data: "b\n", start: 2, next: 4, size: 4 } }; }
    return { json: { source: "/log", data: "", start: offset, next: offset, size: 4 } };
  } });
  await page.goto(`${server.url}#/daemon/logs`);
  const logs = page.getByRole("region", { name: "Daemon logs" });
  const output = logs.getByLabel("kukeond log output");
  await expect(output).toHaveText("a");
  await logs.getByRole("button", { name: "Follow" }).click();
  await expect.poll(() => forward).toContain(2);
  await logs.getByRole("button", { name: "Reload latest" }).click();
  await expect(output).toHaveText("a\nb");
  release();
  // The stale forward chunk is dropped and the next tick resumes from the reload's cursor.
  await expect.poll(() => forward.filter(offset => offset === 4).length, { timeout: 6000 }).toBeGreaterThan(0);
  await expect(output).toHaveText("a\nb");
  expect(forward.filter(offset => offset === 2)).toHaveLength(1);
});

test("Daemon logs follow reports a rotated source and drains bounded chunks", async ({ page }) => {
  await mock(page);
  let phase = 0;
  await mockDaemon(page, { logs: url => {
    const offset = Number(url.searchParams.get("offset"));
    if (offset < 0) return { json: { source: "/log", data: "old\n", start: 0, next: 4, size: 4 } };
    phase++;
    if (phase === 1) return { json: { source: "/log", data: "new 1\n", start: 0, next: 6, size: 12, reset: true, more: true } };
    if (phase === 2) return { json: { source: "/log", data: "new 2\n", start: 6, next: 12, size: 12 } };
    return { json: { source: "/log", data: "", start: offset, next: offset, size: 12 } };
  } });
  await page.goto(`${server.url}#/daemon/logs`);
  const logs = page.getByRole("region", { name: "Daemon logs" });
  await expect(logs.getByLabel("kukeond log output")).toHaveText("old");
  await logs.getByRole("button", { name: "Follow" }).click();
  await expect(logs.getByLabel("kukeond log output")).toHaveText("new 1\nnew 2");
  await expect(logs).toContainText("truncated or replaced");
});

test("Daemon logs surface unavailable sources, permission failures and lost connections", async ({ page }) => {
  let mode = "unavailable_source";
  await mock(page);
  await mockDaemon(page, { logs: url => {
    if (mode === "unavailable_source") return { status: 404, json: { error: { code: "log_unavailable", message: "kukeond log source unavailable: no log file at /opt/kukeon/log" } } };
    if (mode === "permission") return { status: 403, json: { error: { code: "permission_denied", message: "dial kukeond: permission denied" } } };
    if (mode === "outdated") return { status: 501, json: { error: { code: "daemon_outdated", message: "upgrade kukeond: KukeonV1.ReadDaemonLogs" } } };
    if (mode === "down") return failure;
    const offset = Number(url.searchParams.get("offset"));
    return { json: { source: "/log", data: offset < 0 ? "line\n" : "", start: 0, next: 5, size: 5 } };
  } });
  await page.goto(`${server.url}#/daemon/logs`);
  const logs = page.getByRole("region", { name: "Daemon logs" });
  await expect(logs.getByRole("alert")).toContainText("Daemon logs unavailable");
  await expect(logs.getByRole("alert")).toContainText("no log file at /opt/kukeon/log");
  await expect(logs.getByRole("heading", { name: "No log output yet" })).toHaveCount(0);

  mode = "permission";
  await logs.getByRole("button", { name: "Reload latest" }).click();
  await expect(logs.getByRole("alert")).toContainText("Permission denied");

  mode = "outdated";
  await logs.getByRole("button", { name: "Reload latest" }).click();
  await expect(logs.getByRole("alert")).toContainText("Daemon outdated");

  mode = "ok";
  await logs.getByRole("button", { name: "Reload latest" }).click();
  await expect(logs.getByLabel("kukeond log output")).toHaveText("line");
  await expect(logs.getByRole("alert")).toHaveCount(0);

  mode = "down";
  await logs.getByRole("button", { name: "Follow" }).click();
  await expect(logs.getByRole("alert")).toContainText("Daemon unavailable");
  await expect(logs).toContainText("Following is retrying");
  await expect(logs.getByLabel("kukeond log output")).toHaveText("line");
  mode = "ok";
  await expect(logs.getByRole("alert")).toHaveCount(0, { timeout: 5000 });
});

test("an empty daemon log is distinct from a failed read", async ({ page }) => {
  await mock(page);
  await mockDaemon(page);
  await page.goto(`${server.url}#/daemon/logs`);
  const logs = page.getByRole("region", { name: "Daemon logs" });
  await expect(logs.getByRole("heading", { name: "No log output yet" })).toBeVisible();
  await expect(logs.getByRole("alert")).toHaveCount(0);
  await expect(logs.getByRole("button", { name: "Copy" })).toBeDisabled();
});

test("sidebar collapses to a persisted icon rail with accessible cell dots", async ({ page }) => {
  await mock(page);
  await defaultStyle(page);
  await page.goto(server.url);
  const sidebar = page.getByRole("complementary", { name: "Cell sidebar" });
  await expect(sidebar.getByRole("navigation", { name: "Cell navigation" })).toBeVisible();
  await sidebar.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(sidebar.getByRole("navigation", { name: "Cell navigation" })).toBeHidden();
  const rail = sidebar.getByRole("navigation", { name: "Cell shortcuts" });
  const dot = rail.getByRole("link", { name: "Open dev / shared / services / api (Ready)" });
  await expect(dot).toHaveAttribute("title", "Open dev / shared / services / api (Ready)");
  await expect(rail.getByRole("link", { name: "Open prod / shared / services / api (Running)" })).toBeVisible();
  expect((await sidebar.boundingBox()).width).toBeLessThan(80);
  await expect(sidebar.getByRole("separator", { name: "Resize sidebar" })).toHaveCount(0);
  await expect(sidebar.getByRole("link", { name: "kukeon home" })).toHaveAttribute("href", "#/");

  // The Manage rail keeps the brand, expand, majordomo and connection controls and no cell dots (#1635).
  await page.goto(`${server.url}#/resources`);
  const manageRail = page.getByRole("complementary", { name: "Manage sidebar" });
  await expect(manageRail.getByRole("button", { name: "Expand sidebar" })).toBeVisible();
  await expect(manageRail.getByRole("link", { name: "kukeon home" })).toHaveAttribute("href", "#/");
  await expect(manageRail.locator(".rail-connection-dot")).toBeVisible();
  await expect(manageRail.getByRole("button", { name: /^Majordomo \(/ })).toBeVisible();
  await expect(manageRail.getByRole("navigation", { name: "Cell shortcuts" })).toHaveCount(0);
  await expect(manageRail.getByRole("navigation", { name: "Manage navigation" })).toBeHidden();
  expect((await manageRail.boundingBox()).width).toBeLessThan(80);
  await page.goto(server.url);

  await page.reload();
  await expect(sidebar.getByRole("button", { name: "Expand sidebar" })).toBeVisible();
  await expect(sidebar.getByRole("navigation", { name: "Cell navigation" })).toBeHidden();
  await dot.click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  await expect(dot).toHaveAttribute("aria-current", "page");

  await sidebar.getByRole("button", { name: "Expand sidebar" }).click();
  await expect(sidebar.getByRole("navigation", { name: "Cell navigation" })).toBeVisible();
  await page.reload();
  await expect(sidebar.getByRole("button", { name: "Collapse sidebar" })).toBeVisible();
  await expect(rail).toHaveCount(0);
});

test("sidebar width is pointer and keyboard resizable within bounds and persists", async ({ page }) => {
  await mock(page);
  await page.goto(server.url);
  const sidebar = page.getByRole("complementary", { name: "Cell sidebar" });
  const handle = page.getByRole("separator", { name: "Resize sidebar" });
  await expect(handle).toHaveAttribute("aria-valuenow", "264");
  await expect(handle).toHaveAttribute("aria-valuemin", "208");
  await expect(handle).toHaveAttribute("aria-valuemax", "480");
  await handle.focus();
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await expect(handle).toHaveAttribute("aria-valuenow", "296");
  expect(Math.round((await sidebar.boundingBox()).width)).toBe(296);
  await page.keyboard.press("Home");
  await expect(handle).toHaveAttribute("aria-valuenow", "208");
  await page.keyboard.press("ArrowLeft");
  await expect(handle).toHaveAttribute("aria-valuenow", "208");

  const box = await handle.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + 200);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 1000, box.y + 200, { steps: 5 });
  await page.mouse.up();
  await expect(handle).toHaveAttribute("aria-valuenow", "480");
  const after = await handle.boundingBox();
  await page.mouse.move(after.x + after.width / 2, after.y + 200);
  await page.mouse.down();
  await page.mouse.move(after.x + after.width / 2 - 150, after.y + 200, { steps: 5 });
  await page.mouse.up();
  await expect(handle).toHaveAttribute("aria-valuenow", "330");

  await page.reload();
  await expect(handle).toHaveAttribute("aria-valuenow", "330");
  expect(Math.round((await sidebar.boundingBox()).width)).toBe(330);
});

test("Connected section shows web and daemon versions and warns only on a mismatch", async ({ page }) => {
  await mock(page);
  let daemonVersion = "v0.6.2";
  await page.route("**/api/connection", route => route.fulfill({ json: { endpoint: "unix:///run/kukeon/kukeond.sock", version: "v0.7.0", ...(daemonVersion === undefined ? {} : { daemonVersion }) } }));
  await page.goto(server.url);
  const connection = page.getByLabel("Daemon connection");
  const versions = connection.getByRole("definition");
  await expect(versions).toHaveText(["v0.7.0", "v0.6.2"]);
  await expect(connection.getByRole("term")).toHaveText(["web", "daemon"]);
  const warning = connection.getByRole("img", { name: "Version mismatch: web v0.7.0 ≠ daemon v0.6.2" });
  await expect(warning).toBeVisible();
  await expect(warning).toHaveAttribute("title", "Version mismatch: web v0.7.0 ≠ daemon v0.6.2");

  // The collapsed rail keeps the versions in the connection dot's tooltip and still shows the warning.
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  const rail = page.locator(".sidebar-rail");
  await expect(rail.getByRole("status")).toHaveAttribute("title", "Daemon: Connected · web v0.7.0 · daemon v0.6.2");
  await expect(rail.getByRole("img", { name: "Version mismatch: web v0.7.0 ≠ daemon v0.6.2" })).toBeVisible();
  await page.getByRole("button", { name: "Expand sidebar" }).click();
  // Both sidebars carry the Connected section and the majordomo entry (#1635).
  for (const [hash, name] of [["#/", "Cell sidebar"], ["#/configs", "Manage sidebar"]]) {
    await page.goto(`${server.url}${hash}`);
    const sidebar = page.getByRole("complementary", { name });
    await expect(sidebar.getByLabel("Daemon connection").getByRole("definition"), hash).toHaveText(["v0.7.0", "v0.6.2"]);
    await expect(sidebar.getByRole("region", { name: "Majordomo", exact: true }), hash).toBeVisible();
  }

  daemonVersion = "v0.7.0";
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(versions).toHaveText(["v0.7.0", "v0.7.0"]);
  await expect(connection.getByRole("img")).toHaveCount(0);

  daemonVersion = undefined;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(versions).toHaveText(["v0.7.0", "unknown"]);
  await expect(connection.getByRole("img")).toHaveCount(0);
});

// Connectors (#1548, §7). The mock world serves test-only connectors — `example-token` (env delivery, one
// readable field), a grouped harness connector, a git-delivered and a registry-delivered one — so everything
// asserted here is rendered from GET /api/connectors, never from a connector the UI knows by name.
async function mockConnectors(page) {
  const world = connectorWorld();
  await mock(page);
  await mockConnectorAPI(page, world);
  return world;
}
const connectorsPanel = page => page.getByRole("region", { name: "Connectors", exact: true });
const newCellFor = name => `#/new-cell?config=${encodeURIComponent(JSON.stringify(["dev", "shared", "services", name]))}`;
const exampleOther = { name: "example-other", realm: "dev", space: "shared", stack: "services" };

test("Connectors page lists the catalog with the Configs missing an identity, and creates, rotates and deletes identities write-only", async ({ page }) => {
  const world = await mockConnectors(page);
  const value = "s3cr3t-example-value";
  const rotated = "r0tated-example-value";
  const logged = [];
  page.on("console", message => logged.push(message.text()));
  const identityPosts = () => world.posts("/api/connectors/example-token/identities");
  await page.goto(`${server.url}#/resources`);
  await nav(page).getByRole("link", { name: "Connectors" }).click();
  await expect(page).toHaveURL(/#\/connectors$/);
  await expect(page.locator(".page-heading h1")).toHaveText("Connectors");
  await expect(nav(page).getByRole("link", { name: "Connectors" })).toHaveAttribute("aria-current", "page");

  // One row per connector of the catalog, whatever its delivery handler — except a registry-delivered one,
  // whose credentials live on the Images page (#1630).
  const list = page.getByRole("region", { name: "Connectors list" });
  const listRow = title => list.getByRole("row").filter({ has: page.getByRole("link", { name: title, exact: true }) });
  await expect(list.getByRole("row")).toHaveCount(4);
  await expect(listRow("Example")).toContainText("example-token");
  await expect(listRow("Example")).toContainText("token — environment variable EXAMPLE_TOKEN");
  await expect(listRow("Demo")).toContainText("login — login kept in a login identity");
  await expect(listRow("Repo")).toContainText("the cell's git identity");
  await expect(listRow("Pulls")).toHaveCount(0);
  // Example: three identities, three Configs require it, two of them have nothing to fill it.
  await expect(listRow("Example").getByRole("cell").nth(2)).toHaveText("3");
  await expect(listRow("Example").getByRole("cell").nth(3)).toHaveText("3");
  await expect(listRow("Example").getByRole("cell").nth(4)).toContainText("2");
  await expect(listRow("Example").getByText("Needs a default")).toBeVisible();
  await expect(listRow("Example").getByRole("link", { name: "Set a default for Example" })).toHaveAttribute("href", `#/connectors/example-token?scope=${encodeURIComponent(JSON.stringify(["dev", "shared", "services"]))}`);
  await expect(listRow("Demo").getByText("Needs a default")).toHaveCount(0);
  await expect(listRow("Demo").getByRole("cell").nth(3)).toHaveText("1");

  await listRow("Example").getByRole("link", { name: "Example", exact: true }).click();
  await expect(page).toHaveURL(/#\/connectors\/example-token$/);
  await expect(page.getByRole("region", { name: "Example connector" })).toContainText("opaque Secret with account");
  const missing = page.getByRole("region", { name: "Configs without a Example identity" });
  await expect(missing.getByRole("listitem")).toHaveCount(2);
  await expect(missing.getByRole("link", { name: "mine", exact: true })).toHaveAttribute("href", "#/configs/dev/shared/services/mine");
  await expect(missing.getByRole("link", { name: "other", exact: true })).toHaveAttribute("href", "#/configs/prod/shared/services/other");

  // Identities: name, scope, type and readable fields.
  const identities = page.getByRole("region", { name: "Example identities" });
  const table = identities.getByRole("table", { name: "Example identities" });
  await expect(table.getByRole("row")).toHaveCount(4);
  await expect(table.getByRole("row", { name: /example-default/ })).toContainText("account: acme");
  await expect(table.getByRole("row", { name: /example-default/ })).toContainText("opaque");
  await expect(table.getByRole("row", { name: /example-other/ }).locator(".scope-cell")).toHaveText("dev / shared / services");

  // The connector page holds no form (#1630): New identity is its own page, generated from the connector —
  // a single secret type needs no type picker, and its one readable field is required.
  await expect(identities.getByRole("form")).toHaveCount(0);
  await identities.getByRole("link", { name: "New identity" }).click();
  await expect(page).toHaveURL(/#\/connectors\/example-token\/new-identity/);
  await expect(page.locator(".page-heading h1")).toHaveText("New identity");
  await expect(page.locator(".page-heading .page-context")).toHaveText("Example");
  const create = page.getByRole("form", { name: "New Example identity" });
  await expect(page.getByLabel("Identity realm")).toHaveValue("dev");
  await expect(create.getByLabel("Secret type")).toHaveCount(0);
  await expect(create.getByText("Account the token belongs to.")).toBeVisible();
  await expect(create.getByLabel("Value")).toHaveAttribute("type", "password");
  await create.getByLabel("Identity name").fill("example-new");
  await create.getByLabel("Value").fill(value);
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(create.getByRole("alert")).toContainText("account is required.");
  expect(identityPosts()).toHaveLength(0);
  await create.getByLabel("account").fill("gamma");
  // No Example default resolves in dev yet: Make this the default starts on (#1693).
  await expect(create.getByRole("checkbox", { name: "Make this the default in dev" })).toBeChecked();
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(toasts(page).filter({ hasText: "Example identity example-new created in dev." })).toBeVisible();
  // Saving returns to the connector page.
  await expect(page).toHaveURL(/#\/connectors\/example-token(\?|$)/);
  await expect(table.getByRole("row")).toHaveCount(5);
  await expect(table.getByRole("row", { name: /example-new/ })).toContainText("account: gamma");
  expect(identityPosts()).toHaveLength(1);
  expect(identityPosts()[0].body).toEqual({ scope: { realm: "dev" }, name: "example-new", type: "opaque", identity: { account: "gamma" }, value, makeDefault: true, method: "token" });
  expect(world.defaults.dev["example-token:token"]).toEqual({ name: "example-new", realm: "dev" });
  // The value is write-only: absent from the page, the URL, storage and the console.
  await expect(create).toHaveCount(0);
  expect(await page.content()).not.toContain(value);
  expect(await page.evaluate(() => document.body.innerText)).not.toContain(value);
  expect(page.url()).not.toContain(value);
  expect(world.requests.map(request => request.path).join(" ")).not.toContain(value);
  expect(await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))).not.toContain(value);

  // Rotate: its own page with the same form, the name fixed and the readable fields kept; it replaces the value.
  await table.getByRole("link", { name: "Rotate Example identity example-default — dev", exact: true }).click();
  await expect(page).toHaveURL(/#\/connectors\/example-token\/rotate-identity\?identity=/);
  await expect(page.locator(".page-heading h1")).toHaveText("Rotate identity");
  const rotate = page.getByRole("form", { name: "Rotate Example identity example-default" });
  await expect(rotate.getByLabel("Identity name")).toBeDisabled();
  await expect(rotate.getByLabel("Identity name")).toHaveValue("example-default");
  await expect(rotate.getByLabel("account")).toHaveValue("acme");
  await expect(rotate.getByLabel("Value")).toHaveValue("");
  // dev has a default now, so the rotation does not make one.
  await expect(rotate.getByRole("checkbox", { name: "Make this the default in dev" })).not.toBeChecked();
  await rotate.getByLabel("Value").fill(rotated);
  await rotate.getByRole("button", { name: "Rotate identity" }).click();
  await expect(toasts(page).filter({ hasText: "Example identity example-default rotated in dev." })).toBeVisible();
  await expect(rotate).toHaveCount(0);
  await expect(page).toHaveURL(/#\/connectors\/example-token(\?|$)/);
  expect(identityPosts()[1].body).toEqual({ scope: { realm: "dev" }, name: "example-default", type: "opaque", identity: { account: "acme" }, value: rotated });
  await expect(table.getByRole("row")).toHaveCount(5);
  expect(await page.content()).not.toContain(rotated);

  // Delete goes through the existing secret delete route, after the same confirmation.
  await table.getByRole("button", { name: "Delete Example identity example-other — dev / shared / services", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Delete secret" });
  await expect(dialog.locator(".cell-action-target code")).toHaveText("dev / shared / services / example-other");
  await expect(dialog.getByRole("button", { name: "Confirm Delete" })).toBeDisabled();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(table.getByRole("row")).toHaveCount(5);
  await table.getByRole("button", { name: "Delete Example identity example-other — dev / shared / services", exact: true }).click();
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Confirm Delete" }).click();
  await expect(toasts(page).filter({ hasText: "Example identity example-other deleted." })).toBeVisible();
  await expect(table.getByRole("row")).toHaveCount(4);
  await expect(table.getByRole("row", { name: /example-other/ })).toHaveCount(0);
  expect(world.requests.at(-1)).toEqual({ path: "/api/catalog/secrets/example-other/delete?realm=dev&space=shared&stack=services", body: {} });
  expect(logged.join("\n")).not.toContain(value);
  expect(logged.join("\n")).not.toContain(rotated);
});

test("an identity form follows the connector's secret types and field types", async ({ page }) => {
  const world = await mockConnectors(page);
  const key = "-----BEGIN OPENSSH " + "PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA-test-only\n-----END OPENSSH PRIVATE KEY-----";
  await page.goto(`${server.url}#/connectors/repo/new-identity`);
  await page.getByRole("button", { name: /^Enter a key or password/ }).click();
  const create = page.getByRole("form", { name: "New Repo identity" });
  // A person field is a Name box and an Email box (#1745), sent joined as Name <email>.
  const author = create.getByRole("group", { name: "author" });
  const authorName = author.getByRole("textbox", { name: "author name" });
  const authorEmail = author.getByRole("textbox", { name: "author email" });
  // Several accepted types: a described choice (#1693); ssh-key shows every field, including the ones limited to it.
  const types = create.getByRole("group", { name: "Secret type" });
  await expect(types.getByRole("radio")).toHaveCount(3);
  await expect(types.locator("label")).toHaveText(["SSH key — clone, push and sign", "Username and password or token — HTTPS", "Author only — commits without push"]);
  await expect(types.getByRole("radio", { name: /^SSH key/ })).toBeChecked();
  await expect(author.getByText("Name", { exact: true })).toBeVisible();
  await expect(authorEmail).toHaveAttribute("type", "email");
  await expect(create.getByRole("group", { name: "sign" }).getByRole("checkbox")).toHaveCount(2);
  await expect(create.getByLabel("allowedSignersRef")).toHaveAttribute("placeholder", "name@realm[/space[/stack]]");
  await expect(create.locator("textarea")).toHaveCount(2);
  await types.getByRole("radio", { name: /^Username and password/ }).check();
  await expect(create.getByRole("group", { name: "sign" })).toHaveCount(0);
  await expect(create.getByLabel("allowedSignersRef")).toHaveCount(0);
  await expect(create.getByRole("textbox", { name: /^Password or token/ })).toHaveAttribute("type", "password");
  await types.getByRole("radio", { name: /^SSH key/ }).check();

  await create.getByLabel("Identity name").fill("repo-new");
  await authorName.fill("Dev");
  await create.getByRole("textbox", { name: /^hosts/ }).fill("example.com, example.org/group");
  await create.getByRole("group", { name: "sign" }).getByLabel("commits").check();
  await create.getByLabel("Private key").fill(key);
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(create.getByRole("alert")).toContainText("author needs a name and an email.");
  await authorEmail.fill("dev@example.com");
  await expect(create.getByRole("checkbox", { name: "Make this the default in dev" })).toBeChecked();
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(page.getByRole("table", { name: "Repo identities" }).getByRole("row", { name: /repo-new/ })).toContainText("hosts: example.com,example.org/group");
  expect(world.posts("/api/connectors/repo/identities")[0].body).toEqual({
    scope: { realm: "dev" }, name: "repo-new", type: "ssh-key",
    identity: { author: "Dev <dev@example.com>", hosts: "example.com,example.org/group", sign: "commits" }, privateKey: key, makeDefault: true,
  });
  await expect(create).toHaveCount(0);
  expect(await page.content()).not.toContain("b3BlbnNzaC1rZXktdjEAAAAA-test-only");

  // An opaque identity of a git-delivered connector is author-only (#1591): no value, no hosts, no signing.
  await page.goto(`${server.url}#/connectors/repo/new-identity`);
  await page.getByRole("button", { name: /^Enter a key or password/ }).click();
  await types.getByRole("radio", { name: /^Author only/ }).check();
  await expect(create.getByText("An author-only identity: it stores no value")).toBeVisible();
  await expect(create.getByLabel("Value")).toHaveCount(0);
  await expect(create.getByRole("textbox", { name: /^hosts/ })).toHaveCount(0);
  await expect(create.getByRole("group", { name: "sign" })).toHaveCount(0);
  await create.getByLabel("Identity name").fill("repo-author");
  await authorName.fill("Dev");
  await authorEmail.fill("dev@example.com");
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(page.getByRole("table", { name: "Repo identities" }).getByRole("row", { name: /repo-author/ })).toContainText("opaque");
  expect(world.posts("/api/connectors/repo/identities")[1].body).toEqual({
    scope: { realm: "dev" }, name: "repo-author", type: "opaque", identity: { author: "Dev <dev@example.com>" },
  });

  // A method that keeps a login in the state volume has login identities, not a default: the page says
  // so and lists them in a panel of their own (#1685).
  await page.goto(`${server.url}#/connectors/demo`);
  await expect(page.getByRole("region", { name: "Demo connector" })).toContainText("The login method keeps its login in a login identity");
  await expect(page.getByRole("region", { name: "Demo login identities" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Demo default identity" }).getByRole("group", { name: /^Demo .* default$/ })).toHaveCount(1);
  await expect(page.getByRole("region", { name: "Demo default identity" }).getByRole("group", { name: "Demo key default" })).toBeVisible();
});

// Issue #1568, item F: the connector page is headed by the connector's title and labels what each scope picker is for.
test("a connector page shows its title with the slug as context, and its scope pickers sit under labelled groups", async ({ page }) => {
  const world = await mockConnectors(page);
  await page.goto(`${server.url}#/connectors/example-token`);
  const heading = page.locator("main .page-heading");
  await expect(heading.locator("h1")).toHaveText("Example");
  await expect(heading.locator(".page-context")).toHaveText("example-token");
  await expect(page).toHaveTitle("Example · kukeon");
  // Each scope picker is in a group named for what its scope applies to. The default identity leads the page.
  const defaults = page.getByRole("region", { name: "Example default identity" });
  expect(await defaults.evaluate(el => el.compareDocumentPosition(document.querySelector(".connector-identities")) & Node.DOCUMENT_POSITION_FOLLOWING)).toBeTruthy();
  await expect(page.getByRole("region", { name: "Example identities" }).getByRole("group")).toHaveCount(0);
  const scope = defaults.getByRole("group", { name: "Which cells?" });
  await expect(scope).toContainText("to see the identity its cells get when they pick none");
  await expect(scope.getByLabel("Default realm")).toHaveValue("dev");
  await expect(scope.getByRole("combobox")).toHaveCount(3);
  const method = defaults.getByRole("group", { name: "Example token default" });
  await expect(method.getByRole("heading", { name: "token method" })).toBeVisible();
  await expect(method.getByLabel("Example token default level")).toBeVisible();
  // Saving is unchanged: a default set through the grouped pickers posts the same body.
  await scope.getByLabel("Default space").selectOption("shared");
  await defaults.getByLabel("Example token default identity").selectOption({ label: "example-default — dev (opaque)" });
  await defaults.getByRole("button", { name: "Set default" }).click();
  await expect.poll(() => world.posts("/api/connectors/example-token/defaults").length).toBe(1);
  expect(world.posts("/api/connectors/example-token/defaults")[0].body).toEqual({ scope: { realm: "dev", space: "shared" }, method: "token", identity: { name: "example-default", realm: "dev" } });
  // Where a new identity is stored is picked on the New identity page.
  await page.goto(`${server.url}#/connectors/example-token/new-identity`);
  const stored = page.getByRole("group", { name: "Where can cells use it?" });
  await expect(stored.getByRole("radio")).toHaveCount(3);
  await expect(stored.getByRole("radio", { name: /^Everywhere in realm dev/ })).toBeChecked();
  // Two realms in the inventory: the realm is a choice too; the Space and Stack selects wait for a narrower choice.
  await expect(stored.getByRole("combobox")).toHaveCount(1);
  await expect(stored.getByLabel("Identity realm")).toHaveValue("dev");
  // A connector the catalog does not define keeps its slug as the heading, with no context line.
  await page.goto(`${server.url}#/connectors/nope`);
  await expect(heading.locator("h1")).toHaveText("nope");
  await expect(heading.locator(".page-context")).toHaveCount(0);
  await page.goto(`${server.url}#/connectors`);
  await expect(heading.locator("h1")).toHaveText("Connectors");
});

// #1745: a connector's identities are deleted in one batch; Rotate stays a row action.
test("a connector's identities are selected together and deleted in one batch, skipping one in kuke-system", async ({ page }) => {
  const world = await mockConnectors(page);
  world.secrets.push({ name: "example-system", realm: "kuke-system", connector: "example-token", identity: { account: "system" } });
  await page.goto(`${server.url}#/connectors/example-token`);
  const panel = page.getByRole("region", { name: "Example identities" });
  const table = panel.getByRole("table", { name: "Example identities" });
  await expect(table.locator("tbody tr")).toHaveCount(4);
  await panel.getByRole("checkbox", { name: "Select all 4 identities" }).check();
  const bar = panel.getByRole("region", { name: "Bulk actions" });
  await expect(bar.getByRole("status")).toHaveText("4 identities selected");
  await bar.getByRole("button", { name: "Delete…" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete 3 identities" });
  await expect(dialog.getByRole("list", { name: "Targets" }).getByRole("listitem")).toHaveCount(3);
  await expect(dialog.getByRole("list", { name: "Skipped" })).toContainText("example-system");
  await dialog.getByRole("checkbox", { name: "I understand this permanently deletes every identity listed." }).check();
  await dialog.getByRole("button", { name: "Delete 3 identities" }).click();
  await expect(toasts(page).filter({ hasText: "Delete: 3 of 3 identities done, 1 skipped." })).toBeVisible();
  await expect(panel.getByRole("status", { name: "Delete report" }).getByRole("list", { name: "Skipped" })).toContainText("example-system");
  await expect(table.locator("tbody tr")).toHaveCount(1);
  expect(world.secrets.filter(secret => secret.connector === "example-token").map(secret => secret.name)).toEqual(["example-system"]);
  await expect(table.getByRole("link", { name: /^Rotate Example identity/ })).toHaveCount(1);
});

test("a git-delivered connector imports its identity from the server's host", async ({ page }) => {
  const world = await mockConnectors(page);
  await page.goto(`${server.url}#/connectors/repo`);
  const panel = page.getByRole("region", { name: "Repo identities" });
  // Only a git-delivered connector offers the import, opened on its New identity page.
  await expect(panel.getByRole("button", { name: "Import from host" })).toHaveCount(0);
  await panel.getByRole("link", { name: "New identity" }).click();
  // The git New identity page starts with the ways to add one (#1693); with no token connector in the
  // catalog there is no "Use a … token" card.
  const choices = page.getByRole("group", { name: "How to add a Repo identity" });
  await expect(choices.getByRole("button")).toHaveText([/^Import from this machine/, /^Enter a key or password/]);
  await expect(choices.getByRole("link")).toHaveCount(0);
  await expect(page.getByRole("form", { name: "New Repo identity" })).toHaveCount(0);
  await choices.getByRole("button", { name: /^Import from this machine/ }).click();
  await expect(choices.getByRole("button", { name: /^Import from this machine/ })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("button", { name: "Hide import" })).toHaveAttribute("aria-expanded", "true");
  const form = page.getByRole("form", { name: "Import Repo identity from host" });
  await form.getByLabel("Import realm").selectOption("dev");
  await form.getByLabel("Hosts").fill("github.com, gitlab.com");
  await expect(form.getByLabel("Make it the Realm default")).toBeChecked();

  // A failed import shows the server's error and stores nothing.
  await form.getByRole("textbox", { name: /^Key / }).fill(".ssh/absent");
  await form.getByRole("button", { name: "Import identity" }).click();
  await expect(form.getByRole("alert")).toContainText("cannot read private key");

  await form.getByRole("textbox", { name: /^Key / }).fill("");
  await form.getByRole("button", { name: "Import identity" }).click();
  await expect(toasts(page).filter({ hasText: "Repo identity git-ssh imported in dev; it is now the dev default." })).toBeVisible();
  const result = form.getByRole("status", { name: "Imported identity" });
  await expect(result).toContainText("/home/operator/.ssh/id_ed25519");
  await expect(result).toContainText("hosts: github.com,gitlab.com");
  await expect(result).toContainText("git signing was not imported");
  expect(world.posts("/api/connectors/repo/import").map(request => request.body)).toEqual([
    { realm: "dev", makeDefault: true, hosts: ["github.com", "gitlab.com"], key: ".ssh/absent" },
    { realm: "dev", makeDefault: true, hosts: ["github.com", "gitlab.com"] },
  ]);
  await page.goto(`${server.url}#/connectors/repo`);
  await expect(panel.getByRole("table", { name: "Repo identities" }).getByRole("row", { name: /git-ssh/ })).toContainText("ssh-key");
  await expect(page.getByRole("region", { name: "Repo default identity" }).locator(".connector-effective")).toContainText("git-ssh");

  // Other connectors have no import.
  await page.goto(`${server.url}#/connectors/example-token/new-identity`);
  await expect(page.getByRole("form", { name: "New Example identity" })).toBeVisible();
  await expect(page.getByRole("button", { name: /import/i })).toHaveCount(0);
});

// The GitHub token for git (#1693), with a test-only connector the catalog marks with tokenForGit: one token,
// sent once, stored as its own identity and as a git identity for the advertised hosts, both made defaults.
test("a token connector stores its token for git too, and makes both the defaults", async ({ page }) => {
  const world = await mockConnectors(page);
  world.connectors.push(structuredClone(testTokenConnector));
  const token = "f0rge-token-canary";
  const posts = () => world.posts("/api/connectors/forge/identities");
  await page.goto(`${server.url}#/connectors/forge/new-identity`);
  const create = page.getByRole("form", { name: "New Forge identity" });
  await expect(create.getByLabel("Identity name")).toHaveValue("forge-token");
  const git = create.getByRole("checkbox", { name: "Also use this token for git on forge.example.com" });
  const makeDefault = create.getByRole("checkbox", { name: "Make this the default in dev" });
  await expect(git).toBeChecked();
  await expect(makeDefault).toBeChecked();
  await expect(create).toContainText("Also stored as the git identity forge-git");
  await expect(create.getByRole("link", { name: "Import from this machine" })).toHaveAttribute("href", /#\/connectors\/repo\/new-identity/);
  await expect(create.getByLabel("Username")).toHaveAttribute("placeholder", "x-access-token");
  await expect(create.getByText("Forge accepts any non-empty username")).toBeVisible();
  await create.getByRole("textbox", { name: /^Token/ }).fill(token);
  // The commit author is a Name box and an Email box (#1745).
  await create.getByRole("textbox", { name: "Commit author email" }).fill("nobody@example.com");
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(create.getByRole("alert")).toContainText("Commit author needs a name and an email.");
  expect(posts()).toHaveLength(0);
  await create.getByRole("textbox", { name: /^Token/ }).fill(token);
  await create.getByRole("textbox", { name: "Commit author name" }).fill("Dev");
  await create.getByRole("textbox", { name: "Commit author email" }).fill("dev@example.com");
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(toasts(page).filter({ hasText: "Forge identity forge-token created in dev. Git identity forge-git created for forge.example.com. Both are now the defaults in dev." })).toBeVisible();
  expect(posts().map(request => request.body)).toEqual([{
    scope: { realm: "dev" }, name: "forge-token", type: "opaque", value: token, git: { author: "Dev <dev@example.com>" }, makeDefault: true, method: "token",
  }]);
  // Two identities, both defaults of dev.
  expect(world.secrets.find(item => item.name === "forge-git")).toEqual({
    name: "forge-git", realm: "dev", type: "basic-auth", connector: "repo", identity: { hosts: "forge.example.com", author: "Dev <dev@example.com>", "kukeon.io/token-from": "forge-token@dev" },
  });
  expect(world.defaults.dev).toEqual({ "forge:token": { name: "forge-token", realm: "dev" }, "repo:": { name: "forge-git", realm: "dev" } });
  await expect(page).toHaveURL(/#\/connectors\/forge(\?|$)/);
  await expect(page.getByRole("table", { name: "Forge identities" }).getByRole("row", { name: /forge-token/ })).toBeVisible();
  expect(await page.content()).not.toContain(token);
  expect(world.requests.map(request => request.path).join(" ")).not.toContain(token);

  // Git unchecked: only the token identity, and nothing about git changes.
  await page.goto(`${server.url}#/connectors/forge/new-identity`);
  await create.getByLabel("Identity name").fill("forge-api");
  await git.uncheck();
  await expect(create).toContainText("Git is unchanged");
  await expect(create.getByLabel("Commit author")).toHaveCount(0);
  await expect(makeDefault).not.toBeChecked();
  await create.getByRole("textbox", { name: /^Token/ }).fill(token + "-2");
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(toasts(page).filter({ hasText: "Forge identity forge-api created in dev." })).toBeVisible();
  expect(posts()[1].body).toEqual({ scope: { realm: "dev" }, name: "forge-api", type: "opaque", value: token + "-2" });
  expect(world.secrets.filter(item => item.connector === "repo" && item.identity?.["kukeon.io/token-from"])).toHaveLength(1);

  // Rotating the identity a git identity is linked to rotates that one too: the box starts on.
  await page.getByRole("table", { name: "Forge identities" }).getByRole("link", { name: "Rotate Forge identity forge-token — dev", exact: true }).click();
  const rotate = page.getByRole("form", { name: "Rotate Forge identity forge-token" });
  await expect(rotate.getByRole("checkbox", { name: "Also use this token for git on forge.example.com" })).toBeChecked();
  await expect(rotate).toContainText("Rotates the linked git identity forge-git too.");
  await rotate.getByRole("textbox", { name: /^Token/ }).fill(token + "-3");
  await rotate.getByRole("button", { name: "Rotate identity" }).click();
  await expect(toasts(page).filter({ hasText: "Forge identity forge-token rotated in dev. Git identity forge-git rotated for forge.example.com." })).toBeVisible();
  expect(posts()[2].body).toEqual({ scope: { realm: "dev" }, name: "forge-token", type: "opaque", value: token + "-3", git: {} });
  // The rotated git identity keeps its link and its author.
  expect(world.secrets.filter(item => item.name === "forge-git")).toEqual([expect.objectContaining({ identity: { hosts: "forge.example.com", author: "Dev <dev@example.com>", "kukeon.io/token-from": "forge-token@dev" } })]);

  // Rotating it with the box unchecked leaves the linked git identity on the previous token: the form and
  // the result say so (#1707).
  await page.getByRole("table", { name: "Forge identities" }).getByRole("link", { name: "Rotate Forge identity forge-token — dev", exact: true }).click();
  await rotate.getByRole("checkbox", { name: "Also use this token for git on forge.example.com" }).uncheck();
  await expect(rotate).toContainText("The linked git identity forge-git keeps the previous token: check the box to rotate it too.");
  await rotate.getByRole("textbox", { name: /^Token/ }).fill(token + "-4");
  await rotate.getByRole("button", { name: "Rotate identity" }).click();
  await expect(toasts(page).filter({ hasText: `Forge identity forge-token rotated in dev. Git identity forge-git still holds the previous token: rotate again with "Also use this token for git" checked to rotate it too.` })).toBeVisible();
  expect(posts()[3].body).toEqual({ scope: { realm: "dev" }, name: "forge-token", type: "opaque", value: token + "-4" });
  expect(await page.content()).not.toContain(token);
});

test("the git New identity page offers three ways to add one, and identities are scoped by where cells can use them", async ({ page }) => {
  const world = await mockConnectors(page);
  world.connectors.push(structuredClone(testTokenConnector));
  await page.goto(`${server.url}#/connectors/repo/new-identity`);
  const choices = page.getByRole("group", { name: "How to add a Repo identity" });
  await expect(choices.locator(".identity-choice strong")).toHaveText(["Import from this machine", "Use a Forge token", "Enter a key or password"]);
  // Import from this machine: the server's import; Enter a key or password: the form.
  await choices.getByRole("button", { name: /^Import from this machine/ }).click();
  await expect(page.getByRole("form", { name: "Import Repo identity from host" })).toBeVisible();
  await expect(page.getByRole("form", { name: "New Repo identity" })).toHaveCount(0);
  await choices.getByRole("button", { name: /^Enter a key or password/ }).click();
  await expect(page.getByRole("form", { name: "New Repo identity" })).toBeVisible();
  await expect(page.getByRole("form", { name: "Import Repo identity from host" })).toHaveCount(0);
  // Use a token: the token connector's form, with git checked.
  await choices.getByRole("link", { name: /^Use a Forge token/ }).click();
  await expect(page).toHaveURL(/#\/connectors\/forge\/new-identity/);
  await expect(page.getByRole("form", { name: "New Forge identity" }).getByRole("checkbox", { name: /^Also use this token for git/ })).toBeChecked();

  // Where can cells use it: everywhere in the realm by default; a narrower choice reveals its selects and
  // stores — and defaults — the identity there.
  await page.goto(`${server.url}#/connectors/example-token/new-identity`);
  const where = page.getByRole("group", { name: "Where can cells use it?" });
  await expect(where.getByRole("radio", { name: /^Everywhere in realm dev/ })).toBeChecked();
  await expect(where).toContainText("(recommended)");
  await expect(where.getByLabel("Identity space")).toHaveCount(0);
  await where.getByRole("radio", { name: "Only in one space…" }).check();
  await expect(where.getByLabel("Identity space")).toHaveValue("shared");
  await expect(where.getByLabel("Identity stack")).toHaveCount(0);
  await where.getByRole("radio", { name: "Only in one stack…" }).check();
  await expect(where.getByLabel("Identity stack")).toHaveValue("services");
  await where.getByRole("radio", { name: "Only in one space…" }).check();
  const create = page.getByRole("form", { name: "New Example identity" });
  await expect(create).toContainText("in dev / shared");
  await create.getByLabel("Identity name").fill("example-space");
  await create.getByLabel("account").fill("space");
  await create.getByLabel("Value").fill("space-value");
  await expect(create.getByRole("checkbox", { name: "Make this the default in dev / shared" })).toBeChecked();
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(toasts(page).filter({ hasText: "Example identity example-space created in dev / shared. It is now the default in dev / shared." })).toBeVisible();
  expect(world.posts("/api/connectors/example-token/identities").at(-1).body).toEqual({
    scope: { realm: "dev", space: "shared" }, name: "example-space", type: "opaque", identity: { account: "space" }, value: "space-value", makeDefault: true, method: "token",
  });
  expect(world.defaults["dev/shared"]).toEqual({ "example-token:token": { name: "example-space", realm: "dev", space: "shared" } });

  // Levels the connector does not accept are not offered.
  world.connectors.find(item => item.name === "example-token").scopes = ["realm"];
  await page.goto(`${server.url}#/connectors/example-token/new-identity`);
  await page.reload();
  await expect(where.getByRole("radio")).toHaveCount(1);
  await expect(where.getByRole("radio", { name: /^Everywhere in realm dev/ })).toBeChecked();
});

test("setting a Realm default clears the Needs connector badge of the Configs that inherit it, without re-rendering them", async ({ page }) => {
  const world = await mockConnectors(page);
  const defaultPosts = () => world.posts("/api/connectors/example-token/defaults");
  const configRow = name => page.getByRole("region", { name: "Configs list" }).getByRole("row").filter({ has: page.getByRole("link", { name, exact: true }) });
  await page.goto(`${server.url}#/configs`);
  await expect(configRow("mine").locator(".needs-connector")).toHaveText("Needs connector: Example");
  await expect(configRow("other").locator(".needs-connector")).toHaveText("Needs connector: Example");
  await expect(configRow("teams")).toBeVisible();
  await expect(configRow("teams").locator(".needs-connector")).toHaveCount(0);
  const before = JSON.stringify(world.configs);

  // The marker on the Connectors page links to the scope of a Config that is missing the identity.
  await nav(page).getByRole("link", { name: "Connectors" }).click();
  await page.getByRole("link", { name: "Set a default for Example" }).click();
  const defaults = page.getByRole("region", { name: "Example default identity" });
  const openLevels = () => defaults.locator("summary", { hasText: "Defaults by level" }).click();
  await expect(defaults.getByLabel("Default realm")).toHaveValue("dev");
  await expect(defaults.getByLabel("Default space")).toHaveValue("shared");
  await expect(defaults.getByLabel("Default stack")).toHaveValue("services");
  // The per-level table sits behind a disclosure, closed by default.
  const levelTable = defaults.getByRole("table", { name: "Example token defaults by level" });
  await expect(levelTable).toBeHidden();
  await expect(defaults.locator("summary", { hasText: "Defaults by level" })).toContainText("0 of 3 set");
  await openLevels();
  await expect(levelTable.getByRole("row")).toHaveCount(4);
  await expect(levelTable.getByText("Not set")).toHaveCount(3);
  await expect(defaults.locator(".connector-effective")).toContainText("No default resolves in this scope");
  await expect(defaults.getByLabel("Example token default level").getByRole("option")).toHaveText(["Realm — dev", "Space — dev / shared", "Stack — dev / shared / services"]);
  await defaults.getByLabel("Example token default level").selectOption({ label: "Realm — dev" });
  await defaults.getByRole("combobox", { name: /^Example token default identity/ }).selectOption({ label: "example-default — dev (opaque)" });
  await defaults.getByRole("button", { name: "Set default", exact: true }).click();
  await expect(toasts(page).filter({ hasText: "Example token default set to example-default at Realm — dev." })).toBeVisible();
  expect(defaultPosts()[0].body).toEqual({ scope: { realm: "dev" }, method: "token", identity: { name: "example-default", realm: "dev" } });
  // Each level's value and the effective one.
  await expect(defaults.locator(".connector-effective")).toContainText("example-default");
  await expect(defaults.locator(".connector-effective")).toContainText("Realm default — from the Realm — dev level");
  await expect(levelTable.getByRole("row", { name: /^realm/ })).toContainText("example-default");
  await expect(levelTable.getByRole("row", { name: /^realm/ })).toContainText("Effective");
  await expect(levelTable.getByText("Not set")).toHaveCount(2);

  // The Config that inherits the Realm default resolves at once; the one in another Realm does not.
  const missing = page.getByRole("region", { name: "Configs without a Example identity" });
  await expect(missing.getByRole("listitem")).toHaveCount(1);
  await expect(missing.getByRole("link", { name: "other", exact: true })).toBeVisible();
  await nav(page).getByRole("link", { name: "Configs" }).click();
  await expect(configRow("mine")).toBeVisible();
  await expect(configRow("mine").locator(".needs-connector")).toHaveCount(0);
  await expect(configRow("other").locator(".needs-connector")).toHaveText("Needs connector: Example");
  // Nothing re-rendered a Config: the only write was the Realm default.
  expect(JSON.stringify(world.configs)).toBe(before);
  expect(world.requests.map(request => request.path)).toEqual(["/api/connectors/example-token/defaults"]);

  // A deeper level wins, and clearing a level falls back to the next one.
  await page.goto(`${server.url}#/connectors/example-token?scope=${encodeURIComponent(JSON.stringify(["dev", "shared", "services"]))}`);
  await defaults.getByLabel("Example token default level").selectOption({ label: "Stack — dev / shared / services" });
  await defaults.getByRole("combobox", { name: /^Example token default identity/ }).selectOption({ label: "example-team — dev (opaque)" });
  await defaults.getByRole("button", { name: "Set default", exact: true }).click();
  await expect(defaults.locator(".connector-effective")).toContainText("example-team");
  await expect(defaults.locator(".connector-effective")).toContainText("from the Stack — dev / shared / services level");
  expect(defaultPosts()[1].body).toEqual({ scope: { realm: "dev", space: "shared", stack: "services" }, method: "token", identity: { name: "example-team", realm: "dev" } });
  await openLevels();
  await defaults.getByRole("button", { name: "Clear Example token default at stack level" }).click();
  await expect(defaults.locator(".connector-effective")).toContainText("from the Realm — dev level");
  await expect(defaults.locator(".connector-effective-name")).toHaveText("example-default");
  expect(defaultPosts()[2].body).toEqual({ scope: { realm: "dev", space: "shared", stack: "services" }, method: "token", identity: null });
  await defaults.getByRole("button", { name: "Clear Example token default at realm level" }).click();
  await expect(defaults.locator(".connector-effective")).toContainText("No default resolves in this scope");
  await nav(page).getByRole("link", { name: "Configs" }).click();
  await expect(configRow("mine").locator(".needs-connector")).toHaveText("Needs connector: Example");
});

test("Config detail shows each connector's identity and level and fixes an unresolved one with a default or a pick for the Config", async ({ page }) => {
  const world = await mockConnectors(page);
  const value = "inline-created-value";
  await page.goto(`${server.url}#/configs/dev/shared/services/mine`);
  const panel = connectorsPanel(page);
  await expect(panel.locator(".panel-heading .needs-connector")).toHaveText("Needs connector: Example");
  const item = panel.getByRole("listitem", { name: "Example connector" });
  await expect(item.locator(".state-badge")).toHaveText("Unresolved");
  await expect(item).toContainText("No identity — required");
  await expect(item).toContainText('no identity fills secret slot "example-token"');

  // Set default: create an identity inline, then save it as a scope default.
  await item.getByRole("button", { name: "Set default for Example" }).click();
  const fix = item.getByRole("group", { name: "Set default for Example" });
  await expect(fix.getByLabel("Example default level").getByRole("option")).toHaveText(["Realm — dev", "Space — dev / shared", "Stack — dev / shared / services"]);
  await expect(fix.getByRole("button", { name: "Save default" })).toBeDisabled();
  await fix.getByRole("button", { name: "Create a new identity…" }).click();
  const create = fix.getByRole("form", { name: "New Example identity" });
  await create.getByLabel("Identity name").fill("example-inline");
  await create.getByLabel("account").fill("inline");
  await create.getByLabel("Value").fill(value);
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(create).toHaveCount(0);
  await expect(fix.getByRole("combobox", { name: /^Example default identity/ }).locator("option:checked")).toHaveText(/^example-inline — dev/);
  await fix.getByRole("button", { name: "Save default" }).click();
  await expect(item.locator(".state-badge")).toHaveText("Resolved");
  await expect(item).toContainText("example-inline");
  await expect(item).toContainText("Realm default");
  await expect(panel.locator(".panel-heading")).toContainText("All resolved");
  expect(world.posts("/api/connectors/example-token/identities")[0].body).toEqual({ scope: { realm: "dev" }, name: "example-inline", type: "opaque", identity: { account: "inline" }, value });
  expect(world.posts("/api/connectors/example-token/defaults")[0].body).toEqual({ scope: { realm: "dev" }, method: "token", identity: { name: "example-inline", realm: "dev" } });
  expect(await page.content()).not.toContain(value);

  // Pick for this Config: a Config fill, which wins over the default.
  await item.getByRole("button", { name: "Pick Example identity for this Config" }).click();
  const pick = item.getByRole("group", { name: "Pick Example identity for this Config" });
  await pick.getByRole("combobox", { name: /^Example identity for this Config/ }).selectOption({ label: "example-other — dev / shared / services (opaque)" });
  await pick.getByRole("button", { name: "Save for this Config" }).click();
  await expect(item).toContainText("picked for this Config");
  await expect(item).toContainText("example-other");
  await expect(item.locator(".state-badge")).toHaveText("Resolved");
  expect(world.posts("/api/configs/connector")[0].body).toEqual({ config: { name: "mine", realm: "dev", space: "shared", stack: "services" }, connector: "example-token", identity: exampleOther });
  // Un-picking returns the Config to the default.
  await item.getByRole("button", { name: "Pick Example identity for this Config" }).click();
  await pick.getByRole("button", { name: "Use the default instead" }).click();
  await expect(item).toContainText("Realm default");
  expect(world.posts("/api/configs/connector")[1].body).toEqual({ config: { name: "mine", realm: "dev", space: "shared", stack: "services" }, connector: "example-token", identity: null });
});

test("a team-owned Config shows its identities read-only with only Set default, and a login names its login identity", async ({ page }) => {
  const world = await mockConnectors(page);
  await page.goto(`${server.url}#/configs/dev/shared/services/teams`);
  const panel = connectorsPanel(page);
  await expect(panel.getByRole("note")).toContainText("Identities are set by team kukeon");
  await expect(panel.locator(".panel-heading")).toContainText("All resolved");
  const example = panel.getByRole("listitem", { name: "Example connector" });
  await expect(example).toContainText("example-team");
  await expect(example).toContainText("set by team kukeon");
  await expect(example.getByRole("button", { name: "Set default for Example" })).toBeVisible();
  await expect(panel.getByRole("button", { name: /^Pick .* identity for this Config$/ })).toHaveCount(0);
  await expect(example.getByRole("button")).toHaveCount(1);
  // A login's identity is its login identity — a Volume — named with its login status (#1685).
  const demo = panel.getByRole("listitem", { name: "Demo connector" });
  await expect(demo).toContainText("Login identity kukeon-dev-demo-state");
  await expect(demo).toContainText("Empty");
  // The Config names the volume's scope, so the volume is a linked Volume reference.
  await expect(demo.getByRole("link", { name: "kukeon-dev-demo-state" })).toHaveAttribute("href", "#/volumes/dev/shared/services/kukeon-dev-demo-state");
  // A team owns the Config: no picker, only Log in — which starts the identity cell, not a role cell.
  await expect(demo.getByRole("button", { name: /Pick .* login identity/ })).toHaveCount(0);
  await expect(demo.getByRole("button")).toHaveCount(1);
  await expect(demo.getByRole("link", { name: "Manage Demo login identities" })).toHaveAttribute("href", `#/connectors/demo?scope=${encodeURIComponent(JSON.stringify(["dev", "shared", "services"]))}`);
  await demo.getByRole("button", { name: "Log in: Demo identity kukeon-dev-demo-state — dev / shared / services" }).click();
  await expect.poll(() => world.posts("/api/connectors/demo/login").length).toBe(1);
  expect(world.posts("/api/connectors/demo/login")[0].body).toEqual({ name: "kukeon-dev-demo-state", scope: { realm: "dev", space: "shared", stack: "services" }, method: "login" });
  // The browser lands on the identity cell's terminal; no role cell was asked for.
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/login-kukeon-dev-demo-state$/);
  expect(world.posts("/api/cells/from-config/create")).toHaveLength(0);
});

// Login identities (#1685): a method that keeps its login in the harness state volume has identities
// too — Volumes tagged with the connector. Everything asserted here comes from GET /api/connectors and
// GET /api/connectors/logins; no connector is known by name.
test("a connector page lists its login identities, creates one, logs in through an identity cell and reopens a running session", async ({ page }) => {
  const world = await mockConnectors(page);
  await page.goto(`${server.url}#/connectors/demo`);
  const panel = page.getByRole("region", { name: "Demo login identities" });
  const table = panel.getByRole("table", { name: "Demo login identities" });
  const row = name => table.getByRole("row").filter({ hasText: name });
  await expect(panel.locator(".panel-heading")).toContainText("2 stored");
  // A tagged Volume nothing uses, and the team's own state volume — listed before any cell tagged it.
  await expect(row("demo-personal").getByRole("cell")).toHaveText(["demo-personal", "dev", "Empty", "—", "Log in"]);
  await expect(row("kukeon-dev-demo-state")).toContainText("dev / shared / services");
  await expect(row("kukeon-dev-demo-state").getByRole("link", { name: "teams" })).toHaveAttribute("href", "#/configs/dev/shared/services/teams");
  await expect(row("demo-personal").getByRole("link", { name: "demo-personal" })).toHaveAttribute("href", "#/volumes/dev/demo-personal");
  // A plain Volume is no identity, and no login content is ever part of the page.
  await expect(table).not.toContainText("scratch");
  // The catalog list counts them with the Secret identities.
  await page.goto(`${server.url}#/connectors`);
  await expect(page.getByRole("region", { name: "Connectors list" }).getByRole("row").filter({ has: page.getByRole("link", { name: "Demo", exact: true }) }).getByRole("cell").nth(2)).toHaveText("3");

  // New login identity: a name and a scope. Creating it starts no cell.
  await page.goto(`${server.url}#/connectors/demo`);
  await panel.getByRole("button", { name: "New login identity" }).click();
  const form = panel.getByRole("form", { name: "New Demo login identity" });
  await form.getByRole("combobox", { name: "Login identity realm" }).selectOption("dev");
  await form.getByLabel("Login identity name").fill("Not A Name");
  await form.getByRole("button", { name: "Create without logging in" }).click();
  await expect(form.getByRole("alert")).toContainText("Use lowercase letters");
  expect(world.posts("/api/connectors/demo/logins")).toHaveLength(0);
  await form.getByLabel("Login identity name").fill("fresh");
  await form.getByRole("button", { name: "Create without logging in" }).click();
  await expect.poll(() => world.posts("/api/connectors/demo/logins").length).toBe(1);
  expect(world.posts("/api/connectors/demo/logins")[0].body).toEqual({ name: "fresh", scope: { realm: "dev" } });
  await expect(row("fresh").getByRole("cell")).toHaveText(["fresh", "dev", "Empty", "—", "Log in"]);
  expect(world.posts("/api/connectors/demo/login")).toHaveLength(0);
  expect(Object.keys(world.sessions)).toHaveLength(0);

  // Log in starts the identity cell and opens its terminal.
  await row("demo-personal").getByRole("button", { name: "Log in: Demo identity demo-personal — dev" }).click();
  await expect.poll(() => world.posts("/api/connectors/demo/login").length).toBe(1);
  expect(world.posts("/api/connectors/demo/login")[0].body).toEqual({ name: "demo-personal", scope: { realm: "dev" }, method: "login" });
  await expect(page).toHaveURL(/#\/workspace\/dev\/default\/default\/login-demo-personal$/);

  // While the session runs the row says so, and a second click opens the same session.
  await page.goto(`${server.url}#/connectors/demo`);
  await expect(row("demo-personal")).toContainText("Login running");
  await row("demo-personal").getByRole("button", { name: "Open login session: Demo identity demo-personal — dev" }).click();
  await expect.poll(() => world.posts("/api/connectors/demo/login").length).toBe(2);
  await expect(page).toHaveURL(/#\/workspace\/dev\/default\/default\/login-demo-personal$/);
  expect(Object.keys(world.sessions)).toEqual(["dev/demo-personal"]);

  // The login ends: the identity cell is gone and the Volume holds a login. The row follows by itself.
  await page.goto(`${server.url}#/connectors/demo`);
  await expect(row("demo-personal")).toContainText("Login running");
  world.sessions = {};
  world.volumes.find(volume => volume.name === "demo-personal").state = "logged in";
  await expect(row("demo-personal")).toContainText("Logged in", { timeout: 10000 });
  await expect(row("demo-personal")).not.toContainText("Login running");
  await expect(row("demo-personal").getByRole("button", { name: "Log in again: Demo identity demo-personal — dev" })).toBeVisible();

  // Create and log in does both in one step, for a new identity.
  await panel.getByRole("button", { name: "New login identity" }).click();
  await form.getByRole("combobox", { name: "Login identity realm" }).selectOption("dev");
  await form.getByLabel("Login identity name").fill("another");
  await form.getByRole("button", { name: "Create and log in" }).click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/default\/default\/login-another$/);
  expect(world.volumes.find(volume => volume.name === "another")).toMatchObject({ connector: "demo", method: "login" });

  // A Volume that is another connector's cannot be logged in to through this one.
  world.volumes.push({ name: "foreign", realm: "dev", connector: "example-token", method: "login", state: "empty" });
  await page.goto(`${server.url}#/connectors/demo`);
  await panel.getByRole("button", { name: "New login identity" }).click();
  await form.getByRole("combobox", { name: "Login identity realm" }).selectOption("dev");
  await form.getByLabel("Login identity name").fill("foreign");
  await form.getByRole("button", { name: "Create and log in" }).click();
  await expect(form.getByRole("alert")).toContainText('is a login identity of connector "example-token"');
});

test("a Config no team owns picks a login identity, and a Volume page shows its tag and login status", async ({ page }) => {
  const world = await mockConnectors(page);
  // A stand-alone Config on the login method, with a state volume of its own.
  world.volumes.push({ name: "solo-state", realm: "dev", space: "shared", stack: "services" });
  world.configs.push({ apiVersion: "v1beta1", kind: "CellConfig", metadata: { name: "solo", realm: "dev", space: "shared", stack: "services" }, spec: { blueprint: { name: "demo", realm: "dev" }, components: ["demo-login"], values: { STATE_VOLUME: "solo-state", STATE_VOLUME_REALM: "dev", STATE_VOLUME_SPACE: "shared", STATE_VOLUME_STACK: "services" } } });
  await page.goto(`${server.url}#/configs/dev/shared/services/solo`);
  const demo = connectorsPanel(page).getByRole("listitem", { name: "Demo connector" });
  await expect(demo).toContainText("Login identity solo-state");
  await demo.getByRole("button", { name: "Pick Demo login identity for this Config" }).click();
  const picker = demo.getByRole("combobox", { name: "Demo login identity for this Config" });
  await expect(picker.locator("option:checked")).toHaveText("solo-state — dev / shared / services — Empty, used by solo");
  // Re-picking the identity already in use saves nothing.
  await expect(demo.getByRole("button", { name: "Save for this Config" })).toBeDisabled();
  await expect(demo.getByRole("button", { name: "Use the default instead" })).toHaveCount(0);
  await picker.selectOption({ label: "demo-personal — dev — Empty" });
  await demo.getByRole("button", { name: "Save for this Config" }).click();
  await expect.poll(() => world.posts("/api/configs/connector").length).toBe(1);
  expect(world.posts("/api/configs/connector")[0].body).toEqual({ config: { name: "solo", realm: "dev", space: "shared", stack: "services" }, connector: "demo", identity: { name: "demo-personal", realm: "dev" } });
  // The pick is the Config's state volume, scope included.
  expect(world.configs.find(config => config.metadata.name === "solo").spec.values).toEqual({ STATE_VOLUME: "demo-personal", STATE_VOLUME_REALM: "dev", STATE_VOLUME_SPACE: "", STATE_VOLUME_STACK: "" });
  await expect(demo).toContainText("Login identity demo-personal");

  // The Volume says what it is: its connector, its login status, and a Log in of its own.
  await page.goto(`${server.url}#/volumes/dev/demo-personal`);
  const identity = page.getByRole("region", { name: "Login identity", exact: true });
  await expect(identity).toContainText("login identity of the demo connector");
  await expect(identity).toContainText("Empty");
  await expect(identity.getByRole("button", { name: /^Log in:/ })).toBeVisible();
  await expect(page.getByRole("region", { name: "Harness state for" }).getByRole("link", { name: "solo" })).toBeVisible();
  // A plain Volume has no such panel.
  await page.goto(`${server.url}#/volumes/dev/scratch`);
  await expect(page.getByRole("region", { name: "Harness state for" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Login identity", exact: true })).toHaveCount(0);
});

test("New Cell picks a login identity for one cell, and a token method chooses persistent or ephemeral harness state", async ({ page }) => {
  const world = await mockConnectors(page);
  const previews = () => world.posts("/api/cells/from-config/preview");
  const panel = newCellSection(page, "Connectors");
  const demo = panel.getByRole("listitem", { name: "Demo connector" });
  const review = previewPanel(page).getByRole("table", { name: "Connectors", exact: true });
  const previewButton = page.getByRole("button", { name: /^(Preview Cell|Refresh preview)$/ });
  await page.goto(`${server.url}${newCellFor("teams")}`);
  await openNewCellSection(page, "Connectors");

  // The Config's login identity is preselected; picking another sends it as this cell's override.
  const loginPicker = demo.getByRole("combobox", { name: "Demo login identity" });
  await loginPicker.selectOption({ label: "demo-personal — dev — Empty" });
  await expect(demo).toContainText("Picked for this cell");
  // A login always keeps persistent state: there is no choice to make.
  await expect(panel.getByRole("combobox", { name: "Harness state" })).toHaveCount(0);
  await expect(panel).toContainText("Harness state is persistent");
  await previewButton.click();
  await expect.poll(() => previews().length).toBe(1);
  expect(previews()[0].body.connectors).toEqual({ demo: { name: "demo-personal", realm: "dev" } });
  expect(previews()[0].body.state).toBeUndefined();
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("Login identity demo-personal");

  // On a token method the harness state is a choice, sent only when it differs from the Config's.
  await demo.getByRole("combobox", { name: "Demo method" }).selectOption("key");
  await demo.getByRole("combobox", { name: /^Demo identity/ }).selectOption({ label: "demo-key-1 — dev (opaque)" });
  const state = panel.getByRole("combobox", { name: "Harness state" });
  await expect(state).toHaveValue("persistent");
  await previewButton.click();
  await expect.poll(() => previews().length).toBe(2);
  expect(previews()[1].body.state).toBeUndefined();
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("State: kukeon-dev-demo-state");
  await state.selectOption("ephemeral");
  await expect(panel).toContainText("this cell mounts no state volume");
  await previewButton.click();
  await expect.poll(() => previews().length).toBe(3);
  expect(previews()[2].body).toMatchObject({ methods: { demo: "key" }, connectors: { demo: { name: "demo-key-1", realm: "dev" } }, state: "ephemeral" });
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("State: ephemeral");
});

test("New Cell pre-selects each connector's effective identity, sends only changed picks and keeps Create disabled while one is unresolved", async ({ page }) => {
  const world = await mockConnectors(page);
  const previews = () => world.posts("/api/cells/from-config/preview");
  const previewButton = page.getByRole("button", { name: /^(Preview Cell|Refresh preview)$/ });
  const createButton = page.getByRole("button", { name: "Create Cell", exact: true });
  const panel = connectorsPanel(page);
  const item = panel.getByRole("listitem", { name: "Example connector" });
  const picker = panel.getByRole("combobox", { name: /^Example identity/ });
  // The preview lists each connector under its Connectors source (#1603).
  const review = previewPanel(page).getByRole("table", { name: "Connectors", exact: true });

  // A resolved Config: the effective identity is pre-selected and nothing is sent for it. Its Connectors
  // section starts collapsed, with a count (#1603).
  await page.goto(`${server.url}${newCellFor("teams")}`);
  await expect(newCellSection(page, "Connectors").locator("summary")).toContainText("2 resolved");
  await expect(newCellSection(page, "Connectors").locator("details")).not.toHaveAttribute("open");
  await openNewCellSection(page, "Connectors");
  await expect(picker.locator("option:checked")).toHaveText("example-team — dev (opaque)");
  await expect(picker.getByRole("option")).toHaveText(["Use the Config's identity", "example-default — dev (opaque)", "example-team — dev (opaque)", "example-other — dev / shared / services (opaque)", "Name a secret…"]);
  await expect(item).toContainText("set by team kukeon");
  await expect(panel.getByRole("listitem", { name: "Demo connector" })).toContainText("Login identity kukeon-dev-demo-state");
  // A login takes no Secret: its identity is a login identity, picked from the connector's own, with
  // the Config's preselected (#1685).
  await expect(panel.getByRole("listitem", { name: "Demo connector" }).getByRole("combobox", { name: /^Demo identity/ })).toHaveCount(0);
  const loginPicker = panel.getByRole("listitem", { name: "Demo connector" }).getByRole("combobox", { name: "Demo login identity" });
  await expect(loginPicker.locator("option:checked")).toHaveText("kukeon-dev-demo-state — dev / shared / services — Empty, used by teams");
  await expect(loginPicker.getByRole("option")).toHaveText(["Use the Config's login identity", "demo-personal — dev — Empty", "kukeon-dev-demo-state — dev / shared / services — Empty, used by teams"]);
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("example-team");
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("Login identity kukeon-dev-demo-state");
  expect(previews()).toHaveLength(1);
  expect(previews()[0].body).toEqual({ config: { name: "teams", realm: "dev", space: "shared", stack: "services" }, name: "", env: [], ignoreDiskPressure: false, targetSpace: "shared", targetStack: "services" });

  // Choosing another identity of the connector sends it as an override, for this cell only.
  await picker.selectOption({ label: "example-other — dev / shared / services (opaque)" });
  await expect(previewPanel(page)).toHaveCount(0);
  await expect(item).toContainText("Picked for this cell");
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("picked for this cell");
  expect(previews()[1].body.connectors).toEqual({ "example-token": exampleOther });
  // Back on the effective identity, nothing is sent.
  await picker.selectOption({ label: "example-team — dev (opaque)" });
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("set by team kukeon");
  expect(previews()).toHaveLength(3);
  expect(previews()[2].body).not.toHaveProperty("connectors");

  // An untagged Secret can be named by hand; the daemon's refusal names the connector.
  await picker.selectOption({ label: "Name a secret…" });
  await expect(panel.getByLabel("Example identity secret realm")).toHaveValue("dev");
  await panel.getByLabel("Example identity secret name").fill("untagged");
  await previewButton.click();
  await expect(content(page).locator(".creation-layout > .error")).toContainText('not an identity of connector "example-token"');
  expect(previews()[3].body.connectors).toEqual({ "example-token": { name: "untagged", realm: "dev" } });

  // The override travels with the create, bound to the reviewed preview.
  await picker.selectOption({ label: "example-default — dev (opaque)" });
  await page.getByLabel("Cell name (optional)").fill("picked-cell");
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("example-default");
  // Created stopped (#1603): the UI lands on cellRead and no start is requested.
  const starts = [];
  page.on("request", request => { if (new URL(request.url()).pathname.endsWith("/start")) starts.push(request.url()); });
  await page.getByLabel("Start the cell").uncheck();
  await expect(page.getByLabel("Attach a terminal")).toBeDisabled();
  await expect(page.getByLabel("Attach a terminal")).not.toBeChecked();
  await createButton.click();
  await expect(page).toHaveURL(/#\/resource\/dev\/shared\/services\/picked-cell$/);
  await expect(toasts(page)).toContainText("created and left stopped");
  expect(starts).toEqual([]);
  expect(world.posts("/api/cells/from-config/create")).toHaveLength(1);
  expect(world.posts("/api/cells/from-config/create")[0].body).toMatchObject({ name: "picked-cell", resolvedName: "picked-cell", preview: "reviewed", connectors: { "example-token": { name: "example-default", realm: "dev" } } });

  // An unresolved required connector is flagged before any preview or create, and opens its section.
  const sent = previews().length;
  await page.goto(`${server.url}${newCellFor("mine")}`);
  await expect(newCellSection(page, "Connectors").locator("details")).toHaveAttribute("open");
  await expect(item.getByRole("alert")).toContainText("Example has no usable identity");
  await expect(panel.locator(".panel-heading .needs-connector")).toHaveText("Needs connector: Example");
  await expect(picker.locator("option:checked")).toHaveText("No identity — required");
  await expect(createButton).toBeDisabled();
  await expect(previewButton).toBeDisabled();
  await expect(item.getByRole("button", { name: "Set default for Example" })).toBeVisible();
  await expect(item.getByRole("button", { name: "Pick Example identity for this Config" })).toBeVisible();

  // Picking an identity for the cell is enough to preview; un-picking blocks again.
  await picker.selectOption({ label: "example-default — dev (opaque)" });
  await expect(item.getByRole("alert")).toHaveCount(0);
  await expect(previewButton).toBeEnabled();
  await picker.selectOption({ label: "No identity — required" });
  await expect(previewButton).toBeDisabled();
  await expect(createButton).toBeDisabled();
  expect(previews()).toHaveLength(sent);

  // The same Set default fix, inline: the Config resolves and Create opens up.
  await item.getByRole("button", { name: "Set default for Example" }).click();
  const fix = item.getByRole("group", { name: "Set default for Example" });
  await fix.getByLabel("Example default level").selectOption({ label: "Stack — dev / shared / services" });
  await fix.getByRole("combobox", { name: /^Example default identity/ }).selectOption({ label: "example-other — dev / shared / services (opaque)" });
  await fix.getByRole("button", { name: "Save default" }).click();
  await expect(item.getByRole("alert")).toHaveCount(0);
  await expect(picker.locator("option:checked")).toHaveText("example-other — dev / shared / services (opaque)");
  expect(world.posts("/api/connectors/example-token/defaults").at(-1).body).toEqual({ scope: { realm: "dev", space: "shared", stack: "services" }, method: "token", identity: exampleOther });
  await expect(previewButton).toBeEnabled();
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("Stack default");
  await expect(createButton).toBeEnabled();
  expect(previews().at(-1).body).not.toHaveProperty("connectors");
});


test("New Cell picks another method of a connector for this cell only and sends it with that method's identity", async ({ page }) => {
  const world = await mockConnectors(page);
  const previews = () => world.posts("/api/cells/from-config/preview");
  const previewButton = page.getByRole("button", { name: /^(Preview Cell|Refresh preview)$/ });
  const panel = connectorsPanel(page);
  const demo = panel.getByRole("listitem", { name: "Demo connector" });
  const method = panel.getByRole("combobox", { name: "Demo method" });
  const review = previewPanel(page).getByRole("table", { name: "Connectors", exact: true });

  // The Config uses the login: the method starts on it and there is no identity to pick.
  await page.goto(`${server.url}${newCellFor("teams")}`);
  await openNewCellSection(page, "Connectors");
  await expect(method).toHaveValue("login");
  await expect(method.getByRole("option")).toHaveText(["login (the Config's)", "key"]);
  await expect(demo.getByRole("combobox", { name: /^Demo identity/ })).toHaveCount(0);
  await expect(panel.getByRole("combobox", { name: /method$/ })).toHaveCount(1);

  // Another method takes an identity: its picker appears, and the preview fails until one resolves.
  await method.selectOption("key");
  await expect(demo).toContainText("Picked for this cell");
  const picker = demo.getByRole("combobox", { name: /^Demo identity/ });
  await expect(picker.locator("option:checked")).toHaveText("Use the default identity for this method");
  await previewButton.click();
  await expect(page.getByText(/requires connector "demo"/)).toBeVisible();
  expect(previews().at(-1).body.methods).toEqual({ demo: "key" });
  expect(previews().at(-1).body).not.toHaveProperty("connectors");

  await picker.selectOption({ label: "demo-key-1 — dev (opaque)" });
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("demo-key-1");
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("key");
  expect(previews().at(-1).body.methods).toEqual({ demo: "key" });
  expect(previews().at(-1).body.connectors).toEqual({ demo: { name: "demo-key-1", realm: "dev" } });

  // Back on the Config's method, neither the method nor the identity is sent.
  await method.selectOption("login");
  await expect(demo.getByRole("combobox", { name: /^Demo identity/ })).toHaveCount(0);
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("Login identity kukeon-dev-demo-state");
  expect(previews().at(-1).body).not.toHaveProperty("methods");
  expect(previews().at(-1).body).not.toHaveProperty("connectors");
});
test("New Config offers one method per group and an identity picker per connector showing its default", async ({ page }) => {
  const world = await mockConnectors(page);
  world.defaults.dev = { "example-token:token": { name: "example-default", realm: "dev" } };
  const previews = () => world.posts("/api/configs/from-blueprint/preview");
  const review = page.getByRole("table", { name: "Connector identities" });
  await page.goto(`${server.url}#/new-config?blueprint=${encodeURIComponent(JSON.stringify(["dev", "demo"]))}`);
  await expect(page.getByRole("heading", { name: "Answer the demo form" })).toBeVisible();
  await page.getByLabel("Config name").fill("fresh");
  await expect(page.getByLabel("Config realm")).toHaveValue("dev");
  await page.getByLabel("Config space").selectOption("shared");
  await page.getByLabel("Config stack").selectOption("services");

  // The methods of one group are a single choice — never two checkboxes — starting on the catalog's
  // default method. Enabled components are sent in the blueprint's order.
  await openSection(page, "Components");
  const method = page.getByLabel("Demo method", { exact: true });
  await expect(method.getByRole("option")).toHaveText(["login", "key"]);
  await expect(method).toHaveValue("demo-key");
  await expect(page.getByRole("checkbox", { name: /^demo-/ })).toHaveCount(0);
  const demoPicker = page.getByRole("combobox", { name: /^Demo identity/ });
  await expect(demoPicker.getByRole("option")).toHaveText(["No default — required", "demo-key-1 — dev (opaque)", "Name a secret…"]);
  await expect(page.getByRole("note")).toContainText("Demo identity");
  await expect(page.getByRole("link", { name: "set a default" })).toHaveAttribute("href", `#/connectors/demo?scope=${encodeURIComponent(JSON.stringify(["dev", "shared", "services"]))}`);
  // A login method takes no Secret: it offers the connector's login identities instead (#1685).
  await method.selectOption("demo-login");
  await expect(demoPicker).toHaveCount(0);
  await openSection(page, "demo-login");
  const loginPicker = page.getByRole("combobox", { name: "Demo login identity" });
  await expect(loginPicker.getByRole("option")).toHaveText(["The state volume named in this form", "demo-personal — dev — Empty", "kukeon-dev-demo-state — dev / shared / services — Empty, used by teams"]);
  await expect(page.getByText("The login is kept in a login identity")).toBeVisible();
  await expect(page.getByRole("note")).toHaveCount(0);

  // An ungrouped connector is a toggle; its picker shows the default the Config's scope inherits.
  await page.getByRole("checkbox", { name: /^example-token connector/ }).check();
  const examplePicker = page.getByRole("combobox", { name: /^Example identity/ });
  // Its identity resolves from the Realm default, so nothing is required and the section stays collapsed.
  await expect(configSection(page, "example-token").locator("details")).not.toHaveAttribute("open");
  await openSection(page, "example-token");
  await expect(examplePicker.getByRole("option")).toHaveText(["Default: example-default — dev (Realm default)", "example-default — dev (opaque)", "example-team — dev (opaque)", "example-other — dev / shared / services (opaque)", "Name a secret…"]);
  await expect(examplePicker).toHaveValue("");
  // Choosing Default sends no fill: the Config keeps inheriting.
  await page.getByRole("button", { name: "Preview Config", exact: true }).click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("Realm default");
  expect(previews()).toHaveLength(1);
  expect(previews()[0].body).toEqual({ config: { name: "fresh", realm: "dev", space: "shared", stack: "services" }, mode: "interactive", spec: { blueprint: { name: "demo", realm: "dev" }, components: ["example-token", "demo-login"] } });

  // A pick is saved as the Config's fill; the git-delivered connector's pick is the git identity reference.
  await examplePicker.selectOption({ label: "example-other — dev / shared / services (opaque)" });
  await method.selectOption("demo-key");
  await demoPicker.selectOption({ label: "demo-key-1 — dev (opaque)" });
  await page.getByRole("checkbox", { name: /^repo connector/ }).check();
  const repoPicker = page.getByRole("combobox", { name: /^Repo identity/ });
  await openSection(page, "repo");
  await expect(repoPicker.getByRole("option")).toHaveText(["No default", "repo-key — dev (ssh-key)", "Name a secret…"]);
  await repoPicker.selectOption({ label: "repo-key — dev (ssh-key)" });
  await page.getByRole("button", { name: "Preview Config", exact: true }).click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("picked for this Config");
  await expect(review.getByRole("row", { name: /^Repo/ })).toContainText("repo-key");
  expect(previews()[1].body.spec).toEqual({
    blueprint: { name: "demo", realm: "dev" }, components: ["example-token", "demo-key", "repo"],
    secrets: { "demo-key": { secretRef: { name: "demo-key-1", realm: "dev" } }, "example-token": { secretRef: exampleOther } },
    git: { identityRef: { name: "repo-key", realm: "dev" } },
  });
  await page.getByRole("button", { name: "Create Config", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Config created" })).toBeVisible();
  expect(world.configs.at(-1).spec.components).toEqual(["example-token", "demo-key", "repo"]);
});

test("the registry-delivered connector is managed on the Images page, which saves Secret-backed registry entries", async ({ page }) => {
  const world = await mockConnectors(page);
  const token = "s3cr3t-registry-token";
  // #1630: its page describes it and points to Images; no defaults, no identities, no form.
  await page.goto(`${server.url}#/connectors/pulls?scope=${encodeURIComponent(JSON.stringify(["dev"]))}`);
  await expect(page.getByRole("region", { name: "Pulls connector" })).toContainText("image pulls of a Realm");
  await expect(page.getByRole("region", { name: "Pulls default identity" })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Pulls identities" })).toHaveCount(0);
  await expect(page.getByRole("form")).toHaveCount(0);
  const notice = page.getByRole("region", { name: "Pulls on the Images page" });
  await expect(notice).toContainText("nothing of it is injected into a cell");
  await notice.getByRole("link", { name: "Images" }).click();
  await expect(page).toHaveURL(/#\/images\/dev$/);
  await expect(page.getByRole("region", { name: "Images list" })).toBeVisible();

  // The Realm's entries: a Secret-backed one names its Secret, a legacy inline one its username.
  const panel = page.getByRole("region", { name: "Registry credentials" });
  await expect(panel.getByRole("row")).toHaveCount(3);
  await expect(panel.getByRole("row", { name: /^ghcr\.io/ }).getByRole("link", { name: "registry-ghcr.io" })).toHaveAttribute("href", "#/secrets/dev/registry-ghcr.io");
  const legacy = panel.getByRole("row", { name: /^legacy\.example\.com/ });
  await expect(legacy).toContainText("old");
  await expect(legacy).toContainText("Inline (legacy)");

  // Saving the legacy entry again moves it into a Secret; the token stays write-only.
  await panel.getByRole("button", { name: "Replace credential for legacy.example.com" }).click();
  const form = panel.getByRole("form", { name: "Add or replace registry credential" });
  await expect(form.getByLabel("Server")).toHaveValue("legacy.example.com");
  await expect(form.getByLabel("Username")).toHaveValue("old");
  await form.getByLabel("Token").fill(token);
  await form.getByRole("button", { name: "Replace credential" }).click();
  await expect(legacy.getByRole("link", { name: "registry-legacy.example.com" })).toHaveAttribute("href", "#/secrets/dev/registry-legacy.example.com");
  await expect(panel.getByText("Inline (legacy)")).toHaveCount(0);
  expect(world.posts("/api/realms/dev/registry-credentials")).toHaveLength(1);
  expect(world.posts("/api/realms/dev/registry-credentials")[0].body).toEqual({ server: "legacy.example.com", username: "old", password: token });
  await expect(form.getByLabel("Token")).toHaveValue("");
  expect(await page.content()).not.toContain(token);

  // A new server gets a Secret-backed entry too, and the Realm page counts the same entries.
  await form.getByLabel("Server").fill("registry.example.org");
  await form.getByLabel("Username").fill("bot");
  await form.getByLabel("Token").fill(token);
  await form.getByRole("button", { name: "Add credential" }).click();
  await expect(panel.getByRole("row", { name: /^registry\.example\.org/ }).getByRole("link", { name: "registry-registry.example.org" })).toBeVisible();
  await page.goto(`${server.url}#/resource/dev`);
  await expect(page.getByRole("region", { name: "Realm images" })).toContainText("Registry credentials: 3 configured");
  await expect(page.getByRole("region", { name: "Registry credentials" })).toHaveCount(0);
  expect(await page.content()).not.toContain(token);
});

// Cell screen layout and overlays (#1565): the expanded terminal follows the sidebar edge, the
// Advanced actions menu and the phone drawer dismiss like other overlays, and toasts stay clear of
// the title row's lifecycle buttons.
const shellOnly = path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined;
const readyAttach = socket => { socket.send(JSON.stringify({ type: "ready" })); };

test("the expanded terminal's left edge follows the sidebar when expanded, collapsed and resized, and uses the gutter on phones", async ({ page }) => {
  await mock(page, shellOnly);
  await page.routeWebSocket("**/attach", readyAttach);
  await page.setViewportSize({ width: 1440, height: 900 });
  const sidebar = page.getByRole("complementary", { name: "Cell sidebar" });
  const expanded = page.locator(".terminal-workspace.is-expanded");
  const sidebarRight = async () => { const box = await sidebar.boundingBox(); return Math.round(box.x + box.width); };
  async function expandBeside(edge) {
    await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
    await expect.poll(async () => Math.round((await expanded.boundingBox()).x)).toBe(edge + 12);
    await terminalTools(page).getByRole("button", { name: "Collapse terminal" }).click();
    await expect(expanded).toHaveCount(0);
  }
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  expect(await sidebarRight()).toBe(264);
  await expandBeside(264);
  await sidebar.getByRole("button", { name: "Collapse sidebar" }).click();
  expect(await sidebarRight()).toBe(56);
  await expandBeside(56);
  await sidebar.getByRole("button", { name: "Expand sidebar" }).click();
  for (const width of [208, 480]) {
    await page.evaluate(value => localStorage.setItem("kukeon-sidebar-width", String(value)), width);
    await page.reload();
    await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
    expect(await sidebarRight()).toBe(width);
    await expandBeside(width);
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  await expect.poll(async () => Math.round((await expanded.boundingBox()).x)).toBe(12);
});

test("the cell Advanced actions menu closes on Escape, outside pointer down and a chosen action, and stays inside the viewport", async ({ page }) => {
  await mock(page, shellOnly);
  await page.routeWebSocket("**/attach", readyAttach);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  const menu = page.locator("details.cell-advanced-actions");
  const summary = menu.locator("summary");
  // An Escape in the same task as the opening click, before the async toggle event re-renders, still closes it.
  expect(await page.evaluate(() => {
    const details = document.querySelector("details.cell-advanced-actions");
    details.querySelector("summary").click();
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    return details.open;
  })).toBe(false);
  await expect(summary).toBeFocused();

  await summary.click();
  await expect(menu).toHaveAttribute("open", "");
  await page.keyboard.press("Escape");
  await expect(menu).not.toHaveAttribute("open");
  await expect(summary).toBeFocused();

  await summary.click();
  await expect(menu).toHaveAttribute("open", "");
  await content(page).click({ position: { x: 4, y: 4 } });
  await expect(menu).not.toHaveAttribute("open");

  // Over the expanded terminal, Escape closes only the menu.
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  await expect(page.locator(".terminal-workspace")).toHaveClass(/is-expanded/);
  await summary.click();
  await expect(menu).toHaveAttribute("open", "");
  await page.keyboard.press("Escape");
  await expect(menu).not.toHaveAttribute("open");
  await expect(page.locator(".terminal-workspace")).toHaveClass(/is-expanded/);
  await terminalTools(page).getByRole("button", { name: "Collapse terminal" }).click();

  await summary.click();
  await page.getByRole("button", { name: "Kill cell…" }).click();
  await expect(menu).not.toHaveAttribute("open");
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

  for (const [width, height] of [[390, 844], [768, 1024]]) {
    await page.setViewportSize({ width, height });
    await summary.click();
    await expect(menu).toHaveAttribute("open", "");
    const box = await page.locator(".cell-advanced-menu").boundingBox();
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
    expect(box.y + box.height).toBeLessThanOrEqual(height);
    await page.keyboard.press("Escape");
    await expect(menu).not.toHaveAttribute("open");
  }
});

test("the phone sidebar drawer has a scrim, closes on a scrim tap or Escape with focus back on the toggle, and makes main inert", async ({ page }) => {
  await mock(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(server.url);
  const drawer = page.getByRole("complementary", { name: "Cell sidebar" });
  const toggle = page.getByRole("button", { name: "Toggle sidebar" });
  const scrim = page.locator(".sidebar-scrim");
  const main = page.locator("main");
  await expect(scrim).toBeHidden();
  await toggle.click();
  await expect(drawer).toBeVisible();
  await expect(scrim).toBeVisible();
  await page.mouse.click(370, 400);
  await expect(drawer).toBeHidden();
  await expect(scrim).toBeHidden();
  await expect(toggle).toBeFocused();

  await toggle.click();
  await expect(drawer).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
  await expect(toggle).toBeFocused();

  await toggle.click();
  await expect(main).toHaveAttribute("inert", "");
  for (let i = 0; i < 10; i++) {
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => !!document.activeElement?.closest("main"))).toBe(false);
  }
  await drawer.getByRole("button", { name: "Close sidebar" }).click();
  await expect(drawer).toBeHidden();
  await expect(toggle).toBeFocused();
  await expect(main).not.toHaveAttribute("inert");

  // Desktop never sets inert or shows the scrim, even if the drawer was left open at phone width.
  await toggle.click();
  await expect(main).toHaveAttribute("inert", "");
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(main).not.toHaveAttribute("inert");
  await expect(scrim).toBeHidden();
  await expect(drawer).toBeVisible();
});

function lifecycleMock(state) {
  const posts = [];
  const cell = "/api/realms/dev/spaces/shared/stacks/services/cells/api";
  const handler = async (path, request) => {
    if (request.method() === "POST") {
      posts.push(path.split("/").at(-1));
      state.value = path.endsWith("/stop") ? "Stopped" : "Ready";
      return { json: {} };
    }
    if (path.endsWith("/containers")) return { json: [{ id: "shell", attachable: true }] };
    if (path === cell) return { json: { ...doc(["dev", "shared", "services", "api"]), status: { state: state.value } } };
    if (path.endsWith("/cells") && path.includes("/dev/")) return { json: fixture(path).map(item => ({ ...item, status: { state: state.value } })) };
  };
  return { posts, handler };
}

// #1745: Auto-attach terminals, On by default, reattaches after a restart; Off, a terminal waits for Attach.
test("with Auto-attach terminals on a restart reattaches by itself; off, the terminal waits for Attach", async ({ page }) => {
  const state = { value: "Ready" };
  const { posts, handler } = lifecycleMock(state);
  await mock(page, handler);
  let sockets = 0;
  await page.routeWebSocket("**/attach", socket => { sockets++; readyAttach(socket); });
  const cell = `${server.url}#/workspace/dev/shared/services/api`;
  const connection = terminalTools(page).locator(".terminal-connection-state");
  const row = page.locator(".cell-page-title-row");
  await page.goto(cell);
  await expect(connection).toHaveText("Attached");
  expect(sockets).toBe(1);
  await row.getByRole("button", { name: "Restart", exact: true }).click();
  await expect.poll(() => posts).toEqual(["restart"]);
  await expect.poll(() => sockets).toBe(2);
  await expect(connection).toHaveText("Attached");

  // Off: opening the cell attaches nothing until Attach.
  const settings = await openSettings(page);
  await settings.getByRole("switch", { name: "Auto-attach terminals" }).click();
  expect(await page.evaluate(() => localStorage.getItem("kukeon-terminal-auto-attach"))).toBe("false");
  await page.goto(cell);
  await page.reload();
  await expect(connection).toHaveText("Detached");
  await page.waitForTimeout(300);
  expect(sockets).toBe(2);
  await terminalTools(page).getByRole("button", { name: "Attach", exact: true }).click();
  await expect(connection).toHaveText("Attached");
  expect(sockets).toBe(3);
  // A restart ends the session: Reconnect attaches to the new process, nothing reattaches by itself.
  await row.getByRole("button", { name: "Restart", exact: true }).click();
  await expect.poll(() => posts).toEqual(["restart", "restart"]);
  await expect(terminalTools(page).getByRole("button", { name: "Reconnect", exact: true })).toBeVisible();
  await page.waitForTimeout(300);
  expect(sockets).toBe(3);
});

test("cell screen toasts stay clear of the title-row buttons, so Start follows Stop without dismissing the toast", async ({ page }) => {
  // Eight view × viewport passes take over half the default 30s on a single CPU.
  test.slow();
  const state = { value: "Ready" };
  const { posts, handler } = lifecycleMock(state);
  await mock(page, handler);
  await page.routeWebSocket("**/attach", readyAttach);
  const row = page.locator(".cell-page-title-row:visible");
  // After Stop the row re-renders several times (Stop… while pending, Reconnect once the terminal
  // drops, then Start replacing Stop and Restart), so positional nth() locators from .all() go stale
  // and wait out the test timeout. Read every rect in one evaluate and poll until the row settles.
  const rects = locator => locator.evaluateAll(elements => elements.map(element => {
    const { x, y, width, height } = element.getBoundingClientRect();
    return { x, y, width, height, label: element.getAttribute("aria-label") ?? element.textContent };
  }).filter(rect => rect.width && rect.height));
  const overlaps = (a, b) => a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
  for (const [width, height] of [[1440, 900], [1024, 768], [768, 1024], [390, 844]]) {
    await page.setViewportSize({ width, height });
    for (const view of ["workspace", "resource"]) {
      await page.goto(`${server.url}#/${view}/dev/shared/services/api`);
      await row.getByRole("button", { name: "Stop", exact: true }).click();
      await expect.poll(() => posts.at(-1)).toBe("stop");
      await expect(toasts(page).first()).toHaveClass(/success/);
      await expect(row.getByRole("button", { name: "Start", exact: true })).toBeVisible();
      await expect.poll(async () => {
        const buttons = await rects(row.getByRole("button"));
        const toastRects = await rects(toasts(page));
        // The success toast auto-dismisses after 5s, the same as this poll's timeout. Without a toast
        // there is nothing to overlap, so an empty stack must fail rather than pass vacuously.
        if (!toastRects.length) return ["<no toast>"];
        const covered = [];
        for (const toast of toastRects) covered.push(...buttons.filter(button => overlaps(toast, button)).map(button => button.label));
        return covered;
      }, { message: `${width} ${view}: toasts cover title-row buttons` }).toEqual([]);
      // No dismiss: the click has to reach Start.
      const count = posts.length;
      await row.getByRole("button", { name: "Start", exact: true }).click({ timeout: 5000 });
      await expect.poll(() => posts.length).toBe(count + 1);
      expect(posts.at(-1)).toBe("start");
      await expect(toasts(page).first()).toHaveClass(/success/);
    }
  }
});

test("a success toast under a resting pointer still auto-dismisses, while moving onto one pauses it", async ({ page }) => {
  await page.clock.install();
  const state = { value: "Ready" };
  const { posts, handler } = lifecycleMock(state);
  await mock(page, handler);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const row = page.locator(".cell-page-title-row");
  // Rest the pointer where the stack will appear (bottom right on the cell screen), then act from the keyboard.
  const resting = { x: 1440 - 16 - 120, y: 900 - 16 - 20 };
  await page.mouse.move(resting.x, resting.y);
  await row.getByRole("button", { name: "Stop", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect.poll(() => posts.length).toBe(1);
  await expect(toasts(page)).toHaveClass(/success/);
  const box = await toasts(page).boundingBox();
  expect(resting.x > box.x && resting.x < box.x + box.width && resting.y > box.y && resting.y < box.y + box.height).toBe(true);
  await page.clock.fastForward(6000);
  await expect(toasts(page)).toHaveCount(0);

  await row.getByRole("button", { name: "Start", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect.poll(() => posts.length).toBe(2);
  await expect(toasts(page)).toHaveClass(/success/);
  await page.mouse.move(700, 300);
  await toasts(page).hover();
  await page.clock.fastForward(6000);
  await expect(toasts(page)).toHaveCount(1);
  await page.mouse.move(700, 300);
  await page.clock.fastForward(5100);
  await expect(toasts(page)).toHaveCount(0);
});

// Issue #1567, item A: interactive text gets a 24px hit area and the resizers keep a visible keyboard focus ring.
test("resource-name and breadcrumb links have a 24px hit target and resizers show a focus ring", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mock(page);
  await page.goto(`${server.url}#/cells`);
  const name = content(page).locator("a.resource-name").first();
  await expect(name).toBeVisible();
  expect((await name.boundingBox()).height).toBeGreaterThanOrEqual(24);
  // A table row with a name link stays the compact height (about 36px with a state badge).
  expect((await content(page).locator("tbody tr").first().boundingBox()).height).toBeLessThanOrEqual(38);
  const home = page.getByRole("navigation", { name: "Resource scope" }).getByRole("link", { name: "Overview" });
  expect((await home.boundingBox()).height).toBeGreaterThanOrEqual(24);
  // The Manage links and the view switch have the same hit target (#1635).
  for (const link of await nav(page).getByRole("link").all()) expect((await link.boundingBox()).height).toBeGreaterThanOrEqual(24);
  for (const link of await page.locator(".view-switch").getByRole("link").all()) expect((await link.boundingBox()).height).toBeGreaterThanOrEqual(24);

  await page.goto(`${server.url}#/resource/dev/shared/services`);
  // The root crumb leads the scope crumbs on resource and cell screens (#1568).
  const crumbs = page.getByRole("navigation", { name: "Resource scope" }).getByRole("link");
  await expect(crumbs).toHaveText(["Overview", "dev", "shared", "services"]);
  for (const crumb of await crumbs.all()) expect((await crumb.boundingBox()).height).toBeGreaterThanOrEqual(24);

  const handle = page.getByRole("separator", { name: "Resize sidebar" });
  await page.keyboard.press("Shift");
  await handle.focus();
  expect(await handle.evaluate(el => el.matches(":focus-visible"))).toBe(true);
  expect(await handle.evaluate(el => getComputedStyle(el).boxShadow)).toMatch(/rgb/);
});

// Issue #1567, item B: keyboard and screen-reader users keep their place across dialogs and route changes.
test("closing a confirmation dialog returns focus to its trigger, or to the Advanced actions summary", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const summary = page.locator("summary", { hasText: "Advanced actions" });
  for (const [action, close] of [["Kill", "Escape"], ["Delete", "Cancel"], ["Purge", "Escape"]]) {
    await summary.click();
    await page.getByRole("button", { name: `${action} cell…` }).click();
    await expect(page.getByRole("dialog", { name: `${action} cell` })).toBeVisible();
    if (close === "Escape") await page.keyboard.press("Escape");
    else await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(summary).toBeFocused();
  }

  await page.goto(`${server.url}#/cells`);
  const deleteIcon = page.locator("tbody tr").first().getByRole("button", { name: /^Delete cell / });
  await deleteIcon.click();
  await expect(page.getByRole("dialog", { name: "Delete cell" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(deleteIcon).toBeFocused();

  await page.goto(`${server.url}#/realms`);
  const purgeRealm = page.getByRole("button", { name: "Purge realm dev" });
  await purgeRealm.click();
  await expect(page.getByRole("dialog", { name: "Purge realm" })).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();
  await expect(purgeRealm).toBeFocused();
});

// The menu's React open state trails the asynchronous toggle event. Escape must act on the element's
// own state, so it is neither lost right after the menu opens nor swallowed right after an action
// closes the menu and opens its dialog (CI hit both windows).
test("Advanced actions Escape follows the menu's own open state, not the trailing toggle event", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const menu = page.locator("details.cell-advanced-actions");
  await expect(menu.locator("summary")).toBeVisible();
  const escapeInSameTask = action => page.evaluate(action => {
    const details = document.querySelector("details.cell-advanced-actions");
    if (action) [...details.querySelectorAll("button")].find(button => button.textContent === action).click();
    else details.querySelector("summary").click();
    const escape = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    document.body.dispatchEvent(escape);
    return { open: details.open, swallowed: escape.defaultPrevented };
  }, action);
  const settle = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => setTimeout(resolve, 50))));

  expect(await escapeInSameTask(null)).toEqual({ open: false, swallowed: true });
  await settle();
  await expect(menu).not.toHaveAttribute("open");

  await menu.locator("summary").click();
  await expect(menu).toHaveAttribute("open", "");
  await settle();
  expect(await escapeInSameTask("Kill cell…")).toEqual({ open: false, swallowed: false });
  await expect(page.getByRole("dialog", { name: "Kill cell" })).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("route changes focus the new page heading and title the document; the first Tab stop skips to main", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/resources`);
  const heading = page.locator("main .page-heading h1");
  await expect(heading).toHaveText("Overview");
  await expect(page).toHaveTitle("Overview · kukeon");
  // The initial load leaves focus where the browser put it.
  await expect(heading).not.toBeFocused();

  await nav(page).getByRole("link", { name: "Configs" }).click();
  await expect(heading).toHaveText("Configs");
  await expect(heading).toBeFocused();
  await expect(page).toHaveTitle("Configs · kukeon");

  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  await expect(page).toHaveTitle("api · kukeon");
  await page.getByRole("navigation", { name: "Resource scope" }).getByRole("link", { name: "Stack: services" }).click();
  await expect(heading).toHaveText("services");
  await expect(heading).toBeFocused();
  await expect(page).toHaveTitle("services · kukeon");

  await page.reload();
  await expect(heading).toHaveText("services");
  const hash = new URL(page.url()).hash;
  await page.keyboard.press("Tab");
  const skip = page.getByRole("link", { name: "Skip to content" });
  await expect(skip).toBeFocused();
  expect((await skip.boundingBox()).y).toBeGreaterThanOrEqual(0);
  await page.keyboard.press("Enter");
  await expect(page.locator("main")).toBeFocused();
  expect(new URL(page.url()).hash).toBe(hash);
});

// Issue #1567, item C: lists, the Images table and the cell title row fit tablet and phone widths.
test("lists fit tablet and phone widths, Realms omit Scope, short pages have no phantom scroll and paths wrap at separators", async ({ page }) => {
  const longImages = [...inventoryImages, { name: "ghcr.io/eminwux/kukeon-very-long-repository-name/kukeond:v0.10.0-56-ge85ed8c0", size: 123400000, createdAt: "2026-01-02T03:04:05Z", digest: "sha256:fedcba9876543210fedcba9876543210", mediaType: "application/vnd.docker.distribution.manifest.v2+json", labels: {} }];
  await mockCatalog(page);
  await mock(page, path => path === "/api/realms/dev/image-inventory" ? { json: { realm: "dev", namespace: "dev.kukeon.io", images: longImages } } : undefined);
  const fits = async (hash, viewport) => {
    await page.goto(`${server.url}${hash}`);
    await expect(page.locator(".table-scroll tbody tr").first(), `${hash} at ${viewport}`).toBeVisible();
    const { scrollWidth, clientWidth } = await page.locator(".table-scroll").first().evaluate(el => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }));
    expect(scrollWidth, `${hash} at ${viewport}`).toBeLessThanOrEqual(clientWidth + 1);
  };
  for (const [width, height] of [[768, 1024], [390, 844]]) {
    await page.setViewportSize({ width, height });
    for (const hash of ["#/cells", "#/configs", "#/blueprints", "#/images/dev"]) await fits(hash, `${width}x${height}`);
    // Stacked rows keep the name, state and row actions readable; secondary cells are labelled.
    await page.goto(`${server.url}#/cells`);
    const row = page.locator("tbody tr").first();
    await expect(row.locator("a.resource-name")).toBeVisible();
    await expect(row.locator(".state-badge")).toBeVisible();
    await expect(row.getByRole("button", { name: /^Delete cell / })).toBeAttached();
    await expect(row.locator('td[data-label="Scope"]')).toBeVisible();
  }
  await page.setViewportSize({ width: 1024, height: 768 });
  await fits("#/images/dev", "1024x768");
  await expect(page.locator(`td.image-ref[title="${longImages[2].name}"]`)).toHaveCount(1);

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${server.url}#/realms`);
  await expect(page.getByRole("row")).toHaveCount(3);
  await expect(page.getByRole("columnheader", { name: "Scope" })).toHaveCount(0);
  await page.goto(`${server.url}#/spaces`);
  await expect(page.getByRole("columnheader", { name: "Scope" })).toHaveCount(1);

  await page.route("**/api/catalog/volumes", route => route.fulfill({ json: [] }));
  await page.goto(`${server.url}#/volumes`);
  await expect(page.getByRole("heading", { name: "No volumes found" })).toBeVisible();
  expect(await page.evaluate(() => document.scrollingElement.scrollHeight - document.scrollingElement.clientHeight)).toBe(0);

  // Socket and scope paths are segments that each end at a separator, so lines break only there.
  const socket = page.locator(".connection code .path-segment");
  expect((await socket.allTextContents()).slice(0, -1).every(segment => segment.endsWith("/"))).toBe(true);
  await page.goto(`${server.url}#/cells`);
  const scope = page.locator("tbody tr").first().locator(".scope-cell .path-segment");
  await expect(scope).toHaveText(["dev /", "shared /", "services"]);
});

test("the cell title row stays on one line at 1024 and 768, truncating the name", async ({ page }) => {
  const name = "worker-with-a-rather-long-cell-name";
  await mock(page, path => path === "/api/realms/dev/spaces/shared/stacks/services/cells" ? { json: [doc(["dev", "shared", "services", name])] } : path.endsWith(`/cells/${name}`) ? { json: doc(["dev", "shared", "services", name]) } : undefined);
  for (const [width, height] of [[1024, 768], [768, 1024]]) {
    await page.setViewportSize({ width, height });
    for (const hash of [`#/resource/dev/shared/services/${name}`, `#/workspace/dev/shared/services/${name}`]) {
      await page.goto(`${server.url}${hash}`);
      const row = page.locator(".cell-page-title-row");
      await expect(row.getByRole("heading", { level: 1 })).toHaveAttribute("title", name);
      await expect(row.getByText("Advanced actions", { exact: true })).toBeVisible();
      expect((await row.boundingBox()).height, `${hash} at ${width}`).toBeLessThanOrEqual(48);
    }
  }
});

// Teams and Sources (#1618, #1644). The teams API is mocked: the pages only shape requests and render the
// drafts, plans, views and results the server returns, and every action posts the CLI's inputs.
function teamsWorld() {
  const summary = (name, extra = {}) => ({ name, realm: "default", roles: [], rolesInherited: true, harnesses: [], harnessesInherited: true, configs: [], ...extra });
  const plan = (team, origin) => ({
    command: `kuke create team ${team}`, origin, declaration: `apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: ${team}\n`,
    sources: [{ name: "project", url: "https://example.test/web", ref: "main", commit: "0123456789abcdef0123456789abcdef01234567" }],
    pairs: [{ role: "dev", harness: "claude", config: `${team}-dev-claude`, image: "kukeon.internal/claude:1", components: ["github"], tasks: [{ name: "pick-issue", config: `${team}-dev-claude-pick-issue`, schedule: "@hourly" }] }],
    create: [`${team}-dev-claude`], rerender: [], prune: [], missingSecrets: ["gh-token"], createSecrets: [], notes: [],
  });
  const projectContents = { source: { name: "web", url: "https://example.test/web", branch: "main" }, commit: "0123456789abcdef0123456789abcdef01234567", roles: [{ name: "dev", path: "roles/dev", harnesses: ["claude"], tasks: [{ name: "pick-issue", schedule: "@hourly" }, { name: "sweep", schedule: "@daily", suspend: true }], default: false }], harnesses: [] };
  const webForm = () => ({
    name: "web", project: { alias: "project", stored: "web", refKind: "branch", ref: "main", clonePath: "/home/user/web" }, sources: null,
    roles: { inherit: false, items: [{ name: "dev", source: "agents", path: "dev", image: ["go"], tasks: { all: true, items: [] } }] },
    harnesses: { inherit: true, items: [] }, connectors: { inherit: true, items: {} },
  });
  const webView = {
    project: { alias: "project", stored: "web", url: "example.test/web", ref: "branch main", clonePath: "/home/user/web", identity: "git-ssh", declared: true },
    sources: { inherited: true, items: [{ alias: "agents", stored: "agents", url: "example.test/agents", ref: "branch main", clonePath: "/home/user/agents", declared: false }] },
    roles: { inherited: false, items: [{ name: "dev", source: "agents", path: "dev", image: ["go"], secrets: [], allTasks: true, selected: [] }] },
    harnesses: { inherited: true, items: [{ name: "claude", source: "kukeon", path: "team/harnesses/claude" }] },
    connectors: { inherited: true, items: [] },
    tasks: [{ role: "dev", task: "health-sweep", config: "web-dev-claude-health-sweep", scope: ["default", "web", "default"], schedule: { cron: "@hourly", suspend: true }, status: { lastScheduleTime: "2026-11-01T03:00:00Z", skippedFires: 0, lastError: "" } }],
    configs: [{ name: "web-dev-claude", scope: ["default", "web", "default"], role: "dev" }, { name: "web-dev-claude-health-sweep", scope: ["default", "web", "default"], role: "dev", task: "health-sweep" }],
  };
  return {
    requests: [],
    teams: [summary("kuke", { system: true, roles: ["majordomo"], rolesInherited: false, configs: ["kuke-majordomo-claude"] }), summary("web", { projectUrl: "https://example.test/web", projectRef: "main", configs: ["web-dev-claude", "web-dev-claude-health-sweep"], rolesInherited: false, roles: ["dev"] })],
    sources: {
      sources: [
        { name: "agents", url: "https://example.test/agents", branch: "main", usedBy: [{ team: "web", as: "role", name: "dev" }, { as: "default role", name: "dev" }], defaultRoles: [{ name: "dev", source: "agents", path: "dev" }], defaultHarnesses: [] },
        { name: "kukeon", url: "github.com/eminwux/kukeon", tag: "v1", identity: "git-ssh", usedBy: [], defaultRoles: [], defaultHarnesses: [] },
      ],
      defaultRoles: [{ name: "dev", source: "agents", path: "dev" }], defaultHarnesses: [{ name: "claude", source: "kukeon", path: "team/harnesses/claude" }],
    },
    secrets: [{ metadata: { name: "git-ssh", realm: "default" }, spec: { connector: "git", type: "ssh-key" } }, { metadata: { name: "other", realm: "dev" }, spec: { connector: "git" } }],
    draft(body) {
      if (body.declaration) {
        return { origin: { kind: "file", detail: "declaration" }, name: "acme", declaration: body.declaration,
          form: { name: "acme", project: { alias: "project", url: "github.com/acme/app", clonePath: "/home/user/acme" }, sources: [{ alias: "shared", url: "github.com/acme/shared" }], roles: { inherit: true, items: [] }, harnesses: { inherit: true, items: [] }, connectors: { inherit: true, items: {} } },
          sources: [{ alias: "project", stored: "acme", url: "github.com/acme/app", state: "new" }, { alias: "shared", stored: "shared", url: "github.com/acme/shared", state: "conflict", have: "github.com/other/shared" }], notes: [] };
      }
      return { origin: { kind: "repository file", detail: "kuketeam.yaml at 0123456789ab" }, name: "web", declaration: "opaque draft declaration\n",
        form: { name: "web", project: { alias: "project", url: body.source, refKind: "branch", ref: "main", clonePath: "/home/user/web", identity: body.identity }, sources: null, roles: { inherit: false, items: [{ name: "dev", source: "project", path: "roles/dev", image: [], tasks: { all: true, items: [] } }] }, harnesses: { inherit: true, items: [] }, connectors: { inherit: true, items: {} } },
        sources: [{ alias: "project", stored: "web", url: body.source, ref: "branch main", state: "new", contents: projectContents }], notes: [] };
    },
    webForm, webView, plan, exists: true,
  };
}
async function mockTeams(page) {
  const world = teamsWorld();
  await mock(page);
  await page.route(url => url.pathname === "/api/catalog/configs", route => route.fulfill({ json: [{ metadata: { name: "web-dev-claude", realm: "default", space: "web", stack: "default", labels: { "kukeon.io/team": "web" } } }] }));
  await page.route(url => url.pathname === "/api/catalog/secrets", route => route.fulfill({ json: world.secrets }));
  await page.route(url => url.pathname === "/api/connectors", route => route.fulfill({ json: [{ name: "git", title: "Git", delivery: "git", scopes: ["realm"], secretTypes: ["ssh-key"], fields: [], methods: [] }, { name: "claude", title: "Claude", scopes: ["realm"], secretTypes: ["opaque"], fields: [], methods: [{ name: "subscription", component: "claude-sub", delivery: "stateVolume" }, { name: "api-key", component: "claude-key", delivery: "env" }] }] }));
  await page.route(url => url.pathname.startsWith("/api/teams") || url.pathname.startsWith("/api/sources") || url.pathname === "/api/team-defaults", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const body = request.method() === "POST" ? request.postDataJSON() : undefined;
    if (body !== undefined) world.requests.push({ path, body });
    if (request.method() === "GET") world.requests.push({ path: path + url.search });
    if (request.method() === "GET" && path === "/api/teams") return route.fulfill({ json: { teams: world.teams } });
    if (request.method() === "GET" && path === "/api/sources") return route.fulfill({ json: world.sources });
    if (request.method() === "GET" && path.endsWith("/export")) {
      const host = url.searchParams.get("hostFields") === "1";
      return route.fulfill({ json: { document: `apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: web\n${host ? "  realm: default\n" : ""}spec:\n  projectSource:\n    name: project\n    url: example.test/web\n` } });
    }
    if (request.method() === "GET" && path.startsWith("/api/teams/")) {
      const team = world.teams.find(item => item.name === decodeURIComponent(path.split("/")[3]));
      if (!team) return route.fulfill({ status: 404, json: { error: { code: "not_found", message: "team not found" } } });
      const web = team.name === "web";
      return route.fulfill({ json: { summary: team, document: `apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: ${team.name}\n  realm: default\n`, view: web ? world.webView : { ...world.webView, tasks: [], configs: [] }, form: web ? world.webForm() : { ...world.webForm(), name: team.name } } });
    }
    if (path === "/api/teams/draft") return route.fulfill({ json: world.draft(body) });
    if (path === "/api/teams/preview") return route.fulfill({ json: { plan: world.plan(body.name || "web", body.form ? { kind: "file", detail: "form" } : { kind: "file", detail: "declaration" }) } });
    if (path === "/api/teams") {
      if (body.source && world.exists && !body.force) return route.fulfill({ status: 409, json: { error: { code: "already_exists", message: "create team: team already exists: web; run kuke team apply web to recompose it from its stored declaration, or create it again with --force to replace the declaration" } } });
      return route.fulfill({ json: { plan: world.plan(body.name || "web", { kind: "file" }), result: { team: body.name || "web", resources: 4, pruned: [], missingSecrets: [], notes: [] } } });
    }
    if (path.endsWith("/reapply")) return route.fulfill({ json: { plan: world.plan("web", { kind: "stored" }), result: { team: "web", resources: 4, pruned: [], missingSecrets: [], notes: [] } } });
    if (path.endsWith("/delete") && path.startsWith("/api/teams/")) return route.fulfill({ json: { deleted: true, removed: [{ kind: "CellConfig", name: "web-dev-claude", action: "deleted" }], liveCells: ["default/web/default/web-dev-claude-1"], keptVolumes: ["default/web/default/web-dev-claude-state"] } });
    if (path === "/api/sources") return route.fulfill({ json: { action: "created", source: { name: body.name, url: body.url, branch: body.branch ?? "main", identity: body.identity }, usedBy: [], discovered: { source: { name: body.name, url: body.url }, roles: [{ name: "pm", path: "pm", harnesses: [], tasks: [], default: false }], harnesses: [] } } });
    if (path === "/api/sources/agents/delete") return route.fulfill({ status: 409, json: { error: { code: "in_use", message: "source agents is referenced by team web (role dev), default role dev" } } });
    if (path.endsWith("/inspect")) return route.fulfill({ json: { source: world.sources.sources[0], commit: "fedcba9876543210fedcba9876543210fedcba98", roles: [{ name: "dev", path: "dev", harnesses: ["claude"], tasks: [{ name: "pick-issue", schedule: "@hourly" }], default: true }, { name: "pm", path: "pm", harnesses: [], tasks: [], default: false }], harnesses: [] } });
    if (path === "/api/team-defaults") return route.fulfill({ json: { action: "updated", inheriting: ["web"] } });
    return route.fulfill({ status: 404, json: { error: { code: "not_found", message: path } } });
  });
  return world;
}
const wizard = page => page.getByRole("region", { name: /New team wizard|Edit team form/ });
const lastPost = (world, path) => world.requests.filter(item => item.path === path && item.body).at(-1)?.body;

test("New team reads a repository with a git identity, walks the steps, previews and adds the team, asking before a replace", async ({ page }) => {
  const world = await mockTeams(page);
  await page.goto(`${server.url}#/resources`);
  await nav(page).getByRole("link", { name: "Teams" }).click();
  await expect(page).toHaveURL(/#\/teams$/);
  const list = page.getByRole("region", { name: "Teams list" });
  await expect(list.getByRole("row")).toHaveCount(3);
  await expect(list.getByRole("row").filter({ hasText: "kuke" }).first()).toContainText("system");
  await list.getByRole("link", { name: "New team" }).click();
  await expect(page).toHaveURL(/#\/new-team$/);
  await expect(page.locator(".page-heading h1")).toHaveText("New team");
  await expect(nav(page).getByRole("link", { name: "Teams" })).toHaveAttribute("aria-current", "page");

  const steps = wizard(page);
  await expect(steps.getByRole("button", { name: "Next" })).toBeDisabled();
  await steps.getByRole("textbox", { name: /^Repository address/ }).fill("github.com/acme/web");
  await steps.getByLabel("Git identity").selectOption("git-ssh");
  await expect(steps.getByLabel("Git identity").locator("option")).toHaveText(["Default", "git-ssh"]);
  await steps.getByRole("button", { name: "Read repository" }).click();
  await expect(steps.getByRole("status", { name: "Repository read" })).toContainText("repository file (kuketeam.yaml at 0123456789ab)");
  expect(lastPost(world, "/api/teams/draft")).toEqual({ source: "github.com/acme/web", identity: "git-ssh", useRepoFile: true });
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("heading", { name: "Other sources" })).toBeVisible();
  await expect(steps).toContainText("new Source");
  await steps.getByRole("button", { name: "Next" }).click();

  await expect(steps.getByRole("heading", { name: "Roles and harnesses" })).toBeVisible();
  await expect(steps.getByRole("checkbox", { name: "Role dev from project" })).toBeChecked();
  await steps.getByLabel("Image needs").fill("go, node");
  await steps.getByRole("button", { name: "Next" }).click();

  await expect(steps.getByRole("heading", { name: "Tasks and schedules" })).toBeVisible();
  await expect(steps.getByLabel("Cron of dev task pick-issue")).toHaveAttribute("placeholder", "@hourly");
  await steps.getByLabel("Schedule of dev task sweep").selectOption("on");
  await steps.getByLabel("Cron of dev task pick-issue").fill("not cron");
  await expect(steps.getByRole("button", { name: "Next" })).toBeDisabled();
  await steps.getByLabel("Cron of dev task pick-issue").fill("*/30 * * * *");
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("heading", { name: "Credentials" })).toBeVisible();
  await steps.getByRole("button", { name: "Next" }).click();

  // The Review step previews by itself (#1745): it has no Preview button and no Cancel link.
  await expect(steps.getByRole("button", { name: "Preview" })).toHaveCount(0);
  await expect(steps.getByRole("link", { name: "Cancel" })).toHaveCount(0);
  const preview = steps.getByRole("region", { name: "Preview" });
  await expect(preview.getByRole("table", { name: "Role and harness pairs" })).toContainText("pick-issue (@hourly)");
  await expect(preview.getByRole("list", { name: "Missing secrets" })).toContainText("gh-token");
  const sent = lastPost(world, "/api/teams/preview");
  expect(sent.declaration).toBe("opaque draft declaration\n");
  expect(sent).toMatchObject({ name: "web", source: "github.com/acme/web", identity: "git-ssh" });
  expect(sent.form.roles.items[0]).toEqual({ name: "dev", source: "project", path: "roles/dev", image: ["go", "node"], tasks: { all: false, items: [{ name: "pick-issue", suspend: null, schedule: "*/30 * * * *" }, { name: "sweep", suspend: false }] } });
  expect(sent.form.harnesses).toEqual({ inherit: true, items: [] });

  await steps.getByRole("button", { name: "Add team" }).click();
  await expect(steps.getByRole("alert").first()).toContainText("kuke team apply web");
  await steps.getByRole("button", { name: "Replace the stored declaration" }).click();
  await expect(page).toHaveURL(/#\/teams\/web$/);
  expect(lastPost(world, "/api/teams")).toMatchObject({ name: "web", force: true, declaration: "opaque draft declaration\n" });
});

test("New team from a pasted file shows a conflicting source until it is stored under another name", async ({ page }) => {
  const world = await mockTeams(page);
  await page.goto(`${server.url}#/new-team`);
  const steps = wizard(page);
  await steps.getByLabel("A pasted kuketeam.yaml").check();
  const file = "apiVersion: v1beta1\nkind: Team\nmetadata: {name: acme}\nspec:\n  projectSource: {url: github.com/acme/app}\n";
  await steps.getByRole("textbox", { name: /^kuketeam\.yaml/ }).fill(file);
  await steps.getByRole("button", { name: "Read repository" }).click();
  await expect(steps.getByRole("status", { name: "Repository read" })).toContainText("file (declaration)");
  expect(lastPost(world, "/api/teams/draft")).toEqual({ declaration: file });
  await steps.getByRole("button", { name: "Next" }).click();
  const shared = steps.getByRole("group", { name: "Source shared" });
  await expect(shared).toContainText("conflict");
  await expect(shared).toContainText("github.com/other/shared");
  await expect(steps.getByRole("list", { name: "Step problems" })).toContainText("store this one under another name");
  await expect(steps.getByRole("button", { name: "Next" })).toBeDisabled();
  await shared.getByLabel("Stored as").fill("acme-shared");
  await expect(steps.getByRole("button", { name: "Next" })).toBeEnabled();
});

test("a team page shows its project, sources, roles, tasks, harnesses and connectors with declared or inherited, and deletes after a confirmation", async ({ page }) => {
  const world = await mockTeams(page);
  await page.goto(`${server.url}#/teams/web`);
  await expect(page.locator(".page-heading h1")).toHaveText("web");
  await expect(page.getByRole("textbox", { name: "Team declaration YAML" })).toHaveCount(0);
  const panels = await content(page).locator("section.panel > .panel-heading h2").allTextContents();
  expect(panels.slice(panels.indexOf("Overview"))).toEqual(["Overview", "Project", "Sources", "Roles", "Tasks", "Harnesses", "Connectors", "Configs"]);
  const project = page.getByRole("region", { name: "Project" });
  await expect(project.getByRole("link", { name: "web" })).toHaveAttribute("href", "#/sources/web");
  await expect(project).toContainText("git-ssh");
  await expect(page.getByRole("region", { name: "Sources" }).locator(".team-origin")).toHaveText("inherited");
  await expect(page.getByRole("region", { name: "Sources" }).getByRole("link", { name: "agents" })).toHaveAttribute("href", "#/sources/agents");
  const roles = page.getByRole("region", { name: "Roles" });
  await expect(roles.locator(".team-origin")).toHaveText("declared");
  await expect(roles.getByRole("table")).toContainText("go");
  await expect(page.getByRole("region", { name: "Harnesses" }).locator(".team-origin")).toHaveText("inherited");
  const task = page.getByRole("region", { name: "Task dev health-sweep" });
  await expect(task).toContainText("Suspended");
  await expect(task).toContainText("@hourly");
  await expect(task.getByRole("link", { name: "web-dev-claude-health-sweep" })).toHaveAttribute("href", "#/configs/default/web/default/web-dev-claude-health-sweep");
  const overview = page.getByRole("region", { name: "Team overview" });
  await expect(overview.getByRole("link", { name: "Edit" })).toHaveAttribute("href", "#/teams/web/edit");
  await expect(overview.getByRole("link", { name: "Export" })).toHaveAttribute("href", "#/teams/web/export");
  await overview.getByRole("button", { name: "Reapply" }).click();
  await expect(page.getByRole("status").filter({ hasText: "applied: 4 resources" })).toBeVisible();
  expect(lastPost(world, "/api/teams/web/reapply")).toEqual({});

  await overview.getByRole("button", { name: "Delete" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete team" });
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Confirm Delete" }).click();
  const deleted = page.getByRole("region", { name: "Deleted team" });
  await expect(deleted).toContainText("CellConfig web-dev-claude");
  await expect(deleted).toContainText("default/web/default/web-dev-claude-state");
  expect(lastPost(world, "/api/teams/web/delete")).toEqual({});
});

test("the kuke and default teams cannot be deleted from their pages", async ({ page }) => {
  const world = await mockTeams(page);
  await page.goto(`${server.url}#/teams/kuke`);
  const overview = page.getByRole("region", { name: "Team overview" });
  await expect(overview.getByRole("button", { name: "Reapply" })).toBeVisible();
  await expect(overview.getByRole("button", { name: "Delete" })).toHaveCount(0);
  await expect(overview).toContainText("The kuke system team cannot be deleted; reset it with kuke team init --force");
  // The default team (#1692) is a system team too, and the note names it.
  world.teams.push({ ...world.teams[0], name: "default", roles: ["agent"], configs: ["default-agent-claude", "default-agent-codex"] });
  await page.goto(`${server.url}#/teams/default`);
  await expect(overview.getByRole("button", { name: "Delete" })).toHaveCount(0);
  await expect(overview).toContainText("The default system team cannot be deleted; reset it with kuke team init --force");
});

test("Edit team switches one schedule off and moves the project ref without YAML", async ({ page }) => {
  const world = await mockTeams(page);
  await page.goto(`${server.url}#/teams/web/edit`);
  await expect(page.locator(".page-heading h1")).toHaveText("Edit team");
  await expect(page.locator(".breadcrumbs")).toContainText("Teams");
  await expect(page.locator(".breadcrumbs").getByRole("link", { name: "web" })).toHaveAttribute("href", "#/teams/web");
  const steps = wizard(page);
  await expect(steps.getByRole("heading", { name: "Other sources" })).toBeVisible();
  await steps.getByLabel("Project ref", { exact: true }).fill("release");
  await steps.getByRole("button", { name: "Next" }).click();
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByLabel("Schedule of dev task health-sweep")).toHaveValue("role");
  await expect(steps.getByLabel("Schedule of dev task health-sweep").locator("option").first()).toHaveText("The role's (off)");
  // A role on one harness has no per-harness switch (#1668).
  await expect(steps.getByLabel(/^Schedule of dev task health-sweep on /)).toHaveCount(0);
  await steps.getByLabel("Schedule of dev task health-sweep").selectOption("on");
  await steps.getByRole("button", { name: "Next" }).click();
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("region", { name: "Preview" })).toBeVisible();
  const sent = lastPost(world, "/api/teams/preview");
  expect(Object.keys(sent).sort()).toEqual(["form", "name"]);
  expect(sent.form.project).toEqual({ alias: "project", stored: "web", refKind: "branch", ref: "release", clonePath: "/home/user/web" });
  expect(sent.form.roles.items[0].tasks).toEqual({ all: false, items: [{ name: "health-sweep", suspend: false }] });
  expect(sent.form.sources).toBeNull();
  await steps.getByRole("button", { name: "Apply" }).click();
  await expect(page).toHaveURL(/#\/teams\/web$/);
  expect(lastPost(world, "/api/teams")).toEqual(sent);
});

// #1668, AC8 and AC9: a task of a role on two harnesses gets one schedule switch per harness, and the team page
// names the harness of each task Config.
test("Edit team switches a task's schedule on for one harness of a two-harness role", async ({ page }) => {
  const world = await mockTeams(page);
  world.webView.harnesses.items.push({ name: "codex", source: "kukeon", path: "team/harnesses/codex" });
  const sweep = harness => ({ role: "dev", task: "health-sweep", harness, config: `web-dev-${harness}-health-sweep`, scope: ["default", "web", "default"], schedule: { cron: "@hourly", suspend: true } });
  world.webView.tasks = [sweep("claude"), sweep("codex")];

  await page.goto(`${server.url}#/teams/web`);
  for (const harness of ["claude", "codex"]) {
    const task = page.getByRole("region", { name: `Task dev health-sweep on ${harness}` });
    await expect(task.getByRole("heading")).toHaveText(`dev / health-sweep on ${harness} web-dev-${harness}-health-sweep`);
  }

  await page.goto(`${server.url}#/teams/web/edit`);
  const steps = wizard(page);
  await expect(steps.getByRole("heading", { name: "Other sources" })).toBeVisible();
  await steps.getByRole("button", { name: "Next" }).click();
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("heading", { name: "Tasks and schedules" })).toBeVisible();
  const claude = steps.getByLabel("Schedule of dev task health-sweep on claude");
  const codex = steps.getByLabel("Schedule of dev task health-sweep on codex");
  await expect(claude).toHaveValue("same");
  await expect(codex).toHaveValue("same");
  await expect(claude.locator("option")).toHaveText(["Same as the task", "On", "Off"]);
  await claude.selectOption("on");
  await expect(codex).toHaveValue("same");
  // Deselecting the task disables its switches.
  await steps.getByLabel("Select dev task health-sweep").uncheck();
  await expect(codex).toBeDisabled();
  await steps.getByLabel("Select dev task health-sweep").check();
  await steps.getByLabel("Schedule of dev task health-sweep on claude").selectOption("on");
  await steps.getByRole("button", { name: "Next" }).click();
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("region", { name: "Preview" })).toBeVisible();
  const sent = lastPost(world, "/api/teams/preview");
  expect(sent.form.roles.items[0].tasks).toEqual({ all: false, items: [{ name: "health-sweep", suspend: null, harnesses: { claude: { suspend: false } } }] });
  await steps.getByRole("button", { name: "Apply" }).click();
  await expect(page).toHaveURL(/#\/teams\/web$/);
  expect(lastPost(world, "/api/teams")).toEqual(sent);
});

test("Edit as YAML edits the whole declaration with a preview before apply", async ({ page }) => {
  const world = await mockTeams(page);
  await page.goto(`${server.url}#/teams/web/edit`);
  await page.getByRole("navigation", { name: "Edit team" }).getByRole("link", { name: "Edit as YAML" }).click();
  const declaration = page.getByRole("region", { name: "Team declaration" });
  await expect(declaration.getByRole("button", { name: "Apply" })).toBeDisabled();
  const yaml = "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: web\nspec:\n  roles: []\n";
  await declaration.getByLabel("Team declaration YAML").fill(yaml);
  await declaration.getByRole("button", { name: "Preview" }).click();
  await expect(declaration.getByRole("region", { name: "Preview" })).toContainText("web-dev-claude");
  expect(lastPost(world, "/api/teams/preview")).toEqual({ name: "web", declaration: yaml });
  await declaration.getByRole("button", { name: "Apply" }).click();
  await expect(page).toHaveURL(/#\/teams\/web$/);
  expect(lastPost(world, "/api/teams")).toEqual({ name: "web", declaration: yaml });
});

test("Export shows the team as a kuketeam.yaml with its two options, Copy and Download", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const world = await mockTeams(page);
  await page.goto(`${server.url}#/teams/web/export`);
  await expect(page.locator(".page-heading h1")).toHaveText("Export");
  const exported = page.getByLabel("Exported kuketeam.yaml");
  await expect(exported).toContainText("url: example.test/web");
  await expect(exported).not.toContainText("realm: default");
  await page.getByLabel(/Keep host fields/).check();
  await expect(exported).toContainText("realm: default");
  await page.getByLabel(/Write inherited lists out/).check();
  expect(world.requests.map(item => item.path).filter(path => path.includes("/export"))).toEqual(["/api/teams/web/export", "/api/teams/web/export?hostFields=1", "/api/teams/web/export?hostFields=1&explicit=1"]);
  await expect(page.getByRole("link", { name: "Download" })).toHaveAttribute("download", "kuketeam.yaml");
  await page.getByRole("button", { name: "Copy" }).click();
  await expect(page.getByRole("button", { name: "Copied" })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain("kind: Team");
});

test("the Sources page lists, creates, shows contents, toggles a default and reports a refused delete", async ({ page }) => {
  const world = await mockTeams(page);
  await page.goto(`${server.url}#/teams`);
  await nav(page).getByRole("link", { name: "Sources" }).click();
  await expect(page).toHaveURL(/#\/sources$/);
  await expect(page.locator(".page-heading h1")).toHaveText("Sources");
  const list = page.getByRole("region", { name: "Sources list" });
  const agents = list.getByRole("row").filter({ hasText: "agents" });
  await expect(agents).toContainText("branch main");
  await expect(agents.getByRole("link", { name: "web" })).toHaveAttribute("href", "#/teams/web");
  await expect(agents).toContainText("the defaults");
  await expect(list.getByRole("row").filter({ hasText: "kukeon" })).toContainText("git-ssh");

  const create = page.getByRole("region", { name: "New source" });
  await create.getByLabel("Name").fill("tools");
  await create.getByLabel("Address").fill("github.com/acme/tools");
  await create.getByLabel("Git identity").selectOption("git-ssh");
  await create.getByRole("button", { name: "Store source" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Source tools created" })).toContainText("offers 1 roles");
  expect(lastPost(world, "/api/sources")).toEqual({ name: "tools", url: "github.com/acme/tools", identity: "git-ssh" });

  await agents.getByRole("link", { name: "agents" }).click();
  await expect(page).toHaveURL(/#\/sources\/agents$/);
  const overview = page.getByRole("region", { name: "Source overview" });
  await expect(overview.getByRole("list", { name: "Used by" })).toContainText("team web");
  await overview.getByRole("button", { name: "Show contents" }).click();
  const contents = page.getByRole("region", { name: "Source contents" });
  await expect(contents.getByRole("table", { name: "Roles in agents" })).toContainText("pick-issue (@hourly)");
  await expect(contents.getByRole("checkbox", { name: "Default role dev" })).toBeChecked();
  await contents.getByRole("checkbox", { name: "Default role pm" }).click();
  expect(lastPost(world, "/api/team-defaults")).toEqual({ roles: ["dev", "pm=agents:pm"] });
  await expect(page.getByRole("status").filter({ hasText: "Defaults updated" })).toContainText("Teams inheriting from the defaults: web.");
  await overview.getByRole("button", { name: "Delete" }).click();
  await expect(content(page).locator(".error[role=alert]")).toContainText("referenced by team web (role dev)");
  await expect(page).toHaveURL(/#\/sources\/agents$/);
});

test("New Cell fills a repository from a stored Source and shows its identity", async ({ page }) => {
  await mockTeams(page);
  await page.goto(`${server.url}#/new-cell`);
  await page.getByRole("group", { name: "Cell creation method" }).getByRole("button", { name: "Manual" }).click();
  await page.getByRole("button", { name: "Add container" }).click();
  await page.getByRole("button", { name: "Show advanced container fields" }).click();
  await page.getByRole("button", { name: "Add repository" }).click();
  await page.getByLabel("From source for Repository 1").selectOption("kukeon");
  const repo = page.locator(".schema-array-item").filter({ hasText: "Repository 1" });
  await expect(repo).toContainText("Identity: git-ssh");
  await expect(repo.getByRole("textbox", { name: /^Name/ })).toHaveValue("kukeon");
  await expect(repo.getByRole("textbox", { name: /^Repository URL/ })).toHaveValue("github.com/eminwux/kukeon");
  await expect(repo.getByLabel("Tag or commit ref")).toHaveValue("v1");
  await expect(repo.getByLabel("Branch")).toHaveValue("");
});

test("a team-owned Config's page links to its team", async ({ page }) => {
  await mockTeams(page);
  await page.route(url => url.pathname === "/api/catalog/configs/web-dev-claude", route => route.fulfill({ json: {
    kind: "CellConfig", metadata: { name: "web-dev-claude", realm: "default", space: "web", stack: "default", labels: { "kukeon.io/team": "web" } }, spec: { blueprint: { name: "web-dev" } },
  } }));
  await page.goto(`${server.url}#/configs/default/web/default/web-dev-claude`);
  await expect(page.getByRole("heading", { name: "Resource overview" })).toBeVisible();
  await expect(page.locator(".details-grid").getByRole("link", { name: "web", exact: true })).toHaveAttribute("href", "#/teams/web");
});

async function mockSecretWrites(page, writes, handler) {
  await page.route("**/api/catalog/secrets/*", async route => {
    const request = route.request();
    if (request.method() !== "POST") return route.fallback();
    const url = new URL(request.url());
    const body = request.postDataJSON();
    const name = decodeURIComponent(url.pathname.split("/").at(-1));
    writes.push({ path: url.pathname + url.search, body });
    const custom = handler?.(name, body);
    if (custom) return route.fulfill(custom);
    const scope = ["realm", "space", "stack", "cell"].map(key => url.searchParams.get(key) ?? "");
    await route.fulfill({ json: { name, scope, type: body.type, action: body.replace ? "updated" : "created" } });
  });
}
test("New Secret creates an untagged Secret of each type in a chosen scope, refuses an existing name and keeps material write-only", async ({ page }) => {
  await mockCatalog(page);
  const writes = [];
  const value = "n3w-s3cret-value";
  await mockSecretWrites(page, writes, name => name === "taken" ? { status: 409, json: { error: { code: "already_exists", message: "secret already exists: dev / taken" } } } : undefined);
  await page.goto(`${server.url}#/secrets`);
  await page.getByRole("link", { name: "New Secret" }).click();
  await expect(page).toHaveURL(/#\/new-secret$/);
  await expect(page.locator(".page-heading h1")).toHaveText("New Secret");
  await expect(nav(page).getByRole("link", { name: "Secrets" })).toHaveAttribute("aria-current", "page");
  const form = page.getByRole("form", { name: "New Secret" });
  await expect(form.getByLabel("Type")).toHaveValue("opaque");
  await expect(form.getByRole("option")).toHaveText(["opaque", "basic-auth", "ssh-key"]);
  await form.getByLabel("Name").fill("token");
  await form.getByLabel("Value").fill(value);
  await form.getByRole("button", { name: "Create Secret" }).click();
  await expect(form.getByRole("alert")).toContainText("Choose where the Secret is stored.");
  expect(writes).toHaveLength(0);
  await page.getByLabel("Secret scope realm").selectOption("prod");
  await page.getByLabel("Secret scope space").selectOption("shared");
  await form.getByLabel("Name").fill("token");
  await form.getByLabel("Value").fill(value);
  await form.getByRole("button", { name: "Create Secret" }).click();
  await expect(page).toHaveURL(/#\/secrets\/prod\/shared\/token$/);
  await expect(toasts(page).filter({ hasText: "Secret token created in prod / shared." })).toBeVisible();
  expect(writes[0]).toEqual({ path: "/api/catalog/secrets/token?realm=prod&space=shared", body: { type: "opaque", value } });

  await page.goto(`${server.url}#/new-secret`);
  await page.getByLabel("Secret scope realm").selectOption("dev");
  await form.getByLabel("Name").fill("login");
  await form.getByLabel("Type").selectOption("basic-auth");
  await expect(form.getByLabel("Password or token")).toHaveAttribute("type", "password");
  await form.getByLabel("Username").fill("bot");
  await form.getByLabel("Password or token").fill(value);
  await form.getByRole("button", { name: "Create Secret" }).click();
  await expect(page).toHaveURL(/#\/secrets\/dev\/login$/);
  expect(writes[1]).toEqual({ path: "/api/catalog/secrets/login?realm=dev", body: { type: "basic-auth", username: "bot", password: value } });

  await page.goto(`${server.url}#/new-secret`);
  await page.getByLabel("Secret scope realm").selectOption("dev");
  await page.getByLabel("Secret scope space").selectOption("shared");
  await page.getByLabel("Secret scope stack").selectOption("services");
  await form.getByLabel("Name").fill("deploy");
  await form.getByLabel("Type").selectOption("ssh-key");
  // The PEM header is joined at run time so the detect-private-key hook does not
  // read this test value as a committed key.
  const sshKey = ["-----BEGIN OPENSSH", "PRIVATE KEY-----\n" + value + "\n-----END OPENSSH PRIVATE KEY-----"].join(" ");
  await form.getByLabel("Private key").fill(sshKey);
  await form.getByLabel("Known hosts").fill("example.com ssh-ed25519 AAAA");
  await form.getByRole("button", { name: "Create Secret" }).click();
  await expect(page).toHaveURL(/#\/secrets\/dev\/shared\/services\/deploy$/);
  expect(writes[2].path).toBe("/api/catalog/secrets/deploy?realm=dev&space=shared&stack=services");
  expect(writes[2].body).toEqual({ type: "ssh-key", privateKey: sshKey, knownHosts: "example.com ssh-ed25519 AAAA" });

  // A taken name is a 409 that links to the stored Secret; the material is already gone from the form.
  await page.goto(`${server.url}#/new-secret`);
  await page.getByLabel("Secret scope realm").selectOption("dev");
  await form.getByLabel("Name").fill("taken");
  await form.getByLabel("Value").fill(value);
  await form.getByRole("button", { name: "Create Secret" }).click();
  const alert = form.getByRole("alert");
  await expect(alert).toContainText("Secret already exists");
  await expect(alert.getByRole("link", { name: "Open it" })).toHaveAttribute("href", "#/secrets/dev/taken");
  await expect(form.getByLabel("Value")).toHaveValue("");
  expect(await page.content()).not.toContain(value);
  expect(page.url()).not.toContain(value);
  expect(await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))).not.toContain(value);
});

test("an untagged Secret, cell-scoped too, has Replace value; a connector's Secret links to its connector instead", async ({ page }) => {
  await mockCatalog(page, url => url.pathname.endsWith("/secrets/tagged") ? { json: { apiVersion: "v1beta1", kind: "Secret", metadata: { name: "tagged", realm: "dev" }, spec: { type: "basic-auth", connector: "example-token" } } } : undefined);
  const writes = [];
  const value = "r3placed-value";
  await mockSecretWrites(page, writes);
  await page.goto(`${server.url}#/secrets`);
  await expect(page.getByRole("link", { name: "Replace value of secret prod / shared / services / api / shared-name" })).toHaveAttribute("href", "#/secrets/prod/shared/services/api/shared-name?edit=1");
  await page.getByRole("link", { name: "Replace value of secret prod / shared / services / api / shared-name" }).click();
  const form = page.getByRole("form", { name: "Replace value of secret shared-name" });
  await expect(form.getByLabel("Name")).toBeDisabled();
  await expect(form.getByLabel("Name")).toHaveValue("shared-name");
  await expect(form.getByLabel("Type")).toHaveValue("opaque");
  await expect(form.getByRole("note")).toContainText("Running cells keep the value they started with.");
  await form.getByLabel("Value").fill(value);
  await form.getByRole("button", { name: "Replace value" }).click();
  await expect(toasts(page).filter({ hasText: "Secret shared-name replaced in prod / shared / services / api." })).toBeVisible();
  await expect(page).toHaveURL(/#\/secrets\/prod\/shared\/services\/api\/shared-name$/);
  await expect(form).toHaveCount(0);
  expect(writes).toEqual([{ path: "/api/catalog/secrets/shared-name?realm=prod&space=shared&stack=services&cell=api", body: { type: "opaque", replace: true, value } }]);
  expect(await page.content()).not.toContain(value);
  // Cancel leaves the page unchanged.
  await page.getByRole("region", { name: "Secret value" }).getByRole("link", { name: "Replace value" }).click();
  await page.getByRole("form", { name: "Replace value of secret shared-name" }).getByRole("button", { name: "Cancel" }).click();
  await expect(page).toHaveURL(/shared-name$/);
  expect(writes).toHaveLength(1);

  await page.goto(`${server.url}#/secrets/dev/tagged?edit=1`);
  const overview = page.locator(".details-grid");
  await expect(overview.locator("dt:text-is('Type') + dd")).toHaveText("basic-auth");
  await expect(overview.getByRole("link", { name: "example-token" })).toHaveAttribute("href", "#/connectors/example-token");
  const valuePanel = page.getByRole("region", { name: "Secret value" });
  await expect(valuePanel).toContainText("identity of connector example-token");
  await expect(valuePanel.getByRole("link", { name: "Replace value" })).toHaveCount(0);
  await expect(page.getByRole("form")).toHaveCount(0);
});

// #1630 B: which cells mount a Volume. dev/shared/data shadows dev/data for a bare `data` source in dev/shared.
test("Volumes show reclaim policy and mount counts, and a Volume page lists mounting cells and login Configs", async ({ page }) => {
  const volume = (name, scope, reclaimPolicy) => ({ apiVersion: "v1beta1", kind: "Volume", metadata: { name, realm: scope[0], ...(scope[1] ? { space: scope[1] } : {}), ...(scope[2] ? { stack: scope[2] } : {}) }, spec: reclaimPolicy ? { reclaimPolicy } : {} });
  const volumes = [volume("data", ["dev"]), volume("data", ["dev", "shared"], "Retain"), volume("cache", ["dev", "shared", "services"])];
  const loginConfig = { apiVersion: "v1beta1", kind: "CellConfig", metadata: { name: "agent", realm: "dev" }, spec: {}, status: { connectors: [{ connector: "harness", title: "Harness", delivery: "stateVolume", state: "resolved", volumeRef: { name: "data", realm: "dev", space: "shared" } }] } };
  await mockCatalog(page, url => {
    if (url.pathname === "/api/catalog/volumes") return { json: volumes };
    if (url.pathname === "/api/catalog/configs") return { json: [loginConfig] };
    if (url.pathname.startsWith("/api/catalog/volumes/")) {
      const scope = ["realm", "space", "stack"].map(key => url.searchParams.get(key)).filter(Boolean);
      const found = volumes.find(item => item.metadata.name === decodeURIComponent(url.pathname.split("/").at(-1)) && JSON.stringify([item.metadata.realm, item.metadata.space, item.metadata.stack].filter(Boolean)) === JSON.stringify(scope));
      return found ? { json: found } : { status: 404, json: { error: { code: "not_found", message: "volume not found" } } };
    }
    return undefined;
  });
  const cell = (name, state, containers) => ({ ...doc(["dev", "shared", "services", name]), spec: { containers }, status: { state } });
  await page.route("**/api/realms/dev/spaces/shared/stacks/services/cells", route => route.fulfill({ json: [
    cell("api", "Ready", [{ id: "web", volumes: [{ kind: "volume", source: "data", target: "/data" }, { kind: "bind", source: "/tmp", target: "/tmp" }] }]),
    cell("worker", "Stopped", [{ id: "job", volumes: [{ kind: "volume", volumeRef: { name: "data", realm: "dev" }, target: "/in", readOnly: true, ensure: true }] }]),
  ] }));
  await page.goto(`${server.url}#/volumes`);
  const list = page.getByRole("region", { name: "Volumes list" });
  await expect(list.getByRole("columnheader")).toHaveText(["", "Name", "Scope", "Reclaim policy", "Mounted by", "Actions"]);
  const row = scope => list.getByRole("row").filter({ has: page.getByRole("cell", { name: scope, exact: true }) });
  await expect(row("dev").getByRole("cell").nth(3)).toHaveText("Delete");
  await expect(row("dev").getByRole("cell").nth(4)).toHaveText("1");
  await expect(row("dev / shared").getByRole("cell").nth(3)).toHaveText("Retain");
  await expect(row("dev / shared").getByRole("cell").nth(4)).toHaveText("1");
  await expect(row("dev / shared / services").getByRole("cell").nth(4)).toHaveText("0");

  await page.goto(`${server.url}#/volumes/dev/shared/data`);
  await expect(page.locator(".details-grid dt")).toHaveText(["Name", "Scope", "Kind", "Reclaim policy"]);
  const mounted = page.getByRole("region", { name: "Mounted by" });
  await expect(mounted.getByRole("row")).toHaveCount(2);
  await expect(mounted.getByRole("row").nth(1).getByRole("cell")).toHaveText(["apidev / shared / services · by name", "Ready", "web", "/data", "Read-write", "No"]);
  await expect(mounted.getByRole("link", { name: "api" })).toHaveAttribute("href", "#/resource/dev/shared/services/api");
  await expect(page.getByRole("region", { name: "Harness state for" }).getByRole("link", { name: "agent" })).toHaveAttribute("href", "#/configs/dev/agent");

  // The realm Volume is mounted by explicit reference, by a stopped cell.
  await page.goto(`${server.url}#/volumes/dev/data`);
  await expect(page.getByRole("region", { name: "Mounted by" }).getByRole("row").nth(1).getByRole("cell")).toHaveText(["workerdev / shared / services", "Stopped", "job", "/in", "Read-only", "Yes (ensure)"]);
  await expect(page.getByRole("region", { name: "Harness state for" })).toContainText("No Config keeps harness state in this Volume.");

  await page.goto(`${server.url}#/volumes`);
  await row("dev / shared").getByRole("button", { name: /Delete volume/ }).click();
  const dialog = page.getByRole("dialog", { name: "Delete volume" });
  await expect(dialog).toContainText("kukeond refuses the delete while a Ready or Degraded cell mounts the Volume.");
  await expect(dialog.getByRole("link", { name: "api" })).toBeVisible();
});

// #1630 E: schedules on Configs and on the cells they fire.
test("a scheduled Config shows its Schedule panel and list columns, and its cells say they are scheduled runs", async ({ page }) => {
  const at = ["dev", "shared", "services"];
  const sweep = { apiVersion: "v1beta1", kind: "CellConfig", metadata: { name: "sweep", realm: at[0], space: at[1], stack: at[2] }, spec: { blueprint: { name: "template", realm: "library" }, schedule: { cron: "*/15 * * * *", timezone: "Europe/Madrid", startingDeadlineSeconds: 300 } },
    status: { schedule: { nextScheduleTime: "2026-11-23T10:15:00Z", lastScheduleTime: "2025-11-23T10:00:00Z", lastCell: "sweep-1763892000", activeCells: ["sweep-1763892000", "sweep-1763891100"], skippedFires: 2, lastError: "" } } };
  const paused = { ...sweep, metadata: { ...sweep.metadata, name: "paused" }, spec: { ...sweep.spec, schedule: { cron: "@daily", suspend: true } }, status: {} };
  const plain = { ...sweep, metadata: { ...sweep.metadata, name: "plain" }, spec: { blueprint: sweep.spec.blueprint }, status: {} };
  const configs = [sweep, paused, plain];
  await mockCatalog(page, url => {
    if (url.pathname === "/api/catalog/configs") return { json: configs };
    const found = configs.find(item => url.pathname === `/api/catalog/configs/${item.metadata.name}`);
    return found ? { json: found } : undefined;
  });
  const run = { ...doc([...at, "sweep-1763892000"]), metadata: { name: "sweep-1763892000", labels: { "kukeon.io/schedule": "sweep" } }, spec: { realmId: at[0], spaceId: at[1], stackId: at[2], provenance: { bindingKind: "config", bindingRef: { name: "sweep", realm: at[0], space: at[1], stack: at[2] } } } };
  await page.route("**/api/realms/dev/spaces/shared/stacks/services/cells", route => route.fulfill({ json: [doc([...at, "api"]), run] }));
  await page.route("**/api/realms/dev/spaces/shared/stacks/services/cells/sweep-1763892000", route => route.fulfill({ json: run }));

  await page.goto(`${server.url}#/configs`);
  const list = page.getByRole("region", { name: "Configs list" });
  await expect(list.getByRole("columnheader")).toHaveText(["", "Name", "Scope", "Schedule", "Next fire", "Actions"]);
  const row = name => list.getByRole("row").filter({ has: page.getByRole("link", { name, exact: true }) });
  await expect(row("sweep").getByRole("cell").nth(3)).toHaveText("*/15 * * * * (Europe/Madrid)");
  await expect(row("sweep").getByRole("cell").nth(4)).toHaveText("2026-11-23T10:15:00Z");
  await expect(row("paused").getByRole("cell").nth(3)).toHaveText("@daily");
  await expect(row("paused").getByRole("cell").nth(4)).toHaveText("suspended");
  await expect(row("plain").getByRole("cell").nth(3)).toHaveText("—");

  await page.goto(`${server.url}#/configs/dev/shared/services/sweep`);
  const panel = page.getByRole("region", { name: "Schedule" });
  const value = label => panel.locator(`dt:text-is('${label}') + dd`);
  await expect(panel.locator("dt")).toHaveText(["State", "Cron", "Concurrency policy", "Starting deadline", "Next fire", "Last fire", "Last cell", "Active cells", "Skipped fires", "Last error"]);
  await expect(value("State")).toHaveText("Scheduled");
  await expect(value("Cron")).toHaveText("*/15 * * * * (Europe/Madrid)");
  await expect(value("Concurrency policy")).toContainText("Forbid");
  await expect(value("Starting deadline")).toContainText("300 seconds");
  await expect(value("Last cell").getByRole("link", { name: "sweep-1763892000" })).toHaveAttribute("href", "#/resource/dev/shared/services/sweep-1763892000");
  await expect(value("Active cells")).toContainText("sweep-1763891100 (deleted)");
  await expect(value("Skipped fires")).toHaveText("2");
  await expect(value("Last error")).toHaveText("None");
  await expect(panel).toContainText("edited in the Config document");
  await expect(panel.getByRole("button")).toHaveCount(0);

  await page.goto(`${server.url}#/configs/dev/shared/services/paused`);
  await expect(page.getByRole("region", { name: "Schedule" }).locator("dt:text-is('State') + dd")).toHaveText("Suspended");
  await expect(page.getByRole("region", { name: "Schedule" }).locator("dt:text-is('Next fire') + dd")).toHaveText("— (suspended)");
  await page.goto(`${server.url}#/configs/dev/shared/services/plain`);
  await expect(page.locator(".details-grid dt:text-is('Schedule') + dd")).toHaveText("None");
  await expect(page.getByRole("region", { name: "Schedule" })).toHaveCount(0);

  await page.goto(`${server.url}#/resource/dev/shared/services/sweep-1763892000`);
  const schedule = page.locator(".resource-overview dt:text-is('Schedule') + dd");
  await expect(schedule).toContainText("Scheduled run of sweep, fired");
  await expect(schedule.locator("time").first()).toHaveAttribute("datetime", "2025-11-23T10:00:00.000Z");
  await expect(schedule).toContainText("sweep runs */15 * * * * (Europe/Madrid) · next");
  await expect(schedule.getByRole("link", { name: "sweep" }).first()).toHaveAttribute("href", "#/configs/dev/shared/services/sweep");

  await page.goto(`${server.url}#/cells`);
  const cells = page.getByRole("region", { name: "Cells list" });
  await expect(cells.getByRole("row").filter({ hasText: "sweep-1763892000" }).locator(".scheduled-badge")).toHaveText("Scheduled");
  await expect(cells.getByRole("row").filter({ hasText: "api" }).locator(".scheduled-badge")).toHaveCount(0);
});

// #1630 F: the cell IP in the overview, from the one network read the Network section also uses.
test("the cell overview shows the IP address in its four states with one network read per refresh, and the terminal view reads none", async ({ page }) => {
  let reply = { json: { cell: { ipv4State: "assigned", ipv4: "10.88.1.5", rootContainer: "root" } } };
  let reads = 0;
  await mock(page, path => {
    if (path.endsWith("/cells/api/network")) { reads++; return reply; }
    return undefined;
  });
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const ip = page.locator(".resource-overview dt:text-is('IP address') + dd");
  await expect(ip).toHaveText("10.88.1.5 · root container root");
  expect(reads).toBe(1);
  for (const [cell, text] of [
    [{ ipv4State: "not-running", rootContainer: "root" }, "Not running"],
    [{ ipv4State: "host-network", hostNetwork: true, rootContainer: "root" }, "Host network (no CNI attachment)"],
    [{ ipv4State: "not-assigned", rootContainer: "root" }, "Not assigned (no CNI attachment cached)"],
  ]) {
    reply = { json: { cell } };
    const before = reads;
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(ip).toHaveText(text);
    await expect(page.getByRole("button", { name: "Refresh" })).toHaveAttribute("aria-busy", "false");
    expect(reads).toBe(before + 1);
  }
  reply = failure;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(ip).toHaveText("Unavailable");
  await expect(ip.locator("span")).toHaveAttribute("title", "Scoped read failed");
  const settled = reads;
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(page.getByRole("region", { name: "Cell terminal" })).toBeVisible();
  await page.waitForTimeout(300);
  expect(reads).toBe(settled);
});

// #1630 G: the Spaces table reads each visible Space's subnet, six at a time.
test("the Spaces table shows each Space's CIDR, distinguishing unallocated, legacy and failed reads, six reads at a time", async ({ page }) => {
  const names = ["alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"];
  const subnets = {
    alpha: { space: { subnetCIDR: "10.88.1.0/24", subnetSource: "allocator" } },
    bravo: { space: { subnetCIDR: "10.22.0.0/16", subnetSource: "conflist" } },
    charlie: { space: { subnetSource: "unallocated", parentCIDR: "10.88.0.0/16" } },
  };
  let inFlight = 0, peak = 0, total = 0;
  await mock(page, async path => {
    if (path === "/api/realms/dev/spaces") return { json: names.map(name => doc(["dev", name])) };
    const match = path.match(/^\/api\/realms\/(dev|prod)\/spaces\/([^/]+)\/network$/);
    if (!match) return undefined;
    total++; inFlight++; peak = Math.max(peak, inFlight);
    await new Promise(resolve => setTimeout(resolve, 150));
    inFlight--;
    if (match[2] === "delta") return { status: 403, json: { error: { code: "permission_denied", message: "network read denied" } } };
    return { json: subnets[match[2]] ?? { space: { subnetCIDR: "10.88.9.0/24", subnetSource: "allocator" } } };
  });
  await page.goto(`${server.url}#/spaces`);
  const list = page.getByRole("region", { name: "Spaces list" });
  await expect(list.getByRole("columnheader")).toHaveText(["", "Name", "State", "CIDR", "Scope", "Actions"]);
  const cidr = name => list.getByRole("row").filter({ has: page.getByRole("link", { name, exact: true }) }).locator("td.space-cidr");
  await expect(cidr("alpha")).toHaveText("10.88.1.0/24");
  await expect(cidr("bravo")).toHaveText("10.22.0.0/16 (legacy conflist)");
  await expect(cidr("charlie")).toHaveText("Not allocated (allocated from 10.88.0.0/16)");
  await expect(cidr("delta")).toHaveText("Unavailable");
  await expect(cidr("delta").locator("span")).toHaveAttribute("title", "network read denied");
  await expect(cidr("hotel")).toHaveText("10.88.9.0/24");
  // Eight dev Spaces and prod's one, read with at most six at a time; the failed row leaves the table whole.
  expect(total).toBe(9);
  expect(peak).toBeLessThanOrEqual(6);
  await expect(list.getByRole("row")).toHaveCount(10);
  // Only the current page is read: a filtered table reads nothing new.
  await listSearch(page).fill("alpha");
  await expect(list.getByRole("row")).toHaveCount(2);
  expect(total).toBe(9);
});

// ---------------------------------------------------------------------------------------------------
// Workbench and Manage (#1635).

const switchSide = page => page.locator(".view-switch").locator('a[aria-current="true"]');
const grid = page => page.getByRole("region", { name: "Cell terminals" });
const tile = (page, scope) => grid(page).getByRole("link", { name: `Open ${scope}`, exact: true });
const screenshotPath = scope => `/api/realms/${scope[0]}/spaces/${scope[1]}/stacks/${scope[2]}/cells/${scope[3]}/screenshot`;
const screen = (text, extra = {}) => ({ json: { container: "shell", cols: 80, rows: 24, cursorX: 0, cursorY: 0, cursorVisible: true, altScreen: false, text, ...extra } });
// Cells reported in the given states by scope key "realm/cell"; the rest keep their fixture state.
function withStates(path, states, count = 1) {
  if (!path.endsWith("/cells")) return undefined;
  return { json: fixture(path, count).map(cell => {
    const state = states[`${cell.spec.scope[0]}/${cell.metadata.name}`];
    return state ? { ...cell, status: { ...cell.status, ...(typeof state === "string" ? { state } : state) } } : cell;
  }) };
}
async function mockReports(page, reports) {
  await page.route("**/api/health/**", route => {
    const path = new URL(route.request().url()).pathname;
    if (typeof reports === "function") return route.fulfill(reports(path));
    if (path === "/api/health/reports") return route.fulfill({ json: reports.map(({ id, createdAt, findings }) => ({ id, createdAt, since: createdAt, until: createdAt, summary: `Report ${id}`, findings: findings.length, recurring: 0, filed: 0 })) });
    const id = decodeURIComponent(path.split("/").at(-1));
    const found = id === "latest" ? reports[0] : reports.find(report => report.id === id);
    return route.fulfill(found ? { json: { ...found, since: found.createdAt, until: found.createdAt, summary: `Report ${found.id}` } } : { status: 404, json: { error: { code: "not_found", message: "no report" } } });
  });
}
const finding = signature => ({ signature, classification: "kukeon", severity: "high", recurring: false });

test("each route family shows its view's sidebar and side of the switch", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  await mockCatalog(page);
  const workbench = ["#/", "#/workspace/dev/shared/services/api", "#/new-cell", "#/resource/dev/shared/services/api", "#/resource/dev/shared/services/api?document=1", "#/resource/dev/shared/services/api/web"];
  const manage = ["#/resources", "#/realms", "#/cells", "#/resource/dev", "#/resource/dev/shared/services", "#/configs", "#/new-config", "#/secrets", "#/connectors", "#/teams", "#/new-team", "#/sources", "#/images", "#/daemon", "#/daemon/logs", "#/health", "#/daemon/health"];
  // The settings routes; each one shows Profile, the only section (#1745).
  const settings = ["#/settings", "#/settings/profile", "#/settings/nothing"];
  for (const hash of [...workbench, ...manage, ...settings]) {
    const isWorkbench = workbench.includes(hash);
    const isSettings = settings.includes(hash);
    await page.goto(`${server.url}${hash}`);
    await expect(page.locator("#cell-sidebar"), hash).toHaveAttribute("aria-label", isWorkbench ? "Cell sidebar" : isSettings ? "Settings sidebar" : "Manage sidebar");
    // On a settings route neither side of the switch is current.
    if (isSettings) await expect(switchSide(page), hash).toHaveCount(0);
    else await expect(switchSide(page), hash).toHaveText(isWorkbench ? "Workbench" : "Manage");
    await expect(page.getByRole("navigation", { name: "Manage navigation" }), hash).toHaveCount(isWorkbench || isSettings ? 0 : 1);
    await expect(page.getByRole("navigation", { name: "Cell navigation" }), hash).toHaveCount(isWorkbench ? 1 : 0);
    await expect(settingsNav(page), hash).toHaveCount(isSettings ? 1 : 0);
    if (isSettings) {
      await expect(page.locator(".page-heading h1"), hash).toHaveText("Settings");
      await expect(settingsPage(page).getByRole("heading", { level: 2 }), hash).toHaveText("Profile");
      await expect(settingsNav(page).getByRole("link"), hash).toHaveText(["Profile"]);
      await expect(settingsNav(page).locator('[aria-current="page"]'), hash).toHaveText("Profile");
      await expect(page.locator(".breadcrumbs"), hash).toHaveText("SettingsProfile");
    }
    // No page renders a horizontal navigation row.
    await expect(page.locator(".resources-nav, .nav-more"), hash).toHaveCount(0);
    // New Cell, Majordomo, refresh and the user menu stay in the top bar.
    for (const name of [/^New Cell$/, /majordomo panel/, /^Refresh$/, /^User menu/]) await expect(page.locator(".toolbar").getByRole(name.source.includes("New Cell") ? "link" : "button", { name }), `${hash} ${name}`).toHaveCount(1);
  }
  // The switch links home and to the Overview, and the breadcrumb root follows the view.
  await expect(page.locator(".view-switch").getByRole("link", { name: "Workbench" })).toHaveAttribute("href", "#/");
  await expect(page.locator(".view-switch").getByRole("link", { name: "Manage" })).toHaveAttribute("href", "#/resources");
  // Both sides of the switch still navigate from a settings route.
  await page.locator(".view-switch").getByRole("link", { name: "Manage" }).click();
  await expect(page.locator(".breadcrumbs a").first()).toHaveText("Overview");
  await page.goto(`${server.url}#/settings`);
  await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  await expect(page.locator(".breadcrumbs a").first()).toHaveText("Workbench");
});

test("switching to Manage and back keeps an attached terminal", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  let attaches = 0;
  const closed = [];
  await page.routeWebSocket("**/attach", socket => { attaches++; socket.onClose(() => closed.push(socket.url())); socket.send(JSON.stringify({ type: "ready" })); socket.send(Buffer.from("$ ")); });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const state = terminalTools(page).locator(".terminal-connection-state");
  await expect(state).toHaveText("Attached");
  for (let i = 0; i < 3; i++) {
    await page.locator(".view-switch").getByRole("link", { name: "Manage" }).click();
    await expect(page.locator(".page-heading h1")).toHaveText("Overview");
    await nav(page).getByRole("link", { name: "Configs" }).click();
    await expect(page.locator(".page-heading h1")).toHaveText("Configs");
    await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
    await page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" }).click();
    await expect(state).toHaveText("Attached");
  }
  // Opening the settings routes closes no session either; the open cell's terminal is still attached.
  const cell = page.url();
  await openSettings(page);
  await page.goto(cell);
  await expect(state).toHaveText("Attached");
  expect(attaches).toBe(1);
  expect(closed).toEqual([]);
});

test("Back and Forward across a view switch restore the page and its sidebar", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/cells`);
  await expect(page.locator(".page-heading h1")).toHaveText("Cells");
  await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  await expect(page.getByRole("complementary", { name: "Cell sidebar" })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/#\/cells$/);
  await expect(page.locator(".page-heading h1")).toHaveText("Cells");
  await expect(page.getByRole("complementary", { name: "Manage sidebar" })).toBeVisible();
  await expect(nav(page).getByRole("link", { name: "Cells" })).toHaveAttribute("aria-current", "page");
  await page.goForward();
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  await expect(page.getByRole("complementary", { name: "Cell sidebar" })).toBeVisible();
  await expect(switchSide(page)).toHaveText("Workbench");
  // A settings page is one more history entry: Back and Forward restore it, its sidebar and the focus.
  await openSettings(page);
  await expect(page.locator(".page-heading h1")).toBeFocused();
  await page.goBack();
  await expect(page.locator(".page-heading h1")).toHaveText("Workbench");
  await expect(page.getByRole("complementary", { name: "Cell sidebar" })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("complementary", { name: "Manage sidebar" })).toBeVisible();
  await page.goForward();
  await page.goForward();
  await expect(page).toHaveURL(/#\/settings$/);
  await expect(page.getByRole("complementary", { name: "Settings sidebar" })).toBeVisible();
  await expect(settingsNav(page).locator('[aria-current="page"]')).toHaveText("Profile");
  await expect(settingsPage(page).getByRole("heading", { name: "Profile", level: 2 })).toBeVisible();
  await expect(page.locator(".page-heading h1")).toBeFocused();
});

test("grid tiles show the terminal's last rows, attention first, and a state-only tile without a screen", async ({ page }) => {
  const rows = Array.from({ length: 20 }, (_, i) => `row ${i + 1}   `).join("\n") + "\n\n\n";
  await mock(page, path => {
    if (path === screenshotPath(["dev", "shared", "services", "api-00"])) return screen(rows);
    if (path === screenshotPath(["dev", "shared", "services", "api-01"])) return noScreen;
    return withStates(path, { "dev/api-00": "Ready", "dev/api-01": "Ready", "prod/api-00": { state: "Ready", outOfSync: true, outOfSyncReason: "image changed" }, "prod/api-01": "Failed" }, 2);
  });
  await page.goto(server.url);
  // Attention first (drift, then Failed, in scope order), then the Ready cells in scope order.
  await expect(grid(page).locator(".cell-tile-name")).toHaveText(["api-00", "api-01", "api-00", "api-01"]);
  await expect(grid(page).locator(".cell-tile .cell-tile-scope")).toHaveText(["prod / shared / services", "prod / shared / services", "dev / shared / services", "dev / shared / services"]);
  const shown = tile(page, "dev / shared / services / api-00");
  await expect(shown).toHaveAttribute("href", "#/workspace/dev/shared/services/api-00");
  await expect(shown.locator("pre")).toHaveText(Array.from({ length: 12 }, (_, i) => `row ${i + 9}`).join("\n"));
  await expect(shown).toContainText("Ready");
  await expect(tile(page, "dev / shared / services / api-01")).toContainText("No terminal screen");
  await expect(tile(page, "prod / shared / services / api-01")).toContainText("Failed");
  await expect(tile(page, "prod / shared / services / api-01")).toContainText("No terminal screen");
  await expect(tile(page, "prod / shared / services / api-00")).toContainText("Drift");
  await shown.click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api-00$/);
});

test("terminal text in a tile renders as text, never as HTML", async ({ page }) => {
  await mock(page, path => path.endsWith("/screenshot") ? screen('<img src=x onerror="window.injected=1"><b>bold</b>') : undefined);
  await page.goto(server.url);
  await expect(tile(page, "dev / shared / services / api").locator("pre")).toHaveText('<img src=x onerror="window.injected=1"><b>bold</b>');
  await expect(grid(page).locator("pre img, pre b")).toHaveCount(0);
  expect(await page.evaluate(() => window.injected)).toBeUndefined();
});

test("grid polling reads only visible tiles on the grid page and keeps the last screen when a refresh fails", async ({ page }) => {
  test.setTimeout(60_000);
  const requests = [];
  let failNext = false;
  await mock(page, path => {
    // Every cell Ready, so the grid keeps scope order: dev api-00 first, prod api-39 far below the fold.
    if (!path.endsWith("/screenshot")) return path.endsWith("/cells") ? { json: fixture(path, 40).map(cell => ({ ...cell, status: { state: "Ready" } })) } : undefined;
    requests.push(path);
    if (failNext && path === screenshotPath(["dev", "shared", "services", "api-00"])) return failure;
    return screen(`screen of ${path.split("/").at(-2)}`);
  });
  await page.goto(`${server.url}#/resources`);
  await expect(page.locator(".page-heading h1")).toHaveText("Overview");
  await page.waitForTimeout(1500);
  expect(requests).toEqual([]);
  await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
  const first = tile(page, "dev / shared / services / api-00");
  await expect(first.locator("pre")).toHaveText("screen of api-00");
  // Tiles far below the fold are not read.
  const last = screenshotPath(["prod", "shared", "services", "api-39"]);
  expect(requests).not.toContain(last);
  expect(new Set(requests).size).toBeLessThan(80);
  // A failed refresh keeps the text and marks the tile stale.
  failNext = true;
  await expect(first.locator(".cell-tile-stale")).toHaveText("Stale", { timeout: 12_000 });
  await expect(first.locator("pre")).toHaveText("screen of api-00");
  // Leaving the grid stops polling.
  await page.locator(".view-switch").getByRole("link", { name: "Manage" }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Overview");
  const count = requests.length;
  await page.waitForTimeout(6000);
  expect(requests.length).toBe(count);
});

test("grid polling pauses while the browser tab is hidden", async ({ page }) => {
  test.setTimeout(40_000);
  const requests = [];
  await mock(page, path => { if (path.endsWith("/screenshot")) { requests.push(path); return screen("$ "); } });
  await page.goto(server.url);
  await expect(tile(page, "dev / shared / services / api").locator("pre")).toHaveText("$");
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  const count = requests.length;
  await page.waitForTimeout(6000);
  expect(requests.length).toBe(count);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => requests.length, { timeout: 8000 }).toBeGreaterThan(count);
});

test("a Stopped cell leaves the sidebar, rail and grid, and Stopped · 1 opens the filtered Cells table", async ({ page }) => {
  await mock(page, path => withStates(path, { "prod/api": "Stopped" }));
  await page.goto(server.url);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(cells.getByRole("link")).toHaveCount(1);
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toHaveCount(0);
  await expect(grid(page).locator(".cell-tile")).toHaveCount(1);
  await expect(tile(page, "prod / shared / services / api")).toHaveCount(0);
  const sidebarLink = page.getByRole("region", { name: "All cells" }).getByRole("link", { name: "Stopped · 1" });
  await expect(sidebarLink).toHaveAttribute("href", "#/cells?state=inactive");
  await expect(grid(page).getByRole("link", { name: "Stopped · 1" })).toHaveAttribute("href", "#/cells?state=inactive");
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(page.getByRole("navigation", { name: "Cell shortcuts" }).getByRole("link")).toHaveCount(1);
  await page.getByRole("button", { name: "Expand sidebar" }).click();
  await sidebarLink.click();
  await expect(page).toHaveURL(/#\/cells\?state=inactive$/);
  await expect(page.getByLabel("Filter by state")).toHaveValue("inactive");
  await expect(page.getByRole("row")).toHaveCount(2);
  await expect(page.getByRole("row").nth(1)).toContainText("prod / shared / services");
  // All states, Active, Stopped or exited, then each state present.
  await expect(page.getByLabel("Filter by state").locator("option")).toHaveText(["All states", "Active", "Stopped or exited", "Ready", "Stopped"]);
  await page.getByLabel("Filter by state").selectOption("active");
  await expect(page.getByRole("row")).toHaveCount(2);
  await expect(page.getByRole("row").nth(1)).toContainText("dev / shared / services");
  await page.getByLabel("Filter by state").selectOption("");
  await expect(page.getByRole("row")).toHaveCount(3);
  await page.goto(`${server.url}#/cells?state=Ready`);
  await expect(page.getByLabel("Filter by state")).toHaveValue("Ready");
  await expect(page.getByRole("row")).toHaveCount(2);
});

test("the open cell stays in the sidebar whatever its state", async ({ page }) => {
  await mock(page, path => withStates(path, { "prod/api": "Stopped" }));
  await page.goto(`${server.url}#/workspace/prod/shared/services/api`);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("region", { name: "All cells" }).getByRole("link", { name: /^Stopped · / })).toHaveCount(0);
  await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "All cells" }).getByRole("link", { name: "Stopped · 1" })).toBeVisible();
});

test("the grid and the sidebar search and group independently", async ({ page }) => {
  await mock(page, path => {
    if (!path.endsWith("/cells")) return;
    const prod = path.includes("/prod/");
    return { json: fixture(path).map(cell => ({ ...cell, metadata: { ...cell.metadata, labels: { team: prod ? "ops" : "platform", tier: "one" } } })) };
  });
  await defaultStyle(page);
  await page.goto(server.url);
  const sidebar = page.getByRole("region", { name: "All cells" });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  const gridSearch = grid(page).getByRole("searchbox", { name: "Search grid" });
  const gridGroupBy = grid(page).getByLabel("Group grid by");
  await expect(grid(page).locator(".cell-tile")).toHaveCount(2);
  // The grid's controls sit above its tiles.
  expect((await gridSearch.boundingBox()).y).toBeLessThan((await grid(page).locator(".cell-tile").first().boundingBox()).y);
  // Search: each field filters its own list only.
  await sidebar.getByRole("searchbox").fill("prod /");
  await expect(cells.getByRole("link")).toHaveCount(1);
  await expect(grid(page).locator(".cell-tile")).toHaveCount(2);
  await sidebar.getByRole("searchbox").fill("");
  await gridSearch.fill("prod /");
  await expect(grid(page).locator(".cell-tile")).toHaveCount(1);
  await expect(tile(page, "prod / shared / services / api")).toBeVisible();
  await expect(cells.getByRole("link")).toHaveCount(2);
  await gridSearch.fill("");
  // Group by: each select groups and stores its own list only.
  await sidebar.getByLabel("Group by").selectOption("team");
  await expect(cells.getByRole("region", { name: "ops group" })).toBeVisible();
  await expect(grid(page).getByRole("region", { name: /group$/ })).toHaveCount(0);
  await expect(gridGroupBy).toHaveValue("");
  expect(await page.evaluate(() => [localStorage.getItem("kukeon-cell-grouping"), localStorage.getItem("kukeon-grid-grouping")])).toEqual(["team", null]);
  await gridGroupBy.selectOption("tier");
  await expect(grid(page).getByRole("region", { name: "one group" }).locator(".cell-tile")).toHaveCount(2);
  await expect(cells.getByRole("region", { name: "ops group" })).toBeVisible();
  await expect(cells.getByRole("region", { name: "one group" })).toHaveCount(0);
  await page.reload();
  await expect(sidebar.getByLabel("Group by")).toHaveValue("team");
  await expect(gridGroupBy).toHaveValue("tier");
  expect(await page.evaluate(() => [localStorage.getItem("kukeon-cell-grouping"), localStorage.getItem("kukeon-grid-grouping")])).toEqual(["team", "tier"]);
  await expect(grid(page).getByRole("region", { name: "one group" })).toBeVisible();
  await expect(cells.getByRole("region", { name: "platform group" })).toBeVisible();
});

test("the Show search and Show Group by switches hide each list's controls, clear a typed query and keep the grouping", async ({ page }) => {
  await mock(page, path => {
    if (!path.endsWith("/cells")) return;
    const prod = path.includes("/prod/");
    return { json: fixture(path).map(cell => ({ ...cell, metadata: { ...cell.metadata, labels: { team: prod ? "ops" : "platform" } } })) };
  });
  // Default style on the Profile defaults: the sidebar's controls hidden, the grid's shown.
  await seed(page, { "kukeon-cell-list-style": "default", "kukeon-default-group-by": "team" });
  await page.goto(server.url);
  const sidebar = page.getByRole("region", { name: "All cells" });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  const gridSearch = grid(page).getByRole("searchbox", { name: "Search grid" });
  await expect(cells.getByRole("link")).toHaveCount(2);
  await expect(sidebar.getByRole("searchbox")).toHaveCount(0);
  await expect(sidebar.getByRole("combobox")).toHaveCount(0);
  await expect(sidebar.locator(".cell-navigation-controls")).toHaveCount(0);
  // With no Group by select the sidebar is still grouped by its effective grouping.
  await expect(cells.getByRole("region", { name: "ops group" })).toBeVisible();
  await expect(gridSearch).toBeVisible();
  await expect(grid(page).getByLabel("Group grid by")).toHaveValue("team");
  await gridSearch.fill("prod /");
  await expect(grid(page).locator(".cell-tile")).toHaveCount(1);

  const profile = `${server.url}#/settings/profile`;
  const toggle = async (name, on) => {
    const control = settingsPage(page).getByRole("switch", { name });
    await control.click();
    await expect(control).toHaveAttribute("aria-checked", String(on));
  };
  await page.goto(profile);
  await toggle("Show search in grid", false);
  await toggle("Show search in sidebar", true);
  await page.goto(`${server.url}#/`);
  // Hiding the grid's search cleared its query; no search applies to the grid.
  await expect(gridSearch).toHaveCount(0);
  await expect(grid(page).locator(".cell-tile")).toHaveCount(2);
  await expect(grid(page).getByLabel("Group grid by")).toBeVisible();
  await sidebar.getByRole("searchbox", { name: "Search cells" }).fill("prod /");
  await expect(cells.getByRole("link")).toHaveCount(1);
  await page.goto(profile);
  await toggle("Show search in sidebar", false);
  await toggle("Show search in grid", true);
  await toggle("Show Group by in grid", false);
  await page.goto(`${server.url}#/`);
  await expect(sidebar.getByRole("searchbox")).toHaveCount(0);
  await expect(cells.getByRole("link")).toHaveCount(2);
  await expect(gridSearch).toHaveValue("");
  await expect(grid(page).getByLabel("Group grid by")).toHaveCount(0);
  await expect(grid(page).getByRole("region", { name: "ops group" }).locator(".cell-tile")).toHaveCount(1);
  // With both of its controls hidden the grid renders no controls row.
  await page.goto(profile);
  await toggle("Show search in grid", false);
  await page.goto(`${server.url}#/`);
  await expect(grid(page).locator(".cell-tile")).toHaveCount(2);
  await expect(grid(page).locator(".grid-controls")).toHaveCount(0);
  await expect(grid(page).getByRole("region", { name: "platform group" })).toBeVisible();
});

test("Default Group by groups both lists until one picks its own, and changing it clears both choices", async ({ page }) => {
  await mock(page, path => /\/cells$/.test(path) ? { json: fixture(path).map(cell => ({ ...cell, metadata: { ...cell.metadata, labels: { team: "platform", "kukeon.io/config": "app" } } })) } : undefined);
  // No stored grouping and no stored default: both lists are grouped by kukeon.io/config.
  await seed(page, { "kukeon-cell-list-style": "default", "kukeon-sidebar-show-group-by": "true" });
  await page.goto(server.url);
  const sidebar = page.getByRole("region", { name: "All cells" });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(sidebar.getByLabel("Group by")).toHaveValue("kukeon.io/config");
  await expect(grid(page).getByLabel("Group grid by")).toHaveValue("kukeon.io/config");
  await expect(cells.getByRole("region", { name: "app group" }).getByRole("link")).toHaveCount(2);
  await expect(grid(page).getByRole("region", { name: "app group" }).locator(".cell-tile")).toHaveCount(2);
  expect(await page.evaluate(() => [localStorage.getItem("kukeon-cell-grouping"), localStorage.getItem("kukeon-grid-grouping")])).toEqual([null, null]);
  // A grouping stored before the upgrade keeps applying to the sidebar, an explicit None included.
  await page.evaluate(() => localStorage.setItem("kukeon-cell-grouping", ""));
  await page.reload();
  await expect(sidebar.getByLabel("Group by")).toHaveValue("");
  await expect(cells.getByRole("link")).toHaveCount(2);
  await expect(cells.getByRole("region", { name: /group$/ })).toHaveCount(0);
  await expect(grid(page).getByRole("region", { name: "app group" })).toBeVisible();
  await grid(page).getByLabel("Group grid by").selectOption("team");
  await expect(grid(page).getByRole("region", { name: "platform group" })).toBeVisible();
  // Changing the default removes both per-list keys, and both lists show the new default at once.
  await page.goto(`${server.url}#/settings/profile`);
  const defaultGroupBy = settingsPage(page).getByLabel("Default Group by");
  await expect(defaultGroupBy).toHaveValue("kukeon.io/config");
  await defaultGroupBy.selectOption("team");
  expect(await page.evaluate(() => [localStorage.getItem("kukeon-default-group-by"), localStorage.getItem("kukeon-cell-grouping"), localStorage.getItem("kukeon-grid-grouping")])).toEqual(["team", null, null]);
  await page.goto(`${server.url}#/`);
  await expect(sidebar.getByLabel("Group by")).toHaveValue("team");
  await expect(grid(page).getByLabel("Group grid by")).toHaveValue("team");
  await expect(cells.getByRole("region", { name: "platform group" })).toBeVisible();
  await expect(grid(page).getByRole("region", { name: "platform group" })).toBeVisible();
  await page.reload();
  await expect(cells.getByRole("region", { name: "platform group" })).toBeVisible();
});

test("Profile shows every setting in its groups with their defaults; each applies at once, works from the keyboard and survives a reload", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/settings`);
  const settings = settingsPage(page);
  await expect(settings).toContainText("Stored in this browser only.");
  // One section, Profile (#1745): the former General settings lead it.
  await expect(settings.getByRole("heading", { level: 2 })).toHaveText(["Profile"]);
  await expect(settings.getByRole("heading", { level: 3 })).toHaveText(["Appearance", "Terminal", "Refresh", "Workbench", "Cell list", "Sidebar", "Workbench grid"]);
  await expect(settings.getByRole("region", { name: "Appearance" }).getByRole("radio", { name: "System" })).toBeChecked();
  await expect(settings.getByLabel("Refresh interval", { exact: true })).toHaveValue("10");
  await expect(settings.getByLabel("Refresh interval", { exact: true }).locator("option")).toHaveText(["5 s", "10 s", "30 s", "60 s"]);
  // Every Workbench timer sits in Refresh (#1770), each with its own values and default.
  const timers = { "Refresh interval": ["10", ["5 s", "10 s", "30 s", "60 s"]], "Unread check interval": ["2", ["1 s", "2 s", "5 s", "10 s", "30 s"]], "Thumbnail refresh interval": ["5", ["2 s", "5 s", "10 s", "30 s"]], "Mark unread after quiet for": ["0", ["Off", "1 s", "3 s", "5 s", "10 s"]] };
  const refresh = settings.getByRole("region", { name: "Refresh" });
  await expect(refresh.getByRole("combobox")).toHaveCount(4);
  for (const [index, [name, [value, options]]] of Object.entries(timers).entries()) {
    await expect(refresh.getByRole("combobox").nth(index), name).toHaveAccessibleName(name);
    await expect(settings.getByLabel(name, { exact: true }), name).toHaveValue(value);
    await expect(settings.getByLabel(name, { exact: true }).locator("option"), name).toHaveText(options);
  }
  await expect(settings.getByLabel("Mark unread after quiet for").locator("option:checked")).toHaveText("Off");
  await expect(settings.getByLabel("Unread check interval")).toHaveAccessibleDescription(/sidebar, the collapsed rail and the grid tile badges/);
  for (const group of ["Sidebar", "Workbench grid"]) await expect(settings.getByRole("region", { name: group, exact: true }).getByRole("combobox")).toHaveCount(0);
  const switches = { "Auto-attach terminals": true, "Show Get started": true, "Show cell scope in sidebar": true, "Show terminal status in sidebar": true, "Show unread bytes": false, "Show search in sidebar": false, "Show Group by in sidebar": false, "Show search in grid": true, "Show Group by in grid": true };
  const keys = { "Auto-attach terminals": "kukeon-terminal-auto-attach", "Show Get started": "kukeon-get-started", "Show cell scope in sidebar": "kukeon-sidebar-show-scope", "Show terminal status in sidebar": "kukeon-sidebar-show-terminal-status", "Show unread bytes": "kukeon-show-unread-bytes", "Show search in sidebar": "kukeon-sidebar-show-search", "Show Group by in sidebar": "kukeon-sidebar-show-group-by", "Show search in grid": "kukeon-grid-show-search", "Show Group by in grid": "kukeon-grid-show-group-by" };
  await expect(settings.getByRole("region", { name: "Terminal" }).getByRole("switch")).toHaveCount(1);
  await expect(settings.getByRole("region", { name: "Workbench", exact: true }).getByRole("switch")).toHaveCount(1);
  await expect(settings.getByRole("region", { name: "Cell list" }).getByRole("radio")).toHaveCount(2);
  // Show unread bytes (#1696) follows Show terminal status in sidebar.
  const sidebarSwitches = settings.getByRole("region", { name: "Sidebar" }).getByRole("switch");
  await expect(sidebarSwitches).toHaveCount(5);
  await expect(sidebarSwitches.nth(1)).toHaveAccessibleName("Show terminal status in sidebar");
  await expect(sidebarSwitches.nth(2)).toHaveAccessibleName("Show unread bytes");
  await expect(settings.getByRole("region", { name: "Workbench grid" }).getByRole("switch")).toHaveCount(2);
  await expect(settings.getByRole("radio", { name: "Agent" })).toBeChecked();
  await expect(settings.getByLabel("Default Group by")).toHaveValue("kukeon.io/config");
  for (const [name, on] of Object.entries(switches)) {
    const control = settings.getByRole("switch", { name });
    await expect(control, name).toHaveAttribute("aria-checked", String(on));
    expect(await control.evaluate(element => element.tagName), name).toBe("BUTTON");
  }
  // Default Group by and the two Group by switches say they apply to the Default style.
  for (const control of [settings.getByLabel("Default Group by"), settings.getByRole("switch", { name: "Show Group by in sidebar" }), settings.getByRole("switch", { name: "Show Group by in grid" })]) {
    await expect(control).toHaveAccessibleDescription("Applies to the Default cell list style.");
  }
  await expect(settings.getByText("Applies to the Default cell list style.")).toHaveCount(3);
  // An unreadable stored value reads as the default.
  await page.evaluate(() => { localStorage.setItem("kukeon-cell-list-style", "tree"); localStorage.setItem("kukeon-grid-show-search", "maybe"); });
  await page.reload();
  await expect(settings.getByRole("radio", { name: "Agent" })).toBeChecked();
  await expect(settings.getByRole("switch", { name: "Show search in grid" })).toHaveAttribute("aria-checked", "true");
  // Keyboard only: arrow keys move the radio, Space and Enter flip a switch.
  await settings.getByRole("radio", { name: "Agent" }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(settings.getByRole("radio", { name: "Default", exact: true })).toBeChecked();
  await settings.getByLabel("Default Group by").selectOption("team");
  await settings.getByLabel("Refresh interval", { exact: true }).selectOption("30");
  await settings.getByLabel("Unread check interval").selectOption("10");
  await settings.getByLabel("Thumbnail refresh interval").selectOption("30");
  await settings.getByLabel("Mark unread after quiet for").selectOption("3");
  for (const [index, name] of Object.keys(switches).entries()) {
    await settings.getByRole("switch", { name }).focus();
    await page.keyboard.press(index % 2 ? "Enter" : "Space");
    await expect(settings.getByRole("switch", { name }), name).toHaveAttribute("aria-checked", String(!switches[name]));
  }
  await page.reload();
  await expect(settings.getByRole("radio", { name: "Default", exact: true })).toBeChecked();
  await expect(settings.getByLabel("Default Group by")).toHaveValue("team");
  await expect(settings.getByLabel("Refresh interval", { exact: true })).toHaveValue("30");
  for (const [name, on] of Object.entries(switches)) await expect(settings.getByRole("switch", { name }), name).toHaveAttribute("aria-checked", String(!on));
  await expect(settings.getByLabel("Unread check interval")).toHaveValue("10");
  await expect(settings.getByLabel("Thumbnail refresh interval")).toHaveValue("30");
  await expect(settings.getByLabel("Mark unread after quiet for")).toHaveValue("3");
  expect(await page.evaluate(() => ["kukeon-cell-list-style", "kukeon-default-group-by", "kukeon-refresh-seconds", "kukeon-unread-poll-seconds", "kukeon-grid-screen-seconds", "kukeon-unread-quiet-seconds"].map(key => localStorage.getItem(key)))).toEqual(["default", "team", "30", "10", "30", "3"]);
  // Off is a value: choosing it stores 0, which reads back as Off.
  await settings.getByLabel("Mark unread after quiet for").selectOption({ label: "Off" });
  expect(await page.evaluate(() => localStorage.getItem("kukeon-unread-quiet-seconds"))).toBe("0");
  await page.reload();
  await expect(settings.getByLabel("Mark unread after quiet for")).toHaveValue("0");
  // An interval outside the offered values, or garbage, reads as the default.
  await page.evaluate(() => { localStorage.setItem("kukeon-refresh-seconds", "7"); localStorage.setItem("kukeon-unread-poll-seconds", "3"); localStorage.setItem("kukeon-grid-screen-seconds", "soon"); localStorage.setItem("kukeon-unread-quiet-seconds", "2"); });
  await page.reload();
  await expect(settings.getByLabel("Refresh interval", { exact: true })).toHaveValue("10");
  await expect(settings.getByLabel("Unread check interval")).toHaveValue("2");
  await expect(settings.getByLabel("Thumbnail refresh interval")).toHaveValue("5");
  await expect(settings.getByLabel("Mark unread after quiet for")).toHaveValue("0");
  expect(await page.evaluate(stored => stored.map(key => localStorage.getItem(key)), Object.values(keys))).toEqual(Object.keys(keys).map(name => String(!switches[name])));
});

test("at 390×844 the drawer shows the Settings navigation, and its collapsed rail has no cell dots", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/settings`);
  const sidebar = page.getByRole("complementary", { name: "Settings sidebar" });
  await sidebar.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(sidebar.getByRole("button", { name: "Expand sidebar" })).toBeVisible();
  await expect(sidebar.getByRole("navigation", { name: "Cell shortcuts" })).toHaveCount(0);
  await expect(sidebar.locator(".rail-cell, .rail-agent-glyph")).toHaveCount(0);
  await expect(settingsNav(page)).toBeHidden();
  await sidebar.getByRole("button", { name: "Expand sidebar" }).click();

  await page.setViewportSize({ width: 390, height: 844 });
  const fits = async () => expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await fits();
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await expect(sidebar).toBeVisible();
  await expect(settingsNav(page).getByRole("link")).toHaveText(["Profile"]);
  await expect(sidebar.getByRole("navigation", { name: "Cell navigation" })).toHaveCount(0);
  await expect(sidebar.getByRole("navigation", { name: "Manage navigation" })).toHaveCount(0);
  await settingsNav(page).getByRole("link", { name: "Profile" }).click();
  await expect(sidebar).toBeHidden();
  await expect(settingsPage(page).getByRole("heading", { name: "Profile", level: 2 })).toBeVisible();
  await fits();
});

test("agent style lists agents and their instances in the sidebar, with Other cells last", async ({ page }) => {
  await mock(page, agentInventory);
  await page.goto(server.url);
  const sidebar = page.getByRole("region", { name: "All cells" });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  // No stored style: agent style. The hierarchy is the grouping, so neither list has a Group by.
  await expect(cells.getByRole("heading", { level: 3 })).toHaveText(["Agents", "Other cells"]);
  await expect(sidebar.getByRole("combobox")).toHaveCount(0);
  await expect(grid(page).getByRole("combobox")).toHaveCount(0);
  await expect(grid(page).getByRole("searchbox", { name: "Search grid" })).toBeVisible();
  // Agents by name, then realm; two Configs of one name in two realms are two agents showing their realm.
  const agents = cells.getByRole("region", { name: /agent$/ });
  await expect(agents).toHaveCount(3);
  await expect(agents.locator(".agent-name")).toHaveText(["claudedev", "claudeprod", "pm"]);
  // The provenance-bound cell and the label-only cell of one Config are one agent; its dot is the failed instance.
  const claude = agents.nth(0);
  const toggle = claude.getByRole("button", { name: /^claude/ });
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(toggle).toContainText("2 instances");
  await expect(toggle.locator(".rollup-dot")).toHaveClass(/tone-error/);
  await expect(toggle.locator(".rollup-dot")).toHaveText("Failed");
  await expect(claude.getByRole("link")).toHaveCount(2);
  await expect(claude.getByRole("link", { name: "Open dev / shared / services / dev-0" })).toContainText("Ready");
  await expect(claude.getByRole("link", { name: "Open dev / shared / services / dev-1" })).toContainText("Failed");
  // A single-instance agent is one link that opens its cell, with no toggle.
  for (const [index, scope] of [[1, "prod / shared / services / dev-9"], [2, "dev / shared / services / pm-0"]]) {
    await expect(agents.nth(index).getByRole("button")).toHaveCount(0);
    await expect(agents.nth(index).getByRole("link")).toHaveCount(1);
    await expect(agents.nth(index).getByRole("link", { name: `open ${scope}` })).toContainText("1 instance");
  }
  // Cells created from no Config come after every agent.
  const others = cells.getByRole("region", { name: "Other cells" });
  await expect(others.getByRole("link")).toHaveCount(1);
  await expect(others.getByRole("link", { name: "Open dev / shared / services / scratch" })).toBeVisible();
  expect(await cells.locator("section").last().getAttribute("aria-label")).toBe("Other cells");

  // Collapse and expand from the keyboard and the mouse; the collapsed state survives a reload.
  await toggle.focus();
  await page.keyboard.press("Enter");
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(claude.getByRole("link")).toHaveCount(0);
  await page.reload();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(claude.getByRole("link")).toHaveCount(0);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");

  // The open cell's instance row is current; collapsed, its agent row says it holds the open cell.
  await claude.getByRole("link", { name: "Open dev / shared / services / dev-1" }).click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/dev-1$/);
  await expect(claude.getByRole("link", { name: "Open dev / shared / services / dev-1" })).toHaveAttribute("aria-current", "page");
  await expect(toggle).not.toHaveClass(/has-current/);
  await toggle.click();
  await expect(toggle).toHaveClass(/has-current/);
  await expect(toggle).toContainText("contains the open cell");
  await toggle.click();
  await agents.nth(2).getByRole("link").click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/pm-0$/);
  await expect(agents.nth(2).getByRole("link")).toHaveAttribute("aria-current", "page");
});

test("agent style keeps the sidebar's stopped link, search, empty states and paging, and drops Other cells when every cell has a Config", async ({ page }) => {
  let labelled = false;
  let many = false;
  await mock(page, path => {
    if (!path.endsWith("/cells")) return;
    if (many) return { json: fixture(path, 61).map((cell, i) => ({ ...cell, metadata: { ...cell.metadata, labels: { "kukeon.io/config": i < 30 ? `config-${String(i).padStart(2, "0")}` : "" } } })) };
    const result = agentInventory(path).json;
    const dev = path.includes("/dev/");
    return { json: (labelled ? result.filter(cell => cell.metadata.name !== "scratch") : result).map(cell => dev && cell.metadata.name === "dev-0" ? { ...cell, status: { state: "Stopped" } } : cell) };
  });
  await seed(page, { "kukeon-sidebar-show-search": "true" });
  await page.goto(server.url);
  const sidebar = page.getByRole("region", { name: "All cells" });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  const agents = cells.getByRole("region", { name: /agent$/ });
  // The stopped instance leaves the list: its agent is down to one instance and becomes a link.
  await expect(sidebar.getByRole("link", { name: "Stopped · 1" })).toHaveAttribute("href", "#/cells?state=inactive");
  await expect(agents).toHaveCount(3);
  await expect(agents.nth(0).getByRole("button")).toHaveCount(0);
  await expect(agents.nth(0).getByRole("link", { name: "open dev / shared / services / dev-1" })).toBeVisible();
  // Search lists only the agents with a matching instance, and only those instances.
  await sidebar.getByRole("searchbox", { name: "Search cells" }).fill("prod /");
  await expect(agents).toHaveCount(1);
  await expect(agents.getByRole("link", { name: "open prod / shared / services / dev-9" })).toBeVisible();
  await expect(cells.getByRole("region", { name: "Other cells" })).toHaveCount(0);
  await expect(sidebar.locator(".cell-result-count")).toHaveText("1 cell");
  await sidebar.getByRole("searchbox", { name: "Search cells" }).fill("nothing");
  await expect(sidebar.getByText("No matching cells.")).toBeVisible();
  await expect(cells.getByRole("heading", { level: 3 })).toHaveCount(0);
  await sidebar.getByRole("searchbox", { name: "Search cells" }).fill("");
  // With no cell outside a Config the Other cells heading is absent.
  labelled = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(cells.getByRole("heading", { level: 3 })).toHaveText(["Agents"]);
  await expect(cells.getByRole("region", { name: "Other cells" })).toHaveCount(0);
  // Agents page like groups, 25 at a time, and Other cells 100 cells at a time.
  many = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(agents).toHaveCount(25);
  await cells.getByRole("button", { name: "Show 25 more groups" }).click();
  await expect(agents).toHaveCount(50);
  await cells.getByRole("button", { name: "Show 11 more groups" }).click();
  await expect(agents).toHaveCount(60);
  await expect(cells.getByRole("region", { name: "Other cells" }).getByRole("link")).toHaveCount(62);
});

test("agent style shows each agent's glyph and instance dots on the collapsed rail", async ({ page }) => {
  await mock(page, agentInventory);
  await page.goto(server.url);
  const sidebar = page.getByRole("complementary", { name: "Cell sidebar" });
  await sidebar.getByRole("button", { name: "Collapse sidebar" }).click();
  const rail = sidebar.getByRole("navigation", { name: "Cell shortcuts" });
  const agents = rail.locator(".rail-agent");
  await expect(agents).toHaveCount(3);
  // A multi-instance agent: its glyph, toned by the roll-up state, then one dot per instance.
  const glyph = agents.nth(0).getByRole("img", { name: "Agent claude (dev), 2 instances" });
  await expect(glyph).toHaveClass(/tone-error/);
  await expect(glyph).toHaveAttribute("title", "Agent claude (dev), 2 instances");
  await expect(agents.nth(0).locator(".rail-cell")).toHaveCount(2);
  await expect(agents.nth(0).getByRole("link", { name: "Open dev / shared / services / dev-0 (Ready)" })).toBeVisible();
  await expect(agents.nth(0).getByRole("link", { name: "Open dev / shared / services / dev-1 (Failed)" })).toHaveClass(/tone-error/);
  // A single-instance agent is only its glyph, which links to the cell.
  for (const [index, name] of [[1, "Agent claude (prod), 1 instance: open prod / shared / services / dev-9 (Ready)"], [2, "Agent pm, 1 instance: open dev / shared / services / pm-0 (Ready)"]]) {
    await expect(agents.nth(index).locator(".rail-cell")).toHaveCount(0);
    await expect(agents.nth(index).getByRole("link", { name, exact: true })).toHaveClass(/rail-agent-glyph/);
  }
  // The dots of the other cells come last; a divider separates each agent and the other cells.
  expect(await rail.locator(":scope > ul > li").evaluateAll(items => items.map(item => item.className))).toEqual(["rail-agent", "rail-agent", "rail-agent", "rail-others"]);
  await expect(rail.locator(".rail-others").getByRole("link", { name: "Open dev / shared / services / scratch (Ready)" })).toBeVisible();
  expect(await rail.locator(":scope > ul > li").evaluateAll(items => items.map(item => parseFloat(getComputedStyle(item).borderTopWidth) > 0))).toEqual([false, true, true, true]);
  // Every glyph and dot has an accessible name.
  for (const item of await rail.locator(".rail-cell, .rail-agent-glyph").all()) expect(await item.getAttribute("aria-label")).toBeTruthy();
  expect((await sidebar.boundingBox()).width).toBeLessThan(80);
  await agents.nth(2).getByRole("link").click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/pm-0$/);
  await expect(agents.nth(2).getByRole("link")).toHaveAttribute("aria-current", "page");
});

test("agent style sections the grid by agent, searches by instance, and Default style restores the lists without a reload", async ({ page }) => {
  await mock(page, agentInventory);
  let loads = 0;
  page.on("load", () => { loads += 1; });
  await page.goto(server.url);
  const sections = grid(page).locator(".cell-grid-group");
  await expect(sections).toHaveCount(4);
  expect(await sections.evaluateAll(items => items.map(item => item.getAttribute("aria-label")))).toEqual(["claude agent", "claude agent", "pm agent", "Other cells"]);
  await expect(sections.locator(".cell-grid-heading")).toHaveText([/^claudedev\s*2 instancesFailed$/, /^claudeprod\s*1 instanceReady$/, /^pm\s*1 instanceReady$/, /^Other cells\s*1$/]);
  await expect(sections.nth(0).locator(".cell-grid-heading svg")).toHaveCount(1);
  await expect(sections.nth(0).locator(".cell-grid-heading .rollup-dot")).toHaveClass(/tone-error/);
  // Tiles are unchanged; inside an agent the instance that needs attention comes first.
  expect(await sections.nth(0).locator(".cell-tile").evaluateAll(tiles => tiles.map(item => item.getAttribute("aria-label")))).toEqual(["Open dev / shared / services / dev-1", "Open dev / shared / services / dev-0"]);
  await expect(tile(page, "dev / shared / services / scratch")).toContainText("scratch");
  await grid(page).getByRole("searchbox", { name: "Search grid" }).fill("dev-0");
  await expect(sections).toHaveCount(1);
  await expect(sections.locator(".cell-tile")).toHaveCount(1);
  await expect(sections.locator(".cell-grid-heading")).toContainText("1 instance");
  await grid(page).getByRole("searchbox", { name: "Search grid" }).fill("");

  // Default style: the flat or grouped lists come back in the sidebar, the rail and the grid at once.
  await page.goto(`${server.url}#/settings/profile`);
  await settingsPage(page).getByRole("radio", { name: "Default", exact: true }).check();
  await settingsPage(page).getByRole("switch", { name: "Show Group by in sidebar" }).click();
  await page.goto(`${server.url}#/`);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(cells.getByRole("heading", { level: 3 })).toHaveCount(0);
  await expect(cells.getByRole("region", { name: /agent$/ })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "All cells" }).getByLabel("Group by")).toHaveValue("kukeon.io/config");
  await expect(grid(page).getByLabel("Group grid by")).toHaveValue("kukeon.io/config");
  await expect(cells.getByRole("region", { name: /group$/ })).toHaveCount(3);
  await expect(cells.getByRole("region", { name: "claude group" }).getByRole("link")).toHaveCount(3);
  await expect(grid(page).getByRole("region", { name: "claude group" }).locator(".cell-tile")).toHaveCount(3);
  await expect(grid(page).getByRole("region", { name: "Ungrouped group" }).locator(".cell-tile")).toHaveCount(1);
  const sidebar = page.getByRole("complementary", { name: "Cell sidebar" });
  await sidebar.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(sidebar.locator(".rail-agent, .rail-others, .rail-agent-glyph")).toHaveCount(0);
  await expect(sidebar.getByRole("navigation", { name: "Cell shortcuts" }).locator(".rail-cell")).toHaveCount(5);
  expect(loads).toBe(1);
});

test("the Manage navigation marks the current kind on list, detail and form pages", async ({ page }) => {
  await mock(page);
  await mockCatalog(page);
  await mockDaemon(page);
  const current = { "#/resources": "Overview", "#/realms": "Realms", "#/resource/dev": "Realms", "#/resource/dev/shared": "Spaces", "#/resource/dev/shared/services": "Stacks", "#/cells": "Cells", "#/configs": "Configs", "#/new-config": "Configs", "#/new-secret": "Secrets", "#/teams": "Teams", "#/new-team": "Teams", "#/teams/web/edit": "Teams", "#/sources": "Sources", "#/images": "Images", "#/daemon": "Daemon", "#/daemon/logs": "Daemon", "#/health": "Daemon", "#/daemon/health": "Daemon" };
  for (const [hash, name] of Object.entries(current)) {
    await page.goto(`${server.url}${hash}`);
    await expect(nav(page).locator('[aria-current="page"]'), hash).toHaveText([name]);
  }
});

test("the Daemon page has Status, Health and Logs tabs and #/health opens Health", async ({ page }) => {
  await mock(page);
  await mockDaemon(page);
  await mockReports(page, [{ id: "r1", createdAt: "2026-11-20T10:00:00Z", findings: [finding("kukeond restart loop")] }]);
  const tabs = page.getByRole("navigation", { name: "Daemon sections" });
  await page.goto(`${server.url}#/resources`);
  await nav(page).getByRole("link", { name: "Daemon" }).click();
  await expect(page).toHaveURL(/#\/daemon$/);
  await expect(tabs.getByRole("link")).toHaveText(["Status", "Health", "Logs"]);
  await expect(tabs.locator('[aria-current="page"]')).toHaveText("Status");
  await expect(page.getByRole("region", { name: "Daemon connectivity" })).toBeVisible();
  await expect(tabs.getByRole("link", { name: "Health" })).toHaveAttribute("href", "#/daemon/health");
  for (const hash of ["#/daemon/health", "#/health"]) {
    await page.goto(`${server.url}${hash}`);
    await expect(page.locator(".page-heading h1"), hash).toHaveText("Health");
    await expect(tabs.locator('[aria-current="page"]'), hash).toHaveText("Health");
    await expect(page.getByRole("region", { name: "Health report" }), hash).toContainText("kukeond restart loop");
    await expect(page.locator(".breadcrumbs > *"), hash).toHaveText(["Overview", "Daemon", "Health"]);
  }
  await tabs.getByRole("link", { name: "Logs" }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Daemon logs");
  await expect(tabs.locator('[aria-current="page"]')).toHaveText("Logs");
});

test("the Overview heading reads Overview and its Health card shows the newest report", async ({ page }) => {
  await mock(page);
  let mode = "report";
  await mockReports(page, path => mode === "error" ? failure
    : mode === "none" ? { json: [] }
      : path === "/api/health/reports" ? { json: [{ id: "r2", createdAt: "2026-11-21T09:30:00Z", since: "", until: "", summary: "", findings: 3, recurring: 0, filed: 0 }, { id: "r1", createdAt: "2026-11-20T09:30:00Z", since: "", until: "", summary: "", findings: 1, recurring: 0, filed: 0 }] } : failure);
  await page.goto(`${server.url}#/resources`);
  await expect(page.locator(".page-heading h1")).toHaveText("Overview");
  const card = page.locator(".summary-card").filter({ hasText: "Health" });
  await expect(card).toHaveAttribute("href", "#/daemon/health");
  await expect(card.locator("strong")).toHaveText("3 findings");
  await expect(card.locator("time")).toHaveAttribute("datetime", "2026-11-21T09:30:00Z");
  mode = "none";
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(card).toContainText("No report stored");
  mode = "error";
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(card).toContainText("Unavailable");
});

test("the Manage dot names a new report with findings until the Health tab shows it, and a version mismatch", async ({ page }) => {
  await mock(page);
  await mockDaemon(page);
  await mockReports(page, [{ id: "r2", createdAt: "2026-11-21T09:30:00Z", findings: [finding("a"), finding("b")] }, { id: "r1", createdAt: "2026-11-20T09:30:00Z", findings: [] }]);
  const manage = page.locator(".view-switch").getByRole("link", { name: /^Manage/ });
  await page.goto(server.url);
  await expect(manage).toHaveAccessibleName("Manage: 2 findings in the latest health report");
  await expect(manage.locator(".view-switch-dot")).toHaveCount(1);
  await manage.click();
  await page.locator(".summary-card").filter({ hasText: "Health" }).click();
  await expect(page.getByRole("region", { name: "Health report" })).toContainText("Report r2");
  await expect(manage).toHaveAccessibleName("Manage");
  await expect(manage.locator(".view-switch-dot")).toHaveCount(0);
  expect(await page.evaluate(() => localStorage.getItem("kukeon-health-seen"))).toBe("r2");
  await page.reload();
  await expect(page.locator(".page-heading h1")).toHaveText("Health");
  await page.goto(server.url);
  await expect(manage.locator(".view-switch-dot")).toHaveCount(0);
  // A version mismatch is a reason of its own.
  await page.route("**/api/connection", route => route.fulfill({ json: { endpoint: "unix:///run/kukeon/kukeond.sock", version: "v0.7.0", daemonVersion: "v0.6.2" } }));
  await page.reload();
  await expect(manage).toHaveAccessibleName("Manage: Version mismatch");
  await expect(manage.locator(".view-switch-dot")).toHaveCount(1);
});

test("the Manage dot names an unreachable daemon", async ({ page }) => {
  await page.route("**/api/realms**", route => route.fulfill({ status: 403, json: { error: { code: "permission_denied", message: "denied" } } }));
  await page.goto(server.url);
  await expect(page.locator(".view-switch").getByRole("link", { name: /^Manage/ })).toHaveAccessibleName("Manage: Daemon: permission denied");
});

test("at 390×844 the switch shows named icons, the grid is one column and nothing scrolls sideways", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page, path => path.endsWith("/screenshot") ? screen("x".repeat(400)) : path.endsWith("/cells") ? { json: fixture(path, 3) } : undefined);
  await page.goto(server.url);
  const links = page.locator(".view-switch").getByRole("link");
  await expect(links).toHaveCount(2);
  await expect(page.locator(".view-switch").getByRole("link", { name: "Workbench" })).toBeVisible();
  await expect(page.locator(".view-switch").getByRole("link", { name: "Manage" })).toBeVisible();
  for (const label of await page.locator(".view-switch-label").all()) expect(await label.evaluate(el => el.getBoundingClientRect().width <= 1)).toBe(true);
  // prod cells report Running, which needs attention, so the first tile is prod api-00.
  await expect(tile(page, "prod / shared / services / api-00").locator("pre")).toContainText("xxxx");
  const lefts = await grid(page).locator(".cell-tile").evaluateAll(tiles => [...new Set(tiles.map(el => Math.round(el.getBoundingClientRect().left)))]);
  expect(lefts).toHaveLength(1);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  // The toolbar wraps without overlapping: the switch, New Cell and the refresh button do not intersect.
  const boxes = await Promise.all([page.locator(".view-switch"), page.locator(".toolbar").getByRole("link", { name: "New Cell", exact: true }), page.locator(".toolbar").getByRole("button", { name: "Refresh" })].map(locator => locator.boundingBox()));
  for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) {
    const [a, b] = [boxes[i], boxes[j]];
    expect(a.x + a.width <= b.x || b.x + b.width <= a.x || a.y + a.height <= b.y || b.y + b.height <= a.y).toBe(true);
  }
  for (const hash of ["#/resources", "#/cells", "#/daemon", "#/teams", "#/sources"]) {
    await page.goto(`${server.url}${hash}`);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), hash).toBe(true);
  }
});

// Issue #1649: per-role connectors. The catalog the wizard reads offers GitHub, which a team can give a role.
const roleAccessConnectors = [
  { name: "git", title: "Git", delivery: "git", scopes: ["realm"], secretTypes: ["ssh-key"], fields: [], methods: [] },
  { name: "github", title: "GitHub", scopes: ["realm"], secretTypes: ["opaque"], fields: [], methods: [{ name: "token", component: "github", delivery: "env", default: true }] },
  { name: "claude", title: "Claude", harness: "claude", scopes: ["realm"], secretTypes: ["opaque"], fields: [], methods: [{ name: "subscription", component: "claude-sub", delivery: "stateVolume" }] },
];
const roleAccessRows = [
  { role: "dev", config: "web-dev-claude", connector: "git", state: "resolved", identity: "git-ssh", origin: "realm", fill: "git-ssh (realm)", setByTeam: false },
  { role: "dev", config: "web-dev-claude", connector: "github", state: "unresolved", method: "token", required: true, fill: "MISSING", setByTeam: true },
];

test("Edit team switches GitHub on for a role, picks its identity, and shows a connector the role requires (#1649)", async ({ page }) => {
  const world = await mockTeams(page);
  world.webView.connectors = { inherited: true, items: [], roles: roleAccessRows.filter(row => row.connector === "git") };
  world.secrets = [...world.secrets, { metadata: { name: "bot-token", realm: "default" }, spec: { connector: "github", type: "opaque" } }];
  await page.route(url => url.pathname === "/api/connectors", route => route.fulfill({ json: roleAccessConnectors }));
  await page.goto(`${server.url}#/teams/web/edit`);
  const steps = wizard(page);
  for (let i = 0; i < 3; i++) await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("heading", { name: "Credentials" })).toBeVisible();
  const access = steps.getByRole("group", { name: "Per-role access" });
  const rows = access.getByRole("table", { name: "Per-role connectors" }).locator("tbody tr");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("Required by the role");
  await expect(rows.nth(0).getByRole("checkbox")).toHaveCount(0);
  await expect(rows.nth(0)).toContainText("git-ssh (realm)");
  await expect(access).not.toContainText("Claude");
  const github = access.getByRole("checkbox", { name: "GitHub for dev" });
  await expect(github).not.toBeChecked();
  await expect(access.getByLabel("Identity of github for dev")).toBeDisabled();
  await github.check();
  await access.getByLabel("Identity of github for dev").selectOption("bot-token");
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("region", { name: "Preview" })).toBeVisible();
  const sent = lastPost(world, "/api/teams/preview");
  expect(sent.form.roles.items[0].connectors).toEqual([{ name: "github", enabled: true, identity: "bot-token" }]);
  expect(sent.form.connectors).toEqual({ inherit: true, items: {} });
});

// #1745: in New team a selected role's per-role connectors start On, and Resolves to is filled from a dry run
// of the form, run in the background on the Credentials step.
test("New team starts a role's connectors on and fills Resolves to from a background dry run", async ({ page }) => {
  const world = await mockTeams(page);
  await page.route(url => url.pathname === "/api/connectors", route => route.fulfill({ json: roleAccessConnectors }));
  const previews = [];
  await page.route(url => url.pathname === "/api/teams/preview", route => {
    previews.push(route.request().postDataJSON());
    return route.fulfill({ json: { plan: world.plan("web", { kind: "file", detail: "form" }),
      connectors: [{ role: "dev", config: "web-dev-claude", connector: "github", state: "unresolved", method: "token", fill: "MISSING", setByTeam: true }],
      connectorNotes: ["web-dev-claude-pick-issue: connectors not resolved: blueprint not stored"] } });
  });
  await page.goto(`${server.url}#/new-team`);
  const steps = wizard(page);
  await steps.getByRole("textbox", { name: /^Repository address/ }).fill("github.com/acme/web");
  await steps.getByRole("button", { name: "Read repository" }).click();
  await expect(steps.getByRole("status", { name: "Repository read" })).toBeVisible();
  await steps.getByRole("button", { name: "Next" }).click();
  await steps.getByRole("button", { name: "Next" }).click();
  // A role selected here starts with every per-role connector on.
  const role = steps.getByRole("checkbox", { name: "Role dev from project" });
  await role.uncheck();
  await role.check();
  await steps.getByRole("button", { name: "Next" }).click();
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("heading", { name: "Credentials" })).toBeVisible();
  const access = steps.getByRole("group", { name: "Per-role access" });
  await expect(access.getByRole("checkbox", { name: "GitHub for dev" })).toBeChecked();
  await expect(access.getByRole("checkbox", { name: "Git for dev" })).toBeChecked();
  // The dry run answers on its own; the form stays editable meanwhile.
  await expect(access.getByRole("table", { name: "Per-role connectors" })).toBeVisible();
  const github = access.locator("tbody tr").filter({ hasText: "GitHub" });
  await expect(github.locator(".resolves-to")).toHaveText("web-dev-claude: MISSING");
  await expect(access.getByRole("list", { name: "Not resolved" })).toContainText("blueprint not stored");
  expect(previews).toHaveLength(1);
  expect(previews[0].form.roles.items[0].connectors).toEqual([{ name: "git", enabled: true, identity: "" }, { name: "github", enabled: true, identity: "" }]);
  // Review shows the background dry run's plan: no second request for the unchanged form.
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("region", { name: "Preview" })).toBeVisible();
  expect(previews).toHaveLength(1);
  await expect(steps.getByRole("button", { name: "Add team" })).toBeEnabled();
});

test("Edit team disables per-role access while the team inherits its roles (#1649)", async ({ page }) => {
  const world = await mockTeams(page);
  const inheriting = world.webForm();
  inheriting.roles = { inherit: true, items: [] };
  world.webForm = () => inheriting;
  world.webView.roles = { ...world.webView.roles, inherited: true };
  await page.route(url => url.pathname === "/api/connectors", route => route.fulfill({ json: roleAccessConnectors }));
  await page.goto(`${server.url}#/teams/web/edit`);
  const steps = wizard(page);
  for (let i = 0; i < 3; i++) await steps.getByRole("button", { name: "Next" }).click();
  const access = steps.getByRole("group", { name: "Per-role access" });
  await expect(access).toContainText("Declare the team's roles to change per-role access.");
  await expect(access.getByRole("checkbox", { name: "GitHub for dev" })).toBeDisabled();
});

// #1746: the project of team agents is the stored Source agents, which the Roles step offers again as its own
// group. A role selected through agents shows as selected in the project group too, and is not added twice.
test("Edit team agents shows a role selected through agents as selected in the project group, once", async ({ page }) => {
  const world = await mockTeams(page);
  const base = world.webForm();
  world.webForm = () => ({ ...base, project: { ...base.project, stored: "agents" } });
  await page.goto(`${server.url}#/teams/web/edit`);
  const steps = wizard(page);
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("heading", { name: "Roles and harnesses" })).toBeVisible();
  const roles = steps.getByRole("group", { name: "Roles" });
  // Both groups of the repository are listed.
  await expect(roles.getByRole("region", { name: "Source project" })).toBeVisible();
  await expect(roles.getByRole("region", { name: "Source agents" })).toBeVisible();
  const project = roles.getByRole("region", { name: "Source project" });
  await project.getByRole("button", { name: "Show contents" }).click();
  const dev = project.getByRole("checkbox", { name: "Role dev from project" });
  await expect(dev).toBeChecked();
  await expect(project.locator("label", { has: page.getByRole("checkbox", { name: "Role dev from project" }) })).toContainText("selected through agents");
  await expect(roles.locator(".wizard-role")).toHaveCount(1);
  await expect(steps.getByRole("list", { name: "Step problems" })).toHaveCount(0);
  // A role not selected yet is selected from the project group, and then shows as selected in agents.
  await project.getByRole("checkbox", { name: "Role pm from project" }).check();
  await expect(roles.locator(".wizard-role")).toHaveCount(2);
  const agents = roles.getByRole("region", { name: "Source agents" });
  await agents.getByRole("button", { name: "Show contents" }).click();
  await expect(agents.getByRole("checkbox", { name: "Role pm from agents" })).toBeChecked();
  await expect(agents.getByRole("checkbox", { name: "Role dev from agents" })).toBeChecked();
  // Unticking dev in the project group drops the entry selected through agents.
  await dev.uncheck();
  await expect(roles.locator(".wizard-role")).toHaveCount(1);
  await expect(agents.getByRole("checkbox", { name: "Role dev from agents" })).not.toBeChecked();
  for (let i = 0; i < 3; i++) await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("region", { name: "Preview" })).toBeVisible();
  expect(lastPost(world, "/api/teams/preview").form.roles.items.map(role => `${role.name}@${role.source}`)).toEqual(["pm@project"]);
});

// #1746: switching off "Use the default roles" and "Use the defaults' connector choices" starts from the defaults
// as the server's form models them, and sends them unchanged.
test("Edit team switches off the defaults and starts from the default roles and connector choices", async ({ page }) => {
  const world = await mockTeams(page);
  const inheriting = world.webForm();
  inheriting.roles = { inherit: true, items: [] };
  world.webForm = () => inheriting;
  world.webView.roles = { ...world.webView.roles, inherited: true };
  const lead = {
    name: "dev", source: "agents", path: "dev", harness: "claude", initialPath: "$HOME/work", image: ["go", "node"],
    tasks: { all: false, items: [{ name: "pick-issue", schedule: "@daily", suspend: false }] },
    connectors: [{ name: "git", enabled: false, identity: "" }, { name: "github", enabled: true, identity: "bot-token" }],
  };
  world.sources.defaultForm = { roles: [lead], connectors: { claude: { method: "subscription", identity: "claude-key", state: "ephemeral" } } };
  await page.route(url => url.pathname === "/api/connectors", route => route.fulfill({ json: roleAccessConnectors }));
  await page.goto(`${server.url}#/teams/web/edit`);
  const steps = wizard(page);
  await steps.getByRole("button", { name: "Next" }).click();
  await steps.getByRole("checkbox", { name: "Use the default roles" }).uncheck();
  await expect(steps.locator(".wizard-role select")).toHaveValue("claude");
  await expect(steps.getByLabel("Image needs")).toHaveValue("go, node");
  await expect(steps.getByLabel("Initial path")).toHaveValue("$HOME/work");
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByLabel("Select dev task pick-issue")).toBeChecked();
  await expect(steps.getByLabel("Cron of dev task pick-issue")).toHaveValue("@daily");
  await expect(steps.getByLabel("Schedule of dev task pick-issue")).toHaveValue("on");
  await steps.getByRole("button", { name: "Next" }).click();
  const access = steps.getByRole("group", { name: "Per-role access" });
  await expect(access.getByRole("checkbox", { name: "GitHub for dev" })).toBeChecked();
  await expect(access.getByLabel("Identity of github for dev")).toHaveValue("bot-token");
  await steps.getByRole("checkbox", { name: "Use the defaults' connector choices" }).uncheck();
  await expect(steps.getByLabel("Method of claude")).toHaveValue("subscription");
  await expect(steps.getByLabel("Identity of claude", { exact: true })).toHaveValue("claude-key");
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("region", { name: "Preview" })).toBeVisible();
  const sent = lastPost(world, "/api/teams/preview");
  expect(sent.form.roles).toEqual({ inherit: false, items: [lead] });
  expect(sent.form.connectors).toEqual({ inherit: false, items: world.sources.defaultForm.connectors });
  // Switching the connector choices back on sends an inherited list.
  await steps.getByRole("button", { name: "4. Credentials" }).click();
  await steps.getByRole("checkbox", { name: "Use the defaults' connector choices" }).check();
  await steps.getByRole("button", { name: "Next" }).click();
  await expect(steps.getByRole("region", { name: "Preview" })).toBeVisible();
  await expect.poll(() => lastPost(world, "/api/teams/preview").form.connectors).toEqual({ inherit: true, items: {} });
});

test("a team page lists each Config's connectors with identity, level and who asks for it (#1649)", async ({ page }) => {
  const world = await mockTeams(page);
  world.webView.connectors = { inherited: true, items: [], roles: roleAccessRows };
  await page.goto(`${server.url}#/teams/web`);
  const table = page.getByRole("region", { name: "Connectors" }).getByRole("table", { name: "Connectors per role" });
  const rows = table.locator("tbody tr");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("git-ssh");
  await expect(rows.nth(0)).toContainText("realm");
  await expect(rows.nth(0)).toContainText("Role definition");
  await expect(rows.nth(0).getByRole("link", { name: "web-dev-claude" })).toHaveAttribute("href", "#/configs/default/web/default/web-dev-claude");
  await expect(rows.nth(1).getByRole("link", { name: "Needs identity" })).toHaveAttribute("href", "#/connectors/github");
  await expect(rows.nth(1)).toContainText("Team");
});

// ---------------------------------------------------------------------------
// Exec (#1432): run a command or open a shell in a container of a running cell.
const execCell = "#/workspace/dev/shared/services/api";
// The Exec panel of the fixture's one container (`web`, not attachable).
const execPanel = page => page.getByRole("group", { name: "Exec in web" });
// Mocks the run-command route and records each request body. `reply` answers a request.
async function mockExecRun(page, reply = () => ({ json: { stdout: "1000\n", stderr: "", exitCode: 0, truncated: false, timedOut: false } }), extra = () => undefined) {
  const runs = [];
  await mock(page, (path, request) => {
    if (path.endsWith("/containers/web/exec") && request.method() === "POST") {
      runs.push(request.postDataJSON());
      return reply(runs.at(-1));
    }
    return extra(path, request);
  });
  return runs;
}
async function openExec(page) {
  await page.goto(`${server.url}${execCell}`);
  await terminalTools(page).getByRole("button", { name: "Exec" }).click();
  await expect(execPanel(page)).toBeVisible();
  return execPanel(page);
}

test("Exec runs a command as the container user and shows stdout, stderr and the exit code apart", async ({ page }) => {
  const runs = await mockExecRun(page, () => ({ json: { stdout: "1000\n", stderr: "id: warning\n", exitCode: 3, truncated: false, timedOut: false } }));
  const panel = await openExec(page);
  // The panel opens on Run command, as the container user, with the command field focused.
  await expect(panel.getByRole("radio", { name: "Container user" })).toBeChecked();
  await expect(panel.getByRole("tab", { name: "Run command" })).toHaveAttribute("aria-selected", "true");
  const command = panel.getByRole("textbox", { name: /^Command/ });
  await expect(command).toBeFocused();
  await expect(panel.getByRole("button", { name: "Run" })).toBeDisabled();
  await command.fill("id -u");
  await command.press("Enter");
  await expect(panel.locator(".exec-exit")).toHaveText("Exit code 3");
  await expect(panel.locator("pre.exec-output").nth(0)).toHaveText("1000\n");
  await expect(panel.locator("pre.exec-output").nth(1)).toHaveText("id: warning\n");
  await expect(panel.getByRole("heading", { name: "stdout" })).toBeVisible();
  await expect(panel.getByRole("heading", { name: "stderr" })).toBeVisible();
  // The request names the container identity and carries no root confirmation.
  expect(runs).toEqual([{ command: "id -u", identity: "container" }]);
  // Escape outside the terminal closes the panel and returns focus to the Exec button.
  await command.press("Escape");
  await expect(execPanel(page)).toHaveCount(0);
  await expect(terminalTools(page).getByRole("button", { name: "Exec" })).toBeFocused();
});

test("Exec as root asks for confirmation and sends confirmRoot only after it", async ({ page }) => {
  const runs = await mockExecRun(page, body => ({ json: { stdout: body.identity === "root" ? "0\n" : "1000\n", stderr: "", exitCode: 0, truncated: false, timedOut: false } }));
  const panel = await openExec(page);
  await panel.getByRole("radio", { name: "root" }).click();
  const dialog = page.getByRole("dialog", { name: "Run as root" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("dev / shared / services / api / web");
  // Nothing was sent, and root is not selected until the dialog is confirmed.
  await expect(panel.getByRole("radio", { name: "Container user" })).toBeChecked();
  expect(runs).toEqual([]);
  await dialog.getByRole("button", { name: "Run as root" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(panel.getByRole("radio", { name: "root" })).toBeChecked();
  await panel.getByRole("textbox", { name: /^Command/ }).fill("id -u");
  await panel.getByRole("button", { name: "Run" }).click();
  await expect(panel.locator("pre.exec-output").first()).toHaveText("0\n");
  expect(runs).toEqual([{ command: "id -u", identity: "root", confirmRoot: true }]);
  // Back to the container user: no confirmation travels. Root again: it was confirmed for this panel.
  await panel.getByRole("radio", { name: "Container user" }).check();
  await panel.getByRole("button", { name: "Run" }).click();
  await expect(panel.locator("pre.exec-output").first()).toHaveText("1000\n");
  await panel.getByRole("radio", { name: "root" }).check();
  await expect(page.getByRole("dialog", { name: "Run as root" })).toHaveCount(0);
  await expect(panel.getByRole("radio", { name: "root" })).toBeChecked();
  expect(runs.slice(1)).toEqual([{ command: "id -u", identity: "container" }]);
});

test("cancelling the Exec root confirmation sends nothing and keeps the container user", async ({ page }) => {
  const runs = await mockExecRun(page);
  const sockets = [];
  await page.routeWebSocket(/\/exec\/terminal/, socket => { sockets.push(socket.url()); socket.send(JSON.stringify({ type: "ready" })); });
  const panel = await openExec(page);
  await panel.getByRole("textbox", { name: /^Command/ }).fill("id -u");
  for (const dismiss of ["button", "Escape"]) {
    await panel.getByRole("radio", { name: "root" }).click();
    const dialog = page.getByRole("dialog", { name: "Run as root" });
    await expect(dialog).toBeVisible();
    if (dismiss === "button") await dialog.getByRole("button", { name: "Cancel" }).click();
    else await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    // Escape closed the dialog only; the panel stays open on the container user.
    await expect(panel.getByRole("radio", { name: "Container user" })).toBeChecked();
    await expect(panel.getByRole("radio", { name: "root" })).not.toBeChecked();
  }
  expect(runs).toEqual([]);
  expect(sockets).toEqual([]);
  // What is sent afterwards is still a container-user request.
  await panel.getByRole("button", { name: "Run" }).click();
  await expect(panel.locator(".exec-exit")).toHaveText("Exit code 0");
  expect(runs).toEqual([{ command: "id -u", identity: "container" }]);
});

test("Exec reports a truncated or timed-out run and a refused one", async ({ page }) => {
  let answer = { json: { stdout: "partial\n", stderr: "", exitCode: -1, truncated: true, timedOut: true } };
  await mockExecRun(page, () => answer);
  const panel = await openExec(page);
  await panel.getByRole("textbox", { name: /^Command/ }).fill("yes");
  await panel.getByRole("button", { name: "Run" }).click();
  await expect(panel.locator(".exec-exit")).toHaveText("Stopped after the time limit; no exit code.");
  await expect(panel.locator(".exec-result")).toContainText("longer than 60 seconds");
  await expect(panel.locator(".exec-result")).toContainText("Output above 1 MiB per stream was dropped.");
  await expect(panel.locator("pre.exec-output").nth(0)).toHaveText("partial\n");
  await expect(panel.locator("pre.exec-output").nth(1)).toHaveText("(empty)");
  // A refusal replaces the previous result with the server's message.
  answer = { status: 409, json: { error: { code: "not_running", message: "container task is not running; cannot exec into it" } } };
  await panel.getByRole("button", { name: "Run" }).click();
  await expect(panel.getByRole("alert")).toContainText("container task is not running");
  await expect(panel.locator(".exec-result")).toHaveCount(0);
});

test("Exec terminal opens a shell, shows its output, forwards input and reports the exit code", async ({ page }) => {
  await mockExecRun(page);
  const urls = [];
  const messages = [];
  let closed = 0;
  let exit = () => {};
  await page.routeWebSocket(/\/exec\/terminal/, socket => {
    urls.push(new URL(socket.url()));
    socket.onMessage(message => { messages.push(message); });
    socket.onClose(() => { closed++; });
    socket.send(JSON.stringify({ type: "ready" }));
    socket.send(Buffer.from("agent@api:~$ "));
    exit = () => { socket.send(JSON.stringify({ type: "exit", code: 3 })); socket.close(); };
  });
  const panel = await openExec(page);
  // No shell starts until the Terminal tab is shown; the arrow keys move between the two tabs.
  expect(urls).toEqual([]);
  await panel.getByRole("tab", { name: "Run command" }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(panel.getByRole("tab", { name: "Terminal" })).toHaveAttribute("aria-selected", "true");
  await expect(panel.getByRole("tab", { name: "Terminal" })).toBeFocused();
  const screen = panel.getByLabel("Exec terminal for dev / shared / services / api / web");
  await expect(screen.locator(".xterm-rows")).toContainText("agent@api:~$");
  await expect(panel.locator(".exec-terminal-status")).toContainText("Connected");
  expect(urls).toHaveLength(1);
  expect(urls[0].pathname).toBe("/api/realms/dev/spaces/shared/stacks/services/cells/api/containers/web/exec/terminal");
  expect(urls[0].searchParams.get("identity")).toBe("container");
  expect(urls[0].searchParams.has("confirmRoot")).toBe(false);
  await expect.poll(() => messages.some(message => typeof message === "string" && JSON.parse(message).type === "resize")).toBe(true);
  // Typed bytes reach the shell, and Escape typed into it does not close the panel.
  await screen.locator(".xterm-helper-textarea").press("a");
  await screen.locator(".xterm-helper-textarea").press("Escape");
  await expect.poll(() => messages.filter(Buffer.isBuffer).map(message => message.toString()).join("")).toBe("a\x1b");
  await expect(panel).toBeVisible();
  exit();
  await expect(panel.locator(".exec-terminal-status")).toContainText("Process exited with code 3");
  await expect(screen.locator(".xterm-rows")).toContainText("Process exited with code 3");
  // An ended process is not reconnected to: only a new shell is offered, and it is a new connection.
  await expect(panel.getByRole("button", { name: /Reconnect|Attach/ })).toHaveCount(0);
  await panel.getByRole("button", { name: "New shell" }).click();
  await expect.poll(() => urls.length).toBe(2);
  await expect(panel.locator(".exec-terminal-status")).toContainText("Connected");
  // Closing the panel closes the shell's connection, which is what kills its process.
  const before = closed;
  await panel.getByRole("button", { name: "Close Exec" }).click();
  await expect(execPanel(page)).toHaveCount(0);
  await expect.poll(() => closed).toBeGreaterThan(before);
});

test("Exec terminal as root connects with the confirmation only after it is given", async ({ page }) => {
  await mockExecRun(page);
  const urls = [];
  await page.routeWebSocket(/\/exec\/terminal/, socket => {
    urls.push(new URL(socket.url()));
    socket.send(JSON.stringify({ type: "ready" }));
    socket.send(Buffer.from("# "));
  });
  const panel = await openExec(page);
  await panel.getByRole("tab", { name: "Terminal" }).click();
  await expect.poll(() => urls.length).toBe(1);
  await panel.getByRole("radio", { name: "root" }).click();
  await expect(page.getByRole("dialog", { name: "Run as root" })).toBeVisible();
  expect(urls).toHaveLength(1);
  await page.getByRole("dialog", { name: "Run as root" }).getByRole("button", { name: "Run as root" }).click();
  // The container-user shell is replaced by a root one.
  await expect.poll(() => urls.length).toBe(2);
  expect(urls[1].searchParams.get("identity")).toBe("root");
  expect(urls[1].searchParams.get("confirmRoot")).toBe("1");
  await expect(panel.locator(".exec-terminal-status")).toContainText("Connected (root)");
});

test("Exec is disabled with a reason while the cell is not running", async ({ page }) => {
  const runs = await mockExecRun(page, undefined, path => path.endsWith("/cells") && path.includes("/dev/")
    ? { json: fixture(path).map(cell => ({ ...cell, status: { state: "Stopped" } })) } : undefined);
  await page.goto(`${server.url}${execCell}`);
  const exec = terminalTools(page).getByRole("button", { name: "Exec" });
  await expect(exec).toBeDisabled();
  await expect(exec).toHaveAttribute("title", "Exec unavailable: cell is Stopped.");
  await expect(execPanel(page)).toHaveCount(0);
  expect(runs).toEqual([]);
});

test("Exec is offered for attachable and non-attachable containers and follows the selected one", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }, { id: "sidecar" }, { id: "pause", root: true }] } : undefined);
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); socket.send(Buffer.from("attached$ ")); });
  await page.goto(`${server.url}${execCell}`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  await terminalTools(page).getByRole("button", { name: "Exec" }).click();
  await expect(page.getByRole("group", { name: "Exec in shell" })).toBeVisible();
  // The attach terminal stays connected behind the panel and comes back when it closes.
  await expect(page.getByLabel("Terminal for dev / shared / services / api / shell")).toBeHidden();
  // Picking another container closes the panel: it belonged to the first one.
  await terminalTools(page).getByLabel("Container").selectOption("sidecar");
  await expect(page.getByRole("group", { name: /^Exec in/ })).toHaveCount(0);
  await terminalTools(page).getByRole("button", { name: "Exec" }).click();
  await expect(page.getByRole("group", { name: "Exec in sidecar" })).toBeVisible();
  await page.getByRole("group", { name: "Exec in sidecar" }).getByRole("button", { name: "Close Exec" }).click();
  await terminalTools(page).getByLabel("Container").selectOption("shell");
  await expect(page.getByLabel("Terminal for dev / shared / services / api / shell")).toBeVisible();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
});

// ---------------------------------------------------------------------------
// New agent and display names (#1692). A stateful mock of the agent routes: the template Configs of the
// default team, the agent copies and their cells, all in dev / shared / services like the other fixtures.
function agentWorld({ templates = ["claude", "codex"], configs = [], cells = [] } = {}) {
  const scope = { realm: "dev", space: "shared", stack: "services" };
  const world = { posts: [], renames: [], configs: [...configs], cells: [...cells], failCreate: undefined };
  for (const harness of templates) world.configs.push({ metadata: { name: `default-agent-${harness}`, ...scope, labels: { "kukeon.io/team": "default", "kukeon.io/role": "agent", "kukeon.io/harness": harness, "kukeon.io/template": "true" } } });
  world.agentCell = (name, config, { createdAt = "2026-11-26T10:00:00Z", annotations } = {}) => ({
    apiVersion: "v1beta1", kind: "Cell",
    metadata: { name, labels: { "kukeon.io/config": config.metadata.name, "kukeon.io/role": "agent", "kukeon.io/harness": config.metadata.labels["kukeon.io/harness"] }, ...(annotations ? { annotations } : {}) },
    spec: { realmId: "dev", spaceId: "shared", stackId: "services", containers: [{ id: "agent", attachable: true }], provenance: { bindingKind: "config", bindingRef: { name: config.metadata.name, ...scope } } },
    status: { state: "Ready", createdAt },
  });
  world.copy = (name, harness, displayName) => ({ metadata: { name, ...scope, labels: { "kukeon.io/role": "agent", "kukeon.io/harness": harness }, annotations: { "kukeon.io/agent-template": `default-agent-${harness}`, ...(displayName ? { "kukeon.io/display-name": displayName } : {}) } } });
  world.install = async page => {
    await mock(page, path => {
      if (path === "/api/realms/dev/spaces/shared/stacks/services/cells") return { json: world.cells };
      if (path.endsWith("/cells") && path.startsWith("/api/realms/prod")) return { json: [] };
      const found = world.cells.find(cell => path === `/api/realms/dev/spaces/shared/stacks/services/cells/${cell.metadata.name}`);
      if (found) return { json: found };
      if (path.endsWith("/containers")) return { json: [{ id: "agent", attachable: true }] };
      return undefined;
    });
    await page.route(url => url.pathname === "/api/catalog/configs", route => route.fulfill({ json: world.configs }));
    await page.route(url => url.pathname.startsWith("/api/catalog/configs/"), route => {
      const url = new URL(route.request().url());
      const config = world.configs.find(item => url.pathname === `/api/catalog/configs/${item.metadata.name}`);
      return route.fulfill(config ? { json: { apiVersion: "v1beta1", kind: "CellConfig", ...config, spec: { blueprint: { name: "codex", realm: "dev" } } } } : { status: 404, json: { error: { code: "not_found", message: "config not found" } } });
    });
    await page.route(url => url.pathname === "/api/agents/templates", route => route.fulfill({ json: { templates: ["claude", "codex"].map(harness => ({ harness, name: `default-agent-${harness}`, ...scope, exists: templates.includes(harness) })) } }));
    await page.route(url => url.pathname === "/api/agents", async route => {
      const body = route.request().postDataJSON();
      world.posts.push(body);
      if (world.failCreate) return route.fulfill(world.failCreate);
      const config = world.copy(`default-agent-${body.harness}-a1b2c3`, body.harness, body.displayName);
      world.configs.push(config);
      const cell = world.agentCell(`${config.metadata.name}-d4e5f6`, config);
      world.cells.push(cell);
      await route.fulfill({ json: { config: config.metadata, cell: { name: cell.metadata.name, ...scope } } });
    });
    await page.route(url => url.pathname === "/api/configs/display-name", async route => {
      const body = route.request().postDataJSON();
      world.renames.push({ kind: "config", ...body });
      const config = world.configs.find(item => item.metadata.name === body.config.name);
      const annotations = { ...config.metadata.annotations };
      if (body.displayName) annotations["kukeon.io/display-name"] = body.displayName; else delete annotations["kukeon.io/display-name"];
      config.metadata = { ...config.metadata, annotations };
      await route.fulfill({ json: { config: config.metadata } });
    });
    await page.route(url => url.pathname.endsWith("/display-name") && url.pathname.startsWith("/api/realms/"), async route => {
      const body = route.request().postDataJSON();
      const name = new URL(route.request().url()).pathname.split("/").at(-2);
      world.renames.push({ kind: "cell", name, ...body });
      const cell = world.cells.find(item => item.metadata.name === name);
      const annotations = { ...cell.metadata.annotations };
      if (body.displayName) annotations["kukeon.io/display-name"] = body.displayName; else delete annotations["kukeon.io/display-name"];
      cell.metadata = { ...cell.metadata, annotations };
      await route.fulfill({ json: { cell: { name, ...scope, annotations } } });
    });
    await page.routeWebSocket("**/attach", () => {});
  };
  return world;
}

test("New agent creates a named Codex agent, opens its terminal and shows its display name everywhere", async ({ page }) => {
  const world = agentWorld();
  await world.install(page);
  await page.goto(server.url);
  // Linked from the Workbench heading and its empty state.
  await expect(grid(page).getByRole("link", { name: "New agent" })).toHaveAttribute("href", "#/agents/new");
  await page.locator(".page-heading").getByRole("link", { name: "New agent" }).click();
  await expect(page).toHaveURL(/#\/agents\/new$/);
  await expect(page.locator(".page-heading h1")).toHaveText("New agent");
  const form = page.getByRole("region", { name: "New agent" });
  await expect(form.locator(".agent-card")).toHaveText([/Claude.*Template ready/, /Codex.*Template ready/]);
  await form.getByRole("radio", { name: /Codex/ }).check();
  await form.getByLabel(/^Name/).fill("Docs bot");
  await form.getByRole("button", { name: "Create" }).click();
  // The new cell's terminal opens.
  const cell = "default-agent-codex-a1b2c3-d4e5f6";
  await expect(page).toHaveURL(new RegExp(`#/workspace/dev/shared/services/${cell}$`));
  expect(world.posts).toEqual([{ harness: "codex", displayName: "Docs bot" }]);
  await expect(page.locator(".cell-page-title-row h1")).toHaveText("Docs bot #1");
  await expect(page.locator(".cell-page-title-row .cell-page-name")).toHaveText(cell);
  // Sidebar: the agent is listed by its display name, its Config name secondary.
  const agent = page.getByRole("navigation", { name: "Cell navigation" }).getByRole("region", { name: "Docs bot agent" });
  await expect(agent.locator(".agent-name")).toContainText("Docs bot");
  await expect(agent.locator(".agent-internal")).toHaveText("default-agent-codex-a1b2c3");
  // Grid: the agent's section and its instance tile.
  await page.goto(`${server.url}#/`);
  const section = grid(page).getByRole("region", { name: "Docs bot agent" });
  await expect(section.locator(".cell-grid-heading")).toContainText("Docs bot");
  await expect(section.locator(".cell-tile-name")).toHaveText("Docs bot #1");
  await expect(section.locator(".cell-tile-cell")).toHaveText(cell);
  // Rail: the agent glyph is named after the agent.
  const sidebar = page.getByRole("complementary", { name: "Cell sidebar" });
  await sidebar.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(sidebar.getByRole("navigation", { name: "Cell shortcuts" }).getByRole("link", { name: `Agent Docs bot, 1 instance: open dev / shared / services / ${cell} (Ready)` })).toBeVisible();
  // The template Configs are never agents, nor Other cells.
  await expect(page.locator(".cell-grid-group")).toHaveCount(1);
});

test("an unnamed agent copy is listed as Claude agent, and renaming the agent and an instance updates every surface without a reload", async ({ page }) => {
  const world = agentWorld();
  const copy = world.copy("default-agent-claude-0a0b0c", "claude");
  world.configs.push(copy);
  world.cells.push(world.agentCell("default-agent-claude-0a0b0c-111111", copy, { createdAt: "2026-11-26T09:00:00Z" }), world.agentCell("default-agent-claude-0a0b0c-222222", copy, { createdAt: "2026-11-26T10:00:00Z" }));
  await world.install(page);
  let loads = 0;
  page.on("load", () => { loads += 1; });
  await page.goto(`${server.url}#/resource/dev/shared/services/default-agent-claude-0a0b0c-111111`);
  const nav = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(nav.getByRole("region", { name: "Claude agent agent" })).toBeVisible();
  await expect(nav.locator(".cell-link-name")).toHaveText(["Claude agent #1", "Claude agent #2"]);
  await expect(page.locator(".cell-page-title-row h1")).toHaveText("Claude agent #1");
  const overview = page.locator(".resource-overview");
  await expect(overview.locator(".instance-display-name")).toHaveText("Listed as Claude agent #1");
  await expect(overview.locator(".agent-display-name")).toHaveText("Agent: Claude agent");

  // Rename the instance.
  await overview.getByRole("button", { name: "Rename instance dev / shared / services / default-agent-claude-0a0b0c-111111" }).click();
  await overview.getByRole("form", { name: /^Rename instance/ }).getByLabel("Display name").fill("Night shift");
  await overview.getByRole("button", { name: "Save" }).click();
  await expect(nav.locator(".cell-link-name")).toHaveText(["Night shift", "Claude agent #2"]);
  await expect(page.locator(".cell-page-title-row h1")).toHaveText("Night shift");
  await expect(overview.locator(".instance-display-name")).toHaveText("Display name: Night shift");

  // Rename the agent: the agent row and the other instance follow.
  await overview.getByRole("button", { name: "Rename agent dev / shared / services / default-agent-claude-0a0b0c" }).click();
  await overview.getByRole("form", { name: /^Rename agent/ }).getByLabel("Display name").fill("Reviewer");
  await overview.getByRole("button", { name: "Save" }).click();
  await expect(nav.getByRole("region", { name: "Reviewer agent" })).toBeVisible();
  await expect(nav.locator(".cell-link-name")).toHaveText(["Night shift", "Reviewer #2"]);
  expect(world.renames).toEqual([
    { kind: "cell", name: "default-agent-claude-0a0b0c-111111", displayName: "Night shift" },
    { kind: "config", config: { name: "default-agent-claude-0a0b0c", realm: "dev", space: "shared", stack: "services" }, displayName: "Reviewer" },
  ]);

  // Clearing the instance's name brings its ordinal back.
  await overview.getByRole("button", { name: /^Rename instance/ }).click();
  await overview.getByRole("form", { name: /^Rename instance/ }).getByLabel("Display name").fill("");
  await overview.getByRole("button", { name: "Save" }).click();
  await expect(nav.locator(".cell-link-name")).toHaveText(["Reviewer #1", "Reviewer #2"]);
  // The Config page shows the agent's display name with its own Rename action.
  await page.goto(`${server.url}#/configs/dev/shared/services/default-agent-claude-0a0b0c`);
  await expect(page.locator(".details-grid .display-name-row")).toContainText("Reviewer");
  await expect(page.getByRole("button", { name: "Rename agent dev / shared / services / default-agent-claude-0a0b0c" })).toBeVisible();
  expect(loads).toBe(1);
});

test("template Configs carry a Template badge in Manage → Configs and are left out of the agent lists", async ({ page }) => {
  const world = agentWorld();
  // A cell someone ran from the template itself: neither an agent nor an Other cell.
  const template = world.configs.find(config => config.metadata.name === "default-agent-claude");
  world.cells.push({ ...world.agentCell("default-agent-claude-ffffff", template), metadata: { ...world.agentCell("default-agent-claude-ffffff", template).metadata, labels: { ...template.metadata.labels, "kukeon.io/config": "default-agent-claude" } } });
  const copy = world.copy("default-agent-codex-123456", "codex");
  world.configs.push(copy);
  world.cells.push(world.agentCell("default-agent-codex-123456-abcdef", copy));
  await world.install(page);
  await page.goto(server.url);
  const nav = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(nav.getByRole("region", { name: /agent$/ })).toHaveCount(1);
  await expect(nav.getByRole("region", { name: "Codex agent agent" })).toBeVisible();
  await expect(nav.getByRole("region", { name: "Other cells" })).toHaveCount(0);
  await expect(grid(page).locator(".cell-tile")).toHaveCount(1);
  await page.goto(`${server.url}#/configs`);
  const row = name => page.locator("tbody tr").filter({ has: page.getByRole("link", { name, exact: true }) });
  await expect(row("default-agent-claude").locator(".template-badge")).toHaveText("Template");
  await expect(row("default-agent-codex").locator(".template-badge")).toHaveText("Template");
  await expect(row("default-agent-codex-123456").locator(".template-badge")).toHaveCount(0);
  await row("default-agent-claude").getByRole("link", { name: "default-agent-claude", exact: true }).click();
  await expect(page.locator(".details-grid .template-badge")).toHaveText("Template");
  await expect(page.getByRole("button", { name: /^Rename agent/ })).toHaveCount(0);
});

test("New agent says when no template exists, and names the step that failed after the Config was stored", async ({ page }) => {
  const none = agentWorld({ templates: [] });
  await none.install(page);
  await page.goto(`${server.url}#/agents/new`);
  const form = page.getByRole("region", { name: "New agent" });
  await expect(form.getByRole("note")).toContainText("No agent templates");
  await expect(form.getByRole("note").getByRole("link", { name: "Teams" })).toHaveAttribute("href", "#/teams");
  await expect(form.getByRole("button", { name: "Create" })).toHaveCount(0);
  await expect(form.getByRole("radio")).toHaveCount(2);
  for (const radio of await form.getByRole("radio").all()) await expect(radio).toBeDisabled();
  await expect(form.locator(".agent-card")).toHaveText([/Template missing/, /Template missing/]);
  await page.unrouteAll({ behavior: "ignoreErrors" });

  // Only Claude's template: it is picked, and a start failure keeps the stored Config visible.
  const claude = agentWorld({ templates: ["claude"] });
  claude.failCreate = { status: 503, json: { error: { code: "unavailable", message: "kukeond did not answer", step: "start-cell", config: { name: "default-agent-claude-a1b2c3", realm: "dev", space: "shared", stack: "services" }, cell: { name: "default-agent-claude-a1b2c3-d4e5f6", realm: "dev", space: "shared", stack: "services" } } } };
  await signIn(page);
  await claude.install(page);
  await page.reload();
  await expect(form.getByRole("radio", { name: /Claude/ })).toBeChecked();
  await expect(form.getByRole("radio", { name: /Codex/ })).toBeDisabled();
  await expect(form.getByLabel(/^Name/)).toHaveAttribute("maxlength", "63");
  await form.getByRole("button", { name: "Create" }).click();
  await expect(form.getByRole("alert")).toContainText("Agent stored, but not started");
  await expect(form.getByRole("alert")).toContainText("starting its first cell failed: kukeond did not answer");
  await expect(form.getByRole("alert").getByRole("link", { name: "default-agent-claude-a1b2c3" })).toHaveAttribute("href", "#/configs/dev/shared/services/default-agent-claude-a1b2c3");
  expect(claude.posts).toEqual([{ harness: "claude" }]);
  await expect(page).toHaveURL(/#\/agents\/new$/);
});

// ---------------------------------------------------------------------------
// First-run onboarding (#1694). One mutable host, fresh by default: no team, no Config, no Blueprint.
// `initialize()` is what POST /api/teams/init stores — the kuke and default teams, the majordomo Config
// and the agent template of the test-only `demo` harness, their Blueprint, and the two state volumes the
// Configs log in through. Every connector here is test-only: nothing in the UI names one.
function onboardingHost({ initialized = false } = {}) {
  const scope = { realm: "default", space: "default", stack: "default" };
  const team = (name, configs) => ({ name, realm: "default", roles: [], rolesInherited: false, harnesses: ["demo"], harnessesInherited: false, configs, system: true });
  const login = (name, config, state = "empty") => ({ connector: "demo", method: "login", volume: { name, ...scope }, exists: false, tagged: false, login: { state }, usedBy: [{ name: config, ...scope }] });
  const loginStatus = volume => [{ connector: "demo", title: "Demo", component: "demo-login", method: "login", delivery: "stateVolume", state: "resolved", volume, volumeRef: { name: volume, ...scope }, login: { state: "empty" } }];
  const host = {
    scope, teams: [], configs: [], blueprints: [], secrets: [], logins: [], cells: [], requests: [],
    templates: [{ harness: "demo", name: "default-agent-demo", ...scope, exists: false }],
    connectors: [...structuredClone(connectorWorld().connectors), structuredClone(testTokenConnector)],
    // How POST /api/teams/init answers: "ok", "git" (no git on the host) or "conflict" (already stored).
    init: "ok",
    // When set, the init reply waits for it: the test reads the progress text first.
    gate: undefined,
    initialize() {
      host.teams = [team("default", ["default-agent-demo"]), team("kuke", ["kuke-majordomo-demo"])];
      host.blueprints = [{ apiVersion: "v1beta1", kind: "CellBlueprint", metadata: { name: "demo-host", realm: "default" }, spec: {} }];
      host.configs = [
        { apiVersion: "v1beta1", kind: "CellConfig", metadata: { name: "kuke-majordomo-demo", ...scope, labels: { "kukeon.io/team": "kuke", "kukeon.io/role": "majordomo", "kukeon.io/harness": "demo" } }, spec: { blueprint: { name: "demo-host", realm: "default" } }, status: { connectors: loginStatus("kuke-majordomo-demo-state") } },
        { apiVersion: "v1beta1", kind: "CellConfig", metadata: { name: "default-agent-demo", ...scope, labels: { "kukeon.io/team": "default", "kukeon.io/role": "agent", "kukeon.io/harness": "demo", "kukeon.io/template": "true" } }, spec: { blueprint: { name: "demo-host", realm: "default" } }, status: { connectors: loginStatus("default-agent-demo-state") } },
      ];
      host.logins = [login("default-agent-demo-state", "default-agent-demo"), login("kuke-majordomo-demo-state", "kuke-majordomo-demo")];
      host.templates = host.templates.map(template => ({ ...template, exists: true }));
    },
    // The agent New agent stores: a copy of the template and its first cell.
    launchAgent() {
      const config = { apiVersion: "v1beta1", kind: "CellConfig", metadata: { name: "default-agent-demo-a1b2c3", ...scope, labels: { "kukeon.io/role": "agent", "kukeon.io/harness": "demo" }, annotations: { "kukeon.io/agent-template": "default-agent-demo" } }, spec: { blueprint: { name: "demo-host", realm: "default" } } };
      host.configs.push(config);
      const cell = { apiVersion: "v1beta1", kind: "Cell", metadata: { name: `${config.metadata.name}-d4e5f6`, labels: { "kukeon.io/config": config.metadata.name } }, spec: { realmId: "default", spaceId: "default", stackId: "default", containers: [{ id: "agent", attachable: true }], provenance: { bindingKind: "config", bindingRef: { name: config.metadata.name, ...scope } } }, status: { state: "Ready", createdAt: "2026-11-26T10:00:00Z" } };
      host.cells.push(cell);
      return { config, cell };
    },
    // The identity cell of a login session ends: the volume holds a login and the cell is gone.
    endLogin(volume) {
      const item = host.logins.find(entry => entry.volume.name === volume);
      delete item.session;
      Object.assign(item, { exists: true, tagged: true, login: { state: "logged in", since: "2026-11-26T10:05:00Z" } });
      host.cells = host.cells.filter(cell => cell.metadata.name !== `login-${volume}`);
      for (const config of host.configs) for (const status of config.status?.connectors ?? []) if (status.volume === volume) status.login = { state: "logged in" };
    },
    posts: path => host.requests.filter(request => request.method === "POST" && request.path === path),
  };
  if (initialized) host.initialize();
  host.install = async page => {
    const named = name => host.cells.find(cell => cell.metadata.name === name);
    const ready = name => ({ metadata: { name }, status: { state: "Ready" } });
    await page.route("**/api/realms**", route => {
      const parts = new URL(route.request().url()).pathname.split("/").filter(Boolean).slice(1);
      if (parts.length === 1 || parts.length === 3 || parts.length === 5) return route.fulfill({ json: [ready("default")] });
      if (parts.length === 7) return route.fulfill({ json: host.cells });
      if (parts.length === 8) { const found = named(decodeURIComponent(parts[7])); return found ? route.fulfill({ json: found }) : route.fulfill({ status: 404, json: { error: { code: "not_found", message: "cell not found" } } }); }
      if (parts.at(-1) === "screenshot") return route.fulfill(noScreen);
      if (parts.at(-1) === "containers") return route.fulfill({ json: [{ id: "agent", attachable: true }] });
      return route.fulfill({ json: ready(parts.at(-1)) });
    });
    await page.route(url => url.pathname.startsWith("/api/catalog/"), route => {
      const [, , , kind, name] = new URL(route.request().url()).pathname.split("/");
      const list = { configs: host.configs, blueprints: host.blueprints, secrets: host.secrets, volumes: [] }[kind] ?? [];
      if (!name) return route.fulfill({ json: list });
      const found = list.find(item => item.metadata.name === decodeURIComponent(name));
      return found ? route.fulfill({ json: found }) : route.fulfill({ status: 404, json: { error: { code: "not_found", message: "not found" } } });
    });
    await page.route(url => url.pathname === "/api/teams" || url.pathname.startsWith("/api/teams/"), async route => {
      const request = route.request();
      const path = new URL(request.url()).pathname;
      host.requests.push({ method: request.method(), path, contentType: request.headers()["content-type"], body: request.method() === "POST" ? request.postData() : undefined });
      if (request.method() === "GET" && path === "/api/teams") return route.fulfill({ json: { teams: host.teams } });
      if (path === "/api/teams/init") {
        await host.gate;
        if (host.init === "git") return route.fulfill({ status: 422, json: { error: { code: "git_missing", message: "git is not installed: install git on the host running kuke serve, then initialize again" } } });
        if (host.init === "conflict" || host.teams.some(item => item.name === "kuke")) return route.fulfill({ status: 409, json: { error: { code: "already_initialized", message: "already initialized: the kuke team is stored; reapply it to recompose it from its stored declaration" } } });
        host.initialize();
        return route.fulfill({ json: { teams: [{ team: "kuke", resources: 5, configs: ["kuke-majordomo-demo"], blueprints: ["demo-host"], missingSecrets: [], notes: [] }, { team: "default", resources: 2, configs: ["default-agent-demo"], blueprints: ["demo-host"], missingSecrets: [], notes: [] }], notes: [] } });
      }
      if (path === "/api/teams/kuke/reapply") { if (!host.teams.length) host.initialize(); return route.fulfill({ json: { plan: { command: "kuke team apply kuke", origin: { kind: "stored" }, sources: [], pairs: [], create: [], rerender: [], prune: [], missingSecrets: [], createSecrets: [], notes: [], declaration: "" }, result: { team: "kuke", resources: 5, pruned: [], missingSecrets: [], notes: [] } } }); }
      return route.fulfill({ status: 404, json: { error: { code: "not_found", message: path } } });
    });
    await page.route(url => url.pathname === "/api/sources", route => route.fulfill({ json: { sources: [], defaultRoles: [], defaultHarnesses: [] } }));
    await page.route(url => url.pathname === "/api/agents/templates", route => route.fulfill({ json: { templates: host.templates } }));
    await page.route(url => url.pathname === "/api/agents", route => {
      host.requests.push({ method: "POST", path: "/api/agents", body: route.request().postData() });
      const { config, cell } = host.launchAgent();
      return route.fulfill({ json: { config: config.metadata, cell: { name: cell.metadata.name, ...scope } } });
    });
    await page.route(url => url.pathname === "/api/health/reports", route => route.fulfill({ json: [] }));
    await page.route(url => url.pathname === "/api/connectors" || url.pathname.startsWith("/api/connectors/"), route => {
      const request = route.request();
      const parts = new URL(request.url()).pathname.split("/").filter(Boolean);
      if (request.method() === "GET") {
        if (parts.length === 2) return route.fulfill({ json: host.connectors });
        if (parts[2] === "logins") return route.fulfill({ json: host.logins });
        if (parts[2] === "status") return route.fulfill({ json: host.connectors.map(connector => ({ connector: connector.name, configs: 0, missing: [] })) });
        return route.fulfill({ json: [] });
      }
      const body = request.postDataJSON();
      host.requests.push({ method: "POST", path: `/${parts.join("/")}`, body: request.postData() });
      // "Log in": the identity cell of the named Volume starts, and the reply locates it.
      if (parts[3] === "login") {
        const item = host.logins.find(entry => entry.connector === parts[2] && entry.volume.name === body.name);
        if (!item) return route.fulfill({ status: 400, json: { error: { code: "invalid_request", message: `no login identity ${body.name}` } } });
        const cell = `login-${body.name}`;
        item.session = { ...scope, cell, container: "login" };
        if (!named(cell)) host.cells.push({ apiVersion: "v1beta1", kind: "Cell", metadata: { name: cell }, spec: { realmId: "default", spaceId: "default", stackId: "default", containers: [{ id: "login", attachable: true }], tty: { default: "login" } }, status: { state: "Ready" } });
        return route.fulfill({ json: { identity: item, created: false } });
      }
      return route.fulfill({ status: 404, json: { error: { code: "not_found", message: parts.join("/") } } });
    });
    await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  };
  return host;
}
const getStarted = page => content(page).getByRole("region", { name: "Get started" });
const startedStep = (page, id) => getStarted(page).locator(`[data-step="${id}"]`);
const steps = page => getStarted(page).getByRole("listitem");
// Each step is named "<title>: <state>".
const stepNames = page => steps(page).evaluateAll(items => items.map(item => item.getAttribute("aria-label")));
// The page was not reloaded: a mark set on window before an action is still there after it.
const markPage = page => page.evaluate(() => { window.onboardingMark = true; });
const pageMarked = page => page.evaluate(() => window.onboardingMark === true);

test("Initialize replaces the kuke team init empty states and stores the system teams without a reload", async ({ page }) => {
  const host = onboardingHost();
  await host.install(page);
  // New Config and Sources name the button first and keep the command as secondary text.
  await page.goto(`${server.url}#/new-config`);
  await expect(content(page).getByRole("note")).toContainText("No Blueprints are available.");
  await expect(content(page).getByRole("button", { name: "Initialize" })).toBeVisible();
  await expect(content(page).locator(".initialize-cli")).toHaveText("Or run kuke team init on the host.");
  await page.goto(`${server.url}#/sources`);
  await expect(content(page).getByRole("region", { name: "Sources list" }).getByRole("button", { name: "Initialize" })).toBeVisible();
  await page.goto(`${server.url}#/agents/new`);
  await expect(page.getByRole("region", { name: "New agent" }).getByRole("note").getByRole("button", { name: "Initialize" })).toBeVisible();

  await page.goto(`${server.url}#/teams`);
  const list = content(page).getByRole("region", { name: "Teams list" });
  await expect(list.getByRole("heading", { name: "No team is stored" })).toBeVisible();
  const initialize = list.getByRole("button", { name: "Initialize" });
  await expect(initialize).toBeEnabled();
  await expect(list.locator(".initialize-cli")).toContainText("kuke team init");
  // The kuke-team note would repeat the prompt of the empty list.
  await expect(content(page).getByRole("button", { name: "Initialize" })).toHaveCount(1);
  await markPage(page);
  let release;
  host.gate = new Promise(resolve => { release = resolve; });
  await initialize.click();
  // While the server fetches the kukeon source every prompt on screen says so, the sidebar's included.
  await expect(list.getByRole("status")).toHaveText("Initializing… fetching kukeon source");
  await expect(initialize).toBeDisabled();
  await expect(page.getByRole("region", { name: "Majordomo", exact: true }).getByRole("button", { name: "Initialize", exact: true })).toBeDisabled();
  release();
  await expect(list.getByRole("row").filter({ hasText: "kuke" }).getByRole("link", { name: "kuke", exact: true })).toHaveAttribute("href", "#/teams/kuke");
  await expect(list.getByRole("link", { name: "default", exact: true })).toBeVisible();
  await expect(list.getByRole("button", { name: "Initialize" })).toHaveCount(0);
  await expect(toasts(page).filter({ hasText: "Initialized." })).toContainText("Stored teams kuke and default: 2 Configs, 1 Blueprint.");
  expect(await pageMarked(page)).toBe(true);
  expect(host.posts("/api/teams/init")).toEqual([{ method: "POST", path: "/api/teams/init", contentType: "application/json", body: "{}" }]);
  // The catalog was re-read too: New Config now offers the Blueprint, and the sidebar's majordomo is ready to enable.
  await expect(page.getByRole("region", { name: "Majordomo", exact: true })).toContainText("Not enabled");
  await page.goto(`${server.url}#/new-config`);
  await expect(content(page).getByRole("combobox", { name: "Blueprint" }).locator("option")).toContainText(["Select a Blueprint…", "demo-host — default"]);
  await expect(content(page).getByRole("button", { name: "Initialize" })).toHaveCount(0);
});

test("a second Initialize is answered with a reapply offer, and a host without git says to install it", async ({ page }) => {
  const host = onboardingHost();
  host.init = "git";
  await host.install(page);
  await page.goto(`${server.url}#/teams`);
  const list = content(page).getByRole("region", { name: "Teams list" });
  await list.getByRole("button", { name: "Initialize" }).click();
  const alert = list.getByRole("alert");
  await expect(alert).toContainText("git is not installed");
  await expect(alert).toContainText("install git on the host running kuke serve");
  // Nothing was stored, and the button is there to try again.
  await expect(list.getByRole("heading", { name: "No team is stored" })).toBeVisible();
  await expect(list.getByRole("button", { name: "Initialize" })).toBeEnabled();

  // Another session initialized the host meanwhile: the 409 becomes the reapply offer.
  host.init = "conflict";
  await list.getByRole("button", { name: "Initialize" }).click();
  const offer = list.getByRole("group", { name: "Already initialized" });
  await expect(offer).toContainText("The kuke team is stored; reapply recomposes it from its stored declaration.");
  await expect(list.getByRole("alert")).toHaveCount(0);
  await expect(list.getByRole("button", { name: "Initialize" })).toHaveCount(0);
  await expect(list.locator(".initialize-cli")).toContainText("kuke team init --reapply");
  await offer.getByRole("button", { name: "Reapply" }).click();
  await expect(toasts(page).filter({ hasText: "Team kuke reapplied." })).toBeVisible();
  expect(host.posts("/api/teams/kuke/reapply")).toHaveLength(1);
  expect(host.posts("/api/teams/init")).toHaveLength(2);
  await expect(list.getByRole("link", { name: "kuke", exact: true })).toBeVisible();
});

test("the Workbench shows Get started with five steps whose actions reach their targets and complete in place", async ({ page }) => {
  const host = onboardingHost();
  await host.install(page);
  await page.goto(server.url);
  const card = getStarted(page);
  await expect(card.getByRole("heading", { name: "Get started" })).toBeVisible();
  // Titles come from the connector catalog: the harness connector, the git connector and the one whose token serves git.
  await expect.poll(() => stepNames(page)).toEqual(["Initialize: To do", "Log in to Demo: To do", "Set up Repo and Forge: Recommended", "Enable majordomo: Optional", "Launch your first agent: To do"]);
  await expect(card.locator(".panel-heading .badge")).toHaveText("0 of 3 done");
  await expect(startedStep(page, "login")).toContainText("after Initialize");
  await markPage(page);

  // Initialize, in place.
  await startedStep(page, "initialize").getByRole("button", { name: "Initialize" }).click();
  await expect(startedStep(page, "initialize")).toHaveAccessibleName("Initialize: Done");
  await expect(card.locator(".panel-heading .badge")).toHaveText("1 of 3 done");
  await expect(startedStep(page, "initialize").getByRole("button")).toHaveCount(0);

  // Recommended and optional steps link to their targets.
  await expect(startedStep(page, "git-identity").getByRole("link", { name: "New Repo identity" })).toHaveAttribute("href", "#/connectors/repo/new-identity");
  await expect(startedStep(page, "agent").getByRole("link", { name: "New agent" })).toHaveAttribute("href", "#/agents/new");
  await startedStep(page, "majordomo").getByRole("button", { name: "Enable majordomo" }).click();
  const panel = page.getByRole("complementary", { name: "Majordomo panel" });
  await expect(panel.getByRole("button", { name: "Enable majordomo" })).toBeVisible();
  await panel.getByRole("button", { name: "Collapse majordomo panel" }).click();

  // Log in reaches the identity cell of the login the agent template uses; Use a token the connector page.
  const demo = startedStep(page, "login").getByRole("group", { name: "Demo" });
  await expect(demo.getByRole("link", { name: "Use a token for Demo" })).toHaveAttribute("href", "#/connectors/demo");
  await demo.getByRole("button", { name: "Log in: Demo identity default-agent-demo-state — default / default / default" }).click();
  await expect(page).toHaveURL(/#\/workspace\/default\/default\/default\/login-default-agent-demo-state$/);
  expect(JSON.parse(host.posts("/api/connectors/demo/login")[0].body)).toEqual({ name: "default-agent-demo-state", scope: { realm: "default", space: "default", stack: "default" }, method: "login" });
  // While the session runs the step offers to open it again; when it ends the step is done without a refresh.
  await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
  await expect(demo.getByRole("button", { name: /^Open login session/ })).toBeVisible();
  host.endLogin("default-agent-demo-state");
  await expect(startedStep(page, "login")).toHaveAccessibleName("Log in to Demo: Done", { timeout: 10000 });
  await expect(card.locator(".panel-heading .badge")).toHaveText("2 of 3 done");

  // The first agent completes the required steps. Get started is one switch (#1745): the card stays until
  // it is dismissed, and nothing is stored for it.
  await startedStep(page, "agent").getByRole("link", { name: "New agent" }).click();
  await page.getByRole("region", { name: "New agent" }).getByRole("button", { name: "Create" }).click();
  await expect(page).toHaveURL(/#\/workspace\/default\/default\/default\/default-agent-demo-a1b2c3-d4e5f6$/);
  await page.locator(".view-switch").getByRole("link", { name: "Workbench" }).click();
  await expect(content(page).getByRole("region", { name: "Cell terminals" })).toBeVisible();
  await expect(card).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("kukeon-get-started"))).toBeNull();
  expect(await pageMarked(page)).toBe(true);

  // Required steps done, the others still recommended until set up.
  await expect.poll(() => stepNames(page)).toEqual(["Initialize: Done", "Log in to Demo: Done", "Set up Repo and Forge: Recommended", "Enable majordomo: Optional", "Launch your first agent: Done"]);
  await expect(card.locator(".panel-heading .badge")).toHaveText("3 of 3 done");
  await expect(card.getByRole("status")).toContainText("You are set up.");
  // A stored git identity sets the recommended step up.
  host.secrets.push({ metadata: { name: "repo-key", realm: "default" }, spec: { connector: "repo", type: "ssh-key" } });
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(startedStep(page, "git-identity")).toHaveAccessibleName("Set up Repo and Forge: Done");
});

test("Get started reads a card mode stored before it was one switch, and the Profile switch shows and hides it", async ({ page }) => {
  const host = onboardingHost();
  await host.install(page);
  await seed(page, { "kukeon-get-started": "hidden" });
  await page.goto(server.url);
  await expect(content(page).getByRole("heading", { name: "No active cells" })).toBeVisible();
  await expect(getStarted(page)).toHaveCount(0);
  const settings = await openSettings(page);
  const show = settings.getByRole("switch", { name: "Show Get started" });
  await expect(show).toHaveAttribute("aria-checked", "false");
  await show.click();
  await expect(show).toHaveAttribute("aria-checked", "true");
  expect(await page.evaluate(() => localStorage.getItem("kukeon-get-started"))).toBe("true");
  await page.goto(server.url);
  await expect(getStarted(page)).toBeVisible();
});

test("Get started can be dismissed, stays dismissed across a reload and reopens from the user menu", async ({ page }) => {
  const host = onboardingHost();
  await host.install(page);
  await page.goto(server.url);
  await expect(getStarted(page)).toBeVisible();
  // The card sits above the grid's own empty state.
  await expect(content(page).getByRole("heading", { name: "No active cells" })).toBeVisible();
  await getStarted(page).getByRole("button", { name: "Dismiss" }).click();
  await expect(getStarted(page)).toHaveCount(0);
  // Dismiss turns the Profile's Show Get started off (#1745).
  expect(await page.evaluate(() => localStorage.getItem("kukeon-get-started"))).toBe("false");
  await page.reload();
  await expect(content(page).getByRole("heading", { name: "No active cells" })).toBeVisible();
  await expect(getStarted(page)).toHaveCount(0);
  // A dismissed card reads nothing.
  const teamReads = host.requests.filter(request => request.path === "/api/teams").length;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("button", { name: "Refresh" })).not.toHaveAttribute("aria-busy", "true");
  expect(host.requests.filter(request => request.path === "/api/teams").length).toBe(teamReads);
  // From another page the menu item goes to the Workbench and shows the card.
  await page.goto(`${server.url}#/teams`);
  await userButton(page).click();
  await page.getByRole("menuitem", { name: "Get started" }).click();
  await expect(page).toHaveURL(/#\/$/);
  await expect(steps(page)).toHaveCount(5);
  // A token stored for the harness completes Log in too.
  host.initialize();
  host.secrets.push({ metadata: { name: "demo-key", realm: "default" }, spec: { connector: "demo", type: "opaque" } });
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(startedStep(page, "login")).toHaveAccessibleName("Log in to Demo: Done");
  await expect(startedStep(page, "initialize")).toHaveAccessibleName("Initialize: Done");
});

test("a connector without identities says Not set up, recommended for what its description says", async ({ page }) => {
  const host = onboardingHost({ initialized: true });
  host.secrets.push({ metadata: { name: "repo-key", realm: "default" }, spec: { connector: "repo", type: "ssh-key", identity: { hosts: "example.com" } } });
  await host.install(page);
  await page.goto(`${server.url}#/connectors`);
  const row = title => content(page).getByRole("region", { name: "Connectors list" }).getByRole("row").filter({ has: page.getByRole("link", { name: title, exact: true }) });
  // Forge has no identity; Demo only a state volume nobody logged in to; Repo has a key.
  await expect(row("Forge").getByRole("cell").nth(2)).toHaveText("0 Not set up — recommended");
  await expect(row("Demo").getByRole("cell").nth(2)).toHaveText("2 Not set up — recommended");
  await expect(row("Repo").getByRole("cell").nth(2)).toHaveText("1");
  await row("Forge").getByRole("link", { name: "Forge", exact: true }).click();
  await expect(content(page).getByRole("region", { name: "Forge connector" }).getByRole("note")).toHaveText("Not set up — recommended for a test-only API connector whose token can also serve git.");
  await page.goto(`${server.url}#/connectors/demo`);
  const demo = content(page).getByRole("region", { name: "Demo connector" });
  await expect(demo.getByRole("note")).toHaveText("Not set up — recommended for how a demo harness authenticates.");
  // Logging in sets it up.
  host.endLogin("default-agent-demo-state");
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(demo.getByRole("note")).toHaveCount(0);
  await page.goto(`${server.url}#/connectors/repo`);
  await expect(content(page).getByRole("region", { name: "Repo connector" })).toBeVisible();
  await expect(content(page).getByRole("region", { name: "Repo connector" }).getByRole("note")).toHaveCount(0);
});

test("New agent offers Log in first while the template's login is empty, and Create still works", async ({ page }) => {
  const host = onboardingHost({ initialized: true });
  await host.install(page);
  await page.goto(`${server.url}#/agents/new`);
  const form = page.getByRole("region", { name: "New agent" });
  await expect(form.getByRole("radio", { name: /demo/ })).toBeChecked();
  const logIn = form.getByRole("button", { name: "Log in first: Demo identity default-agent-demo-state — default / default / default" });
  await expect(logIn).toBeVisible();
  await expect(form.getByRole("note")).toContainText("Demo is not logged in for agents yet.");
  await expect(form.getByRole("button", { name: "Create" })).toBeEnabled();
  // Log in first opens the identity cell of the template's state volume.
  await logIn.click();
  await expect(page).toHaveURL(/#\/workspace\/default\/default\/default\/login-default-agent-demo-state$/);
  // Back on the page while the session runs, the button reopens it; once logged in it is gone.
  await page.goto(`${server.url}#/agents/new`);
  await expect(form.getByRole("button", { name: /^Open login session: Demo identity/ })).toBeVisible();
  host.endLogin("default-agent-demo-state");
  await expect(form.getByRole("button", { name: /Demo identity/ })).toHaveCount(0, { timeout: 10000 });
  await expect(form.getByRole("note")).toHaveCount(0);

  // With an empty login Create is not held back.
  const empty = onboardingHost({ initialized: true });
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await signIn(page);
  await empty.install(page);
  await page.reload();
  await expect(form.getByRole("button", { name: /^Log in first/ })).toBeVisible();
  await form.getByRole("button", { name: "Create" }).click();
  await expect(page).toHaveURL(/#\/workspace\/default\/default\/default\/default-agent-demo-a1b2c3-d4e5f6$/);
  expect(empty.posts("/api/agents")).toHaveLength(1);
  expect(empty.posts("/api/connectors/demo/login")).toHaveLength(0);
});

// Unread terminal output (#1696). The mocked GET /api/terminal-activity answers each cell parameter
// from counters, keyed realm/space/stack/cell; a cell missing there is not_found. hold makes the next
// answers wait until the promise it is given settles.
const runOne = "2026-11-26T10:00:00Z";
const runTwo = "2026-11-26T11:00:00Z";
const counters = (bytesOut, idleMs = 60_000, startedAt = runOne) => ({ container: "shell", startedAt, bytesOut, bytesIn: 0, idleMs });
const seenKey = "kukeon-terminal-seen";
const storedMarkers = page => page.evaluate(key => JSON.parse(localStorage.getItem(key) ?? "{}"), seenKey);
const cellKey = cell => JSON.stringify(cell.split("/"));
// The cells' one attachable container, so opening a cell attaches its terminal.
const shellContainers = path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined;
async function mockActivity(page, counts) {
  const requests = [];
  let answered = 0;
  let held = null;
  await page.route("**/api/terminal-activity**", async route => {
    const asked = new URL(route.request().url()).searchParams.getAll("cell");
    requests.push(asked);
    if (held) await held;
    await route.fulfill({ json: { cells: asked.map(cell => ({ scope: cell.split("/"), ...(counts[cell] ?? { unavailable: "not_found" }) })) } });
    answered++;
  });
  return { requests, answered: () => answered, hold: promise => { held = promise; } };
}
// mockAttach answers terminal attaches; the function it returns waits for the next attach to open and
// tells it the terminal is ready, so the session becomes attached.
async function mockAttach(page) {
  const sockets = [];
  await page.routeWebSocket("**/attach", socket => { sockets.push(socket); });
  let used = 0;
  return async () => {
    await expect.poll(() => sockets.length).toBeGreaterThan(used);
    used = sockets.length;
    sockets.at(-1).send(JSON.stringify({ type: "ready" }));
  };
}
// tick advances the page clock by one poll interval and waits for the poll it starts to be answered.
async function tick(page, activity) {
  const count = activity.answered();
  await page.clock.fastForward(5000);
  await expect.poll(() => activity.answered()).toBeGreaterThan(count);
  await page.clock.fastForward(100);
}

test("unread output marks a cell in the sidebar, rail and grid once quiet, and reading it clears the mark", async ({ page }) => {
  await page.clock.install();
  await mock(page, shellContainers);
  // The quiet wait this test exercises is a Profile choice; Off is the default (#1770).
  await defaultStyle(page, { "kukeon-unread-quiet-seconds": "3" });
  const dev = "dev/shared/services/api";
  const prod = "prod/shared/services/api";
  const counts = { [dev]: counters(100), [prod]: counters(200) };
  const activity = await mockActivity(page, counts);
  const sendReady = await mockAttach(page);
  await page.goto(server.url);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  const devRow = cells.getByRole("link", { name: /^Open dev \/ shared \/ services \/ api/ });
  const prodRow = cells.getByRole("link", { name: /^Open prod \/ shared \/ services \/ api/ });
  const devTile = grid(page).locator(`.cell-tile[data-scope='${cellKey(dev)}']`);
  // The polled cells are the ones the sidebar lists, each a realm/space/stack/cell parameter.
  await expect.poll(() => activity.requests[0]?.slice().sort()).toEqual([dev, prod]);
  // A browser with no stored markers marks nothing on first load, and keeps what it saw.
  await expect.poll(() => storedMarkers(page)).toEqual({ [cellKey(dev)]: { startedAt: runOne, bytes: 100 }, [cellKey(prod)]: { startedAt: runOne, bytes: 200 } });
  await expect(page.locator(".has-unread")).toHaveCount(0);
  await expect(page.locator(".cell-tile-unread")).toHaveCount(0);

  // Output still arriving does not mark the cell; once quiet for the chosen three seconds it does.
  counts[dev] = counters(1329, 1200);
  await tick(page, activity);
  await expect(devRow).not.toHaveClass(/has-unread/);
  counts[dev] = counters(1329, 4000);
  await tick(page, activity);
  await expect(devRow).toHaveClass(/has-unread/);
  await expect(devRow).toHaveAccessibleName("Open dev / shared / services / api, unread output");
  await expect(devRow.locator(".unread-dot")).toBeVisible();
  await expect(devTile.locator(".cell-tile-unread")).toHaveText("Unread");
  await expect(devTile).toHaveAccessibleName("Open dev / shared / services / api, unread output");
  await expect(prodRow).not.toHaveClass(/has-unread/);
  await expect(page.locator(".cell-unread-count")).toHaveCount(0);
  // Not colour alone: the dot is a shape and the name is bolder, in both themes.
  const weight = locator => locator.locator(".cell-link-name").evaluate(element => Number(getComputedStyle(element).fontWeight));
  expect(await weight(devRow)).toBeGreaterThan(await weight(prodRow));
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme });
    const [dot, accent, ground] = await devRow.locator(".unread-dot").evaluate(element => [getComputedStyle(element).backgroundColor, getComputedStyle(document.documentElement).getPropertyValue("--accent").trim(), getComputedStyle(document.body).backgroundColor]);
    expect(dot, colorScheme).not.toBe(ground);
    expect(accent, colorScheme).not.toBe("");
    await expect(devTile.locator(".cell-tile-unread")).toBeVisible();
  }
  await page.emulateMedia({ colorScheme: "light" });

  // The collapsed rail rings the cell's dot.
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  const rail = page.getByRole("navigation", { name: "Cell shortcuts" });
  const devDot = rail.getByRole("link", { name: "Open dev / shared / services / api (Ready), unread output" });
  await expect(devDot).toHaveClass(/has-unread/);
  await expect(devDot).toHaveAttribute("title", "Open dev / shared / services / api (Ready), unread output");
  expect(await devDot.locator(".state-dot").evaluate(element => getComputedStyle(element).boxShadow)).not.toBe("none");
  await expect(rail.getByRole("link", { name: "Open prod / shared / services / api (Running)" })).not.toHaveClass(/has-unread/);
  await page.getByRole("button", { name: "Expand sidebar" }).click();

  // Opening the cell clears the mark once its terminal is attached.
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await sendReady();
  await expect(terminalTools(page).locator(".terminal-connection-state")).not.toHaveText("Connecting…");
  await expect(devRow).not.toHaveClass(/has-unread/);
  // Output read on screen moves the marker, including the last seconds before navigating away.
  counts[dev] = counters(2000, 4000);
  await tick(page, activity);
  await expect.poll(async () => (await storedMarkers(page))[cellKey(dev)]).toEqual({ startedAt: runOne, bytes: 2000 });
  counts[dev] = counters(2100, 3500);
  const beforeLeaving = activity.requests.length;
  await page.goto(`${server.url}#/`);
  await expect.poll(() => activity.requests.length).toBeGreaterThan(beforeLeaving);
  expect(activity.requests.at(-1)).toEqual([dev]);
  await expect.poll(async () => (await storedMarkers(page))[cellKey(dev)]).toEqual({ startedAt: runOne, bytes: 2100 });
  await tick(page, activity);
  await expect(devRow).not.toHaveClass(/has-unread/);

  // The terminal stays attached in a hidden session; new output marks the cell like an unattached one.
  counts[dev] = counters(2300, 4000);
  await tick(page, activity);
  await expect(devRow).toHaveClass(/has-unread/);
  // A restart (new startedAt): all output of the new run is unread.
  counts[prod] = counters(10, 4000, runTwo);
  await tick(page, activity);
  await expect(prodRow).toHaveClass(/has-unread/);
  // An unavailable cell is not marked, and its marker is kept.
  counts[dev] = { unavailable: "not_live" };
  await tick(page, activity);
  await expect(devRow).not.toHaveClass(/has-unread/);
  expect((await storedMarkers(page))[cellKey(dev)]).toEqual({ startedAt: runOne, bytes: 2100 });
  // Markers survive a reload: the output past the stored marker is still unread.
  counts[dev] = counters(2300, 4000);
  await page.reload();
  await expect(devRow).toHaveClass(/has-unread/);
  await expect(prodRow).toHaveClass(/has-unread/);
});

test("with nothing stored the first new byte marks a cell, and the mark pulses unless motion is reduced", async ({ page }) => {
  await page.clock.install();
  await mock(page, shellContainers);
  await defaultStyle(page);
  const dev = "dev/shared/services/api";
  const counts = { [dev]: counters(100), "prod/shared/services/api": counters(100) };
  const activity = await mockActivity(page, counts);
  await page.goto(server.url);
  await expect.poll(async () => Object.keys(await storedMarkers(page)).length).toBe(2);
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  const devRow = cells.getByRole("link", { name: /^Open dev \/ shared \/ services \/ api/ });
  const prodRow = cells.getByRole("link", { name: /^Open prod \/ shared \/ services \/ api/ });
  // Mark unread after quiet for is Off: output that is still arriving marks the cell.
  counts[dev] = counters(101, 0);
  await tick(page, activity);
  await expect(devRow).toHaveClass(/has-unread/);
  // Every unread mark pulses; only opacity and box-shadow move, and nothing else animates.
  const animation = locator => locator.evaluate(element => getComputedStyle(element).animationName);
  const devTile = grid(page).locator(`.cell-tile[data-scope='${cellKey(dev)}']`);
  expect(await animation(devRow.locator(".unread-dot"))).toBe("unread-pulse");
  expect(await animation(devTile.locator(".unread-dot"))).toBe("unread-pulse");
  expect(await animation(prodRow.locator(".state-dot"))).toBe("none");
  const keyframes = await page.evaluate(() => Object.fromEntries([...document.styleSheets].flatMap(sheet => [...sheet.cssRules]).filter(rule => rule instanceof CSSKeyframesRule && rule.name.startsWith("unread-")).map(rule => [rule.name, [...rule.cssRules].flatMap(frame => [...frame.style])])));
  expect(Object.keys(keyframes).sort()).toEqual(["unread-glyph-pulse", "unread-pulse", "unread-ring-pulse"]);
  for (const properties of Object.values(keyframes)) for (const property of properties) expect(property).toMatch(/^(opacity|box-shadow)$/);
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  const rail = page.getByRole("navigation", { name: "Cell shortcuts" });
  const devDot = rail.getByRole("link", { name: /^Open dev \/ shared \/ services \/ api .*unread output$/ });
  expect(await animation(devDot.locator(".state-dot"))).toBe("unread-ring-pulse");
  expect(await animation(rail.getByRole("link", { name: /^Open prod / }).locator(".state-dot"))).toBe("none");
  // Reduced motion leaves the static mark.
  await page.emulateMedia({ reducedMotion: "reduce" });
  expect(await animation(devDot.locator(".state-dot"))).toBe("none");
  await page.getByRole("button", { name: "Expand sidebar" }).click();
  expect(await animation(devRow.locator(".unread-dot"))).toBe("none");
  await expect(devRow.locator(".unread-dot")).toBeVisible();
});

test("activity is polled only on Workbench routes while the tab is visible, one request at a time, and markers of gone cells are pruned", async ({ page }) => {
  await page.clock.install();
  await seed(page, { [seenKey]: JSON.stringify({ [cellKey("dev/shared/services/gone")]: { startedAt: runOne, bytes: 5 }, [cellKey("dev/shared/services/api")]: { startedAt: runOne, bytes: 1 } }) });
  await mock(page);
  const activity = await mockActivity(page, { "dev/shared/services/api": counters(1), "prod/shared/services/api": counters(1) });
  for (const route of ["#/realms", "#/settings", "#/settings/profile"]) {
    await page.goto(`${server.url}${route}`);
    await expect(page.locator(".page-heading h1")).toBeVisible();
    await page.clock.fastForward(12_000);
  }
  expect(activity.requests).toEqual([]);
  // The inventory loaded on Manage already pruned the marker of the cell that left it.
  await expect.poll(() => storedMarkers(page)).toEqual({ [cellKey("dev/shared/services/api")]: { startedAt: runOne, bytes: 1 } });

  let release = () => {};
  activity.hold(new Promise(resolve => { release = resolve; }));
  await page.goto(`${server.url}#/`);
  await expect.poll(() => activity.requests.length).toBe(1);
  // A tick is skipped while a request is in flight.
  await page.clock.fastForward(15_000);
  expect(activity.requests.length).toBe(1);
  activity.hold(null);
  release();
  await tick(page, activity);
  expect(activity.requests.length).toBe(2);

  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.clock.fastForward(15_000);
  expect(activity.requests.length).toBe(2);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => activity.requests.length).toBe(3);
  // An unreadable stored value reads as empty: nothing is marked and the next reading is stored.
  await page.evaluate(key => localStorage.setItem(key, "{not json"), seenKey);
  await page.reload();
  await expect.poll(() => storedMarkers(page).catch(() => null)).toEqual({ [cellKey("dev/shared/services/api")]: { startedAt: runOne, bytes: 1 }, [cellKey("prod/shared/services/api")]: { startedAt: runOne, bytes: 1 } });
  await expect(page.locator(".has-unread")).toHaveCount(0);
});

test("agent style rolls unread output up to agents, and Show unread bytes adds the amount", async ({ page }) => {
  await page.clock.install();
  // scratch is still busy: with a 3 s quiet wait it stays unmarked.
  await seed(page, { "kukeon-unread-quiet-seconds": "3" });
  await mock(page, path => shellContainers(path) ?? agentInventory(path));
  const at = cell => `dev/shared/services/${cell}`;
  const counts = { [at("dev-0")]: counters(100), [at("dev-1")]: counters(100), [at("pm-0")]: counters(100), [at("scratch")]: counters(100), "prod/shared/services/dev-9": counters(100) };
  const activity = await mockActivity(page, counts);
  const sendReady = await mockAttach(page);
  await page.goto(server.url);
  await expect.poll(async () => Object.keys(await storedMarkers(page)).length).toBe(5);
  counts[at("dev-0")] = counters(1329, 4000);
  counts[at("dev-1")] = counters(2148, 4000);
  counts[at("pm-0")] = counters(612, 4000);
  counts[at("scratch")] = counters(5000, 200);
  await tick(page, activity);

  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  const claude = cells.getByRole("region", { name: /agent$/ }).nth(0);
  const toggle = claude.getByRole("button", { name: /^claude/ });
  const devRow = claude.getByRole("link", { name: /^Open dev \/ shared \/ services \/ dev-0/ });
  const pm = cells.getByRole("link", { name: /open dev \/ shared \/ services \/ pm-0/ });
  const scratch = cells.getByRole("link", { name: /^Open dev \/ shared \/ services \/ scratch/ });
  await expect(toggle).toHaveClass(/has-unread/);
  await expect(toggle).toHaveAccessibleName(/, unread output$/);
  await expect(devRow).toHaveAccessibleName("Open dev / shared / services / dev-0, unread output");
  await expect(pm).toHaveClass(/has-unread/);
  await expect(pm).toHaveAccessibleName(/\(Ready\), unread output$/);
  await expect(scratch).not.toHaveClass(/has-unread/);
  await expect(page.locator(".cell-unread-count")).toHaveCount(0);
  // A collapsed agent still shows the mark.
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(toggle).toHaveClass(/has-unread/);
  await toggle.click();
  // The rail rings the agent glyph and the instance's dot.
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  const rail = page.getByRole("navigation", { name: "Cell shortcuts" });
  const glyph = rail.getByRole("img", { name: /^Agent claude.*, 2 instances, unread output$/ });
  await expect(glyph).toHaveClass(/has-unread/);
  await expect(rail.getByRole("link", { name: /dev-0 \(Ready\), unread output$/ })).toHaveClass(/has-unread/);
  await page.getByRole("button", { name: "Expand sidebar" }).click();

  // Show unread bytes: the amount on every marked surface, its sum on the agent row.
  await page.goto(`${server.url}#/settings/profile`);
  const control = settingsPage(page).getByRole("switch", { name: "Show unread bytes" });
  await expect(control).toHaveAttribute("aria-checked", "false");
  await control.click();
  await expect(control).toHaveAttribute("aria-checked", "true");
  await page.goto(`${server.url}#/`);
  await tick(page, activity);
  await expect(devRow.locator(".cell-unread-count")).toHaveText("1.2 KiB");
  await expect(devRow).toHaveAccessibleName("Open dev / shared / services / dev-0, 1.2 KiB unread output");
  await expect(toggle.locator(".cell-unread-count")).toHaveText("3.2 KiB");
  await expect(toggle).toHaveAccessibleName(/, 3\.2 KiB unread output$/);
  await expect(pm.locator(".cell-unread-count")).toHaveText("512 B");
  await expect(scratch.locator(".cell-unread-count")).toHaveCount(0);
  const devTile = grid(page).locator(`.cell-tile[data-scope='${cellKey(at("dev-0"))}']`);
  await expect(devTile.locator(".cell-tile-unread")).toHaveText("Unread · 1.2 KiB");
  await expect(grid(page).locator(`.cell-tile[data-scope='${cellKey(at("scratch"))}'] .cell-tile-unread`)).toHaveCount(0);
  // The rail shows no number; the tooltip and accessible name carry it.
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(rail.locator(".cell-unread-count")).toHaveCount(0);
  const railDot = rail.getByRole("link", { name: /dev-0 \(Ready\), 1\.2 KiB unread output$/ });
  await expect(railDot).toHaveAttribute("title", /dev-0 \(Ready\), 1\.2 KiB unread output$/);
  await expect(rail.getByRole("img", { name: /, 3\.2 KiB unread output$/ })).toBeVisible();
  await page.getByRole("button", { name: "Expand sidebar" }).click();
  // The count follows each poll, and the choice survives a reload.
  counts[at("dev-0")] = counters(2353, 4000);
  await tick(page, activity);
  await expect(devRow.locator(".cell-unread-count")).toHaveText("2.2 KiB");
  await page.reload();
  await expect(devRow.locator(".cell-unread-count")).toHaveText("2.2 KiB");
  // Reading the cell removes its count.
  await page.goto(`${server.url}#/workspace/dev/shared/services/dev-0`);
  await sendReady();
  await expect(devRow.locator(".cell-unread-count")).toHaveCount(0);
  await expect(toggle.locator(".cell-unread-count")).toHaveText("2 KiB");
});

test("an unread mark reaches every tab of the browser, and reading the cell in one tab clears it in the other", async ({ page }) => {
  test.setTimeout(60_000);
  await defaultStyle(page);
  const dev = "dev/shared/services/api";
  const counts = { [dev]: counters(100), "prod/shared/services/api": counters(100) };
  await mock(page, shellContainers);
  await mockActivity(page, counts);
  const sendReady = await mockAttach(page);
  await page.goto(server.url);
  await expect.poll(async () => Object.keys(await storedMarkers(page)).length).toBe(2);
  const other = await page.context().newPage();
  await signIn(other);
  await mock(other, shellContainers);
  await mockActivity(other, counts);
  await other.goto(server.url);
  counts[dev] = counters(900, 4000);
  const row = tab => tab.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: /^Open dev \/ shared \/ services \/ api/ });
  await expect(row(page)).toHaveClass(/has-unread/, { timeout: 12_000 });
  await expect(row(other)).toHaveClass(/has-unread/, { timeout: 12_000 });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await sendReady();
  await expect(row(page)).not.toHaveClass(/has-unread/);
  // The reading tab moves the marker on its next poll; the other tab follows through the storage event.
  await expect(row(other)).not.toHaveClass(/has-unread/, { timeout: 12_000 });
  await other.close();
});
