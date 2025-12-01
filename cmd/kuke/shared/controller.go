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
	"log/slog"

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

// GetControllerWithMock is a generic helper to get a controller from context,
// supporting mock injection via a context key. If a mock is found in the context,
// it is returned. Otherwise, a real controller is created using ControllerFromCmd.
// The mockKey should be a unique type used as the context key.
func GetControllerWithMock[T any](
	cmd *cobra.Command,
	mockKey any,
	realController func(*cobra.Command) (T, error),
) (T, error) {
	// Check for mock controller in context
	if mockCtrl, ok := cmd.Context().Value(mockKey).(T); ok {
		return mockCtrl, nil
	}

	// Get real controller
	return realController(cmd)
}

// GetControllerWithMockWrapper is a convenience function that wraps GetControllerWithMock
// to use ControllerFromCmd as the real controller factory.
func GetControllerWithMockWrapper[T any](cmd *cobra.Command, mockKey any, wrapper func(*controller.Exec) T) (T, error) {
	var zero T

	// Check for mock controller in context
	if mockCtrl, ok := cmd.Context().Value(mockKey).(T); ok {
		return mockCtrl, nil
	}

	// Get real controller and wrap it
	realCtrl, err := ControllerFromCmd(cmd)
	if err != nil {
		return zero, err
	}

	return wrapper(realCtrl), nil
}
