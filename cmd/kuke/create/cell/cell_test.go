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

	createCellFn func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error)
}

func (f *fakeClient) CreateCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	if f.createCellFn == nil {
		return kukeonv1.CreateCellResult{}, errors.New("unexpected CreateCell call")
	}
	return f.createCellFn(doc)
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
}
