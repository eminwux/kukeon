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

package cellconfig

import (
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestMaterialize_ExpandsPerCellVolume confirms the Config 1:N binding path
// resolves the ${CELL_NAME} per-cell volume template against the caller-
// supplied cell name and marks the mount Ensure, identically to the blueprint
// path (shared cellblueprint.ExpandPerCellVolumes). Two cells stamped from the
// same Config get distinct, private Volume names (#1017 AC1).
func TestMaterialize_ExpandsPerCellVolume(t *testing.T) {
	bp := v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "agent", Realm: "bp-realm"},
		Spec: v1beta1.CellBlueprintSpec{
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{{
					ID:    "main",
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
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "agent", Realm: "bp-realm"},
		},
	}

	cellA, err := MaterializeWithName(cfg, bp, "agent-aaa111")
	if err != nil {
		t.Fatalf("MaterializeWithName(A) error = %v", err)
	}
	cellB, err := MaterializeWithName(cfg, bp, "agent-bbb222")
	if err != nil {
		t.Fatalf("MaterializeWithName(B) error = %v", err)
	}

	volA := cellA.Spec.Containers[0].Volumes[0]
	volB := cellB.Spec.Containers[0].Volumes[0]

	if volA.Source != "mem-agent-aaa111" {
		t.Errorf("cellA source = %q, want mem-agent-aaa111", volA.Source)
	}
	if volB.Source != "mem-agent-bbb222" {
		t.Errorf("cellB source = %q, want mem-agent-bbb222", volB.Source)
	}
	if volA.Source == volB.Source {
		t.Errorf("two Config-stamped cells share volume source %q, want distinct", volA.Source)
	}
	if !volA.Ensure || !volB.Ensure {
		t.Errorf("per-cell volume not marked Ensure (A=%v B=%v)", volA.Ensure, volB.Ensure)
	}
}
