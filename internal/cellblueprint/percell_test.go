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

package cellblueprint_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestExpandPerCellVolumes_ExpandsSourceAndMarksEnsure pins the per-cell
// naming + provisioning contract: a kind: volume mount whose source embeds
// ${CELL_NAME} is rewritten to the cell's name and marked Ensure so the daemon
// auto-provisions it (#1017 AC1/AC4).
func TestExpandPerCellVolumes_ExpandsSourceAndMarksEnsure(t *testing.T) {
	containers := []v1beta1.ContainerSpec{{
		ID: "app",
		Volumes: []v1beta1.VolumeMount{{
			Kind:   v1beta1.VolumeKindVolume,
			Source: "mem-${CELL_NAME}",
			Target: "/memory",
		}},
	}}

	cellblueprint.ExpandPerCellVolumes("agent-a1b2c3", containers)

	got := containers[0].Volumes[0]
	if got.Source != "mem-agent-a1b2c3" {
		t.Errorf("Source = %q, want mem-agent-a1b2c3", got.Source)
	}
	if !got.Ensure {
		t.Errorf("Ensure = false, want true on an expanded per-cell mount")
	}
}

// TestExpandPerCellVolumes_DistinctPerCell is the isolation guarantee: two
// cells materialized from the same templated source get two distinct Volume
// names, so neither shares the other's directory (#1017 AC1).
func TestExpandPerCellVolumes_DistinctPerCell(t *testing.T) {
	tmpl := func() []v1beta1.ContainerSpec {
		return []v1beta1.ContainerSpec{{
			ID:      "app",
			Volumes: []v1beta1.VolumeMount{{Kind: v1beta1.VolumeKindVolume, Source: "mem-${CELL_NAME}", Target: "/m"}},
		}}
	}

	a := tmpl()
	b := tmpl()
	cellblueprint.ExpandPerCellVolumes("cellA", a)
	cellblueprint.ExpandPerCellVolumes("cellB", b)

	if a[0].Volumes[0].Source == b[0].Volumes[0].Source {
		t.Fatalf("two cells share volume source %q, want distinct", a[0].Volumes[0].Source)
	}
	if a[0].Volumes[0].Source != "mem-cellA" || b[0].Volumes[0].Source != "mem-cellB" {
		t.Errorf("sources = %q, %q; want mem-cellA, mem-cellB", a[0].Volumes[0].Source, b[0].Volumes[0].Source)
	}
}

// TestExpandPerCellVolumes_Deterministic underpins the successor-inherits and
// reconcile-preserve ACs: expanding the same templated source against the same
// cell name yields the same Volume name, so re-materializing a cell with the
// same identity re-binds the same Volume rather than minting a fresh one
// (#1017 AC2/AC4).
func TestExpandPerCellVolumes_Deterministic(t *testing.T) {
	mk := func() []v1beta1.ContainerSpec {
		return []v1beta1.ContainerSpec{{
			ID:      "app",
			Volumes: []v1beta1.VolumeMount{{Kind: v1beta1.VolumeKindVolume, Source: "mem-${CELL_NAME}", Target: "/m"}},
		}}
	}
	first := mk()
	second := mk()
	cellblueprint.ExpandPerCellVolumes("agent-deadbe", first)
	cellblueprint.ExpandPerCellVolumes("agent-deadbe", second)

	if first[0].Volumes[0].Source != second[0].Volumes[0].Source {
		t.Errorf("non-deterministic expansion: %q vs %q",
			first[0].Volumes[0].Source, second[0].Volumes[0].Source)
	}
}

// TestExpandPerCellVolumes_ClonesVolumeRef confirms a cross-scope per-cell ref
// is expanded on a clone, so the caller's (potentially shared) VolumeRef is
// never aliased/mutated, and the rewritten mount is marked Ensure.
func TestExpandPerCellVolumes_ClonesVolumeRef(t *testing.T) {
	ref := &v1beta1.VolumeRef{Name: "mem-${CELL_NAME}", Realm: "default", Space: "agents"}
	containers := []v1beta1.ContainerSpec{{
		ID:      "app",
		Volumes: []v1beta1.VolumeMount{{Kind: v1beta1.VolumeKindVolume, VolumeRef: ref, Target: "/m"}},
	}}

	cellblueprint.ExpandPerCellVolumes("cellX", containers)

	if ref.Name != "mem-${CELL_NAME}" {
		t.Errorf("input VolumeRef mutated: Name = %q, want template preserved", ref.Name)
	}
	got := containers[0].Volumes[0]
	if got.VolumeRef == ref {
		t.Errorf("VolumeRef not cloned (still points at input)")
	}
	if got.VolumeRef.Name != "mem-cellX" {
		t.Errorf("clone Name = %q, want mem-cellX", got.VolumeRef.Name)
	}
	if !got.Ensure {
		t.Errorf("Ensure = false, want true on an expanded per-cell ref")
	}
}

// TestExpandPerCellVolumes_LeavesUntemplatedAlone confirms a statically-named
// volume keeps step 4's "missing is a hard error" default — no ${CELL_NAME}
// means no rewrite and no Ensure flip — and non-volume kinds are never touched.
func TestExpandPerCellVolumes_LeavesUntemplatedAlone(t *testing.T) {
	containers := []v1beta1.ContainerSpec{{
		ID: "app",
		Volumes: []v1beta1.VolumeMount{
			{Kind: v1beta1.VolumeKindVolume, Source: "shared-cache", Target: "/cache"},
			{Kind: v1beta1.VolumeKindBind, Source: "/host/${CELL_NAME}", Target: "/b"},
			{Kind: v1beta1.VolumeKindTmpfs, Target: "/t"},
		},
	}}

	cellblueprint.ExpandPerCellVolumes("cellZ", containers)

	if v := containers[0].Volumes[0]; v.Source != "shared-cache" || v.Ensure {
		t.Errorf("static volume mount changed: %+v", v)
	}
	if v := containers[0].Volumes[1]; v.Source != "/host/${CELL_NAME}" || v.Ensure {
		t.Errorf("bind mount touched: %+v (only kind: volume sources expand)", v)
	}
}

// TestMaterializeWithName_PerCellVolumeFanout exercises the full blueprint
// path: each materialization (a fresh <prefix>-<6hex> cell) yields a distinct
// per-cell Volume name, each marked Ensure — the 1:N fan-out isolation
// guarantee (#1017 AC1).
func TestMaterializeWithName_PerCellVolumeFanout(t *testing.T) {
	doc := v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "agent", Realm: "default", Space: "agents", Stack: "team"},
		Spec: v1beta1.CellBlueprintSpec{
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{{
					ID:    "app",
					Image: "img",
					Volumes: []v1beta1.VolumeMount{{
						Kind:   v1beta1.VolumeKindVolume,
						Source: "mem-${CELL_NAME}",
						Target: "/memory",
					}},
				}},
			},
		},
	}

	cellA, err := cellblueprint.MaterializeWithName(doc, "", nil)
	if err != nil {
		t.Fatalf("MaterializeWithName(A) error = %v", err)
	}
	cellB, err := cellblueprint.MaterializeWithName(doc, "", nil)
	if err != nil {
		t.Fatalf("MaterializeWithName(B) error = %v", err)
	}

	srcA := cellA.Spec.Containers[0].Volumes[0].Source
	srcB := cellB.Spec.Containers[0].Volumes[0].Source

	if srcA == srcB {
		t.Fatalf("fan-out shares volume source %q, want distinct per cell", srcA)
	}
	for _, c := range []v1beta1.CellDoc{cellA, cellB} {
		v := c.Spec.Containers[0].Volumes[0]
		if !strings.HasPrefix(v.Source, "mem-") || strings.Contains(v.Source, "${") {
			t.Errorf("source %q not expanded to mem-<cell>", v.Source)
		}
		if !v.Ensure {
			t.Errorf("cell %q volume not marked Ensure", c.Metadata.Name)
		}
		if v.Source != "mem-"+c.Metadata.Name {
			t.Errorf("source %q != mem-%s", v.Source, c.Metadata.Name)
		}
	}
}
