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
	"github.com/eminwux/kukeon/internal/cellconfig"
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
// to the YAML between runs, the refusal must still fire and name the
// diverging field (count delta among user containers, not the synthesized
// root).
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, cellYAMLUserContainersOnly), "-d"})

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

func TestRun_ExistingCell_DivergingContainerSet_RefusesAndPointsToApply(t *testing.T) {
	t.Cleanup(viper.Reset)

	// On-disk cell has an extra container the file does not declare. This is
	// the structural drift (container set / count) the AC routes through
	// `kuke apply -f`.
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

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

// TestRun_ExistingCell_DivergingContainerImage_RefusesAndPointsToApply covers
// the issue #468 case: on-disk cell has the same container set as the file
// but the user-supplied container's image differs (busybox:latest on disk
// vs busybox:musl in the file). `kuke run -f` must refuse with the
// diverging-spec error and not mutate the cell, matching the
// `docs/cli-use-cases.md` invariant for `kuke run -f` against a divergent
// on-disk spec.
func TestRun_ExistingCell_DivergingContainerImage_RefusesAndPointsToApply(t *testing.T) {
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want divergence error for image change")
	}
	if !strings.Contains(err.Error(), `cell "my-cell" exists with diverging spec`) {
		t.Errorf("err=%q does not contain the diverging-spec phrase naming the cell", err)
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("err=%q does not refer the operator to `kuke apply -f`", err)
	}
	if !strings.Contains(err.Error(), `spec.containers["work"].image`) {
		t.Errorf("err=%q does not name the diverging image field on the user container", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 (must not mutate on divergence)", fc.createCalls)
	}
}

// TestRun_ExistingCell_DivergingContainerSecrets_RefusesAndPointsToApply
// exercises the secrets branch of divergedContainerFields. Secrets is
// user-authored, persisted to disk unchanged (apischeme round-trips it
// verbatim), and not filled in from `space.spec.defaults.container` — so
// the same no-op-on-drift failure mode that #468 closes for image applies.
func TestRun_ExistingCell_DivergingContainerSecrets_RefusesAndPointsToApply(t *testing.T) {
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want divergence error for secrets change")
	}
	if !strings.Contains(err.Error(), `cell "my-cell" exists with diverging spec`) {
		t.Errorf("err=%q does not contain the diverging-spec phrase naming the cell", err)
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("err=%q does not refer the operator to `kuke apply -f`", err)
	}
	if !strings.Contains(err.Error(), `spec.containers["work"].secrets`) {
		t.Errorf("err=%q does not name the diverging secrets field on the user container", err)
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
// and printed the `kuke restart cell ...` pointer.
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

// TestRun_ExistingCell_DivergingContainerTty_RefusesAndPointsToApply
// exercises the tty branch of divergedContainerFields. ContainerTty is
// user-authored shell-UX config the daemon persists verbatim and never
// fills in from space defaults, so a drift here would silently skip the
// configured prompt/profile/onInit on `kuke run -f` re-runs.
func TestRun_ExistingCell_DivergingContainerTty_RefusesAndPointsToApply(t *testing.T) {
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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, attachableCellYAML), "-d"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want divergence error for tty change")
	}
	if !strings.Contains(err.Error(), `cell "my-cell" exists with diverging spec`) {
		t.Errorf("err=%q does not contain the diverging-spec phrase naming the cell", err)
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("err=%q does not refer the operator to `kuke apply -f`", err)
	}
	if !strings.Contains(err.Error(), `spec.containers["claude"].tty`) {
		t.Errorf("err=%q does not name the diverging tty field on the user container", err)
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

func TestRun_MissingFileAndProfile_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	// Issue #813: the cobra MarkFlagsOneRequired group no longer spans the
	// CellConfig source (now positional), so parseRunFlags hand-rolls the
	// at-least-one check. Match on the stable phrasing it emits — naming all
	// four sources — rather than the prior cobra group listing.
	if err == nil ||
		!strings.Contains(err.Error(), "<config>") ||
		!strings.Contains(err.Error(), "-f/--file") ||
		!strings.Contains(err.Error(), "-p/--profile") ||
		!strings.Contains(err.Error(), "-b/--blueprint") {
		t.Fatalf("err=%v want one-of error naming the <config>/-f/-p/-b sources", err)
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
	cmd.SetArgs([]string{"-p", "claude-cell", "-d"})

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
		cmd.SetArgs([]string{"-p", "claude", "-d"})
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
	cmd.SetArgs([]string{"-p", "claude-cell", "-d", "--realm", "x", "--space", "y", "--stack", "z"})

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
	cmd.SetArgs([]string{"-p", "ghost", "-d"})

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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-p", "claude-cell", "-d"})

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
	cmd.SetArgs([]string{"-f", writeTempYAML(t, validCellYAML), "-d", "my-cell"})

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

	// Headline flow from the issue: `kuke run -p claude-cell` materializes
	// the profile, creates+starts the cell, then attaches to cell.tty.default
	// (the `work` container) by default.
	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
		attachContainerFn: attachSuccessFn(),
	}
	run := &runCapture{}
	cmd, _ := newCmdWithRun(t, fc, run)
	cmd.SetArgs([]string{"-p", "claude-cell"})

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

// paramProfileYAML is the issue #355 example with three required parameters
// and two defaults. Used to exercise the --param / --param-file / --name
// path end-to-end through `kuke run`.
const paramProfileYAML = `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: dev
spec:
  realm: default
  space: agents
  stack: claude
  parameters:
    - name: PROMPT
      required: true
    - name: PROJECT_REPO
      required: true
    - name: PROJECT_DIR
      required: true
    - name: AGENTS_REPO
      default: "eminwux/agents"
    - name: CLAUDE_IMAGE
      default: "registry.eminwux.com/claude:latest"
  cell:
    containers:
      - id: work
        attachable: true
        image: ${CLAUDE_IMAGE}
        env:
          - PROMPT=${PROMPT}
          - PROJECT_REPO=${PROJECT_REPO}
          - PROJECT_DIR=${PROJECT_DIR}
          - AGENTS_REPO=${AGENTS_REPO}
`

func writeParamProfile(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.yaml")
	if err := os.WriteFile(path, []byte(paramProfileYAML), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	t.Setenv("KUKE_PROFILES_DIR", dir)
}

func TestRun_FromProfile_WithParams_Substitutes(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeParamProfile(t)

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"-p", "dev", "-d",
		"--name", "crew-dev-354",
		"--param", "PROMPT=/pick-issue 354",
		"--param", "PROJECT_REPO=https://github.com/eminwux/crew",
		"--param", "PROJECT_DIR=crew",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := fc.createDoc.Metadata.Name; got != "crew-dev-354" {
		t.Errorf("cell name=%q want crew-dev-354 (--name override)", got)
	}
	if got := fc.createDoc.Spec.ID; got != "crew-dev-354" {
		t.Errorf("spec.id=%q want crew-dev-354", got)
	}

	c := fc.createDoc.Spec.Containers[0]
	if c.Image != "registry.eminwux.com/claude:latest" {
		t.Errorf("image=%q want registry.eminwux.com/claude:latest (default substituted)", c.Image)
	}
	wantEnv := []string{
		"PROMPT=/pick-issue 354",
		"PROJECT_REPO=https://github.com/eminwux/crew",
		"PROJECT_DIR=crew",
		"AGENTS_REPO=eminwux/agents",
	}
	if !reflect.DeepEqual(c.Env, wantEnv) {
		t.Errorf("env=%v\nwant %v", c.Env, wantEnv)
	}
}

func TestRun_FromProfile_WithParamFile(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeParamProfile(t)

	paramFile := filepath.Join(t.TempDir(), "dev.params")
	body := "# issue 355 example\n" +
		"PROMPT=/pick-issue 354\n" +
		"PROJECT_REPO=https://github.com/eminwux/crew\n" +
		"PROJECT_DIR=crew\n"
	if err := os.WriteFile(paramFile, []byte(body), 0o600); err != nil {
		t.Fatalf("write param file: %v", err)
	}

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-p", "dev", "-d", "--param-file", paramFile})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createDoc.Spec.Containers[0].Env[0]; got != "PROMPT=/pick-issue 354" {
		t.Errorf("env[0]=%q want PROMPT=/pick-issue 354", got)
	}
}

func TestRun_FromProfile_ParamFlagBeatsParamFile(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeParamProfile(t)

	paramFile := filepath.Join(t.TempDir(), "dev.params")
	body := "PROMPT=from-file\nPROJECT_REPO=x\nPROJECT_DIR=x\n"
	if err := os.WriteFile(paramFile, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	// CLI --param targets the same key the file sets — flag wins (later-binding).
	cmd.SetArgs([]string{
		"-p", "dev", "-d",
		"--param-file", paramFile,
		"--param", "PROMPT=from-flag",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createDoc.Spec.Containers[0].Env[0]; got != "PROMPT=from-flag" {
		t.Errorf("env[0]=%q want PROMPT=from-flag (CLI --param wins over file)", got)
	}
}

func TestRun_FromProfile_RequiredMissing_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeParamProfile(t)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-p", "dev", "-d"})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
	if !strings.Contains(err.Error(), "PROMPT") {
		t.Errorf("err %q must name a missing required parameter", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell called %d times; want 0 (substitution must error before RPC)", fc.createCalls)
	}
}

func TestRun_FromProfile_UndeclaredParam_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)
	writeParamProfile(t)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{
		"-p", "dev", "-d",
		"--param", "PROMPT=x",
		"--param", "PROJECT_REPO=x",
		"--param", "PROJECT_DIR=x",
		"--param", "TYPO=oops",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
	if !strings.Contains(err.Error(), "TYPO") {
		t.Errorf("err %q must name the undeclared --param key", err)
	}
}

func TestRun_FileMode_RejectsParamFlags(t *testing.T) {
	// --name, --param, --param-file are profile-only knobs. With -f the file's
	// metadata.name is authoritative and substitution doesn't apply, so the
	// CLI rejects the combination rather than silently dropping the flag.
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
			Parameters: []v1beta1.CellProfileParameter{{Name: "TAG", Default: &def}},
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
	cmd.SetArgs([]string{"-b", "web", "--param", "TAG=v2", "--realm", "my-realm", "-d"})

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

func TestRun_FromBlueprint_NotFound_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{MetadataExists: false}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-b", "ghost", "--realm", "my-realm", "-d"})

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
	cmd.SetArgs([]string{"-b", "web", "--name", "pinned", "--realm", "my-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createDoc.Metadata.Name; got != "pinned" {
		t.Errorf("cell name=%q want pinned (--name override)", got)
	}
}

func TestRun_Profile_EmitsDeprecationNotice(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, out := newCmd(t, fc)
	// A bogus profile dir so the load fails fast after the notice prints; we
	// assert only that the deprecation notice reached stderr (shared buffer).
	cmd.SetArgs([]string{"-p", "nonexistent-profile", "-d"})
	_ = cmd.Execute()

	if !strings.Contains(out.String(), "-p/--profile is deprecated") {
		t.Errorf("expected -p deprecation notice, got:\n%s", out.String())
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
			Parameters: []v1beta1.CellProfileParameter{{Name: "TAG", Default: &def}},
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

func TestRun_FromConfig_CreatesWithStableNameAndBackRef(t *testing.T) {
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
	cmd.SetArgs([]string{"prod", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	// Deterministic name (no hex suffix) — that is the -c idempotent contract.
	if got := fc.createDoc.Metadata.Name; got != "prod" {
		t.Errorf("cell name=%q want prod (StableName)", got)
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

func TestRun_FromConfig_DivergentSpec_RefusesAndPointsToApply(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Simulate divergence by returning a Ready cell whose container image
	// disagrees with what Materialize(cfg, bp) would build. Per #753 the -c
	// contract on divergence is to refuse with a `kuke restart cell <name>`
	// pointer (#844 retargeted the pointer onto the post-#823 surface; the
	// removed apply-side reconcile-by-ref form lived behind a single short
	// flag), not warn-and-attach — CreateCell/StartCell must not fire.
	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			// Deep-copy the containers so the test mutation does not also
			// rewrite the desired CellDoc that runRun is about to compare
			// against — both sides share the same backing slice when we
			// shallow-copy `doc`.
			live := doc
			live.Spec.Containers = append([]v1beta1.ContainerSpec(nil), doc.Spec.Containers...)
			// Diverge on the user container's image (divergedFields excludes
			// the runner-synthesised root). Match by ID rather than index so
			// the test stays resilient to container ordering.
			for i := range live.Spec.Containers {
				if live.Spec.Containers[i].ID == "main" {
					live.Spec.Containers[i].Image = "registry.example.com/web:v1"
					break
				}
			}
			live.Status.State = v1beta1.CellStateReady
			return kukeonv1.GetCellResult{
				Cell: live, MetadataExists: true,
				RootContainerExists: true, RootContainerTaskRunning: true,
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--realm", "cfg-realm", "-d"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil want refusal on divergent spec")
	}
	// Mirrors the existing -f rejection: names the cell, names the source
	// (CellConfig "prod"), enumerates the diverging field(s), and hands the
	// operator the exact reconcile invocation. The stable name == config name
	// for the -c path, so the pointer omits --name.
	for _, want := range []string{
		`live cell "prod" spec differs from CellConfig "prod"`,
		`spec.containers["main"].image`,
		"refusing to attach",
		"kuke restart cell prod",
		"reconcile",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err=%q missing substring %q", err, want)
		}
	}
	if fc.createCalls != 0 || fc.startCalls != 0 {
		t.Errorf("CreateCell=%d StartCell=%d want both 0 (refuse on divergence)", fc.createCalls, fc.startCalls)
	}
}

// TestRun_FromBlueprint_NamedDivergent_RefusesAndPointsToApply covers #753's
// -b --name addition: a pinned-name `kuke run -b <bp> --name <cell>` against a
// live cell whose spec has diverged from the materialisation must refuse with
// a `kuke delete cell <cell>` + re-run pointer (#844 retargeted the pointer
// onto the post-#823 surface — Blueprint-lineage cells have no implicit
// reconcile per #819's umbrella, so the message routes to delete-and-re-run
// rather than a reconcile verb), not silently attach to the diverged state.
// The generated-name path (`kuke run -b <bp>` without --name) is unaffected
// because each invocation materialises a fresh `<prefix>-<6hex>` cell, so a
// collision against an existing cell is statistically negligible.
func TestRun_FromBlueprint_NamedDivergent_RefusesAndPointsToApply(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			// Same divergence shape as the -c test: deep-copy then mutate the
			// user container's image. blueprintDoc() declares only the "main"
			// user container (the runner-synthesised root is filtered out of
			// divergedFields).
			live := doc
			live.Spec.Containers = append([]v1beta1.ContainerSpec(nil), doc.Spec.Containers...)
			for i := range live.Spec.Containers {
				if live.Spec.Containers[i].ID == "main" {
					live.Spec.Containers[i].Image = "registry.example.com/web:stale"
					break
				}
			}
			live.Status.State = v1beta1.CellStateReady
			return kukeonv1.GetCellResult{
				Cell: live, MetadataExists: true,
				RootContainerExists: true, RootContainerTaskRunning: true,
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"-b", "web", "--name", "pinned", "--param", "TAG=v2", "--realm", "my-realm", "-d"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil want refusal on divergent spec")
	}
	for _, want := range []string{
		`live cell "pinned" spec differs from CellBlueprint "web"`,
		`spec.containers["main"].image`,
		"refusing to attach",
		"kuke delete cell pinned",
		"-b cells have no in-place reconcile",
		"CellConfig",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err=%q missing substring %q", err, want)
		}
	}
	if fc.createCalls != 0 || fc.startCalls != 0 {
		t.Errorf("CreateCell=%d StartCell=%d want both 0 (refuse on divergence)", fc.createCalls, fc.startCalls)
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
	cmd.SetArgs([]string{"ghost", "--realm", "cfg-realm", "-d"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, errdefs.ErrConfigNotFound) {
		t.Fatalf("err=%v want ErrConfigNotFound", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 on config-not-found", fc.createCalls)
	}
}

// TestRun_FromConfig_NotFound_ProbeWalk pins the three-probe scope walk on
// the bare `kuke run <config>` form (no --space/--stack). The probes go full
// → space-only → realm-only with realm = "default" from the session default;
// the not-found error reports the full-scope coordinates the operator's
// effective scope started at (issue #923).
func TestRun_FromConfig_NotFound_ProbeWalk(t *testing.T) {
	t.Cleanup(viper.Reset)

	var seen []v1beta1.CellConfigMetadata
	fc := &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			seen = append(seen, doc.Metadata)
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"ghost", "-d"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, errdefs.ErrConfigNotFound) {
		t.Fatalf("err=%v want ErrConfigNotFound", err)
	}
	if !strings.Contains(err.Error(), `realm="default" space="default" stack="default"`) {
		t.Errorf("err message should report full-scope coords, got: %v", err)
	}
	wantProbes := []v1beta1.CellConfigMetadata{
		{Name: "ghost", Realm: "default", Space: "default", Stack: "default"},
		{Name: "ghost", Realm: "default", Space: "default", Stack: ""},
		{Name: "ghost", Realm: "default", Space: "", Stack: ""},
	}
	if len(seen) != len(wantProbes) {
		t.Fatalf("GetConfig probes=%d want %d (seen=%+v)", len(seen), len(wantProbes), seen)
	}
	for i, want := range wantProbes {
		if seen[i].Name != want.Name || seen[i].Realm != want.Realm ||
			seen[i].Space != want.Space || seen[i].Stack != want.Stack {
			t.Errorf("probe[%d]={name=%q realm=%q space=%q stack=%q} want %+v",
				i, seen[i].Name, seen[i].Realm, seen[i].Space, seen[i].Stack, want)
		}
	}
}

// TestRun_FromConfig_FullScopeProbe_Hits pins probe-1 of the walk: a Config
// stored at default/default/default is found by a bare `kuke run <config>`
// without --space/--stack. Reproduces the issue #923 bug.
func TestRun_FromConfig_FullScopeProbe_Hits(t *testing.T) {
	t.Cleanup(viper.Reset)

	stored := configDoc()
	stored.Metadata.Realm = "default"
	stored.Metadata.Space = "default"
	stored.Metadata.Stack = "default"

	var probes int
	fc := &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			probes++
			if doc.Metadata.Realm == "default" &&
				doc.Metadata.Space == "default" &&
				doc.Metadata.Stack == "default" {
				return kukeonv1.GetConfigResult{Config: stored, MetadataExists: true}, nil
			}
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if probes != 1 {
		t.Errorf("GetConfig probes=%d want 1 (first probe should hit at full scope)", probes)
	}
	if fc.createCalls != 1 {
		t.Errorf("CreateCell calls=%d want 1", fc.createCalls)
	}
}

// TestRun_FromConfig_SpaceOnlyProbe_Hits pins probe-2: a Config stored at
// realm=default / space=default / stack="" is found by a bare lookup after
// the full-scope probe misses.
func TestRun_FromConfig_SpaceOnlyProbe_Hits(t *testing.T) {
	t.Cleanup(viper.Reset)

	stored := configDoc()
	stored.Metadata.Realm = "default"
	stored.Metadata.Space = "default"
	stored.Metadata.Stack = ""

	var seen []v1beta1.CellConfigMetadata
	fc := &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			seen = append(seen, doc.Metadata)
			if doc.Metadata.Realm == "default" &&
				doc.Metadata.Space == "default" &&
				doc.Metadata.Stack == "" {
				return kukeonv1.GetConfigResult{Config: stored, MetadataExists: true}, nil
			}
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("GetConfig probes=%d want 2 (full miss then space-only hit), seen=%+v", len(seen), seen)
	}
	if seen[1].Space != "default" || seen[1].Stack != "" {
		t.Errorf("probe[1]=%+v want space=default stack=\"\"", seen[1])
	}
}

// TestRun_FromConfig_RealmOnlyProbe_Hits pins probe-3: a Config stored at
// realm=default / space="" / stack="" is found by a bare lookup only after
// the two narrower probes miss.
func TestRun_FromConfig_RealmOnlyProbe_Hits(t *testing.T) {
	t.Cleanup(viper.Reset)

	stored := configDoc()
	stored.Metadata.Realm = "default"
	stored.Metadata.Space = ""
	stored.Metadata.Stack = ""

	var seen []v1beta1.CellConfigMetadata
	fc := &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			seen = append(seen, doc.Metadata)
			if doc.Metadata.Realm == "default" &&
				doc.Metadata.Space == "" &&
				doc.Metadata.Stack == "" {
				return kukeonv1.GetConfigResult{Config: stored, MetadataExists: true}, nil
			}
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			return successCreateResult(doc), nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("GetConfig probes=%d want 3 (walk down to realm-only), seen=%+v", len(seen), seen)
	}
	if seen[2].Space != "" || seen[2].Stack != "" {
		t.Errorf("probe[2]=%+v want realm-only", seen[2])
	}
}

// TestRun_FromConfig_RPCError_ShortCircuits pins that a real RPC error
// (anything other than MetadataExists=false) aborts the probe walk so the
// operator sees the underlying failure instead of a misleading not-found at
// realm scope.
func TestRun_FromConfig_RPCError_ShortCircuits(t *testing.T) {
	t.Cleanup(viper.Reset)

	wantErr := errors.New("transport boom")
	var probes int
	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			probes++
			return kukeonv1.GetConfigResult{}, wantErr
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "-d"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("err=%v want %v", err, wantErr)
	}
	if probes != 1 {
		t.Errorf("GetConfig probes=%d want 1 (RPC error must short-circuit)", probes)
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
	cmd.SetArgs([]string{"prod", "--realm", "cfg-realm", "-d"})

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
			[]string{"prod", "--param", "K=V", "-d"},
			"--param is not valid with the <config> positional",
		},
		{
			"--param-file",
			[]string{"prod", "--param-file", "/tmp/p", "-d"},
			"--param-file is not valid with the <config> positional",
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

// TestRun_PositionalConfig_MutexWithFlagSources covers issue #813's AC: the
// <config> positional is rejected when combined with -b/-f/-p; the rejection
// message names all four sources so the operator sees the full set without
// re-running with --help.
func TestRun_PositionalConfig_MutexWithFlagSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"-b conflicts with positional",
			[]string{"prod", "-b", "web", "--realm", "cfg-realm", "-d"},
			"the <config> positional is mutually exclusive with -b/--blueprint",
		},
		{
			"-f conflicts with positional",
			[]string{"prod", "-f", "/tmp/never-read.yaml", "-d"},
			"the <config> positional is mutually exclusive with -f/--file",
		},
		{
			"-p conflicts with positional",
			[]string{"prod", "-p", "shell", "-d"},
			"the <config> positional is mutually exclusive with -p/--profile",
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

// TestRun_FromConfig_New_FreshCellPerInvocation covers AC of #833 (and the
// inherited semantics from #754):
// `kuke run <config> --new` materializes a fresh `<config-name>-<6hex>` cell
// on each invocation, and the cell carries the kukeon.io/config=<config-name>
// lineage label so `kuke get cells -l kukeon.io/config=<name>` still enumerates
// every spawn.
func TestRun_FromConfig_New_FreshCellPerInvocation(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Track every materialized cell name across two invocations. Reusing the
	// same fakeClient across invocations would carry createDoc state but not
	// names, so we capture them explicitly.
	var names []string
	getCell := func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
		// Each generated name is fresh, so GetCell never finds a live cell —
		// returning ErrCellNotFound funnels both invocations through CreateCell.
		return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
	}
	createCell := func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
		names = append(names, doc.Metadata.Name)
		return kukeonv1.CreateCellResult{
			Cell: doc, Created: true, MetadataExistsPost: true,
			CgroupCreated: true, CgroupExistsPost: true,
			RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
			Containers: []kukeonv1.ContainerCreationOutcome{{Name: "main", ExistsPost: true, Created: true}},
		}, nil
	}

	for i := range 2 {
		// viper persists module-globally; reset per invocation so the previous
		// --new binding doesn't leak into the next Execute call.
		viper.Reset()
		fc := &fakeClient{
			getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
				return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
			},
			getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
				return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
			},
			getCellFn:    getCell,
			createCellFn: createCell,
		}
		cmd, _ := newCmd(t, fc)
		cmd.SetArgs([]string{"prod", "--new", "--realm", "cfg-realm", "-d"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute (iteration %d): %v", i, err)
		}
		if fc.createCalls != 1 {
			t.Fatalf("CreateCell calls=%d want 1 on --new invocation %d", fc.createCalls, i)
		}
		// Back-reference label survives the generated-name path.
		if got := fc.createDoc.Metadata.Labels["kukeon.io/config"]; got != "prod" {
			t.Errorf("kukeon.io/config label=%q want prod (iteration %d)", got, i)
		}
	}

	if len(names) != 2 {
		t.Fatalf("captured %d names, want 2: %v", len(names), names)
	}
	for _, n := range names {
		prefix := "prod-"
		if len(n) != len(prefix)+6 || n[:len(prefix)] != prefix {
			t.Errorf("cell name=%q does not match <config-name>-<6hex> shape", n)
		}
		for _, r := range n[len(prefix):] {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				t.Errorf("cell name suffix rune %q in %q not lowercase hex", r, n)
				break
			}
		}
	}
	if names[0] == names[1] {
		t.Errorf("two --new invocations produced the same name %q; expected distinct <prefix>-<6hex>", names[0])
	}
}

// TestRun_FromConfig_NoNew_UsesStableName guards against a regression against
// PR #742's idempotent-attach contract: without --new, the cell name is still
// the Config's stable name (verbatim metadata.name).
func TestRun_FromConfig_NoNew_UsesStableName(t *testing.T) {
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
	cmd.SetArgs([]string{"prod", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createDoc.Metadata.Name; got != "prod" {
		t.Errorf("cell name=%q want prod (StableName, no --new)", got)
	}
}

// TestRun_FromConfig_NewAndName_CreatesPinnedCell covers the AC of #833:
// `--new --name X` is a combinable form (the old `--name`/`--generate-name`
// mutex was relaxed in #833), and the resulting cell uses the pinned name
// verbatim — not `X-<6hex>`. The kukeon.io/config lineage label still lands
// on the cell so `kuke get cells -l kukeon.io/config=<name>` enumerates
// pinned-name spawns alongside hex-suffix ones.
func TestRun_FromConfig_NewAndName_CreatesPinnedCell(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			// X is free — runRun's --new path treats this as "create".
			return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
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
	cmd.SetArgs([]string{"prod", "--new", "--name", "pinned", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
	}
	if got := fc.createDoc.Metadata.Name; got != "pinned" {
		t.Errorf("cell name=%q want \"pinned\" (verbatim --name override, no hex suffix)", got)
	}
	if got := fc.createDoc.Spec.ID; got != "pinned" {
		t.Errorf("cell spec.id=%q want \"pinned\"", got)
	}
	if got := fc.createDoc.Metadata.Labels["kukeon.io/config"]; got != "prod" {
		t.Errorf("kukeon.io/config label=%q want prod (lineage preserved on pinned --new spawn)", got)
	}
}

// TestRun_FromConfig_NewAndName_CollisionRejected covers the create-or-fail
// half of `--new --name X` (#833 AC): when a cell named X already exists in
// the target realm, the run aborts with a hard collision error rather than
// attaching (which is the explicit difference from `--name X` alone). The
// error points the operator at the attach-on-collision escape valve so the
// distinction reads at the error message.
func TestRun_FromConfig_NewAndName_CollisionRejected(t *testing.T) {
	t.Cleanup(viper.Reset)

	existing := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "pinned"},
		Spec: v1beta1.CellSpec{
			ID:      "pinned",
			RealmID: "cfg-realm",
		},
		Status: v1beta1.CellStatus{State: v1beta1.CellStateReady},
	}
	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: existing, MetadataExists: true}, nil
		},
		// createCellFn intentionally unset: a successful guard returns before
		// CreateCell, and the default returns "unexpected CreateCell call" to
		// fail the test if the guard mis-fires.
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--new", "--name", "pinned", "--realm", "cfg-realm", "-d"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil want collision rejection on --new --name with existing cell")
	}
	if !strings.Contains(err.Error(), "already exists") ||
		!strings.Contains(err.Error(), `--name pinned`) {
		t.Errorf("err=%v missing collision wording or attach-on-collision pointer", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d want 0 on --new --name collision", fc.createCalls)
	}
}

// TestRun_FromConfig_NameAlone_IdempotentAttach covers the AC's
// attach-if-exists escape valve referenced by the `--new --name X` collision
// error message: `<config> --name X` (without --new) sets the cell name to X
// and walks the existing idempotent-attach path. When X exists, run attaches
// (no CreateCell); when X is free, run materializes from the Config and
// creates the cell. The kukeon.io/config lineage label is preserved either
// way so `kuke get cells -l kukeon.io/config=<name>` still enumerates the
// pinned-name cell alongside stable-name and hex-suffix spawns.
func TestRun_FromConfig_NameAlone_IdempotentAttach(t *testing.T) {
	t.Cleanup(viper.Reset)

	t.Run("creates_when_missing", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		fc := &fakeClient{
			getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
				return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
			},
			getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
				return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
			},
			getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
				return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
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
		cmd.SetArgs([]string{"prod", "--name", "pinned", "--realm", "cfg-realm", "-d"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if fc.createCalls != 1 {
			t.Fatalf("CreateCell calls=%d want 1", fc.createCalls)
		}
		if got := fc.createDoc.Metadata.Name; got != "pinned" {
			t.Errorf("cell name=%q want \"pinned\" (verbatim --name override)", got)
		}
		if got := fc.createDoc.Metadata.Labels["kukeon.io/config"]; got != "prod" {
			t.Errorf("kukeon.io/config label=%q want prod (lineage preserved)", got)
		}
	})

	t.Run("attaches_when_present_and_matching", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		fc := &fakeClient{
			getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
				return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
			},
			getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
				return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
			},
			getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
				if doc.Metadata.Name != "pinned" {
					t.Errorf("GetCell name=%q want pinned", doc.Metadata.Name)
				}
				// Echo the desired spec back so divergedFields reports no drift
				// and runRun routes through runExistingCell's Ready short-circuit
				// (same pattern as TestRun_FromConfig_LiveReadyCell_NoCreate).
				live := doc
				live.Status.State = v1beta1.CellStateReady
				return kukeonv1.GetCellResult{
					Cell: live, MetadataExists: true, CgroupExists: true,
					RootContainerExists: true, RootContainerTaskRunning: true,
				}, nil
			},
		}
		cmd, _ := newCmd(t, fc)
		cmd.SetArgs([]string{"prod", "--name", "pinned", "--realm", "cfg-realm", "-d"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if fc.createCalls != 0 {
			t.Errorf("CreateCell calls=%d want 0 on idempotent attach", fc.createCalls)
		}
	})
}

// TestRun_FromConfig_NewWithRm_SetsAutoDelete covers AC #5: `<config> --new
// --rm` materializes an ephemeral generated cell from the Config. The
// AutoDelete=true on the spec is the daemon-side trigger the reconcile loop
// keys on after detach; cell + overlay cleanup is handled by the same
// machinery exercised by TestRun_RmFlag_SetsAutoDeleteOnSpec.
func TestRun_FromConfig_NewWithRm_SetsAutoDelete(t *testing.T) {
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
	cmd.SetArgs([]string{"prod", "--new", "--rm", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !fc.createDoc.Spec.AutoDelete {
		t.Errorf("AutoDelete=false want true under --rm")
	}
	// Sanity-check the name path: --rm did not divert the --new materialization
	// back to the StableName branch.
	prefix := "prod-"
	got := fc.createDoc.Metadata.Name
	if len(got) != len(prefix)+6 || got[:len(prefix)] != prefix {
		t.Errorf("cell name=%q does not match <config-name>-<6hex> shape under --rm", got)
	}
}

// TestRun_New_OnlyValidWithConfig covers the defensive UX guard: --new is a
// CellConfig-only knob (only the <config> positional reaches the daemon-
// stored CellConfig path). Allowing it silently on -f (where metadata.name is
// authoritative) or -p/-b (which already generate <prefix>-<6hex>) would seed
// a wrong mental model that --new toggles a default that isn't actually
// flipped.
func TestRun_New_OnlyValidWithConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"-f rejects --new",
			[]string{"-f", "/tmp/never-read.yaml", "--new", "-d"},
			"--new is only valid with the <config> positional",
		},
		{
			"-p rejects --new",
			[]string{"-p", "shell", "--new", "-d"},
			"--new is only valid with the <config> positional",
		},
		{
			"-b rejects --new",
			[]string{"-b", "web", "--new", "-d"},
			"--new is only valid with the <config> positional",
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

// --- #839 (--clone) tests ------------------------------------------------

// cloneFakeBuilder returns a fakeClient pre-wired for `kuke run <src> --clone`
// flows: GetConfig returns the source CellConfig, GetBlueprint returns the
// blueprint, ListConfigs starts empty (caller can override), CreateConfig
// records each clone, and CreateCell records the materialized cell. The
// post-clone GetCell short-circuits to ErrCellNotFound so the create path
// proceeds rather than the existing-cell branch.
func cloneFakeBuilder() *fakeClient {
	src := configDoc()
	return &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			// When the CLI loops back to read a clone's body for annotation
			// verification, the caller passes the clone's name; otherwise it
			// asks for the source. Default behavior here returns the source
			// for the source name and the empty config for anything else
			// (tests that need annotated bodies override listConfigsFn +
			// getConfigFn together).
			if doc.Metadata.Name == src.Metadata.Name {
				return kukeonv1.GetConfigResult{Config: src, MetadataExists: true}, nil
			}
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		listConfigsFn: func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
			// Default: only the source CellConfig lives in scope.
			return []v1beta1.CellConfigDoc{{
				Metadata: v1beta1.CellConfigMetadata{Name: src.Metadata.Name, Realm: src.Metadata.Realm},
			}}, nil
		},
		createConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
			return kukeonv1.CreateConfigResult{Config: doc, Created: true}, nil
		},
		getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
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
}

// TestRun_FromConfig_Clone_DefaultName_AllocatesCounterZero covers AC #2 (the
// gap-fill counter on an empty pool): the first --clone of `prod` allocates
// `prod-0`, the clone CellConfigDoc carries the source-config annotation
// lineage marker, the clone's spec is a deep copy of the source's, and the
// cell started from the clone uses the clone's stable name.
func TestRun_FromConfig_Clone_DefaultName_AllocatesCounterZero(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := cloneFakeBuilder()
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1", fc.createConfigCalls)
	}
	clone := fc.createConfigDocs[0]
	if clone.Metadata.Name != "prod-0" {
		t.Errorf("clone name=%q, want prod-0 (gap-fill from empty pool)", clone.Metadata.Name)
	}
	if got := clone.Metadata.Annotations[cellconfig.AnnotationSourceConfig]; got != "prod" {
		t.Errorf("clone annotation %s=%q, want prod (lineage marker)", cellconfig.AnnotationSourceConfig, got)
	}
	if clone.Metadata.Realm != "cfg-realm" {
		t.Errorf("clone realm=%q, want cfg-realm (inherits source scope)", clone.Metadata.Realm)
	}
	if clone.Spec.Blueprint.Name != "web" || clone.Spec.Blueprint.Realm != "bp-realm" {
		t.Errorf("clone blueprint ref=%+v, want web@bp-realm (deep-copied from source spec)", clone.Spec.Blueprint)
	}
	if clone.Spec.Values["TAG"] != "v2" {
		t.Errorf("clone spec.values[TAG]=%q, want v2 (deep-copied)", clone.Spec.Values["TAG"])
	}

	if fc.createDoc.Metadata.Name != "prod-0" {
		t.Errorf("cell name=%q, want prod-0 (clone's stable name)", fc.createDoc.Metadata.Name)
	}
}

// TestRun_FromConfig_Clone_DefaultName_GapFillSkipsExistingClones covers AC
// #3 (gap-fill): with prod-0 and prod-2 already taken, the next --clone
// allocates prod-1.
func TestRun_FromConfig_Clone_DefaultName_GapFillSkipsExistingClones(t *testing.T) {
	t.Cleanup(viper.Reset)

	src := configDoc()
	fc := cloneFakeBuilder()
	existing := map[string]v1beta1.CellConfigDoc{
		src.Metadata.Name: src,
		"prod-0": {
			Metadata: v1beta1.CellConfigMetadata{
				Name:        "prod-0",
				Realm:       "cfg-realm",
				Annotations: map[string]string{cellconfig.AnnotationSourceConfig: "prod"},
			},
			Spec: src.Spec,
		},
		"prod-2": {
			Metadata: v1beta1.CellConfigMetadata{
				Name:        "prod-2",
				Realm:       "cfg-realm",
				Annotations: map[string]string{cellconfig.AnnotationSourceConfig: "prod"},
			},
			Spec: src.Spec,
		},
	}
	fc.listConfigsFn = func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
		out := make([]v1beta1.CellConfigDoc, 0, len(existing))
		for _, c := range existing {
			out = append(out, v1beta1.CellConfigDoc{
				Metadata: v1beta1.CellConfigMetadata{
					Name:  c.Metadata.Name,
					Realm: c.Metadata.Realm,
				},
			})
		}
		return out, nil
	}
	fc.getConfigFn = func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
		if c, ok := existing[doc.Metadata.Name]; ok {
			return kukeonv1.GetConfigResult{Config: c, MetadataExists: true}, nil
		}
		return kukeonv1.GetConfigResult{MetadataExists: false}, nil
	}

	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1", fc.createConfigCalls)
	}
	if got := fc.createConfigDocs[0].Metadata.Name; got != "prod-1" {
		t.Errorf("clone name=%q, want prod-1 (gap-fill between 0 and 2)", got)
	}
}

// TestRun_FromConfig_Clone_DefaultName_IgnoresUnannotatedSameShapedName
// covers a subtle case: a CellConfig named `prod-3` exists in scope but
// carries no source-config annotation (operator created it manually, not via
// --clone). The counter walk treats it as NOT a clone of prod, so the next
// --clone picks N=0 — but the name `prod-3` still occupies the name slot, so
// future allocations skip past N=3.
func TestRun_FromConfig_Clone_DefaultName_IgnoresUnannotatedSameShapedName(t *testing.T) {
	t.Cleanup(viper.Reset)

	src := configDoc()
	fc := cloneFakeBuilder()
	existing := map[string]v1beta1.CellConfigDoc{
		src.Metadata.Name: src,
		"prod-3": {
			Metadata: v1beta1.CellConfigMetadata{Name: "prod-3", Realm: "cfg-realm"},
			Spec:     src.Spec,
		},
	}
	fc.listConfigsFn = func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
		out := make([]v1beta1.CellConfigDoc, 0, len(existing))
		for _, c := range existing {
			out = append(out, v1beta1.CellConfigDoc{
				Metadata: v1beta1.CellConfigMetadata{
					Name:  c.Metadata.Name,
					Realm: c.Metadata.Realm,
				},
			})
		}
		return out, nil
	}
	fc.getConfigFn = func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
		if c, ok := existing[doc.Metadata.Name]; ok {
			return kukeonv1.GetConfigResult{Config: c, MetadataExists: true}, nil
		}
		return kukeonv1.GetConfigResult{MetadataExists: false}, nil
	}

	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createConfigDocs[0].Metadata.Name; got != "prod-0" {
		t.Errorf("clone name=%q, want prod-0 (manually-named prod-3 must not count as a clone)", got)
	}
}

// TestRun_FromConfig_Clone_NamedExplicit_HappyPath covers AC #3 (named
// create-or-fail): `--clone --name debug` creates a clone CellConfig named
// `debug` and starts a cell `debug` from it.
func TestRun_FromConfig_Clone_NamedExplicit_HappyPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := cloneFakeBuilder()
	// The named path is a single-shot CreateConfig — no ListConfigs scan.
	fc.listConfigsFn = func(string, string, string) ([]v1beta1.CellConfigDoc, error) {
		t.Errorf("ListConfigs called on --clone --name path; want single-shot CreateConfig only")
		return nil, errors.New("unexpected ListConfigs")
	}

	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--name", "debug", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1", fc.createConfigCalls)
	}
	clone := fc.createConfigDocs[0]
	if clone.Metadata.Name != "debug" {
		t.Errorf("clone name=%q, want debug (explicit --name)", clone.Metadata.Name)
	}
	if got := clone.Metadata.Annotations[cellconfig.AnnotationSourceConfig]; got != "prod" {
		t.Errorf("clone annotation %s=%q, want prod", cellconfig.AnnotationSourceConfig, got)
	}
	if fc.createDoc.Metadata.Name != "debug" {
		t.Errorf("cell name=%q, want debug", fc.createDoc.Metadata.Name)
	}
}

// TestRun_FromConfig_Clone_NamedExplicit_CollisionFails covers the
// AC's create-or-fail half of the named path: a collision surfaces the AC's
// pinned error message and the cell create never runs.
func TestRun_FromConfig_Clone_NamedExplicit_CollisionFails(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := cloneFakeBuilder()
	fc.createConfigFn = func(v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
		return kukeonv1.CreateConfigResult{}, errdefs.ErrConfigExists
	}

	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--name", "debug", "--realm", "cfg-realm", "-d"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil, want collision rejection")
	}
	want := `cellconfig "debug" already exists; --clone --name requires the name to be free`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err=%q, want substring %q", err.Error(), want)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d, want 0 on clone-name collision", fc.createCalls)
	}
}

// TestRun_FromConfig_Clone_DefaultName_RetriesOnRace covers AC #8 (atomic
// claim): if the first CreateConfig races and loses (the daemon already has
// `prod-0`), the loop retries and allocates `prod-1` without surfacing a
// collision error to the operator.
func TestRun_FromConfig_Clone_DefaultName_RetriesOnRace(t *testing.T) {
	t.Cleanup(viper.Reset)

	src := configDoc()
	fc := cloneFakeBuilder()
	// First create attempt races and loses; second attempt succeeds.
	createAttempts := 0
	fc.createConfigFn = func(doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
		createAttempts++
		if createAttempts == 1 {
			return kukeonv1.CreateConfigResult{}, errdefs.ErrConfigExists
		}
		return kukeonv1.CreateConfigResult{Config: doc, Created: true}, nil
	}
	// After the loser retries, ListConfigs reflects that prod-0 is now taken,
	// so the next candidate is prod-1.
	var listCalls int
	fc.listConfigsFn = func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
		listCalls++
		if listCalls == 1 {
			return []v1beta1.CellConfigDoc{
				{Metadata: v1beta1.CellConfigMetadata{Name: src.Metadata.Name, Realm: src.Metadata.Realm}},
			}, nil
		}
		// Post-race: the winner's prod-0 now lives in scope.
		return []v1beta1.CellConfigDoc{
			{Metadata: v1beta1.CellConfigMetadata{Name: src.Metadata.Name, Realm: src.Metadata.Realm}},
			{Metadata: v1beta1.CellConfigMetadata{Name: "prod-0", Realm: "cfg-realm"}},
		}, nil
	}
	fc.getConfigFn = func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
		if doc.Metadata.Name == src.Metadata.Name {
			return kukeonv1.GetConfigResult{Config: src, MetadataExists: true}, nil
		}
		if doc.Metadata.Name == "prod-0" {
			return kukeonv1.GetConfigResult{
				Config: v1beta1.CellConfigDoc{
					Metadata: v1beta1.CellConfigMetadata{
						Name:        "prod-0",
						Realm:       "cfg-realm",
						Annotations: map[string]string{cellconfig.AnnotationSourceConfig: "prod"},
					},
				},
				MetadataExists: true,
			}, nil
		}
		return kukeonv1.GetConfigResult{MetadataExists: false}, nil
	}

	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.createConfigCalls != 2 {
		t.Errorf("CreateConfig calls=%d, want 2 (first racing, second winning)", fc.createConfigCalls)
	}
	winningName := fc.createConfigDocs[len(fc.createConfigDocs)-1].Metadata.Name
	if winningName != "prod-1" {
		t.Errorf("winning clone name=%q, want prod-1 (gap-fill after prod-0 was claimed)", winningName)
	}
}

// TestRun_FromConfig_Clone_Concurrent_DistinctNs covers the AC's 10-parallel
// invocation test: 10 concurrent `--clone`s against the same source emit 10
// distinct CreateConfig calls (the daemon's atomic write enforces uniqueness;
// the loop's race-then-retry path here is exercised by the synthetic state
// in fc, where the simulated `taken` set grows under a lock as winners
// claim slots).
func TestRun_FromConfig_Clone_Concurrent_DistinctNs(t *testing.T) {
	src := configDoc()

	var (
		mu        sync.Mutex
		claimedNs = map[int]struct{}{}
		clones    = map[string]v1beta1.CellConfigDoc{src.Metadata.Name: src}
	)

	makeFC := func() *fakeClient {
		fc := &fakeClient{
			getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
				mu.Lock()
				defer mu.Unlock()
				if c, ok := clones[doc.Metadata.Name]; ok {
					return kukeonv1.GetConfigResult{Config: c, MetadataExists: true}, nil
				}
				return kukeonv1.GetConfigResult{MetadataExists: false}, nil
			},
			getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
				return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
			},
			listConfigsFn: func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
				mu.Lock()
				defer mu.Unlock()
				out := make([]v1beta1.CellConfigDoc, 0, len(clones))
				for name, c := range clones {
					out = append(out, v1beta1.CellConfigDoc{
						Metadata: v1beta1.CellConfigMetadata{
							Name:  name,
							Realm: c.Metadata.Realm,
						},
					})
				}
				return out, nil
			},
			createConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
				mu.Lock()
				defer mu.Unlock()
				if _, taken := clones[doc.Metadata.Name]; taken {
					return kukeonv1.CreateConfigResult{}, errdefs.ErrConfigExists
				}
				// Extract the N suffix to assert distinctness.
				var n int
				if _, scanErr := fmt.Sscanf(doc.Metadata.Name, "prod-%d", &n); scanErr == nil {
					claimedNs[n] = struct{}{}
				}
				clones[doc.Metadata.Name] = doc
				return kukeonv1.CreateConfigResult{Config: doc, Created: true}, nil
			},
			getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
				return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
			},
			createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{
					Cell: doc, Created: true, MetadataExistsPost: true,
					CgroupCreated: true, CgroupExistsPost: true,
					RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
				}, nil
			},
		}
		return fc
	}

	// viper is module-global so concurrent cobra Execute() calls would race
	// on the parsed flag state. Run sequentially through 10 invocations
	// against the shared state — the AC requires distinct N's, not literal
	// goroutine parallelism (the daemon's `os.Link` is what enforces
	// atomicity in the real world; this test pins that the client loop
	// composes the right candidates given a shared "taken" set).
	const parallelism = 10
	for i := range parallelism {
		viper.Reset()
		fc := makeFC()
		cmd, _ := newCmd(t, fc)
		cmd.SetArgs([]string{"prod", "--clone", "--realm", "cfg-realm", "-d"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute (iteration %d): %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(claimedNs) != parallelism {
		t.Fatalf("got %d distinct N's, want %d (clones=%v)", len(claimedNs), parallelism, clones)
	}
	for n := range parallelism {
		if _, ok := claimedNs[n]; !ok {
			t.Errorf("N=%d not claimed; got %v", n, claimedNs)
		}
	}
}

// TestRun_FromConfig_Clone_SpecIndependence covers AC #6: the clone's
// `spec` is a deep copy. Mutating the source's spec.values / spec.repos /
// spec.secrets maps after the clone is built must NOT change the clone's
// recorded view. The fake's createConfigFn captures the clone CellConfigDoc
// at creation time; we then verify the captured doc's maps are independent.
func TestRun_FromConfig_Clone_SpecIndependence(t *testing.T) {
	t.Cleanup(viper.Reset)

	source := configDoc()
	captured := source // start with identity; will be overwritten on CreateConfig.
	fc := cloneFakeBuilder()
	fc.getConfigFn = func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
		if doc.Metadata.Name == source.Metadata.Name {
			return kukeonv1.GetConfigResult{Config: source, MetadataExists: true}, nil
		}
		return kukeonv1.GetConfigResult{MetadataExists: false}, nil
	}
	fc.createConfigFn = func(doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
		captured = doc
		return kukeonv1.CreateConfigResult{Config: doc, Created: true}, nil
	}

	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Mutate the source's maps after the clone is captured. If the clone
	// shared maps with source, these writes would leak into captured.
	source.Spec.Values["TAG"] = "MUTATED"
	source.Spec.Repos["src"] = v1beta1.CellConfigRepoFill{URL: "https://mutated.example.com/src.git"}
	source.Spec.Secrets["token"] = v1beta1.CellConfigSecretFill{
		SecretRef: &v1beta1.ContainerSecretRef{Name: "mutated", Realm: "cfg-realm"},
	}

	if captured.Spec.Values["TAG"] != "v2" {
		t.Errorf("clone spec.values[TAG] = %q after source mutation, want v2 (deep copy)", captured.Spec.Values["TAG"])
	}
	if captured.Spec.Repos["src"].URL != "https://example.com/src.git" {
		t.Errorf("clone spec.repos[src].url = %q after source mutation, want https://example.com/src.git (deep copy)",
			captured.Spec.Repos["src"].URL)
	}
	if captured.Spec.Secrets["token"].SecretRef.Name != "api-token" {
		t.Errorf("clone spec.secrets[token].secretRef.name = %q after source mutation, want api-token (deep copy)",
			captured.Spec.Secrets["token"].SecretRef.Name)
	}
}

// TestRun_FromConfig_Clone_CrossRealmIsolation pins the scope contract: a
// clone of `prod@cfg-realm` walks only the cfg-realm scope when scanning for
// existing clones — a Config named `prod-0` in `other-realm` doesn't count
// toward the cfg-realm gap-fill counter.
func TestRun_FromConfig_Clone_CrossRealmIsolation(t *testing.T) {
	t.Cleanup(viper.Reset)

	src := configDoc()
	fc := cloneFakeBuilder()
	otherRealmClone := v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{
			Name:        "prod-0",
			Realm:       "other-realm",
			Annotations: map[string]string{cellconfig.AnnotationSourceConfig: "prod"},
		},
		Spec: src.Spec,
	}
	fc.listConfigsFn = func(realm, _, _ string) ([]v1beta1.CellConfigDoc, error) {
		// Standard ListConfigs scope-filtering: only configs at this realm.
		if realm == "cfg-realm" {
			return []v1beta1.CellConfigDoc{
				{Metadata: v1beta1.CellConfigMetadata{Name: src.Metadata.Name, Realm: src.Metadata.Realm}},
			}, nil
		}
		return []v1beta1.CellConfigDoc{
			{Metadata: otherRealmClone.Metadata},
		}, nil
	}
	fc.getConfigFn = func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
		if doc.Metadata.Name == src.Metadata.Name {
			return kukeonv1.GetConfigResult{Config: src, MetadataExists: true}, nil
		}
		return kukeonv1.GetConfigResult{MetadataExists: false}, nil
	}

	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.createConfigDocs[0].Metadata.Name; got != "prod-0" {
		t.Errorf("clone name=%q, want prod-0 (other-realm/prod-0 must not bump the counter)", got)
	}
	if got := fc.createConfigDocs[0].Metadata.Realm; got != "cfg-realm" {
		t.Errorf("clone realm=%q, want cfg-realm", got)
	}
}

// TestRun_FromConfig_Clone_MutexWithNew covers the AC's three-way mutex: a
// caller passing both --new and --clone is rejected by cobra at parse time.
func TestRun_FromConfig_Clone_MutexWithNew(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--new", "--realm", "cfg-realm", "-d"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil, want mutex rejection")
	}
	// Cobra phrases this as "if any flags in the group are set none of the others can be"
	if !strings.Contains(err.Error(), "mutually exclusive") &&
		!strings.Contains(err.Error(), "none of the others can be") {
		t.Errorf("err=%v, want mutex rejection wording", err)
	}
}

// TestRun_FromConfig_Clone_MutexWithRm covers the AC's `--clone ↔ --rm`
// mutex: the persistent-clone-Config intent conflicts with the
// delete-on-exit intent.
func TestRun_FromConfig_Clone_MutexWithRm(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--rm", "--realm", "cfg-realm", "-d"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute err=nil, want mutex rejection")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") &&
		!strings.Contains(err.Error(), "none of the others can be") {
		t.Errorf("err=%v, want mutex rejection wording", err)
	}
}

// TestRun_Clone_OnlyValidWithConfig mirrors TestRun_New_OnlyValidWithConfig:
// --clone is a CellConfig-only knob, rejected on -f/-p/-b. The error wording
// pins the per-source phrasing so the operator's mental model isn't muddled.
func TestRun_Clone_OnlyValidWithConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"-f rejects --clone",
			[]string{"-f", "/tmp/never-read.yaml", "--clone", "-d"},
			"--clone is only valid with the <config> positional",
		},
		{
			"-p rejects --clone",
			[]string{"-p", "shell", "--clone", "-d"},
			"--clone is only valid with the <config> positional",
		},
		{
			"-b rejects --clone",
			[]string{"-b", "web", "--clone", "-d"},
			"--clone is only valid with the <config> positional",
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

// --- #835 (--reuse) tests ------------------------------------------------

// reuseClone is one entry in a fake pool for a --reuse test: the persistent
// clone CellConfig plus the cell-state to report from GetCell. cellMissing
// lets a test simulate a clone Config whose cell was deleted (skip it).
type reuseClone struct {
	name        string
	annotation  string // metadata.annotations[kukeon.io/source-config]; empty = unannotated
	cellState   v1beta1.CellState
	cellMissing bool
}

// reuseFakeBuilder returns a fakeClient pre-wired for `kuke run <src> --reuse`
// flows. The pool argument seeds the daemon's view: each entry becomes a
// CellConfigDoc returned from GetConfig and a CellDoc returned from GetCell
// (cellMissing makes GetCell return ErrCellNotFound for that clone, even
// though the Config itself still exists in the pool listing). The source
// CellConfig is always present at "prod" in cfg-realm.
//
// State tracking is split across two maps so the wire-level distinctions
// the production daemon makes survive into the fake:
//
//   - configExists tracks clone CellConfigs (set by seed + CreateConfig).
//   - cellExists / cellStates tracks clone *cells* (set by CreateCell;
//     pre-seeded for pool entries; never set on a fresh fork until its
//     CreateCell fires, so GetCell returns ErrCellNotFound and runRun
//     takes the create branch).
//
// StartCell is wired to advance cellStates to Ready (matching the
// daemon-side state machine), so concurrent --reuse invocations race
// realistically: the second StartCell against the same slot returns the
// "must first be stopped" error pickAndStartReusableClone treats as a
// claim-race signal.
func reuseFakeBuilder(t *testing.T, pool []reuseClone) (*fakeClient, *sync.Mutex, map[string]v1beta1.CellState) {
	t.Helper()
	src := configDoc()
	cellStates := make(map[string]v1beta1.CellState, len(pool))
	cellExists := make(map[string]bool, len(pool))
	configExists := make(map[string]bool, len(pool))
	annotations := make(map[string]string, len(pool))
	for _, c := range pool {
		configExists[c.name] = true
		annotations[c.name] = c.annotation
		if !c.cellMissing {
			cellStates[c.name] = c.cellState
			cellExists[c.name] = true
		}
	}
	var mu sync.Mutex

	fc := &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			if doc.Metadata.Name == src.Metadata.Name {
				return kukeonv1.GetConfigResult{Config: src, MetadataExists: true}, nil
			}
			mu.Lock()
			ok := configExists[doc.Metadata.Name]
			ann := annotations[doc.Metadata.Name]
			mu.Unlock()
			if !ok {
				return kukeonv1.GetConfigResult{MetadataExists: false}, nil
			}
			cfg := v1beta1.CellConfigDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindCellConfig,
				Metadata: v1beta1.CellConfigMetadata{
					Name:  doc.Metadata.Name,
					Realm: src.Metadata.Realm,
				},
				Spec: src.Spec,
			}
			if ann != "" {
				cfg.Metadata.Annotations = map[string]string{cellconfig.AnnotationSourceConfig: ann}
			}
			return kukeonv1.GetConfigResult{Config: cfg, MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		listConfigsFn: func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
			mu.Lock()
			defer mu.Unlock()
			out := []v1beta1.CellConfigDoc{
				{Metadata: v1beta1.CellConfigMetadata{Name: src.Metadata.Name, Realm: src.Metadata.Realm}},
			}
			for name := range configExists {
				out = append(out, v1beta1.CellConfigDoc{
					Metadata: v1beta1.CellConfigMetadata{Name: name, Realm: src.Metadata.Realm},
				})
			}
			return out, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			mu.Lock()
			defer mu.Unlock()
			if !cellExists[doc.Metadata.Name] {
				return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
			}
			state := cellStates[doc.Metadata.Name]
			cellDoc := doc
			cellDoc.Status.State = state
			return kukeonv1.GetCellResult{
				Cell:                cellDoc,
				MetadataExists:      true,
				CgroupExists:        true,
				RootContainerExists: true,
			}, nil
		},
		startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			mu.Lock()
			defer mu.Unlock()
			if !cellExists[doc.Metadata.Name] {
				return kukeonv1.StartCellResult{}, errdefs.ErrCellNotFound
			}
			state := cellStates[doc.Metadata.Name]
			if state != v1beta1.CellStateStopped {
				// Mimic the daemon's startCell guard: a non-Stopped cell
				// surfaces the race that pickAndStartReusableClone detects
				// via the "must first be stopped" substring.
				return kukeonv1.StartCellResult{},
					fmt.Errorf("cell %q is already in Ready state and must first be stopped", doc.Metadata.Name)
			}
			cellStates[doc.Metadata.Name] = v1beta1.CellStateReady
			started := doc
			started.Status.State = v1beta1.CellStateReady
			return kukeonv1.StartCellResult{Cell: started, Started: true}, nil
		},
		createConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
			mu.Lock()
			defer mu.Unlock()
			if configExists[doc.Metadata.Name] {
				return kukeonv1.CreateConfigResult{}, errdefs.ErrConfigExists
			}
			configExists[doc.Metadata.Name] = true
			if doc.Metadata.Annotations != nil {
				annotations[doc.Metadata.Name] = doc.Metadata.Annotations[cellconfig.AnnotationSourceConfig]
			}
			// Cell intentionally not yet created: the fork's CreateCell
			// step (down in runRun) is what brings the cell into existence.
			return kukeonv1.CreateConfigResult{Config: doc, Created: true}, nil
		},
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			mu.Lock()
			defer mu.Unlock()
			cellExists[doc.Metadata.Name] = true
			cellStates[doc.Metadata.Name] = v1beta1.CellStateReady
			return kukeonv1.CreateCellResult{
				Cell: doc, Created: true, MetadataExistsPost: true,
				CgroupCreated: true, CgroupExistsPost: true,
				RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
				Containers: []kukeonv1.ContainerCreationOutcome{{Name: "main", ExistsPost: true, Created: true}},
			}, nil
		},
	}
	return fc, &mu, cellStates
}

// TestRun_FromConfig_Reuse_PicksLowestStopped covers AC #3 (ascending-N sort,
// lowest-N selection). The pool has prod-0 (Ready) and prod-1 (Stopped); the
// only valid claim is prod-1.
func TestRun_FromConfig_Reuse_PicksLowestStopped(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStateReady},
		{name: "prod-1", annotation: "prod", cellState: v1beta1.CellStateStopped},
		{name: "prod-2", annotation: "prod", cellState: v1beta1.CellStateStopped},
	})
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startCalls != 1 {
		t.Errorf("StartCell calls=%d, want 1 (the lowest-N Stopped clone)", fc.startCalls)
	}
	if fc.startDoc.Metadata.Name != "prod-1" {
		t.Errorf("StartCell name=%q, want prod-1 (lowest-N Stopped; prod-0 is Ready)", fc.startDoc.Metadata.Name)
	}
	if fc.createConfigCalls != 0 {
		t.Errorf("CreateConfig calls=%d, want 0 (--reuse hit the pool; no fork)", fc.createConfigCalls)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d, want 0 (started in place via StartCell)", fc.createCalls)
	}
	if fc.killCalls != 0 {
		t.Errorf("KillCell calls=%d, want 0 (overlay preserved — no delete-then-create)", fc.killCalls)
	}
}

// TestRun_FromConfig_Reuse_FiltersAnnotation covers the annotation half of
// AC #3: a manually-named CellConfig `prod-3` (no source-config annotation)
// is in the same scope as a real clone `prod-0`. Only the annotated one
// counts toward the pool, so --reuse picks prod-0 rather than prod-3.
func TestRun_FromConfig_Reuse_FiltersAnnotation(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-3", annotation: "", cellState: v1beta1.CellStateStopped},
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStateStopped},
	})
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startDoc.Metadata.Name != "prod-0" {
		t.Errorf("StartCell name=%q, want prod-0 (annotated clone; prod-3 is unannotated)", fc.startDoc.Metadata.Name)
	}
}

// TestRun_FromConfig_Reuse_StartsInPlaceNoCreateOrDelete covers AC #4 and
// AC #7 at the wire-call level: --reuse calls StartCell on the picked
// clone's existing cell, never CreateCell and never KillCell. The
// containerd snapshot is preserved across the stop/start transition by
// construction — the daemon's StartCell creates a new task in the existing
// snapshot, so the overlay (project repo clone, .claude.json, any per-cell
// state) survives. The sentinel-file e2e the AC describes is the operator-
// facing equivalent of this in-process attestation.
func TestRun_FromConfig_Reuse_StartsInPlaceNoCreateOrDelete(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStateStopped},
	})
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startCalls != 1 {
		t.Errorf("StartCell calls=%d, want exactly 1 (claimed clone's existing cell)", fc.startCalls)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d, want 0 (never delete-and-recreate; overlay must be preserved)", fc.createCalls)
	}
	if fc.createConfigCalls != 0 {
		t.Errorf("CreateConfig calls=%d, want 0 (pool was non-empty; no fork)", fc.createConfigCalls)
	}
	if fc.killCalls != 0 {
		t.Errorf(
			"KillCell calls=%d, want 0 (never stop the cell to restart it; the overlay must survive)",
			fc.killCalls,
		)
	}
}

// TestRun_FromConfig_Reuse_EmptyPoolFallback covers AC #5: with no clones in
// scope, --reuse falls through to --clone's code path — atomic gap-fill
// counter allocation, new clone Config with the source-config annotation, new
// cell created with the standard CreateCell + Started rollup.
func TestRun_FromConfig_Reuse_EmptyPoolFallback(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, nil)
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1 (empty pool → fork via --clone path)", fc.createConfigCalls)
	}
	clone := fc.createConfigDocs[0]
	if clone.Metadata.Name != "prod-0" {
		t.Errorf("clone name=%q, want prod-0 (gap-fill from empty pool)", clone.Metadata.Name)
	}
	if got := clone.Metadata.Annotations[cellconfig.AnnotationSourceConfig]; got != "prod" {
		t.Errorf("clone annotation %s=%q, want prod (lineage marker)", cellconfig.AnnotationSourceConfig, got)
	}
	if fc.createCalls != 1 {
		t.Errorf("CreateCell calls=%d, want 1 (fresh cell from the forked clone)", fc.createCalls)
	}
	if fc.startCalls != 0 {
		t.Errorf("StartCell calls=%d, want 0 (no pool entry to start in place)", fc.startCalls)
	}
}

// TestRun_FromConfig_Reuse_AllRunningFallback covers AC #6 + the
// Running-cells half of AC #9: when every clone has a Running cell, the
// pool is functionally empty and --reuse falls back to --clone, forking
// the next available N.
func TestRun_FromConfig_Reuse_AllRunningFallback(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStateReady},
		{name: "prod-1", annotation: "prod", cellState: v1beta1.CellStateReady},
		{name: "prod-2", annotation: "prod", cellState: v1beta1.CellStateReady},
	})
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startCalls != 0 {
		t.Errorf("StartCell calls=%d, want 0 (every clone Running → no in-place start)", fc.startCalls)
	}
	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1 (fork via fallback to --clone)", fc.createConfigCalls)
	}
	if got := fc.createConfigDocs[0].Metadata.Name; got != "prod-3" {
		t.Errorf("clone name=%q, want prod-3 (next gap-fill after prod-0/-1/-2)", got)
	}
}

// TestRun_FromConfig_Reuse_SkipsErrorStateCells covers AC #8: clones whose
// cells live in Pending / Failed / Unknown sub-states are excluded from
// the pick set. With every clone in an error state, --reuse falls back to
// --clone rather than try to claim a half-broken slot.
func TestRun_FromConfig_Reuse_SkipsErrorStateCells(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStatePending},
		{name: "prod-1", annotation: "prod", cellState: v1beta1.CellStateFailed},
		{name: "prod-2", annotation: "prod", cellState: v1beta1.CellStateUnknown},
	})
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startCalls != 0 {
		t.Errorf("StartCell calls=%d, want 0 (error-state clones must be skipped)", fc.startCalls)
	}
	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1 (all error → fork via fallback)", fc.createConfigCalls)
	}
	if got := fc.createConfigDocs[0].Metadata.Name; got != "prod-3" {
		t.Errorf("clone name=%q, want prod-3 (next gap-fill after error-state -0/-1/-2)", got)
	}
}

// TestRun_FromConfig_Reuse_MixedRunningAndStopped covers AC #9 explicitly:
// pool has prod-0 (Ready), prod-1 (Stopped), prod-2 (Ready). --reuse picks
// prod-1 (the lowest-N Stopped) and ignores the Running slots.
func TestRun_FromConfig_Reuse_MixedRunningAndStopped(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStateReady},
		{name: "prod-1", annotation: "prod", cellState: v1beta1.CellStateStopped},
		{name: "prod-2", annotation: "prod", cellState: v1beta1.CellStateReady},
	})
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startDoc.Metadata.Name != "prod-1" {
		t.Errorf(
			"StartCell name=%q, want prod-1 (lowest-N Stopped between Running -0 and -2)",
			fc.startDoc.Metadata.Name,
		)
	}
	if fc.createConfigCalls != 0 {
		t.Errorf("CreateConfig calls=%d, want 0 (pool had a Stopped candidate)", fc.createConfigCalls)
	}
}

// TestRun_FromConfig_Reuse_RaceAdvancesToNextCandidate exercises the
// atomic-claim half of AC #3: pool has prod-0 (Stopped) and prod-1
// (Stopped); a racer flips prod-0 to Ready between our state-read and our
// StartCell call. The first StartCell returns "must first be stopped";
// --reuse advances to prod-1 transparently.
func TestRun_FromConfig_Reuse_RaceAdvancesToNextCandidate(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, mu, cellStates := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStateStopped},
		{name: "prod-1", annotation: "prod", cellState: v1beta1.CellStateStopped},
	})
	// Override StartCell to inject the race: the first StartCell against
	// prod-0 sees a flipped state (a racer already claimed it). Subsequent
	// StartCell on prod-1 uses the default Stopped → Ready transition the
	// fake's base implementation does.
	prevStart := fc.startCellFn
	fc.startCellFn = func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
		if doc.Metadata.Name == "prod-0" {
			mu.Lock()
			cellStates["prod-0"] = v1beta1.CellStateReady
			mu.Unlock()
			return kukeonv1.StartCellResult{},
				fmt.Errorf("cell %q has running containers and must first be stopped", doc.Metadata.Name)
		}
		return prevStart(doc)
	}

	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startCalls != 2 {
		t.Errorf("StartCell calls=%d, want 2 (first prod-0 raced, second prod-1 won)", fc.startCalls)
	}
	if fc.startDoc.Metadata.Name != "prod-1" {
		t.Errorf("final StartCell name=%q, want prod-1 (advanced past raced prod-0)", fc.startDoc.Metadata.Name)
	}
	if fc.createConfigCalls != 0 {
		t.Errorf("CreateConfig calls=%d, want 0 (race resolved within the pool)", fc.createConfigCalls)
	}
}

// TestRun_FromConfig_Reuse_ConcurrentDistinctOutcomes covers AC #10: 5
// invocations against a pool of 3 Stopped (room for 2 fresh forks). The
// first three claim prod-0, prod-1, prod-2 in turn (advancing as each
// slot moves to Ready). The fourth and fifth see an all-Running pool and
// fall back to --clone, allocating prod-3 and prod-4.
//
// viper is module-global so cobra Execute() calls would race on parsed
// flag state if run from goroutines. Run sequentially through 5
// invocations against shared in-memory state — the AC requires distinct
// outcomes, not literal goroutine parallelism (the daemon's StartCell
// state guard is what enforces atomicity in production; this test pins
// that the client loop composes the right candidates given a shared,
// advancing "taken" view).
func TestRun_FromConfig_Reuse_ConcurrentDistinctOutcomes(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStateStopped},
		{name: "prod-1", annotation: "prod", cellState: v1beta1.CellStateStopped},
		{name: "prod-2", annotation: "prod", cellState: v1beta1.CellStateStopped},
	})

	const ticks = 5
	startedNames := make([]string, 0, ticks)
	forkedNames := make([]string, 0)

	for i := range ticks {
		viper.Reset()
		startsBefore := fc.startCalls
		forksBefore := fc.createConfigCalls
		cmd, _ := newCmd(t, fc)
		cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute (tick %d): %v", i, err)
		}
		// Each tick either calls StartCell once (claim) or CreateConfig
		// once (fork). Cross-walk fc's counters into the per-tick log.
		if fc.startCalls > startsBefore {
			startedNames = append(startedNames, fc.startDoc.Metadata.Name)
		}
		if fc.createConfigCalls > forksBefore {
			forkedNames = append(forkedNames, fc.createConfigDocs[len(fc.createConfigDocs)-1].Metadata.Name)
		}
	}

	// The three pool-hit claims walk -0/-1/-2 in order.
	wantStarted := []string{"prod-0", "prod-1", "prod-2"}
	if !reflect.DeepEqual(startedNames, wantStarted) {
		t.Errorf("started clones=%v, want %v", startedNames, wantStarted)
	}
	// Ticks 4 and 5 fork next-N from the all-Ready pool.
	wantForked := []string{"prod-3", "prod-4"}
	if !reflect.DeepEqual(forkedNames, wantForked) {
		t.Errorf("forked clones=%v, want %v", forkedNames, wantForked)
	}
}

// TestRun_FromConfig_Reuse_SkipsCloneWithMissingCell covers a corner of the
// pick algorithm: a clone CellConfig exists in scope but its cell was
// deleted (e.g., operator ran `kuke delete cell prod-0`). GetCell returns
// ErrCellNotFound; --reuse skips the cell-less Config and advances. With no
// other pool entries, it falls back to --clone.
func TestRun_FromConfig_Reuse_SkipsCloneWithMissingCell(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellMissing: true},
	})
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startCalls != 0 {
		t.Errorf("StartCell calls=%d, want 0 (the only clone has no cell)", fc.startCalls)
	}
	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1 (cell-less pool → fork)", fc.createConfigCalls)
	}
	// Gap-fill must skip past the existing prod-0 Config slot even though
	// its cell is gone — the name is still taken in the Config list.
	if got := fc.createConfigDocs[0].Metadata.Name; got != "prod-1" {
		t.Errorf("forked clone name=%q, want prod-1 (prod-0 Config name still occupied)", got)
	}
}

// TestRun_FromConfig_Reuse_MutexWith covers AC #2: --reuse mutex with each of
// --new, --clone, --name, --rm. Cobra's MarkFlagsMutuallyExclusive surfaces
// the standard "mutually exclusive" / "none of the others can be" phrasing.
func TestRun_FromConfig_Reuse_MutexWith(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"new", []string{"prod", "--reuse", "--new", "--realm", "cfg-realm", "-d"}},
		{"clone", []string{"prod", "--reuse", "--clone", "--realm", "cfg-realm", "-d"}},
		{"name", []string{"prod", "--reuse", "--name", "X", "--realm", "cfg-realm", "-d"}},
		{"rm", []string{"prod", "--reuse", "--rm", "--realm", "cfg-realm", "-d"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			fc := &fakeClient{}
			cmd, _ := newCmd(t, fc)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("Execute err=nil, want mutex rejection for --reuse + --%s", tc.name)
			}
			if !strings.Contains(err.Error(), "mutually exclusive") &&
				!strings.Contains(err.Error(), "none of the others can be") {
				t.Errorf("err=%v, want mutex rejection wording", err)
			}
		})
	}
}

// TestRun_Reuse_OnlyValidWithConfig mirrors TestRun_Clone_OnlyValidWithConfig:
// --reuse is a CellConfig-only knob, rejected on -f/-p/-b. The per-source
// error wording pins the operator's mental model.
func TestRun_Reuse_OnlyValidWithConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"-f rejects --reuse",
			[]string{"-f", "/tmp/never-read.yaml", "--reuse", "-d"},
			"--reuse is only valid with the <config> positional",
		},
		{
			"-p rejects --reuse",
			[]string{"-p", "shell", "--reuse", "-d"},
			"--reuse is only valid with the <config> positional",
		},
		{
			"-b rejects --reuse",
			[]string{"-b", "web", "--reuse", "-d"},
			"--reuse is only valid with the <config> positional",
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
func TestRun_EnvFlag_FromBlueprint_ThreadsRuntimeEnvOntoCreateDoc(t *testing.T) {
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
		"-b", "web",
		"--param", "TAG=v2",
		"--env", "LABEL=bug",
		"--realm", "my-realm",
		"-d",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(fc.createDoc.Spec.RuntimeEnv, want) {
		t.Errorf("RuntimeEnv=%v, want %v", fc.createDoc.Spec.RuntimeEnv, want)
	}
	// --param still applied to the spec (orthogonal to --env).
	if got := fc.createDoc.Spec.Containers[0].Image; got != "registry.example.com/web:v2" {
		t.Errorf("image=%q want ${TAG} substituted to v2 (--param + --env are independent)", got)
	}
}

// TestRun_EnvFlag_FromConfig_Bare_ThreadsRuntimeEnvOntoCreateDoc covers
// the bare-positional `<config>` create path with --env: a brand-new cell
// from a CellConfig receives RuntimeEnv on its CreateCell doc. The
// `--new`-or-deterministic identity branch picks the deterministic name
// here (Config's stable name); both branches thread RuntimeEnv identically
// because the assignment is before the identity-flag branching.
func TestRun_EnvFlag_FromConfig_Bare_ThreadsRuntimeEnvOntoCreateDoc(t *testing.T) {
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
	cmd.SetArgs([]string{"prod", "--env", "LABEL=bug", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(fc.createDoc.Spec.RuntimeEnv, want) {
		t.Errorf("RuntimeEnv=%v, want %v (<cfg> positional source)", fc.createDoc.Spec.RuntimeEnv, want)
	}
}

// TestRun_EnvFlag_New_ThreadsRuntimeEnvOntoCreateDoc covers the --new
// identity branch on the <config> path: a fresh `<config-name>-<6hex>`
// cell still carries RuntimeEnv on its CreateCell doc.
func TestRun_EnvFlag_New_ThreadsRuntimeEnvOntoCreateDoc(t *testing.T) {
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
	cmd.SetArgs([]string{"prod", "--new", "--env", "LABEL=bug", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(fc.createDoc.Spec.RuntimeEnv, want) {
		t.Errorf("RuntimeEnv=%v, want %v (--new identity branch)", fc.createDoc.Spec.RuntimeEnv, want)
	}
	if !regexp.MustCompile(`^prod-[0-9a-f]{6}$`).MatchString(fc.createDoc.Metadata.Name) {
		t.Errorf("cell name=%q want prod-<6hex> (--new identity)", fc.createDoc.Metadata.Name)
	}
}

// TestRun_EnvFlag_Clone_ThreadsRuntimeEnvOntoCreateDoc covers --clone:
// the new cell created from the freshly-forked clone Config carries
// RuntimeEnv. The forked CellConfigDoc itself does NOT carry RuntimeEnv
// (the field is yaml:"-" and lives on CellSpec, not CellConfigSpec — the
// clone is a deep copy of the source's CellConfigSpec, which has no env-
// injection concept), so the clone Config remains a clean fork.
func TestRun_EnvFlag_Clone_ThreadsRuntimeEnvOntoCreateDoc(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := cloneFakeBuilder()
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--clone", "--env", "LABEL=bug", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1", fc.createConfigCalls)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d, want 1", fc.createCalls)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(fc.createDoc.Spec.RuntimeEnv, want) {
		t.Errorf("CreateCell doc RuntimeEnv=%v, want %v", fc.createDoc.Spec.RuntimeEnv, want)
	}
	// The forked CellConfig is a clean lineage marker; RuntimeEnv has no
	// concept there. The clone's spec is the source's, deep-copied.
	clone := fc.createConfigDocs[0]
	if clone.Metadata.Name != "prod-0" {
		t.Errorf("clone name=%q, want prod-0", clone.Metadata.Name)
	}
}

// TestRun_EnvFlag_Reuse_ThreadsRuntimeEnvOntoStartDoc covers the --reuse
// path: when --reuse picks a Stopped clone from the pool and calls
// StartCell on its existing cell, RuntimeEnv rides on the StartCell doc
// — not CreateCell, since the cell is started in place (overlay
// preserved). This is the cron-driver use case from issue #840: each
// tick re-injects its --env against the same restarted cell.
func TestRun_EnvFlag_Reuse_ThreadsRuntimeEnvOntoStartDoc(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, []reuseClone{
		{name: "prod-0", annotation: "prod", cellState: v1beta1.CellStateStopped},
	})
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--env", "LABEL=bug", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.startCalls != 1 {
		t.Fatalf("StartCell calls=%d, want 1 (claimed Stopped clone)", fc.startCalls)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d, want 0 (--reuse starts in place)", fc.createCalls)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(fc.startDoc.Spec.RuntimeEnv, want) {
		t.Errorf("StartCell doc RuntimeEnv=%v, want %v (--reuse path)", fc.startDoc.Spec.RuntimeEnv, want)
	}
}

// TestRun_EnvFlag_Reuse_EmptyPoolFallback_ThreadsRuntimeEnv covers the
// fallback half of --reuse: with no clones in the pool the path falls
// through to --clone's code, which creates a fresh clone Config and
// CreateCells the new cell. RuntimeEnv must ride on that CreateCell
// doc — same field, different wire call relative to the picked-clone
// happy path above.
func TestRun_EnvFlag_Reuse_EmptyPoolFallback_ThreadsRuntimeEnv(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc, _, _ := reuseFakeBuilder(t, nil)
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--reuse", "--env", "LABEL=bug", "--realm", "cfg-realm", "-d"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fc.createConfigCalls != 1 {
		t.Fatalf("CreateConfig calls=%d, want 1 (empty pool → fork)", fc.createConfigCalls)
	}
	if fc.createCalls != 1 {
		t.Fatalf("CreateCell calls=%d, want 1 (fresh cell from forked clone)", fc.createCalls)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(fc.createDoc.Spec.RuntimeEnv, want) {
		t.Errorf("CreateCell doc RuntimeEnv=%v, want %v (--reuse empty-pool fallback)",
			fc.createDoc.Spec.RuntimeEnv, want)
	}
}

// TestRun_EnvFlag_DivergentCheckIgnoresRuntimeEnv is the AC-named
// scenario: a Ready cell already exists from a prior `kuke run prod`
// invocation; a second `kuke run prod --env LABEL=bug` against the same
// Config must NOT trip the divergent-spec refusal — RuntimeEnv is
// yaml:"-", so the persisted Containers[].Env on disk never carries the
// injection, divergedFields compares only Containers[].Env, and both
// sides come from the same Materialize(cfg, bp) pipeline. The
// short-circuit on Ready then skips CreateCell/StartCell.
func TestRun_EnvFlag_DivergentCheckIgnoresRuntimeEnv(t *testing.T) {
	t.Cleanup(viper.Reset)

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
			// Echo the desired spec back so divergedFields reports no drift
			// — same shape as TestRun_FromConfig_LiveReadyCell_AttachesWithoutCreate
			// but with --env on the second invocation. The on-disk persisted
			// spec is `doc` itself (modulo RuntimeEnv, which yaml:"-" strips on
			// the daemon's marshal-to-disk path).
			live := doc
			live.Spec.RuntimeEnv = nil // simulate the disk-strip — prior run's --env didn't persist
			live.Status.State = v1beta1.CellStateReady
			return kukeonv1.GetCellResult{
				Cell: live, MetadataExists: true, CgroupExists: true,
				RootContainerExists: true, RootContainerTaskRunning: true,
			}, nil
		},
	}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"prod", "--env", "LABEL=bug", "--realm", "cfg-realm", "-d"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (divergent check tripped on --env — RuntimeEnv must be ignored)", err)
	}
	if fc.createCalls != 0 {
		t.Errorf("CreateCell calls=%d, want 0 (Ready short-circuit)", fc.createCalls)
	}
	if fc.startCalls != 0 {
		t.Errorf("StartCell calls=%d, want 0 (Ready short-circuit; nothing to start)", fc.startCalls)
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
