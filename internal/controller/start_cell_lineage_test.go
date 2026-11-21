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

// Tests for the start-time lineage reapply of issue #1569: the reapply is
// decided from a render taken at start time (not from Status.OutOfSync), the
// Failed / Error / Degraded recovery route recreates from that render, the
// lineage Config is resolved from the cell's provenance, and a successful
// reapply records itself through the locked patch.

package controller_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// lineageCalls records what a lineage start drove on the runner.
type lineageCalls struct {
	recreated []intmodel.Cell
	started   []intmodel.Cell
	updated   []intmodel.Cell
	lookups   []intmodel.CellConfigMetadata
	patches   int
	patched   *intmodel.Cell
}

// newLineageStartRunner wires a fakeRunner for StartCell on live: GetCell
// returns live, RecreateCell / StartCell / UpdateCell record their argument
// and report Ready, and PatchCellMetadata applies the patch to a copy of live
// (the persisted document).
func newLineageStartRunner(
	t *testing.T,
	live intmodel.Cell,
	calls *lineageCalls,
	getConfig func(intmodel.CellConfig) (intmodel.CellConfig, error),
	getBlueprint func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error),
) *fakeRunner {
	t.Helper()
	ready := func(c intmodel.Cell) intmodel.Cell {
		c.Status.State = intmodel.CellStateReady
		return c
	}
	return &fakeRunner{
		GetCellFn:                   func(intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:              func(any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn:   func(intmodel.Cell) (bool, error) { return true, nil },
		GetSpaceFn:                  func(s intmodel.Space) (intmodel.Space, error) { return s, nil },
		GetRealmFn:                  func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil },
		GetStackFn:                  func(st intmodel.Stack) (intmodel.Stack, error) { return st, nil },
		ResolveSpaceCNIConfigPathFn: func(string, string) (string, error) { return "", nil },
		GetConfigFn: func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
			calls.lookups = append(calls.lookups, c.Metadata)
			return getConfig(c)
		},
		GetBlueprintFn: getBlueprint,
		RecreateCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			calls.recreated = append(calls.recreated, c)
			return ready(c), nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			calls.started = append(calls.started, c)
			return ready(c), nil
		},
		UpdateCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			calls.updated = append(calls.updated, c)
			return ready(c), nil
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error {
			return errors.New("controller must not write the cell outside the patch (issue #1569)")
		},
		PatchCellMetadataFn: func(_ intmodel.Cell, mutate func(*intmodel.Cell) bool) error {
			calls.patches++
			fresh := cloneCellForTest(t, live)
			if mutate(&fresh) {
				calls.patched = &fresh
			}
			return nil
		},
	}
}

func sampleConfigLookup(intmodel.CellConfig) (intmodel.CellConfig, error) {
	return reapplySampleCellConfig("test-realm", "test-space", "test-stack"), nil
}

func sampleBlueprintLookup(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
	return reapplySampleBlueprint("test-realm"), nil
}

// assertReapplyRecorded checks the D7 record a successful reapply leaves: the
// OutOfSync fields cleared, the fresh render's annotations and its provenance.
func assertReapplyRecorded(t *testing.T, calls *lineageCalls) {
	t.Helper()
	if calls.patched == nil {
		t.Fatal("no reapply record patched")
	}
	st := calls.patched.Status
	if st.OutOfSync || st.OutOfSyncReason != "" || st.OutOfSyncError != "" {
		t.Errorf("OutOfSync fields not cleared by the reapply: %+v", st)
	}
	ann := calls.patched.Metadata.Annotations
	if ann[cellconfig.AnnotationRenderBlueprintRevision] == "" || ann[cellconfig.AnnotationRenderConfigRevision] == "" {
		t.Errorf("render annotations not recorded: %v", ann)
	}
	prov := calls.patched.Spec.Provenance
	if prov == nil || prov.BindingKind != ext.BindingKindConfig || prov.BindingRef.Name != "prod" {
		t.Errorf("provenance not recorded from the fresh render: %+v", prov)
	}
}

// TestStartCell_RecoveryStateRecreatesFromFreshRender is case 1 (AC1): an
// Error, Failed or Degraded lineage cell whose Config changed is recreated
// from the fresh render in one start, whatever the change class, and the
// reapply is recorded.
func TestStartCell_RecoveryStateRecreatesFromFreshRender(t *testing.T) {
	for name, state := range map[string]intmodel.CellState{
		"Error": intmodel.CellStateError, "Failed": intmodel.CellStateFailed, "Degraded": intmodel.CellStateDegraded,
	} {
		t.Run(name, func(t *testing.T) {
			live := reapplyLineageCell("test-realm", "test-space", "test-stack")
			live.Status.State = state
			live.Status.OutOfSync = false // the tick has not flagged it yet
			calls := &lineageCalls{}
			f := newLineageStartRunner(t, live, calls, sampleConfigLookup, sampleBlueprintLookup)

			res, err := setupTestController(t, f).StartCell(
				buildTestCell("prod", "test-realm", "test-space", "test-stack"))
			if err != nil {
				t.Fatalf("StartCell: %v", err)
			}
			if len(calls.recreated) != 1 {
				t.Fatalf("RecreateCell calls = %d, want 1", len(calls.recreated))
			}
			if got := reapplyContainerImage(calls.recreated[0], "main"); got != "nginx:2.0" {
				t.Errorf("RecreateCell main image = %q, want the fresh render's nginx:2.0", got)
			}
			if len(calls.started) != 0 || len(calls.updated) != 0 {
				t.Errorf("StartCell/UpdateCell ran on a recovery start: %d/%d", len(calls.started), len(calls.updated))
			}
			if !res.Started {
				t.Error("Started = false")
			}
			assertReapplyRecorded(t, calls)
		})
	}
}

// TestStartCell_RecoveryStateFallsBackToOnDiskSpec is case 2: with the
// lineage Config deleted, a render failure, nothing to apply, or a failed
// recreate of the fresh spec, a recovery start recreates from the on-disk
// spec and records nothing.
func TestStartCell_RecoveryStateFallsBackToOnDiskSpec(t *testing.T) {
	for _, tc := range []struct {
		name          string
		getConfig     func(intmodel.CellConfig) (intmodel.CellConfig, error)
		getBlueprint  func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error)
		liveImage     string
		failFirst     bool
		wantRecreates int
	}{
		{
			name: "config deleted",
			getConfig: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
				return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
			},
			getBlueprint:  sampleBlueprintLookup,
			wantRecreates: 1,
		},
		{
			name:      "render fails",
			getConfig: sampleConfigLookup,
			getBlueprint: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
				return intmodel.CellBlueprint{}, errdefs.ErrBlueprintNotFound
			},
			wantRecreates: 1,
		},
		{
			name:          "render matches the on-disk spec",
			getConfig:     sampleConfigLookup,
			getBlueprint:  sampleBlueprintLookup,
			liveImage:     "nginx:2.0",
			wantRecreates: 1,
		},
		{
			name:          "recreate from the fresh spec fails",
			getConfig:     sampleConfigLookup,
			getBlueprint:  sampleBlueprintLookup,
			failFirst:     true,
			wantRecreates: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := reapplyLineageCell("test-realm", "test-space", "test-stack")
			live.Status.State = intmodel.CellStateError
			if tc.liveImage != "" {
				live.Spec.Containers[0].Image = tc.liveImage
			}
			calls := &lineageCalls{}
			f := newLineageStartRunner(t, live, calls, tc.getConfig, tc.getBlueprint)
			if tc.failFirst {
				f.RecreateCellFn = func(c intmodel.Cell) (intmodel.Cell, error) {
					calls.recreated = append(calls.recreated, c)
					if len(calls.recreated) == 1 {
						return intmodel.Cell{}, errors.New("pull failed")
					}
					c.Status.State = intmodel.CellStateReady
					return c, nil
				}
			}

			if _, err := setupTestController(t, f).StartCell(
				buildTestCell("prod", "test-realm", "test-space", "test-stack")); err != nil {
				t.Fatalf("StartCell: %v", err)
			}
			if len(calls.recreated) != tc.wantRecreates {
				t.Fatalf("RecreateCell calls = %d, want %d", len(calls.recreated), tc.wantRecreates)
			}
			last := calls.recreated[len(calls.recreated)-1]
			if got, want := reapplyContainerImage(last, "main"), live.Spec.Containers[0].Image; got != want {
				t.Errorf("final RecreateCell main image = %q, want the on-disk %q", got, want)
			}
			if calls.patched != nil {
				t.Errorf("a recovery from the on-disk spec recorded a reapply: %+v", calls.patched.Status)
			}
		})
	}
}

// TestStartCell_LineageReapplyIgnoresStatusGates covers cases 3, 4 and 9
// (AC2, AC5): neither a clear Status.OutOfSync (the tick has not run since
// the Config edit) nor a stale Status.OutOfSyncError (the render failed
// before the Config was fixed) gates the start-time reapply. The Compatible
// diff is applied in place and the reapply records the fresh render and
// clears the OutOfSync fields itself.
func TestStartCell_LineageReapplyIgnoresStatusGates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*intmodel.CellStatus)
	}{
		{name: "OutOfSync false", mutate: func(s *intmodel.CellStatus) {
			s.OutOfSync, s.OutOfSyncReason = false, ""
		}},
		{name: "OutOfSyncError set", mutate: func(s *intmodel.CellStatus) {
			s.OutOfSync, s.OutOfSyncReason = false, ""
			s.OutOfSyncError = "referenced blueprint missing"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := reapplyLineageCell("test-realm", "test-space", "test-stack")
			tc.mutate(&live.Status)
			calls := &lineageCalls{}
			f := newLineageStartRunner(t, live, calls, sampleConfigLookup, sampleBlueprintLookup)

			res, err := setupTestController(t, f).StartCell(
				buildTestCell("prod", "test-realm", "test-space", "test-stack"))
			if err != nil {
				t.Fatalf("StartCell: %v", err)
			}
			if len(calls.lookups) == 0 {
				t.Fatal("the lineage Config was never read; the reapply was gated")
			}
			if len(calls.started) != 1 || reapplyContainerImage(calls.started[0], "main") != "nginx:1.0" {
				t.Fatalf("runner.StartCell calls = %d, want one on the on-disk spec", len(calls.started))
			}
			if len(calls.updated) != 1 || reapplyContainerImage(calls.updated[0], "main") != "nginx:2.0" {
				t.Fatalf("runner.UpdateCell calls = %d, want one with the fresh render", len(calls.updated))
			}
			if len(calls.recreated) != 0 {
				t.Errorf("RecreateCell ran for a Compatible diff")
			}
			if !res.Started || res.Cell.Status.OutOfSync || res.Cell.Status.OutOfSyncError != "" {
				t.Errorf("result = started %v status %+v, want started and Synced", res.Started, res.Cell.Status)
			}
			assertReapplyRecorded(t, calls)
		})
	}
}

// TestStartCell_LineageReapplyUpdateFailureRecordsNothing: when the in-place
// UpdateCell fails the cell is still started, but the reapply is not
// recorded, so the cell does not read Synced while running the old spec.
func TestStartCell_LineageReapplyUpdateFailureRecordsNothing(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")
	calls := &lineageCalls{}
	f := newLineageStartRunner(t, live, calls, sampleConfigLookup, sampleBlueprintLookup)
	f.UpdateCellFn = func(intmodel.Cell) (intmodel.Cell, error) {
		return intmodel.Cell{}, errors.New("image pull failed")
	}
	res, err := setupTestController(t, f).StartCell(
		buildTestCell("prod", "test-realm", "test-space", "test-stack"))
	if err != nil {
		t.Fatalf("StartCell: %v", err)
	}
	if !res.Started {
		t.Error("Started = false, want the cell started on its on-disk spec")
	}
	if calls.patches != 0 {
		t.Errorf("PatchCellMetadata calls = %d, want 0 after a failed in-place apply", calls.patches)
	}
}

// provenanceBoundCell is a lineage cell in test-realm/test-space/default —
// where a space-scoped Config places its cells — whose provenance records
// that it was rendered from the space-scoped Config "prod"
// (test-realm/test-space), the shape of the A3 repro in issue #1569.
func provenanceBoundCell() intmodel.Cell {
	live := reapplyLineageCell("test-realm", "test-space", "default")
	live.Spec.Provenance = &intmodel.CellProvenance{
		BindingKind: ext.BindingKindConfig,
		BindingRef:  intmodel.CellBindingRef{Name: "prod", Realm: "test-realm", Space: "test-space"},
	}
	return live
}

// shadowedConfigLookup serves a space-scoped "prod" Config (blueprint web,
// nginx:2.0) and a same-named impostor scoped to the cell's own stack
// (blueprint impostor). With spaceDeleted the space-scoped one is gone.
func shadowedConfigLookup(spaceDeleted bool) func(intmodel.CellConfig) (intmodel.CellConfig, error) {
	return func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
		switch {
		case c.Metadata.Stack == "default":
			impostor := reapplySampleCellConfig("test-realm", "test-space", "default")
			impostor.Document = []byte(`apiVersion: v1beta1
kind: CellConfig
metadata:
  name: prod
  realm: test-realm
  space: test-space
  stack: default
spec:
  blueprint:
    name: impostor
    realm: test-realm
`)
			return impostor, nil
		case c.Metadata.Space == "test-space" && !spaceDeleted:
			return reapplySampleCellConfig("test-realm", "test-space", ""), nil
		default:
			return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
		}
	}
}

func impostorAwareBlueprintLookup(t *testing.T) func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
	return func(b intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
		if b.Metadata.Name != "web" {
			t.Errorf("GetBlueprint(%q): the impostor Config was used", b.Metadata.Name)
		}
		return reapplySampleBlueprint("test-realm"), nil
	}
}

// TestStartCell_LineageLookupUsesProvenance is the start half of cases 5 and
// 6 (AC3): with same-named Configs at space and stack scope, a cell stamped
// from the space-scoped one is reapplied from that one only; when that one is
// deleted, the stack-scoped one is not used in its place.
func TestStartCell_LineageLookupUsesProvenance(t *testing.T) {
	spaceRef := intmodel.CellConfigMetadata{Name: "prod", Realm: "test-realm", Space: "test-space"}

	t.Run("bound config present", func(t *testing.T) {
		calls := &lineageCalls{}
		f := newLineageStartRunner(t, provenanceBoundCell(), calls,
			shadowedConfigLookup(false), impostorAwareBlueprintLookup(t))
		if _, err := setupTestController(t, f).StartCell(
			buildTestCell("prod", "test-realm", "test-space", "default")); err != nil {
			t.Fatalf("StartCell: %v", err)
		}
		if len(calls.lookups) != 1 || calls.lookups[0].Name != spaceRef.Name ||
			calls.lookups[0].Realm != spaceRef.Realm || calls.lookups[0].Space != spaceRef.Space ||
			calls.lookups[0].Stack != "" {
			t.Errorf("GetConfig lookups = %+v, want exactly one at %+v", calls.lookups, spaceRef)
		}
		if len(calls.updated) != 1 || reapplyContainerImage(calls.updated[0], "main") != "nginx:2.0" {
			t.Errorf("UpdateCell calls = %d, want the space Config's render", len(calls.updated))
		}
	})

	t.Run("bound config deleted", func(t *testing.T) {
		calls := &lineageCalls{}
		f := newLineageStartRunner(t, provenanceBoundCell(), calls,
			shadowedConfigLookup(true), impostorAwareBlueprintLookup(t))
		if _, err := setupTestController(t, f).StartCell(
			buildTestCell("prod", "test-realm", "test-space", "default")); err != nil {
			t.Fatalf("StartCell: %v", err)
		}
		if len(calls.lookups) != 1 {
			t.Errorf("GetConfig lookups = %+v, want one exact lookup and no widening", calls.lookups)
		}
		if len(calls.updated) != 0 || len(calls.recreated) != 0 || calls.patches != 0 {
			t.Errorf("reapplied from another Config: updated=%d recreated=%d patches=%d",
				len(calls.updated), len(calls.recreated), calls.patches)
		}
		if len(calls.started) != 1 || reapplyContainerImage(calls.started[0], "main") != "nginx:1.0" {
			t.Errorf("want one plain start on the on-disk spec, got %d", len(calls.started))
		}
	})
}
