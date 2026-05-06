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
