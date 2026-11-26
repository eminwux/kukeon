#!/usr/bin/env bash
# Copyright 2026 Emiliano Spinella (eminwux)
# SPDX-License-Identifier: Apache-2.0
#
# install-test.sh — checks of scripts/install.sh that need no network, no
# root, no containerd and no systemd (issue #1694):
#
#   - the kuke-serve.service unit renders with the invoking user, the kukeon
#     group and the listen address;
#   - KUKE_SKIP_WEB_UI=1 installs no unit, and a systemd-less host skips it;
#   - a failed `kuke team init` (a stub that fails, or a host without git)
#     prints the ✗ line with the reason and the retry command, and the
#     installer exits 3; KUKE_SKIP_TEAM_INIT=1 exits 0.
#
# It sources install.sh with KUKE_INSTALL_LIB=1, which defines the installer's
# functions without running it, and calls them with stubs for `kuke`,
# `systemctl` and `git` on PATH. Nothing outside a temporary directory is
# written.
#
# Usage: bash scripts/install-test.sh
#
# Run by the installer job of .github/workflows/test.yaml.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALLER="${REPO_ROOT}/scripts/install.sh"
WORK="$(mktemp -d -t kuke-install-test.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
# A case that fails where no failure is expected ends the script (set -e) with
# its output captured in a variable: say where, and print what it printed.
LAST_OUT="${WORK}/last.out"
trap 'printf "install-test.sh: aborted at line %s; the last installer run printed:\n" "$LINENO" >&2; cat "$LAST_OUT" >&2 2>/dev/null || true' ERR

failures=0
pass() { printf 'ok   %s\n' "$1"; }
flunk() {
    printf 'FAIL %s\n' "$1" >&2
    shift
    if [ "$#" -gt 0 ]; then printf '     %s\n' "$@" >&2; fi
    failures=$((failures + 1))
}
# expect_line <name> <text> <exact line>
expect_line() {
    if printf '%s\n' "$2" | grep -qxF -- "$3"; then
        pass "$1"
    else
        flunk "$1" "missing line: $3" "in:" "$2"
    fi
}
# expect_text <name> <text> <substring>
expect_text() {
    if printf '%s\n' "$2" | grep -qF -- "$3"; then
        pass "$1"
    else
        flunk "$1" "missing text: $3" "in:" "$2"
    fi
}
# expect_no_text <name> <text> <substring>
expect_no_text() {
    if printf '%s\n' "$2" | grep -qF -- "$3"; then
        flunk "$1" "unexpected text: $3" "in:" "$2"
    else
        pass "$1"
    fi
}
expect_status() {
    if [ "$2" -eq "$3" ]; then
        pass "$1"
    else
        flunk "$1" "exit status $2, want $3"
    fi
}

# --- Stubs --------------------------------------------------------------------
# A private PATH holds only what a case puts there, plus the few tools
# install.sh itself calls. `kuke` is the installed binary the installer runs
# from KUKE_INSTALL_PREFIX.
BASE_BIN="${WORK}/base"
mkdir -p "$BASE_BIN"
for tool in bash cat sed awk grep head tr mktemp install rm sleep id printf env getent uname stat; do
    if path="$(command -v "$tool" 2>/dev/null)" && [ -x "$path" ]; then
        ln -sf "$path" "${BASE_BIN}/${tool}"
    fi
done
# Run as a non-root user (CI), the installer refuses to load without sudo.
# Nothing here needs root: the stub runs its arguments as the current user.
cat >"${BASE_BIN}/sudo" <<'EOF'
#!/bin/bash
exec "$@"
EOF
chmod +x "${BASE_BIN}/sudo"

# new_case <name> makes an empty stub directory and prefix for one case and
# points CASE_BIN / CASE_PREFIX / CASE_LOG at them.
new_case() {
    CASE_DIR="${WORK}/$1"
    CASE_BIN="${CASE_DIR}/bin"
    CASE_PREFIX="${CASE_DIR}/prefix"
    CASE_LOG="${CASE_DIR}/calls.log"
    mkdir -p "$CASE_BIN" "$CASE_PREFIX"
    : >"$CASE_LOG"
}

# stub <dir> <name> <body> writes an executable script that logs its call.
stub() {
    cat >"$1/$2" <<EOF
#!/bin/bash
printf '%s %s\n' "$2" "\$*" >>"${CASE_LOG}"
$3
EOF
    chmod +x "$1/$2"
}

# in_installer <script> runs <script> in a fresh shell that has sourced the
# installer as a library, with the case's stubs first on PATH. The script is
# the installer's own functions and assignments; `exit` inside them ends only
# that shell.
in_installer() {
    local rc=0
    env -i HOME="$WORK" PATH="${CASE_BIN}:${BASE_BIN}" NO_COLOR=1 TMPDIR="$WORK" \
        KUKE_INSTALL_LIB=1 KUKE_INSTALL_PREFIX="$CASE_PREFIX" \
        ${SUDO_USER_FOR_CASE:+SUDO_USER="$SUDO_USER_FOR_CASE"} \
        ${EXTRA_ENV:-} \
        bash -c '
            set -euo pipefail
            installer="$1" script="$2"
            # The installer parses "$@" when sourced: give it none.
            set --
            # shellcheck disable=SC1090
            . "$installer"
            SUDO=""
            WEB_UI_SETTLE_SECONDS=0
            eval "$script"
        ' install-test "$INSTALLER" "$1" >"$LAST_OUT" 2>&1 || rc=$?
    cat "$LAST_OUT"
    return "$rc"
}

# --- The unit file ------------------------------------------------------------
new_case unit-user
out="$(SUDO_USER_FOR_CASE=alice in_installer 'render_web_ui_unit')"
expect_line "unit runs as the invoking user" "$out" "User=alice"
expect_line "unit joins the kukeon group" "$out" "SupplementaryGroups=kukeon"
expect_line "unit listens on localhost:5129 from the install prefix" "$out" "ExecStart=${CASE_PREFIX}/kuke serve --listen localhost:5129"
expect_line "unit restarts on failure" "$out" "Restart=on-failure"
expect_line "unit is wanted by multi-user.target" "$out" "WantedBy=multi-user.target"

new_case unit-root
out="$(in_installer 'render_web_ui_unit')"
expect_line "unit runs as root without an invoking user" "$out" "User=root"

# --- Installing the unit ------------------------------------------------------
new_case unit-install
stub "$CASE_BIN" systemctl 'exit 0'
out="$(SUDO_USER_FOR_CASE=alice in_installer "WEB_UI_UNIT_PATH='${CASE_DIR}/kuke-serve.service'; install_web_ui_unit; printf 'KUKE_WEB_UI=%s\n' \"\$KUKE_WEB_UI\"")"
calls="$(cat "$CASE_LOG")"
if [ -f "${CASE_DIR}/kuke-serve.service" ] && grep -qxF 'User=alice' "${CASE_DIR}/kuke-serve.service"; then
    pass "the unit file is written"
else
    flunk "the unit file is written" "$out"
fi
expect_line "systemd is reloaded" "$calls" "systemctl daemon-reload"
expect_line "the unit is enabled" "$calls" "systemctl enable kuke-serve.service"
expect_line "the unit is (re)started" "$calls" "systemctl restart kuke-serve.service"
expect_line "a started unit is recorded" "$out" "KUKE_WEB_UI=ok"

new_case unit-does-not-start
stub "$CASE_BIN" systemctl 'case "$1" in is-active) exit 3 ;; esac; exit 0'
out="$(in_installer "WEB_UI_UNIT_PATH='${CASE_DIR}/kuke-serve.service'; install_web_ui_unit; printf 'KUKE_WEB_UI=%s\n' \"\$KUKE_WEB_UI\"")"
expect_line "a unit that does not stay up is recorded, not fatal" "$out" "KUKE_WEB_UI=failed"
expect_text "a unit that does not stay up names journalctl" "$out" "journalctl -u kuke-serve.service"

new_case unit-skip
stub "$CASE_BIN" systemctl 'exit 0'
out="$(EXTRA_ENV="KUKE_SKIP_WEB_UI=1" in_installer "WEB_UI_UNIT_PATH='${CASE_DIR}/kuke-serve.service'; install_web_ui_unit; printf 'KUKE_WEB_UI=%s\n' \"\$KUKE_WEB_UI\"")"
if [ ! -e "${CASE_DIR}/kuke-serve.service" ] && [ ! -s "$CASE_LOG" ]; then
    pass "KUKE_SKIP_WEB_UI=1 writes no unit and calls no systemctl"
else
    flunk "KUKE_SKIP_WEB_UI=1 writes no unit and calls no systemctl" "$out" "$(cat "$CASE_LOG")"
fi
expect_text "KUKE_SKIP_WEB_UI=1 says how to run the web UI" "$out" "kuke serve"
expect_line "KUKE_SKIP_WEB_UI=1 is recorded as skipped" "$out" "KUKE_WEB_UI=skipped"

new_case unit-no-systemd
out="$(in_installer "WEB_UI_UNIT_PATH='${CASE_DIR}/kuke-serve.service'; install_web_ui_unit; printf 'KUKE_WEB_UI=%s\n' \"\$KUKE_WEB_UI\"")"
if [ ! -e "${CASE_DIR}/kuke-serve.service" ]; then
    pass "a host without systemd gets no unit"
else
    flunk "a host without systemd gets no unit" "$out"
fi
expect_text "a host without systemd is told so" "$out" "systemd not detected"

# --- The team step and the exit status ----------------------------------------
# finish needs a version for its banner and a `kuke` for the web UI login
# lookup; the stub answers every subcommand except `team init`.
finish_script='KUKE_VERSION=v0.0.0-test; KUKE_CHANNEL=stable; run_team_step init; finish'
kuke_stub() { # <team init body>
    stub "$CASE_PREFIX" kuke "case \"\$1 \$2\" in
  'team init') $1 ;;
  'get secret') printf '%s\n' '{\"spec\":{\"basicAuth\":{\"username\":\"root\",\"password\": \"stub-password\"}}}' ;;
esac"
    ln -sf "${CASE_PREFIX}/kuke" "${CASE_BIN}/kuke"
}

new_case team-fails
stub "$CASE_BIN" git 'exit 0'
kuke_stub "echo 'composing the kuke team' ; echo 'Error: materialize source \"kukeon\": git source access failed' >&2; exit 1"
set +e
out="$(in_installer "$finish_script")"
rc=$?
set -e
expect_status "a failed team init exits 3" "$rc" 3
expect_text "a failed team init prints the ✗ line with the reason" "$out" '✗ kuke team init failed: materialize source "kukeon": git source access failed'
expect_text "a failed team init names the retry command" "$out" "Retry:  sudo kuke team init"
expect_text "a failed team init points at the web UI's Initialize" "$out" "Initialize"
expect_text "a failed team init still prints the web UI login" "$out" "password: stub-password"
expect_no_text "a failed team init does not offer the majordomo it did not store" "$out" "kuke-majordomo-codex"

new_case team-no-git
kuke_stub "echo 'team init must not run without git' >&2; exit 99"
set +e
out="$(in_installer "$finish_script")"
rc=$?
set -e
expect_status "a host without git exits 3" "$rc" 3
expect_text "a host without git prints the ✗ line with the reason" "$out" "✗ kuke team init failed: git is not installed"
expect_no_text "a host without git never runs team init" "$(cat "$CASE_LOG")" "kuke team init"

new_case team-skipped
stub "$CASE_BIN" git 'exit 0'
kuke_stub "echo 'team init must not run when skipped' >&2; exit 99"
set +e
out="$(EXTRA_ENV="KUKE_SKIP_TEAM_INIT=1" in_installer "$finish_script")"
rc=$?
set -e
expect_status "KUKE_SKIP_TEAM_INIT=1 exits 0" "$rc" 0
expect_no_text "KUKE_SKIP_TEAM_INIT=1 prints no ✗ line" "$out" "✗"
expect_no_text "KUKE_SKIP_TEAM_INIT=1 never runs team init" "$(cat "$CASE_LOG")" "kuke team init"

new_case team-ok
stub "$CASE_BIN" git 'exit 0'
kuke_stub "echo 'Applied 7 resources; stored team kuke.'; exit 0"
set +e
out="$(in_installer "$finish_script")"
rc=$?
set -e
expect_status "a stored team exits 0" "$rc" 0
expect_text "a stored team prints the team step's output" "$out" "Applied 7 resources; stored team kuke."
expect_no_text "a stored team prints no ✗ line" "$out" "✗"
expect_text "the summary sends the operator to Get started" "$out" "Get started"
# The web UI block comes before the CLI examples.
web_line="$(printf '%s\n' "$out" | grep -n 'Open the web UI' | head -n1 | cut -d: -f1)"
cli_line="$(printf '%s\n' "$out" | grep -n 'Try your first session' | head -n1 | cut -d: -f1)"
if [ -n "$web_line" ] && [ -n "$cli_line" ] && [ "$web_line" -lt "$cli_line" ]; then
    pass "the summary prints the web UI URL and login first"
else
    flunk "the summary prints the web UI URL and login first" "$out"
fi
expect_text "without the unit the summary names kuke serve" "$out" "kuke serve"

new_case summary-with-unit
stub "$CASE_BIN" git 'exit 0'
kuke_stub "exit 0"
set +e
out="$(in_installer "KUKE_WEB_UI=ok; $finish_script")"
rc=$?
set -e
expect_status "a running unit exits 0" "$rc" 0
expect_line "with the unit running the summary is a URL to open" "$out" "  http://localhost:5129"

if [ "$failures" -gt 0 ]; then
    printf '\n%d installer check(s) failed\n' "$failures" >&2
    exit 1
fi
printf '\nall installer checks passed\n'
