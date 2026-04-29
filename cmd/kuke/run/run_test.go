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

package run_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	runcmd "github.com/eminwux/kukeon/cmd/kuke/run"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshattach "github.com/eminwux/sbsh/pkg/attach"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const validCellYAML = `apiVersion: v1beta1
kind: Cell
metadata:
  name: my-cell
spec:
  id: my-cell
  realmId: my-realm
  spaceId: my-space
  stackId: my-stack
  containers:
    - id: root
      root: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
    - id: work
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
`

// cellYAMLNoLocation omits realmId/spaceId/stackId so the flag/default fallback
// path is exercised. It still satisfies the parser's required fields by setting
// the spec.containers entry the validator demands.
const cellYAMLNoLocation = `apiVersion: v1beta1
kind: Cell
metadata:
  name: bare-cell
spec:
  id: bare-cell
  realmId: ""
  spaceId: ""
  stackId: ""
  containers:
    - id: root
      root: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
`

const multiDocYAML = validCellYAML + "\n---\n" + validCellYAML

const realmDocYAML = `apiVersion: v1beta1
kind: Realm
metadata:
  name: realm-only
spec:
  namespace: realm-only
`

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return path
}

type fakeClient struct {
	kukeonv1.FakeClient

	getCellFn         func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	createCellFn      func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error)
	attachContainerFn func(doc v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error)

	getCalls    int
	createCalls int
	attachCalls int
	createDoc   v1beta1.CellDoc
	attachDoc   v1beta1.ContainerDoc
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	f.getCalls++
	if f.getCellFn == nil {
		return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) CreateCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	f.createCalls++
	f.createDoc = doc
	if f.createCellFn == nil {
		return kukeonv1.CreateCellResult{}, errors.New("unexpected CreateCell call")
	}
	return f.createCellFn(doc)
}

func (f *fakeClient) AttachContainer(
	_ context.Context,
	doc v1beta1.ContainerDoc,
) (kukeonv1.AttachContainerResult, error) {
	f.attachCalls++
	f.attachDoc = doc
	if f.attachContainerFn == nil {
		return kukeonv1.AttachContainerResult{}, errors.New("unexpected AttachContainer call")
	}
	return f.attachContainerFn(doc)
}

// runCapture records the Options passed to the in-process attach loop and
// returns nil so the test treats the call as a clean detach.
type runCapture struct {
	calls int
	opts  sbshattach.Options
}

func (r *runCapture) fn(_ context.Context, opts sbshattach.Options) error {
	r.calls++
	r.opts = opts
	return nil
}

func newCmd(t *testing.T, fc *fakeClient) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	return newCmdWithRun(t, fc, nil)
}

func newCmdWithRun(t *testing.T, fc *fakeClient, run *runCapture) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := runcmd.NewRunCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	if fc != nil {
		ctx = context.WithValue(ctx, runcmd.MockControllerKey{}, kukeonv1.Client(fc))
	}
	if run != nil {
		ctx = context.WithValue(ctx, runcmd.MockRunKey{}, runcmd.RunFn(run.fn))
	}
	cmd.SetContext(ctx)
	return cmd, buf
}

func successCreateResult(doc v1beta1.CellDoc) kukeonv1.CreateCellResult {
	return kukeonv1.CreateCellResult{
		Cell:                    doc,
		Created:                 true,
		MetadataExistsPost:      true,
		CgroupCreated:           true,
		CgroupExistsPost:        true,
		RootContainerCreated:    true,
		RootContainerExistsPost: true,
		Started:                 true,
		Containers: []kukeonv1.ContainerCreationOutcome{
			{Name: "root", ExistsPost: true, Created: true},
			{Name: "work", ExistsPost: true, Created: true},
		},
	}
}

func TestRun_FromFile_CreatesAndStarts(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	if got := fc.createDoc.Spec.RealmID; got != "my-realm" {
		t.Errorf("RealmID=%q want my-realm", got)
	}
	if got := fc.createDoc.Spec.SpaceID; got != "my-space" {
		t.Errorf("SpaceID=%q want my-space", got)
	}
	if got := fc.createDoc.Spec.StackID; got != "my-stack" {
		t.Errorf("StackID=%q want my-stack", got)
	}
	wantSubstrs := []string{
		`Cell "my-cell" (realm "my-realm", space "my-space", stack "my-stack")`,
		"  - metadata: created",
		"  - cgroup: created",
		"  - root container: created",
		`  - container "root": created`,
		`  - container "work": created`,
		"  - containers: started",
	}
	for _, want := range wantSubstrs {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q\nGot:\n%s", want, out.String())
		}
	}
}

func TestRun_FlagFallback_WhenDocOmitsLocation(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, cellYAMLNoLocation),
		"--realm", "flag-realm",
		"--space", "flag-space",
		"--stack", "flag-stack",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createDoc.Spec.RealmID; got != "flag-realm" {
		t.Errorf("RealmID=%q want flag-realm", got)
	}
	if got := fc.createDoc.Spec.SpaceID; got != "flag-space" {
		t.Errorf("SpaceID=%q want flag-space", got)
	}
	if got := fc.createDoc.Spec.StackID; got != "flag-stack" {
		t.Errorf("StackID=%q want flag-stack", got)
	}
}

func TestRun_DefaultLocation_WhenDocAndFlagsOmit(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, cellYAMLNoLocation)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, got := range []string{
		fc.createDoc.Spec.RealmID,
		fc.createDoc.Spec.SpaceID,
		fc.createDoc.Spec.StackID,
	} {
		if got != "default" {
			t.Errorf("location=%q want %q (session default)", got, "default")
		}
	}
}

func TestRun_DocLocationWinsOverFlag(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, validCellYAML),
		"--realm", "ignored", "--space", "ignored", "--stack", "ignored",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createDoc.Spec.RealmID; got != "my-realm" {
		t.Errorf("RealmID=%q want my-realm (doc must win over --realm)", got)
	}
}

func TestRun_MultiDoc_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, multiDocYAML)})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want multi-doc error")
	}
	if !strings.Contains(err.Error(), "single Cell document") {
		t.Errorf("err=%q missing multi-doc explanation", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0", fc.createCalls)
	}
}

func TestRun_NonCellKind_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, realmDocYAML)})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want non-Cell-kind error")
	}
	if !strings.Contains(err.Error(), `expected kind "Cell"`) {
		t.Errorf("err=%q missing kind explanation", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0", fc.createCalls)
	}
}

func TestRun_ExistingCell_MatchingSpec_AlreadyReady_ShortCircuits(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Cell exists, spec matches the file, runtime state is Ready. Run must
	// short-circuit *without* calling CreateCell — re-entering the daemon's
	// create-and-start path on a healthy cell trips the runner's
	// CNI-duplicate-allocation bug (#TODO file).
	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true, Image: "registry.eminwux.com/busybox:latest"},
				{ID: "work", Image: "registry.eminwux.com/busybox:latest"},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                existing,
				MetadataExists:      true,
				CgroupExists:        true,
				RootContainerExists: true,
			}, nil
		},
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 0 {
		t.Fatalf("CreateCell calls=%d want 0 (short-circuit on Ready)", fc.createCalls)
	}
	if !strings.Contains(out.String(), "  - metadata: already existed") {
		t.Errorf("output missing 'already existed' marker:\n%s", out.String())
	}
}

func TestRun_ExistingCell_MatchingSpec_NotReady_StillEnsures(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Cell exists with matching spec but its runtime state is not Ready
	// (Pending/Stopped). Run must fall through to CreateCell so the daemon
	// can ensure resources and start containers.
	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true, Image: "registry.eminwux.com/busybox:latest"},
				{ID: "work", Image: "registry.eminwux.com/busybox:latest"},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                existing,
				MetadataExists:      true,
				CgroupExists:        true,
				RootContainerExists: true,
			}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return kukeonv1.CreateCellResult{
				Cell:                    doc,
				Created:                 false,
				MetadataExistsPost:      true,
				CgroupExistsPost:        true,
				RootContainerExistsPost: true,
				Started:                 true,
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1 (must ensure+start when not Ready)", fc.createCalls)
	}
}

func TestRun_ExistingCell_DivergingContainerSet_RefusesAndPointsToApply(t *testing.T) {
	t.Cleanup(viper.Reset)

	// On-disk cell has an extra container the file does not declare. This is
	// the structural drift (container set / count) the AC routes through
	// `kuke apply -f`. We deliberately do NOT compare container images/env —
	// the runner rewrites those during create, so a deep diff would flag a
	// fresh cell as divergent on the next run.
	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true, Image: "registry.eminwux.com/busybox:latest"},
				{ID: "work", Image: "registry.eminwux.com/busybox:latest"},
				{ID: "extra", Image: "registry.eminwux.com/busybox:latest"},
			},
		},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: existing, MetadataExists: true}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML)})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want divergence error")
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("err=%q does not refer the operator to `kuke apply -f`", err)
	}
	if !strings.Contains(err.Error(), "spec.containers (count:") {
		t.Errorf("err=%q does not name the diverging field", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (must not mutate on divergence)", fc.createCalls)
	}
}

func TestRun_OutputJSON(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`"name": "my-cell"`,
		`"realmId": "my-realm"`,
		`"created": true`,
		`"started": true`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("json output missing %q\nGot:\n%s", want, got)
		}
	}
}

func TestRun_OutputYAML(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-o", "yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"name: my-cell",
		"realmId: my-realm",
		"created: true",
		"started: true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml output missing %q\nGot:\n%s", want, got)
		}
	}
}

func TestRun_InvalidOutput_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-o", "table"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --output") {
		t.Fatalf("err=%v want 'invalid --output ...'", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell called=%d want 0", fc.createCalls)
	}
}

func TestRun_MissingFileAndProfile_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	// MarkFlagsOneRequired produces "at least one of the flags in the group
	// [file profile] is required" — match on the stable phrase rather than
	// the exact wording so a cobra phrasing change doesn't break the test.
	if err == nil || !strings.Contains(err.Error(), "[file profile]") {
		t.Fatalf("err=%v want one-of error naming both flags", err)
	}
}

func TestNewRunCmd_AutocompleteRegistration(t *testing.T) {
	cmd := runcmd.NewRunCmd()
	for _, flag := range []string{"realm", "space", "stack"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("expected %q flag to exist", flag)
		}
	}
	fileFlag := cmd.Flags().Lookup("file")
	if fileFlag == nil || fileFlag.Shorthand != "f" {
		t.Errorf("expected -f/--file flag, got %+v", fileFlag)
	}
	profileFlag := cmd.Flags().Lookup("profile")
	if profileFlag == nil || profileFlag.Shorthand != "p" {
		t.Errorf("expected -p/--profile flag, got %+v", profileFlag)
	}
	outputFlag := cmd.Flags().Lookup("output")
	if outputFlag == nil || outputFlag.Shorthand != "o" {
		t.Errorf("expected -o/--output flag, got %+v", outputFlag)
	}
}

// attachableCellYAML declares two attachable containers and pins
// cell.tty.default to the second one so the precedence-rule tests below can
// distinguish the explicit-default branch from the first-attachable branch.
const attachableCellYAML = `apiVersion: v1beta1
kind: Cell
metadata:
  name: my-cell
spec:
  id: my-cell
  realmId: my-realm
  spaceId: my-space
  stackId: my-stack
  tty:
    default: claude
  containers:
    - id: root
      root: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
    - id: shell
      attachable: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
    - id: claude
      attachable: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
`

// attachableNoDefaultYAML omits cell.tty.default so the first-attachable
// fallback branch fires.
const attachableNoDefaultYAML = `apiVersion: v1beta1
kind: Cell
metadata:
  name: my-cell
spec:
  id: my-cell
  realmId: my-realm
  spaceId: my-space
  stackId: my-stack
  containers:
    - id: root
      root: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
    - id: shell
      attachable: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
    - id: claude
      attachable: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
`

const testHostSocket = "/opt/kukeon/r/s/st/c/work/tty/socket"

func attachSuccessFn() func(v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
	return func(_ v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
		return kukeonv1.AttachContainerResult{HostSocketPath: testHostSocket}, nil
	}
}

func TestPickAttachTarget_PrefersExplicitContainer(t *testing.T) {
	spec := v1beta1.CellSpec{
		Tty: &v1beta1.CellTty{Default: "claude"},
		Containers: []v1beta1.ContainerSpec{
			{ID: "shell", Attachable: true},
			{ID: "claude", Attachable: true},
		},
	}
	got, err := runcmd.PickAttachTarget(spec, "my-cell", "shell")
	if err != nil {
		t.Fatalf("pickAttachTarget: %v", err)
	}
	if got != "shell" {
		t.Errorf("got %q, want %q (--container must beat tty.default)", got, "shell")
	}
}

func TestPickAttachTarget_FallsBackToTtyDefault(t *testing.T) {
	spec := v1beta1.CellSpec{
		Tty: &v1beta1.CellTty{Default: "claude"},
		Containers: []v1beta1.ContainerSpec{
			{ID: "shell", Attachable: true},
			{ID: "claude", Attachable: true},
		},
	}
	got, err := runcmd.PickAttachTarget(spec, "my-cell", "")
	if err != nil {
		t.Fatalf("pickAttachTarget: %v", err)
	}
	if got != "claude" {
		t.Errorf("got %q, want %q (tty.default must beat first-attachable)", got, "claude")
	}
}

func TestPickAttachTarget_FallsBackToFirstAttachable(t *testing.T) {
	spec := v1beta1.CellSpec{
		Containers: []v1beta1.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "shell", Attachable: true},
			{ID: "claude", Attachable: true},
		},
	}
	got, err := runcmd.PickAttachTarget(spec, "my-cell", "")
	if err != nil {
		t.Fatalf("pickAttachTarget: %v", err)
	}
	if got != "shell" {
		t.Errorf("got %q, want %q (first attachable wins, declaration order)", got, "shell")
	}
}

func TestPickAttachTarget_NoAttachable_Errors(t *testing.T) {
	spec := v1beta1.CellSpec{
		Containers: []v1beta1.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "side", Attachable: false},
		},
	}
	_, err := runcmd.PickAttachTarget(spec, "my-cell", "")
	if !errors.Is(err, errdefs.ErrAttachNoCandidate) {
		t.Fatalf("err=%v want ErrAttachNoCandidate", err)
	}
	if !strings.Contains(err.Error(), "attachable: true") {
		t.Errorf("error %q must guide operator to declare attachable=true", err)
	}
}

func TestPickAttachTarget_ExplicitNonAttachable_NamesAttachables(t *testing.T) {
	spec := v1beta1.CellSpec{
		Containers: []v1beta1.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "side", Attachable: false},
			{ID: "shell", Attachable: true},
		},
	}
	_, err := runcmd.PickAttachTarget(spec, "my-cell", "side")
	if !errors.Is(err, errdefs.ErrAttachNotSupported) {
		t.Fatalf("err=%v want ErrAttachNotSupported", err)
	}
	if !strings.Contains(err.Error(), "shell") {
		t.Errorf("error %q must list available attachables", err)
	}
}

func TestPickAttachTarget_ExplicitUnknown_NamesAttachables(t *testing.T) {
	spec := v1beta1.CellSpec{
		Containers: []v1beta1.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "shell", Attachable: true},
		},
	}
	_, err := runcmd.PickAttachTarget(spec, "my-cell", "ghost")
	if !errors.Is(err, errdefs.ErrContainerNotFound) {
		t.Fatalf("err=%v want ErrContainerNotFound", err)
	}
	if !strings.Contains(err.Error(), "shell") {
		t.Errorf("error %q must list available attachables", err)
	}
}

func TestRun_Attach_AfterCreate_UsesTtyDefault(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "-a"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	if fc.attachCalls != 1 {
		t.Fatalf("AttachContainer calls=%d want 1", fc.attachCalls)
	}
	if got := fc.attachDoc.Metadata.Name; got != "claude" {
		t.Errorf("AttachContainer target=%q want claude (tty.default)", got)
	}
	if run.calls != 1 {
		t.Fatalf("attach loop calls=%d want 1", run.calls)
	}
	if run.opts.SocketPath != testHostSocket {
		t.Errorf("SocketPath=%q want %q", run.opts.SocketPath, testHostSocket)
	}
}

func TestRun_Attach_AfterCreate_FirstAttachableWhenNoDefault(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableNoDefaultYAML), "-a"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.attachDoc.Metadata.Name; got != "shell" {
		t.Errorf("AttachContainer target=%q want shell (first attachable)", got)
	}
}

func TestRun_Attach_AfterCreate_ExplicitContainerWinsOverDefault(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, attachableCellYAML),
		"-a", "--container", "shell",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.attachDoc.Metadata.Name; got != "shell" {
		t.Errorf("AttachContainer target=%q want shell (--container must beat tty.default)", got)
	}
}

func TestRun_Attach_NoCandidate_Errors_NoMutationOnAttach(t *testing.T) {
	t.Cleanup(viper.Reset)

	// validCellYAML has no attachable containers — -a must fail with the
	// explicit ErrAttachNoCandidate without driving the attach loop. The
	// CreateCell ran already (fail-late after start is the documented UX);
	// the cell is left Ready and the operator can re-run with --container or
	// fix the spec.
	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-a"})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachNoCandidate) {
		t.Fatalf("err=%v want ErrAttachNoCandidate", err)
	}
	if fc.attachCalls != 0 {
		t.Errorf("AttachContainer calls=%d want 0", fc.attachCalls)
	}
	if run.calls != 0 {
		t.Errorf("attach loop calls=%d want 0", run.calls)
	}
}

func TestRun_Attach_BadContainerFlag_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	run := &runCapture{}
	cmd, out := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, attachableCellYAML),
		"-a", "--container", "ghost",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrContainerNotFound) {
		t.Fatalf("err=%v want ErrContainerNotFound", err)
	}
	if !strings.Contains(err.Error(), "shell") || !strings.Contains(err.Error(), "claude") {
		t.Errorf("err %q must list attachables (shell, claude); output:\n%s", err, out.String())
	}
	if fc.attachCalls != 0 {
		t.Errorf("AttachContainer calls=%d want 0", fc.attachCalls)
	}
	if run.calls != 0 {
		t.Errorf("attach loop calls=%d want 0", run.calls)
	}
}

func TestRun_Attach_ContainerWithoutAttachFlag_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmdWithRun(t, fc, &runCapture{})
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, attachableCellYAML),
		"--container", "shell",
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--container is only valid") {
		t.Fatalf("err=%v want '--container is only valid' guard", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (must reject before mutating)", fc.createCalls)
	}
}

func TestRun_Attach_OutputFlag_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmdWithRun(t, fc, &runCapture{})
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, attachableCellYAML),
		"-a", "-o", "json",
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("err=%v want incompatibility guard", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0", fc.createCalls)
	}
}

func TestRun_Attach_AlreadyReady_ShortCircuitThenAttaches(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Tty:     &v1beta1.CellTty{Default: "claude"},
			Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true, Image: "registry.eminwux.com/busybox:latest"},
				{ID: "shell", Attachable: true, Image: "registry.eminwux.com/busybox:latest"},
				{ID: "claude", Attachable: true, Image: "registry.eminwux.com/busybox:latest"},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                existing,
				MetadataExists:      true,
				CgroupExists:        true,
				RootContainerExists: true,
			}, nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "-a"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (short-circuit on Ready)", fc.createCalls)
	}
	if fc.attachCalls != 1 {
		t.Fatalf("AttachContainer calls=%d want 1", fc.attachCalls)
	}
	if got := fc.attachDoc.Metadata.Name; got != "claude" {
		t.Errorf("AttachContainer target=%q want claude", got)
	}
	if run.calls != 1 {
		t.Errorf("attach loop calls=%d want 1", run.calls)
	}
}

func TestNewRunCmd_AttachFlagRegistered(t *testing.T) {
	cmd := runcmd.NewRunCmd()
	attachFlag := cmd.Flags().Lookup("attach")
	if attachFlag == nil || attachFlag.Shorthand != "a" {
		t.Errorf("expected -a/--attach flag, got %+v", attachFlag)
	}
	if got := cmd.Flags().Lookup("container"); got == nil {
		t.Errorf("expected --container flag")
	}
}

// claudeProfileYAML is the headline -p example from issue #142: a per-user
// profile that opts a `work` container into attach + tty.default. Drives the
// `-p -a` round-trip tests below.
const claudeProfileYAML = `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: claude-cell
spec:
  realm: default
  space: agents
  stack: claude
  cell:
    tty:
      default: work
    containers:
      - id: root
        root: true
        image: registry.eminwux.com/busybox:latest
        command: sleep
        args:
          - "3600"
      - id: work
        attachable: true
        image: registry.eminwux.com/busybox:latest
        command: /bin/sh
`

// writeTempProfile drops the headline claudeProfileYAML in a t.TempDir as
// `claude-cell.yaml` and points KUKE_PROFILES_DIR at it. The filename + content
// are hard-coded because every -p test in this file targets the same headline
// profile from issue #142; tests that exercise the metadata.name fallback or
// alternative shapes live in the cellprofile package itself.
func writeTempProfile(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-cell.yaml")
	if err := os.WriteFile(path, []byte(claudeProfileYAML), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	t.Setenv("KUKE_PROFILES_DIR", dir)
}

func TestRun_FromProfile_CreatesAndStarts(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeTempProfile(t)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-p", "claude-cell"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	if got := fc.createDoc.Metadata.Name; !strings.HasPrefix(got, "claude-cell-") || len(got) != len("claude-cell-")+6 {
		t.Errorf("cell name=%q want claude-cell-<6hex> (default prefix from metadata.name)", got)
	}
	if got := fc.createDoc.Spec.RealmID; got != "default" {
		t.Errorf("RealmID=%q want default", got)
	}
	if got := fc.createDoc.Spec.SpaceID; got != "agents" {
		t.Errorf("SpaceID=%q want agents", got)
	}
	if got := fc.createDoc.Spec.StackID; got != "claude" {
		t.Errorf("StackID=%q want claude", got)
	}
	if got := fc.createDoc.Metadata.Labels["kukeon.io/profile"]; got != "claude-cell" {
		t.Errorf("labels[kukeon.io/profile]=%q want claude-cell", got)
	}
}

func TestRun_FromProfile_PrefixOverride_GeneratesFreshCell(t *testing.T) {
	// spec.prefix overrides the default prefix (metadata.name). Every
	// invocation must produce a distinct cell name shaped `<prefix>-<6hex>`.
	t.Cleanup(viper.Reset)
	dir := t.TempDir()
	const profileYAML = `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: claude
spec:
  realm: default
  space: agents
  stack: claude
  prefix: agent
  cell:
    containers:
      - id: work
        attachable: true
        image: registry.eminwux.com/busybox:latest
        command: /bin/sh
`
	if err := os.WriteFile(filepath.Join(dir, "claude.yaml"), []byte(profileYAML), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	t.Setenv("KUKE_PROFILES_DIR", dir)

	names := make(map[string]struct{}, 2)
	for i := range 2 {
		fc := &fakeClient{
			createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return successCreateResult(doc), nil
			},
		}
		cmd, _ := newCmd(t, fc)
		cmd.SetArgs([]string{"-p", "claude"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute #%d: %v", i, err)
		}
		name := fc.createDoc.Metadata.Name
		if !strings.HasPrefix(name, "agent-") || len(name) != len("agent-")+6 {
			t.Fatalf("cell name=%q want agent-<6hex>", name)
		}
		if _, dup := names[name]; dup {
			t.Errorf("name=%q repeated across invocations", name)
		}
		names[name] = struct{}{}
		if got := fc.createDoc.Metadata.Labels["kukeon.io/profile"]; got != "claude" {
			t.Errorf("labels[kukeon.io/profile]=%q want claude (label tracks metadata.name)", got)
		}
	}
}

func TestRun_FromProfile_LocationFlagsOverride(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeTempProfile(t)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	// The materialized profile sets realm/space/stack already; --realm/--space/--stack
	// flags must NOT override values the profile already provides — the same
	// "doc wins over flag" rule that -f obeys.
	cmd.SetArgs([]string{"-p", "claude-cell", "--realm", "x", "--space", "y", "--stack", "z"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createDoc.Spec.RealmID; got != "default" {
		t.Errorf("RealmID=%q want default (profile must beat --realm)", got)
	}
}

func TestRun_FromProfile_UnknownProfile_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeTempProfile(t)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-p", "ghost"})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrProfileNotFound) {
		t.Fatalf("err=%v want ErrProfileNotFound", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err %q must name the profile", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0", fc.createCalls)
	}
}

func TestRun_FromProfile_FileAndProfile_MutuallyExclusive(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeTempProfile(t)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-p", "claude-cell"})

	err := cmd.Execute()
	// MarkFlagsMutuallyExclusive emits "if any flags in the group [file profile]
	// are set none of the others can be" — match on the [file profile] phrase
	// rather than wording so cobra rephrasing doesn't break the test.
	if err == nil || !strings.Contains(err.Error(), "[file profile]") {
		t.Fatalf("err=%v want mutually-exclusive guard naming both flags", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0", fc.createCalls)
	}
}

func TestRun_RejectsPositionalArgs(t *testing.T) {
	// `kuke run` is for creating cells; re-attaching to a known cell is
	// `kuke attach <cell>`'s job. Cobra's NoArgs guard enforces the split.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "my-cell"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want positional-arg rejection")
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 on rejected args", fc.createCalls)
	}
}

func TestRun_FromProfile_Attach_HeadlineFlow(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeTempProfile(t)

	// Headline flow from the issue: `kuke run -p claude-cell -a` materializes
	// the profile, creates+starts the cell, then attaches to cell.tty.default
	// (the `work` container).
	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-p", "claude-cell", "-a"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	if fc.attachCalls != 1 {
		t.Fatalf("AttachContainer calls=%d want 1", fc.attachCalls)
	}
	if got := fc.attachDoc.Metadata.Name; got != "work" {
		t.Errorf("attach target=%q want work (cell.tty.default)", got)
	}
	if run.calls != 1 {
		t.Errorf("attach loop calls=%d want 1", run.calls)
	}
}

func TestRun_RmFlag_SetsAutoDeleteOnSpec(t *testing.T) {
	// `kuke run --rm -f cell.yaml` must surface AutoDelete=true on the
	// CellDoc the daemon receives, in both attached and detached modes.
	// The daemon side (KukeonV1Service.CreateCell) reads that bool to
	// install the auto-delete watcher.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "--rm"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !fc.createDoc.Spec.AutoDelete {
		t.Errorf("CreateCell received AutoDelete=false; --rm must set it true")
	}
}

func TestRun_NoRmFlag_LeavesAutoDeleteFalse(t *testing.T) {
	// Default is opt-in — without --rm, the daemon must not see AutoDelete
	// flipped on. Guards against accidental regression where the CLI
	// always sets the bool.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createDoc.Spec.AutoDelete {
		t.Errorf("CreateCell received AutoDelete=true without --rm; default must be false")
	}
}

func TestRun_RmFlag_RejectsNoDaemon(t *testing.T) {
	// --rm needs a long-lived process to watch the root task. The --no-daemon
	// CLI returns immediately after CreateCell, so the watcher would never
	// run. Reject the combo at flag-parse — before any cell is mutated.
	t.Cleanup(viper.Reset)
	// The real --no-daemon flag is registered on rootCmd; in this run-only
	// fixture we only have the run subcommand, so flip the underlying viper
	// key directly. Either path lands the same bool in viper.
	viper.Set(config.KUKEON_ROOT_NO_DAEMON.ViperKey, true)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "--rm"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--rm is incompatible with --no-daemon") {
		t.Fatalf("err=%v want '--rm is incompatible with --no-daemon'", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (must reject before mutating)", fc.createCalls)
	}
}

func TestRun_RmFlag_FromYAMLAlreadySet_StillHonored(t *testing.T) {
	// A YAML manifest with `autoDelete: true` already in the spec must be
	// honored even without --rm — the spec is the declarative source of
	// truth and the CLI should not silently strip the bit.
	t.Cleanup(viper.Reset)

	const yamlWithAutoDelete = `apiVersion: v1beta1
kind: Cell
metadata:
  name: my-cell
spec:
  id: my-cell
  realmId: my-realm
  spaceId: my-space
  stackId: my-stack
  autoDelete: true
  containers:
    - id: root
      root: true
      image: registry.eminwux.com/busybox:latest
      command: sleep
      args:
        - "3600"
`
	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, yamlWithAutoDelete)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !fc.createDoc.Spec.AutoDelete {
		t.Errorf("CreateCell received AutoDelete=false; YAML autoDelete:true must survive when --rm is absent")
	}
}

func TestNewRunCmd_RmFlagRegistered(t *testing.T) {
	cmd := runcmd.NewRunCmd()
	rmFlag := cmd.Flags().Lookup("rm")
	if rmFlag == nil {
		t.Fatal("expected --rm flag")
	}
	if rmFlag.Value.Type() != "bool" {
		t.Errorf("--rm type=%q want bool", rmFlag.Value.Type())
	}
	if def := rmFlag.DefValue; def != "false" {
		t.Errorf("--rm default=%q want false", def)
	}
}

func TestPickLocation_DefaultsViaConfigKV(t *testing.T) {
	// Sanity-check that the run command's KV defaults still resolve to "default"
	// when viper is reset (mirrors session-default behavior on a fresh shell).
	t.Cleanup(viper.Reset)
	viper.Reset()

	if got := strings.TrimSpace(config.KUKE_RUN_REALM.ValueOrDefault()); got != "default" {
		t.Errorf("KUKE_RUN_REALM default=%q want default", got)
	}
	if got := strings.TrimSpace(config.KUKE_RUN_SPACE.ValueOrDefault()); got != "default" {
		t.Errorf("KUKE_RUN_SPACE default=%q want default", got)
	}
	if got := strings.TrimSpace(config.KUKE_RUN_STACK.ValueOrDefault()); got != "default" {
		t.Errorf("KUKE_RUN_STACK default=%q want default", got)
	}
}
