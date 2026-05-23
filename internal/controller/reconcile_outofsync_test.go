//go:build !integration

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

// Tests for the OutOfSync detection pass ReconcileCells runs after each
// per-cell lifecycle reconcile (issue #820, foundation phase of #819's
// umbrella). AC pins the test file to reconcile_test.go but the codebase
// convention is one reconcile_*_test.go per reconcile pass (see
// reconcile_config_test.go, reconcile_blueprint_test.go,
// reconcile_secret_test.go), so the OutOfSync tests live in their own
// file alongside the new helper and stay co-located with the production
// code (reconcile_outofsync.go).

package controller_test

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// materializeSampleCell runs the production materialize+convert pipeline
// against sampleConfig()+sampleReferencedBlueprint() so the test's "live"
// cell starts from the byte-for-byte spec the OutOfSync detector
// materializes. The detector's Synced path is then "diff returns no
// changes," and divergence cases mutate the returned cell to introduce a
// targeted, well-known field difference.
func materializeSampleCell(t *testing.T) intmodel.Cell {
	t.Helper()
	cellDoc, err := cellconfig.Materialize(sampleConfig(), sampleReferencedBlueprint())
	if err != nil {
		t.Fatalf("cellconfig.Materialize: %v", err)
	}
	cell, err := apischeme.ConvertCellDocToInternal(cellDoc)
	if err != nil {
		t.Fatalf("ConvertCellDocToInternal: %v", err)
	}
	return cell
}

// stubLineageRunner builds a fakeRunner skeleton sufficient for the
// OutOfSync detection pass: the lifecycle ReconcileCell stub is a no-op,
// and the harness's ListRealms/Spaces/Stacks scaffolding (built by
// reconcileCellsHarness) names the same kuke-system / "" / "" scope the
// sample Config carries so the walker reaches the test cell.
func stubLineageRunner(
	getConfig func(intmodel.CellConfig) (intmodel.CellConfig, error),
	getBlueprint func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error),
	captureUpdate func(intmodel.Cell) error,
) *fakeRunner {
	return &fakeRunner{
		GetConfigFn:          getConfig,
		GetBlueprintFn:       getBlueprint,
		UpdateCellMetadataFn: captureUpdate,
		ReconcileCellFn: func(c intmodel.Cell) (intmodel.Cell, runner.ReconcileOutcome, error) {
			// Lifecycle reconcile is a no-op for the OutOfSync tests —
			// the cell's state is preserved and the OutOfSync pass runs
			// against the same cell the harness fed in.
			return c, runner.ReconcileOutcome{}, nil
		},
		ListRealmsFn: func() ([]intmodel.Realm, error) {
			return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
		},
		ListSpacesFn: func(string) ([]intmodel.Space, error) {
			// Sample config has empty Space, so the cell lives directly under
			// the realm. A "synthetic-default" stack is enough to land in
			// ListCells with the right name triple.
			return []intmodel.Space{buildTestSpace("", "kuke-system")}, nil
		},
		ListStacksFn: func(string, string) ([]intmodel.Stack, error) {
			return []intmodel.Stack{buildTestStack("", "kuke-system", "")}, nil
		},
	}
}

// runOutOfSyncHarness wires a single-cell ReconcileCells walk against the
// stub above and returns whatever the OutOfSync pass observed: the result
// summary, and the cell handed to UpdateCellMetadata when one occurred.
func runOutOfSyncHarness(t *testing.T, cell intmodel.Cell, mock *fakeRunner) (controllerResult, *intmodel.Cell) {
	t.Helper()
	var captured *intmodel.Cell
	if mock.UpdateCellMetadataFn == nil {
		mock.UpdateCellMetadataFn = func(c intmodel.Cell) error {
			cp := c
			captured = &cp
			return nil
		}
	}
	mock.ListCellsFn = func(string, string, string) ([]intmodel.Cell, error) {
		return []intmodel.Cell{cell}, nil
	}
	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() error = %v", err)
	}
	return controllerResult{
		CellsScanned: res.CellsScanned,
		CellsUpdated: res.CellsUpdated,
		CellsErrored: res.CellsErrored,
		CellsDeleted: res.CellsDeleted,
		Errors:       res.Errors,
	}, captured
}

// controllerResult mirrors controller.ReconcileResult so tests can read
// the harness's counters via a struct literal without importing the
// controller package's tag-heavy alias.
type controllerResult struct {
	CellsScanned int
	CellsUpdated int
	CellsErrored int
	CellsDeleted int
	Errors       []string
}

// TestReconcileCells_OutOfSync_MatchingCellStaysSynced is AC case 1: a
// Config-lineage cell whose live spec matches the materialized spec
// leaves OutOfSync false and skips the metadata write, so the Synced
// fleet does not spin the metadata file once per reconcile tick.
func TestReconcileCells_OutOfSync_MatchingCellStaysSynced(t *testing.T) {
	live := materializeSampleCell(t)

	var updateCalls atomic.Int32
	mock := stubLineageRunner(
		func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return configCarrier(t, sampleConfig()), nil
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		func(intmodel.Cell) error {
			updateCalls.Add(1)
			return nil
		},
	)

	res, _ := runOutOfSyncHarness(t, live, mock)

	if got := updateCalls.Load(); got != 0 {
		t.Errorf("UpdateCellMetadata calls: got %d, want 0 (synced cell must not persist)", got)
	}
	if res.CellsUpdated != 0 {
		t.Errorf("CellsUpdated: got %d, want 0", res.CellsUpdated)
	}
	if res.CellsErrored != 0 || len(res.Errors) != 0 {
		t.Errorf("synced pass surfaced errors: %+v", res)
	}
}

// TestReconcileCells_OutOfSync_ModifiedConfigProducesOutOfSync is AC case 2:
// when the daemon-stored Config's would-be spec has drifted from the
// live cell (here, the live cell's image differs from what the Config
// materializes), the pass persists OutOfSync=true with a Reason that
// names the differing field.
func TestReconcileCells_OutOfSync_ModifiedConfigProducesOutOfSync(t *testing.T) {
	live := materializeSampleCell(t)
	if len(live.Spec.Containers) == 0 {
		t.Fatalf("test fixture has no containers — sampleConfig/sampleReferencedBlueprint changed?")
	}
	// Mutate the live cell to introduce a single divergence vs. what
	// sampleConfig+sampleReferencedBlueprint materialize. Image is the
	// canonical "drifted" field for this AC.
	live.Spec.Containers[0].Image = "drifted-image:v2"

	mock := stubLineageRunner(
		func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return configCarrier(t, sampleConfig()), nil
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		nil,
	)

	res, captured := runOutOfSyncHarness(t, live, mock)

	if captured == nil {
		t.Fatalf("UpdateCellMetadata was not called — divergent cell must persist OutOfSync status")
	}
	if !captured.Status.OutOfSync {
		t.Errorf("Status.OutOfSync = false, want true on a divergent cell")
	}
	if captured.Status.OutOfSyncError != "" {
		t.Errorf(
			"Status.OutOfSyncError = %q, want empty on a routine drift (error surface is for undecidable diffs)",
			captured.Status.OutOfSyncError,
		)
	}
	if !strings.Contains(captured.Status.OutOfSyncReason, "spec differs") {
		t.Errorf("Status.OutOfSyncReason = %q, want a 'spec differs' summary", captured.Status.OutOfSyncReason)
	}
	if !strings.Contains(captured.Status.OutOfSyncReason, "image") {
		t.Errorf("Status.OutOfSyncReason = %q, want the differing field (image) named", captured.Status.OutOfSyncReason)
	}
	if res.CellsUpdated != 1 {
		t.Errorf("CellsUpdated: got %d, want 1 (OutOfSync write counts as an update)", res.CellsUpdated)
	}
}

// TestReconcileCells_OutOfSync_DeletedConfigProducesOutOfSyncWithReason is
// AC case 3: when the operator deletes the lineage Config out from under
// a live cell (GetConfig → ErrConfigNotFound), the pass persists
// OutOfSync=true with the canonical "lineage Config deleted" reason.
// Distinct from the divergent-spec reason so a downstream `kuke get cell`
// SYNC column can hand the operator a hint at the right next action
// (restore the Config vs. restart the cell).
func TestReconcileCells_OutOfSync_DeletedConfigProducesOutOfSyncWithReason(t *testing.T) {
	live := materializeSampleCell(t)

	mock := stubLineageRunner(
		func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			t.Fatalf("GetBlueprint must not be called when the lineage Config is missing")
			return intmodel.CellBlueprint{}, nil
		},
		nil,
	)

	res, captured := runOutOfSyncHarness(t, live, mock)

	if captured == nil {
		t.Fatalf("UpdateCellMetadata was not called — deleted Config must persist OutOfSync status")
	}
	if !captured.Status.OutOfSync {
		t.Errorf("Status.OutOfSync = false, want true after lineage Config deletion")
	}
	if captured.Status.OutOfSyncReason != "lineage Config deleted" {
		t.Errorf("Status.OutOfSyncReason = %q, want %q", captured.Status.OutOfSyncReason, "lineage Config deleted")
	}
	if captured.Status.OutOfSyncError != "" {
		t.Errorf(
			"Status.OutOfSyncError = %q, want empty (deletion is decidable, not an error)",
			captured.Status.OutOfSyncError,
		)
	}
	if res.CellsUpdated != 1 {
		t.Errorf("CellsUpdated: got %d, want 1", res.CellsUpdated)
	}
}

// TestReconcileCells_OutOfSync_CellsWithoutLineageSkipped is AC case 4:
// cells that carry no `kukeon.io/config` lineage label sit outside v1's
// scope and the pass must not call GetConfig / GetBlueprint /
// UpdateCellMetadata for them. Hand-built cells and `-b`/`-p`-lineage
// cells stay untouched.
func TestReconcileCells_OutOfSync_CellsWithoutLineageSkipped(t *testing.T) {
	live := buildTestCell("hand-built", "kuke-system", "", "")
	// Defensive: ensure no Config label slipped in via the test fixture.
	delete(live.Metadata.Labels, cellconfig.LabelConfig)

	mock := stubLineageRunner(
		func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			t.Fatalf("GetConfig must not be called for a cell without a lineage label")
			return intmodel.CellConfig{}, nil
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			t.Fatalf("GetBlueprint must not be called for a cell without a lineage label")
			return intmodel.CellBlueprint{}, nil
		},
		func(intmodel.Cell) error {
			t.Fatalf("UpdateCellMetadata must not be called for a cell without a lineage label")
			return nil
		},
	)

	res, _ := runOutOfSyncHarness(t, live, mock)

	if res.CellsUpdated != 0 || res.CellsErrored != 0 {
		t.Errorf("non-lineage cell triggered counters: %+v", res)
	}
}

// TestReconcileCells_OutOfSync_MaterializationErrorProducesDistinctErrorState
// is AC case 5: when the materialization pipeline cannot produce a
// would-be spec at all (referenced Blueprint missing here; same shape
// for slot-fill validation errors), the pass persists a distinct error
// state — OutOfSync=false with OutOfSyncError set — so downstream
// consumers do not mistake an undecidable diff for a routine drift.
func TestReconcileCells_OutOfSync_MaterializationErrorProducesDistinctErrorState(t *testing.T) {
	live := materializeSampleCell(t)

	mock := stubLineageRunner(
		func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return configCarrier(t, sampleConfig()), nil
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return intmodel.CellBlueprint{}, errdefs.ErrBlueprintNotFound
		},
		nil,
	)

	res, captured := runOutOfSyncHarness(t, live, mock)

	if captured == nil {
		t.Fatalf("UpdateCellMetadata was not called — materialization error must persist the error state")
	}
	if captured.Status.OutOfSync {
		t.Errorf("Status.OutOfSync = true, want false (an undecidable diff is not a routine drift)")
	}
	if captured.Status.OutOfSyncReason != "" {
		t.Errorf(
			"Status.OutOfSyncReason = %q, want empty (reason is for the divergent path)",
			captured.Status.OutOfSyncReason,
		)
	}
	if !strings.Contains(captured.Status.OutOfSyncError, "blueprint") {
		t.Errorf(
			"Status.OutOfSyncError = %q, want a message naming the missing blueprint",
			captured.Status.OutOfSyncError,
		)
	}
	if res.CellsUpdated != 1 {
		t.Errorf("CellsUpdated: got %d, want 1", res.CellsUpdated)
	}
	// A runner-level error during GetBlueprint must surface as a stable
	// status field, not as a per-pass loop error — divergence detection
	// is best-effort and the lifecycle reconcile already succeeded.
	if res.CellsErrored != 0 || len(res.Errors) != 0 {
		t.Errorf("materialization error surfaced as loop error: %+v", res)
	}
}

// TestReconcileCells_OutOfSync_NoChangeNoRewrite guards the no-write
// fast-path on the persistence step: a cell already carrying OutOfSync=
// true with the matching Reason and a still-divergent live spec must
// not re-write the metadata every tick. The Synced equivalent is
// covered by the matching-cell case above; this asserts the diff-the-
// status side of the same predicate.
func TestReconcileCells_OutOfSync_NoChangeNoRewrite(t *testing.T) {
	live := materializeSampleCell(t)
	live.Spec.Containers[0].Image = "drifted-image:v2"
	// Pin the expected post-write status on the live cell so the next
	// pass sees it as already-recorded.
	live.Status.OutOfSync = true
	live.Status.OutOfSyncReason = "spec differs at containers[main].image"

	var updateCalls atomic.Int32
	mock := stubLineageRunner(
		func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return configCarrier(t, sampleConfig()), nil
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		func(intmodel.Cell) error {
			updateCalls.Add(1)
			return nil
		},
	)

	res, _ := runOutOfSyncHarness(t, live, mock)

	if got := updateCalls.Load(); got != 0 {
		t.Errorf("UpdateCellMetadata calls: got %d, want 0 (status already records the divergence)", got)
	}
	if res.CellsUpdated != 0 {
		t.Errorf("CellsUpdated: got %d, want 0", res.CellsUpdated)
	}
}
