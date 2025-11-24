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

package deletecmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	deletecmd "github.com/eminwux/kukeon/cmd/kuke/delete"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewDeleteCmdMetadata(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name: "use statement",
			check: func(t *testing.T, cmd *cobra.Command) {
				if cmd.Use != "delete [name]" {
					t.Fatalf("expected Use to be %q, got %q", "delete", cmd.Use)
				}
			},
		},
		{
			name: "short description",
			check: func(t *testing.T, cmd *cobra.Command) {
				expected := "Delete Kukeon resources (realm, space, stack, cell, container)"
				if cmd.Short != expected {
					t.Fatalf("expected Short to be %q, got %q", expected, cmd.Short)
				}
			},
		},
		{
			name: "run invokes help",
			check: func(t *testing.T, cmd *cobra.Command) {
				buf := &bytes.Buffer{}
				cmd.SetOut(buf)
				cmd.SetErr(buf)

				cmd.Run(cmd, nil)

				output := buf.String()
				if !strings.Contains(output, "Usage:") {
					t.Fatalf("expected help output to contain %q, got %q", "Usage:", output)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := deletecmd.NewDeleteCmd()
			tt.check(t, cmd)
		})
	}
}

func TestNewDeleteCmdPersistentFlags(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		flagName    string
		defaultVal  bool
		description string
		viperKey    string
	}{
		{
			name:        "cascade flag",
			flagName:    "cascade",
			defaultVal:  false,
			description: "Automatically delete child resources recursively (does not apply to containers)",
			viperKey:    config.KUKE_DELETE_CASCADE.ViperKey,
		},
		{
			name:        "force flag",
			flagName:    "force",
			defaultVal:  false,
			description: "Skip validation and attempt deletion anyway",
			viperKey:    config.KUKE_DELETE_FORCE.ViperKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := deletecmd.NewDeleteCmd()

			flag := cmd.PersistentFlags().Lookup(tt.flagName)
			if flag == nil {
				t.Fatalf("expected persistent flag %q to be registered", tt.flagName)
			}

			if flag.Usage != tt.description {
				t.Fatalf("expected flag %q description to be %q, got %q", tt.flagName, tt.description, flag.Usage)
			}

			// Check default value
			val, err := cmd.PersistentFlags().GetBool(tt.flagName)
			if err != nil {
				t.Fatalf("failed to get flag %q: %v", tt.flagName, err)
			}
			if val != tt.defaultVal {
				t.Fatalf("expected flag %q default to be %v, got %v", tt.flagName, tt.defaultVal, val)
			}
		})
	}
}

func TestNewDeleteCmdViperBindings(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name     string
		flagName string
		viperKey string
		value    bool
	}{
		{
			name:     "cascade flag binding",
			flagName: "cascade",
			viperKey: config.KUKE_DELETE_CASCADE.ViperKey,
			value:    true,
		},
		{
			name:     "force flag binding",
			flagName: "force",
			viperKey: config.KUKE_DELETE_FORCE.ViperKey,
			value:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := deletecmd.NewDeleteCmd()

			if err := cmd.PersistentFlags().Set(tt.flagName, "true"); err != nil {
				t.Fatalf("failed to set flag %q: %v", tt.flagName, err)
			}

			got := viper.GetBool(tt.viperKey)
			if got != tt.value {
				t.Fatalf("expected viper key %q to be %v, got %v", tt.viperKey, tt.value, got)
			}
		})
	}
}

func TestNewDeleteCmdRegistersSubcommands(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "realm"},
		{name: "space"},
		{name: "stack"},
		{name: "cell"},
		{name: "container"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := deletecmd.NewDeleteCmd()
			if findSubCommand(cmd, tt.name) == nil {
				t.Fatalf("expected %q subcommand to be registered", tt.name)
			}
		})
	}
}

func TestNewDeleteCmd_AutocompleteRegistration(t *testing.T) {
	cmd := deletecmd.NewDeleteCmd()

	// Verify that ValidArgsFunction is registered (completion function registration is verified by Cobra)
	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be registered")
	}

	// Test the completion function directly
	completions, _ := cmd.ValidArgsFunction(cmd, []string{}, "")
	expected := []string{"realm", "space", "stack", "cell", "container"}
	if len(completions) != len(expected) {
		t.Fatalf("expected %d completions, got %d", len(expected), len(completions))
	}

	for _, exp := range expected {
		found := false
		for _, comp := range completions {
			if comp == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected completion %q not found", exp)
		}
	}

	// Test prefix filtering
	filtered, _ := cmd.ValidArgsFunction(cmd, []string{}, "c")
	expectedFiltered := []string{"cell", "container"}
	if len(filtered) != len(expectedFiltered) {
		t.Fatalf("expected %d filtered completions, got %d", len(expectedFiltered), len(filtered))
	}

	for _, exp := range expectedFiltered {
		found := false
		for _, comp := range filtered {
			if comp == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected filtered completion %q not found", exp)
		}
	}
}

func findSubCommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, sc := range cmd.Commands() {
		if sc.Name() == name || sc.HasAlias(name) {
			return sc
		}
	}
	return nil
}
