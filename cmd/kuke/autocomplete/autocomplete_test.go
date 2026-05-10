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

package autocomplete_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke"
	"github.com/eminwux/kukeon/cmd/kuke/autocomplete"
	"github.com/eminwux/kukeon/cmd/types"
)

func TestNewAutocompleteCmd(t *testing.T) {
	cmd := autocomplete.NewAutocompleteCmd()

	if cmd.Use != "autocomplete" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "autocomplete")
	}

	if cmd.Short != "Generate shell completion scripts" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Generate shell completion scripts")
	}

	// Verify command has subcommands
	subcommands := cmd.Commands()
	if len(subcommands) != 3 {
		t.Errorf("Expected 3 subcommands, got %d", len(subcommands))
	}

	// Verify subcommands exist
	subcommandNames := make(map[string]bool)
	for _, subcmd := range subcommands {
		subcommandNames[subcmd.Use] = true
	}

	expectedSubcommands := map[string]bool{
		"bash": true,
		"zsh":  true,
		"fish": true,
	}

	for name := range expectedSubcommands {
		if !subcommandNames[name] {
			t.Errorf("Missing expected subcommand: %q", name)
		}
	}
}

func TestAutocompleteBash(t *testing.T) {
	// Create root command for testing
	rootCmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("Failed to create root command: %v", err)
	}

	// Set up context with logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	rootCmd.SetContext(ctx)

	// Get autocomplete command
	autocompleteCmd := autocomplete.NewAutocompleteCmd()
	rootCmd.AddCommand(autocompleteCmd)

	// Capture stdout — the bash RunE writes the generated script there.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	errChan := make(chan error, 1)
	go func() {
		rootCmd.SetArgs([]string{"autocomplete", "bash"})
		errChan <- rootCmd.Execute()
		w.Close()
	}()

	var output bytes.Buffer
	if _, readErr := output.ReadFrom(r); readErr != nil {
		t.Fatalf("Failed to read from pipe: %v", readErr)
	}
	r.Close()
	os.Stdout = oldStdout

	if execErr := <-errChan; execErr != nil {
		t.Fatalf("Execute() error = %v, want nil", execErr)
	}

	// Verify output is the cobra V2 bash completion: it routes every tab
	// through __complete via __kuke_get_completion_results, instead of the
	// V1 __kuke_handle_go_custom_completion dispatcher that ships inlined
	// flag arrays. Guards against a silent revert to V1 (#375).
	outputStr := output.String()
	if !strings.Contains(outputStr, "__kuke_get_completion_results") {
		t.Errorf("bash completion is not cobra V2 (missing __kuke_get_completion_results marker)")
	}
	if strings.Contains(outputStr, "__kuke_handle_go_custom_completion") {
		t.Errorf("bash completion still contains V1 dispatcher __kuke_handle_go_custom_completion")
	}
}

func TestAutocompleteZsh(t *testing.T) {
	// Create root command for testing
	rootCmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("Failed to create root command: %v", err)
	}

	// Set up context with logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	rootCmd.SetContext(ctx)

	// Get autocomplete command
	autocompleteCmd := autocomplete.NewAutocompleteCmd()
	rootCmd.AddCommand(autocompleteCmd)

	// Capture stdout — the zsh RunE writes the generated script there.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	errChan := make(chan error, 1)
	go func() {
		rootCmd.SetArgs([]string{"autocomplete", "zsh"})
		errChan <- rootCmd.Execute()
		w.Close()
	}()

	var output bytes.Buffer
	if _, readErr := output.ReadFrom(r); readErr != nil {
		t.Fatalf("Failed to read from pipe: %v", readErr)
	}
	r.Close()
	os.Stdout = oldStdout

	if execErr := <-errChan; execErr != nil {
		t.Fatalf("Execute() error = %v, want nil", execErr)
	}

	// Guard against silent regression to a code path that returns root help
	// (which contains neither marker) instead of running GenZshCompletion.
	// `#compdef kuke` is the cobra zsh header; `__complete` is the V2
	// request-dispatch verb that proves the script drives `kuke __complete`
	// at runtime — the same hot path the bash V2 test guards.
	outputStr := output.String()
	if !strings.Contains(outputStr, "#compdef kuke") {
		t.Errorf("zsh completion missing #compdef header (got %d bytes)", len(outputStr))
	}
	if !strings.Contains(outputStr, "__complete") {
		t.Errorf("zsh completion missing __complete V2 dispatch verb")
	}
}

func TestAutocompleteFish(t *testing.T) {
	// Create root command for testing
	rootCmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("Failed to create root command: %v", err)
	}

	// Set up context with logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	rootCmd.SetContext(ctx)

	// Get autocomplete command
	autocompleteCmd := autocomplete.NewAutocompleteCmd()
	rootCmd.AddCommand(autocompleteCmd)

	// Capture stdout — the fish RunE writes the generated script there.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	errChan := make(chan error, 1)
	go func() {
		rootCmd.SetArgs([]string{"autocomplete", "fish"})
		errChan <- rootCmd.Execute()
		w.Close()
	}()

	var output bytes.Buffer
	if _, readErr := output.ReadFrom(r); readErr != nil {
		t.Fatalf("Failed to read from pipe: %v", readErr)
	}
	r.Close()
	os.Stdout = oldStdout

	if execErr := <-errChan; execErr != nil {
		t.Fatalf("Execute() error = %v, want nil", execErr)
	}

	// Guard against silent regression to a code path that returns root help
	// (whose body contains the substring `complete` from the autocomplete
	// Short description) instead of running GenFishCompletion. `complete -c
	// kuke` is the per-arg dispatch fish needs; `__kuke_perform_completion`
	// is the function that drives `kuke __complete` from the running shell.
	outputStr := output.String()
	if !strings.Contains(outputStr, "complete -c kuke") {
		t.Errorf("fish completion missing `complete -c kuke` dispatch (got %d bytes)", len(outputStr))
	}
	if !strings.Contains(outputStr, "__kuke_perform_completion") {
		t.Errorf("fish completion missing __kuke_perform_completion dispatcher")
	}
}

func TestAutocompleteCmdHelp(t *testing.T) {
	cmd := autocomplete.NewAutocompleteCmd()

	// Set up output buffers
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	// Set up context
	ctx := context.Background()
	cmd.SetContext(ctx)

	// Execute help
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
}

func TestAutocompleteSubcommands(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantUse   string
		wantShort string
	}{
		{
			name:      "bash subcommand",
			args:      []string{"bash"},
			wantUse:   "bash",
			wantShort: "Generate bash completion script",
		},
		{
			name:      "zsh subcommand",
			args:      []string{"zsh"},
			wantUse:   "zsh",
			wantShort: "Generate zsh completion script",
		},
		{
			name:      "fish subcommand",
			args:      []string{"fish"},
			wantUse:   "fish",
			wantShort: "Generate fish completion script",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := autocomplete.NewAutocompleteCmd()
			subcmd, _, err := cmd.Find(tt.args)
			if err != nil {
				t.Fatalf("Find() error = %v, want nil", err)
			}

			if subcmd.Use != tt.wantUse {
				t.Errorf("Use mismatch: got %q, want %q", subcmd.Use, tt.wantUse)
			}

			if subcmd.Short != tt.wantShort {
				t.Errorf("Short mismatch: got %q, want %q", subcmd.Short, tt.wantShort)
			}
		})
	}
}
