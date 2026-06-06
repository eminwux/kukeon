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
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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

// cellYAMLUserContainersOnly mirrors docs/examples/hello-world.yaml: the user
// declares only their workload container(s), and relies on the runner to
// synthesize the root container during create. Used by the same-file re-run
// regression below (issue #437).
const cellYAMLUserContainersOnly = `apiVersion: v1beta1
kind: Cell
metadata:
  name: my-cell
spec:
  id: my-cell
  realmId: my-realm
  spaceId: my-space
  stackId: my-stack
  containers:
    - id: web
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
	startCellFn       func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error)
	attachContainerFn func(doc v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error)
	killCellFn        func(doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error)
	getBlueprintFn    func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error)
	getConfigFn       func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error)
	listConfigsFn     func(realm, space, stack string) ([]v1beta1.CellConfigDoc, error)
	createConfigFn    func(doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error)

	getCalls          int
	createCalls       int
	startCalls        int
	attachCalls       int
	killCalls         int
	createConfigCalls int
	createConfigDocs  []v1beta1.CellConfigDoc
	createDoc         v1beta1.CellDoc
	startDoc          v1beta1.CellDoc
	attachDoc         v1beta1.ContainerDoc
	killDoc           v1beta1.CellDoc
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

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	f.startCalls++
	f.startDoc = doc
	if f.startCellFn == nil {
		return kukeonv1.StartCellResult{}, errors.New("unexpected StartCell call")
	}
	return f.startCellFn(doc)
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

func (f *fakeClient) KillCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
	f.killCalls++
	f.killDoc = doc
	if f.killCellFn == nil {
		return kukeonv1.KillCellResult{}, errors.New("unexpected KillCell call")
	}
	return f.killCellFn(doc)
}

func (f *fakeClient) GetBlueprint(
	_ context.Context,
	doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	if f.getBlueprintFn == nil {
		return kukeonv1.GetBlueprintResult{}, errors.New("unexpected GetBlueprint call")
	}
	return f.getBlueprintFn(doc)
}

func (f *fakeClient) GetConfig(
	_ context.Context,
	doc v1beta1.CellConfigDoc,
) (kukeonv1.GetConfigResult, error) {
	if f.getConfigFn == nil {
		return kukeonv1.GetConfigResult{}, errors.New("unexpected GetConfig call")
	}
	return f.getConfigFn(doc)
}

func (f *fakeClient) ListConfigs(
	_ context.Context,
	realm, space, stack string,
) ([]v1beta1.CellConfigDoc, error) {
	if f.listConfigsFn == nil {
		return nil, errors.New("unexpected ListConfigs call")
	}
	return f.listConfigsFn(realm, space, stack)
}

func (f *fakeClient) CreateConfig(
	_ context.Context,
	doc v1beta1.CellConfigDoc,
) (kukeonv1.CreateConfigResult, error) {
	f.createConfigCalls++
	f.createConfigDocs = append(f.createConfigDocs, doc)
	if f.createConfigFn == nil {
		return kukeonv1.CreateConfigResult{}, errors.New("unexpected CreateConfig call")
	}
	return f.createConfigFn(doc)
}

// runCapture records the Options passed to the in-process attach loop. By
// default returns nil so the test treats the call as a clean detach; set
// err to inject an attach-loop failure (e.g. control-socket lost).
type runCapture struct {
	calls int
	opts  sbshattach.Options
	err   error
}

func (r *runCapture) fn(_ context.Context, opts sbshattach.Options) error {
	r.calls++
	r.opts = opts
	return r.err
}

// runErrorCapture is a thin wrapper used by tests that only care that the
// attach loop returned a specific error — it shares the same shape as
// runCapture so existing newCmdWithRun plumbing works unchanged.
type runErrorCapture struct {
	calls int
	err   error
}

func (r *runErrorCapture) fn(_ context.Context, _ sbshattach.Options) error {
	r.calls++
	return r.err
}

func newCmd(t *testing.T, fc *fakeClient) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	return newCmdWithRun(t, fc, nil)
}

func newCmdWithRun(t *testing.T, fc *fakeClient, run *runCapture) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var fn runcmd.RunFn
	if run != nil {
		fn = run.fn
	}
	return newCmdWithRunFn(t, fc, fn)
}

func newCmdWithRunFn(t *testing.T, fc *fakeClient, run runcmd.RunFn) (*cobra.Command, *bytes.Buffer) {
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
		ctx = context.WithValue(ctx, runcmd.MockRunKey{}, run)
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

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
		"-d",
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, cellYAMLNoLocation), "-d"})

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
		"-d",
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, multiDocYAML), "-d"})

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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, realmDocYAML), "-d"})

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
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                     existing,
				MetadataExists:           true,
				CgroupExists:             true,
				RootContainerExists:      true,
				RootContainerTaskRunning: true,
			}, nil
		},
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

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

func TestRun_ExistingCell_RecordedReady_ContainerdLostContainers_Refuses(t *testing.T) {
	t.Cleanup(viper.Reset)

	// #654: the cell is recorded Ready on disk, but containerd no longer has
	// its root container (a daemon/host restart dropped the containers while
	// the metadata survived). Run must NOT print phantom `already existed` /
	// `containers: started` lines and must NOT attach to the dead socket;
	// instead it refuses with a divergence message + delete-then-rerun pointer.
	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:           existing,
				MetadataExists: true,
				CgroupExists:   true,
				// Containerd lost the root container — the divergence signal.
				RootContainerExists: false,
			}, nil
		},
	}
	// Default (attach) mode: the bug attaches to a dead socket. Omit -d so the
	// test exercises the path that would otherwise reach the failing attach.
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML)})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute: want divergence refusal, got nil")
	}
	if !strings.Contains(err.Error(), "diverged") {
		t.Errorf("error missing divergence wording: %v", err)
	}
	if !strings.Contains(err.Error(), "kuke delete cell my-cell") {
		t.Errorf("error missing `kuke delete cell` recovery pointer: %v", err)
	}
	if strings.Contains(out.String(), "already existed") {
		t.Errorf("must not print phantom `already existed` for a diverged cell:\n%s", out.String())
	}
	if strings.Contains(out.String(), "containers: started") {
		t.Errorf("must not print phantom `containers: started` for a diverged cell:\n%s", out.String())
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (refuse, do not re-enter create — #630)", fc.createCalls)
	}
	if fc.startCalls != 0 {
		t.Errorf("StartCell calls=%d want 0 (refuse, do not re-enter start — #630)", fc.startCalls)
	}
	if fc.attachCalls != 0 {
		t.Errorf("AttachContainer calls=%d want 0 (must not attach to a dead socket)", fc.attachCalls)
	}
}

// TestRun_ExistingCell_RecordedReady_TaskGone_Refuses covers #683: unlike #654
// (the root container *record* is gone), here the record survived a host/daemon
// restart but its backing task did not. The original record-existence guard
// would pass — RootContainerExists is true — and run would attach to a dead
// socket. The task-liveness guard must refuse instead, identically to the
// record-gone case: no phantom output, no attach, divergence error.
func TestRun_ExistingCell_RecordedReady_TaskGone_Refuses(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:           existing,
				MetadataExists: true,
				CgroupExists:   true,
				// Record survived the restart; the task did not — the #683 signal.
				RootContainerExists:      true,
				RootContainerTaskRunning: false,
			}, nil
		},
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML)})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute: want divergence refusal, got nil")
	}
	if !strings.Contains(err.Error(), "diverged") {
		t.Errorf("error missing divergence wording: %v", err)
	}
	if !strings.Contains(err.Error(), "kuke delete cell my-cell") {
		t.Errorf("error missing `kuke delete cell` recovery pointer: %v", err)
	}
	if strings.Contains(out.String(), "already existed") {
		t.Errorf("must not print phantom `already existed` for a diverged cell:\n%s", out.String())
	}
	if fc.attachCalls != 0 {
		t.Errorf("AttachContainer calls=%d want 0 (must not attach to a dead socket)", fc.attachCalls)
	}
}

func TestRun_ExistingCell_MatchingSpec_Stopped_StartsAndAttaches(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Cell exists with matching spec but is Stopped. Run must call StartCell
	// (not CreateCell — that was an unsafe re-entry per #630) and then attach.
	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
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
		startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
		},
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 0 {
		t.Fatalf("CreateCell calls=%d want 0 (Stopped must start, not re-create)", fc.createCalls)
	}
	if fc.startCalls != 1 {
		t.Fatalf("StartCell calls=%d want 1 (Stopped must start)", fc.startCalls)
	}
	if !strings.Contains(out.String(), "  - containers: started") {
		t.Errorf("output missing 'containers: started' marker:\n%s", out.String())
	}
}

func TestRun_ExistingCell_MatchingSpec_ErrorPartial_Refuses(t *testing.T) {
	t.Cleanup(viper.Reset)

	// A cell in an error / partial state has no clean start path. Run must
	// refuse with a `kuke delete cell <name>` pointer (parity with the `-c`
	// identity contract in #625) rather than re-create or start.
	base := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
			},
		},
	}

	for _, tc := range []struct {
		name  string
		state v1beta1.CellState
	}{
		{"failed", v1beta1.CellStateFailed},
		{"pending", v1beta1.CellStatePending},
		{"unknown", v1beta1.CellStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			existing := base
			existing.Status = v1beta1.CellStatus{State: tc.state}
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
			cmd, _ := newCmd(t, fc)
			cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("Execute: want refusal error for %s state, got nil", tc.state.String())
			}
			if !strings.Contains(err.Error(), "kuke delete cell my-cell") {
				t.Errorf("error missing `kuke delete cell` pointer: %v", err)
			}
			if fc.createCalls != 0 {
				t.Errorf("CreateCell calls=%d want 0 (must refuse, not re-create)", fc.createCalls)
			}
			if fc.startCalls != 0 {
				t.Errorf("StartCell calls=%d want 0 (must refuse, not start)", fc.startCalls)
			}
		})
	}
}

// TestRun_ExistingCell_SynthesizedRoot_DoesNotDiverge covers the same-file
// re-run path for a YAML that omits an explicit root container (the canonical
// case — `docs/examples/hello-world.yaml`). The on-disk cell carries the
// runner-synthesized root entry; a naive count comparison would treat actual=2
// vs desired=1 as divergent and refuse the re-run with `spec.containers
// (count: actual=2, desired=1)`. Per issue #437, the divergence check must
// exclude the synthesized root on both sides so the idempotent path works.
func TestRun_ExistingCell_SynthesizedRoot_DoesNotDiverge(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "web",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                     existing,
				MetadataExists:           true,
				CgroupExists:             true,
				RootContainerExists:      true,
				RootContainerTaskRunning: true,
			}, nil
		},
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, cellYAMLUserContainersOnly), "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned %v, want nil (re-run of same file must succeed)", err)
	}
	if fc.createCalls != 0 {
		t.Fatalf("CreateCell calls=%d want 0 (short-circuit on matching spec + Ready)", fc.createCalls)
	}
	if !strings.Contains(out.String(), "  - metadata: already existed") {
		t.Errorf("output missing 'already existed' marker:\n%s", out.String())
	}
}

// TestRun_ExistingCell_SynthesizedRoot_RealDivergenceStillCaught makes sure
// the issue #437 fix did not over-narrow: when the user adds a new container
// to the YAML between runs, the divergence detection must still fire and
// name the diverging field (count delta among user containers, not the
// synthesized root). Under --require-synced the refusal is the same shape
// the pre-#986 default emitted.
func TestRun_ExistingCell_SynthesizedRoot_RealDivergenceStillCaught(t *testing.T) {
	t.Cleanup(viper.Reset)

	// On-disk: synthesized root + the original "web" container.
	// YAML in cellYAMLUserContainersOnly: only "web".
	// Real divergence: on-disk has an additional user container "extra".
	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true, Image: "registry.eminwux.com/busybox:latest"},
				{ID: "web", Image: "registry.eminwux.com/busybox:latest"},
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, cellYAMLUserContainersOnly), "-d", "--require-synced"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want divergence error for real user-container delta")
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("err=%q does not refer the operator to `kuke apply -f`", err)
	}
	if !strings.Contains(err.Error(), "actual=2, desired=1") {
		t.Errorf("err=%q should report user-container counts excluding the synthesized root, got count delta", err)
	}
}

func TestRun_ExistingCell_DivergingContainerSet_RequireSynced_RefusesAndPointsToApply(t *testing.T) {
	t.Cleanup(viper.Reset)

	// On-disk cell has an extra container the file does not declare. This is
	// the structural drift (container set / count) the AC routes through
	// `kuke apply -f`. With --require-synced (#986) the behaviour matches the
	// pre-#986 default: the divergence guard refuses and points the operator
	// at `kuke apply -f`.
	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "extra",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
			},
		},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: existing, MetadataExists: true}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "--require-synced"})

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
	if !strings.Contains(err.Error(), "--require-synced") {
		t.Errorf("err=%q should mention --require-synced as the strict-mode trigger", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (must not mutate on divergence)", fc.createCalls)
	}
}

// TestRun_ExistingCell_DivergingContainerImage_RequireSynced_RefusesAndPointsToApply
// covers the issue #468 case: on-disk cell has the same container set as the
// file but the user-supplied container's image differs (busybox:latest on
// disk vs busybox:musl in the file). With --require-synced (#986),
// `kuke run -f` refuses with the diverging-spec error and does not mutate
// the cell, matching the pre-#986 default behaviour and the
// `docs/cli-use-cases.md` invariant for opt-in strict mode.
func TestRun_ExistingCell_DivergingContainerImage_RequireSynced_RefusesAndPointsToApply(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:musl",
					Command: "sleep",
					Args:    []string{"3600"},
				},
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
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "--require-synced"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want divergence error for image change")
	}
	if !strings.Contains(err.Error(), `live cell "my-cell" spec differs from on-disk spec`) {
		t.Errorf("err=%q does not contain the diverging-spec phrase naming the cell + on-disk source", err)
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("err=%q does not refer the operator to `kuke apply -f`", err)
	}
	if !strings.Contains(err.Error(), `spec.containers["work"].image`) {
		t.Errorf("err=%q does not name the diverging image field on the user container", err)
	}
	if !strings.Contains(err.Error(), "--require-synced") {
		t.Errorf("err=%q should mention --require-synced as the strict-mode trigger", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (must not mutate on divergence)", fc.createCalls)
	}
}

// TestRun_ExistingCell_DivergingContainerSecrets_RequireSynced_RefusesAndPointsToApply
// exercises the secrets branch of divergedContainerFields under
// --require-synced (#986). Secrets is user-authored, persisted to disk
// unchanged (apischeme round-trips it verbatim), and not filled in from
// `space.spec.defaults.container` — so the same no-op-on-drift failure mode
// that #468 closes for image applies; the opt-in strict mode preserves the
// pre-#986 refusal.
func TestRun_ExistingCell_DivergingContainerSecrets_RequireSynced_RefusesAndPointsToApply(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
					Secrets: []v1beta1.ContainerSecret{
						{Name: "db-pass", FromFile: "/etc/kukeon/secrets/db-pass"},
					},
				},
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
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "--require-synced"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want divergence error for secrets change")
	}
	if !strings.Contains(err.Error(), `live cell "my-cell" spec differs from on-disk spec`) {
		t.Errorf("err=%q does not contain the diverging-spec phrase naming the cell + on-disk source", err)
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("err=%q does not refer the operator to `kuke apply -f`", err)
	}
	if !strings.Contains(err.Error(), `spec.containers["work"].secrets`) {
		t.Errorf("err=%q does not name the diverging secrets field on the user container", err)
	}
	if !strings.Contains(err.Error(), "--require-synced") {
		t.Errorf("err=%q should mention --require-synced as the strict-mode trigger", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (must not mutate on divergence)", fc.createCalls)
	}
}

// TestRun_ExistingCell_EqualSecretRefByValue_DoesNotRefuse pins the
// regression from #920: containerSecretsEqual used to compare
// []ContainerSecret element-by-element with struct ==, which on a struct
// holding a *ContainerSecretRef pointer field compares the pointer by
// identity. The apischeme round-trip allocates a fresh *ContainerSecretRef
// on each conversion, so the YAML-decoded side and the daemon-persisted
// side are always address-distinct even when value-equal — every
// re-`kuke run` of a secretRef:-using cell tripped the divergence guard
// and printed the `kuke restart ...` pointer.
//
// The sibling Diverging... test above only exercises FromFile (pointer-free
// fields), so the pointer-identity bug escaped review. This test puts a
// SecretRef equal by value on both sides and asserts `kuke run` does not
// refuse.
func TestRun_ExistingCell_EqualSecretRefByValue_DoesNotRefuse(t *testing.T) {
	t.Cleanup(viper.Reset)

	const yamlWithSecretRef = `apiVersion: v1beta1
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
      secrets:
        - name: claude-code-oauth-token
          secretRef:
            name: claude-code-oauth-token
            realm: default
`

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
					Secrets: []v1beta1.ContainerSecret{
						{
							Name: "claude-code-oauth-token",
							SecretRef: &v1beta1.ContainerSecretRef{
								Name:  "claude-code-oauth-token",
								Realm: "default",
							},
						},
					},
				},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                     existing,
				MetadataExists:           true,
				CgroupExists:             true,
				RootContainerExists:      true,
				RootContainerTaskRunning: true,
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, yamlWithSecretRef), "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned %v, want nil (equal-by-value SecretRef must not register as drift)", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (Ready cell with matching spec is a no-op)", fc.createCalls)
	}
}

// TestRun_ExistingCell_DivergingContainerTty_RequireSynced_RefusesAndPointsToApply
// exercises the tty branch of divergedContainerFields under --require-synced
// (#986). ContainerTty is user-authored shell-UX config the daemon persists
// verbatim and never fills in from space defaults, so a drift here would
// silently skip the configured prompt/profile/onInit on `kuke run -f`
// re-runs. The opt-in strict mode preserves the pre-#986 refusal.
func TestRun_ExistingCell_DivergingContainerTty_RequireSynced_RefusesAndPointsToApply(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Tty:     &v1beta1.CellTty{Default: "claude"},
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:         "shell",
					Attachable: true,
					Image:      "registry.eminwux.com/busybox:latest",
					Command:    "sleep",
					Args:       []string{"3600"},
				},
				{
					ID:         "claude",
					Attachable: true,
					Image:      "registry.eminwux.com/busybox:latest",
					Command:    "sleep",
					Args:       []string{"3600"},
					Tty: &v1beta1.ContainerTty{
						Prompt: `claude> `,
					},
				},
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
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "-d", "--require-synced"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want divergence error for tty change")
	}
	if !strings.Contains(err.Error(), `live cell "my-cell" spec differs from on-disk spec`) {
		t.Errorf("err=%q does not contain the diverging-spec phrase naming the cell + on-disk source", err)
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("err=%q does not refer the operator to `kuke apply -f`", err)
	}
	if !strings.Contains(err.Error(), `spec.containers["claude"].tty`) {
		t.Errorf("err=%q does not name the diverging tty field on the user container", err)
	}
	if !strings.Contains(err.Error(), "--require-synced") {
		t.Errorf("err=%q should mention --require-synced as the strict-mode trigger", err)
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "-o", "json"})

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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "-o", "yaml"})

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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "-o", "table"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --output") {
		t.Fatalf("err=%v want 'invalid --output ...'", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell called=%d want 0", fc.createCalls)
	}
}

func TestRun_MissingAllSources_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	// epic:cell-identity #1025: cobra's mutex spans flags only, so parseRunFlags
	// hand-rolls the at-least-one-source check. Match on the stable phrasing it
	// emits — naming the existing-cell positional, -f, and the fused --from-*
	// sources.
	if err == nil ||
		!strings.Contains(err.Error(), "<cell>") ||
		!strings.Contains(err.Error(), "-f/--file") ||
		!strings.Contains(err.Error(), "--from-blueprint/--from-config/--clone") {
		t.Fatalf("err=%v want a source-required error naming <cell>/-f/--from-* sources", err)
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
	if cmd.Flags().Lookup("profile") != nil {
		t.Errorf("profile flag must not exist after #626 removal")
	}
	outputFlag := cmd.Flags().Lookup("output")
	if outputFlag == nil || outputFlag.Shorthand != "o" {
		t.Errorf("expected -o/--output flag, got %+v", outputFlag)
	}
	// Issue #813: the CellConfig source moved from `-c/--config` to the
	// optional positional argument. Assert the flag is gone and the positional
	// completer is wired so a regression that re-adds `-c` or drops the
	// completer wiring fails this test.
	if f := cmd.Flags().Lookup("config"); f != nil {
		t.Errorf("`config` flag must be removed (#813); got %+v", f)
	}
	if cmd.ValidArgsFunction == nil {
		t.Error("ValidArgsFunction must be wired to CompleteConfigNames for the positional <config> arg")
	}
}

// TestRun_ProfileFlag_RemovedWithMigrationPointer asserts both spellings of
// the removed -p / --profile flag surface the migration error #626 added,
// rather than cobra's generic "unknown flag" message. The flag-error
// interceptor in NewRunCmd is what makes this work.
func TestRun_ProfileFlag_RemovedWithMigrationPointer(t *testing.T) {
	cases := [][]string{
		{"-p", "claude-cell"},
		{"--profile", "claude-cell"},
		{"--profile=claude-cell"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Cleanup(viper.Reset)
			fc := &fakeClient{}
			cmd, _ := newCmd(t, fc)
			cmd.SetArgs(args)

			err := cmd.Execute()
			if err == nil {
				t.Fatal("Execute returned nil; want migration error")
			}
			if !strings.Contains(err.Error(), "-p/--profile (CellProfile) was removed") {
				t.Errorf("err %q must name the removed flag", err)
			}
			if !strings.Contains(err.Error(), "kuke run -b") {
				t.Errorf("err %q must point at the -b replacement", err)
			}
			if fc.createCalls != 0 {
				t.Errorf("CreateCell called; flag removal must reject before RPC")
			}
		})
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableNoDefaultYAML)})

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
		"--container", "shell",
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

	// validCellYAML has no attachable containers — the default attach
	// mode must fail with the explicit ErrAttachNoCandidate without
	// driving the attach loop. The CreateCell ran already (fail-late
	// after start is the documented UX); the cell is left Ready and
	// the operator can re-run with --container, fix the spec, or pass
	// -d/--detach.
	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML)})

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
		"--container", "ghost",
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

// TestRun_Attach_AttachContainer_NotFound_SurfacesSentinel locks in the
// ErrContainerNotFound branch's %w wrap in runAttachLoop: when the daemon's
// AttachContainer RPC reports the target container doesn't exist, run must
// propagate the sentinel so upstream callers can still errors.Is it.
func TestRun_Attach_AttachContainer_NotFound_SurfacesSentinel(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			return kukeonv1.AttachContainerResult{}, errdefs.ErrContainerNotFound
		},
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrContainerNotFound) {
		t.Fatalf("err=%v want ErrContainerNotFound", err)
	}
	if fc.attachCalls != 1 {
		t.Errorf("AttachContainer calls=%d want 1", fc.attachCalls)
	}
	if run.calls != 0 {
		t.Errorf("attach loop calls=%d want 0", run.calls)
	}
}

func TestRun_Attach_ContainerWithDetachFlag_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	// --container only makes sense in the default attach mode; combining
	// it with -d/--detach is a contradiction and must be rejected before
	// any cell is mutated.
	fc := &fakeClient{}
	cmd, _ := newCmdWithRun(t, fc, &runCapture{})
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, attachableCellYAML),
		"-d", "--container", "shell",
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--container is incompatible") {
		t.Fatalf("err=%v want '--container is incompatible with -d/--detach' guard", err)
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
		"-o", "json",
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
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:         "shell",
					Attachable: true,
					Image:      "registry.eminwux.com/busybox:latest",
					Command:    "sleep",
					Args:       []string{"3600"},
				},
				{
					ID:         "claude",
					Attachable: true,
					Image:      "registry.eminwux.com/busybox:latest",
					Command:    "sleep",
					Args:       []string{"3600"},
				},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                     existing,
				MetadataExists:           true,
				CgroupExists:             true,
				RootContainerExists:      true,
				RootContainerTaskRunning: true,
			}, nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

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

func TestNewRunCmd_DetachFlagRegistered(t *testing.T) {
	cmd := runcmd.NewRunCmd()
	detachFlag := cmd.Flags().Lookup("detach")
	if detachFlag == nil || detachFlag.Shorthand != "d" {
		t.Errorf("expected -d/--detach flag, got %+v", detachFlag)
	}
	if got := cmd.Flags().Lookup("container"); got == nil {
		t.Errorf("expected --container flag")
	}
}

// CellProfile / -p tests removed in #626 — the kind, the loader, and the
// flag were deleted in this PR. The blueprint equivalents
// (TestRun_FromBlueprint_*) cover the surviving template path;
// TestRun_ProfileFlag_RemovedWithMigrationPointer above attests the
// removal surface.

func TestRun_RejectsPositionalArgs(t *testing.T) {
	// `kuke run` is for creating cells; re-attaching to a known cell is
	// `kuke attach <cell>`'s job. Cobra's NoArgs guard enforces the split.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "my-cell"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want positional-arg rejection")
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 on rejected args", fc.createCalls)
	}
}

func TestRun_RmFlag_SetsAutoDeleteOnSpec(t *testing.T) {
	// `kuke run -d --rm -f cell.yaml` must surface AutoDelete=true on
	// the CellDoc the daemon receives, in both attached and detached
	// modes. The daemon side (KukeonV1Service.CreateCell) reads that
	// bool to install the auto-delete watcher.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "--rm"})

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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createDoc.Spec.AutoDelete {
		t.Errorf("CreateCell received AutoDelete=true without --rm; default must be false")
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, yamlWithAutoDelete), "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !fc.createDoc.Spec.AutoDelete {
		t.Errorf("CreateCell received AutoDelete=false; YAML autoDelete:true must survive when --rm is absent")
	}
}

func TestRun_RmAttach_KeepsCellAliveOnCleanDetach(t *testing.T) {
	// Issue #279: a clean ^]^] detach must NOT trigger the --rm
	// KillCell under the default attach mode. The operator may want
	// to re-attach later — same semantics as `kuke attach`. Only
	// workload-end signals (peer hangup, shell exit, controller
	// error) should fire cleanup.
	//
	// Inject attach.ErrDetached as the attach-loop result; that is
	// what sbsh v0.10.1 returns when the in-band detach keystroke
	// fires.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
		killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{Killed: true}, nil
		},
	}
	run := &runErrorCapture{
		err: fmt.Errorf("wrapped by harness: %w", sbshattach.ErrDetached),
	}
	cmd, _ := newCmdWithRunFn(t, fc, run.fn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "--rm"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (clean detach must surface as exit 0)", err)
	}
	if !fc.createDoc.Spec.AutoDelete {
		t.Errorf("CreateCell received AutoDelete=false; --rm must set it true even when clean detach skips KillCell")
	}
	if run.calls != 1 {
		t.Fatalf("attach loop calls=%d want 1", run.calls)
	}
	if fc.killCalls != 0 {
		t.Fatalf("KillCell calls=%d want 0 (clean detach must leave cell alive for re-attach)", fc.killCalls)
	}
}

func TestRun_RmAttach_KillsCellOnPeerClosed(t *testing.T) {
	// Issue #265: with --rm in the default attach mode and a
	// long-lived root (`sleep infinity`) peering an attachable
	// container, the root task never exits when the workload
	// terminates on the peer side, so the reconciler's auto-delete
	// trigger never fires. The CLI must call KillCell when the attach
	// loop returns peer-closed so the daemon's reconciler reaps the
	// cell on the next tick.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
		killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{Killed: true}, nil
		},
	}
	run := &runErrorCapture{
		err: fmt.Errorf("wrapped by harness: %w", sbshattach.ErrPeerClosed),
	}
	cmd, _ := newCmdWithRunFn(t, fc, run.fn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "--rm"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (peer-closed must surface as exit 0 — workload ended)", err)
	}
	if !fc.createDoc.Spec.AutoDelete {
		t.Errorf("CreateCell received AutoDelete=false; --rm must set it true")
	}
	if run.calls != 1 {
		t.Fatalf("attach loop calls=%d want 1", run.calls)
	}
	if fc.killCalls != 1 {
		t.Fatalf("KillCell calls=%d want 1 (peer-closed must trigger cleanup)", fc.killCalls)
	}
	if got := fc.killDoc.Metadata.Name; got != "my-cell" {
		t.Errorf("KillCell target=%q want my-cell", got)
	}
	if got := fc.killDoc.Spec.RealmID; got != "my-realm" {
		t.Errorf("KillCell realm=%q want my-realm", got)
	}
}

func TestRun_RmAttach_KillsCellEvenWhenAttachLoopErrors(t *testing.T) {
	// --rm is best-effort cleanup keyed on "the operator is done", which
	// is true regardless of whether the attach loop returned cleanly or
	// errored. KillCell must still fire so a peer-shell crash does not
	// leak the cell.
	t.Cleanup(viper.Reset)

	attachLoopErr := errors.New("control socket lost")
	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
		killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{Killed: true}, nil
		},
	}
	run := &runErrorCapture{err: attachLoopErr}
	cmd, _ := newCmdWithRunFn(t, fc, run.fn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "--rm"})

	if err := cmd.Execute(); !errors.Is(err, attachLoopErr) {
		t.Fatalf("Execute err=%v want attachLoopErr (must surface to caller)", err)
	}
	if fc.killCalls != 1 {
		t.Errorf("KillCell calls=%d want 1 (must fire even when attach loop errors)", fc.killCalls)
	}
}

func TestRun_RmAttach_KillCellFailureDoesNotMaskAttachExit(t *testing.T) {
	// On the workload-ended path (peer hangup / shell exit), a KillCell
	// RPC failure is logged to stderr but does not become the exit
	// error: --rm is documented best-effort and the daemon's reconciler
	// is the safety net for any orphaned cell. The attach loop result
	// dictates the run rc.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
		killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{}, errors.New("daemon RPC: connection refused")
		},
	}
	run := &runCapture{err: fmt.Errorf("wrapped by harness: %w", sbshattach.ErrPeerClosed)}
	cmd, buf := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "--rm"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (KillCell failure on workload-end must not surface as a run error)", err)
	}
	if fc.killCalls != 1 {
		t.Fatalf("KillCell calls=%d want 1", fc.killCalls)
	}
	if !strings.Contains(buf.String(), "--rm cleanup: failed to kill cell") {
		t.Errorf("expected stderr warning about KillCell failure, got:\n%s", buf.String())
	}
}

func TestRun_RmAttach_KillCellNotFound_Silent(t *testing.T) {
	// If the daemon's reconciler raced ahead and already reaped the cell
	// (e.g. attach target was the root and exiting it triggered the
	// existing root-task path), KillCell returns ErrCellNotFound. That
	// is the expected idempotent outcome — no stderr noise. The attach
	// loop must report a workload-end exit (peer hangup) so KillCell
	// fires in the first place under the issue #279 semantics.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
		killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{}, errdefs.ErrCellNotFound
		},
	}
	run := &runCapture{err: fmt.Errorf("wrapped by harness: %w", sbshattach.ErrPeerClosed)}
	cmd, buf := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "--rm"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(buf.String(), "--rm cleanup: failed to kill cell") {
		t.Errorf("ErrCellNotFound must be silent (idempotent), got stderr:\n%s", buf.String())
	}
}

func TestRun_RmDetach_DoesNotCallKillCell(t *testing.T) {
	// With -d/--detach, --rm preserves its original semantics: the
	// daemon's reconciler watches the root task and reaps when it
	// exits. The CLI must not pre-empt that path with a KillCell —
	// that would break `kuke run -d --rm -f cell.yaml` for a
	// long-running workload that the operator wants left running
	// until it exits on its own.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "--rm"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.killCalls != 0 {
		t.Errorf("KillCell calls=%d want 0 (-d means the reconciler owns cleanup)", fc.killCalls)
	}
}

func TestRun_RmAttach_AlreadyReady_StillKillsCellOnPeerClosed(t *testing.T) {
	// Idempotent branch regression guard: re-running `kuke run --rm`
	// against an already-Ready cell with matching spec must still fire
	// KillCell when the attach loop reports workload-end (peer hangup
	// here). Otherwise a user with a dangling cell from a pre-fix
	// invocation cannot recover via --rm, reproducing the original
	// #265 symptom.
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Tty:     &v1beta1.CellTty{Default: "claude"},
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:         "shell",
					Attachable: true,
					Image:      "registry.eminwux.com/busybox:latest",
					Command:    "sleep",
					Args:       []string{"3600"},
				},
				{
					ID:         "claude",
					Attachable: true,
					Image:      "registry.eminwux.com/busybox:latest",
					Command:    "sleep",
					Args:       []string{"3600"},
				},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                     existing,
				MetadataExists:           true,
				CgroupExists:             true,
				RootContainerExists:      true,
				RootContainerTaskRunning: true,
			}, nil
		},
		attachContainerFn: attachSuccessFn(),
		killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{Killed: true}, nil
		},
	}
	run := &runCapture{err: fmt.Errorf("wrapped by harness: %w", sbshattach.ErrPeerClosed)}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "--rm"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (short-circuit on Ready)", fc.createCalls)
	}
	if run.calls != 1 {
		t.Fatalf("attach loop calls=%d want 1", run.calls)
	}
	if fc.killCalls != 1 {
		t.Fatalf("KillCell calls=%d want 1 (idempotent branch must also fire cleanup)", fc.killCalls)
	}
	if got := fc.killDoc.Metadata.Name; got != "my-cell" {
		t.Errorf("KillCell target=%q want my-cell", got)
	}
}

func TestRun_AttachNoRm_DoesNotCallKillCell(t *testing.T) {
	// Defensive guard: the default attach mode alone must not engage
	// cleanup. KillCell only fires when --rm is also set.
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.killCalls != 0 {
		t.Errorf("KillCell calls=%d want 0 (attach without --rm must not clean up)", fc.killCalls)
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

// Profile param-resolution tests (paramProfileYAML + writeParamProfile +
// TestRun_FromProfile_With*) were removed in #626 alongside the CellProfile
// kind. The blueprint path's --param / --param-file / --name behavior is
// covered by TestRun_FromBlueprint_* below; the underlying parameter
// helpers (ParseParamArgs / ParseParamFile / MergeParams) are exercised in
// internal/cellblueprint/params_test.go.

func TestRun_FileMode_RejectsParamFlags(t *testing.T) {
	// --name, --param, --param-file are blueprint-only knobs. With -f the
	// file's metadata.name is authoritative and substitution doesn't apply,
	// so the CLI rejects the combination rather than silently dropping the
	// flag.
	cases := []struct {
		name string
		flag []string
		want string
	}{
		{"name", []string{"--name", "x"}, "--name is only valid"},
		{"param", []string{"--param", "K=V"}, "--param is only valid"},
		{"param-file", []string{"--param-file", "/tmp/whatever"}, "--param-file is only valid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			fc := &fakeClient{}
			cmd, _ := newCmd(t, fc)
			args := []string{"-f", writeTempYAML(t, validCellYAML), "-d"}
			args = append(args, tc.flag...)
			cmd.SetArgs(args)

			err := cmd.Execute()
			if err == nil {
				t.Fatal("Execute returned nil; want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %q must contain %q", err, tc.want)
			}
			if fc.createCalls != 0 {
				t.Errorf("CreateCell called; flag rejection must short-circuit before RPC")
			}
		})
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

// blueprintDoc returns a minimal runnable CellBlueprintDoc the fake daemon can
// hand back over GetBlueprint, with one ${TAG} parameter substituted into the
// image.
func blueprintDoc() v1beta1.CellBlueprintDoc {
	def := "latest"
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  "web",
			Realm: "my-realm",
			Space: "my-space",
			Stack: "my-stack",
		},
		Spec: v1beta1.CellBlueprintSpec{
			Prefix:     "web",
			Parameters: []v1beta1.CellBlueprintParameter{{Name: "TAG", Default: &def}},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "main", Image: "registry.example.com/web:${TAG}", Attachable: true},
				},
			},
		},
	}
}

func TestRun_FromBlueprint_ResolvesAndCreates(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			// The lookup carries the requested name + scope; echo back the body.
			if doc.Metadata.Name != "web" {
				t.Errorf("lookup name=%q want web", doc.Metadata.Name)
			}
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return kukeonv1.CreateCellResult{
				Cell: doc, Created: true, MetadataExistsPost: true,
				CgroupCreated: true, CgroupExistsPost: true,
				RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
				Containers: []kukeonv1.ContainerCreationOutcome{{Name: "main", ExistsPost: true, Created: true}},
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-blueprint", "web", "--param", "TAG=v2", "--realm", "my-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	// Fresh <prefix>-<6hex> name, scope from blueprint metadata, ${TAG} filled.
	if got := fc.createDoc.Metadata.Name; !regexp.MustCompile(`^web-[0-9a-f]{6}$`).MatchString(got) {
		t.Errorf("cell name=%q want web-<6hex>", got)
	}
	if got := fc.createDoc.Spec.RealmID; got != "my-realm" {
		t.Errorf("RealmID=%q want my-realm", got)
	}
	if got := fc.createDoc.Spec.Containers[0].Image; got != "registry.example.com/web:v2" {
		t.Errorf("image=%q want ${TAG} substituted to v2", got)
	}
}

// TestRun_FromBlueprint_IgnoreDiskPressure_ThreadsToSpec pins that the
// `--ignore-disk-pressure` flag on `kuke run` reaches Spec.IgnoreDiskPressure
// on the fused create+start+attach path. The flag is registered by
// cell.RegisterSourceFlags (shared with `kuke create cell`) and is read off
// cmd.Flags() rather than viper — the run side has no viper bind for the key,
// so a `viper.GetBool` read would silently see false and drop the operator's
// opt-in past the daemon's disk-pressure guard.
func TestRun_FromBlueprint_IgnoreDiskPressure_ThreadsToSpec(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return kukeonv1.CreateCellResult{
				Cell: doc, Created: true, MetadataExistsPost: true,
				CgroupCreated: true, CgroupExistsPost: true,
				RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
				Containers: []kukeonv1.ContainerCreationOutcome{{Name: "main", ExistsPost: true, Created: true}},
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"--from-blueprint", "web", "--param", "TAG=v2",
		"--realm", "my-realm", "--ignore-disk-pressure", "-d",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !fc.createDoc.Spec.IgnoreDiskPressure {
		t.Errorf("Spec.IgnoreDiskPressure=false, want true (--ignore-disk-pressure was set)")
	}
}

// TestRun_FromBlueprint_NoIgnoreDiskPressure_LeavesSpecFalse is the negative
// counterpart: omitting `--ignore-disk-pressure` leaves Spec.IgnoreDiskPressure
// at its zero value, so the daemon's CreateCell guard still applies.
func TestRun_FromBlueprint_NoIgnoreDiskPressure_LeavesSpecFalse(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return kukeonv1.CreateCellResult{
				Cell: doc, Created: true, MetadataExistsPost: true,
				CgroupCreated: true, CgroupExistsPost: true,
				RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
				Containers: []kukeonv1.ContainerCreationOutcome{{Name: "main", ExistsPost: true, Created: true}},
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-blueprint", "web", "--param", "TAG=v2", "--realm", "my-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createDoc.Spec.IgnoreDiskPressure {
		t.Errorf("Spec.IgnoreDiskPressure=true, want false (flag omitted)")
	}
}

// TestRun_RequireSynced_RejectedOutsideFile pins that --require-synced rejects
// at parse time on the positional and the fused source paths rather than
// silently no-op'ing. The flag compares the live cell against the source
// manifest, which only -f supplies (the fused paths refuse on --name collision
// rather than diverge, and the existing-cell positional has no source).
func TestRun_RequireSynced_RejectedOutsideFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{
			name: "positional",
			args: []string{"my-cell", "--require-synced", "-d"},
		},
		{
			name: "from-blueprint",
			args: []string{"--from-blueprint", "web", "--require-synced", "-d"},
		},
		{
			name: "from-config",
			args: []string{"--from-config", "prod", "--require-synced", "-d"},
		},
		{
			name: "clone",
			args: []string{"--clone", "src", "--require-synced", "-d"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			cmd, _ := newCmd(t, &fakeClient{})
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("Execute returned nil, want --require-synced rejection")
			}
			if !strings.Contains(err.Error(), "--require-synced is only valid with -f/--file") {
				t.Errorf("err=%q should reject --require-synced as -f-only", err)
			}
		})
	}
}

func TestRun_FromBlueprint_NotFound_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{MetadataExists: false}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-blueprint", "ghost", "--realm", "my-realm", "-d"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, errdefs.ErrBlueprintNotFound) {
		t.Fatalf("err=%v want ErrBlueprintNotFound", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell called %d times, want 0 on not-found", fc.createCalls)
	}
}

func TestRun_FromBlueprint_NameOverride(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return kukeonv1.CreateCellResult{
				Cell: doc, Created: true, MetadataExistsPost: true,
				CgroupCreated: true, CgroupExistsPost: true,
				RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
				Containers: []kukeonv1.ContainerCreationOutcome{{Name: "main", ExistsPost: true, Created: true}},
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-blueprint", "web", "--name", "pinned", "--realm", "my-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createDoc.Metadata.Name; got != "pinned" {
		t.Errorf("cell name=%q want pinned (--name override)", got)
	}
}

// configBlueprintDoc returns a minimal CellBlueprintDoc the fake daemon can
// hand back over GetBlueprint to a -c run, with one parameter, one structural
// repo slot, and one env-mode secret slot. The blueprint carries a root
// container plus a user (non-root) container so the divergent-spec check
// (which excludes the runner-synthesised root, see divergedFields' rationale)
// has at least one user-container to compare against.
func configBlueprintDoc() v1beta1.CellBlueprintDoc {
	def := "latest"
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  "web",
			Realm: "bp-realm",
		},
		Spec: v1beta1.CellBlueprintSpec{
			Parameters: []v1beta1.CellBlueprintParameter{{Name: "TAG", Default: &def}},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{
						ID:    "root",
						Root:  true,
						Image: "registry.example.com/root:latest",
					},
					{
						ID:         "main",
						Image:      "registry.example.com/web:${TAG}",
						Attachable: true,
						Repos: []v1beta1.ContainerRepo{
							{Name: "src", Target: "/srv", Required: true},
						},
						Secrets: []v1beta1.BlueprintSecretSlot{
							{Name: "token", Mode: v1beta1.BlueprintSecretModeEnv, EnvName: "TOKEN", Required: true},
						},
					},
				},
			},
		},
	}
}

// configDoc returns a CellConfigDoc that fills configBlueprintDoc()'s slots.
func configDoc() v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  "prod",
			Realm: "cfg-realm",
		},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"TAG": "v2"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src": {URL: "https://example.com/src.git"},
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "api-token", Realm: "cfg-realm"}},
			},
		},
	}
}

func TestRun_FromConfig_BareGeneratesNameAndBackRef(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			if doc.Metadata.Name != "prod" {
				t.Errorf("GetConfig name=%q want prod", doc.Metadata.Name)
			}
			if doc.Metadata.Realm != "cfg-realm" {
				t.Errorf("GetConfig realm=%q want cfg-realm", doc.Metadata.Realm)
			}
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			if doc.Metadata.Name != "web" || doc.Metadata.Realm != "bp-realm" {
				t.Errorf("GetBlueprint=%+v want web@bp-realm", doc.Metadata)
			}
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return kukeonv1.CreateCellResult{
				Cell: doc, Created: true, MetadataExistsPost: true,
				CgroupCreated: true, CgroupExistsPost: true,
				RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
				Containers: []kukeonv1.ContainerCreationOutcome{{Name: "main", ExistsPost: true, Created: true}},
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-config", "prod", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	// Bare `<config>` generates a fresh <prefix>-<6hex> cell per invocation
	// (epic:cell-identity #1022) — prefix = the Config's metadata.name.
	if got := fc.createDoc.Metadata.Name; !regexp.MustCompile(`^prod-[0-9a-f]{6}$`).MatchString(got) {
		t.Errorf("cell name=%q want prod-<6hex> (generated)", got)
	}
	// Back-reference label points at the Config.
	if got := fc.createDoc.Metadata.Labels["kukeon.io/config"]; got != "prod" {
		t.Errorf("kukeon.io/config label=%q want prod", got)
	}
	// Scope from the Config, not the blueprint.
	if got := fc.createDoc.Spec.RealmID; got != "cfg-realm" {
		t.Errorf("RealmID=%q want cfg-realm", got)
	}
	// Slot fills + scalar substitution applied to the user container.
	// configBlueprintDoc declares root + main; match by ID rather than index
	// so this stays resilient to container ordering.
	if len(fc.createDoc.Spec.Containers) != 2 {
		t.Fatalf("containers=%d want 2 (root + main)", len(fc.createDoc.Spec.Containers))
	}
	var main *v1beta1.ContainerSpec
	for i := range fc.createDoc.Spec.Containers {
		if fc.createDoc.Spec.Containers[i].ID == "main" {
			main = &fc.createDoc.Spec.Containers[i]
		}
	}
	if main == nil {
		t.Fatalf("main container missing from materialized cell; got %+v", fc.createDoc.Spec.Containers)
	}
	if main.Image != "registry.example.com/web:v2" {
		t.Errorf("image=%q want ${TAG} substituted to v2", main.Image)
	}
	if len(main.Repos) != 1 || main.Repos[0].URL != "https://example.com/src.git" {
		t.Errorf("repos=%+v want src filled", main.Repos)
	}
	if len(main.Secrets) != 1 || main.Secrets[0].Name != "TOKEN" {
		t.Errorf("secrets=%+v want TOKEN env-mode secret", main.Secrets)
	}
}

func TestRun_FromConfig_LiveReadyCell_AttachesWithoutCreate(t *testing.T) {
	t.Cleanup(viper.Reset)

	// The deterministic name is the Config's name verbatim. A live Ready cell
	// with a matching spec must NOT call CreateCell — the idempotent identity
	// contract of -c is "at most one live cell per Config".
	cfg := configDoc()
	bp := configBlueprintDoc()
	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: cfg, MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: bp, MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			if doc.Metadata.Name != "prod" {
				t.Errorf("GetCell name=%q want prod", doc.Metadata.Name)
			}
			// Echo the desired spec back so divergedFields reports no drift.
			live := doc
			live.Status.State = v1beta1.CellStateReady
			return kukeonv1.GetCellResult{
				Cell: live, MetadataExists: true, CgroupExists: true,
				RootContainerExists: true, RootContainerTaskRunning: true,
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (live Ready cell)", fc.createCalls)
	}
	if fc.startCalls != 0 {
		t.Errorf("StartCell calls=%d want 0 (live Ready cell)", fc.startCalls)
	}
}

func TestRun_FromConfig_LiveStoppedCell_StartsThenAttaches(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			live := doc
			live.Status.State = v1beta1.CellStateStopped
			return kukeonv1.GetCellResult{Cell: live, MetadataExists: true}, nil
		},
		startCellFn: func(v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			return kukeonv1.StartCellResult{Started: true}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (Stopped cell goes through StartCell)", fc.createCalls)
	}
	if fc.startCalls != 1 {
		t.Errorf("StartCell calls=%d want 1", fc.startCalls)
	}
	if got := fc.startDoc.Metadata.Name; got != "prod" {
		t.Errorf("StartCell name=%q want prod", got)
	}
}

func TestRun_FromConfig_LiveFailedCell_RefusesWithDeletePointer(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			live := doc
			live.Status.State = v1beta1.CellStateFailed
			return kukeonv1.GetCellResult{Cell: live, MetadataExists: true}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--realm", "cfg-realm", "-d"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil want refusal on Failed state")
	}
	if !strings.Contains(err.Error(), "kuke delete cell prod") {
		t.Errorf("err=%q want `kuke delete cell prod` pointer", err)
	}
	if fc.createCalls != 0 || fc.startCalls != 0 {
		t.Errorf("CreateCell=%d StartCell=%d want both 0", fc.createCalls, fc.startCalls)
	}
}

// TestRun_ExistingCell_DivergingContainerImage_Default_WarnsAndAttaches is
// the `-f` half of the #986 warn-and-attach default. The notice cites the
// on-disk spec source and the `kuke apply -f` pointer (no Config/Blueprint
// lineage on a bare -f cell) and the flow falls through into the Ready
// short-circuit. Pins the same divergence shape as the require-synced test
// above but asserts the opposite contract.
func TestRun_ExistingCell_DivergingContainerImage_Default_WarnsAndAttaches(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm",
			SpaceID: "my-space",
			StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:musl",
					Command: "sleep",
					Args:    []string{"3600"},
				},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:                     existing,
				MetadataExists:           true,
				CgroupExists:             true,
				RootContainerExists:      true,
				RootContainerTaskRunning: true,
			}, nil
		},
	}
	cmd, buf := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute err=%v want nil (warn-and-attach is the default)", err)
	}
	output := buf.String()
	for _, want := range []string{
		`notice: cell "my-cell" is OutOfSync with on-disk spec`,
		`spec.containers["work"].image`,
		"attaching to current live state",
		"kuke apply -f",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing substring %q:\n%s", want, output)
		}
	}
	if fc.createCalls != 0 || fc.startCalls != 0 {
		t.Errorf("CreateCell=%d StartCell=%d want both 0 (warn-and-attach is a no-op against the live cell)",
			fc.createCalls, fc.startCalls)
	}
}

func TestRun_FromConfig_NotFound_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-config", "ghost", "--realm", "cfg-realm", "-d"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, errdefs.ErrConfigNotFound) {
		t.Fatalf("err=%v want ErrConfigNotFound", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 on config-not-found", fc.createCalls)
	}
}

func TestRun_FromConfig_BlueprintMissing_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{MetadataExists: false}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-config", "prod", "--realm", "cfg-realm", "-d"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, errdefs.ErrBlueprintNotFound) {
		t.Fatalf("err=%v want ErrBlueprintNotFound (referenced blueprint missing)", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 on missing referenced blueprint", fc.createCalls)
	}
}

// TestRun_FromConfig_RejectsParamFlags pins the rejection of template-only
// knobs on the <config> positional: --param/--param-file would silently shadow
// the Config's spec.values and break the identity contract, so the run path
// rejects them rather than apply. --name is *not* on this list since #833 —
// `<config> --name X` is the AC's idempotent-attach escape valve, and
// `<config> --new --name X` is the create-or-fail variant (both covered by
// their own tests).
func TestRun_FromConfig_RejectsParamFlags(t *testing.T) {
	t.Cleanup(viper.Reset)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"--param",
			[]string{"--from-config", "prod", "--param", "K=V", "-d"},
			"--param is not valid with --from-config",
		},
		{
			"--param-file",
			[]string{"--from-config", "prod", "--param-file", "/tmp/p", "-d"},
			"--param-file is not valid with --from-config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			fc := &fakeClient{}
			cmd, _ := newCmd(t, fc)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
		})
	}
}

// TestRun_PositionalConfig_MutexWithFlagSources covers epic:cell-identity
// #1025's AC: the <cell> positional (start an existing cell) is rejected when
// combined with -f or the fused --from-* create sources; the rejection message
// names the conflicting sources so the operator sees the full set without
// re-running with --help.
func TestRun_PositionalConfig_MutexWithFlagSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"--from-blueprint conflicts with positional",
			[]string{"prod", "--from-blueprint", "web", "--realm", "cfg-realm", "-d"},
			"the <cell> positional (start an existing cell) is mutually exclusive with " +
				"--from-blueprint/--from-config/--clone",
		},
		{
			"-f conflicts with positional",
			[]string{"prod", "-f", "/tmp/never-read.yaml", "-d"},
			"the <cell> positional is mutually exclusive with -f/--file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			fc := &fakeClient{}
			cmd, _ := newCmd(t, fc)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
		})
	}
}

// TestRun_PositionalConfig_TooManyArgs covers the cobra.MaximumNArgs(1) gate:
// the operator can pass at most one positional. A second positional must be
// rejected so a stray argument is surfaced rather than silently dropped.
func TestRun_PositionalConfig_TooManyArgs(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "stray", "-d"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil want cobra rejection of >1 positional arg")
	}
}

// --- #834 (--env KEY=VALUE runtime env injection) tests ------------------

// TestRun_EnvFlag_FromFile_ThreadsRuntimeEnvOntoCreateDoc covers the -f
// source path: a single `--env LABEL=bug` round-trips onto the CellDoc
// handed to CreateCell as Spec.RuntimeEnv. The per-container Env on the
// authored spec is untouched (the merge fires server-side inside the
// runner against the OCI build, not against the persisted spec).
func TestRun_EnvFlag_FromFile_ThreadsRuntimeEnvOntoCreateDoc(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "--env", "LABEL=bug", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	wantRE := []string{"LABEL=bug"}
	if !reflect.DeepEqual(fc.createDoc.Spec.RuntimeEnv, wantRE) {
		t.Errorf("CreateCell doc RuntimeEnv=%v, want %v", fc.createDoc.Spec.RuntimeEnv, wantRE)
	}
	// The persisted spec's per-container Env stays the YAML author's value
	// (validCellYAML declares no env on `work`), proving --env routes through
	// the transport-only RuntimeEnv field, not the authored Containers[].Env.
	for _, c := range fc.createDoc.Spec.Containers {
		if c.ID == "work" && len(c.Env) != 0 {
			t.Errorf("containers[work].Env=%v want nil (--env must not pollute the persisted spec)", c.Env)
		}
	}
}

// TestRun_EnvFlag_Repeated_AllThreaded pins the StringArray-repeatable
// shape of the flag: every `--env` instance lands on RuntimeEnv in
// declaration order, deduplicated where identical, preserving the order
// the user invoked them.
func TestRun_EnvFlag_Repeated_AllThreaded(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, validCellYAML),
		"--env", "LABEL=bug",
		"--env", "PRIORITY=A",
		"--env", "REGION=us-east-1",
		"-d",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"LABEL=bug", "PRIORITY=A", "REGION=us-east-1"}
	if !reflect.DeepEqual(fc.createDoc.Spec.RuntimeEnv, want) {
		t.Errorf("RuntimeEnv=%v, want %v (declaration order preserved)", fc.createDoc.Spec.RuntimeEnv, want)
	}
}

// TestRun_EnvFlag_MissingEquals_RejectedAtCLI covers the parseEnvArgs
// surface from the cobra side: `--env LABELbug` (no `=`) exits with the
// AC-specified format error before any wire call fires. The cell create
// must not happen.
func TestRun_EnvFlag_MissingEquals_RejectedAtCLI(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "--env", "LABELbug", "-d"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want format error")
	}
	if !strings.Contains(err.Error(), "--env requires KEY=VALUE") {
		t.Errorf("err=%q, want '--env requires KEY=VALUE' substring", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d, want 0 (reject before wire call)", fc.createCalls)
	}
}

// TestRun_EnvFlag_DuplicateKeyDifferentValue_RejectedAtCLI covers the
// "no silent last-wins" half of the AC: two --env entries with the same
// KEY but different VALUEs exit with a 'pick one' hint before CreateCell.
func TestRun_EnvFlag_DuplicateKeyDifferentValue_RejectedAtCLI(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, validCellYAML),
		"--env", "LABEL=bug",
		"--env", "LABEL=enh",
		"-d",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want duplicate-key error")
	}
	if !strings.Contains(err.Error(), "pick one") {
		t.Errorf("err=%q, want 'pick one' resolution hint", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d, want 0 (reject before wire call)", fc.createCalls)
	}
}

// TestRun_EnvFlag_FromBlueprint_ThreadsRuntimeEnvOntoCreateDoc covers the
// `-b` source path: `--env` lands on the CellDoc materialized from the
// blueprint. Combines with --param to confirm the two knobs are
// orthogonal (--param does render-time spec substitution into the
// container image; --env injects at start-time into the OCI process env).
// TestRun_EnvFlag_FromBlueprint_Rejected covers the per-path override symmetry
// shared with `kuke create cell` (epic:cell-identity #1023/#1025): --env is
// rejected on the --from-blueprint fused path (materialise from a Config to
// layer env overrides). The symmetric --param-rejected-with-from-config case is
// covered by TestRun_FromConfig_RejectsParamFlags.
func TestRun_EnvFlag_FromBlueprint_Rejected(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"--from-blueprint", "web",
		"--param", "TAG=v2",
		"--env", "LABEL=bug",
		"--realm", "my-realm",
		"-d",
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--env is not valid with --from-blueprint") {
		t.Fatalf("err=%v want --env-rejected-with-from-blueprint", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (rejected before create)", fc.createCalls)
	}
}

// TestRun_EnvFlag_FromConfig_BakesOverride covers the --from-config fused create
// path with --env: the per-cell override is baked into the materialised CellDoc
// (recorded in Spec.Provenance.EnvOverrides), NOT threaded as transport-only
// RuntimeEnv. This is the persisted-override semantics `kuke create cell
// --from-config --env` produces (epic:cell-identity #1023); run's fused form
// inherits it via the shared cell.Materialize.
func TestRun_EnvFlag_FromConfig_BakesOverride(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return kukeonv1.CreateCellResult{
				Cell: doc, Created: true, MetadataExistsPost: true,
				CgroupCreated: true, CgroupExistsPost: true,
				RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
				Containers: []kukeonv1.ContainerCreationOutcome{{Name: "main", ExistsPost: true, Created: true}},
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-config", "prod", "--env", "LABEL=bug", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createDoc.Spec.RuntimeEnv != nil {
		t.Errorf("RuntimeEnv=%v want nil (--from-config --env is a baked override, not runtime injection)",
			fc.createDoc.Spec.RuntimeEnv)
	}
	prov := fc.createDoc.Spec.Provenance
	if prov == nil {
		t.Fatalf("Provenance nil; want the --env override recorded")
	}
	if !reflect.DeepEqual(prov.EnvOverrides, []string{"LABEL=bug"}) {
		t.Errorf("Provenance.EnvOverrides=%v want [LABEL=bug]", prov.EnvOverrides)
	}
}

// TestRun_EnvFlag_StoppedCell_StartsWithRuntimeEnv covers the Stopped
// → Started transition with --env: a prior `kuke run prod` created and
// then stopped the cell; a follow-up `kuke run prod --env LABEL=bug`
// reaches StartCell and the doc carries the per-invocation RuntimeEnv.
// Mirrors TestRun_ExistingCell_MatchingSpec_Stopped_StartsAndAttaches
// but exercises the --env wiring on the StartCell path.
func TestRun_EnvFlag_StoppedCell_StartsWithRuntimeEnv(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "my-cell"},
		Spec: v1beta1.CellSpec{
			RealmID: "my-realm", SpaceID: "my-space", StackID: "my-stack",
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "root",
					Root:    true,
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "sleep",
					Args:    []string{"3600"},
				},
				{ID: "work", Image: "registry.eminwux.com/busybox:latest", Command: "sleep", Args: []string{"3600"}},
			},
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
	}
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: existing, MetadataExists: true, CgroupExists: true, RootContainerExists: true,
			}, nil
		},
		startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"-f", writeTempYAML(t, validCellYAML),
		"--env", "LABEL=bug",
		"-d",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.startCalls != 1 {
		t.Fatalf("StartCell calls=%d, want 1", fc.startCalls)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(fc.startDoc.Spec.RuntimeEnv, want) {
		t.Errorf("StartCell doc RuntimeEnv=%v, want %v (-f Stopped restart path)",
			fc.startDoc.Spec.RuntimeEnv, want)
	}
}

// pingTimeoutErr returns an error chain that mirrors sbsh's wrap shape
// from clientrunner/io.go's dialTerminalCtrlSocket — `fmt.Errorf("ping
// failed: %w", err)` with context.DeadlineExceeded in the chain — so
// the runWithPingRetry classifier sees the same surface real sbsh
// returns when its 3 s ping window fires before kuketty's Serve()
// accept loop has come up (#926).
func pingTimeoutErr() error {
	return fmt.Errorf("ping failed: %w", context.DeadlineExceeded)
}

// TestRun_Attach_PingDeadline_RetriesWithinBudget pins the
// readiness-handshake guarantee from #926: when the first call into
// the in-process attach loop fails with sbsh's "ping failed: context
// deadline exceeded" (i.e. kuketty has bound the control socket but
// not yet entered Serve()'s Accept loop), runWithPingRetry must retry
// instead of surfacing the timeout to the operator. Pre-fix this test
// fails: the original runAttachLoop called run() exactly once and
// returned the ping-timeout straight through to ClassifyAttachExit.
func TestRun_Attach_PingDeadline_RetriesWithinBudget(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Tight budget keeps the test cheap on the budget-exhausted negative
	// path covered by the sibling test below; the retry path here only
	// needs one retry inside the budget so the production 10s default
	// would also pass — overridden for symmetry with the negative test.
	restore := runcmd.SetAttachPingRetryForTest(500*time.Millisecond, 10*time.Millisecond)
	t.Cleanup(restore)

	var calls int
	var mu sync.Mutex
	runFn := runcmd.RunFn(func(_ context.Context, _ sbshattach.Options) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return pingTimeoutErr()
		}
		return nil
	})

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	cmd, _ := newCmdWithRunFn(t, fc, runFn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (want nil — retry must absorb the ping-timeout)", err)
	}
	if fc.attachCalls != 1 {
		t.Errorf(
			"AttachContainer calls=%d, want 1 (HostSocketPath resolved once; retries are on the dial side)",
			fc.attachCalls,
		)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("attach loop calls=%d, want 2 (one ping-timeout, one success)", calls)
	}
}

// TestRun_Attach_PingDeadline_BudgetExhausted_WrapsSentinel pins the
// budget-exhausted surface: when sbsh keeps firing ping-deadline past
// the configured budget, runWithPingRetry must surface the timeout
// class via errdefs.ErrAttachPingTimeout so callers can errors.Is the
// readiness-handshake failure without string-matching sbsh's wrap.
func TestRun_Attach_PingDeadline_BudgetExhausted_WrapsSentinel(t *testing.T) {
	t.Cleanup(viper.Reset)

	restore := runcmd.SetAttachPingRetryForTest(50*time.Millisecond, 10*time.Millisecond)
	t.Cleanup(restore)

	var calls int
	var mu sync.Mutex
	runFn := runcmd.RunFn(func(_ context.Context, _ sbshattach.Options) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return pingTimeoutErr()
	})

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	cmd, _ := newCmdWithRunFn(t, fc, runFn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachPingTimeout) {
		t.Fatalf("err=%v, want chain to include errdefs.ErrAttachPingTimeout", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf(
			"err=%v, want chain to preserve context.DeadlineExceeded so operators can still see the underlying cause",
			err,
		)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Errorf("attach loop calls=%d, want >= 2 (at least one retry before budget exhaustion)", calls)
	}
}

// TestRun_Attach_NonPingError_DoesNotRetry pins the negative side of
// the classifier: an error that is not a context-deadline-exceeded
// class (e.g. a generic controller failure) must NOT trigger retry,
// otherwise the retry loop would mask real failures behind a 10 s
// budget on every kuke run.
func TestRun_Attach_NonPingError_DoesNotRetry(t *testing.T) {
	t.Cleanup(viper.Reset)

	restore := runcmd.SetAttachPingRetryForTest(500*time.Millisecond, 10*time.Millisecond)
	t.Cleanup(restore)

	sentinel := errors.New("controller-level failure (not a ping timeout)")
	var calls int
	var mu sync.Mutex
	runFn := runcmd.RunFn(func(_ context.Context, _ sbshattach.Options) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return sentinel
	})

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	cmd, _ := newCmdWithRunFn(t, fc, runFn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

	err := cmd.Execute()
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v, want chain to include the injected non-ping sentinel", err)
	}
	if errors.Is(err, errdefs.ErrAttachPingTimeout) {
		t.Errorf("err=%v, must NOT be wrapped with ErrAttachPingTimeout — non-ping errors are not the retry class", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("attach loop calls=%d, want 1 (non-ping errors must not retry)", calls)
	}
}

// staleSocketErr returns an error chain that mirrors the surface
// `kuke run` sees when sbsh's pkg/attach dial(2) hits a stale pre-fix
// kuketty tty socket — `ping failed: dial unix /opt/kukeon/s/<hash>:
// connect: permission denied`. The chain carries syscall.EACCES so
// the runWithPingRetry classifier sees the same surface real sbsh
// returns through net.OpError → os.SyscallError → syscall.Errno
// (#933).
func staleSocketErr() error {
	return fmt.Errorf("ping failed: dial unix /opt/kukeon/s/abc: connect: %w", syscall.EACCES)
}

// staleSocketENOENTErr returns an error chain that mirrors the
// sub-millisecond Remove→Listen gap inside new kuketty's init path —
// the stale inode has been unlinked but the replacement bind(2) has
// not landed, so dial(2) returns ENOENT. Defense-in-depth path
// alongside the dominant EACCES window (#933).
func staleSocketENOENTErr() error {
	return fmt.Errorf("ping failed: dial unix /opt/kukeon/s/abc: connect: %w", syscall.ENOENT)
}

// TestRun_Attach_StaleSocket_EACCES_RetriesWithinBudget pins the
// stale-socket guarantee from #933: when the first call into the
// in-process attach loop fails with EACCES against a stale pre-fix
// kuketty tty socket (mode 0o640, group-read only), runWithPingRetry
// must retry instead of surfacing `connect: permission denied` to the
// operator. Pre-fix this test fails: the original classifier matched
// only context.DeadlineExceeded, so EACCES propagated straight through.
func TestRun_Attach_StaleSocket_EACCES_RetriesWithinBudget(t *testing.T) {
	t.Cleanup(viper.Reset)

	restore := runcmd.SetAttachPingRetryForTest(500*time.Millisecond, 10*time.Millisecond)
	t.Cleanup(restore)

	var calls int
	var mu sync.Mutex
	runFn := runcmd.RunFn(func(_ context.Context, _ sbshattach.Options) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return staleSocketErr()
		}
		return nil
	})

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	cmd, _ := newCmdWithRunFn(t, fc, runFn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (want nil — retry must absorb the stale-socket EACCES)", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("attach loop calls=%d, want 2 (one EACCES, one success)", calls)
	}
}

// TestRun_Attach_StaleSocket_EACCES_BudgetExhausted_WrapsSentinel pins
// the budget-exhausted surface for the stale-socket class: when dial(2)
// keeps firing EACCES past the budget, runWithPingRetry must surface
// the readiness-race class via errdefs.ErrAttachStaleSocket so callers
// can errors.Is the stale-socket failure without sniffing for raw
// syscall.EACCES (#933).
func TestRun_Attach_StaleSocket_EACCES_BudgetExhausted_WrapsSentinel(t *testing.T) {
	t.Cleanup(viper.Reset)

	restore := runcmd.SetAttachPingRetryForTest(50*time.Millisecond, 10*time.Millisecond)
	t.Cleanup(restore)

	var calls int
	var mu sync.Mutex
	runFn := runcmd.RunFn(func(_ context.Context, _ sbshattach.Options) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return staleSocketErr()
	})

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	cmd, _ := newCmdWithRunFn(t, fc, runFn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachStaleSocket) {
		t.Fatalf("err=%v, want chain to include errdefs.ErrAttachStaleSocket", err)
	}
	if !errors.Is(err, syscall.EACCES) {
		t.Errorf(
			"err=%v, want chain to preserve syscall.EACCES so operators can still see the underlying cause",
			err,
		)
	}
	if errors.Is(err, errdefs.ErrAttachPingTimeout) {
		t.Errorf("err=%v, must NOT be wrapped with ErrAttachPingTimeout — EACCES is the stale-socket class, not ping-timeout", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Errorf("attach loop calls=%d, want >= 2 (at least one retry before budget exhaustion)", calls)
	}
}

// TestRun_Attach_StaleSocket_ENOENT_RetriesWithinBudget pins the
// defense-in-depth ENOENT retry: the sub-millisecond gap between
// kuketty's os.Remove of the stale inode and its listenUnixWithMode
// bind(2) on the replacement surfaces as ENOENT from dial(2), and
// runWithPingRetry must absorb that window the same way it absorbs
// the dominant EACCES one (#933).
func TestRun_Attach_StaleSocket_ENOENT_RetriesWithinBudget(t *testing.T) {
	t.Cleanup(viper.Reset)

	restore := runcmd.SetAttachPingRetryForTest(500*time.Millisecond, 10*time.Millisecond)
	t.Cleanup(restore)

	var calls int
	var mu sync.Mutex
	runFn := runcmd.RunFn(func(_ context.Context, _ sbshattach.Options) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return staleSocketENOENTErr()
		}
		return nil
	})

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	cmd, _ := newCmdWithRunFn(t, fc, runFn)
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (want nil — retry must absorb the stale-socket ENOENT)", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("attach loop calls=%d, want 2 (one ENOENT, one success)", calls)
	}
}

// --- epic:cell-identity #1025: cell-positional + fused create+start+attach ---

// sourceConfigCellForRun is a Config-lineage source cell (provenance points at
// configDoc()) used to exercise `kuke run --clone <cell>`.
func sourceConfigCellForRun() v1beta1.CellDoc {
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   "prod-src",
			Labels: map[string]string{"kukeon.io/config": "prod"},
		},
		Spec: v1beta1.CellSpec{
			ID: "prod-src", RealmID: "cfg-realm", SpaceID: "cfg-space", StackID: "cfg-stack",
			Containers: []v1beta1.ContainerSpec{
				{ID: "main", Image: "registry.example.com/web:v2", Attachable: true},
			},
			Provenance: &v1beta1.CellProvenance{
				BindingKind: v1beta1.BindingKindConfig,
				BindingRef:  v1beta1.CellBindingRef{Name: "prod", Realm: "cfg-realm"},
			},
		},
	}
}

// TestRun_Positional_NotFound_Errors covers `kuke run <cell>` against a cell
// that does not exist: there is no source to materialise from, so the verb
// errors (ErrCellNotFound) and points at the create paths rather than silently
// creating anything.
func TestRun_Positional_NotFound_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{} // getCellFn nil → ErrCellNotFound
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"ghost", "-d"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, errdefs.ErrCellNotFound) {
		t.Fatalf("err=%v want ErrCellNotFound", err)
	}
	if fc.createCalls != 0 || fc.startCalls != 0 {
		t.Errorf("CreateCell=%d StartCell=%d want both 0", fc.createCalls, fc.startCalls)
	}
}

// TestRun_Positional_Ready_Attaches covers `kuke run <cell>` (no -d) against a
// live Ready cell: no CreateCell/StartCell, just attach to its attachable
// container.
func TestRun_Positional_Ready_Attaches(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			if doc.Metadata.Name != "live" {
				t.Errorf("GetCell name=%q want live", doc.Metadata.Name)
			}
			live := doc
			live.Status.State = v1beta1.CellStateReady
			live.Spec.Containers = []v1beta1.ContainerSpec{
				{ID: "main", Image: "registry.example.com/web:v2", Attachable: true},
			}
			return kukeonv1.GetCellResult{
				Cell: live, MetadataExists: true,
				RootContainerExists: true, RootContainerTaskRunning: true,
			}, nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"live"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 0 || fc.startCalls != 0 {
		t.Errorf("CreateCell=%d StartCell=%d want both 0 (Ready short-circuit)", fc.createCalls, fc.startCalls)
	}
	if run.calls != 1 {
		t.Errorf("attach loop calls=%d want 1", run.calls)
	}
}

// TestRun_FromClone_CreatesStartsAttaches covers `kuke run --clone <cell>`: the
// fused form forks the source cell's recipe via the shared cell.Materialize and
// create+starts the clone via CreateCell (the create cell verb would
// MaterializeCell and leave it stopped). The materialisation details are
// covered by cmd/kuke/create/cell's clone tests; here we verify run's dispatch.
func TestRun_FromClone_CreatesStartsAttaches(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceConfigCellForRun()
	fc := &fakeClient{
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			if doc.Metadata.Name == src.Metadata.Name {
				return kukeonv1.GetCellResult{Cell: src, MetadataExists: true}, nil
			}
			return kukeonv1.GetCellResult{MetadataExists: false}, errdefs.ErrCellNotFound
		},
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--clone", src.Metadata.Name, "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1 (fused clone create+start)", fc.createCalls)
	}
	// Auto-name <source>-<6hex>, source-cell annotation, lineage label inherited.
	if got := fc.createDoc.Metadata.Name; !regexp.MustCompile(`^prod-src-[0-9a-f]{6}$`).MatchString(got) {
		t.Errorf("clone name=%q want prod-src-<6hex>", got)
	}
	if got := fc.createDoc.Metadata.Annotations["kukeon.io/source-cell"]; got != src.Metadata.Name {
		t.Errorf("source-cell annotation=%q want %q", got, src.Metadata.Name)
	}
	if got := fc.createDoc.Metadata.Labels["kukeon.io/config"]; got != "prod" {
		t.Errorf("kukeon.io/config label=%q want prod (lineage inherited)", got)
	}
}

// TestRun_Fused_NameCollision_Refuses covers the fused create path with an
// explicit --name that collides with a live cell: the create form refuses
// (parity with `kuke create cell`) and points at `kuke run <cell>` for the
// start+attach-existing path. CreateCell must not fire.
func TestRun_Fused_NameCollision_Refuses(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			// The explicit --name "pinned" already exists.
			live := doc
			live.Status.State = v1beta1.CellStateReady
			return kukeonv1.GetCellResult{Cell: live, MetadataExists: true}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--from-blueprint", "web", "--name", "pinned", "--realm", "my-realm", "-d"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil want collision refusal")
	}
	for _, want := range []string{`cell "pinned" already exists`, "kuke run pinned", "kuke delete cell pinned"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err=%q missing substring %q", err, want)
		}
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (refuse on collision)", fc.createCalls)
	}
}

// TestRun_Name_RejectedWithPositionalAndFile covers validateSourceFlagCompat:
// --name names the cell only on the fused create path; the existing-cell
// positional and -f take their name from the positional / metadata.name.
func TestRun_Name_RejectedWithPositionalAndFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"positional",
			[]string{"mycell", "--name", "foo", "-d"},
			"--name is only valid with --from-blueprint/--from-config/--clone, not the <cell> positional",
		},
		{
			"file",
			[]string{"-f", "/tmp/never-read.yaml", "--name", "foo", "-d"},
			"--name is only valid with --from-blueprint/--from-config/--clone, not -f/--file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			fc := &fakeClient{}
			cmd, _ := newCmd(t, fc)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
		})
	}
}

// TestRun_RemovedFlags_Error covers AC#5: the retired sources -b/--blueprint,
// --new, and --reuse no longer exist on `kuke run`; invoking them errors.
func TestRun_RemovedFlags_Error(t *testing.T) {
	for _, args := range [][]string{
		{"-b", "web", "-d"},
		{"--blueprint", "web", "-d"},
		{"--new", "-d"},
		{"--reuse", "-d"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Cleanup(viper.Reset)
			fc := &fakeClient{}
			cmd, _ := newCmd(t, fc)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("Execute err=nil want unknown-flag error for %v", args)
			}
			if fc.createCalls != 0 || fc.startCalls != 0 {
				t.Errorf("CreateCell=%d StartCell=%d want both 0", fc.createCalls, fc.startCalls)
			}
		})
	}
}
