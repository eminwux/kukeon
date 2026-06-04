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

func TestStartCell_OutOfSyncReapply_RecreatesFromConfig(t *testing.T) {
	live := reapplyLineageCell("test-realm", "test-space", "test-stack")

	var recreateCalled bool
	var startCellCalled bool
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
		RecreateCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			// The desired cell must carry the materialised spec — image bumped
			// to nginx:2.0 from the Blueprint — and the live cell's identity.
			if cell.Metadata.Name != "prod" {
				t.Errorf("RecreateCell name = %q, want prod", cell.Metadata.Name)
			}
			if cell.Spec.RealmName != "test-realm" {
				t.Errorf("RecreateCell realm = %q, want test-realm", cell.Spec.RealmName)
			}
			gotImage := ""
			for _, c := range cell.Spec.Containers {
				if c.ID == "main" {
					gotImage = c.Image
					break
				}
			}
			if gotImage != "nginx:2.0" {
				t.Errorf("RecreateCell main image = %q, want nginx:2.0 (materialised from Blueprint)", gotImage)
			}
			cell.Status.State = intmodel.CellStateReady
			return cell, nil
		},
		StartCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			startCellCalled = true
			return intmodel.Cell{}, errors.New("runner.StartCell must not be called after RecreateCell already brought the cell to Ready")
		},
		UpdateCellMetadataFn: func(intmodel.Cell) error { return nil },
	}

	ctrl := setupTestController(t, f)
	res, err := ctrl.StartCell(buildTestCell("prod", "test-realm", "test-space", "test-stack"))
	if err != nil {
		t.Fatalf("StartCell returned error: %v", err)
	}
	if !recreateCalled {
		t.Error("RecreateCell was not called; reapply path was skipped")
	}
	if startCellCalled {
		t.Error("runner.StartCell was called after RecreateCell — double-start")
	}
	if !res.Started {
		t.Error("Started = false, want true after successful reapply")
	}
	if res.Cell.Status.State != intmodel.CellStateReady {
		t.Errorf("cell state = %v, want Ready after RecreateCell", res.Cell.Status.State)
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
// graceful degradation: if RecreateCell fails (e.g. containerd unreachable),
// reapply logs and falls back to the on-disk spec via runner.StartCell so the
// operator's `kuke start` request is honored.
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
			return reapplySampleBlueprint("test-realm"), nil
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
