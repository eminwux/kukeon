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

package realm_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	realm "github.com/eminwux/kukeon/cmd/kuke/delete/realm"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Behaviors covered:
// 1. Command structure (Use, Short, Args, SilenceUsage, SilenceErrors)
// 2. Name argument handling (from args, whitespace trimming)
// 3. Flag parsing (--force, --cascade)
// 4. Controller creation from command context
// 5. Error propagation from dependencies

func TestNewRealmCmd_CommandStructure(t *testing.T) {
	cmd := realm.NewRealmCmd()

	if cmd.Use != "realm [name]" {
		t.Errorf("expected Use to be 'realm [name]', got %q", cmd.Use)
	}

	if cmd.Short != "Delete a realm" {
		t.Errorf("expected Short to be 'Delete a realm', got %q", cmd.Short)
	}

	if !cmd.SilenceUsage {
		t.Error("expected SilenceUsage to be true")
	}

	if cmd.SilenceErrors {
		t.Error("expected SilenceErrors to be false")
	}

	// Args should be ExactArgs(1) which will be validated by Cobra
	// We can't easily test ExactArgs directly, but we can verify the command fails with wrong arg count
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no args provided, got nil")
	}
}

func TestNewRealmCmd(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		forceFlag     bool
		cascadeFlag   bool
		controller    realm.RealmController
		expectErr     bool
		expectErrText string
		expectMatch   string
		noLogger      bool
	}{
		{
			name:          "error missing logger in context",
			args:          []string{"test-realm"},
			noLogger:      true,
			expectErr:     true,
			expectErrText: "logger not found in context",
		},
		{
			name:          "error with empty name after trim",
			args:          []string{"   "},
			expectErr:     true,
			expectErrText: "realm name is required",
		},
		{
			name:      "error with no args",
			args:      []string{},
			expectErr: true,
		},
		{
			name:        "success with name argument and default flags",
			args:        []string{"test-realm"},
			forceFlag:   false,
			cascadeFlag: false,
			controller: &fakeRealmController{
				deleteRealmFn: func(doc *v1beta1.RealmDoc, force, cascade bool) (controller.DeleteRealmResult, error) {
					if doc.Metadata.Name != "test-realm" {
						t.Fatalf("expected name %q, got %q", "test-realm", doc.Metadata.Name)
					}
					if force {
						t.Fatalf("expected force to be false, got true")
					}
					if cascade {
						t.Fatalf("expected cascade to be false, got true")
					}
					return controller.DeleteRealmResult{
						RealmDoc: &v1beta1.RealmDoc{
							Metadata: v1beta1.RealmMetadata{
								Name: "test-realm",
							},
						},
						Deleted: []string{"metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted realm "test-realm"`,
		},
		{
			name:        "name trimming whitespace",
			args:        []string{"  test-realm  "},
			forceFlag:   false,
			cascadeFlag: false,
			controller: &fakeRealmController{
				deleteRealmFn: func(doc *v1beta1.RealmDoc, _ bool, _ bool) (controller.DeleteRealmResult, error) {
					if doc.Metadata.Name != "test-realm" {
						t.Fatalf("expected trimmed name %q, got %q", "test-realm", doc.Metadata.Name)
					}
					return controller.DeleteRealmResult{
						RealmDoc: &v1beta1.RealmDoc{
							Metadata: v1beta1.RealmMetadata{
								Name: "test-realm",
							},
						},
						Deleted: []string{"metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted realm "test-realm"`,
		},
		{
			name:        "success with --force flag",
			args:        []string{"test-realm"},
			forceFlag:   true,
			cascadeFlag: false,
			controller: &fakeRealmController{
				deleteRealmFn: func(doc *v1beta1.RealmDoc, force bool, cascade bool) (controller.DeleteRealmResult, error) {
					if doc.Metadata.Name != "test-realm" {
						t.Fatalf("expected name %q, got %q", "test-realm", doc.Metadata.Name)
					}
					if !force {
						t.Fatalf("expected force to be true, got false")
					}
					if cascade {
						t.Fatalf("expected cascade to be false, got true")
					}
					return controller.DeleteRealmResult{
						RealmDoc: &v1beta1.RealmDoc{
							Metadata: v1beta1.RealmMetadata{
								Name: "test-realm",
							},
						},
						Deleted: []string{"metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted realm "test-realm"`,
		},
		{
			name:        "success with --cascade flag",
			args:        []string{"test-realm"},
			forceFlag:   false,
			cascadeFlag: true,
			controller: &fakeRealmController{
				deleteRealmFn: func(doc *v1beta1.RealmDoc, force bool, cascade bool) (controller.DeleteRealmResult, error) {
					if doc.Metadata.Name != "test-realm" {
						t.Fatalf("expected name %q, got %q", "test-realm", doc.Metadata.Name)
					}
					if force {
						t.Fatalf("expected force to be false, got true")
					}
					if !cascade {
						t.Fatalf("expected cascade to be true, got false")
					}
					return controller.DeleteRealmResult{
						RealmDoc: &v1beta1.RealmDoc{
							Metadata: v1beta1.RealmMetadata{
								Name: "test-realm",
							},
						},
						Deleted: []string{"space:space1", "space:space2", "metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted realm "test-realm"`,
		},
		{
			name:        "success with both --force and --cascade flags",
			args:        []string{"test-realm"},
			forceFlag:   true,
			cascadeFlag: true,
			controller: &fakeRealmController{
				deleteRealmFn: func(doc *v1beta1.RealmDoc, force bool, cascade bool) (controller.DeleteRealmResult, error) {
					if doc.Metadata.Name != "test-realm" {
						t.Fatalf("expected name %q, got %q", "test-realm", doc.Metadata.Name)
					}
					if !force {
						t.Fatalf("expected force to be true, got false")
					}
					if !cascade {
						t.Fatalf("expected cascade to be true, got false")
					}
					return controller.DeleteRealmResult{
						RealmDoc: &v1beta1.RealmDoc{
							Metadata: v1beta1.RealmMetadata{
								Name: "test-realm",
							},
						},
						Deleted: []string{"space:space1", "metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted realm "test-realm"`,
		},
		{
			name: "DeleteRealm error propagation",
			args: []string{"test-realm"},
			controller: &fakeRealmController{
				deleteRealmFn: func(_ *v1beta1.RealmDoc, _ bool, _ bool) (controller.DeleteRealmResult, error) {
					return controller.DeleteRealmResult{}, errdefs.ErrDeleteRealm
				},
			},
			expectErr:     true,
			expectErrText: "failed to delete realm",
		},
		{
			name: "DeleteRealm returns realm not found error",
			args: []string{"nonexistent-realm"},
			controller: &fakeRealmController{
				deleteRealmFn: func(_ *v1beta1.RealmDoc, _ bool, _ bool) (controller.DeleteRealmResult, error) {
					return controller.DeleteRealmResult{}, errdefs.ErrRealmNotFound
				},
			},
			expectErr:     true,
			expectErrText: "realm not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			viper.Reset()

			runPath := t.TempDir()
			viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, runPath)
			viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, filepath.Join(runPath, "containerd.sock"))

			cmd := realm.NewRealmCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			var ctx context.Context
			if tt.noLogger {
				ctx = context.Background()
			} else {
				ctx = context.WithValue(context.Background(), types.CtxLogger, testLogger())
			}

			// Inject mock controller via context if needed
			if tt.controller != nil {
				ctx = context.WithValue(ctx, realm.MockControllerKey{}, tt.controller)
			}

			// Set up parent command for persistent flags
			parentCmd := &cobra.Command{Use: "delete"}
			parentCmd.PersistentFlags().Bool("force", false, "")
			parentCmd.PersistentFlags().Bool("cascade", false, "")
			_ = viper.BindPFlag(config.KUKE_DELETE_FORCE.ViperKey, parentCmd.PersistentFlags().Lookup("force"))
			_ = viper.BindPFlag(config.KUKE_DELETE_CASCADE.ViperKey, parentCmd.PersistentFlags().Lookup("cascade"))
			parentCmd.AddCommand(cmd)
			parentCmd.SetContext(ctx)
			parentCmd.SetOut(buf)
			parentCmd.SetErr(buf)

			// Build args with flags
			args := tt.args
			if tt.forceFlag {
				args = append(args, "--force")
			}
			if tt.cascadeFlag {
				args = append(args, "--cascade")
			}

			parentCmd.SetArgs(append([]string{"realm"}, args...))

			err := parentCmd.Execute()

			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.expectErrText != "" && !strings.Contains(err.Error(), tt.expectErrText) {
					t.Fatalf("expected error containing %q, got %v", tt.expectErrText, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectMatch != "" {
				output := buf.String()
				if !strings.Contains(output, tt.expectMatch) {
					t.Fatalf("output %q missing expected match %q", output, tt.expectMatch)
				}
			}
		})
	}
}

func TestNewRealmCmdRunE(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
	})

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		controllerFn   func(doc *v1beta1.RealmDoc, force, cascade bool) (controller.DeleteRealmResult, error)
		wantErr        string
		wantCallDelete bool
		wantOpts       *struct {
			docName string
			force   bool
			cascade bool
		}
		wantOutput []string
	}{
		{
			name: "success: delete realm with default flags",
			args: []string{"test-realm"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				// No flags set, defaults to false
			},
			controllerFn: func(doc *v1beta1.RealmDoc, force, cascade bool) (controller.DeleteRealmResult, error) {
				if doc.Metadata.Name != "test-realm" {
					t.Fatalf("expected name %q, got %q", "test-realm", doc.Metadata.Name)
				}
				if force {
					t.Fatalf("expected force to be false, got true")
				}
				if cascade {
					t.Fatalf("expected cascade to be false, got true")
				}
				return controller.DeleteRealmResult{
					RealmDoc: doc,
					Deleted:  []string{"metadata", "cgroup", "network"},
				}, nil
			},
			wantCallDelete: true,
			wantOpts: &struct {
				docName string
				force   bool
				cascade bool
			}{
				docName: "test-realm",
				force:   false,
				cascade: false,
			},
			wantOutput: []string{`Deleted realm "test-realm"`},
		},
		{
			name: "success: delete realm with force flag",
			args: []string{"test-realm"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				// Set up parent command for persistent flags
				parentCmd := &cobra.Command{Use: "delete"}
				parentCmd.PersistentFlags().Bool("force", false, "")
				parentCmd.PersistentFlags().Bool("cascade", false, "")
				_ = viper.BindPFlag(config.KUKE_DELETE_FORCE.ViperKey, parentCmd.PersistentFlags().Lookup("force"))
				_ = viper.BindPFlag(config.KUKE_DELETE_CASCADE.ViperKey, parentCmd.PersistentFlags().Lookup("cascade"))
				parentCmd.AddCommand(cmd)
				// Set flag on parent's persistent flags (child inherits them)
				if err := parentCmd.PersistentFlags().Set("force", "true"); err != nil {
					t.Fatalf("failed to set force flag: %v", err)
				}
			},
			controllerFn: func(doc *v1beta1.RealmDoc, force, _ bool) (controller.DeleteRealmResult, error) {
				if !force {
					t.Fatalf("expected force to be true, got false")
				}
				return controller.DeleteRealmResult{
					RealmDoc: doc,
					Deleted:  []string{"metadata", "cgroup", "network"},
				}, nil
			},
			wantCallDelete: true,
			wantOpts: &struct {
				docName string
				force   bool
				cascade bool
			}{
				docName: "test-realm",
				force:   true,
				cascade: false,
			},
			wantOutput: []string{`Deleted realm "test-realm"`},
		},
		{
			name: "success: delete realm with cascade flag",
			args: []string{"test-realm"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				// Set up parent command for persistent flags
				parentCmd := &cobra.Command{Use: "delete"}
				parentCmd.PersistentFlags().Bool("force", false, "")
				parentCmd.PersistentFlags().Bool("cascade", false, "")
				_ = viper.BindPFlag(config.KUKE_DELETE_FORCE.ViperKey, parentCmd.PersistentFlags().Lookup("force"))
				_ = viper.BindPFlag(config.KUKE_DELETE_CASCADE.ViperKey, parentCmd.PersistentFlags().Lookup("cascade"))
				parentCmd.AddCommand(cmd)
				// Set flag on parent's persistent flags (child inherits them)
				if err := parentCmd.PersistentFlags().Set("cascade", "true"); err != nil {
					t.Fatalf("failed to set cascade flag: %v", err)
				}
			},
			controllerFn: func(doc *v1beta1.RealmDoc, _, cascade bool) (controller.DeleteRealmResult, error) {
				if !cascade {
					t.Fatalf("expected cascade to be true, got false")
				}
				return controller.DeleteRealmResult{
					RealmDoc: doc,
					Deleted:  []string{"space:space1", "space:space2", "metadata", "cgroup", "network"},
				}, nil
			},
			wantCallDelete: true,
			wantOpts: &struct {
				docName string
				force   bool
				cascade bool
			}{
				docName: "test-realm",
				force:   false,
				cascade: true,
			},
			wantOutput: []string{`Deleted realm "test-realm"`},
		},
		{
			name: "success: name trimming whitespace",
			args: []string{"  test-realm  "},
			setup: func(_ *testing.T, _ *cobra.Command) {
				// No flags set
			},
			controllerFn: func(doc *v1beta1.RealmDoc, _, _ bool) (controller.DeleteRealmResult, error) {
				if doc.Metadata.Name != "test-realm" {
					t.Fatalf("expected trimmed name %q, got %q", "test-realm", doc.Metadata.Name)
				}
				return controller.DeleteRealmResult{
					RealmDoc: doc,
					Deleted:  []string{"metadata", "cgroup", "network"},
				}, nil
			},
			wantCallDelete: true,
			wantOpts: &struct {
				docName string
				force   bool
				cascade bool
			}{
				docName: "test-realm",
				force:   false,
				cascade: false,
			},
			wantOutput: []string{`Deleted realm "test-realm"`},
		},
		{
			name: "error: logger not in context",
			args: []string{"test-realm"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
			},
			controllerFn: func(_ *v1beta1.RealmDoc, _ bool, _ bool) (controller.DeleteRealmResult, error) {
				return controller.DeleteRealmResult{}, errors.New("unexpected call")
			},
			wantErr:        "logger not found",
			wantCallDelete: false,
		},
		{
			name: "error: DeleteRealm fails",
			args: []string{"test-realm"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				// No flags set
			},
			controllerFn: func(_ *v1beta1.RealmDoc, _ bool, _ bool) (controller.DeleteRealmResult, error) {
				return controller.DeleteRealmResult{}, errdefs.ErrDeleteRealm
			},
			wantErr:        "failed to delete realm",
			wantCallDelete: true,
			wantOpts: &struct {
				docName string
				force   bool
				cascade bool
			}{
				docName: "test-realm",
				force:   false,
				cascade: false,
			},
		},
		{
			name: "error: DeleteRealm returns realm not found",
			args: []string{"nonexistent-realm"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				// No flags set
			},
			controllerFn: func(doc *v1beta1.RealmDoc, _ bool, _ bool) (controller.DeleteRealmResult, error) {
				if doc.Metadata.Name != "nonexistent-realm" {
					t.Fatalf("expected name %q, got %q", "nonexistent-realm", doc.Metadata.Name)
				}
				return controller.DeleteRealmResult{}, errdefs.ErrRealmNotFound
			},
			wantErr:        "realm not found",
			wantCallDelete: true,
			wantOpts: &struct {
				docName string
				force   bool
				cascade bool
			}{
				docName: "nonexistent-realm",
				force:   false,
				cascade: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			runPath := t.TempDir()
			viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, runPath)
			viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, filepath.Join(runPath, "containerd.sock"))

			var deleteCalled bool
			var deleteOpts struct {
				doc     *v1beta1.RealmDoc
				force   bool
				cascade bool
			}

			cmd := realm.NewRealmCmd()
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
					fakeCtrl := &fakeRealmController{
						deleteRealmFn: func(doc *v1beta1.RealmDoc, force, cascade bool) (controller.DeleteRealmResult, error) {
							deleteCalled = true
							deleteOpts.doc = doc
							deleteOpts.force = force
							deleteOpts.cascade = cascade
							return tt.controllerFn(doc, force, cascade)
						},
					}
					// Inject mock controller into context
					ctx = context.WithValue(ctx, realm.MockControllerKey{}, fakeCtrl)
				}
			}

			cmd.SetContext(ctx)

			if tt.setup != nil {
				tt.setup(t, cmd)
			}

			// If setup created a parent command, use it for execution
			// Otherwise, create one for persistent flags
			var isParentCmd bool
			var execCmd *cobra.Command
			if cmd.Parent() != nil {
				// Setup created a parent, use it
				execCmd = cmd.Parent()
				execCmd.SetContext(ctx)
				execCmd.SetOut(cmd.OutOrStdout())
				execCmd.SetErr(cmd.ErrOrStderr())
				execCmd.SetArgs(append([]string{"realm"}, tt.args...))
				isParentCmd = true
			} else {
				// Create parent command for persistent flags
				parentCmd := &cobra.Command{Use: "delete"}
				parentCmd.PersistentFlags().Bool("force", false, "")
				parentCmd.PersistentFlags().Bool("cascade", false, "")
				_ = viper.BindPFlag(config.KUKE_DELETE_FORCE.ViperKey, parentCmd.PersistentFlags().Lookup("force"))
				_ = viper.BindPFlag(config.KUKE_DELETE_CASCADE.ViperKey, parentCmd.PersistentFlags().Lookup("cascade"))
				parentCmd.AddCommand(cmd)
				parentCmd.SetContext(ctx)
				parentCmd.SetOut(cmd.OutOrStdout())
				parentCmd.SetErr(cmd.ErrOrStderr())
				parentCmd.SetArgs(append([]string{"realm"}, tt.args...))
				execCmd = parentCmd
				isParentCmd = true
			}

			// Only set args if we didn't create/use a parent command
			if !isParentCmd {
				execCmd = cmd
				execCmd.SetArgs(tt.args)
			}

			err := execCmd.Execute()

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

			if deleteCalled != tt.wantCallDelete {
				t.Errorf("DeleteRealm called=%v want=%v", deleteCalled, tt.wantCallDelete)
			}

			if tt.wantOpts != nil {
				if deleteOpts.doc == nil || deleteOpts.doc.Metadata.Name != tt.wantOpts.docName {
					t.Errorf("DeleteRealm doc name=%v want=%q", deleteOpts.doc, tt.wantOpts.docName)
				}
				if deleteOpts.force != tt.wantOpts.force {
					t.Errorf("DeleteRealm force=%v want=%v", deleteOpts.force, tt.wantOpts.force)
				}
				if deleteOpts.cascade != tt.wantOpts.cascade {
					t.Errorf("DeleteRealm cascade=%v want=%v", deleteOpts.cascade, tt.wantOpts.cascade)
				}
			}

			if tt.wantOutput != nil {
				output := execCmd.OutOrStdout().(*bytes.Buffer).String()
				for _, expected := range tt.wantOutput {
					if !strings.Contains(output, expected) {
						t.Errorf("output missing expected string %q\nGot output:\n%s", expected, output)
					}
				}
			}
		})
	}
}

func TestNewRealmCmd_AutocompleteRegistration(t *testing.T) {
	cmd := realm.NewRealmCmd()

	// Test that ValidArgsFunction is set to CompleteRealmNames
	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be set")
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeRealmController struct {
	deleteRealmFn func(doc *v1beta1.RealmDoc, force, cascade bool) (controller.DeleteRealmResult, error)
}

func (f *fakeRealmController) DeleteRealm(
	doc *v1beta1.RealmDoc,
	force, cascade bool,
) (controller.DeleteRealmResult, error) {
	if f.deleteRealmFn == nil {
		panic("DeleteRealm was called unexpectedly")
	}
	return f.deleteRealmFn(doc, force, cascade)
}
