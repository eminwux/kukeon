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

package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/attach"
	"github.com/spf13/cobra"
)

// attachPingRetryBudget bounds the total wall-clock time runWithPingRetry
// will spend re-invoking attach.Run while the failure looks like a
// kuketty boot-race class — either sbsh's per-attempt 3 s ping window
// firing (#926) or dial(2) returning EACCES/ENOENT against a stale
// pre-fix tty socket (#933). Each ping-deadline attempt re-pays the
// 3 s window inside sbsh, so a 10 s budget covers two retries past
// the initial attempt on the slow-host race. The stale-socket window
// is sub-second once kuketty is up, so the same budget comfortably
// absorbs it.
//
//nolint:gochecknoglobals // test seam for the production retry budget
var attachPingRetryBudget = 10 * time.Second

// attachPingRetryBackoff is the sleep between retry attempts. Kept
// short on purpose: the dominant wait is sbsh's per-attempt 3 s ping
// window, not this gap. The backoff exists only to avoid a tight loop
// on the chance the prior attempt failed for a reason that resolves
// in microseconds — including the stale-socket Remove→Listen gap
// (#933), which is sub-millisecond in practice.
//
//nolint:gochecknoglobals // test seam for the production retry backoff
var attachPingRetryBackoff = 200 * time.Millisecond

// MockRunKey is used to inject a mock runFn via context in tests, so the
// real pkg/attach.Run (which would open a TTY and connect to a real
// control socket) is bypassed.
type MockRunKey struct{}

// runFn drives the in-process sbsh attach loop. Returns nil on a clean
// detach / context cancel and any unrecoverable controller error otherwise.
type runFn func(ctx context.Context, opts attach.Options) error

// pickAttachTarget resolves the container the post-start attach should
// connect to, applying the documented precedence:
//
//  1. explicit --container flag, when set
//  2. cell.tty.default, when the spec sets it
//  3. the first container in declaration order with attachable=true
//
// Errors when no candidate exists or the explicit flag names a non-attachable
// or unknown container; the error message names the attachable containers so
// the operator can re-run with a valid --container.
func pickAttachTarget(spec v1beta1.CellSpec, cellName, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		for _, c := range spec.Containers {
			if c.ID == explicit {
				if !c.Attachable {
					return "", fmt.Errorf(
						"container %q in cell %q is not attachable: %w; available attachable containers: %s",
						explicit, cellName, errdefs.ErrAttachNotSupported,
						formatAttachableList(spec),
					)
				}
				return c.ID, nil
			}
		}
		return "", fmt.Errorf(
			"container %q not found in cell %q: %w; available attachable containers: %s",
			explicit, cellName, errdefs.ErrContainerNotFound,
			formatAttachableList(spec),
		)
	}

	if spec.Tty != nil {
		if name := strings.TrimSpace(spec.Tty.Default); name != "" {
			return name, nil
		}
	}

	for _, c := range spec.Containers {
		if c.Attachable {
			return c.ID, nil
		}
	}

	return "", fmt.Errorf(
		"%w (cell %q); declare 'attachable: true' on one container or pass -d/--detach",
		errdefs.ErrAttachNoCandidate, cellName,
	)
}

// formatAttachableList returns a comma-separated list of attachable container
// IDs in the cell, or "<none>" when there are zero. Used to point operators
// at a valid --container value when their explicit choice is rejected.
func formatAttachableList(spec v1beta1.CellSpec) string {
	names := make([]string, 0, len(spec.Containers))
	for _, c := range spec.Containers {
		if c.Attachable {
			names = append(names, c.ID)
		}
	}
	if len(names) == 0 {
		return "<none>"
	}
	return strings.Join(names, ", ")
}

// runAttachLoop drives the in-process attach loop over control-socket
// connections kukeond dials and passes back (ConnectContainerTerminal).
// Returns the tri-state used by the --rm cleanup decision in
// attachAndMaybeAutoDelete:
//
//   - detached=true,  err=nil — operator pressed ^]^] (or peer issued
//     a Detach RPC). Cell must stay alive.
//   - detached=false, err=nil — peer dropped the connection (workload
//     exited / hung up). Surface exit 0; --rm fires KillCell so a
//     long-lived root does not pin the cell.
//   - detached=false, err≠nil — pre-attach setup error or unrecoverable
//     controller error. Surface to the user; --rm still fires KillCell
//     because a half-detached session would otherwise leak the cell.
func runAttachLoop(
	cmd *cobra.Command,
	client kukeonv1.Client,
	doc v1beta1.CellDoc,
	container string,
) (bool, error) {
	containerDoc := v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   container,
			Labels: make(map[string]string),
		},
		Spec: v1beta1.ContainerSpec{
			ID:      container,
			RealmID: doc.Spec.RealmID,
			SpaceID: doc.Spec.SpaceID,
			StackID: doc.Spec.StackID,
			CellID:  doc.Metadata.Name,
		},
	}

	// Each attempt preflights a daemon-passed connection (#1535): kukeond
	// enforces the Attachable / task-liveness gates and dials the socket, so
	// a boot-race dial failure comes back as a typed sentinel the retry
	// classifier can errors.Is, and the attach loop then dials through the
	// same RPC.
	dialer := kukeshared.NewTerminalDialer(client, containerDoc)
	defer dialer.Close()
	run := resolveRun(cmd)
	runErr := runWithPingRetry(cmd.Context(), func(ctx context.Context) (string, error) {
		opts := attach.Options{
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}
		if err := dialer.Prepare(ctx, &opts); err != nil {
			return "", wrapAttachGateError(err, container, doc.Metadata.Name)
		}
		return opts.SocketPath, run(ctx, opts)
	})
	switch kukeshared.ClassifyAttachExit(runErr) {
	case kukeshared.AttachExitDetached:
		return true, nil
	case kukeshared.AttachExitPeerClosed:
		return false, nil
	case kukeshared.AttachExitError:
		return false, runErr
	default:
		return false, runErr
	}
}

// wrapAttachGateError names the container and cell on the gate refusals the
// preflight surfaces, matching `kuke attach`'s wording.
func wrapAttachGateError(err error, container, cell string) error {
	switch {
	case errors.Is(err, errdefs.ErrAttachNotSupported):
		return fmt.Errorf("container %q in cell %q is not attachable: %w", container, cell, err)
	case errors.Is(err, errdefs.ErrContainerNotFound):
		return fmt.Errorf("container %q not found in cell %q: %w", container, cell, err)
	default:
		return err
	}
}

func resolveRun(cmd *cobra.Command) runFn {
	if mock, ok := cmd.Context().Value(MockRunKey{}).(runFn); ok {
		return mock
	}
	return attach.Run
}

// attachAttempt performs one attach attempt: prepare the connection route
// and run the attach loop. It returns the socket path the attempt dialed
// directly — empty on the daemon-passed route, set only on the fallback to
// AttachContainer against an older kukeond — so the classifier can stat it.
type attachAttempt func(ctx context.Context) (socketPath string, err error)

// runWithPingRetry retries attempt() while the failure looks like a
// kuketty boot-race class — either sbsh's per-attempt 3 s ping
// deadline firing before kuketty has entered Serve()'s Accept loop
// (#926), or the control socket being absent / not yet re-bound when
// it is dialed (#933). Both classes share the same retry budget and
// backoff: the post-StartCell attach can race either window, and a
// kuketty whose stale socket has been replaced does not re-enter the
// window on subsequent runs (the race window collapses to the
// sub-millisecond Remove→Listen gap inside kuketty's init path).
//
// The socket is dialed by kukeond (ConnectContainerTerminal, #1535), so
// dial failures arrive as typed sentinels — errdefs.ErrAttachStaleSocket
// or errdefs.ErrAttachPermissionDenied — rather than a raw errno the
// client could pair with a stat of a path it may not be able to see.
//
// Behaviour:
//   - non-boot-race errors propagate immediately; the existing
//     ClassifyAttachExit branch handles them.
//   - boot-race errors are retried with attachPingRetryBackoff
//     between attempts until either an attempt succeeds, the outer ctx
//     is cancelled (returns ctx.Err), or attachPingRetryBudget is
//     exhausted (returns the last error carrying the matching
//     sentinel — errdefs.ErrAttachPingTimeout for the ping-deadline
//     class, errdefs.ErrAttachStaleSocket for the absent-socket
//     class — so callers can errors.Is either class without
//     string-matching sbsh's wrap).
//   - a socket that is present but not dialable is a permanent
//     wrong-mode condition, not a race: errdefs.ErrAttachPermissionDenied
//     surfaces immediately rather than burning the full budget (#1170).
func runWithPingRetry(ctx context.Context, attempt attachAttempt) error {
	deadline := time.Now().Add(attachPingRetryBudget)
	var lastErr error
	for {
		var socketPath string
		socketPath, lastErr = attempt(ctx)
		sentinel, retry := attachBootRaceSentinel(ctx, lastErr, socketPath)
		if sentinel == nil {
			return lastErr
		}
		if !retry || !time.Now().Before(deadline) {
			// Permanent failure (a present socket with the wrong mode), or
			// the budget is spent: surface the matching sentinel.
			return withSentinel(sentinel, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(attachPingRetryBackoff):
		}
	}
}

// withSentinel wraps err with sentinel unless the chain already carries it
// (a typed dial failure from kukeond does), so the message does not stutter.
func withSentinel(sentinel, err error) error {
	if errors.Is(err, sentinel) {
		return err
	}
	return fmt.Errorf("%w: %w", sentinel, err)
}

// attachBootRaceSentinel classifies err for runWithPingRetry, returning
// the matching errdefs sentinel plus whether the failure is retryable.
// A nil sentinel means err is not a recognised boot-race / socket class
// and should propagate unchanged; a non-nil sentinel with retry=false
// is a permanent failure runWithPingRetry surfaces immediately rather
// than waiting out the budget. The classifier is conservative on purpose:
//
//   - err must be non-nil (a clean detach is not a retry condition);
//   - ctx must not be done (a cancelled outer ctx surfaces as
//     DeadlineExceeded on the inner WithTimeout child, but is the
//     operator's ^C not kuketty's boot race — don't retry);
//   - errdefs.ErrAttachPermissionDenied (kukeond found the socket present
//     but could not dial it) is permanent: retry=false (#1170);
//   - errdefs.ErrAttachStaleSocket (kukeond found the socket absent, or
//     absent-on-stat after EACCES) retries (#933);
//   - context.DeadlineExceeded in the chain maps to ErrAttachPingTimeout
//     and retries (the only bounded-timeout in sbsh's attach.Run setup
//     is the 3 s ping window in clientrunner/io.go dialTerminalCtrlSocket);
//   - raw syscall.ENOENT / syscall.EACCES only reach here on the fallback
//     to AttachContainer against an older kukeond, where the client dials
//     socketPath itself; they keep the pre-#1535 classification, splitting
//     EACCES by stat(2) on socketPath.
func attachBootRaceSentinel(ctx context.Context, err error, socketPath string) (error, bool) {
	if err == nil {
		return nil, false
	}
	if ctx.Err() != nil {
		return nil, false
	}
	switch {
	case errors.Is(err, errdefs.ErrAttachPermissionDenied):
		return errdefs.ErrAttachPermissionDenied, false
	case errors.Is(err, errdefs.ErrAttachStaleSocket):
		return errdefs.ErrAttachStaleSocket, true
	case errors.Is(err, context.DeadlineExceeded):
		return errdefs.ErrAttachPingTimeout, true
	case errors.Is(err, syscall.ENOENT):
		return errdefs.ErrAttachStaleSocket, true
	case errors.Is(err, syscall.EACCES):
		if socketPresent(socketPath) {
			return errdefs.ErrAttachPermissionDenied, false
		}
		return errdefs.ErrAttachStaleSocket, true
	}
	return nil, false
}

// socketPresent reports whether socketPath currently resolves to an
// existing inode. Used by attachBootRaceSentinel to tell a permanent
// wrong-mode socket (present + EACCES on connect) from the transient
// pre-fix stale-socket window (absent + EACCES/ENOENT). Any stat error
// other than a clean success is treated as "not present" so the
// classifier falls back to the conservative retry path rather than
// failing fast on an ambiguous signal (e.g. EACCES walking the parent
// directory).
func socketPresent(socketPath string) bool {
	if socketPath == "" {
		return false
	}
	_, err := os.Stat(socketPath)
	return err == nil
}

// AttachTarget is the container `kuke run -a` attaches to after a start when
// no --container is given: the cell's tty default, else its first attachable
// container (`kuke create agent -a`, issue #1692).
func AttachTarget(spec v1beta1.CellSpec, cellName string) (string, error) {
	return pickAttachTarget(spec, cellName, "")
}

// AttachCell attaches the terminal to one attachable container of a running
// cell and returns when the session ends: detached=true when the operator
// detached (^]^]) and the cell is still running, false when the workload
// ended. It is the attach half of `kuke run`, for commands that start a cell
// of their own (`kuke connector login`).
func AttachCell(cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.CellDoc, container string) (bool, error) {
	return runAttachLoop(cmd, client, doc, container)
}
