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
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// materializeSampleCell runs the production materialize+convert pipeline
// against sampleConfig()+sampleReferencedBlueprint() so the test's "live"
// cell starts from the byte-for-byte spec the OutOfSync detector
// materializes. The detector's Synced path is then "diff returns no
// changes," and divergence cases mutate the returned cell to introduce a
// targeted, well-known field difference.
func materializeSampleCell(t *testing.T) intmodel.Cell {
	t.Helper()
	cellDoc, err := cellconfig.MaterializeWithName(
		sampleConfig(), sampleReferencedBlueprint(),
		cellconfig.Prefix(sampleConfig()),
	)
	if err != nil {
		t.Fatalf("cellconfig.MaterializeWithName: %v", err)
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
		// materializeCellFromConfig now reproduces the runner-injected
		// space-derived container fields (#1136). The sample cell carries no
		// container defaults and an empty cniConfigPath, so the defaults below
		// are no-ops that keep the materialized "desired" byte-identical to the
		// materialized "live" sample cell; divergence tests override them.
		GetSpaceFn: func(s intmodel.Space) (intmodel.Space, error) {
			return s, nil
		},
		ResolveSpaceCNIConfigPathFn: func(string, string) (string, error) {
			return "", nil
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

// sampleAttachableBlueprint mirrors sampleReferencedBlueprint but marks the
// "main" container attachable, so the re-resolve path's ApplyEnvOverrides has a
// container to bake the recorded per-cell --env override into
// (epic:cell-identity P5, #1024).
func sampleAttachableBlueprint() ext.CellBlueprintDoc {
	bp := sampleReferencedBlueprint()
	bp.Spec.Cell.Containers[0].Attachable = true
	return bp
}

// TestReconcileCells_OutOfSync_EnvOverrideSurvivesNoSpuriousDrift pins AC2/AC3
// of epic:cell-identity P5 (#1024): a Config-lineage cell created
// `--from-config --env K=V` records the override in Spec.Provenance.EnvOverrides
// (P3 #1023) and bakes it into the attachable container's Env. The re-resolve
// path re-applies the recorded override last (P3 precedence), so the
// materialized `desired` spec matches the live cell — no spurious OutOfSync and
// no metadata write. Without the fix the materialized spec lacks the override,
// the diff flags the env field, and the cell sticks on a spurious OutOfSync.
func TestReconcileCells_OutOfSync_EnvOverrideSurvivesNoSpuriousDrift(t *testing.T) {
	cellDoc, err := cellconfig.MaterializeWithName(
		sampleConfig(), sampleAttachableBlueprint(), cellconfig.Prefix(sampleConfig()),
	)
	if err != nil {
		t.Fatalf("cellconfig.MaterializeWithName: %v", err)
	}
	live, err := apischeme.ConvertCellDocToInternal(cellDoc)
	if err != nil {
		t.Fatalf("ConvertCellDocToInternal: %v", err)
	}

	// Simulate `--from-config --env APP_ENV=prod`: bake the override into the
	// attachable container's Env and record it in provenance, as the create
	// path (applyEnvOverrides, #1023) does.
	idx := -1
	for i, c := range live.Spec.Containers {
		if !c.Root && c.Attachable {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("test fixture has no attachable container — sampleAttachableBlueprint changed?")
	}
	live.Spec.Containers[idx].Env = append(live.Spec.Containers[idx].Env, "APP_ENV=prod")
	live.Spec.Provenance = &intmodel.CellProvenance{EnvOverrides: []string{"APP_ENV=prod"}}

	var updateCalls atomic.Int32
	mock := stubLineageRunner(
		func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return configCarrier(t, sampleConfig()), nil
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleAttachableBlueprint()), nil
		},
		func(intmodel.Cell) error {
			updateCalls.Add(1)
			return nil
		},
	)

	res, _ := runOutOfSyncHarness(t, live, mock)

	if got := updateCalls.Load(); got != 0 {
		t.Errorf("UpdateCellMetadata calls: got %d, want 0 (recorded --env override must not produce spurious OutOfSync)", got)
	}
	if res.CellsUpdated != 0 {
		t.Errorf("CellsUpdated: got %d, want 0", res.CellsUpdated)
	}
	if res.CellsErrored != 0 || len(res.Errors) != 0 {
		t.Errorf("override-survival pass surfaced errors: %+v", res)
	}
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

// TestReconcileCells_OutOfSync_RunnerInjectedContainerFieldsStaySynced pins
// the #1136 fix: the runner bakes a space/runtime-derived cniConfigPath — and,
// when the space declares them, container defaults (user, …) — onto every
// persisted container spec at create/start, but cellconfig.MaterializeWithName
// reproduces neither. Before the fix apply.DiffCell saw `"" != "/opt/.../
// network.conflist"` (and the analogous space-default mismatch) and the cell
// stuck on a permanent spurious OutOfSync. The detector now re-applies both
// classes against the cell's space, so a cell that has only the runner-injected
// fields stays Synced and skips the metadata write.
func TestReconcileCells_OutOfSync_RunnerInjectedContainerFieldsStaySynced(t *testing.T) {
	live := materializeSampleCell(t)
	if len(live.Spec.Containers) == 0 {
		t.Fatalf("test fixture has no containers — sampleConfig/sampleReferencedBlueprint changed?")
	}

	const cniPath = "/opt/kukeon/data/default/default/network.conflist"
	const spaceDefaultUser = "1000:1000"
	// Mimic provision.go: bake the space-derived cniConfigPath plus a space
	// container default onto every non-root container. The root container is
	// runner-synthesized and skipped by DiffCell, so it is left alone.
	for i := range live.Spec.Containers {
		if live.Spec.Containers[i].Root {
			continue
		}
		live.Spec.Containers[i].CNIConfigPath = cniPath
		live.Spec.Containers[i].User = spaceDefaultUser
	}

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
	// Reproduce what the runner resolved at create time: the same cniConfigPath
	// and the same space container default. The re-materialized desired must
	// then match the live cell.
	mock.ResolveSpaceCNIConfigPathFn = func(string, string) (string, error) {
		return cniPath, nil
	}
	mock.GetSpaceFn = func(s intmodel.Space) (intmodel.Space, error) {
		s.Spec.Defaults = &intmodel.SpaceDefaults{
			Container: &intmodel.SpaceContainerDefaults{User: spaceDefaultUser},
		}
		return s, nil
	}

	res, _ := runOutOfSyncHarness(t, live, mock)

	if got := updateCalls.Load(); got != 0 {
		t.Errorf("UpdateCellMetadata calls: got %d, want 0 (runner-injected container fields must not produce spurious OutOfSync)", got)
	}
	if res.CellsUpdated != 0 {
		t.Errorf("CellsUpdated: got %d, want 0", res.CellsUpdated)
	}
	if res.CellsErrored != 0 || len(res.Errors) != 0 {
		t.Errorf("runner-injected-field pass surfaced errors: %+v", res)
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

// TestReconcileCells_OutOfSync_RealmScopedConfigFoundForStackPlacedCell
// pins the issue #921 fix: a cell at full scope (realm/space/stack) whose
// `kukeon.io/config` lineage label names a Config bound only at realm
// scope must not surface as "lineage Config deleted". The lookup probes
// full → space-only → realm-only and uses the first hit, so the realm-
// scoped Config is found even though the live cell sits two scope levels
// below it. Without the fix, GetConfig is called once at the cell's full
// scope, ErrConfigNotFound surfaces, and the cell sticks permanently on
// the deleted-Config OutOfSync verdict.
func TestReconcileCells_OutOfSync_RealmScopedConfigFoundForStackPlacedCell(t *testing.T) {
	live := buildTestCell("kukeon-dev-root-0", "kuke-system", "default", "default")
	live.Metadata.Labels[cellconfig.LabelConfig] = "kukeon-dev"

	var probes []intmodel.CellConfigMetadata
	mock := stubLineageRunner(
		func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
			probes = append(probes, c.Metadata)
			// Realm scope is the only hit; intermediate scopes miss.
			if c.Metadata.Space == "" && c.Metadata.Stack == "" {
				return configCarrier(t, sampleConfig()), nil
			}
			return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		nil,
	)
	// Walk the realm/space/stack the live cell sits in so the harness reaches it.
	mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
		return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
	}
	mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
		return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
	}

	_, captured := runOutOfSyncHarness(t, live, mock)

	// Probe ordering is deepest → shallowest, with no duplicates.
	wantProbes := []intmodel.CellConfigMetadata{
		{Name: "kukeon-dev", Realm: "kuke-system", Space: "default", Stack: "default"},
		{Name: "kukeon-dev", Realm: "kuke-system", Space: "default", Stack: ""},
		{Name: "kukeon-dev", Realm: "kuke-system", Space: "", Stack: ""},
	}
	if len(probes) != len(wantProbes) {
		t.Fatalf("GetConfig probe count: got %d want %d (probes=%+v)", len(probes), len(wantProbes), probes)
	}
	for i, p := range probes {
		// CellConfigMetadata carries a Labels map (issue #1027) so the
		// struct is no longer comparable with !=; compare scope-coordinate
		// fields explicitly since that is what this probe-ordering test
		// asserts.
		w := wantProbes[i]
		if p.Name != w.Name || p.Realm != w.Realm || p.Space != w.Space || p.Stack != w.Stack {
			t.Errorf("probe[%d] = %+v, want %+v", i, p, w)
		}
	}
	// The Config was found at realm scope, so the verdict must NOT be
	// "lineage Config deleted". A scope-divergence reason or a Synced
	// verdict are both acceptable — the bug being pinned is the lookup
	// missing entirely.
	if captured != nil && captured.Status.OutOfSyncReason == outOfSyncReasonConfigDeletedTest {
		t.Errorf("OutOfSyncReason = %q, want anything but %q (Config found at realm scope)",
			captured.Status.OutOfSyncReason, outOfSyncReasonConfigDeletedTest)
	}
}

// TestReconcileCells_OutOfSync_RealmScopedCellSkipsRedundantProbes
// confirms the dedupe rule: when the live cell sits at realm scope (no
// space, no stack), only one GetConfig probe fires — the full and
// narrowed scopes collapse to the same realm-only lookup.
func TestReconcileCells_OutOfSync_RealmScopedCellSkipsRedundantProbes(t *testing.T) {
	live := materializeSampleCell(t) // realm=kuke-system, space="", stack=""
	live.Metadata.Labels[cellconfig.LabelConfig] = "kukeon-dev"

	var probeCount int
	mock := stubLineageRunner(
		func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			probeCount++
			return configCarrier(t, sampleConfig()), nil
		},
		func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		nil,
	)

	runOutOfSyncHarness(t, live, mock)

	if probeCount != 1 {
		t.Errorf("GetConfig probe count: got %d want 1 (realm-only cell needs no narrowing)", probeCount)
	}
}

// outOfSyncReasonConfigDeletedTest is a string-literal duplicate of the
// production constant the controller package keeps unexported. Copied
// here so the external test package can assert against the expected
// value without exporting the constant — the assertion intent is
// "anything but this", so a drift between this literal and the
// production constant would soften the test (would still flag a true
// regression as long as the production constant remains stable).
const outOfSyncReasonConfigDeletedTest = "lineage Config deleted"
