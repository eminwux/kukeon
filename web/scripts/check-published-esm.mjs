// Loads the built @kukeon/api-client and @kukeon/ui from plain Node, by package name, so their
// published dist/ resolves the way a consumer's runtime sees it: through each package's exports
// without the @kukeon/source condition, with Node ESM's fully specified relative imports. tsc emits
// import specifiers unchanged, so an extensionless "./icons" in src/ breaks here and not in Vite.
//
// @kukeon/console is a Vite library build whose own imports are bundled, and it imports these two
// packages as externals. Plain Node can't load it: @xterm/xterm ships ESM only through the
// bundler-only "module" field, and Node gets its CommonJS "main", which lacks named exports.
const packages = ["@kukeon/api-client", "@kukeon/ui"];

let failed = false;
for (const name of packages) {
  try {
    const mod = await import(name);
    if (Object.keys(mod).length === 0) throw new Error("no exports");
    console.log(`${name}: loads from Node (${Object.keys(mod).length} exports)`);
  } catch (err) {
    failed = true;
    console.error(`${name}: built package does not load from Node: ${err.message}`);
  }
}
if (failed) process.exit(1);
