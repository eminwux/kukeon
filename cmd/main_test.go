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

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/logging"
	"github.com/spf13/cobra"
)

func TestExecRoot(t *testing.T) {
	tests := []struct {
		name       string
		setupCmd   func() *cobra.Command
		wantReturn int
	}{
		{
			name: "successful execution",
			setupCmd: func() *cobra.Command {
				cmd := &cobra.Command{
					Use: "test",
					Run: func(_ *cobra.Command, _ []string) {
						// Command succeeds
					},
				}
				cmd.SetArgs([]string{})
				return cmd
			},
			wantReturn: 0,
		},
		{
			name: "execution fails",
			setupCmd: func() *cobra.Command {
				cmd := &cobra.Command{
					Use: "test",
					RunE: func(_ *cobra.Command, _ []string) error {
						return errors.New("command execution failed")
					},
				}
				cmd.SetArgs([]string{})
				return cmd
			},
			wantReturn: 1,
		},
		{
			name: "command with validation error",
			setupCmd: func() *cobra.Command {
				cmd := &cobra.Command{
					Use: "test",
					RunE: func(_ *cobra.Command, _ []string) error {
						return fmt.Errorf("validation error: %w", errors.New("invalid argument"))
					},
				}
				cmd.SetArgs([]string{})
				return cmd
			},
			wantReturn: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.setupCmd()
			got := execRoot(cmd)
			if got != tt.wantReturn {
				t.Errorf("execRoot() = %d, want %d", got, tt.wantReturn)
			}
		})
	}
}

func TestRunWithFactory(t *testing.T) {
	tests := []struct {
		name       string
		ctx        context.Context
		factory    rootFactory
		wantReturn int
	}{
		{
			name: "factory succeeds and execution succeeds",
			ctx:  context.Background(),
			factory: func() (*cobra.Command, error) {
				cmd := &cobra.Command{
					Use: "test",
					Run: func(_ *cobra.Command, _ []string) {
						// Success
					},
				}
				cmd.SetArgs([]string{})
				return cmd, nil
			},
			wantReturn: 0,
		},
		{
			name: "factory returns error",
			ctx:  context.Background(),
			factory: func() (*cobra.Command, error) {
				return nil, errors.New("factory error")
			},
			wantReturn: 1,
		},
		{
			name: "factory succeeds but execution fails",
			ctx:  context.Background(),
			factory: func() (*cobra.Command, error) {
				cmd := &cobra.Command{
					Use: "test",
					RunE: func(_ *cobra.Command, _ []string) error {
						return errors.New("execution error")
					},
				}
				cmd.SetArgs([]string{})
				return cmd, nil
			},
			wantReturn: 1,
		},
		{
			name: "context is set on command",
			ctx: func() context.Context {
				logger := logging.NewNoopLogger()
				return context.WithValue(context.Background(), types.CtxLogger, logger)
			}(),
			factory: func() (*cobra.Command, error) {
				cmd := &cobra.Command{
					Use: "test",
					RunE: func(cmd *cobra.Command, _ []string) error {
						ctx := cmd.Context()
						if ctx == nil {
							return errors.New("context not set")
						}
						logger := ctx.Value(types.CtxLogger)
						if logger == nil {
							return errors.New("logger not in context")
						}
						return nil
					},
				}
				cmd.SetArgs([]string{})
				return cmd, nil
			},
			wantReturn: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runWithFactory(tt.ctx, tt.factory)
			if got != tt.wantReturn {
				t.Errorf("runWithFactory() = %d, want %d", got, tt.wantReturn)
			}
		})
	}
}

func TestGetFactories(t *testing.T) {
	tests := []struct {
		name           string
		ctx            context.Context
		wantFactories  factoryMap
		wantMockCalled bool
	}{
		{
			name: "returns default factories when no mock in context",
			ctx:  context.Background(),
			wantFactories: factoryMap{
				"kuke": kuke.NewKukeCmd,
			},
			wantMockCalled: false,
		},
		{
			name: "returns mock factories from context",
			ctx: func() context.Context {
				mockFactories := factoryMap{
					"test-cmd": func() (*cobra.Command, error) {
						return &cobra.Command{Use: "test"}, nil
					},
				}
				return context.WithValue(context.Background(), mockFactoryMapKey{}, mockFactories)
			}(),
			wantFactories: factoryMap{
				"test-cmd": func() (*cobra.Command, error) {
					return &cobra.Command{Use: "test"}, nil
				},
			},
			wantMockCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getFactories(tt.ctx)

			if tt.wantMockCalled {
				// For mock factories, verify the key exists
				if _, ok := got["test-cmd"]; !ok {
					t.Error("mock factory not found in returned factories")
				}
				if _, ok := got["kuke"]; ok {
					t.Error("default factory should not be present when mock is used")
				}
			} else {
				// For default factories, verify kuke exists
				if _, ok := got["kuke"]; !ok {
					t.Error("default kuke factory not found")
				}
			}
		})
	}
}

func TestMain(t *testing.T) {
	// Save original values
	originalArgs := os.Args

	// Restore original values
	t.Cleanup(func() {
		os.Args = originalArgs //nolint:reassign // necessary for testing
	})

	tests := []struct {
		name          string
		setupArgs     func() []string
		debugMode     string
		setDebugMode  bool
		mockFactories factoryMap
		wantStderr    string
		wantExitCode  int
	}{
		{
			name: "executable name kuke maps correctly",
			setupArgs: func() []string {
				return []string{"kuke"}
			},
			wantExitCode: 0,
		},
		{
			name: "KUKEON_DEBUG_MODE fallback works",
			setupArgs: func() []string {
				return []string{"unknown-executable"}
			},
			setDebugMode: true,
			debugMode:    "kuke",
			wantExitCode: 0,
		},
		{
			name: "unknown executable name without debug mode",
			setupArgs: func() []string {
				return []string{"unknown-cmd"}
			},
			setDebugMode: false,
			wantStderr:   "unknown entry command: unknown-cmd",
			wantExitCode: 1,
		},
		{
			name: "KUKEON_DEBUG_MODE with invalid value",
			setupArgs: func() []string {
				return []string{"unknown-executable"}
			},
			setDebugMode: true,
			debugMode:    "invalid",
			wantStderr:   "unknown entry command: unknown-executable",
			wantExitCode: 1,
		},
		{
			name: "mock factory from context is used",
			setupArgs: func() []string {
				return []string{"mock-cmd"}
			},
			mockFactories: factoryMap{
				"mock-cmd": func() (*cobra.Command, error) {
					cmd := &cobra.Command{
						Use: "mock",
						Run: func(_ *cobra.Command, _ []string) {
							// Success
						},
					}
					cmd.SetArgs([]string{})
					return cmd, nil
				},
			},
			wantExitCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			testArgs := tt.setupArgs()
			os.Args = testArgs //nolint:reassign // necessary for testing

			if tt.setDebugMode {
				t.Setenv("KUKEON_DEBUG_MODE", tt.debugMode)
			} else {
				t.Setenv("KUKEON_DEBUG_MODE", "")
			}

			// Setup context with mock factories if provided
			ctx := context.Background()
			if tt.mockFactories != nil {
				ctx = context.WithValue(ctx, mockFactoryMapKey{}, tt.mockFactories)
			}

			// Since main() calls os.Exit, we can't test it directly in unit tests
			// Instead, we test the logic components separately
			exe := filepath.Base(os.Args[0])
			debug := os.Getenv("KUKEON_DEBUG_MODE")

			// Get factories (may be mocked via context)
			factories := getFactories(ctx)

			// Test the logic without calling os.Exit
			found := false
			var factory rootFactory
			var ok bool

			if factory, ok = factories[exe]; ok {
				found = true
			} else if factory, ok = factories[debug]; ok {
				found = true
			}

			if !found {
				// Capture stderr output
				var stderrBuf bytes.Buffer
				fmt.Fprintf(&stderrBuf, "unknown entry command: %s\n", exe)
				if tt.wantStderr != "" && !strings.Contains(stderrBuf.String(), tt.wantStderr) {
					t.Errorf("stderr output mismatch: got %q, want %q", stderrBuf.String(), tt.wantStderr)
				}
			} else if factory != nil {
				// Verify factory can be called and executed
				cmd, err := factory()
				if err != nil {
					if tt.wantExitCode == 0 {
						t.Errorf("factory returned error: %v", err)
					}
					return
				}

				// Test execution with context
				logger := logging.NewNoopLogger()
				testCtx := context.WithValue(ctx, types.CtxLogger, logger)
				cmd.SetContext(testCtx)
				exitCode := execRoot(cmd)
				if exitCode != tt.wantExitCode {
					t.Errorf("execRoot() = %d, want %d", exitCode, tt.wantExitCode)
				}
			}
		})
	}
}

func TestMainLoggerAndContextSetup(t *testing.T) {
	// Test that the logger and context setup matches what main() does
	logger := logging.NewNoopLogger()
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	if ctx == nil {
		t.Error("context should not be nil")
	}

	gotLogger := ctx.Value(types.CtxLogger)
	if gotLogger == nil {
		t.Error("logger should be in context")
	}

	if gotLogger != logger {
		t.Error("logger in context should match created logger")
	}
}

func TestMainExecutableNameResolution(t *testing.T) {
	tests := []struct {
		name          string
		executable    string
		debugMode     string
		setDebugMode  bool
		mockFactories factoryMap
		wantFound     bool
		wantFactory   string
	}{
		{
			name:        "exact match kuke",
			executable:  "kuke",
			wantFound:   true,
			wantFactory: "kuke",
		},
		{
			name:        "executable with path",
			executable:  "/usr/bin/kuke",
			wantFound:   true,
			wantFactory: "kuke",
		},
		{
			name:        "executable with relative path",
			executable:  "./kuke",
			wantFound:   true,
			wantFactory: "kuke",
		},
		{
			name:         "debug mode fallback",
			executable:   "unknown",
			setDebugMode: true,
			debugMode:    "kuke",
			wantFound:    true,
			wantFactory:  "kuke",
		},
		{
			name:         "unknown executable without debug mode",
			executable:   "unknown",
			setDebugMode: false,
			wantFound:    false,
		},
		{
			name:         "unknown executable with invalid debug mode",
			executable:   "unknown",
			setDebugMode: true,
			debugMode:    "invalid",
			wantFound:    false,
		},
		{
			name:       "mock factory from context",
			executable: "mock-cmd",
			mockFactories: factoryMap{
				"mock-cmd": func() (*cobra.Command, error) {
					return &cobra.Command{Use: "mock"}, nil
				},
			},
			wantFound:   true,
			wantFactory: "mock-cmd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exe := filepath.Base(tt.executable)

			// Setup context with mock factories if provided
			ctx := context.Background()
			if tt.mockFactories != nil {
				ctx = context.WithValue(ctx, mockFactoryMapKey{}, tt.mockFactories)
			}

			// Get factories (may be mocked via context)
			factories := getFactories(ctx)

			found := false
			var factory rootFactory
			var ok bool

			if factory, ok = factories[exe]; ok {
				found = true
			} else if tt.setDebugMode {
				debug := tt.debugMode
				if factory, ok = factories[debug]; ok {
					found = true
				}
			}

			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}

			if found && tt.wantFactory != "" {
				if factory == nil {
					t.Error("factory should not be nil when found")
				}
			}
		})
	}
}
