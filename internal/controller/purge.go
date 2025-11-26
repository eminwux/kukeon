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

// PurgeRealmResult reports what was purged during realm purging.
type PurgeRealmResult struct {
	RealmDoc       *v1beta1.RealmDoc
	RealmDeleted   bool     // Whether realm deletion succeeded
	PurgeSucceeded bool     // Whether comprehensive purge succeeded
	Force          bool     // Force flag that was used
	Cascade        bool     // Cascade flag that was used
	Deleted        []string // Resources that were deleted (standard cleanup)
	Purged         []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeSpaceResult reports what was purged during space purging.
type PurgeSpaceResult struct {
	SpaceDoc *v1beta1.SpaceDoc

	MetadataDeleted   bool
	CgroupDeleted     bool
	CNINetworkDeleted bool
	PurgeSucceeded    bool
	Force             bool
	Cascade           bool

	Deleted []string // Resources that were deleted (standard cleanup)
	Purged  []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeStackResult reports what was purged during stack purging.
type PurgeStackResult struct {
	StackDoc *v1beta1.StackDoc
	Deleted  []string // Resources that were deleted (standard cleanup)
	Purged   []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeCellResult reports what was purged during cell purging.
type PurgeCellResult struct {
	CellDoc           *v1beta1.CellDoc
	ContainersDeleted bool
	CgroupDeleted     bool
	MetadataDeleted   bool
	PurgeSucceeded    bool
	Force             bool
	Cascade           bool
	Deleted           []string // Resources that were deleted (standard cleanup)
	Purged            []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeContainerResult reports what was purged during container purging.
type PurgeContainerResult struct {
	ContainerDoc       *v1beta1.ContainerDoc
	CellMetadataExists bool
	ContainerExists    bool
	Deleted            []string // Resources that were deleted (standard cleanup)
	Purged             []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeRealm purges a realm with comprehensive cleanup. If cascade is true, purges all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeRealm(doc *v1beta1.RealmDoc, force, cascade bool) (PurgeRealmResult, error) {
	var result PurgeRealmResult

	if doc == nil {
		return result, errdefs.ErrRealmNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	// Get realm document
	getResult, err := b.GetRealm(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return result, fmt.Errorf("realm %q not found", name)
		}
		return result, err
	}

	// Initialize result with realm document and flags
	result = PurgeRealmResult{
		RealmDoc: getResult.RealmDoc,
		Force:    force,
		Cascade:  cascade,
		Deleted:  []string{},
		Purged:   []string{},
	}

	// If cascade, purge all spaces first
	if cascade {
		var spaces []*v1beta1.SpaceDoc
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return result, fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, space := range spaces {
			spaceDoc := &v1beta1.SpaceDoc{
				Metadata: v1beta1.SpaceMetadata{
					Name: space.Metadata.Name,
				},
				Spec: v1beta1.SpaceSpec{
					RealmID: name,
				},
			}
			_, err = b.PurgeSpace(spaceDoc, force, cascade)
			if err != nil {
				return result, fmt.Errorf("failed to purge space %q: %w", space.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("space:%s", space.Metadata.Name))
		}
	} else if !force {
		// Validate no spaces exist
		var spaces []*v1beta1.SpaceDoc
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return result, fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return result, fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(spaces))
		}
	}

	// Perform standard delete first
	deleteDoc := &v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: name,
		},
		Spec: v1beta1.RealmSpec{
			Namespace: name,
		},
	}
	deleteResult, err := b.DeleteRealm(deleteDoc, force, cascade)
	if err != nil {
		// Log but continue with purge
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
		result.RealmDeleted = false
	} else {
		result.Deleted = deleteResult.Deleted
		result.RealmDeleted = true
		// Update RealmDoc from delete result if available
		if deleteResult.RealmDoc != nil {
			result.RealmDoc = deleteResult.RealmDoc
		}
	}

	// Perform comprehensive purge
	// Convert external realm to internal for runner.PurgeRealm
	internalRealm, _, convertErr := apischeme.NormalizeRealm(*getResult.RealmDoc)
	if convertErr != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-conversion-error:%v", convertErr))
		result.PurgeSucceeded = false
	} else {
		if err = b.runner.PurgeRealm(internalRealm); err != nil {
			result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
			result.PurgeSucceeded = false
		} else {
			result.Purged = append(result.Purged, "orphaned-containers", "cni-resources", "all-metadata")
			result.PurgeSucceeded = true
		}
	}

	return result, nil
}

// PurgeSpace purges a space with comprehensive cleanup. If cascade is true, purges all stacks first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeSpace(doc *v1beta1.SpaceDoc, force, cascade bool) (PurgeSpaceResult, error) {
	var result PurgeSpaceResult

	if doc == nil {
		return result, errdefs.ErrSpaceNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrSpaceNameRequired
	}
	doc.Metadata.Name = name

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}
	doc.Spec.RealmID = realmName

	getResult, err := b.GetSpace(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			return result, fmt.Errorf("space %q not found in realm %q", name, realmName)
		}
		return result, err
	}
	if !getResult.MetadataExists || getResult.SpaceDoc == nil {
		return result, fmt.Errorf("space %q not found in realm %q", name, realmName)
	}

	result = PurgeSpaceResult{
		SpaceDoc: getResult.SpaceDoc,
		Force:    force,
		Cascade:  cascade,
		Deleted:  []string{},
		Purged:   []string{},
	}

	// If cascade, purge all stacks first (recursively cascades to cells)
	if cascade {
		var stacks []*v1beta1.StackDoc
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return result, fmt.Errorf("failed to list stacks: %w", err)
		}
		for _, stack := range stacks {
			stackDoc := &v1beta1.StackDoc{
				Metadata: v1beta1.StackMetadata{
					Name: stack.Metadata.Name,
				},
				Spec: v1beta1.StackSpec{
					RealmID: realmName,
					SpaceID: name,
				},
			}
			_, err = b.PurgeStack(stackDoc, force, cascade)
			if err != nil {
				return result, fmt.Errorf("failed to purge stack %q: %w", stack.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("stack:%s", stack.Metadata.Name))
		}
	} else if !force {
		// Validate no stacks exist
		var stacks []*v1beta1.StackDoc
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return result, fmt.Errorf("failed to list stacks: %w", err)
		}
		if len(stacks) > 0 {
			return result, fmt.Errorf("%w: space %q has %d stack(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(stacks))
		}
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteSpace(doc, force, cascade)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.Deleted = append(result.Deleted, deleteResult.Deleted...)
		result.MetadataDeleted = deleteResult.MetadataDeleted
		result.CgroupDeleted = deleteResult.CgroupDeleted
		result.CNINetworkDeleted = deleteResult.CNINetworkDeleted
		if deleteResult.SpaceDoc != nil {
			result.SpaceDoc = deleteResult.SpaceDoc
		}
	}

	// Perform comprehensive purge
	// Convert external space to internal for runner.PurgeSpace
	internalSpace, _, convertErr := apischeme.NormalizeSpace(*getResult.SpaceDoc)
	if convertErr != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-conversion-error:%v", convertErr))
		result.PurgeSucceeded = false
	} else {
		if err = b.runner.PurgeSpace(internalSpace); err != nil {
			result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
			result.PurgeSucceeded = false
		} else {
			result.Purged = append(result.Purged, "cni-network", "cni-cache", "orphaned-containers", "all-metadata")
			result.PurgeSucceeded = true
		}
	}

	return result, nil
}

// PurgeStack purges a stack with comprehensive cleanup. If cascade is true, purges all cells first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeStack(doc *v1beta1.StackDoc, force, cascade bool) (PurgeStackResult, error) {
	var result PurgeStackResult

	if doc == nil {
		return result, errdefs.ErrStackNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrStackNameRequired
	}
	doc.Metadata.Name = name

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}
	doc.Spec.RealmID = realmName

	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return result, errdefs.ErrSpaceNameRequired
	}
	doc.Spec.SpaceID = spaceName

	// Get stack document
	getResult, err := b.GetStack(doc)
	if err != nil {
		return result, err
	}
	if !getResult.MetadataExists {
		return result, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
	}

	stackDoc := getResult.StackDoc
	if stackDoc == nil {
		stackDoc = doc
	}

	result = PurgeStackResult{
		StackDoc: stackDoc,
		Deleted:  []string{},
		Purged:   []string{},
	}

	// If cascade, purge all cells first
	if cascade {
		var cells []*v1beta1.CellDoc
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return result, fmt.Errorf("failed to list cells: %w", err)
		}
		for _, cell := range cells {
			_, err = b.PurgeCell(cell, force, false)
			if err != nil {
				return result, fmt.Errorf("failed to purge cell %q: %w", cell.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("cell:%s", cell.Metadata.Name))
		}
	} else if !force {
		// Validate no cells exist
		var cells []*v1beta1.CellDoc
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return result, fmt.Errorf("failed to list cells: %w", err)
		}
		if len(cells) > 0 {
			return result, fmt.Errorf("%w: stack %q has %d cell(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(cells))
		}
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteStack(doc, force, cascade)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.Deleted = deleteResult.Deleted
		if deleteResult.StackDoc != nil {
			result.StackDoc = deleteResult.StackDoc
		}
	}

	// Perform comprehensive purge
	// Convert external stack to internal for runner.PurgeStack
	internalStack, _, convertErr := apischeme.NormalizeStack(*getResult.StackDoc)
	if convertErr != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-conversion-error:%v", convertErr))
	} else {
		if err = b.runner.PurgeStack(internalStack); err != nil {
			result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		} else {
			result.Purged = append(result.Purged, "cni-resources", "orphaned-containers", "all-metadata")
		}
	}

	return result, nil
}

// PurgeCell purges a cell with comprehensive cleanup. Always purges all containers first.
// If force is true, skips validation (currently unused but recorded for auditing).
func (b *Exec) PurgeCell(doc *v1beta1.CellDoc, force, cascade bool) (PurgeCellResult, error) {
	var result PurgeCellResult

	if doc == nil {
		return result, errdefs.ErrCellNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrCellNameRequired
	}
	doc.Metadata.Name = name

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}
	doc.Spec.RealmID = realmName

	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return result, errdefs.ErrSpaceNameRequired
	}
	doc.Spec.SpaceID = spaceName

	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return result, errdefs.ErrStackNameRequired
	}
	doc.Spec.StackID = stackName

	// Ensure cell exists and capture latest state.
	getResult, err := b.GetCell(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return result, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				name,
				realmName,
				spaceName,
				stackName,
			)
		}
		return result, err
	}

	result = PurgeCellResult{
		CellDoc: getResult.CellDoc,
		Force:   force,
		Cascade: cascade,
		Deleted: []string{},
		Purged:  []string{},
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteCell(doc)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.ContainersDeleted = deleteResult.ContainersDeleted
		result.CgroupDeleted = deleteResult.CgroupDeleted
		result.MetadataDeleted = deleteResult.MetadataDeleted

		if deleteResult.ContainersDeleted {
			result.Deleted = append(result.Deleted, "containers")
		}
		if deleteResult.CgroupDeleted {
			result.Deleted = append(result.Deleted, "cgroup")
		}
		if deleteResult.MetadataDeleted {
			result.Deleted = append(result.Deleted, "metadata")
		}
		if deleteResult.CellDoc != nil {
			result.CellDoc = deleteResult.CellDoc
		}
	}

	// Perform comprehensive purge
	// Convert external cell to internal for runner.PurgeCell
	internalCell, _, convertErr := apischeme.NormalizeCell(*getResult.CellDoc)
	if convertErr != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-conversion-error:%v", convertErr))
		result.PurgeSucceeded = false
	} else {
		if err = b.runner.PurgeCell(internalCell); err != nil {
			result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
			result.PurgeSucceeded = false
		} else {
			result.Purged = append(result.Purged, "cni-resources", "orphaned-containers", "all-metadata")
			result.PurgeSucceeded = true
		}
	}

	return result, nil
}

// PurgeContainer purges a single container with comprehensive cleanup. Cascade flag is not applicable.
func (b *Exec) PurgeContainer(doc *v1beta1.ContainerDoc) (PurgeContainerResult, error) {
	var result PurgeContainerResult

	sanitizedDoc, name, realmName, spaceName, stackName, cellName, err := normalizeContainerDoc(doc)
	if err != nil {
		return result, err
	}

	result = PurgeContainerResult{
		ContainerDoc: sanitizedDoc,
		Deleted:      []string{},
		Purged:       []string{},
	}

	// Get cell to find container metadata
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
			return result, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				cellName,
				realmName,
				spaceName,
				stackName,
			)
		}
		return result, err
	}
	result.CellMetadataExists = getResult.MetadataExists

	cellDoc = getResult.CellDoc
	if cellDoc == nil {
		return result, fmt.Errorf("cell %q not found", cellName)
	}

	// Check if container exists in cell metadata by name (ID now stores just the container name)
	var foundContainer *v1beta1.ContainerSpec
	for i := range cellDoc.Spec.Containers {
		if cellDoc.Spec.Containers[i].ID == name {
			foundContainer = &cellDoc.Spec.Containers[i]
			break
		}
	}

	// Perform standard delete if container is in metadata
	if foundContainer != nil {
		result.ContainerExists = true
		var deleteResult DeleteContainerResult
		deleteResult, err = b.DeleteContainer(result.ContainerDoc)
		if err != nil {
			result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
		} else {
			result.Deleted = append(result.Deleted, deleteResult.Deleted...)
			if deleteResult.ContainerDoc != nil {
				result.ContainerDoc = deleteResult.ContainerDoc
			}
			result.CellMetadataExists = deleteResult.CellMetadataExists
			result.ContainerExists = deleteResult.ContainerExists
		}
	} else {
		result.ContainerExists = false
	}

	// Get realm to pass to runner.PurgeContainer
	realmDocInput := &v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmName,
		},
	}
	realmGetResult, err := b.GetRealm(realmDocInput)
	if err != nil {
		return result, fmt.Errorf("failed to get realm: %w", err)
	}
	if realmGetResult.RealmDoc == nil {
		return result, fmt.Errorf("realm %q not found", realmName)
	}

	// Convert external realm to internal for runner.PurgeContainer
	internalRealm, _, convertErr := apischeme.NormalizeRealm(*realmGetResult.RealmDoc)
	if convertErr != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-conversion-error:%v", convertErr))
	} else {
		// Use container name directly for containerd operations
		if err = b.runner.PurgeContainer(internalRealm, name); err != nil {
			result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		} else {
			result.Purged = append(result.Purged, "cni-resources", "ipam-allocation", "cache-entries")
		}
	}

	return result, nil
}

func normalizeContainerDoc(
	doc *v1beta1.ContainerDoc,
) (*v1beta1.ContainerDoc, string, string, string, string, string, error) {
	if doc == nil {
		return nil, "", "", "", "", "", errdefs.ErrContainerNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return nil, "", "", "", "", "", errdefs.ErrContainerNameRequired
	}

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return nil, "", "", "", "", "", errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return nil, "", "", "", "", "", errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return nil, "", "", "", "", "", errdefs.ErrStackNameRequired
	}

	cellName := strings.TrimSpace(doc.Spec.CellID)
	if cellName == "" {
		return nil, "", "", "", "", "", errdefs.ErrCellNameRequired
	}

	labels := make(map[string]string, len(doc.Metadata.Labels))
	for k, v := range doc.Metadata.Labels {
		labels[k] = v
	}

	sanitized := &v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: v1beta1.ContainerSpec{
			ID:      name,
			RealmID: realmName,
			SpaceID: spaceName,
			StackID: stackName,
			CellID:  cellName,
		},
	}

	return sanitized, name, realmName, spaceName, stackName, cellName, nil
}
