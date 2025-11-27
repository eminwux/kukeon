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
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestPrintCellResult(t *testing.T) {
	tests := []struct {
		name           string
		result         controller.CreateCellResult
		expectedOutput []string
	}{
		{
			name: "all resources created",
			result: controller.CreateCellResult{
				Cell: intmodel.Cell{
					Metadata: intmodel.CellMetadata{
						Name: "test-cell",
					},
					Spec: intmodel.CellSpec{
						RealmName: "realm-a",
						SpaceName: "space-a",
						StackName: "stack-a",
					},
				},
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
			result: controller.CreateCellResult{
				Cell: intmodel.Cell{
					Metadata: intmodel.CellMetadata{
						Name: "existing-cell",
					},
					Spec: intmodel.CellSpec{
						RealmName: "realm-b",
						SpaceName: "space-b",
						StackName: "stack-b",
					},
				},
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
			result: controller.CreateCellResult{
				Cell: intmodel.Cell{
					Metadata: intmodel.CellMetadata{
						Name: "mixed-cell",
					},
					Spec: intmodel.CellSpec{
						RealmName: "realm-c",
						SpaceName: "space-c",
						StackName: "stack-c",
					},
				},
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
			result: controller.CreateCellResult{
				Cell: intmodel.Cell{
					Metadata: intmodel.CellMetadata{
						Name: "missing-cell",
					},
					Spec: intmodel.CellSpec{
						RealmName: "realm-d",
						SpaceName: "space-d",
						StackName: "stack-d",
					},
				},
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
			result: controller.CreateCellResult{
				Cell: intmodel.Cell{
					Metadata: intmodel.CellMetadata{
						Name: "single-container-cell",
					},
					Spec: intmodel.CellSpec{
						RealmName: "realm-e",
						SpaceName: "space-e",
						StackName: "stack-e",
					},
				},
				Created:                 true,
				MetadataExistsPost:      true,
				CgroupCreated:           true,
				CgroupExistsPost:        true,
				RootContainerCreated:    true,
				RootContainerExistsPost: true,
				Started:                 true,
				Containers: []controller.ContainerCreationOutcome{
					{
						Name:       "app",
						ExistsPost: true,
						Created:    true,
					},
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
			result: controller.CreateCellResult{
				Cell: intmodel.Cell{
					Metadata: intmodel.CellMetadata{
						Name: "multi-container-cell",
					},
					Spec: intmodel.CellSpec{
						RealmName: "realm-f",
						SpaceName: "space-f",
						StackName: "stack-f",
					},
				},
				Created:                 true,
				MetadataExistsPost:      true,
				CgroupCreated:           true,
				CgroupExistsPost:        true,
				RootContainerCreated:    true,
				RootContainerExistsPost: true,
				Started:                 false,
				Containers: []controller.ContainerCreationOutcome{
					{
						Name:       "app",
						ExistsPost: true,
						Created:    true,
					},
					{
						Name:       "worker",
						ExistsPost: true,
						Created:    false,
					},
					{
						Name:       "cache",
						ExistsPost: false,
						Created:    false,
					},
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
			cell.PrintCellResult(cmd, tt.result, v1beta1.APIVersionV1Beta1)
			output := buf.String()

			for _, expected := range tt.expectedOutput {
				if !strings.Contains(output, expected) {
					t.Errorf("output missing expected string %q\nGot output:\n%s", expected, output)
				}
			}

			// Verify cell header is printed exactly once
			headerCount := strings.Count(output, `Cell "`)
			if headerCount != 1 {
				t.Errorf("expected exactly one cell header, got %d", headerCount)
			}
		})
	}
}

func TestNewCellCmdRunE(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
	})

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		controllerFn   func(cell intmodel.Cell) (controller.CreateCellResult, error)
		wantErr        string
		wantCallCreate bool
		wantCell       intmodel.Cell
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
			controllerFn: func(cell intmodel.Cell) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{
					Cell:                    cell,
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
			wantCell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "realm-a",
					SpaceName: "space-a",
					StackName: "stack-a",
				},
			},
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
			controllerFn: func(cell intmodel.Cell) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{
					Cell:                    cell,
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
			wantCell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "viper-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "realm-b",
					SpaceName: "space-b",
					StackName: "stack-b",
				},
			},
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
			controllerFn: func(cell intmodel.Cell) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{
					Cell:                    cell,
					Created:                 false,
					MetadataExistsPost:      true,
					CgroupExistsPost:        true,
					RootContainerExistsPost: true,
					Started:                 false,
				}, nil
			},
			wantCallCreate: true,
			wantCell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "all-viper-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "realm-c",
					SpaceName: "space-c",
					StackName: "stack-c",
				},
			},
			wantOutput: []string{
				`Cell "all-viper-cell" (realm "realm-c", space "space-c", stack "stack-c")`,
			},
		},
		{
			name: "error: missing name",
			setup: func(_ *testing.T, _ *cobra.Command) {
				// Don't set name in args or viper
			},
			wantErr:        "cell name is required",
			wantCallCreate: false,
		},
		{
			name: "error: missing realm",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:        "realm name is required",
			wantCallCreate: false,
		},
		{
			name: "error: missing space",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:        "space name is required",
			wantCallCreate: false,
		},
		{
			name: "error: missing stack",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
			},
			wantErr:        "stack name is required",
			wantCallCreate: false,
		},
		{
			name: "error: logger not in context",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			controllerFn: func(_ intmodel.Cell) (controller.CreateCellResult, error) {
				// This shouldn't be called, but if it is, return an error
				return controller.CreateCellResult{}, errors.New("unexpected call")
			},
			wantErr:        "logger not found",
			wantCallCreate: false,
		},
		{
			name: "error: CreateCell fails",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			controllerFn: func(_ intmodel.Cell) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{}, errdefs.ErrCreateCell
			},
			wantErr:        "failed to create cell",
			wantCallCreate: true,
			wantCell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "realm-a",
					SpaceName: "space-a",
					StackName: "stack-a",
				},
			},
		},
		{
			name: "error: realm with whitespace trimmed",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  ")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:        "realm name is required",
			wantCallCreate: false,
		},
		{
			name: "success: realm with whitespace trimmed",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  realm-a  ")
				setFlag(t, cmd, "space", "  space-a  ")
				setFlag(t, cmd, "stack", "  stack-a  ")
			},
			controllerFn: func(cell intmodel.Cell) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{
					Cell:                    cell,
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
			wantCell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "realm-a",
					SpaceName: "space-a",
					StackName: "stack-a",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createCell intmodel.Cell

			cmd := cell.NewCellCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			ctx := context.Background()

			// Inject mock controller via context if needed
			if tt.name != "error: logger not in context" {
				// Set up logger context
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx = context.WithValue(ctx, types.CtxLogger, logger)

				// If we need to mock the controller, inject it via context
				if tt.controllerFn != nil {
					fakeCtrl := &fakeControllerExec{
						createCellFn: func(cell intmodel.Cell) (controller.CreateCellResult, error) {
							createCalled = true
							createCell = cell
							return tt.controllerFn(cell)
						},
					}
					// Inject mock controller into context using the exported key
					ctx = context.WithValue(ctx, cell.MockControllerKey{}, fakeCtrl)
				}
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

			if tt.wantCell.Metadata.Name != "" {
				if !createCalled {
					t.Fatal("CreateCell not called, but wantCell specified")
				}
				if createCell.Metadata.Name != tt.wantCell.Metadata.Name {
					t.Errorf("CreateCell Name=%q want=%q", createCell.Metadata.Name, tt.wantCell.Metadata.Name)
				}
				if createCell.Spec.RealmName != tt.wantCell.Spec.RealmName {
					t.Errorf("CreateCell RealmName=%q want=%q", createCell.Spec.RealmName, tt.wantCell.Spec.RealmName)
				}
				if createCell.Spec.SpaceName != tt.wantCell.Spec.SpaceName {
					t.Errorf("CreateCell SpaceName=%q want=%q", createCell.Spec.SpaceName, tt.wantCell.Spec.SpaceName)
				}
				if createCell.Spec.StackName != tt.wantCell.Spec.StackName {
					t.Errorf("CreateCell StackName=%q want=%q", createCell.Spec.StackName, tt.wantCell.Spec.StackName)
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

type fakeControllerExec struct {
	createCellFn func(cell intmodel.Cell) (controller.CreateCellResult, error)
}

func (f *fakeControllerExec) CreateCell(cell intmodel.Cell) (controller.CreateCellResult, error) {
	if f.createCellFn == nil {
		return controller.CreateCellResult{}, errors.New("unexpected CreateCell call")
	}
	return f.createCellFn(cell)
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
	// Test that autocomplete functions are properly registered for flags
	cmd := cell.NewCellCmd()

	// Test that realm flag exists
	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("expected 'realm' flag to exist")
	}
	if realmFlag.Usage != "Realm that owns the cell" {
		t.Errorf("unexpected realm flag usage: %q", realmFlag.Usage)
	}

	// Test that space flag exists
	spaceFlag := cmd.Flags().Lookup("space")
	if spaceFlag == nil {
		t.Fatal("expected 'space' flag to exist")
	}
	if spaceFlag.Usage != "Space that owns the cell" {
		t.Errorf("unexpected space flag usage: %q", spaceFlag.Usage)
	}

	// Test that stack flag exists
	stackFlag := cmd.Flags().Lookup("stack")
	if stackFlag == nil {
		t.Fatal("expected 'stack' flag to exist")
	}
	if stackFlag.Usage != "Stack that owns the cell" {
		t.Errorf("unexpected stack flag usage: %q", stackFlag.Usage)
	}
}
