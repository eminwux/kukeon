import { test, expect } from "@playwright/test";
import { build, defaultClientConditions } from "vite";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { extname, join, resolve } from "node:path";

// The extension contract (#1754): the console mounted with the test-only fixture extension
// (fixtures/extension-fixture.tsx), whose route, nav entries and slot content all require the capability
// fixture.v1. The page is built from the sources with Vite, like mount.spec.mjs, and served from a
// loopback server that mocks the API; each test picks what GET /api/capabilities answers.
const contentTypes = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".woff2": "font/woff2" };
let outDir;

test.beforeAll(async () => {
  test.setTimeout(180000);
  outDir = await mkdtemp(join(tmpdir(), "kuke-extension-"));
  await build({ root: resolve("."), configFile: false, logLevel: "silent", resolve: { conditions: ["@kukeon/source", ...defaultClientConditions] }, build: { outDir, emptyOutDir: true, rollupOptions: { input: resolve("tests/fixtures/extension.html") } } });
});
test.afterAll(async () => { if (outDir) await rm(outDir, { recursive: true, force: true }); });

const kinds = ["Realm", "Space", "Stack", "Cell"];
const names = ["dev", "shared", "services", "api"];
const doc = scope => ({ apiVersion: "v1beta1", kind: kinds[scope.length - 1], metadata: { name: scope.at(-1) }, spec: {}, status: { state: "Ready" } });
const cell = "dev/shared/services/api";
const withFixture = { json: { version: "v9.9.9", features: ["resources.v1", "fixture.v1"] } };
const withoutFixture = { json: { version: "v9.9.8", features: ["resources.v1"] } };
// A host from before /api/capabilities answers the path like any unknown one.
const notServed = { status: 404, json: { error: { code: "not_found", message: "no such route" } } };

// serve starts the loopback server; capabilities is the answer to GET /api/capabilities. It records the
// API paths requested.
async function serve(capabilities) {
  const apiPaths = [];
  const server = createServer(async (request, response) => {
    const { pathname, search } = new URL(request.url, "http://localhost");
    if (pathname.startsWith("/api/")) {
      const path = pathname.slice("/api".length);
      apiPaths.push(path + search);
      const { status = 200, json } = answer(path, capabilities);
      response.writeHead(status, { "Content-Type": "application/json" }).end(JSON.stringify(json));
      return;
    }
    try {
      const body = await readFile(join(outDir, pathname));
      response.writeHead(200, { "Content-Type": contentTypes[extname(pathname)] ?? "application/octet-stream" }).end(body);
    } catch {
      response.writeHead(404).end();
    }
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const url = `http://127.0.0.1:${server.address().port}/tests/fixtures/extension.html`;
  return { url, apiPaths, stop: async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); } };
}
// One realm/space/stack/cell chain whose cell has one container, a signed-in session, the capabilities
// answer and a 404 for everything else.
function answer(path, capabilities) {
  if (path === "/session") return { json: { user: "root" } };
  if (path === "/capabilities") return capabilities;
  if (path === "/connection") return { json: { endpoint: "unix:///test.sock", version: "v9.9.9" } };
  const parts = path.split("/").filter(Boolean);
  if (parts[0] === "realms") {
    if (parts.length === 9 && parts[8] === "containers") return { json: [{ id: "shell" }] };
    const depth = Math.floor(parts.length / 2);
    if (parts.length % 2 === 1 && depth < 4) return { json: [doc(names.slice(0, depth + 1))] };
    if (parts.length % 2 === 0 && depth <= 4) return { json: doc(names.slice(0, depth)) };
  }
  return { status: 404, json: { error: { code: "not_found", message: `no mock for ${path}` } } };
}

const manageNav = page => page.getByRole("navigation", { name: "Manage navigation" });
const settingsNav = page => page.getByRole("navigation", { name: "Settings navigation" });
const content = page => page.locator(".workspace-content");
const heading = page => content(page).locator(".page-heading h1");
const userButton = page => page.locator(".toolbar").getByRole("button", { name: /^User menu/ });

test("with its capability the fixture extension's route, nav entries and slots render", async ({ page }) => {
  const server = await serve(withFixture);
  try {
    await page.goto(`${server.url}#/resources`);
    // topBarStart and overviewCards.
    await expect(page.locator(".toolbar").getByText("Fixture top bar")).toBeVisible();
    await expect(content(page).locator(".summary-grid .extension-card")).toContainText("Fixture card42");
    // The Manage entry opens the route, which renders with the host's capabilities.
    await manageNav(page).getByRole("link", { name: "Fixture reports" }).click();
    await expect(page).toHaveURL(/#\/ext\/fixture\/reports$/);
    await expect(heading(page)).toHaveText("Fixture reports");
    await expect(content(page).getByText("Fixture route of fixture on kuke v9.9.9.")).toBeVisible();
    await expect(manageNav(page).getByRole("link", { name: "Fixture reports" })).toHaveAttribute("aria-current", "page");
    // userMenuItems.
    await userButton(page).click();
    await expect(page.getByRole("menuitem", { name: "Fixture menu item" })).toHaveAttribute("href", "#/ext/fixture/reports");
    await page.keyboard.press("Escape");
    // The Settings entry, beside the console's Profile section; its route shows in the Settings view.
    await page.goto(`${server.url}#/settings`);
    await expect(settingsNav(page).getByRole("link", { name: "Profile" })).toHaveAttribute("aria-current", "page");
    await settingsNav(page).getByRole("link", { name: "Fixture preferences" }).click();
    await expect(page).toHaveURL(/#\/ext\/fixture\/preferences$/);
    await expect(heading(page)).toHaveText("Fixture preferences");
    await expect(content(page).getByText("Fixture preferences page.")).toBeVisible();
    await expect(settingsNav(page).getByRole("link", { name: "Fixture preferences" })).toHaveAttribute("aria-current", "page");
    await expect(settingsNav(page).getByRole("link", { name: "Profile" })).not.toHaveAttribute("aria-current", "page");
    // cellDetailsTabs: the console's details are the first tab, the extension's tab follows.
    await page.goto(`${server.url}#/resource/${cell}`);
    const tabs = content(page).getByRole("tablist", { name: "Cell details sections" });
    await expect(tabs.getByRole("tab", { name: "Details" })).toHaveAttribute("aria-selected", "true");
    await expect(content(page).getByRole("heading", { name: "Containers" })).toBeVisible();
    await tabs.getByRole("tab", { name: "Fixture notes" }).click();
    await expect(content(page).getByRole("tabpanel")).toHaveText("Fixture notes for dev / shared / services / api.");
    await expect(content(page).getByRole("heading", { name: "Containers" })).toHaveCount(0);
    await tabs.getByRole("tab", { name: "Fixture notes" }).press("ArrowLeft");
    await expect(tabs.getByRole("tab", { name: "Details" })).toHaveAttribute("aria-selected", "true");
    await expect(content(page).getByRole("heading", { name: "Containers" })).toBeVisible();
    // Every move above changed only the hash, so the console stayed mounted on one connection and read
    // the capabilities once, not on every page.
    expect(server.apiPaths.filter(path => path === "/session")).toHaveLength(1);
    expect(server.apiPaths.filter(path => path === "/capabilities")).toHaveLength(1);
  } finally {
    await server.stop();
  }
});

for (const [name, capabilities, reason] of [
  ["without its capability", withoutFixture, "This host does not provide the features this page needs."],
  ["on a host that does not serve /api/capabilities", notServed, "This host did not report its capabilities, so it cannot run this page."],
]) {
  test(`${name} the fixture extension is hidden and its route shows the update notice`, async ({ page }) => {
    const server = await serve(capabilities);
    try {
      await page.goto(`${server.url}#/resources`);
      await expect(content(page).locator(".summary-grid .summary-card-health")).toBeVisible();
      await expect.poll(() => server.apiPaths.includes("/capabilities")).toBe(true);
      await expect(manageNav(page).getByRole("link", { name: "Daemon" })).toBeVisible();
      await expect(manageNav(page).getByRole("link", { name: "Fixture reports" })).toHaveCount(0);
      await expect(page.locator(".toolbar-extensions")).toHaveCount(0);
      await expect(page.getByText("Fixture top bar")).toHaveCount(0);
      await expect(content(page).locator(".extension-card")).toHaveCount(0);
      await userButton(page).click();
      await expect(page.getByRole("menuitem", { name: "Sign out" })).toBeVisible();
      await expect(page.getByRole("menuitem", { name: "Fixture menu item" })).toHaveCount(0);
      await page.keyboard.press("Escape");
      await page.goto(`${server.url}#/settings`);
      await expect(settingsNav(page).getByRole("link", { name: "Profile" })).toBeVisible();
      await expect(settingsNav(page).getByRole("link", { name: "Fixture preferences" })).toHaveCount(0);
      await page.goto(`${server.url}#/resource/${cell}`);
      await expect(content(page).getByRole("heading", { name: "Containers" })).toBeVisible();
      await expect(content(page).getByRole("tablist", { name: "Cell details sections" })).toHaveCount(0);
      // The route itself is still recognized: it names what the host is missing.
      await page.goto(`${server.url}#/ext/fixture/reports`);
      await expect(heading(page)).toHaveText("Fixture reports");
      const notice = content(page).getByRole("region", { name: "Update this host" });
      await expect(notice).toContainText(reason);
      await expect(notice.locator("code")).toHaveText("fixture.v1");
      await expect(content(page).getByText(/Fixture route of/)).toHaveCount(0);
    } finally {
      await server.stop();
    }
  });
}

test("a route no mounted extension declares is not found", async ({ page }) => {
  const server = await serve(withFixture);
  try {
    await page.goto(`${server.url}#/ext/fixture/missing`);
    await expect(heading(page)).toHaveText("Page not found");
    await expect(content(page).getByText("No console extension provides this page.")).toBeVisible();
  } finally {
    await server.stop();
  }
});
