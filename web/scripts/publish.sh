#!/usr/bin/env bash
# Publishes @kukeon/api-client, @kukeon/ui and @kukeon/console to the npm
# registry at one version under one dist-tag (#1753): release.yaml at the
# release version under `latest`, edge.yaml at x.y.z-edge.<sha> under `edge`.
#
#   web/scripts/publish.sh <version> <dist-tag> [--dry-run]
#
# Run from anywhere after `npm ci` in web/. The version is written into each
# package.json (and into the packages' dependencies on one another), the
# packages are built, and each is published in dependency order. A version the
# registry already holds is skipped, so a rerun of the workflow is harmless.
# Authentication is the caller's: setup-node's .npmrc with NODE_AUTH_TOKEN.
set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ] || { [ "$#" -eq 3 ] && [ "$3" != "--dry-run" ]; }; then
	echo "usage: $0 <version> <dist-tag> [--dry-run]" >&2
	exit 2
fi
version="$1"
tag="$2"
dry_run="${3:-}"

cd "$(dirname "$0")/.."
packages=(api-client ui console)

# npm itself rejects a version that is not semver; checking first names the
# culprit before anything is built.
node -e 'const v = process.argv[1]; if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-(0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(\.(0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*)?$/.test(v)) { console.error(`not a semver version: ${v}`); process.exit(1); }' "$version"

VERSION="$version" node --input-type=module -e '
import { readFileSync, writeFileSync } from "node:fs";
const version = process.env.VERSION;
for (const name of ["api-client", "ui", "console"]) {
  const file = `packages/${name}/package.json`;
  const manifest = JSON.parse(readFileSync(file, "utf8"));
  manifest.version = version;
  for (const dep of Object.keys(manifest.dependencies ?? {})) {
    if (dep.startsWith("@kukeon/")) manifest.dependencies[dep] = version;
  }
  writeFileSync(file, JSON.stringify(manifest, null, 2) + "\n");
}
'

npm run build

for name in "${packages[@]}"; do
	if npm view "@kukeon/${name}@${version}" version >/dev/null 2>&1; then
		echo "@kukeon/${name}@${version} is already published; skipping"
		continue
	fi
	npm publish --workspace "packages/${name}" --tag "$tag" ${dry_run:+"$dry_run"}
done
