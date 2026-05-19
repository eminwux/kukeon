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

// Package restart implements `kuke daemon restart`, which is the composed
// stop-then-start lifecycle verb for the kukeond cell. It runs in-process for
// the same reason `kuke daemon stop`/`start` do — kukeond cannot relay its own
// teardown — and forwards `--timeout` to the stop phase's grace period.
package restart

import (
	"fmt"
	"log/slog"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/daemon/internal/lifecycle"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
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

// NewRestartCmd builds the `kuke daemon restart` cobra command.
func NewRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the kukeond daemon cell (stop, then start)",
		Long: "Compose `kuke daemon stop` and `kuke daemon start` into a single verb.\n\n" +
			"Sends SIGTERM and waits up to --timeout (default 10s) for the daemon " +
			"to exit; if the grace period expires, escalates to SIGKILL. Once the " +
			"cell is stopped, brings it back up. " +
			"Idempotent: when the daemon is already stopped, the stop phase is " +
			"skipped and the start phase still runs.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runRestart,
	}

	cmd.Flags().Duration(
		"timeout", lifecycle.DefaultTimeout,
		"Grace period for the stop phase before escalating from SIGTERM to SIGKILL",
	)
	_ = viper.BindPFlag(config.KUKE_DAEMON_RESTART_TIMEOUT.ViperKey, cmd.Flags().Lookup("timeout"))

	return cmd
}

func runRestart(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	if err := kukshared.RequireRoot("kuke daemon restart"); err != nil {
		return err
	}

	timeout := viper.GetDuration(config.KUKE_DAEMON_RESTART_TIMEOUT.ViperKey)
	if timeout <= 0 {
		timeout = lifecycle.DefaultTimeout
	}

	client := resolveClient(cmd, logger)
	defer func() { _ = client.Close() }()

	doc := kukeondCellDoc()

	getRes, err := client.GetCell(cmd.Context(), doc)
	if err != nil {
		return fmt.Errorf("inspect kukeond cell: %w", err)
	}
	if !getRes.MetadataExists {
		return errdefs.ErrHostNotInitialized
	}

	probe := lifecycle.ResolveReachableProbe(cmd)
	socketPath := lifecycle.ResolveSocketPath()
	socketReachable := probe(cmd.Context(), socketPath, lifecycle.DefaultReachableTimeout)
	cellRunning := lifecycle.IsCellRunning(getRes.Cell)
	switch {
	case !cellRunning && !socketReachable:
		cmd.Printf(
			"kukeond was already stopped (cell %q in realm %q)\n",
			consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
	case !cellRunning && socketReachable:
		// Metadata lags behind a live daemon — fall through to StopPhase
		// rather than skipping the stop phase and trying to re-start on
		// top of an already-running cell.
		cmd.Printf(
			"kukeond metadata reports not-Ready but socket %s is reachable; stopping cell\n",
			socketPath,
		)
		logger.WarnContext(cmd.Context(),
			"daemon metadata stale: marked not-Ready but socket reachable; stopping cell",
			"socket", socketPath,
			"cell", consts.KukeSystemCellName,
			"realm", consts.KukeSystemRealmName,
		)
		if stopErr := lifecycle.StopPhase(cmd, client, doc, timeout); stopErr != nil {
			return stopErr
		}
	default:
		// cellRunning — either fully-live or stale-Ready. Run StopPhase
		// so the controller reconciles cell state regardless of which
		// side is stale.
		if stopErr := lifecycle.StopPhase(cmd, client, doc, timeout); stopErr != nil {
			return stopErr
		}
	}

	startRes, err := client.StartCell(cmd.Context(), doc)
	if err != nil {
		return fmt.Errorf("start kukeond cell: %w", err)
	}
	if !startRes.Started {
		return fmt.Errorf("start kukeond cell: %w", errdefs.ErrControllerNoChange)
	}

	cmd.Printf(
		"kukeond started (cell %q in realm %q)\n",
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

// resolveClient returns the kukeonv1.Client used by runRestart. Tests inject
// a fake via lifecycle.MockClientKey; production always builds an in-process
// client — `kuke daemon` is daemon-lifecycle (per the umbrella in #217), so
// routing through the daemon is impossible by definition.
func resolveClient(cmd *cobra.Command, logger *slog.Logger) kukeonv1.Client {
	if mockClient, ok := cmd.Context().Value(lifecycle.MockClientKey{}).(kukeonv1.Client); ok {
		return mockClient
	}
	opts := controller.Options{
		RunPath:          viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey),
		ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	}
	return local.New(cmd.Context(), logger, opts)
}
