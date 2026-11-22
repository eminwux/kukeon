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

//nolint:testpackage // tests exercise RefreshX against the private test Exec
package runner

import (
	"reflect"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v3/cgroup2"
)

// RefreshRealm/Space/Stack rebuild status from what they observe and persist
// it wholesale on any detected change. Fields refresh never derives —
// subtreeControllers (#328) and observedGeneration — must survive that
// persist (#1466). Each case seeds them, lets the cgroup come back present
// so cgroupReady flips false→true (a refresh-detected change), and reads the
// persisted document back through GetX.

const refreshTestObservedGeneration int64 = 7

func newRefreshTestExec(t *testing.T) *Exec {
	t.Helper()
	fake := &deleteCellFakeClient{
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return &cgroup2.Manager{}, nil
		},
		existsNamespaceFn: func(string) (bool, error) { return true, nil },
	}
	r := newHealthTestExec(t, fake)
	seedHealthDocs(t, r, false)
	return r
}

func TestRefreshRealm_PreservesUnderivedStatusFields(t *testing.T) {
	r := newRefreshTestExec(t)
	realm := getHealthRealm(t, r)
	realm.Status.ObservedGeneration = refreshTestObservedGeneration
	if err := r.UpdateRealmMetadata(realm); err != nil {
		t.Fatalf("seed realm observedGeneration: %v", err)
	}
	realm = getHealthRealm(t, r)

	if _, updated, err := r.RefreshRealm(realm); err != nil || !updated {
		t.Fatalf("RefreshRealm = (updated %v, err %v), want (true, nil)", updated, err)
	}

	got := getHealthRealm(t, r).Status
	if !got.CgroupReady {
		t.Fatal("persisted cgroupReady = false, want true: refresh change did not persist")
	}
	if want := []string{"cpu", "memory"}; !reflect.DeepEqual(got.SubtreeControllers, want) {
		t.Errorf("persisted subtreeControllers = %v, want %v", got.SubtreeControllers, want)
	}
	if got.ObservedGeneration != refreshTestObservedGeneration {
		t.Errorf("persisted observedGeneration = %d, want %d",
			got.ObservedGeneration, refreshTestObservedGeneration)
	}
}

func TestRefreshSpace_PreservesUnderivedStatusFields(t *testing.T) {
	r := newRefreshTestExec(t)
	space := getHealthSpace(t, r)
	space.Status.ObservedGeneration = refreshTestObservedGeneration
	if err := r.UpdateSpaceMetadata(space); err != nil {
		t.Fatalf("seed space observedGeneration: %v", err)
	}
	space = getHealthSpace(t, r)

	if _, updated, err := r.RefreshSpace(space); err != nil || !updated {
		t.Fatalf("RefreshSpace = (updated %v, err %v), want (true, nil)", updated, err)
	}

	got := getHealthSpace(t, r).Status
	if got.State == space.Status.State && got.CgroupReady == space.Status.CgroupReady {
		t.Fatal("persisted space status unchanged: refresh change did not persist")
	}
	if want := []string{"cpu", "memory"}; !reflect.DeepEqual(got.SubtreeControllers, want) {
		t.Errorf("persisted subtreeControllers = %v, want %v", got.SubtreeControllers, want)
	}
	if got.ObservedGeneration != refreshTestObservedGeneration {
		t.Errorf("persisted observedGeneration = %d, want %d",
			got.ObservedGeneration, refreshTestObservedGeneration)
	}
}

func TestRefreshStack_PreservesUnderivedStatusFields(t *testing.T) {
	r := newRefreshTestExec(t)
	stack := getHealthStack(t, r)
	stack.Status.ObservedGeneration = refreshTestObservedGeneration
	if err := r.UpdateStackMetadata(stack); err != nil {
		t.Fatalf("seed stack observedGeneration: %v", err)
	}
	stack = getHealthStack(t, r)

	if _, updated, err := r.RefreshStack(stack); err != nil || !updated {
		t.Fatalf("RefreshStack = (updated %v, err %v), want (true, nil)", updated, err)
	}

	got := getHealthStack(t, r).Status
	if !got.CgroupReady {
		t.Fatal("persisted cgroupReady = false, want true: refresh change did not persist")
	}
	if want := []string{"cpu", "memory"}; !reflect.DeepEqual(got.SubtreeControllers, want) {
		t.Errorf("persisted subtreeControllers = %v, want %v", got.SubtreeControllers, want)
	}
	if got.ObservedGeneration != refreshTestObservedGeneration {
		t.Errorf("persisted observedGeneration = %d, want %d",
			got.ObservedGeneration, refreshTestObservedGeneration)
	}
}
