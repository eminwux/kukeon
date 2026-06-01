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

package cell_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	cell "github.com/eminwux/kukeon/cmd/kuke/create/cell"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newCellDoc(name, realm, space, stack string) v1beta1.CellDoc {
	return v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: name},
		Spec: v1beta1.CellSpec{
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
		},
	}
}

func TestPrintCellResult(t *testing.T) {
	tests := []struct {
		name           string
		result         kukeonv1.CreateCellResult
		expectedOutput []string
	}{
		{
			name: "all resources created",
			result: kukeonv1.CreateCellResult{
				Cell:                    newCellDoc("test-cell", "realm-a", "space-a", "stack-a"),
				Created:                 true,
				MetadataExistsPost:      true,
				CgroupCreated:           true,
				CgroupExistsPost:        true,
				RootContainerCreated:    true,
				RootContainerExistsPost: true,
				Started:                 true,
			},
			expectedOutput: []string{
				`Cell "test-cell" (realm "realm-a", space "space-a", stack "stack-a")`,
				"  - metadata: created",
				"  - cgroup: created",
				"  - root container: created",
				"  - containers: none defined",
				"  - containers: started",
			},
		},
		{
			name: "all resources already existed",
			result: kukeonv1.CreateCellResult{
				Cell:                    newCellDoc("existing-cell", "realm-b", "space-b", "stack-b"),
				Created:                 false,
				MetadataExistsPost:      true,
				CgroupExistsPost:        true,
				RootContainerExistsPost: true,
				Started:                 false,
			},
			expectedOutput: []string{
				`Cell "existing-cell" (realm "realm-b", space "space-b", stack "stack-b")`,
				"  - metadata: already existed",
				"  - cgroup: already existed",
				"  - root container: already existed",
				"  - containers: none defined",
				"  - containers: not started",
			},
		},
		{
			name: "mixed states",
			result: kukeonv1.CreateCellResult{
				Cell:                    newCellDoc("mixed-cell", "realm-c", "space-c", "stack-c"),
				Created:                 false,
				MetadataExistsPost:      true,
				CgroupCreated:           true,
				CgroupExistsPost:        true,
				RootContainerExistsPost: true,
				Started:                 true,
			},
			expectedOutput: []string{
				`Cell "mixed-cell" (realm "realm-c", space "space-c", stack "stack-c")`,
				"  - metadata: already existed",
				"  - cgroup: created",
				"  - root container: already existed",
				"  - containers: none defined",
				"  - containers: started",
			},
		},
		{
			name: "missing resources",
			result: kukeonv1.CreateCellResult{
				Cell:                    newCellDoc("missing-cell", "realm-d", "space-d", "stack-d"),
				Created:                 false,
				MetadataExistsPost:      false,
				CgroupExistsPost:        false,
				RootContainerExistsPost: false,
				Started:                 false,
			},
			expectedOutput: []string{
				`Cell "missing-cell" (realm "realm-d", space "space-d", stack "stack-d")`,
				"  - metadata: missing",
				"  - cgroup: missing",
				"  - root container: missing",
				"  - containers: none defined",
				"  - containers: not started",
			},
		},
		{
			name: "single container created",
			result: kukeonv1.CreateCellResult{
				Cell:                    newCellDoc("single-container-cell", "realm-e", "space-e", "stack-e"),
				Created:                 true,
				MetadataExistsPost:      true,
				CgroupCreated:           true,
				CgroupExistsPost:        true,
				RootContainerCreated:    true,
				RootContainerExistsPost: true,
				Started:                 true,
				Containers: []kukeonv1.ContainerCreationOutcome{
					{Name: "app", ExistsPost: true, Created: true},
				},
			},
			expectedOutput: []string{
				`Cell "single-container-cell" (realm "realm-e", space "space-e", stack "stack-e")`,
				"  - metadata: created",
				"  - cgroup: created",
				"  - root container: created",
				`  - container "app": created`,
				"  - containers: started",
			},
		},
		{
			name: "multiple containers with mixed states",
			result: kukeonv1.CreateCellResult{
				Cell:                    newCellDoc("multi-container-cell", "realm-f", "space-f", "stack-f"),
				Created:                 true,
				MetadataExistsPost:      true,
				CgroupCreated:           true,
				CgroupExistsPost:        true,
				RootContainerCreated:    true,
				RootContainerExistsPost: true,
				Started:                 false,
				Containers: []kukeonv1.ContainerCreationOutcome{
					{Name: "app", ExistsPost: true, Created: true},
					{Name: "worker", ExistsPost: true, Created: false},
					{Name: "cache", ExistsPost: false, Created: false},
				},
			},
			expectedOutput: []string{
				`Cell "multi-container-cell" (realm "realm-f", space "space-f", stack "stack-f")`,
				"  - metadata: created",
				"  - cgroup: created",
				"  - root container: created",
				`  - container "app": created`,
				`  - container "worker": already existed`,
				`  - container "cache": missing`,
				"  - containers: not started",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, buf := newTestCommand()
			cell.PrintCellResult(cmd, tt.result)
			output := buf.String()

			for _, expected := range tt.expectedOutput {
				if !strings.Contains(output, expected) {
					t.Errorf("output missing expected string %q\nGot output:\n%s", expected, output)
				}
			}

			headerCount := strings.Count(output, `Cell "`)
			if headerCount != 1 {
				t.Errorf("expected exactly one cell header, got %d", headerCount)
			}
		})
	}
}

func TestNewCellCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		clientFn       func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error)
		wantErr        string
		wantCallCreate bool
		wantDoc        v1beta1.CellDoc
		wantOutput     []string
	}{
		{
			name: "success: name from args with flags",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			clientFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{
					Cell:                    doc,
					Created:                 true,
					MetadataExistsPost:      true,
					CgroupCreated:           true,
					CgroupExistsPost:        true,
					RootContainerCreated:    true,
					RootContainerExistsPost: true,
					Started:                 true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc:        newCellDoc("test-cell", "realm-a", "space-a", "stack-a"),
			wantOutput: []string{
				`Cell "test-cell" (realm "realm-a", space "space-a", stack "stack-a")`,
			},
		},
		{
			name: "success: name from viper with flags",
			setup: func(t *testing.T, cmd *cobra.Command) {
				viper.Set(config.KUKE_CREATE_CELL_NAME.ViperKey, "viper-cell")
				setFlag(t, cmd, "realm", "realm-b")
				setFlag(t, cmd, "space", "space-b")
				setFlag(t, cmd, "stack", "stack-b")
			},
			clientFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{
					Cell:                    doc,
					Created:                 true,
					MetadataExistsPost:      true,
					CgroupCreated:           true,
					CgroupExistsPost:        true,
					RootContainerCreated:    true,
					RootContainerExistsPost: true,
					Started:                 true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc:        newCellDoc("viper-cell", "realm-b", "space-b", "stack-b"),
			wantOutput: []string{
				`Cell "viper-cell" (realm "realm-b", space "space-b", stack "stack-b")`,
			},
		},
		{
			name: "success: all from viper",
			setup: func(_ *testing.T, _ *cobra.Command) {
				viper.Set(config.KUKE_CREATE_CELL_NAME.ViperKey, "all-viper-cell")
				viper.Set(config.KUKE_CREATE_CELL_REALM.ViperKey, "realm-c")
				viper.Set(config.KUKE_CREATE_CELL_SPACE.ViperKey, "space-c")
				viper.Set(config.KUKE_CREATE_CELL_STACK.ViperKey, "stack-c")
			},
			clientFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{
					Cell:                    doc,
					Created:                 false,
					MetadataExistsPost:      true,
					CgroupExistsPost:        true,
					RootContainerExistsPost: true,
					Started:                 false,
				}, nil
			},
			wantCallCreate: true,
			wantDoc:        newCellDoc("all-viper-cell", "realm-c", "space-c", "stack-c"),
			wantOutput: []string{
				`Cell "all-viper-cell" (realm "realm-c", space "space-c", stack "stack-c")`,
			},
		},
		{
			name:           "error: missing name",
			setup:          func(_ *testing.T, _ *cobra.Command) {},
			wantErr:        "cell name is required",
			wantCallCreate: false,
		},
		{
			name: "uses default realm when realm flag not set",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			clientFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{
					Cell:                    doc,
					Created:                 true,
					MetadataExistsPost:      true,
					CgroupCreated:           true,
					CgroupExistsPost:        true,
					RootContainerCreated:    true,
					RootContainerExistsPost: true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc:        newCellDoc("test-cell", "default", "space-a", "stack-a"),
		},
		{
			name: "uses default space when space flag not set",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			clientFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{
					Cell:                    doc,
					Created:                 true,
					MetadataExistsPost:      true,
					CgroupCreated:           true,
					CgroupExistsPost:        true,
					RootContainerCreated:    true,
					RootContainerExistsPost: true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc:        newCellDoc("test-cell", "realm-a", "default", "stack-a"),
		},
		{
			name: "uses default stack when stack flag not set",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
			},
			clientFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{
					Cell:                    doc,
					Created:                 true,
					MetadataExistsPost:      true,
					CgroupCreated:           true,
					CgroupExistsPost:        true,
					RootContainerCreated:    true,
					RootContainerExistsPost: true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc:        newCellDoc("test-cell", "realm-a", "space-a", "default"),
		},
		{
			name: "error: CreateCell fails",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			clientFn: func(_ v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{}, errdefs.ErrCreateCell
			},
			wantErr:        "failed to create cell",
			wantCallCreate: true,
			wantDoc:        newCellDoc("test-cell", "realm-a", "space-a", "stack-a"),
		},
		{
			name: "error: realm with whitespace trimmed",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  ")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			clientFn: func(_ v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{}, errors.New("unexpected call")
			},
			// With blank realm, local path short-circuits in the controller;
			// but through the mock client we also see the empty RealmID get through
			// since normalization runs server-side. Assert call path + empty realm.
			wantCallCreate: true,
			wantDoc:        newCellDoc("test-cell", "", "space-a", "stack-a"),
			wantErr:        "unexpected call",
		},
		{
			name: "success: realm with whitespace trimmed",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  realm-a  ")
				setFlag(t, cmd, "space", "  space-a  ")
				setFlag(t, cmd, "stack", "  stack-a  ")
			},
			clientFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
				return kukeonv1.CreateCellResult{
					Cell:                    doc,
					Created:                 true,
					MetadataExistsPost:      true,
					CgroupCreated:           true,
					CgroupExistsPost:        true,
					RootContainerCreated:    true,
					RootContainerExistsPost: true,
					Started:                 true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc:        newCellDoc("test-cell", "realm-a", "space-a", "stack-a"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createDoc v1beta1.CellDoc

			cmd := cell.NewCellCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			if tt.clientFn != nil {
				fake := &fakeClient{
					createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
						createCalled = true
						createDoc = doc
						return tt.clientFn(doc)
					},
				}
				ctx = context.WithValue(ctx, cell.MockControllerKey{}, kukeonv1.Client(fake))
			}

			cmd.SetContext(ctx)

			if tt.setup != nil {
				tt.setup(t, cmd)
			}

			cmd.SetArgs(tt.args)

			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if createCalled != tt.wantCallCreate {
				t.Errorf("CreateCell called=%v want=%v", createCalled, tt.wantCallCreate)
			}

			if tt.wantCallCreate {
				if createDoc.Metadata.Name != tt.wantDoc.Metadata.Name {
					t.Errorf("CreateCell Name=%q want=%q", createDoc.Metadata.Name, tt.wantDoc.Metadata.Name)
				}
				if createDoc.Spec.RealmID != tt.wantDoc.Spec.RealmID {
					t.Errorf("CreateCell RealmID=%q want=%q", createDoc.Spec.RealmID, tt.wantDoc.Spec.RealmID)
				}
				if createDoc.Spec.SpaceID != tt.wantDoc.Spec.SpaceID {
					t.Errorf("CreateCell SpaceID=%q want=%q", createDoc.Spec.SpaceID, tt.wantDoc.Spec.SpaceID)
				}
				if createDoc.Spec.StackID != tt.wantDoc.Spec.StackID {
					t.Errorf("CreateCell StackID=%q want=%q", createDoc.Spec.StackID, tt.wantDoc.Spec.StackID)
				}
			}

			if tt.wantOutput != nil {
				output := cmd.OutOrStdout().(*bytes.Buffer).String()
				for _, expected := range tt.wantOutput {
					if !strings.Contains(output, expected) {
						t.Errorf("output missing expected string %q\nGot output:\n%s", expected, output)
					}
				}
			}
		})
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	createCellFn      func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error)
	materializeCellFn func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error)
	getCellFn         func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	getBlueprintFn    func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error)
	getConfigFn       func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error)
}

func (f *fakeClient) CreateCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	if f.createCellFn == nil {
		return kukeonv1.CreateCellResult{}, errors.New("unexpected CreateCell call")
	}
	return f.createCellFn(doc)
}

func (f *fakeClient) MaterializeCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	if f.materializeCellFn == nil {
		return kukeonv1.CreateCellResult{}, errors.New("unexpected MaterializeCell call")
	}
	return f.materializeCellFn(doc)
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	if f.getCellFn == nil {
		// Default to "not found" so the existence pre-check sees a clean slate
		// when a test doesn't care about it.
		return kukeonv1.GetCellResult{MetadataExists: false}, errdefs.ErrCellNotFound
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) GetBlueprint(
	_ context.Context, doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	if f.getBlueprintFn == nil {
		return kukeonv1.GetBlueprintResult{}, errors.New("unexpected GetBlueprint call")
	}
	return f.getBlueprintFn(doc)
}

func (f *fakeClient) GetConfig(
	_ context.Context, doc v1beta1.CellConfigDoc,
) (kukeonv1.GetConfigResult, error) {
	if f.getConfigFn == nil {
		return kukeonv1.GetConfigResult{}, errors.New("unexpected GetConfig call")
	}
	return f.getConfigFn(doc)
}

func newTestCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("failed to set flag %s: %v", name, err)
	}
}

func TestNewCellCmd_AutocompleteRegistration(t *testing.T) {
	cmd := cell.NewCellCmd()

	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("expected 'realm' flag to exist")
	}
	if realmFlag.Usage != "Realm that owns the cell" {
		t.Errorf("unexpected realm flag usage: %q", realmFlag.Usage)
	}

	spaceFlag := cmd.Flags().Lookup("space")
	if spaceFlag == nil {
		t.Fatal("expected 'space' flag to exist")
	}
	if spaceFlag.Usage != "Space that owns the cell" {
		t.Errorf("unexpected space flag usage: %q", spaceFlag.Usage)
	}

	stackFlag := cmd.Flags().Lookup("stack")
	if stackFlag == nil {
		t.Fatal("expected 'stack' flag to exist")
	}
	if stackFlag.Usage != "Stack that owns the cell" {
		t.Errorf("unexpected stack flag usage: %q", stackFlag.Usage)
	}

	for _, name := range []string{"from-blueprint", "from-config", "param", "param-file"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected %q flag to exist", name)
		}
	}
}

// blueprintDoc returns a minimal CellBlueprintDoc the fake daemon can hand
// back from GetBlueprint. One TAG parameter substitutes into the container
// image so --param coverage has something to verify.
func blueprintDoc() v1beta1.CellBlueprintDoc {
	def := "latest"
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  "web",
			Realm: "bp-realm",
			Space: "bp-space",
			Stack: "bp-stack",
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

// configDoc returns a CellConfigDoc that references the blueprint above with
// one spec.values override. Same minimal shape used by cmd/kuke/run tests.
func configDoc() v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  "prod",
			Realm: "cfg-realm",
			Space: "cfg-space",
			Stack: "cfg-stack",
		},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{
				Name:  "web",
				Realm: "bp-realm",
				Space: "bp-space",
				Stack: "bp-stack",
			},
			Values: map[string]string{"TAG": "stable"},
		},
	}
}

// successResultFromDoc echoes the input doc back as a "successfully created
// stopped cell" result for the MaterializeCell fake. Started=false mirrors
// what the daemon-side controller.MaterializeCell produces.
func successResultFromDoc(doc v1beta1.CellDoc) kukeonv1.CreateCellResult {
	return kukeonv1.CreateCellResult{
		Cell: doc, Created: true, MetadataExistsPost: true,
		CgroupCreated: true, CgroupExistsPost: true,
		RootContainerCreated: true, RootContainerExistsPost: true,
		Started: false,
		Containers: []kukeonv1.ContainerCreationOutcome{
			{Name: "main", ExistsPost: true, Created: true},
		},
	}
}

func newTestExecCmd(t *testing.T, fc *fakeClient) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := cell.NewCellCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, cell.MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)
	return cmd, out
}

func TestCreateCell_FromBlueprint_HappyPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	var materializeCalled bool
	var materializeDoc v1beta1.CellDoc
	fc := &fakeClient{
		getBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			if doc.Metadata.Name != "web" {
				t.Errorf("GetBlueprint name=%q want web", doc.Metadata.Name)
			}
			if doc.Metadata.Realm != "bp-realm" {
				t.Errorf("GetBlueprint realm=%q want bp-realm", doc.Metadata.Realm)
			}
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		materializeCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			materializeCalled = true
			materializeDoc = doc
			return successResultFromDoc(doc), nil
		},
	}

	cmd, out := newTestExecCmd(t, fc)
	setFlag(t, cmd, "realm", "bp-realm")
	setFlag(t, cmd, "from-blueprint", "web")
	setFlag(t, cmd, "param", "TAG=v9")
	cmd.SetArgs([]string{"web-1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !materializeCalled {
		t.Fatal("MaterializeCell was not called")
	}
	if materializeDoc.Metadata.Name != "web-1" {
		t.Errorf("materialised name=%q want web-1 (cell name pinned to positional arg)", materializeDoc.Metadata.Name)
	}
	if materializeDoc.Spec.RealmID != "bp-realm" {
		t.Errorf("RealmID=%q want bp-realm (blueprint metadata wins)", materializeDoc.Spec.RealmID)
	}
	if got := materializeDoc.Spec.Containers[0].Image; got != "registry.example.com/web:v9" {
		t.Errorf("image=%q want ${TAG} substituted to v9", got)
	}
	if !strings.Contains(out.String(), "containers: not started") {
		t.Errorf("expected 'containers: not started' in output (materialise-but-don't-start); got:\n%s", out.String())
	}
}

func TestCreateCell_FromBlueprint_NotFound_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{MetadataExists: false}, nil
		},
	}
	cmd, _ := newTestExecCmd(t, fc)
	setFlag(t, cmd, "realm", "bp-realm")
	setFlag(t, cmd, "from-blueprint", "ghost")
	cmd.SetArgs([]string{"missing-cell"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, errdefs.ErrBlueprintNotFound) {
		t.Fatalf("err=%v want ErrBlueprintNotFound", err)
	}
}

func TestCreateCell_FromConfig_HappyPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	var materializeCalled bool
	var materializeDoc v1beta1.CellDoc
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
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		materializeCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			materializeCalled = true
			materializeDoc = doc
			return successResultFromDoc(doc), nil
		},
	}

	cmd, out := newTestExecCmd(t, fc)
	setFlag(t, cmd, "realm", "cfg-realm")
	setFlag(t, cmd, "from-config", "prod")
	cmd.SetArgs([]string{"prod-1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !materializeCalled {
		t.Fatal("MaterializeCell was not called")
	}
	if materializeDoc.Metadata.Name != "prod-1" {
		t.Errorf("materialised name=%q want prod-1", materializeDoc.Metadata.Name)
	}
	if materializeDoc.Spec.RealmID != "cfg-realm" {
		t.Errorf("RealmID=%q want cfg-realm (Config metadata wins)", materializeDoc.Spec.RealmID)
	}
	if got := materializeDoc.Spec.Containers[0].Image; got != "registry.example.com/web:stable" {
		t.Errorf("image=%q want ${TAG} substituted to stable from Config values", got)
	}
	if !strings.Contains(out.String(), "containers: not started") {
		t.Errorf("expected 'containers: not started' in output; got:\n%s", out.String())
	}
}

func TestCreateCell_FromConfig_NotFound_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
	}
	cmd, _ := newTestExecCmd(t, fc)
	setFlag(t, cmd, "realm", "cfg-realm")
	setFlag(t, cmd, "from-config", "ghost")
	cmd.SetArgs([]string{"missing-cell"})

	err := cmd.Execute()
	if err == nil || !errors.Is(err, errdefs.ErrConfigNotFound) {
		t.Fatalf("err=%v want ErrConfigNotFound", err)
	}
}

func TestCreateCell_MutualExclusion_FromBlueprintAndFromConfig(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, _ := newTestExecCmd(t, &fakeClient{})
	setFlag(t, cmd, "from-blueprint", "web")
	setFlag(t, cmd, "from-config", "prod")
	cmd.SetArgs([]string{"x"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --from-blueprint + --from-config combination")
	}
	// Cobra's MarkFlagsMutuallyExclusive emits "if any flags in the group ...
	// none of the others can be" — anchor on the stable substring rather than
	// the verbatim message so a cobra wording tweak doesn't false-flag this.
	if !strings.Contains(err.Error(), "from-blueprint") || !strings.Contains(err.Error(), "from-config") {
		t.Errorf("err=%v should name both --from-blueprint and --from-config", err)
	}
}

func TestCreateCell_ParamRejectedWithFromConfig(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			t.Fatal("GetConfig must not be called when --param + --from-config is rejected")
			return kukeonv1.GetConfigResult{}, nil
		},
	}
	cmd, _ := newTestExecCmd(t, fc)
	setFlag(t, cmd, "from-config", "prod")
	setFlag(t, cmd, "param", "K=V")
	cmd.SetArgs([]string{"x"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --param + --from-config combination")
	}
	if !strings.Contains(err.Error(), "--param is not valid with --from-config") {
		t.Errorf("err=%v want '--param is not valid with --from-config'", err)
	}
}

func TestCreateCell_NameCollision_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{MetadataExists: true}, nil
		},
		materializeCellFn: func(v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			t.Fatal("MaterializeCell must not be called when cell already exists")
			return kukeonv1.CreateCellResult{}, nil
		},
	}
	cmd, _ := newTestExecCmd(t, fc)
	setFlag(t, cmd, "realm", "bp-realm")
	setFlag(t, cmd, "from-blueprint", "web")
	cmd.SetArgs([]string{"taken-name"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when cell name collides with existing cell")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err=%v should mention the cell already exists", err)
	}
	if !strings.Contains(err.Error(), "kuke delete cell taken-name") {
		t.Errorf("err=%v should point at `kuke delete cell taken-name`", err)
	}
}

func TestCreateCell_EmptyShell_StillUsesCreateCell(t *testing.T) {
	// Regression guard for AC #1: the empty-shell path (no --from-* flag)
	// must continue to call CreateCell — *not* MaterializeCell — so the
	// daemon's idempotent start step still runs for the pre-#818 workflow C
	// (create cell → create container → start).
	t.Cleanup(viper.Reset)

	var createCalled bool
	fc := &fakeClient{
		createCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			createCalled = true
			if doc.Metadata.Name != "empty-1" {
				t.Errorf("CreateCell name=%q want empty-1", doc.Metadata.Name)
			}
			return kukeonv1.CreateCellResult{
				Cell: doc, Created: true, MetadataExistsPost: true,
				CgroupCreated: true, CgroupExistsPost: true,
				RootContainerCreated: true, RootContainerExistsPost: true, Started: true,
			}, nil
		},
		materializeCellFn: func(v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			t.Fatal("empty-shell path must not call MaterializeCell")
			return kukeonv1.CreateCellResult{}, nil
		},
	}
	cmd, _ := newTestExecCmd(t, fc)
	setFlag(t, cmd, "realm", "r")
	setFlag(t, cmd, "space", "s")
	setFlag(t, cmd, "stack", "k")
	cmd.SetArgs([]string{"empty-1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !createCalled {
		t.Fatal("CreateCell was not called on the empty-shell path")
	}
}
