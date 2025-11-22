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

package shared_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	sharedpkg "github.com/eminwux/kukeon/cmd/kuke/create/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestLoggerFromCmd(t *testing.T) {
	tests := []struct {
		name     string
		setupCtx func(*cobra.Command)
		wantErr  string
		wantOk   bool
	}{
		{
			name: "logger present in context",
			setupCtx: func(cmd *cobra.Command) {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				cmd.SetContext(ctx)
			},
			wantOk: true,
		},
		{
			name: "logger missing from context",
			setupCtx: func(cmd *cobra.Command) {
				cmd.SetContext(context.Background())
			},
			wantErr: errdefs.ErrLoggerNotFound.Error(),
		},
		{
			name: "logger is nil in context",
			setupCtx: func(cmd *cobra.Command) {
				var logger *slog.Logger
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				cmd.SetContext(ctx)
			},
			wantErr: errdefs.ErrLoggerNotFound.Error(),
		},
		{
			name: "wrong type in context",
			setupCtx: func(cmd *cobra.Command) {
				ctx := context.WithValue(context.Background(), types.CtxLogger, "not a logger")
				cmd.SetContext(ctx)
			},
			wantErr: errdefs.ErrLoggerNotFound.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			if tt.setupCtx != nil {
				tt.setupCtx(cmd)
			}

			logger, err := sharedpkg.LoggerFromCmd(cmd)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				if logger != nil {
					t.Errorf("expected nil logger on error, got %v", logger)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if logger == nil {
				t.Fatal("expected logger, got nil")
			}
		})
	}
}

func TestControllerFromCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		setupCtx    func(*cobra.Command)
		viperConfig map[string]string
		wantErr     string
	}{
		{
			name: "success with valid logger and viper config",
			setupCtx: func(cmd *cobra.Command) {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				cmd.SetContext(ctx)
			},
			viperConfig: map[string]string{
				config.KUKEON_ROOT_RUN_PATH.ViperKey:          "/test/run",
				config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey: "/test/socket",
			},
		},
		{
			name: "success with default viper values",
			setupCtx: func(cmd *cobra.Command) {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				cmd.SetContext(ctx)
			},
			viperConfig: map[string]string{},
		},
		{
			name: "error when logger missing",
			setupCtx: func(cmd *cobra.Command) {
				cmd.SetContext(context.Background())
			},
			wantErr: errdefs.ErrLoggerNotFound.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := &cobra.Command{Use: "test"}
			if tt.setupCtx != nil {
				tt.setupCtx(cmd)
			}

			for k, v := range tt.viperConfig {
				viper.Set(k, v)
			}

			ctrl, err := sharedpkg.ControllerFromCmd(cmd)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				if ctrl != nil {
					t.Errorf("expected nil controller on error, got %v", ctrl)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ctrl == nil {
				t.Fatal("expected controller, got nil")
			}
		})
	}
}

func TestRequireNameArg(t *testing.T) {
	tests := []struct {
		name      string
		cmd       *cobra.Command
		args      []string
		resource  string
		wantErr   string
		wantValue string
	}{
		{
			name:      "valid name",
			cmd:       &cobra.Command{Use: "test [name]"},
			args:      []string{"my-resource"},
			resource:  "resource",
			wantValue: "my-resource",
		},
		{
			name:      "name with whitespace",
			cmd:       &cobra.Command{Use: "test [name]"},
			args:      []string{"  my-resource  "},
			resource:  "resource",
			wantValue: "my-resource",
		},
		{
			name:     "empty args",
			cmd:      &cobra.Command{Use: "test [name]"},
			args:     []string{},
			resource: "resource",
			wantErr:  "resource name is required",
		},
		{
			name:     "empty string arg",
			cmd:      &cobra.Command{Use: "test [name]"},
			args:     []string{""},
			resource: "resource",
			wantErr:  "resource name is required",
		},
		{
			name:     "whitespace only arg",
			cmd:      &cobra.Command{Use: "test [name]"},
			args:     []string{"   "},
			resource: "resource",
			wantErr:  "resource name is required",
		},
		{
			name:      "different resource type",
			cmd:       &cobra.Command{Use: "test [container]"},
			args:      []string{"my-container"},
			resource:  "container",
			wantValue: "my-container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := sharedpkg.RequireNameArg(tt.cmd, tt.args, tt.resource)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				if !strings.Contains(err.Error(), tt.cmd.UseLine()) {
					t.Errorf("error should contain usage line %q", tt.cmd.UseLine())
				}
				if value != "" {
					t.Errorf("expected empty value on error, got %q", value)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if value != tt.wantValue {
				t.Errorf("expected value %q, got %q", tt.wantValue, value)
			}
		})
	}
}

func TestRequireNameArgOrDefault(t *testing.T) {
	tests := []struct {
		name      string
		cmd       *cobra.Command
		args      []string
		resource  string
		fallback  string
		wantErr   string
		wantValue string
	}{
		{
			name:      "args provided, fallback ignored",
			cmd:       &cobra.Command{Use: "test [name]"},
			args:      []string{"my-resource"},
			resource:  "resource",
			fallback:  "default-resource",
			wantValue: "my-resource",
		},
		{
			name:      "no args, valid fallback used",
			cmd:       &cobra.Command{Use: "test [name]"},
			args:      []string{},
			resource:  "resource",
			fallback:  "default-resource",
			wantValue: "default-resource",
		},
		{
			name:      "no args, fallback with whitespace trimmed",
			cmd:       &cobra.Command{Use: "test [name]"},
			args:      []string{},
			resource:  "resource",
			fallback:  "  default-resource  ",
			wantValue: "default-resource",
		},
		{
			name:     "no args, empty fallback",
			cmd:      &cobra.Command{Use: "test [name]"},
			args:     []string{},
			resource: "resource",
			fallback: "",
			wantErr:  "resource name is required",
		},
		{
			name:     "no args, whitespace only fallback",
			cmd:      &cobra.Command{Use: "test [name]"},
			args:     []string{},
			resource: "resource",
			fallback: "   ",
			wantErr:  "resource name is required",
		},
		{
			name:      "empty arg provided, valid fallback used",
			cmd:       &cobra.Command{Use: "test [name]"},
			args:      []string{""},
			resource:  "resource",
			fallback:  "default-resource",
			wantValue: "default-resource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := sharedpkg.RequireNameArgOrDefault(tt.cmd, tt.args, tt.resource, tt.fallback)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				if value != "" {
					t.Errorf("expected empty value on error, got %q", value)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if value != tt.wantValue {
				t.Errorf("expected value %q, got %q", tt.wantValue, value)
			}
		})
	}
}

func TestPrintCreationOutcome(t *testing.T) {
	tests := []struct {
		name          string
		label         string
		existsPost    bool
		created       bool
		wantOutput    []string
		notWantOutput []string
	}{
		{
			name:       "resource created",
			label:      "container",
			existsPost: true,
			created:    true,
			wantOutput: []string{
				"  - container: created",
			},
			notWantOutput: []string{
				"already existed",
				"missing",
			},
		},
		{
			name:       "resource already existed",
			label:      "space",
			existsPost: true,
			created:    false,
			wantOutput: []string{
				"  - space: already existed",
			},
			notWantOutput: []string{
				"created",
				"missing",
			},
		},
		{
			name:       "resource missing",
			label:      "stack",
			existsPost: false,
			created:    false,
			wantOutput: []string{
				"  - stack: missing",
			},
			notWantOutput: []string{
				"created",
				"already existed",
			},
		},
		{
			name:       "created takes precedence over exists",
			label:      "cell",
			existsPost: true,
			created:    true,
			wantOutput: []string{
				"  - cell: created",
			},
			notWantOutput: []string{
				"already existed",
				"missing",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			sharedpkg.PrintCreationOutcome(cmd, tt.label, tt.existsPost, tt.created)
			output := buf.String()

			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing expected string %q. Got output: %q", want, output)
				}
			}

			for _, notWant := range tt.notWantOutput {
				if strings.Contains(output, notWant) {
					t.Errorf("output contains unexpected string %q. Got output: %q", notWant, output)
				}
			}
		})
	}
}

// Test helpers

func newOutputCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}
