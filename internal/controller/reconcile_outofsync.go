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
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

const (
	outOfSyncReasonConfigDeleted = "lineage Config deleted"
	outOfSyncReasonSpecDiffers   = "spec differs"
)

// reconcileCellOutOfSync runs the per-cell OutOfSync detection pass for
// Config-lineage cells (issue #820, foundation phase of #819's umbrella).
//
// Cells without a `kukeon.io/config=<name>` lineage label are skipped (the
// `-b`-lineage and hand-built cases are deliberately out of scope per the
// umbrella). For a Config-lineage cell, the pass re-derives the would-be
// CellDoc from the daemon-stored Config + its referenced Blueprint via the
// same `cellconfig.MaterializeWithName` call the CellConfig materialisation
// path uses, then compares the materialized spec against the live cell with
// `apply.DiffCell`. The outcome lands on three status fields:
//
//   - OutOfSync = true with OutOfSyncReason describing the divergence when
//     the live spec differs from the materialized spec, or when the lineage
//     Config has been deleted (the operator removed it but the cell
//     remains).
//   - OutOfSync = false with empty Reason when the live spec matches the
//     materialized spec (Synced).
//   - OutOfSync = false with OutOfSyncError set when divergence is
//     undecidable — referenced Blueprint missing, slot-fill validation
//     failure, or any other materialization error. Distinct from OutOfSync
//     so downstream verbs (`kuke get cell` SYNC column, `kuke restart
//     <name>`) can route the error case separately from a routine drift.
//
// Persists via runner.UpdateCellMetadata only when any of the three fields
// would change, so an already-Synced cell costs at most one Config + one
// Blueprint read per tick. The caller (ReconcileCells) treats a persisted
// write as a CellsUpdated bump.
func reconcileCellOutOfSync(r runner.Runner, cell intmodel.Cell) (bool, error) {
	configName, ok := configLineage(cell)
	if !ok {
		// Not a Config-lineage cell — leave all OutOfSync fields alone. A
		// cell that lost its lineage label between ticks keeps whatever
		// the previous tick wrote, which is the correct read: until a
		// human reconciles the labels, the daemon has no Config to
		// re-derive against.
		return false, nil
	}

	desired := desiredCellOutOfSync(r, cell, configName)
	return persistCellOutOfSync(r, cell, desired)
}

// cellOutOfSync bundles the three OutOfSync status fields the detection
// pass writes so the helper functions can return them as a unit.
type cellOutOfSync struct {
	OutOfSync bool
	Reason    string
	Err       string
}

// desiredCellOutOfSync computes the OutOfSync verdict the live cell
// should carry after this pass. The function is pure with respect to the
// runner reads it makes — no metadata writes — so it can be inspected in
// isolation by tests that pin the runner's responses.
func desiredCellOutOfSync(r runner.Runner, cell intmodel.Cell, configName string) cellOutOfSync {
	cfg, found, getErr := lookupLineageConfig(r, cell, configName)
	if getErr != nil {
		return cellOutOfSync{Err: fmt.Sprintf("read lineage config %q: %v", configName, getErr)}
	}
	if !found {
		return cellOutOfSync{OutOfSync: true, Reason: outOfSyncReasonConfigDeleted}
	}

	desiredCell, materializeErr := materializeCellFromConfig(r, cfg, cell.Metadata.Name, provenanceEnvOverrides(cell))
	if materializeErr != nil {
		return cellOutOfSync{Err: materializeErr.Error()}
	}

	diff := apply.DiffCell(desiredCell, cell)
	if !diff.HasChanges {
		return cellOutOfSync{}
	}
	return cellOutOfSync{
		OutOfSync: true,
		Reason:    outOfSyncSpecDifferReason(diff),
	}
}

// provenanceEnvOverrides returns the per-cell `--env` overrides recorded in the
// cell's materialization provenance (P3 #1023), or nil for a cell with no
// provenance (hand-built, or a pre-#1021 cell). Threaded into
// materializeCellFromConfig so the re-resolve path re-applies the overrides the
// live cell baked at create time (epic:cell-identity P5, #1024).
func provenanceEnvOverrides(cell intmodel.Cell) []string {
	if cell.Spec.Provenance == nil {
		return nil
	}
	return cell.Spec.Provenance.EnvOverrides
}

// configLineage returns the lineage Config name carried on the cell's
// `kukeon.io/config` label, if any. The second return is false for cells
// without the label (the bulk of the host — hand-built cells, `-b` and
// `-p` lineage cells are skipped per the umbrella's v1 scope).
func configLineage(cell intmodel.Cell) (string, bool) {
	name := strings.TrimSpace(cell.Metadata.Labels[cellconfig.LabelConfig])
	if name == "" {
		return "", false
	}
	return name, true
}

// lookupLineageConfig fetches the daemon-stored Config the cell's lineage
// label points to, returning (cfg, true, nil) on hit, (zero, false, nil)
// when the operator deleted the Config (the canonical OutOfSync trigger
// the umbrella documents), or (zero, false, err) on any other error.
//
// The `kukeon.io/config` lineage label records only the Config name, not
// the scope it was bound at, so the lookup must probe progressively
// shallower scopes: full (realm/space/stack) → space-only (realm/space) →
// realm-only (realm). `kuke run <config>` resolves Config lookups via
// cmd/kuke/shared.ExplicitScope (empty space/stack unless the operator
// passed --space/--stack), so a `kuke run kukeon-dev-root-0` against a
// realm-scoped Config materializes a cell at realm=default /
// space=default / stack=default (resolveCellLocation fills the session
// defaults). Without the widening here, that cell's reconciler tick
// resolves to `/opt/kukeon/data/default/default/default/configs/<name>`
// (which does not exist) and persists a permanent "lineage Config
// deleted" OutOfSync verdict.
//
// Probes are deduplicated when the cell's space or stack is already empty
// (a realm-scoped cell or a space-scoped cell only takes the one probe
// its own scope names).
func lookupLineageConfig(
	r runner.Runner, cell intmodel.Cell, configName string,
) (intmodel.CellConfig, bool, error) {
	realm, space, stack := cell.Spec.RealmName, cell.Spec.SpaceName, cell.Spec.StackName

	type probe struct{ space, stack string }
	probes := []probe{{space, stack}}
	if stack != "" {
		probes = append(probes, probe{space, ""})
	}
	if space != "" {
		probes = append(probes, probe{"", ""})
	}

	for _, p := range probes {
		cfg, err := r.GetConfig(intmodel.CellConfig{
			Metadata: intmodel.CellConfigMetadata{
				Name:  configName,
				Realm: realm,
				Space: p.space,
				Stack: p.stack,
			},
		})
		if err == nil {
			return cfg, true, nil
		}
		if !errors.Is(err, errdefs.ErrConfigNotFound) {
			return intmodel.CellConfig{}, false, err
		}
	}
	return intmodel.CellConfig{}, false, nil
}

// materializeCellFromConfig re-runs the CellConfig materialization pipeline
// against a daemon-stored Config: decode the Config's body, resolve the
// referenced Blueprint, then `cellconfig.MaterializeWithName` the two. Returns
// the materialized cell in the same internal-model shape the reconciler's live
// cell uses, so apply.DiffCell can compare them directly.
//
// The live cell's own name is threaded through as the materialized name so the
// diff never trips on metadata.name — materialization no longer derives the
// name from the Config name (epic:cell-identity #1021), and the OutOfSync
// detector compares *spec drift*, not identity. This keeps the detector
// correct now that P2 (#1022) lands generated cell names that no longer match
// the Config name (the legacy StableName pin, retired in P2).
//
// envOverrides carries the cell's recorded `Spec.Provenance.EnvOverrides` (the
// per-cell `--env KEY=VALUE` baked at create time, P3 #1023). Re-resolving from
// the Config alone re-runs `MaterializeWithName`, which never re-applies those
// overrides — so without re-applying them here a cell created
// `--from-config --env K=V` reports a spurious OutOfSync (the override is in the
// live spec but not the freshly materialized one) and has it stripped on
// reapply. ApplyEnvOverrides re-bakes them last (P3 precedence) through the same
// helper the create and clone paths use (epic:cell-identity P5, #1024), so the
// re-materialized spec matches the live cell's attachable-container Env.
func materializeCellFromConfig(
	r runner.Runner, cfg intmodel.CellConfig, cellName string, envOverrides []string,
) (intmodel.Cell, error) {
	cfgDoc, err := apischeme.ConvertCellConfigToExternal(cfg)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("decode lineage config: %w", err)
	}

	ref := cfgDoc.Spec.Blueprint
	bpCarrier, getErr := r.GetBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{
			Name:  ref.Name,
			Realm: ref.Realm,
			Space: ref.Space,
			Stack: ref.Stack,
		},
	})
	if getErr != nil {
		if errors.Is(getErr, errdefs.ErrBlueprintNotFound) {
			return intmodel.Cell{}, fmt.Errorf("referenced blueprint %q not found", ref.Name)
		}
		return intmodel.Cell{}, fmt.Errorf("read referenced blueprint %q: %w", ref.Name, getErr)
	}
	bpDoc, bpErr := apischeme.ConvertCellBlueprintToExternal(bpCarrier)
	if bpErr != nil {
		return intmodel.Cell{}, fmt.Errorf("decode referenced blueprint: %w", bpErr)
	}

	cellDoc, mErr := cellconfig.MaterializeWithName(cfgDoc, bpDoc, cellName)
	if mErr != nil {
		return intmodel.Cell{}, fmt.Errorf("materialize cell: %w", mErr)
	}

	// Re-apply the cell's recorded per-cell --env overrides last (P3 precedence,
	// #1023) so the re-materialized spec carries the same attachable-container
	// Env the live cell baked at create time. A nil/empty slice is a no-op.
	cellconfig.ApplyEnvOverrides(&cellDoc, envOverrides)

	desired, convErr := apischeme.ConvertCellDocToInternal(cellDoc)
	if convErr != nil {
		return intmodel.Cell{}, fmt.Errorf("convert materialized cell: %w", convErr)
	}
	return desired, nil
}

// outOfSyncSpecDifferReason renders the DiffCell verdict as a stable,
// human-readable one-line summary suitable for the OutOfSyncReason status
// field. Always starts with "spec differs" so downstream parsers (a
// future `kuke get cell` SYNC column, the `kuke restart` verb) can
// recognize the divergence class without parsing the full diff payload.
// Field paths are sorted so the same diff produces the same reason
// across ticks, keeping the persisted status stable.
func outOfSyncSpecDifferReason(diff apply.CellDiffResult) string {
	fields := collectDiffFieldPaths(diff)
	if len(fields) == 0 {
		return outOfSyncReasonSpecDiffers
	}
	return fmt.Sprintf("%s at %s", outOfSyncReasonSpecDiffers, strings.Join(fields, ", "))
}

// collectDiffFieldPaths gathers every divergent field path the DiffCell
// result names, deduplicated and sorted. The set spans the cell-level
// changes, the root-container marker, per-container ChangedFields /
// BreakingChanges qualified by container name, and orphan container IDs.
func collectDiffFieldPaths(diff apply.CellDiffResult) []string {
	seen := make(map[string]struct{})
	add := func(path string) {
		if path == "" {
			return
		}
		seen[path] = struct{}{}
	}

	for _, f := range diff.ChangedFields {
		add(f)
	}
	for _, f := range diff.BreakingChanges {
		add(f)
	}
	if diff.RootContainerChanged {
		add("spec.rootContainer")
	}
	for _, cd := range diff.Containers {
		switch cd.Action {
		case "add":
			add(fmt.Sprintf("containers[%s] (added)", cd.Name))
		case "update":
			for _, f := range cd.ChangedFields {
				add(fmt.Sprintf("containers[%s].%s", cd.Name, f))
			}
			for _, f := range cd.BreakingChanges {
				add(fmt.Sprintf("containers[%s].%s", cd.Name, f))
			}
		}
	}
	for _, id := range diff.Orphans {
		add(fmt.Sprintf("containers[%s] (removed)", id))
	}

	fields := make([]string, 0, len(seen))
	for k := range seen {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return fields
}

// persistCellOutOfSync writes the new OutOfSync verdict to the cell's
// metadata when any of the three status fields would change, and reports
// whether a write actually occurred. The no-write fast-path keeps a
// Synced fleet from spinning the metadata file once per tick.
func persistCellOutOfSync(
	r runner.Runner, cell intmodel.Cell, desired cellOutOfSync,
) (bool, error) {
	if cell.Status.OutOfSync == desired.OutOfSync &&
		cell.Status.OutOfSyncReason == desired.Reason &&
		cell.Status.OutOfSyncError == desired.Err {
		return false, nil
	}

	cell.Status.OutOfSync = desired.OutOfSync
	cell.Status.OutOfSyncReason = desired.Reason
	cell.Status.OutOfSyncError = desired.Err

	if err := r.UpdateCellMetadata(cell); err != nil {
		return false, fmt.Errorf("persist OutOfSync status: %w", err)
	}
	return true, nil
}
