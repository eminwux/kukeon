// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package cgroups

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// hostProbe reports a host-environment risk that interacts with cgroup-v2
// memory accounting: a non-empty return is a high-severity warning the
// operator should see; an empty return means "this risk does not apply".
//
// Probes are advisory — they never change the exit code (per the
// 2026-05-15 incident author's directive on issue #532: warnings only,
// never fail). The point is to flag hosts that are one cgroup-unlimited
// container away from a hard wedge so the operator decides what to do,
// not to refuse work.
type hostProbe func() string

// probeSwap is the swap-availability probe runCheck calls on the unscoped
// host-root path. Indirected via a package var so unit tests can simulate
// hosts with/without swap without mounting a fake /proc.
//
//nolint:gochecknoglobals // test seam for the production swap probe
var probeSwap hostProbe = defaultProbeSwap

// probeUserspaceOOM is the userspace-OOM-guard probe runCheck calls on the
// unscoped host-root path. Indirected via a package var so unit tests can
// simulate matching/non-matching hosts.
//
//nolint:gochecknoglobals // test seam for the production OOM-guard probe
var probeUserspaceOOM hostProbe = defaultProbeUserspaceOOM

// runHostProbes invokes both host probes and writes any non-empty warning
// lines to w prefixed with "warning:" so operators can scan severity at a
// glance. Called only from the unscoped host-root path in runCheck —
// scoped probes (--scope realm|space|stack|cell) inspect a sub-tree, not
// the host environment, so the host warnings would be misleading there.
func runHostProbes(w io.Writer) {
	for _, msg := range []string{probeSwap(), probeUserspaceOOM()} {
		if msg == "" {
			continue
		}
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
}

// defaultProbeSwap reads /proc/swaps and returns a warning when no swap is
// configured. Wraps probeSwapAt so the on-disk path is overridable in
// tests via the export_test.go hooks.
func defaultProbeSwap() string {
	return probeSwapAt("/proc/swaps")
}

// probeSwapAt is the path-parameterized core of defaultProbeSwap. /proc/swaps
// always emits a header line (`Filename Type Size Used Priority`) even when
// no swap is configured; an empty swap-config has only that header. An
// unreadable file is treated as "cannot tell" rather than a warning so the
// probe stays silent on platforms that do not expose /proc/swaps.
func probeSwapAt(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= 1 {
		return "host has no swap (/proc/swaps shows only the header line); on no-swap, no-userspace-OOM hosts a single cgroup-unlimited container can wedge the kernel before any userspace OOM kills it (see kuke daemon --default-memory-limit-bytes for a daemon-side fallback)"
	}
	return ""
}

// defaultProbeUserspaceOOM reports a warning when no known userspace OOM
// guard (systemd-oomd, earlyoom, oomd) is running. Wraps
// probeUserspaceOOMAt so the on-disk /proc directory is overridable in
// tests via the export_test.go hooks.
func defaultProbeUserspaceOOM() string {
	return probeUserspaceOOMAt("/proc")
}

// probeUserspaceOOMAt is the path-parameterized core of
// defaultProbeUserspaceOOM. Walks /proc directly instead of shelling out
// to systemctl/pgrep so the probe stays dependency-free and works on
// minimal hosts. Returns empty when /proc itself is unreadable so a
// sandboxed environment without /proc silently skips the check rather
// than emitting a false-positive warning.
func probeUserspaceOOMAt(procDir string) string {
	for _, name := range []string{"systemd-oomd", "earlyoom", "oomd"} {
		if processRunningAt(procDir, name) {
			return ""
		}
	}
	return "host has no userspace OOM guard (systemd-oomd / earlyoom / oomd); the in-kernel OOM killer alone may not act before journald, sshd, or other host-critical services degrade on a memory-pressure event"
}

// processRunningAt reports whether any process under procDir has the given
// comm. /proc/<pid>/comm is the kernel-truncated 15-char base name, which
// matches all three guard names above (longest is "systemd-oomd" = 12
// chars). Returns false on any read error along the way.
func processRunningAt(procDir, name string) bool {
	entries, readErr := os.ReadDir(procDir)
	if readErr != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, atoiErr := strconv.Atoi(e.Name()); atoiErr != nil {
			continue
		}
		comm, commErr := os.ReadFile(filepath.Join(procDir, e.Name(), "comm"))
		if commErr != nil {
			continue
		}
		if strings.TrimSpace(string(comm)) == name {
			return true
		}
	}
	return false
}
