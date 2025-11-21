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

package version_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/version"
)

func TestNewVersionCmd(t *testing.T) {
	cmd := version.NewVersionCmd()

	if cmd.Use != "version" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "version")
	}

	if cmd.Short != "Print the version number" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Print the version number")
	}

	// Verify command has no flags (simple command)
	if cmd.Flags().HasFlags() {
		t.Error("version command should not have any flags")
	}
}

type fakeVersionProvider struct {
	versionFn func() string
}

func (f *fakeVersionProvider) Version() string {
	if f.versionFn == nil {
		return "test-version"
	}
	return f.versionFn()
}

func TestVersionCmdRun(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		provider     version.VersionProvider
		wantOutput   string
		wantContains string
	}{
		{
			name:         "prints version from config",
			args:         []string{},
			provider:     nil, // Use default config version provider
			wantOutput:   config.Version + "\n",
			wantContains: config.Version,
		},
		{
			name:         "ignores arguments",
			args:         []string{"arg1", "arg2"},
			provider:     nil, // Use default config version provider
			wantOutput:   config.Version + "\n",
			wantContains: config.Version,
		},
		{
			name: "prints mock version",
			args: []string{},
			provider: &fakeVersionProvider{
				versionFn: func() string {
					return "v1.2.3"
				},
			},
			wantOutput:   "v1.2.3\n",
			wantContains: "v1.2.3",
		},
		{
			name: "prints custom mock version",
			args: []string{},
			provider: &fakeVersionProvider{
				versionFn: func() string {
					return "custom-version-123"
				},
			},
			wantOutput:   "custom-version-123\n",
			wantContains: "custom-version-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original stdout
			oldStdout := os.Stdout
			defer func() {
				os.Stdout = oldStdout
			}()

			// Create pipe to capture stdout
			r, w, pipeErr := os.Pipe()
			if pipeErr != nil {
				t.Fatalf("failed to create pipe: %v", pipeErr)
			}
			os.Stdout = w

			// Set up context with mock provider if provided
			ctx := context.Background()
			if tt.provider != nil {
				ctx = context.WithValue(ctx, version.MockVersionProviderKey{}, tt.provider)
			}

			// Execute command in goroutine
			cmd := version.NewVersionCmd()
			cmd.SetContext(ctx)
			cmd.SetArgs(tt.args)
			errChan := make(chan error, 1)
			go func() {
				execErr := cmd.Execute()
				w.Close()
				errChan <- execErr
			}()

			// Read output from pipe
			var got bytes.Buffer
			_, readErr := got.ReadFrom(r)
			if readErr != nil {
				t.Fatalf("failed to read from pipe: %v", readErr)
			}
			r.Close()

			// Wait for command to complete
			execErr := <-errChan
			if execErr != nil {
				t.Fatalf("Execute() error = %v, want nil", execErr)
			}

			output := got.String()
			if output != tt.wantOutput {
				t.Errorf("output mismatch: got %q, want %q", output, tt.wantOutput)
			}

			// Verify it contains the expected version string
			if !strings.Contains(output, tt.wantContains) {
				t.Errorf("output %q does not contain version %q", output, tt.wantContains)
			}
		})
	}
}
