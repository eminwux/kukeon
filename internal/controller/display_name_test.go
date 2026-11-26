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
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestSetDisplayName_Config (issue #1692): renaming an agent rewrites only
// its Config's display-name annotation, keeps the rest of the document, and
// an empty name clears it.
func TestSetDisplayName_Config(t *testing.T) {
	store := newTeamStore(t)
	doc := teamConfig("default-agent-claude-1a2b3c", "", sharedRef("claude"))
	doc.Metadata.Labels = map[string]string{v1beta1.LabelRole: "agent", v1beta1.LabelHarness: "claude"}
	doc.Metadata.Annotations = map[string]string{v1beta1.AnnotationAgentTemplate: "default-agent-claude"}
	doc.Spec.Values = map[string]string{"STATE_VOLUME": "default-agent-claude-state"}
	store.storeConfig(doc)
	exec := setupTestController(t, store.runner())
	target := controller.DisplayNameTarget{
		Kind: controller.DisplayNameConfig, Name: doc.Metadata.Name, Realm: "default", Space: "default", Stack: "default",
	}
	stored := func() v1beta1.CellConfigDoc {
		t.Helper()
		got, err := apischeme.ConvertCellConfigToExternal(store.configs["default/default/default/"+doc.Metadata.Name])
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	res, err := exec.SetDisplayName(target, "  Reviewer ")
	if err != nil {
		t.Fatal(err)
	}
	got := stored()
	if !res.Changed || res.DisplayName != "Reviewer" ||
		got.Metadata.Annotations[v1beta1.AnnotationDisplayName] != "Reviewer" ||
		got.Metadata.Annotations[v1beta1.AnnotationAgentTemplate] != "default-agent-claude" ||
		got.Metadata.Labels[v1beta1.LabelRole] != "agent" || got.Spec.Values["STATE_VOLUME"] != "default-agent-claude-state" {
		t.Fatalf("result %+v, stored %+v", res, got)
	}
	if res, err = exec.SetDisplayName(target, "Reviewer"); err != nil || res.Changed {
		t.Fatalf("same name again: %+v, %v", res, err)
	}
	if res, err = exec.SetDisplayName(target, ""); err != nil || !res.Changed {
		t.Fatalf("clear: %+v, %v", res, err)
	}
	if _, set := stored().Metadata.Annotations[v1beta1.AnnotationDisplayName]; set {
		t.Fatal("an empty name did not clear the display name")
	}

	missing := target
	missing.Name = "absent"
	if _, err = exec.SetDisplayName(missing, "x"); !errors.Is(err, errdefs.ErrConfigNotFound) {
		t.Fatalf("missing config: %v", err)
	}
	if _, err = exec.SetDisplayName(target, "two\nlines"); !errors.Is(err, v1beta1.ErrInvalidDisplayName) {
		t.Fatalf("invalid name: %v", err)
	}
	unsafe := target
	unsafe.Name = "../escape"
	if _, err = exec.SetDisplayName(unsafe, "x"); !errors.Is(err, errdefs.ErrConfig) {
		t.Fatalf("unsafe name: %v", err)
	}
}

// TestSetDisplayName_Cell: renaming an instance patches only the cell's
// annotations, through the locked metadata patch, and never its spec or
// status.
func TestSetDisplayName_Cell(t *testing.T) {
	live := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name:        "default-agent-claude-1a2b3c-4d5e6f",
			Annotations: map[string]string{"kukeon.io/render-digest": "abc"},
			Generation:  3,
		},
		Spec:   intmodel.CellSpec{RealmName: "default", SpaceName: "default", StackName: "default"},
		Status: intmodel.CellStatus{OutOfSync: false},
	}
	patches := 0
	mock := &fakeRunner{
		PatchCellMetadataFn: func(cell intmodel.Cell, mutate func(fresh *intmodel.Cell) bool) error {
			if cell.Metadata.Name != live.Metadata.Name || cell.Spec.StackName != "default" {
				t.Fatalf("patched %+v", cell)
			}
			fresh := live
			if mutate(&fresh) {
				patches++
				live = fresh
			}
			return nil
		},
	}
	exec := setupTestController(t, mock)
	target := controller.DisplayNameTarget{
		Kind: controller.DisplayNameCell, Name: live.Metadata.Name, Realm: "default", Space: "default", Stack: "default",
	}
	res, err := exec.SetDisplayName(target, "Docs bot #1")
	if err != nil || !res.Changed || patches != 1 {
		t.Fatalf("res=%+v err=%v patches=%d", res, err, patches)
	}
	if live.Metadata.Annotations[v1beta1.AnnotationDisplayName] != "Docs bot #1" ||
		live.Metadata.Annotations["kukeon.io/render-digest"] != "abc" || live.Metadata.Generation != 3 ||
		live.Status.OutOfSync {
		t.Fatalf("cell after rename = %+v", live)
	}
	if _, err = exec.SetDisplayName(target, "Docs bot #1"); err != nil || patches != 1 {
		t.Fatalf("an unchanged name was written: %v patches=%d", err, patches)
	}
	if _, err = exec.SetDisplayName(target, ""); err != nil || patches != 2 {
		t.Fatalf("clear: %v patches=%d", err, patches)
	}
	if _, set := live.Metadata.Annotations[v1beta1.AnnotationDisplayName]; set {
		t.Fatal("the display name was not cleared")
	}
	partial := target
	partial.Stack = ""
	if _, err = exec.SetDisplayName(partial, "x"); !errors.Is(err, errdefs.ErrConfig) {
		t.Fatalf("a cell without a stack: %v", err)
	}
}
