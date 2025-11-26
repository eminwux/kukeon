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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// StartCell starts the root container and all containers defined in the CellDoc.
// The root container is started first, then all containers in doc.Spec.Containers are started.
func (r *Exec) StartCell(doc *v1beta1.CellDoc) error {
	if doc == nil {
		return errdefs.ErrCellNotFound
	}

	cellName := strings.TrimSpace(doc.Metadata.Name)
	if cellName == "" {
		return errdefs.ErrCellNotFound
	}

	cellID := doc.Spec.ID
	if cellID == "" {
		return errdefs.ErrCellIDRequired
	}

	realmID := doc.Spec.RealmID
	if realmID == "" {
		return errdefs.ErrRealmNameRequired
	}

	spaceID := doc.Spec.SpaceID
	if spaceID == "" {
		return errdefs.ErrSpaceNameRequired
	}

	stackID := doc.Spec.StackID
	if stackID == "" {
		return errdefs.ErrStackNameRequired
	}

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmID, spaceID)
	if cniErr != nil {
		return fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	// This ensures operations complete even if the parent context is canceled
	// The logger is passed separately, so we don't need to preserve context values
	ctrCtx := context.Background()

	// Always create a fresh client with background context to avoid cancellation issues
	// Close any existing client first to ensure clean state
	if r.ctrClient != nil {
		_ = r.ctrClient.Close() // Ignore errors when closing old client
		r.ctrClient = nil
	}
	r.ctrClient = ctr.NewClient(context.Background(), r.logger, r.opts.ContainerdSocket)

	err := r.ctrClient.Connect()
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
	// Convert internal realm back to external for accessing namespace
	realmDoc, convertErr := apischeme.BuildRealmExternalFromInternal(internalRealm, apischeme.VersionV1Beta1)
	if convertErr != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Generate container ID with cell identifier for uniqueness
	containerID, err := naming.BuildRootContainerName(spaceID, stackID, cellID)
	if err != nil {
		return fmt.Errorf("failed to build root container name: %w", err)
	}

	// Start root container
	rootTask, err := r.ctrClient.StartContainer(ctrCtx, ctr.ContainerSpec{ID: containerID}, ctr.TaskSpec{})
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
	if addErr := cniMgr.AddContainerToNetwork(ctrCtx, containerID, netnsPath); addErr != nil {
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
	for _, containerSpec := range doc.Spec.Containers {
		// Build container ID using hierarchical format
		containerID, err = naming.BuildContainerName(
			containerSpec.SpaceID,
			containerSpec.StackID,
			containerSpec.CellID,
			containerSpec.ID,
		)
		if err != nil {
			return fmt.Errorf("failed to build container name: %w", err)
		}

		// Log which container we're attempting to start
		startFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		startFields = append(startFields, "space", spaceID, "realm", realmID, "containerName", containerSpec.ID)
		r.logger.DebugContext(
			r.ctx,
			"attempting to start container from CellDoc",
			startFields...,
		)

		// Use container name with UUID for containerd operations
		specWithNamespaces := ctr.JoinContainerNamespaces(
			ctr.ContainerSpec{ID: containerID},
			namespacePaths,
		)

		_, err = r.ctrClient.StartContainer(ctrCtx, specWithNamespaces, ctr.TaskSpec{})
		if err != nil {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.ErrorContext(
				r.ctx,
				"failed to start container from CellDoc",
				fields...,
			)
			return fmt.Errorf("failed to start container %s: %w", containerID, err)
		}

		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"started container",
			fields...,
		)
	}

	return nil
}
