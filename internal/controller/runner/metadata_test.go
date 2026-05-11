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

//nolint:testpackage // tests the unexported stamp helpers
package runner

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// newMetadataTestExec builds a minimal *Exec wired with a frozen clock
// for the persist-and-read tests below. No containerd, no CNI — the
// metadata-write path only needs a runPath and a logger.
func newMetadataTestExec(t *testing.T, runPath string, now time.Time) *Exec {
	t.Helper()
	return &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:   Options{RunPath: runPath},
		nowFn:  func() time.Time { return now },
	}
}

// TestStampRealmLifecycle pins the issue #166 lifecycle invariants for
// Realm: CreatedAt is set-once (only when zero), UpdatedAt is bumped
// every call, and ReadyAt is set-once on the first State==Ready persist.
// Covered explicitly per-kind because the contract carries identical
// semantics but lives on distinct struct types.
func TestStampRealmLifecycle(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t1.Add(time.Hour)

	t.Run("first_persist_stamps_created_and_updated", func(t *testing.T) {
		r := intmodel.Realm{Status: intmodel.RealmStatus{State: intmodel.RealmStateCreating}}
		stampRealmLifecycle(&r, t0)
		if !r.Status.CreatedAt.Equal(t0) {
			t.Errorf("CreatedAt = %v, want %v", r.Status.CreatedAt, t0)
		}
		if !r.Status.UpdatedAt.Equal(t0) {
			t.Errorf("UpdatedAt = %v, want %v", r.Status.UpdatedAt, t0)
		}
		if !r.Status.ReadyAt.IsZero() {
			t.Errorf("ReadyAt = %v, want zero (not Ready yet)", r.Status.ReadyAt)
		}
	})

	t.Run("created_at_is_set_once", func(t *testing.T) {
		r := intmodel.Realm{Status: intmodel.RealmStatus{
			State:     intmodel.RealmStateCreating,
			CreatedAt: t0,
		}}
		stampRealmLifecycle(&r, t1)
		if !r.Status.CreatedAt.Equal(t0) {
			t.Errorf("CreatedAt moved: got %v, want set-once %v", r.Status.CreatedAt, t0)
		}
		if !r.Status.UpdatedAt.Equal(t1) {
			t.Errorf("UpdatedAt = %v, want %v", r.Status.UpdatedAt, t1)
		}
	})

	t.Run("ready_at_set_on_first_ready", func(t *testing.T) {
		r := intmodel.Realm{Status: intmodel.RealmStatus{
			State:     intmodel.RealmStateReady,
			CreatedAt: t0,
		}}
		stampRealmLifecycle(&r, t1)
		if !r.Status.ReadyAt.Equal(t1) {
			t.Errorf("ReadyAt = %v, want %v", r.Status.ReadyAt, t1)
		}
	})

	t.Run("ready_at_is_set_once_through_reready", func(t *testing.T) {
		// A realm that transitioned to Ready at t1 and is re-persisted
		// later (still Ready, or briefly off-Ready and back) must keep
		// the original ReadyAt — the set-once contract.
		r := intmodel.Realm{Status: intmodel.RealmStatus{
			State:     intmodel.RealmStateReady,
			CreatedAt: t0,
			ReadyAt:   t1,
		}}
		stampRealmLifecycle(&r, t2)
		if !r.Status.ReadyAt.Equal(t1) {
			t.Errorf("ReadyAt moved: got %v, want set-once %v", r.Status.ReadyAt, t1)
		}
		if !r.Status.UpdatedAt.Equal(t2) {
			t.Errorf("UpdatedAt = %v, want %v", r.Status.UpdatedAt, t2)
		}
	})

	t.Run("non_ready_persist_does_not_set_ready_at", func(t *testing.T) {
		r := intmodel.Realm{Status: intmodel.RealmStatus{
			State:     intmodel.RealmStateFailed,
			CreatedAt: t0,
		}}
		stampRealmLifecycle(&r, t1)
		if !r.Status.ReadyAt.IsZero() {
			t.Errorf("ReadyAt = %v, want zero (Failed state)", r.Status.ReadyAt)
		}
	})
}

// TestStampSpaceLifecycle mirrors TestStampRealmLifecycle on Space. The
// per-kind coverage is intentional: the four stampers diverge on the
// state-enum check and a regression that wired a Realm stamper into the
// Space update path would silently never set ReadyAt.
func TestStampSpaceLifecycle(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	t.Run("ready_transition_sets_ready_at", func(t *testing.T) {
		s := intmodel.Space{Status: intmodel.SpaceStatus{State: intmodel.SpaceStateReady}}
		stampSpaceLifecycle(&s, t0)
		if !s.Status.ReadyAt.Equal(t0) {
			t.Errorf("ReadyAt = %v, want %v", s.Status.ReadyAt, t0)
		}
	})

	t.Run("ready_at_set_once", func(t *testing.T) {
		s := intmodel.Space{Status: intmodel.SpaceStatus{
			State:     intmodel.SpaceStateReady,
			CreatedAt: t0,
			ReadyAt:   t0,
		}}
		stampSpaceLifecycle(&s, t1)
		if !s.Status.ReadyAt.Equal(t0) {
			t.Errorf("ReadyAt moved: got %v, want %v", s.Status.ReadyAt, t0)
		}
	})

	t.Run("failed_state_skips_ready_at", func(t *testing.T) {
		s := intmodel.Space{Status: intmodel.SpaceStatus{State: intmodel.SpaceStateFailed}}
		stampSpaceLifecycle(&s, t0)
		if !s.Status.ReadyAt.IsZero() {
			t.Errorf("ReadyAt = %v, want zero", s.Status.ReadyAt)
		}
	})
}

// TestStampStackLifecycle — see TestStampRealmLifecycle for rationale.
func TestStampStackLifecycle(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	t.Run("ready_transition_sets_ready_at", func(t *testing.T) {
		s := intmodel.Stack{Status: intmodel.StackStatus{State: intmodel.StackStateReady}}
		stampStackLifecycle(&s, t0)
		if !s.Status.ReadyAt.Equal(t0) {
			t.Errorf("ReadyAt = %v, want %v", s.Status.ReadyAt, t0)
		}
	})

	t.Run("ready_at_set_once", func(t *testing.T) {
		s := intmodel.Stack{Status: intmodel.StackStatus{
			State:     intmodel.StackStateReady,
			CreatedAt: t0,
			ReadyAt:   t0,
		}}
		stampStackLifecycle(&s, t1)
		if !s.Status.ReadyAt.Equal(t0) {
			t.Errorf("ReadyAt moved: got %v, want %v", s.Status.ReadyAt, t0)
		}
	})
}

// TestStampCellLifecycle — see TestStampRealmLifecycle for rationale.
func TestStampCellLifecycle(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	t.Run("ready_transition_sets_ready_at", func(t *testing.T) {
		c := intmodel.Cell{Status: intmodel.CellStatus{State: intmodel.CellStateReady}}
		stampCellLifecycle(&c, t0)
		if !c.Status.ReadyAt.Equal(t0) {
			t.Errorf("ReadyAt = %v, want %v", c.Status.ReadyAt, t0)
		}
	})

	t.Run("ready_at_set_once_through_stopped_and_back", func(t *testing.T) {
		// Cells flap Ready→Stopped (KillCell race in #275) — the
		// originating ReadyAt must survive the round trip.
		c := intmodel.Cell{Status: intmodel.CellStatus{
			State:     intmodel.CellStateStopped,
			CreatedAt: t0,
			ReadyAt:   t0,
		}}
		stampCellLifecycle(&c, t1)
		if !c.Status.ReadyAt.Equal(t0) {
			t.Errorf("ReadyAt moved through Stopped: got %v, want %v", c.Status.ReadyAt, t0)
		}
	})
}

// TestNowUTCFallsBackToRealClock confirms the nowFn override hook returns
// real time when not set — the production path. Tests that need a frozen
// clock plumb their own nowFn into the Exec.
func TestNowUTCFallsBackToRealClock(t *testing.T) {
	r := &Exec{}
	got := r.nowUTC()
	if got.IsZero() {
		t.Errorf("nowUTC returned zero time")
	}
	if got.Location() != time.UTC {
		t.Errorf("nowUTC returned non-UTC time: %v", got.Location())
	}
}

// TestNowUTCRespectsOverride verifies the hook the tests rely on.
func TestNowUTCRespectsOverride(t *testing.T) {
	fixed := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	r := &Exec{nowFn: func() time.Time { return fixed }}
	if got := r.nowUTC(); !got.Equal(fixed) {
		t.Errorf("nowUTC = %v, want %v", got, fixed)
	}
}

// TestUpdateRealmMetadataPersistsLifecycleFields exercises the full
// stamp → convert → write → read pipeline at the runner level. The
// pure-helper tests above pin the in-memory invariants; this test
// catches a regression where the wiring forgets to call the helper or
// drops a field on the apischeme boundary.
func TestUpdateRealmMetadataPersistsLifecycleFields(t *testing.T) {
	t0 := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	t.Run("first_persist_creating", func(t *testing.T) {
		runPath := t.TempDir()
		r := newMetadataTestExec(t, runPath, t0)
		realm := intmodel.Realm{
			Metadata: intmodel.RealmMetadata{Name: "r-creating"},
			Spec:     intmodel.RealmSpec{Namespace: "r-creating.kukeon.io"},
			Status:   intmodel.RealmStatus{State: intmodel.RealmStateCreating, CgroupPath: "/kukeon/r-creating"},
		}
		if err := r.UpdateRealmMetadata(realm); err != nil {
			t.Fatalf("UpdateRealmMetadata: %v", err)
		}
		got, err := r.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: "r-creating"}})
		if err != nil {
			t.Fatalf("GetRealm: %v", err)
		}
		if !got.Status.CreatedAt.Equal(t0) {
			t.Errorf("CreatedAt = %v, want %v", got.Status.CreatedAt, t0)
		}
		if !got.Status.UpdatedAt.Equal(t0) {
			t.Errorf("UpdatedAt = %v, want %v", got.Status.UpdatedAt, t0)
		}
		if !got.Status.ReadyAt.IsZero() {
			t.Errorf("ReadyAt = %v, want zero (not Ready)", got.Status.ReadyAt)
		}
	})

	t.Run("ready_persist_sets_ready_at", func(t *testing.T) {
		runPath := t.TempDir()
		r := newMetadataTestExec(t, runPath, t0)
		realm := intmodel.Realm{
			Metadata: intmodel.RealmMetadata{Name: "r-ready"},
			Spec:     intmodel.RealmSpec{Namespace: "r-ready.kukeon.io"},
			Status: intmodel.RealmStatus{
				State:                    intmodel.RealmStateReady,
				CgroupPath:               "/kukeon/r-ready",
				CgroupReady:              true,
				ContainerdNamespaceReady: true,
				Reason:                   "RealmReady",
				Message:                  "namespace + cgroup observed",
			},
		}
		if err := r.UpdateRealmMetadata(realm); err != nil {
			t.Fatalf("UpdateRealmMetadata: %v", err)
		}
		got, err := r.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: "r-ready"}})
		if err != nil {
			t.Fatalf("GetRealm: %v", err)
		}
		if !got.Status.ReadyAt.Equal(t0) {
			t.Errorf("ReadyAt = %v, want %v", got.Status.ReadyAt, t0)
		}
		if !got.Status.CgroupReady || !got.Status.ContainerdNamespaceReady {
			t.Errorf("probes not persisted: cgroupReady=%v, namespaceReady=%v",
				got.Status.CgroupReady, got.Status.ContainerdNamespaceReady)
		}
		if got.Status.Reason != "RealmReady" || got.Status.Message != "namespace + cgroup observed" {
			t.Errorf("reason/message lost: %+v", got.Status)
		}
	})

	t.Run("second_persist_keeps_created_and_ready_at", func(t *testing.T) {
		runPath := t.TempDir()
		r0 := newMetadataTestExec(t, runPath, t0)
		realm := intmodel.Realm{
			Metadata: intmodel.RealmMetadata{Name: "r-aging"},
			Spec:     intmodel.RealmSpec{Namespace: "r-aging.kukeon.io"},
			Status:   intmodel.RealmStatus{State: intmodel.RealmStateReady, CgroupPath: "/kukeon/r-aging"},
		}
		if err := r0.UpdateRealmMetadata(realm); err != nil {
			t.Fatalf("first UpdateRealmMetadata: %v", err)
		}
		first, err := r0.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: "r-aging"}})
		if err != nil {
			t.Fatalf("GetRealm after first persist: %v", err)
		}

		// Second persist: simulate the next reconcile tick — same
		// realm, advanced clock, still Ready. CreatedAt and ReadyAt
		// must not move; UpdatedAt must advance to t1.
		r1 := newMetadataTestExec(t, runPath, t1)
		if err = r1.UpdateRealmMetadata(first); err != nil {
			t.Fatalf("second UpdateRealmMetadata: %v", err)
		}
		second, err := r1.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: "r-aging"}})
		if err != nil {
			t.Fatalf("GetRealm after second persist: %v", err)
		}
		if !second.Status.CreatedAt.Equal(t0) {
			t.Errorf("CreatedAt moved: got %v, want %v", second.Status.CreatedAt, t0)
		}
		if !second.Status.ReadyAt.Equal(t0) {
			t.Errorf("ReadyAt moved: got %v, want %v", second.Status.ReadyAt, t0)
		}
		if !second.Status.UpdatedAt.Equal(t1) {
			t.Errorf("UpdatedAt = %v, want %v", second.Status.UpdatedAt, t1)
		}
	})
}

// TestCarryRealmLifecycle covers the helper the refresh path uses to
// carry set-once timestamps + Reason/Message through the locally-built
// newStatus that drops everything not derived from a probe. Without
// this carry, every refresh tick would erase CreatedAt/ReadyAt and
// the next stamping would re-stamp CreatedAt to "now" — losing the
// set-once invariant on every tick.
func TestCarryRealmLifecycle(t *testing.T) {
	t0 := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	orig := intmodel.RealmStatus{
		CreatedAt: t0,
		ReadyAt:   t0.Add(time.Minute),
		UpdatedAt: t0.Add(time.Hour),
		Reason:    "RealmReady",
		Message:   "msg",
	}
	next := intmodel.RealmStatus{State: intmodel.RealmStateReady}
	carryRealmLifecycle(orig, &next)
	if !next.CreatedAt.Equal(orig.CreatedAt) {
		t.Errorf("CreatedAt not carried: got %v, want %v", next.CreatedAt, orig.CreatedAt)
	}
	if !next.ReadyAt.Equal(orig.ReadyAt) {
		t.Errorf("ReadyAt not carried: got %v, want %v", next.ReadyAt, orig.ReadyAt)
	}
	if next.Reason != orig.Reason || next.Message != orig.Message {
		t.Errorf("reason/message not carried: got reason=%q message=%q",
			next.Reason, next.Message)
	}
	// UpdatedAt is intentionally NOT carried — the stamp helper writes
	// the fresh value on every persist. Carrying it here would freeze
	// UpdatedAt at the original value if the post-carry stamp ever got
	// short-circuited.
	if next.UpdatedAt.Equal(orig.UpdatedAt) {
		t.Errorf("UpdatedAt was carried; the stamper would never bump it")
	}
}
