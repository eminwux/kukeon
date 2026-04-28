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

package kukeond

import (
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/daemon"
	"github.com/eminwux/kukeon/internal/logging"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Socket modes applied to the kukeond unix listener. The narrow mode is the
// fallback when no SocketGID was passed (root-only access); the group-readable
// mode is used when init plumbed a kukeon GID through `--socket-gid` so a
// non-root operator in the kukeon group can dial the daemon after restart.
const (
	socketModeRootOnly      os.FileMode = 0o600
	socketModeGroupReadable os.FileMode = 0o660
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "serve",
		Short:        "Run the kukeond daemon in the foreground",
		SilenceUsage: true,
		RunE:         runServe,
	}
	return cmd
}

func runServe(cmd *cobra.Command, _ []string) error {
	logLevel := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey)
	if logLevel == "" {
		logLevel = "info"
	}
	levelVar := new(slog.LevelVar)
	levelVar.Set(logging.ParseLevel(logLevel))
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})
	logger := slog.New(handler)

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	socketPath := viper.GetString(config.KUKEOND_SOCKET.ViperKey)
	if socketPath == "" {
		socketPath = config.KUKEOND_SOCKET.Default
	}
	runPath := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
	}

	socketGID := viper.GetInt(config.KUKEOND_SOCKET_GID.ViperKey)
	socketMode := socketModeRootOnly
	if socketGID > 0 {
		socketMode = socketModeGroupReadable
	}

	opts := daemon.Options{
		SocketPath: socketPath,
		SocketMode: socketMode,
		SocketGID:  socketGID,
		PIDFile:    filepath.Join(runPath, "kukeond.pid"),
		Controller: controller.Options{
			RunPath:          runPath,
			ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
		},
	}

	server := daemon.NewServer(ctx, logger, opts)

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve() }()

	select {
	case <-ctx.Done():
		logger.InfoContext(ctx, "received shutdown signal", "signal", ctx.Err())
	case err := <-serveErr:
		if err != nil {
			logger.ErrorContext(cmd.Context(), "serve exited with error", "error", err)
			_ = server.Stop()
			return err
		}
	}

	if err := server.Stop(); err != nil {
		logger.ErrorContext(cmd.Context(), "stop error", "error", err)
		return err
	}
	logger.InfoContext(cmd.Context(), "kukeond stopped")
	return nil
}
