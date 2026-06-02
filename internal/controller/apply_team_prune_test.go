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

package controller_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestApplyDocuments_TeamPruneScopedToLabel pins issue #1027's pruning
// apply contract: re-applying a shrunken team set must (a) persist the
// new set with the team label stamped, (b) delete the team's prior
// objects that fell out of the set, and (c) leave other teams' objects
// and unlabeled objects untouched.
func TestApplyDocuments_TeamPruneScopedToLabel(t *testing.T) {
	storedBP := []intmodel.CellBlueprint{
		labeledBlueprint("alpha-keep", "default", "alpha"),
		labeledBlueprint("alpha-drop", "default", "alpha"),
		labeledBlueprint("beta-keep", "default", "beta"),
		unlabeledBlueprint("unlabeled-keep", "default"),
	}
	storedCfg := []intmodel.CellConfig{
		labeledConfig("alpha-cfg-drop", "default", "alpha"),
		labeledConfig("beta-cfg-keep", "default", "beta"),
	}

	var (
		deletedBP, deletedCfg []string
		writtenBP             []intmodel.CellBlueprint
	)

	mock := &fakeRunner{
		// Scope existence — Blueprint reconcile checks GetRealm.
		GetRealmFn: func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil },
		GetSpaceFn: func(s intmodel.Space) (intmodel.Space, error) { return s, nil },
		GetStackFn: func(s intmodel.Stack) (intmodel.Stack, error) { return s, nil },

		WriteBlueprintFn: func(bp intmodel.CellBlueprint) (bool, error) {
			writtenBP = append(writtenBP, bp)
			return false, nil
		},
		ListBlueprintsFn: func(_, _, _ string) ([]intmodel.CellBlueprint, error) {
			return storedBP, nil
		},
		DeleteBlueprintFn: func(bp intmodel.CellBlueprint) error {
			deletedBP = append(deletedBP, bp.Metadata.Name)
			return nil
		},

		ListConfigsFn: func(_, _, _ string) ([]intmodel.CellConfig, error) {
			return storedCfg, nil
		},
		DeleteConfigFn: func(cfg intmodel.CellConfig) error {
			deletedCfg = append(deletedCfg, cfg.Metadata.Name)
			return nil
		},
	}

	exec := setupTestController(t, mock)

	docs := []parser.Document{
		blueprintDoc(0, "alpha-keep", "default"),
	}
	result, err := exec.ApplyDocuments(docs, "alpha")
	if err != nil {
		t.Fatalf("ApplyDocuments(team=alpha) error = %v", err)
	}

	// The applied blueprint must have been written with the team label
	// stamped onto its persisted document.
	if len(writtenBP) != 1 {
		t.Fatalf("WriteBlueprint call count = %d, want 1", len(writtenBP))
	}
	if !strings.Contains(string(writtenBP[0].Document), v1beta1.LabelTeam+": alpha") {
		t.Errorf("written blueprint document missing team label stamp:\n%s", writtenBP[0].Document)
	}

	// Prune must touch only the alpha-team orphans — not other teams,
	// not unlabeled objects, not the applied set member.
	sort.Strings(deletedBP)
	wantDeletedBP := []string{"alpha-drop"}
	if !equalStringSlices(deletedBP, wantDeletedBP) {
		t.Errorf("deleted blueprints = %v, want %v", deletedBP, wantDeletedBP)
	}
	sort.Strings(deletedCfg)
	wantDeletedCfg := []string{"alpha-cfg-drop"}
	if !equalStringSlices(deletedCfg, wantDeletedCfg) {
		t.Errorf("deleted configs = %v, want %v", deletedCfg, wantDeletedCfg)
	}

	// The result must surface the prune actions so the caller can render
	// the count without re-querying.
	var pruneActions []string
	for _, r := range result.Resources {
		if r.Action == "pruned" {
			pruneActions = append(pruneActions, r.Kind+"/"+r.Name)
		}
	}
	sort.Strings(pruneActions)
	wantPruneActions := []string{"CellBlueprint/alpha-drop", "CellConfig/alpha-cfg-drop"}
	if !equalStringSlices(pruneActions, wantPruneActions) {
		t.Errorf("result prune actions = %v, want %v", pruneActions, wantPruneActions)
	}
}

// TestApplyDocuments_TeamPruneEmptySet covers AC bullet 2 in the empty
// case: applying an empty set with a team label still prunes every
// daemon-stored object carrying that label — the team's full roster was
// removed from the source.
func TestApplyDocuments_TeamPruneEmptySet(t *testing.T) {
	storedBP := []intmodel.CellBlueprint{
		labeledBlueprint("alpha-1", "default", "alpha"),
		labeledBlueprint("alpha-2", "default", "alpha"),
	}

	var deleted []string
	mock := &fakeRunner{
		GetRealmFn: func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil },
		ListBlueprintsFn: func(_, _, _ string) ([]intmodel.CellBlueprint, error) {
			return storedBP, nil
		},
		ListConfigsFn: func(_, _, _ string) ([]intmodel.CellConfig, error) {
			return nil, nil
		},
		DeleteBlueprintFn: func(bp intmodel.CellBlueprint) error {
			deleted = append(deleted, bp.Metadata.Name)
			return nil
		},
	}
	exec := setupTestController(t, mock)

	// An empty applied set requires at least one document to satisfy
	// the parseAndValidate "no valid documents" gate at the wire layer,
	// but the controller-level ApplyDocuments accepts an empty slice —
	// pass nil to exercise the pure prune path.
	result, err := exec.ApplyDocuments(nil, "alpha")
	if err != nil {
		t.Fatalf("ApplyDocuments(nil, alpha) error = %v", err)
	}

	sort.Strings(deleted)
	want := []string{"alpha-1", "alpha-2"}
	if !equalStringSlices(deleted, want) {
		t.Errorf("deleted = %v, want %v (empty applied set must prune the whole team)", deleted, want)
	}
	if len(result.Resources) != len(want) {
		t.Errorf("result.Resources len = %d, want %d", len(result.Resources), len(want))
	}
}

// TestApplyDocuments_NoTeamSkipsPrune confirms the historical
// no-team `kuke apply -f` path stays prune-free: team="" must not call
// ListBlueprints / ListConfigs and must not delete anything.
func TestApplyDocuments_NoTeamSkipsPrune(t *testing.T) {
	var (
		listCalled   bool
		deleteCalled bool
	)
	mock := &fakeRunner{
		GetRealmFn: func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil },
		WriteBlueprintFn: func(intmodel.CellBlueprint) (bool, error) {
			return false, nil
		},
		ListBlueprintsFn: func(_, _, _ string) ([]intmodel.CellBlueprint, error) {
			listCalled = true
			return nil, nil
		},
		ListConfigsFn: func(_, _, _ string) ([]intmodel.CellConfig, error) {
			listCalled = true
			return nil, nil
		},
		DeleteBlueprintFn: func(intmodel.CellBlueprint) error {
			deleteCalled = true
			return nil
		},
	}
	exec := setupTestController(t, mock)

	docs := []parser.Document{blueprintDoc(0, "any", "default")}
	if _, err := exec.ApplyDocuments(docs, ""); err != nil {
		t.Fatalf("ApplyDocuments(team='') error = %v", err)
	}
	if listCalled {
		t.Error("team='' apply must not enumerate daemon objects (no prune)")
	}
	if deleteCalled {
		t.Error("team='' apply must not delete anything (no prune)")
	}
}

func labeledBlueprint(name, realm, team string) intmodel.CellBlueprint {
	return intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{
			Name:   name,
			Realm:  realm,
			Labels: map[string]string{v1beta1.LabelTeam: team},
		},
	}
}

func unlabeledBlueprint(name, realm string) intmodel.CellBlueprint {
	return intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: name, Realm: realm},
	}
}

func labeledConfig(name, realm, team string) intmodel.CellConfig {
	return intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{
			Name:   name,
			Realm:  realm,
			Labels: map[string]string{v1beta1.LabelTeam: team},
		},
	}
}

// blueprintDoc builds a parser.Document carrying a minimal CellBlueprintDoc
// at realm scope. The body is irrelevant to the team-prune contract; the
// metadata is what the apply path stamps and persists.
func blueprintDoc(index int, name, realm string) parser.Document {
	bp := &v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  name,
			Realm: realm,
		},
		Spec: v1beta1.CellBlueprintSpec{
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "root", Root: true, Image: "busybox:latest"},
				},
			},
		},
	}
	return parser.Document{
		Index:            index,
		Kind:             v1beta1.KindCellBlueprint,
		APIVersion:       v1beta1.APIVersionV1Beta1,
		CellBlueprintDoc: bp,
	}
}

