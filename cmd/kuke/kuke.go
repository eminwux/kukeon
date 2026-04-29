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

package kuke

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/eminwux/kukeon/cmd/config"
	applycmd "github.com/eminwux/kukeon/cmd/kuke/apply"
	attachcmd "github.com/eminwux/kukeon/cmd/kuke/attach"
	autocompletecmd "github.com/eminwux/kukeon/cmd/kuke/autocomplete"
	createcmd "github.com/eminwux/kukeon/cmd/kuke/create"
	deletecmd "github.com/eminwux/kukeon/cmd/kuke/delete"
	getcmd "github.com/eminwux/kukeon/cmd/kuke/get"
	initcmd "github.com/eminwux/kukeon/cmd/kuke/init"
	killcmd "github.com/eminwux/kukeon/cmd/kuke/kill"
	logcmd "github.com/eminwux/kukeon/cmd/kuke/log"
	purgecmd "github.com/eminwux/kukeon/cmd/kuke/purge"
	refreshcmd "github.com/eminwux/kukeon/cmd/kuke/refresh"
	runcmd "github.com/eminwux/kukeon/cmd/kuke/run"
	startcmd "github.com/eminwux/kukeon/cmd/kuke/start"
	stopcmd "github.com/eminwux/kukeon/cmd/kuke/stop"
	uninstallcmd "github.com/eminwux/kukeon/cmd/kuke/uninstall"
	"github.com/eminwux/kukeon/cmd/kuke/version"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/clientconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/logging"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type ConfigLoader interface {
	LoadConfig() error
}

// MockConfigLoaderKey is used to inject mock config loaders in tests via context.
type MockConfigLoaderKey struct{}

func NewKukeCmd() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "kuke",
		Short: "Kukeon is a tool for managing Kukeon entities",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := loadClientConfiguration(cmd); err != nil {
				return err
			}

			var logger *slog.Logger
			if viper.GetBool(config.KUKEON_ROOT_VERBOSE.ViperKey) {
				logLevel := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey)
				if logLevel == "" {
					logLevel = "info"
				}

				// Create a new logger that writes to the file with the specified log level
				levelVar := new(slog.LevelVar)
				levelVar.Set(logging.ParseLevel(logLevel))

				textHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})
				handler := &logging.ReformatHandler{Inner: textHandler, Writer: os.Stdout}
				logger = slog.New(handler)

				// Store both logger and levelVar in context using struct keys
				ctx := cmd.Context()
				ctx = context.WithValue(ctx, types.CtxLogger, logger)
				ctx = context.WithValue(ctx, types.CtxLevelVar, &levelVar)
				ctx = context.WithValue(ctx, types.CtxHandler, handler)
				cmd.SetContext(ctx)
				logger.DebugContext(
					cmd.Context(),
					"enabling verbose",
					"log-level",
					viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey),
				)
			}

			// Check for mock config loader in context (for testing)
			var loader ConfigLoader
			if mockLoader, ok := cmd.Context().Value(MockConfigLoaderKey{}).(ConfigLoader); ok {
				loader = mockLoader
			} else {
				loader = &realConfigLoader{}
			}

			err := loader.LoadConfig()
			if err != nil {
				// Only log if logger was created (verbose mode)
				if logger != nil {
					logger.DebugContext(cmd.Context(), "config error", "error", err)
				}
				return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
			}
			return nil
		},
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	if err := SetupKukeCmd(cmd); err != nil {
		return nil, fmt.Errorf("failed to setup kukeon command: %w", err)
	}

	return cmd, nil
}

func SetupKukeCmd(rootCmd *cobra.Command) error {
	rootCmd.AddCommand(initcmd.NewInitCmd())
	rootCmd.AddCommand(applycmd.NewApplyCmd())
	rootCmd.AddCommand(createcmd.NewCreateCmd())
	rootCmd.AddCommand(getcmd.NewGetCmd())
	rootCmd.AddCommand(deletecmd.NewDeleteCmd())
	rootCmd.AddCommand(startcmd.NewStartCmd())
	rootCmd.AddCommand(stopcmd.NewStopCmd())
	rootCmd.AddCommand(killcmd.NewKillCmd())
	rootCmd.AddCommand(purgecmd.NewPurgeCmd())
	rootCmd.AddCommand(refreshcmd.NewRefreshCmd())
	rootCmd.AddCommand(runcmd.NewRunCmd())
	rootCmd.AddCommand(attachcmd.NewAttachCmd())
	rootCmd.AddCommand(logcmd.NewLogCmd())
	rootCmd.AddCommand(autocompletecmd.NewAutocompleteCmd())
	rootCmd.AddCommand(uninstallcmd.NewUninstallCmd())
	rootCmd.AddCommand(version.NewVersionCmd())

	// Persistent flags
	if err := SetPersistentLoggingFlags(rootCmd); err != nil {
		return err
	}

	// Bind Non-persistent Flags to Viper
	if err := SetFlags(rootCmd); err != nil {
		return err
	}

	return nil
}

func SetPersistentLoggingFlags(rootCmd *cobra.Command) error {
	rootCmd.PersistentFlags().String(
		"configuration", config.DefaultClientConfigurationFile(),
		"Path to a ClientConfiguration YAML; absent file uses hardcoded defaults",
	)
	if err := viper.BindPFlag(
		config.KUKE_CONFIGURATION.ViperKey,
		rootCmd.PersistentFlags().Lookup("configuration"),
	); err != nil {
		return err
	}

	rootCmd.PersistentFlags().String("run-path", "/opt/kukeon", "Run path for the kukeon runtime")
	if err := viper.BindPFlag(config.KUKEON_ROOT_RUN_PATH.ViperKey, rootCmd.PersistentFlags().Lookup("run-path")); err != nil {
		return err
	}

	rootCmd.PersistentFlags().String("containerd-socket", "/run/containerd/containerd.sock", "containerd socket file")
	if err := viper.BindPFlag(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, rootCmd.PersistentFlags().Lookup("containerd-socket")); err != nil {
		return err
	}

	rootCmd.PersistentFlags().String(
		"host", config.KUKEON_ROOT_HOST.Default,
		"kukeond endpoint (unix:///path or ssh://user@host)",
	)
	if err := viper.BindPFlag(config.KUKEON_ROOT_HOST.ViperKey, rootCmd.PersistentFlags().Lookup("host")); err != nil {
		return err
	}

	rootCmd.PersistentFlags().Bool(
		"no-daemon", false,
		"bypass kukeond and run operations in-process (requires privileges)",
	)
	if err := viper.BindPFlag(config.KUKEON_ROOT_NO_DAEMON.ViperKey, rootCmd.PersistentFlags().Lookup("no-daemon")); err != nil {
		return err
	}

	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose logging")
	if err := viper.BindPFlag(config.KUKEON_ROOT_VERBOSE.ViperKey, rootCmd.PersistentFlags().Lookup("verbose")); err != nil {
		return err
	}

	rootCmd.PersistentFlags().String("log-level", "", "Log level (debug, info, warn, error)")
	if err := viper.BindPFlag(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, rootCmd.PersistentFlags().Lookup("log-level")); err != nil {
		return err
	}

	return nil
}

func SetFlags(_ *cobra.Command) error {
	// rootCmd.Flags().String("terminal-id", "", "Optional terminal ID (random if omitted)")
	// if err := viper.BindPFlag(config.KUKEON_ROOT_TERM_ID.ViperKey, rootCmd.Flags().Lookup("terminal-id")); err != nil {
	// 	return err
	// }

	return nil
}

type realConfigLoader struct{}

func (r *realConfigLoader) LoadConfig() error {
	return loadConfig()
}

func loadConfig() error {
	_ = config.KUKEON_ROOT_HOST.BindEnv()
	_ = config.KUKEON_ROOT_CONTAINERD_SOCKET.BindEnv()

	_ = config.KUKEON_ROOT_RUN_PATH.BindEnv()
	if viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey) == "" {
		viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, config.DefaultRunPath())
	}

	_ = config.KUKEON_ROOT_LOG_LEVEL.BindEnv()
	if viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey) == "" {
		viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, "info")
	}

	return nil
}

// LoadConfig is a public wrapper for backward compatibility.
func LoadConfig() error {
	return loadConfig()
}

// loadClientConfiguration reads the ClientConfiguration document at
// `--configuration` (default `~/.kuke/kuke.yaml`) and layers its values onto
// viper for fields the operator did not set explicitly. Called from the root
// PersistentPreRunE so every subcommand sees the seeded defaults before
// reading viper.
func loadClientConfiguration(cmd *cobra.Command) error {
	path := viper.GetString(config.KUKE_CONFIGURATION.ViperKey)
	if path == "" {
		path = config.DefaultClientConfigurationFile()
	}
	doc, err := clientconfig.Load(path)
	if err != nil {
		return fmt.Errorf("load client configuration: %w", err)
	}
	applyClientConfiguration(cmd, doc.Spec)
	return nil
}

// applyClientConfiguration layers the loaded ClientConfiguration on top of
// viper for fields the operator did not explicitly set on the command line
// or via environment. Order of precedence: explicit `--flag` > env >
// ClientConfiguration > flag default. The flag check skips fields whose
// `--flag` was changed; the env check skips fields whose env var is set —
// without it, `viper.Set` would override viper's env binding and silently
// invert env > YAML.
func applyClientConfiguration(cmd *cobra.Command, spec v1beta1.ClientConfigurationSpec) {
	if spec.Host != "" && !flagChanged(cmd, "host") && !envSet(config.KUKEON_ROOT_HOST) {
		viper.Set(config.KUKEON_ROOT_HOST.ViperKey, spec.Host)
	}
	if spec.RunPath != "" && !flagChanged(cmd, "run-path") && !envSet(config.KUKEON_ROOT_RUN_PATH) {
		viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, spec.RunPath)
	}
	if spec.ContainerdSocket != "" && !flagChanged(cmd, "containerd-socket") &&
		!envSet(config.KUKEON_ROOT_CONTAINERD_SOCKET) {
		viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, spec.ContainerdSocket)
	}
	if spec.LogLevel != "" && !flagChanged(cmd, "log-level") && !envSet(config.KUKEON_ROOT_LOG_LEVEL) {
		viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, spec.LogLevel)
	}
}

// flagChanged checks both the local and persistent flag sets so the helper
// is correct in tests (where cmd is the root and persistent flags are not
// yet merged into cmd.Flags()) and in production (where cmd is the leaf
// subcommand and the merged set already contains the parent's persistent
// flags).
func flagChanged(cmd *cobra.Command, name string) bool {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		return true
	}
	if f := cmd.PersistentFlags().Lookup(name); f != nil && f.Changed {
		return true
	}
	return false
}

// envSet reports whether the OS env var backing v is present (any value,
// including empty string, counts as set — same semantics as viper's BindEnv).
func envSet(v config.Var) bool {
	_, ok := os.LookupEnv(v.EnvVar())
	return ok
}
