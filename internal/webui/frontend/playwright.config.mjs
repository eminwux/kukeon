import { defineConfig } from "@playwright/test";
// Every test starts its own server on an ephemeral loopback port and mocks the
// API per page, so tests do not share state and can run side by side. CI runs
// four workers (one per runner CPU); a local run keeps one unless CI is set.
// fullyParallel spreads the tests of one file over the workers: without it
// Playwright gives each file to a single worker, and browser.spec.mjs holds
// most of the suite.
export default defineConfig({
  testDir: "./tests",
  timeout: 30000,
  fullyParallel: true,
  workers: process.env.CI ? 4 : 1,
  use: { browserName: "chromium", trace: "retain-on-failure" },
});
