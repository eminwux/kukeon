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
	"errors"
	"fmt"
	"log/slog"
	"time"

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
// context so unit tests can exercise runStop without a real controller.
type MockClientKey struct{}

// defaultTimeout matches the issue spec (#219): 10s graceful grace period
// before SIGKILL escalation.
const defaultTimeout = 10 * time.Second

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
		"timeout", defaultTimeout,
		"Grace period before escalating from SIGTERM to SIGKILL",
	)
	_ = viper.BindPFlag(config.KUKE_DAEMON_STOP_TIMEOUT.ViperKey, cmd.Flags().Lookup("timeout"))

	return cmd
}

func runStop(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	timeout := viper.GetDuration(config.KUKE_DAEMON_STOP_TIMEOUT.ViperKey)
	if timeout <= 0 {
		timeout = defaultTimeout
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

	type stopOutcome struct {
		res kukeonv1.StopCellResult
		err error
	}
	done := make(chan stopOutcome, 1)
	go func() {
		res, sErr := client.StopCell(cmd.Context(), doc)
		done <- stopOutcome{res: res, err: sErr}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			return fmt.Errorf("stop kukeond cell: %w", out.err)
		}
		if !out.res.Stopped {
			return errors.New("stop kukeond cell: controller reported no change")
		}
		cmd.Printf(
			"kukeond stopped (cell %q in realm %q)\n",
			consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
		return nil
	case <-time.After(timeout):
		// Graceful path did not complete within the grace period — escalate.
		// The in-flight StopCell may still be running; the underlying containerd
		// task will be torn down by KillCell regardless, and the goroutine exits
		// cleanly when its call returns.
		killRes, killErr := client.KillCell(cmd.Context(), doc)
		if killErr != nil {
			return fmt.Errorf(
				"kill kukeond cell after %s grace period expired: %w",
				timeout, killErr,
			)
		}
		if !killRes.Killed {
			return errors.New("kill kukeond cell: controller reported no change")
		}
		cmd.Printf(
			"kukeond force-killed after %s grace period expired (cell %q in realm %q)\n",
			timeout, consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
		return nil
	}
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
// the persisted cell state is Ready. This mirrors the running-check used by
// `kuke daemon start` so the two verbs agree on what "running" means.
func isCellRunning(cell v1beta1.CellDoc) bool {
	for _, c := range cell.Status.Containers {
		if c.State == v1beta1.ContainerStateReady {
			return true
		}
	}
	return cell.Status.State == v1beta1.CellStateReady
}

// resolveClient returns the kukeonv1.Client used by runStop. Tests inject a
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
