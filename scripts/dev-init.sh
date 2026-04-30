#!/usr/bin/env bash
# Copyright 2025 Emiliano Spinella (eminwux)
# SPDX-License-Identifier: Apache-2.0
#
# dev-init.sh — local kukeond build + load + init loop for contributors.
#
# Composes existing primitives end-to-end so a `kuke init` runs against the
# locally built kukeond image instead of the registry-resolved default. Lifts
# the manual "Local smoke test" sequence from CLAUDE.md into a runnable artifact.
#
# Idempotent: re-running on a healthy host produces a clean re-bootstrap.
# On a fresh host (no /opt/kukeon/kuke-system) the script first runs `kuke
# init` to create realm metadata so the subsequent `kuke image load --realm
# kuke-system` has a realm to land into; cell provisioning during that
# first-pass init may fail before the image is staged, which is expected.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

LOCAL_TAG="kukeon-local:dev"
KUKEOND_IMAGE_REF="docker.io/library/${LOCAL_TAG}"
SYSTEM_REALM_DIR="/opt/kukeon/kuke-system"
KUKEOND_CELL_DIR="${SYSTEM_REALM_DIR}/kukeon/kukeon/kukeond"

step() {
    printf '\n==> %s\n' "$*"
}

step "Build kuke (and the kukeond symlink)"
make kuke
ln -sf kuke kukeond

step "Build local kukeond image (${LOCAL_TAG})"
docker build --build-arg VERSION=v0.0.0-dev -t "${LOCAL_TAG}" .

if [ ! -d "${SYSTEM_REALM_DIR}" ]; then
    step "First-time bootstrap: run kuke init to create realm metadata"
    # The kuke-system realm must exist before `kuke image load --realm
    # kuke-system` will accept the load. On a fresh host that realm is
    # created by kuke init's bootstrap pass; cell provisioning at the end
    # of that pass may fail because the local image is not yet staged in
    # containerd. Tolerate that — the second init below recreates the
    # cell after the image load succeeds.
    sudo ./kuke init --kukeond-image "${KUKEOND_IMAGE_REF}" \
        || echo "first-pass init returned non-zero (expected before image is staged); continuing"
fi

if [ -d "${KUKEOND_CELL_DIR}" ]; then
    step "Reset prior kukeond cell"
    sudo ./kuke daemon reset
else
    step "No prior kukeond cell at ${KUKEOND_CELL_DIR}; skipping reset"
fi

step "Load ${LOCAL_TAG} into the kuke-system realm"
sudo ./kuke image load --from-docker "${LOCAL_TAG}" --realm kuke-system --no-daemon

step "Run kuke init with --kukeond-image ${KUKEOND_IMAGE_REF}"
sudo ./kuke init --kukeond-image "${KUKEOND_IMAGE_REF}"

step "Daemon parity check (both must show identical output)"
sudo ./kuke get realms
sudo ./kuke get realms --no-daemon
