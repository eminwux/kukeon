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
// kukeond) are static — set by `kuke init` and centralised in internal/consts.
//
// The command reads through the daemon first: the ReadDaemonLogs RPC over
// the kukeond socket (KUKEON_HOST) returns bounded, cursor-addressed chunks,
// so it works from a cell that has the socket mounted but only a read-only
// view of /opt/kukeon (#1525). When the daemon cannot be reached — socket
// missing, connection refused, permission denied, or a daemon too old to
// serve the RPC — it falls back to the in-process path, which resolves the
// log file through a local controller and tails it with `kuke log`'s loop.
// That fallback is why the verb keeps working when kukeond is down.
package logs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/internal/lifecycle"
	logcmd "github.com/eminwux/kukeon/cmd/kuke/log"
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

// MockDaemonClientKey injects the kukeonv1.Client used for the socket path
// via context, so tests can drive the daemon read without dialing a real
// kukeond. Without it, production dials KUKEON_HOST.
type MockDaemonClientKey struct{}

// daemonPollInterval is how often follow mode asks the daemon for new
// bytes once it has caught up — the same cadence as `kuke log -f`.
//
//nolint:gochecknoglobals // test seam: tests shorten the follow poll
var daemonPollInterval = 200 * time.Millisecond

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

	// --server-configuration targets a specific kukeond instance, via the
	// shared precedence chain (flag > KUKEOND_CONFIGURATION env > default
	// file > hardcoded defaults). The loaded containerdNamespaceSuffix
	// scopes consts.KukeSystemRealmName's namespace resolution so
	// `kuke daemon logs` tails the kukeond cell of the targeted instance.
	// Issue #284.
	kukshared.RegisterServerConfigurationFlag(cmd)

	return cmd
}

func runLogs(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	if _, _, err := kukshared.LoadServerConfigurationFromFlag(cmd); err != nil {
		return err
	}

	follow := viper.GetBool(config.KUKE_DAEMON_LOGS_FOLLOW.ViperKey)

	if !readThroughDaemon(cmd) {
		return runInProcess(cmd, logger, follow)
	}

	handled, sockErr := runViaDaemon(cmd, follow)
	if handled {
		return sockErr
	}
	logger.DebugContext(cmd.Context(),
		"kukeond socket unavailable; reading the daemon log in-process", "error", sockErr)
	if err := runInProcess(cmd, logger, follow); err != nil {
		return fmt.Errorf("%w (kukeond socket unavailable: %w)", err, sockErr)
	}
	return nil
}

// readThroughDaemon reports whether the socket path is tried at all. It is
// skipped when the operator asked for in-process execution (KUKEON_NO_DAEMON,
// or an explicit --run-path, which kuke.go promotes to no-daemon) and when
// --server-configuration (flag or KUKEOND_CONFIGURATION) targets a specific
// kukeond instance: KUKEON_HOST would not follow that choice, so the
// in-process read the flag was built for (#284) stays authoritative.
func readThroughDaemon(cmd *cobra.Command) bool {
	if viper.GetBool(config.KUKEON_ROOT_NO_DAEMON.ViperKey) {
		return false
	}
	if f := cmd.Flags().Lookup(kukshared.ServerConfigurationFlag); f != nil && f.Changed {
		return false
	}
	if _, set := os.LookupEnv(config.KUKEOND_CONFIGURATION.EnvVar()); set {
		return false
	}
	return true
}

// runViaDaemon prints kukeond's log through the ReadDaemonLogs RPC. handled
// is false only when the daemon could not be reached before the first chunk
// arrived (see daemonUnreachable); the caller then falls back to the
// in-process read. Any other outcome — success, a cancelled follow, or an
// error the daemon itself reported — is final.
func runViaDaemon(cmd *cobra.Command, follow bool) (bool, error) {
	client, err := resolveDaemonClient(cmd)
	if err != nil {
		return !daemonUnreachable(err), err
	}
	defer func() { _ = client.Close() }()

	ctx := cmd.Context()
	if follow {
		// Same SIGINT/SIGTERM scope as `kuke log -f`: Ctrl+C ends the follow
		// cleanly with exit 0 instead of killing the process mid-write.
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
	}
	return streamDaemonLogs(ctx, client, cmd.OutOrStdout(), follow)
}

// streamDaemonLogs reads the log forward from byte 0 in DaemonLogMaxBytes
// chunks until it has caught up, so the default mode prints the whole
// current log like the in-process dump does. With follow it then polls from
// the returned cursor until ctx is done. The daemon only returns complete
// lines, so a trailing line still being written appears once its newline
// lands. A Reset reply (the source shrank or was replaced) carries a tail
// read of the new source; printing it and continuing from its cursor is the
// tail -F behaviour.
func streamDaemonLogs(
	ctx context.Context, client kukeonv1.Client, out io.Writer, follow bool,
) (bool, error) {
	var (
		offset   int64
		answered bool
	)
	for {
		res, err := client.ReadDaemonLogs(ctx, kukeonv1.DaemonLogRequest{
			Offset:   offset,
			MaxBytes: kukeonv1.DaemonLogMaxBytes,
		})
		if err != nil {
			if ctx.Err() != nil {
				// SIGINT/SIGTERM (or the caller) ended the read: a clean stop,
				// matching `kuke log -f`, not a failure to report.
				return true, nil //nolint:nilerr // cancellation is the normal end of a follow
			}
			if !answered && daemonUnreachable(err) {
				return false, err
			}
			return true, fmt.Errorf("read kukeond log over the daemon socket: %w", err)
		}
		answered = true
		if _, writeErr := io.WriteString(out, res.Data); writeErr != nil {
			return true, writeErr
		}
		offset = res.Next
		if res.More {
			continue
		}
		if !follow {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, nil
		case <-time.After(daemonPollInterval):
		}
	}
}

// daemonUnreachable reports whether err means the RPC never reached a
// kukeond that serves ReadDaemonLogs: the dial failed (socket missing,
// refused, permission denied — all *net.OpError), the connection dropped,
// the address scheme is unsupported, or the daemon predates the RPC.
// Errors the daemon answered with (wire errors such as
// ErrDaemonLogUnavailable) are not in this set: the daemon is up and its
// answer is the authoritative one.
func daemonUnreachable(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, rpc.ErrShutdown) ||
		errors.Is(err, kukeonv1.ErrUnsupportedScheme) ||
		errors.Is(err, errdefs.ErrDaemonMethodUnsupported)
}

// runInProcess is the daemon-down path: resolve the log file through an
// in-process controller and tail it directly. It needs root (the controller
// takes locks under the run path) and a writable kukeon install, which is
// why it is the fallback rather than the default.
func runInProcess(cmd *cobra.Command, logger *slog.Logger, follow bool) error {
	if err := kukshared.RequireRoot("kuke daemon logs"); err != nil {
		return err
	}

	client := resolveClient(cmd, logger)
	defer func() { _ = client.Close() }()

	cellDoc := kukeondCellDoc()
	getRes, err := client.GetCell(cmd.Context(), cellDoc)
	if err != nil {
		return fmt.Errorf("inspect kukeond cell: %w", err)
	}
	if !getRes.MetadataExists {
		return errdefs.ErrHostNotInitialized
	}
	if !lifecycle.IsCellRunning(getRes.Cell) {
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

// resolveDaemonClient returns the client for the socket path. Tests inject
// a fake via MockDaemonClientKey; production dials KUKEON_HOST, the address
// every daemon-routed verb uses (a cell's kukeond-socket mount points it at
// the hard-linked socket).
func resolveDaemonClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mock, ok := cmd.Context().Value(MockDaemonClientKey{}).(kukeonv1.Client); ok {
		return mock, nil
	}
	return kukshared.DaemonClientFromCmd(cmd)
}

// resolveClient returns the in-process kukeonv1.Client used by the fallback
// path. Tests inject a fake via lifecycle.MockClientKey; production builds an
// in-process client so the verb works even when the daemon socket is not
// reachable.
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
