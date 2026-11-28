import { defineConfig } from "vite";

// The published build of @kukeon/console: an ES module with its lazy pages as chunks, plus one
// stylesheet (the console's and xterm's) the host imports as @kukeon/console/style.css. React, xterm and
// the sibling @kukeon packages stay imports, resolved once by the host's bundler. tsc then emits the
// declarations into the same dist/ (tsconfig.build.json).
const external = (id: string) =>
  /^react(-dom)?(\/|$)/.test(id) || id === "@xterm/xterm" || id === "@xterm/addon-fit" || id.startsWith("@kukeon/");

export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: true,
    lib: { entry: "src/index.ts", formats: ["es"], fileName: "index", cssFileName: "style" },
    rollupOptions: { external },
  },
});
