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

// TestDiffCell_RootContainerPrivileged_Breaking pins AC1/AC2/AC3/AC5 of
// issue #990: a Privileged drift on the root container must classify as
// Breaking (the OCI Process cap-set is baked at StartCell), surface the
// qualified `rootContainer.privileged` field path on the cell-level
// BreakingChanges, and toggle `RootContainerChanged`. Prior to #990, the
// root-container diff only checked image/command/args and silently
// dropped every other edit including security-posture changes.
func TestDiffCell_RootContainerPrivileged_Breaking(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest", Privileged: true},
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
				{ID: "root", Root: true, Image: "busybox:latest", Privileged: false},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Fatal("expected changes for root privileged toggle")
	}
	if !diff.RootContainerChanged {
		t.Error("expected RootContainerChanged=true for root privileged toggle")
	}
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Errorf("expected breaking change for root privileged toggle, got %v", diff.ChangeType)
	}
	foundBreaking := false
	for _, f := range diff.BreakingChanges {
		if f == "rootContainer.privileged" {
			foundBreaking = true
			break
		}
	}
	if !foundBreaking {
		t.Errorf("expected BreakingChanges to include %q, got %v", "rootContainer.privileged", diff.BreakingChanges)
	}
	if got := diff.RootContainerDetails["privileged"]; got == "" {
		t.Errorf("expected RootContainerDetails[\"privileged\"] populated, got empty")
	}
}

// TestDiffCell_RootContainerEnv_Compatible pins AC1/AC2/AC3/AC5 of issue
// #990 on the Compatible-on-root branch: an Env edit on the root
// container must surface as a Compatible change (not silently dropped,
// not forced through RecreateCell) and qualify under
// `rootContainer.env`. Env injection on the root is rebuilt at every
// start, so a recreate is not warranted.
func TestDiffCell_RootContainerEnv_Compatible(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest", Env: []string{"FOO=new"}},
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
				{ID: "root", Root: true, Image: "busybox:latest", Env: []string{"FOO=old"}},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Fatal("expected changes for root env edit")
	}
	if !diff.RootContainerChanged {
		t.Error("expected RootContainerChanged=true for root env edit")
	}
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Errorf("expected compatible change for root env edit, got %v", diff.ChangeType)
	}
	if len(diff.BreakingChanges) != 0 {
		t.Errorf("root env edit must not populate BreakingChanges, got %v", diff.BreakingChanges)
	}
	foundChanged := false
	for _, f := range diff.ChangedFields {
		if f == "rootContainer.env" {
			foundChanged = true
			break
		}
	}
	if !foundChanged {
		t.Errorf("expected ChangedFields to include %q, got %v", "rootContainer.env", diff.ChangedFields)
	}
}

// TestDiffCell_RootContainer_NoDriftOnEqualSpec guards against false
// positives on the widened root-container diff: a same-spec re-apply on
// every field diffContainerSpec now reads from the root container must
// still produce zero changes. Issue #990.
func TestDiffCell_RootContainer_NoDriftOnEqualSpec(t *testing.T) {
	limit := int64(128 << 20)
	root := intmodel.ContainerSpec{
		ID:                     "root",
		Root:                   true,
		Image:                  "busybox:latest",
		Command:                "/bin/sleep",
		Args:                   []string{"60"},
		Env:                    []string{"FOO=bar"},
		Volumes:                []intmodel.VolumeMount{{Source: "/host", Target: "/cell"}},
		Privileged:             true,
		User:                   "nobody",
		ReadOnlyRootFilesystem: true,
		Capabilities:           &intmodel.ContainerCapabilities{Add: []string{"CAP_NET_ADMIN"}},
		SecurityOpts:           []string{"no-new-privileges"},
		Tmpfs:                  []intmodel.ContainerTmpfsMount{{Path: "/tmp", SizeBytes: 1 << 20}},
		Resources:              &intmodel.ContainerResources{MemoryLimitBytes: &limit},
	}
	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName:  "default",
			SpaceName:  "default",
			StackName:  "default",
			Containers: []intmodel.ContainerSpec{root},
		},
	}

	diff := apply.DiffCell(cell, cell)
	if diff.HasChanges {
		t.Errorf("expected no changes for identical root container spec, got: %+v", diff)
	}
	if diff.RootContainerChanged {
		t.Errorf("expected RootContainerChanged=false for identical root, got true")
	}
}

// TestDiffCell_ProvenanceIgnored pins issue #1021 AC3: the Spec.Provenance
// block is lineage/identity data, not a runtime spec field, so a cell that
// differs from its would-be-materialized twin *only* in provenance must not
// report any change. Were DiffCell to compare it, the reconciler would stamp a
// permanent false OutOfSync on every Config-lineage cell.
func TestDiffCell_ProvenanceIgnored(t *testing.T) {
	root := intmodel.ContainerSpec{
		ID:    "root",
		Root:  true,
		Image: "busybox:latest",
	}
	base := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName:  "default",
			SpaceName:  "default",
			StackName:  "default",
			Containers: []intmodel.ContainerSpec{root},
			Provenance: &intmodel.CellProvenance{
				BindingKind: "config",
				BindingRef:  intmodel.CellBindingRef{Name: "web", Realm: "default"},
				Params:      map[string]string{"TAG": "v1"},
			},
		},
	}

	// actual carries a divergent provenance block (different params, a
	// different binding ref) but an identical runtime spec.
	actual := base
	actual.Spec.Provenance = &intmodel.CellProvenance{
		BindingKind:  "config",
		BindingRef:   intmodel.CellBindingRef{Name: "web", Realm: "default", Space: "team-b"},
		Params:       map[string]string{"TAG": "v2"},
		EnvOverrides: []string{"DEBUG=1"},
	}

	if diff := apply.DiffCell(base, actual); diff.HasChanges {
		t.Errorf("provenance-only difference reported changes: %+v", diff)
	}

	// A nil-vs-set provenance must likewise be a no-op.
	noProv := base
	noProv.Spec.Provenance = nil
	if diff := apply.DiffCell(base, noProv); diff.HasChanges {
		t.Errorf("nil-vs-set provenance reported changes: %+v", diff)
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

// TestDiffCell_Tty_CompatibleChange pins AC #1+#3 of issue #992: a change to
// the cell-default TTY pointer (`Spec.Tty.Default`) must surface as a
// Compatible diff with `spec.tty` in ChangedFields and a populated Details
// entry — so `kuke apply` re-stamps the attach target instead of silently
// dropping the edit.
func TestDiffCell_Tty_CompatibleChange(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Tty:       &intmodel.CellTty{Default: "web"},
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
				{ID: "web", Image: "busybox:latest"},
			},
		},
	}

	actual := desired
	actual.Spec.Tty = &intmodel.CellTty{Default: "root"}

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Fatal("expected changes for tty default edit")
	}
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible change, got %v", diff.ChangeType)
	}
	if !hasChangedField(diff.DiffResult, "spec.tty") {
		t.Errorf("expected ChangedFields to include spec.tty, got %v", diff.ChangedFields)
	}
	if diff.Details["spec.tty"] == "" {
		t.Errorf("expected Details[spec.tty] to be populated, got empty")
	}
	if len(diff.BreakingChanges) != 0 {
		t.Errorf("tty edit must not populate BreakingChanges, got %v", diff.BreakingChanges)
	}
}

// TestDiffCell_Tty_NilEqualsZero exercises the cellTtyEqual helper's
// nil-versus-zero-value contract: a same-file re-apply where one side carries
// nil and the other an explicitly-empty CellTty must not register drift.
func TestDiffCell_Tty_NilEqualsZero(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Tty:       nil,
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
			},
		},
	}

	actual := desired
	actual.Spec.Tty = &intmodel.CellTty{Default: ""}

	diff := apply.DiffCell(desired, actual)
	if diff.HasChanges {
		t.Errorf("nil vs empty CellTty must not register drift, got changes: %+v", diff)
	}
}

// TestDiffCell_AutoDelete_CompatibleChange pins AC #1+#3 of issue #992: a
// toggle of `Spec.AutoDelete` must surface as a Compatible diff with
// `spec.autoDelete` in ChangedFields and a populated Details entry.
func TestDiffCell_AutoDelete_CompatibleChange(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName:  "default",
			SpaceName:  "default",
			StackName:  "default",
			AutoDelete: true,
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
			},
		},
	}

	actual := desired
	actual.Spec.AutoDelete = false

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Fatal("expected changes for autoDelete toggle")
	}
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible change, got %v", diff.ChangeType)
	}
	if !hasChangedField(diff.DiffResult, "spec.autoDelete") {
		t.Errorf("expected ChangedFields to include spec.autoDelete, got %v", diff.ChangedFields)
	}
	if diff.Details["spec.autoDelete"] == "" {
		t.Errorf("expected Details[spec.autoDelete] to be populated, got empty")
	}
	if len(diff.BreakingChanges) != 0 {
		t.Errorf("autoDelete edit must not populate BreakingChanges, got %v", diff.BreakingChanges)
	}
}

// TestDiffCell_NestedCgroupRuntime_BreakingChange pins AC #1+#2 of issue
// #992: a toggle of `Spec.NestedCgroupRuntime` must classify as Breaking and
// surface in BreakingChanges as `spec.nestedCgroupRuntime` — the runner's
// cgroup-delegation and in-container /sys/fs/cgroup mount cannot be
// re-stamped in place, so RecreateCell is the only safe path.
func TestDiffCell_NestedCgroupRuntime_BreakingChange(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName:           "default",
			SpaceName:           "default",
			StackName:           "default",
			NestedCgroupRuntime: true,
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
			},
		},
	}

	actual := desired
	actual.Spec.NestedCgroupRuntime = false

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Fatal("expected changes for nestedCgroupRuntime toggle")
	}
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Fatalf("expected breaking change, got %v", diff.ChangeType)
	}
	found := false
	for _, f := range diff.BreakingChanges {
		if f == "spec.nestedCgroupRuntime" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected BreakingChanges to include spec.nestedCgroupRuntime, got %v", diff.BreakingChanges)
	}
	if diff.Details["spec.nestedCgroupRuntime"] == "" {
		t.Errorf("expected Details[spec.nestedCgroupRuntime] to be populated, got empty")
	}
}

func hasChangedField(diff apply.DiffResult, field string) bool {
	for _, f := range diff.ChangedFields {
		if f == field {
			return true
		}
	}
	return false
}

// TestDiffContainer_ReposChange exercises the repos[]-only edit path: an apply
// that touches only Spec.Repos must register as a compatible change so the
// reconcile triggers, rather than being realized lazily on the next start
// (issue #647).
func TestDiffContainer_ReposChange(t *testing.T) {
	desired := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "web"},
		Spec: intmodel.ContainerSpec{
			ID:        "web",
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image: "nginx:1.27",
			Repos: []intmodel.ContainerRepo{{Name: "app", URL: "https://example.com/app.git", Target: "/src"}},
		},
	}
	actual := desired
	actual.Spec.Repos = []intmodel.ContainerRepo{{Name: "app", URL: "https://example.com/app.git", Target: "/srv"}}

	diff := apply.DiffContainer(desired, actual)
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible change for repos edit, got %v", diff.ChangeType)
	}
	if !hasChangedField(diff, "repos") {
		t.Errorf("expected ChangedFields to include repos, got %v", diff.ChangedFields)
	}
}

// TestDiffContainer_SecretsChange exercises the secrets[]-only edit path,
// including the SecretRef pointer-value comparison (issue #647).
func TestDiffContainer_SecretsChange(t *testing.T) {
	desired := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "web"},
		Spec: intmodel.ContainerSpec{
			ID:        "web",
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image: "nginx:1.27",
			Secrets: []intmodel.ContainerSecret{{
				Name:      "db",
				SecretRef: &intmodel.ContainerSecretRef{Name: "db-creds", Realm: "default"},
			}},
		},
	}
	actual := desired
	actual.Spec.Secrets = []intmodel.ContainerSecret{{
		Name:      "db",
		SecretRef: &intmodel.ContainerSecretRef{Name: "db-creds", Realm: "prod"},
	}}

	diff := apply.DiffContainer(desired, actual)
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible change for secrets edit, got %v", diff.ChangeType)
	}
	if !hasChangedField(diff, "secrets") {
		t.Errorf("expected ChangedFields to include secrets, got %v", diff.ChangedFields)
	}
}

// TestDiffContainer_WorkingDirChange covers a Compatible-class field added by
// issue #991: a non-root container `workingDir` edit must register as drift on
// the in-place updateable path so the apply layer drives the spec change into
// UpdateCell instead of returning "no changes" while the running container
// keeps its prior process.cwd.
func TestDiffContainer_WorkingDirChange(t *testing.T) {
	desired := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "web"},
		Spec: intmodel.ContainerSpec{
			ID:        "web",
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image:      "nginx:1.27",
			WorkingDir: "/opt/app",
		},
	}
	actual := desired
	actual.Spec.WorkingDir = "/srv"

	diff := apply.DiffContainer(desired, actual)
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible change for workingDir edit, got %v", diff.ChangeType)
	}
	if !hasChangedField(diff, "workingDir") {
		t.Errorf("expected ChangedFields to include workingDir, got %v", diff.ChangedFields)
	}
	if len(diff.BreakingChanges) != 0 {
		t.Errorf("workingDir is in-place updateable; BreakingChanges must be empty, got %v", diff.BreakingChanges)
	}
}

// TestDiffContainer_HostNetworkChange covers a Breaking-class field added by
// issue #991: host-namespace toggles change the cell's OCI namespace shape
// (the root container sets up netns at cell-start, child containers join via
// JoinContainerNamespaces) so flipping `hostNetwork` cannot be applied in
// place — the diff must classify as breaking to route through RecreateCell.
func TestDiffContainer_HostNetworkChange(t *testing.T) {
	desired := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "web"},
		Spec: intmodel.ContainerSpec{
			ID:        "web",
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image:       "nginx:1.27",
			HostNetwork: true,
		},
	}
	actual := desired
	actual.Spec.HostNetwork = false

	diff := apply.DiffContainer(desired, actual)
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Fatalf("expected breaking change for hostNetwork flip, got %v", diff.ChangeType)
	}
	found := false
	for _, f := range diff.BreakingChanges {
		if f == "hostNetwork" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected BreakingChanges to include hostNetwork, got %v", diff.BreakingChanges)
	}
}

// TestDiffContainer_TtyChange exercises the *ContainerTty pointer-field
// equality helper added by issue #991: a tty edit on the pointer-backed
// stage list must register as drift, and an identical block (including a
// populated OnInit slice) must not.
func TestDiffContainer_TtyChange(t *testing.T) {
	desired := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "web"},
		Spec: intmodel.ContainerSpec{
			ID:        "web",
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image: "nginx:1.27",
			Tty: &intmodel.ContainerTty{
				Prompt: "[web] ",
				OnInit: []intmodel.TtyStage{{Script: "echo hello", RunOn: "create"}},
			},
		},
	}
	actual := desired
	// Same fields, different OnInit script — must register drift.
	actual.Spec.Tty = &intmodel.ContainerTty{
		Prompt: "[web] ",
		OnInit: []intmodel.TtyStage{{Script: "echo bye", RunOn: "create"}},
	}

	diff := apply.DiffContainer(desired, actual)
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Fatalf("expected compatible change for tty edit, got %v", diff.ChangeType)
	}
	if !hasChangedField(diff, "tty") {
		t.Errorf("expected ChangedFields to include tty, got %v", diff.ChangedFields)
	}

	// Identical Tty blocks (same pointer-shape, same content) must not drift.
	sameDesired := desired
	sameActual := desired
	sameActual.Spec.Tty = &intmodel.ContainerTty{
		Prompt: "[web] ",
		OnInit: []intmodel.TtyStage{{Script: "echo hello", RunOn: "create"}},
	}
	noDiff := apply.DiffContainer(sameDesired, sameActual)
	if noDiff.HasChanges {
		t.Errorf("expected no drift on identical tty blocks, got %v", noDiff.ChangedFields)
	}

	// Nil vs. zero-valued *ContainerTty must compare equal (same treatment
	// resourcesEqual gives an empty resources block).
	nilSide := desired
	nilSide.Spec.Tty = nil
	zeroSide := desired
	zeroSide.Spec.Tty = &intmodel.ContainerTty{}
	nilDiff := apply.DiffContainer(nilSide, zeroSide)
	for _, f := range nilDiff.ChangedFields {
		if f == "tty" {
			t.Errorf("nil and zero-valued tty must compare equal; got tty drift %v", nilDiff.Details["tty"])
		}
	}
}

// TestDiffContainer_ReposSecretsNoChange guards the equality helpers: identical
// repos/secrets (including a populated SecretRef) must not register drift on a
// same-spec re-apply.
func TestDiffContainer_ReposSecretsNoChange(t *testing.T) {
	desired := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "web"},
		Spec: intmodel.ContainerSpec{
			ID:        "web",
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image: "nginx:1.27",
			Repos: []intmodel.ContainerRepo{{Name: "app", URL: "https://example.com/app.git", Target: "/src", Required: true}},
			Secrets: []intmodel.ContainerSecret{{
				Name:      "db",
				SecretRef: &intmodel.ContainerSecretRef{Name: "db-creds", Realm: "default"},
			}},
		},
	}
	actual := desired

	diff := apply.DiffContainer(desired, actual)
	if diff.HasChanges {
		t.Fatalf("expected no changes for identical repos/secrets, got %v", diff.ChangedFields)
	}
}
