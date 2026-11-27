#!/usr/bin/env bash
# Copyright 2025 Emiliano Spinella (eminwux)
# SPDX-License-Identifier: Apache-2.0
#
# e2e-preflight.sh — host prerequisite check `make e2e` runs before it builds
# the kukeond image or starts `go test ./e2e`.
#
# Each missing piece otherwise surfaces deep inside the suite (a kukebuild
# dial timeout several minutes in, or every daemon-bringing test failing at
# "E2E network preflight" because another suite holds the network lock).
# Checking up front turns those into one actionable line each and a non-zero
# exit before any build. The harness reads addresses, routes and bridges over
# netlink (internal/hostnet), so iproute2 (`ip`) is not a prerequisite.
# Every failure is reported in one pass so a fresh host or dev cell can be
# fixed in a single round.
#
# Usage: scripts/e2e-preflight.sh [BIN_DIR]
#   BIN_DIR is where `make e2e` built kukebuild (default: repo root).

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${1:-${REPO_ROOT}}"
CONTAINERD_SOCKET="/run/containerd/containerd.sock"
NETWORK_LOCK="/run/kukeon-e2e-network.lock"

failures=0
fail() {
    printf 'e2e preflight: %s\n' "$1" >&2
    failures=$((failures + 1))
}

# The harness uses ctr to verify the kukebuild-built image and the realms'
# containerd namespaces.
if ! command -v ctr > /dev/null 2>&1; then
    fail "'ctr' not found on PATH — install containerd (ctr ships with it)"
fi

# `make e2e` builds kukebuild into BIN_DIR before this script runs; a missing
# or non-executable binary means that build step did not produce it.
if [ ! -x "${BIN_DIR}/kukebuild" ]; then
    fail "kukebuild not found at ${BIN_DIR}/kukebuild — build it with 'make kukebuild'"
fi

# The standalone containerd, not docker's private one under
# /var/run/docker/containerd/.
if [ ! -S "${CONTAINERD_SOCKET}" ]; then
    fail "standalone containerd socket ${CONTAINERD_SOCKET} is absent — start it with 'service containerd start' (or 'systemctl start containerd'); with no init system: 'containerd > /tmp/containerd.log 2>&1 &'"
fi

# Another suite holding the host-wide network lock would fail every
# daemon-bringing test. Probe only an existing lock file so the check never
# creates it.
if [ -e "${NETWORK_LOCK}" ] && command -v flock > /dev/null 2>&1; then
    if ! flock -n "${NETWORK_LOCK}" true 2> /dev/null; then
        fail "another e2e suite holds ${NETWORK_LOCK} — wait for it to finish (or stop it) before re-running"
    fi
fi

if [ "${failures}" -gt 0 ]; then
    printf 'e2e preflight: %d prerequisite(s) missing; not running the e2e suite\n' "${failures}" >&2
    exit 1
fi
echo "e2e preflight: ok"
