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

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type BootstrapReport struct {
	RunPath string

	RealmName                          string
	RealmContainerdNamespace           string
	RealmMetadataExistsPre             bool
	RealmMetadataExistsPost            bool
	RealmContainerdNamespaceExistsPre  bool
	RealmContainerdNamespaceExistsPost bool
	RealmContainerdNamespaceCreated    bool
	RealmCreated                       bool
	RealmCgroupExistsPre               bool
	RealmCgroupExistsPost              bool
	RealmCgroupCreated                 bool

	// CNI bootstrap details
	CniConfigDir           string
	CniCacheDir            string
	CniBinDir              string
	CniConfigDirExistsPre  bool
	CniCacheDirExistsPre   bool
	CniBinDirExistsPre     bool
	CniConfigDirCreated    bool
	CniCacheDirCreated     bool
	CniBinDirCreated       bool
	CniConfigDirExistsPost bool
	CniCacheDirExistsPost  bool
	CniBinDirExistsPost    bool

	SpaceName                 string
	SpaceCNINetworkName       string
	SpaceMetadataExistsPre    bool
	SpaceMetadataExistsPost   bool
	SpaceCNINetworkExistsPre  bool
	SpaceCNINetworkExistsPost bool
	SpaceCNINetworkCreated    bool
	SpaceCreated              bool
	SpaceCgroupExistsPre      bool
	SpaceCgroupExistsPost     bool
	SpaceCgroupCreated        bool

	StackName               string
	StackMetadataExistsPre  bool
	StackMetadataExistsPost bool
	StackCreated            bool
	StackCgroupExistsPre    bool
	StackCgroupExistsPost   bool
	StackCgroupCreated      bool

	CellName                    string
	CellMetadataExistsPre       bool
	CellMetadataExistsPost      bool
	CellCreated                 bool
	CellCgroupExistsPre         bool
	CellCgroupExistsPost        bool
	CellCgroupCreated           bool
	CellRootContainerExistsPre  bool
	CellRootContainerExistsPost bool
	CellRootContainerCreated    bool
	CellStartedPre              bool
	CellStartedPost             bool
	CellStarted                 bool
}

func (b *Exec) bootstrapRealm(report BootstrapReport) (BootstrapReport, error) {
	var err error

	// Pre-state
	realmDocPre, err := b.runner.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{Name: consts.KukeonRealmName},
		Spec:     v1beta1.RealmSpec{Namespace: consts.KukeonRealmNamespace},
	})
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			report.RealmMetadataExistsPre = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		report.RealmMetadataExistsPre = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(realmDocPre)
		if cgroupErr != nil {
			return report, fmt.Errorf("failed to check if realm cgroup exists: %w", cgroupErr)
		}
		report.RealmCgroupExistsPre = cgroupExists
	}
	nsExistsPre, err := b.runner.ExistsRealmContainerdNamespace(consts.KukeonRealmNamespace)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}
	report.RealmContainerdNamespaceExistsPre = nsExistsPre

	_, err = b.runner.CreateRealm(
		&v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{
				Name: consts.KukeonRealmName,
				Labels: map[string]string{
					consts.KukeonRealmLabelKey: consts.KukeonRealmNamespace,
				},
			},
			Spec: v1beta1.RealmSpec{
				Namespace: consts.KukeonRealmNamespace,
			},
		},
	)
	if err != nil && !errors.Is(err, errdefs.ErrNamespaceAlreadyExists) {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCreateRealm, err)
	}

	// Post-state
	realmDocPost, err := b.runner.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{Name: consts.KukeonRealmName},
		Spec:     v1beta1.RealmSpec{Namespace: consts.KukeonRealmNamespace},
	})
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			report.RealmMetadataExistsPost = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		report.RealmMetadataExistsPost = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(realmDocPost)
		if cgroupErr != nil {
			return report, fmt.Errorf("failed to check if realm cgroup exists: %w", cgroupErr)
		}
		report.RealmCgroupExistsPost = cgroupExists
	}
	nsExistsPost, err := b.runner.ExistsRealmContainerdNamespace(consts.KukeonRealmNamespace)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}
	report.RealmContainerdNamespaceExistsPost = nsExistsPost

	report.RealmCgroupCreated = !report.RealmCgroupExistsPre && report.RealmCgroupExistsPost

	return report, nil
}

func (b *Exec) bootstrapSpace(report BootstrapReport) (BootstrapReport, error) {
	var err error
	spaceDoc := &v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: consts.KukeonSpaceName,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: consts.KukeonRealmName,
				consts.KukeonSpaceLabelKey: consts.KukeonSpaceName,
			},
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: consts.KukeonRealmName,
		},
	}
	spaceName := spaceDoc.Metadata.Name
	spaceNet, err := naming.BuildSpaceNetworkName(spaceDoc.Spec.RealmID, spaceName)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}

	// Fill static fields
	report.SpaceName = spaceName
	report.SpaceCNINetworkName = spaceNet

	// Try to read existing space metadata (best-effort)
	spaceDocPre, err := b.runner.GetSpace(spaceDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			report.SpaceMetadataExistsPre = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		report.SpaceMetadataExistsPre = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(spaceDocPre)
		if cgroupErr != nil {
			return report, fmt.Errorf("failed to check if space cgroup exists: %w", cgroupErr)
		}
		report.SpaceCgroupExistsPre = cgroupExists
	}

	// Ensure network exists for the space (createSpace will also ensure)
	exists, err := b.runner.ExistsSpaceCNIConfig(spaceDoc)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	report.SpaceCNINetworkExistsPre = exists

	// Create or reconcile space
	_, err = b.runner.CreateSpace(spaceDoc)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCreateSpace, err)
	}

	// Post-state checks
	spaceDocPost, err := b.runner.GetSpace(spaceDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			report.SpaceMetadataExistsPost = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		report.SpaceMetadataExistsPost = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(spaceDocPost)
		if cgroupErr != nil {
			return report, fmt.Errorf("failed to check if space cgroup exists: %w", cgroupErr)
		}
		report.SpaceCgroupExistsPost = cgroupExists
	}
	exists, err = b.runner.ExistsSpaceCNIConfig(spaceDoc)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	report.SpaceCNINetworkExistsPost = exists

	// Derived outcomes
	report.SpaceCreated = !report.SpaceMetadataExistsPre && report.SpaceMetadataExistsPost
	report.SpaceCNINetworkCreated = !report.SpaceCNINetworkExistsPre && report.SpaceCNINetworkExistsPost
	report.SpaceCgroupCreated = !report.SpaceCgroupExistsPre && report.SpaceCgroupExistsPost

	return report, nil
}

func (b *Exec) bootstrapStack(report BootstrapReport) (BootstrapReport, error) {
	var err error
	stackDoc := &v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: consts.KukeonStackName,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: consts.KukeonRealmName,
				consts.KukeonSpaceLabelKey: consts.KukeonSpaceName,
				consts.KukeonStackLabelKey: consts.KukeonStackName,
			},
		},
		Spec: v1beta1.StackSpec{
			ID:      consts.KukeonStackName,
			RealmID: consts.KukeonRealmName,
			SpaceID: consts.KukeonSpaceName,
		},
	}
	stackName := stackDoc.Metadata.Name

	// Fill static fields
	report.StackName = stackName

	// Try to read existing stack metadata (best-effort)
	stackDocPre, err := b.runner.GetStack(stackDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			report.StackMetadataExistsPre = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		report.StackMetadataExistsPre = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(stackDocPre)
		if cgroupErr != nil {
			return report, fmt.Errorf("failed to check if stack cgroup exists: %w", cgroupErr)
		}
		report.StackCgroupExistsPre = cgroupExists
	}

	// Create or reconcile stack
	_, err = b.runner.CreateStack(stackDoc)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCreateStack, err)
	}

	// Post-state checks
	stackDocPost, err := b.runner.GetStack(stackDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			report.StackMetadataExistsPost = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		report.StackMetadataExistsPost = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(stackDocPost)
		if cgroupErr != nil {
			return report, fmt.Errorf("failed to check if stack cgroup exists: %w", cgroupErr)
		}
		report.StackCgroupExistsPost = cgroupExists
	}

	// Derived outcomes
	report.StackCreated = !report.StackMetadataExistsPre && report.StackMetadataExistsPost
	report.StackCgroupCreated = !report.StackCgroupExistsPre && report.StackCgroupExistsPost

	return report, nil
}

func (b *Exec) bootstrapCell(report BootstrapReport) (BootstrapReport, error) {
	var err error
	cellDoc := &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: consts.KukeonCellName,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: consts.KukeonRealmName,
				consts.KukeonSpaceLabelKey: consts.KukeonSpaceName,
				consts.KukeonStackLabelKey: consts.KukeonStackName,
				consts.KukeonCellLabelKey:  consts.KukeonCellName,
			},
		},
		Spec: v1beta1.CellSpec{
			ID:      consts.KukeonCellName,
			RealmID: consts.KukeonRealmName,
			SpaceID: consts.KukeonSpaceName,
			StackID: consts.KukeonStackName,
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "debian", // Store just the container name, not the full ID
					RealmID: consts.KukeonRealmName,
					SpaceID: consts.KukeonSpaceName,
					StackID: consts.KukeonStackName,
					CellID:  consts.KukeonCellName,
					Image:   "docker.io/library/debian:stable",
					// Image:   "docker.io/jonlabelle/network-tools:latest",
					Command: "sleep",
					Args:    []string{"infinity"},
				},
			},
		},
	}
	cellName := cellDoc.Metadata.Name

	// Fill static fields
	report.CellName = cellName

	// Try to read existing cell metadata (best-effort)
	cellDocPre, err := b.runner.GetCell(cellDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			report.CellMetadataExistsPre = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		report.CellMetadataExistsPre = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(cellDocPre)
		if cgroupErr != nil {
			return report, fmt.Errorf("failed to check if cell cgroup exists: %w", cgroupErr)
		}
		report.CellCgroupExistsPre = cgroupExists
		// Check if root container exists pre (only if cell exists)
		rootExistsPre, rootErr := b.runner.ExistsCellRootContainer(cellDocPre)
		if rootErr != nil {
			return report, fmt.Errorf("failed to check if root container exists: %w", rootErr)
		}
		report.CellRootContainerExistsPre = rootExistsPre
		// Check if containers are started pre (best-effort, only if cell exists)
		// For now, we assume containers are not started pre
		// This could be enhanced later to check task status
		report.CellStartedPre = false
	}

	// Create or reconcile cell
	_, err = b.runner.CreateCell(cellDoc)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
	}

	// Start cell containers
	err = b.runner.StartCell(cellDoc)
	if err != nil {
		return report, fmt.Errorf("failed to start cell containers: %w", err)
	}

	// Post-state checks
	cellDocPost, err := b.runner.GetCell(cellDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			report.CellMetadataExistsPost = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		report.CellMetadataExistsPost = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(cellDocPost)
		if cgroupErr != nil {
			return report, fmt.Errorf("failed to check if cell cgroup exists: %w", cgroupErr)
		}
		report.CellCgroupExistsPost = cgroupExists
		// Check if root container exists post
		rootExistsPost, rootErr := b.runner.ExistsCellRootContainer(cellDocPost)
		if rootErr != nil {
			return report, fmt.Errorf("failed to check if root container exists: %w", rootErr)
		}
		report.CellRootContainerExistsPost = rootExistsPost
		// Check if containers are started post
		// After StartCell succeeds, containers should be started
		report.CellStartedPost = true
	}

	// Derived outcomes
	report.CellCreated = !report.CellMetadataExistsPre && report.CellMetadataExistsPost
	report.CellCgroupCreated = !report.CellCgroupExistsPre && report.CellCgroupExistsPost
	report.CellRootContainerCreated = !report.CellRootContainerExistsPre && report.CellRootContainerExistsPost
	report.CellStarted = !report.CellStartedPre && report.CellStartedPost

	return report, nil
}

func (b *Exec) bootstrapCNI(report BootstrapReport) (BootstrapReport, error) {
	// Use defaults by passing empty values
	cniRep, err := b.runner.BootstrapCNI("", "", "")
	if err != nil {
		return report, err
	}
	report.CniConfigDir = cniRep.CniConfigDir
	report.CniCacheDir = cniRep.CniCacheDir
	report.CniBinDir = cniRep.CniBinDir
	report.CniConfigDirExistsPre = cniRep.ConfigDirExistsPre
	report.CniCacheDirExistsPre = cniRep.CacheDirExistsPre
	report.CniBinDirExistsPre = cniRep.BinDirExistsPre
	report.CniConfigDirCreated = cniRep.ConfigDirCreated
	report.CniCacheDirCreated = cniRep.CacheDirCreated
	report.CniBinDirCreated = cniRep.BinDirCreated
	report.CniConfigDirExistsPost = cniRep.ConfigDirExistsPost
	report.CniCacheDirExistsPost = cniRep.CacheDirExistsPost
	report.CniBinDirExistsPost = cniRep.BinDirExistsPost
	return report, nil
}
