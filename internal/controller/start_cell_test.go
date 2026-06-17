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

package controller_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestStartCell_SuccessfulStart(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.StartCellResult)
		wantErr     bool
	}{
		{
			name:      "successful start",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
					{ID: "container2", Image: "alpine:latest"},
				}
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(cell intmodel.Cell) error {
					if cell.Status.State != intmodel.CellStateReady {
						return errors.New("expected cell state to be Ready after start")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartCellResult) {
				if !result.Started {
					t.Error("expected Started to be true")
				}
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf("expected cell name to be 'test-cell', got %q", result.Cell.Metadata.Name)
				}
				if result.Cell.Status.State != intmodel.CellStateReady {
					t.Errorf("expected cell state to be Ready, got %v", result.Cell.Status.State)
				}
			},
			wantErr: false,
		},
		{
			name:      "successful start with no containers",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				existingCell.Spec.Containers = []intmodel.ContainerSpec{}
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(cell intmodel.Cell) error {
					if cell.Status.State != intmodel.CellStateReady {
						return errors.New("expected cell state to be Ready after start")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartCellResult) {
				if !result.Started {
					t.Error("expected Started to be true")
				}
				if result.Cell.Status.State != intmodel.CellStateReady {
					t.Errorf("expected cell state to be Ready, got %v", result.Cell.Status.State)
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			result, err := ctrl.StartCell(cell)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestStartCell_ReadyStateValidation(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "cell in Ready state with empty Status.Containers returns error (fallback to metadata)",
			cellName:  "ready-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("ready-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady
				existingCell.Status.Containers = []intmodel.ContainerStatus{} // Empty - fallback to metadata check
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantErr:     true,
			errContains: "cell \"ready-cell\" is already in Ready state and must first be stopped",
		},
		{
			name:      "cell in Ready state with running containers returns error",
			cellName:  "ready-cell-running",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("ready-cell-running", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "container1", State: intmodel.ContainerStateReady}, // Actually running
					{ID: "container2", State: intmodel.ContainerStateStopped},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantErr:     true,
			errContains: "cell \"ready-cell-running\" has running containers and must first be stopped",
		},
		{
			name:      "cell in Ready state but all containers stopped allows start",
			cellName:  "ready-cell-stopped",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("ready-cell-stopped", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady // Stale metadata
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "container1", State: intmodel.ContainerStateStopped}, // Actually stopped
					{ID: "container2", State: intmodel.ContainerStateStopped},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			name:      "cell in Ready state with ContainerStateUnknown allows start",
			cellName:  "ready-cell-unknown",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("ready-cell-unknown", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "container1", State: intmodel.ContainerStateUnknown}, // Unknown treated as not running
					{ID: "container2", State: intmodel.ContainerStateStopped},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			name:      "cell in Stopped state can be started",
			cellName:  "stopped-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("stopped-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			// #1268: Pending is genuinely unrecoverable (a create that never
			// completed). StartCell — the single funnel for kuke start/run/restart
			// — refuses it with the delete-then-rerun pointer so all three verbs
			// agree (previously kuke start had no such guard and tried to start).
			name:      "cell in Pending state is rejected with delete pointer",
			cellName:  "pending-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("pending-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStatePending
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantErr:     true,
			errContains: "delete it with `kuke delete cell pending-cell` before restarting",
		},
		{
			// #1268: Unknown (transient containerd misread) is rejected alongside
			// Pending, matching the run/restart CLI guards.
			name:      "cell in Unknown state is rejected with delete pointer",
			cellName:  "unknown-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("unknown-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateUnknown
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantErr:     true,
			errContains: "delete it with `kuke delete cell unknown-cell` before restarting",
		},
		{
			// #1274: Error (workload crash) is recovered through RecreateCell, not
			// a plain StartCell. The crashed cell's root container is sticky and
			// not wound down, so it stays alive — a plain StartCell would trip the
			// "has running containers" guard. RecreateCell stops/deletes the
			// leftover root and rebuilds, which is the only path that brings the
			// cell back up. This reverses #1272's plain-StartCell routing for
			// Error, which could not recover a cell whose root was still live.
			name:      "cell in Error state routes through RecreateCell",
			cellName:  "error-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("error-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateError
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("Error must route through RecreateCell, not StartCell")
				}
				f.RecreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			// #1274: an Error cell with a leftover-running ROOT container must
			// still recover — the running-container guard must NOT fire for it,
			// because the recovery switch claims Error before the guard is
			// reached. Before the fix this returned "has running containers and
			// must first be stopped" instead of recovering.
			name:      "cell in Error state with leftover-running root recovers via RecreateCell",
			cellName:  "error-cell-live-root",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("error-cell-live-root", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateError
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "root", Image: "alpine:latest"},
					{ID: "container1", Image: "alpine:latest"},
				}
				// Root container still running (sticky Error is not wound down),
				// non-root workload crashed.
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "container1", State: intmodel.ContainerStateError},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("Error must route through RecreateCell, not StartCell")
				}
				f.RecreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			// #1268: Failed (kukeon bring-up fault, possibly half-created records)
			// is recovered through RecreateCell, not a plain StartCell.
			name:      "cell in Failed state routes through RecreateCell",
			cellName:  "failed-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("failed-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateFailed
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("Failed must route through RecreateCell, not StartCell")
				}
				f.RecreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			// #1317 review: a Degraded cell (root/sandbox up, a non-root workload
			// down with a preserved non-zero exit) that an operator start/restarts
			// must recover to Ready in a single action. The recovery switch claims
			// Degraded BEFORE the running-container guard, so it routes through
			// RecreateCell like Error/Failed rather than falling through. Before the
			// fix Degraded fell through; the live root + live sidecar then tripped
			// the "has running containers and must first be stopped" guard, leaving
			// the operator stuck at Error N/N after the daemon re-derived the
			// preserved crash exit — the inverse of the Ready 1/2 contradiction
			// Degraded was introduced to kill. RecreateCell stops/deletes the
			// leftover containers and rebuilds, the only path that lands Ready.
			name:      "cell in Degraded state with live root and sidecar recovers via RecreateCell",
			cellName:  "degraded-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("degraded-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateDegraded
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "root", Image: "alpine:latest"},
					{ID: "sidecar", Image: "alpine:latest"},
					{ID: "job", Image: "alpine:latest"},
				}
				// Root + sidecar still running (a Degraded cell is partially live);
				// the job crashed and is held down.
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "sidecar", State: intmodel.ContainerStateReady},
					{ID: "job", State: intmodel.ContainerStateError, ExitCode: 137},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("Degraded must route through RecreateCell, not StartCell")
				}
				f.RecreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			// #1316: an Exited cell whose non-root workload cleanly exited under a
			// restart-policy that vetoes wind-down (on-failure clean exit, or
			// never) deliberately keeps its ROOT container alive (#1003). Exited is
			// NOT in the recovery switch above (its container records are intact and
			// re-runnable), so it falls through to this guard — which must exclude
			// the live root and allow the start, restarting the root rather than
			// refusing. Before the fix the running root tripped "has running
			// containers and must first be stopped", wedging the cell (it would
			// neither wind down nor restart). The fix narrows the guard to live
			// non-root workloads.
			name:      "cell in Exited state with restart-policy-pinned live root allows start",
			cellName:  "exited-cell-live-root",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("exited-cell-live-root", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateExited
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "root", Image: "alpine:latest", Root: true},
					{ID: "container1", Image: "alpine:latest"},
				}
				// Root still running (restart-policy veto leaves it alive), non-root
				// workload exited cleanly — the Ready 1/2 shape from the repro.
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "container1", State: intmodel.ContainerStateExited},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				// The start must go through a plain StartCell (Exited is not a
				// recovery-switch state) — assert it is reached, not refused.
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			// #1316 guardrail: a root-only cell (kukeond-style, no non-root spec)
			// keeps the root in the running-container check — there the root IS the
			// workload, so a live root must still block a duplicate start. This pins
			// that the root-exclusion is gated on hasNonRootContainerSpec and does
			// not leak into root-only cells.
			name:      "root-only cell with running root still blocks start",
			cellName:  "root-only-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("root-only-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "root", Image: "alpine:latest", Root: true},
				}
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantErr:     true,
			errContains: "cell \"root-only-cell\" has running containers and must first be stopped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			_, err := ctrl.StartCell(cell)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.errContains != "" {
				errStr := err.Error()
				found := false
				for i := 0; i <= len(errStr)-len(tt.errContains); i++ {
					if i+len(tt.errContains) <= len(errStr) && errStr[i:i+len(tt.errContains)] == tt.errContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error message to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

// TestStartCell_DegradedRecoversToReadyInOneAction pins the #1317-review fix: a
// single operator start/restart of a Degraded cell lands Ready, not a stuck
// Error N/N. It asserts the recovery routing concretely — RecreateCell is the
// path taken (not a plain StartCell), and the returned cell is Ready — so a
// regression that drops Degraded back out of the recovery switch (falling
// through to a plain start that the daemon re-derives into sticky Error) is
// caught here rather than only on a live host.
func TestStartCell_DegradedRecoversToReadyInOneAction(t *testing.T) {
	existingCell := buildTestCell("degraded-recover", "test-realm", "test-space", "test-stack")
	existingCell.Status.State = intmodel.CellStateDegraded
	existingCell.Spec.Containers = []intmodel.ContainerSpec{
		{ID: "root", Image: "alpine:latest"},
		{ID: "sidecar", Image: "alpine:latest"},
		{ID: "job", Image: "alpine:latest"},
	}
	existingCell.Status.Containers = []intmodel.ContainerStatus{
		{ID: "root", State: intmodel.ContainerStateReady},
		{ID: "sidecar", State: intmodel.ContainerStateReady},
		{ID: "job", State: intmodel.ContainerStateError, ExitCode: 137},
	}

	var recreateCalled, startCalled bool
	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return existingCell, nil
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return true, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return true, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			startCalled = true
			return c, nil
		},
		RecreateCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			// RecreateCell's start phase funnels through markCellReady, which
			// lands the cell Ready and clears the failure breadcrumb.
			c.Status.State = intmodel.CellStateReady
			return c, nil
		},
		UpdateCellMetadataFn: func(_ intmodel.Cell) error {
			return nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	cell := buildTestCell("degraded-recover", "test-realm", "test-space", "test-stack")

	res, err := ctrl.StartCell(cell)
	if err != nil {
		t.Fatalf("StartCell on a Degraded cell: unexpected error: %v", err)
	}
	if !recreateCalled {
		t.Error("RecreateCell was not called — a Degraded cell must recover through the recreate path")
	}
	if startCalled {
		t.Error("plain StartCell was called — a Degraded cell must route through RecreateCell, not a plain start")
	}
	if res.Cell.Status.State != intmodel.CellStateReady {
		t.Errorf("recovered cell state = %v, want Ready (a single recovery action must land Ready, not a stuck Error)",
			res.Cell.Status.State)
	}
	if !res.Started {
		t.Error("res.Started = false, want true (the recovery brought the cell up)")
	}
}

func TestStartCell_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		cellName  string
		realmName string
		spaceName string
		stackName string
		wantErr   error
	}{
		{
			name:      "empty cell name returns ErrCellNameRequired",
			cellName:  "",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			wantErr:   errdefs.ErrCellNameRequired,
		},
		{
			name:      "whitespace-only cell name returns ErrCellNameRequired",
			cellName:  "   ",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			wantErr:   errdefs.ErrCellNameRequired,
		},
		{
			name:      "empty realm name returns ErrRealmNameRequired",
			cellName:  "test-cell",
			realmName: "",
			spaceName: "test-space",
			stackName: "test-stack",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only realm name returns ErrRealmNameRequired",
			cellName:  "test-cell",
			realmName: "   ",
			spaceName: "test-space",
			stackName: "test-stack",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "empty space name returns ErrSpaceNameRequired",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "",
			stackName: "test-stack",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "whitespace-only space name returns ErrSpaceNameRequired",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "   ",
			stackName: "test-stack",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "empty stack name returns ErrStackNameRequired",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "",
			wantErr:   errdefs.ErrStackNameRequired,
		},
		{
			name:      "whitespace-only stack name returns ErrStackNameRequired",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "   ",
			wantErr:   errdefs.ErrStackNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			_, err := ctrl.StartCell(cell)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestStartCell_CellNotFound(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "cell not found - ErrCellNotFound",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				// GetCell returns ErrCellNotFound (called by validateAndGetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
			},
			wantErr:     true,
			errContains: "cell \"test-cell\" not found in realm \"test-realm\", space \"test-space\", stack \"test-stack\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			result, err := ctrl.StartCell(cell)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if result.Started {
				t.Error("expected Started to be false when error occurs")
			}

			if tt.errContains != "" {
				errStr := err.Error()
				found := false
				for i := 0; i <= len(errStr)-len(tt.errContains); i++ {
					if i+len(tt.errContains) <= len(errStr) && errStr[i:i+len(tt.errContains)] == tt.errContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error message to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

func TestStartCell_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "GetCell error (non-NotFound) is returned",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				// GetCell error (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("unexpected error")
				}
			},
			wantErr:     true,
			errContains: "unexpected error",
		},
		{
			name:      "ExistsCgroup error from GetCell is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     true,
			errContains: "failed to check if cell cgroup exists",
		},
		{
			name:      "ExistsCellRootContainer error from GetCell is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return false, errors.New("root container check failed")
				}
			},
			wantErr:     true,
			errContains: "failed to check root container",
		},
		{
			name:      "StartCell runner error is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("start failed")
				}
			},
			wantErr:     true,
			errContains: "failed to start cell containers",
		},
		{
			name:      "UpdateCellMetadata runner error is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return errors.New("metadata update failed")
				}
			},
			wantErr:     true,
			errContains: "failed to update cell metadata",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			_, err := ctrl.StartCell(cell)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.errContains != "" {
				errStr := err.Error()
				found := false
				for i := 0; i <= len(errStr)-len(tt.errContains); i++ {
					if i+len(tt.errContains) <= len(errStr) && errStr[i:i+len(tt.errContains)] == tt.errContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error message to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

func TestStartCell_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.StartCellResult)
		wantErr     bool
	}{
		{
			name:      "cell name, realm name, space name, and stack name with leading/trailing whitespace are trimmed",
			cellName:  "  test-cell  ",
			realmName: "  test-realm  ",
			spaceName: "  test-space  ",
			stackName: "  test-stack  ",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify trimmed names are used
					if cell.Metadata.Name != "test-cell" {
						return intmodel.Cell{}, errors.New("unexpected cell name")
					}
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartCellResult) {
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf("expected cell name to be trimmed to 'test-cell', got %q", result.Cell.Metadata.Name)
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			result, err := ctrl.StartCell(cell)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}
