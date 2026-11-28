import { test, expect } from "@playwright/test";
import { build, defaultClientConditions } from "vite";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { extname, join, resolve } from "node:path";

// The transport seam (#1752): the console mounted with apiBase "/x/api" sends every API request and the
// attach WebSocket under that prefix, and nothing to the default /api. The page is a test-only entry
// (fixtures/mount-prefix.html) built from the sources with Vite and served from a loopback server that
// answers the API under /x/api and 404s anything else that is not a built file.
const contentTypes = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".woff2": "font/woff2" };
let outDir;

test.beforeAll(async () => {
  test.setTimeout(180000);
  outDir = await mkdtemp(join(tmpdir(), "kuke-mount-"));
  await build({ root: resolve("."), configFile: false, logLevel: "silent", resolve: { conditions: ["@kukeon/source", ...defaultClientConditions] }, build: { outDir, emptyOutDir: true, rollupOptions: { input: resolve("tests/fixtures/mount-prefix.html") } } });
});
test.afterAll(async () => { if (outDir) await rm(outDir, { recursive: true, force: true }); });

const kinds = ["Realm", "Space", "Stack", "Cell"];
const names = ["dev", "shared", "services", "api"];
const doc = scope => ({ apiVersion: "v1beta1", kind: kinds[scope.length - 1], metadata: { name: scope.at(-1) }, spec: {}, status: { state: "Ready" } });
// The answer to one API path (relative to the prefix): one realm/space/stack/cell chain whose cell has
// one attachable container, a signed-in session, and a 404 for everything else.
function answer(path) {
  if (path === "/session") return { json: { user: "root" } };
  const parts = path.split("?")[0].split("/").filter(Boolean);
  if (parts[0] === "realms") {
    if (parts.length === 9 && parts[8] === "containers") return { json: [{ id: "shell", attachable: true }] };
    const depth = Math.floor(parts.length / 2);
    if (parts.length % 2 === 1 && depth < 4) return { json: [doc(names.slice(0, depth + 1))] };
    if (parts.length % 2 === 0 && depth <= 4) return { json: doc(names.slice(0, depth)) };
  }
  return { status: 404, json: { error: { code: "not_found", message: `no mock for ${path}` } } };
}

test("a console mounted with apiBase /x/api requests and attaches under that prefix", async ({ page }) => {
  const apiPaths = [];
  const stray = [];
  const server = createServer(async (request, response) => {
    const { pathname, search } = new URL(request.url, "http://localhost");
    if (pathname.startsWith("/x/api/")) {
      const path = pathname.slice("/x/api".length) + search;
      apiPaths.push(path);
      const { status = 200, json } = answer(path);
      response.writeHead(status, { "Content-Type": "application/json" }).end(JSON.stringify(json));
      return;
    }
    try {
      const body = await readFile(join(outDir, pathname));
      response.writeHead(200, { "Content-Type": contentTypes[extname(pathname)] ?? "application/octet-stream" }).end(body);
    } catch {
      stray.push(pathname);
      response.writeHead(404).end();
    }
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const origin = `127.0.0.1:${server.address().port}`;
  try {
    const sockets = [];
    await page.routeWebSocket("**/attach", socket => { sockets.push(socket.url()); });
    await page.goto(`http://${origin}/tests/fixtures/mount-prefix.html#/workspace/dev/shared/services/api`);
    await expect.poll(() => sockets[0] ?? "").toBe(`ws://${origin}/x/api/realms/dev/spaces/shared/stacks/services/cells/api/containers/shell/attach`);
    expect(apiPaths).toContain("/session");
    expect(apiPaths).toContain("/realms");
    // Every request outside the built assets went to /x/api; none reached the default /api.
    expect(stray).toEqual([]);
  } finally {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  }
});
