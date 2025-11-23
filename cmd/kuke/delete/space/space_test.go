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

package space_test

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
	space "github.com/eminwux/kukeon/cmd/kuke/delete/space"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Behaviors covered:
// 1. Command structure (Use, Short, Args, SilenceUsage, SilenceErrors)
// 2. Realm flag definition and viper binding
// 3. Args validation (ExactArgs(1))
// 4. Name argument handling (whitespace trimming)
// 5. Realm flag handling (required, whitespace trimming)
// 6. Flag parsing (--force, --cascade, default values)
// 7. Controller creation from command context
// 8. DeleteSpace call with correct parameters
// 9. Error propagation from dependencies
// 10. Success path with result printing
//
// Note: Tests use context-based mocking to inject fake controllers via mockControllerKey.
// This allows tests to exercise the real implementation code path while using mocks.

func TestNewSpaceCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := space.NewSpaceCmd()

	if cmd.Use != "space [name]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "space [name]")
	}
	if cmd.Short != "Delete a space" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Delete a space")
	}
	if !cmd.SilenceUsage {
		t.Error("SilenceUsage = false, want true")
	}
	if cmd.SilenceErrors {
		t.Error("SilenceErrors = true, want false")
	}

	// Check Args validator
	if err := cmd.ValidateArgs([]string{"test"}); err != nil {
		t.Errorf("ValidateArgs with 1 arg failed: %v", err)
	}
	if err := cmd.ValidateArgs([]string{"test", "extra"}); err == nil {
		t.Error("ValidateArgs with 2 args should fail")
	}
	if err := cmd.ValidateArgs([]string{}); err == nil {
		t.Error("ValidateArgs with 0 args should fail")
	}

	// Check realm flag exists and is bound
	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("realm flag not found")
	}
	if realmFlag.Usage != "Realm that owns the space" {
		t.Errorf("realm flag usage = %q, want %q", realmFlag.Usage, "Realm that owns the space")
	}

	// Check viper binding
	if err := cmd.Flags().Set("realm", "test-realm"); err != nil {
		t.Fatalf("failed to set realm flag: %v", err)
	}
	if viper.GetString(config.KUKE_DELETE_SPACE_REALM.ViperKey) != "test-realm" {
		t.Errorf(
			"viper binding failed: got %q, want %q",
			viper.GetString(config.KUKE_DELETE_SPACE_REALM.ViperKey),
			"test-realm",
		)
	}
}

func TestNewSpaceCmd_AutocompleteRegistration(t *testing.T) {
	cmd := space.NewSpaceCmd()

	// Test that realm flag exists and has completion function registered
	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("expected 'realm' flag to exist")
	}
	if realmFlag.Usage != "Realm that owns the space" {
		t.Errorf("unexpected realm flag usage: %q", realmFlag.Usage)
	}

	// Verify flag structure (completion function registration is verified by Cobra)
	// The completion function is registered via RegisterFlagCompletionFunc

	// Test that positional argument has completion function registered
	if cmd.ValidArgsFunction == nil {
		t.Error("expected ValidArgsFunction to be set for positional argument")
	}
}

func TestNewSpaceCmdRunE(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		realmFlag     string
		forceFlag     bool
		cascadeFlag   bool
		controller    space.SpaceController
		expectErr     bool
		expectErrText string
		expectMatch   string
		noLogger      bool
	}{
		{
			name:          "error missing logger in context",
			args:          []string{"test-space"},
			realmFlag:     "test-realm",
			noLogger:      true,
			expectErr:     true,
			expectErrText: "logger not found in context",
		},
		{
			name:          "error missing realm",
			args:          []string{"test-space"},
			expectErr:     true,
			expectErrText: "realm name is required",
		},
		{
			name:          "error empty realm after trimming",
			args:          []string{"test-space"},
			realmFlag:     "   ",
			expectErr:     true,
			expectErrText: "realm name is required",
		},
		{
			name:        "success with name argument and default flags",
			args:        []string{"test-space"},
			realmFlag:   "test-realm",
			forceFlag:   false,
			cascadeFlag: false,
			controller: &fakeSpaceController{
				deleteSpaceFn: func(doc *v1beta1.SpaceDoc, force, cascade bool) (*controller.DeleteSpaceResult, error) {
					if doc == nil {
						t.Fatal("expected space doc, got nil")
					}
					if doc.Metadata.Name != "test-space" {
						t.Fatalf("expected name %q, got %q", "test-space", doc.Metadata.Name)
					}
					if doc.Spec.RealmID != "test-realm" {
						t.Fatalf("expected realm %q, got %q", "test-realm", doc.Spec.RealmID)
					}
					if force {
						t.Fatalf("expected force to be false, got true")
					}
					if cascade {
						t.Fatalf("expected cascade to be false, got true")
					}
					return &controller.DeleteSpaceResult{
						SpaceName: "test-space",
						RealmName: "test-realm",
						Deleted:   []string{"metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted space "test-space" from realm "test-realm"`,
		},
		{
			name:        "name trimming whitespace",
			args:        []string{"  test-space  "},
			realmFlag:   "  test-realm  ",
			forceFlag:   false,
			cascadeFlag: false,
			controller: &fakeSpaceController{
				deleteSpaceFn: func(doc *v1beta1.SpaceDoc, _, _ bool) (*controller.DeleteSpaceResult, error) {
					if doc == nil {
						t.Fatal("expected space doc, got nil")
					}
					if doc.Metadata.Name != "test-space" {
						t.Fatalf("expected trimmed name %q, got %q", "test-space", doc.Metadata.Name)
					}
					if doc.Spec.RealmID != "test-realm" {
						t.Fatalf("expected trimmed realm %q, got %q", "test-realm", doc.Spec.RealmID)
					}
					return &controller.DeleteSpaceResult{
						SpaceName: "test-space",
						RealmName: "test-realm",
						Deleted:   []string{"metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted space "test-space" from realm "test-realm"`,
		},
		{
			name:        "success with --force flag",
			args:        []string{"test-space"},
			realmFlag:   "test-realm",
			forceFlag:   true,
			cascadeFlag: false,
			controller: &fakeSpaceController{
				deleteSpaceFn: func(_ *v1beta1.SpaceDoc, force, cascade bool) (*controller.DeleteSpaceResult, error) {
					if !force {
						t.Fatalf("expected force to be true, got false")
					}
					if cascade {
						t.Fatalf("expected cascade to be false, got true")
					}
					return &controller.DeleteSpaceResult{
						SpaceName: "test-space",
						RealmName: "test-realm",
						Deleted:   []string{"metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted space "test-space" from realm "test-realm"`,
		},
		{
			name:        "success with --cascade flag",
			args:        []string{"test-space"},
			realmFlag:   "test-realm",
			forceFlag:   false,
			cascadeFlag: true,
			controller: &fakeSpaceController{
				deleteSpaceFn: func(_ *v1beta1.SpaceDoc, force, cascade bool) (*controller.DeleteSpaceResult, error) {
					if force {
						t.Fatalf("expected force to be false, got true")
					}
					if !cascade {
						t.Fatalf("expected cascade to be true, got false")
					}
					return &controller.DeleteSpaceResult{
						SpaceName: "test-space",
						RealmName: "test-realm",
						Deleted:   []string{"stack:stack1", "stack:stack2", "metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted space "test-space" from realm "test-realm"`,
		},
		{
			name:        "success with both --force and --cascade flags",
			args:        []string{"test-space"},
			realmFlag:   "test-realm",
			forceFlag:   true,
			cascadeFlag: true,
			controller: &fakeSpaceController{
				deleteSpaceFn: func(_ *v1beta1.SpaceDoc, force, cascade bool) (*controller.DeleteSpaceResult, error) {
					if !force {
						t.Fatalf("expected force to be true, got false")
					}
					if !cascade {
						t.Fatalf("expected cascade to be true, got false")
					}
					return &controller.DeleteSpaceResult{
						SpaceName: "test-space",
						RealmName: "test-realm",
						Deleted:   []string{"stack:stack1", "metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted space "test-space" from realm "test-realm"`,
		},
		{
			name:      "DeleteSpace error propagation",
			args:      []string{"test-space"},
			realmFlag: "test-realm",
			controller: &fakeSpaceController{
				deleteSpaceFn: func(_ *v1beta1.SpaceDoc, _, _ bool) (*controller.DeleteSpaceResult, error) {
					return nil, errors.New("space deletion failed")
				},
			},
			expectErr:     true,
			expectErrText: "space deletion failed",
		},
		{
			name:      "DeleteSpace returns space not found error",
			args:      []string{"nonexistent-space"},
			realmFlag: "test-realm",
			controller: &fakeSpaceController{
				deleteSpaceFn: func(_ *v1beta1.SpaceDoc, _, _ bool) (*controller.DeleteSpaceResult, error) {
					return nil, errdefs.ErrSpaceNotFound
				},
			},
			expectErr:     true,
			expectErrText: "space not found",
		},
		{
			name:      "DeleteSpace returns general delete error",
			args:      []string{"test-space"},
			realmFlag: "test-realm",
			controller: &fakeSpaceController{
				deleteSpaceFn: func(_ *v1beta1.SpaceDoc, _, _ bool) (*controller.DeleteSpaceResult, error) {
					return nil, errdefs.ErrDeleteSpace
				},
			},
			expectErr:     true,
			expectErrText: "failed to delete space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			viper.Reset()

			runPath := t.TempDir()
			viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, runPath)
			viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, filepath.Join(runPath, "containerd.sock"))

			cmd := space.NewSpaceCmd()
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
				ctx = context.WithValue(ctx, space.MockControllerKey{}, tt.controller)
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

			if tt.realmFlag != "" {
				if err := cmd.Flags().Set("realm", tt.realmFlag); err != nil {
					t.Fatalf("failed to set realm flag: %v", err)
				}
			}

			// Build args with flags
			args := tt.args
			if tt.forceFlag {
				args = append(args, "--force")
			}
			if tt.cascadeFlag {
				args = append(args, "--cascade")
			}

			parentCmd.SetArgs(append([]string{"space"}, args...))
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

type fakeSpaceController struct {
	deleteSpaceFn func(doc *v1beta1.SpaceDoc, force, cascade bool) (*controller.DeleteSpaceResult, error)
}

func (f *fakeSpaceController) DeleteSpace(
	doc *v1beta1.SpaceDoc,
	force, cascade bool,
) (*controller.DeleteSpaceResult, error) {
	if f.deleteSpaceFn == nil {
		panic("DeleteSpace was called unexpectedly")
	}
	return f.deleteSpaceFn(doc, force, cascade)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
