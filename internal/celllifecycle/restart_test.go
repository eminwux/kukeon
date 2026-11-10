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

package celllifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type restartClient struct {
	kukeonv1.FakeClient

	state     v1beta1.CellState
	exists    bool
	getErr    error
	stopErr   error
	startErr  error
	calls     []string
	resultDoc v1beta1.CellDoc
}

func (c *restartClient) GetCell(_ context.Context, _ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	c.calls = append(c.calls, "get")
	return kukeonv1.GetCellResult{
		MetadataExists: c.exists,
		Cell:           v1beta1.CellDoc{Status: v1beta1.CellStatus{State: c.state}},
	}, c.getErr
}

func (c *restartClient) StopCell(_ context.Context, _ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
	c.calls = append(c.calls, "stop")
	return kukeonv1.StopCellResult{}, c.stopErr
}

func (c *restartClient) StartCell(_ context.Context, _ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	c.calls = append(c.calls, "start")
	return kukeonv1.StartCellResult{Cell: c.resultDoc}, c.startErr
}

func TestRestartUsesCLIStateSemantics(t *testing.T) {
	doc := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: "web"},
		Spec:     v1beta1.CellSpec{RealmID: "r", SpaceID: "s", StackID: "t"},
	}
	for _, tc := range []struct {
		name        string
		state       v1beta1.CellState
		wantCalls   []string
		wantStopped bool
		wantErr     bool
		wantInvalid bool
	}{
		{name: "ready bounces", state: v1beta1.CellStateReady, wantCalls: []string{"get", "stop", "start"}, wantStopped: true},
		{name: "stopped uses start recovery", state: v1beta1.CellStateStopped, wantCalls: []string{"get", "start"}},
		{name: "failed uses daemon recovery", state: v1beta1.CellStateFailed, wantCalls: []string{"get", "start"}},
		{name: "error uses daemon recovery", state: v1beta1.CellStateError, wantCalls: []string{"get", "start"}},
		{name: "degraded uses daemon recovery", state: v1beta1.CellStateDegraded, wantCalls: []string{"get", "start"}},
		{name: "exited uses start recovery", state: v1beta1.CellStateExited, wantCalls: []string{"get", "start"}},
		{name: "pending is invalid", state: v1beta1.CellStatePending, wantCalls: []string{"get"}, wantErr: true, wantInvalid: true},
		{name: "unknown is invalid", state: v1beta1.CellStateUnknown, wantCalls: []string{"get"}, wantErr: true, wantInvalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &restartClient{state: tc.state, exists: true, resultDoc: doc}
			result, err := Restart(context.Background(), client, doc)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Restart() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(client.calls, tc.wantCalls) {
				t.Fatalf("calls = %v, want %v", client.calls, tc.wantCalls)
			}
			if result.StoppedFirst != tc.wantStopped {
				t.Fatalf("StoppedFirst = %v, want %v", result.StoppedFirst, tc.wantStopped)
			}
			if tc.wantInvalid && !errors.Is(err, errdefs.ErrInvalidCellState) {
				t.Fatalf("error %v does not identify invalid state", err)
			}
		})
	}
}

func TestRestartReportsFailurePhase(t *testing.T) {
	doc := v1beta1.CellDoc{Metadata: v1beta1.CellMetadata{Name: "web"}}
	boom := errors.New("daemon failed")
	for _, tc := range []struct {
		name      string
		client    *restartClient
		wantPhase Phase
		wantCalls []string
	}{
		{name: "lookup", client: &restartClient{exists: true, state: v1beta1.CellStateReady, getErr: boom}, wantPhase: PhaseLookup, wantCalls: []string{"get"}},
		{name: "stop", client: &restartClient{exists: true, state: v1beta1.CellStateReady, stopErr: boom}, wantPhase: PhaseStopping, wantCalls: []string{"get", "stop"}},
		{name: "start after stop", client: &restartClient{exists: true, state: v1beta1.CellStateReady, startErr: boom}, wantPhase: PhaseStarting, wantCalls: []string{"get", "stop", "start"}},
		{name: "start from stopped", client: &restartClient{exists: true, state: v1beta1.CellStateStopped, startErr: boom}, wantPhase: PhaseStarting, wantCalls: []string{"get", "start"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Restart(context.Background(), tc.client, doc)
			var phaseErr *PhaseError
			if !errors.As(err, &phaseErr) || phaseErr.Phase != tc.wantPhase {
				t.Fatalf("Restart() error = %#v, want phase %q", err, tc.wantPhase)
			}
			if !errors.Is(err, boom) {
				t.Fatalf("Restart() error %v does not unwrap to cause", err)
			}
			if !reflect.DeepEqual(tc.client.calls, tc.wantCalls) {
				t.Fatalf("calls = %v, want %v", tc.client.calls, tc.wantCalls)
			}
		})
	}
}

func TestRestartMissingCellIsNotFound(t *testing.T) {
	client := &restartClient{}
	doc := v1beta1.CellDoc{Metadata: v1beta1.CellMetadata{Name: "missing"}}
	_, err := Restart(context.Background(), client, doc)
	if !errors.Is(err, errdefs.ErrCellNotFound) {
		t.Fatalf("Restart() error = %v, want cell not found", err)
	}
	if len(client.calls) != 1 || client.calls[0] != "get" {
		t.Fatalf("calls = %v, want only get", client.calls)
	}
}
