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
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// GetContainerResult reports the current state of a container.
type GetContainerResult struct {
	Container          intmodel.Container
	CellMetadataExists bool
	ContainerExists    bool
}

// GetContainer retrieves a single container and reports its current state.
func (b *Exec) GetContainer(container intmodel.Container) (GetContainerResult, error) {
	var res GetContainerResult

	name := strings.TrimSpace(container.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	realmName := strings.TrimSpace(container.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(container.Spec.SpaceName)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(container.Spec.StackName)
	if stackName == "" {
		return res, errdefs.ErrStackNameRequired
	}
	cellName := strings.TrimSpace(container.Spec.CellName)
	if cellName == "" {
		return res, errdefs.ErrCellNameRequired
	}
	// The scope coordinates are checked by the runner's GetCell; the
	// container name is checked here so a get never accepts a name no
	// create could have written (issue #1719).
	if err := naming.ValidateHierarchyName("container", name); err != nil {
		return res, err
	}

	// Build lookup cell for GetCell
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: stackName,
		},
	}
	internalCell, err := b.runner.GetCell(lookupCell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.CellMetadataExists = false
			return res, fmt.Errorf("failed to get cell %q: %w", cellName, errdefs.ErrCellNotFound)
		}
		return res, fmt.Errorf("failed to get cell %q: %w", cellName, err)
	}
	res.CellMetadataExists = true

	// Find container in cell spec by name (ID now stores just the container name)
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range internalCell.Spec.Containers {
		if internalCell.Spec.Containers[i].ID == name {
			foundContainerSpec = &internalCell.Spec.Containers[i]
			break
		}
	}

	if foundContainerSpec != nil {
		res.ContainerExists = true
		// Construct Container from the found container spec
		labels := container.Metadata.Labels
		if labels == nil {
			labels = make(map[string]string)
		}

		// Query actual container state from containerd
		var actualState intmodel.ContainerState
		actualState, err = b.runner.GetContainerState(internalCell, name)
		if err != nil {
			// Log error at info level so it's visible
			b.logger.InfoContext(b.ctx, "failed to get container state from containerd",
				"container", name,
				"cell", cellName,
				"error", err)
			actualState = intmodel.ContainerStateUnknown
		}

		// Log state for debugging (use info level so it's visible)
		b.logger.InfoContext(b.ctx, "container state from containerd",
			"container", name,
			"cell", cellName,
			"state", actualState)

		// Note: Container state is not currently persisted in ContainerSpec.
		// The state is queried from containerd each time GetContainer is called.
		// If state persistence is needed in the future, ContainerSpec would need
		// to be extended to include a Status field, or container states would need
		// to be stored separately in cell metadata.

		// GetCell populated the complete per-container status — CreatedAt
		// (issue #605), start/finish/restart times, restart count, exit
		// code/signal (issue #1137), the per-repo / per-create-stage
		// outcomes and the supervisor status read from kuketty's
		// supervisor document, and the derived reason (issue #1711).
		// Carry the matching observation through whole so the named
		// Container document agrees with its parent Cell (issue #1379),
		// overriding only identity and the freshly-queried containerd state.
		status := containerStatusForContainer(internalCell, name)
		status.Name = name
		status.ID = name
		status.State = actualState

		res.Container = intmodel.Container{
			Metadata: intmodel.ContainerMetadata{
				Name:   name,
				Labels: labels,
			},
			Spec:   *foundContainerSpec,
			Status: status,
		}
	} else {
		res.ContainerExists = false
	}

	if !res.ContainerExists {
		return res, fmt.Errorf(
			"container %q not found in cell %q at run-path %q: %w",
			name,
			cellName,
			b.opts.RunPath,
			errdefs.ErrContainerNotFound,
		)
	}

	return res, nil
}

// containerStatusForContainer returns the named container's status from the
// cell's per-container statuses (populated and preserved by
// populateCellContainerStatuses), or the zero status when the cell has no
// entry for the name yet. Zero times render as "-" via shared.RenderAge.
func containerStatusForContainer(cell intmodel.Cell, name string) intmodel.ContainerStatus {
	for i := range cell.Status.Containers {
		if cell.Status.Containers[i].ID == name {
			return cell.Status.Containers[i]
		}
	}
	return intmodel.ContainerStatus{}
}

// ListContainers lists all containers, optionally filtered by realm, space, stack, and/or cell.
func (b *Exec) ListContainers(realmName, spaceName, stackName, cellName string) ([]intmodel.ContainerSpec, error) {
	return b.runner.ListContainers(realmName, spaceName, stackName, cellName)
}

// ReapplyAttachableSocketPerms heals a single attachable container's live
// tty control socket inode to the connect(2)-able mode/group on the attach
// path (#1169). AttachContainer calls it before handing back the socket
// path so a `kuke run` against an already-Ready cell — which short-circuits
// past StartCell and so past the #935 StartCell-skip heal — still corrects a
// wrong-mode (pre-sbsh#361 0o640) live socket in place, matching
// `kuke restart` without reintroducing the #630 start-on-running hazard.
// Best-effort and root-owned (the daemon runs as root); a chmod miss is
// logged by the runner and never surfaced, so a healthy socket is untouched.
func (b *Exec) ReapplyAttachableSocketPerms(spec intmodel.ContainerSpec) {
	b.runner.ReapplyAttachableSocketPerms(spec)
}
