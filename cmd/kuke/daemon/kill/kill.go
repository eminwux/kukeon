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

// Package kill implements `kuke daemon kill`, the immediate-SIGKILL escape
// hatch for the kukeond cell. The command runs in-process — kukeond cannot
// relay its own teardown — and skips the SIGTERM grace path entirely.
package kill

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/client/local"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockClientKey injects a kukeonv1.Client (typically a fake) via the command
// context so unit tests can exercise runKill without a real controller.
type MockClientKey struct{}

// NewKillCmd builds the `kuke daemon kill` cobra command.
func NewKillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kill",
		Short: "Immediately SIGKILL the kukeond daemon cell",
		Long: "Force-kill the kukeond cell with no grace period.\n\n" +
			"This is the escape hatch for a hung or unresponsive daemon — use " +
			"`kuke daemon stop` for the graceful path. " +
			"Idempotent: succeeds with a clear message when the daemon is already stopped.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runKill,
	}

	return cmd
}

func runKill(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	client := resolveClient(cmd, logger)
	defer func() { _ = client.Close() }()

	doc := kukeondCellDoc()

	getRes, err := client.GetCell(cmd.Context(), doc)
	if err != nil {
		return fmt.Errorf("inspect kukeond cell: %w", err)
	}
	if !getRes.MetadataExists {
		return errors.New("kukeon host is not initialized: kukeond cell metadata is missing; run `kuke init` first")
	}

	if !isCellRunning(getRes.Cell) {
		cmd.Printf(
			"kukeond is already stopped (cell %q in realm %q)\n",
			consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
		return nil
	}

	killRes, err := client.KillCell(cmd.Context(), doc)
	if err != nil {
		return fmt.Errorf("kill kukeond cell: %w", err)
	}
	if !killRes.Killed {
		return errors.New("kill kukeond cell: controller reported no change")
	}

	cmd.Printf(
		"kukeond force-killed (cell %q in realm %q)\n",
		consts.KukeSystemCellName, consts.KukeSystemRealmName,
	)
	return nil
}

// kukeondCellDoc builds the lookup CellDoc for the system kukeond cell. The
// names are fixed by `kuke init` and centralised in internal/consts.
func kukeondCellDoc() v1beta1.CellDoc {
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   consts.KukeSystemCellName,
			Labels: map[string]string{},
		},
		Spec: v1beta1.CellSpec{
			ID:      consts.KukeSystemCellName,
			RealmID: consts.KukeSystemRealmName,
			SpaceID: consts.KukeSystemSpaceName,
			StackID: consts.KukeSystemStackName,
		},
	}
}

// isCellRunning treats the cell as live if any container reports Ready, or if
// the persisted cell state is Ready. Mirrors the check in `kuke daemon start`
// and `kuke daemon stop` so all three verbs agree on what "running" means.
func isCellRunning(cell v1beta1.CellDoc) bool {
	for _, c := range cell.Status.Containers {
		if c.State == v1beta1.ContainerStateReady {
			return true
		}
	}
	return cell.Status.State == v1beta1.CellStateReady
}

// resolveClient returns the kukeonv1.Client used by runKill. Tests inject a
// fake via MockClientKey; production always builds an in-process client —
// `kuke daemon` is daemon-lifecycle (per the umbrella in #217), so routing
// through the daemon is impossible by definition.
func resolveClient(cmd *cobra.Command, logger *slog.Logger) kukeonv1.Client {
	if mockClient, ok := cmd.Context().Value(MockClientKey{}).(kukeonv1.Client); ok {
		return mockClient
	}
	opts := controller.Options{
		RunPath:          viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey),
		ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	}
	return local.New(cmd.Context(), logger, opts)
}
