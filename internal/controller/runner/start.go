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
	"errors"
	"fmt"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// StartCell starts the root container and all containers defined in the CellDoc.
// The root container is started first, then all containers in doc.Spec.Containers are started.
func (r *Exec) StartCell(cell intmodel.Cell) (intmodel.Cell, error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrStackNameRequired
	}

	// Get the cell document to access all containers
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
	internalCell, err := r.GetCell(lookupCell)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", internalerrdefs.ErrGetCell, err)
	}

	cellSpec := internalCell.Spec
	cellID := cellSpec.ID
	if cellID == "" {
		return intmodel.Cell{}, internalerrdefs.ErrCellIDRequired
	}

	realmID := cellSpec.RealmName
	spaceID := cellSpec.SpaceName
	stackID := cellSpec.StackName

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmID, spaceID)
	if cniErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	// This ensures operations complete even if the parent context is canceled
	// The logger is passed separately, so we don't need to preserve context values

	if err = r.ensureClientConnected(); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", internalerrdefs.ErrConnectContainerd, err)
	}

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmID,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return intmodel.Cell{}, fmt.Errorf("realm %q has no namespace", realmID)
	}

	// Set namespace to realm namespace with credentials if available
	if internalRealm.Spec.RegistryCredentials != nil {
		creds := ctr.ConvertRealmCredentials(internalRealm.Spec.RegistryCredentials)
		r.ctrClient.SetNamespaceWithCredentials(namespace, creds)
	} else {
		r.ctrClient.SetNamespace(namespace)
	}

	// Generate containerd ID with cell identifier for uniqueness
	containerID, err := naming.BuildRootContainerdID(spaceID, stackID, cellID)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to build root container containerd ID: %w", err)
	}

	// Check if container exists and clean it up
	container, err := r.ctrClient.GetContainer(containerID)
	if err != nil {
		// Container doesn't exist, will create fresh
		if errors.Is(err, internalerrdefs.ErrContainerNotFound) {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID)
			r.logger.DebugContext(
				r.ctx,
				"root container does not exist, will create fresh",
				fields...,
			)
		} else {
			// Other errors are unexpected
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.WarnContext(
				r.ctx,
				"failed to check if root container exists, will attempt to create",
				fields...,
			)
		}
	} else {
		// Container exists, check if it has a task and delete it
		nsCtx := namespaces.WithNamespace(r.ctx, namespace)
		task, taskErr := container.Task(nsCtx, nil)
		if taskErr == nil {
			// Task exists, delete it
			_, deleteTaskErr := task.Delete(nsCtx, containerd.WithProcessKill)
			if deleteTaskErr != nil {
				fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
				fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", deleteTaskErr))
				r.logger.WarnContext(
					r.ctx,
					"failed to delete existing task, continuing",
					fields...,
				)
			}
		}

		// Delete the container to remove stale spec
		err = r.ctrClient.DeleteContainer(containerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})
		if err != nil {
			// Check if container doesn't exist (might have been deleted between check and delete)
			if errors.Is(err, internalerrdefs.ErrContainerNotFound) {
				fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
				fields = append(fields, "space", spaceID, "realm", realmID)
				r.logger.DebugContext(
					r.ctx,
					"root container already deleted, will create fresh",
					fields...,
				)
			} else {
				fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
				fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
				r.logger.WarnContext(
					r.ctx,
					"failed to delete existing container, continuing",
					fields...,
				)
			}
		} else {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID)
			r.logger.InfoContext(
				r.ctx,
				"deleted existing root container for recreation",
				fields...,
			)
		}
	}

	// Recreate root container fresh
	rootContainerSpec, err := r.ensureCellRootContainerSpec(internalCell)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get root container spec: %w", err)
	}

	rootLabels := buildRootContainerLabels(internalCell)
	ctrContainerSpec := ctr.BuildRootContainerSpec(rootContainerSpec, rootLabels)

	_, err = r.ctrClient.CreateContainer(ctrContainerSpec)
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to create root container",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to create root container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	fields = append(fields, "space", spaceID, "realm", realmID)
	r.logger.InfoContext(
		r.ctx,
		"created root container",
		fields...,
	)

	// Start root container
	rootTask, err := r.ctrClient.StartContainer(ctr.ContainerSpec{ID: containerID}, ctr.TaskSpec{})
	if err != nil {
		fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to start root container",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to start root container %s: %w", containerID, err)
	}

	rootPID := rootTask.Pid()
	if rootPID == 0 {
		return intmodel.Cell{}, fmt.Errorf("root container %s has invalid pid (0)", containerID)
	}

	namespacePaths := ctr.NamespacePaths{
		Net: fmt.Sprintf("/proc/%d/ns/net", rootPID),
		IPC: fmt.Sprintf("/proc/%d/ns/ipc", rootPID),
		UTS: fmt.Sprintf("/proc/%d/ns/uts", rootPID),
	}

	// Log CNI paths being used for debugging
	// Note: NewManager applies defaults AFTER creating the CNI config,
	// so if cniBinDir is empty, the CNI config will have an empty path array
	cniBinDir := r.cniConf.CniBinDir
	cniConfigDir := r.cniConf.CniConfigDir
	cniCacheDir := r.cniConf.CniCacheDir
	debugFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	debugFields = append(
		debugFields,
		"space",
		spaceID,
		"realm",
		realmID,
		"stack",
		stackID,
		"cniBinDir",
		cniBinDir,
		"cniConfigDir",
		cniConfigDir,
		"cniCacheDir",
		cniCacheDir,
	)
	if cniBinDir == "" {
		debugFields = append(debugFields, "cniBinDirDefault", "/opt/cni/bin")
	}
	if cniConfigDir == "" {
		debugFields = append(debugFields, "cniConfigDirDefault", "/opt/cni/net.d")
	}
	if cniCacheDir == "" {
		debugFields = append(debugFields, "cniCacheDirDefault", "/opt/cni/cache")
	}
	r.logger.DebugContext(
		r.ctx,
		"creating CNI manager",
		debugFields...,
	)

	cniMgr, mgrErr := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if mgrErr != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", internalerrdefs.ErrInitCniManager, mgrErr)
	}

	if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr != nil {
		fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceID,
			"realm",
			realmID,
			"cniConfig",
			cniConfigPath,
			"err",
			fmt.Sprintf("%v", loadErr),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to load CNI config",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to load CNI config %s: %w", cniConfigPath, loadErr)
	}

	netnsPath := namespacePaths.Net
	if addErr := cniMgr.AddContainerToNetwork(r.ctx, containerID, netnsPath); addErr != nil {
		// Check if the error indicates the container is already attached to the network
		// This can happen when the task was already running from a previous start
		errMsg := addErr.Error()
		if strings.Contains(errMsg, "already exists") {
			// Container is already attached to the network, log and continue
			fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"cniConfig",
				cniConfigPath,
				"netns",
				netnsPath,
			)
			r.logger.DebugContext(
				r.ctx,
				"root container already attached to network, skipping",
				fields...,
			)
			// Continue execution - container is already attached
		} else {
			// Log the actual CNI bin dir value being used (may be empty, which causes the error)
			// Note: NewManager creates CNI config with this value BEFORE applying defaults,
			// so if empty, the CNI config will search in an empty path array
			cniBinDirValue := r.cniConf.CniBinDir
			fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"cniConfig",
				cniConfigPath,
				"netns",
				netnsPath,
				"cniBinDir",
				cniBinDirValue,
				"err",
				fmt.Sprintf("%v", addErr),
			)
			if cniBinDirValue == "" {
				fields = append(
					fields,
					"cniBinDirNote",
					"empty path - CNI config was created with empty plugin search path, default /opt/cni/bin not applied to CNI config",
				)
			}
			r.logger.ErrorContext(
				r.ctx,
				"failed to attach root container to network",
				fields...,
			)
			return intmodel.Cell{}, fmt.Errorf("failed to attach root container %s to network: %w", containerID, addErr)
		}
	}

	infoFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	infoFields = append(infoFields, "space", spaceID, "realm", realmID, "pid", rootPID, "cniConfig", cniConfigPath)
	r.logger.InfoContext(
		r.ctx,
		"started root container",
		infoFields...,
	)

	// Start all containers defined in the CellDoc
	for _, containerSpec := range cellSpec.Containers {
		// Skip root container - it's already created and started above
		if containerSpec.Root {
			continue
		}

		// Use ContainerdID from spec
		ctrContainerID := containerSpec.ContainerdID
		if ctrContainerID == "" {
			return intmodel.Cell{}, fmt.Errorf("container %q has empty ContainerdID", containerSpec.ID)
		}

		// Log which container we're attempting to start
		startFields := appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
		startFields = append(startFields, "space", spaceID, "realm", realmID, "containerName", containerSpec.ID)
		r.logger.DebugContext(
			r.ctx,
			"attempting to start container from CellDoc",
			startFields...,
		)

		// Delete container if it exists (idempotent - DeleteContainer handles non-existent containers gracefully)
		// This ensures any stale container specs and tasks are cleaned up before recreation
		err = r.ctrClient.DeleteContainer(ctrContainerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})
		if err != nil {
			// Log warning but continue - DeleteContainer is idempotent, so errors here are unexpected
			fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"containerName",
				containerSpec.ID,
				"err",
				fmt.Sprintf("%v", err),
			)
			r.logger.WarnContext(
				r.ctx,
				"failed to delete existing container, continuing with recreation",
				fields...,
			)
		}

		// Recreate container fresh
		_, err = r.ctrClient.CreateContainerFromSpec(containerSpec)
		if err != nil {
			fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"containerName",
				containerSpec.ID,
				"err",
				fmt.Sprintf("%v", err),
			)
			r.logger.ErrorContext(
				r.ctx,
				"failed to create container",
				fields...,
			)
			return intmodel.Cell{}, fmt.Errorf("failed to create container %s: %w", ctrContainerID, err)
		}

		fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "containerName", containerSpec.ID)
		r.logger.InfoContext(
			r.ctx,
			"created container",
			fields...,
		)

		// Use container name with UUID for containerd operations
		specWithNamespaces := ctr.JoinContainerNamespaces(
			ctr.ContainerSpec{ID: ctrContainerID},
			namespacePaths,
		)

		_, err = r.ctrClient.StartContainer(specWithNamespaces, ctr.TaskSpec{})
		if err != nil {
			fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.ErrorContext(
				r.ctx,
				"failed to start container from CellDoc",
				fields...,
			)
			return intmodel.Cell{}, fmt.Errorf("failed to start container %s: %w", ctrContainerID, err)
		}

		fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"started container",
			fields...,
		)
	}

	// Update cell state in internal model
	internalCell.Status.State = intmodel.CellStateReady

	return internalCell, nil
}

// StartContainer starts a specific container in a cell.
func (r *Exec) StartContainer(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return intmodel.Cell{}, errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrCellNameRequired
	}

	cellID := cell.Spec.ID
	if cellID == "" {
		return intmodel.Cell{}, internalerrdefs.ErrCellIDRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrSpaceNameRequired
	}

	if err := r.ensureClientConnected(); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", internalerrdefs.ErrConnectContainerd, err)
	}

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return intmodel.Cell{}, fmt.Errorf("realm %q has no namespace", realmName)
	}

	// Set namespace to realm namespace with credentials if available
	if len(internalRealm.Spec.RegistryCredentials) > 0 {
		creds := ctr.ConvertRealmCredentials(internalRealm.Spec.RegistryCredentials)
		r.ctrClient.SetNamespaceWithCredentials(namespace, creds)
	} else {
		r.ctrClient.SetNamespace(namespace)
	}

	// Find container in cell spec by ID (base name)
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == containerID {
			foundContainerSpec = &cell.Spec.Containers[i]
			break
		}
	}

	if foundContainerSpec == nil {
		return intmodel.Cell{}, fmt.Errorf("container %q not found in cell %q", containerID, cellName)
	}

	// Root container cannot be started directly - it must be started by starting the cell
	if foundContainerSpec.Root {
		return intmodel.Cell{}, fmt.Errorf(
			"root container cannot be started directly, start the cell instead using 'kuke start cell %s'",
			cellName,
		)
	}

	// Use ContainerdID from spec
	containerdID := foundContainerSpec.ContainerdID
	if containerdID == "" {
		return intmodel.Cell{}, fmt.Errorf("container %q has empty ContainerdID", containerID)
	}

	// Get root container to get namespace paths
	rootContainerID, err := r.getRootContainerContainerdID(cell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	// Get root container's namespace paths
	rootContainer, err := r.ctrClient.GetContainer(rootContainerID)
	if err != nil {
		if errors.Is(err, internalerrdefs.ErrContainerNotFound) {
			return intmodel.Cell{}, fmt.Errorf(
				"root container %q does not exist, start the cell first using 'kuke start cell %s': %w",
				rootContainerID,
				cellName,
				err,
			)
		}
		return intmodel.Cell{}, fmt.Errorf("failed to get root container: %w", err)
	}

	nsCtx := namespaces.WithNamespace(r.ctx, namespace)
	rootTask, err := rootContainer.Task(nsCtx, nil)
	if err != nil {
		// Check if task doesn't exist
		if errdefs.IsNotFound(err) {
			return intmodel.Cell{}, fmt.Errorf(
				"root container %q exists but has no task, start the cell first using 'kuke start cell %s': %w",
				rootContainerID,
				cellName,
				err,
			)
		}
		return intmodel.Cell{}, fmt.Errorf("root container task not found, ensure root container is started: %w", err)
	}

	rootPID := rootTask.Pid()
	if rootPID == 0 {
		return intmodel.Cell{}, errors.New("root container has invalid pid (0)")
	}

	namespacePaths := ctr.NamespacePaths{
		Net: fmt.Sprintf("/proc/%d/ns/net", rootPID),
		IPC: fmt.Sprintf("/proc/%d/ns/ipc", rootPID),
		UTS: fmt.Sprintf("/proc/%d/ns/uts", rootPID),
	}

	// Delete container if it exists (idempotent - DeleteContainer handles non-existent containers gracefully)
	// This ensures any stale container specs and tasks are cleaned up before recreation
	err = r.ctrClient.DeleteContainer(containerdID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		// Log warning but continue - DeleteContainer is idempotent, so errors here are unexpected
		fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceName,
			"realm",
			realmName,
			"containerName",
			containerID,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.WarnContext(
			r.ctx,
			"failed to delete existing container, continuing with recreation",
			fields...,
		)
	}

	// Recreate container fresh
	_, err = r.ctrClient.CreateContainerFromSpec(*foundContainerSpec)
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceName,
			"realm",
			realmName,
			"containerName",
			containerID,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to create container",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to create container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"created container",
		fields...,
	)

	// Start container with namespace paths
	specWithNamespaces := ctr.JoinContainerNamespaces(
		ctr.ContainerSpec{ID: containerdID},
		namespacePaths,
	)

	_, err = r.ctrClient.StartContainer(specWithNamespaces, ctr.TaskSpec{})
	if err != nil {
		fields = appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceName,
			"realm",
			realmName,
			"containerName",
			containerID,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to start container",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to start container %s: %w", containerID, err)
	}

	fields = appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"started container",
		fields...,
	)

	// Get the cell again to ensure we have the latest state
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: cell.Spec.StackName,
		},
	}
	updatedCell, err := r.GetCell(lookupCell)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to retrieve cell after starting container: %w", err)
	}

	// Update cell state in internal model
	updatedCell.Status.State = intmodel.CellStateReady

	return updatedCell, nil
}
