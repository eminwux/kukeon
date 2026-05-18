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
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
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
		return nil
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
