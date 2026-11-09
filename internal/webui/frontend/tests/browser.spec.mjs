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
    const result = await handler(path);
    await route.fulfill(result ?? { json: fixture(path) });
  });
}
const failure = { status: 503, json: { error: { code: "unavailable", message: "Scoped read failed" } } };
const nav = page => page.getByRole("navigation", { name: "Main navigation" });

test("cells are the landing page and dashboard remains available", async ({ page }) => {
  await mock(page);
  await page.goto(server.url);
  await expect(page.getByRole("heading", { name: "Cells", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Create cell" })).toBeVisible();
  await expect(page.getByRole("row")).toHaveCount(3);
  await page.goto(`${server.url}#/dashboard`);
  await expect(page.getByRole("heading", { name: "Dashboard", exact: true })).toBeVisible();
  for (const type of ["Realms", "Spaces", "Stacks", "Cells"]) {
    await expect(page.locator(".summary-card").filter({ hasText: type }).locator("strong")).toHaveText("2");
    await expect(nav(page).getByRole("link", { name: type, exact: true })).toBeVisible();
  }
  await expect(page.getByRole("heading", { name: "Cell states" })).toBeVisible();
  await expect(page.locator("pre")).toHaveCount(0);
  await expect(nav(page).getByText("shared")).toHaveCount(0);
});

for (const type of ["Realms", "Spaces", "Stacks", "Cells"]) {
  test(`${type} list shows entries and scope, with complete documents only on request`, async ({ page }) => {
    await mock(page);
    await page.goto(`${server.url}#/${type.toLowerCase()}`);
    await expect(page.getByRole("heading", { name: type, exact: true })).toBeVisible();
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
  await page.getByRole("searchbox").fill("missing");
  await expect(page.getByText("No matching resources")).toBeVisible();
  await page.getByRole("searchbox").fill("api");
  await page.getByRole("link", { name: "api", exact: true }).click();
  await expect(page).toHaveURL(/#\/resource\/prod\/shared\/services\/api$/);
  await expect(page.locator("pre")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Containers" })).toBeVisible();
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
  await page.getByRole("searchbox").fill("api-00");
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
  await page.goto(`${server.url}#/dashboard`);
  await expect(page.getByRole("alert")).toContainText("Incomplete inventory");
  await expect(page.locator(".summary-card").filter({ hasText: "Cells" })).toContainText("Incomplete or stale data");
  await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Needs attention");
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

test("theme preference follows the system and persists explicit overrides", async ({ page }) => {
  await mock(page);
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(server.url);
  const scheme = () => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme);
  await expect.poll(scheme).toBe("dark");
  await page.getByLabel("Appearance").selectOption("light");
  await page.reload();
  await expect(page.getByLabel("Appearance")).toHaveValue("light");
  await expect.poll(scheme).toBe("light");
  await page.getByLabel("Appearance").selectOption("system");
  await expect.poll(scheme).toBe("dark");
  await page.emulateMedia({ colorScheme: "light" });
  await expect.poll(scheme).toBe("light");
});

test("mobile navigation closes after selecting a list and dashboard fits the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await page.goto(server.url);
  await expect(page.getByRole("heading", { name: "Cells", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await nav(page).getByRole("link", { name: "Cells", exact: true }).click();
  await expect(page.getByRole("complementary")).toBeHidden();
  await expect(page.getByRole("heading", { name: "Cells", exact: true })).toBeVisible();
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

test("cells page creates a cell from a selected config", async ({ page }) => {
  await mock(page);
  const config = { metadata: { name: "web", realm: "dev" }, blueprint: { name: "web-template", realm: "dev" }, valueKeys: ["IMAGE_TAG"], repoSlots: [], secretSlots: [] };
  await page.route("**/api/configs**", route => route.fulfill({ json: [config] }));
  let requestBody;
  await page.route("**/api/cells/from-config", async route => {
    requestBody = route.request().postDataJSON();
    await route.fulfill({ status: 201, json: { cell: doc(["dev", "shared", "services", "web-123abc"]), started: false } });
  });
  await page.goto(server.url);
  await page.getByRole("button", { name: "Create cell" }).click();
  const dialog = page.getByRole("dialog", { name: "Create cell from config" });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("Realm", { exact: false }).selectOption("dev");
  await dialog.getByLabel("Space").selectOption("shared");
  await dialog.getByLabel("Stack").selectOption("services");
  await dialog.getByLabel("Cell config").selectOption(JSON.stringify(config.metadata));
  await dialog.getByLabel("Cell name").fill("web-123abc");
  await dialog.getByRole("button", { name: "Create stopped cell" }).click();
  await expect(page).toHaveURL(/#\/resource\/dev\/shared\/services\/web-123abc$/);
  expect(requestBody).toMatchObject({ config: config.metadata, scope: { realm: "dev", space: "shared", stack: "services" }, name: "web-123abc", start: false });
});

test("cell detail links config lineage and opens a browser terminal", async ({ page }) => {
  const scope = ["prod", "shared", "services", "api"];
  const cell = { ...doc(scope), spec: { containers: [{ id: "work", attachable: true }], provenance: { bindingKind: "config", bindingRef: { name: "web", realm: "prod", space: "shared", stack: "services" } } }, status: { state: "Ready", cgroupReady: true, containers: [{ name: "work", state: "Running" }] } };
  await mock(page, path => path === "/api/realms/prod/spaces/shared/stacks/services/cells/api" ? { json: cell } : undefined);
  await page.route("**/api/configs**", route => route.fulfill({ json: { metadata: { name: "web", realm: "prod", space: "shared", stack: "services" }, blueprint: { name: "web-template", realm: "prod" }, valueKeys: ["IMAGE_TAG"], repoSlots: [], secretSlots: [] } }));
  await page.routeWebSocket(/\/terminal$/, socket => { socket.onMessage(() => {}); });
  await page.goto(`${server.url}#/resource/${scope.join("/")}`);
  const lineage = page.locator(".lineage-panel");
  await expect(lineage.getByRole("heading", { name: "Created from config" })).toBeVisible();
  await lineage.getByRole("link", { name: "web", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Config binding" })).toBeVisible();
  await expect(page.getByText("1 values · 0 repository slots · 0 secret slots")).toBeVisible();
  await page.goto(`${server.url}#/resource/${scope.join("/")}`);
  await page.getByRole("button", { name: "Open terminal" }).first().click();
  await expect(page.getByRole("region", { name: "Terminal for work" })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
  await page.getByRole("button", { name: "Close terminal" }).click();
  await expect(page.getByRole("region", { name: "Terminal for work" })).toHaveCount(0);
});
