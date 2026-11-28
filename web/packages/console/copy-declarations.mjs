// The hand-written declarations of the plain-JavaScript modules (foo.mjs + foo.d.mts, foo.js + foo.d.ts)
// are inputs to tsc, not outputs, so the declarations tsc emits import them from dist/ without tsc
// putting them there. Copy them next to the emitted ones.
import { copyFileSync, readdirSync } from "node:fs";

for (const name of readdirSync("src")) {
  if (/\.d\.m?ts$/.test(name)) copyFileSync(`src/${name}`, `dist/${name}`);
}
