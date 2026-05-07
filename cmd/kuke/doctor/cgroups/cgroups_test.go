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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cgroupscmd "github.com/eminwux/kukeon/cmd/kuke/doctor/cgroups"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// writeFakeCgroup materializes a directory pretending to be a cgroup-v2
// root: cgroup.controllers + cgroup.subtree_control with the given
// space-separated controller lists.
func writeFakeCgroup(t *testing.T, controllers, subtree string) string {
	t.Helper()
	root := t.TempDir()
	writeFakeCgroupAt(t, root, controllers, subtree)
	return root
}

// writeFakeCgroupAt is like writeFakeCgroup but writes at a caller-supplied
// directory so scoped tests can build a host-root + nested-cgroup-dir
// hierarchy in a single TempDir.
func writeFakeCgroupAt(t *testing.T, dir, controllers, subtree string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.controllers"), []byte(controllers), 0o644); err != nil {
		t.Fatalf("write cgroup.controllers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"), []byte(subtree), 0o644); err != nil {
		t.Fatalf("write cgroup.subtree_control: %v", err)
	}
}

func runCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runCmdWithClient(t, nil, args...)
}

// runCmdWithClient invokes the cgroups command with an optional fake
// kukeonv1.Client injected via the MockControllerKey context value. nil
// client means "no scope; the host-root path is taken".
func runCmdWithClient(t *testing.T, client kukeonv1.Client, args ...string) (string, string, error) {
	t.Helper()
	cmd := cgroupscmd.NewCgroupsCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	ctx := context.Background()
	if client != nil {
		ctx = context.WithValue(ctx, cgroupscmd.MockControllerKey{}, client)
	}
	cmd.SetContext(ctx)
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

// fakeScopedClient embeds FakeClient so only the Get* methods relevant to
// each --scope test are overridden. CgroupPath is the value the daemon
// would have populated on Status; tests join it with --root to point the
// pre-flight at a synthetic cgroup directory.
type fakeScopedClient struct {
	kukeonv1.FakeClient

	realmCgroupPath string
	spaceCgroupPath string
	stackCgroupPath string
	cellCgroupPath  string

	// realm/space/stack/cell metadata gates so tests can simulate "not
	// found" without overriding all four methods.
	realmExists bool
	spaceExists bool
	stackExists bool
	cellExists  bool
}

func (f *fakeScopedClient) GetRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	out := v1beta1.RealmDoc{
		Metadata: doc.Metadata,
		Status:   v1beta1.RealmStatus{CgroupPath: f.realmCgroupPath},
	}
	return kukeonv1.GetRealmResult{
		Realm:          out,
		MetadataExists: f.realmExists,
	}, nil
}

func (f *fakeScopedClient) GetSpace(_ context.Context, doc v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
	out := v1beta1.SpaceDoc{
		Metadata: doc.Metadata,
		Spec:     doc.Spec,
		Status:   v1beta1.SpaceStatus{CgroupPath: f.spaceCgroupPath},
	}
	return kukeonv1.GetSpaceResult{
		Space:          out,
		MetadataExists: f.spaceExists,
	}, nil
}

func (f *fakeScopedClient) GetStack(_ context.Context, doc v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
	out := v1beta1.StackDoc{
		Metadata: doc.Metadata,
		Spec:     doc.Spec,
		Status:   v1beta1.StackStatus{CgroupPath: f.stackCgroupPath},
	}
	return kukeonv1.GetStackResult{
		Stack:          out,
		MetadataExists: f.stackExists,
	}, nil
}

func (f *fakeScopedClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	out := v1beta1.CellDoc{
		Metadata: doc.Metadata,
		Spec:     doc.Spec,
		Status:   v1beta1.CellStatus{CgroupPath: f.cellCgroupPath},
	}
	return kukeonv1.GetCellResult{
		Cell:           out,
		MetadataExists: f.cellExists,
	}, nil
}

// TestCgroupsCmdScopeResolvesAndPasses exercises every --scope kind
// against a synthetic cgroup-v2 hierarchy under one TempDir: the host
// root has the kukeond resource subset, and each named scope has the
// same set fully delegated, so the pre-flight must exit 0 for all four
// scope kinds. This is the resolution-path coverage AC #5 calls for.
func TestCgroupsCmdScopeResolvesAndPasses(t *testing.T) {
	root := t.TempDir()
	// Host-root files keep the unscoped path callable too (defensive —
	// the scoped path joins root with the cgroup-relative path).
	writeFakeCgroupAt(t, root,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	// Synthetic per-scope directories. Cell sits under stack sits under
	// space sits under realm sits under root, mirroring real-world
	// /sys/fs/cgroup/kukeon/<realm>/<space>/<stack>/<cell> layout.
	full := "cpuset cpu io memory hugetlb pids rdma misc"
	enabled := "cpu memory io pids"
	realmCg := "/kukeon/default"
	spaceCg := "/kukeon/default/sp"
	stackCg := "/kukeon/default/sp/st"
	cellCg := "/kukeon/default/sp/st/ce"
	for _, p := range []string{realmCg, spaceCg, stackCg, cellCg} {
		writeFakeCgroupAt(t, filepath.Join(root, p), full, enabled)
	}

	client := &fakeScopedClient{
		realmExists: true, realmCgroupPath: realmCg,
		spaceExists: true, spaceCgroupPath: spaceCg,
		stackExists: true, stackCgroupPath: stackCg,
		cellExists: true, cellCgroupPath: cellCg,
	}

	cases := []struct {
		name string
		args []string
	}{
		{"realm", []string{"--scope", "realm", "default", "--root", root}},
		{"space", []string{"--scope", "space", "sp", "--realm", "default", "--root", root}},
		{"stack", []string{"--scope", "stack", "st", "--realm", "default", "--space", "sp", "--root", root}},
		{"cell", []string{
			"--scope", "cell", "ce",
			"--realm", "default", "--space", "sp", "--stack", "st",
			"--root", root,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runCmdWithClient(t, client, tc.args...)
			if err != nil {
				t.Fatalf("scope=%s Execute() error = %v\nstdout=%q\nstderr=%q",
					tc.name, err, stdout, stderr)
			}
			if stdout != "" {
				t.Errorf("scope=%s stdout = %q, want empty on success", tc.name, stdout)
			}
		})
	}
}

// TestCgroupsCmdScopeFailsOnGap: with a synthetic gap (memory missing
// from the realm's cgroup.subtree_control), --scope realm must exit
// non-zero and the stderr must identify the realm via the new "scope:"
// header line so the operator can tell which sub-tree failed. This is
// the gap-exit coverage AC #5 calls for.
func TestCgroupsCmdScopeFailsOnGap(t *testing.T) {
	root := t.TempDir()
	writeFakeCgroupAt(t, root,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	realmCg := "/kukeon/default"
	// Gap: memory + io present in cgroup.controllers but not delegated
	// onto subtree_control at the realm level.
	writeFakeCgroupAt(t, filepath.Join(root, realmCg),
		"cpuset cpu io memory pids",
		"cpu pids",
	)

	client := &fakeScopedClient{realmExists: true, realmCgroupPath: realmCg}

	_, stderr, err := runCmdWithClient(t, client,
		"--scope", "realm", "default", "--root", root,
	)
	if err == nil {
		t.Fatal("scope=realm gap Execute() error = nil, want error")
	}
	for _, want := range []string{`scope: realm "default"`, "memory", "io", "cgroup.subtree_control"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// TestCgroupsCmdScopeRequiresName: every --scope kind requires a
// positional <name>. The error must say so and not crash inside the
// daemon path.
func TestCgroupsCmdScopeRequiresName(t *testing.T) {
	client := &fakeScopedClient{}
	_, _, err := runCmdWithClient(t, client, "--scope", "realm")
	if err == nil {
		t.Fatal("Execute() error = nil for --scope realm without name, want error")
	}
	if !strings.Contains(err.Error(), "<name>") {
		t.Errorf("error = %q, want it to mention <name>", err.Error())
	}
}

// TestCgroupsCmdScopeRequiresParentFlags: --scope space|stack|cell each
// require their parent flags. Mirrors the same enforcement in
// `kuke get space|stack|cell` so operators learn one rule.
func TestCgroupsCmdScopeRequiresParentFlags(t *testing.T) {
	client := &fakeScopedClient{}
	cases := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{"space-no-realm", []string{"--scope", "space", "sp"}, "--realm"},
		{"stack-no-realm", []string{"--scope", "stack", "st", "--space", "sp"}, "--realm"},
		{"stack-no-space", []string{"--scope", "stack", "st", "--realm", "r"}, "--space"},
		{"cell-no-realm", []string{"--scope", "cell", "c", "--space", "sp", "--stack", "st"}, "--realm"},
		{"cell-no-space", []string{"--scope", "cell", "c", "--realm", "r", "--stack", "st"}, "--space"},
		{"cell-no-stack", []string{"--scope", "cell", "c", "--realm", "r", "--space", "sp"}, "--stack"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runCmdWithClient(t, client, tc.args...)
			if err == nil {
				t.Fatalf("Execute() error = nil for %s, want error", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestCgroupsCmdScopeRejectsUnknownKind: an unknown --scope value must
// fail with a clear "must be one of" message and not silently fall
// through to the host-root path.
func TestCgroupsCmdScopeRejectsUnknownKind(t *testing.T) {
	client := &fakeScopedClient{}
	_, _, err := runCmdWithClient(t, client, "--scope", "container", "x")
	if err == nil {
		t.Fatal("Execute() error = nil for unknown scope, want error")
	}
	if !strings.Contains(err.Error(), "must be one of") {
		t.Errorf("error = %q, want it to mention 'must be one of'", err.Error())
	}
}

// TestCgroupsCmdScopeNotFound: when the daemon does not know the named
// resource, the scoped pre-flight must surface that (and not crash)
// before reading any cgroup file.
func TestCgroupsCmdScopeNotFound(t *testing.T) {
	root := t.TempDir()
	writeFakeCgroupAt(t, root,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	client := &fakeScopedClient{realmExists: false}

	_, _, err := runCmdWithClient(t, client,
		"--scope", "realm", "ghost", "--root", root,
	)
	if err == nil {
		t.Fatal("Execute() error = nil for unknown realm, want error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %q, want it to name the missing realm", err.Error())
	}
}

// TestCgroupsCmdNameWithoutScope: a positional name without --scope is a
// usage mistake — surface it cleanly instead of silently ignoring the
// argument.
func TestCgroupsCmdNameWithoutScope(t *testing.T) {
	root := writeFakeCgroup(t,
		"cpuset cpu io memory hugetlb pids rdma misc",
		"cpu memory io pids",
	)
	_, _, err := runCmd(t, "--root", root, "default")
	if err == nil {
		t.Fatal("Execute() error = nil for name without --scope, want error")
	}
	if !strings.Contains(err.Error(), "--scope") {
		t.Errorf("error = %q, want it to mention --scope", err.Error())
	}
}
