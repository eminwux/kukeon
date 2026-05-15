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

// Package logs implements `kuke daemon logs`, a shortcut that prints the
// kukeond container's stdout/stderr stream. The kukeond cell coordinates
// (realm=kuke-system, space=kukeon, stack=kukeon, cell=kukeond, container=
// kukeond) are static — set by `kuke init` and centralised in internal/consts
// — so this verb fills them in and dispatches through the same tail loop
// `kuke log` uses. The command runs in-process for the same reason the rest
// of `kuke daemon` does: it operates on the daemon cell and must keep working
// when the daemon socket is not reachable.
package logs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/eminwux/kukeon/cmd/config"
	logcmd "github.com/eminwux/kukeon/cmd/kuke/log"
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
// context so unit tests can exercise runLogs without a real controller.
type MockClientKey struct{}

// MockTailKey injects a logcmd.TailFn via context for tests so the real
// follow loop (which would block on a real file) can be bypassed.
type MockTailKey struct{}

// NewLogsCmd builds the `kuke daemon logs` cobra command.
func NewLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "logs",
		Aliases: []string{"log"},
		Short:   "Print the kukeond daemon's stdout/stderr (use -f to follow)",
		Long: "Print the kukeond container's stdout/stderr stream.\n\n" +
			"Shortcut for `kuke log --realm kuke-system --space kukeon " +
			"--stack kukeon kukeond`; the coordinates are static and " +
			"filled in for you. By default the current contents are printed " +
			"and the command exits; pass -f/--follow to tail until SIGINT.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runLogs,
	}

	cmd.Flags().BoolP("follow", "f", false,
		"Tail the file until SIGINT instead of printing current contents and exiting")
	_ = viper.BindPFlag(config.KUKE_DAEMON_LOGS_FOLLOW.ViperKey, cmd.Flags().Lookup("follow"))

	return cmd
}

func runLogs(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	follow := viper.GetBool(config.KUKE_DAEMON_LOGS_FOLLOW.ViperKey)

	client := resolveClient(cmd, logger)
	defer func() { _ = client.Close() }()

	cellDoc := kukeondCellDoc()
	getRes, err := client.GetCell(cmd.Context(), cellDoc)
	if err != nil {
		return fmt.Errorf("inspect kukeond cell: %w", err)
	}
	if !getRes.MetadataExists {
		return errors.New(
			"kukeon host is not initialized: kukeond cell metadata is missing; run `kuke init` first",
		)
	}
	if !isCellRunning(getRes.Cell) {
		return fmt.Errorf(
			"kukeond is not running (cell %q in realm %q); run `kuke daemon start` to bring it up "+
				"(or `kuke status` to inspect)",
			consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
	}

	logRes, err := client.LogContainer(cmd.Context(), kukeondContainerDoc())
	if err != nil {
		if errors.Is(err, errdefs.ErrContainerNotFound) {
			return fmt.Errorf(
				"kukeond container %q not found in cell %q: %w",
				consts.KukeSystemContainerName, consts.KukeSystemCellName, err,
			)
		}
		return fmt.Errorf("resolve kukeond log path: %w", err)
	}
	streamPath := logRes.HostLogPath
	if streamPath == "" {
		streamPath = logRes.HostCapturePath
	}
	if streamPath == "" {
		return errors.New("controller returned no log path for kukeond container")
	}

	tail := resolveTail(cmd)
	if tailErr := tail(cmd.Context(), streamPath, cmd.OutOrStdout(), follow); tailErr != nil {
		if errors.Is(tailErr, os.ErrNotExist) {
			return fmt.Errorf(
				"kukeond has no log file at %s yet (the runtime shim has not opened it — "+
					"try again once the daemon produces output)",
				streamPath,
			)
		}
		return tailErr
	}
	return nil
}

// kukeondCellDoc builds the lookup CellDoc for the system kukeond cell.
// Identical shape to the one used by `kuke daemon start`/`stop`/`restart`;
// the names are fixed by `kuke init` and centralised in internal/consts.
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

// kukeondContainerDoc builds the lookup ContainerDoc for the kukeond
// container inside the kukeond cell. The container name matches the cell
// name by convention (see internal/consts.KukeSystemContainerName).
func kukeondContainerDoc() v1beta1.ContainerDoc {
	return v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   consts.KukeSystemContainerName,
			Labels: map[string]string{},
		},
		Spec: v1beta1.ContainerSpec{
			ID:      consts.KukeSystemContainerName,
			RealmID: consts.KukeSystemRealmName,
			SpaceID: consts.KukeSystemSpaceName,
			StackID: consts.KukeSystemStackName,
			CellID:  consts.KukeSystemCellName,
		},
	}
}

// isCellRunning treats the cell as live if any container reports Ready, or
// if the persisted cell state is Ready. Same definition the other daemon
// lifecycle verbs use, so "not running" means the same thing across the
// `kuke daemon` subcommand group.
func isCellRunning(cell v1beta1.CellDoc) bool {
	for _, c := range cell.Status.Containers {
		if c.State == v1beta1.ContainerStateReady {
			return true
		}
	}
	return cell.Status.State == v1beta1.CellStateReady
}

// resolveClient returns the kukeonv1.Client used by runLogs. Tests inject a
// fake via MockClientKey; production always builds an in-process client —
// `kuke daemon logs` is part of the daemon-lifecycle umbrella, so it must
// work even when the daemon socket is not reachable.
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

// resolveTail returns the file-tailing function used by runLogs. Tests inject
// a mock via MockTailKey; production dispatches to `kuke log`'s TailFile so
// the two commands share dump-and-exit and follow semantics.
func resolveTail(cmd *cobra.Command) logcmd.TailFn {
	if mock, ok := cmd.Context().Value(MockTailKey{}).(logcmd.TailFn); ok {
		return mock
	}
	return func(ctx context.Context, path string, out io.Writer, follow bool) error {
		return logcmd.TailFile(ctx, path, out, follow)
	}
}
