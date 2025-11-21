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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	space "github.com/eminwux/kukeon/cmd/kuke/purge/space"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewSpaceCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := space.NewSpaceCmd()

	if cmd.Use != "space [name]" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "space [name]")
	}

	if cmd.Short != "Purge a space with comprehensive cleanup" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Purge a space with comprehensive cleanup")
	}

	if !cmd.SilenceUsage {
		t.Error("SilenceUsage should be true")
	}

	if cmd.SilenceErrors {
		t.Error("SilenceErrors should be false")
	}

	// Test flags exist
	flags := []struct {
		name     string
		required bool
	}{
		{"realm", true},
	}

	for _, flag := range flags {
		f := cmd.Flags().Lookup(flag.name)
		if f == nil {
			t.Errorf("flag %q not found", flag.name)
			continue
		}
	}

	// Test viper binding
	testCases := []struct {
		name     string
		viperKey string
		value    string
	}{
		{"realm", config.KUKE_PURGE_SPACE_REALM.ViperKey, "test-realm"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			// Create a new command for each test to ensure clean state
			testCmd := space.NewSpaceCmd()
			if err := testCmd.Flags().Set(tc.name, tc.value); err != nil {
				t.Fatalf("failed to set flag: %v", err)
			}
			got := viper.GetString(tc.viperKey)
			if got != tc.value {
				t.Errorf("viper binding mismatch: got %q, want %q", got, tc.value)
			}
		})
	}
}

func TestNewSpaceCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		args        []string
		flags       map[string]string
		viperConfig map[string]string
		setupCtx    func(*cobra.Command)
		controller  *fakePurgeController
		wantErr     string
		wantOutput  []string
	}{
		{
			name:    "missing realm error",
			args:    []string{"my-space"},
			wantErr: "realm name is required",
		},
		{
			name: "empty realm after trimming",
			args: []string{"my-space"},
			flags: map[string]string{
				"realm": "   ",
			},
			wantErr: "realm name is required",
		},
		{
			name: "empty space name after trimming",
			args: []string{"   "},
			flags: map[string]string{
				"realm": "my-realm",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(name, _ string, _ bool, _ bool) (*controller.PurgeSpaceResult, error) {
					// Should not reach here due to validation, but if it does, expect empty name
					if name == "" {
						return nil, errdefs.ErrSpaceNameRequired
					}
					return nil, errors.New("unexpected call")
				},
			},
			wantErr: "space name is required",
		},
		{
			name: "controller creation error - missing logger",
			args: []string{"my-space"},
			flags: map[string]string{
				"realm": "my-realm",
			},
			setupCtx: func(cmd *cobra.Command) {
				// Don't set logger in context
				cmd.SetContext(context.Background())
			},
			wantErr: "logger not found",
		},
		{
			name: "controller PurgeSpace returns error",
			args: []string{"my-space"},
			flags: map[string]string{
				"realm": "my-realm",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(name, realm string, _ bool, _ bool) (*controller.PurgeSpaceResult, error) {
					if name != "my-space" || realm != "my-realm" {
						return nil, errors.New("unexpected args")
					}
					return nil, errors.New("space not found")
				},
			},
			wantErr: "space not found",
		},
		{
			name: "controller PurgeSpace returns space not found error",
			args: []string{"nonexistent-space"},
			flags: map[string]string{
				"realm": "my-realm",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(_ string, _ string, _ bool, _ bool) (*controller.PurgeSpaceResult, error) {
					return nil, errdefs.ErrSpaceNotFound
				},
			},
			wantErr: "space not found",
		},
		{
			name: "successful space purge",
			args: []string{"my-space"},
			flags: map[string]string{
				"realm": "my-realm",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(name, realm string, _ bool, _ bool) (*controller.PurgeSpaceResult, error) {
					if name != "my-space" || realm != "my-realm" {
						return nil, errors.New("unexpected args")
					}
					return &controller.PurgeSpaceResult{
						SpaceName: "my-space",
						RealmName: "my-realm",
						Deleted:   []string{"space"},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged space \"my-space\" from realm \"my-realm\"",
			},
		},
		{
			name: "successful purge with additional resources",
			args: []string{"my-space"},
			flags: map[string]string{
				"realm": "my-realm",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(name, realm string, _ bool, _ bool) (*controller.PurgeSpaceResult, error) {
					if name != "my-space" || realm != "my-realm" {
						return nil, errors.New("unexpected args")
					}
					return &controller.PurgeSpaceResult{
						SpaceName: "my-space",
						RealmName: "my-realm",
						Deleted:   []string{"space"},
						Purged:    []string{"cni-network", "orphaned-containers"},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged space \"my-space\" from realm \"my-realm\"",
				"Additional resources purged: [cni-network orphaned-containers]",
			},
		},
		{
			name: "successful purge with trimmed whitespace in args and flags",
			args: []string{"  my-space  "},
			flags: map[string]string{
				"realm": "  my-realm  ",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(name, realm string, _ bool, _ bool) (*controller.PurgeSpaceResult, error) {
					// Verify that trimming happened
					if name != "my-space" || realm != "my-realm" {
						return nil, errors.New("unexpected trimmed args")
					}
					return &controller.PurgeSpaceResult{
						SpaceName: "my-space",
						RealmName: "my-realm",
						Deleted:   []string{"space"},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged space \"my-space\" from realm \"my-realm\"",
			},
		},
		{
			name: "values from viper config",
			args: []string{"my-space"},
			viperConfig: map[string]string{
				config.KUKE_PURGE_SPACE_REALM.ViperKey: "viper-realm",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(name, realm string, _ bool, _ bool) (*controller.PurgeSpaceResult, error) {
					if name != "my-space" || realm != "viper-realm" {
						return nil, errors.New("unexpected args from viper")
					}
					return &controller.PurgeSpaceResult{
						SpaceName: "my-space",
						RealmName: "viper-realm",
						Deleted:   []string{"space"},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged space \"my-space\" from realm \"viper-realm\"",
			},
		},
		{
			name: "force flag is parsed correctly",
			args: []string{"my-space"},
			flags: map[string]string{
				"realm": "my-realm",
				"force": "true",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(_ string, _ string, force bool, _ bool) (*controller.PurgeSpaceResult, error) {
					if !force {
						return nil, errors.New("force flag not parsed correctly")
					}
					return &controller.PurgeSpaceResult{
						SpaceName: "my-space",
						RealmName: "my-realm",
						Deleted:   []string{"space"},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged space \"my-space\" from realm \"my-realm\"",
			},
		},
		{
			name: "cascade flag is parsed correctly",
			args: []string{"my-space"},
			flags: map[string]string{
				"realm":   "my-realm",
				"cascade": "true",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(_ string, _ string, _ bool, cascade bool) (*controller.PurgeSpaceResult, error) {
					if !cascade {
						return nil, errors.New("cascade flag not parsed correctly")
					}
					return &controller.PurgeSpaceResult{
						SpaceName: "my-space",
						RealmName: "my-realm",
						Deleted:   []string{"space"},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged space \"my-space\" from realm \"my-realm\"",
			},
		},
		{
			name: "both force and cascade flags are parsed correctly",
			args: []string{"my-space"},
			flags: map[string]string{
				"realm":   "my-realm",
				"force":   "true",
				"cascade": "true",
			},
			controller: &fakePurgeController{
				purgeSpaceFn: func(_ string, _ string, force bool, cascade bool) (*controller.PurgeSpaceResult, error) {
					if !force || !cascade {
						return nil, errors.New("flags not parsed correctly")
					}
					return &controller.PurgeSpaceResult{
						SpaceName: "my-space",
						RealmName: "my-realm",
						Deleted:   []string{"space"},
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged space \"my-space\" from realm \"my-realm\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := space.NewSpaceCmd()
			var outBuf bytes.Buffer
			cmd.SetOut(&outBuf)
			cmd.SetErr(&outBuf)

			// Add persistent flags for force and cascade if needed (they're normally on parent command)
			if tt.flags != nil {
				if _, hasForce := tt.flags["force"]; hasForce {
					cmd.PersistentFlags().Bool("force", false, "Skip validation and attempt purge anyway")
					_ = viper.BindPFlag(config.KUKE_PURGE_FORCE.ViperKey, cmd.PersistentFlags().Lookup("force"))
				}
				if _, hasCascade := tt.flags["cascade"]; hasCascade {
					cmd.PersistentFlags().Bool("cascade", false, "Automatically purge child resources recursively")
					_ = viper.BindPFlag(config.KUKE_PURGE_CASCADE.ViperKey, cmd.PersistentFlags().Lookup("cascade"))
				}
			}

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
				ctx = context.WithValue(ctx, space.MockControllerKey{}, tt.controller)
			}

			cmd.SetContext(ctx)

			// Set viper config
			for k, v := range tt.viperConfig {
				viper.Set(k, v)
			}

			// Set flags
			for name, value := range tt.flags {
				// Use PersistentFlags for force and cascade, regular Flags for others
				if name == "force" || name == "cascade" {
					if err := cmd.PersistentFlags().Set(name, value); err != nil {
						t.Fatalf("failed to set persistent flag %q: %v", name, err)
					}
				} else {
					if err := cmd.Flags().Set(name, value); err != nil {
						t.Fatalf("failed to set flag %q: %v", name, err)
					}
				}
			}

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

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

// fakePurgeController provides a mock implementation for testing PurgeSpace.
type fakePurgeController struct {
	purgeSpaceFn func(name, realmName string, force, cascade bool) (*controller.PurgeSpaceResult, error)
}

func (f *fakePurgeController) PurgeSpace(
	name, realmName string,
	force, cascade bool,
) (*controller.PurgeSpaceResult, error) {
	if f.purgeSpaceFn == nil {
		return nil, errors.New("unexpected PurgeSpace call")
	}
	return f.purgeSpaceFn(name, realmName, force, cascade)
}
