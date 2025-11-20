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

package shared

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// LoggerFromCmd extracts the slog logger from the Cobra command context.
func LoggerFromCmd(cmd *cobra.Command) (*slog.Logger, error) {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return nil, errdefs.ErrLoggerNotFound
	}
	return logger, nil
}

// ControllerFromCmd instantiates a controller.Exec configured with the shared
// persistent flags (run path, containerd socket) used by the parent command.
func ControllerFromCmd(cmd *cobra.Command) (*controller.Exec, error) {
	logger, err := LoggerFromCmd(cmd)
	if err != nil {
		return nil, err
	}

	opts := controller.Options{
		RunPath:          viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey),
		ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	}

	return controller.NewControllerExec(cmd.Context(), logger, opts), nil
}

// RequireNameArg ensures the first positional argument exists and returns it.
func RequireNameArg(cmd *cobra.Command, args []string, resource string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("%s name is required (usage: %s)", resource, cmd.UseLine())
	}
	value := strings.TrimSpace(args[0])
	if value == "" {
		return "", fmt.Errorf("%s name is required (usage: %s)", resource, cmd.UseLine())
	}
	return value, nil
}

// RequireNameArgOrDefault wraps RequireNameArg but falls back to the provided
// default value if no positional argument is supplied.
func RequireNameArgOrDefault(cmd *cobra.Command, args []string, resource string, fallback string) (string, error) {
	if len(args) == 0 {
		fallback = strings.TrimSpace(fallback)
		if fallback != "" {
			args = []string{fallback}
		}
	}
	return RequireNameArg(cmd, args, resource)
}

// PrintCreationOutcome prints a human-friendly status line for a created resource.
func PrintCreationOutcome(cmd *cobra.Command, label string, existsPost bool, created bool) {
	switch {
	case created:
		cmd.Printf("  - %s: created\n", label)
	case existsPost:
		cmd.Printf("  - %s: already existed\n", label)
	default:
		cmd.Printf("  - %s: missing\n", label)
	}
}
