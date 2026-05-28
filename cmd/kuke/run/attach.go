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
	"time"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/attach"
	"github.com/spf13/cobra"
)

// attachPingRetryBudget bounds the total wall-clock time runWithPingRetry
// will spend re-invoking attach.Run when sbsh's per-attempt 3 s ping
// window keeps firing before kuketty's Serve() accept loop has come
// up. Each attempt re-pays the 3 s window inside sbsh, so a 10 s budget
// covers two retries past the initial attempt on the slow-host race
// that #926 documents.
//
//nolint:gochecknoglobals // test seam for the production retry budget
var attachPingRetryBudget = 10 * time.Second

// attachPingRetryBackoff is the sleep between retry attempts. Kept
// short on purpose: the dominant wait is sbsh's per-attempt 3 s ping
// window, not this gap. The backoff exists only to avoid a tight loop
// on the chance the prior attempt failed for a reason that resolves
// in microseconds.
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

// runAttachLoop resolves the per-container sbsh control socket via the
// daemon's AttachContainer RPC and drives the in-process attach loop.
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

	result, err := client.AttachContainer(cmd.Context(), containerDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrAttachNotSupported) {
			return false, fmt.Errorf("container %q in cell %q is not attachable: %w",
				container, doc.Metadata.Name, err)
		}
		if errors.Is(err, errdefs.ErrContainerNotFound) {
			return false, fmt.Errorf("container %q not found in cell %q: %w",
				container, doc.Metadata.Name, err)
		}
		return false, err
	}
	if result.HostSocketPath == "" {
		return false, fmt.Errorf("daemon returned empty HostSocketPath for container %q", container)
	}

	run := resolveRun(cmd)
	runErr := runWithPingRetry(cmd.Context(), run, attach.Options{
		SocketPath: result.HostSocketPath,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
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

func resolveRun(cmd *cobra.Command) runFn {
	if mock, ok := cmd.Context().Value(MockRunKey{}).(runFn); ok {
		return mock
	}
	return attach.Run
}

// runWithPingRetry retries run() while the failure looks like sbsh's
// per-attempt 3 s control-socket ping deadline firing before kuketty
// has entered Serve()'s Accept loop. The dial(2) succeeds as soon as
// kuketty's bind(2) lands (the kernel queues the connect into the
// listening socket's backlog), so the post-StartCell attach can race
// kuketty's Serve() boot — a window distinct from the EACCES one
// #918 closed (#926).
//
// Behaviour:
//   - non-ping-timeout errors propagate immediately; the existing
//     ClassifyAttachExit branch handles them.
//   - ping-timeout errors are retried with attachPingRetryBackoff
//     between attempts until either run() succeeds, the outer ctx
//     is cancelled (returns ctx.Err), or attachPingRetryBudget is
//     exhausted (returns the last runErr wrapped with
//     errdefs.ErrAttachPingTimeout so callers can errors.Is the
//     timeout class without string-matching sbsh's wrap).
func runWithPingRetry(ctx context.Context, run runFn, opts attach.Options) error {
	deadline := time.Now().Add(attachPingRetryBudget)
	var lastErr error
	for {
		lastErr = run(ctx, opts)
		if !isAttachPingTimeout(ctx, lastErr) {
			return lastErr
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: %w", errdefs.ErrAttachPingTimeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(attachPingRetryBackoff):
		}
	}
}

// isAttachPingTimeout reports whether err is the sbsh "ping failed:
// context deadline exceeded" class that runWithPingRetry treats as a
// readiness-handshake race rather than a hard failure. The classifier
// is conservative on purpose:
//
//   - err must be non-nil (a clean detach is not a retry condition);
//   - ctx must not be done (a cancelled outer ctx surfaces as
//     DeadlineExceeded on the inner WithTimeout child, but is the
//     operator's ^C not kuketty's boot race — don't retry);
//   - context.DeadlineExceeded must appear in the chain (the only
//     bounded-timeout in sbsh's attach.Run setup is the 3 s ping
//     window in clientrunner/io.go dialTerminalCtrlSocket).
func isAttachPingTimeout(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}
