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

//nolint:testpackage // tests the AutoDelete predicate which lives unexported
package runner

import (
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestCellStateAutoDeleteTriggers locks down the predicate the reconciler
// uses to decide when Spec.AutoDelete=true should kick off cleanup. Stopped
// and Failed are the only triggers — Unknown is excluded so a transient
// containerd hiccup doesn't nuke the cell, and the running states stay
// untouched.
func TestCellStateAutoDeleteTriggers(t *testing.T) {
	cases := []struct {
		state intmodel.CellState
		want  bool
	}{
		{intmodel.CellStateStopped, true},
		{intmodel.CellStateFailed, true},
		{intmodel.CellStateReady, false},
		{intmodel.CellStatePending, false},
		{intmodel.CellStateUnknown, false},
	}
	for _, tc := range cases {
		if got := cellStateAutoDeleteTriggers(tc.state); got != tc.want {
			t.Errorf("cellStateAutoDeleteTriggers(%v) = %v, want %v",
				tc.state, got, tc.want)
		}
	}
}
