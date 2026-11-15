#!/usr/bin/env bash
# Transient plans are derived from Harness; builds never rewrite release catalogs.
set -euo pipefail
cd "$(dirname "$0")/.."
export REGISTRY="${REGISTRY-ghcr.io/eminwux/kuketeams}"
export TAG="${TAG-latest}"
native_arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
export PLATFORMS="${PLATFORMS:-linux/$native_arch}"
mode="${MODE:-load}"
args=()
case "$mode" in
  check) ;;
  load)
    if [[ "$PLATFORMS" == *,* ]]; then
      echo 'MODE=load needs one PLATFORMS value; use MODE=check for multi-arch validation.' >&2
      exit 2
    fi
    args+=(--set '*.output=type=docker') ;;
  push) args+=(--set '*.output=type=registry') ;;
  *) echo 'MODE must be check, load, or push' >&2; exit 2 ;;
esac
if [[ "${1:-}" == --print ]]; then
  args+=(--print)
  shift
fi
plan=$(mktemp --suffix=.json)
trap 'rm -f "$plan"' EXIT
"${GO:-go}" run ./harnesses/cmd/images plan --harness "${HARNESS_PATH:-}" "$@" > "$plan"
# A subset belonging to a sibling harness is skipped during aggregate builds.
if python3 -c 'import json,sys; sys.exit(bool(json.load(open(sys.argv[1]))["group"]["default"]["targets"]))' "$plan"; then
  if [[ "${ALLOW_EMPTY_SUBSET:-0}" == 1 ]]; then exit 0; fi
  echo "No selected images belong to harness path '${HARNESS_PATH:-all}'; check IMAGES." >&2
  exit 2
fi
docker buildx bake --file "$plan" "${args[@]}"
