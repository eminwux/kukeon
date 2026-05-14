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

package apply_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/controller/apply"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestDiffRealm_NoChanges(t *testing.T) {
	desired := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name:   "test-realm",
			Labels: map[string]string{"env": "test"},
		},
		Spec: intmodel.RealmSpec{
			Namespace: "test-ns",
		},
	}

	actual := desired

	diff := apply.DiffRealm(desired, actual)
	if diff.HasChanges {
		t.Error("expected no changes")
	}
}

func TestDiffRealm_BreakingChange_Name(t *testing.T) {
	desired := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: "new-realm",
		},
	}

	actual := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: "old-realm",
		},
	}

	diff := apply.DiffRealm(desired, actual)
	if !diff.HasChanges {
		t.Error("expected changes")
	}
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Errorf("expected breaking change, got %v", diff.ChangeType)
	}
	if len(diff.BreakingChanges) == 0 {
		t.Error("expected breaking changes")
	}
}

func TestDiffRealm_CompatibleChange_Labels(t *testing.T) {
	desired := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name:   "test-realm",
			Labels: map[string]string{"env": "prod"},
		},
	}

	actual := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name:   "test-realm",
			Labels: map[string]string{"env": "test"},
		},
	}

	diff := apply.DiffRealm(desired, actual)
	if !diff.HasChanges {
		t.Error("expected changes")
	}
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Errorf("expected compatible change, got %v", diff.ChangeType)
	}
}

func TestDiffCell_RootContainerChanged(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: "test-cell",
		},
		Spec: intmodel.CellSpec{
			RealmName: "realm",
			SpaceName: "space",
			StackName: "stack",
			Containers: []intmodel.ContainerSpec{
				{
					ID:    "root",
					Root:  true,
					Image: "busybox:latest",
				},
			},
		},
	}

	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: "test-cell",
		},
		Spec: intmodel.CellSpec{
			RealmName: "realm",
			SpaceName: "space",
			StackName: "stack",
			Containers: []intmodel.ContainerSpec{
				{
					ID:    "root",
					Root:  true,
					Image: "alpine:latest",
				},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Error("expected changes")
	}
	if !diff.RootContainerChanged {
		t.Error("expected root container to be changed")
	}
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Errorf("expected breaking change, got %v", diff.ChangeType)
	}
}

// TestDiffCell_ManagedLabels_NoChange covers the apply-side label leak called
// out in issue #437. The runner injects canonical labels (cell.kukeon.io,
// realm.kukeon.io, …) on CreateCell. A user YAML that omits labels must not
// be reported as a `metadata.labels changed` drift on the same-file re-apply
// — otherwise apply reports `updated` on the unchanged file and falls through
// to UpdateCell which clobbers the canonical labels with the user's empty
// map.
func TestDiffCell_ManagedLabels_NoChange(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "web", Image: "busybox:latest"},
			},
		},
	}

	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: "hello-world",
			Labels: map[string]string{
				"cell.kukeon.io":  "hello-world",
				"realm.kukeon.io": "default",
				"space.kukeon.io": "default",
				"stack.kukeon.io": "default",
			},
		},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
				{ID: "web", Image: "busybox:latest"},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if diff.HasChanges {
		t.Errorf("expected no changes when actual carries only controller-managed labels, got: %+v", diff)
	}
}

// TestDiffCell_UserLabels_StillDetectsDrift makes sure the managed-label
// filter does not over-narrow: a real user-authored label change must still
// register as drift.
func TestDiffCell_UserLabels_StillDetectsDrift(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name:   "hello-world",
			Labels: map[string]string{"env": "prod"},
		},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "web", Image: "busybox:latest"},
			},
		},
	}

	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: "hello-world",
			Labels: map[string]string{
				"cell.kukeon.io": "hello-world",
				"env":            "staging",
			},
		},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
				{ID: "web", Image: "busybox:latest"},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Error("expected drift on user-authored label change")
	}
	found := false
	for _, f := range diff.ChangedFields {
		if f == "metadata.labels" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected metadata.labels in ChangedFields, got %v", diff.ChangedFields)
	}
}

// TestDiffCell_SynthesizedRoot_NoChange exercises the same-file re-apply path
// for a YAML that omits a root container (the canonical case —
// docs/examples/hello-world.yaml). The runner synthesizes a root entry during
// create, so the on-disk Containers slice picks up a `Root: true` element the
// user never authored. DiffCell must not treat that synthesized root as a
// removal — doing so trips RecreateCell and produces the spurious
// `Cell <name>: updated\n  - root container recreated` output on every
// idempotent re-apply (issue #437).
func TestDiffCell_SynthesizedRoot_NoChange(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{
					ID:    "web",
					Image: "busybox:latest",
				},
			},
		},
	}

	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{
					ID:    "root",
					Root:  true,
					Image: "busybox:latest",
				},
				{
					ID:    "web",
					Image: "busybox:latest",
				},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if diff.HasChanges {
		t.Errorf("expected no changes for same-file re-apply, got changes: %+v", diff)
	}
	if diff.RootContainerChanged {
		t.Error("synthesized root must not flag RootContainerChanged on re-apply")
	}
}

// TestDiffCell_NonRootContainerImage_InPlaceUpdate pins issue #485: a
// non-root container `image` change must NOT classify as breaking — the
// runner's UpdateCell path stops, removes, recreates, and starts the
// affected child in place. Otherwise the apply layer refuses the diff and
// the docs/cli-use-cases.md "apply updates a divergent existing cell"
// claim stays gapped (phase 1 #469 only fixed the empty-`[]` formatter on
// the refusal message; this is the divergent-update use case itself).
func TestDiffCell_NonRootContainerImage_InPlaceUpdate(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
				{ID: "web", Image: "nginx:1.27"},
			},
		},
	}

	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
				{ID: "web", Image: "nginx:1.25"},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Fatal("expected changes")
	}
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible (in-place updateable) change, got %v", diff.ChangeType)
	}
	if len(diff.BreakingChanges) != 0 {
		t.Errorf("non-root image bump must not populate BreakingChanges, got %v", diff.BreakingChanges)
	}
	if len(diff.Containers) != 1 {
		t.Fatalf("expected one ContainerDiff entry, got %d", len(diff.Containers))
	}
	cd := diff.Containers[0]
	if cd.Name != "web" || cd.Action != "update" {
		t.Errorf("expected web update, got %+v", cd)
	}
	if len(cd.BreakingChanges) != 0 {
		t.Errorf("per-container BreakingChanges must be empty, got %v", cd.BreakingChanges)
	}
	foundImage := false
	for _, f := range cd.ChangedFields {
		if f == "image" {
			foundImage = true
			break
		}
	}
	if !foundImage {
		t.Errorf("expected ContainerDiff.ChangedFields to include %q, got %v", "image", cd.ChangedFields)
	}
}

// TestDiffCell_NonRootContainerCommandArgs_InPlaceUpdate covers AC2 of
// issue #485: `command` and `args` changes on non-root containers travel
// the same in-place updateable path as `image`.
func TestDiffCell_NonRootContainerCommandArgs_InPlaceUpdate(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
				{ID: "web", Image: "nginx:1.27", Command: "/new/cmd", Args: []string{"--new"}},
			},
		},
	}

	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
				{ID: "web", Image: "nginx:1.27", Command: "/old/cmd", Args: []string{"--old"}},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible change for command/args bump, got %v", diff.ChangeType)
	}
	if len(diff.BreakingChanges) != 0 {
		t.Errorf("non-root command/args bump must not populate BreakingChanges, got %v", diff.BreakingChanges)
	}
	if len(diff.Containers) != 1 {
		t.Fatalf("expected one ContainerDiff entry, got %d", len(diff.Containers))
	}
	cd := diff.Containers[0]
	wantFields := map[string]bool{"command": false, "args": false}
	for _, f := range cd.ChangedFields {
		if _, ok := wantFields[f]; ok {
			wantFields[f] = true
		}
	}
	for f, seen := range wantFields {
		if !seen {
			t.Errorf("expected ContainerDiff.ChangedFields to include %q, got %v", f, cd.ChangedFields)
		}
	}
}

// TestDiffCell_RootContainerImage_StillBreaking pins AC4 of issue #485:
// even after the non-root reclassification, root-container image changes
// stay on the existing RecreateCell path so the cell-wide network/CNI
// recreate dance is preserved.
func TestDiffCell_RootContainerImage_StillBreaking(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:1.36"},
			},
		},
	}

	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:1.35"},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if !diff.RootContainerChanged {
		t.Fatal("expected RootContainerChanged=true for root image bump")
	}
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Errorf("expected breaking change for root image bump, got %v", diff.ChangeType)
	}
}

// TestDiffContainer_RootStillBreaking ensures the single-container
// reconcile path (ReconcileContainer) keeps the breaking-change
// classification for root containers — the rootContainer flag flows
// through diffContainerSpec via desired.Spec.Root.
func TestDiffContainer_RootStillBreaking(t *testing.T) {
	desired := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "root"},
		Spec: intmodel.ContainerSpec{
			ID:        "root",
			Root:      true,
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image: "busybox:1.36",
		},
	}
	actual := desired
	actual.Spec.Image = "busybox:1.35"

	diff := apply.DiffContainer(desired, actual)
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Fatalf("expected breaking change for root image bump, got %v", diff.ChangeType)
	}
	found := false
	for _, f := range diff.BreakingChanges {
		if f == "image" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected BreakingChanges to include image, got %v", diff.BreakingChanges)
	}
}

// TestDiffContainer_NonRootInPlace mirrors the cell-level reclassification
// at the single-container reconcile path: a non-root image bump must
// surface as Compatible.
func TestDiffContainer_NonRootInPlace(t *testing.T) {
	desired := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "web"},
		Spec: intmodel.ContainerSpec{
			ID:        "web",
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image: "nginx:1.27",
		},
	}
	actual := desired
	actual.Spec.Image = "nginx:1.25"

	diff := apply.DiffContainer(desired, actual)
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible change for non-root image bump, got %v", diff.ChangeType)
	}
	if len(diff.BreakingChanges) != 0 {
		t.Errorf("non-root image bump must not populate BreakingChanges, got %v", diff.BreakingChanges)
	}
}
