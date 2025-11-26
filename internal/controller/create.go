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
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// CreateRealmResult reports the reconciliation outcomes for a realm.
type CreateRealmResult struct {
	RealmDoc *v1beta1.RealmDoc

	MetadataExistsPre             bool
	MetadataExistsPost            bool
	CgroupExistsPre               bool
	CgroupExistsPost              bool
	CgroupCreated                 bool
	ContainerdNamespaceExistsPre  bool
	ContainerdNamespaceExistsPost bool
	ContainerdNamespaceCreated    bool
	Created                       bool
}

func (b *Exec) CreateRealm(doc *v1beta1.RealmDoc) (CreateRealmResult, error) {
	var res CreateRealmResult

	if doc == nil {
		return res, errdefs.ErrRealmNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	namespace := strings.TrimSpace(doc.Spec.Namespace)
	if namespace == "" {
		namespace = name
		// Update doc with default namespace
		doc.Spec.Namespace = namespace
	}

	// Ensure default labels are set
	if doc.Metadata.Labels == nil {
		doc.Metadata.Labels = make(map[string]string)
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonRealmLabelKey] = namespace
	}

	// Build minimal internal realm for GetRealm lookup
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: name,
		},
	}

	internalRealmPre, err := b.runner.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		res.MetadataExistsPre = true
		// Convert internal realm back to external for ExistsCgroup
		realmDocPre, convertErr := apischeme.BuildRealmExternalFromInternal(internalRealmPre, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(&realmDocPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if realm cgroup exists: %w", err)
		}
	}

	res.ContainerdNamespaceExistsPre, err = b.runner.ExistsRealmContainerdNamespace(namespace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}

	// Convert external doc to internal model at boundary
	realm, version, err := apischeme.NormalizeRealm(*doc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Call runner with internal type
	resultRealm, err := b.runner.CreateRealm(realm)
	if err != nil && !errors.Is(err, errdefs.ErrNamespaceAlreadyExists) {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateRealm, err)
	}

	// Convert result back to external model at boundary
	resultDoc, err := apischeme.BuildRealmExternalFromInternal(resultRealm, version)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Build minimal internal realm for GetRealm lookup (after creation)
	internalRealmPost, err := b.runner.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		res.MetadataExistsPost = true
		// Convert internal realm back to external for ExistsCgroup
		realmDocPost, convertErr := apischeme.BuildRealmExternalFromInternal(internalRealmPost, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(&realmDocPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if realm cgroup exists: %w", err)
		}
		// Use the result from CreateRealm instead of GetRealm to ensure consistency
		res.RealmDoc = &resultDoc
	}

	res.ContainerdNamespaceExistsPost, err = b.runner.ExistsRealmContainerdNamespace(namespace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost
	res.ContainerdNamespaceCreated = !res.ContainerdNamespaceExistsPre && res.ContainerdNamespaceExistsPost

	return res, nil
}

// CreateSpaceResult reports reconciliation outcomes for a space.
type CreateSpaceResult struct {
	SpaceDoc *v1beta1.SpaceDoc

	MetadataExistsPre    bool
	MetadataExistsPost   bool
	CgroupExistsPre      bool
	CgroupExistsPost     bool
	CgroupCreated        bool
	CNINetworkExistsPre  bool
	CNINetworkExistsPost bool
	CNINetworkCreated    bool
	Created              bool
}

func (b *Exec) CreateSpace(doc *v1beta1.SpaceDoc) (CreateSpaceResult, error) {
	var res CreateSpaceResult

	if doc == nil {
		return res, errdefs.ErrSpaceNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	realm := strings.TrimSpace(doc.Spec.RealmID)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	// Ensure default labels are set
	if doc.Metadata.Labels == nil {
		doc.Metadata.Labels = make(map[string]string)
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonRealmLabelKey] = realm
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonSpaceLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonSpaceLabelKey] = name
	}

	// Build minimal internal space for GetSpace lookup
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: name,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realm,
		},
	}

	internalSpacePre, err := b.runner.GetSpace(lookupSpace)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		res.MetadataExistsPre = true
		// Convert internal space back to external for ExistsCgroup
		spaceDocPre, convertErr := apischeme.BuildSpaceExternalFromInternal(internalSpacePre, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(&spaceDocPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if space cgroup exists: %w", err)
		}
	}

	res.CNINetworkExistsPre, err = b.runner.ExistsSpaceCNIConfig(lookupSpace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	// Convert external doc to internal model at boundary
	space, version, err := apischeme.NormalizeSpace(*doc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Call runner with internal type
	resultSpace, err := b.runner.CreateSpace(space)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateSpace, err)
	}

	// Convert result back to external model at boundary
	resultDoc, err := apischeme.BuildSpaceExternalFromInternal(resultSpace, version)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Build minimal internal space for GetSpace lookup (after creation)
	internalSpacePost, err := b.runner.GetSpace(lookupSpace)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		res.MetadataExistsPost = true
		// Convert internal space back to external for ExistsCgroup
		spaceDocPost, convertErr := apischeme.BuildSpaceExternalFromInternal(internalSpacePost, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(&spaceDocPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if space cgroup exists: %w", err)
		}
		// Use the result from CreateSpace instead of GetSpace to ensure consistency
		res.SpaceDoc = &resultDoc
	}

	res.CNINetworkExistsPost, err = b.runner.ExistsSpaceCNIConfig(lookupSpace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CNINetworkCreated = !res.CNINetworkExistsPre && res.CNINetworkExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost

	return res, nil
}

// CreateStackResult reports reconciliation outcomes for a stack.
type CreateStackResult struct {
	StackDoc *v1beta1.StackDoc

	MetadataExistsPre  bool
	MetadataExistsPost bool
	CgroupExistsPre    bool
	CgroupExistsPost   bool
	CgroupCreated      bool
	Created            bool
}

func (b *Exec) CreateStack(doc *v1beta1.StackDoc) (CreateStackResult, error) {
	var res CreateStackResult

	if doc == nil {
		return res, errdefs.ErrStackNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrStackNameRequired
	}
	realm := strings.TrimSpace(doc.Spec.RealmID)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(doc.Spec.SpaceID)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	// Ensure default labels are set
	if doc.Metadata.Labels == nil {
		doc.Metadata.Labels = make(map[string]string)
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonRealmLabelKey] = realm
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonSpaceLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonSpaceLabelKey] = space
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonStackLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonStackLabelKey] = name
	}

	// Ensure Spec.ID is set
	if doc.Spec.ID == "" {
		doc.Spec.ID = name
	}

	// Build minimal internal stack for GetStack lookup
	lookupStackPre := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
		},
		Spec: intmodel.StackSpec{
			RealmName: realm,
			SpaceName: space,
		},
	}
	internalStackPre, err := b.runner.GetStack(lookupStackPre)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		res.MetadataExistsPre = true
		// Verify space exists before checking cgroup to provide better error message
		verifySpace := intmodel.Space{
			Metadata: intmodel.SpaceMetadata{
				Name: space,
			},
			Spec: intmodel.SpaceSpec{
				RealmName: realm,
			},
		}
		_, spaceErr := b.runner.GetSpace(verifySpace)
		if spaceErr != nil {
			return res, fmt.Errorf("space %q not found at run-path %q: %w", space, b.opts.RunPath, spaceErr)
		}
		// Convert internal stack back to external for ExistsCgroup
		stackDocPre, convertErr := apischeme.BuildStackExternalFromInternal(internalStackPre, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(&stackDocPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}
	}

	// Convert external doc to internal model at boundary
	stack, version, err := apischeme.NormalizeStack(*doc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Call runner with internal type
	resultStack, err := b.runner.CreateStack(stack)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateStack, err)
	}

	// Convert result back to external model at boundary
	resultDoc, err := apischeme.BuildStackExternalFromInternal(resultStack, version)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Build minimal internal stack for GetStack lookup
	lookupStackPost := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
		},
		Spec: intmodel.StackSpec{
			RealmName: realm,
			SpaceName: space,
		},
	}
	internalStackPost, err := b.runner.GetStack(lookupStackPost)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		res.MetadataExistsPost = true
		// Verify space exists before checking cgroup to provide better error message
		verifySpace := intmodel.Space{
			Metadata: intmodel.SpaceMetadata{
				Name: space,
			},
			Spec: intmodel.SpaceSpec{
				RealmName: realm,
			},
		}
		_, spaceErr := b.runner.GetSpace(verifySpace)
		if spaceErr != nil {
			return res, fmt.Errorf("space %q not found at run-path %q: %w", space, b.opts.RunPath, spaceErr)
		}
		// Convert internal stack back to external for ExistsCgroup
		stackDocPost, convertErr := apischeme.BuildStackExternalFromInternal(internalStackPost, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(&stackDocPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}
		// Use the result from CreateStack instead of GetStack to ensure consistency
		res.StackDoc = &resultDoc
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost

	return res, nil
}

// CreateCellResult reports reconciliation outcomes for a cell.
type CreateCellResult struct {
	CellDoc *v1beta1.CellDoc

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

func (b *Exec) CreateCell(doc *v1beta1.CellDoc) (CreateCellResult, error) {
	var res CreateCellResult

	if doc == nil {
		return res, errdefs.ErrCellNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrCellNameRequired
	}
	realm := strings.TrimSpace(doc.Spec.RealmID)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(doc.Spec.SpaceID)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(doc.Spec.StackID)
	if stack == "" {
		return res, errdefs.ErrStackNameRequired
	}

	// Ensure default labels are set
	if doc.Metadata.Labels == nil {
		doc.Metadata.Labels = make(map[string]string)
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonRealmLabelKey] = realm
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonSpaceLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonSpaceLabelKey] = space
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonStackLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonStackLabelKey] = stack
	}
	if _, exists := doc.Metadata.Labels[consts.KukeonCellLabelKey]; !exists {
		doc.Metadata.Labels[consts.KukeonCellLabelKey] = name
	}

	// Ensure Spec.ID is set
	if doc.Spec.ID == "" {
		doc.Spec.ID = name
	}

	// Ensure container ownership
	doc.Spec.Containers = ensureContainerOwnership(doc.Spec.Containers, realm, space, stack, name)

	preContainerExists := make(map[string]bool)

	lookupCellPre := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: doc.Metadata.Name,
		},
		Spec: intmodel.CellSpec{
			RealmName: doc.Spec.RealmID,
			SpaceName: doc.Spec.SpaceID,
			StackName: doc.Spec.StackID,
		},
	}
	internalCellPre, err := b.runner.GetCell(lookupCellPre)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		res.MetadataExistsPre = true
		// Convert internal cell back to external for ExistsCgroup and ExistsCellRootContainer
		cellDocPreExternal, convertErr := apischeme.BuildCellExternalFromInternal(internalCellPre, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(&cellDocPreExternal)
		if err != nil {
			return res, fmt.Errorf("failed to check if cell cgroup exists: %w", err)
		}
		res.RootContainerExistsPre, err = b.runner.ExistsCellRootContainer(internalCellPre)
		if err != nil {
			return res, fmt.Errorf("failed to check root container: %w", err)
		}
		for _, container := range cellDocPreExternal.Spec.Containers {
			id := strings.TrimSpace(container.ID)
			if id != "" {
				preContainerExists[id] = true
			}
		}
		res.StartedPre = false
	}

	// Convert external doc to internal model at boundary
	cell, version, err := apischeme.NormalizeCell(*doc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Call runner with internal type
	resultCell, err := b.runner.CreateCell(cell)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
	}

	// Convert result back to external model at boundary
	resultDoc, err := apischeme.BuildCellExternalFromInternal(resultCell, version)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Convert external cell to internal for runner.StartCell (use the same internal cell from CreateCell)
	// Since resultCell is already internal, we can use it directly
	if err = b.runner.StartCell(resultCell); err != nil {
		return res, fmt.Errorf("failed to start cell containers: %w", err)
	}

	postContainerExists := make(map[string]bool)

	lookupCellPost := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: doc.Metadata.Name,
		},
		Spec: intmodel.CellSpec{
			RealmName: doc.Spec.RealmID,
			SpaceName: doc.Spec.SpaceID,
			StackName: doc.Spec.StackID,
		},
	}
	internalCellPost, err := b.runner.GetCell(lookupCellPost)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		res.MetadataExistsPost = true
		// Convert internal cell back to external for ExistsCgroup and ExistsCellRootContainer
		cellDocPostExternal, convertErr := apischeme.BuildCellExternalFromInternal(internalCellPost, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(&cellDocPostExternal)
		if err != nil {
			return res, fmt.Errorf("failed to check if cell cgroup exists: %w", err)
		}
		res.RootContainerExistsPost, err = b.runner.ExistsCellRootContainer(internalCellPost)
		if err != nil {
			return res, fmt.Errorf("failed to check root container: %w", err)
		}
		for _, container := range cellDocPostExternal.Spec.Containers {
			id := strings.TrimSpace(container.ID)
			if id != "" {
				postContainerExists[id] = true
			}
		}
		res.StartedPost = true
		// Use the result from CreateCell instead of GetCell to ensure consistency
		res.CellDoc = &resultDoc
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost
	res.RootContainerCreated = !res.RootContainerExistsPre && res.RootContainerExistsPost
	res.Started = !res.StartedPre && res.StartedPost

	for _, container := range doc.Spec.Containers {
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

// CreateContainerResult reports reconciliation outcomes for container creation within a cell.
type CreateContainerResult struct {
	ContainerDoc *v1beta1.ContainerDoc

	CellMetadataExistsPre  bool
	CellMetadataExistsPost bool
	ContainerExistsPre     bool
	ContainerExistsPost    bool
	ContainerCreated       bool
	Started                bool
}

func (b *Exec) CreateContainer(doc *v1beta1.ContainerDoc) (CreateContainerResult, error) {
	var res CreateContainerResult

	if doc == nil {
		return res, errdefs.ErrContainerNameRequired
	}

	containerName := strings.TrimSpace(doc.Metadata.Name)
	if containerName == "" {
		containerName = strings.TrimSpace(doc.Spec.ID)
	}
	if containerName == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	if strings.TrimSpace(doc.Spec.ID) == "" {
		doc.Spec.ID = containerName
	}

	realm := strings.TrimSpace(doc.Spec.RealmID)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(doc.Spec.SpaceID)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(doc.Spec.StackID)
	if stack == "" {
		return res, errdefs.ErrStackNameRequired
	}
	cell := strings.TrimSpace(doc.Spec.CellID)
	if cell == "" {
		return res, errdefs.ErrCellNameRequired
	}
	image := strings.TrimSpace(doc.Spec.Image)
	if image == "" {
		return res, errdefs.ErrInvalidImage
	}

	cellDoc := &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: cell,
		},
		Spec: v1beta1.CellSpec{
			ID:      cell,
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      doc.Spec.ID, // Store just the container name, not the full ID
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
					CellID:  cell,
					Image:   image,
					Command: doc.Spec.Command,
					Args:    doc.Spec.Args,
				},
			},
		},
	}

	// Log the container spec being created
	b.logger.DebugContext(
		b.ctx,
		"creating container in cell",
		"containerName", containerName,
		"cell", cell,
		"realm", realm,
		"space", space,
		"stack", stack,
		"image", image,
		"command", doc.Spec.Command,
		"containerSpecID", doc.Spec.ID,
	)

	lookupCellPre := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellDoc.Metadata.Name,
		},
		Spec: intmodel.CellSpec{
			RealmName: cellDoc.Spec.RealmID,
			SpaceName: cellDoc.Spec.SpaceID,
			StackName: cellDoc.Spec.StackID,
		},
	}
	internalCellPre, err := b.runner.GetCell(lookupCellPre)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return res, fmt.Errorf("cell %q not found", cell)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	res.CellMetadataExistsPre = true
	// Convert internal cell back to external to access Containers field
	cellDocPreExternal, convertErr := apischeme.BuildCellExternalFromInternal(internalCellPre, apischeme.VersionV1Beta1)
	if convertErr != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}
	res.ContainerExistsPre = containerSpecExists(cellDocPreExternal.Spec.Containers, containerName)

	// Log before calling CreateCell
	b.logger.DebugContext(
		b.ctx,
		"calling CreateCell to merge container",
		"containerName", containerName,
		"cell", cell,
		"containerExistsPre", res.ContainerExistsPre,
		"containersInCellDoc", len(cellDoc.Spec.Containers),
	)

	// Convert external doc to internal model at boundary
	cellInternal, version, err := apischeme.NormalizeCell(*cellDoc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// CreateCell returns the Cell with merged containers - we must use this
	// returned cell for StartCell to ensure we're starting the containers
	// that were actually created
	resultCell, err := b.runner.CreateCell(cellInternal)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
	}

	// Convert result back to external model at boundary for StartCell
	cellDocCreated, err := apischeme.BuildCellExternalFromInternal(resultCell, version)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Log after CreateCell returns
	b.logger.DebugContext(
		b.ctx,
		"CreateCell returned successfully",
		"containerName", containerName,
		"cell", cell,
		"containersInCreatedCellDoc", len(cellDocCreated.Spec.Containers),
	)

	// Use the CellDoc returned from CreateCell, which has the containers properly merged
	b.logger.DebugContext(
		b.ctx,
		"calling StartCell to start containers",
		"containerName", containerName,
		"cell", cell,
		"containersToStart", len(cellDocCreated.Spec.Containers),
	)

	// Use the same internal cell from CreateCell for runner.StartCell
	if err = b.runner.StartCell(resultCell); err != nil {
		return res, fmt.Errorf("failed to start container %s: %w", containerName, err)
	}

	lookupCellPost := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellDoc.Metadata.Name,
		},
		Spec: intmodel.CellSpec{
			RealmName: cellDoc.Spec.RealmID,
			SpaceName: cellDoc.Spec.SpaceID,
			StackName: cellDoc.Spec.StackID,
		},
	}
	internalCellPost, err := b.runner.GetCell(lookupCellPost)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	res.CellMetadataExistsPost = true
	// Convert internal cell back to external to access Containers field
	cellDocPostExternal, convertErr := apischeme.BuildCellExternalFromInternal(
		internalCellPost,
		apischeme.VersionV1Beta1,
	)
	if convertErr != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}
	res.ContainerExistsPost = containerSpecExists(cellDocPostExternal.Spec.Containers, containerName)
	res.ContainerCreated = !res.ContainerExistsPre && res.ContainerExistsPost
	res.Started = true

	// Construct ContainerDoc from the created container spec
	var containerSpec *v1beta1.ContainerSpec
	for i := range cellDocPostExternal.Spec.Containers {
		if cellDocPostExternal.Spec.Containers[i].ID == containerName {
			containerSpec = &cellDocPostExternal.Spec.Containers[i]
			break
		}
	}

	if containerSpec != nil {
		// Use labels from doc if provided, otherwise empty map
		labels := doc.Metadata.Labels
		if labels == nil {
			labels = make(map[string]string)
		}

		res.ContainerDoc = &v1beta1.ContainerDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindContainer,
			Metadata: v1beta1.ContainerMetadata{
				Name:   containerName,
				Labels: labels,
			},
			Spec: *containerSpec,
			Status: v1beta1.ContainerStatus{
				State: v1beta1.ContainerStateReady,
			},
		}
	}

	return res, nil
}

func ensureContainerOwnership(
	containers []v1beta1.ContainerSpec,
	realm, space, stack, cell string,
) []v1beta1.ContainerSpec {
	if len(containers) == 0 {
		return containers
	}
	result := make([]v1beta1.ContainerSpec, len(containers))
	for i, c := range containers {
		c.RealmID = realm
		c.SpaceID = space
		c.StackID = stack
		c.CellID = cell
		result[i] = c
	}
	return result
}

func containerSpecExists(specs []v1beta1.ContainerSpec, id string) bool {
	for _, spec := range specs {
		if spec.ID == id {
			return true
		}
	}
	return false
}
