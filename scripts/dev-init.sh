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

# Nested-kukeon detection (issue #547). The canonical probe is the
# `/.kukeon/bin/kuketty` bind the daemon stages into every attachable cell
# (`internal/ctr.AttachableBinaryPath`). When present, this script is
# running inside a `kukeon-dev-root` cell whose parent host has bind-
# mounted `/run/kukeon/tty/<container>/` into us at `/run/kukeon/tty/`. A
# nested `kuke init` against the default `/run/kukeon/kukeond.sock` would
# share the `/run/kukeon` parent dir with that bind and break the parent's
# `kuke attach <this-cell>` plumbing on script exit (#545 repro).
#
# The fix is to publish the nested daemon's socket under a per-nest
# directory (`/run/kukeon-dev/`) so its lifecycle never touches the
# parent's bind. The host-run path (probe absent) is unchanged.
NESTED_PROBE="/.kukeon/bin/kuketty"
PARENT_ATTACH_SOCKET="/run/kukeon/tty/socket"
PARENT_ATTACH_METADATA="/.kukeon/kuketty/metadata.json"
NESTED_SOCKET_DIR="/run/kukeon-dev"
NESTED_SOCKET_PATH="${NESTED_SOCKET_DIR}/kukeond.sock"
NESTED_HOST_URI="unix://${NESTED_SOCKET_PATH}"
NESTED_SERVER_CONFIG=""
INIT_EXTRA_ARGS=()

if [ -e "${NESTED_PROBE}" ]; then
    step "Nested kukeon detected (${NESTED_PROBE} present)"
    echo "Routing the nested kukeond through ${NESTED_SOCKET_PATH} (per-nest socket)."
    echo "Parent host's /run/kukeon/tty bind is left untouched; verified on exit."

    # Snapshot the parent's per-container attach plumbing so the EXIT trap
    # can fail loud if anything we did clobbered it. The socket file and
    # metadata file are the parent's, populated by the parent's daemon at
    # the moment this cell was created — they're the things the parent's
    # `kuke attach <this-cell>` depends on.
    if [ ! -S "${PARENT_ATTACH_SOCKET}" ]; then
        printf 'parent attach socket missing or not a socket at %s — refusing to run\n' \
            "${PARENT_ATTACH_SOCKET}" >&2
        exit 1
    fi
    if [ ! -r "${PARENT_ATTACH_METADATA}" ]; then
        printf 'parent attach metadata missing or unreadable at %s — refusing to run\n' \
            "${PARENT_ATTACH_METADATA}" >&2
        exit 1
    fi
    PARENT_ATTACH_METADATA_PRE_SHA="$(sha256sum "${PARENT_ATTACH_METADATA}" | awk '{print $1}')"

    # Make the per-nest socket dir up front so its parent exists before
    # `kuke init`'s `applyKukeonOwnership` chowns it (`ensureSocketDir` in
    # the controller bootstrap also creates it, so this is belt-and-braces
    # — it lets us point `KUKEON_HOST` at the right place from line one).
    sudo mkdir -p "${NESTED_SOCKET_DIR}"

    # KUKEON_HOST is bound to viper for every `kuke` subcommand
    # (`KUKEON_ROOT_HOST.BindEnv` in cmd/kuke/kuke.go's loadConfig), so
    # exporting it here covers every client invocation below — `kuke
    # daemon reset`, `kuke get realms`, `kuke create realm`, `kuke apply`,
    # `kuke purge`, etc. — without per-call --host overrides.
    export KUKEON_HOST="${NESTED_HOST_URI}"

    # `kuke init` does not env-bind KUKEOND_SOCKET (the daemon-side
    # variable), so the supported channel for steering the daemon socket
    # at init time is `--server-configuration`. We write a minimal
    # ServerConfiguration document with `spec.socket` only — every other
    # field falls back to the daemon's hardcoded default, matching the
    # default-host invocation everywhere else.
    NESTED_SERVER_CONFIG="$(mktemp -t kukeon-dev-init-nested-server-config.XXXXXX.yaml)"
    cat > "${NESTED_SERVER_CONFIG}" <<EOF
apiVersion: v1beta1
kind: ServerConfiguration
metadata:
  name: kukeon-dev-init-nested
spec:
  socket: ${NESTED_SOCKET_PATH}
EOF
    INIT_EXTRA_ARGS+=("--server-configuration" "${NESTED_SERVER_CONFIG}")
fi

# Post-flight: re-stat the parent's attach socket and metadata. Runs on
# every exit path so a botched re-bootstrap surfaces here rather than at
# the operator's next `kuke attach <this-cell>` on the parent host. When
# the script was not nested the snapshot is empty and this is a no-op.
verify_parent_attach_intact() {
    if [ -z "${PARENT_ATTACH_METADATA_PRE_SHA:-}" ]; then
        return 0
    fi
    local ok=1
    if [ ! -S "${PARENT_ATTACH_SOCKET}" ]; then
        printf 'POST-FLIGHT: parent attach socket missing or not a socket at %s\n' \
            "${PARENT_ATTACH_SOCKET}" >&2
        ok=0
    fi
    if [ ! -r "${PARENT_ATTACH_METADATA}" ]; then
        printf 'POST-FLIGHT: parent attach metadata missing or unreadable at %s\n' \
            "${PARENT_ATTACH_METADATA}" >&2
        ok=0
    else
        local now
        now="$(sha256sum "${PARENT_ATTACH_METADATA}" | awk '{print $1}')"
        if [ "${now}" != "${PARENT_ATTACH_METADATA_PRE_SHA}" ]; then
            printf 'POST-FLIGHT: parent attach metadata at %s changed (%s -> %s)\n' \
                "${PARENT_ATTACH_METADATA}" "${PARENT_ATTACH_METADATA_PRE_SHA}" "${now}" >&2
            ok=0
        fi
    fi
    if [ "${ok}" -eq 0 ]; then
        printf 'POST-FLIGHT: nested run disturbed the parent host attach plumbing for this cell\n' >&2
        return 1
    fi
    echo "post-flight: parent attach plumbing at ${PARENT_ATTACH_SOCKET} still intact"
    return 0
}

# Register the post-flight check BEFORE any state-mutating step so an
# early failure (e.g. during `make kuke`, `docker build`, or the first
# `kuke init`) still surfaces a disturbance of the parent's attach
# plumbing. The smoke section below replaces this trap with
# `cleanup_attach_smoke`, whose last step is the same
# `verify_parent_attach_intact` invocation — so the post-flight contract
# holds at every exit point in the script.
trap 'verify_parent_attach_intact || exit 1' EXIT

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
    sudo --preserve-env=KUKEON_HOST ./kuke init --kukeond-image "${KUKEOND_IMAGE_REF}" \
        "${INIT_EXTRA_ARGS[@]}" \
        || echo "first-pass init returned non-zero (expected before image is staged); continuing"
fi

if [ -d "${KUKEOND_CELL_DIR}" ]; then
    step "Reset prior kukeond cell"
    sudo --preserve-env=KUKEON_HOST ./kuke daemon reset
else
    step "No prior kukeond cell at ${KUKEOND_CELL_DIR}; skipping reset"
fi

step "Load ${LOCAL_TAG} into the kuke-system realm"
sudo --preserve-env=KUKEON_HOST ./kuke image load --from-docker "${LOCAL_TAG}" --realm kuke-system --no-daemon

step "Run kuke init with --kukeond-image ${KUKEOND_IMAGE_REF}"
sudo --preserve-env=KUKEON_HOST ./kuke init --kukeond-image "${KUKEOND_IMAGE_REF}" \
    "${INIT_EXTRA_ARGS[@]}"

step "Daemon parity check (both must show identical output)"
sudo --preserve-env=KUKEON_HOST ./kuke get realms
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
    sudo --preserve-env=KUKEON_HOST ./kuke purge cell "${ATTACH_SMOKE_CELL}" \
        --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}" --stack "${ATTACH_SMOKE_STACK}" \
        --cascade 2>/dev/null || true
    sudo --preserve-env=KUKEON_HOST ./kuke purge stack "${ATTACH_SMOKE_STACK}" \
        --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}" 2>/dev/null || true
    sudo --preserve-env=KUKEON_HOST ./kuke purge space "${ATTACH_SMOKE_SPACE}" --realm "${ATTACH_SMOKE_REALM}" 2>/dev/null || true
    sudo --preserve-env=KUKEON_HOST ./kuke purge realm "${ATTACH_SMOKE_REALM}" 2>/dev/null || true
}

cleanup_attach_smoke() {
    rm -rf "${ATTACH_SMOKE_TMP}"
    teardown_attach_smoke_state
    if [ -n "${NESTED_SERVER_CONFIG}" ]; then
        rm -f "${NESTED_SERVER_CONFIG}"
    fi
    # Issue #547 AC#4: the parent host's `/run/kukeon/tty` bind must
    # still be live before this script exits. Runs after the nested
    # daemon's smoke artifacts are torn down so we catch any late
    # disturbance, and runs on the error-exit path too (the trap fires
    # regardless of exit status). Returns non-zero to surface the
    # disturbance even when the rest of the script succeeded.
    verify_parent_attach_intact || exit 1
}
trap cleanup_attach_smoke EXIT

# Best-effort teardown of daemon-side leftovers from a prior crashed run
# before claiming the realm names. The mktemp dir for the current run is
# left intact — only the on-disk realm/space/stack/cell state is wiped.
teardown_attach_smoke_state

sudo --preserve-env=KUKEON_HOST ./kuke create realm "${ATTACH_SMOKE_REALM}"
sudo --preserve-env=KUKEON_HOST ./kuke create space "${ATTACH_SMOKE_SPACE}" --realm "${ATTACH_SMOKE_REALM}"
sudo --preserve-env=KUKEON_HOST ./kuke create stack "${ATTACH_SMOKE_STACK}" --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}"

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

sudo --preserve-env=KUKEON_HOST ./kuke apply -f "${ATTACH_SMOKE_TMP}/cell.yaml"

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
    sudo --preserve-env=KUKEON_HOST ./kuke attach "${ATTACH_SMOKE_CELL}" \
        --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}" \
        --stack "${ATTACH_SMOKE_STACK}" --container "${ATTACH_SMOKE_CONTAINER}"
echo "kuke attach exited cleanly (see ${ATTACH_LOG} for the full transcript)"

# Issue #547 AC#5: when the script runs nested, the existing smoke above
# only validates the nested daemon's disposable cell. The contract the AC
# adds is that the parent host's pre-existing attach paths must still be
# usable after the nested smoke completes — exercise that by re-statting
# the parent's per-container attach socket and metadata bind. The EXIT
# trap's `verify_parent_attach_intact` is the final fail-loud guard; this
# is the in-script verification surfaced in the smoke output so a
# regression is visible at the smoke step, not opaquely on exit.
if [ -n "${PARENT_ATTACH_METADATA_PRE_SHA:-}" ]; then
    step "Nested smoke: verify parent host attach plumbing still live"
    if [ ! -S "${PARENT_ATTACH_SOCKET}" ]; then
        printf 'parent attach socket missing at %s after nested smoke\n' \
            "${PARENT_ATTACH_SOCKET}" >&2
        exit 1
    fi
    if ! sudo test -r "${PARENT_ATTACH_METADATA}"; then
        printf 'parent attach metadata unreadable at %s after nested smoke\n' \
            "${PARENT_ATTACH_METADATA}" >&2
        exit 1
    fi
    echo "parent attach socket ${PARENT_ATTACH_SOCKET} still a socket; metadata still readable"
fi
