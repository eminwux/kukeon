import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { test, expect, request as playwrightRequest } from "@playwright/test";
import { startServer } from "./server.mjs";

// Explicit smoke command must fail, not silently skip, without its prerequisite.
const endpoint = process.env.KUKE_WEB_SMOKE_HOST;
if (!endpoint) throw new Error("Set KUKE_WEB_SMOKE_HOST to a running kukeond Unix endpoint (see docs/web-ui-development.md).");

// The web UI login (#1652): KUKE_WEB_SMOKE_PASSWORD, or read back from the web-ui-admin secret.
function loginPassword() {
  if (process.env.KUKE_WEB_SMOKE_PASSWORD) return process.env.KUKE_WEB_SMOKE_PASSWORD;
  const binary = resolve(process.env.KUKE_WEB_BINARY ?? "../../../kuke");
  const out = execFileSync(binary, ["--configuration", "/dev/null", "--host", endpoint, "get", "secret", "web-ui-admin", "--realm", "kuke-system", "-o", "json"], { encoding: "utf8" });
  const password = JSON.parse(out).spec?.basicAuth?.password;
  if (!password) throw new Error("web-ui-admin has no password; run `sudo kuke init` once (see docs/web-ui-development.md).");
  return password;
}

test("packaged browser reads real daemon documents and unavailable socket", async ({ page }) => {
  const password = loginPassword();
  const request = await playwrightRequest.newContext({ extraHTTPHeaders: { Authorization: `Bearer ${password}` } });
  const server = await startServer(endpoint);
  try {
    // Every API route needs the login; the bearer header carries it for direct requests.
    expect((await request.get(`${server.url}/api/realms`, { headers: { Authorization: "" } })).status()).toBe(401);
    await page.goto(server.url);
    const login = page.getByRole("form", { name: "Sign in" });
    await login.getByLabel("Password").fill(password);
    await login.getByRole("button", { name: "Sign in" }).click();
    const response = await request.get(`${server.url}/api/realms`);
    expect(response.ok()).toBeTruthy();
    const realms = await response.json();
    expect(realms.length, "Smoke daemon must contain at least one realm").toBeGreaterThan(0);
    await page.goto(server.url);
    await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Connected");
    await expect(page.getByLabel("Daemon connection")).toContainText(endpoint);
    for (const realm of realms) {
      const detail = await request.get(`${server.url}/api/realms/${encodeURIComponent(realm.metadata.name)}`);
      expect(detail.ok()).toBeTruthy();
      await page.goto(`${server.url}#/resource/${encodeURIComponent(realm.metadata.name)}?document=1`);
      await expect(page.getByLabel("Complete realm JSON")).toHaveText(JSON.stringify(await detail.json(), null, 2));
    }
    // Catalog APIs are read-only and retain daemon-side secret redaction.
    for (const kind of ["blueprints", "configs", "volumes", "secrets"]) {
      const collection = await request.get(`${server.url}/api/catalog/${kind}`);
      expect(collection.ok()).toBeTruthy();
      const entries = await collection.json();
      await page.goto(`${server.url}#/${kind}`);
      await expect(page.getByRole("heading", { name: kind[0].toUpperCase() + kind.slice(1), exact: true })).toBeVisible();
      if (kind === "secrets") for (const entry of entries) expect(entry.spec?.data).toBeUndefined();
      if (!entries.length) continue;
      const md = entries[0].metadata;
      const coords = ["realm", "space", "stack", "cell"];
      const params = new URLSearchParams();
      const scope = [];
      for (const key of coords) if (md[key]) { params.set(key, md[key]); scope.push(md[key]); }
      const detail = await request.get(`${server.url}/api/catalog/${kind}/${encodeURIComponent(md.name)}?${params}`);
      expect(detail.ok()).toBeTruthy();
      const document = await detail.json();
      if (kind === "secrets") expect(document.spec?.data).toBeUndefined();
      await page.goto(`${server.url}#/${kind}/${[...scope, md.name].map(encodeURIComponent).join("/")}?document=1`);
      await expect(page.getByLabel(`Complete ${kind} JSON`)).toHaveText(JSON.stringify(document, null, 2));
    }
    // Walk one populated branch at each level against the actual daemon.
    let scope = [];
    let path = "/api/realms";
    for (const kind of ["realm", "space", "stack", "cell", "container"]) {
      const response = await request.get(`${server.url}${path}`);
      expect(response.ok()).toBeTruthy();
      const entries = await response.json();
      if (!entries.length) break;
      const name = entries[0].metadata?.name ?? entries[0].id;
      scope.push(name);
      path += `/${encodeURIComponent(name)}`;
      const document = await request.get(`${server.url}${path}`);
      expect(document.ok()).toBeTruthy();
      await page.goto(`${server.url}#/resource/${scope.map(encodeURIComponent).join("/")}?document=1`);
      await expect(page.getByLabel(`Complete ${kind} JSON`)).toHaveText(JSON.stringify(await document.json(), null, 2));
      const next = { realm: "spaces", space: "stacks", stack: "cells", cell: "containers" }[kind];
      if (next) path += `/${next}`;
    }
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(page.getByLabel("Daemon connection").getByRole("status")).toHaveText("Connected");
    // Daemon health and logs are read from the running kukeond over its socket.
    const health = await (await request.get(`${server.url}/api/daemon/health`)).json();
    expect(health.status).toBe("reachable");
    expect(health.endpoint).toBe(endpoint);
    await page.goto(`${server.url}#/daemon`);
    await expect(page.getByRole("region", { name: "Daemon connectivity" }).locator(".state-badge")).toHaveText("Reachable");
    const logs = await request.get(`${server.url}/api/daemon/logs?maxBytes=4096`);
    expect(logs.ok(), "kukeond must run in its kuke-system cell so its log file exists").toBeTruthy();
    const chunk = await logs.json();
    expect(chunk.source).toMatch(/kukeond\/kukeond\/log$/);
    expect(chunk.next).toBeGreaterThanOrEqual(chunk.start);
    expect(chunk.data.length).toBeLessThanOrEqual(4096);
    const forward = await (await request.get(`${server.url}/api/daemon/logs?offset=${chunk.next}`)).json();
    expect(forward.start).toBe(chunk.next);
    await page.goto(`${server.url}#/daemon/logs`);
    if (chunk.data) await expect(page.getByLabel("kukeond log output")).toBeVisible();
    else await expect(page.getByRole("heading", { name: "No log output yet" })).toBeVisible();
  } finally { await server.stop(); }
  // Never stop the operator's daemon to induce the failure case. The session check cannot reach
  // kukeond, so the login screen shows the daemon notice with a Retry.
  const unavailable = await startServer();
  try {
    await page.goto(unavailable.url);
    const login = page.getByRole("form", { name: "Sign in" });
    await expect(login.getByRole("alert")).toContainText("Daemon unavailable");
    await login.getByRole("button", { name: "Retry" }).click();
    await expect(login.getByRole("alert")).toContainText("Daemon unavailable");
  } finally { await unavailable.stop(); await request.dispose(); }
});
