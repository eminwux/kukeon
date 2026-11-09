import { test, expect } from "@playwright/test";
import { startServer } from "./server.mjs";

// Explicit smoke command must fail, not silently skip, without its prerequisite.
const endpoint = process.env.KUKE_WEB_SMOKE_HOST;
if (!endpoint) throw new Error("Set KUKE_WEB_SMOKE_HOST to a running kukeond Unix endpoint (see docs/web-ui-development.md).");

test("packaged browser reads real daemon documents and unavailable socket", async ({ page, request }) => {
  const server = await startServer(endpoint);
  try {
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
  } finally { await server.stop(); }
  // Never stop the operator's daemon to induce the failure case.
  const unavailable = await startServer();
  try {
    await page.goto(unavailable.url);
    await expect(page.getByRole("alert")).toContainText("Daemon unavailable");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(page.getByRole("alert")).toContainText("Daemon unavailable");
  } finally { await unavailable.stop(); }
});
