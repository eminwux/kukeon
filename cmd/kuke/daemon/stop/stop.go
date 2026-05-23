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

// Package stop implements `kuke daemon stop`, which gracefully shuts down the
// kukeond cell. The command runs in-process — kukeond cannot relay its own
// shutdown — and escalates to a force-kill if the graceful path does not
// complete within the configured grace period.
package stop

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

// NewStopCmd builds the `kuke daemon stop` cobra command.
func NewStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the kukeond daemon cell",
		Long: "Gracefully shut down the kukeond cell.\n\n" +
			"Sends SIGTERM and waits up to --timeout (default 10s) for the daemon " +
			"to exit; if the grace period expires, escalates to SIGKILL. " +
			"Idempotent: succeeds with a clear message when the daemon is already stopped.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runStop,
	}

	cmd.Flags().Duration(
		"timeout", lifecycle.DefaultTimeout,
		"Grace period before escalating from SIGTERM to SIGKILL",
	)
	_ = viper.BindPFlag(config.KUKE_DAEMON_STOP_TIMEOUT.ViperKey, cmd.Flags().Lookup("timeout"))

	// --server-configuration targets a specific kukeond instance, via the
	// shared precedence chain (flag > KUKEOND_CONFIGURATION env > default
	// file > hardcoded defaults). Issue #284.
	kukshared.RegisterServerConfigurationFlag(cmd)

	return cmd
}

func runStop(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	if err := kukshared.RequireRoot("kuke daemon stop"); err != nil {
		return err
	}

	if _, _, err := kukshared.LoadServerConfigurationFromFlag(cmd); err != nil {
		return err
	}

	timeout := viper.GetDuration(config.KUKE_DAEMON_STOP_TIMEOUT.ViperKey)
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

	if !lifecycle.IsCellRunning(getRes.Cell) {
		probe := lifecycle.ResolveReachableProbe(cmd)
		socketPath := lifecycle.ResolveSocketPath()
		if !probe(cmd.Context(), socketPath, lifecycle.DefaultReachableTimeout) {
			cmd.Printf(
				"kukeond is already stopped (cell %q in realm %q)\n",
				consts.KukeSystemCellName, consts.KukeSystemRealmName,
			)
			return nil
		}
		// Persisted state reads not-Ready but the socket answers — the
		// daemon is up and metadata lags. Fall through to StopPhase
		// rather than silently no-op while the daemon is still serving.
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
	}

	return lifecycle.StopPhase(cmd, client, doc, timeout)
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

// resolveClient returns the kukeonv1.Client used by runStop. Tests inject a
// fake via lifecycle.MockClientKey; production always builds an in-process
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
