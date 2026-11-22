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

//nolint:testpackage // tests exercise private provision/ensure helpers on *Exec
package runner

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// Parent health observations (#1381): provision, ensure and the daemon-tick
// ReconcileXHealth methods must all persist cgroupReady (and the realm's
// containerdNamespaceReady) from what they actually observed. Every
// assertion reads the persisted document back through GetX, since the bug
// was the on-disk document never carrying the fields.

const healthTestRealm, healthTestSpace, healthTestStack = "audit", "lab", "suite"

// errCgroupAbsent mirrors the error string ExistsCgroup maps to "absent".
var errCgroupAbsent = errors.New("cgroup path does not exist")

// newHealthTestExec returns an Exec over fake whose ContainerdSocket points
// at a real file, so ExistsRealmContainerdNamespace consults the fake
// instead of short-circuiting to "absent" on a missing socket.
func newHealthTestExec(t *testing.T, fake *deleteCellFakeClient) *Exec {
	t.Helper()
	r := newDeleteCellTestExec(t, fake)
	socket := filepath.Join(t.TempDir(), "containerd.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}
	r.opts.ContainerdSocket = socket
	return r
}

func healthRealm() intmodel.Realm {
	return intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: healthTestRealm},
		Spec:     intmodel.RealmSpec{Namespace: healthTestRealm + ".kukeon.io"},
	}
}

func healthSpace() intmodel.Space {
	return intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: healthTestSpace},
		Spec:     intmodel.SpaceSpec{RealmName: healthTestRealm},
	}
}

func healthStack() intmodel.Stack {
	return intmodel.Stack{
		Metadata: intmodel.StackMetadata{Name: healthTestStack},
		Spec: intmodel.StackSpec{
			ID:        healthTestStack,
			RealmName: healthTestRealm,
			SpaceName: healthTestSpace,
		},
	}
}

// seedHealthDocs persists a Ready realm/space/stack trio whose health
// observations carry the given values, the shape of a pre-fix host (false)
// or of a host whose cgroups a reboot just wiped (true).
func seedHealthDocs(t *testing.T, r *Exec, ready bool) {
	t.Helper()
	for _, dir := range []string{
		filepath.Dir(fs.RealmMetadataPath(r.opts.RunPath, healthTestRealm)),
		filepath.Dir(fs.SpaceMetadataPath(r.opts.RunPath, healthTestRealm, healthTestSpace)),
		filepath.Dir(fs.StackMetadataPath(r.opts.RunPath, healthTestRealm, healthTestSpace, healthTestStack)),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	realm := healthRealm()
	realm.Status = intmodel.RealmStatus{
		State:                    intmodel.RealmStateReady,
		CgroupPath:               "/kukeon/" + healthTestRealm,
		SubtreeControllers:       []string{"cpu", "memory"},
		CgroupReady:              ready,
		ContainerdNamespaceReady: ready,
	}
	if err := r.UpdateRealmMetadata(realm); err != nil {
		t.Fatalf("seed realm: %v", err)
	}
	space := healthSpace()
	space.Status = intmodel.SpaceStatus{
		State:              intmodel.SpaceStateReady,
		CgroupPath:         "/kukeon/" + healthTestRealm + "/" + healthTestSpace,
		SubtreeControllers: []string{"cpu", "memory"},
		CgroupReady:        ready,
	}
	if err := r.UpdateSpaceMetadata(space); err != nil {
		t.Fatalf("seed space: %v", err)
	}
	stack := healthStack()
	stack.Status = intmodel.StackStatus{
		State:              intmodel.StackStateReady,
		CgroupPath:         "/kukeon/" + healthTestRealm + "/" + healthTestSpace + "/" + healthTestStack,
		SubtreeControllers: []string{"cpu", "memory"},
		CgroupReady:        ready,
	}
	if err := r.UpdateStackMetadata(stack); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

func getHealthRealm(t *testing.T, r *Exec) intmodel.Realm {
	t.Helper()
	got, err := r.GetRealm(healthRealm())
	if err != nil {
		t.Fatalf("GetRealm: %v", err)
	}
	return got
}

func getHealthSpace(t *testing.T, r *Exec) intmodel.Space {
	t.Helper()
	got, err := r.GetSpace(healthSpace())
	if err != nil {
		t.Fatalf("GetSpace: %v", err)
	}
	return got
}

func getHealthStack(t *testing.T, r *Exec) intmodel.Stack {
	t.Helper()
	got, err := r.GetStack(healthStack())
	if err != nil {
		t.Fatalf("GetStack: %v", err)
	}
	return got
}

func TestProvisionNewRealm_PersistsHealthObservations(t *testing.T) {
	fake := &deleteCellFakeClient{}
	r := newHealthTestExec(t, fake)

	realm := healthRealm()
	realm.Status.State = intmodel.RealmStateCreating
	if _, err := r.provisionNewRealm(realm); err != nil {
		t.Fatalf("provisionNewRealm: %v", err)
	}

	got := getHealthRealm(t, r)
	if got.Status.State != intmodel.RealmStateReady {
		t.Errorf("state = %v, want Ready", got.Status.State)
	}
	if !got.Status.CgroupReady {
		t.Error("persisted cgroupReady = false after successful provision, want true")
	}
	if !got.Status.ContainerdNamespaceReady {
		t.Error("persisted containerdNamespaceReady = false after successful provision, want true")
	}
}

func TestProvisionNewRealm_PreSeededNamespaceIsReady(t *testing.T) {
	fake := &deleteCellFakeClient{
		existsNamespaceFn: func(string) (bool, error) { return true, nil },
	}
	r := newHealthTestExec(t, fake)

	if _, err := r.provisionNewRealm(healthRealm()); err != nil {
		t.Fatalf("provisionNewRealm: %v", err)
	}
	if got := getHealthRealm(t, r); !got.Status.ContainerdNamespaceReady {
		t.Error("containerdNamespaceReady = false for a pre-seeded namespace, want true")
	}
}

func TestProvisionNewRealm_FailedCgroupDoesNotMarkHealthy(t *testing.T) {
	fake := &deleteCellFakeClient{
		newCgroupFn: func(ctr.CgroupSpec) (*cgroup2.Manager, error) {
			return nil, errors.New("boom")
		},
	}
	r := newHealthTestExec(t, fake)

	if _, err := r.provisionNewRealm(healthRealm()); err == nil {
		t.Fatal("provisionNewRealm: want error from failed cgroup create")
	}
	got := getHealthRealm(t, r)
	if got.Status.State != intmodel.RealmStateFailed {
		t.Errorf("state = %v, want Failed", got.Status.State)
	}
	if got.Status.CgroupReady {
		t.Error("cgroupReady = true after a failed cgroup create, want false")
	}
}

func TestProvisionNewStack_PersistsCgroupReady(t *testing.T) {
	fake := &deleteCellFakeClient{}
	r := newHealthTestExec(t, fake)
	if err := os.MkdirAll(filepath.Dir(
		fs.StackMetadataPath(r.opts.RunPath, healthTestRealm, healthTestSpace, healthTestStack)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := r.provisionNewStack(healthStack()); err != nil {
		t.Fatalf("provisionNewStack: %v", err)
	}
	if got := getHealthStack(t, r); !got.Status.CgroupReady {
		t.Error("persisted cgroupReady = false after successful provision, want true")
	}
}

// TestEnsure_FillsMissingHealthObservations is the repeated-create AC: a
// healthy existing realm/space/stack whose document predates the fix (fields
// false) gets them filled by the idempotent ensure pass.
func TestEnsure_FillsMissingHealthObservations(t *testing.T) {
	fake := &deleteCellFakeClient{
		existsNamespaceFn: func(string) (bool, error) { return true, nil },
	}
	r := newHealthTestExec(t, fake)
	seedHealthDocs(t, r, false)

	if _, err := r.EnsureRealm(getHealthRealm(t, r)); err != nil {
		t.Fatalf("EnsureRealm: %v", err)
	}
	if _, err := r.ensureSpaceCgroup(getHealthSpace(t, r)); err != nil {
		t.Fatalf("ensureSpaceCgroup: %v", err)
	}
	if _, err := r.EnsureStack(getHealthStack(t, r)); err != nil {
		t.Fatalf("EnsureStack: %v", err)
	}

	realm := getHealthRealm(t, r)
	if !realm.Status.CgroupReady || !realm.Status.ContainerdNamespaceReady {
		t.Errorf("realm cgroupReady=%v containerdNamespaceReady=%v after ensure, want both true",
			realm.Status.CgroupReady, realm.Status.ContainerdNamespaceReady)
	}
	if !getHealthSpace(t, r).Status.CgroupReady {
		t.Error("space cgroupReady = false after ensure, want true")
	}
	if !getHealthStack(t, r).Status.CgroupReady {
		t.Error("stack cgroupReady = false after ensure, want true")
	}
}

func TestEnsureRealm_FailedNamespaceProbeDoesNotMarkHealthy(t *testing.T) {
	fake := &deleteCellFakeClient{
		existsNamespaceFn: func(string) (bool, error) { return false, errors.New("containerd down") },
	}
	r := newHealthTestExec(t, fake)
	seedHealthDocs(t, r, false)

	if _, err := r.EnsureRealm(getHealthRealm(t, r)); err == nil {
		t.Fatal("EnsureRealm: want error from failed namespace probe")
	}
	if got := getHealthRealm(t, r); got.Status.ContainerdNamespaceReady || got.Status.CgroupReady {
		t.Errorf("realm cgroupReady=%v containerdNamespaceReady=%v after failed ensure, want both false",
			got.Status.CgroupReady, got.Status.ContainerdNamespaceReady)
	}
}

// TestReconcileHealth_FlipsBothDirections covers the daemon-tick AC: a wiped
// cgroup/namespace flips the persisted observations true→false, and their
// return flips them false→true, with every other status field preserved.
func TestReconcileHealth_FlipsBothDirections(t *testing.T) {
	present := false
	fake := &deleteCellFakeClient{
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			if present {
				return &cgroup2.Manager{}, nil
			}
			return nil, errCgroupAbsent
		},
		existsNamespaceFn: func(string) (bool, error) { return present, nil },
	}
	r := newHealthTestExec(t, fake)
	seedHealthDocs(t, r, true)
	seededRealm := getHealthRealm(t, r)

	reconcileAll := func(wantUpdated bool) {
		t.Helper()
		for name, fn := range map[string]func() (bool, error){
			"realm": func() (bool, error) { return r.ReconcileRealmHealth(healthRealm()) },
			"space": func() (bool, error) { return r.ReconcileSpaceHealth(healthSpace()) },
			"stack": func() (bool, error) { return r.ReconcileStackHealth(healthStack()) },
		} {
			updated, err := fn()
			if err != nil {
				t.Fatalf("Reconcile%sHealth: %v", name, err)
			}
			if updated != wantUpdated {
				t.Errorf("Reconcile%sHealth updated = %v, want %v", name, updated, wantUpdated)
			}
		}
	}
	assertHealth := func(want bool) {
		t.Helper()
		realm := getHealthRealm(t, r)
		if realm.Status.CgroupReady != want || realm.Status.ContainerdNamespaceReady != want {
			t.Errorf("realm cgroupReady=%v containerdNamespaceReady=%v, want %v",
				realm.Status.CgroupReady, realm.Status.ContainerdNamespaceReady, want)
		}
		if got := getHealthSpace(t, r).Status.CgroupReady; got != want {
			t.Errorf("space cgroupReady = %v, want %v", got, want)
		}
		if got := getHealthStack(t, r).Status.CgroupReady; got != want {
			t.Errorf("stack cgroupReady = %v, want %v", got, want)
		}
	}

	// Reboot: everything wiped.
	reconcileAll(true)
	assertHealth(false)
	// Steady state: nothing to write.
	reconcileAll(false)
	// Recovery: cgroups and namespace back.
	present = true
	reconcileAll(true)
	assertHealth(true)

	got := getHealthRealm(t, r)
	if got.Status.State != intmodel.RealmStateReady {
		t.Errorf("realm state = %v, want Ready (health reconcile must not re-derive State)", got.Status.State)
	}
	if !reflect.DeepEqual(got.Status.SubtreeControllers, seededRealm.Status.SubtreeControllers) {
		t.Errorf("realm subtreeControllers = %v, want %v preserved",
			got.Status.SubtreeControllers, seededRealm.Status.SubtreeControllers)
	}
	if got.Status.CgroupPath != seededRealm.Status.CgroupPath {
		t.Errorf("realm cgroupPath = %q, want %q preserved", got.Status.CgroupPath, seededRealm.Status.CgroupPath)
	}
	if !got.Status.CreatedAt.Equal(seededRealm.Status.CreatedAt) ||
		!got.Status.ReadyAt.Equal(seededRealm.Status.ReadyAt) {
		t.Error("realm createdAt/readyAt changed; set-once lifecycle timestamps must survive")
	}
	if got.Metadata.Generation != seededRealm.Metadata.Generation {
		t.Errorf("realm generation = %d, want %d (status-only write must not bump it)",
			got.Metadata.Generation, seededRealm.Metadata.Generation)
	}
}

// TestReconcileHealth_ProbeErrorKeepsLastObservation pins that an errored
// probe is not an observation: nothing is written either way.
func TestReconcileHealth_ProbeErrorKeepsLastObservation(t *testing.T) {
	for _, seeded := range []bool{true, false} {
		fake := &deleteCellFakeClient{
			loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
				return nil, errors.New("permission denied")
			},
			existsNamespaceFn: func(string) (bool, error) { return false, errors.New("containerd down") },
		}
		r := newHealthTestExec(t, fake)
		seedHealthDocs(t, r, seeded)

		for name, fn := range map[string]func() (bool, error){
			"realm": func() (bool, error) { return r.ReconcileRealmHealth(healthRealm()) },
			"space": func() (bool, error) { return r.ReconcileSpaceHealth(healthSpace()) },
			"stack": func() (bool, error) { return r.ReconcileStackHealth(healthStack()) },
		} {
			updated, err := fn()
			if err != nil || updated {
				t.Errorf("seeded=%v Reconcile%sHealth = (%v, %v), want (false, nil)", seeded, name, updated, err)
			}
		}
		realm := getHealthRealm(t, r)
		if realm.Status.CgroupReady != seeded || realm.Status.ContainerdNamespaceReady != seeded ||
			getHealthSpace(t, r).Status.CgroupReady != seeded ||
			getHealthStack(t, r).Status.CgroupReady != seeded {
			t.Errorf("seeded=%v: an errored probe changed a persisted observation", seeded)
		}
	}
}

// TestReconcileHealth_VanishedResourceIsNotResurrected pins that a resource
// deleted after the walk listed it surfaces as NotFound and its metadata is
// not rewritten.
func TestReconcileHealth_VanishedResourceIsNotResurrected(t *testing.T) {
	fake := &deleteCellFakeClient{}
	r := newHealthTestExec(t, fake)

	if _, err := r.ReconcileRealmHealth(healthRealm()); !errors.Is(err, errdefs.ErrRealmNotFound) {
		t.Errorf("ReconcileRealmHealth err = %v, want ErrRealmNotFound", err)
	}
	if _, err := r.ReconcileSpaceHealth(healthSpace()); !errors.Is(err, errdefs.ErrSpaceNotFound) {
		t.Errorf("ReconcileSpaceHealth err = %v, want ErrSpaceNotFound", err)
	}
	if _, err := r.ReconcileStackHealth(healthStack()); !errors.Is(err, errdefs.ErrStackNotFound) {
		t.Errorf("ReconcileStackHealth err = %v, want ErrStackNotFound", err)
	}
	for _, path := range []string{
		fs.RealmMetadataPath(r.opts.RunPath, healthTestRealm),
		fs.SpaceMetadataPath(r.opts.RunPath, healthTestRealm, healthTestSpace),
		fs.StackMetadataPath(r.opts.RunPath, healthTestRealm, healthTestSpace, healthTestStack),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists after reconciling a vanished resource (stat err %v)", path, err)
		}
	}
}

// TestReconcileHealth_DeleteBetweenProbeAndPersistIsNotResurrected pins the
// DeleteX ordering race: the delete removes the cgroup first and the metadata
// after, so a tick that read the document, then probed the already-removed
// cgroup, would flip cgroupReady and persist into a deleted resource. The
// must-exist status write returns NotFound instead of recreating the file.
func TestReconcileHealth_DeleteBetweenProbeAndPersistIsNotResurrected(t *testing.T) {
	cases := []struct {
		name     string
		path     func(r *Exec) string
		notFound error
		run      func(r *Exec) (bool, error)
	}{
		{
			name:     "realm",
			path:     func(r *Exec) string { return fs.RealmMetadataPath(r.opts.RunPath, healthTestRealm) },
			notFound: errdefs.ErrRealmNotFound,
			run:      func(r *Exec) (bool, error) { return r.ReconcileRealmHealth(healthRealm()) },
		},
		{
			name: "space",
			path: func(r *Exec) string {
				return fs.SpaceMetadataPath(r.opts.RunPath, healthTestRealm, healthTestSpace)
			},
			notFound: errdefs.ErrSpaceNotFound,
			run:      func(r *Exec) (bool, error) { return r.ReconcileSpaceHealth(healthSpace()) },
		},
		{
			name: "stack",
			path: func(r *Exec) string {
				return fs.StackMetadataPath(r.opts.RunPath, healthTestRealm, healthTestSpace, healthTestStack)
			},
			notFound: errdefs.ErrStackNotFound,
			run:      func(r *Exec) (bool, error) { return r.ReconcileStackHealth(healthStack()) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r *Exec
			fake := &deleteCellFakeClient{
				// The probe runs after the tick's re-read: deleting the
				// document here lands the delete in the probe→persist window.
				loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
					if err := os.Remove(tc.path(r)); err != nil && !os.IsNotExist(err) {
						t.Fatalf("remove metadata: %v", err)
					}
					return nil, errCgroupAbsent
				},
				existsNamespaceFn: func(string) (bool, error) { return true, nil },
			}
			r = newHealthTestExec(t, fake)
			seedHealthDocs(t, r, true)

			updated, err := tc.run(r)
			if updated || !errors.Is(err, tc.notFound) {
				t.Errorf("Reconcile%sHealth = (%v, %v), want (false, %v)", tc.name, updated, err, tc.notFound)
			}
			if _, statErr := os.Stat(tc.path(r)); !os.IsNotExist(statErr) {
				t.Errorf("%s recreated by the health persist (stat err %v)", tc.path(r), statErr)
			}
		})
	}
}

// TestReconcileHealth_StatusWriteBetweenReadAndPersistIsNotClobbered pins the
// provision race: provisioning persists Creating, then creates the cgroup (and
// realm namespace) the probe reports present, then persists Ready. A tick that
// read the Creating document and persisted its snapshot after that Ready write
// would roll the resource back to Creating with an empty cgroupPath. A
// status-only write does not bump the generation, so only the tick's
// read-matches-disk precondition catches it; the tick must skip instead.
func TestReconcileHealth_StatusWriteBetweenReadAndPersistIsNotClobbered(t *testing.T) {
	const cgroupPath = "/kukeon/provisioned"
	controllers := []string{"cpu", "memory"}
	cases := []struct {
		name      string
		seed      func(r *Exec) error
		provision func(r *Exec) error
		run       func(r *Exec) (bool, error)
		check     func(t *testing.T, r *Exec)
	}{
		{
			name: "realm",
			seed: func(r *Exec) error {
				realm := healthRealm()
				realm.Status.State = intmodel.RealmStateCreating
				return r.UpdateRealmMetadata(realm)
			},
			provision: func(r *Exec) error {
				realm := healthRealm()
				realm.Status = intmodel.RealmStatus{
					State:                    intmodel.RealmStateReady,
					CgroupPath:               cgroupPath,
					SubtreeControllers:       controllers,
					CgroupReady:              true,
					ContainerdNamespaceReady: true,
				}
				return r.UpdateRealmMetadata(realm)
			},
			run: func(r *Exec) (bool, error) { return r.ReconcileRealmHealth(healthRealm()) },
			check: func(t *testing.T, r *Exec) {
				t.Helper()
				got := getHealthRealm(t, r).Status
				if got.State != intmodel.RealmStateReady || got.CgroupPath != cgroupPath ||
					!reflect.DeepEqual(got.SubtreeControllers, controllers) {
					t.Errorf("realm status = %+v, want the provision's Ready write preserved", got)
				}
			},
		},
		{
			name: "space",
			seed: func(r *Exec) error {
				space := healthSpace()
				space.Status.State = intmodel.SpaceStateCreating
				return r.UpdateSpaceMetadata(space)
			},
			provision: func(r *Exec) error {
				space := healthSpace()
				space.Status = intmodel.SpaceStatus{
					State:              intmodel.SpaceStateReady,
					CgroupPath:         cgroupPath,
					SubtreeControllers: controllers,
					CgroupReady:        true,
				}
				return r.UpdateSpaceMetadata(space)
			},
			run: func(r *Exec) (bool, error) { return r.ReconcileSpaceHealth(healthSpace()) },
			check: func(t *testing.T, r *Exec) {
				t.Helper()
				got := getHealthSpace(t, r).Status
				if got.State != intmodel.SpaceStateReady || got.CgroupPath != cgroupPath ||
					!reflect.DeepEqual(got.SubtreeControllers, controllers) {
					t.Errorf("space status = %+v, want the provision's Ready write preserved", got)
				}
			},
		},
		{
			name: "stack",
			seed: func(r *Exec) error {
				stack := healthStack()
				stack.Status.State = intmodel.StackStatePending
				return r.UpdateStackMetadata(stack)
			},
			provision: func(r *Exec) error {
				stack := healthStack()
				stack.Status = intmodel.StackStatus{
					State:              intmodel.StackStateReady,
					CgroupPath:         cgroupPath,
					SubtreeControllers: controllers,
					CgroupReady:        true,
				}
				return r.UpdateStackMetadata(stack)
			},
			run: func(r *Exec) (bool, error) { return r.ReconcileStackHealth(healthStack()) },
			check: func(t *testing.T, r *Exec) {
				t.Helper()
				got := getHealthStack(t, r).Status
				if got.State != intmodel.StackStateReady || got.CgroupPath != cgroupPath ||
					!reflect.DeepEqual(got.SubtreeControllers, controllers) {
					t.Errorf("stack status = %+v, want the provision's Ready write preserved", got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r *Exec
			fake := &deleteCellFakeClient{
				// The probe runs after the tick's re-read: the provision's
				// Ready write lands in the read→persist window.
				loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
					if err := tc.provision(r); err != nil {
						t.Fatalf("provision Ready write: %v", err)
					}
					return &cgroup2.Manager{}, nil
				},
				existsNamespaceFn: func(string) (bool, error) { return true, nil },
			}
			r = newHealthTestExec(t, fake)
			seedHealthDocs(t, r, false)
			if err := tc.seed(r); err != nil {
				t.Fatalf("seed Creating document: %v", err)
			}

			updated, err := tc.run(r)
			if err != nil || updated {
				t.Errorf("Reconcile%sHealth = (%v, %v), want (false, nil)", tc.name, updated, err)
			}
			tc.check(t, r)
		})
	}
}

// TestReconcileRealmHealth_MissingSocketKeepsNamespaceObservation pins that a
// missing containerd socket (containerd restarting) is a probe error on the
// tick path, not a clean "absent": containerdNamespaceReady must not flap.
func TestReconcileRealmHealth_MissingSocketKeepsNamespaceObservation(t *testing.T) {
	fake := &deleteCellFakeClient{
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return nil, errors.New("permission denied")
		},
		existsNamespaceFn: func(string) (bool, error) { return true, nil },
	}
	r := newHealthTestExec(t, fake)
	seedHealthDocs(t, r, true)
	if err := os.Remove(r.opts.ContainerdSocket); err != nil {
		t.Fatalf("remove fake socket: %v", err)
	}

	updated, err := r.ReconcileRealmHealth(healthRealm())
	if err != nil || updated {
		t.Fatalf("ReconcileRealmHealth = (%v, %v), want (false, nil)", updated, err)
	}
	if !getHealthRealm(t, r).Status.ContainerdNamespaceReady {
		t.Error("missing containerd socket flipped containerdNamespaceReady to false")
	}
}
