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
