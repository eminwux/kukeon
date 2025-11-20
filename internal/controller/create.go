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
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// CreateRealmOptions captures the inputs required to create or reconcile a realm.
type CreateRealmOptions struct {
	Name      string
	Namespace string
	Labels    map[string]string
}

// CreateRealmResult reports the reconciliation outcomes for a realm.
type CreateRealmResult struct {
	Name   string
	Labels map[string]string

	Namespace                     string
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

func (b *Exec) CreateRealm(opts CreateRealmOptions) (CreateRealmResult, error) {
	var res CreateRealmResult

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	namespace := strings.TrimSpace(opts.Namespace)
	if namespace == "" {
		namespace = name
	}
	defaultLabels := map[string]string{
		consts.KukeonRealmLabelKey: namespace,
	}
	labels := mergeLabels(defaultLabels, opts.Labels)

	res.Name = name
	res.Namespace = namespace
	res.Labels = labels

	doc := &v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: v1beta1.RealmSpec{
			Namespace: namespace,
		},
	}

	realmDocPre, err := b.runner.GetRealm(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		res.MetadataExistsPre = true
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(realmDocPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if realm cgroup exists: %w", err)
		}
	}

	res.ContainerdNamespaceExistsPre, err = b.runner.ExistsRealmContainerdNamespace(namespace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}

	if _, err = b.runner.CreateRealm(doc); err != nil && !errors.Is(err, errdefs.ErrNamespaceAlreadyExists) {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateRealm, err)
	}

	realmDocPost, err := b.runner.GetRealm(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		res.MetadataExistsPost = true
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(realmDocPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if realm cgroup exists: %w", err)
		}
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

// CreateSpaceOptions captures inputs required to create a space.
type CreateSpaceOptions struct {
	Name      string
	RealmName string
	Labels    map[string]string
}

// CreateSpaceResult reports reconciliation outcomes for a space.
type CreateSpaceResult struct {
	Name        string
	RealmName   string
	Labels      map[string]string
	NetworkName string

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

func (b *Exec) CreateSpace(opts CreateSpaceOptions) (CreateSpaceResult, error) {
	var res CreateSpaceResult

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	realm := strings.TrimSpace(opts.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	defaultLabels := map[string]string{
		consts.KukeonRealmLabelKey: realm,
		consts.KukeonSpaceLabelKey: name,
	}
	labels := mergeLabels(defaultLabels, opts.Labels)

	res.Name = name
	res.RealmName = realm
	res.Labels = labels

	doc := &v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: realm,
		},
	}

	networkName, err := naming.BuildSpaceNetworkName(doc)
	if err != nil {
		return res, err
	}
	res.NetworkName = networkName

	spaceDocPre, err := b.runner.GetSpace(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		res.MetadataExistsPre = true
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(spaceDocPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if space cgroup exists: %w", err)
		}
	}

	res.CNINetworkExistsPre, err = b.runner.ExistsSpaceCNIConfig(doc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	if _, err = b.runner.CreateSpace(doc); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateSpace, err)
	}

	spaceDocPost, err := b.runner.GetSpace(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		res.MetadataExistsPost = true
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(spaceDocPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if space cgroup exists: %w", err)
		}
	}

	res.CNINetworkExistsPost, err = b.runner.ExistsSpaceCNIConfig(doc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CNINetworkCreated = !res.CNINetworkExistsPre && res.CNINetworkExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost

	return res, nil
}

// CreateStackOptions captures inputs required to create a stack.
type CreateStackOptions struct {
	Name      string
	RealmName string
	SpaceName string
	Labels    map[string]string
}

// CreateStackResult reports reconciliation outcomes for a stack.
type CreateStackResult struct {
	Name      string
	RealmName string
	SpaceName string
	Labels    map[string]string

	MetadataExistsPre  bool
	MetadataExistsPost bool
	CgroupExistsPre    bool
	CgroupExistsPost   bool
	CgroupCreated      bool
	Created            bool
}

func (b *Exec) CreateStack(opts CreateStackOptions) (CreateStackResult, error) {
	var res CreateStackResult

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return res, errdefs.ErrStackNameRequired
	}
	realm := strings.TrimSpace(opts.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(opts.SpaceName)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	defaultLabels := map[string]string{
		consts.KukeonRealmLabelKey: realm,
		consts.KukeonSpaceLabelKey: space,
		consts.KukeonStackLabelKey: name,
	}
	labels := mergeLabels(defaultLabels, opts.Labels)

	res.Name = name
	res.RealmName = realm
	res.SpaceName = space
	res.Labels = labels

	doc := &v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: v1beta1.StackSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
		},
	}

	stackDocPre, err := b.runner.GetStack(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		res.MetadataExistsPre = true
		// Verify space exists before checking cgroup to provide better error message
		_, spaceErr := b.runner.GetSpace(&v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{
				Name: space,
			},
		})
		if spaceErr != nil {
			return res, fmt.Errorf("space %q not found at run-path %q: %w", space, b.opts.RunPath, spaceErr)
		}
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(stackDocPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}
	}

	if _, err = b.runner.CreateStack(doc); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateStack, err)
	}

	stackDocPost, err := b.runner.GetStack(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		res.MetadataExistsPost = true
		// Verify space exists before checking cgroup to provide better error message
		_, spaceErr := b.runner.GetSpace(&v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{
				Name: space,
			},
		})
		if spaceErr != nil {
			return res, fmt.Errorf("space %q not found at run-path %q: %w", space, b.opts.RunPath, spaceErr)
		}
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(stackDocPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost

	return res, nil
}

// CreateCellOptions captures inputs required to create a cell.
type CreateCellOptions struct {
	Name      string
	RealmName string
	SpaceName string
	StackName string
	Labels    map[string]string

	Containers []v1beta1.ContainerSpec
}

// CreateCellResult reports reconciliation outcomes for a cell.
type CreateCellResult struct {
	Name      string
	RealmName string
	SpaceName string
	StackName string
	Labels    map[string]string

	MetadataExistsPre  bool
	MetadataExistsPost bool
	CgroupExistsPre    bool
	CgroupExistsPost   bool
	CgroupCreated      bool
	PauseExistsPre     bool
	PauseExistsPost    bool
	PauseCreated       bool
	StartedPre         bool
	StartedPost        bool
	Started            bool
	Created            bool
}

func (b *Exec) CreateCell(opts CreateCellOptions) (CreateCellResult, error) {
	var res CreateCellResult

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return res, errdefs.ErrCellNameRequired
	}
	realm := strings.TrimSpace(opts.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(opts.SpaceName)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(opts.StackName)
	if stack == "" {
		return res, errdefs.ErrStackNameRequired
	}

	defaultLabels := map[string]string{
		consts.KukeonRealmLabelKey: realm,
		consts.KukeonSpaceLabelKey: space,
		consts.KukeonStackLabelKey: stack,
		consts.KukeonCellLabelKey:  name,
	}
	labels := mergeLabels(defaultLabels, opts.Labels)

	res.Name = name
	res.RealmName = realm
	res.SpaceName = space
	res.StackName = stack
	res.Labels = labels

	doc := &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: v1beta1.CellSpec{
			ID:         name,
			RealmID:    realm,
			SpaceID:    space,
			StackID:    stack,
			Containers: ensureContainerOwnership(opts.Containers, realm, space, stack, name),
		},
	}

	cellDocPre, err := b.runner.GetCell(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		res.MetadataExistsPre = true
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(cellDocPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if cell cgroup exists: %w", err)
		}
		res.PauseExistsPre, err = b.runner.ExistsCellPauseContainer(cellDocPre)
		if err != nil {
			return res, fmt.Errorf("failed to check pause container: %w", err)
		}
		res.StartedPre = false
	}

	if _, err = b.runner.CreateCell(doc); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
	}

	if err = b.runner.StartCell(doc); err != nil {
		return res, fmt.Errorf("failed to start cell containers: %w", err)
	}

	cellDocPost, err := b.runner.GetCell(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		res.MetadataExistsPost = true
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(cellDocPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if cell cgroup exists: %w", err)
		}
		res.PauseExistsPost, err = b.runner.ExistsCellPauseContainer(cellDocPost)
		if err != nil {
			return res, fmt.Errorf("failed to check pause container: %w", err)
		}
		res.StartedPost = true
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost
	res.PauseCreated = !res.PauseExistsPre && res.PauseExistsPost
	res.Started = !res.StartedPre && res.StartedPost

	return res, nil
}

// CreateContainerOptions captures inputs required to add a workload container to a cell.
type CreateContainerOptions struct {
	RealmName string
	SpaceName string
	StackName string
	CellName  string

	ContainerName string
	Image         string
	Command       string
	Args          []string
	Labels        map[string]string
}

// CreateContainerResult reports reconciliation outcomes for container creation within a cell.
type CreateContainerResult struct {
	RealmName string
	SpaceName string
	StackName string
	CellName  string

	ContainerName string
	ContainerID   string

	CellMetadataExistsPre  bool
	CellMetadataExistsPost bool
	ContainerExistsPre     bool
	ContainerExistsPost    bool
	ContainerCreated       bool
	Started                bool
}

func (b *Exec) CreateContainer(opts CreateContainerOptions) (CreateContainerResult, error) {
	var res CreateContainerResult

	realm := strings.TrimSpace(opts.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(opts.SpaceName)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(opts.StackName)
	if stack == "" {
		return res, errdefs.ErrStackNameRequired
	}
	cell := strings.TrimSpace(opts.CellName)
	if cell == "" {
		return res, errdefs.ErrCellNameRequired
	}
	containerName := strings.TrimSpace(opts.ContainerName)
	if containerName == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		return res, errdefs.ErrInvalidImage
	}

	containerID := naming.BuildContainerName(realm, space, cell, containerName)

	res.RealmName = realm
	res.SpaceName = space
	res.StackName = stack
	res.CellName = cell
	res.ContainerName = containerName
	res.ContainerID = containerID

	doc := &v1beta1.CellDoc{
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
					ID:      containerID,
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
					CellID:  cell,
					Image:   image,
					Command: opts.Command,
					Args:    opts.Args,
				},
			},
		},
	}

	cellDocPre, err := b.runner.GetCell(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return res, fmt.Errorf("cell %q not found", cell)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	res.CellMetadataExistsPre = true
	res.ContainerExistsPre = containerSpecExists(cellDocPre.Spec.Containers, containerID)

	if _, err = b.runner.CreateCell(doc); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
	}

	if err = b.runner.StartCell(doc); err != nil {
		return res, fmt.Errorf("failed to start container %s: %w", containerID, err)
	}

	cellDocPost, err := b.runner.GetCell(doc)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	res.CellMetadataExistsPost = true
	res.ContainerExistsPost = containerSpecExists(cellDocPost.Spec.Containers, containerID)
	res.ContainerCreated = !res.ContainerExistsPre && res.ContainerExistsPost
	res.Started = true

	return res, nil
}

func mergeLabels(base, overrides map[string]string) map[string]string {
	merged := make(map[string]string)
	for k, v := range base {
		if strings.TrimSpace(k) == "" {
			continue
		}
		merged[k] = v
	}
	for k, v := range overrides {
		if strings.TrimSpace(k) == "" {
			continue
		}
		merged[k] = v
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
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
