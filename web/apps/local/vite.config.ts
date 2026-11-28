import { defaultClientConditions, defineConfig } from "vite";

// The console kuke serve embeds: built from the workspace packages' sources (the @kukeon/source export
// condition), not their published dist/, into internal/webui/dist, which internal/webui embeds.
export default defineConfig({
  resolve: { conditions: ["@kukeon/source", ...defaultClientConditions] },
  build: {
    outDir: "../../../internal/webui/dist",
    emptyOutDir: true,
  },
});
