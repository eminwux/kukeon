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
	"fmt"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// WaitCellRootTaskExit returns a channel that fires when the cell's root
// container task exits. The runner switches the ctr client into the realm's
// containerd namespace before calling task.Wait so the channel is bound to
// the right task. Used by the daemon's `kuke run --rm` watcher; the channel
// emits exactly once and the caller is responsible for any cleanup.
func (r *Exec) WaitCellRootTaskExit(ctx context.Context, cell intmodel.Cell) (<-chan containerd.ExitStatus, error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return nil, internalerrdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return nil, internalerrdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return nil, internalerrdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return nil, internalerrdefs.ErrStackNameRequired
	}
	cellID := strings.TrimSpace(cell.Spec.ID)
	if cellID == "" {
		cellID = cellName
	}

	if err := r.ensureClientConnected(); err != nil {
		return nil, fmt.Errorf("%w: %w", internalerrdefs.ErrConnectContainerd, err)
	}

	internalRealm, err := r.GetRealm(intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: realmName},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get realm %q: %w", realmName, err)
	}
	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return nil, fmt.Errorf("realm %q has no namespace", realmName)
	}
	r.ctrClient.SetNamespace(namespace)

	rootContainerdID, err := naming.BuildRootContainerdID(spaceName, stackName, cellID)
	if err != nil {
		return nil, fmt.Errorf("failed to build root containerd ID: %w", err)
	}

	exitChan, err := r.ctrClient.WaitTaskExit(ctx, rootContainerdID)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for root task %s: %w", rootContainerdID, err)
	}
	return exitChan, nil
}
