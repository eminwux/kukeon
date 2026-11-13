import { test, expect } from "@playwright/test";
import { startServer } from "./server.mjs";

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
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(terminalTools(page).locator("option")).toHaveCount(2);
  await terminalTools(page).getByLabel("Container").selectOption("shell");
  await expect.poll(() => selected).toContain("/containers/shell/attach");
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Connecting…");
  sendReady();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveAttribute("title", /waiting for container output/);
  sendOutput();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  await expect.poll(() => panel.locator(".xterm-fg-13").evaluate(el => getComputedStyle(el).color)).toBe("rgb(255, 0, 255)");
  await expect.poll(() => panel.locator(".xterm-rows").first().evaluate(el => getComputedStyle(el).fontFamily)).toContain("monospace");
  await expect(panel.locator(".xterm-cursor.xterm-cursor-blink")).toHaveClass(/xterm-cursor-underline/);
  await expect.poll(() => panel.locator(".xterm-cursor.xterm-cursor-blink").evaluate(el => getComputedStyle(el).animationName)).not.toBe("none");
  await expect.poll(() => messages.some(message => typeof message === "string" && JSON.parse(message).type === "resize")).toBe(true);
  await panel.locator(".terminal-session:not([hidden]) .xterm-helper-textarea").press("a");
  await expect.poll(() => messages.some(message => Buffer.isBuffer(message) && message.toString() === "a")).toBe(true);
  await terminalTools(page).getByRole("button", { name: "Detach" }).click();
  await expect.poll(() => closed).toBe(true);
});

test("expanded terminal keeps its session, resizes, and follows the appearance setting", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  let connections = 0;
  const messages = [];
  await page.routeWebSocket("**/attach", socket => {
    connections++;
    socket.onMessage(message => { messages.push(message); });
    socket.send(JSON.stringify({ type: "ready" }));
  });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  const screen = panel.locator(".terminal-screen");
  const screenHandle = await screen.elementHandle();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(panel).toBeHidden();
  expect(connections).toBe(1);
  await page.getByRole("tab", { name: "Terminal" }).click();
  await expect(panel).toBeVisible();
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

test("expanded terminal starts below a wrapped terminal tabbar", async ({ page }) => {
  await mock(page, path => path.endsWith("/containers") ? { json: [{ id: "shell", attachable: true }] } : undefined);
  await page.routeWebSocket("**/attach", socket => { socket.send(JSON.stringify({ type: "ready" })); });
  // 933px is a 1400px window at 150% zoom: the expanded tabbar wraps its terminal controls onto a second row.
  await page.setViewportSize({ width: 933, height: 600 });
  await page.goto(`${server.url}#/workspace/dev/shared/services/api`);
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  await terminalTools(page).getByRole("button", { name: "Expand terminal" }).click();
  const panel = page.getByRole("region", { name: "Terminal workspace" });
  await expect(panel).toHaveClass(/is-expanded/);
  const tabbar = page.locator(".cell-detail-tabbar");
  await expect.poll(async () => (await panel.boundingBox()).y - ((await tabbar.boundingBox()).y + (await tabbar.boundingBox()).height)).toBeGreaterThanOrEqual(0);
  await page.setViewportSize({ width: 1400, height: 900 });
  await expect.poll(async () => (await panel.boundingBox()).y - ((await tabbar.boundingBox()).y + (await tabbar.boundingBox()).height)).toBeGreaterThanOrEqual(0);
  await terminalTools(page).getByRole("button", { name: "Collapse terminal" }).click();
  await expect(page.locator(".cell-detail-screen")).not.toHaveAttribute("style", /--terminal-expanded-top/);
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
  await panel.locator(".terminal-session:not([hidden]) .xterm-helper-textarea").press("p");
  await expect.poll(() => messages.get(prodPath).some(message => Buffer.isBuffer(message) && message.toString() === "p")).toBe(true);
  expect(messages.get(devPath).some(message => Buffer.isBuffer(message) && message.toString() === "p")).toBe(false);
  await cells.getByRole("link", { name: "Open dev / shared / services / api" }).click();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toHaveText("Attached");
  expect(await devScreen.evaluate(el => el === document.querySelector('.terminal-screen[aria-label="Terminal for dev / shared / services / api / shell"]'))).toBe(true);
  await panel.locator(".terminal-session:not([hidden]) .xterm-helper-textarea").press("d");
  await expect.poll(() => messages.get(devPath).some(message => Buffer.isBuffer(message) && message.toString() === "d")).toBe(true);
  await expect.poll(() => messages.get(devPath).filter(message => typeof message === "string" && JSON.parse(message).type === "resize").length).toBeGreaterThanOrEqual(2);
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
  await expect(terminalTools(page).getByText("Terminal limit reached.", { exact: false })).toBeVisible();
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
  await expect(page.getByText("Stale inventory — refresh failed.", { exact: false })).toBeVisible();
  await expect(terminalTools(page).locator(".terminal-connection-state")).toContainText("Attached");
  expect(closed).toBe(closedBefore);
  failInventory = false;
  stopped = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(terminalTools(page).getByText("Terminal unavailable: cell is Stopped.")).toBeVisible();
  await expect(terminalTools(page).getByRole("button", { name: "Reconnect" })).toHaveCount(0);
  await expect.poll(() => closed).toBeGreaterThan(closedBefore);
});

test("Workspace lands first and Resources retains resource summaries and navigation", async ({ page }) => {
  await mock(page);
  await page.goto(server.url);
  await expect(page.locator(".page-heading h1")).toHaveText("Workspace");
  await expect(page.getByRole("region", { name: "Workspace welcome" })).toContainText("Choose a cell");
  await page.getByRole("link", { name: "Resources", exact: true }).click();
  await expect(page.locator(".page-heading h1")).toHaveText("Resources");
  for (const type of ["Realms", "Spaces", "Stacks", "Cells"]) {
    await expect(page.locator(".summary-card").filter({ hasText: type }).locator("strong")).toHaveText("2");
    await expect(nav(page).getByRole("link", { name: type, exact: true })).toBeVisible();
  }
  await expect(page.getByRole("heading", { name: "Cell states" })).toBeVisible();
  await expect(page.locator("pre")).toHaveCount(0);
  await expect(page.getByRole("navigation", { name: "Cell navigation" }).getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
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
  await expect(page.getByText("No attachable containers are available.")).toBeVisible();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByRole("heading", { name: "Runtime status" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Containers" })).toBeVisible();
  await expect(page.locator("#cell-terminal")).toHaveCount(1);
  await expect(page.getByRole("link", { name: "+ New Cell" })).toHaveAttribute("href", "#/new-cell");
  await cells.getByRole("link", { name: "Open prod / shared / services / api" }).click();
  await expect(page).toHaveURL(/#\/workspace\/prod\/shared\/services\/api$/);
  await expect(cells.getByRole("link", { name: "Open prod / shared / services / api" })).toHaveAttribute("aria-current", "page");
  expect(inventoryReads).toBe(initialReads);
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
  await expect(page.getByText(/Stale inventory/)).toBeVisible();
  await expect(cells.getByRole("link")).toHaveCount(2);
  await expect(page.getByRole("region", { name: "All cells" })).toContainText("Inventory incomplete or stale");
  fail = false;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Connected");
  await expect(page.locator(".stale")).toHaveCount(0);
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

test("empty Workspace offers existing config creation without inventing cells", async ({ page }) => {
  await mockCatalog(page);
  await page.unroute("**/api/realms**");
  await page.route("**/api/realms**", route => route.fulfill({ json: [] }));
  await page.goto(server.url);
  await expect(page.getByRole("region", { name: "All cells" })).toContainText("No cells found");
  await page.getByRole("region", { name: "Workspace welcome" }).getByRole("link", { name: /New Cell/ }).click();
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
    if (type === "Cells") {
      await expect(page.getByRole("tab", { name: "Overview" })).toBeVisible();
    } else {
      await expect(page.getByRole("heading", { name: "Resource overview" })).toBeVisible();
    }
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
  await page.getByRole("tab", { name: "Overview" }).click();
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
  await expect(page.getByText(/Stale inventory/)).toBeVisible();
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
  await expect(page.getByRole("alert")).toContainText("Incomplete inventory");
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
  await expect(page.getByText(/Stale inventory/)).toBeVisible();
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

test("mobile cell navigation closes after selection and Workspace fits the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await page.goto(server.url);
  await expect(page.locator(".page-heading h1")).toHaveText("Workspace");
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
  await page.getByRole("tab", { name: "Overview" }).click();
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
  await page.getByRole("tab", { name: "Overview" }).click();
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
  await expect(page.getByRole("alert")).toContainText("Blueprint is missing");
  missing = false;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("heading", { name: "Resource overview" })).toBeVisible();
  await page.getByRole("link", { name: "View document", exact: true }).click();
  await expect(page.getByLabel("Complete blueprints JSON")).toContainText('"realm": "library"');
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
  await expect(page.locator(".cell-lifecycle-feedback")).toContainText("Cell killed");
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
  await expect(page.locator(".cell-global-feedback")).toContainText("Cell purged");
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
  const feedback = page.locator(".cell-lifecycle-feedback");
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
  await expect(page).toHaveURL(/#\/workspace$/);
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
  await expect(page.locator(".cell-lifecycle-feedback")).toContainText("Permission denied");
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
  await expect(page.locator(".cell-lifecycle-feedback")).toContainText("Daemon unavailable");
  await expect(page.locator(".cell-lifecycle-feedback")).toContainText("fresh daemon read confirms the cell still exists");
  await expect(page.getByRole("link", { name: "Open dev / shared / services / api" })).toBeVisible();
  await expect.poll(() => closed).toBe(true);
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

test("cell Overview tab shows the cell cgroup with nested runtime and delegated controllers", async ({ page }) => {
  await mock(page, path => path.endsWith("/cells/api/cgroup") ? { json: { cgroupPath: "/kukeon/dev/shared/services/api", absolutePath: "/sys/fs/cgroup/kukeon/dev/shared/services/api", exists: true, ready: true, nestedCgroupRuntime: true, subtreeControllers: ["cpu", "io", "memory", "pids"], live: { memory: { max: { state: "set", raw: "1073741824", value: 1073741824 } }, io: { state: "set", entries: [] } } } } : undefined);
  await page.goto(`${server.url}#/resource/dev/shared/services/api`);
  await page.getByRole("tab", { name: "Overview" }).click();
  const panel = page.getByRole("region", { name: "cell cgroup" });
  await expect(panel.getByText("cpu io memory pids", { exact: true })).toBeVisible();
  await expect(panel.getByText("Enabled", { exact: true })).toBeVisible();
  await expect(panel.getByText("1 GiB", { exact: false })).toBeVisible();
  await expect(panel.getByText("No device limits")).toBeVisible();
});
