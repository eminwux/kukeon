#!/usr/bin/env bash
# Copyright 2025 Emiliano Spinella (eminwux)
# SPDX-License-Identifier: Apache-2.0
#
# install.sh — one-line installer for kukeon.
#
#   curl -fsSL https://kukeon.io/install.sh | bash
#   curl -fsSL https://kukeon.io/install.sh | bash -s -- --check
#
# Collapses the multi-step manual install (download → chmod → install →
# hardlink → init) into a single invocation. Checks but never installs host
# prerequisites (containerd, cgroups v2) — auto-installing those
# has too much blast radius on arbitrary user systems; surface a distro-aware
# hint instead.
#
# Env overrides:
#   KUKE_VERSION           pin a release tag (default: resolve via GitHub API)
#   KUKE_REPO              GitHub repo path (default: eminwux/kukeon — for forks)
#   KUKE_INSTALL_PREFIX    install dir (default: /usr/local/bin)
#   KUKE_SKIP_INIT=1       skip the `kuke init` step
#   KUKE_SKIP_CHECKSUM=1   skip .sha256 verification (NOT recommended)
#   KUKE_SKIP_KUKEBUILD=1  skip downloading/installing kukebuild (disables
#                          `kuke build`; for release tags predating its asset)
#   KUKE_SKIP_DAEMON_UPGRADE=1
#                          on a live host, skip the daemon version check and
#                          keep an intentionally pinned older daemon (preserves
#                          the historical silent-skip behavior)

set -euo pipefail

KUKE_REPO="${KUKE_REPO:-eminwux/kukeon}"
KUKE_INSTALL_PREFIX="${KUKE_INSTALL_PREFIX:-/usr/local/bin}"
KUKE_VERSION="${KUKE_VERSION:-}"
KUKE_SKIP_INIT="${KUKE_SKIP_INIT:-}"
KUKE_SKIP_CHECKSUM="${KUKE_SKIP_CHECKSUM:-}"
KUKE_SKIP_KUKEBUILD="${KUKE_SKIP_KUKEBUILD:-}"
KUKE_SKIP_DAEMON_UPGRADE="${KUKE_SKIP_DAEMON_UPGRADE:-}"

# Set by handle_running_daemon when a live host's daemon is older than the
# binaries just installed; main exits non-zero on this so the installer never
# prints an unqualified success banner over a client/daemon version skew.
KUKE_DAEMON_SKEW=""

MODE="install"

# --- Output helpers -----------------------------------------------------------
# Colors are emitted only on a TTY so piped output (e.g. CI logs) stays clean.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    C_RESET=$'\033[0m'
    C_BOLD=$'\033[1m'
    C_GREEN=$'\033[32m'
    C_RED=$'\033[31m'
    C_YELLOW=$'\033[33m'
    C_BLUE=$'\033[34m'
else
    C_RESET=""; C_BOLD=""; C_GREEN=""; C_RED=""; C_YELLOW=""; C_BLUE=""
fi

ok()    { printf '%s✓%s %s\n' "$C_GREEN" "$C_RESET" "$*"; }
warn()  { printf '%s!%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2; }
fail()  { printf '%s✗%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; }
step()  { printf '\n%s==>%s %s\n' "$C_BOLD$C_BLUE" "$C_RESET" "$*"; }

usage() {
    cat <<EOF
Usage: install.sh [--check] [--help]

Installs kukeon (kuke + kukeond + kukebuild + kukepause) on a Linux host.

  --check    Run prerequisite checks only; do not touch the system.
  --help     Show this help.

Env overrides:
  KUKE_VERSION           Pin a release tag (default: resolve latest via GitHub API).
  KUKE_REPO              GitHub repo (default: eminwux/kukeon).
  KUKE_INSTALL_PREFIX    Install dir (default: /usr/local/bin).
  KUKE_SKIP_INIT=1       Skip the post-install \`kuke init\` step.
  KUKE_SKIP_CHECKSUM=1   Skip .sha256 verification (NOT recommended).
  KUKE_SKIP_KUKEBUILD=1  Skip kukebuild (disables \`kuke build\`).
  KUKE_SKIP_DAEMON_UPGRADE=1
                         On a live host, skip the daemon version check and keep
                         an intentionally pinned older daemon.
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --check) MODE="check"; shift ;;
        -h|--help) usage; exit 0 ;;
        *) fail "unknown argument: $1"; usage >&2; exit 2 ;;
    esac
done

# --- Distro detection --------------------------------------------------------
# Used only to render an actionable "install containerd" hint. Falls through
# to a generic message on hosts without /etc/os-release (e.g. some minimal
# container images) — never blocks the prereq check itself.
distro_family() {
    if [ ! -r /etc/os-release ]; then
        echo "unknown"; return
    fi
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}${ID_LIKE:+ }${ID_LIKE:-}" in
        *debian*|*ubuntu*) echo "debian" ;;
        *fedora*|*rhel*|*centos*|*rocky*|*almalinux*) echo "rhel" ;;
        *arch*) echo "arch" ;;
        *suse*|*opensuse*) echo "suse" ;;
        *) echo "unknown" ;;
    esac
}

install_hint() {
    local package="$1"
    case "$(distro_family)" in
        debian) echo "    sudo apt-get update && sudo apt-get install -y ${package}" ;;
        rhel)   echo "    sudo dnf install -y ${package}" ;;
        arch)   echo "    sudo pacman -S --noconfirm ${package}" ;;
        suse)   echo "    sudo zypper install -y ${package}" ;;
        *)      echo "    Install '${package}' via your distribution's package manager." ;;
    esac
}

# --- Platform detection ------------------------------------------------------
detect_platform() {
    local kernel arch
    kernel="$(uname -s)"
    arch="$(uname -m)"
    if [ "$kernel" != "Linux" ]; then
        fail "kukeon requires Linux (detected ${kernel})."
        fail "On macOS or Windows, run the installer inside a Linux VM (Multipass, UTM, WSL2)."
        exit 1
    fi
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *)
            fail "unsupported architecture: ${arch} (supported: amd64, arm64)"
            exit 1
            ;;
    esac
}

# --- Prereq checks -----------------------------------------------------------
# Each check returns 0 on success and prints a hint on failure. We collect
# all failures and report them together so the user sees the full picture in
# one pass instead of fixing-and-retrying repeatedly.
PREREQ_FAILURES=0

check_cgroupv2() {
    if ! [ -d /sys/fs/cgroup ]; then
        fail "/sys/fs/cgroup is not a directory"
        printf '    cgroups v2 must be mounted at /sys/fs/cgroup.\n' >&2
        PREREQ_FAILURES=$((PREREQ_FAILURES + 1))
        return 1
    fi
    # `stat -f -c %T` prints the filesystem type. cgroup v2 reports "cgroup2fs";
    # legacy cgroup v1 reports "tmpfs" with a different layout — checking the
    # FS type is the only reliable distinguisher across distros.
    local fstype
    fstype="$(stat -f -c %T /sys/fs/cgroup 2>/dev/null || echo "")"
    if [ "$fstype" != "cgroup2fs" ]; then
        fail "/sys/fs/cgroup is not a cgroup v2 mount (got: ${fstype:-unknown})"
        printf '    Boot the host with systemd.unified_cgroup_hierarchy=1 or enable cgroup v2 in\n' >&2
        printf '    your kernel boot parameters. Most modern distros (Ubuntu 22.04+, Fedora 31+,\n' >&2
        printf '    Debian 11+) default to v2 already.\n' >&2
        PREREQ_FAILURES=$((PREREQ_FAILURES + 1))
        return 1
    fi
    ok "cgroups v2 mounted at /sys/fs/cgroup"
}

check_containerd() {
    local sock=/run/containerd/containerd.sock
    if [ ! -S "$sock" ]; then
        fail "containerd socket not found at ${sock}"
        printf '    Install containerd:\n' >&2
        install_hint containerd >&2
        printf '    Then start it:\n' >&2
        printf '      sudo systemctl enable --now containerd\n' >&2
        PREREQ_FAILURES=$((PREREQ_FAILURES + 1))
        return 1
    fi
    # Opportunistic responsiveness probe — only runs when `ctr` is on PATH
    # and we're root, since the containerd socket is typically root:root
    # mode 660 and a non-root probe fails with EACCES (which we'd misread
    # as a stale socket). A stale socket from a crashed daemon is rare but
    # real, and root is the typical invocation context (curl … | sudo bash),
    # so the probe still fires where it matters.
    if [ "$(id -u)" -eq 0 ] && command -v ctr >/dev/null 2>&1; then
        if ! timeout 5 ctr version >/dev/null 2>&1; then
            fail "containerd socket present at ${sock} but not responsive"
            printf '    `ctr version` failed against the socket — containerd may be stopped\n' >&2
            printf '    or in a degraded state. Try:\n' >&2
            printf '      sudo systemctl restart containerd\n' >&2
            PREREQ_FAILURES=$((PREREQ_FAILURES + 1))
            return 1
        fi
        ok "containerd responsive at ${sock} (ctr version OK)"
        return 0
    fi
    ok "containerd socket present at ${sock}"
}

# CNI plugins are deliberately NOT checked on the host: kukeond's container
# image bundles them at /opt/cni/bin (Dockerfile cni-plugins stage), and
# `kukeondCellDoc` (internal/controller/bootstrap.go) only bind-mounts CNI
# state directories (/opt/cni/net.d, /var/lib/cni, /opt/cni/cache) from the
# host — not the plugin binaries. Host plugins are needed only by in-process
# workflows (KUKEON_NO_DAEMON=true or explicit --run-path; documented at
# docs/site/cli/commands.md). Requiring them in the default install path
# would force every operator to apt-install a package the standard daemon
# path never reads.
run_prereqs() {
    step "Checking prerequisites"
    check_cgroupv2 || true
    check_containerd || true
    if [ "$PREREQ_FAILURES" -gt 0 ]; then
        printf '\n' >&2
        fail "${PREREQ_FAILURES} prerequisite check(s) failed — see hints above."
        exit 1
    fi
}

# --- Privilege helper --------------------------------------------------------
# `kuke init` needs to touch /opt/kukeon, /run/kukeon, cgroups, and containerd,
# which all require root. Use the existing sudo session if one is open;
# otherwise the install/init steps will prompt for a password — which is fine
# under `curl | bash` because bash inherits the TTY for sudo's prompt.
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
    if ! command -v sudo >/dev/null 2>&1; then
        fail "running as a non-root user but \`sudo\` is not installed."
        printf '    Re-run this script as root, or install sudo first.\n' >&2
        exit 1
    fi
    SUDO="sudo"
fi

# --- Version resolution ------------------------------------------------------
resolve_latest_version() {
    # Resolve the literal latest release tag via the GitHub API rather than
    # the "/releases/latest" redirect alias — the alias is mutable and can
    # silently roll back to a withdrawn release. The API call returns the
    # exact tag_name we then bake into the download URL and checksum lookup.
    local api_url="https://api.github.com/repos/${KUKE_REPO}/releases/latest"
    local resp
    if ! resp="$(curl -fsSL "$api_url" 2>/dev/null)"; then
        fail "could not query ${api_url} for the latest release tag."
        printf '    Pin a version manually:  KUKE_VERSION=v0.1.0 bash install.sh\n' >&2
        exit 1
    fi
    # Stay grep/sed-only so the script has no jq dependency.
    local tag
    tag="$(printf '%s\n' "$resp" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')"
    if [ -z "$tag" ]; then
        fail "could not parse tag_name from GitHub API response."
        exit 1
    fi
    printf '%s\n' "$tag"
}

# --- Install -----------------------------------------------------------------
# Global so the EXIT trap below can see it after do_install returns. A `local`
# binding would be invisible by the time the trap fires in the outer shell.
INSTALL_TMPDIR=""
cleanup_tmpdir() {
    if [ -n "${INSTALL_TMPDIR}" ] && [ -d "${INSTALL_TMPDIR}" ]; then
        rm -rf "${INSTALL_TMPDIR}"
    fi
}

# fetch_verified <asset-name> <dest-path>
# Downloads the per-arch release asset to <dest-path>, fetches its `.sha256`
# companion and verifies the digest (honoring KUKE_SKIP_CHECKSUM). Factored out
# of do_install so `kuke` and `kukebuild` share one download+verify path rather
# than duplicating the checksum logic per binary.
fetch_verified() {
    local asset="$1" dest="$2"
    local asset_url sha_url sha_path
    asset_url="https://github.com/${KUKE_REPO}/releases/download/${KUKE_VERSION}/${asset}"
    sha_url="${asset_url}.sha256"
    sha_path="${dest}.sha256"

    step "Downloading ${asset} ${KUKE_VERSION}"
    if ! curl -fsSL -o "$dest" "$asset_url"; then
        fail "download failed: ${asset_url}"
        printf '    Confirm KUKE_VERSION=%s exists at\n' "$KUKE_VERSION" >&2
        printf '      https://github.com/%s/releases\n' "$KUKE_REPO" >&2
        exit 1
    fi
    ok "downloaded $(wc -c <"$dest" | tr -d ' ') bytes"

    step "Verifying checksum (${asset})"
    if [ -n "$KUKE_SKIP_CHECKSUM" ]; then
        warn "KUKE_SKIP_CHECKSUM=1 set — skipping verification (not recommended)."
    elif curl -fsSL -o "$sha_path" "$sha_url" 2>/dev/null; then
        # Releases publish the .sha256 in `sha256sum` format ("<hex>  <name>"),
        # so we take the first field and compare it to a local digest rather
        # than hand-rolling the parser.
        local expected actual
        expected="$(awk '{print $1}' "$sha_path")"
        if [ -z "$expected" ]; then
            fail ".sha256 asset at ${sha_url} is empty or malformed."
            exit 1
        fi
        actual="$(sha256sum "$dest" | awk '{print $1}')"
        if [ "$expected" != "$actual" ]; then
            fail "checksum mismatch for ${asset_url}"
            printf '    expected: %s\n' "$expected" >&2
            printf '    actual:   %s\n' "$actual" >&2
            exit 1
        fi
        ok "sha256 ${actual}"
    else
        fail "no .sha256 asset published at ${sha_url}"
        printf '    Set KUKE_SKIP_CHECKSUM=1 to bypass (NOT recommended), or pin a release\n' >&2
        printf '    that ships a checksum.\n' >&2
        exit 1
    fi
}

do_install() {
    local arch kuke_path kukebuild_path kukepause_path
    arch="$(detect_platform)"

    step "Resolving release version"
    if [ -z "$KUKE_VERSION" ]; then
        KUKE_VERSION="$(resolve_latest_version)"
    fi
    ok "version: ${KUKE_VERSION}"

    INSTALL_TMPDIR="$(mktemp -d -t kuke-install.XXXXXX)"
    trap cleanup_tmpdir EXIT
    kuke_path="${INSTALL_TMPDIR}/kuke"
    kukebuild_path="${INSTALL_TMPDIR}/kukebuild"
    kukepause_path="${INSTALL_TMPDIR}/kukepause"

    # kuke is the CLI + daemon (argv[0]-dispatched to kukeond); kukebuild is the
    # docker-free image builder `kuke build` execs; kukepause is the static PID 1
    # `kuke init` stages to /opt/kukeon/bin/kukepause and bind-mounts into every
    # cell's root container. Ship all three so a fresh installer host can build
    # images and bootstrap the runtime without a source checkout (issues #1227,
    # #1241) — without kukebuild, `kuke build` fails with
    # errdefs.ErrKukebuildNotFound; without kukepause, `kuke init` aborts at its
    # "stage kukepause" step.
    #
    # All assets are downloaded + verified into the tmpdir before anything is
    # placed on the system, so a fetch/verify failure leaves the host untouched.
    # KUKE_SKIP_KUKEBUILD skips kukebuild entirely — release tags predating its
    # asset (#1227) have no kukebuild-linux-* to fetch, so an old KUKE_VERSION
    # pin sets it to install kuke alone rather than 404 on the missing asset.
    # kukepause has no skip flag: it is mandatory for `kuke init`, so a release
    # pin too old to ship kukepause-linux-* fails loudly here rather than
    # installing a runtime that cannot bootstrap.
    fetch_verified "kuke-linux-${arch}" "$kuke_path"
    chmod +x "$kuke_path"
    if [ -n "$KUKE_SKIP_KUKEBUILD" ]; then
        warn "KUKE_SKIP_KUKEBUILD=1 set — skipping kukebuild (\`kuke build\` will be unavailable)."
    else
        fetch_verified "kukebuild-linux-${arch}" "$kukebuild_path"
        chmod +x "$kukebuild_path"
    fi
    fetch_verified "kukepause-linux-${arch}" "$kukepause_path"
    chmod +x "$kukepause_path"

    step "Installing to ${KUKE_INSTALL_PREFIX}"
    $SUDO install -m 0755 "$kuke_path" "${KUKE_INSTALL_PREFIX}/kuke"
    # Hardlink — not symlink — because the binary dispatches on argv[0]
    # basename and we want `kukeond` resolved as a real path, not as
    # `kuke -> kukeond` indirection that some shells resolve.
    $SUDO ln -f "${KUKE_INSTALL_PREFIX}/kuke" "${KUKE_INSTALL_PREFIX}/kukeond"
    # kukepause is a distinct static binary (not an argv[0] alias of kuke) that
    # `kuke init` resolves on $PATH / alongside kuke to stage as every cell's
    # PID 1 — install it as a real file so init finds it (issue #1241).
    $SUDO install -m 0755 "$kukepause_path" "${KUKE_INSTALL_PREFIX}/kukepause"
    if [ -n "$KUKE_SKIP_KUKEBUILD" ]; then
        ok "installed kuke + kukeond + kukepause at ${KUKE_INSTALL_PREFIX}"
    else
        # kukebuild is a distinct binary (its own Go module embedding BuildKit),
        # not an argv[0] alias of kuke — install it as a real file so `kuke
        # build` resolves it on PATH.
        $SUDO install -m 0755 "$kukebuild_path" "${KUKE_INSTALL_PREFIX}/kukebuild"
        ok "installed kuke + kukeond + kukebuild + kukepause at ${KUKE_INSTALL_PREFIX}"
    fi

    if [ -n "$KUKE_SKIP_INIT" ]; then
        warn "KUKE_SKIP_INIT=1 set — skipping \`kuke init\`. Run it manually:"
        printf '      sudo kuke init\n'
        return 0
    fi

    step "Initializing the runtime"
    # Idempotency: if the daemon socket exists, init already ran. Skip
    # rather than re-running, because `kuke init` on a healthy host is a
    # no-op only if the prior bootstrap left consistent state — and we
    # cannot reliably detect that from a shell script. But a live daemon
    # older than the binaries we just installed must NOT be declared a
    # success — handle_running_daemon compares versions and surfaces the
    # skew + recreate command instead of an unqualified skip (issue #1260).
    if [ -S /run/kukeon/kukeond.sock ]; then
        handle_running_daemon
    else
        $SUDO kuke init
    fi

    install_systemd_unit
}

# --- Running-daemon version gate ---------------------------------------------
# A binary upgrade on a live host leaves the old kukeond running. The previous
# liveness-only skip (socket present → skip) declared that a success even when
# the daemon predated the new binaries, ending in a client/daemon version skew
# the installer just called done (issue #1260). This compares the live daemon's
# version against the just-installed client and, on a mismatch, surfaces the
# exact `kuke daemon recreate` command rather than a silent skip.
#
# `kuke version --strict` prints the Client/Daemon lines and exits non-zero
# only on a real version mismatch; an unreachable daemon (socket present but
# dead) prints a warning and exits 0, so it falls through to the historical
# skip rather than a spurious recreate prompt. The installer does not auto-cycle
# the daemon — a recreate tears down and re-provisions the kuke-system kukeond
# cell, too aggressive to do unprompted from a piped curl|bash — so it prints
# the copy-pasteable command and lets main exit non-zero on the skew.
handle_running_daemon() {
    if [ -n "$KUKE_SKIP_DAEMON_UPGRADE" ]; then
        ok "kukeond already running at /run/kukeon/kukeond.sock — skipping \`kuke init\` (KUKE_SKIP_DAEMON_UPGRADE=1)"
        return 0
    fi

    local version_out
    if version_out="$($SUDO kuke version --strict 2>&1)"; then
        ok "kukeond already running at /run/kukeon/kukeond.sock (version-matched) — skipping \`kuke init\`"
        return 0
    fi

    KUKE_DAEMON_SKEW=1
    fail "kukeond is older than the binaries just installed (client ${KUKE_VERSION}) — leaving a client/daemon version skew."
    printf '%s\n' "$version_out" | sed 's/^/    /' >&2
    printf '\n    Upgrade the daemon to match. Workload cells are untouched — recreate\n' >&2
    printf '    only cycles the kuke-system kukeond cell:\n\n' >&2
    printf '      %ssudo kuke daemon recreate --kukeond-image ghcr.io/%s:%s%s\n\n' "$C_BOLD" "$KUKE_REPO" "$KUKE_VERSION" "$C_RESET" >&2
    printf '    To keep an intentionally pinned older daemon, re-run with KUKE_SKIP_DAEMON_UPGRADE=1.\n' >&2
    return 0
}

# --- systemd unit ------------------------------------------------------------
# Drops /etc/systemd/system/kukeond.service so the daemon survives host and
# containerd restarts (issue #541). Without it, nothing brings kukeond back
# after a reboot — containerd does not restart tasks across its own restart
# and there is no host-level supervisor on the kuke side. The unit invokes
# `kuke daemon start` (the in-process verb, idempotent against an already-
# running daemon) ordered after containerd.service so the daemon's
# containerd client always has a socket to talk to.
#
# Skipped on systemd-less hosts (dev containers, minimal images) with a
# visible notice — the operator falls back to running `sudo kuke daemon start`
# manually after each reboot. Re-running install.sh on a host that already
# has the unit installed overwrites it and runs daemon-reload, so a version
# bump that changes the unit contents picks up cleanly.
SYSTEMD_UNIT_PATH="/etc/systemd/system/kukeond.service"

install_systemd_unit() {
    if ! command -v systemctl >/dev/null 2>&1; then
        step "Configuring host supervisor"
        warn "systemd not detected — skipping kukeond.service unit install."
        printf '    On systemd-less hosts, bring kukeond up after each reboot with:\n' >&2
        printf '      sudo kuke daemon start\n' >&2
        return 0
    fi
    step "Installing kukeond.service systemd unit"
    # Write through a tmpfile + atomic install so a concurrent systemd read of
    # /etc/systemd/system never sees a half-written unit. `install -m 0644`
    # mirrors the perms systemd-supplied units ship with.
    local unit_tmp
    unit_tmp="$(mktemp -t kukeond.service.XXXXXX)"
    cat >"$unit_tmp" <<EOF
[Unit]
Description=kukeon daemon (kukeond)
Documentation=https://kukeon.io
After=containerd.service
Requires=containerd.service

[Service]
# Type=oneshot + RemainAfterExit=yes: \`kuke daemon start\` is an in-process
# verb that brings the kukeond cell up via containerd and then exits;
# containerd supervises the kukeond container once it is running. The unit
# stays "active" so \`systemctl status kukeond\` reports the bootstrap as
# the supervised state, and Restart=on-failure retries the bootstrap if it
# loses a race with containerd.service coming up.
Type=oneshot
RemainAfterExit=yes
ExecStart=${KUKE_INSTALL_PREFIX}/kuke daemon start
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
    $SUDO install -m 0644 "$unit_tmp" "$SYSTEMD_UNIT_PATH"
    rm -f "$unit_tmp"
    $SUDO systemctl daemon-reload
    # `enable --now` is idempotent when the daemon is already running (the
    # oneshot ExecStart calls `kuke daemon start`, which itself returns
    # success when the kukeond cell is up). On a fresh host where `kuke
    # init` just ran, the daemon is already live and this call is a no-op
    # start + a real enable; on a re-run after `systemctl disable`, this is
    # the path that re-enables the unit.
    $SUDO systemctl enable --now kukeond.service
    ok "installed and enabled ${SYSTEMD_UNIT_PATH}"
}

# --- Next steps --------------------------------------------------------------
print_next_steps() {
    cat <<EOF

${C_GREEN}${C_BOLD}✓ kukeon installed and initialized${C_RESET}

Try your first session — create a cell from an image, list it, start it:
  ${C_BOLD}kuke create cell my-first --image docker.io/library/alpine:3${C_RESET}
  ${C_BOLD}kuke get cells${C_RESET}
  ${C_BOLD}kuke start my-first${C_RESET}

Or, for a one-shot interactive session (create + start + attach):
  ${C_BOLD}kuke run --image docker.io/library/alpine:3${C_RESET}

Or apply a manifest:
  ${C_BOLD}kuke apply -f my-stack.yaml${C_RESET}

Docs:    https://kukeon.io
Issues:  https://github.com/${KUKE_REPO}/issues
EOF
}

# --- Main --------------------------------------------------------------------
run_prereqs

if [ "$MODE" = "check" ]; then
    printf '\n'
    ok "all prerequisites satisfied — system is ready for \`bash install.sh\`."
    exit 0
fi

do_install

# A live host with a daemon older than the just-installed binaries is left in a
# version skew (handle_running_daemon already printed the recreate command).
# Exit non-zero with a qualified message instead of the rosy "installed and
# initialized" banner — the installer must never claim an unqualified success
# over a skew it just declared (issue #1260).
if [ -n "$KUKE_DAEMON_SKEW" ]; then
    fail "binaries installed, but the running daemon is older — run the recreate command above to finish the upgrade."
    exit 1
fi

print_next_steps
