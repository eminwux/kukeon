import { test, expect } from "@playwright/test";
import { startServer, connectorWorld, mockConnectorAPI } from "./server.mjs";

let server;
test.beforeEach(async () => { server = await startServer(); });
test.afterEach(async () => { await server?.stop(); });
const kinds = ["Realm", "Space", "Stack", "Cell", "Container"];
function doc(scope) {
  return { apiVersion: "kukeon.io/v1beta1", kind: kinds[scope.length - 1], metadata: { name: scope.at(-1), labels: { team: "platform" } }, spec: { scope, futureField: { nested: true } }, status: { state: scope[0] === "prod" ? "Running" : "Ready" } };
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
async function mock(page, handler = () => undefined) {
  await page.route("**/api/realms**", async route => {
    const path = new URL(route.request().url()).pathname;
    const result = await handler(path, route.request());
    await route.fulfill(result ?? { json: fixture(path) });
  });
}
const failure = { status: 503, json: { error: { code: "unavailable", message: "Scoped read failed" } } };
const nav = page => page.getByRole("navigation", { name: "Resources navigation" });
const listSearch = page => page.locator(".list-controls").getByRole("searchbox");
const terminalTools = page => page.locator(".cell-terminal-tools");
const toasts = page => page.getByRole("region", { name: "Notifications" }).locator(".toast");
const content = page => page.locator(".workspace-content");
const inventoryIndicator = (page, state) => page.locator(".toolbar").getByRole("button", { name: `Inventory ${state}: show details` });

test("terminal selects eligible containers, forwards input and resize, and detaches", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [
    { id: "worker", attachable: true }, { id: "shell", attachable: true },
    { id: "pause", root: true, attachable: true }, { id: "no-tty" },
  ] } : undefined);
  const messages = [];
  let selected = "";
  let closed = false;
  let sendReady = () => {};
  let sendOutput = () => {};
  await page.routeWebSocket("**/attach", socket => {
    selected = new URL(socket.url()).pathname;
    socket.onMessage(message => { messages.push(message); });
    socket.onClose(() => { closed = true; });
    sendReady = () => socket.send(JSON.stringify({ type: "ready" }));
    sendOutput = () => socket.send(Buffer.from("\x1b[95;49mYOLOmo\x1b[0m $ "));
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(terminalTools(page).locator("option")).toHaveCount(2);
  await terminalTools(page).getByLabel("Container").selectOption("shell");
  await expect.poll(() => selected).toContain("/containers/shell/attach");
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
  const appearance = page.getByRole("button", { name: /Appearance:/ });
  await appearance.click();
  await appearance.click();
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
  await page.getByRole("link", { name: "Resources", exact: true }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Resources");
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
  await expect(panel.getByText("Terminal unavailable: cell is Stopped.")).toBeVisible();
  await expect(terminalTools(page).getByText("Terminal unavailable", { exact: false })).toHaveCount(0);
  await expect(terminalTools(page).getByRole("button", { name: "Reconnect" })).toHaveCount(0);
  await expect.poll(() => closed).toBeGreaterThan(closedBefore);
});

test("Resources is home and retains resource summaries and navigation", async ({ page }) => {
  await mock(page);
  await page.goto(server.url);
  await expect(page.locator(".page-heading h1")).toHaveText("Resources");
  // The document title names the page (#1567).
  await expect(page).toHaveTitle("Resources · kukeon");
  await expect(page.locator('link[rel="icon"][type="image/svg+xml"]')).toHaveAttribute("href", /^\/assets\/.+\.svg$/);
  const brand = page.getByRole("complementary", { name: "Cell sidebar" }).getByRole("link", { name: "kukeon", exact: true });
  await expect(brand).toHaveAttribute("href", "#/resources");
  await expect(brand.locator(".mark svg")).toHaveCount(1);
  await expect(page.getByText("LOCAL WORKSPACE")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Workspace", exact: true })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Workspace welcome" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "+ New Cell" })).toHaveCount(0);
  expect(await page.evaluate(() => document.body.innerText)).not.toMatch(/Kukeon|Workspace/);
  for (const hash of ["#/", "#/workspace", "#/workspace/dev", "#/%E0%A4%A"]) {
    await page.goto(`${server.url}${hash}`);
    await expect(page.locator(".page-heading h1"), hash).toHaveText("Resources");
    await expect(page.locator(".summary-card").filter({ hasText: "Cells" }).locator("strong")).toHaveText("2");
  }
  await page.goto(`${server.url}#/cells`);
  await brand.click();
  await expect(page).toHaveURL(/#\/resources$/);
  await expect(page.locator(".page-heading h1")).toHaveText("Resources");
  for (const type of ["Realms", "Spaces", "Stacks", "Cells"]) {
    await expect(page.locator(".summary-card").filter({ hasText: type }).locator("strong")).toHaveText("2");
    await expect(nav(page).getByRole("link", { name: type, exact: true })).toBeVisible();
  }
  await expect(page.locator(".summary-card").filter({ hasText: "Cells" }).getByRole("list", { name: "Cell states" })).toContainText("Running");
  const attention = page.getByRole("region", { name: "Cells needing attention" });
  await expect(attention.getByRole("link", { name: "api", exact: true })).toHaveAttribute("href", "#/resource/prod/shared/services/api");
  await expect(attention.getByRole("listitem")).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "Resource overview" })).toHaveCount(0);
  await expect(page.locator("pre")).toHaveCount(0);
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
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  let opened = 0;
  await page.routeWebSocket("**/attach", socket => { opened++; socket.send(JSON.stringify({ type: "ready" })); });
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  await expect(page.getByRole("heading", { name: "Runtime status" })).toBeVisible();
  await expect(page.locator(".container-panel li").filter({ hasText: "shell" })).toBeVisible();
  // Give a would-be auto-attach every chance to fire: the inventory and container list have loaded.
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
  await page.waitForTimeout(1000);
  expect(opened).toBe(0);
  await expect(page.locator(".cell-terminal-state")).toHaveCount(0);
  await page.getByRole("link", { name: "Open terminal for dev / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/api$/);
  await expect.poll(() => opened).toBe(1);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
});

for (const [width, height] of [[1440, 900], [390, 844]]) {
  for (const theme of ["light", "dark"]) {
    test(`cellRead panels render at full content height and the page scrolls at ${width}x${height} (${theme})`, async ({ page }) => {
      await page.addInitScript(value => localStorage.setItem("kukeon-theme", value), theme);
      await page.setViewportSize({ width, height });
      await mock(page, path => path.endsWith("/cells/api/cgroup") ? { json: { cgroupPath: "/kukeon/dev/shared/services/api", absolutePath: "/sys/fs/cgroup/kukeon/dev/shared/services/api", exists: true, ready: true, nestedCgroupRuntime: true, subtreeControllers: ["cpu", "io", "memory", "pids"], live: { memory: { max: { state: "set", raw: "1073741824", value: 1073741824 } }, io: { state: "set", entries: [] } } } }
        : path.endsWith("/containers") ? { json: [{ id: "root", root: true }, { id: "web" }, { id: "shell", attachable: true }] } : undefined);
      await page.goto(`${server.url}#/resource/dev/shared/services/api`);
      const panels = page.locator(".cell-overview-content > .panel");
      await expect(page.getByRole("region", { name: "cell cgroup" }).getByText("cpu io memory pids", { exact: true })).toBeVisible();
      await expect(page.getByRole("region", { name: "cell network" })).toBeVisible();
      await expect(panels).toHaveCount(5);
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
  await expect(page.getByText("No attachable containers are available.")).toBeVisible();
  await expect(page.locator("#cell-terminal .terminal-empty")).toHaveText("No attachable containers are available.");
  await expect(terminalTools(page).getByRole("button", { name: "Expand terminal" })).toHaveCount(0);
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
    return { json: fixture(path).map(cell => ({ ...cell, metadata: { ...cell.metadata, labels: prod ? {} : { team: "platform" } }, status: { state: prod ? "Stopped" : "Failed" } })) };
  });
  await page.goto(server.url);
  const sidebar = page.getByRole("region", { name: "All cells" });
  const cells = page.getByRole("navigation", { name: "Cell navigation" });
  await expect(sidebar.getByLabel("Group by")).toHaveValue("");
  await expect(cells.getByRole("link")).toHaveCount(2);
  await expect(cells.getByRole("link", { name: "Open dev / shared / services / api" })).toContainText("Failed");
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toContainText("Stopped");
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
  await expect(page.locator(".page-heading h1")).toHaveText("Resources");
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
    await expect(first.getByRole("cell").nth(1)).toContainText("Ready");
    await first.getByRole("link", { name: /View document/ }).click();
    await expect(page.locator("pre")).toContainText('"futureField"');
    await page.getByRole("link", { name: "Show overview" }).click();
    await expect(page.locator("pre")).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Resource overview" })).toBeVisible();
  });
}

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

test("theme icon cycles through system, light, and dark preferences", async ({ page }) => {
  await mock(page);
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(server.url);
  const scheme = () => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme);
  await expect.poll(scheme).toBe("dark");
  const appearance = page.getByRole("button", { name: /Appearance:/ });
  await appearance.click();
  await page.reload();
  await expect(page.getByRole("button", { name: /Appearance: light/ })).toBeVisible();
  await expect.poll(scheme).toBe("light");
  await page.getByRole("button", { name: /Appearance: light/ }).click();
  await page.getByRole("button", { name: /Appearance: dark/ }).click();
  await expect.poll(scheme).toBe("dark");
  await page.emulateMedia({ colorScheme: "light" });
  await page.getByRole("button", { name: /Appearance: system/ }).click();
  await expect.poll(scheme).toBe("light");
});

test("mobile cell navigation closes after selection and Resources fits the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  // A collapsed desktop preference must not leak into the mobile drawer.
  await page.addInitScript(() => localStorage.setItem("kukeon-sidebar-collapsed", "true"));
  await mock(page);
  await page.goto(server.url);
  await expect(page.locator(".page-heading h1")).toHaveText("Resources");
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
  await page.getByRole("link", { name: "Resources", exact: true }).click();
  await expect(nav(page).getByRole("link", { name: "Secrets" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
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
  return { apiVersion: "kukeon.io/v1beta1", kind: { configs: "CellConfig", blueprints: "CellBlueprint", volumes: "Volume", secrets: "Secret" }[kind], metadata, spec };
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

test("create from config previews, prevents duplicate submits and opens the scoped cell", async ({ page }) => {
  await mockCatalog(page);
  let creates = 0;
  await page.route("**/api/realms/prod/spaces/shared/stacks/services/cells", route => route.fulfill({ json: [...fixture("/api/realms/prod/spaces/shared/stacks/services/cells"), ...(creates ? [doc(["prod", "shared", "services", "new-cell"])] : [])] }));
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
  await expect(page.getByRole("heading", { name: "Review Cell document" })).toBeVisible();
  expect(creates).toBe(0);
  await page.getByLabel("Environment overrides").fill("APP=production");
  await expect(page.getByRole("button", { name: "Create Cell", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await page.getByRole("button", { name: "Create Cell", exact: true }).click();
  await expect(page).toHaveURL(/#\/workspace\/prod\/shared\/services\/new-cell$/);
  expect(creates).toBe(1);
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open prod / shared / services / new-cell" })).toBeVisible();
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
  await page.getByLabel(/^github connector/).check();
  await expect(picker.getByRole("option")).toHaveText(["Choose from the realm's images…", "ghcr.io/example/claude-gh:v1", "docker.io/library/alpine:3 — capabilities unknown"]);
  await expect(page.getByText("This image does not provide: gh.")).toBeVisible();
  await picker.selectOption("ghcr.io/example/claude-gh:v1");
  await expect(page.getByLabel("IMAGE", { exact: true })).toHaveValue("ghcr.io/example/claude-gh:v1");
  await page.getByLabel("github-token secret").selectOption({ label: "gh-token — prod (opaque)" });

  // A mode's own parameters show only when previewing that mode.
  await expect(page.getByLabel("PROMPT")).toHaveCount(0);
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
  await expect(page.getByRole("table", { name: "Git identity for project" }).getByRole("row").nth(1)).toHaveText(/signing.*git-ssh \(ssh-key\).*space/);
  await expect(page.getByRole("table", { name: "Merged values and their origin" }).getByRole("row")).toHaveCount(2);
});

// kuke team writes realm-only blueprint refs from stack-scoped configs; the
// mock 404s any lookup whose scope slots do not match exactly, so a name that
// leaks into the space or stack slot fails the test.
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
  for (const { config, blueprint } of cases) {
    await page.goto(`${server.url}#/new-cell?config=${encodeURIComponent(JSON.stringify(config))}`);
    await expect(page.getByRole("link", { name: "claude", exact: true })).toHaveAttribute("href", `#/blueprints/${blueprint.join("/")}`);
    await expect(page.getByText("Blueprint reference is missing")).toHaveCount(0);
  }
  await expect(page.getByRole("button", { name: "Preview Cell", exact: true })).toBeEnabled();
  await page.getByRole("button", { name: "Preview Cell", exact: true }).click();
  await expect(page.getByLabel(/Start (and attach )?after creation/)).not.toBeChecked();
  await page.getByRole("button", { name: "Create Cell", exact: true }).click();
  await expect.poll(() => created?.config).toEqual({ name: "team", realm: "prod", space: "shared", stack: "services" });
  for (const lookup of lookups) expect(lookup.params).not.toContain(lookup.scope.at(-1));
});

test("kill and purge require explicit confirmation and pass only selected purge options", async ({ page }) => {
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
  const force = dialog.locator("label").filter({ hasText: "--force" }).locator("input");
  const cascade = dialog.locator("label").filter({ hasText: "--cascade" }).locator("input");
  await expect(force).not.toBeChecked();
  await expect(cascade).not.toBeChecked();
  await expect(dialog.getByRole("button", { name: "Confirm Purge" })).toBeDisabled();
  await dialog.getByLabel("Type the full target above to enable Purge").fill("dev / shared / services / api");
  await force.check();
  await cascade.check();
  await dialog.getByRole("button", { name: "Confirm Purge" }).click();
  await expect(toasts(page)).toHaveCount(1);
  await expect(toasts(page)).toContainText("Cell purged");
  await expect(toasts(page).locator(".toast-title")).toHaveText("dev / shared / services / api");
  expect(submitted).toEqual([{ force: true, cascade: true }]);
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
  await expect(page).toHaveURL(/#\/resources$/);
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
  const messages = [];
  await page.routeWebSocket("**/attach", socket => {
    socket.onMessage(message => { messages.push(message); });
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
  await expect(page.locator("#cell-terminal").getByText("Terminal unavailable: cell is Stopped.")).toBeVisible();
  expect(await terminal.boundingBox()).toEqual(before);
  await act("Start");
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
  await page.getByLabel(/Start (and attach )?after creation/).check();
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
  await expect(page.locator("#cell-terminal").getByText("Terminal unavailable: cell is Stopped.")).toBeVisible();
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
  await expect(panel.getByText("cpu io memory pids", { exact: true })).toBeVisible();
  await expect(panel.getByText("Enabled", { exact: true })).toBeVisible();
  await expect(panel.getByText("1 GiB", { exact: false })).toBeVisible();
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
    cell: { bridgeName: "k-0123abcd", hostNetwork: false, ipv4State: "not-running", rootContainer: "root", containers: [{ id: "root", root: true }, { id: "web", ports: ["8080:80"] }] },
  } } : undefined);
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "cell network" });
  await expect(panel.getByText("Bridged (space network)")).toBeVisible();
  await expect(panel.getByText("Not running", { exact: true })).toBeVisible();
  const containers = panel.locator("table.network-containers");
  await expect(containers.locator("tr").filter({ hasText: "web" })).toContainText("No (default)");
  await expect(containers.locator("tr").filter({ hasText: "web" })).toContainText("Declared — not applied at runtime yet");
  await expect(containers.locator("tr").filter({ hasText: "root" })).toContainText("Not set");
});

test("container network section labels defaults, inherited CNI path and unset declarations", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers/web/network") ? { json: {
    space: { networkName: "prod-shared", subnetCIDR: "10.88.1.0/24", subnetSource: "allocator", configPath: "/s.conflist", configPathSource: "default", cniNetworkExists: true, egress: { default: "allow", defaultSource: "default" } },
    cell: { hostNetwork: true, ipv4State: "host-network", rootContainer: "root" },
    container: { id: "web", hostNetwork: true, hostPID: false, cniConfigPath: "/s.conflist", cniConfigPathSource: "space", networks: ["backend"] },
  } } : undefined);
  await page.goto(`${server.url}#/resource/prod/shared/services/api/web`);
  const panel = page.getByRole("region", { name: "container network" });
  await expect(panel.locator("dt:text-is('Host network') + dd")).toHaveText("Yes");
  await expect(panel.locator("dt:text-is('Host PID') + dd")).toHaveText("No (default)");
  await expect(panel.getByText("Inherited from space (/s.conflist)")).toBeVisible();
  await expect(panel.getByText("Joins the network namespace of root container")).toBeVisible();
  await expect(panel.getByText("Host", { exact: true })).toBeVisible();
  await expect(panel.getByText("Host network (no CNI attachment)").first()).toBeVisible();
  await expect(panel.locator("dd").filter({ hasText: "backend" })).toContainText("Declared — not applied at runtime yet");
  await expect(panel.locator("dd").filter({ hasText: /^Not set$/ })).toHaveCount(2);
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

test("realm registry credentials list server and username, keep the token write-only and refresh the summary", async ({ page }) => {
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
  await page.goto(`${server.url}#/resource/dev`);
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

  await page.goto(`${server.url}#/resource/dev?document=1`);
  await expect(page.getByLabel("Complete realm JSON")).toContainText("ghcr.io");
  await expect(page.getByLabel("Complete realm JSON")).not.toContainText(token);
});

test("realm scope, breadcrumbs and narrow lists stay within the page", async ({ page }) => {
  await mock(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${server.url}#/realms`);
  await expect(page.getByRole("row")).toHaveCount(3);
  await expect(page.locator(".scope-cell").first()).toHaveText("—");
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
  await page.goto(`${server.url}#/daemon`);
  const panel = page.getByRole("region", { name: "Daemon connectivity" });
  await expect(panel.locator(".state-badge")).toHaveText("Unavailable");
  await expect(panel).toContainText("missing.sock");
  await expect(page.getByLabel("Daemon connection").getByRole("definition")).toHaveText([/./, "unknown"]);
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
  await expect(sidebar.getByRole("link", { name: "kukeon home" })).toHaveAttribute("href", "#/resources");

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

  // One row per connector of the catalog, whatever its delivery handler.
  const list = page.getByRole("region", { name: "Connectors list" });
  const listRow = title => list.getByRole("row").filter({ has: page.getByRole("link", { name: title, exact: true }) });
  await expect(list.getByRole("row")).toHaveCount(5);
  await expect(listRow("Example")).toContainText("example-token");
  await expect(listRow("Example")).toContainText("token — environment variable EXAMPLE_TOKEN");
  await expect(listRow("Demo")).toContainText("login — login kept in the harness state volume (no identity)");
  await expect(listRow("Repo")).toContainText("the cell's git identity");
  await expect(listRow("Pulls")).toContainText("image pulls of a Realm");
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

  // The create form is generated from the connector: a single secret type needs no type picker, and its
  // one readable field is required.
  const create = identities.getByRole("form", { name: "New Example identity" });
  await expect(identities.getByLabel("Identity realm")).toHaveValue("dev");
  await expect(create.getByLabel("Secret type")).toHaveCount(0);
  await expect(create.getByText("Account the token belongs to.")).toBeVisible();
  await expect(create.getByLabel("Value")).toHaveAttribute("type", "password");
  await create.getByLabel("Identity name").fill("example-new");
  await create.getByLabel("Value").fill(value);
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(create.getByRole("alert")).toContainText("account is required.");
  expect(identityPosts()).toHaveLength(0);
  await create.getByLabel("account").fill("gamma");
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(toasts(page).filter({ hasText: "Example identity example-new created in dev." })).toBeVisible();
  await expect(table.getByRole("row")).toHaveCount(5);
  await expect(table.getByRole("row", { name: /example-new/ })).toContainText("account: gamma");
  expect(identityPosts()).toHaveLength(1);
  expect(identityPosts()[0].body).toEqual({ scope: { realm: "dev" }, name: "example-new", type: "opaque", identity: { account: "gamma" }, value });
  // The value is write-only: gone from the form, absent from the page, the URL, storage and the console.
  await expect(create.getByLabel("Value")).toHaveValue("");
  await expect(create.getByLabel("Identity name")).toHaveValue("");
  expect(await page.content()).not.toContain(value);
  expect(await page.evaluate(() => document.body.innerText)).not.toContain(value);
  expect(page.url()).not.toContain(value);
  expect(world.requests.map(request => request.path).join(" ")).not.toContain(value);
  expect(await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))).not.toContain(value);

  // Rotate: the same form with the name fixed and the readable fields kept; it replaces the value.
  await table.getByRole("button", { name: "Rotate Example identity example-default — dev", exact: true }).click();
  const rotate = identities.getByRole("form", { name: "Rotate Example identity example-default" });
  await expect(rotate.getByLabel("Identity name")).toBeDisabled();
  await expect(rotate.getByLabel("Identity name")).toHaveValue("example-default");
  await expect(rotate.getByLabel("account")).toHaveValue("acme");
  await expect(rotate.getByLabel("Value")).toHaveValue("");
  await rotate.getByLabel("Value").fill(rotated);
  await rotate.getByRole("button", { name: "Rotate identity" }).click();
  await expect(toasts(page).filter({ hasText: "Example identity example-default rotated in dev." })).toBeVisible();
  await expect(rotate).toHaveCount(0);
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
  await page.goto(`${server.url}#/connectors/repo`);
  const create = page.getByRole("form", { name: "New Repo identity" });
  // Two accepted types: a type picker; ssh-key shows every field, including the ones limited to it.
  await expect(create.getByLabel("Secret type").getByRole("option")).toHaveText(["ssh-key", "basic-auth"]);
  await expect(create.getByLabel("author")).toHaveAttribute("placeholder", "Name <email>");
  await expect(create.getByRole("group", { name: "sign" }).getByRole("checkbox")).toHaveCount(2);
  await expect(create.getByLabel("allowedSignersRef")).toHaveAttribute("placeholder", "name@realm[/space[/stack]]");
  await expect(create.locator("textarea")).toHaveCount(2);
  await create.getByLabel("Secret type").selectOption("basic-auth");
  await expect(create.getByRole("group", { name: "sign" })).toHaveCount(0);
  await expect(create.getByLabel("allowedSignersRef")).toHaveCount(0);
  await expect(create.getByLabel("Password or token")).toHaveAttribute("type", "password");
  await create.getByLabel("Secret type").selectOption("ssh-key");

  await create.getByLabel("Identity name").fill("repo-new");
  await create.getByLabel("author").fill("not-a-person");
  await create.getByRole("textbox", { name: /^hosts/ }).fill("example.com, example.org/group");
  await create.getByRole("group", { name: "sign" }).getByLabel("commits").check();
  await create.getByLabel("Private key").fill(key);
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(create.getByRole("alert")).toContainText("author must be written Name <email>.");
  await create.getByLabel("author").fill("Dev <dev@example.com>");
  await create.getByRole("button", { name: "Create identity" }).click();
  await expect(page.getByRole("table", { name: "Repo identities" }).getByRole("row", { name: /repo-new/ })).toContainText("hosts: example.com,example.org/group");
  expect(world.posts("/api/connectors/repo/identities")[0].body).toEqual({
    scope: { realm: "dev" }, name: "repo-new", type: "ssh-key",
    identity: { author: "Dev <dev@example.com>", hosts: "example.com,example.org/group", sign: "commits" }, privateKey: key,
  });
  await expect(create.getByLabel("Private key")).toHaveValue("");
  expect(await page.content()).not.toContain("b3BlbnNzaC1rZXktdjEAAAAA-test-only");

  // A connector whose every method keeps a login in the state volume would have no identity; this one
  // has one login method and says so.
  await page.goto(`${server.url}#/connectors/demo`);
  await expect(page.getByRole("region", { name: "Demo connector" })).toContainText("The login method has no identity");
  await expect(page.getByRole("region", { name: "Demo defaults" }).getByRole("group")).toHaveCount(1);
  await expect(page.getByRole("region", { name: "Demo defaults" }).getByRole("group", { name: "Demo key default" })).toBeVisible();
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
  const defaults = page.getByRole("region", { name: "Example defaults" });
  await expect(defaults.getByLabel("Default realm")).toHaveValue("dev");
  await expect(defaults.getByLabel("Default space")).toHaveValue("shared");
  await expect(defaults.getByLabel("Default stack")).toHaveValue("services");
  const levelTable = defaults.getByRole("table", { name: "Example token defaults by level" });
  await expect(levelTable.getByRole("row")).toHaveCount(4);
  await expect(levelTable.getByText("Not set")).toHaveCount(3);
  await expect(defaults.locator(".connector-effective")).toContainText("No default in this scope");
  await expect(defaults.getByLabel("Example token default level").getByRole("option")).toHaveText(["Realm — dev", "Space — dev / shared", "Stack — dev / shared / services"]);
  await defaults.getByLabel("Example token default level").selectOption({ label: "Realm — dev" });
  await defaults.getByRole("combobox", { name: /^Example token default identity/ }).selectOption({ label: "example-default — dev (opaque)" });
  await defaults.getByRole("button", { name: "Set default", exact: true }).click();
  await expect(toasts(page).filter({ hasText: "Example token default set to example-default at Realm — dev." })).toBeVisible();
  expect(defaultPosts()[0].body).toEqual({ scope: { realm: "dev" }, method: "token", identity: { name: "example-default", realm: "dev" } });
  // Each level's value and the effective one.
  await expect(defaults.locator(".connector-effective")).toContainText("Effective: example-default dev — from the realm level.");
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
  await expect(defaults.locator(".connector-effective")).toContainText("Effective: example-team dev — from the stack level.");
  expect(defaultPosts()[1].body).toEqual({ scope: { realm: "dev", space: "shared", stack: "services" }, method: "token", identity: { name: "example-team", realm: "dev" } });
  await defaults.getByRole("button", { name: "Clear Example token default at stack level" }).click();
  await expect(defaults.locator(".connector-effective")).toContainText("Effective: example-default dev — from the realm level.");
  expect(defaultPosts()[2].body).toEqual({ scope: { realm: "dev", space: "shared", stack: "services" }, method: "token", identity: null });
  await defaults.getByRole("button", { name: "Clear Example token default at realm level" }).click();
  await expect(defaults.locator(".connector-effective")).toContainText("No default in this scope");
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

test("a team-owned Config shows its identities read-only with only Set default, and a login names its state volume", async ({ page }) => {
  await mockConnectors(page);
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
  // A login has no identity: the status names the state volume and "Log in" opens the New Cell flow.
  const demo = panel.getByRole("listitem", { name: "Demo connector" });
  await expect(demo).toContainText("Login kept in state volume kukeon-dev-demo-state");
  // The Config names the volume's scope, so the volume is a linked Volume reference.
  await expect(demo.getByRole("link", { name: "kukeon-dev-demo-state" })).toHaveAttribute("href", "#/volumes/dev/shared/services/kukeon-dev-demo-state");
  await expect(demo.getByRole("button")).toHaveCount(0);
  await expect(demo.getByRole("link", { name: "Log in to Demo" })).toHaveAttribute("href", newCellFor("teams"));
  await demo.getByRole("link", { name: "Log in to Demo" }).click();
  await expect(page.getByRole("heading", { name: "Create a stopped Cell" })).toBeVisible();
});

test("New Cell pre-selects each connector's effective identity, sends only changed picks and keeps Create disabled while one is unresolved", async ({ page }) => {
  const world = await mockConnectors(page);
  const previews = () => world.posts("/api/cells/from-config/preview");
  const previewButton = page.getByRole("button", { name: /^(Preview Cell|Refresh preview)$/ });
  const createButton = page.getByRole("button", { name: "Create Cell", exact: true });
  const panel = connectorsPanel(page);
  const item = panel.getByRole("listitem", { name: "Example connector" });
  const picker = panel.getByRole("combobox", { name: /^Example identity/ });
  const review = page.getByRole("table", { name: "Connector identities" });

  // A resolved Config: the effective identity is pre-selected and nothing is sent for it.
  await page.goto(`${server.url}${newCellFor("teams")}`);
  await expect(picker.locator("option:checked")).toHaveText("example-team — dev (opaque)");
  await expect(picker.getByRole("option")).toHaveText(["Use the Config's identity", "example-default — dev (opaque)", "example-team — dev (opaque)", "example-other — dev / shared / services (opaque)", "Name a secret…"]);
  await expect(item).toContainText("set by team kukeon");
  await expect(panel.getByRole("listitem", { name: "Demo connector" })).toContainText("Login kept in state volume kukeon-dev-demo-state");
  // A login has no identity to pick; its only control is the method picker (#1551).
  await expect(panel.getByRole("listitem", { name: "Demo connector" }).getByRole("combobox", { name: /^Demo identity/ })).toHaveCount(0);
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("example-team");
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("Login kept in state volume");
  expect(previews()).toHaveLength(1);
  expect(previews()[0].body).toEqual({ config: { name: "teams", realm: "dev", space: "shared", stack: "services" }, name: "", env: [], ignoreDiskPressure: false });

  // Choosing another identity of the connector sends it as an override, for this cell only.
  await picker.selectOption({ label: "example-other — dev / shared / services (opaque)" });
  await expect(createButton).toHaveCount(0);
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
  await expect(page.locator("form.creation-form").getByRole("alert")).toContainText('not an identity of connector "example-token"');
  expect(previews()[3].body.connectors).toEqual({ "example-token": { name: "untagged", realm: "dev" } });

  // The override travels with the create, bound to the reviewed preview.
  await picker.selectOption({ label: "example-default — dev (opaque)" });
  await page.getByLabel("Cell name (optional)").fill("picked-cell");
  await previewButton.click();
  await expect(review.getByRole("row", { name: /^Example/ })).toContainText("example-default");
  await createButton.click();
  await expect(page).toHaveURL(/#\/workspace\/dev\/shared\/services\/picked-cell$/);
  expect(world.posts("/api/cells/from-config/create")).toHaveLength(1);
  expect(world.posts("/api/cells/from-config/create")[0].body).toMatchObject({ name: "picked-cell", resolvedName: "picked-cell", preview: "reviewed", connectors: { "example-token": { name: "example-default", realm: "dev" } } });

  // An unresolved required connector is flagged before any preview or create.
  const sent = previews().length;
  await page.goto(`${server.url}${newCellFor("mine")}`);
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
  const review = page.getByRole("table", { name: "Connector identities" });

  // The Config uses the login: the method starts on it and there is no identity to pick.
  await page.goto(`${server.url}${newCellFor("teams")}`);
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
  await expect(review.getByRole("row", { name: /^Demo/ })).toContainText("Login kept in state volume");
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
  const method = page.getByLabel("Demo method", { exact: true });
  await expect(method.getByRole("option")).toHaveText(["login", "key"]);
  await expect(method).toHaveValue("demo-key");
  await expect(page.getByRole("checkbox", { name: /^demo-/ })).toHaveCount(0);
  const demoPicker = page.getByRole("combobox", { name: /^Demo identity/ });
  await expect(demoPicker.getByRole("option")).toHaveText(["No default — required", "demo-key-1 — dev (opaque)", "Name a secret…"]);
  await expect(page.getByRole("note")).toContainText("Demo identity");
  await expect(page.getByRole("link", { name: "set a default" })).toHaveAttribute("href", `#/connectors/demo?scope=${encodeURIComponent(JSON.stringify(["dev", "shared", "services"]))}`);
  // A login method takes no identity.
  await method.selectOption("demo-login");
  await expect(demoPicker).toHaveCount(0);
  await expect(page.getByText("Uses the login kept in the harness state volume")).toBeVisible();
  await expect(page.getByRole("note")).toHaveCount(0);

  // An ungrouped connector is a toggle; its picker shows the default the Config's scope inherits.
  await page.getByRole("checkbox", { name: /^example-token connector/ }).check();
  const examplePicker = page.getByRole("combobox", { name: /^Example identity/ });
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

test("the registry-delivered connector is Realm only and saves Secret-backed registry entries", async ({ page }) => {
  const world = await mockConnectors(page);
  const token = "s3cr3t-registry-token";
  await page.goto(`${server.url}#/connectors/pulls`);
  await expect(page.getByRole("region", { name: "Pulls connector" })).toContainText("image pulls of a Realm");
  // Realm only: no Space or Stack to choose, no scope default, no free-standing identity form.
  await expect(page.getByLabel("Registry realm")).toHaveValue("dev");
  await expect(page.getByLabel("Registry space")).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Pulls defaults" })).toHaveCount(0);
  await expect(page.getByRole("form", { name: "New Pulls identity" })).toHaveCount(0);
  const identities = page.getByRole("table", { name: "Pulls identities" });
  await expect(identities.getByRole("row")).toHaveCount(2);
  await expect(identities.getByRole("row", { name: /registry-ghcr\.io/ })).toContainText("basic-auth");

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
  // The credential it stored is an identity of the connector.
  await expect(identities.getByRole("row")).toHaveCount(3);
  await expect(identities.getByRole("row", { name: /registry-legacy\.example\.com/ })).toContainText("basic-auth");

  // A new server gets a Secret-backed entry too, and the Realm page lists the same entries.
  await form.getByLabel("Server").fill("registry.example.org");
  await form.getByLabel("Username").fill("bot");
  await form.getByLabel("Token").fill(token);
  await form.getByRole("button", { name: "Add credential" }).click();
  await expect(panel.getByRole("row", { name: /^registry\.example\.org/ }).getByRole("link", { name: "registry-registry.example.org" })).toBeVisible();
  await page.goto(`${server.url}#/resource/dev`);
  const realmPanel = page.getByRole("region", { name: "Registry credentials" });
  await expect(realmPanel.getByRole("row")).toHaveCount(4);
  await expect(realmPanel.getByRole("row", { name: /^ghcr\.io/ }).getByRole("link", { name: "registry-ghcr.io" })).toBeVisible();
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

test("cell screen toasts stay clear of the title-row buttons, so Start follows Stop without dismissing the toast", async ({ page }) => {
  const state = { value: "Ready" };
  const { posts, handler } = lifecycleMock(state);
  await mock(page, handler);
  await page.routeWebSocket("**/attach", readyAttach);
  const row = page.locator(".cell-page-title-row:visible");
  const overlaps = (a, b) => a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
  for (const [width, height] of [[1440, 900], [1024, 768], [768, 1024], [390, 844]]) {
    await page.setViewportSize({ width, height });
    for (const view of ["workspace", "resource"]) {
      await page.goto(`${server.url}#/${view}/dev/shared/services/api`);
      await row.getByRole("button", { name: "Stop", exact: true }).click();
      await expect.poll(() => posts.at(-1)).toBe("stop");
      await expect(toasts(page).first()).toHaveClass(/success/);
      const buttons = await row.getByRole("button").all();
      for (const toast of await toasts(page).all()) {
        const box = await toast.boundingBox();
        for (const button of buttons) {
          const target = await button.boundingBox();
          if (target) expect(overlaps(box, target), `${width} ${view}: toast covers ${await button.getAttribute("aria-label")}`).toBe(false);
        }
      }
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
  const home = page.getByRole("navigation", { name: "Resource scope" }).getByRole("link", { name: "Resources" });
  expect((await home.boundingBox()).height).toBeGreaterThanOrEqual(24);

  await page.goto(`${server.url}#/resource/dev/shared/services`);
  const crumbs = page.getByRole("navigation", { name: "Resource scope" }).getByRole("link");
  await expect(crumbs).toHaveCount(3);
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

test("route changes focus the new page heading and title the document; the first Tab stop skips to main", async ({ page }) => {
  await mock(page);
  await page.goto(`${server.url}#/resources`);
  const heading = page.locator("main .page-heading h1");
  await expect(heading).toHaveText("Resources");
  await expect(page).toHaveTitle("Resources · kukeon");
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
