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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	realm "github.com/eminwux/kukeon/cmd/kuke/purge/realm"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewRealmCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := realm.NewRealmCmd()

	if cmd.Use != "realm [name]" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "realm [name]")
	}

	if cmd.Short != "Purge a realm with comprehensive cleanup" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Purge a realm with comprehensive cleanup")
	}

	if !cmd.SilenceUsage {
		t.Error("SilenceUsage should be true")
	}

	if cmd.SilenceErrors {
		t.Error("SilenceErrors should be false")
	}

	// Verify command accepts exactly 1 argument
	if cmd.Args == nil {
		t.Error("Args validator should be set")
	} else {
		// Test with wrong number of args
		err := cmd.Args(cmd, []string{})
		if err == nil {
			t.Error("Expected error for zero args")
		}
		err = cmd.Args(cmd, []string{"realm1", "realm2"})
		if err == nil {
			t.Error("Expected error for two args")
		}
		err = cmd.Args(cmd, []string{"realm1"})
		if err != nil {
			t.Errorf("Unexpected error for one arg: %v", err)
		}
	}
}

func TestNewRealmCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		flags      map[string]string
		setupCtx   func(*cobra.Command)
		controller *fakePurgeRealmController
		wantErr    string
		wantOutput []string
	}{
		{
			name: "empty realm name after trimming",
			args: []string{"   "},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(name string, _ bool, _ bool) (*controller.PurgeRealmResult, error) {
					// Should not reach here due to validation, but if it does, expect empty name
					if name == "" {
						return nil, errdefs.ErrRealmNameRequired
					}
					return nil, errors.New("unexpected call")
				},
			},
			wantErr: "realm name is required",
		},
		{
			name: "controller creation error - missing logger",
			args: []string{"my-realm"},
			setupCtx: func(cmd *cobra.Command) {
				// Don't set logger in context
				cmd.SetContext(context.Background())
			},
			wantErr: "logger not found",
		},
		{
			name: "controller PurgeRealm returns error",
			args: []string{"my-realm"},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(name string, _ bool, _ bool) (*controller.PurgeRealmResult, error) {
					if name != "my-realm" {
						return nil, errors.New("unexpected args")
					}
					return nil, errors.New("realm not found")
				},
			},
			wantErr: "realm not found",
		},
		{
			name: "controller PurgeRealm returns realm not found error",
			args: []string{"nonexistent-realm"},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(_ string, _ bool, _ bool) (*controller.PurgeRealmResult, error) {
					return nil, errors.New("realm nonexistent-realm not found")
				},
			},
			wantErr: "realm nonexistent-realm not found",
		},
		{
			name: "successful purge with no additional resources",
			args: []string{"my-realm"},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(name string, force, cascade bool) (*controller.PurgeRealmResult, error) {
					if name != "my-realm" || force != false || cascade != false {
						return nil, errors.New("unexpected args")
					}
					return &controller.PurgeRealmResult{
						RealmName: "my-realm",
						Deleted:   []string{},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged realm \"my-realm\"",
			},
		},
		{
			name: "successful purge with additional purged resources",
			args: []string{"my-realm"},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(name string, _ bool, _ bool) (*controller.PurgeRealmResult, error) {
					if name != "my-realm" {
						return nil, errors.New("unexpected args")
					}
					return &controller.PurgeRealmResult{
						RealmName: "my-realm",
						Deleted:   []string{"space:test-space"},
						Purged:    []string{"orphaned-containers", "cni-resources", "all-metadata"},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged realm \"my-realm\"",
				"Additional resources purged: [orphaned-containers cni-resources all-metadata]",
			},
		},
		{
			name: "successful purge with force flag",
			args: []string{"my-realm"},
			flags: map[string]string{
				"force": "true",
			},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(name string, force, cascade bool) (*controller.PurgeRealmResult, error) {
					if name != "my-realm" || force != true || cascade != false {
						return nil, errors.New("unexpected args")
					}
					return &controller.PurgeRealmResult{
						RealmName: "my-realm",
						Deleted:   []string{},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged realm \"my-realm\"",
			},
		},
		{
			name: "successful purge with cascade flag",
			args: []string{"my-realm"},
			flags: map[string]string{
				"cascade": "true",
			},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(name string, force, cascade bool) (*controller.PurgeRealmResult, error) {
					if name != "my-realm" || force != false || cascade != true {
						return nil, errors.New("unexpected args")
					}
					return &controller.PurgeRealmResult{
						RealmName: "my-realm",
						Deleted:   []string{"space:test-space"},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged realm \"my-realm\"",
			},
		},
		{
			name: "successful purge with both force and cascade flags",
			args: []string{"my-realm"},
			flags: map[string]string{
				"force":   "true",
				"cascade": "true",
			},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(name string, force, cascade bool) (*controller.PurgeRealmResult, error) {
					if name != "my-realm" || force != true || cascade != true {
						return nil, errors.New("unexpected args")
					}
					return &controller.PurgeRealmResult{
						RealmName: "my-realm",
						Deleted:   []string{"space:test-space"},
						Purged:    []string{"orphaned-containers"},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged realm \"my-realm\"",
				"Additional resources purged: [orphaned-containers]",
			},
		},
		{
			name: "successful purge with trimmed whitespace from realm name",
			args: []string{"  my-realm  "},
			controller: &fakePurgeRealmController{
				purgeRealmFn: func(name string, _ bool, _ bool) (*controller.PurgeRealmResult, error) {
					// Verify that trimming happened
					if name != "my-realm" {
						return nil, errors.New("unexpected trimmed args")
					}
					return &controller.PurgeRealmResult{
						RealmName: "my-realm",
						Deleted:   []string{},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged realm \"my-realm\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := realm.NewRealmCmd()
			var outBuf bytes.Buffer

			// Create parent command with persistent flags (matching purge.go structure)
			parentCmd := &cobra.Command{
				Use: "purge",
			}
			parentCmd.PersistentFlags().Bool("force", false, "")
			parentCmd.PersistentFlags().Bool("cascade", false, "")
			_ = viper.BindPFlag(config.KUKE_PURGE_FORCE.ViperKey, parentCmd.PersistentFlags().Lookup("force"))
			_ = viper.BindPFlag(config.KUKE_PURGE_CASCADE.ViperKey, parentCmd.PersistentFlags().Lookup("cascade"))
			parentCmd.AddCommand(cmd)
			parentCmd.SetOut(&outBuf)
			parentCmd.SetErr(&outBuf)

			// Set up context with logger (unless overridden)
			var ctx context.Context
			if tt.setupCtx != nil {
				tt.setupCtx(cmd)
				ctx = cmd.Context()
			} else {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx = context.WithValue(context.Background(), types.CtxLogger, logger)
			}

			// Inject mock controller via context if needed
			if tt.controller != nil {
				ctx = context.WithValue(ctx, realm.MockControllerKey{}, tt.controller)
			}

			cmd.SetContext(ctx)

			// Set flags on parent's persistent flags
			for name, value := range tt.flags {
				if err := parentCmd.PersistentFlags().Set(name, value); err != nil {
					t.Fatalf("failed to set flag %q: %v", name, err)
				}
			}

			parentCmd.SetArgs(append([]string{"realm"}, tt.args...))
			err := parentCmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(tt.wantOutput) > 0 {
				output := outBuf.String()
				for _, want := range tt.wantOutput {
					if !strings.Contains(output, want) {
						t.Errorf("output missing expected string %q. Got output: %q", want, output)
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

// fakePurgeRealmController provides a mock implementation for testing PurgeRealm.
type fakePurgeRealmController struct {
	purgeRealmFn func(name string, force, cascade bool) (*controller.PurgeRealmResult, error)
}

func (f *fakePurgeRealmController) PurgeRealm(name string, force, cascade bool) (*controller.PurgeRealmResult, error) {
	if f.purgeRealmFn == nil {
		return nil, errors.New("unexpected PurgeRealm call")
	}
	return f.purgeRealmFn(name, force, cascade)
}
