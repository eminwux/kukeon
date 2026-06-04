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

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// CreateCellResult reports reconciliation outcomes for a cell.
type CreateCellResult struct {
	Cell intmodel.Cell

	MetadataExistsPre       bool
	MetadataExistsPost      bool
	CgroupExistsPre         bool
	CgroupExistsPost        bool
	CgroupCreated           bool
	RootContainerExistsPre  bool
	RootContainerExistsPost bool
	RootContainerCreated    bool
	StartedPre              bool
	StartedPost             bool
	Started                 bool
	Created                 bool

	Containers []ContainerCreationOutcome
}

type ContainerCreationOutcome struct {
	Name       string
	ExistsPre  bool
	ExistsPost bool
	Created    bool
}

// CreateCell creates a new cell or ensures an existing cell's resources exist,
// then starts the cell's containers. See createCellInternal for the full
// contract.
func (b *Exec) CreateCell(cell intmodel.Cell) (CreateCellResult, error) {
	return b.createCellInternal(cell, true)
}

// MaterializeCell creates a new cell record (or ensures an existing cell's
// resources exist) without starting any container tasks. The resulting cell
// is left in a stopped/created state; the operator runs `kuke start <name>`
// to start it. Used by the CLI's `kuke create cell --from-blueprint` and
// `--from-config` scaffolding modes (#818) — distinct from `kuke run -b` /
// `kuke run <cfg>` (materialise + start + attach) and (for Config-lineage
// cells) `kuke restart <name>` (reconcile + start on OutOfSync).
func (b *Exec) MaterializeCell(cell intmodel.Cell) (CreateCellResult, error) {
	return b.createCellInternal(cell, false)
}

// normalizeCellInputs validates the cell's required identity fields (name,
// realm, space, stack, container IDs), trims them in place, applies the
// default scope/cell labels when unset, and ensures Spec.ID + per-container
// ownership are filled. The returned name/realm/space/stack are the trimmed
// values used by the caller; the cell argument is mutated to carry the
// normalised body.
//
// Extracted from createCellInternal to keep the latter's cyclomatic
// complexity under the gocyclo budget after #818's startAfterCreate branch
// was added.
func normalizeCellInputs(cell *intmodel.Cell) (string, string, string, string, error) {
	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return "", "", "", "", errdefs.ErrCellNameRequired
	}
	if err := naming.ValidateHierarchyName("cell", name); err != nil {
		return "", "", "", "", err
	}
	cell.Metadata.Name = name
	realm := strings.TrimSpace(cell.Spec.RealmName)
	if realm == "" {
		return "", "", "", "", errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(cell.Spec.SpaceName)
	if space == "" {
		return "", "", "", "", errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(cell.Spec.StackName)
	if stack == "" {
		return "", "", "", "", errdefs.ErrStackNameRequired
	}

	// Validate container names embedded in the cell spec so a malformed
	// container ID is rejected at the cell-create boundary, before
	// provisionNewCell would build a containerd ID with an embedded "_".
	for i := range cell.Spec.Containers {
		id := strings.TrimSpace(cell.Spec.Containers[i].ID)
		if id == "" {
			return "", "", "", "", errdefs.ErrContainerNameRequired
		}
		if err := naming.ValidateHierarchyName("container", id); err != nil {
			return "", "", "", "", err
		}
		cell.Spec.Containers[i].ID = id
	}

	applyDefaultCellLabels(cell, realm, space, stack, name)

	// Ensure Spec.ID is set
	if cell.Spec.ID == "" {
		cell.Spec.ID = name
	}

	// Ensure container ownership (work with internal types)
	for i := range cell.Spec.Containers {
		cell.Spec.Containers[i].RealmName = realm
		cell.Spec.Containers[i].SpaceName = space
		cell.Spec.Containers[i].StackName = stack
		cell.Spec.Containers[i].CellName = name
	}

	return name, realm, space, stack, nil
}

// applyDefaultCellLabels sets the four scope/cell labels on the cell when
// the operator did not author them explicitly. Extracted from
// normalizeCellInputs to keep that function under the funlen budget.
func applyDefaultCellLabels(cell *intmodel.Cell, realm, space, stack, name string) {
	if cell.Metadata.Labels == nil {
		cell.Metadata.Labels = make(map[string]string)
	}
	for key, value := range map[string]string{
		consts.KukeonRealmLabelKey: realm,
		consts.KukeonSpaceLabelKey: space,
		consts.KukeonStackLabelKey: stack,
		consts.KukeonCellLabelKey:  name,
	} {
		if _, exists := cell.Metadata.Labels[key]; !exists {
			cell.Metadata.Labels[key] = value
		}
	}
}

// acquireOrCreateCell branches on whether the cell record already exists.
// On the "not found" path, runner.CreateCell creates a fresh record. On the
// "found" path, the cell's pre-state (cgroup/root-container existence,
// container ID set) is recorded into res and preContainerExists, then
// runner.EnsureCell reconciles any missing resources. The bool return is
// wasCreated — true for the fresh-record path, false for the existing path.
//
// Extracted from createCellInternal to keep that function under the funlen
// budget after #818's startAfterCreate branch was added.
func (b *Exec) acquireOrCreateCell(
	cell, lookupCell intmodel.Cell,
	res *CreateCellResult,
	preContainerExists map[string]bool,
) (intmodel.Cell, bool, error) {
	internalCellPre, getErr := b.runner.GetCell(lookupCell)
	if getErr != nil {
		if !errors.Is(getErr, errdefs.ErrCellNotFound) {
			return intmodel.Cell{}, false, fmt.Errorf("%w: %w", errdefs.ErrGetCell, getErr)
		}
		res.MetadataExistsPre = false
		resultCell, createErr := b.runner.CreateCell(cell)
		if createErr != nil {
			return intmodel.Cell{}, false, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, createErr)
		}
		return resultCell, true, nil
	}

	res.MetadataExistsPre = true
	cgroupExists, cgErr := b.runner.ExistsCgroup(internalCellPre)
	if cgErr != nil {
		return intmodel.Cell{}, false, fmt.Errorf("failed to check if cell cgroup exists: %w", cgErr)
	}
	res.CgroupExistsPre = cgroupExists
	rootExists, rcErr := b.runner.ExistsCellRootContainer(internalCellPre)
	if rcErr != nil {
		return intmodel.Cell{}, false, fmt.Errorf("failed to check root container: %w", rcErr)
	}
	res.RootContainerExistsPre = rootExists
	for _, container := range internalCellPre.Spec.Containers {
		id := strings.TrimSpace(container.ID)
		if id != "" {
			preContainerExists[id] = true
		}
	}
	res.StartedPre = false

	resultCell, ensureErr := b.runner.EnsureCell(internalCellPre)
	if ensureErr != nil {
		return intmodel.Cell{}, false, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, ensureErr)
	}
	return resultCell, false, nil
}

// createCellInternal is the shared implementation behind CreateCell and
// MaterializeCell. When startAfterCreate is true, the cell's containers are
// started before the result is built; when false, the cell is left stopped.
//
// The CreateCellResult reports the state of cell resources before and after
// the operation, indicating what was created vs what already existed,
// including container-level outcomes. An error is returned if the cell name
// is required, the realm name is required, the space name is required, the
// stack name is required, the cell cgroup does not exist, the root container
// does not exist, or the cell creation fails.
func (b *Exec) createCellInternal(cell intmodel.Cell, startAfterCreate bool) (CreateCellResult, error) {
	var res CreateCellResult

	name, realm, space, stack, err := normalizeCellInputs(&cell)
	if err != nil {
		return res, err
	}

	preContainerExists := make(map[string]bool)

	// Build minimal internal cell for GetCell lookup
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: name,
		},
		Spec: intmodel.CellSpec{
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
		},
	}

	resultCell, wasCreated, err := b.acquireOrCreateCell(cell, lookupCell, &res, preContainerExists)
	if err != nil {
		return res, err
	}

	// Start cell containers when the caller asked for it. MaterializeCell
	// (#818) skips this step so the cell record is left in a stopped state for
	// the operator to start explicitly with `kuke start <name>`.
	if startAfterCreate {
		// Carry `kuke run --env` runtime entries onto the cell handed to the
		// runner. acquireOrCreateCell returns either the freshly-created cell
		// (provisionNewCell already saw RuntimeEnv on the input `cell` and
		// applied it during createCellContainers) or the existing cell from
		// disk (RuntimeEnv stripped at persistence). Either way, the
		// downstream runner.StartCell rebuilds the non-root container OCI
		// specs, so it needs the runtime env from the inbound RPC. Issue #834.
		resultCell.Spec.RuntimeEnv = cell.Spec.RuntimeEnv
		resultCell, err = b.runner.StartCell(resultCell)
		if err != nil {
			return res, fmt.Errorf("failed to start cell containers: %w", err)
		}
	}

	// Build post-container existence map from resultCell directly
	postContainerExists := make(map[string]bool)
	for _, container := range resultCell.Spec.Containers {
		id := strings.TrimSpace(container.ID)
		if id != "" {
			postContainerExists[id] = true
		}
	}

	// Set result fields
	res.Cell = resultCell
	res.MetadataExistsPost = true
	// After CreateCell/EnsureCell, cgroup and root container are guaranteed to exist
	res.CgroupExistsPost = true
	res.RootContainerExistsPost = true
	res.StartedPost = startAfterCreate
	res.Created = wasCreated
	if wasCreated {
		// New cell: all resources were created
		res.CgroupCreated = true
		res.RootContainerCreated = true
		res.Started = startAfterCreate
	} else {
		// Existing cell: resources were created only if they didn't exist before
		res.CgroupCreated = !res.CgroupExistsPre
		res.RootContainerCreated = !res.RootContainerExistsPre
		res.Started = startAfterCreate && !res.StartedPre
	}

	// Build container creation outcomes
	for _, container := range cell.Spec.Containers {
		id := strings.TrimSpace(container.ID)
		if id == "" {
			continue
		}
		created := !preContainerExists[id] && postContainerExists[id]
		res.Containers = append(res.Containers, ContainerCreationOutcome{
			Name:       id,
			ExistsPre:  preContainerExists[id],
			ExistsPost: postContainerExists[id],
			Created:    created,
		})
	}

	return res, nil
}
