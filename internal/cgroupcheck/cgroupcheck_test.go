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

package cgroupcheck_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/eminwux/kukeon/internal/cgroupcheck"
)

// TestCellResourceControllersStable pins the cell resource subset. The
// controller names ride into the kernel's cgroup.subtree_control writes
// and into provision.go's enableCellControllers; reordering or renaming
// here would silently change the cell-creation path on every host.
func TestCellResourceControllersStable(t *testing.T) {
	got := cgroupcheck.CellResourceControllers()
	want := []string{"cpu", "memory", "io", "pids"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CellResourceControllers() = %v, want %v", got, want)
	}

	// Defensive copy: mutating the returned slice must not leak back
	// into the package-level constant.
	got[0] = "mutated"
	if cgroupcheck.CellResourceControllers()[0] == "mutated" {
		t.Fatal("CellResourceControllers must return a fresh slice; caller mutation leaked into the package")
	}
}

func TestRequiredForKukeondNotNested(t *testing.T) {
	got := cgroupcheck.RequiredForKukeond(false, []string{"cpuset", "cpu", "io", "memory", "hugetlb", "pids"})
	want := cgroupcheck.CellResourceControllers()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RequiredForKukeond(nested=false) = %v, want resource subset %v", got, want)
	}
}

func TestRequiredForKukeondNested(t *testing.T) {
	advertised := []string{"cpuset", "cpu", "io", "memory", "hugetlb", "pids", "rdma", "misc"}
	got := cgroupcheck.RequiredForKukeond(true, advertised)
	if !reflect.DeepEqual(got, advertised) {
		t.Fatalf("RequiredForKukeond(nested=true) = %v, want %v", got, advertised)
	}
	got[0] = "mutated"
	if advertised[0] == "mutated" {
		t.Fatal("RequiredForKukeond must copy hostAdvertised; caller mutation leaked into the input slice")
	}
}

// writeCgroupFiles materializes a fake host root cgroup hierarchy: a
// directory with cgroup.controllers and cgroup.subtree_control populated
// from the given strings. Mirrors what /sys/fs/cgroup looks like to
// readControllers without requiring a real cgroup-v2 mount.
func writeCgroupFiles(t *testing.T, controllers, subtree string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte(controllers), 0o644); err != nil {
		t.Fatalf("write cgroup.controllers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte(subtree), 0o644); err != nil {
		t.Fatalf("write cgroup.subtree_control: %v", err)
	}
	return root
}

// writeCgroupType seeds a cgroup.type file in an existing fake host root
// so the threaded-subtree classifier reads the value the test wants.
func writeCgroupType(t *testing.T, root, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "cgroup.type"), []byte(value+"\n"), 0o644); err != nil {
		t.Fatalf("write cgroup.type: %v", err)
	}
}

func TestCheckAllEnabled(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), nil)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	if !res.OK() {
		t.Fatalf("Check() OK=false on fully-enabled host; unresolved=%v", res.Unresolved())
	}
	for _, c := range cgroupcheck.CellResourceControllers() {
		if got, want := res.Status[c], cgroupcheck.StatusEnabled; got != want {
			t.Errorf("Status[%q] = %v, want %v", c, got, want)
		}
	}
	if msg := cgroupcheck.FormatRemediation(res); msg != "" {
		t.Errorf("FormatRemediation on OK result = %q, want empty", msg)
	}
}

func TestCheckMissingFromSubtreeNoProbe(t *testing.T) {
	// memory and io are advertised but not yet enabled into subtree_control.
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), nil)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	if res.OK() {
		t.Fatalf("Check() OK=true with memory/io missing from subtree_control")
	}
	for _, c := range []string{"memory", "io"} {
		if got, want := res.Status[c], cgroupcheck.StatusNeedsDelegation; got != want {
			t.Errorf("Status[%q] = %v, want %v", c, got, want)
		}
	}
	for _, c := range []string{"cpu", "pids"} {
		if got, want := res.Status[c], cgroupcheck.StatusEnabled; got != want {
			t.Errorf("Status[%q] = %v, want %v", c, got, want)
		}
	}
}

func TestCheckKernelMissing(t *testing.T) {
	// Kernel built without the io controller; cgroup.controllers will
	// not list it even at the host root.
	root := writeCgroupFiles(t,
		"cpuset cpu memory pids",
		"cpu memory pids",
	)

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), nil)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	if got, want := res.Status["io"], cgroupcheck.StatusKernelMissing; got != want {
		t.Errorf("Status[io] = %v, want %v", got, want)
	}
	if res.OK() {
		t.Fatal("Check() OK=true with io missing from cgroup.controllers")
	}
	msg := cgroupcheck.FormatRemediation(res)
	if !strings.Contains(msg, "kernel does not support") {
		t.Errorf("FormatRemediation missing 'kernel does not support' classification:\n%s", msg)
	}
	// The fix line must NOT include +io — operators can't enable a
	// controller the kernel doesn't expose.
	if strings.Contains(msg, "+io") {
		t.Errorf("FormatRemediation suggests `+io` for a kernel-missing controller:\n%s", msg)
	}
}

func TestCheckProbeSuccess(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)

	probedFor := map[string]int{}
	probe := func(_, ctrl string) error {
		probedFor[ctrl]++
		return nil
	}

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	if !res.OK() {
		t.Fatalf("Check() OK=false after successful probe; unresolved=%v", res.Unresolved())
	}
	for _, c := range []string{"memory", "io"} {
		if got, want := res.Status[c], cgroupcheck.StatusEnabledByProbe; got != want {
			t.Errorf("Status[%q] = %v, want %v", c, got, want)
		}
		if probedFor[c] != 1 {
			t.Errorf("probe called %d times for %q, want exactly once", probedFor[c], c)
		}
	}
	// Already-enabled controllers must not be probed — that would be a
	// pointless host write on the happy path.
	for _, c := range []string{"cpu", "pids"} {
		if probedFor[c] != 0 {
			t.Errorf("probe called for already-enabled %q (count=%d); should be skipped", c, probedFor[c])
		}
	}
}

func TestCheckProbeEOPNOTSUPP(t *testing.T) {
	// The cgroup-namespace trap from the issue: cgroup.controllers
	// advertises memory + io but the actual parent never delegated them,
	// so the kernel returns EOPNOTSUPP on the write.
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)

	probe := func(_, ctrl string) error {
		if ctrl == "memory" || ctrl == "io" {
			return syscall.EOPNOTSUPP
		}
		return nil
	}

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	for _, c := range []string{"memory", "io"} {
		if got, want := res.Status[c], cgroupcheck.StatusNotDelegated; got != want {
			t.Errorf("Status[%q] = %v, want %v", c, got, want)
		}
	}
	if res.OK() {
		t.Fatal("Check() OK=true after EOPNOTSUPP probe failures")
	}
	msg := cgroupcheck.FormatRemediation(res)
	for _, want := range []string{
		"parent did not delegate",
		"cgroup-namespace trap",
		"memory, io",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("FormatRemediation missing %q:\n%s", want, msg)
		}
	}
}

// TestCheckProbeThreadedSubtreeDomainOnly: when cgroup.type is "domain
// threaded" and the probe write returns EOPNOTSUPP for a domain-only
// controller (memory, io), the classifier must report
// StatusThreadedSubtree — not StatusNotDelegated. The remediation must
// name the threaded-subtree case and must not include the misleading
// "escalate to the parent runtime" footer for the affected controllers
// (the +<ctrl> tee fix line excludes them).
func TestCheckProbeThreadedSubtreeDomainOnly(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)
	writeCgroupType(t, root, "domain threaded")

	probe := func(_, ctrl string) error {
		if ctrl == "memory" || ctrl == "io" {
			return syscall.EOPNOTSUPP
		}
		return nil
	}

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	for _, c := range []string{"memory", "io"} {
		if got, want := res.Status[c], cgroupcheck.StatusThreadedSubtree; got != want {
			t.Errorf("Status[%q] = %v, want %v", c, got, want)
		}
	}
	if res.OK() {
		t.Fatal("Check() OK=true with threaded-subtree EOPNOTSUPP failures")
	}

	msg := cgroupcheck.FormatRemediation(res)
	for _, want := range []string{
		"threaded-subtree forbids domain-only controllers",
		"memory, io",
		`"domain threaded"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("FormatRemediation missing %q:\n%s", want, msg)
		}
	}
	// The threaded-subtree fix is not "+<ctrl> | sudo tee" — those
	// writes will EOPNOTSUPP again. The fix line must omit the
	// threaded-subtree controllers so the suggestion stays correct.
	if strings.Contains(msg, "+memory") || strings.Contains(msg, "+io") {
		t.Errorf("FormatRemediation suggests +memory/+io tee fix for threaded-subtree controllers:\n%s", msg)
	}
	// And the threaded-subtree class must not be misclassified as the
	// namespace trap, which would lead the operator to escalate
	// pointlessly to the host/container runtime.
	if strings.Contains(msg, "cgroup-namespace trap") {
		t.Errorf("FormatRemediation surfaces cgroup-namespace trap stanza for threaded-subtree-only failures:\n%s", msg)
	}
}

// TestCheckProbeThreadedTypeAlone: cgroup.type "threaded" (the cgroup is
// itself a threaded child, not just a domain root with threaded
// children) is the second value the kernel uses to gate domain-only
// controllers. Pin the same classification path so a future regression
// that only handles "domain threaded" still trips this test.
func TestCheckProbeThreadedTypeAlone(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)
	writeCgroupType(t, root, "threaded")

	probe := func(_, ctrl string) error {
		if ctrl == "memory" || ctrl == "io" {
			return syscall.EOPNOTSUPP
		}
		return nil
	}

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	for _, c := range []string{"memory", "io"} {
		if got, want := res.Status[c], cgroupcheck.StatusThreadedSubtree; got != want {
			t.Errorf("Status[%q] = %v, want %v under cgroup.type=threaded", c, got, want)
		}
	}
}

// TestCheckProbeThreadedTypeThreadAwareControllerStaysNotDelegated: a
// thread-aware controller (cpu, pids, ...) failing with EOPNOTSUPP on a
// "domain threaded" cgroup is a real namespace trap, not the
// threaded-subtree case — the kernel does allow these in a threaded
// subtree. Misclassifying here would suppress the correct escalation
// hint, so pin the boundary.
func TestCheckProbeThreadedTypeThreadAwareControllerStaysNotDelegated(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"memory io",
	)
	writeCgroupType(t, root, "domain threaded")

	probe := func(_, ctrl string) error {
		if ctrl == "cpu" || ctrl == "pids" {
			return syscall.EOPNOTSUPP
		}
		return nil
	}

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	for _, c := range []string{"cpu", "pids"} {
		if got, want := res.Status[c], cgroupcheck.StatusNotDelegated; got != want {
			t.Errorf("Status[%q] = %v, want %v (thread-aware controller in threaded subtree → namespace trap)",
				c, got, want)
		}
	}
}

// TestCheckProbeMissingCgroupTypeFallsBackToNotDelegated: the
// classifier's cgroup.type read must fail soft — when the file is
// absent (e.g. tempdir-backed fakes that don't set it), an EOPNOTSUPP
// probe write stays StatusNotDelegated rather than getting reclassified.
// This pins the conservative-fallback contract so a future "treat
// missing cgroup.type as threaded" change can't silently break the
// existing namespace-trap diagnosis on hosts where cgroup.type is
// genuinely unreadable.
func TestCheckProbeMissingCgroupTypeFallsBackToNotDelegated(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)
	// Deliberately do not write cgroup.type.

	probe := func(_, ctrl string) error {
		if ctrl == "memory" || ctrl == "io" {
			return syscall.EOPNOTSUPP
		}
		return nil
	}

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	for _, c := range []string{"memory", "io"} {
		if got, want := res.Status[c], cgroupcheck.StatusNotDelegated; got != want {
			t.Errorf("Status[%q] = %v, want %v with cgroup.type absent", c, got, want)
		}
	}
}

// TestStatusThreadedSubtreeString pins the human-readable label so
// downstream parsers / dev-init.sh greps that key on the label stay
// stable.
func TestStatusThreadedSubtreeString(t *testing.T) {
	if got, want := cgroupcheck.StatusThreadedSubtree.String(), "threaded-subtree"; got != want {
		t.Errorf("StatusThreadedSubtree.String() = %q, want %q", got, want)
	}
}

// TestStatusInternalProcessString pins the human-readable label for the
// EBUSY (no-internal-process) class. Same reason as the threaded-subtree
// pinning: downstream greps key on the label string.
func TestStatusInternalProcessString(t *testing.T) {
	if got, want := cgroupcheck.StatusInternalProcess.String(), "internal-process"; got != want {
		t.Errorf("StatusInternalProcess.String() = %q, want %q", got, want)
	}
}

// TestCheckProbeEBUSY: the no-internal-process trap from issue #335. The
// kernel returns EBUSY when subtree_control is asked to enable
// non-thread-aware controllers on a non-leaf cgroup that contains
// processes in its own cgroup.procs. The classifier must report
// StatusInternalProcess — not StatusNotDelegated. The remediation must
// name the no-internal-process rule, must not advertise the misleading
// "escalate to the parent runtime" footer for the affected controllers
// (the +<ctrl> tee fix line excludes them), and must not surface the
// cgroup-namespace trap stanza.
func TestCheckProbeEBUSY(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)

	probe := func(_, ctrl string) error {
		if ctrl == "memory" || ctrl == "io" {
			return syscall.EBUSY
		}
		return nil
	}

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	for _, c := range []string{"memory", "io"} {
		if got, want := res.Status[c], cgroupcheck.StatusInternalProcess; got != want {
			t.Errorf("Status[%q] = %v, want %v", c, got, want)
		}
	}
	if res.OK() {
		t.Fatal("Check() OK=true after EBUSY probe failures")
	}

	msg := cgroupcheck.FormatRemediation(res)
	for _, want := range []string{
		"no-internal-process rule",
		"memory, io",
		"move the",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("FormatRemediation missing %q:\n%s", want, msg)
		}
	}
	// The internal-process fix is not "+<ctrl> | sudo tee" — those writes
	// will EBUSY again until the operator first moves the contained
	// processes out. The fix line must omit the affected controllers so
	// the suggestion stays correct.
	if strings.Contains(msg, "+memory") || strings.Contains(msg, "+io") {
		t.Errorf("FormatRemediation suggests +memory/+io tee fix for internal-process controllers:\n%s", msg)
	}
	// And the internal-process class must not be misclassified as the
	// namespace trap, which would lead the operator to escalate
	// pointlessly to the host/container runtime.
	if strings.Contains(msg, "cgroup-namespace trap") {
		t.Errorf("FormatRemediation surfaces cgroup-namespace trap stanza for internal-process-only failures:\n%s", msg)
	}
}

func TestCheckProbePermissionDenied(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)

	probe := func(_, _ string) error { return syscall.EACCES }

	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	for _, c := range []string{"memory", "io"} {
		if got, want := res.Status[c], cgroupcheck.StatusPermissionDenied; got != want {
			t.Errorf("Status[%q] = %v, want %v", c, got, want)
		}
	}
	if !strings.Contains(cgroupcheck.FormatRemediation(res), "permission denied") {
		t.Errorf("FormatRemediation missing permission-denied classification:\n%s", cgroupcheck.FormatRemediation(res))
	}
}

func TestCheckMissingHostRoot(t *testing.T) {
	_, err := cgroupcheck.Check(filepath.Join(t.TempDir(), "does-not-exist"),
		cgroupcheck.CellResourceControllers(), nil)
	if err == nil {
		t.Fatal("Check() error = nil for missing host root, want error")
	}
}

// TestCheckUnresolvedOrderMatchesRequired pins the order of Unresolved()
// against the caller-supplied required slice. The remediation message
// repeats this order; reordering would surprise downstream consumers.
func TestCheckUnresolvedOrderMatchesRequired(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"",
	)

	required := []string{"pids", "io", "cpu", "memory"}
	res, err := cgroupcheck.Check(root, required, nil)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	if !reflect.DeepEqual(res.Unresolved(), required) {
		t.Fatalf("Unresolved() = %v, want %v (order must match Required)", res.Unresolved(), required)
	}
}

// TestDefaultProberWritesPlusController exercises the production prober
// against an ordinary tempdir file (not a real cgroupfs). The write must
// land "+<ctrl>" verbatim — cgroupfs interprets the line additively, so
// the prober must not pad with newlines or other framing that would
// change semantics on a real subtree_control file.
// TestOnlyInternalProcessAllStatusInternalProcess: every unresolved
// controller is StatusInternalProcess → the doctor's self-heal downgrade
// can fire. Pins the all-EBUSY case dev-init.sh hits on a
// NestedCgroupRuntime sandbox.
func TestOnlyInternalProcessAllStatusInternalProcess(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)
	probe := func(_, ctrl string) error {
		if ctrl == "memory" || ctrl == "io" {
			return syscall.EBUSY
		}
		return nil
	}
	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	if !res.OnlyInternalProcess() {
		t.Fatalf("OnlyInternalProcess() = false on all-EBUSY result; unresolved=%v", res.Unresolved())
	}
}

// TestOnlyInternalProcessMixedReturnsFalse: when at least one unresolved
// controller is not StatusInternalProcess (e.g. kernel-missing), the
// runtime drain cannot self-heal it; the doctor must keep the failure
// fatal. Pins the boundary so a future change can't silently downgrade
// mixed-class failures.
func TestOnlyInternalProcessMixedReturnsFalse(t *testing.T) {
	// memory missing from cgroup.controllers (kernel-missing) AND io
	// EBUSY on probe (internal-process). Mixed class → must return false.
	root := writeCgroupFiles(t,
		"cpuset cpu io pids",
		"cpu pids",
	)
	probe := func(_, ctrl string) error {
		if ctrl == "io" {
			return syscall.EBUSY
		}
		return nil
	}
	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), probe)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	if res.OnlyInternalProcess() {
		t.Fatalf("OnlyInternalProcess() = true on mixed-class result; unresolved=%v", res.Unresolved())
	}
}

// TestOnlyInternalProcessFullyResolvedReturnsFalse: a result with no
// unresolved controllers is not "only internal-process" — there's
// nothing to downgrade. Otherwise the doctor's downgrade branch could
// fire on a happy-path result and emit a misleading warning.
func TestOnlyInternalProcessFullyResolvedReturnsFalse(t *testing.T) {
	root := writeCgroupFiles(t,
		"cpuset cpu io memory pids",
		"cpu memory io pids",
	)
	res, err := cgroupcheck.Check(root, cgroupcheck.CellResourceControllers(), nil)
	if err != nil {
		t.Fatalf("Check() unexpected error: %v", err)
	}
	if !res.OK() {
		t.Fatalf("Check() OK=false on fully-enabled host; unresolved=%v", res.Unresolved())
	}
	if res.OnlyInternalProcess() {
		t.Errorf("OnlyInternalProcess() = true on OK result; want false")
	}
}

// TestIsHostRootPopulatedReadsCgroupEvents: the populated half of the
// self-heal fingerprint reads <hostRoot>/cgroup.events and keys on the
// "populated 1" line. Pins both the file location and the exact line
// match so a future format change in the kernel surface (or a bad
// refactor) trips the test instead of silently breaking the gate.
func TestIsHostRootPopulatedReadsCgroupEvents(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"populated-1", "populated 1\nfrozen 0\n", true},
		{"populated-0", "populated 0\nfrozen 0\n", false},
		{"missing-key", "frozen 0\n", false},
		{"empty-file", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "cgroup.events"), []byte(tc.content), 0o644); err != nil {
				t.Fatalf("seed cgroup.events: %v", err)
			}
			if got := cgroupcheck.IsHostRootPopulated(dir); got != tc.want {
				t.Errorf("IsHostRootPopulated(%q) = %v, want %v\ncontent=%q", dir, got, tc.want, tc.content)
			}
		})
	}
}

// TestIsHostRootPopulatedMissingFileReturnsFalse: a missing
// cgroup.events (e.g. a non-cgroupfs path) must not error — the
// conservative-fallback contract returns false so the doctor's
// self-heal branch does not fire on a path that isn't even a cgroup.
func TestIsHostRootPopulatedMissingFileReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	if got := cgroupcheck.IsHostRootPopulated(dir); got != false {
		t.Errorf("IsHostRootPopulated on missing file = %v, want false", got)
	}
}

func TestDefaultProberWritesPlusController(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cgroup.subtree_control")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed subtree_control: %v", err)
	}
	if err := cgroupcheck.DefaultProber(root, "memory"); err != nil {
		t.Fatalf("DefaultProber error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "+memory" {
		t.Errorf("subtree_control after DefaultProber = %q, want %q", string(got), "+memory")
	}
}
