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

// Tests for the daemon-side OutOfSync reapply on StartCell — issue #983.
// `kuke stop` + `kuke start` on a Config-lineage OutOfSync cell must
// produce the same end state as `kuke restart` on the same cell. The
// reapply lives in controller.StartCell so every client that issues StartCell
// (CLI `kuke start`, `kuke run` on existing-Stopped, future API
// consumers) inherits the reconcile-on-start behaviour.

package controller_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// reapplySampleCellConfig builds a Config whose Blueprint reference and
// materialised spec match the reapplySampleBlueprint below. Realm matches the
// test cell's realm so lookupLineageConfig finds it on the first probe.
func reapplySampleCellConfig(realm, space, stack string) intmodel.CellConfig {
	doc := `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: prod
  realm: ` + realm + `
  space: ` + space + `
  stack: ` + stack + `
spec:
  blueprint:
    name: web
    realm: ` + realm + `
`
	return intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{
			Name:  "prod",
			Realm: realm,
			Space: space,
			Stack: stack,
		},
		Document: []byte(doc),
	}
}

func reapplySampleBlueprint(realm string) intmodel.CellBlueprint {
	doc := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: ` + realm + `
spec:
  cell:
    containers:
      - id: main
        image: nginx:2.0
`
	return intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{
			Name:  "web",
			Realm: realm,
		},
		Document: []byte(doc),
	}
}

// reapplyAttachableBlueprint mirrors reapplySampleBlueprint but marks the
// "main" container attachable, so the re-resolve path's ApplyEnvOverrides has a
// container to bake the recorded per-cell --env override into (epic:cell-identity
// P5, #1024). Image bumps to nginx:2.0 from the live cell's nginx:1.0 so a real
// drift still drives RecreateCell.
func reapplyAttachableBlueprint(realm string) intmodel.CellBlueprint {
	doc := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: ` + realm + `
spec:
  cell:
    containers:
      - id: main
        image: nginx:2.0
        attachable: true
`
	return intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{
			Name:  "web",
			Realm: realm,
		},
		Document: []byte(doc),
	}
}

// reapplyBreakingBlueprint materialises the "main" container with
// hostNetwork:true — a host-namespace toggle apply.DiffCell classifies as
// Breaking even on a non-root child (the netns shape is baked into the cell at
// start). The live cell's main carries hostNetwork:false, so this is the sole
// diff and it drives the RecreateCell (overlay-wiping) path. Image stays
// nginx:1.0 so hostNetwork is the only divergence.
func reapplyBreakingBlueprint(realm string) intmodel.CellBlueprint {
	doc := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: ` + realm + `
spec:
  cell:
    containers:
      - id: main
        image: nginx:1.0
        hostNetwork: true
`
	return intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{
			Name:  "web",
			Realm: realm,
		},
		Document: []byte(doc),
	}
}

// reapplyContainerImage returns the image of the container with the given ID in
// the cell, or "" when absent. Used by the in-place reapply tests to assert
// which spec (on-disk vs materialised) each runner method receives.
func reapplyContainerImage(cell intmodel.Cell, id string) string {
	for _, c := range cell.Spec.Containers {
		if c.ID == id {
			return c.Image
		}
	}
	return ""
}

// reapplyLineageCell builds a stopped, OutOfSync, Config-lineage cell with the
// given realm/space/stack scope. The cell's on-disk container image is
// "nginx:1.0" — diverged from the Config's "nginx:2.0" — so the reapply path
// has something to do.
func reapplyLineageCell(realm, space, stack string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name:   "prod",
			Labels: map[string]string{cellconfig.LabelConfig: "prod"},
		},
		Spec: intmodel.CellSpec{
			ID:        "prod",
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			Containers: []intmodel.ContainerSpec{
				{ID: "main", Image: "nginx:1.0"},
			},
		},
		Status: intmodel.CellStatus{
			State:           intmodel.CellStateStopped,
			OutOfSync:       true,
			OutOfSyncReason: "spec differs at containers[main].image",
		},
	}
}

// TestStartCell_OutOfSyncReapply_CompatibleAppliesInPlace pins AC1 of
// epic:cell-identity P7 (#1095): a Compatible diff (here a non-root child image
// bump nginx:1.0 → nginx:2.0) is applied in place — StartCell restarts the cell
// on its existing snapshot so the overlay survives, then UpdateCell applies the
// materialised drift. RecreateCell (which would wipe the overlay) must not run.
func TestStartCell_OutOfSyncReapply_CompatibleAppliesInPlace(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")

	var recreateCalled, startCellCalled, updateCellCalled bool
	f := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return live, nil
		},
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		GetConfigFn: func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
			if c.Metadata.Name != "prod" || c.Metadata.Realm != "test-realm" {
				t.Errorf("GetConfig lookup = %+v, want name=prod realm=test-realm", c.Metadata)
			}
			return reapplySampleCellConfig("test-realm", "test-space", "test-stack"), nil
		},
		GetBlueprintFn: func(b intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			if b.Metadata.Name != "web" {
				t.Errorf("GetBlueprint name = %q, want web", b.Metadata.Name)
			}
			return reapplySampleBlueprint("test-realm"), nil
		},
		RecreateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			return intmodel.Cell{}, errors.New(
				"RecreateCell must not run for a Compatible diff — the overlay would be wiped",
			)
		},
		StartCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			startCellCalled = true
			// The in-place path starts on the live (on-disk) spec so the root
			// snapshot is reused; the materialised image bump is applied by
			// UpdateCell, not here. Starting from the materialised spec would
			// trip ErrCellSpecHashDrift on the changed child.
			if got := reapplyContainerImage(cell, "main"); got != "nginx:1.0" {
				t.Errorf("StartCell main image = %q, want nginx:1.0 (on-disk spec, snapshot reuse)", got)
			}
			cell.Status.State = intmodel.CellStateReady
			return cell, nil
		},
		UpdateCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			updateCellCalled = true
			// UpdateCell receives the materialised spec — image bumped to
			// nginx:2.0 — and the live cell's identity.
			if cell.Metadata.Name != "prod" {
				t.Errorf("UpdateCell name = %q, want prod", cell.Metadata.Name)
			}
			if cell.Spec.RealmName != "test-realm" {
				t.Errorf("UpdateCell realm = %q, want test-realm", cell.Spec.RealmName)
			}
			if got := reapplyContainerImage(cell, "main"); got != "nginx:2.0" {
				t.Errorf("UpdateCell main image = %q, want nginx:2.0 (materialised from Blueprint)", got)
			}
			cell.Status.State = intmodel.CellStateReady
			return cell, nil
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	res, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack"))
	if err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if recreateCalled {
		t.Error("RecreateCell was called for a Compatible diff; the in-place path must be used")
	}
	if !startCellCalled {
		t.Error("runner.StartCell was not called; the in-place reapply was skipped")
	}
	if !updateCellCalled {
		t.Error("runner.UpdateCell was not called; the Compatible drift was not applied in place")
	}
	if !res.Started {
		t.Error("Started = false, want true after successful in-place reapply")
	}
	if res.Cell.Status.State != intmodel.CellStateReady {
		t.Errorf("cell state = %v, want Ready after in-place reapply", res.Cell.Status.State)
	}
}

// TestStartCell_OutOfSyncReapply_BreakingRecreates pins AC2 of
// epic:cell-identity P7 (#1095): a Breaking diff (here a hostNetwork toggle on
// the child, baked into the cell's netns at start) drives RecreateCell — the
// overlay-wiping path — not the in-place StartCell/UpdateCell pair.
func TestStartCell_OutOfSyncReapply_BreakingRecreates(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")

	var recreateCalled, startCellCalled, updateCellCalled bool
	f := &fakeRunner{
		GetCellFn:                 func(_ intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return reapplySampleCellConfig("test-realm", "test-space", "test-stack"), nil
		},
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return reapplyBreakingBlueprint("test-realm"), nil
		},
		RecreateCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			if cell.Metadata.Name != "prod" {
				t.Errorf("RecreateCell name = %q, want prod", cell.Metadata.Name)
			}
			var hostNet bool
			for _, c := range cell.Spec.Containers {
				if c.ID == "main" {
					hostNet = c.HostNetwork
					break
				}
			}
			if !hostNet {
				t.Error("RecreateCell main hostNetwork = false, want true (materialised from Blueprint)")
			}
			cell.Status.State = intmodel.CellStateReady
			return cell, nil
		},
		StartCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			startCellCalled = true
			return intmodel.Cell{}, errors.New(
				"runner.StartCell must not run for a Breaking diff — RecreateCell owns the start",
			)
		},
		UpdateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			updateCellCalled = true
			return intmodel.Cell{}, errors.New("runner.UpdateCell must not run for a Breaking diff")
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	res, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack"))
	if err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if !recreateCalled {
		t.Error("RecreateCell was not called for a Breaking diff; reapply routed in-place instead")
	}
	if startCellCalled {
		t.Error("runner.StartCell was called for a Breaking diff — double-start")
	}
	if updateCellCalled {
		t.Error("runner.UpdateCell was called for a Breaking diff")
	}
	if !res.Started {
		t.Error("Started = false, want true after successful Breaking reapply")
	}
	if res.Cell.Status.State != intmodel.CellStateReady {
		t.Errorf("cell state = %v, want Ready after RecreateCell", res.Cell.Status.State)
	}
}

// TestStartCell_OutOfSyncReapply_EnvOverridePreserved pins the reapply half of
// epic:cell-identity P5 (#1024): a Config-lineage cell created
// `--from-config --env K=V` records the override in Spec.Provenance.EnvOverrides
// (P3 #1023). The reapply path re-materialises from the Config — which has no
// knowledge of the per-cell override — so without re-applying provenance the
// override is silently stripped. This is a Compatible diff (a non-root child
// image bump + env), so post-P7 (#1095) it flows through the in-place
// UpdateCell path; the desired cell handed to UpdateCell must carry the
// materialised Blueprint image *and* the re-baked override (re-applied last,
// P3 precedence).
func TestStartCell_OutOfSyncReapply_EnvOverridePreserved(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")
	// Simulate a cell created `--from-config --env APP_ENV=prod`: the override
	// is baked into the attachable container's Env and recorded in provenance.
	live.Spec.Containers[0].Attachable = true
	live.Spec.Containers[0].Env = []string{"APP_ENV=prod"}
	live.Spec.Provenance = &intmodel.CellProvenance{
		EnvOverrides: []string{"APP_ENV=prod"},
	}

	var updateCellCalled bool
	f := &fakeRunner{
		GetCellFn:                 func(_ intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return reapplySampleCellConfig("test-realm", "test-space", "test-stack"), nil
		},
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return reapplyAttachableBlueprint("test-realm"), nil
		},
		RecreateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{}, errors.New("RecreateCell must not run for a Compatible diff")
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			c.Status.State = intmodel.CellStateReady
			return c, nil
		},
		UpdateCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			updateCellCalled = true
			var main intmodel.ContainerSpec
			for _, c := range cell.Spec.Containers {
				if c.ID == "main" {
					main = c
					break
				}
			}
			if main.Image != "nginx:2.0" {
				t.Errorf("UpdateCell main image = %q, want nginx:2.0 (materialised from Blueprint)", main.Image)
			}
			// The recorded override must survive re-resolve, not be stripped.
			found := false
			for _, e := range main.Env {
				if e == "APP_ENV=prod" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf(
					"UpdateCell main Env = %v, want it to retain APP_ENV=prod (provenance override stripped)",
					main.Env,
				)
			}
			cell.Status.State = intmodel.CellStateReady
			return cell, nil
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	if _, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack")); err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if !updateCellCalled {
		t.Error("UpdateCell was not called; the in-place reapply path was skipped")
	}
}

// TestStartCell_OutOfSyncReapply_CompatibleUpdateErrorStillReady covers the
// in-place degradation path: StartCell brought the cell to Ready on its
// existing snapshot, but UpdateCell failed (e.g. an image pull error). The
// operator's start request is still honoured with the running cell, and the
// persisted OutOfSync flag stays set so the next start retries the apply.
func TestStartCell_OutOfSyncReapply_CompatibleUpdateErrorStillReady(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")

	var startCellCalled, updateCellCalled bool
	f := &fakeRunner{
		GetCellFn:                 func(_ intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return reapplySampleCellConfig("test-realm", "test-space", "test-stack"), nil
		},
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return reapplySampleBlueprint("test-realm"), nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			startCellCalled = true
			c.Status.State = intmodel.CellStateReady
			return c, nil
		},
		UpdateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			updateCellCalled = true
			return intmodel.Cell{}, errors.New("image pull failed")
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	res, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack"))
	if err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if !startCellCalled {
		t.Error("runner.StartCell was not called; the in-place reapply was skipped")
	}
	if !updateCellCalled {
		t.Error("runner.UpdateCell was not called; the Compatible drift was not attempted")
	}
	if !res.Started {
		t.Error("Started = false, want true — the cell is Ready from StartCell even though UpdateCell failed")
	}
	if res.Cell.Status.State != intmodel.CellStateReady {
		t.Errorf("cell state = %v, want Ready (degraded: started but not reconciled)", res.Cell.Status.State)
	}
}

func TestStartCell_OutOfSyncReapply_NoLineageLabelSkipsReapply(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")
	delete(live.Metadata.Labels, cellconfig.LabelConfig) // strip the lineage

	var recreateCalled bool
	var startCellCalled bool
	f := &fakeRunner{
		GetCellFn:                 func(_ intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		RecreateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			return intmodel.Cell{}, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			startCellCalled = true
			c.Status.State = intmodel.CellStateReady
			return c, nil
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	if _, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack")); err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if recreateCalled {
		t.Error("RecreateCell was called for a cell with no lineage label; reapply must skip")
	}
	if !startCellCalled {
		t.Error("runner.StartCell was not called; the normal start path was skipped")
	}
}

func TestStartCell_OutOfSyncReapply_OutOfSyncErrorSkipsReapply(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")
	live.Status.OutOfSyncError = "referenced blueprint missing"
	live.Status.OutOfSync = false // detector blocked on the error

	var recreateCalled, getConfigCalled bool
	f := &fakeRunner{
		GetCellFn:                 func(_ intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			getConfigCalled = true
			return intmodel.CellConfig{}, nil
		},
		RecreateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			return intmodel.Cell{}, nil
		},
		StartCellFn:          func(c intmodel.Cell) (intmodel.Cell, error) { c.Status.State = intmodel.CellStateReady; return c, nil },
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	if _, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack")); err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if getConfigCalled {
		t.Error("GetConfig was called despite OutOfSyncError being set; reapply must skip")
	}
	if recreateCalled {
		t.Error("RecreateCell was called despite OutOfSyncError being set")
	}
}

func TestStartCell_OutOfSyncReapply_OutOfSyncFalseSkipsReapply(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")
	live.Status.OutOfSync = false
	live.Status.OutOfSyncReason = ""

	var getConfigCalled, recreateCalled bool
	f := &fakeRunner{
		GetCellFn:                 func(_ intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			getConfigCalled = true
			return intmodel.CellConfig{}, nil
		},
		RecreateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			return intmodel.Cell{}, nil
		},
		StartCellFn:          func(c intmodel.Cell) (intmodel.Cell, error) { c.Status.State = intmodel.CellStateReady; return c, nil },
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	if _, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack")); err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if getConfigCalled {
		t.Error("GetConfig was called for a Synced cell; reapply must skip")
	}
	if recreateCalled {
		t.Error("RecreateCell was called for a Synced cell")
	}
}

// TestStartCell_OutOfSyncReapply_LineageConfigDeletedFallsThrough covers the
// "lineage Config deleted" case — the reconciler keeps the OutOfSync flag set
// with reason "lineage Config deleted", but the lookup returns MetadataExists
// = false. Reapply must fall through to a vanilla runner.StartCell so the
// runtime is still bounced as the operator asked.
func TestStartCell_OutOfSyncReapply_LineageConfigDeletedFallsThrough(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")

	var recreateCalled, startCellCalled bool
	f := &fakeRunner{
		GetCellFn:                 func(_ intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			// The scope-narrowing walk probes (space, stack), (space, ""), ("", "").
			// All three miss on a deleted Config.
			return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
		},
		RecreateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			return intmodel.Cell{}, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			startCellCalled = true
			c.Status.State = intmodel.CellStateReady
			return c, nil
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	if _, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack")); err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if recreateCalled {
		t.Error("RecreateCell was called despite lineage Config being deleted")
	}
	if !startCellCalled {
		t.Error("runner.StartCell was not called; cell would never start under deleted-Config fall-through")
	}
}

// TestStartCell_OutOfSyncReapply_RecreateErrorFallsThrough covers the
// graceful degradation on the Breaking path: if RecreateCell fails (e.g.
// containerd unreachable), reapply logs and falls back to the on-disk spec via
// the caller's runner.StartCell so the operator's `kuke start` request is
// honored. Uses a Breaking blueprint (hostNetwork toggle) so RecreateCell is
// the path under test.
func TestStartCell_OutOfSyncReapply_RecreateErrorFallsThrough(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")

	var startCellCalled bool
	f := &fakeRunner{
		GetCellFn:                 func(_ intmodel.Cell) (intmodel.Cell, error) { return live, nil },
		ExistsCgroupFn:            func(_ any) (bool, error) { return true, nil },
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) { return true, nil },
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return reapplySampleCellConfig("test-realm", "test-space", "test-stack"), nil
		},
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return reapplyBreakingBlueprint("test-realm"), nil
		},
		RecreateCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{}, errors.New("containerd unreachable")
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			startCellCalled = true
			c.Status.State = intmodel.CellStateReady
			return c, nil
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	if _, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack")); err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if !startCellCalled {
		t.Error("runner.StartCell was not called after RecreateCell failure; cell would never start")
	}
}
