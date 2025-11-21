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
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type Runner interface {
	BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error)

	GetRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error)
	CreateRealm(spec *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error)
	ExistsRealmContainerdNamespace(namespace string) (bool, error)
	DeleteRealm(doc *v1beta1.RealmDoc) error

	GetSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error)
	CreateSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error)
	ExistsSpaceCNIConfig(doc *v1beta1.SpaceDoc) (bool, error)
	DeleteSpace(doc *v1beta1.SpaceDoc) error

	GetCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error)
	CreateCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error)
	StartCell(doc *v1beta1.CellDoc) error
	StopCell(doc *v1beta1.CellDoc) error
	StopContainer(doc *v1beta1.CellDoc, containerID string) error
	KillCell(doc *v1beta1.CellDoc) error
	KillContainer(doc *v1beta1.CellDoc, containerID string) error
	DeleteContainer(doc *v1beta1.CellDoc, containerID string) error
	UpdateCellMetadata(doc *v1beta1.CellDoc) error
	ExistsCellPauseContainer(doc *v1beta1.CellDoc) (bool, error)
	DeleteCell(doc *v1beta1.CellDoc) error

	GetStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error)
	CreateStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error)
	DeleteStack(doc *v1beta1.StackDoc) error

	ExistsCgroup(doc any) (bool, error)

	PurgeRealm(doc *v1beta1.RealmDoc) error
	PurgeSpace(doc *v1beta1.SpaceDoc) error
	PurgeStack(doc *v1beta1.StackDoc) error
	PurgeCell(doc *v1beta1.CellDoc) error
	PurgeContainer(containerID, namespace string) error
}

type Exec struct {
	ctx    context.Context
	logger *slog.Logger
	opts   Options

	ctrClient ctr.Client

	cniConf *cni.Conf
}

type Options struct {
	ContainerdSocket string
	RunPath          string
	CniConf          cni.Conf
}

func NewRunner(ctx context.Context, logger *slog.Logger, opts Options) Runner {
	return &Exec{
		ctx:     ctx,
		logger:  logger,
		opts:    opts,
		cniConf: &opts.CniConf,
	}
}

func (r *Exec) GetRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	// Get realm metadata
	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, doc.Metadata.Name)
	realmDoc, err := metadata.ReadMetadata[v1beta1.RealmDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrRealmNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}
	return &realmDoc, nil
}

func (r *Exec) CreateRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	r.logger.Debug("run-path", "run-path", r.opts.RunPath)

	rDoc, err := r.GetRealm(doc)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Realm found, check if namespace exists
	if rDoc != nil {
		doc, err = r.ensureRealmContainerdNamespace(rDoc)
		if err != nil {
			return nil, err
		}
		return r.ensureRealmCgroup(doc)
	}

	// Realm not found, normalize request to internal then emit external doc for storage
	var internalRealm intmodel.Realm
	var version v1beta1.Version
	internalRealm, version, err = apischeme.NormalizeRealm(*doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	internalRealm.Status.State = intmodel.RealmStateCreating
	rDocNewExt, err := apischeme.BuildRealmExternalFromInternal(internalRealm, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	rDocNew := &rDocNewExt

	return r.provisionNewRealm(rDocNew)
}

func (r *Exec) ExistsCellPauseContainer(doc *v1beta1.CellDoc) (bool, error) {
	if doc == nil {
		return false, errdefs.ErrCellNotFound
	}

	cellName := doc.Metadata.Name
	if cellName == "" {
		return false, errdefs.ErrCellNotFound
	}

	cellID := doc.Spec.ID
	if cellID == "" {
		return false, errdefs.ErrCellIDRequired
	}

	realmID := doc.Spec.RealmID
	if realmID == "" {
		return false, errdefs.ErrRealmNameRequired
	}

	spaceID := doc.Spec.SpaceID
	if spaceID == "" {
		return false, errdefs.ErrSpaceNameRequired
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Generate container ID with cell identifier for uniqueness
	// Need to get spaceID and stackID from doc
	stackID := doc.Spec.StackID
	if stackID == "" {
		return false, errdefs.ErrStackNameRequired
	}
	containerID, err := naming.BuildPauseContainerName(spaceID, stackID, cellID)
	if err != nil {
		return false, fmt.Errorf("failed to build pause container name: %w", err)
	}

	// Check if container exists
	exists, err := r.ctrClient.ExistsContainer(r.ctx, containerID)
	if err != nil {
		return false, fmt.Errorf("failed to check if pause container exists: %w", err)
	}

	return exists, nil
}

func (r *Exec) ExistsCgroup(doc any) (bool, error) {
	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	var spec ctr.CgroupSpec
	var err error

	// Build cgroup spec based on doc type
	switch d := doc.(type) {
	case *v1beta1.RealmDoc:
		if d == nil {
			return false, errdefs.ErrRealmNotFound
		}
		spec = cgroups.DefaultRealmSpec(d)

	case *v1beta1.SpaceDoc:
		if d == nil {
			return false, errdefs.ErrSpaceNotFound
		}
		if d.Spec.RealmID == "" {
			return false, errdefs.ErrRealmNameRequired
		}
		realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{
				Name: d.Spec.RealmID,
			},
		})
		if realmErr != nil {
			return false, fmt.Errorf("failed to get realm: %w", realmErr)
		}
		spec = cgroups.DefaultSpaceSpec(realmDoc, d)

	case *v1beta1.StackDoc:
		if d == nil {
			return false, errdefs.ErrStackNotFound
		}
		if d.Spec.RealmID == "" {
			return false, errdefs.ErrRealmNameRequired
		}
		if d.Spec.SpaceID == "" {
			return false, errdefs.ErrSpaceNameRequired
		}
		realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{
				Name: d.Spec.RealmID,
			},
		})
		if realmErr != nil {
			return false, fmt.Errorf("failed to get realm: %w", realmErr)
		}
		spaceDoc, spaceErr := r.GetSpace(&v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{
				Name: d.Spec.SpaceID,
			},
			Spec: v1beta1.SpaceSpec{
				RealmID: d.Spec.RealmID,
			},
		})
		if spaceErr != nil {
			return false, fmt.Errorf("failed to get space: %w", spaceErr)
		}
		spec = cgroups.DefaultStackSpec(realmDoc, spaceDoc, d)

	case *v1beta1.CellDoc:
		if d == nil {
			return false, errdefs.ErrCellNotFound
		}
		if d.Spec.RealmID == "" {
			return false, errdefs.ErrRealmNameRequired
		}
		if d.Spec.SpaceID == "" {
			return false, errdefs.ErrSpaceNameRequired
		}
		if d.Spec.StackID == "" {
			return false, errdefs.ErrStackNameRequired
		}
		realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{
				Name: d.Spec.RealmID,
			},
		})
		if realmErr != nil {
			return false, fmt.Errorf("failed to get realm: %w", realmErr)
		}
		spaceDoc, spaceErr := r.GetSpace(&v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{
				Name: d.Spec.SpaceID,
			},
			Spec: v1beta1.SpaceSpec{
				RealmID: d.Spec.RealmID,
			},
		})
		if spaceErr != nil {
			return false, fmt.Errorf("failed to get space: %w", spaceErr)
		}
		stackDoc, stackErr := r.GetStack(&v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{
				Name: d.Spec.StackID,
			},
			Spec: v1beta1.StackSpec{
				RealmID: d.Spec.RealmID,
				SpaceID: d.Spec.SpaceID,
			},
		})
		if stackErr != nil {
			return false, fmt.Errorf("failed to get stack: %w", stackErr)
		}
		spec = cgroups.DefaultCellSpec(realmDoc, spaceDoc, stackDoc, d)

	default:
		return false, fmt.Errorf("unsupported doc type: %T", doc)
	}

	// Build the cgroup path
	spec, _, err = r.buildCgroupPath(spec)
	if err != nil {
		return false, fmt.Errorf("failed to build cgroup path: %w", err)
	}

	// Check if cgroup exists
	_, err = r.ctrClient.LoadCgroup(spec.Group, spec.Mountpoint)
	if err != nil {
		// Check if error is "cgroup path does not exist"
		if err.Error() == "cgroup path does not exist" {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if cgroup exists: %w", err)
	}

	return true, nil
}

func (r *Exec) ExistsRealmContainerdNamespace(namespace string) (bool, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()
	return r.ctrClient.ExistsNamespace(namespace)
}

func (r *Exec) GetSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error) {
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	// Get space metadata
	metadataFilePath := fs.SpaceMetadataPath(r.opts.RunPath, realmName, doc.Metadata.Name)
	spaceDoc, err := metadata.ReadMetadata[v1beta1.SpaceDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrSpaceNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}
	return &spaceDoc, nil
}

func (r *Exec) CreateSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	sDoc, err := r.GetSpace(doc)
	if err != nil && !errors.Is(err, errdefs.ErrSpaceNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	realmName := doc.Spec.RealmID
	if sDoc != nil && sDoc.Spec.RealmID != "" {
		realmName = sDoc.Spec.RealmID
	}
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmName,
		},
	})
	if realmErr != nil {
		return nil, realmErr
	}

	// Space found, ensure CNI config exists
	if sDoc != nil {
		spaceDocEnsured, ensureErr := r.ensureSpaceCNIConfig(sDoc)
		if ensureErr != nil {
			return nil, ensureErr
		}
		return r.ensureSpaceCgroup(spaceDocEnsured, realmDoc)
	}

	// Space not found, normalize request to internal then emit external doc for storage
	var internalSpace intmodel.Space
	var version v1beta1.Version
	internalSpace, version, err = apischeme.NormalizeSpace(*doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	internalSpace.Status.State = intmodel.SpaceStateCreating
	sDocNewExt, err := apischeme.BuildSpaceExternalFromInternal(internalSpace, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	sDocNew := &sDocNewExt

	return r.provisionNewSpace(sDocNew)
}

func (r *Exec) ExistsSpaceCNIConfig(doc *v1beta1.SpaceDoc) (bool, error) {
	if doc == nil {
		return false, errdefs.ErrSpaceDocRequired
	}
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return false, errdefs.ErrRealmNameRequired
	}
	mgr, err := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, realmName, doc.Metadata.Name)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	networkName, err := naming.BuildSpaceNetworkName(doc)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	exists, _, err := mgr.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	return exists, nil
}

func (r *Exec) GetStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error) {
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	// Get stack metadata
	metadataFilePath := fs.StackMetadataPath(r.opts.RunPath, realmName, spaceName, doc.Metadata.Name)
	stackDoc, err := metadata.ReadMetadata[v1beta1.StackDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrStackNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}
	return &stackDoc, nil
}

func (r *Exec) CreateStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	sDoc, err := r.GetStack(doc)
	if err != nil && !errors.Is(err, errdefs.ErrStackNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Stack found, ensure cgroup exists
	if sDoc != nil {
		return r.ensureStackCgroup(sDoc)
	}

	// Stack not found, create new stack
	return r.provisionNewStack(doc)
}

func (r *Exec) GetCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error) {
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return nil, errdefs.ErrStackNameRequired
	}
	// Get cell metadata
	metadataFilePath := fs.CellMetadataPath(r.opts.RunPath, realmName, spaceName, stackName, doc.Metadata.Name)
	cellDoc, err := metadata.ReadMetadata[v1beta1.CellDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrCellNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}
	return &cellDoc, nil
}

func (r *Exec) CreateCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	cDoc, err := r.GetCell(doc)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Cell found, ensure cgroup exists
	if cDoc != nil {
		cellDocEnsured, ensureErr := r.ensureCellCgroup(cDoc)
		if ensureErr != nil {
			return nil, ensureErr
		}
		// Merge containers from the new doc into the existing cell document
		// This ensures containers specified in the new doc are created even if
		// they weren't in the stored cell document
		if len(doc.Spec.Containers) > 0 {
			// Create a map of existing container IDs to avoid duplicates
			existingContainerIDs := make(map[string]bool)
			for _, container := range cellDocEnsured.Spec.Containers {
				existingContainerIDs[container.ID] = true
			}
			// Add containers from the new doc that don't already exist
			for _, container := range doc.Spec.Containers {
				if !existingContainerIDs[container.ID] {
					cellDocEnsured.Spec.Containers = append(cellDocEnsured.Spec.Containers, container)
				}
			}
		}
		_, ensureErr = r.ensureCellContainers(cellDocEnsured)
		if ensureErr != nil {
			return nil, ensureErr
		}
		return cellDocEnsured, nil
	}

	// Cell not found, create new cell
	return r.provisionNewCell(doc)
}

func (r *Exec) BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error) {
	// Delegate to cni package bootstrap; empty params will default.
	return cni.BootstrapCNI(cfgDir, cacheDir, binDir)
}

func (r *Exec) DeleteCell(doc *v1beta1.CellDoc) error {
	if doc == nil {
		return errdefs.ErrCellNotFound
	}

	// Get the cell document to access all containers
	cellDoc, err := r.GetCell(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			// Idempotent: cell doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	var realmDoc *v1beta1.RealmDoc
	realmDoc, err = r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: cellDoc.Spec.RealmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Delete all containers in the cell (workload + pause)
	ctrCtx := context.Background()
	for _, containerSpec := range cellDoc.Spec.Containers {
		// Build container ID using hierarchical format
		var containerID string
		containerID, err = naming.BuildContainerName(
			containerSpec.SpaceID,
			containerSpec.StackID,
			containerSpec.CellID,
			containerSpec.ID,
		)
		if err != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to build container name, skipping",
				"container",
				containerSpec.ID,
				"error",
				err,
			)
			continue
		}

		// Use container name with UUID for containerd operations
		// Stop and delete the container
		_, err = r.ctrClient.StopContainer(ctrCtx, containerID, ctr.StopContainerOptions{})
		if err != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container, continuing with deletion",
				"container",
				containerID,
				"error",
				err,
			)
		}

		err = r.ctrClient.DeleteContainer(ctrCtx, containerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})
		if err != nil {
			r.logger.WarnContext(r.ctx, "failed to delete container", "container", containerID, "error", err)
			// Continue with other containers
		}
	}

	// Delete pause container
	pauseContainerID, err := naming.BuildPauseContainerName(cellDoc.Spec.SpaceID, cellDoc.Spec.StackID, cellDoc.Spec.ID)
	if err != nil {
		return fmt.Errorf("failed to build pause container name: %w", err)
	}

	// Clean up CNI network configuration before stopping/deleting the pause container
	// Try to get the task to retrieve the netns path
	container, loadErr := r.ctrClient.GetContainer(ctrCtx, pauseContainerID)
	if loadErr == nil {
		// Try to get the task to get PID and netns path
		task, taskErr := container.Task(ctrCtx, nil)
		if taskErr == nil {
			pid := task.Pid()
			if pid > 0 {
				netnsPath := fmt.Sprintf("/proc/%d/ns/net", pid)

				// Get CNI config path
				cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(cellDoc.Spec.RealmID, cellDoc.Spec.SpaceID)
				if cniErr == nil {
					// Create CNI manager and remove container from network
					cniMgr, mgrErr := cni.NewManager(
						r.cniConf.CniBinDir,
						r.cniConf.CniConfigDir,
						r.cniConf.CniCacheDir,
					)
					if mgrErr == nil {
						if configLoadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); configLoadErr == nil {
							delErr := cniMgr.DelContainerFromNetwork(ctrCtx, pauseContainerID, netnsPath)
							if delErr != nil {
								r.logger.WarnContext(
									r.ctx,
									"failed to remove pause container from CNI network, continuing with deletion",
									"container",
									pauseContainerID,
									"netns",
									netnsPath,
									"error",
									delErr,
								)
							} else {
								r.logger.InfoContext(
									r.ctx,
									"removed pause container from CNI network",
									"container",
									pauseContainerID,
									"netns",
									netnsPath,
								)
							}
						} else {
							r.logger.WarnContext(
								r.ctx,
								"failed to load CNI config for cleanup",
								"container",
								pauseContainerID,
								"config",
								cniConfigPath,
								"error",
								configLoadErr,
							)
						}
					} else {
						r.logger.WarnContext(
							r.ctx,
							"failed to create CNI manager for cleanup",
							"container",
							pauseContainerID,
							"error",
							mgrErr,
						)
					}
				} else {
					r.logger.WarnContext(
						r.ctx,
						"failed to resolve CNI config path for cleanup",
						"container",
						pauseContainerID,
						"error",
						cniErr,
					)
				}
			}
		} else {
			r.logger.DebugContext(
				r.ctx,
				"pause container task not found, skipping CNI cleanup",
				"container",
				pauseContainerID,
				"error",
				taskErr,
			)
		}
	} else {
		r.logger.DebugContext(
			r.ctx,
			"pause container not found, skipping CNI cleanup",
			"container",
			pauseContainerID,
			"error",
			loadErr,
		)
	}

	_, err = r.ctrClient.StopContainer(ctrCtx, pauseContainerID, ctr.StopContainerOptions{})
	if err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to stop pause container, continuing with deletion",
			"container",
			pauseContainerID,
			"error",
			err,
		)
	}

	err = r.ctrClient.DeleteContainer(ctrCtx, pauseContainerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete pause container", "container", pauseContainerID, "error", err)
		// Continue with cgroup and metadata deletion
	}

	// Delete cell cgroup
	// Get space and stack to build proper cgroup spec
	var spaceDoc *v1beta1.SpaceDoc
	spaceDoc, err = r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: cellDoc.Spec.SpaceID,
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: cellDoc.Spec.RealmID,
		},
	})
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get space for cgroup deletion", "error", err)
	} else {
		var stackDoc *v1beta1.StackDoc
		stackDoc, err = r.GetStack(&v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{
				Name: cellDoc.Spec.StackID,
			},
			Spec: v1beta1.StackSpec{
				RealmID: cellDoc.Spec.RealmID,
				SpaceID: cellDoc.Spec.SpaceID,
			},
		})
		if err != nil {
			r.logger.WarnContext(r.ctx, "failed to get stack for cgroup deletion", "error", err)
		} else {
			spec := cgroups.DefaultCellSpec(realmDoc, spaceDoc, stackDoc, cellDoc)
			mountpoint := r.ctrClient.GetCgroupMountpoint()
			err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
			if err != nil {
				r.logger.WarnContext(r.ctx, "failed to delete cell cgroup", "cgroup", spec.Group, "error", err)
				// Continue with metadata deletion
			}
		}
	}

	// Delete cell metadata
	metadataFilePath := fs.CellMetadataPath(
		r.opts.RunPath,
		cellDoc.Spec.RealmID,
		cellDoc.Spec.SpaceID,
		cellDoc.Spec.StackID,
		cellDoc.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete cell metadata: %w", errdefs.ErrDeleteCell, err)
	}

	return nil
}

func (r *Exec) DeleteStack(doc *v1beta1.StackDoc) error {
	if doc == nil {
		return errdefs.ErrStackNotFound
	}

	// Get the stack document
	stackDoc, err := r.GetStack(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			// Idempotent: stack doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm and space to build cgroup spec
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: stackDoc.Spec.RealmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	spaceDoc, err := r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: stackDoc.Spec.SpaceID,
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: stackDoc.Spec.RealmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get space: %w", err)
	}

	// Delete stack cgroup
	spec := cgroups.DefaultStackSpec(realmDoc, spaceDoc, stackDoc)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete stack cgroup", "cgroup", spec.Group, "error", err)
		// Continue with metadata deletion
	}

	// Delete stack metadata
	metadataFilePath := fs.StackMetadataPath(
		r.opts.RunPath,
		stackDoc.Spec.RealmID,
		stackDoc.Spec.SpaceID,
		stackDoc.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete stack metadata: %w", errdefs.ErrDeleteStack, err)
	}

	return nil
}

func (r *Exec) DeleteSpace(doc *v1beta1.SpaceDoc) error {
	if doc == nil {
		return errdefs.ErrSpaceNotFound
	}

	// Get the space document
	spaceDoc, err := r.GetSpace(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			// Idempotent: space doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to build cgroup spec
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: spaceDoc.Spec.RealmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Delete CNI network config
	var networkName string
	networkName, err = naming.BuildSpaceNetworkName(spaceDoc)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to build network name, skipping CNI config deletion", "error", err)
	} else {
		var confPath string
		confPath, err = r.resolveSpaceCNIConfigPath(spaceDoc.Spec.RealmID, spaceDoc.Metadata.Name)
		if err == nil {
			var mgr *cni.Manager
			mgr, err = cni.NewManager(
				r.cniConf.CniBinDir,
				r.cniConf.CniConfigDir,
				r.cniConf.CniCacheDir,
			)
			if err == nil {
				if err = mgr.DeleteNetwork(networkName, confPath); err != nil {
					r.logger.WarnContext(r.ctx, "failed to delete CNI network config", "network", networkName, "error", err)
					// Continue with cgroup and metadata deletion
				}
			}
		}
	}

	// Delete space cgroup
	spec := cgroups.DefaultSpaceSpec(realmDoc, spaceDoc)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete space cgroup", "cgroup", spec.Group, "error", err)
		// Continue with metadata deletion
	}

	// Delete space metadata
	metadataFilePath := fs.SpaceMetadataPath(
		r.opts.RunPath,
		spaceDoc.Spec.RealmID,
		spaceDoc.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete space metadata: %w", errdefs.ErrDeleteSpace, err)
	}

	return nil
}

func (r *Exec) DeleteRealm(doc *v1beta1.RealmDoc) error {
	if doc == nil {
		return errdefs.ErrRealmNotFound
	}

	// Get the realm document
	realmDoc, err := r.GetRealm(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			// Idempotent: realm doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Delete realm cgroup
	spec := cgroups.DefaultRealmSpec(realmDoc)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete realm cgroup", "cgroup", spec.Group, "error", err)
		// Continue with namespace and metadata deletion
	}

	// Delete containerd namespace
	if err = r.ctrClient.DeleteNamespace(realmDoc.Spec.Namespace); err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to delete containerd namespace",
			"namespace",
			realmDoc.Spec.Namespace,
			"error",
			err,
		)
		// Continue with metadata deletion
	}

	// Delete realm metadata
	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, realmDoc.Metadata.Name)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete realm metadata: %w", errdefs.ErrDeleteRealm, err)
	}

	return nil
}
