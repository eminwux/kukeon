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

package controller

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/controller/runner"
)

// TestHostPathsOf_DaemonPaths pins #1710: the render expands a team
// blueprint's host-path variables to the run path and socket the daemon's
// controller was built with, and a runner that knows no paths (a test fake)
// gets the standard layout.
func TestHostPathsOf_DaemonPaths(t *testing.T) {
	ctrl := NewControllerExec(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		RunPath:       "/tmp/ke-1",
		KukeondSocket: "/tmp/ke-1/kukeond.sock",
	})
	got := hostPathsOf(ctrl.runner)
	want := cellblueprint.HostPaths{RunPath: "/tmp/ke-1", Socket: "/tmp/ke-1/kukeond.sock"}
	if got != want {
		t.Errorf("hostPathsOf(daemon runner) = %+v, want %+v", got, want)
	}

	var fake struct{ runner.Runner }
	if got = hostPathsOf(fake); got != (cellblueprint.HostPaths{}) {
		t.Errorf("hostPathsOf(fake) = %+v, want the standard layout", got)
	}
}
