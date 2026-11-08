import { test, expect } from "@playwright/test";
import { startServer } from "./server.mjs";

const realm = (name) => ({ apiVersion: "kukeon.io/v1beta1", kind: "Realm", metadata: { name, labels: { team: "platform" } }, spec: { namespace: `${name}.kukeon.io` }, status: { state: "Ready", futureField: [1, { nested: true }] } });
let server;
test.beforeEach(async () => { server = await startServer(); });
test.afterEach(async () => { await server?.stop(); });

async function mock(page, handler) {
  await page.route("**/api/realms**", async (route) => {
    const result = await handler(new URL(route.request().url()).pathname);
    await route.fulfill({ status: result.status ?? 200, json: result.body });
  });
}
const error = (code, status) => ({ status, body: { error: { code, message: "Test connection failure" } } });

test("complete documents, refresh failure retains stale data, retry recovers", async ({ page }) => {
  let fail = false;
  await mock(page, (path) => fail ? error("unavailable", 503) : { body: path === "/api/realms" ? [realm("default")] : realm("default") });
  await page.goto(server.url);
  await expect(page.getByRole("status")).toHaveText("Connected");
  await expect(page.getByLabel("Daemon connection")).toContainText("missing.sock");
  await page.getByRole("button", { name: "default" }).click();
  const document = page.getByLabel("Complete realm JSON");
  await expect(document).toHaveText(JSON.stringify(realm("default"), null, 2));
  fail = true;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("Stale realm list — refresh failed.")).toBeVisible();
  await expect(page.getByText("Stale realm document — refresh failed.")).toBeVisible();
  await expect(document).toHaveText(JSON.stringify(realm("default"), null, 2));
  fail = false;
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("status")).toHaveText("Connected");
  await expect(page.locator(".stale")).toHaveCount(0);
});

test("loading, empty, and permission errors are distinct", async ({ page }) => {
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  let result = { body: [] };
  await mock(page, async () => { await gate; return result; });
  await page.goto(server.url);
  await expect(page.getByText("Loading realms…")).toBeVisible();
  release();
  await expect(page.getByText("No realms found.")).toBeVisible();
  result = error("permission_denied", 403);
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("alert")).toContainText("Permission denied");
  await expect(page.getByText("Stale realm list — refresh failed.")).toBeVisible();
});

test("a replaced detail request cannot overwrite a newer selection", async ({ page }) => {
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  await mock(page, async (path) => {
    if (path === "/api/realms") return { body: [realm("first"), realm("second")] };
    if (path.endsWith("/first")) await gate;
    return { body: realm(path.split("/").at(-1)) };
  });
  await page.goto(server.url);
  await page.getByRole("button", { name: "first" }).click();
  await expect(page.getByText("Loading realm document…")).toBeVisible();
  await page.getByRole("button", { name: "second" }).click();
  await expect(page.getByLabel("Complete realm JSON")).toHaveText(JSON.stringify(realm("second"), null, 2));
  release();
  await expect(page.getByRole("heading", { name: "second", exact: true })).toBeVisible();
  await expect(page.getByLabel("Complete realm JSON")).not.toContainText('"first"');
});

test("switching realms clears the previous document even when the next realm is missing", async ({ page }) => {
  await mock(page, (path) => path === "/api/realms" ? { body: [realm("first"), realm("deleted")] }
    : path.endsWith("/first") ? { body: realm("first") } : error("not_found", 404));
  await page.goto(server.url);
  await page.getByRole("button", { name: "first" }).click();
  await expect(page.getByLabel("Complete realm JSON")).toBeVisible();
  await page.getByRole("button", { name: "deleted" }).click();
  await expect(page.getByRole("alert")).toContainText("Realm no longer exists");
  await expect(page.getByLabel("Complete realm JSON")).toHaveCount(0);
});

test("packaged UI survives an actual missing Unix socket and can retry", async ({ page }) => {
  await page.goto(server.url);
  await expect(page.getByRole("alert")).toContainText("Daemon unavailable");
  await expect(page.getByText("Stale realm list — refresh failed.")).toHaveCount(0);
  await mock(page, () => ({ body: [] }));
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("status")).toHaveText("Connected");
  await expect(page.getByText("No realms found.")).toBeVisible();
});
