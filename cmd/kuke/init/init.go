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

package init

import (
	"fmt"
	"log/slog"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Initialize a new Kukeon project",
		RunE:         runInit,
		SilenceUsage: true,
	}

	if err := setupInitCmd(cmd); err != nil {
		return nil
	}

	return cmd
}

func setupInitCmd(cmd *cobra.Command) error {
	if err := setFlags(cmd); err != nil {
		return fmt.Errorf("failed to set flags: %w", err)
	}

	if err := setPersistentFlags(cmd); err != nil {
		return fmt.Errorf("failed to set persistent flags: %w", err)
	}

	return nil
}

func setFlags(cmd *cobra.Command) error {
	cmd.Flags().String("realm", "main", "Name of default realm")
	err := viper.BindPFlag(config.KUKE_INIT_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}

	cmd.Flags().String("space", "default", "Name of default space")
	err = viper.BindPFlag(config.KUKE_INIT_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}
	return nil
}

func setPersistentFlags(_ *cobra.Command) error {
	return nil
}

func runInit(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	opts := controller.Options{
		RunPath:          viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey),
		ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	}

	logger.DebugContext(cmd.Context(), "running init", "opts", opts)

	ctrl := controller.NewControllerExec(cmd.Context(), logger, opts)
	report, err := ctrl.Bootstrap()
	if err != nil {
		return err
	}

	// Cobra prints to stdout
	if report.RealmCreated || report.NamespaceCreated {
		cmd.Println("Initialized Kukeon runtime")
	} else {
		cmd.Println("Kukeon runtime already initialized")
	}
	cmd.Println(fmt.Sprintf("Realm: %s (namespace: %s)", report.RealmName, report.Namespace))
	switch {
	case report.RealmCreated && report.NamespaceCreated:
		cmd.Println("Status: created realm and namespace")
	case report.RealmCreated && !report.NamespaceCreated:
		cmd.Println("Status: created realm; namespace existed")
	case !report.RealmCreated && report.NamespaceCreated:
		cmd.Println("Status: realm existed; created namespace")
	default:
		cmd.Println("Status: realm and namespace existed")
	}
	cmd.Println(fmt.Sprintf("Run path: %s", report.RunPath))
	cmd.Println(fmt.Sprintf("realm metadata exists: %t", report.RealmMetadataExistsPost))
	cmd.Println(fmt.Sprintf("container namespace exists: %t", report.NamespaceExistsPost))
	return nil
}
