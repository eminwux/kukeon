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
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ControllerFromCmd uses the centralized controller helper from kuke/shared.
func ControllerFromCmd(cmd *cobra.Command) (*controller.Exec, error) {
	return kukshared.ControllerFromCmd(cmd)
}

// LoggerFromCmd uses the centralized logger helper from kuke/shared.
func LoggerFromCmd(cmd *cobra.Command) (*slog.Logger, error) {
	return kukshared.LoggerFromCmd(cmd)
}

// ParseCascadeFlag reads the persistent --cascade flag from the command.
func ParseCascadeFlag(cmd *cobra.Command) bool {
	cascade, _ := cmd.Flags().GetBool("cascade")
	return cascade
}

// ParseForceFlag reads the --force flag from the command.
func ParseForceFlag(cmd *cobra.Command) bool {
	force, _ := cmd.Flags().GetBool("force")
	return force
}

// GetCascadeFromViper reads the cascade flag from viper.
func GetCascadeFromViper() bool {
	return viper.GetBool(config.KUKE_DELETE_CASCADE.ViperKey)
}

// GetForceFromViper reads the force flag from viper.
func GetForceFromViper() bool {
	return viper.GetBool(config.KUKE_DELETE_FORCE.ViperKey)
}
