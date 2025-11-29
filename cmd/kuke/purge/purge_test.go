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

package purge_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/purge"
	"github.com/spf13/viper"
)

func TestNewPurgeCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := purge.NewPurgeCmd()

	if cmd.Use != "purge [name]" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "purge [name]")
	}

	expectedShort := "Purge Kukeon resources with comprehensive cleanup (realm, space, stack, cell, container)"
	if cmd.Short != expectedShort {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, expectedShort)
	}

	// Test persistent flags exist
	flags := []struct {
		name     string
		required bool
	}{
		{"cascade", false},
		{"force", false},
	}

	for _, flag := range flags {
		f := cmd.PersistentFlags().Lookup(flag.name)
		if f == nil {
			t.Errorf("persistent flag %q not found", flag.name)
			continue
		}
		if f.Shorthand != "" {
			t.Errorf("persistent flag %q should not have shorthand", flag.name)
		}
	}

	// Test viper binding
	testCases := []struct {
		name     string
		viperKey string
		value    bool
	}{
		{"cascade", config.KUKE_PURGE_CASCADE.ViperKey, true},
		{"cascade", config.KUKE_PURGE_CASCADE.ViperKey, false},
		{"force", config.KUKE_PURGE_FORCE.ViperKey, true},
		{"force", config.KUKE_PURGE_FORCE.ViperKey, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name+"_"+strings.ToLower(tc.viperKey), func(t *testing.T) {
			viper.Reset()
			testCmd := purge.NewPurgeCmd()
			if err := testCmd.PersistentFlags().Set(tc.name, boolToString(tc.value)); err != nil {
				t.Fatalf("failed to set flag: %v", err)
			}
			got := viper.GetBool(tc.viperKey)
			if got != tc.value {
				t.Errorf("viper binding mismatch: got %v, want %v", got, tc.value)
			}
		})
	}

	// Test subcommands are registered
	expectedSubcommands := []string{"realm", "space", "stack", "cell", "container"}
	if len(cmd.Commands()) != len(expectedSubcommands) {
		t.Errorf("subcommand count mismatch: got %d, want %d", len(cmd.Commands()), len(expectedSubcommands))
	}

	subcommandNames := make(map[string]bool)
	for _, subcmd := range cmd.Commands() {
		subcommandNames[subcmd.Name()] = true
	}

	for _, expectedName := range expectedSubcommands {
		if !subcommandNames[expectedName] {
			t.Errorf("subcommand %q not found", expectedName)
		}
	}
}

func TestNewPurgeCmdRun(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := purge.NewPurgeCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := outBuf.String()
	// Help output should contain the command name and description
	wantStrings := []string{
		"purge",
		"Purge Kukeon resources",
	}

	for _, want := range wantStrings {
		if !strings.Contains(output, want) {
			t.Errorf("help output missing expected string %q. Got output: %q", want, output)
		}
	}

	// Verify subcommands are listed in help
	expectedSubcommands := []string{"realm", "space", "stack", "cell", "container"}
	for _, subcmd := range expectedSubcommands {
		if !strings.Contains(output, subcmd) {
			t.Errorf("help output missing subcommand %q. Got output: %q", subcmd, output)
		}
	}
}

func TestNewPurgeCmd_AutocompleteRegistration(t *testing.T) {
	cmd := purge.NewPurgeCmd()

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

func TestNewPurgeCmdPersistentFlags(t *testing.T) {
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
			description: "Automatically purge child resources recursively (does not apply to containers)",
			viperKey:    config.KUKE_PURGE_CASCADE.ViperKey,
		},
		{
			name:        "force flag",
			flagName:    "force",
			defaultVal:  false,
			description: "Skip validation and attempt purge anyway",
			viperKey:    config.KUKE_PURGE_FORCE.ViperKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := purge.NewPurgeCmd()

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

func TestNewPurgeCmdViperBindings(t *testing.T) {
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
			viperKey: config.KUKE_PURGE_CASCADE.ViperKey,
			value:    true,
		},
		{
			name:     "force flag binding",
			flagName: "force",
			viperKey: config.KUKE_PURGE_FORCE.ViperKey,
			value:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := purge.NewPurgeCmd()

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

// Helper function to convert bool to string for flag setting.
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
