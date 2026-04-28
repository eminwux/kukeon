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

	getCellFn    func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	createCellFn func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error)

	getCalls    int
	createCalls int
	createDoc   v1beta1.CellDoc
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

func newCmd(t *testing.T, fc *fakeClient) (*cobra.Command, *bytes.Buffer) {
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

func TestRun_MissingFile_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{}
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "required flag") {
		t.Fatalf("err=%v want missing-flag error", err)
	}
}

func TestNewRunCmd_AutocompleteRegistration(t *testing.T) {
	cmd := runcmd.NewRunCmd()
	for _, flag := range []string{"realm", "space", "stack"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("expected %q flag to exist", flag)
		}
	}
	// File flag is required and short-aliased.
	fileFlag := cmd.Flags().Lookup("file")
	if fileFlag == nil || fileFlag.Shorthand != "f" {
		t.Errorf("expected -f/--file flag, got %+v", fileFlag)
	}
	outputFlag := cmd.Flags().Lookup("output")
	if outputFlag == nil || outputFlag.Shorthand != "o" {
		t.Errorf("expected -o/--output flag, got %+v", outputFlag)
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
