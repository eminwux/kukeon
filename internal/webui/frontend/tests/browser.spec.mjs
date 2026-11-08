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

test("dashboard is the landing page with resource summaries and type navigation", async ({ page }) => {
  await mock(page);
  await page.goto(server.url);
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
  await page.goto(server.url);
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
  await expect(page.getByRole("heading", { name: "Dashboard", exact: true })).toBeVisible();
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
    await page.getByRole("searchbox").fill("missing");
    await expect(page.getByText("No matching resources")).toBeVisible();
    await page.getByRole("searchbox").fill(kind === "secrets" ? "prod / shared / services / api" : "prod / shared / services");
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
