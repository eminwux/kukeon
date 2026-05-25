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

package cgroups_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cgroupscmd "github.com/eminwux/kukeon/cmd/kuke/doctor/cgroups"
)

// TestCgroupsCmdHostProbeSwapMissingWarns: when /proc/swaps shows no swap,
// the unscoped pre-flight emits a high-severity warning to stderr but the
// exit code is unchanged. Pins the "warnings only, never fail" contract
// the 2026-05-15 incident author asked for in the issue #532 comment.
func TestCgroupsCmdHostProbeSwapMissingWarns(t *testing.T) {
	root := writeFakeCgroup(t,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	restore := cgroupscmd.SetHostProbesForTest(
		func() string { return "host has no swap" },
		func() string { return "" },
	)
	defer restore()

	stdout, stderr, err := runCmd(t, "--root", root)
	if err != nil {
		t.Fatalf(
			"Execute() error = %v on healthy cgroups + missing swap (warnings must not fail):\nstdout=%q\nstderr=%q",
			err,
			stdout,
			stderr,
		)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty (host warnings go to stderr only)", stdout)
	}
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("stderr missing 'warning:' prefix on no-swap host:\n%s", stderr)
	}
	if !strings.Contains(stderr, "no swap") {
		t.Errorf("stderr missing swap warning text:\n%s", stderr)
	}
}

// TestCgroupsCmdHostProbeOOMMissingWarns: when no userspace OOM guard is
// running, the unscoped pre-flight emits a high-severity warning to
// stderr but the exit code is unchanged.
func TestCgroupsCmdHostProbeOOMMissingWarns(t *testing.T) {
	root := writeFakeCgroup(t,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	restore := cgroupscmd.SetHostProbesForTest(
		func() string { return "" },
		func() string { return "host has no userspace OOM guard" },
	)
	defer restore()

	_, stderr, err := runCmd(t, "--root", root)
	if err != nil {
		t.Fatalf("Execute() error = %v on healthy cgroups + missing OOM guard (warnings must not fail):\nstderr=%q",
			err, stderr)
	}
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("stderr missing 'warning:' prefix on no-OOM host:\n%s", stderr)
	}
	if !strings.Contains(stderr, "userspace OOM") {
		t.Errorf("stderr missing OOM-guard warning text:\n%s", stderr)
	}
}

// TestCgroupsCmdHostProbesBothMissingWarnsBoth: when both probes fire,
// both warning lines appear independently — the doctor surfaces each
// risk on its own line so the operator sees the full picture rather
// than a collapsed combined verdict.
func TestCgroupsCmdHostProbesBothMissingWarnsBoth(t *testing.T) {
	root := writeFakeCgroup(t,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	restore := cgroupscmd.SetHostProbesForTest(
		func() string { return "host has no swap" },
		func() string { return "host has no userspace OOM guard" },
	)
	defer restore()

	_, stderr, err := runCmd(t, "--root", root)
	if err != nil {
		t.Fatalf("Execute() error = %v with both host warnings (warnings must never fail):\nstderr=%q",
			err, stderr)
	}
	if got := strings.Count(stderr, "warning:"); got != 2 {
		t.Errorf("expected 2 'warning:' lines, got %d:\n%s", got, stderr)
	}
	for _, want := range []string{"no swap", "userspace OOM"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// TestCgroupsCmdHostProbesAllGreen: when both host probes return empty
// (swap present + OOM guard active), the unscoped pre-flight stays
// silent on stderr — preserves the dev-init.sh tail-clean rule the
// project AGENTS.md regression guard depends on.
func TestCgroupsCmdHostProbesAllGreen(t *testing.T) {
	root := writeFakeCgroup(t,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	// TestMain already neutralizes the probes to "", but pin it again
	// locally so this test reads as self-contained.
	restore := cgroupscmd.SetHostProbesForTest(
		func() string { return "" },
		func() string { return "" },
	)
	defer restore()

	stdout, stderr, err := runCmd(t, "--root", root)
	if err != nil {
		t.Fatalf("Execute() error = %v on all-green host:\nstdout=%q\nstderr=%q",
			err, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on healthy host", stdout)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("stderr contains warning on all-green host (host probes leaked):\n%s", stderr)
	}
}

// TestCgroupsCmdHostProbesScopedSkipped: the host probes never run on the
// --scope realm|space|stack|cell paths because the host environment does
// not depend on which sub-tree the operator is inspecting. Pins this
// scoping so a future change can't accidentally start emitting host
// warnings for every scoped invocation.
func TestCgroupsCmdHostProbesScopedSkipped(t *testing.T) {
	root := t.TempDir()
	writeFakeCgroupAt(t, root,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	realmCg := "/kukeon/default"
	writeFakeCgroupAt(t, filepath.Join(root, realmCg),
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	// Both probes WOULD warn loudly if invoked — the test asserts they
	// are NOT invoked on the scoped path.
	restore := cgroupscmd.SetHostProbesForTest(
		func() string { return "host has no swap" },
		func() string { return "host has no userspace OOM guard" },
	)
	defer restore()

	client := &fakeScopedClient{realmExists: true, realmCgroupPath: realmCg}

	_, stderr, err := runCmdWithClient(t, client,
		"--scope", "realm", "default", "--root", root,
	)
	if err != nil {
		t.Fatalf("scope=realm Execute() error = %v on healthy scoped path:\nstderr=%q",
			err, stderr)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("scoped pre-flight emitted host warning (probes must only fire on the unscoped host-root path):\n%s",
			stderr)
	}
}

// TestCgroupsCmdHostProbesFireOnFailure: when the cgroup pre-flight
// itself fails AND the host probes warn, the operator must see both —
// the host warnings are advisory and orthogonal to the cgroup-controller
// failure. Pins the defer-runHostProbes contract so a future refactor
// can't quietly drop warnings on the failing branch.
func TestCgroupsCmdHostProbesFireOnFailure(t *testing.T) {
	// Missing memory + io from subtree_control → cgroup pre-flight fails.
	root := writeFakeCgroup(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)
	restore := cgroupscmd.SetHostProbesForTest(
		func() string { return "host has no swap" },
		func() string { return "host has no userspace OOM guard" },
	)
	defer restore()

	_, stderr, err := runCmd(t, "--root", root, "--no-probe")
	if err == nil {
		t.Fatal("Execute() error = nil with missing memory + io, want cgroup pre-flight failure")
	}
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("stderr missing host warnings on the failing cgroup branch:\n%s", stderr)
	}
	// Cgroup-failure remediation must still appear — host warnings are
	// additive, not a replacement for the cgroup-controller diagnosis.
	for _, want := range []string{"memory", "io", "cgroup.subtree_control"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing cgroup-remediation token %q on failing branch:\n%s", want, stderr)
		}
	}
}

// TestProbeSwapAt covers the file-parsing branches of the swap probe
// directly against synthetic /proc/swaps files in t.TempDir(): unreadable
// (silent), header-only (warns), and header + entry (silent).
func TestProbeSwapAt(t *testing.T) {
	tmp := t.TempDir()

	missing := filepath.Join(tmp, "absent")
	if got := cgroupscmd.ProbeSwapAtForTest(missing); got != "" {
		t.Errorf("missing /proc/swaps: got %q, want empty (silent fallback)", got)
	}

	headerOnly := filepath.Join(tmp, "header-only")
	if err := os.WriteFile(headerOnly, []byte("Filename                                Type            Size            Used            Priority\n"), 0o644); err != nil {
		t.Fatalf("write header-only: %v", err)
	}
	if got := cgroupscmd.ProbeSwapAtForTest(headerOnly); !strings.Contains(got, "no swap") {
		t.Errorf("header-only /proc/swaps: got %q, want a 'no swap' warning", got)
	}

	withEntry := filepath.Join(tmp, "with-entry")
	body := "Filename                                Type            Size            Used            Priority\n" +
		"/dev/zram0                              partition       2097148         0               100\n"
	if err := os.WriteFile(withEntry, []byte(body), 0o644); err != nil {
		t.Fatalf("write with-entry: %v", err)
	}
	if got := cgroupscmd.ProbeSwapAtForTest(withEntry); got != "" {
		t.Errorf("/proc/swaps with one entry: got %q, want empty (swap present)", got)
	}
}

// TestProbeUserspaceOOMAt covers the /proc-walking branches of the OOM
// probe directly against a synthetic /proc tree in t.TempDir(): empty
// (warns), with one of the recognized comms (silent), and with an
// unrecognized comm (warns). Each <pid>/comm carries the kernel's
// trailing newline so the test reflects the real /proc shape.
func TestProbeUserspaceOOMAt(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(procDir string)
		wantWarn bool
	}{
		{
			name:     "empty proc",
			setup:    func(string) {},
			wantWarn: true,
		},
		{
			name: "missing procDir",
			setup: func(procDir string) {
				if err := os.RemoveAll(procDir); err != nil {
					t.Fatalf("remove procDir: %v", err)
				}
			},
			wantWarn: false,
		},
		{
			name: "systemd-oomd present",
			setup: func(procDir string) {
				writeFakeProcessAt(t, procDir, "42", "systemd-oomd")
			},
			wantWarn: false,
		},
		{
			name: "earlyoom present",
			setup: func(procDir string) {
				writeFakeProcessAt(t, procDir, "100", "earlyoom")
			},
			wantWarn: false,
		},
		{
			name: "oomd present",
			setup: func(procDir string) {
				writeFakeProcessAt(t, procDir, "200", "oomd")
			},
			wantWarn: false,
		},
		{
			name: "unrelated process only",
			setup: func(procDir string) {
				writeFakeProcessAt(t, procDir, "1", "init")
			},
			wantWarn: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			procDir := t.TempDir()
			tc.setup(procDir)
			got := cgroupscmd.ProbeUserspaceOOMAtForTest(procDir)
			if tc.wantWarn && got == "" {
				t.Errorf("expected warning, got empty")
			}
			if !tc.wantWarn && got != "" {
				t.Errorf("expected no warning, got %q", got)
			}
		})
	}
}

// writeFakeProcessAt materializes <procDir>/<pid>/comm with the given
// command name plus the trailing newline the kernel always writes, so
// processRunningAt's TrimSpace logic reflects the real /proc shape.
func writeFakeProcessAt(t *testing.T, procDir, pid, comm string) {
	t.Helper()
	dir := filepath.Join(procDir, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
		t.Fatalf("write comm for pid %s: %v", pid, err)
	}
}
