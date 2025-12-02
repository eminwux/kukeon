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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/cmd/config"
	applycmd "github.com/eminwux/kukeon/cmd/kuke/apply"
	autocompletecmd "github.com/eminwux/kukeon/cmd/kuke/autocomplete"
	createcmd "github.com/eminwux/kukeon/cmd/kuke/create"
	deletecmd "github.com/eminwux/kukeon/cmd/kuke/delete"
	getcmd "github.com/eminwux/kukeon/cmd/kuke/get"
	initcmd "github.com/eminwux/kukeon/cmd/kuke/init"
	killcmd "github.com/eminwux/kukeon/cmd/kuke/kill"
	purgecmd "github.com/eminwux/kukeon/cmd/kuke/purge"
	refreshcmd "github.com/eminwux/kukeon/cmd/kuke/refresh"
	startcmd "github.com/eminwux/kukeon/cmd/kuke/start"
	stopcmd "github.com/eminwux/kukeon/cmd/kuke/stop"
	"github.com/eminwux/kukeon/cmd/kuke/version"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/logging"
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
	rootCmd.AddCommand(autocompletecmd.NewAutocompleteCmd())
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
	rootCmd.PersistentFlags().String("run-path", "/opt/kukeon", "Run path for the kukeon runtime")
	if err := viper.BindPFlag(config.KUKEON_ROOT_RUN_PATH.ViperKey, rootCmd.PersistentFlags().Lookup("run-path")); err != nil {
		return err
	}

	rootCmd.PersistentFlags().
		String("config", "/etc/kukeon/config.yaml", "config file (default is /etc/kukeon/config.yaml)")
	if err := viper.BindPFlag(config.KUKEON_ROOT_CONFIG_FILE.ViperKey, rootCmd.PersistentFlags().Lookup("config")); err != nil {
		return err
	}

	rootCmd.PersistentFlags().String("containerd-socket", "/run/containerd/containerd.sock", "containerd socket file")
	if err := viper.BindPFlag(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, rootCmd.PersistentFlags().Lookup("containerd-socket")); err != nil {
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
	var configFile string
	if viper.GetString(config.KUKEON_ROOT_CONFIG_FILE.ViperKey) == "" {
		configFile = config.DefaultConfigFile()
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		// Add the directory containing the config file
		viper.AddConfigPath(filepath.Dir(configFile))
	}
	_ = config.KUKEON_ROOT_CONFIG_FILE.BindEnv()

	if err := config.KUKEON_ROOT_CONFIG_FILE.Set(configFile); err != nil {
		return fmt.Errorf("%w: failed to set config file: %w", errdefs.ErrConfig, err)
	}

	var runPath string
	runPath = viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
		viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, runPath)
	}
	_ = config.KUKEON_ROOT_RUN_PATH.BindEnv()

	_ = config.KUKEON_ROOT_LOG_LEVEL.BindEnv()
	logLevel := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey)
	if logLevel == "" {
		viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, "info")
	}

	if err := viper.ReadInConfig(); err != nil {
		// File not found is OK if ENV is set
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
		}
	}

	return nil
}

// LoadConfig is a public wrapper for backward compatibility.
func LoadConfig() error {
	return loadConfig()
}
