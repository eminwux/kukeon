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

package runner

import (
	"reflect"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestResolveAttachableContainerID_PrefersTtyDefault locks in the
// precedence: cell.Spec.Tty.Default wins over the "first attachable in
// declaration order" fallback when the named container exists and is not
// the root. Mirrors the CLI-side pickAttachTarget precedence so `kuke run
// --env` and `kuke run` itself agree on which container receives the
// injection / attach target.
func TestResolveAttachableContainerID_PrefersTtyDefault(t *testing.T) {
	cell := intmodel.Cell{Spec: intmodel.CellSpec{
		Tty: &intmodel.CellTty{Default: "work"},
		Containers: []intmodel.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "main", Attachable: true},
			{ID: "work", Attachable: true},
		},
	}}
	if got := resolveAttachableContainerID(cell); got != "work" {
		t.Errorf("attachable=%q, want work (cell.Spec.Tty.Default wins over main)", got)
	}
}

// TestResolveAttachableContainerID_FallsBackToFirstAttachable covers the
// implicit-attachable case: no cell.Spec.Tty.Default, multiple containers
// with Attachable=true, the first one in declaration order wins.
func TestResolveAttachableContainerID_FallsBackToFirstAttachable(t *testing.T) {
	cell := intmodel.Cell{Spec: intmodel.CellSpec{
		Containers: []intmodel.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "main", Attachable: true},
			{ID: "work", Attachable: true},
		},
	}}
	if got := resolveAttachableContainerID(cell); got != "main" {
		t.Errorf("attachable=%q, want main (first attachable in declaration order)", got)
	}
}

// TestResolveAttachableContainerID_SkipsRootEvenWhenAttachable defends
// against a malformed spec that marks the root container Attachable=true:
// root is excluded from the pick set so the runtime env injection never
// lands in the root container's OCI env (which would change the daemon's
// own behaviour for the kuke-system kukeond cell, the canonical root use
// case).
func TestResolveAttachableContainerID_SkipsRootEvenWhenAttachable(t *testing.T) {
	cell := intmodel.Cell{Spec: intmodel.CellSpec{
		Containers: []intmodel.ContainerSpec{
			{ID: "root", Root: true, Attachable: true},
			{ID: "work", Attachable: true},
		},
	}}
	if got := resolveAttachableContainerID(cell); got != "work" {
		t.Errorf("attachable=%q, want work (root excluded from pick set)", got)
	}
}

// TestResolveAttachableContainerID_TtyDefaultMissing_FallsThrough covers
// the malformed-spec case where cell.Spec.Tty.Default names a container
// that isn't in the array (typo, container removed without updating
// cell.spec.tty.default). Rather than return the missing name (which
// would silently drop runtime env when the OCI build doesn't find a
// matching containerSpec), fall through to the first-attachable rule so
// at least some attachable container receives the injection.
func TestResolveAttachableContainerID_TtyDefaultMissing_FallsThrough(t *testing.T) {
	cell := intmodel.Cell{Spec: intmodel.CellSpec{
		Tty: &intmodel.CellTty{Default: "ghost"},
		Containers: []intmodel.ContainerSpec{
			{ID: "main", Attachable: true},
		},
	}}
	if got := resolveAttachableContainerID(cell); got != "main" {
		t.Errorf("attachable=%q, want main (Tty.Default named missing container; fall through)", got)
	}
}

// TestResolveAttachableContainerID_NoAttachableReturnsEmpty covers the
// "all containers are non-attachable" case: every spec entry has
// Attachable=false. The helper returns "" so the merge helper treats it
// as a no-op rather than misdirecting runtime env into the root or some
// random container.
func TestResolveAttachableContainerID_NoAttachableReturnsEmpty(t *testing.T) {
	cell := intmodel.Cell{Spec: intmodel.CellSpec{
		Containers: []intmodel.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "worker"},
		},
	}}
	if got := resolveAttachableContainerID(cell); got != "" {
		t.Errorf("attachable=%q, want '' (no attachable container declared)", got)
	}
}

// TestMergeRuntimeEnv_AppendsDistinctKeys pins the merge contract: when
// runtime env keys don't collide with spec env keys, both lists are
// concatenated (spec first, runtime second). The order matters because
// downstream ctr.kukeonContainerEnv applies its "user entries win on
// collision" rule against this list — keeping spec first lets the daemon
// see a deterministic ordering across runs with the same flag set.
func TestMergeRuntimeEnv_AppendsDistinctKeys(t *testing.T) {
	got := mergeRuntimeEnv(
		[]string{"FOO=spec", "BAR=spec"},
		[]string{"BAZ=runtime"},
	)
	want := []string{"FOO=spec", "BAR=spec", "BAZ=runtime"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge = %v, want %v", got, want)
	}
}

// TestMergeRuntimeEnv_RuntimeWinsOnKeyCollision pins the AC's override
// semantics: a runtime KEY that collides with a spec KEY drops the spec
// entry and emits the runtime value in its place. The non-colliding
// spec entries keep their relative order; the runtime entry lands at
// the end (matching the "spec first, runtime second" rule above).
func TestMergeRuntimeEnv_RuntimeWinsOnKeyCollision(t *testing.T) {
	got := mergeRuntimeEnv(
		[]string{"FOO=spec", "LABEL=spec-label", "BAR=spec"},
		[]string{"LABEL=bug"},
	)
	want := []string{"FOO=spec", "BAR=spec", "LABEL=bug"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge = %v, want %v (runtime LABEL overrides spec LABEL; spec order preserved)", got, want)
	}
}

// TestMergeRuntimeEnv_EmptyRuntimeIsNoOp covers the no-env case: a runner
// pass against a cell whose `kuke run` invocation didn't supply --env
// returns the spec env verbatim (same slice header, no allocation, no
// reordering). Important for the divergent-spec check: a subsequent
// `kuke run <cfg>` (no --env) against a cell whose original create
// invocation also had no --env must see actual.Env == desired.Env.
func TestMergeRuntimeEnv_EmptyRuntimeIsNoOp(t *testing.T) {
	specEnv := []string{"FOO=spec"}
	got := mergeRuntimeEnv(specEnv, nil)
	if !reflect.DeepEqual(got, specEnv) {
		t.Errorf("merge with nil runtime = %v, want %v", got, specEnv)
	}
}

// TestMergeRuntimeEnv_EmptySpecJustReturnsRuntime covers the inverse: a
// container that authored no env at all but receives --env still gets
// the runtime list as its OCI env.
func TestMergeRuntimeEnv_EmptySpecJustReturnsRuntime(t *testing.T) {
	got := mergeRuntimeEnv(nil, []string{"LABEL=bug"})
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge with nil spec = %v, want %v", got, want)
	}
}

// TestMergeRuntimeEnvForContainer_NonAttachableUnchanged pins the
// per-container gate: a container that is not the attachable container
// keeps its authored env verbatim, even when runtime env is supplied.
// The AC scopes injection to the attachable container only — other
// containers must not receive --env (their env is the cell author's
// intent, not the operator's per-invocation knob).
func TestMergeRuntimeEnvForContainer_NonAttachableUnchanged(t *testing.T) {
	spec := intmodel.ContainerSpec{
		ID:  "worker",
		Env: []string{"FOO=spec"},
	}
	got := mergeRuntimeEnvForContainer(spec, "main", []string{"LABEL=bug"})
	if !reflect.DeepEqual(got.Env, spec.Env) {
		t.Errorf("non-attachable Env = %v, want %v (unchanged)", got.Env, spec.Env)
	}
	if got.ID != spec.ID {
		t.Errorf("non-attachable ID = %q, want %q (passthrough)", got.ID, spec.ID)
	}
}

// TestMergeRuntimeEnvForContainer_AttachableMerges pins the per-container
// happy path: the attachable container's Env is replaced with the merged
// list (spec entries first, runtime second, runtime wins on KEY
// collision). All other ContainerSpec fields are preserved through the
// shallow copy.
func TestMergeRuntimeEnvForContainer_AttachableMerges(t *testing.T) {
	spec := intmodel.ContainerSpec{
		ID:         "main",
		Attachable: true,
		Image:      "registry.example.com/web:latest",
		Env:        []string{"FOO=spec", "LABEL=spec-label"},
	}
	got := mergeRuntimeEnvForContainer(spec, "main", []string{"LABEL=bug", "PRIORITY=A"})
	wantEnv := []string{"FOO=spec", "LABEL=bug", "PRIORITY=A"}
	if !reflect.DeepEqual(got.Env, wantEnv) {
		t.Errorf("attachable Env = %v, want %v", got.Env, wantEnv)
	}
	if got.Image != spec.Image {
		t.Errorf("attachable Image = %q, want %q (other fields preserved)", got.Image, spec.Image)
	}
}

// TestMergeRuntimeEnvForContainer_EmptyAttachableIDIsNoOp covers the
// "no attachable container declared" case from
// resolveAttachableContainerID. Even a container whose ID happens to
// match an empty string (never, but the contract belongs to this
// helper, not the caller) must not receive runtime env. The empty
// attachableID always returns the spec unchanged.
func TestMergeRuntimeEnvForContainer_EmptyAttachableIDIsNoOp(t *testing.T) {
	spec := intmodel.ContainerSpec{ID: "main", Env: []string{"FOO=spec"}}
	got := mergeRuntimeEnvForContainer(spec, "", []string{"LABEL=bug"})
	if !reflect.DeepEqual(got.Env, spec.Env) {
		t.Errorf("Env = %v, want %v (no attachable resolved; no injection)", got.Env, spec.Env)
	}
}

// TestMergeRuntimeEnvForContainer_DoesNotMutateInput is the defensive
// pin: the helper must not mutate the caller's ContainerSpec.Env slice,
// because the caller persists cell.Spec.Containers[i] to disk via
// UpdateCellMetadata and a mutation here would pollute the on-disk
// spec — defeating the yaml:"-" guard on RuntimeEnv (issue #834).
func TestMergeRuntimeEnvForContainer_DoesNotMutateInput(t *testing.T) {
	origEnv := []string{"FOO=spec", "LABEL=spec"}
	spec := intmodel.ContainerSpec{ID: "main", Attachable: true, Env: origEnv}
	specEnvBefore := append([]string(nil), spec.Env...)
	_ = mergeRuntimeEnvForContainer(spec, "main", []string{"LABEL=bug"})
	if !reflect.DeepEqual(spec.Env, specEnvBefore) {
		t.Errorf("spec.Env mutated to %v, want unchanged %v", spec.Env, specEnvBefore)
	}
}
