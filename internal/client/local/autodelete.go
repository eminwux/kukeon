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

package local

import (
	"context"
	"fmt"
	"log/slog"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// WatchCellAutoDelete spawns a background goroutine that waits for the
// cell's root containerd task to exit, then best-effort kills and deletes
// the cell. Returns immediately — the wait happens in the goroutine.
//
// Cleanup is scoped to the cell only; it never cascades to stack/space/realm.
// Best-effort: if any step (Wait, Kill, Delete) fails it is logged and the
// goroutine exits, leaving the cell behind for the operator (or the future
// reconciliation loop in #161) to sweep up.
//
// Lifetime is bound to bgCtx — when bgCtx is cancelled (daemon shutdown),
// the wait is abandoned and no cleanup runs. The caller (the daemon) owns
// passing a long-lived context; the CLI's --no-daemon path would tie the
// watcher to a short-lived process, so `--rm --no-daemon` is rejected at
// the flag layer.
func (c *Client) WatchCellAutoDelete(
	bgCtx context.Context,
	logger *slog.Logger,
	doc v1beta1.CellDoc,
) error {
	internal, _, err := apischeme.NormalizeCell(doc)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	exitChan, err := c.ctrl.WaitCellRootTaskExit(bgCtx, internal)
	if err != nil {
		return fmt.Errorf("install --rm watcher: %w", err)
	}

	go c.runAutoDeleteWatcher(bgCtx, logger, doc, exitChan)
	return nil
}

func (c *Client) runAutoDeleteWatcher(
	bgCtx context.Context,
	logger *slog.Logger,
	doc v1beta1.CellDoc,
	exitChan <-chan containerd.ExitStatus,
) {
	cellLog := []any{
		"cell", doc.Metadata.Name,
		"realm", doc.Spec.RealmID,
		"space", doc.Spec.SpaceID,
		"stack", doc.Spec.StackID,
	}

	select {
	case <-bgCtx.Done():
		logger.InfoContext(bgCtx, "--rm watcher cancelled before task exit", cellLog...)
		return
	case exit, ok := <-exitChan:
		if !ok {
			logger.WarnContext(bgCtx, "--rm watcher: exit channel closed without status", cellLog...)
			return
		}
		fields := append([]any{"exit_code", exit.ExitCode()}, cellLog...)
		logger.InfoContext(bgCtx, "--rm watcher: root task exited, cleaning up cell", fields...)
	}

	// Defensive kill: the task may already be gone, but other peers in the
	// cell could still be running. KillCell is idempotent on already-stopped
	// containers and bounds the time DeleteCell needs to acquire its locks.
	if _, killErr := c.KillCell(bgCtx, doc); killErr != nil {
		logger.WarnContext(bgCtx, "--rm watcher: KillCell failed, continuing to delete",
			append([]any{"error", killErr}, cellLog...)...)
	}

	if _, delErr := c.DeleteCell(bgCtx, doc); delErr != nil {
		logger.ErrorContext(bgCtx, "--rm watcher: DeleteCell failed; cell may be orphaned",
			append([]any{"error", delErr}, cellLog...)...)
		return
	}

	logger.InfoContext(bgCtx, "--rm watcher: cell auto-deleted", cellLog...)
}
