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

// Package log implements the `kuke log` subcommand. It mirrors `kuke
// attach`'s flag/arg shape but instead of opening an interactive sbsh
// session it prints the per-container output stream. By default it
// dumps the current capture/log file contents and exits; pass
// `-f`/`--follow` to tail until SIGINT, matching `kubectl logs` /
// `docker logs` conventions.
//
// Bytes are read through the daemon: the ReadContainerLog RPC returns
// bounded, cursor-addressed chunks of the stream, so `kuke log` works
// from a cell that mounts only the kukeond socket and no /opt/kukeon
// (issue #1537). A full read pages from offset 0 to the end; follow mode
// keeps polling from the returned cursor and starts over from 0 when the
// stream shrinks below it (truncated or replaced). With KUKEON_NO_DAEMON
// the in-process client serves the same method by reading the file
// itself, so there is one code path. Against a kukeond that predates the
// RPC, `kuke log` falls back to resolving the host path with LogContainer
// and opening it directly, which needs the host's /opt/kukeon.
//
// The stream depends on the target container's IO model:
//
//   - Attachable containers route output through sbsh, which writes a
//     tty byte stream to the container's capture file.
//   - Non-Attachable containers (including kukeond) have the containerd
//     runtime shim write stdout/stderr to a log file via cio.LogFile.
//     Implemented per issue #203.
package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey injects a kukeonv1.Client via context for tests.
type MockControllerKey struct{}

// MockTailKey injects a tailFn via context for tests, so the real follow
// loop (which would block on a real file) can be bypassed.
type MockTailKey struct{}

// tailFn opens path and copies its contents to out, then either returns
// immediately after the first dump (follow=false, the default) or keeps
// streaming new bytes until ctx is cancelled (follow=true). Real
// implementation: tailFile.
type tailFn func(ctx context.Context, path string, out io.Writer, follow bool) error

// pollInterval is how long the follow loop waits between reads once it has
// caught up. Polling is intentional: avoiding fsnotify keeps the dependency
// surface small and behaves correctly for sbsh's append-only capture file.
//
//nolint:gochecknoglobals // test seam: tests shorten the follow poll
var pollInterval = 200 * time.Millisecond

// followOutageLimit is how long `kuke log -f` keeps retrying while kukeond
// is unreachable (a restart, an upgrade) before it gives up.
//
//nolint:gochecknoglobals // test seam: tests shorten the outage limit
var followOutageLimit = 2 * time.Minute

// transientDaemonError reports whether err means the request never reached
// kukeond (dial failed, connection dropped), as opposed to an answer from it.
func transientDaemonError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, rpc.ErrShutdown)
}

// TailFn is the exported alias for tailFile-shaped functions: open path,
// dump current contents to out, and optionally follow until ctx is
// cancelled. Sibling commands that share `kuke log`'s streaming semantics
// (e.g. `kuke daemon logs`) accept values of this type for injection.
type TailFn = tailFn

// TailFile re-exports tailFile so sibling commands that share the same
// log-streaming semantics (e.g. `kuke daemon logs`) can dispatch through
// the same dump-and-exit / follow loop instead of duplicating it.
func TailFile(ctx context.Context, path string, out io.Writer, follow bool) error {
	return tailFile(ctx, path, out, follow)
}

// NewLogCmd builds the `kuke log` cobra command.
func NewLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "log <cell>",
		Aliases:       []string{"logs"},
		Short:         "Print a container's stdout/stderr stream (use -f to follow)",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runLog,
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", consts.KukeonDefaultSpaceName, "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", consts.KukeonDefaultStackName, "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("container", "",
		"Container within the cell to read (omit to auto-pick the only non-root container)")
	_ = viper.BindPFlag(config.KUKE_LOG_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))
	cmd.Flags().
		BoolP("follow", "f", false, "Tail the file until SIGINT instead of printing current contents and exiting")
	_ = viper.BindPFlag(config.KUKE_LOG_FOLLOW.ViperKey, cmd.Flags().Lookup("follow"))

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("container", config.CompleteContainerNames)

	return cmd
}

func runLog(cmd *cobra.Command, args []string) error {
	cell := strings.TrimSpace(args[0])
	realm := strings.TrimSpace(viper.GetString(config.KUKE_LOG_REALM.ViperKey))
	space := strings.TrimSpace(viper.GetString(config.KUKE_LOG_SPACE.ViperKey))
	stack := strings.TrimSpace(viper.GetString(config.KUKE_LOG_STACK.ViperKey))
	container := strings.TrimSpace(viper.GetString(config.KUKE_LOG_CONTAINER.ViperKey))
	follow := viper.GetBool(config.KUKE_LOG_FOLLOW.ViperKey)

	if cell == "" {
		return fmt.Errorf("%w (positional cell)", errdefs.ErrCellNameRequired)
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if container == "" {
		container, err = kukeshared.PickContainer(cmd.Context(), client, realm, space, stack, cell,
			func(spec v1beta1.ContainerSpec) bool {
				// `kuke log` accepts non-Attachable too — sbsh-capture
				// for Attachable containers, cio.LogFile for the rest
				// (e.g. kukeond). Only the root container is excluded.
				return !spec.Root
			})
		if err != nil {
			return err
		}
	}

	doc := buildContainerDoc(container, realm, space, stack, cell)
	out := cmd.OutOrStdout()

	ctx := cmd.Context()
	if follow {
		// Install a local cancel scope tied to SIGINT/SIGTERM so Ctrl+C
		// during follow ends the loop cleanly with exit 0. It chains off
		// the caller's ctx so an upstream cancellation still wins.
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
	}

	answered, err := streamContainerLog(ctx, client, doc, out, follow)
	if err == nil {
		return nil
	}
	if !answered && errors.Is(err, errdefs.ErrDaemonMethodUnsupported) {
		// kukeond predates ReadContainerLog: resolve the host path and read
		// it directly, as `kuke log` did before #1537.
		return runViaHostPath(cmd, client, doc, cell, container, out, follow)
	}
	return describeLogError(err, cell, container, stoppedContainerStatus(ctx, client, doc))
}

// streamContainerLog prints the container's stream through ReadContainerLog.
// It reads forward from byte 0 in ContainerLogMaxBytes chunks until it has
// caught up, so the default mode prints the whole current stream; with
// follow it then polls from the returned cursor until ctx is done. When the
// reported Size drops below the cursor the stream was truncated or replaced,
// and the read starts over from 0 instead of stalling. answered reports
// whether at least one chunk arrived, so the caller only falls back to the
// host-path read when the daemon refused the very first request. Once a
// chunk has arrived, a follow survives kukeond going away: it drops the dead
// connection, keeps the cursor and polls until the daemon answers again or
// followOutageLimit passes, printing nothing in between.
func streamContainerLog(
	ctx context.Context, client kukeonv1.Client, doc v1beta1.ContainerDoc, out io.Writer, follow bool,
) (bool, error) {
	var (
		offset      int64
		answered    bool
		outageStart time.Time
	)
	for {
		res, err := client.ReadContainerLog(ctx, doc, kukeonv1.ContainerLogRequest{
			Offset:   offset,
			MaxBytes: kukeonv1.ContainerLogMaxBytes,
		})
		switch {
		case err != nil && ctx.Err() != nil:
			// SIGINT/SIGTERM (or the caller) ended the read: a clean stop.
			return answered, nil
		case err != nil && answered && follow && errors.Is(err, errdefs.ErrContainerLogUnavailable):
			// The stream file vanished mid-follow (container recreated):
			// keep polling and read the new file from its start.
			offset = 0
		case err != nil && answered && follow && transientDaemonError(err):
			// kukeond went away mid-follow (restart, upgrade). Drop the dead
			// connection so the next poll dials again, and keep the cursor.
			if outageStart.IsZero() {
				outageStart = time.Now()
			} else if time.Since(outageStart) > followOutageLimit {
				return answered, fmt.Errorf("kukeond unreachable for %s: %w", followOutageLimit, err)
			}
			_ = client.Close()
		case err != nil:
			if errors.Is(err, errdefs.ErrContainerLogUnavailable) && res.Source != "" {
				return answered, &missingLogFileError{path: res.Source, cause: err}
			}
			return answered, err
		default:
			answered = true
			outageStart = time.Time{}
			if res.Size < offset {
				offset = 0
				continue
			}
			if _, writeErr := out.Write(res.Data); writeErr != nil {
				return answered, writeErr
			}
			offset = res.Next
			if res.More {
				continue
			}
			if !follow {
				return answered, nil
			}
		}
		select {
		case <-ctx.Done():
			return answered, nil
		case <-time.After(pollInterval):
		}
	}
}

// describeLogError adds the cell/container context `kuke log` has always
// reported to the errors a stream read can end with. status is the
// container's status when it has stopped (see stoppedContainerStatus), nil
// otherwise.
func describeLogError(err error, cell, container string, status lazyStatus) error {
	var missing *missingLogFileError
	switch {
	case errors.Is(err, errdefs.ErrContainerNotFound):
		return fmt.Errorf("container %q not found in cell %q: %w", container, cell, err)
	case errors.As(err, &missing):
		return noLogFileError(cell, container, missing.path, status())
	}
	return err
}

// lazyStatus returns the container's status if it has stopped, nil
// otherwise. It is only called when the log file is missing.
type lazyStatus func() *v1beta1.ContainerStatus

// stoppedContainerStatus reads the container from kukeond and returns its
// status when the task is no longer running, so a missing log file is
// explained by how the container ended rather than with "try again" (issue
// #1711). A failed read, or a container still starting or running, yields
// nil and the historical message.
func stoppedContainerStatus(ctx context.Context, client kukeonv1.Client, doc v1beta1.ContainerDoc) lazyStatus {
	return func() *v1beta1.ContainerStatus {
		res, err := client.GetContainer(ctx, doc)
		if err != nil || !res.ContainerExists {
			return nil
		}
		st := res.Container.Status
		switch st.State {
		case v1beta1.ContainerStateStopped, v1beta1.ContainerStateExited,
			v1beta1.ContainerStateError, v1beta1.ContainerStateFailed:
			return &st
		case v1beta1.ContainerStatePending, v1beta1.ContainerStateReady, v1beta1.ContainerStatePaused,
			v1beta1.ContainerStatePausing, v1beta1.ContainerStateUnknown, v1beta1.ContainerStateNotCreated:
		}
		return nil
	}
}

// missingLogFileError carries the daemon-side path of a stream file that
// does not exist yet, so the message can name it.
type missingLogFileError struct {
	path  string
	cause error
}

func (e *missingLogFileError) Error() string { return e.cause.Error() }
func (e *missingLogFileError) Unwrap() error { return e.cause }

// noLogFileError is the message `kuke log` prints when the container exists
// but its stream file has not been opened. For a container that has stopped
// (stopped non-nil) the file will never appear, so the message says the
// container exited before its terminal started and names the supervisor
// phase and the reason kukeond derived.
func noLogFileError(cell, container, path string, stopped *v1beta1.ContainerStatus) error {
	if stopped == nil {
		return fmt.Errorf(
			"cell %q container %q has no log file at %s (the runtime shim has not opened it yet — try again after the container produces output)",
			cell,
			container,
			path,
		)
	}
	var details []string
	if stopped.Supervisor != nil && stopped.Supervisor.Phase != "" {
		details = append(details, "phase "+string(stopped.Supervisor.Phase))
	}
	switch {
	case stopped.Reason != "" && stopped.Message != "":
		details = append(details, stopped.Reason+": "+stopped.Message)
	case stopped.Reason != "":
		details = append(details, stopped.Reason)
	default:
		details = append(details, fmt.Sprintf("exit code %d", stopped.ExitCode))
	}
	return fmt.Errorf(
		"cell %q container %q exited before its terminal started, so it has no log file at %s (%s)",
		cell,
		container,
		path,
		strings.Join(details, "; "),
	)
}

// runViaHostPath is the pre-#1537 read path, kept for a kukeond that does not
// serve ReadContainerLog: resolve the host path of the stream with
// LogContainer and open it directly. It needs the host's /opt/kukeon.
func runViaHostPath(
	cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.ContainerDoc,
	cell, container string, out io.Writer, follow bool,
) error {
	status := stoppedContainerStatus(cmd.Context(), client, doc)
	result, err := client.LogContainer(cmd.Context(), doc)
	if err != nil {
		return describeLogError(err, cell, container, status)
	}
	streamPath := result.HostCapturePath
	if streamPath == "" {
		streamPath = result.HostLogPath
	}
	if streamPath == "" {
		return fmt.Errorf("daemon returned no stream path for container %q", container)
	}

	tail := resolveTail(cmd)
	if tailErr := tail(cmd.Context(), streamPath, out, follow); tailErr != nil {
		if errors.Is(tailErr, os.ErrNotExist) {
			return noLogFileError(cell, container, streamPath, status())
		}
		return tailErr
	}
	return nil
}

// tailFile opens path and streams its bytes to out. With follow=false
// (the default for `kuke log`) it dumps the current contents and
// returns. With follow=true it dumps and then polls for new bytes until
// ctx is cancelled (SIGINT/SIGTERM). On cancellation it returns nil so
// `kuke log -f` exits 0 — the user pressing Ctrl+C is a benign session
// end, not a failure.
func tailFile(ctx context.Context, path string, out io.Writer, follow bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, copyErr := io.Copy(out, f); copyErr != nil {
		return copyErr
	}
	if !follow {
		return nil
	}

	// Install a local cancel scope tied to SIGINT/SIGTERM so Ctrl+C
	// during follow drains the goroutine cleanly. We chain off the
	// caller's ctx so an upstream cancellation still wins.
	followCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-followCtx.Done():
			return nil
		case <-ticker.C:
			// io.Copy from the same file handle resumes at the
			// previous offset; sbsh appends to the capture file so
			// any new bytes since the last copy are now between EOF
			// and the new write boundary.
			if _, copyErr := io.Copy(out, f); copyErr != nil {
				return copyErr
			}
		}
	}
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func resolveTail(cmd *cobra.Command) tailFn {
	if mock, ok := cmd.Context().Value(MockTailKey{}).(tailFn); ok {
		return mock
	}
	return tailFile
}

func buildContainerDoc(name, realm, space, stack, cell string) v1beta1.ContainerDoc {
	return v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   name,
			Labels: make(map[string]string),
		},
		Spec: v1beta1.ContainerSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
			CellID:  cell,
		},
	}
}
