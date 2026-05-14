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
# On a fresh host (no /opt/kukeon/data/kuke-system) the script first runs
# `kuke init` to create realm metadata so the subsequent `kuke image load
# --realm kuke-system` has a realm to land into; cell provisioning during
# that first-pass init may fail before the image is staged, which is
# expected.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

LOCAL_TAG="kukeon-local:dev"
KUKEOND_IMAGE_REF="docker.io/library/${LOCAL_TAG}"
# The on-disk metadata layout lives under /opt/kukeon/data/ — see
# internal/consts.KukeonMetadataSubdir. Siblings of the data root (e.g.
# /opt/kukeon/bin/kuketty staged by `kuke init`) intentionally fall outside
# the reconcile-loop walker.
METADATA_ROOT="/opt/kukeon/data"
SYSTEM_REALM_DIR="${METADATA_ROOT}/kuke-system"
KUKEOND_CELL_DIR="${SYSTEM_REALM_DIR}/kukeon/kukeon/kukeond"

step() {
    printf '\n==> %s\n' "$*"
}

step "Build kuke (and the kukeond symlink)"
make kuke
ln -sf kuke kukeond

step "Install dev symlinks on PATH (make install-dev)"
make install-dev

# Pre-flight: catch missing host cgroup-v2 controller delegation BEFORE the
# multi-minute docker build runs (issue #324). On a fully-delegated host
# this is silent; on a misconfigured host (e.g. cgroup namespace whose
# parent only delegated a subset) it fails fast with the missing
# controllers named and a copy-pasteable remediation. --probe attempts the
# +<ctrl> write so the EOPNOTSUPP cgroup-namespace trap is distinguished
# from "kernel does not support". Idempotent on a healthy host (write is
# a no-op when the controller is already in subtree_control).
step "Pre-flight: host cgroup controller delegation"
sudo ./kuke doctor cgroups --probe

# Pre-flight: catch a missing standalone containerd BEFORE the multi-minute
# docker build, and BEFORE the first-pass `kuke init` below whose tolerate-
# non-zero (image-not-staged) would otherwise swallow the connection-timeout
# error and surface a misleading downstream "realm not found: kuke-system"
# at image load (issue #344).
step "Pre-flight: containerd reachability"
if [ ! -S /run/containerd/containerd.sock ]; then
    printf 'containerd is not running at /run/containerd/containerd.sock — start it before re-running\n' >&2
    exit 1
fi

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

# Phase 1b smoke (#410): the daemon's metadata-rendering path now emits
# api.TerminalDoc consumed by kuketty's sbsh-backed RPC server. A regression
# in the renderer or the kuketty image bundle would otherwise surface only
# the next time someone ran `kuke attach`, well after the dev-init success
# message had lulled the contributor into a false sense of safety. Drive a
# disposable attachable cell through the daemon, wait for kuketty to bind
# the per-container socket, sanity-check the rendered TerminalDoc, and run
# a PTY-driven `kuke attach` that detaches cleanly via the standard ^]^]
# sequence.
step "kuke attach smoke against a kuketty-wrapped cell"

ATTACH_SMOKE_REALM="dev-init-attach"
ATTACH_SMOKE_SPACE="ds"
ATTACH_SMOKE_STACK="dks"
ATTACH_SMOKE_CELL="cattach"
ATTACH_SMOKE_CONTAINER="work"
ATTACH_SMOKE_BASE="${METADATA_ROOT}/${ATTACH_SMOKE_REALM}/${ATTACH_SMOKE_SPACE}/${ATTACH_SMOKE_STACK}/${ATTACH_SMOKE_CELL}/${ATTACH_SMOKE_CONTAINER}"
ATTACH_SMOKE_SOCKET="${ATTACH_SMOKE_BASE}/tty/socket"
ATTACH_SMOKE_METADATA="${ATTACH_SMOKE_BASE}/kuketty-metadata.json"
ATTACH_SMOKE_TMP="$(mktemp -d)"

teardown_attach_smoke_state() {
    sudo ./kuke purge cell "${ATTACH_SMOKE_CELL}" \
        --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}" --stack "${ATTACH_SMOKE_STACK}" \
        --cascade 2>/dev/null || true
    sudo ./kuke purge stack "${ATTACH_SMOKE_STACK}" \
        --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}" 2>/dev/null || true
    sudo ./kuke purge space "${ATTACH_SMOKE_SPACE}" --realm "${ATTACH_SMOKE_REALM}" 2>/dev/null || true
    sudo ./kuke purge realm "${ATTACH_SMOKE_REALM}" 2>/dev/null || true
}

cleanup_attach_smoke() {
    rm -rf "${ATTACH_SMOKE_TMP}"
    teardown_attach_smoke_state
}
trap cleanup_attach_smoke EXIT

# Best-effort teardown of daemon-side leftovers from a prior crashed run
# before claiming the realm names. The mktemp dir for the current run is
# left intact — only the on-disk realm/space/stack/cell state is wiped.
teardown_attach_smoke_state

sudo ./kuke create realm "${ATTACH_SMOKE_REALM}"
sudo ./kuke create space "${ATTACH_SMOKE_SPACE}" --realm "${ATTACH_SMOKE_REALM}"
sudo ./kuke create stack "${ATTACH_SMOKE_STACK}" --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}"

cat > "${ATTACH_SMOKE_TMP}/cell.yaml" <<EOF
apiVersion: v1beta1
kind: Cell
metadata:
  name: ${ATTACH_SMOKE_CELL}
spec:
  id: ${ATTACH_SMOKE_CELL}
  realmId: ${ATTACH_SMOKE_REALM}
  spaceId: ${ATTACH_SMOKE_SPACE}
  stackId: ${ATTACH_SMOKE_STACK}
  containers:
    - id: root
      root: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args: ["3600"]
    - id: ${ATTACH_SMOKE_CONTAINER}
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args: ["3600"]
      attachable: true
EOF

sudo ./kuke apply -f "${ATTACH_SMOKE_TMP}/cell.yaml"

# Wait for kuketty to bind the per-container socket. The window covers
# image pull + container start + sbsh server bring-up; a regression that
# never binds the socket trips the timeout instead of an opaque attach
# hang downstream.
for _ in $(seq 1 40); do
    if sudo test -S "${ATTACH_SMOKE_SOCKET}"; then break; fi
    sleep 0.5
done
sudo test -S "${ATTACH_SMOKE_SOCKET}" \
    || { printf 'kuketty socket not bound at %s after 20s\n' "${ATTACH_SMOKE_SOCKET}" >&2; exit 1; }

# Validate the on-disk schema discriminator. A renderer regression
# (e.g. kukeon-side v1alpha1 schema sneaking back in) is caught here
# rather than as an opaque "kind/apiVersion mismatch" in the kuketty log.
sudo grep -q '"apiVersion": "sbsh/v1beta1"' "${ATTACH_SMOKE_METADATA}" \
    || { printf 'rendered metadata at %s missing apiVersion sbsh/v1beta1\n' "${ATTACH_SMOKE_METADATA}" >&2; exit 1; }
sudo grep -q '"kind": "Terminal"' "${ATTACH_SMOKE_METADATA}" \
    || { printf 'rendered metadata at %s missing kind Terminal\n' "${ATTACH_SMOKE_METADATA}" >&2; exit 1; }

# PTY-driven `kuke attach` smoke. hack/attach-smoke allocates a TTY
# (pkg/attach requires one), waits for pkg/attach's raw-mode keyboard
# filter to wire up, sends the Ctrl+] Ctrl+] detach sequence sbsh
# registers, and enforces a 20s overall deadline. A clean exit confirms
# the kuketty server is serving the JSON-RPC + SCM_RIGHTS protocol
# pkg/attach speaks.
ATTACH_LOG="${ATTACH_SMOKE_TMP}/attach.log"
go run ./hack/attach-smoke --log "${ATTACH_LOG}" -- \
    sudo ./kuke attach "${ATTACH_SMOKE_CELL}" \
        --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}" \
        --stack "${ATTACH_SMOKE_STACK}" --container "${ATTACH_SMOKE_CONTAINER}"
echo "kuke attach exited cleanly (see ${ATTACH_LOG} for the full transcript)"
