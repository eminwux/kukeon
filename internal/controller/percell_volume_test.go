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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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
	// existing are the Volumes already on disk, with their stored spec.
	existing map[intmodel.VolumeMetadata]intmodel.VolumeSpec
}

func (s *ensureStubRunner) GetRealm(r intmodel.Realm) (intmodel.Realm, error)  { return r, nil }
func (s *ensureStubRunner) GetSpace(sp intmodel.Space) (intmodel.Space, error) { return sp, nil }
func (s *ensureStubRunner) GetStack(st intmodel.Stack) (intmodel.Stack, error) { return st, nil }
func (s *ensureStubRunner) GetVolume(v intmodel.Volume) (intmodel.Volume, error) {
	if got, ok := s.existing[v.Metadata]; ok {
		return intmodel.Volume{Metadata: v.Metadata, Spec: got}, nil
	}
	return intmodel.Volume{}, errdefs.ErrVolumeNotFound
}

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

func (s *reapplyStubRunner) GetVolume(intmodel.Volume) (intmodel.Volume, error) {
	return intmodel.Volume{}, errdefs.ErrVolumeNotFound
}

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

// loginStubRunner is ensureStubRunner plus the reads resolving a cell's Config
// needs, so ensurePerCellVolumes can find the connector of a state volume.
type loginStubRunner struct {
	ensureStubRunner

	cfg intmodel.CellConfig
	bp  intmodel.CellBlueprint
}

func (s *loginStubRunner) GetConfig(intmodel.CellConfig) (intmodel.CellConfig, error) {
	return s.cfg, nil
}

func (s *loginStubRunner) GetBlueprint(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
	return s.bp, nil
}

func (s *loginStubRunner) ListConfigs(string, string, string) ([]intmodel.CellConfig, error) {
	return nil, nil
}

func (s *loginStubRunner) VolumeLoginStatus(intmodel.Volume) *intmodel.LoginStatus { return nil }

// newLoginStub builds a runner serving one Config whose blueprint enables a
// stateVolume connector with a login marker, and the cell created from it.
func newLoginStub(t *testing.T) (*loginStubRunner, intmodel.Cell, intmodel.VolumeMetadata) {
	t.Helper()
	bpDoc := v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "dev", Realm: "default"},
		Spec: v1beta1.CellBlueprintSpec{
			Parameters: []v1beta1.CellBlueprintParameter{
				{Name: "STATE_VOLUME"},
				{Name: "STATE_VOLUME_REALM"},
				{Name: "STATE_VOLUME_SPACE"},
				{Name: "STATE_VOLUME_STACK"},
			},
			Components: []v1beta1.BlueprintComponent{{
				Kind: v1beta1.ComponentKindConnector, Name: "agent-login", Optional: true,
				Connector: &v1beta1.BlueprintConnector{
					Name: "agent", Method: "login", Delivery: v1beta1.ConnectorDeliveryStateVolume,
					SecretTypes: []string{v1beta1.SecretTypeOpaque}, Harness: "agent",
					Login: &v1beta1.ConnectorLogin{Marker: "auth.json"},
				},
			}},
			Cell: v1beta1.BlueprintCellSpec{Containers: []v1beta1.BlueprintContainer{{
				ID: "main", Image: "busybox", Attachable: true,
			}}},
		},
	}
	cfgDoc := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "dev", Realm: "default"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint:  v1beta1.CellConfigBlueprintRef{Name: "dev", Realm: "default"},
			Components: []string{"agent-login"},
			Values: map[string]string{
				"STATE_VOLUME": "dev-state", "STATE_VOLUME_REALM": "default",
				"STATE_VOLUME_SPACE": "teams", "STATE_VOLUME_STACK": "kukeon",
			},
		},
	}
	bp, err := apischeme.ConvertCellBlueprintDocToInternal(bpDoc)
	if err != nil {
		t.Fatalf("convert blueprint: %v", err)
	}
	cfg, err := apischeme.ConvertCellConfigDocToInternal(cfgDoc)
	if err != nil {
		t.Fatalf("convert config: %v", err)
	}
	volume := intmodel.VolumeMetadata{Name: "dev-state", Realm: "default", Space: "teams", Stack: "kukeon"}
	cell := cellWith([3]string{"default", "default", "default"}, intmodel.VolumeMount{
		Kind: intmodel.VolumeKindVolume, Target: "/home/user/.agent", Ensure: true,
		VolumeRef: &intmodel.VolumeRef{
			Name: volume.Name, Realm: volume.Realm, Space: volume.Space, Stack: volume.Stack,
		},
	})
	cell.Spec.Provenance = &intmodel.CellProvenance{
		BindingKind: v1beta1.BindingKindConfig,
		BindingRef:  intmodel.CellBindingRef{Name: "dev", Realm: "default"},
	}
	return &loginStubRunner{cfg: cfg, bp: bp}, cell, volume
}

// TestEnsurePerCellVolumes_TagsLoginVolume: starting a cell from a Config
// that uses a login method tags its state Volume with the connector, method
// and login marker — whether the start creates the Volume or it already
// existed untagged — and keeps the reclaim policy an existing Volume has
// (#1685).
func TestEnsurePerCellVolumes_TagsLoginVolume(t *testing.T) {
	want := intmodel.VolumeSpec{Connector: "agent", Method: "login", LoginMarker: "auth.json"}

	t.Run("created by the start", func(t *testing.T) {
		stub, cell, volume := newLoginStub(t)
		if err := (&Exec{runner: stub}).ensurePerCellVolumes(cell); err != nil {
			t.Fatalf("ensurePerCellVolumes() error = %v", err)
		}
		if len(stub.writes) != 1 || stub.writes[0].Metadata != volume || stub.writes[0].Spec != want {
			t.Fatalf("writes = %+v, want %+v tagged %+v", stub.writes, volume, want)
		}
	})

	t.Run("existing and untagged", func(t *testing.T) {
		stub, cell, volume := newLoginStub(t)
		stub.existing = map[intmodel.VolumeMetadata]intmodel.VolumeSpec{
			volume: {ReclaimPolicy: intmodel.ReclaimRetain},
		}
		if err := (&Exec{runner: stub}).ensurePerCellVolumes(cell); err != nil {
			t.Fatalf("ensurePerCellVolumes() error = %v", err)
		}
		retained := want
		retained.ReclaimPolicy = intmodel.ReclaimRetain
		if len(stub.writes) != 1 || stub.writes[0].Spec != retained {
			t.Fatalf("writes = %+v, want the tag and the Retain policy kept", stub.writes)
		}
	})

	t.Run("tagged for another connector", func(t *testing.T) {
		stub, cell, volume := newLoginStub(t)
		stub.existing = map[intmodel.VolumeMetadata]intmodel.VolumeSpec{volume: {Connector: "other"}}
		err := (&Exec{runner: stub}).ensurePerCellVolumes(cell)
		if !errors.Is(err, errdefs.ErrVolumeIdentity) {
			t.Fatalf("error = %v, want ErrVolumeIdentity", err)
		}
		if len(stub.writes) != 0 {
			t.Errorf("writes = %+v, want none", stub.writes)
		}
	})
}

// TestEnsurePerCellVolumes_KeepsExistingSpec: an ensure mount of a cell with
// no Config lineage re-provisions an existing Volume with the spec it has, so
// a start never drops a Retain policy or a login-identity tag.
func TestEnsurePerCellVolumes_KeepsExistingSpec(t *testing.T) {
	volume := intmodel.VolumeMetadata{Name: "mem", Realm: "default", Space: "agents", Stack: "team"}
	spec := intmodel.VolumeSpec{ReclaimPolicy: intmodel.ReclaimRetain, Connector: "agent", Method: "login"}
	stub := &ensureStubRunner{existing: map[intmodel.VolumeMetadata]intmodel.VolumeSpec{volume: spec}}

	cell := cellWith([3]string{"default", "agents", "team"}, intmodel.VolumeMount{
		Kind: intmodel.VolumeKindVolume, Source: "mem", Target: "/memory", Ensure: true,
	})
	if err := (&Exec{runner: stub}).ensurePerCellVolumes(cell); err != nil {
		t.Fatalf("ensurePerCellVolumes() error = %v", err)
	}
	if len(stub.writes) != 1 || stub.writes[0].Spec != spec {
		t.Fatalf("writes = %+v, want the stored spec %+v", stub.writes, spec)
	}
}

// TestBindingFromCell_KeepsLoginIdentityAndState: a re-render of a live cell —
// `kuke restart`, a reconcile-driven recreate — rebuilds its binding from
// provenance, so the login identity the cell picked and its harness state
// choice are rendered again (#1685).
func TestBindingFromCell_KeepsLoginIdentityAndState(t *testing.T) {
	cell := cellWith([3]string{"default", "teams", "core"})
	cell.Spec.Provenance = &intmodel.CellProvenance{
		BindingKind: v1beta1.BindingKindConfig,
		BindingRef:  intmodel.CellBindingRef{Name: "dev", Realm: "default"},
		ConnectorOverrides: map[string]intmodel.ContainerSecretRef{
			"forge": {Name: "forge-token", Realm: "default"},
		},
		ConnectorVolumes: map[string]intmodel.VolumeRef{
			"agent": {Name: "personal-max", Realm: "default", Space: "teams"},
		},
		State: v1beta1.HarnessStateEphemeral,
	}
	binding := bindingFromCell(cell)
	if binding.State != v1beta1.HarnessStateEphemeral {
		t.Errorf("state = %q, want ephemeral", binding.State)
	}
	want := map[string]v1beta1.ContainerSecretRef{
		"forge": {Name: "forge-token", Realm: "default"},
		"agent": {Name: "personal-max", Realm: "default", Space: "teams"},
	}
	if len(binding.Connectors) != len(want) {
		t.Fatalf("connectors = %+v, want %+v", binding.Connectors, want)
	}
	for name, ref := range want {
		if binding.Connectors[name] != ref {
			t.Errorf("connectors[%s] = %+v, want %+v", name, binding.Connectors[name], ref)
		}
	}
}
