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

// Package start implements `kuke daemon start`, which brings up the existing
// kukeond cell on a host that has already been initialized. The command runs
// in-process — kukeond does not exist when this verb is invoked, so there is
// no daemon to route through.
package start

import (
	"fmt"
	"log/slog"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/internal/lifecycle"
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

// NewStartCmd builds the `kuke daemon start` cobra command.
func NewStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the kukeond daemon cell",
		Long: "Bring up the existing kukeond cell provisioned by `kuke init`.\n\n" +
			"Idempotent: returns success when the daemon is already running. " +
			"Errors when the host has not been initialized — run `kuke init` first.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runStart,
	}

	// --server-configuration targets a specific kukeond instance, via the
	// shared precedence chain (flag > KUKEOND_CONFIGURATION env > default
	// file > hardcoded defaults). Issue #284.
	kukshared.RegisterServerConfigurationFlag(cmd)

	return cmd
}

func runStart(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	if err := kukshared.RequireRoot("kuke daemon start"); err != nil {
		return err
	}

	if _, _, err := kukshared.LoadServerConfigurationFromFlag(cmd); err != nil {
		return err
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

	if lifecycle.IsCellRunning(getRes.Cell) {
		probe := lifecycle.ResolveReachableProbe(cmd)
		socketPath := lifecycle.ResolveSocketPath()
		if probe(cmd.Context(), socketPath, lifecycle.DefaultReachableTimeout) {
			cmd.Printf(
				"kukeond is already running (cell %q in realm %q)\n",
				consts.KukeSystemCellName, consts.KukeSystemRealmName,
			)
			return nil
		}
		// Persisted state reads Ready but the socket does not answer — the
		// daemon was killed externally (OOM, host reboot mid-run, kill -9)
		// and the metadata never got the "stopped" write. Reconcile by
		// falling through to StartCell rather than claiming "already
		// running" while the socket is missing.
		cmd.Printf(
			"kukeond metadata reports Ready but socket %s is unreachable; restarting cell\n",
			socketPath,
		)
		logger.WarnContext(cmd.Context(),
			"daemon metadata stale: marked Ready but socket unreachable; restarting cell",
			"socket", socketPath,
			"cell", consts.KukeSystemCellName,
			"realm", consts.KukeSystemRealmName,
		)
	}

	// Recreate /run/kukeon (the cell's bind-mount source) if a reboot wiped
	// the tmpfs — only `kuke init` created it, so without this the start
	// fails with "open /run/kukeon: no such file or directory".
	if ensureErr := lifecycle.ResolveEnsureSocketDir(cmd)(); ensureErr != nil {
		return fmt.Errorf("ensure kukeond socket dir: %w", ensureErr)
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

// resolveClient returns the kukeonv1.Client used by runStart. Tests inject a
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
