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

package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestRootContainerWantsCNI is the gate StartCell uses before any CNI work
// (NewManager, LoadNetworkConfigList, AddContainerToNetwork). The negative
// case is the kukeond invariant from issue #96 — host-netns containers must
// not be CNI-attached, otherwise the bridge plugin runs in the daemon's own
// netns and the host loses visibility of the cell's veths and iptables rules.
func TestRootContainerWantsCNI(t *testing.T) {
	tests := []struct {
		name string
		spec intmodel.ContainerSpec
		want bool
	}{
		{
			name: "default container goes through CNI attach",
			spec: intmodel.ContainerSpec{ID: "c1"},
			want: true,
		},
		{
			name: "privileged-only container still goes through CNI",
			spec: intmodel.ContainerSpec{ID: "c2", Privileged: true},
			want: true,
		},
		{
			name: "host network container skips CNI attach",
			spec: intmodel.ContainerSpec{ID: "kukeond", HostNetwork: true},
			want: false,
		},
		{
			name: "host network + privileged skips CNI attach",
			spec: intmodel.ContainerSpec{ID: "kukeond", HostNetwork: true, Privileged: true},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rootContainerWantsCNI(tt.spec); got != tt.want {
				t.Errorf("rootContainerWantsCNI(%+v) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

// TestCellWantsHostNetworkRoot covers the propagation rule that makes the
// auto-default root container host-network whenever any container in the
// cell asked for HostNetwork=true. Without this, the kukeond container
// would join the netns of a busybox sleep root that has its own
// per-container netns — exactly the divergence issue #96 fixes.
func TestCellWantsHostNetworkRoot(t *testing.T) {
	tests := []struct {
		name string
		cell intmodel.Cell
		want bool
	}{
		{
			name: "empty containers list",
			cell: intmodel.Cell{},
			want: false,
		},
		{
			name: "all containers default network",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{Containers: []intmodel.ContainerSpec{
				{ID: "a"}, {ID: "b"},
			}}},
			want: false,
		},
		{
			name: "one container wants host network",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{Containers: []intmodel.ContainerSpec{
				{ID: "a"}, {ID: "kukeond", HostNetwork: true},
			}}},
			want: true,
		},
		{
			name: "single host-network container",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{Containers: []intmodel.ContainerSpec{
				{ID: "kukeond", HostNetwork: true},
			}}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellWantsHostNetworkRoot(tt.cell); got != tt.want {
				t.Errorf("cellWantsHostNetworkRoot() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidateExplicitRootHostNetwork covers the explicit-root branch's
// host-network alignment guard from issue #103. Without it, a peer with
// HostNetwork=true alongside an explicit non-host root would silently lose
// its host-network intent — peers join the root's netns via
// JoinContainerNamespaces, so the root owns the decision.
func TestValidateExplicitRootHostNetwork(t *testing.T) {
	tests := []struct {
		name     string
		cell     intmodel.Cell
		rootSpec intmodel.ContainerSpec
		wantErr  bool
	}{
		{
			name: "explicit root, no peer wants host-network",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{
				RootContainerID: "c2",
				Containers: []intmodel.ContainerSpec{
					{ID: "c1"}, {ID: "c2"},
				},
			}},
			rootSpec: intmodel.ContainerSpec{ID: "c2"},
			wantErr:  false,
		},
		{
			name: "explicit root host-network, peers default — fine, peers join host netns",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{
				RootContainerID: "c2",
				Containers: []intmodel.ContainerSpec{
					{ID: "c1"}, {ID: "c2", HostNetwork: true},
				},
			}},
			rootSpec: intmodel.ContainerSpec{ID: "c2", HostNetwork: true},
			wantErr:  false,
		},
		{
			name: "explicit root host-network, peer also host-network — aligned",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{
				RootContainerID: "c2",
				Containers: []intmodel.ContainerSpec{
					{ID: "c1", HostNetwork: true}, {ID: "c2", HostNetwork: true},
				},
			}},
			rootSpec: intmodel.ContainerSpec{ID: "c2", HostNetwork: true},
			wantErr:  false,
		},
		{
			name: "peer wants host-network but explicit root does not — reject",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{
				RootContainerID: "c2",
				Containers: []intmodel.ContainerSpec{
					{ID: "c1", HostNetwork: true}, {ID: "c2"},
				},
			}},
			rootSpec: intmodel.ContainerSpec{ID: "c2"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExplicitRootHostNetwork(tt.cell, tt.rootSpec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateExplicitRootHostNetwork() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, internalerrdefs.ErrExplicitRootHostNetworkMismatch) {
				t.Errorf("err = %v, want wrapped ErrExplicitRootHostNetworkMismatch", err)
			}
		})
	}
}

// TestCellTasksAllRunningFn pins the StartCell idempotency guard from
// issue #149: only every-task-running yields a no-op skip. Anything else
// — root not running, root status error, a non-root in any non-Running
// state, a non-root with an empty ContainerdID — has to fall through to
// the destructive teardown-and-recreate path so a wedged cell can still
// recover.
func TestCellTasksAllRunningFn(t *testing.T) {
	const rootID = "space_stack_cell_root"

	statusOf := func(states map[string]containerd.ProcessStatus) func(string) (containerd.Status, error) {
		return func(id string) (containerd.Status, error) {
			s, ok := states[id]
			if !ok {
				return containerd.Status{}, fmt.Errorf("no task for %q", id)
			}
			return containerd.Status{Status: s}, nil
		}
	}

	cellWithPeers := func(peers ...intmodel.ContainerSpec) intmodel.Cell {
		return intmodel.Cell{Spec: intmodel.CellSpec{Containers: peers}}
	}

	tests := []struct {
		name string
		cell intmodel.Cell
		fn   func(string) (containerd.Status, error)
		want bool
	}{
		{
			name: "root running, no non-root containers — already up",
			cell: intmodel.Cell{},
			fn:   statusOf(map[string]containerd.ProcessStatus{rootID: containerd.Running}),
			want: true,
		},
		{
			name: "root running and all non-roots running — already up",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "a", ContainerdID: "cid_a"},
				intmodel.ContainerSpec{ID: "b", ContainerdID: "cid_b"},
			),
			fn: statusOf(map[string]containerd.ProcessStatus{
				rootID:  containerd.Running,
				"cid_a": containerd.Running,
				"cid_b": containerd.Running,
			}),
			want: true,
		},
		{
			name: "explicit-root entry in Containers list is skipped",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "root", Root: true, ContainerdID: "ignored"},
				intmodel.ContainerSpec{ID: "a", ContainerdID: "cid_a"},
			),
			fn: statusOf(map[string]containerd.ProcessStatus{
				rootID:  containerd.Running,
				"cid_a": containerd.Running,
			}),
			want: true,
		},
		{
			name: "root TaskStatus errors — not up",
			cell: intmodel.Cell{},
			fn:   statusOf(map[string]containerd.ProcessStatus{}),
			want: false,
		},
		{
			name: "root stopped — not up",
			cell: intmodel.Cell{},
			fn:   statusOf(map[string]containerd.ProcessStatus{rootID: containerd.Stopped}),
			want: false,
		},
		{
			name: "root running, one non-root stopped — not up",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "a", ContainerdID: "cid_a"},
				intmodel.ContainerSpec{ID: "b", ContainerdID: "cid_b"},
			),
			fn: statusOf(map[string]containerd.ProcessStatus{
				rootID:  containerd.Running,
				"cid_a": containerd.Running,
				"cid_b": containerd.Stopped,
			}),
			want: false,
		},
		{
			name: "non-root has empty ContainerdID — not up",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "a", ContainerdID: ""},
			),
			fn:   statusOf(map[string]containerd.ProcessStatus{rootID: containerd.Running}),
			want: false,
		},
		{
			name: "non-root TaskStatus errors — not up",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "a", ContainerdID: "cid_a"},
			),
			fn:   statusOf(map[string]containerd.ProcessStatus{rootID: containerd.Running}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellTasksAllRunningFn(tt.cell, rootID, tt.fn); got != tt.want {
				t.Errorf("cellTasksAllRunningFn() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTeardownRootContainerCNI_OrderingAndSafetyNet is the regression guard for
// issue #630. Both StartCell's teardown-before-recreate branch and RecreateCell
// route their root-container teardown through teardownRootContainerCNI, which
// must run CNI DEL (releaseCNI) *before* deleting the container so host-local
// IPAM releases the reservation ahead of the re-ADD against the same
// deterministic containerd ID — otherwise the re-ADD is rejected as a duplicate
// allocation. The IPAM-file purge safety net must run *after* the delete, and
// must run even when the delete fails so a leaked allocation file is still
// scrubbed.
func TestTeardownRootContainerCNI_OrderingAndSafetyNet(t *testing.T) {
	t.Run("release_runs_before_delete_purge_runs_after", func(t *testing.T) {
		var order []string
		err := teardownRootContainerCNI(
			func() { order = append(order, "release") },
			func() error { order = append(order, "delete"); return nil },
			func() { order = append(order, "purge") },
		)
		if err != nil {
			t.Fatalf("teardownRootContainerCNI returned error: %v", err)
		}
		want := []string{"release", "delete", "purge"}
		if strings.Join(order, ",") != strings.Join(want, ",") {
			t.Errorf("call order = %v, want %v", order, want)
		}
	})

	t.Run("purge_runs_and_error_propagates_when_delete_fails", func(t *testing.T) {
		var order []string
		sentinel := errors.New("delete failed")
		err := teardownRootContainerCNI(
			func() { order = append(order, "release") },
			func() error { order = append(order, "delete"); return sentinel },
			func() { order = append(order, "purge") },
		)
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want %v", err, sentinel)
		}
		want := []string{"release", "delete", "purge"}
		if strings.Join(order, ",") != strings.Join(want, ",") {
			t.Errorf("call order = %v, want %v (purge must run even on delete failure)", order, want)
		}
	})
}

// TestTruncateFailureMessage pins the single-line + length-bounded contract
// markCellFailed relies on for Status.Message: long, multi-line wrapped
// `fmt.Errorf("%w: %w", …)` chains must end up as a single, capped string
// so `kuke get cell -o yaml` stays human-readable (issue #504).
func TestTruncateFailureMessage(t *testing.T) {
	t.Run("nil_cause_returns_empty", func(t *testing.T) {
		if got := truncateFailureMessage(nil); got != "" {
			t.Errorf("truncateFailureMessage(nil) = %q, want \"\"", got)
		}
	})

	t.Run("newlines_collapsed_to_space", func(t *testing.T) {
		err := errors.New("first line\nsecond line\twith tab")
		got := truncateFailureMessage(err)
		if strings.ContainsAny(got, "\n\r\t") {
			t.Errorf("truncateFailureMessage left control whitespace in %q", got)
		}
		if got != "first line second line with tab" {
			t.Errorf("truncateFailureMessage() = %q, want collapsed", got)
		}
	})

	t.Run("long_cause_is_truncated_with_indicator", func(t *testing.T) {
		long := strings.Repeat("a", maxFailureMessageLen+50)
		got := truncateFailureMessage(errors.New(long))
		if len(got) > maxFailureMessageLen {
			t.Errorf("truncateFailureMessage returned %d bytes, want ≤ %d", len(got), maxFailureMessageLen)
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("truncated message %q missing trailing ellipsis indicator", got[len(got)-10:])
		}
	})

	t.Run("short_cause_kept_verbatim", func(t *testing.T) {
		err := fmt.Errorf("pull image: %w", errors.New("connection refused"))
		got := truncateFailureMessage(err)
		if got != "pull image: connection refused" {
			t.Errorf("truncateFailureMessage() = %q, want verbatim", got)
		}
	})
}

// TestMarkCellFailed_PersistsReasonAndMessage verifies the helper writes
// State=Failed, Status.Reason, and Status.Message onto the on-disk cell
// document. Without this, the operator-facing reason for the failure stays
// trapped in daemon logs and `kuke get cell -o yaml` shows a derived
// Stopped (or worse, a Failed cell with empty Reason/Message). Issue #504.
//
// The helper internally invokes KillCell, which requires containerd
// connectivity the unit test does not have — KillCell's error is logged at
// WARN and swallowed by markCellFailed, so the persist step still runs.
// The test is deliberately not asserting on KillCell behavior.
func TestMarkCellFailed_PersistsReasonAndMessage(t *testing.T) {
	t0 := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		reason      string
		cause       error
		wantMessage string
	}{
		{
			name:        "create_cell_failed",
			reason:      "CreateCellFailed",
			cause:       fmt.Errorf("createCellContainers: %w", errors.New("image pull: connection refused")),
			wantMessage: "createCellContainers: image pull: connection refused",
		},
		{
			name:        "start_cell_failed",
			reason:      "StartCellFailed",
			cause:       fmt.Errorf("failed to attach root container: %w", errors.New("cni: bridge plugin missing")),
			wantMessage: "failed to attach root container: cni: bridge plugin missing",
		},
		{
			name:        "start_container_failed",
			reason:      "StartContainerFailed",
			cause:       errors.New("failed to start container worker: containerd: task already exists"),
			wantMessage: "failed to start container worker: containerd: task already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			r := newMetadataTestExec(t, runPath, t0)
			cell := intmodel.Cell{
				Metadata: intmodel.CellMetadata{Name: "c-" + tt.name},
				Spec: intmodel.CellSpec{
					ID:         "c-" + tt.name,
					RealmName:  "default",
					SpaceName:  "default",
					StackName:  "default",
					Containers: []intmodel.ContainerSpec{},
				},
				Status: intmodel.CellStatus{
					State: intmodel.CellStatePending,
				},
			}
			if err := r.UpdateCellMetadata(cell); err != nil {
				t.Fatalf("UpdateCellMetadata (stub write): %v", err)
			}

			r.markCellFailed(cell, tt.reason, tt.cause)

			got, err := r.GetCell(cell)
			if err != nil {
				t.Fatalf("GetCell: %v", err)
			}
			if got.Status.State != intmodel.CellStateFailed {
				t.Errorf("Status.State = %v, want Failed", got.Status.State)
			}
			if got.Status.Reason != tt.reason {
				t.Errorf("Status.Reason = %q, want %q", got.Status.Reason, tt.reason)
			}
			if got.Status.Message != tt.wantMessage {
				t.Errorf("Status.Message = %q, want %q", got.Status.Message, tt.wantMessage)
			}
		})
	}
}

// TestMarkCellFailed_LongCauseIsTruncated covers a real-world repro: a
// chain of wrapped errors from CNI / IPAM / containerd can balloon past a
// few hundred bytes. The Message field must stay capped + single-line on
// disk so YAML emit doesn't bury the rest of `kuke get cell -o yaml`
// output. Issue #504.
func TestMarkCellFailed_LongCauseIsTruncated(t *testing.T) {
	t0 := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, t0)

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "c-long"},
		Spec: intmodel.CellSpec{
			ID:         "c-long",
			RealmName:  "default",
			SpaceName:  "default",
			StackName:  "default",
			Containers: []intmodel.ContainerSpec{},
		},
		Status: intmodel.CellStatus{State: intmodel.CellStatePending},
	}
	if err := r.UpdateCellMetadata(cell); err != nil {
		t.Fatalf("UpdateCellMetadata: %v", err)
	}

	cause := errors.New(strings.Repeat("x", maxFailureMessageLen*2))
	r.markCellFailed(cell, "CreateCellFailed", cause)

	got, err := r.GetCell(cell)
	if err != nil {
		t.Fatalf("GetCell: %v", err)
	}
	if got.Status.State != intmodel.CellStateFailed {
		t.Errorf("Status.State = %v, want Failed", got.Status.State)
	}
	if got.Status.Reason != "CreateCellFailed" {
		t.Errorf("Status.Reason = %q, want CreateCellFailed", got.Status.Reason)
	}
	if len(got.Status.Message) > maxFailureMessageLen {
		t.Errorf("Status.Message length = %d, want ≤ %d", len(got.Status.Message), maxFailureMessageLen)
	}
	if !strings.HasSuffix(got.Status.Message, "...") {
		t.Errorf(
			"Status.Message missing truncation indicator: tail=%q",
			got.Status.Message[len(got.Status.Message)-10:],
		)
	}
}

// TestMarkCellFailed_PreservesFieldsAcrossRefreshCarry guards the
// reason/message carry on the refresh path. `carryCellLifecycle` already
// copies Reason+Message from originalStatus to newStatus, but a regression
// that wires a Failed-state writer in front of a refresh tick that drops
// the field would leave the operator-facing breadcrumb empty after one
// reconciler pass. The test simulates the carry by manually invoking
// the helper and then mimicking the refresh-path `newStatus :=` reset.
// Issue #504 acceptance criterion #4.
func TestMarkCellFailed_PreservesFieldsAcrossRefreshCarry(t *testing.T) {
	t0 := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, t0)
	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "c-carry"},
		Spec: intmodel.CellSpec{
			ID:         "c-carry",
			RealmName:  "default",
			SpaceName:  "default",
			StackName:  "default",
			Containers: []intmodel.ContainerSpec{},
		},
		Status: intmodel.CellStatus{State: intmodel.CellStatePending},
	}
	if err := r.UpdateCellMetadata(cell); err != nil {
		t.Fatalf("UpdateCellMetadata: %v", err)
	}

	r.markCellFailed(cell, "CreateCellFailed", errors.New("createCellContainers: image not found"))

	got, err := r.GetCell(cell)
	if err != nil {
		t.Fatalf("GetCell: %v", err)
	}

	// Simulate a refresh tick: refresh builds newStatus from scratch then
	// calls carryCellLifecycle to keep set-once fields. If the carry ever
	// stops copying Reason/Message, this assert catches it.
	newStatus := intmodel.CellStatus{State: intmodel.CellStateFailed}
	carryCellLifecycle(got.Status, &newStatus)
	if newStatus.Reason != "CreateCellFailed" {
		t.Errorf("carryCellLifecycle dropped Reason: got %q", newStatus.Reason)
	}
	if newStatus.Message != "createCellContainers: image not found" {
		t.Errorf("carryCellLifecycle dropped Message: got %q", newStatus.Message)
	}
}

// TestMarkCellFailed_PreStampsBeforeKillCell pins the issue #409 ordering:
// markCellFailed must persist State=Failed *before* calling KillCell. Without
// the pre-stamp, the on-disk state stays at its pre-failure value (e.g.
// Ready) until KillCell's own PopulateAndPersistCellContainerStatuses writes
// Stopped near the end of its run; on an AutoDelete + ReadyObserved cell a
// reconcile tick that lands in that window would trip shouldAutoDeleteCell
// and reap the cell before the post-kill Failed-stamp lands.
//
// We probe the ordering by counting UpdateCellMetadata calls during the
// helper's run. nowFn is bumped once per persist via stampCellLifecycle, so
// the call counter is a direct proxy for "how many UpdateCellMetadata writes
// happened". KillCell errors out at ensureClientConnected in the unit-test
// harness (no real containerd socket) so KillCell's own persist is skipped
// — that leaves the counter as a clean probe for pre-stamp + post-kill
// restamp.
//
// One write means the pre-stamp regressed (only the post-kill restamp ran).
// Two writes means both the pre-stamp and the post-kill restamp ran, which
// is the fixed contract.
func TestMarkCellFailed_PreStampsBeforeKillCell(t *testing.T) {
	base := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	var callCount int64
	runPath := t.TempDir()
	r := &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:   Options{RunPath: runPath},
		nowFn: func() time.Time {
			n := atomic.AddInt64(&callCount, 1)
			return base.Add(time.Duration(n) * time.Second)
		},
	}

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "c-prekill"},
		Spec: intmodel.CellSpec{
			ID:         "c-prekill",
			RealmName:  "default",
			SpaceName:  "default",
			StackName:  "default",
			AutoDelete: true,
			Containers: []intmodel.ContainerSpec{},
		},
		Status: intmodel.CellStatus{
			State:         intmodel.CellStateReady,
			ReadyObserved: true,
		},
	}
	if err := r.UpdateCellMetadata(cell); err != nil {
		t.Fatalf("UpdateCellMetadata seed: %v", err)
	}
	seed := atomic.LoadInt64(&callCount)

	r.markCellFailed(cell, "StartContainerFailed", errors.New("createContainer: boom"))

	got, err := r.GetCell(cell)
	if err != nil {
		t.Fatalf("GetCell: %v", err)
	}
	if got.Status.State != intmodel.CellStateFailed {
		t.Errorf("Status.State = %v, want Failed", got.Status.State)
	}
	if !got.Status.ReadyObserved {
		t.Errorf("Status.ReadyObserved = false, want true (one-way latch preserved across Failed transition)")
	}

	if delta := atomic.LoadInt64(&callCount) - seed; delta != 2 {
		t.Errorf(
			"UpdateCellMetadata writes during markCellFailed = %d, want 2 (pre-stamp + post-kill restamp). Issue #409.",
			delta,
		)
	}
}
