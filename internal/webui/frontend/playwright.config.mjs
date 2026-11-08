import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  timeout: 30000,
  workers: 1,
  use: { browserName: "chromium", trace: "retain-on-failure" },
});
