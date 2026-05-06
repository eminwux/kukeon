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
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cgroupscmd "github.com/eminwux/kukeon/cmd/kuke/doctor/cgroups"
)

// writeFakeCgroup materializes a directory pretending to be a cgroup-v2
// root: cgroup.controllers + cgroup.subtree_control with the given
// space-separated controller lists.
func writeFakeCgroup(t *testing.T, controllers, subtree string) string {
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

func runCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := cgroupscmd.NewCgroupsCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// TestCgroupsCmdHappyPath: when every required controller is enabled, the
// command exits 0 and prints nothing to stdout. The dev-init.sh tail
// regression guard relies on this silence.
func TestCgroupsCmdHappyPath(t *testing.T) {
	root := writeFakeCgroup(t,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)

	stdout, stderr, err := runCmd(t, "--root", root)
	if err != nil {
		t.Fatalf("happy-path Execute() error = %v\nstdout=%q\nstderr=%q", err, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("happy-path stdout = %q, want empty (silence keeps dev-init.sh tail clean)", stdout)
	}
}

// TestCgroupsCmdMissingFails: when memory + io are missing from
// subtree_control, the command exits non-zero and the stderr remediation
// names them and the canonical fix.
func TestCgroupsCmdMissingFails(t *testing.T) {
	root := writeFakeCgroup(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)

	_, stderr, err := runCmd(t, "--root", root)
	if err == nil {
		t.Fatal("Execute() error = nil for host missing memory + io, want error")
	}

	for _, want := range []string{"memory", "io", "cgroup.subtree_control"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// TestCgroupsCmdProbeRecoversOnPlainFile: --probe writes "+<ctrl>" to the
// fake subtree_control file. After the write the controllers are present
// in the file and a re-read inside cgroupcheck classifies them as
// EnabledByProbe → command returns success. Exercises the same probe
// path dev-init.sh uses on a misconfigured host.
func TestCgroupsCmdProbeRecoversOnPlainFile(t *testing.T) {
	root := writeFakeCgroup(t,
		"cpuset cpu io memory pids",
		"cpu pids",
	)

	stdout, stderr, err := runCmd(t, "--root", root, "--probe")
	if err != nil {
		t.Fatalf("probe-recover Execute() error = %v\nstdout=%q\nstderr=%q", err, stdout, stderr)
	}
}

// TestCgroupsCmdNestedReadsAdvertised: --nested-cgroup-runtime requires
// the full host-advertised set. Seeding subtree_control with the full
// set must therefore pass the pre-flight (no second source of truth).
func TestCgroupsCmdNestedReadsAdvertised(t *testing.T) {
	full := "cpuset cpu io memory hugetlb pids rdma misc"
	root := writeFakeCgroup(t, full, full)

	if _, stderr, err := runCmd(t, "--root", root, "--nested-cgroup-runtime"); err != nil {
		t.Fatalf("nested happy-path failed: %v\nstderr=%q", err, stderr)
	}
}

// TestCgroupsCmdNestedFailsOnPartialSubtree: with --nested-cgroup-runtime,
// any host-advertised controller missing from subtree_control must fail
// the pre-flight. Confirms the "moving target" handling: when the cell
// adopts NestedCgroupRuntime the required set widens automatically.
func TestCgroupsCmdNestedFailsOnPartialSubtree(t *testing.T) {
	full := "cpuset cpu io memory hugetlb pids rdma misc"
	root := writeFakeCgroup(t, full, "cpu memory io pids")

	_, stderr, err := runCmd(t, "--root", root, "--nested-cgroup-runtime")
	if err == nil {
		t.Fatal("nested partial-subtree Execute() error = nil, want error")
	}
	for _, want := range []string{"cpuset", "hugetlb", "rdma", "misc"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q under --nested-cgroup-runtime:\n%s", want, stderr)
		}
	}
}

func TestCgroupsCmdNoSuchRoot(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "no-such-root")
	_, stderr, err := runCmd(t, "--root", bogus)
	if err == nil {
		t.Fatal("Execute() error = nil for missing host root, want error")
	}
	if !strings.Contains(err.Error(), "cgroup pre-flight") {
		t.Errorf("error message = %q, want it to mention 'cgroup pre-flight'", err.Error())
	}
	_ = stderr
}
