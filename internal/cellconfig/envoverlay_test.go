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

// Tests for the env-overlay helpers shared by the create, clone, and reconcile
// re-resolve paths (epic:cell-identity P5, #1024). The helpers were extracted
// from cmd/kuke/create/cell so all three paths re-apply per-cell --env overrides
// through one precedence-identical implementation.

package cellconfig

import (
	"reflect"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestMergeEnv(t *testing.T) {
	tests := []struct {
		name    string
		specEnv []string
		envArgs []string
		want    []string
	}{
		{
			name:    "empty override is a no-op returning the spec env",
			specEnv: []string{"A=1", "B=2"},
			envArgs: nil,
			want:    []string{"A=1", "B=2"},
		},
		{
			name:    "override wins on key collision and lands last",
			specEnv: []string{"A=1", "B=2"},
			envArgs: []string{"B=9"},
			want:    []string{"A=1", "B=9"},
		},
		{
			name:    "new key appends after surviving spec entries in order",
			specEnv: []string{"A=1"},
			envArgs: []string{"C=3"},
			want:    []string{"A=1", "C=3"},
		},
		{
			name:    "override into empty spec env",
			specEnv: nil,
			envArgs: []string{"A=1"},
			want:    []string{"A=1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeEnv(tt.specEnv, tt.envArgs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MergeEnv(%v, %v) = %v, want %v", tt.specEnv, tt.envArgs, got, tt.want)
			}
		})
	}
}

func TestMergeEnv_DoesNotMutateInputs(t *testing.T) {
	specEnv := []string{"A=1", "B=2"}
	envArgs := []string{"B=9"}
	_ = MergeEnv(specEnv, envArgs)
	if !reflect.DeepEqual(specEnv, []string{"A=1", "B=2"}) {
		t.Errorf("MergeEnv mutated specEnv: %v", specEnv)
	}
	if !reflect.DeepEqual(envArgs, []string{"B=9"}) {
		t.Errorf("MergeEnv mutated envArgs: %v", envArgs)
	}
}

func TestResolveAttachableContainerIndex(t *testing.T) {
	tests := []struct {
		name string
		spec v1beta1.CellSpec
		want int
	}{
		{
			name: "no attachable container yields -1",
			spec: v1beta1.CellSpec{Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "main"},
			}},
			want: -1,
		},
		{
			name: "first non-root attachable container in declaration order",
			spec: v1beta1.CellSpec{Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "a", Attachable: true},
				{ID: "b", Attachable: true},
			}},
			want: 1,
		},
		{
			name: "Tty.Default takes precedence over declaration order",
			spec: v1beta1.CellSpec{
				Tty: &v1beta1.CellTty{Default: "b"},
				Containers: []v1beta1.ContainerSpec{
					{ID: "a", Attachable: true},
					{ID: "b", Attachable: true},
				},
			},
			want: 1,
		},
		{
			name: "a root container is never the attachable target",
			spec: v1beta1.CellSpec{Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true, Attachable: true},
				{ID: "main", Attachable: true},
			}},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveAttachableContainerIndex(tt.spec); got != tt.want {
				t.Errorf("ResolveAttachableContainerIndex() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestApplyEnvOverrides_BakesIntoAttachableAndRecordsProvenance(t *testing.T) {
	doc := &v1beta1.CellDoc{
		Spec: v1beta1.CellSpec{
			Provenance: &v1beta1.CellProvenance{},
			Containers: []v1beta1.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "main", Attachable: true, Env: []string{"A=1"}},
			},
		},
	}
	ApplyEnvOverrides(doc, []string{"B=2"})

	if got := doc.Spec.Containers[1].Env; !reflect.DeepEqual(got, []string{"A=1", "B=2"}) {
		t.Errorf("attachable container Env = %v, want [A=1 B=2]", got)
	}
	if got := doc.Spec.Containers[0].Env; len(got) != 0 {
		t.Errorf("root container Env = %v, want empty (override targets the attachable container only)", got)
	}
	if got := doc.Spec.Provenance.EnvOverrides; !reflect.DeepEqual(got, []string{"B=2"}) {
		t.Errorf("provenance EnvOverrides = %v, want [B=2]", got)
	}
}

func TestApplyEnvOverrides_EmptyArgsIsNoOp(t *testing.T) {
	doc := &v1beta1.CellDoc{
		Spec: v1beta1.CellSpec{
			Provenance: &v1beta1.CellProvenance{},
			Containers: []v1beta1.ContainerSpec{{ID: "main", Attachable: true, Env: []string{"A=1"}}},
		},
	}
	ApplyEnvOverrides(doc, nil)
	if got := doc.Spec.Containers[0].Env; !reflect.DeepEqual(got, []string{"A=1"}) {
		t.Errorf("Env mutated by a no-op ApplyEnvOverrides: %v", got)
	}
	if doc.Spec.Provenance.EnvOverrides != nil {
		t.Errorf("provenance EnvOverrides recorded on a no-op: %v", doc.Spec.Provenance.EnvOverrides)
	}
}

func TestApplyEnvOverrides_NoAttachableStillRecordsProvenance(t *testing.T) {
	doc := &v1beta1.CellDoc{
		Spec: v1beta1.CellSpec{
			Provenance: &v1beta1.CellProvenance{},
			Containers: []v1beta1.ContainerSpec{{ID: "main"}}, // not attachable
		},
	}
	ApplyEnvOverrides(doc, []string{"B=2"})
	if got := doc.Spec.Containers[0].Env; len(got) != 0 {
		t.Errorf("non-attachable container Env = %v, want empty (nowhere to bake)", got)
	}
	if got := doc.Spec.Provenance.EnvOverrides; !reflect.DeepEqual(got, []string{"B=2"}) {
		t.Errorf("provenance EnvOverrides = %v, want [B=2] (intent preserved even with no bake target)", got)
	}
}

func TestApplyEnvOverrides_NilProvenanceBakesWithoutPanic(t *testing.T) {
	doc := &v1beta1.CellDoc{
		Spec: v1beta1.CellSpec{
			Containers: []v1beta1.ContainerSpec{{ID: "main", Attachable: true}},
		},
	}
	ApplyEnvOverrides(doc, []string{"B=2"})
	if got := doc.Spec.Containers[0].Env; !reflect.DeepEqual(got, []string{"B=2"}) {
		t.Errorf("attachable container Env = %v, want [B=2]", got)
	}
}
