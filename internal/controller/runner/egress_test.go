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
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/netpolicy"
	"github.com/eminwux/kukeon/internal/util/fs"
)

type recordingEnforcer struct{ applied, removed []string }

func (e *recordingEnforcer) Apply(_ context.Context, p *netpolicy.Policy) error {
	e.applied = append(e.applied, p.RealmName+"/"+p.SpaceName)
	return nil
}

func (e *recordingEnforcer) Remove(_ context.Context, realmName, spaceName string) error {
	e.removed = append(e.removed, realmName+"/"+spaceName)
	return nil
}

// A space whose conflist and bridge are already gone must still lose its
// egress chain: once the realm metadata is removed nothing reconciles it.
func TestTeardownRealmCNI_RemovesEgressWithoutBridge(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	r.bridgeRunner = &failingTeardownBridge{}
	enforcer := &recordingEnforcer{}
	r.netPolicy = enforcer
	if err := os.MkdirAll(fs.SpaceMetadataDir(runPath, "test-realm", "test-space"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := r.teardownRealmCNI("test-realm"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(enforcer.removed, []string{"test-realm/test-space"}) {
		t.Fatalf("removed egress = %v", enforcer.removed)
	}
}

// A reconcile that listed a space before it was deleted must not re-install
// its egress chain: nothing would be left to remove it.
func TestEnsureSpace_DeletedSpaceInstallsNoEgress(t *testing.T) {
	r := newProvisionTestExec(t, t.TempDir(), false)
	enforcer := &recordingEnforcer{}
	r.netPolicy = enforcer
	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: "test-space"},
		Spec:     intmodel.SpaceSpec{RealmName: "test-realm"},
	}

	_, err := r.EnsureSpace(space)
	if !errors.Is(err, errdefs.ErrSpaceNotFound) {
		t.Fatalf("EnsureSpace error = %v, want ErrSpaceNotFound", err)
	}
	if len(enforcer.applied) != 0 {
		t.Fatalf("applied egress for a deleted space: %v", enforcer.applied)
	}
}

func TestLockSpace_SerializesSameSpaceOnly(t *testing.T) {
	r := &Exec{}
	release := r.lockSpace("realm", "space")

	other := make(chan struct{})
	go func() {
		r.lockSpace("realm", "other")()
		r.lockCell(intmodel.Cell{
			Metadata: intmodel.CellMetadata{Name: "space"},
			Spec:     intmodel.CellSpec{RealmName: "realm"},
		})()
		close(other)
	}()
	select {
	case <-other:
	case <-time.After(5 * time.Second):
		t.Fatal("a different space or a cell blocked on the space lock")
	}

	same := make(chan struct{})
	go func() {
		r.lockSpace("realm", "space")()
		close(same)
	}()
	select {
	case <-same:
		t.Fatal("same space acquired the lock while it was held")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-same:
	case <-time.After(5 * time.Second):
		t.Fatal("same space never acquired the lock after release")
	}
}

// policyEnforcer keeps every policy Apply receives.
type policyEnforcer struct{ applied []*netpolicy.Policy }

func (e *policyEnforcer) Apply(_ context.Context, p *netpolicy.Policy) error {
	e.applied = append(e.applied, p)
	return nil
}

func (e *policyEnforcer) Remove(context.Context, string, string) error { return nil }

func seedEgressSpace(t *testing.T, r *Exec, egress *intmodel.EgressPolicy) intmodel.Space {
	t.Helper()
	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: "spb"},
		Spec: intmodel.SpaceSpec{
			RealmName:     "default",
			CNIConfigPath: "/etc/cni/spb.conflist",
			Network:       &intmodel.SpaceNetwork{Egress: egress},
		},
		Status: intmodel.SpaceStatus{State: intmodel.SpaceStateReady},
	}
	if err := r.UpdateSpaceMetadata(space); err != nil {
		t.Fatalf("seed UpdateSpaceMetadata: %v", err)
	}
	stored, err := r.GetSpace(space)
	if err != nil {
		t.Fatalf("GetSpace: %v", err)
	}
	return stored
}

// TestUpdateSpace_AppliesAndPersistsEgress pins #1703: an apply that changes
// only spec.network.egress persists the new policy and rebuilds the space's
// chain at apply time, in both directions (allow rule added; allow → deny).
func TestUpdateSpace_AppliesAndPersistsEgress(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	enforcer := &policyEnforcer{}
	r.netPolicy = enforcer
	seeded := seedEgressSpace(t, r, &intmodel.EgressPolicy{Default: intmodel.EgressDefaultDeny})

	desired := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: "spb"},
		Spec: intmodel.SpaceSpec{
			RealmName: "default",
			Network: &intmodel.SpaceNetwork{Egress: &intmodel.EgressPolicy{
				Default: intmodel.EgressDefaultDeny,
				Allow:   []intmodel.EgressAllowRule{{CIDR: "10.89.2.0/24", Ports: []int{443}}},
			}},
		},
	}
	updated, err := r.UpdateSpace(desired)
	if err != nil {
		t.Fatalf("UpdateSpace: %v", err)
	}
	if len(enforcer.applied) != 1 {
		t.Fatalf("Apply calls = %d, want 1", len(enforcer.applied))
	}
	got := enforcer.applied[0]
	if got.Default != intmodel.EgressDefaultDeny || len(got.Allow) != 1 ||
		got.Allow[0].CIDR != "10.89.2.0/24" ||
		!slices.Equal(got.Allow[0].Ports, []int{443}) {
		t.Errorf("applied policy = %+v, want deny + 10.89.2.0/24:443", got)
	}
	stored, err := r.GetSpace(desired)
	if err != nil {
		t.Fatalf("GetSpace: %v", err)
	}
	if stored.Spec.Network == nil || stored.Spec.Network.Egress == nil ||
		len(stored.Spec.Network.Egress.Allow) != 1 || stored.Spec.Network.Egress.Allow[0].CIDR != "10.89.2.0/24" {
		t.Errorf("stored network = %+v, want the new allow rule", stored.Spec.Network)
	}
	if stored.Spec.CNIConfigPath != "/etc/cni/spb.conflist" {
		t.Errorf("CNIConfigPath = %q, want it preserved", stored.Spec.CNIConfigPath)
	}
	if stored.Metadata.Generation <= seeded.Metadata.Generation {
		t.Errorf("generation %d not bumped past %d by an egress-only update",
			stored.Metadata.Generation, seeded.Metadata.Generation)
	}
	if updated.Spec.Network == nil || updated.Spec.Network.Egress == nil {
		t.Errorf("returned space lost its network block: %+v", updated.Spec)
	}

	// Removing spec.network clears the policy: the admit-all chain goes in.
	desired.Spec.Network = nil
	if _, err = r.UpdateSpace(desired); err != nil {
		t.Fatalf("UpdateSpace (network omitted): %v", err)
	}
	last := enforcer.applied[len(enforcer.applied)-1]
	if last.Default != intmodel.EgressDefaultAllow || len(last.Allow) != 0 {
		t.Errorf("policy after omitting spec.network = %+v, want admit-all", last)
	}
	if stored, err = r.GetSpace(desired); err != nil {
		t.Fatalf("GetSpace: %v", err)
	}
	if stored.Spec.Network != nil && stored.Spec.Network.Egress != nil {
		t.Errorf("stored egress = %+v after omitting spec.network, want none", stored.Spec.Network.Egress)
	}
}

// TestUpdateSpace_InvalidEgressNotPersisted: a rule the policy layer rejects
// (both host and cidr) fails the update before anything is installed or
// stored, so the reconcile loop keeps re-asserting the old, valid policy.
func TestUpdateSpace_InvalidEgressNotPersisted(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	enforcer := &policyEnforcer{}
	r.netPolicy = enforcer
	seedEgressSpace(t, r, &intmodel.EgressPolicy{Default: intmodel.EgressDefaultDeny})

	desired := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: "spb"},
		Spec: intmodel.SpaceSpec{
			RealmName: "default",
			Network: &intmodel.SpaceNetwork{Egress: &intmodel.EgressPolicy{
				Default: intmodel.EgressDefaultDeny,
				Allow:   []intmodel.EgressAllowRule{{Host: "example.com", CIDR: "10.0.0.0/8"}},
			}},
		},
	}
	if _, err := r.UpdateSpace(desired); !errors.Is(err, errdefs.ErrEgressApply) {
		t.Fatalf("UpdateSpace error = %v, want ErrEgressApply", err)
	}
	if len(enforcer.applied) != 0 {
		t.Errorf("an invalid policy reached the enforcer: %+v", enforcer.applied)
	}
	stored, err := r.GetSpace(desired)
	if err != nil {
		t.Fatalf("GetSpace: %v", err)
	}
	if stored.Spec.Network == nil || stored.Spec.Network.Egress == nil ||
		stored.Spec.Network.Egress.Default != intmodel.EgressDefaultDeny || len(stored.Spec.Network.Egress.Allow) != 0 {
		t.Errorf("stored egress = %+v, want the seeded deny-all policy", stored.Spec.Network)
	}
}
