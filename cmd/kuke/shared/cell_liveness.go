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

package shared

import (
	"fmt"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// GuardCellTaskLiveness refuses to attach to a cell whose persisted state
// is anything other than "Ready with a live root-container task". It is
// the single shared guard backing `kuke run`'s Ready short-circuit and
// `kuke attach` — both entry points hand a host socket path to the
// in-process sbsh attach loop, and both must refuse the same divergence
// classes.
//
// Five terminal answers, each with a state-appropriate operator pointer:
//
//   - !MetadataExists → the cell has never been created (or has already
//     been deleted). Direct the operator at `kuke run` with the standard
//     profile + name flags so attach is preceded by a creation, not by
//     a confusing ListContainers empty.
//   - Ready + RootContainerTaskRunning → nil (attach proceeds).
//   - Ready + !RootContainerTaskRunning → the post-reboot divergence
//     #683 was originally written for: the on-disk metadata records
//     Ready but containerd has lost the backing task. Recovery is a
//     delete-then-rerun so the next CreateCell starts from a clean
//     containerd slate.
//   - Stopped / Exited / Error → the work container is not running
//     (wind-down reaped it, it exited cleanly, or the workload crashed),
//     so the attach socket inode is orphaned. Recovery is `kuke start` —
//     the cell metadata is intact, and Error is restartable without a
//     delete (#1274), so a restart re-binds kuketty against a fresh inode.
//   - Pending / Failed / Unknown → no clean attach path. Same recovery
//     as Ready+task-dead (delete-then-rerun); a Failed cell is sticky
//     per the reconciler so only a delete clears it.
//
// `kuke run`'s switch in runExistingCell calls this from the Ready
// branch only — its Stopped / Failed / Unknown branches route the
// operator through their own verbs (StartCell, or the
// delete-then-rerun pointer) — so the broader switch here is a
// strict superset that only `kuke attach` exercises in full.
func GuardCellTaskLiveness(get kukeonv1.GetCellResult, cellName string) error {
	if !get.MetadataExists {
		return fmt.Errorf(
			"cell %q does not exist; create it first with "+
				"`kuke run -p <profile> --name %s`",
			cellName, cellName,
		)
	}
	switch get.Cell.Status.State {
	case v1beta1.CellStateReady, v1beta1.CellStateDegraded:
		// Degraded (#1233 follow-up) is a live cell — root/sandbox up, a non-root
		// workload down or restarting — so it guards the same as Ready: the root
		// task must still be present in containerd. Attaching to a down workload
		// container then fails at the container level with its own diagnostic.
		if get.RootContainerTaskRunning {
			return nil
		}
		return fmt.Errorf(
			"cell %q is recorded %s but its containers are gone from containerd "+
				"(kukeon metadata and containerd have diverged); "+
				"delete it with `kuke delete cell %s` before re-running",
			cellName,
			get.Cell.Status.State.String(),
			cellName,
		)
	case v1beta1.CellStateStopped, v1beta1.CellStateExited, v1beta1.CellStateError:
		// Stopped (operator stop/kill), Exited (clean self-exit, #1267) and Error
		// (workload crash, #1267) all leave no live work container — point the
		// operator at `kuke start`. Error is restartable without a delete (#1274):
		// the daemon's StartCell recovers it through a recreate-style path (stop ->
		// recreate containers -> start) that also winds down the sticky leftover
		// root, so the delete-then-rerun pointer the default branch prints would be
		// stale advice for a crashed cell.
		return fmt.Errorf(
			"cell %q is in state %s (no work container is running); "+
				"start it first with `kuke start %s`",
			cellName, get.Cell.Status.State.String(), cellName,
		)
	default:
		return fmt.Errorf(
			"cell %q is in state %s; "+
				"delete it with `kuke delete cell %s` before re-running",
			cellName, get.Cell.Status.State.String(), cellName,
		)
	}
}
