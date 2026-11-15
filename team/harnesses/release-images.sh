#!/usr/bin/env bash
# Manually generate -> commit/merge -> tag. Tag CI only runs --check.
set -euo pipefail
cd "$(dirname "$0")/.."
tag="${1:-}"
if [ "$#" -gt 2 ] || [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9][a-zA-Z0-9.-]*)?$ ]] || [[ "${2:-}" != "" && "${2:-}" != --check ]]; then
  echo "usage: $0 <tag> [--check]" >&2
  exit 2
fi
args=()
if [[ "${2:-}" == --check ]]; then args+=(--check); fi
"${GO:-go}" run ./harnesses/cmd/images catalog --tag "$tag" "${args[@]}"
