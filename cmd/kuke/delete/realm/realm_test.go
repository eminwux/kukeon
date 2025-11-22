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
				deleteRealmFn: func(name string, force, cascade bool) (*controller.DeleteRealmResult, error) {
					if name != "test-realm" {
						t.Fatalf("expected name %q, got %q", "test-realm", name)
					}
					if force {
						t.Fatalf("expected force to be false, got true")
					}
					if cascade {
						t.Fatalf("expected cascade to be false, got true")
					}
					return &controller.DeleteRealmResult{
						RealmName: "test-realm",
						Deleted:   []string{"metadata", "cgroup", "network"},
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
				deleteRealmFn: func(name string, _ bool, _ bool) (*controller.DeleteRealmResult, error) {
					if name != "test-realm" {
						t.Fatalf("expected trimmed name %q, got %q", "test-realm", name)
					}
					return &controller.DeleteRealmResult{
						RealmName: "test-realm",
						Deleted:   []string{"metadata", "cgroup", "network"},
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
				deleteRealmFn: func(_ string, force bool, cascade bool) (*controller.DeleteRealmResult, error) {
					if !force {
						t.Fatalf("expected force to be true, got false")
					}
					if cascade {
						t.Fatalf("expected cascade to be false, got true")
					}
					return &controller.DeleteRealmResult{
						RealmName: "test-realm",
						Deleted:   []string{"metadata", "cgroup", "network"},
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
				deleteRealmFn: func(_ string, force bool, cascade bool) (*controller.DeleteRealmResult, error) {
					if force {
						t.Fatalf("expected force to be false, got true")
					}
					if !cascade {
						t.Fatalf("expected cascade to be true, got false")
					}
					return &controller.DeleteRealmResult{
						RealmName: "test-realm",
						Deleted:   []string{"space:space1", "space:space2", "metadata", "cgroup", "network"},
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
				deleteRealmFn: func(_ string, force bool, cascade bool) (*controller.DeleteRealmResult, error) {
					if !force {
						t.Fatalf("expected force to be true, got false")
					}
					if !cascade {
						t.Fatalf("expected cascade to be true, got false")
					}
					return &controller.DeleteRealmResult{
						RealmName: "test-realm",
						Deleted:   []string{"space:space1", "metadata", "cgroup", "network"},
					}, nil
				},
			},
			expectMatch: `Deleted realm "test-realm"`,
		},
		{
			name: "DeleteRealm error propagation",
			args: []string{"test-realm"},
			controller: &fakeRealmController{
				deleteRealmFn: func(_ string, _ bool, _ bool) (*controller.DeleteRealmResult, error) {
					return nil, errdefs.ErrDeleteRealm
				},
			},
			expectErr:     true,
			expectErrText: "failed to delete realm",
		},
		{
			name: "DeleteRealm returns realm not found error",
			args: []string{"nonexistent-realm"},
			controller: &fakeRealmController{
				deleteRealmFn: func(_ string, _ bool, _ bool) (*controller.DeleteRealmResult, error) {
					return nil, errdefs.ErrRealmNotFound
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

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeRealmController struct {
	deleteRealmFn func(name string, force, cascade bool) (*controller.DeleteRealmResult, error)
}

func (f *fakeRealmController) DeleteRealm(name string, force, cascade bool) (*controller.DeleteRealmResult, error) {
	if f.deleteRealmFn == nil {
		panic("DeleteRealm was called unexpectedly")
	}
	return f.deleteRealmFn(name, force, cascade)
}
