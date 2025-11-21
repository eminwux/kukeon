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
				Name:                    "test-cell",
				RealmName:               "realm-a",
				SpaceName:               "space-a",
				StackName:               "stack-a",
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
				Name:                    "existing-cell",
				RealmName:               "realm-b",
				SpaceName:               "space-b",
				StackName:               "stack-b",
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
				Name:                    "mixed-cell",
				RealmName:               "realm-c",
				SpaceName:               "space-c",
				StackName:               "stack-c",
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
				Name:                    "missing-cell",
				RealmName:               "realm-d",
				SpaceName:               "space-d",
				StackName:               "stack-d",
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
				Name:                    "single-container-cell",
				RealmName:               "realm-e",
				SpaceName:               "space-e",
				StackName:               "stack-e",
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
				Name:                    "multi-container-cell",
				RealmName:               "realm-f",
				SpaceName:               "space-f",
				StackName:               "stack-f",
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
			cell.PrintCellResult(cmd, tt.result)
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
		controllerFn   func(opts controller.CreateCellOptions) (controller.CreateCellResult, error)
		wantErr        string
		wantCallCreate bool
		wantOpts       *controller.CreateCellOptions
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
			controllerFn: func(opts controller.CreateCellOptions) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{
					Name:                    opts.Name,
					RealmName:               opts.RealmName,
					SpaceName:               opts.SpaceName,
					StackName:               opts.StackName,
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
			wantOpts: &controller.CreateCellOptions{
				Name:      "test-cell",
				RealmName: "realm-a",
				SpaceName: "space-a",
				StackName: "stack-a",
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
			controllerFn: func(opts controller.CreateCellOptions) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{
					Name:                    opts.Name,
					RealmName:               opts.RealmName,
					SpaceName:               opts.SpaceName,
					StackName:               opts.StackName,
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
			wantOpts: &controller.CreateCellOptions{
				Name:      "viper-cell",
				RealmName: "realm-b",
				SpaceName: "space-b",
				StackName: "stack-b",
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
			controllerFn: func(opts controller.CreateCellOptions) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{
					Name:                    opts.Name,
					RealmName:               opts.RealmName,
					SpaceName:               opts.SpaceName,
					StackName:               opts.StackName,
					Created:                 false,
					MetadataExistsPost:      true,
					CgroupExistsPost:        true,
					RootContainerExistsPost: true,
					Started:                 false,
				}, nil
			},
			wantCallCreate: true,
			wantOpts: &controller.CreateCellOptions{
				Name:      "all-viper-cell",
				RealmName: "realm-c",
				SpaceName: "space-c",
				StackName: "stack-c",
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
			controllerFn: func(_ controller.CreateCellOptions) (controller.CreateCellResult, error) {
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
			controllerFn: func(_ controller.CreateCellOptions) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{}, errdefs.ErrCreateCell
			},
			wantErr:        "failed to create cell",
			wantCallCreate: true,
			wantOpts: &controller.CreateCellOptions{
				Name:      "test-cell",
				RealmName: "realm-a",
				SpaceName: "space-a",
				StackName: "stack-a",
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
			controllerFn: func(opts controller.CreateCellOptions) (controller.CreateCellResult, error) {
				return controller.CreateCellResult{
					Name:                    opts.Name,
					RealmName:               opts.RealmName,
					SpaceName:               opts.SpaceName,
					StackName:               opts.StackName,
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
			wantOpts: &controller.CreateCellOptions{
				Name:      "test-cell",
				RealmName: "realm-a",
				SpaceName: "space-a",
				StackName: "stack-a",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createOpts controller.CreateCellOptions

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
						createCellFn: func(opts controller.CreateCellOptions) (controller.CreateCellResult, error) {
							createCalled = true
							createOpts = opts
							return tt.controllerFn(opts)
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

			if tt.wantOpts != nil {
				if createOpts.Name != tt.wantOpts.Name {
					t.Errorf("CreateCell Name=%q want=%q", createOpts.Name, tt.wantOpts.Name)
				}
				if createOpts.RealmName != tt.wantOpts.RealmName {
					t.Errorf("CreateCell RealmName=%q want=%q", createOpts.RealmName, tt.wantOpts.RealmName)
				}
				if createOpts.SpaceName != tt.wantOpts.SpaceName {
					t.Errorf("CreateCell SpaceName=%q want=%q", createOpts.SpaceName, tt.wantOpts.SpaceName)
				}
				if createOpts.StackName != tt.wantOpts.StackName {
					t.Errorf("CreateCell StackName=%q want=%q", createOpts.StackName, tt.wantOpts.StackName)
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
	createCellFn func(opts controller.CreateCellOptions) (controller.CreateCellResult, error)
}

func (f *fakeControllerExec) CreateCell(opts controller.CreateCellOptions) (controller.CreateCellResult, error) {
	if f.createCellFn == nil {
		return controller.CreateCellResult{}, errors.New("unexpected CreateCell call")
	}
	return f.createCellFn(opts)
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
