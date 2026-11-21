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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestPatchCellMetadata_WritesOnlyWhenMutateReturnsTrue pins D1 of issue
// #1569: the patch hands mutate the persisted document and writes the
// mutated copy back only when mutate returns true.
func TestPatchCellMetadata_WritesOnlyWhenMutateReturnsTrue(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seed := seedCell(t, r)

	if err := r.PatchCellMetadata(seed, func(fresh *intmodel.Cell) bool {
		fresh.Status.OutOfSync = true
		return false
	}); err != nil {
		t.Fatalf("PatchCellMetadata (no write): %v", err)
	}
	if readCell(t, r).Status.OutOfSync {
		t.Fatal("mutate returned false but the change reached disk")
	}

	if err := r.PatchCellMetadata(seed, func(fresh *intmodel.Cell) bool {
		if fresh.Spec.ID != "cell-1" {
			t.Errorf("mutate saw Spec.ID %q, want the persisted cell-1", fresh.Spec.ID)
		}
		fresh.Status.OutOfSync = true
		fresh.Status.OutOfSyncReason = "config changed"
		return true
	}); err != nil {
		t.Fatalf("PatchCellMetadata: %v", err)
	}
	got := readCell(t, r)
	if !got.Status.OutOfSync || got.Status.OutOfSyncReason != "config changed" {
		t.Errorf("patched status = %+v, want OutOfSync with reason", got.Status)
	}
	if got.Metadata.Generation != 1 {
		t.Errorf("status-only patch bumped generation to %d, want 1", got.Metadata.Generation)
	}
}

// TestPatchCellMetadata_KeepsAnInterveningLockedWrite is case 11 of issue
// #1569 at the runner level: an OutOfSync verdict computed from an earlier
// snapshot, then a stop that persists, then the patch. The patch starts from
// the fresh document, so the stop's state survives and only the OutOfSync
// fields change.
func TestPatchCellMetadata_KeepsAnInterveningLockedWrite(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seedCell(t, r)
	snapshot := readCell(t, r) // the tick's view

	stopped := readCell(t, r)
	stopped.Status.State = intmodel.CellStateStopped
	stopped.Status.Reason = "OperatorStopped"
	if err := r.UpdateCellMetadata(stopped); err != nil {
		t.Fatalf("stop persist: %v", err)
	}

	if err := r.PatchCellMetadata(snapshot, func(fresh *intmodel.Cell) bool {
		fresh.Status.OutOfSync = true
		fresh.Status.OutOfSyncReason = "spec differs at containers[work].args"
		return true
	}); err != nil {
		t.Fatalf("PatchCellMetadata: %v", err)
	}
	got := readCell(t, r)
	if got.Status.State != intmodel.CellStateStopped || got.Status.Reason != "OperatorStopped" {
		t.Errorf("stop's state lost: state=%v reason=%q", got.Status.State, got.Status.Reason)
	}
	if !got.Status.OutOfSync {
		t.Error("OutOfSync not recorded")
	}
}

// TestPatchCellMetadata_DeletedCellStaysDeleted is case 12 (AC8) for the
// patch: a patch that lands after DeleteCell removed the document returns
// errdefs.ErrCellNotFound and leaves no metadata file behind.
func TestPatchCellMetadata_DeletedCellStaysDeleted(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seed := seedCell(t, r)
	path := fs.CellMetadataPath(r.opts.RunPath, "r1", "s1", "st1", "c1")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove cell metadata: %v", err)
	}

	called := false
	err := r.PatchCellMetadata(seed, func(_ *intmodel.Cell) bool {
		called = true
		return true
	})
	if !errors.Is(err, errdefs.ErrCellNotFound) {
		t.Fatalf("PatchCellMetadata error = %v, want ErrCellNotFound", err)
	}
	if called {
		t.Error("mutate ran for a deleted cell")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("cell metadata recreated by a patch after delete: stat err = %v", statErr)
	}
}

// TestPersistCellStatusGuarded_DeletedCellStaysDeleted is case 12 (AC8) for
// the reconciler's status persist: a write from a snapshot listed before
// DeleteCell is a benign skip, not a resurrection.
func TestPersistCellStatusGuarded_DeletedCellStaysDeleted(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seedCell(t, r)
	listed := readCell(t, r)
	path := fs.CellMetadataPath(r.opts.RunPath, "r1", "s1", "st1", "c1")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove cell metadata: %v", err)
	}

	listed.Status.State = intmodel.CellStateReady
	persisted, err := r.persistCellStatusGuarded(listed)
	if err != nil {
		t.Fatalf("persistCellStatusGuarded: %v", err)
	}
	if persisted {
		t.Error("persisted=true for a deleted cell")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("cell metadata recreated by a status write after delete: stat err = %v", statErr)
	}
}

// TestUpdateCellMetadata_StillCreatesFirstDocument guards the create flows:
// UpdateCellMetadata keeps its initial-write branch (missingErr nil), so
// create_cell.go and provision.go can still write a cell's first document.
func TestUpdateCellMetadata_StillCreatesFirstDocument(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seedCell(t, r)
	if got := readCell(t, r).Metadata.Generation; got != 1 {
		t.Fatalf("first write generation = %d, want 1", got)
	}
}

// TestPatchCellMetadata_WaitsForTheCellLock pins that the patch serialises
// with the lifecycle ops: while another holder has the per-cell lock, the
// patch blocks.
func TestPatchCellMetadata_WaitsForTheCellLock(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	seed := seedCell(t, r)

	release := r.lockCell(seed)
	done := make(chan error, 1)
	go func() {
		done <- r.PatchCellMetadata(seed, func(fresh *intmodel.Cell) bool {
			fresh.Status.OutOfSync = true
			return true
		})
	}()
	select {
	case <-done:
		t.Fatal("PatchCellMetadata returned while the per-cell lock was held")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PatchCellMetadata: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PatchCellMetadata did not proceed after the lock was released")
	}
}
