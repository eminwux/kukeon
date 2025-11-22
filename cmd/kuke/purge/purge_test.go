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

	if cmd.Use != "purge" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "purge")
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

// Helper function to convert bool to string for flag setting.
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
