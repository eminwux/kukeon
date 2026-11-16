import assert from "node:assert/strict";
import test from "node:test";
import { catalogAPI, referenceScope } from "./catalog-url.mjs";

function parse(url) {
  const parsed = new URL(url, "http://kukeon.invalid");
  return { path: parsed.pathname, params: Object.fromEntries(parsed.searchParams) };
}

const refs = {
  realm: { name: "claude", realm: "default" },
  space: { name: "claude", realm: "default", space: "team" },
  stack: { name: "claude", realm: "default", space: "team", stack: "agents" },
};

for (const kind of ["blueprints", "configs"]) {
  test(`${kind} URLs keep the name out of every scope slot`, () => {
    assert.deepEqual(parse(catalogAPI(kind, referenceScope(refs.realm))), { path: `/api/catalog/${kind}/claude`, params: { realm: "default" } });
    assert.deepEqual(parse(catalogAPI(kind, referenceScope(refs.space))), { path: `/api/catalog/${kind}/claude`, params: { realm: "default", space: "team" } });
    assert.deepEqual(parse(catalogAPI(kind, referenceScope(refs.stack))), { path: `/api/catalog/${kind}/claude`, params: { realm: "default", space: "team", stack: "agents" } });
    for (const ref of Object.values(refs)) {
      assert.ok(!Object.values(parse(catalogAPI(kind, referenceScope(ref))).params).includes(ref.name));
    }
  });
}

test("reference scope ignores non-string and empty coordinates", () => {
  assert.deepEqual(referenceScope({ name: "claude", realm: "default", space: "", stack: undefined }), ["default", "claude"]);
  assert.deepEqual(referenceScope(undefined), []);
});

test("names are path-encoded", () => {
  assert.equal(catalogAPI("configs", ["default", "a b"]), "/api/catalog/configs/a%20b?realm=default");
});
