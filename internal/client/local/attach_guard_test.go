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

package local

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// containerWith builds a minimal intmodel.Container with the named
// state so each guard case below only diffs the field it cares about.
// The cell/container identity strings flow into the error message and
// are asserted in the negative cases.
func containerWith(state intmodel.ContainerState) intmodel.Container {
	return intmodel.Container{
		Spec: intmodel.ContainerSpec{
			ID:       "work",
			CellName: "kukeon-pm-0",
		},
		Status: intmodel.ContainerStatus{State: state},
	}
}

// TestGuardAttachableTaskLiveness_Ready_Passes pins the only passing
// input to the server-side gate: the container's task must be Ready
// (Running) for AttachContainer to hand back a socket path.
func TestGuardAttachableTaskLiveness_Ready_Passes(t *testing.T) {
	if err := guardAttachableTaskLiveness(containerWith(intmodel.ContainerStateReady)); err != nil {
		t.Fatalf("guard returned %v on Ready task, want nil", err)
	}
}

// TestGuardAttachableTaskLiveness_NonReady_RefusesWithTypedError covers
// the #852 server-side check: every non-Ready state must surface
// errdefs.ErrAttachTaskNotRunning so that any consumer bypassing the
// CLI guard (raw RPC clients, scripts, in-process callers in
// alternative branches) still gets a typed refusal instead of a socket
// path that resolves to a dead inode.
func TestGuardAttachableTaskLiveness_NonReady_RefusesWithTypedError(t *testing.T) {
	cases := []struct {
		name  string
		state intmodel.ContainerState
		// wantStateText is the substring the error must include so the
		// operator can see which actual state triggered the refusal —
		// distinguishing a Stopped container (expected post-wind-down)
		// from a Failed one (sticky terminal) without reading server
		// logs.
		wantStateText string
	}{
		{"Stopped", intmodel.ContainerStateStopped, "Stopped"},
		{"Failed", intmodel.ContainerStateFailed, "Failed"},
		{"Unknown", intmodel.ContainerStateUnknown, "Unknown"},
		{"Pending", intmodel.ContainerStatePending, "Pending"},
		{"NotCreated", intmodel.ContainerStateNotCreated, "NotCreated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guardAttachableTaskLiveness(containerWith(tc.state))
			if err == nil {
				t.Fatalf("guard returned nil on %s container, want ErrAttachTaskNotRunning", tc.name)
			}
			if !errors.Is(err, errdefs.ErrAttachTaskNotRunning) {
				t.Fatalf("error %v does not unwrap to ErrAttachTaskNotRunning", err)
			}
			if got := err.Error(); !strings.Contains(got, tc.wantStateText) {
				t.Errorf("error %q missing state-name %q", got, tc.wantStateText)
			}
			if got := err.Error(); !strings.Contains(got, "work") {
				t.Errorf("error %q missing container ID %q", got, "work")
			}
			if got := err.Error(); !strings.Contains(got, "kukeon-pm-0") {
				t.Errorf("error %q missing cell name %q", got, "kukeon-pm-0")
			}
		})
	}
}
