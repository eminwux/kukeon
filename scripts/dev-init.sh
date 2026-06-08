#!/usr/bin/env bash
# Copyright 2025 Emiliano Spinella (eminwux)
# SPDX-License-Identifier: Apache-2.0
#
# dev-init.sh — local kukeond build + init loop for contributors.
#
# Composes existing primitives end-to-end so a `kuke init` runs against the
# locally built kukeond image instead of the registry-resolved default. Lifts
# the manual "Local smoke test" sequence from AGENTS.md into a runnable artifact.
#
# Idempotent: re-running on a healthy host produces a clean re-bootstrap.
# On a fresh host (no /opt/kukeon/data/kuke-system) the script first runs
# `kuke init` to create realm metadata so the subsequent `kuke build --realm
# kuke-system` has a realm to write into; cell provisioning during that
# first-pass init may fail before the image is built, which is expected.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# Local-only kukeond image ref. `kukeon.internal` is the ICANN-reserved,
# non-routable internal host the epic (#1063) adopts for every locally-built
# kukeon image — `kuke build` writes it into the realm's containerd namespace
# before `kuke init` consumes it, and a stray pull attempt against the
# reserved TLD fails fast instead of silently hitting Docker Hub (the failure
# mode `docker.io/library/kukeon-local:dev` left open). The published default
# `KukeondImageRepo = ghcr.io/eminwux/kukeon` (cmd/config/version.go) is
# unchanged — only the local dev-loop ref moves.
KUKEOND_VERSION="v0.0.0-dev"
KUKEOND_IMAGE_REF="kukeon.internal/kukeond:${KUKEOND_VERSION}"
# The on-disk metadata layout lives under /opt/kukeon/data/ — see
# internal/consts.KukeonMetadataSubdir. Siblings of the data root (e.g.
# /opt/kukeon/bin/kuketty staged by `kuke init`) intentionally fall outside
# the reconcile-loop walker.
METADATA_ROOT="/opt/kukeon/data"
SYSTEM_REALM_DIR="${METADATA_ROOT}/kuke-system"
KUKEOND_CELL_DIR="${SYSTEM_REALM_DIR}/kukeon/kukeon/kukeond"

# Profile gate (phase 3, #285). The default (KUKEON_PROFILE unset) leaves
# containerdNamespaceSuffix and cgroupRoot at the package defaults
# (kukeon.io / /kukeon), so a contributor running `make dev-init` to
# install main against the host gets the canonical layout — the historical
# behavior of this script. Set KUKEON_PROFILE=dev to exercise the
# multi-instance path wired by phases 1 (#262, server-side suffix/cgroupRoot)
# and 2 (#284, --server-configuration plumbed through admin commands):
# dev-init then writes ./kukeond-dev.yaml + ./kuke-dev.yaml idempotently
# (guarded by `[ ! -e ]`), routes admin commands through
# --server-configuration, routes client commands through KUKE_CONFIGURATION,
# and the parity tail expects the dev.kukeon.io suffix and /kukeon-dev
# cgroup root. Phase 3 mirrors the same two knobs on ClientConfigurationSpec
# so --no-daemon workload paths read them through KUKE_CONFIGURATION instead
# of the package constants.
KUKEON_PROFILE="${KUKEON_PROFILE:-}"
DEV_PROFILE_SERVER_CONFIG="${REPO_ROOT}/kukeond-dev.yaml"
DEV_PROFILE_CLIENT_CONFIG="${REPO_ROOT}/kuke-dev.yaml"

# Defaults below match the historical script behavior: canonical
# kuke-system.kukeon.io namespace for the staleness check, no
# --server-configuration arg, and the original --preserve-env lists. The
# dev-profile branch (after the EXIT trap is set, so YAML writes are
# guarded too) overrides these.
KUKEOND_NS="kuke-system.kukeon.io"
SERVER_CONFIG_FLAGS=()
PRESERVE_ENV_WORKLOAD="KUKEON_HOST"
PRESERVE_ENV_ADMIN="KUKEON_HOST,KUKEOND_SOCKET"

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

    # KUKEOND_SOCKET steers the daemon-side socket path read by `kuke
    # init` (when seeding the kukeond cell's serve args) and `kuke daemon
    # reset` (when resolving which kukeond.{sock,pid} to clean up). Both
    # paths read it through viper via `KUKEOND_SOCKET.BindEnv` in
    # cmd/kuke/kuke.go's loadConfig, so exporting it here is sufficient.
    export KUKEOND_SOCKET="${NESTED_SOCKET_PATH}"
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
# early failure (e.g. during `make kuke`, `kuke build`, or the first
# `kuke init`) still surfaces a disturbance of the parent's attach
# plumbing. The smoke section below replaces this trap with
# `cleanup_attach_smoke`, whose last step is the same
# `verify_parent_attach_intact` invocation — so the post-flight contract
# holds at every exit point in the script.
trap 'verify_parent_attach_intact || exit 1' EXIT

if [ "${KUKEON_PROFILE}" = "dev" ]; then
    step "KUKEON_PROFILE=dev: enabling dev-profile (suffix dev.kukeon.io, cgroup /kukeon-dev, pod CIDR 10.89.0.0/16)"

    # Write the dev-profile config files idempotently before any kuke
    # invocation. `-e` instead of `-f` so a broken symlink to a missing
    # operator-curated config is detected; a regular file matches too. The
    # server-side config is consumed by `kuke init --server-configuration`
    # and `kuke daemon reset --server-configuration`; the client-side
    # config is consumed by every `kuke` subcommand below via the exported
    # KUKE_CONFIGURATION (the env name viper binds to `kuke/configuration`
    # — cmd/config/env.go's KUKE_CONFIGURATION).
    if [ ! -e "${DEV_PROFILE_SERVER_CONFIG}" ]; then
        step "Write dev-profile server config to ${DEV_PROFILE_SERVER_CONFIG}"
        cat > "${DEV_PROFILE_SERVER_CONFIG}" <<'EOF'
# kukeond ServerConfiguration — dev profile (#285 phase 3).
# Switches containerdNamespaceSuffix, cgroupRoot, and podSubnetCIDR off the
# defaults so a parallel or nested kukeon instance can coexist with the
# canonical kukeon.io / /kukeon / 10.88.0.0/16 tree. socket / runPath are left
# at defaults — dev-init runs the only kukeon instance on the host, so disjoint
# storage is unnecessary; the multi-instance two-host-smoke is exercised by the
# issue's manual test plan, not this script.
#
# podSubnetCIDR is the nested-mode egress fix (#1079): running `make dev-init`
# inside a kukeon-dev-root cell, the nested allocator must not reuse the parent
# host's 10.88.0.0/16 + .1 gateway — that .1 is the cell's own default gateway,
# and claiming it inside the cell shadows the gateway and blackholes egress.
apiVersion: v1beta1
kind: ServerConfiguration
metadata:
  name: dev
spec:
  containerdNamespaceSuffix: dev.kukeon.io
  cgroupRoot: /kukeon-dev
  podSubnetCIDR: 10.89.0.0/16
EOF
    fi

    if [ ! -e "${DEV_PROFILE_CLIENT_CONFIG}" ]; then
        step "Write dev-profile client config to ${DEV_PROFILE_CLIENT_CONFIG}"
        cat > "${DEV_PROFILE_CLIENT_CONFIG}" <<'EOF'
# kuke ClientConfiguration — dev profile (#285 phase 3).
# Read by every `kuke` invocation below via KUKE_CONFIGURATION. The
# --no-daemon parity check below depends on this file to address the dev
# instance's containerd namespaces (dev.kukeon.io), cgroup tree (/kukeon-dev),
# and pod subnet (10.89.0.0/16) without a per-command
# --containerd-namespace-suffix / --cgroup-root / --pod-subnet-cidr flag.
# podSubnetCIDR mirrors the server-side nested-mode egress fix (#1079) so
# in-process `--no-daemon` space creation never lands on the parent's subnet.
apiVersion: v1beta1
kind: ClientConfiguration
metadata:
  name: dev
spec:
  containerdNamespaceSuffix: dev.kukeon.io
  cgroupRoot: /kukeon-dev
  podSubnetCIDR: 10.89.0.0/16
EOF
    fi

    # KUKE_CONFIGURATION is bound to viper for every `kuke` subcommand
    # (KUKE_CONFIGURATION.BindEnv in cmd/kuke/kuke.go's loadConfig), so
    # exporting it here routes every client invocation below through the
    # dev-profile client config. sudo invocations must add KUKE_CONFIGURATION
    # to --preserve-env=... or the value is stripped at the sudo boundary.
    export KUKE_CONFIGURATION="${DEV_PROFILE_CLIENT_CONFIG}"

    # Override the defaults set near the top so every sudo call below
    # picks up the dev-profile suffix, cgroup root, and the server-config
    # flag for admin commands.
    KUKEOND_NS="kuke-system.dev.kukeon.io"
    SERVER_CONFIG_FLAGS=(--server-configuration "${DEV_PROFILE_SERVER_CONFIG}")
    PRESERVE_ENV_WORKLOAD="KUKEON_HOST,KUKE_CONFIGURATION"
    PRESERVE_ENV_ADMIN="KUKEON_HOST,KUKEOND_SOCKET,KUKE_CONFIGURATION"
fi

step "Build kuke, kukepause, kukebuild (and the kukeond symlink)"
make kuke kukepause kukebuild
ln -sf kuke kukeond

step "Install dev symlinks on PATH (make install-dev)"
# install-dev symlinks kuke/kukeond and — because `make kuke kukepause kukebuild`
# above has built them — kukepause and kukebuild into INSTALL_PREFIX
# (/usr/local/bin by default). The `kuke build` step below runs as
# `sudo ./kuke build`, a thin shim that resolves `kukebuild` via PATH and exec's
# it; the `kuke init` step resolves `kukepause` the same way to pre-stage it
# under <RunPath>/bin before any root container is created (issue #931). Both
# must sit on root's secure_path; routing them through install-dev keeps a
# single PATH-placement mechanism.
make install-dev

# Pre-flight: catch missing host cgroup-v2 controller delegation BEFORE the
# multi-minute kuke build runs (issue #324). On a fully-delegated host
# this is silent; on a misconfigured host (e.g. cgroup namespace whose
# parent only delegated a subset) it fails fast with the missing
# controllers named and a copy-pasteable remediation. --probe attempts the
# +<ctrl> write so the EOPNOTSUPP cgroup-namespace trap is distinguished
# from "kernel does not support". Idempotent on a healthy host (write is
# a no-op when the controller is already in subtree_control).
step "Pre-flight: host cgroup controller delegation"
sudo ./kuke doctor cgroups --probe

# Pre-flight: catch a missing standalone containerd BEFORE the multi-minute
# kuke build (kukebuild dials the same /run/containerd/containerd.sock), and
# BEFORE the first-pass `kuke init` below whose tolerate-non-zero (image-not-
# staged) would otherwise swallow the connection-timeout error and surface a
# misleading downstream "realm not found: kuke-system" at build time
# (issue #344).
step "Pre-flight: containerd reachability"
if [ ! -S /run/containerd/containerd.sock ]; then
    printf 'containerd is not running at /run/containerd/containerd.sock — start it before re-running\n' >&2
    exit 1
fi

if [ ! -d "${SYSTEM_REALM_DIR}" ]; then
    step "First-time bootstrap: run kuke init to create realm metadata"
    # The kuke-system realm must exist before `kuke build --realm
    # kuke-system` will write the image into its containerd namespace. On a
    # fresh host that realm is created by kuke init's bootstrap pass; cell
    # provisioning at the end of that pass may fail because the local image
    # is not yet built into containerd. Tolerate that — the second init
    # below recreates the cell after the build succeeds.
    sudo --preserve-env="${PRESERVE_ENV_ADMIN}" ./kuke init \
        --kukeond-image "${KUKEOND_IMAGE_REF}" \
        "${SERVER_CONFIG_FLAGS[@]}" \
        || echo "first-pass init returned non-zero (expected before image is staged); continuing"
fi

# sudo the directory test: after the post-init permission fix sets
# /opt/kukeon to root:kukeon 0o2750, an invoking user not in the
# kukeon group cannot traverse the tree and the unprivileged `[ -d ]`
# test returns false even when the cell dir exists — silently skipping
# reset on every re-run (issue #915 defect 1). Every other access to
# KUKEOND_CELL_DIR in this script is already sudo'd; the gate must be
# too. The matching `[ ! -d "${SYSTEM_REALM_DIR}" ]` probe at line 272
# is the same anti-pattern but doesn't bite today — its first-pass init
# is idempotent on a pre-bootstrapped host.
if sudo test -d "${KUKEOND_CELL_DIR}"; then
    step "Reset prior kukeond cell"
    sudo --preserve-env="${PRESERVE_ENV_ADMIN}" ./kuke daemon reset \
        "${SERVER_CONFIG_FLAGS[@]}"
else
    step "No prior kukeond cell at ${KUKEOND_CELL_DIR}; skipping reset"
fi

step "Build ${KUKEOND_IMAGE_REF} into the kuke-system realm"
# `kuke build` shells out to `kukebuild`, which embeds BuildKit and writes the
# image straight into the kuke-system realm's containerd namespace — no docker
# daemon, no docker-private containerd, no `--from-docker` loader hop. The -t
# tag is fully qualified (`kukeon.internal/kukeond:<version>`), so kukebuild's
# normalizeImageName preserves it verbatim — no docker.io/library/ rewrite —
# matching the exact ref `kuke init --kukeond-image` resolves below.
sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke build --build-arg VERSION="${KUKEOND_VERSION}" \
    -t "${KUKEOND_IMAGE_REF}" \
    --realm kuke-system .

step "Run kuke init with --kukeond-image ${KUKEOND_IMAGE_REF}"
sudo --preserve-env="${PRESERVE_ENV_ADMIN}" ./kuke init \
    --kukeond-image "${KUKEOND_IMAGE_REF}" \
    "${SERVER_CONFIG_FLAGS[@]}"

# Fail-loud when the running kukeond container is anchored on a stale
# image (issue #857). The bug captured there is that `kuke daemon reset`
# + `kuke init --kukeond-image` does not refresh the kukeond cell when
# the image tag is reused (init's bootstrapCell takes the existing-cell
# path on a leftover record and never compares the desired image
# against the running container). The user-visible failure surfaces
# downstream as an opaque pre-#641 schema mismatch in the attach smoke;
# this check turns that into a fail-loud at the place the divergence
# was introduced.
#
# Compare the *snapshot chain digest*, not the image ref name: a
# container's `.Image` field is the ref string set at create time
# (e.g. `kukeon.internal/kukeond:v0.0.0-dev`) and is unchanged when
# `kuke build` overwrites the same tag with a new manifest. The
# canonical drift signal is the chain digest stored on the container's
# snapshot at unpack time, derived from the image config blob's
# `containerd.io/gc.ref.snapshot.overlayfs` label. The two should be
# byte-identical on a healthy `make dev-init`; a mismatch means the
# running container is anchored on an older image than the one we just
# built.
#
# Stay grep/sed-only — scripts in this repo intentionally avoid a jq
# dependency (see scripts/install.sh's "no jq dependency" guarantee).
step "Verify running kukeond container is anchored on the just-built image"
# KUKEOND_NS is set near the top of the script: the default profile uses
# the canonical `kuke-system.kukeon.io`; KUKEON_PROFILE=dev overrides it
# to `kuke-system.dev.kukeon.io` so the dev profile's suffix flows through
# the snapshots/images/content ctr probes below without per-call edits.
KUKEOND_CTR_ID="kukeon_kukeon_kukeond_kukeond"

ctr_chain="$(sudo ctr -n "${KUKEOND_NS}" snapshots info "${KUKEOND_CTR_ID}" 2>/dev/null \
    | grep -oE '"Parent":[[:space:]]*"sha256:[a-f0-9]+"' \
    | sed -E 's/.*"(sha256:[a-f0-9]+)"/\1/')"
if [ -z "${ctr_chain}" ]; then
    printf 'unable to resolve snapshot chain for running kukeond container %s/%s\n' \
        "${KUKEOND_NS}" "${KUKEOND_CTR_ID}" >&2
    exit 1
fi

manifest_digest="$(sudo ctr -n "${KUKEOND_NS}" images ls 2>/dev/null \
    | awk -v r="${KUKEOND_IMAGE_REF}" 'NR>1 && $1==r {print $3; exit}')"
if [ -z "${manifest_digest}" ]; then
    printf 'image %s not found in containerd namespace %s after kuke build\n' \
        "${KUKEOND_IMAGE_REF}" "${KUKEOND_NS}" >&2
    exit 1
fi

config_digest="$(sudo ctr -n "${KUKEOND_NS}" content get "${manifest_digest}" 2>/dev/null \
    | grep -oE '"digest":[[:space:]]*"sha256:[a-f0-9]+"' \
    | head -1 \
    | sed -E 's/.*"(sha256:[a-f0-9]+)"/\1/')"
if [ -z "${config_digest}" ]; then
    printf 'unable to resolve config digest from manifest %s\n' "${manifest_digest}" >&2
    exit 1
fi

img_chain="$(sudo ctr -n "${KUKEOND_NS}" content ls 2>/dev/null \
    | awk -v d="${config_digest}" '$1==d' \
    | grep -oE 'containerd\.io/gc\.ref\.snapshot\.overlayfs=sha256:[a-f0-9]+' \
    | head -1 \
    | cut -d= -f2)"
if [ -z "${img_chain}" ]; then
    printf 'unable to resolve image chain digest from config blob %s\n' "${config_digest}" >&2
    exit 1
fi

if [ "${ctr_chain}" != "${img_chain}" ]; then
    printf 'running kukeond container is anchored on a stale image (#857)\n' >&2
    printf '  running container snapshot chain: %s\n' "${ctr_chain}" >&2
    printf '  just-built image chain:           %s\n' "${img_chain}" >&2
    printf '  manifest %s did not propagate to the kukeond cell —\n' "${manifest_digest}" >&2
    printf '  `kuke daemon reset` + `kuke init --kukeond-image` did not refresh the container.\n' >&2
    printf '  Try `sudo ./kuke daemon reset --purge-system` then re-run `make dev-init`.\n' >&2
    exit 1
fi
echo "running kukeond container chain ${ctr_chain} matches just-built ${KUKEOND_IMAGE_REF}"

step "Daemon parity check (both must show identical output)"
# Default profile keeps the historical NAME / STATE / AGE tail (the
# minimal pinned regression guard documented in AGENTS.md "Local smoke
# test"). KUKEON_PROFILE=dev switches to `-o wide` so NAMESPACE surfaces
# (the dev suffix is otherwise invisible at the default-table level —
# epic:get retired NAMESPACE from the default table) and adds a
# cgroupPath surfacing step so /kukeon-dev is visible too. The
# daemon-routed call is gated by KUKEON_HOST + the daemon's server
# config; the --no-daemon call routes through KUKE_CONFIGURATION to read
# the same suffix/cgroupRoot in-process.
if [ "${KUKEON_PROFILE}" = "dev" ]; then
    sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke get realms -o wide
    sudo --preserve-env=KUKE_CONFIGURATION ./kuke get realms -o wide --no-daemon

    # CGROUP no longer surfaces in the default or wide table (epic:get also
    # retired CGROUP); `-o yaml` is the supported way to read cgroupPath.
    # Surface the dev profile's cgroup root here so an operator skimming
    # `make dev-init` output sees /kukeon-dev anchored on the realms.
    step "Daemon parity: cgroupPath surfacing (dev profile expects /kukeon-dev/...)"
    sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke get realms -o yaml \
        | grep -E 'cgroupPath:' || true
else
    sudo --preserve-env=KUKEON_HOST ./kuke get realms
    sudo ./kuke get realms --no-daemon
fi

# Phase 1b smoke (#410): the daemon's metadata-rendering path emits the
# bind-mounted kuketty config consumed by kuketty's sbsh-backed RPC server.
# Since issue #641 that config is a kukeon ContainerDoc (kuketty builds the
# sbsh TerminalSpec from it in-process) rather than a pre-rendered TerminalDoc.
# A regression in the renderer or the kuketty image bundle would otherwise
# surface only the next time someone ran `kuke attach`, well after the
# dev-init success message had lulled the contributor into a false sense of
# safety. Drive a disposable attachable cell through the daemon, wait for
# kuketty to bind the per-container socket, sanity-check the rendered
# ContainerDoc, and run a PTY-driven `kuke attach` that detaches cleanly via
# the standard ^]^] sequence.
step "kuke attach smoke against a kuketty-wrapped cell"

ATTACH_SMOKE_REALM="dev-init-attach"
ATTACH_SMOKE_SPACE="ds"
ATTACH_SMOKE_STACK="dks"
ATTACH_SMOKE_CELL="cattach"
ATTACH_SMOKE_CONTAINER="work"
# Local-only attach-smoke image ref, mirroring KUKEOND_IMAGE_REF above. Built
# from hack/attach-smoke/Dockerfile into the attach realm below so the smoke
# no longer pins the single-arch-amd64 external registry.eminwux.com/busybox
# that broke on arm64 hosts (#1158).
ATTACH_SMOKE_IMAGE_REF="kukeon.internal/attach-smoke:${KUKEOND_VERSION}"
ATTACH_SMOKE_BASE="${METADATA_ROOT}/${ATTACH_SMOKE_REALM}/${ATTACH_SMOKE_SPACE}/${ATTACH_SMOKE_STACK}/${ATTACH_SMOKE_CELL}/${ATTACH_SMOKE_CONTAINER}"
ATTACH_SMOKE_SOCKET="${ATTACH_SMOKE_BASE}/tty/socket"
ATTACH_SMOKE_METADATA="${ATTACH_SMOKE_BASE}/kuketty-metadata.json"
# Pin to the same suffix the daemon resolves for the dev-init-attach realm
# (defaults: kukeon.io; KUKEON_PROFILE=dev: dev.kukeon.io). KUKEOND_NS
# encodes the active suffix as `kuke-system.<suffix>`; strip the
# `kuke-system.` prefix to derive the attach-smoke realm's namespace.
ATTACH_SMOKE_NS="${ATTACH_SMOKE_REALM}.${KUKEOND_NS#kuke-system.}"
ATTACH_SMOKE_TMP="$(mktemp -d)"

teardown_attach_smoke_state() {
    sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke purge cell "${ATTACH_SMOKE_CELL}" \
        --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}" --stack "${ATTACH_SMOKE_STACK}" \
        --cascade 2>/dev/null || true
    sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke purge stack "${ATTACH_SMOKE_STACK}" \
        --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}" 2>/dev/null || true
    sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke purge space "${ATTACH_SMOKE_SPACE}" --realm "${ATTACH_SMOKE_REALM}" 2>/dev/null || true
    sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke purge realm "${ATTACH_SMOKE_REALM}" 2>/dev/null || true
    # Backstop the kuke purge chain: `kuke purge realm` does call
    # DeleteNamespace, but issue #919 surfaces a cross-run trap where the
    # `dev-init-attach.kukeon.io` containerd namespace lingers empty after
    # the purge and strands the next run's `kuke apply` image-pull→
    # snapshot-unpack with "parent snapshot ... does not exist". Removing
    # the namespace by hand makes the teardown idempotent across N runs.
    sudo ctr namespaces remove "${ATTACH_SMOKE_NS}" 2>/dev/null || true
    # Since #1158 the attach smoke *is* a `kuke build` target (it builds
    # kukeon.internal/attach-smoke into this realm), so the #904 lane now
    # applies: wipe the per-namespace BuildKit cache at
    # /var/lib/kukebuild/<ns>/ alongside the containerd namespace so the
    # teardown stays idempotent across runs.
    sudo rm -rf "/var/lib/kukebuild/${ATTACH_SMOKE_NS}" 2>/dev/null || true
}

cleanup_attach_smoke() {
    rm -rf "${ATTACH_SMOKE_TMP}"
    teardown_attach_smoke_state
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

sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke create realm "${ATTACH_SMOKE_REALM}"
sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke create space "${ATTACH_SMOKE_SPACE}" --realm "${ATTACH_SMOKE_REALM}"
sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke create stack "${ATTACH_SMOKE_STACK}" --realm "${ATTACH_SMOKE_REALM}" --space "${ATTACH_SMOKE_SPACE}"

# Build the attach-smoke image into the attach realm's containerd namespace,
# mirroring the kukeon.internal/kukeond build above (the kuke-system realm at
# line ~329). This replaces the former external registry.eminwux.com/busybox
# pin, which was single-arch amd64 and broke the smoke on arm64 hosts (#1158).
# docker.io/library/busybox is multi-arch, so `kuke build` resolves the host's
# architecture and the resulting kukeon.internal/attach-smoke image matches the
# build host automatically. Runs after the realm exists (its namespace must be
# present for kukebuild to write into) and before the `kuke apply` below.
step "Build ${ATTACH_SMOKE_IMAGE_REF} into the ${ATTACH_SMOKE_REALM} realm"
sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke build \
    -t "${ATTACH_SMOKE_IMAGE_REF}" \
    --realm "${ATTACH_SMOKE_REALM}" hack/attach-smoke

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
      image: ${ATTACH_SMOKE_IMAGE_REF}
      command: sleep
      args: ["3600"]
    - id: ${ATTACH_SMOKE_CONTAINER}
      image: ${ATTACH_SMOKE_IMAGE_REF}
      command: sleep
      args: ["3600"]
      attachable: true
EOF

sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke apply -f "${ATTACH_SMOKE_TMP}/cell.yaml"

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

# Validate the on-disk schema discriminator. Since issue #641 the daemon
# mounts a kukeon ContainerDoc (apiVersion v1beta1, kind Container) rather
# than a pre-rendered sbsh TerminalDoc — kuketty reads ContainerDoc.Spec and
# builds the TerminalSpec itself. A renderer regression (e.g. an old
# TerminalDoc schema sneaking back in) is caught here rather than as an opaque
# kind/apiVersion mismatch in the kuketty log.
sudo grep -q '"apiVersion": "v1beta1"' "${ATTACH_SMOKE_METADATA}" \
    || { printf 'rendered metadata at %s missing apiVersion v1beta1\n' "${ATTACH_SMOKE_METADATA}" >&2; exit 1; }
sudo grep -q '"kind": "Container"' "${ATTACH_SMOKE_METADATA}" \
    || { printf 'rendered metadata at %s missing kind Container\n' "${ATTACH_SMOKE_METADATA}" >&2; exit 1; }

# PTY-driven `kuke attach` smoke. hack/attach-smoke allocates a TTY
# (pkg/attach requires one), waits for pkg/attach's raw-mode keyboard
# filter to wire up, sends the Ctrl+] Ctrl+] detach sequence sbsh
# registers, and enforces a 20s overall deadline. A clean exit confirms
# the kuketty server is serving the JSON-RPC + SCM_RIGHTS protocol
# pkg/attach speaks.
ATTACH_LOG="${ATTACH_SMOKE_TMP}/attach.log"
go run ./hack/attach-smoke --log "${ATTACH_LOG}" -- \
    sudo --preserve-env="${PRESERVE_ENV_WORKLOAD}" ./kuke attach "${ATTACH_SMOKE_CELL}" \
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
