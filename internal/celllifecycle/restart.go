// Copyright 2026 Emiliano Spinella (eminwux)
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

// Package celllifecycle contains shared cell lifecycle orchestration used by
// the CLI and the local web interface. kukeond remains authoritative for the
// actual state transitions.
package celllifecycle

import (
	"context"
	"fmt"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Phase identifies the step at which a restart failed.
type Phase string

const (
	PhaseLookup     Phase = "lookup"
	PhaseValidation Phase = "validation"
	PhaseStopping   Phase = "stopping"
	PhaseStarting   Phase = "starting"
)

// PhaseError preserves the daemon error while identifying the restart step.
// Error deliberately returns the original message so the CLI keeps its
// existing error text while HTTP clients can present the failure stage.
type PhaseError struct {
	Phase        Phase
	StoppedFirst bool
	Err          error
}

func (e *PhaseError) Error() string { return e.Err.Error() }
func (e *PhaseError) Unwrap() error { return e.Err }

// RestartResult describes the path taken by the CLI-compatible restart.
// StoppedFirst is true only when a Ready cell completed StopCell before the
// start step. Cell is the daemon's result from the final successful RPC.
type RestartResult struct {
	Cell         v1beta1.CellDoc
	StoppedFirst bool
}

// Restart matches `kuke restart` for one fully scoped cell. Ready cells are
// stopped and then started. Stopped, Exited, Error, Failed, and Degraded cells
// go directly through StartCell so kukeond can choose the appropriate
// recovery path. Pending and Unknown cells are rejected.
func Restart(ctx context.Context, client kukeonv1.Client, doc v1beta1.CellDoc) (RestartResult, error) {
	pre, err := client.GetCell(ctx, doc)
	if err != nil {
		return RestartResult{}, &PhaseError{Phase: PhaseLookup, Err: err}
	}
	if !pre.MetadataExists {
		return RestartResult{}, &PhaseError{Phase: PhaseLookup, Err: fmt.Errorf(
			"%w (cell %q in realm=%q space=%q stack=%q)",
			errdefs.ErrCellNotFound,
			doc.Metadata.Name,
			doc.Spec.RealmID,
			doc.Spec.SpaceID,
			doc.Spec.StackID,
		)}
	}

	switch pre.Cell.Status.State {
	case v1beta1.CellStateReady:
		if _, stopErr := client.StopCell(ctx, doc); stopErr != nil {
			return RestartResult{}, &PhaseError{Phase: PhaseStopping, Err: stopErr}
		}
		startResult, startErr := client.StartCell(ctx, doc)
		if startErr != nil {
			return RestartResult{}, &PhaseError{Phase: PhaseStarting, StoppedFirst: true, Err: startErr}
		}
		return RestartResult{Cell: startResult.Cell, StoppedFirst: true}, nil
	case v1beta1.CellStateStopped, v1beta1.CellStateExited, v1beta1.CellStateError,
		v1beta1.CellStateFailed, v1beta1.CellStateDegraded:
		startResult, startErr := client.StartCell(ctx, doc)
		if startErr != nil {
			return RestartResult{}, &PhaseError{Phase: PhaseStarting, Err: startErr}
		}
		return RestartResult{Cell: startResult.Cell}, nil
	case v1beta1.CellStatePending, v1beta1.CellStateUnknown:
		state := pre.Cell.Status.State
		return RestartResult{}, &PhaseError{
			Phase: PhaseValidation,
			Err: &invalidStateError{
				message: fmt.Sprintf(
					"cell %q exists in %s state; delete it with `kuke delete cell %s` before restarting",
					doc.Metadata.Name,
					state.String(),
					doc.Metadata.Name,
				),
			},
		}
	default:
		return RestartResult{}, &PhaseError{
			Phase: PhaseValidation,
			Err: &invalidStateError{
				message: fmt.Sprintf(
					"cell %q exists in unrecognized state %d",
					doc.Metadata.Name,
					pre.Cell.Status.State,
				),
			},
		}
	}
}

type invalidStateError struct{ message string }

func (e *invalidStateError) Error() string { return e.message }
func (e *invalidStateError) Unwrap() error { return errdefs.ErrInvalidCellState }
