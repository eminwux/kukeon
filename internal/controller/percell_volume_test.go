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

//nolint:testpackage // white-box: exercises the unexported ensurePerCellVolumes path
package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// newReapplyTestExec builds an *Exec with a discard logger + context for the
// reapply-helper tests, whose success paths log via b.logger/b.ctx (the
// ensure-only tests above use a bare &Exec{runner: stub} because they never log).
func newReapplyTestExec(r runner.Runner) *Exec {
	return &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		runner: r,
	}
}

// ensureStubRunner satisfies runner.Runner (via the embedded interface) but
// implements only the scope-lookup + WriteVolume methods ReconcileVolume
// touches, recording every provisioned Volume. Any other method panics, which
// is the assertion that ensurePerCellVolumes provisions through the canonical
// reconcile path and nothing else.
type ensureStubRunner struct {
	runner.Runner
	writes   []intmodel.Volume
	writeErr error
	created  bool
}

func (s *ensureStubRunner) GetRealm(r intmodel.Realm) (intmodel.Realm, error)  { return r, nil }
func (s *ensureStubRunner) GetSpace(sp intmodel.Space) (intmodel.Space, error) { return sp, nil }
func (s *ensureStubRunner) GetStack(st intmodel.Stack) (intmodel.Stack, error) { return st, nil }
func (s *ensureStubRunner) WriteVolume(v intmodel.Volume) (bool, error) {
	s.writes = append(s.writes, v)
	return s.created, s.writeErr
}

func cellWith(scope [3]string, vols ...intmodel.VolumeMount) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "agent-a1b2c3"},
		Spec: intmodel.CellSpec{
			RealmName: scope[0],
			SpaceName: scope[1],
			StackName: scope[2],
			Containers: []intmodel.ContainerSpec{{
				ID:      "app",
				Volumes: vols,
			}},
		},
	}
}

// TestEnsurePerCellVolumes_ProvisionsAtCellScope confirms a bare-source ensure
// mount provisions a Volume named by the source at the cell's own
// realm/space/stack — where step 4's same-scope resolver finds it (#1017).
func TestEnsurePerCellVolumes_ProvisionsAtCellScope(t *testing.T) {
	stub := &ensureStubRunner{created: true}
	b := &Exec{runner: stub}

	cell := cellWith([3]string{"default", "agents", "team"}, intmodel.VolumeMount{
		Kind:   intmodel.VolumeKindVolume,
		Source: "mem-agent-a1b2c3",
		Target: "/memory",
		Ensure: true,
	})

	if err := b.ensurePerCellVolumes(cell); err != nil {
		t.Fatalf("ensurePerCellVolumes() error = %v", err)
	}
	if len(stub.writes) != 1 {
		t.Fatalf("WriteVolume calls = %d, want 1", len(stub.writes))
	}
	got := stub.writes[0].Metadata
	want := intmodel.VolumeMetadata{Name: "mem-agent-a1b2c3", Realm: "default", Space: "agents", Stack: "team"}
	if got != want {
		t.Errorf("provisioned volume = %+v, want %+v", got, want)
	}
}

// TestEnsurePerCellVolumes_VolumeRefScopeHonored confirms a cross-scope ensure
// mount provisions at the ref's explicit coordinates, not the cell's scope.
func TestEnsurePerCellVolumes_VolumeRefScopeHonored(t *testing.T) {
	stub := &ensureStubRunner{created: true}
	b := &Exec{runner: stub}

	cell := cellWith([3]string{"cell-realm", "cell-space", "cell-stack"}, intmodel.VolumeMount{
		Kind:   intmodel.VolumeKindVolume,
		Target: "/memory",
		Ensure: true,
		VolumeRef: &intmodel.VolumeRef{
			Name:  "mem-agent-a1b2c3",
			Realm: "shared-realm",
			Space: "shared-space",
		},
	})

	if err := b.ensurePerCellVolumes(cell); err != nil {
		t.Fatalf("ensurePerCellVolumes() error = %v", err)
	}
	if len(stub.writes) != 1 {
		t.Fatalf("WriteVolume calls = %d, want 1", len(stub.writes))
	}
	got := stub.writes[0].Metadata
	want := intmodel.VolumeMetadata{Name: "mem-agent-a1b2c3", Realm: "shared-realm", Space: "shared-space"}
	if got != want {
		t.Errorf("provisioned volume = %+v, want %+v (ref scope, not cell scope)", got, want)
	}
}

// TestEnsurePerCellVolumes_SkipsNonEnsureAndNonVolume confirms the provisioning
// step is opt-in: a kind: volume mount without Ensure (step 4's default
// hard-error-on-missing) and bind/tmpfs mounts are never provisioned.
func TestEnsurePerCellVolumes_SkipsNonEnsureAndNonVolume(t *testing.T) {
	stub := &ensureStubRunner{created: true}
	b := &Exec{runner: stub}

	cell := cellWith([3]string{"default", "", ""},
		intmodel.VolumeMount{Kind: intmodel.VolumeKindVolume, Source: "shared", Target: "/s"}, // no Ensure
		intmodel.VolumeMount{Kind: intmodel.VolumeKindBind, Source: "/host", Target: "/b", Ensure: true},
		intmodel.VolumeMount{Kind: intmodel.VolumeKindTmpfs, Target: "/t", Ensure: true},
	)

	if err := b.ensurePerCellVolumes(cell); err != nil {
		t.Fatalf("ensurePerCellVolumes() error = %v", err)
	}
	if len(stub.writes) != 0 {
		t.Errorf("WriteVolume calls = %d, want 0 (only ensure kind: volume mounts provision)", len(stub.writes))
	}
}

// TestEnsurePerCellVolumes_Idempotent confirms re-running against a cell whose
// Volume already exists (WriteVolume reports created=false) is a no-op error-
// wise and re-targets the same deterministic name — never minting a fresh
// Volume for an already-bound cell (#1017 AC4).
func TestEnsurePerCellVolumes_Idempotent(t *testing.T) {
	stub := &ensureStubRunner{created: false} // models an already-provisioned volume
	b := &Exec{runner: stub}

	cell := cellWith([3]string{"default", "", ""}, intmodel.VolumeMount{
		Kind:   intmodel.VolumeKindVolume,
		Source: "mem-agent-a1b2c3",
		Target: "/memory",
		Ensure: true,
	})

	for i := 0; i < 2; i++ {
		if err := b.ensurePerCellVolumes(cell); err != nil {
			t.Fatalf("ensurePerCellVolumes() pass %d error = %v", i, err)
		}
	}
	if len(stub.writes) != 2 {
		t.Fatalf("WriteVolume calls = %d, want 2", len(stub.writes))
	}
	if stub.writes[0].Metadata.Name != stub.writes[1].Metadata.Name {
		t.Errorf("re-provision targeted different names %q vs %q (should be deterministic)",
			stub.writes[0].Metadata.Name, stub.writes[1].Metadata.Name)
	}
}

// TestEnsurePerCellVolumes_PropagatesError confirms a provisioning failure is
// surfaced (wrapped as ErrWriteVolume) so cell create/start fails fast rather
// than starting a container whose Volume mount cannot resolve.
func TestEnsurePerCellVolumes_PropagatesError(t *testing.T) {
	sentinel := errors.New("disk full")
	stub := &ensureStubRunner{writeErr: sentinel}
	b := &Exec{runner: stub}

	cell := cellWith([3]string{"default", "", ""}, intmodel.VolumeMount{
		Kind:   intmodel.VolumeKindVolume,
		Source: "mem-agent-a1b2c3",
		Target: "/memory",
		Ensure: true,
	})

	err := b.ensurePerCellVolumes(cell)
	if !errors.Is(err, errdefs.ErrWriteVolume) {
		t.Errorf("error = %v, want wrapped ErrWriteVolume", err)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want to wrap the underlying cause", err)
	}
}

// reapplyStubRunner records the order of the runner calls the reapply helpers
// make — WriteVolume (via ReconcileVolume), RecreateCell, StartCell, UpdateCell —
// so the per-cell-volume tests can assert provisioning precedes the container
// rebuild. Scope getters return their input (the scope exists); the embedded
// runner.Runner panics on any other method, asserting nothing else is touched.
type reapplyStubRunner struct {
	runner.Runner
	order *[]string
	out   intmodel.Cell
}

func (s *reapplyStubRunner) GetRealm(r intmodel.Realm) (intmodel.Realm, error)  { return r, nil }
func (s *reapplyStubRunner) GetSpace(sp intmodel.Space) (intmodel.Space, error) { return sp, nil }
func (s *reapplyStubRunner) GetStack(st intmodel.Stack) (intmodel.Stack, error) { return st, nil }

func (s *reapplyStubRunner) WriteVolume(v intmodel.Volume) (bool, error) {
	*s.order = append(*s.order, "WriteVolume:"+v.Metadata.Name)
	return true, nil
}

func (s *reapplyStubRunner) RecreateCell(_ intmodel.Cell) (intmodel.Cell, error) {
	*s.order = append(*s.order, "RecreateCell")
	return s.out, nil
}

func (s *reapplyStubRunner) StartCell(_ intmodel.Cell) (intmodel.Cell, error) {
	*s.order = append(*s.order, "StartCell")
	return s.out, nil
}

func (s *reapplyStubRunner) UpdateCell(_ intmodel.Cell) (intmodel.Cell, error) {
	*s.order = append(*s.order, "UpdateCell")
	return s.out, nil
}

// desiredWithEnsureMount builds a re-materialised `desired` cell carrying a
// single bare-source ensure mount — the shape ExpandPerCellVolumes stamps when a
// Config edit adds a ${CELL_NAME} mount.
func desiredWithEnsureMount() intmodel.Cell {
	return cellWith([3]string{"default", "agents", "team"}, intmodel.VolumeMount{
		Kind:   intmodel.VolumeKindVolume,
		Source: "mem-agent-a1b2c3",
		Target: "/memory",
		Ensure: true,
	})
}

// TestReapplyBreaking_ProvisionsDesiredBeforeRecreate is the #1294 reconcile-gap
// regression: an OutOfSync breaking reapply re-materialises a `desired` spec that
// may add a new per-cell ${CELL_NAME} mount the StartCell-level ensure pass (run
// against the on-disk spec) never saw. reapplyBreaking must provision `desired`'s
// ensure volumes *before* RecreateCell rebuilds containers against it, or step
// 4's resolver hard-errors on the unprovisioned Volume and OutOfSync never
// converges.
func TestReapplyBreaking_ProvisionsDesiredBeforeRecreate(t *testing.T) {
	var order []string
	out := desiredWithEnsureMount()
	out.Status.State = intmodel.CellStateReady
	b := newReapplyTestExec(&reapplyStubRunner{order: &order, out: out})

	cell := desiredWithEnsureMount()
	_, started, ok := b.reapplyBreaking(cell, desiredWithEnsureMount(), "cfg")
	if !ok || !started {
		t.Fatalf("reapplyBreaking() = (_, started=%v, ok=%v), want both true", started, ok)
	}
	want := []string{"WriteVolume:mem-agent-a1b2c3", "RecreateCell"}
	if len(order) != 2 || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("call order = %v, want %v (provision before recreate)", order, want)
	}
}

// TestReapplyCompatibleInPlace_ProvisionsDesiredBeforeUpdate is the in-place
// counterpart of the #1294 gap: a compatible/additive reapply that adds a new
// per-cell mount must provision `desired`'s ensure volumes before UpdateCell
// stop-removes-recreates the affected child against it. StartCell (on the on-disk
// spec) runs first and needs no new Volume; the provision lands between StartCell
// and UpdateCell.
func TestReapplyCompatibleInPlace_ProvisionsDesiredBeforeUpdate(t *testing.T) {
	var order []string
	out := desiredWithEnsureMount()
	out.Status.State = intmodel.CellStateReady
	b := newReapplyTestExec(&reapplyStubRunner{order: &order, out: out})

	cell := desiredWithEnsureMount()
	_, started, ok := b.reapplyCompatibleInPlace(cell, desiredWithEnsureMount(), "cfg")
	if !ok || !started {
		t.Fatalf("reapplyCompatibleInPlace() = (_, started=%v, ok=%v), want both true", started, ok)
	}
	want := []string{"StartCell", "WriteVolume:mem-agent-a1b2c3", "UpdateCell"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("call order = %v, want %v (provision between start and update)", order, want)
	}
}
