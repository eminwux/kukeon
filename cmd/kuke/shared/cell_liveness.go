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

package shared

import (
	"fmt"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// GuardCellTaskLiveness refuses to attach to a cell whose on-disk metadata
// records it Ready but whose root-container task is gone from containerd. This
// is the post-reboot divergence (#654, #683): containerd container *records*
// survive a host/daemon restart while the backing tasks do not, so a
// record-existence check passes even though attaching would land on a dead
// socket. The guard keys on task liveness (the root task is Running) rather
// than record existence.
//
// It is scoped to Ready: a legitimately Stopped cell has no live task by
// design, and the run path's Stopped branch starts it before attaching.
// Returns nil when the cell is not Ready or its root task is live; otherwise
// the diverged-state error with a delete-then-rerun pointer.
//
// Both `kuke run` (Ready short-circuit) and `kuke attach` call this before
// handing a host socket path to the in-process sbsh attach loop — the single
// shared guard backing both entry points.
func GuardCellTaskLiveness(get kukeonv1.GetCellResult, cellName string) error {
	if get.Cell.Status.State != v1beta1.CellStateReady {
		return nil
	}
	if get.RootContainerTaskRunning {
		return nil
	}
	return fmt.Errorf(
		"cell %q is recorded Ready but its containers are gone from containerd "+
			"(kukeon metadata and containerd have diverged); "+
			"delete it with `kuke delete cell %s` before re-running",
		cellName,
		cellName,
	)
}
