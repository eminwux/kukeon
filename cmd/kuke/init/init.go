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
	printBootstrapReport(cmd, report)
	return nil
}

func printBootstrapReport(cmd *cobra.Command, report controller.BootstrapReport) {
	printHeader(cmd, report)
	printOverview(cmd, report)
	cmd.Println("Actions:")
	printRealmActions(cmd, report)
	printSpaceActions(cmd, report)
	printStackActions(cmd, report)
	printCellActions(cmd, report)
	printCNIActions(cmd, report)
}

func printHeader(cmd *cobra.Command, report controller.BootstrapReport) {
	anyCreated := report.RealmCreated ||
		report.RealmContainerdNamespaceCreated ||
		report.SpaceCreated ||
		report.SpaceCNINetworkCreated ||
		report.StackCreated ||
		report.CellCreated ||
		report.CellPauseContainerCreated ||
		report.CellStarted ||
		report.CniConfigDirCreated ||
		report.CniCacheDirCreated ||
		report.CniBinDirCreated
	if anyCreated {
		cmd.Println("Initialized Kukeon runtime")
		return
	}
	cmd.Println("Kukeon runtime already initialized")
}

func printOverview(cmd *cobra.Command, report controller.BootstrapReport) {
	cmd.Println(fmt.Sprintf(
		"Realm: %s (namespace: %s)",
		report.RealmName,
		report.RealmContainerdNamespace,
	))
	cmd.Println(fmt.Sprintf("Run path: %s", report.RunPath))
}

func printRealmActions(cmd *cobra.Command, report controller.BootstrapReport) {
	if report.RealmCreated {
		cmd.Println("  - realm: created")
	} else {
		cmd.Println("  - realm: already existed")
	}
	if report.RealmContainerdNamespaceCreated {
		cmd.Println("  - containerd namespace: created")
	} else {
		cmd.Println("  - containerd namespace: already existed")
	}
	printCgroupAction(
		cmd,
		"realm",
		report.RealmCgroupExistsPre,
		report.RealmCgroupExistsPost,
		report.RealmCgroupCreated,
	)
}

func printSpaceActions(cmd *cobra.Command, report controller.BootstrapReport) {
	if report.SpaceCreated {
		cmd.Println("  - space: created")
	} else {
		cmd.Println("  - space: already existed")
	}
	if report.SpaceCNINetworkCreated {
		cmd.Println(fmt.Sprintf(
			"  - network %q: created",
			report.SpaceCNINetworkName,
		))
	} else {
		cmd.Println(fmt.Sprintf(
			"  - network %q: already existed",
			report.SpaceCNINetworkName,
		))
	}
	printCgroupAction(
		cmd,
		"space",
		report.SpaceCgroupExistsPre,
		report.SpaceCgroupExistsPost,
		report.SpaceCgroupCreated,
	)
}

func printStackActions(cmd *cobra.Command, report controller.BootstrapReport) {
	if report.StackCreated {
		cmd.Println("  - stack: created")
	} else {
		cmd.Println("  - stack: already existed")
	}
	printCgroupAction(
		cmd,
		"stack",
		report.StackCgroupExistsPre,
		report.StackCgroupExistsPost,
		report.StackCgroupCreated,
	)
}

func printCellActions(cmd *cobra.Command, report controller.BootstrapReport) {
	if report.CellCreated {
		cmd.Println("  - cell: created")
	} else {
		cmd.Println("  - cell: already existed")
	}
	printCgroupAction(
		cmd,
		"cell",
		report.CellCgroupExistsPre,
		report.CellCgroupExistsPost,
		report.CellCgroupCreated,
	)
	printCgroupAction(
		cmd,
		"cell pause container",
		report.CellPauseContainerExistsPre,
		report.CellPauseContainerExistsPost,
		report.CellPauseContainerCreated,
	)
	printCgroupAction(
		cmd,
		"cell containers",
		report.CellStartedPre,
		report.CellStartedPost,
		report.CellStarted,
	)
}

func printCNIActions(cmd *cobra.Command, report controller.BootstrapReport) {
	printDirAction(
		cmd,
		"CNI config dir",
		report.CniConfigDir,
		report.CniConfigDirCreated,
		report.CniConfigDirExistsPost,
	)
	printDirAction(cmd, "CNI cache dir", report.CniCacheDir, report.CniCacheDirCreated, report.CniCacheDirExistsPost)
	printDirAction(cmd, "CNI bin dir", report.CniBinDir, report.CniBinDirCreated, report.CniBinDirExistsPost)
}

func printDirAction(cmd *cobra.Command, label string, path string, created bool, existsPost bool) {
	if created {
		cmd.Println(fmt.Sprintf("  - %s %q: created", label, path))
		return
	}
	if existsPost {
		cmd.Println(fmt.Sprintf("  - %s %q: already existed", label, path))
		return
	}
	cmd.Println(fmt.Sprintf("  - %s %q: not created", label, path))
}

func printCgroupAction(cmd *cobra.Command, label string, existedPre bool, existsPost bool, created bool) {
	switch {
	case created:
		cmd.Println(fmt.Sprintf("  - %s cgroup: created", label))
	case existsPost:
		cmd.Println(fmt.Sprintf("  - %s cgroup: already existed", label))
	default:
		if existedPre {
			cmd.Println(fmt.Sprintf("  - %s cgroup: missing (was previously present)", label))
		} else {
			cmd.Println(fmt.Sprintf("  - %s cgroup: missing", label))
		}
	}
}
