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

//nolint:testpackage // exercises the unexported generation/CAS write helpers
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func newGenerationTestExec(t *testing.T, runPath string) *Exec {
	t.Helper()
	return &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:   Options{RunPath: runPath},
		nowFn:  func() time.Time { return time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC) },
	}
}

func readRealmGeneration(t *testing.T, r *Exec, name string) intmodel.Realm {
	t.Helper()
	got, err := r.readRealmInternal(fs.RealmMetadataPath(r.opts.RunPath, name))
	if err != nil {
		t.Fatalf("readRealmInternal(%s): %v", name, err)
	}
	return got
}

// TestUpdateRealmMetadataBumpsGenerationOnSpecChange pins AC1: the writer
// stamps generation 1 on the first persist, leaves it untouched on a
// status-only update, and bumps it only when the spec actually changes.
func TestUpdateRealmMetadataBumpsGenerationOnSpecChange(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	ref := intmodel.RealmMetadata{Name: "r-gen"}

	realm := intmodel.Realm{
		Metadata: ref,
		Spec:     intmodel.RealmSpec{Namespace: "r-gen.kukeon.io"},
		Status:   intmodel.RealmStatus{State: intmodel.RealmStateCreating},
	}
	if err := r.UpdateRealmMetadata(realm); err != nil {
		t.Fatalf("first UpdateRealmMetadata: %v", err)
	}
	if got := readRealmGeneration(t, r, "r-gen").Metadata.Generation; got != 1 {
		t.Fatalf("first persist generation = %d, want 1", got)
	}

	// Status-only update: state flips, spec is byte-identical → no bump.
	statusOnly := readRealmGeneration(t, r, "r-gen")
	statusOnly.Status.State = intmodel.RealmStateReady
	statusOnly.Status.Message = "now ready"
	if err := r.UpdateRealmMetadata(statusOnly); err != nil {
		t.Fatalf("status-only UpdateRealmMetadata: %v", err)
	}
	if got := readRealmGeneration(t, r, "r-gen").Metadata.Generation; got != 1 {
		t.Fatalf("status-only persist generation = %d, want 1 (no bump)", got)
	}

	// Spec change: append a registry credential → generation bumps to 2.
	specChange := readRealmGeneration(t, r, "r-gen")
	specChange.Spec.RegistryCredentials = append(specChange.Spec.RegistryCredentials,
		intmodel.RegistryCredentials{ServerAddress: "registry.example.com"})
	if err := r.UpdateRealmMetadata(specChange); err != nil {
		t.Fatalf("spec-change UpdateRealmMetadata: %v", err)
	}
	if got := readRealmGeneration(t, r, "r-gen").Metadata.Generation; got != 2 {
		t.Fatalf("spec-change persist generation = %d, want 2", got)
	}
}

// TestUpdateRealmMetadataStaleTokenRejected pins AC2: a versioned caller
// whose observed generation has been overtaken on disk is rejected with
// errdefs.ErrStaleResource at the writer's call site, rather than silently
// clobbering the newer write.
func TestUpdateRealmMetadataStaleTokenRejected(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seed := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: "r-cas"},
		Spec:     intmodel.RealmSpec{Namespace: "r-cas.kukeon.io"},
		Status:   intmodel.RealmStatus{State: intmodel.RealmStateReady},
	}
	if err := r.UpdateRealmMetadata(seed); err != nil {
		t.Fatalf("seed UpdateRealmMetadata: %v", err)
	}

	// Two callers read the same generation-1 snapshot.
	writerA := readRealmGeneration(t, r, "r-cas")
	writerB := readRealmGeneration(t, r, "r-cas")

	// A lands a spec change first, advancing the on-disk generation to 2.
	writerA.Spec.RegistryCredentials = append(writerA.Spec.RegistryCredentials,
		intmodel.RegistryCredentials{ServerAddress: "reg-a"})
	if err := r.UpdateRealmMetadata(writerA); err != nil {
		t.Fatalf("writer A UpdateRealmMetadata: %v", err)
	}

	// B writes against its now-stale generation-1 token and is rejected.
	writerB.Spec.RegistryCredentials = append(writerB.Spec.RegistryCredentials,
		intmodel.RegistryCredentials{ServerAddress: "reg-b"})
	err := r.UpdateRealmMetadata(writerB)
	if !errors.Is(err, errdefs.ErrStaleResource) {
		t.Fatalf("stale writer B error = %v, want ErrStaleResource", err)
	}

	// A's write survived intact; B's clobber was rejected.
	final := readRealmGeneration(t, r, "r-cas")
	if len(final.Spec.RegistryCredentials) != 1 || final.Spec.RegistryCredentials[0].ServerAddress != "reg-a" {
		t.Fatalf("on-disk credentials = %+v, want only reg-a", final.Spec.RegistryCredentials)
	}
}

// TestUpdateRealmMetadataConcurrentWritesConverge pins AC4: many concurrent
// read-modify-write spec mutations that retry on ErrStaleResource all land
// without a lost update. Goroutines stand in for the daemon and
// `kuke --no-daemon` writers — both go through the same flock + optimistic
// generation primitive, so the convergence property is identical.
func TestUpdateRealmMetadataConcurrentWritesConverge(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seed := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: "r-converge"},
		Spec:     intmodel.RealmSpec{Namespace: "r-converge.kukeon.io"},
		Status:   intmodel.RealmStatus{State: intmodel.RealmStateReady},
	}
	if err := r.UpdateRealmMetadata(seed); err != nil {
		t.Fatalf("seed UpdateRealmMetadata: %v", err)
	}

	const writers = 8
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		go func(i int) {
			defer wg.Done()
			for {
				got := readRealmGeneration(t, r, "r-converge")
				got.Spec.RegistryCredentials = append(got.Spec.RegistryCredentials,
					intmodel.RegistryCredentials{ServerAddress: fmt.Sprintf("reg-%d", i)})
				err := r.UpdateRealmMetadata(got)
				if err == nil {
					return
				}
				if errors.Is(err, errdefs.ErrStaleResource) {
					continue // re-read the winner's state and retry
				}
				t.Errorf("writer %d: unexpected error: %v", i, err)
				return
			}
		}(i)
	}
	wg.Wait()

	final := readRealmGeneration(t, r, "r-converge")
	if got := len(final.Spec.RegistryCredentials); got != writers {
		t.Fatalf("converged credential count = %d, want %d (lost update)", got, writers)
	}
	// Seed (gen 1) + one bump per successful spec-changing write.
	if got := final.Metadata.Generation; got != int64(1+writers) {
		t.Fatalf("converged generation = %d, want %d", got, 1+writers)
	}
}

func seedCell(t *testing.T, r *Exec) intmodel.Cell {
	t.Helper()
	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "c1"},
		Spec: intmodel.CellSpec{
			ID:        "cell-1",
			RealmName: "r1",
			SpaceName: "s1",
			StackName: "st1",
		},
		Status: intmodel.CellStatus{State: intmodel.CellStatePending},
	}
	if err := r.UpdateCellMetadata(cell); err != nil {
		t.Fatalf("seed UpdateCellMetadata: %v", err)
	}
	return cell
}

func readCell(t *testing.T, r *Exec) intmodel.Cell {
	t.Helper()
	got, err := r.readCellInternal(fs.CellMetadataPath(r.opts.RunPath, "r1", "s1", "st1", "c1"))
	if err != nil {
		t.Fatalf("readCellInternal: %v", err)
	}
	return got
}

// TestChainedSpecThenStatusWriteNeedsGenerationRefresh locks in the
// optimistic-token escape valve the create-into-existing-cell chain depends
// on: after a spec-bumping write the in-memory token is stale, so a
// follow-up status persist on the same object is rejected — until
// refreshCellGeneration re-syncs it. EnsureCell calls the refresh for this
// reason.
func TestChainedSpecThenStatusWriteNeedsGenerationRefresh(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seedCell(t, r) // generation 1

	// A spec change bumps the on-disk generation to 2; the in-memory copy
	// still carries the generation-1 token it was read with.
	chained := readCell(t, r)
	chained.Spec.AutoDelete = true
	if err := r.UpdateCellMetadata(chained); err != nil {
		t.Fatalf("spec-bump UpdateCellMetadata: %v", err)
	}

	// Without a refresh, a follow-up status write on the stale object is
	// (correctly) rejected as stale.
	stale := chained
	stale.Status.State = intmodel.CellStateReady
	if err := r.UpdateCellMetadata(stale); !errors.Is(err, errdefs.ErrStaleResource) {
		t.Fatalf("stale follow-up write error = %v, want ErrStaleResource", err)
	}

	// After re-syncing the token, the same status write lands.
	refreshed := chained
	r.refreshCellGeneration(&refreshed)
	refreshed.Status.State = intmodel.CellStateReady
	if err := r.UpdateCellMetadata(refreshed); err != nil {
		t.Fatalf("refreshed follow-up write: %v", err)
	}
	final := readCell(t, r)
	if final.Status.State != intmodel.CellStateReady {
		t.Errorf("status flip not persisted: state = %v", final.Status.State)
	}
	if final.Metadata.Generation != 2 {
		t.Errorf("status-only follow-up bumped generation: got %d, want 2", final.Metadata.Generation)
	}
}

// TestPersistCellStatusGuardedSkipsWhenBehind pins AC3/AC5: when a
// concurrent spec write has advanced the on-disk Generation past the value
// the reconciler observed, the guarded persist skips rather than clobbering
// the newer spec; when the observation is current, it stamps
// ObservedGeneration and persists.
func TestPersistCellStatusGuardedSkipsWhenBehind(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seedCell(t, r) // generation 1

	// Capture the stale reconciler view (generation 1) before the spec moves.
	staleView := readCell(t, r)
	if staleView.Metadata.Generation != 1 {
		t.Fatalf("seed generation = %d, want 1", staleView.Metadata.Generation)
	}

	// A concurrent spec writer advances the cell to generation 2.
	specWrite := readCell(t, r)
	specWrite.Spec.AutoDelete = true
	if err := r.UpdateCellMetadata(specWrite); err != nil {
		t.Fatalf("concurrent spec UpdateCellMetadata: %v", err)
	}
	if got := readCell(t, r).Metadata.Generation; got != 2 {
		t.Fatalf("post-spec-write generation = %d, want 2", got)
	}

	// The reconciler tries to persist a status flip against its stale view.
	staleView.Status.State = intmodel.CellStateReady
	persisted, err := r.persistCellStatusGuarded(staleView)
	if err != nil {
		t.Fatalf("guarded persist (stale): %v", err)
	}
	if persisted {
		t.Fatal("guarded persist reported persisted=true for a stale observation")
	}
	afterSkip := readCell(t, r)
	if !afterSkip.Spec.AutoDelete {
		t.Error("spec clobbered by stale reconciler: AutoDelete reverted to false")
	}
	if afterSkip.Status.State == intmodel.CellStateReady {
		t.Error("stale status flip leaked to disk despite the skip")
	}
	if afterSkip.Metadata.Generation != 2 {
		t.Errorf("generation moved on a skip: got %d, want 2", afterSkip.Metadata.Generation)
	}

	// A current observation (generation 2) persists and stamps ObservedGeneration.
	current := readCell(t, r)
	current.Status.State = intmodel.CellStateReady
	persisted, err = r.persistCellStatusGuarded(current)
	if err != nil {
		t.Fatalf("guarded persist (current): %v", err)
	}
	if !persisted {
		t.Fatal("guarded persist skipped a current observation")
	}
	final := readCell(t, r)
	if final.Status.ObservedGeneration != 2 {
		t.Errorf("ObservedGeneration = %d, want 2", final.Status.ObservedGeneration)
	}
	if final.Status.State != intmodel.CellStateReady {
		t.Errorf("status flip not persisted: state = %v", final.Status.State)
	}
	if final.Metadata.Generation != 2 {
		t.Errorf("status-only persist bumped generation: got %d, want 2", final.Metadata.Generation)
	}
}
