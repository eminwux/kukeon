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
	"os"
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

	expectedSubcommands := []string{"init", "create", "get", "delete", "start", "stop", "restart", "kill", "purge", "version"}
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

	expectedSubcommands := []string{"init", "create", "get", "delete", "start", "stop", "restart", "kill", "purge", "version"}
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

// TestLoadConfigBindsKukeondSocketEnv locks down the env binding for
// KUKEOND_SOCKET / KUKEOND_SOCKET_GID added to mirror the daemon-side
// binds (cmd/kukeond/kukeond.go:bindEnvVars). Without these, `kuke init`'s
// applyServerConfiguration env-gate at cmd/kuke/init/init.go:216 sees the
// env var, skips the YAML write, but viper has no binding to read it back
// — so the resolved socket path silently falls through to the default and
// `kuke daemon reset`'s resolveSocketDir cleans the wrong directory.
func TestLoadConfigBindsKukeondSocketEnv(t *testing.T) {
	t.Cleanup(viper.Reset)
	t.Setenv(config.KUKEOND_SOCKET.EnvVar(), "/run/kukeon-dev/kukeond.sock")
	t.Setenv(config.KUKEOND_SOCKET_GID.EnvVar(), "1234")

	viper.Reset()
	if err := kuke.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	if got := viper.GetString(config.KUKEOND_SOCKET.ViperKey); got != "/run/kukeon-dev/kukeond.sock" {
		t.Errorf("KUKEOND_SOCKET: got %q, want %q", got, "/run/kukeon-dev/kukeond.sock")
	}
	if got := viper.GetString(config.KUKEOND_SOCKET_GID.ViperKey); got != "1234" {
		t.Errorf("KUKEOND_SOCKET_GID: got %q, want %q", got, "1234")
	}
}

// TestLoadConfigBindsKukeConfigurationEnv locks down the env binding for
// KUKE_CONFIGURATION (#1330). Before the binding, the env var never reached
// viper — only the `--configuration` flag bound the key — so env-delivered
// client config (how scripts/dev-init.sh delivers the dev profile for the
// `--no-daemon` parity walk) was silently ignored and loadClientConfiguration
// fell through to the flag default (~/.kuke/kuke.yaml).
func TestLoadConfigBindsKukeConfigurationEnv(t *testing.T) {
	t.Cleanup(viper.Reset)
	t.Setenv(config.KUKE_CONFIGURATION.EnvVar(), "/tmp/dev-profile.yaml")

	viper.Reset()
	if err := kuke.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	if got := viper.GetString(config.KUKE_CONFIGURATION.ViperKey); got != "/tmp/dev-profile.yaml" {
		t.Errorf("KUKE_CONFIGURATION: got %q, want %q", got, "/tmp/dev-profile.yaml")
	}
}

// TestLoadConfigKukeConfigurationFlagBeatsEnv pins the precedence the #1330
// fix relies on: an explicit `--configuration` flag (pflag Changed) overrides
// the KUKE_CONFIGURATION env var, which in turn overrides the flag default.
// Both BindPFlag (SetPersistentLoggingFlags) and BindEnv (loadConfig) target
// the same viper key, so this guards viper's flag > env ordering from
// regressing if either binding moves.
func TestLoadConfigKukeConfigurationFlagBeatsEnv(t *testing.T) {
	t.Cleanup(viper.Reset)
	t.Setenv(config.KUKE_CONFIGURATION.EnvVar(), "/tmp/from-env.yaml")

	viper.Reset()
	rootCmd := &cobra.Command{Use: "test"}
	if err := kuke.SetPersistentLoggingFlags(rootCmd); err != nil {
		t.Fatalf("SetPersistentLoggingFlags() error = %v, want nil", err)
	}
	if err := kuke.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	// Env wins while the flag is unchanged.
	if got := viper.GetString(config.KUKE_CONFIGURATION.ViperKey); got != "/tmp/from-env.yaml" {
		t.Errorf("env precedence: got %q, want %q", got, "/tmp/from-env.yaml")
	}

	// An explicit flag overrides the env var.
	if err := rootCmd.PersistentFlags().Set("configuration", "/tmp/from-flag.yaml"); err != nil {
		t.Fatalf("failed to set configuration flag: %v", err)
	}
	if got := viper.GetString(config.KUKE_CONFIGURATION.ViperKey); got != "/tmp/from-flag.yaml" {
		t.Errorf("flag precedence: got %q, want %q", got, "/tmp/from-flag.yaml")
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

// TestNewKukeCmdStreams guards against cobra's OutOrStderr default — if
// SetOut/SetErr are dropped from NewKukeCmd, every cmd.Print* call in the
// subcommand tree silently routes to stderr. Issue #436.
func TestNewKukeCmdStreams(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, err := kuke.NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v, want nil", err)
	}

	if got := cmd.OutOrStdout(); got != os.Stdout {
		t.Errorf("OutOrStdout() not wired to os.Stdout: got %T", got)
	}
	if got := cmd.ErrOrStderr(); got != os.Stderr {
		t.Errorf("ErrOrStderr() not wired to os.Stderr: got %T", got)
	}
}

// TestRunPathImpliesNoDaemon locks down the issue #554 fix: explicit
// `--run-path` (flag or env) promotes `--no-daemon` to true, but only when
// `--no-daemon` itself was not set. The daemon ignores the client's
// run-path, so a caller passing `--run-path` and reaching a daemon dial
// would silently read/write the daemon's path instead — the failure mode
// that broke 40/75 e2e tests under per-test `--run-path` isolation.
//
// `--no-daemon` is no longer a root-persistent flag (#222) — it only lives
// on the retained commands (init, uninstall, purge, every get <kind>). The
// flag-set-to-false case drives `kuke init` because that's a representative
// leaf with the local `--no-daemon` flag; the env-set-to-false case stays
// on the root since the envSet check in applyRunPathImpliesNoDaemon reads
// os.LookupEnv directly and doesn't depend on a cobra flag instance.
func TestRunPathImpliesNoDaemon(t *testing.T) {
	tests := []struct {
		name            string
		setFlag         bool   // --run-path on the command line
		setEnv          string // KUKEON_RUN_PATH in env ("" = unset)
		setNoDaemonFlag bool   // --no-daemon on the leaf cmd (=false)
		setNoDaemonEnv  string // KUKEON_NO_DAEMON in env ("" = unset)
		wantNoDaemon    bool
	}{
		{
			name:         "neither set leaves no-daemon at default",
			wantNoDaemon: false,
		},
		{
			name:         "run-path flag promotes no-daemon",
			setFlag:      true,
			wantNoDaemon: true,
		},
		{
			name:         "run-path env promotes no-daemon",
			setEnv:       "/tmp/foo",
			wantNoDaemon: true,
		},
		{
			name:            "explicit --no-daemon=false on leaf blocks promotion",
			setFlag:         true,
			setNoDaemonFlag: true,
			wantNoDaemon:    false,
		},
		{
			name:           "KUKEON_NO_DAEMON=false blocks promotion",
			setFlag:        true,
			setNoDaemonEnv: "false",
			wantNoDaemon:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			viper.Reset()

			if tc.setEnv != "" {
				t.Setenv(config.KUKEON_ROOT_RUN_PATH.EnvVar(), tc.setEnv)
			}
			if tc.setNoDaemonEnv != "" {
				t.Setenv(config.KUKEON_ROOT_NO_DAEMON.EnvVar(), tc.setNoDaemonEnv)
			}

			cmd, err := kuke.NewKukeCmd()
			if err != nil {
				t.Fatalf("NewKukeCmd() error = %v", err)
			}

			// Find the leaf, then call ParseFlags so cobra merges the
			// inherited persistent flag set (--run-path lives on root)
			// into leaf.Flags(). Without the merge, neither
			// flagChanged nor rebindNoDaemonViperToLeaf sees --run-path
			// via cmd.Flags().Lookup, and the env case is the only
			// path that can be exercised.
			leaf, _, findErr := cmd.Find([]string{"init"})
			if findErr != nil {
				t.Fatalf("find init subcommand: %v", findErr)
			}
			if parseErr := leaf.ParseFlags(nil); parseErr != nil {
				t.Fatalf("parse leaf flags: %v", parseErr)
			}

			if tc.setFlag {
				if setErr := leaf.Flags().Set("run-path", "/tmp/from-flag"); setErr != nil {
					t.Fatalf("set --run-path on leaf: %v", setErr)
				}
			}
			if tc.setNoDaemonFlag {
				if setErr := leaf.Flags().Set("no-daemon", "false"); setErr != nil {
					t.Fatalf("set --no-daemon on leaf: %v", setErr)
				}
			}

			leaf.SetContext(context.Background())
			if preErr := cmd.PersistentPreRunE(leaf, []string{}); preErr != nil {
				t.Fatalf("PersistentPreRunE: %v", preErr)
			}

			got := viper.GetBool(config.KUKEON_ROOT_NO_DAEMON.ViperKey)
			if got != tc.wantNoDaemon {
				t.Errorf("KUKEON_NO_DAEMON: got %v, want %v", got, tc.wantNoDaemon)
			}
		})
	}
}

// TestRunPathImpliesKukeondSocket locks down the issue #570 fix: explicit
// `--run-path` (flag or env) auto-derives KUKEOND_SOCKET = `<X>/kukeond.sock`
// for every kuke subcommand, not just `kuke init` (#569). Without this,
// `kuke daemon reset --run-path X` still cleans the default `/run/kukeon/`
// while `kuke init --run-path X` lays the socket under X — the asymmetry
// the source PR #569 flagged in its reviewer notes.
//
// The daemon-reset leaf is the canonical regression target; the init leaf
// case re-verifies the same root promotion still satisfies init's
// pre-#570 contract (init.go no longer calls the helper itself).
func TestRunPathImpliesKukeondSocket(t *testing.T) {
	tests := []struct {
		name        string
		leafArgs    []string
		setFlag     bool   // --run-path on the command line
		setEnv      string // KUKEON_RUN_PATH in env ("" = unset)
		setSocket   string // KUKEOND_SOCKET in env ("" = unset)
		preSet      string // pre-PreRunE viper.Set on KUKEOND_SOCKET ("" = skip)
		wantSocket  string // empty string asserts viper key remains unset
		wantNonZero bool   // for default-path cases, just assert non-empty
	}{
		{
			name:       "no-flag-no-env-no-derivation",
			leafArgs:   []string{"daemon", "reset"},
			wantSocket: "",
		},
		{
			name:       "run-path-flag-derives-on-reset",
			leafArgs:   []string{"daemon", "reset"},
			setFlag:    true,
			wantSocket: "/tmp/issue-570/kukeond.sock",
		},
		{
			name:       "run-path-env-derives-on-reset",
			leafArgs:   []string{"daemon", "reset"},
			setEnv:     "/tmp/issue-570-env",
			wantSocket: "/tmp/issue-570-env/kukeond.sock",
		},
		{
			name:       "run-path-flag-derives-on-init",
			leafArgs:   []string{"init"},
			setFlag:    true,
			wantSocket: "/tmp/issue-570/kukeond.sock",
		},
		{
			name:       "kukeond-socket-env-pinned-respects-env",
			leafArgs:   []string{"daemon", "reset"},
			setFlag:    true,
			setSocket:  "/run/kukeon/operator.sock",
			wantSocket: "/run/kukeon/operator.sock",
		},
		{
			name:       "pre-set-viper-respects-existing-pin",
			leafArgs:   []string{"daemon", "reset"},
			setFlag:    true,
			preSet:     "/run/kukeon/from-yaml.sock",
			wantSocket: "/run/kukeon/from-yaml.sock",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			viper.Reset()

			if tc.setEnv != "" {
				t.Setenv(config.KUKEON_ROOT_RUN_PATH.EnvVar(), tc.setEnv)
			}
			if tc.setSocket != "" {
				t.Setenv(config.KUKEOND_SOCKET.EnvVar(), tc.setSocket)
			}

			cmd, err := kuke.NewKukeCmd()
			if err != nil {
				t.Fatalf("NewKukeCmd() error = %v", err)
			}

			leaf, _, findErr := cmd.Find(tc.leafArgs)
			if findErr != nil {
				t.Fatalf("find %v subcommand: %v", tc.leafArgs, findErr)
			}
			if parseErr := leaf.ParseFlags(nil); parseErr != nil {
				t.Fatalf("parse leaf flags: %v", parseErr)
			}

			if tc.setFlag {
				if setErr := leaf.Flags().Set("run-path", "/tmp/issue-570"); setErr != nil {
					t.Fatalf("set --run-path on leaf: %v", setErr)
				}
			}
			if tc.preSet != "" {
				// Stand-in for a pre-derivation pin (env binding's
				// viper.Set on read, or a future caller that pre-pins
				// the socket from YAML before the root PreRunE runs).
				viper.Set(config.KUKEOND_SOCKET.ViperKey, tc.preSet)
			}

			leaf.SetContext(context.Background())
			if preErr := cmd.PersistentPreRunE(leaf, []string{}); preErr != nil {
				t.Fatalf("PersistentPreRunE: %v", preErr)
			}

			got := viper.GetString(config.KUKEOND_SOCKET.ViperKey)
			if got != tc.wantSocket {
				t.Errorf("KUKEOND_SOCKET: got %q, want %q", got, tc.wantSocket)
			}
		})
	}
}
