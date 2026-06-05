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

package controller

import (
	"fmt"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/diskpressure"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// ReconcileResult summarizes a single pass of the daemon's background
// cell-reconciliation loop. Counts are scoped to cells (v1 of #161 is
// cell-only); per-pass errors are collected so the loop can keep ticking.
type ReconcileResult struct {
	CellsScanned int
	CellsUpdated int
	// CellsDeleted counts cells the reconciler removed during the pass
	// because Spec.AutoDelete=true and the root container's task had
	// exited. Tracked separately from CellsUpdated so callers can tell
	// "state flip persisted" apart from "cell is gone".
	CellsDeleted int
	CellsErrored int
	Errors       []string
}

// ReconcileCells walks every realm/space/stack and reconciles each cell's
// status against observed container state. Errors at any level are logged
// and recorded in Errors; the walk continues so a single bad cell does not
// silence the rest of the host.
func (b *Exec) ReconcileCells() (ReconcileResult, error) {
	result := ReconcileResult{Errors: []string{}}

	realms, err := b.runner.ListRealms()
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list realms: %v", err))
		return result, nil
	}

	// Make disk pressure visible before doing per-cell work. Observation-only:
	// this never deletes, reaps, or mutates anything — it only logs a
	// rate-limited WARN when a realm's data volume crosses the high-water mark
	// (issue #1035).
	b.checkDiskPressure(realms)

	for _, realm := range realms {
		realmName := realm.Metadata.Name
		spaces, listErr := b.runner.ListSpaces(realmName)
		if listErr != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("list spaces in realm %q: %v", realmName, listErr))
			continue
		}
		for _, space := range spaces {
			spaceName := space.Metadata.Name
			stacks, stacksErr := b.runner.ListStacks(realmName, spaceName)
			if stacksErr != nil {
				result.Errors = append(result.Errors,
					fmt.Sprintf("list stacks in %s/%s: %v", realmName, spaceName, stacksErr))
				continue
			}
			for _, stack := range stacks {
				stackName := stack.Metadata.Name
				cells, cellsErr := b.runner.ListCells(realmName, spaceName, stackName)
				if cellsErr != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("list cells in %s/%s/%s: %v",
							realmName, spaceName, stackName, cellsErr))
					continue
				}
				for _, cell := range cells {
					result.CellsScanned++
					reconciled, outcome, reconcileErr := b.runner.ReconcileCell(cell)
					if reconcileErr != nil {
						result.CellsErrored++
						result.Errors = append(result.Errors,
							fmt.Sprintf("reconcile cell %s/%s/%s/%s: %v",
								realmName, spaceName, stackName,
								cell.Metadata.Name, reconcileErr))
						continue
					}
					switch {
					case outcome.Deleted:
						result.CellsDeleted++
					case outcome.Updated:
						result.CellsUpdated++
					}
					// OutOfSync detection (issue #820, foundation phase of
					// #819's umbrella): for Config-lineage cells, surface a
					// persistent OutOfSync flag in status by re-deriving
					// the would-be cell from the daemon-stored Config +
					// Blueprint and diffing against the live spec. Skips
					// deleted cells (the reconcile outcome already wiped
					// them) and cells without the kukeon.io/config label.
					// A persisted write counts toward CellsUpdated so the
					// reconcile summary reflects the metadata flip.
					if outcome.Deleted {
						continue
					}
					syncUpdated, syncErr := reconcileCellOutOfSync(b.runner, reconciled)
					if syncErr != nil {
						result.CellsErrored++
						result.Errors = append(result.Errors,
							fmt.Sprintf("OutOfSync detect cell %s/%s/%s/%s: %v",
								realmName, spaceName, stackName,
								cell.Metadata.Name, syncErr))
						continue
					}
					if syncUpdated && !outcome.Updated {
						result.CellsUpdated++
					}
				}
			}
		}
	}

	return result, nil
}

// checkDiskPressure samples the data volume backing each realm's metadata tree
// and emits a rate-limited WARN for any realm whose usage is at or above the
// configured warn threshold. It deletes nothing — the WARN is the entire
// action. Disabled when DiskPressureWarnPercent <= 0. Realms that share one
// filesystem each warn under their own rate-limit key; a statfs failure is
// logged at debug and skipped so a monitoring hiccup never disrupts the
// reconcile pass. Issue #1035.
func (b *Exec) checkDiskPressure(realms []intmodel.Realm) {
	if b.opts.DiskPressureWarnPercent <= 0 {
		return
	}
	sample := b.diskSampler
	if sample == nil {
		sample = diskpressure.Sample
	}
	for _, realm := range realms {
		realmName := realm.Metadata.Name
		dir := fs.RealmMetadataDir(b.opts.RunPath, realmName)
		usage, err := sample(dir)
		if err != nil {
			b.logger.DebugContext(b.ctx, "disk-pressure sample failed",
				"realm", realmName, "path", dir, "error", err)
			continue
		}
		if usage.UsedPercent < float64(b.opts.DiskPressureWarnPercent) {
			continue
		}
		if b.diskWarner != nil && !b.diskWarner.ShouldWarn(realmName) {
			continue
		}
		b.logger.WarnContext(b.ctx, "data volume under disk pressure",
			"realm", realmName,
			"path", dir,
			"usedPercent", fmt.Sprintf("%.1f", usage.UsedPercent),
			"warnPercent", b.opts.DiskPressureWarnPercent,
			"totalBytes", usage.TotalBytes,
			"availBytes", usage.AvailBytes)
	}
}
