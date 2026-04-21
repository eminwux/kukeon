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
	"os"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// RealmSection holds the bootstrap outcome for a single realm.
type RealmSection struct {
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
}

// SpaceSection holds the bootstrap outcome for a single space.
type SpaceSection struct {
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
}

// StackSection holds the bootstrap outcome for a single stack.
type StackSection struct {
	StackName               string
	StackMetadataExistsPre  bool
	StackMetadataExistsPost bool
	StackCreated            bool
	StackCgroupExistsPre    bool
	StackCgroupExistsPost   bool
	StackCgroupCreated      bool
}

// CellSection holds the bootstrap outcome for a single cell.
type CellSection struct {
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

type BootstrapReport struct {
	RunPath string

	KukeonCgroupExistsPre  bool
	KukeonCgroupExistsPost bool
	KukeonCgroupCreated    bool

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

	// Default user hierarchy (realm=default, space=default, stack=default, no cell).
	DefaultRealm RealmSection
	DefaultSpace SpaceSection
	DefaultStack StackSection

	// System hierarchy (realm=kuke-system, space=kukeon, stack=kukeon, cell=kukeond).
	SystemRealm RealmSection
	SystemSpace SpaceSection
	SystemStack StackSection
	SystemCell  CellSection

	// KukeondImage is the resolved image reference provisioned for the kukeond
	// container inside the system cell.
	KukeondImage string
}

func (b *Exec) bootstrapKukeonCgroup(report BootstrapReport) (BootstrapReport, error) {
	existsPre, created, err := b.runner.EnsureKukeonRootCgroup()
	if err != nil {
		return report, err
	}
	report.KukeonCgroupExistsPre = existsPre
	report.KukeonCgroupExistsPost = true
	report.KukeonCgroupCreated = created
	return report, nil
}

func (b *Exec) bootstrapRealm(section *RealmSection, realmName, realmNamespace string) error {
	section.RealmName = realmName
	section.RealmContainerdNamespace = realmNamespace

	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}

	internalRealmPre, err := b.runner.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			section.RealmMetadataExistsPre = false
		} else {
			return fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		section.RealmMetadataExistsPre = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(internalRealmPre)
		if cgroupErr != nil {
			return fmt.Errorf("failed to check if realm cgroup exists: %w", cgroupErr)
		}
		section.RealmCgroupExistsPre = cgroupExists
	}
	nsExistsPre, err := b.runner.ExistsRealmContainerdNamespace(realmNamespace)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}
	section.RealmContainerdNamespaceExistsPre = nsExistsPre

	realmDoc := &v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmName,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: realmNamespace,
			},
		},
		Spec: v1beta1.RealmSpec{
			Namespace: realmNamespace,
		},
	}

	realm, _, err := apischeme.NormalizeRealm(*realmDoc)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	if section.RealmMetadataExistsPre {
		if _, err = b.runner.EnsureRealm(internalRealmPre); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrCreateRealm, err)
		}
	} else {
		if _, err = b.runner.CreateRealm(realm); err != nil && !errors.Is(err, errdefs.ErrNamespaceAlreadyExists) {
			return fmt.Errorf("%w: %w", errdefs.ErrCreateRealm, err)
		}
	}

	internalRealmPost, err := b.runner.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			section.RealmMetadataExistsPost = false
		} else {
			return fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		section.RealmMetadataExistsPost = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(internalRealmPost)
		if cgroupErr != nil {
			return fmt.Errorf("failed to check if realm cgroup exists: %w", cgroupErr)
		}
		section.RealmCgroupExistsPost = cgroupExists
	}
	nsExistsPost, err := b.runner.ExistsRealmContainerdNamespace(realmNamespace)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}
	section.RealmContainerdNamespaceExistsPost = nsExistsPost

	section.RealmCreated = !section.RealmMetadataExistsPre && section.RealmMetadataExistsPost
	section.RealmContainerdNamespaceCreated = !section.RealmContainerdNamespaceExistsPre &&
		section.RealmContainerdNamespaceExistsPost
	section.RealmCgroupCreated = !section.RealmCgroupExistsPre && section.RealmCgroupExistsPost

	return nil
}

func (b *Exec) bootstrapSpace(section *SpaceSection, realmName, spaceName string) error {
	section.SpaceName = spaceName

	spaceDoc := &v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: spaceName,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: realmName,
				consts.KukeonSpaceLabelKey: spaceName,
			},
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: realmName,
		},
	}

	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: spaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmName,
		},
	}

	internalSpacePre, err := b.runner.GetSpace(lookupSpace)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			section.SpaceMetadataExistsPre = false
		} else {
			return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		section.SpaceMetadataExistsPre = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(internalSpacePre)
		if cgroupErr != nil {
			return fmt.Errorf("failed to check if space cgroup exists: %w", cgroupErr)
		}
		section.SpaceCgroupExistsPre = cgroupExists
	}

	exists, err := b.runner.ExistsSpaceCNIConfig(lookupSpace)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	section.SpaceCNINetworkExistsPre = exists

	space, _, err := apischeme.NormalizeSpace(*spaceDoc)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	spaceNet, err := naming.BuildSpaceNetworkName(space.Spec.RealmName, spaceName)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	section.SpaceCNINetworkName = spaceNet

	if section.SpaceMetadataExistsPre {
		if _, err = b.runner.EnsureSpace(internalSpacePre); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrCreateSpace, err)
		}
	} else {
		if _, err = b.runner.CreateSpace(space); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrCreateSpace, err)
		}
	}

	internalSpacePost, err := b.runner.GetSpace(lookupSpace)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			section.SpaceMetadataExistsPost = false
		} else {
			return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		section.SpaceMetadataExistsPost = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(internalSpacePost)
		if cgroupErr != nil {
			return fmt.Errorf("failed to check if space cgroup exists: %w", cgroupErr)
		}
		section.SpaceCgroupExistsPost = cgroupExists
	}
	exists, err = b.runner.ExistsSpaceCNIConfig(lookupSpace)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	section.SpaceCNINetworkExistsPost = exists

	section.SpaceCreated = !section.SpaceMetadataExistsPre && section.SpaceMetadataExistsPost
	section.SpaceCNINetworkCreated = !section.SpaceCNINetworkExistsPre && section.SpaceCNINetworkExistsPost
	section.SpaceCgroupCreated = !section.SpaceCgroupExistsPre && section.SpaceCgroupExistsPost

	return nil
}

func (b *Exec) bootstrapStack(section *StackSection, realmName, spaceName, stackName string) error {
	section.StackName = stackName

	stackDoc := &v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: stackName,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: realmName,
				consts.KukeonSpaceLabelKey: spaceName,
				consts.KukeonStackLabelKey: stackName,
			},
		},
		Spec: v1beta1.StackSpec{
			ID:      stackName,
			RealmID: realmName,
			SpaceID: spaceName,
		},
	}

	lookupStackPre := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: stackName,
		},
		Spec: intmodel.StackSpec{
			RealmName: realmName,
			SpaceName: spaceName,
		},
	}
	internalStackPre, err := b.runner.GetStack(lookupStackPre)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			section.StackMetadataExistsPre = false
		} else {
			return fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		section.StackMetadataExistsPre = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(internalStackPre)
		if cgroupErr != nil {
			return fmt.Errorf("failed to check if stack cgroup exists: %w", cgroupErr)
		}
		section.StackCgroupExistsPre = cgroupExists
	}

	stack, _, err := apischeme.NormalizeStack(*stackDoc)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	if section.StackMetadataExistsPre {
		if _, err = b.runner.EnsureStack(internalStackPre); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrCreateStack, err)
		}
	} else {
		if _, err = b.runner.CreateStack(stack); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrCreateStack, err)
		}
	}

	lookupStackPost := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: stack.Metadata.Name,
		},
		Spec: intmodel.StackSpec{
			RealmName: stack.Spec.RealmName,
			SpaceName: stack.Spec.SpaceName,
		},
	}
	internalStackPost, err := b.runner.GetStack(lookupStackPost)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			section.StackMetadataExistsPost = false
		} else {
			return fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		section.StackMetadataExistsPost = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(internalStackPost)
		if cgroupErr != nil {
			return fmt.Errorf("failed to check if stack cgroup exists: %w", cgroupErr)
		}
		section.StackCgroupExistsPost = cgroupExists
	}

	section.StackCreated = !section.StackMetadataExistsPre && section.StackMetadataExistsPost
	section.StackCgroupCreated = !section.StackCgroupExistsPre && section.StackCgroupExistsPost

	return nil
}

// kukeondCellDoc builds the CellDoc describing the kukeond system cell.
// The host runPath is bind-mounted into the container at the same path and
// passed via --run-path so that kukeond reads/writes the same metadata store
// that kuke uses in --no-daemon mode. The host containerd socket directory is
// also bind-mounted and --containerd-socket is forwarded so kukeond can reach
// the same containerd daemon as kuke does on the host.
func kukeondCellDoc(image, socketPath, runPath, containerdSocket string) *v1beta1.CellDoc {
	args := []string{"serve", "--socket", socketPath}
	if runPath != "" {
		args = append(args, "--run-path", runPath)
	}
	if containerdSocket != "" {
		args = append(args, "--containerd-socket", containerdSocket)
	}

	sockDir := socketDir(socketPath)
	volumes := []v1beta1.VolumeMount{
		{Source: sockDir, Target: sockDir},
	}
	if runPath != "" && runPath != sockDir {
		volumes = append(volumes, v1beta1.VolumeMount{Source: runPath, Target: runPath})
	}
	if containerdSocket != "" {
		ctrdDir := socketDir(containerdSocket)
		if ctrdDir != sockDir && ctrdDir != runPath {
			volumes = append(volumes, v1beta1.VolumeMount{Source: ctrdDir, Target: ctrdDir})
		}
	}

	return &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: consts.KukeSystemCellName,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: consts.KukeSystemRealmName,
				consts.KukeonSpaceLabelKey: consts.KukeSystemSpaceName,
				consts.KukeonStackLabelKey: consts.KukeSystemStackName,
				consts.KukeonCellLabelKey:  consts.KukeSystemCellName,
			},
		},
		Spec: v1beta1.CellSpec{
			ID:      consts.KukeSystemCellName,
			RealmID: consts.KukeSystemRealmName,
			SpaceID: consts.KukeSystemSpaceName,
			StackID: consts.KukeSystemStackName,
			Containers: []v1beta1.ContainerSpec{
				{
					ID:         consts.KukeSystemContainerName,
					RealmID:    consts.KukeSystemRealmName,
					SpaceID:    consts.KukeSystemSpaceName,
					StackID:    consts.KukeSystemStackName,
					CellID:     consts.KukeSystemCellName,
					Image:      image,
					Command:    "/bin/kukeond",
					Args:       args,
					Volumes:    volumes,
					Privileged: true,
				},
			},
		},
	}
}

// ensureSocketDir creates the parent directory of the kukeond unix socket so
// that the bind-mount into the kukeond container has a source to mount.
func ensureSocketDir(socketPath string) error {
	dir := socketDir(socketPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create kukeond socket dir %q: %w", dir, err)
	}
	return nil
}

// socketDir returns the parent directory of the kukeond unix socket path.
func socketDir(socketPath string) string {
	for i := len(socketPath) - 1; i >= 0; i-- {
		if socketPath[i] == '/' {
			if i == 0 {
				return "/"
			}
			return socketPath[:i]
		}
	}
	return "/run/kukeon"
}

func (b *Exec) bootstrapCell(section *CellSection, cellDoc *v1beta1.CellDoc) error {
	section.CellName = cellDoc.Metadata.Name

	cell, _, err := apischeme.NormalizeCell(*cellDoc)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	lookupCellPre := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cell.Metadata.Name,
		},
		Spec: intmodel.CellSpec{
			RealmName: cell.Spec.RealmName,
			SpaceName: cell.Spec.SpaceName,
			StackName: cell.Spec.StackName,
		},
	}
	internalCellPre, err := b.runner.GetCell(lookupCellPre)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			section.CellMetadataExistsPre = false
		} else {
			return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		section.CellMetadataExistsPre = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(internalCellPre)
		if cgroupErr != nil {
			return fmt.Errorf("failed to check if cell cgroup exists: %w", cgroupErr)
		}
		section.CellCgroupExistsPre = cgroupExists
		rootExistsPre, rootErr := b.runner.ExistsCellRootContainer(internalCellPre)
		if rootErr != nil {
			return fmt.Errorf("failed to check if root container exists: %w", rootErr)
		}
		section.CellRootContainerExistsPre = rootExistsPre
		section.CellStartedPre = false
	}

	var ensuredCell intmodel.Cell
	if section.CellMetadataExistsPre {
		ensuredCell, err = b.runner.EnsureCell(internalCellPre)
		if err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
		}
	} else {
		ensuredCell, err = b.runner.CreateCell(cell)
		if err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
		}
	}

	if _, err = b.runner.StartCell(ensuredCell); err != nil {
		return fmt.Errorf("failed to start cell containers: %w", err)
	}

	lookupCellPost := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cell.Metadata.Name,
		},
		Spec: intmodel.CellSpec{
			RealmName: cell.Spec.RealmName,
			SpaceName: cell.Spec.SpaceName,
			StackName: cell.Spec.StackName,
		},
	}
	internalCellPost, err := b.runner.GetCell(lookupCellPost)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			section.CellMetadataExistsPost = false
		} else {
			return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		section.CellMetadataExistsPost = true
		cgroupExists, cgroupErr := b.runner.ExistsCgroup(internalCellPost)
		if cgroupErr != nil {
			return fmt.Errorf("failed to check if cell cgroup exists: %w", cgroupErr)
		}
		section.CellCgroupExistsPost = cgroupExists
		rootExistsPost, rootErr := b.runner.ExistsCellRootContainer(internalCellPost)
		if rootErr != nil {
			return fmt.Errorf("failed to check if root container exists: %w", rootErr)
		}
		section.CellRootContainerExistsPost = rootExistsPost
		section.CellStartedPost = true
	}

	section.CellCreated = !section.CellMetadataExistsPre && section.CellMetadataExistsPost
	section.CellCgroupCreated = !section.CellCgroupExistsPre && section.CellCgroupExistsPost
	section.CellRootContainerCreated = !section.CellRootContainerExistsPre && section.CellRootContainerExistsPost
	section.CellStarted = !section.CellStartedPre && section.CellStartedPost

	return nil
}

func (b *Exec) bootstrapCNI(report BootstrapReport) (BootstrapReport, error) {
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
