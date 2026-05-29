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

// Package lifecycle holds the scaffolding shared by the `kuke daemon`
// lifecycle verbs (start, stop, kill, restart, reset, logs). Each verb used
// to maintain its own near-identical copy of these helpers; centralising
// them prevents silent drift of the "running" definition, the
// SIGTERM → SIGKILL grace period, and the test-injection key.
package lifecycle

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockClientKey injects a kukeonv1.Client (typically a fake) via the command
// context so unit tests can drive the daemon-lifecycle verbs without a real
// controller. The key lives in this package so every verb's resolveClient
// agrees on a single context-value identity, and the type stays off each
// verb's production surface.
type MockClientKey struct{}

// DefaultTimeout is the SIGTERM grace period before SIGKILL escalation,
// shared by `kuke daemon stop`/`restart`/`reset`. Centralised so the three
// verbs cannot drift on what "graceful" means.
const DefaultTimeout = 10 * time.Second

// DefaultReachableTimeout is the dial budget the lifecycle verbs use when
// probing the kukeond socket. Short on purpose: the only acceptable failure
// mode here is "the socket is dead" — a healthy daemon answers in <1ms over
// the local unix socket, so 500ms is generous and a hung accept still
// surfaces fast enough that the operator notices.
const DefaultReachableTimeout = 500 * time.Millisecond

// IsCellRunning treats the cell as live if any container reports Ready, or
// if the persisted cell state is Ready. Every `kuke daemon` lifecycle verb
// uses this to agree on what "running" means; the controller's StartCell
// path uses the same definition so the in-process and stateful views stay
// aligned even when external crashes leave the persisted state stale.
func IsCellRunning(cell v1beta1.CellDoc) bool {
	for _, c := range cell.Status.Containers {
		if c.State == v1beta1.ContainerStateReady {
			return true
		}
	}
	return cell.Status.State == v1beta1.CellStateReady
}

// ReachableProbe reports whether a kukeond socket at socketPath answers a
// short-budget dial. The lifecycle verbs use it together with IsCellRunning
// to disambiguate the two staleness directions: persisted state says Ready
// while the daemon has been externally killed (socket=down → start must
// re-bring it up, stop must not silently no-op), and persisted state lags
// behind a live daemon (socket=up → stop/kill must not silently no-op).
type ReachableProbe func(ctx context.Context, socketPath string, timeout time.Duration) bool

// ReachableProbeKey injects a custom ReachableProbe via the command context
// so unit tests can drive the verbs without a real listening socket. Same
// pattern as MockClientKey; production verbs call ResolveReachableProbe to
// pick either the injected fake or the net.Dialer-backed default.
type ReachableProbeKey struct{}

// IsDaemonReachable is the production reachability probe: a bounded
// net.Dialer-backed dial of the kukeond unix socket. Empty socketPath is
// treated as unreachable so a missing config does not masquerade as "up".
// The dial errors are intentionally not surfaced to the caller — the only
// question this answers is "did the socket accept a connection within the
// budget?", and any error (ENOENT, ECONNREFUSED, ETIMEDOUT) maps to no.
func IsDaemonReachable(ctx context.Context, socketPath string, timeout time.Duration) bool {
	if socketPath == "" {
		return false
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", socketPath)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ResolveReachableProbe picks the probe a lifecycle verb uses. Tests inject
// a fake via ReachableProbeKey; production returns IsDaemonReachable. The
// indirection lets every verb call the same one-liner without duplicating
// the context-value lookup or hard-coding the default.
func ResolveReachableProbe(cmd *cobra.Command) ReachableProbe {
	if probe, ok := cmd.Context().Value(ReachableProbeKey{}).(ReachableProbe); ok && probe != nil {
		return probe
	}
	return IsDaemonReachable
}

// ResolveSocketPath returns the kukeond socket path the reachability probe
// should dial. Honours an explicit KUKEOND_SOCKET (env or viper) and falls
// back to the registered default. Centralised so every lifecycle verb agrees
// on which socket counts as the source of truth.
func ResolveSocketPath() string {
	if path := viper.GetString(config.KUKEOND_SOCKET.ViperKey); path != "" {
		return path
	}
	return config.KUKEOND_SOCKET.Default
}

// StopPhase runs the graceful StopCell → SIGKILL escalation used by
// `kuke daemon stop`/`restart`/`reset`. The inner StopCell call runs in a
// goroutine under a context derived from `cmd.Context()` via
// context.WithCancel; on timeout, the derived context is cancelled before
// KillCell is invoked so the orphan StopCell observes the cancellation
// instead of continuing against an unmodified parent context. KillCell uses
// the original `cmd.Context()` so the escalation itself is not affected by
// the stop cancellation.
func StopPhase(
	cmd *cobra.Command,
	client kukeonv1.Client,
	doc v1beta1.CellDoc,
	timeout time.Duration,
) error {
	stopCtx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	type stopOutcome struct {
		res kukeonv1.StopCellResult
		err error
	}
	done := make(chan stopOutcome, 1)
	go func() {
		res, sErr := client.StopCell(stopCtx, doc)
		done <- stopOutcome{res: res, err: sErr}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			return fmt.Errorf("stop kukeond cell: %w", out.err)
		}
		if !out.res.Stopped {
			return fmt.Errorf("stop kukeond cell: %w", errdefs.ErrControllerNoChange)
		}
		cmd.Printf(
			"kukeond stopped (cell %q in realm %q)\n",
			consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
		// StopCell can return Stopped=true while a container task survives —
		// the runner's per-container StopContainer errors are logged-and-
		// continue rather than aggregated into the StopCell result (see
		// internal/controller/runner/stop.go), so a containerd shim that
		// rejects SIGTERM or a stop call that returns before the task exits
		// surfaces here as "successful stop" with a still-Ready container.
		// `kuke daemon reset` would then proceed to DeleteCell against a
		// surviving kukeond task, leaving the operator's daemon binary
		// frozen on the prior build across a re-bootstrap. Issue #868.
		// Verify the post-stop state and escalate to KillCell if a task
		// remains.
		return verifyStoppedOrEscalate(cmd, client, doc)
	case <-time.After(timeout):
		cancel()
		killRes, killErr := client.KillCell(cmd.Context(), doc)
		if killErr != nil {
			return fmt.Errorf(
				"kill kukeond cell after %s grace period expired: %w",
				timeout, killErr,
			)
		}
		if !killRes.Killed {
			return fmt.Errorf("kill kukeond cell: %w", errdefs.ErrControllerNoChange)
		}
		cmd.Printf(
			"kukeond force-killed after %s grace period expired (cell %q in realm %q)\n",
			timeout, consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
		return nil
	}
}

// verifyStoppedOrEscalate re-reads the cell after a successful StopCell, and
// if a container task is still Ready, drives the SIGKILL escalation that
// would otherwise have fired on grace-period timeout. The post-kill GetCell
// is the second gate: if the task remains Ready even after KillCell claimed
// to terminate it, the caller must surface a hard error rather than allow
// the subsequent DeleteCell to race against a live shim.
func verifyStoppedOrEscalate(
	cmd *cobra.Command,
	client kukeonv1.Client,
	doc v1beta1.CellDoc,
) error {
	getRes, getErr := client.GetCell(cmd.Context(), doc)
	if getErr != nil {
		return fmt.Errorf("re-inspect kukeond cell after stop: %w", getErr)
	}
	if !getRes.MetadataExists || !IsCellRunning(getRes.Cell) {
		return nil
	}

	killRes, killErr := client.KillCell(cmd.Context(), doc)
	if killErr != nil {
		return fmt.Errorf(
			"escalate to kill kukeond cell after stop returned success but task survived: %w",
			killErr,
		)
	}
	if !killRes.Killed {
		return fmt.Errorf(
			"escalate to kill kukeond cell after stop returned success but task survived: %w",
			errdefs.ErrControllerNoChange,
		)
	}

	verifyRes, verifyErr := client.GetCell(cmd.Context(), doc)
	if verifyErr != nil {
		return fmt.Errorf("re-inspect kukeond cell after escalated kill: %w", verifyErr)
	}
	if verifyRes.MetadataExists && IsCellRunning(verifyRes.Cell) {
		return fmt.Errorf(
			"kukeond cell %q in realm %q still reports a running container after stop and escalated kill",
			consts.KukeSystemCellName, consts.KukeSystemRealmName,
		)
	}
	cmd.Printf(
		"kukeond force-killed after stop returned success but task survived (cell %q in realm %q)\n",
		consts.KukeSystemCellName, consts.KukeSystemRealmName,
	)
	return nil
}
