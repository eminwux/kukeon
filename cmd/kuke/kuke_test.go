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

package kuke_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type fakeConfigLoader struct {
	loadConfigFn func() error
}

func (f *fakeConfigLoader) LoadConfig() error {
	if f.loadConfigFn == nil {
		return nil
	}
	return f.loadConfigFn()
}

func TestNewKukeCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v, want nil", err)
	}

	if cmd.Use != "kuke" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "kuke")
	}

	if cmd.Short != "Kukeon is a tool for managing Kukeon entities" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Kukeon is a tool for managing Kukeon entities")
	}

	expectedSubcommands := []string{"init", "create", "get", "delete", "start", "stop", "kill", "purge", "version"}
	for _, subcmdName := range expectedSubcommands {
		subcmd := cmd.Commands()
		found := false
		for _, c := range subcmd {
			if c.Name() == subcmdName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("subcommand %q not found", subcmdName)
		}
	}
}

func TestNewKukeCmdPersistentPreRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		verbose     bool
		logLevel    string
		loader      kuke.ConfigLoader
		wantErr     bool
		wantErrMsg  string
		checkLogger bool
	}{
		{name: "verbose disabled", verbose: false, checkLogger: false},
		{name: "verbose enabled with default log level", verbose: true, checkLogger: true},
		{name: "verbose enabled with debug log level", verbose: true, logLevel: "debug", checkLogger: true},
		{name: "verbose enabled with info log level", verbose: true, logLevel: "info", checkLogger: true},
		{name: "verbose enabled with warn log level", verbose: true, logLevel: "warn", checkLogger: true},
		{name: "verbose enabled with error log level", verbose: true, logLevel: "error", checkLogger: true},
		{
			name: "config loading error",
			loader: &fakeConfigLoader{
				loadConfigFn: func() error {
					return fmt.Errorf("config error: %w", errdefs.ErrConfig)
				},
			},
			wantErr:    true,
			wantErrMsg: "config error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			viper.Set(config.KUKEON_ROOT_VERBOSE.ViperKey, tt.verbose)
			if tt.logLevel != "" {
				viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, tt.logLevel)
			}

			cmd, err := kuke.NewKukeCmd()
			if err != nil {
				t.Fatalf("NewKukeCmd() error = %v", err)
			}

			ctx := context.Background()
			if tt.loader != nil {
				ctx = context.WithValue(ctx, kuke.MockConfigLoaderKey{}, tt.loader)
			}
			cmd.SetContext(ctx)

			err = cmd.PersistentPreRunE(cmd, []string{})

			if tt.wantErr {
				if err == nil {
					t.Fatalf("PersistentPreRunE() error = nil, want error containing %q", tt.wantErrMsg)
				}
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Fatalf("PersistentPreRunE() error = %q, want error containing %q", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if err != nil {
				t.Fatalf("PersistentPreRunE() error = %v, want nil", err)
			}

			logger := cmd.Context().Value(types.CtxLogger)
			if tt.checkLogger {
				if logger == nil {
					t.Error("logger not found in context when verbose is enabled")
				}
				if _, ok := logger.(*slog.Logger); !ok {
					t.Errorf("logger type mismatch: got %T, want *slog.Logger", logger)
				}
			} else if logger != nil {
				t.Error("logger found in context when verbose is disabled")
			}
		})
	}
}

func TestSetupKukeCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	rootCmd := &cobra.Command{Use: "test"}
	err := kuke.SetupKukeCmd(rootCmd)
	if err != nil {
		t.Fatalf("setupKukeCmd() error = %v, want nil", err)
	}

	expectedSubcommands := []string{"init", "create", "get", "delete", "start", "stop", "kill", "purge", "version"}
	commands := rootCmd.Commands()
	commandMap := make(map[string]bool)
	for _, cmd := range commands {
		commandMap[cmd.Name()] = true
	}

	for _, subcmdName := range expectedSubcommands {
		if !commandMap[subcmdName] {
			t.Errorf("subcommand %q not found", subcmdName)
		}
	}

	persistentFlags := []string{"run-path", "containerd-socket", "verbose", "log-level"}
	for _, flagName := range persistentFlags {
		flag := rootCmd.PersistentFlags().Lookup(flagName)
		if flag == nil {
			t.Errorf("persistent flag %q not found", flagName)
		}
	}

	if rootCmd.PersistentFlags().Lookup("config") != nil {
		t.Errorf("persistent flag %q must not be present (removed in favor of `kukeond --configuration`)", "config")
	}
}

func TestSetPersistentLoggingFlags(t *testing.T) {
	t.Cleanup(viper.Reset)

	rootCmd := &cobra.Command{Use: "test"}
	if err := kuke.SetPersistentLoggingFlags(rootCmd); err != nil {
		t.Fatalf("SetPersistentLoggingFlags() error = %v, want nil", err)
	}

	expectedFlags := []struct {
		name         string
		viperKey     string
		defaultValue string
	}{
		{"run-path", config.KUKEON_ROOT_RUN_PATH.ViperKey, "/opt/kukeon"},
		{
			"containerd-socket",
			config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey,
			"/run/containerd/containerd.sock",
		},
		{"verbose", config.KUKEON_ROOT_VERBOSE.ViperKey, "false"},
		{"log-level", config.KUKEON_ROOT_LOG_LEVEL.ViperKey, ""},
	}

	for _, flag := range expectedFlags {
		f := rootCmd.PersistentFlags().Lookup(flag.name)
		if f == nil {
			t.Errorf("flag %q not found", flag.name)
			continue
		}

		if flag.name == "verbose" {
			if err := rootCmd.PersistentFlags().Set(flag.name, "true"); err != nil {
				t.Fatalf("failed to set flag %q: %v", flag.name, err)
			}
			if !viper.GetBool(flag.viperKey) {
				t.Errorf("viper binding mismatch for %q: got false, want true", flag.name)
			}
		} else {
			testValue := "test-value"
			if err := rootCmd.PersistentFlags().Set(flag.name, testValue); err != nil {
				t.Fatalf("failed to set flag %q: %v", flag.name, err)
			}
			if got := viper.GetString(flag.viperKey); got != testValue {
				t.Errorf("viper binding mismatch for %q: got %q, want %q", flag.name, got, testValue)
			}
		}
	}
}

func TestSetFlags(t *testing.T) {
	t.Cleanup(viper.Reset)

	rootCmd := &cobra.Command{Use: "test"}
	if err := kuke.SetFlags(rootCmd); err != nil {
		t.Errorf("setFlags() error = %v, want nil", err)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	if err := kuke.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	if got := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey); got == "" {
		t.Error("LoadConfig did not set a default run path")
	}
	if got := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey); got == "" {
		t.Error("LoadConfig did not set a default log level")
	}
}

func TestLoadConfigPreservesExplicitValues(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, "/custom/run/path")
	viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, "debug")

	if err := kuke.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	if got := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey); got != "/custom/run/path" {
		t.Errorf("RunPath: got %q, want %q", got, "/custom/run/path")
	}
	if got := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey); got != "debug" {
		t.Errorf("LogLevel: got %q, want %q", got, "debug")
	}
}

func TestNewKukeCmdRun(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	var outBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)

	cmd.SetArgs([]string{})
	cmd.Run(cmd, []string{})

	if !strings.Contains(outBuf.String(), "kuke") {
		t.Errorf("Run() output missing 'kuke'. Got: %q", outBuf.String())
	}
}

func TestPersistentPreRunELoggerContext(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	viper.Set(config.KUKEON_ROOT_VERBOSE.ViperKey, true)
	viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, "debug")

	ctx := context.Background()
	cmd.SetContext(ctx)

	if err = cmd.PersistentPreRunE(cmd, []string{}); err != nil {
		t.Fatalf("PersistentPreRunE() error = %v, want nil", err)
	}

	if logger := cmd.Context().Value(types.CtxLogger); logger == nil {
		t.Fatal("logger not found in context")
	} else if _, ok := logger.(*slog.Logger); !ok {
		t.Errorf("logger type mismatch: got %T, want *slog.Logger", logger)
	}
	if levelVar := cmd.Context().Value(types.CtxLevelVar); levelVar == nil {
		t.Fatal("levelVar not found in context")
	}
	if handler := cmd.Context().Value(types.CtxHandler); handler == nil {
		t.Fatal("handler not found in context")
	}
}

func TestPersistentPreRunEConfigErrorWrapping(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	loader := &fakeConfigLoader{
		loadConfigFn: func() error {
			return fmt.Errorf("synthetic load error: %w", errdefs.ErrConfig)
		},
	}

	ctx := context.WithValue(context.Background(), kuke.MockConfigLoaderKey{}, kuke.ConfigLoader(loader))
	cmd.SetContext(ctx)

	err = cmd.PersistentPreRunE(cmd, []string{})
	if err == nil {
		t.Fatalf("PersistentPreRunE() error = nil, want error wrapping ErrConfig")
	}
	if !errors.Is(err, errdefs.ErrConfig) {
		t.Errorf("PersistentPreRunE() error = %v, want error wrapping %v", err, errdefs.ErrConfig)
	}
}

func TestNewKukeCmdSubcommandStructure(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	commands := cmd.Commands()
	if len(commands) == 0 {
		t.Fatal("no subcommands found")
	}

	for _, subcmd := range commands {
		if subcmd == nil {
			t.Error("found nil subcommand")
			continue
		}
		if subcmd.Use == "" {
			t.Errorf("subcommand %q has empty Use field", subcmd.Name())
		}
	}
}

func TestSetPersistentLoggingFlagsViperBinding(t *testing.T) {
	t.Cleanup(viper.Reset)

	rootCmd := &cobra.Command{Use: "test"}
	if err := kuke.SetPersistentLoggingFlags(rootCmd); err != nil {
		t.Fatalf("SetPersistentLoggingFlags() error = %v, want nil", err)
	}

	testCases := []struct {
		flagName  string
		flagValue string
		viperKey  string
	}{
		{"run-path", "/test/run/path", config.KUKEON_ROOT_RUN_PATH.ViperKey},
		{"containerd-socket", "/test/containerd.sock", config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey},
		{"log-level", "debug", config.KUKEON_ROOT_LOG_LEVEL.ViperKey},
	}

	for _, tc := range testCases {
		t.Run(tc.flagName, func(t *testing.T) {
			if err := rootCmd.PersistentFlags().Set(tc.flagName, tc.flagValue); err != nil {
				t.Fatalf("failed to set flag %q: %v", tc.flagName, err)
			}
			if got := viper.GetString(tc.viperKey); got != tc.flagValue {
				t.Errorf("viper binding mismatch: got %q, want %q", got, tc.flagValue)
			}
		})
	}

	if err := rootCmd.PersistentFlags().Set("verbose", "true"); err != nil {
		t.Fatalf("failed to set verbose flag: %v", err)
	}
	if !viper.GetBool(config.KUKEON_ROOT_VERBOSE.ViperKey) {
		t.Errorf("viper binding mismatch for verbose: got false, want true")
	}
}
