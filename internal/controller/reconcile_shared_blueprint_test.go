// Copyright 2026 Emiliano Spinella (eminwux)
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

package controller_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// sharedLineageConfig is sampleConfig under another name, labelled for team: one of
// several teams' Configs on the same shared blueprint.
func sharedLineageConfig(name, team string) ext.CellConfigDoc {
	doc := sampleConfig()
	doc.Metadata.Name = name
	doc.Metadata.Labels = map[string]string{ext.LabelTeam: team}
	return doc
}

// TestSharedBlueprintReplacedInPlaceReachesEveryTeamsCells pins the runtime
// half of #1560: when one team's `kuke team` apply replaces a shared blueprint
// in place, the running cells of every team's Configs on it report OutOfSync
// on the next reconcile tick, and restarting one brings it to the new content.
func TestSharedBlueprintReplacedInPlaceReachesEveryTeamsCells(t *testing.T) {
	configs := map[string]ext.CellConfigDoc{}
	var live []intmodel.Cell
	for _, cfg := range []ext.CellConfigDoc{
		sharedLineageConfig("kuke-majordomo-codex", "kuke"),
		sharedLineageConfig("kukeon-pm-codex", "kukeon"),
	} {
		configs[cfg.Metadata.Name] = cfg
		// Each cell was created from the blueprint as it was before the apply.
		doc, err := cellconfig.MaterializeWithName(cfg, sampleReferencedBlueprint(), cellconfig.Prefix(cfg))
		if err != nil {
			t.Fatalf("MaterializeWithName(%s): %v", cfg.Metadata.Name, err)
		}
		cell, err := apischeme.ConvertCellDocToInternal(doc)
		if err != nil {
			t.Fatalf("ConvertCellDocToInternal: %v", err)
		}
		live = append(live, cell)
	}
	if live[0].Metadata.Name == live[1].Metadata.Name {
		t.Fatal("fixture cells collide")
	}

	// The apply replaced "dev" in place: same name, new content.
	replaced := sampleReferencedBlueprint()
	replaced.Spec.Cell.Containers[0].Image = "img:v2"

	updated := map[string]intmodel.Cell{}
	mock := stubLineageRunner(
		func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
			cfg, ok := configs[c.Metadata.Name]
			if !ok {
				t.Fatalf("GetConfig(%q): unexpected lookup", c.Metadata.Name)
			}
			return configCarrier(t, cfg), nil
		},
		func(b intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			if b.Metadata.Name != "dev" {
				t.Fatalf("GetBlueprint(%q), want the shared plain name dev", b.Metadata.Name)
			}
			return blueprintCarrier(t, replaced), nil
		},
		func(c intmodel.Cell) error {
			updated[c.Metadata.Name] = c
			return nil
		},
	)
	mock.ListCellsFn = func(string, string, string) ([]intmodel.Cell, error) { return live, nil }
	mock.PatchCellMetadataFn = patchAgainstCellSet(t, live, mock.UpdateCellMetadataFn)
	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() error = %v", err)
	}
	if res.CellsUpdated != len(live) {
		t.Errorf("CellsUpdated = %d, want %d: every team's cell is OutOfSync", res.CellsUpdated, len(live))
	}
	for _, cell := range live {
		got, ok := updated[cell.Metadata.Name]
		if !ok {
			t.Fatalf("cell %s: no OutOfSync status persisted", cell.Metadata.Name)
		}
		if !got.Status.OutOfSync || got.Status.OutOfSyncError != "" ||
			!strings.Contains(got.Status.OutOfSyncReason, "image") {
			t.Fatalf("cell %s: OutOfSync=%v reason=%q error=%q, want an image drift",
				cell.Metadata.Name, got.Status.OutOfSync, got.Status.OutOfSyncReason, got.Status.OutOfSyncError)
		}
	}

	// `kuke restart` = stop + start: StartCell on the stopped OutOfSync cell
	// re-renders it from its Config and the replaced blueprint.
	stopped := updated[live[1].Metadata.Name]
	stopped.Status.State = intmodel.CellStateStopped
	var restartedImage string
	mock.GetCellFn = func(intmodel.Cell) (intmodel.Cell, error) { return stopped, nil }
	mock.ExistsCgroupFn = func(any) (bool, error) { return true, nil }
	mock.ExistsCellRootContainerFn = func(intmodel.Cell) (bool, error) { return true, nil }
	mock.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
		cell.Status.State = intmodel.CellStateReady
		return cell, nil
	}
	mock.UpdateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
		restartedImage = reapplyContainerImage(cell, "main")
		cell.Status.State = intmodel.CellStateReady
		return cell, nil
	}
	mock.RecreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
		restartedImage = reapplyContainerImage(cell, "main")
		cell.Status.State = intmodel.CellStateReady
		return cell, nil
	}
	start, err := ctrl.StartCell(stopped)
	if err != nil {
		t.Fatalf("StartCell: %v", err)
	}
	if !start.Started || restartedImage != "img:v2" {
		t.Fatalf("restart: started=%v image=%q, want the replaced blueprint's img:v2", start.Started, restartedImage)
	}
}
