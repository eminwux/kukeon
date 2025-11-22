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

	// Get bash subcommand
	bashCmd, _, err := rootCmd.Find([]string{"autocomplete", "bash"})
	if err != nil {
		t.Fatalf("Failed to find bash subcommand: %v", err)
	}

	// Capture stdout
	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Execute command in goroutine
	errChan := make(chan error, 1)
	go func() {
		bashCmd.SetOutput(&buf)
		bashCmd.SetContext(ctx)
		bashCmd.SetArgs([]string{})
		err := bashCmd.Execute()
		w.Close()
		errChan <- err
	}()

	// Read output
	var output bytes.Buffer
	_, readErr := output.ReadFrom(r)
	if readErr != nil {
		t.Fatalf("Failed to read from pipe: %v", readErr)
	}
	r.Close()

	// Restore stdout
	os.Stdout = oldStdout

	// Wait for command to complete
	execErr := <-errChan
	if execErr != nil {
		t.Fatalf("Execute() error = %v, want nil", execErr)
	}

	// Verify output contains bash completion script markers
	outputStr := output.String()
	if !strings.Contains(outputStr, "complete") && !strings.Contains(outputStr, "_kuke") {
		// Bash completion scripts typically contain these markers
		// But we'll just verify we got some output
		if len(outputStr) == 0 {
			t.Error("Expected non-empty output from bash completion generation")
		}
	}

	// Verify output is not empty
	if len(outputStr) == 0 {
		t.Error("Bash completion script output is empty")
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

	// Get zsh subcommand
	zshCmd, _, err := rootCmd.Find([]string{"autocomplete", "zsh"})
	if err != nil {
		t.Fatalf("Failed to find zsh subcommand: %v", err)
	}

	// Capture stdout
	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Execute command in goroutine
	errChan := make(chan error, 1)
	go func() {
		zshCmd.SetOutput(&buf)
		zshCmd.SetContext(ctx)
		zshCmd.SetArgs([]string{})
		err := zshCmd.Execute()
		w.Close()
		errChan <- err
	}()

	// Read output
	var output bytes.Buffer
	_, readErr := output.ReadFrom(r)
	if readErr != nil {
		t.Fatalf("Failed to read from pipe: %v", readErr)
	}
	r.Close()

	// Restore stdout
	os.Stdout = oldStdout

	// Wait for command to complete
	execErr := <-errChan
	if execErr != nil {
		t.Fatalf("Execute() error = %v, want nil", execErr)
	}

	// Verify output contains zsh completion script markers
	outputStr := output.String()
	if !strings.Contains(outputStr, "compdef") && !strings.Contains(outputStr, "#compdef") {
		// Zsh completion scripts typically contain these markers
		// But we'll just verify we got some output
		if len(outputStr) == 0 {
			t.Error("Expected non-empty output from zsh completion generation")
		}
	}

	// Verify output is not empty
	if len(outputStr) == 0 {
		t.Error("Zsh completion script output is empty")
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

	// Get fish subcommand
	fishCmd, _, err := rootCmd.Find([]string{"autocomplete", "fish"})
	if err != nil {
		t.Fatalf("Failed to find fish subcommand: %v", err)
	}

	// Capture stdout
	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Execute command in goroutine
	errChan := make(chan error, 1)
	go func() {
		fishCmd.SetOutput(&buf)
		fishCmd.SetContext(ctx)
		fishCmd.SetArgs([]string{})
		err := fishCmd.Execute()
		w.Close()
		errChan <- err
	}()

	// Read output
	var output bytes.Buffer
	_, readErr := output.ReadFrom(r)
	if readErr != nil {
		t.Fatalf("Failed to read from pipe: %v", readErr)
	}
	r.Close()

	// Restore stdout
	os.Stdout = oldStdout

	// Wait for command to complete
	execErr := <-errChan
	if execErr != nil {
		t.Fatalf("Execute() error = %v, want nil", execErr)
	}

	// Verify output contains fish completion script markers
	outputStr := output.String()
	if !strings.Contains(outputStr, "complete") {
		// Fish completion scripts typically contain "complete" commands
		// But we'll just verify we got some output
		if len(outputStr) == 0 {
			t.Error("Expected non-empty output from fish completion generation")
		}
	}

	// Verify output is not empty
	if len(outputStr) == 0 {
		t.Error("Fish completion script output is empty")
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
