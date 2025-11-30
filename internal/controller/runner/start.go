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

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// StartCell starts the root container and all containers defined in the CellDoc.
// The root container is started first, then all containers in doc.Spec.Containers are started.
func (r *Exec) StartCell(cell intmodel.Cell) error {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return errdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return errdefs.ErrStackNameRequired
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
		return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	cellSpec := internalCell.Spec
	cellID := cellSpec.ID
	if cellID == "" {
		return errdefs.ErrCellIDRequired
	}

	realmID := cellSpec.RealmName
	spaceID := cellSpec.SpaceName
	stackID := cellSpec.StackName

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmID, spaceID)
	if cniErr != nil {
		return fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	// This ensures operations complete even if the parent context is canceled
	// The logger is passed separately, so we don't need to preserve context values

	// Always create a fresh client with background context to avoid cancellation issues
	// Close any existing client first to ensure clean state
	if r.ctrClient != nil {
		_ = r.ctrClient.Close() // Ignore errors when closing old client
		r.ctrClient = nil
	}
	r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)

	err = r.ctrClient.Connect()
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmID,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return fmt.Errorf("realm %q has no namespace", realmID)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(namespace)

	// Generate containerd ID with cell identifier for uniqueness
	containerID, err := naming.BuildRootContainerdID(spaceID, stackID, cellID)
	if err != nil {
		return fmt.Errorf("failed to build root container containerd ID: %w", err)
	}

	// Start root container
	rootTask, err := r.ctrClient.StartContainer(r.ctx, ctr.ContainerSpec{ID: containerID}, ctr.TaskSpec{})
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to start root container",
			fields...,
		)
		return fmt.Errorf("failed to start root container %s: %w", containerID, err)
	}

	rootPID := rootTask.Pid()
	if rootPID == 0 {
		return fmt.Errorf("root container %s has invalid pid (0)", containerID)
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
		return fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, mgrErr)
	}

	if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
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
		return fmt.Errorf("failed to load CNI config %s: %w", cniConfigPath, loadErr)
	}

	netnsPath := namespacePaths.Net
	if addErr := cniMgr.AddContainerToNetwork(r.ctx, containerID, netnsPath); addErr != nil {
		// Check if the error indicates the container is already attached to the network
		// This can happen when the task was already running from a previous start
		errMsg := addErr.Error()
		if strings.Contains(errMsg, "already exists") {
			// Container is already attached to the network, log and continue
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
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
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
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
			return fmt.Errorf("failed to attach root container %s to network: %w", containerID, addErr)
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
		// Use ContainerdID from spec
		ctrContainerID := containerSpec.ContainerdID
		if ctrContainerID == "" {
			return fmt.Errorf("container %q has empty ContainerdID", containerSpec.ID)
		}

		// Log which container we're attempting to start
		startFields := appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
		startFields = append(startFields, "space", spaceID, "realm", realmID, "containerName", containerSpec.ID)
		r.logger.DebugContext(
			r.ctx,
			"attempting to start container from CellDoc",
			startFields...,
		)

		// Use container name with UUID for containerd operations
		specWithNamespaces := ctr.JoinContainerNamespaces(
			ctr.ContainerSpec{ID: ctrContainerID},
			namespacePaths,
		)

		_, err = r.ctrClient.StartContainer(r.ctx, specWithNamespaces, ctr.TaskSpec{})
		if err != nil {
			fields := appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.ErrorContext(
				r.ctx,
				"failed to start container from CellDoc",
				fields...,
			)
			return fmt.Errorf("failed to start container %s: %w", ctrContainerID, err)
		}

		fields := appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"started container",
			fields...,
		)
	}

	return nil
}

// StartContainer starts a specific container in a cell.
func (r *Exec) StartContainer(cell intmodel.Cell, containerID string) error {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return errdefs.ErrCellNotFound
	}

	cellID := cell.Spec.ID
	if cellID == "" {
		return errdefs.ErrCellIDRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}

	// Always create a fresh client with background context to avoid cancellation issues
	if r.ctrClient != nil {
		_ = r.ctrClient.Close() // Ignore errors when closing old client
		r.ctrClient = nil
	}
	r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)

	err := r.ctrClient.Connect()
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return fmt.Errorf("realm %q has no namespace", realmName)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(namespace)

	// Find container in cell spec by ID (base name)
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == containerID {
			foundContainerSpec = &cell.Spec.Containers[i]
			break
		}
	}

	if foundContainerSpec == nil {
		return fmt.Errorf("container %q not found in cell %q", containerID, cellName)
	}

	// Use ContainerdID from spec
	containerdID := foundContainerSpec.ContainerdID
	if containerdID == "" {
		return fmt.Errorf("container %q has empty ContainerdID", containerID)
	}

	// Use containerd ID for containerd operations
	_, err = r.ctrClient.StartContainer(r.ctx, ctr.ContainerSpec{ID: containerdID}, ctr.TaskSpec{})
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
			"failed to start container",
			fields...,
		)
		return fmt.Errorf("failed to start container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"started container",
		fields...,
	)

	return nil
}
