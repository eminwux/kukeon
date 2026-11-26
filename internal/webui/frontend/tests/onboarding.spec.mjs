import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { test, expect, request as playwrightRequest } from "@playwright/test";
import { startServer } from "./server.mjs";

// First-run onboarding against a real daemon (#1694): a host where only `kuke init` ran. The browser
// signs in, follows the Workbench's Get started checklist and ends with a running agent and a running
// majordomo. Run it with `make e2e-onboarding`, which bootstraps the daemon (TestOnboarding_WebUI in
// e2e/) and tears it down; nothing here is mocked.
//
// No vendor login happens: the login step is completed with a fake marker file in the agent template's
// state volume, the way the daemon's own login-identity e2e does. The test checks cell state and UI only.

// Explicit command must fail, not silently skip, without its prerequisites.
const endpoint = process.env.KUKE_WEB_SMOKE_HOST;
if (!endpoint) throw new Error("Set KUKE_WEB_SMOKE_HOST to the kukeond Unix endpoint of a host where only `kuke init` ran (make e2e-onboarding does).");
// The host directory holding the volumes of realm default, space default, stack default.
const volumesDir = process.env.KUKE_ONBOARDING_VOLUMES;
if (!volumesDir) throw new Error("Set KUKE_ONBOARDING_VOLUMES to the volumes directory of the default scope (make e2e-onboarding does).");

// The harness the test launches, and the file whose presence means its state volume holds a login
// (team/connectors/claude/connector.yaml: methods[login].login.marker).
const harness = "claude";
const harnessTitle = "Claude";
const loginMarker = ".credentials.json";
const scope = ["default", "default", "default"];

// A fetch of the kukeon source, an image pull and a cell start each take minutes on a cold runner.
const minutes = n => n * 60 * 1000;
test.setTimeout(minutes(22));

// The web UI login (#1652): KUKE_WEB_SMOKE_PASSWORD, or read back from the web-ui-admin secret.
function loginPassword() {
  if (process.env.KUKE_WEB_SMOKE_PASSWORD) return process.env.KUKE_WEB_SMOKE_PASSWORD;
  const binary = resolve(process.env.KUKE_WEB_BINARY ?? "../../../kuke");
  const out = execFileSync(binary, ["--configuration", "/dev/null", "--host", endpoint, "get", "secret", "web-ui-admin", "--realm", "kuke-system", "-o", "json"], { encoding: "utf8" });
  const password = JSON.parse(out).spec?.basicAuth?.password;
  if (!password) throw new Error("web-ui-admin has no password; the daemon was not bootstrapped with `kuke init`.");
  return password;
}

const content = page => page.locator(".workspace-content");
const card = page => content(page).getByRole("region", { name: "Get started" });
const step = (page, id) => card(page).locator(`[data-step="${id}"]`);
const workbench = page => page.locator(".view-switch").getByRole("link", { name: "Workbench" });
const refresh = page => page.getByRole("button", { name: "Refresh" });

test("a fresh host is set up from the browser: Initialize, git, a login, a first agent and majordomo", async ({ page }) => {
  const password = loginPassword();
  const api = await playwrightRequest.newContext({ extraHTTPHeaders: { Authorization: `Bearer ${password}` } });
  const server = await startServer(endpoint);
  const get = async path => {
    const response = await api.get(`${server.url}${path}`);
    expect(response.ok(), `GET ${path}: HTTP ${response.status()}`).toBeTruthy();
    return response.json();
  };
  const cellState = async name => {
    const response = await api.get(`${server.url}/api/realms/${scope[0]}/spaces/${scope[1]}/stacks/${scope[2]}/cells/${encodeURIComponent(name)}`);
    return response.ok() ? (await response.json()).status?.state ?? "" : `HTTP ${response.status()}`;
  };
  try {
    // Sign in.
    await page.goto(server.url);
    const signIn = page.getByRole("form", { name: "Sign in" });
    await signIn.getByLabel("Password").fill(password);
    await signIn.getByRole("button", { name: "Sign in" }).click();

    // Only `kuke init` ran: no team is stored, and Get started opens on Initialize.
    expect((await get("/api/teams")).teams).toEqual([]);
    await expect(card(page).getByRole("heading", { name: "Get started" })).toBeVisible();
    await expect(step(page, "initialize")).toHaveAccessibleName("Initialize: To do");
    await expect(step(page, "login")).toHaveAccessibleName(/^Log in to .*Claude.*: To do$/);
    await expect(step(page, "agent")).toHaveAccessibleName("Launch your first agent: To do");

    // Initialize: the server fetches the kukeon source and stores the kuke and default teams.
    await step(page, "initialize").getByRole("button", { name: "Initialize", exact: true }).click();
    await expect(step(page, "initialize")).toHaveAccessibleName("Initialize: Done", { timeout: minutes(5) });
    expect((await get("/api/teams")).teams.map(team => team.name).sort()).toEqual(["default", "kuke"]);
    await page.goto(`${server.url}#/teams`);
    const teams = content(page).getByRole("region", { name: "Teams list" });
    await expect(teams.getByRole("link", { name: "kuke", exact: true })).toBeVisible();
    await expect(teams.getByRole("link", { name: "default", exact: true })).toBeVisible();
    await expect(content(page).getByRole("button", { name: "Initialize", exact: true })).toHaveCount(0);

    // A GitHub token that also serves git, with a fake token: both Secrets are listed.
    await workbench(page).click();
    await expect(step(page, "git-identity")).toHaveAccessibleName(/^Set up .*: Recommended$/);
    await page.goto(`${server.url}#/connectors/github/new-identity`);
    const identity = content(page).getByRole("form", { name: "New GitHub identity" });
    await expect(identity.getByLabel(/^Identity name/)).toHaveValue("github-token");
    await identity.getByLabel(/^Token/).fill("ghp_onboardingE2EfakeTokenNotARealOne000");
    await expect(identity.getByRole("checkbox", { name: /Also use this token for git/ })).toBeChecked();
    await identity.getByRole("button", { name: "Create identity" }).click();
    await expect(page).toHaveURL(/#\/connectors\/github(\?.*)?$/);
    const secrets = await get("/api/catalog/secrets");
    const tagged = connector => secrets.filter(doc => doc.spec?.connector === connector && doc.metadata.realm === "default").map(doc => doc.metadata.name);
    expect(tagged("github")).toEqual(["github-token"]);
    expect(tagged("git")).toHaveLength(1);
    // Redaction holds on a real daemon: no Secret document carries its value.
    for (const doc of secrets) expect(doc.spec?.data).toBeUndefined();
    await page.goto(`${server.url}#/connectors/git`);
    await expect(content(page).getByRole("region", { name: "Git identities" })).toContainText(tagged("git")[0]);
    await workbench(page).click();
    await expect(step(page, "git-identity")).toHaveAccessibleName(/^Set up .*: Done$/);

    // The login: the state volume the agent template logs in through is created as a login identity,
    // and the marker a vendor login would write is faked in it. The step turns done.
    await expect(step(page, "login")).toHaveAccessibleName(/: To do$/);
    const logins = await get("/api/connectors/logins");
    const templateLogin = logins.find(item => item.connector === harness && item.usedBy.some(config => config.name === `default-agent-${harness}`));
    expect(templateLogin, `the login identity of default-agent-${harness}`).toBeTruthy();
    // The name docs/site/getting-started.md gives for `kuke connector login`.
    expect(templateLogin.volume.name).toBe(`default-agent-${harness}-state`);
    expect([templateLogin.volume.realm, templateLogin.volume.space, templateLogin.volume.stack]).toEqual(scope);
    const created = await api.post(`${server.url}/api/connectors/${harness}/logins`, { headers: { "Content-Type": "application/json", Origin: server.url }, data: { name: templateLogin.volume.name, scope: { realm: scope[0], space: scope[1], stack: scope[2] } } });
    expect(created.ok(), `create the login identity: HTTP ${created.status()} ${await created.text()}`).toBeTruthy();
    mkdirSync(join(volumesDir, templateLogin.volume.name), { recursive: true });
    writeFileSync(join(volumesDir, templateLogin.volume.name, loginMarker), "{}\n", { mode: 0o600 });
    await refresh(page).click();
    await expect(step(page, "login")).toHaveAccessibleName(/: Done$/, { timeout: minutes(1) });

    // New agent → Claude → "E2E agent": no Log in first, since the template's volume holds a login.
    await step(page, "agent").getByRole("link", { name: "New agent" }).click();
    const agent = page.getByRole("region", { name: "New agent" });
    await agent.getByRole("radio", { name: new RegExp(harnessTitle) }).check();
    await expect(agent.getByRole("button", { name: /^Log in first/ })).toHaveCount(0);
    await agent.getByLabel(/^Name/).fill("E2E agent");
    await agent.getByRole("button", { name: "Create" }).click();
    // The server stores the Config, creates the cell (pulling the harness image) and starts it.
    await expect(page).toHaveURL(/#\/workspace\/default\/default\/default\/[^/]+$/, { timeout: minutes(8) });
    const agentCell = decodeURIComponent(page.url().split("/").at(-1));
    await expect.poll(() => cellState(agentCell), { timeout: minutes(5), message: `agent cell ${agentCell} reaches Ready` }).toBe("Ready");
    // The sidebar lists the agent by its name, and its one instance is "E2E agent #1" on the cell's page.
    const agentRow = page.getByRole("navigation", { name: "Cell navigation" }).getByRole("region", { name: "E2E agent agent" });
    await expect(agentRow.getByRole("link", { name: new RegExp(`^E2E agent: open .*${agentCell} \\(Ready\\)$`) })).toHaveAttribute("aria-current", "page", { timeout: minutes(1) });
    await expect(page.locator(".cell-page-title-row h1")).toHaveText("E2E agent #1");
    // The required steps are done: the card is gone from the Workbench.
    await workbench(page).click();
    await expect(content(page).getByRole("region", { name: "Cell terminals" })).toBeVisible();
    await expect(card(page)).toHaveCount(0);

    // Enable majordomo on the same harness. Its own state volume holds no login, so the panel says so
    // and asks first; Continue anyway starts it.
    await page.getByRole("region", { name: "Majordomo", exact: true }).locator(".majordomo-entry-button").click();
    const panel = page.getByRole("complementary", { name: "Majordomo panel" });
    await panel.getByLabel("Harness").selectOption({ label: `kuke-majordomo-${harness}` });
    await expect(panel.getByRole("status", { name: `${harnessTitle} login` })).toHaveText(`${harnessTitle}: not logged in`);
    await panel.getByRole("button", { name: "Enable majordomo" }).click();
    await panel.getByRole("group", { name: "Log in first" }).getByRole("button", { name: "Continue anyway" }).click();
    await expect.poll(() => cellState("majordomo"), { timeout: minutes(5), message: "the majordomo cell reaches Ready" }).toBe("Ready");
    await expect(page.getByRole("region", { name: "Majordomo", exact: true })).toContainText("Ready", { timeout: minutes(1) });
  } finally {
    await server.stop();
    await api.dispose();
  }
});
