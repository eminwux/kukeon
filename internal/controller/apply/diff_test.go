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

// TestDiffCell_NonRootContainerBreaking_NamesField pins issue #469: when a
// breaking change is rooted in a non-root container (e.g. image bump),
// DiffCell must propagate the field name into the cell-level
// BreakingChanges slice qualified by container ID. Otherwise the
// reconcile-side error formatter ("cell %q has breaking changes: %v") prints
// an empty `[]` and the operator has no way to tell what was rejected.
func TestDiffCell_NonRootContainerBreaking_NamesField(t *testing.T) {
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
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Fatalf("expected breaking change, got %v", diff.ChangeType)
	}
	want := "containers[web].image"
	found := false
	for _, f := range diff.BreakingChanges {
		if f == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected cell-level BreakingChanges to include %q, got %v", want, diff.BreakingChanges)
	}
}
