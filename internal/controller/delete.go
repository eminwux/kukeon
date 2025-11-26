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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// DeleteRealmResult reports what was deleted during realm deletion.
type DeleteRealmResult struct {
	RealmDoc                   *v1beta1.RealmDoc
	Deleted                    []string // Resources that were deleted (metadata, cgroup, namespace, cascaded resources)
	MetadataDeleted            bool
	CgroupDeleted              bool
	ContainerdNamespaceDeleted bool
}

// DeleteSpaceResult reports what was deleted during space deletion.
type DeleteSpaceResult struct {
	SpaceName string
	RealmName string
	SpaceDoc  *v1beta1.SpaceDoc

	MetadataDeleted   bool
	CgroupDeleted     bool
	CNINetworkDeleted bool

	Deleted []string // Resources that were deleted (metadata, cgroup, network, cascaded resources)
}

// DeleteStackResult reports what was deleted during stack deletion.
type DeleteStackResult struct {
	StackName string
	RealmName string
	SpaceName string
	StackDoc  *v1beta1.StackDoc

	MetadataDeleted bool
	CgroupDeleted   bool

	Deleted []string // Resources that were deleted (metadata, cgroup, cascaded resources)
}

// DeleteCellResult reports what was deleted during cell deletion.
type DeleteCellResult struct {
	CellDoc           *v1beta1.CellDoc
	ContainersDeleted bool
	CgroupDeleted     bool
	MetadataDeleted   bool
}

// DeleteContainerResult mirrors GetContainerResult but also reports what was deleted.
type DeleteContainerResult struct {
	ContainerDoc       *v1beta1.ContainerDoc
	CellMetadataExists bool
	ContainerExists    bool
	Deleted            []string // Resources that were deleted (container, task)
}

// DeleteRealm deletes a realm. If cascade is true, deletes all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteRealm(doc *v1beta1.RealmDoc, force, cascade bool) (DeleteRealmResult, error) {
	var res DeleteRealmResult
	if doc == nil {
		return res, errdefs.ErrRealmNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	doc.Metadata.Name = name

	namespace := strings.TrimSpace(doc.Spec.Namespace)
	if namespace == "" {
		namespace = name
	}
	doc.Spec.Namespace = namespace

	// Ensure realm exists and capture its latest state
	getResult, err := b.GetRealm(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return res, fmt.Errorf("realm %q not found", name)
		}
		return res, err
	}
	if !getResult.MetadataExists || getResult.RealmDoc == nil {
		return res, fmt.Errorf("realm %q not found", name)
	}

	res = DeleteRealmResult{
		RealmDoc: getResult.RealmDoc,
		Deleted:  []string{},
	}

	// If cascade, delete all spaces first
	var spaces []*v1beta1.SpaceDoc
	if cascade {
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return res, fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, space := range spaces {
			_, err = b.DeleteSpace(space, force, cascade)
			if err != nil {
				return res, fmt.Errorf("failed to delete space %q: %w", space.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("space:%s", space.Metadata.Name))
		}
	} else if !force {
		// Validate no spaces exist
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return res, fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return res, fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(spaces))
		}
	}

	// Convert external realm to internal for runner.DeleteRealm
	internalRealm, _, convertErr := apischeme.NormalizeRealm(*getResult.RealmDoc)
	if convertErr != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}

	// Delete realm via runner and capture detailed outcome
	outcome, err := b.runner.DeleteRealm(internalRealm)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteRealm, err)
	}

	res.MetadataDeleted = outcome.MetadataDeleted
	res.CgroupDeleted = outcome.CgroupDeleted
	res.ContainerdNamespaceDeleted = outcome.ContainerdNamespaceDeleted

	if outcome.MetadataDeleted {
		res.Deleted = append(res.Deleted, "metadata")
	}
	if outcome.CgroupDeleted {
		res.Deleted = append(res.Deleted, "cgroup")
	}
	if outcome.ContainerdNamespaceDeleted {
		res.Deleted = append(res.Deleted, "namespace")
	}

	return res, nil
}

// DeleteSpace deletes a space. If cascade is true, deletes all stacks first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteSpace(doc *v1beta1.SpaceDoc, force, cascade bool) (DeleteSpaceResult, error) {
	var res DeleteSpaceResult
	if doc == nil {
		return res, errdefs.ErrSpaceNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	doc.Metadata.Name = name

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	doc.Spec.RealmID = realmName

	getResult, err := b.GetSpace(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			return res, fmt.Errorf("space %q not found in realm %q", name, realmName)
		}
		return res, err
	}
	if !getResult.MetadataExists || getResult.SpaceDoc == nil {
		return res, fmt.Errorf("space %q not found in realm %q", name, realmName)
	}

	res = DeleteSpaceResult{
		SpaceName: name,
		RealmName: realmName,
		SpaceDoc:  getResult.SpaceDoc,
		Deleted:   []string{},
	}

	// If cascade, delete all stacks first (recursively cascades to cells)
	var stacks []*v1beta1.StackDoc
	if cascade {
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list stacks: %w", err)
		}
		for _, stack := range stacks {
			if stack == nil {
				continue
			}
			_, err = b.DeleteStack(stack, force, cascade)
			if err != nil {
				return res, fmt.Errorf("failed to delete stack %q: %w", stack.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("stack:%s", stack.Metadata.Name))
		}
	} else if !force {
		// Validate no stacks exist
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list stacks: %w", err)
		}
		if len(stacks) > 0 {
			return res, fmt.Errorf("%w: space %q has %d stack(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(stacks))
		}
	}

	// Convert external space to internal for runner.DeleteSpace
	internalSpace, _, convertErr := apischeme.NormalizeSpace(*getResult.SpaceDoc)
	if convertErr != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}

	// Delete space
	if err = b.runner.DeleteSpace(internalSpace); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteSpace, err)
	}

	res.MetadataDeleted = true
	res.CgroupDeleted = true
	res.CNINetworkDeleted = true
	res.Deleted = append(res.Deleted, "metadata", "cgroup", "network")
	return res, nil
}

// DeleteStack deletes a stack. If cascade is true, deletes all cells first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteStack(doc *v1beta1.StackDoc, force, cascade bool) (DeleteStackResult, error) {
	var res DeleteStackResult
	if doc == nil {
		return res, errdefs.ErrStackNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrStackNameRequired
	}
	doc.Metadata.Name = name

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	doc.Spec.RealmID = realmName

	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	doc.Spec.SpaceID = spaceName

	getResult, err := b.GetStack(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			return res, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
		}
		return res, err
	}
	if !getResult.MetadataExists || getResult.StackDoc == nil {
		return res, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
	}

	res = DeleteStackResult{
		StackName: name,
		RealmName: realmName,
		SpaceName: spaceName,
		StackDoc:  getResult.StackDoc,
		Deleted:   []string{},
	}

	// If cascade, delete all cells first
	var cells []*v1beta1.CellDoc
	if cascade {
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list cells: %w", err)
		}
		for _, cell := range cells {
			_, err = b.DeleteCell(cell)
			if err != nil {
				return res, fmt.Errorf("failed to delete cell %q: %w", cell.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("cell:%s", cell.Metadata.Name))
		}
	} else if !force {
		// Validate no cells exist
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list cells: %w", err)
		}
		if len(cells) > 0 {
			return res, fmt.Errorf("%w: stack %q has %d cell(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(cells))
		}
	}

	// Convert external stack to internal for runner.DeleteStack
	internalStack, _, convertErr := apischeme.NormalizeStack(*getResult.StackDoc)
	if convertErr != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}

	// Delete stack
	if err = b.runner.DeleteStack(internalStack); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteStack, err)
	}

	res.MetadataDeleted = true
	res.CgroupDeleted = true
	res.Deleted = append(res.Deleted, "metadata", "cgroup")
	return res, nil
}

// DeleteCell deletes a cell. Always deletes all containers first.
func (b *Exec) DeleteCell(doc *v1beta1.CellDoc) (DeleteCellResult, error) {
	var res DeleteCellResult
	if doc == nil {
		return res, errdefs.ErrCellNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrCellNameRequired
	}
	doc.Metadata.Name = name

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	doc.Spec.RealmID = realmName

	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	doc.Spec.SpaceID = spaceName

	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return res, errdefs.ErrStackNameRequired
	}
	doc.Spec.StackID = stackName

	getResult, err := b.GetCell(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return res, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				name,
				realmName,
				spaceName,
				stackName,
			)
		}
		return res, err
	}
	cellDoc := getResult.CellDoc
	if cellDoc == nil {
		return res, fmt.Errorf("cell %q not found", name)
	}

	res = DeleteCellResult{
		CellDoc: cellDoc,
	}

	// Always delete all containers in cell first
	if len(cellDoc.Spec.Containers) > 0 {
		res.ContainersDeleted = true
	}

	// Convert external cell to internal for runner.DeleteCell
	internalCell, err := apischeme.ConvertCellDocToInternal(*cellDoc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	if err = b.runner.DeleteCell(internalCell); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteCell, err)
	}

	res.CgroupDeleted = true
	res.MetadataDeleted = true
	return res, nil
}

// DeleteContainer deletes a single container. Cascade flag is not applicable.
func (b *Exec) DeleteContainer(doc *v1beta1.ContainerDoc) (DeleteContainerResult, error) {
	var res DeleteContainerResult
	if doc == nil {
		return res, errdefs.ErrContainerNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return res, errdefs.ErrStackNameRequired
	}
	cellName := strings.TrimSpace(doc.Spec.CellID)
	if cellName == "" {
		return res, errdefs.ErrCellNameRequired
	}

	res.Deleted = []string{}

	// Get cell to find container
	cellDoc := &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: cellName,
		},
		Spec: v1beta1.CellSpec{
			RealmID: realmName,
			SpaceID: spaceName,
			StackID: stackName,
		},
	}
	getResult, err := b.GetCell(cellDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.CellMetadataExists = false
			return res, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				cellName,
				realmName,
				spaceName,
				stackName,
			)
		}
		return res, err
	}
	res.CellMetadataExists = getResult.MetadataExists
	cellDoc = getResult.CellDoc
	if cellDoc == nil {
		return res, fmt.Errorf("cell %q not found", cellName)
	}

	// Find container in cell by name (ID now stores just the container name)
	var foundContainer *v1beta1.ContainerSpec
	for i := range cellDoc.Spec.Containers {
		if cellDoc.Spec.Containers[i].ID == name {
			foundContainer = &cellDoc.Spec.Containers[i]
			break
		}
	}

	if foundContainer == nil {
		return res, fmt.Errorf("container %q not found in cell %q", name, cellName)
	}

	res.ContainerExists = true
	labels := doc.Metadata.Labels
	if labels == nil {
		labels = make(map[string]string)
	}
	res.ContainerDoc = &v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: *foundContainer,
		Status: v1beta1.ContainerStatus{
			State: v1beta1.ContainerStateReady,
		},
	}

	// Convert external cell to internal for runner.DeleteContainer
	internalCell, err := apischeme.ConvertCellDocToInternal(*cellDoc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Delete container from containerd (via runner)
	if err = b.runner.DeleteContainer(internalCell, name); err != nil {
		return res, fmt.Errorf("failed to delete container %s: %w", name, err)
	}

	// Remove container from cell's Spec.Containers list
	var updatedContainers []v1beta1.ContainerSpec
	for _, container := range cellDoc.Spec.Containers {
		if container.ID != name {
			updatedContainers = append(updatedContainers, container)
		}
	}
	cellDoc.Spec.Containers = updatedContainers

	// Convert to internal model for UpdateCellMetadata
	cell, err := apischeme.ConvertCellDocToInternal(*cellDoc)
	if err != nil {
		return res, fmt.Errorf("failed to convert cell to internal model: %w", err)
	}

	// Update cell metadata to persist the change
	if err = b.runner.UpdateCellMetadata(cell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	res.Deleted = append(res.Deleted, "container", "task")
	return res, nil
}
