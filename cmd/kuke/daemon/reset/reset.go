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

// Package reset implements `kuke daemon reset`, the lightweight dev-loop
// teardown for the kukeond cell. It composes stop (with the same SIGTERM →
// SIGKILL escalation as `kuke daemon stop`) plus delete, then clears the
// transient socket/PID files under /run/kukeon. User-realm data under
// /opt/kukeon/default/** is left untouched so a subsequent `kuke init` can
// re-bootstrap without wiping user workloads. `--purge-system` additionally
// removes /opt/kukeon/kuke-system for a fully clean re-bootstrap.
package reset

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
// context so unit tests can exercise runReset without a real controller.
type MockClientKey struct{}

// MockSocketDirKey overrides the directory containing kukeond.{sock,pid}.
// Tests use this to point the cleanup step at a tmpdir; production reads the
// path from KUKEOND_SOCKET viper config.
type MockSocketDirKey struct{}

// MockRunPathKey overrides the run-path the --purge-system step removes
// /opt/kukeon/kuke-system from. Tests use this to point at a tmpdir so the
// cleanup never touches the real /opt/kukeon.
type MockRunPathKey struct{}

// defaultTimeout matches `kuke daemon stop`'s grace period (#219) so the two
// verbs agree on how long the SIGTERM phase gets before SIGKILL escalates.
const defaultTimeout = 10 * time.Second

// NewResetCmd builds the `kuke daemon reset` cobra command.
func NewResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset the kukeond daemon cell (stop, delete, clear socket+pid)",
		Long: "Lightweight dev-loop teardown of the kukeond daemon.\n\n" +
			"Stops the kukeond cell (SIGTERM, escalating to SIGKILL after --timeout), " +
			"deletes the cell metadata + cgroups, and clears the transient files " +
			"/run/kukeon/kukeond.{sock,pid}. User-realm data under " +
			"/opt/kukeon/default/** is left intact. Re-running `kuke init` after " +
			"`kuke daemon reset` produces a clean re-bootstrap.\n\n" +
			"Pass --purge-system to additionally remove /opt/kukeon/kuke-system " +
			"for a fully clean re-bootstrap. Distinct from `kuke uninstall`, which " +
			"is the per-host teardown (every realm, the system user/group, and the " +
			"run path itself).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runReset,
	}

	cmd.Flags().Duration(
		"timeout", defaultTimeout,
		"Grace period for the stop phase before escalating from SIGTERM to SIGKILL",
	)
	_ = viper.BindPFlag(config.KUKE_DAEMON_RESET_TIMEOUT.ViperKey, cmd.Flags().Lookup("timeout"))

	cmd.Flags().Bool(
		"purge-system", false,
		"Also remove /opt/kukeon/kuke-system (user-realm data is still preserved)",
	)
	_ = viper.BindPFlag(config.KUKE_DAEMON_RESET_PURGE_SYSTEM.ViperKey, cmd.Flags().Lookup("purge-system"))

	return cmd
}

func runReset(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	timeout := viper.GetDuration(config.KUKE_DAEMON_RESET_TIMEOUT.ViperKey)
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	purgeSystem := viper.GetBool(config.KUKE_DAEMON_RESET_PURGE_SYSTEM.ViperKey)

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

	if isCellRunning(getRes.Cell) {
		if stopErr := stopPhase(cmd, client, doc, timeout); stopErr != nil {
			return stopErr
		}
	} else {
		cmd.Printf(
			"kukeond was already stopped (cell %q in realm %q)\n",
			consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
	}

	delRes, err := client.DeleteCell(cmd.Context(), doc)
	if err != nil {
		return fmt.Errorf("delete kukeond cell: %w", err)
	}
	if !delRes.MetadataDeleted {
		return errors.New("delete kukeond cell: controller reported no change")
	}
	cmd.Printf(
		"kukeond cell deleted (cell %q in realm %q)\n",
		consts.KukeSystemCellName, consts.KukeSystemRealmName,
	)

	socketDir := resolveSocketDir(cmd)
	for _, name := range []string{"kukeond.sock", "kukeond.pid"} {
		path := filepath.Join(socketDir, name)
		removed, removeErr := removeFileIfExists(path)
		if removeErr != nil {
			return fmt.Errorf("remove %q: %w", path, removeErr)
		}
		if removed {
			cmd.Printf("removed %s\n", path)
		}
	}

	if purgeSystem {
		runPath := resolveRunPath(cmd)
		systemDir := filepath.Join(runPath, consts.KukeSystemRealmName)
		removed, removeErr := removeDirIfExists(systemDir)
		if removeErr != nil {
			return fmt.Errorf("remove %q: %w", systemDir, removeErr)
		}
		if removed {
			cmd.Printf("removed %s\n", systemDir)
		}
	}

	return nil
}

// stopPhase mirrors `kuke daemon stop`: graceful StopCell with SIGTERM →
// SIGKILL escalation when the grace period expires. Duplicated here rather
// than imported from the stop package to keep each daemon-lifecycle verb
// self-contained — start/stop/kill/restart follow the same convention.
func stopPhase(
	cmd *cobra.Command,
	client kukeonv1.Client,
	doc v1beta1.CellDoc,
	timeout time.Duration,
) error {
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

// removeFileIfExists removes a single file. Missing-file is a no-op so a
// re-run of `kuke daemon reset` on a clean tree does not error.
func removeFileIfExists(path string) (bool, error) {
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, statErr
	}
	if rmErr := os.Remove(path); rmErr != nil {
		return false, rmErr
	}
	return true, nil
}

// removeDirIfExists removes a directory tree. Missing-dir is a no-op.
func removeDirIfExists(path string) (bool, error) {
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, statErr
	}
	if rmErr := os.RemoveAll(path); rmErr != nil {
		return false, rmErr
	}
	return true, nil
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
// the persisted cell state is Ready. Mirrors the check used by all other
// daemon-lifecycle verbs so they agree on what "running" means.
func isCellRunning(cell v1beta1.CellDoc) bool {
	for _, c := range cell.Status.Containers {
		if c.State == v1beta1.ContainerStateReady {
			return true
		}
	}
	return cell.Status.State == v1beta1.CellStateReady
}

// resolveClient returns the kukeonv1.Client used by runReset. Tests inject a
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

// resolveSocketDir picks the directory holding kukeond.{sock,pid}. Tests
// inject a tmpdir via MockSocketDirKey; production derives the parent from
// KUKEOND_SOCKET (default /run/kukeon/kukeond.sock → /run/kukeon).
func resolveSocketDir(cmd *cobra.Command) string {
	if dir, ok := cmd.Context().Value(MockSocketDirKey{}).(string); ok && dir != "" {
		return dir
	}
	socketPath := viper.GetString(config.KUKEOND_SOCKET.ViperKey)
	if socketPath == "" {
		socketPath = config.KUKEOND_SOCKET.Default
	}
	return filepath.Dir(socketPath)
}

// resolveRunPath picks the run path for the --purge-system step. Tests inject
// a tmpdir via MockRunPathKey; production reads KUKEON_ROOT_RUN_PATH (default
// /opt/kukeon).
func resolveRunPath(cmd *cobra.Command) string {
	if rp, ok := cmd.Context().Value(MockRunPathKey{}).(string); ok && rp != "" {
		return rp
	}
	runPath := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
	}
	return runPath
}
