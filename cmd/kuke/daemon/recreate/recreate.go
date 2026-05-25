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

// Package recreate implements `kuke daemon recreate`, a composed verb that
// tears down the kukeond cell and re-provisions it using the same cell-creation
// path `kuke init` exercises. It composes the reset tear-down (stop + delete +
// clear socket/pid) and the init cell-provisioning into one step for the
// dev-loop: `kuke daemon recreate --kukeond-image <ref>` replaces the manual
// `kuke daemon reset && kuke build && kuke init` sequence.
//
// Image-load is intentionally out of scope: the verb assumes the desired
// kukeond image is already in the kuke-system realm. Operators call
// `kuke build` (or `kuke image load`) before `kuke daemon recreate`.
package recreate

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

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

const (
	kukeondReadyTimeout = 30 * time.Second
	kukeondReadyTick    = 200 * time.Millisecond
)

// MockSocketDirKey overrides the directory containing kukeond.{sock,pid}.
// Tests use this to point the cleanup step at a tmpdir; production reads the
// path from KUKEOND_SOCKET viper config.
type MockSocketDirKey struct{}

// MockProvisionKukeondCellKey injects a stub for the provisioning phase so
// unit tests can verify the teardown phase without a real containerd socket.
// Production always calls ProvisionKukeondCell on the controller.
type MockProvisionKukeondCellKey struct{}

// MockWaitForReadyKey injects a stub for the wait-for-ready step so unit
// tests can verify the teardown phase without a real listening socket.
type MockWaitForReadyKey struct{}

// resolveProvisionKukeondCell picks the provisioning implementation. Tests
// inject a stub via MockProvisionKukeondCellKey; production builds a real
// controller and delegates to ProvisionKukeondCell.
func resolveProvisionKukeondCell(
	cmd *cobra.Command,
	logger *slog.Logger,
	image, serverConfigPath string,
) error {
	if stub, ok := cmd.Context().Value(MockProvisionKukeondCellKey{}).(func() error); ok && stub != nil {
		return stub()
	}

	runPath := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
	}
	socketPath := viper.GetString(config.KUKEOND_SOCKET.ViperKey)
	if socketPath == "" {
		socketPath = config.KUKEOND_SOCKET.Default
	}
	containerdSocket := viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey)

	ctrl := controller.NewControllerExec(cmd.Context(), logger, controller.Options{
		RunPath:              runPath,
		ContainerdSocket:     containerdSocket,
		KukeondSocket:        socketPath,
		KukeondImage:         image,
		KukeondSocketGID:     0,
		KukeondConfiguration: serverConfigPath,
	})
	defer ctrl.Close()

	cellSection, err := ctrl.ProvisionKukeondCell()
	_ = cellSection
	return err
}

// resolveWaitForReady picks the wait-for-ready implementation. Tests inject a
// stub via MockWaitForReadyKey; production dials the socket and pings.
func resolveWaitForReady(cmd *cobra.Command, socketPath string) error {
	if stub, ok := cmd.Context().Value(MockWaitForReadyKey{}).(func() error); ok && stub != nil {
		return stub()
	}
	return waitForKukeondReady(cmd.Context(), socketPath, kukeondReadyTimeout)
}

// NewRecreateCmd builds the `kuke daemon recreate` cobra command.
func NewRecreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recreate",
		Short: "Recreate the kukeond daemon cell (tear down, re-provision, start)",
		Long: "Compose `kuke daemon reset` and `kuke init`'s kukeond cell provisioning " +
			"into a single verb.\n\n" +
			"Tears down the existing kukeond cell (stop, delete, clear socket+pid) " +
			"and re-provisions it from scratch using the specified --kukeond-image. " +
			"The cell-creation path is shared with `kuke init`, so the two cannot drift.\n\n" +
			"Requires --kukeond-image and errors with ErrHostNotInitialized when the " +
			"host has not been bootstrapped by `kuke init` yet.\n\n" +
			"Image-load is out of scope: the desired image must already be present in " +
			"the kuke-system realm (via `kuke build` or `kuke image load`) before " +
			"invoking this command.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runRecreate,
	}

	cmd.Flags().String(
		"kukeond-image", "",
		"Container image for kukeond (required)",
	)
	_ = viper.BindPFlag(config.KUKE_DAEMON_RECREATE_KUKEOND_IMAGE.ViperKey, cmd.Flags().Lookup("kukeond-image"))

	cmd.Flags().Duration(
		"timeout", lifecycle.DefaultTimeout,
		"Grace period for the stop phase before escalating from SIGTERM to SIGKILL",
	)
	_ = viper.BindPFlag(config.KUKE_DAEMON_RECREATE_TIMEOUT.ViperKey, cmd.Flags().Lookup("timeout"))

	// --server-configuration targets a specific kukeond instance, via the
	// shared precedence chain (flag > KUKEOND_CONFIGURATION env > default
	// file > hardcoded defaults). Issue #284.
	kukshared.RegisterServerConfigurationFlag(cmd)

	return cmd
}

func runRecreate(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	if err := kukshared.RequireRoot("kuke daemon recreate"); err != nil {
		return err
	}

	serverSpec, serverConfigPath, err := kukshared.LoadServerConfigurationFromFlag(cmd)
	if err != nil {
		return err
	}

	image := viper.GetString(config.KUKE_DAEMON_RECREATE_KUKEOND_IMAGE.ViperKey)
	if image == "" {
		// Use the server configuration's kukeondImage as fallback, then error
		// if still empty — recreate requires knowing which image to provision.
		image = serverSpec.KukeondImage
	}
	if image == "" {
		return fmt.Errorf("--kukeond-image is required; the host's server configuration also has no kukeondImage set")
	}

	timeout := viper.GetDuration(config.KUKE_DAEMON_RECREATE_TIMEOUT.ViperKey)
	if timeout <= 0 {
		timeout = lifecycle.DefaultTimeout
	}

	// Phase 1: Tear down — composed from reset's stop + delete + clear path.
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
		if stopErr := lifecycle.StopPhase(cmd, client, doc, timeout); stopErr != nil {
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
		return fmt.Errorf("delete kukeond cell: %w", errdefs.ErrControllerNoChange)
	}
	cmd.Printf(
		"kukeond cell deleted (cell %q in realm %q)\n",
		consts.KukeSystemCellName, consts.KukeSystemRealmName,
	)

	// Clear transient socket/pid files (same as reset).
	socketDir := resolveSocketDir(cmd)
	transientFiles := []string{
		filepath.Join(socketDir, "kukeond.sock"),
		filepath.Join(socketDir, "kukeond.pid"),
	}
	for _, path := range transientFiles {
		removed, removeErr := removeFileIfExists(path)
		if removeErr != nil {
			return fmt.Errorf("remove %q: %w", path, removeErr)
		}
		if removed {
			cmd.Printf("removed %s\n", path)
		}
	}

	// Phase 2: Re-provision using the shared controller helper.
	if ensureErr := lifecycle.ResolveEnsureSocketDir(cmd)(); ensureErr != nil {
		return fmt.Errorf("ensure kukeond socket dir: %w", ensureErr)
	}

	if err = resolveProvisionKukeondCell(cmd, logger, image, serverConfigPath); err != nil {
		return fmt.Errorf("provision kukeond cell: %w", err)
	}

	socketPath := lifecycle.ResolveSocketPath()

	cmd.Printf(
		"kukeond recreated (cell %q in realm %q, image %s)\n",
		consts.KukeSystemCellName, consts.KukeSystemRealmName, image,
	)

	// Wait for the daemon to become reachable.
	if waitErr := resolveWaitForReady(cmd, socketPath); waitErr != nil {
		return fmt.Errorf("kukeond did not become ready after recreate: %w", waitErr)
	}
	cmd.Printf("kukeond is ready (unix://%s)\n", socketPath)
	return nil
}

// waitForKukeondReady polls the kukeond socket until it responds or the
// timeout expires. Mirrors the same function in cmd/kuke/init/init.go.
func waitForKukeondReady(ctx context.Context, socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out after %s: %w", timeout, lastErr)
			}
			return fmt.Errorf("timed out after %s", timeout)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, kukeondReadyTick)
		err := pingKukeond(attemptCtx, socketPath)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(kukeondReadyTick):
		}
	}
}

func pingKukeond(ctx context.Context, socketPath string) error {
	client := kukeonv1.NewUnixClient(socketPath, kukeonv1.WithDialTimeout(kukeondReadyTick))
	defer func() { _ = client.Close() }()
	return client.Ping(ctx)
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

// resolveClient returns the kukeonv1.Client used by runRecreate. Tests inject a
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

// resolveSocketDir picks the directory holding kukeond.{sock,pid}. Tests
// inject a tmpdir via MockSocketDirKey; production derives the parent from
// KUKEOND_SOCKET (default /run/kukeon/kukeond.sock -> /run/kukeon).
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

// removeFileIfExists removes a single file. Missing-file is a no-op so
// re-running on a clean tree does not error.
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