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
	"strings"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func appendCellLogFields(fields []any, cellID, cellName string) []any {
	fields = append(fields, "cell", cellID)
	if cellName != "" && cellName != cellID {
		fields = append(fields, "cellName", cellName)
	}
	return fields
}

// createCellContainers creates the pause container and all containers defined in the CellDoc.
// The pause container is created first, then all containers in doc.Spec.Containers are created.
func (r *Exec) createCellContainers(doc *v1beta1.CellDoc) (containerd.Container, error) {
	if doc == nil {
		return nil, errdefs.ErrCellNotFound
	}

	cellName := strings.TrimSpace(doc.Metadata.Name)
	if cellName == "" {
		return nil, errdefs.ErrCellNotFound
	}

	cellID := doc.Spec.ID
	if cellID == "" {
		return nil, errdefs.ErrCellIDRequired
	}

	realmID := doc.Spec.RealmID
	if realmID == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	spaceID := doc.Spec.SpaceID
	if spaceID == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}

	stackID := doc.Spec.StackID
	if stackID == "" {
		return nil, errdefs.ErrStackNameRequired
	}

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(spaceID)
	if cniErr != nil {
		return nil, fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	// This ensures operations complete even if the parent context is canceled
	// The logger is passed separately, so we don't need to preserve context values
	ctrCtx := context.Background()

	// Initialize ctr client if needed
	// Use background context for client creation to avoid cancellation issues
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(context.Background(), r.logger, r.opts.ContainerdSocket)
	}

	err := r.ctrClient.Connect()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Use just "pause" as the container name
	containerID := "pause"

	// Use default pause image (busybox with sleep)
	image := "docker.io/library/busybox:latest"

	// Build labels
	labels := make(map[string]string)
	if doc.Metadata.Labels != nil {
		for k, v := range doc.Metadata.Labels {
			labels[k] = v
		}
	}
	// Add kukeon-specific labels
	labels["kukeon.io/container-type"] = "pause"
	labels["kukeon.io/cell"] = cellID
	if cellName != "" {
		labels["kukeon.io/cell-name"] = cellName
	}
	labels["kukeon.io/space"] = spaceID
	labels["kukeon.io/realm"] = realmID
	labels["kukeon.io/stack"] = stackID

	// Create container spec with minimal OCI spec options
	// The pause container should run a minimal command that keeps it alive
	specOpts := []oci.SpecOpts{
		// Run sleep infinity to keep container alive
		oci.WithProcessArgs("sleep", "infinity"),
		// Set hostname to container ID
		oci.WithHostname(containerID),
		// Keep the container running in the background
		oci.WithDefaultPathEnv,
	}

	containerSpec := ctr.ContainerSpec{
		ID:            containerID,
		Image:         image,
		Labels:        labels,
		SpecOpts:      specOpts,
		CNIConfigPath: cniConfigPath,
	}

	container, err := r.ctrClient.CreateContainer(ctrCtx, containerSpec)
	if err != nil {
		logFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		logFields = append(
			logFields,
			"space",
			spaceID,
			"realm",
			realmID,
			"cniConfig",
			cniConfigPath,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to create pause container",
			logFields...,
		)
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCreatePauseContainer, err)
	}

	infoFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	infoFields = append(infoFields, "space", spaceID, "realm", realmID, "cniConfig", cniConfigPath)
	r.logger.InfoContext(
		r.ctx,
		"created pause container",
		infoFields...,
	)

	// Create all containers defined in the CellDoc
	for i := range doc.Spec.Containers {
		containerSpec := doc.Spec.Containers[i]
		if containerSpec.CellID == "" {
			containerSpec.CellID = cellID
		}
		if containerSpec.SpaceID == "" {
			containerSpec.SpaceID = spaceID
		}
		if containerSpec.RealmID == "" {
			containerSpec.RealmID = realmID
		}
		if containerSpec.StackID == "" {
			containerSpec.StackID = stackID
		}
		if containerSpec.CNIConfigPath == "" {
			containerSpec.CNIConfigPath = cniConfigPath
		}
		doc.Spec.Containers[i] = containerSpec

		// Use container name directly for containerd operations
		_, err = r.ctrClient.CreateContainerFromSpec(
			ctrCtx,
			&containerSpec,
		)
		if err != nil {
			fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"cniConfig",
				containerSpec.CNIConfigPath,
				"err",
				fmt.Sprintf("%v", err),
			)
			r.logger.ErrorContext(
				r.ctx,
				"failed to create container from CellDoc",
				fields...,
			)
			return nil, fmt.Errorf("failed to create container %s: %w", containerSpec.ID, err)
		}
	}

	return container, nil
}

// ensureCellContainers ensures the pause container and all containers defined in the CellDoc exist.
// The pause container is ensured first, then all containers in doc.Spec.Containers are ensured.
// If any container doesn't exist, it is created. Returns the pause container or an error.
func (r *Exec) ensureCellContainers(doc *v1beta1.CellDoc) (containerd.Container, error) {
	if doc == nil {
		return nil, errdefs.ErrCellNotFound
	}

	cellName := strings.TrimSpace(doc.Metadata.Name)
	if cellName == "" {
		return nil, errdefs.ErrCellNotFound
	}

	cellID := doc.Spec.ID
	if cellID == "" {
		return nil, errdefs.ErrCellIDRequired
	}

	realmID := doc.Spec.RealmID
	if realmID == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	spaceID := doc.Spec.SpaceID
	if spaceID == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}

	stackID := doc.Spec.StackID
	if stackID == "" {
		return nil, errdefs.ErrStackNameRequired
	}

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(spaceID)
	if cniErr != nil {
		return nil, fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	// This ensures operations complete even if the parent context is canceled
	// The logger is passed separately, so we don't need to preserve context values
	ctrCtx := context.Background()

	// Initialize ctr client if needed
	// Use background context for client creation to avoid cancellation issues
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(context.Background(), r.logger, r.opts.ContainerdSocket)
	}

	err := r.ctrClient.Connect()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Use just "pause" as the container name
	containerID := "pause"

	// Check if container exists
	exists, err := r.ctrClient.ExistsContainer(ctrCtx, containerID)
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to check if pause container exists",
			fields...,
		)
		return nil, fmt.Errorf("failed to check if pause container exists: %w", err)
	}

	if exists {
		// Container exists, load and return it
		container, loadErr := r.ctrClient.GetContainer(ctrCtx, containerID)
		if loadErr != nil {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", loadErr))
			r.logger.WarnContext(
				r.ctx,
				"pause container exists but failed to load",
				fields...,
			)
			return nil, fmt.Errorf("failed to load existing pause container: %w", loadErr)
		}
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.DebugContext(
			r.ctx,
			"pause container exists",
			fields...,
		)
		return container, nil
	}

	// Container doesn't exist, create it
	createFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	createFields = append(createFields, "space", spaceID, "realm", realmID, "cniConfig", cniConfigPath)
	r.logger.InfoContext(
		r.ctx,
		"pause container does not exist, creating",
		createFields...,
	)

	// Use default pause image (busybox with sleep)
	image := "docker.io/library/busybox:latest"

	// Build labels
	labels := make(map[string]string)
	if doc.Metadata.Labels != nil {
		for k, v := range doc.Metadata.Labels {
			labels[k] = v
		}
	}
	// Add kukeon-specific labels
	labels["kukeon.io/container-type"] = "pause"
	labels["kukeon.io/cell"] = cellID
	if cellName != "" {
		labels["kukeon.io/cell-name"] = cellName
	}
	labels["kukeon.io/space"] = spaceID
	labels["kukeon.io/realm"] = realmID
	labels["kukeon.io/stack"] = stackID

	// Create container spec with minimal OCI spec options
	// The pause container should run a minimal command that keeps it alive
	specOpts := []oci.SpecOpts{
		// Run sleep infinity to keep container alive
		oci.WithProcessArgs("sleep", "infinity"),
		// Set hostname to container ID
		oci.WithHostname(containerID),
		// Keep the container running in the background
		oci.WithDefaultPathEnv,
	}

	containerSpec := ctr.ContainerSpec{
		ID:            containerID,
		Image:         image,
		Labels:        labels,
		SpecOpts:      specOpts,
		CNIConfigPath: cniConfigPath,
	}

	container, createErr := r.ctrClient.CreateContainer(ctrCtx, containerSpec)
	if createErr != nil {
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
			fmt.Sprintf("%v", createErr),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to create pause container",
			fields...,
		)
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCreatePauseContainer, createErr)
	}

	ensuredFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	ensuredFields = append(ensuredFields, "space", spaceID, "realm", realmID, "cniConfig", cniConfigPath)
	r.logger.InfoContext(
		r.ctx,
		"ensured pause container exists",
		ensuredFields...,
	)

	// Ensure all containers defined in the CellDoc exist
	for i := range doc.Spec.Containers {
		containerSpec := doc.Spec.Containers[i]
		if containerSpec.CNIConfigPath == "" {
			containerSpec.CNIConfigPath = cniConfigPath
			doc.Spec.Containers[i] = containerSpec
		}

		// Ensure container spec has required IDs
		if containerSpec.CellID == "" {
			containerSpec.CellID = cellID
		}
		if containerSpec.SpaceID == "" {
			containerSpec.SpaceID = spaceID
		}
		if containerSpec.RealmID == "" {
			containerSpec.RealmID = realmID
		}
		if containerSpec.StackID == "" {
			containerSpec.StackID = stackID
		}
		doc.Spec.Containers[i] = containerSpec

		// Use container name directly for containerd operations
		exists, err = r.ctrClient.ExistsContainer(ctrCtx, containerSpec.ID)
		if err != nil {
			fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.ErrorContext(
				r.ctx,
				"failed to check if container exists",
				fields...,
			)
			return nil, fmt.Errorf("failed to check if container %s exists: %w", containerSpec.ID, err)
		}

		if !exists {
			fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "cniConfig", containerSpec.CNIConfigPath)
			r.logger.InfoContext(
				r.ctx,
				"container does not exist, creating",
				fields...,
			)
			if containerSpec.CNIConfigPath == "" {
				containerSpec.CNIConfigPath = cniConfigPath
				doc.Spec.Containers[i] = containerSpec
			}

			// Use container name directly for containerd operations
			_, err = r.ctrClient.CreateContainerFromSpec(
				ctrCtx,
				&containerSpec,
			)
			if err != nil {
				// Check if the error indicates the container already exists
				// This can happen due to race conditions where the container
				// was created between the existence check and creation attempt
				errMsg := err.Error()
				if strings.Contains(errMsg, "container already exists") {
					debugFields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
					debugFields = append(debugFields, "space", spaceID, "realm", realmID)
					r.logger.DebugContext(
						r.ctx,
						"container already exists (race condition), skipping",
						debugFields...,
					)
					continue
				}
				fields = appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
				fields = append(
					fields,
					"space",
					spaceID,
					"realm",
					realmID,
					"cniConfig",
					containerSpec.CNIConfigPath,
					"err",
					fmt.Sprintf("%v", err),
				)
				r.logger.ErrorContext(
					r.ctx,
					"failed to create container from CellDoc",
					fields...,
				)
				return nil, fmt.Errorf("failed to create container %s: %w", containerSpec.ID, err)
			}
		} else {
			fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID)
			r.logger.DebugContext(
				r.ctx,
				"container exists",
				fields...,
			)
		}
	}

	return container, nil
}

// StartCell starts the pause container and all containers defined in the CellDoc.
// The pause container is started first, then all containers in doc.Spec.Containers are started.
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

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(spaceID)
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
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Use just "pause" as the container name
	containerID := "pause"

	// Start pause container
	pauseTask, err := r.ctrClient.StartContainer(ctrCtx, ctr.ContainerSpec{ID: containerID}, ctr.TaskSpec{})
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to start pause container",
			fields...,
		)
		return fmt.Errorf("failed to start pause container %s: %w", containerID, err)
	}

	pausePID := pauseTask.Pid()
	if pausePID == 0 {
		return fmt.Errorf("pause container %s has invalid pid (0)", containerID)
	}

	namespacePaths := ctr.NamespacePaths{
		Net: fmt.Sprintf("/proc/%d/ns/net", pausePID),
		IPC: fmt.Sprintf("/proc/%d/ns/ipc", pausePID),
		UTS: fmt.Sprintf("/proc/%d/ns/uts", pausePID),
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
				"pause container already attached to network, skipping",
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
				"failed to attach pause container to network",
				fields...,
			)
			return fmt.Errorf("failed to attach pause container %s to network: %w", containerID, addErr)
		}
	}

	infoFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	infoFields = append(infoFields, "space", spaceID, "realm", realmID, "pid", pausePID, "cniConfig", cniConfigPath)
	r.logger.InfoContext(
		r.ctx,
		"started pause container",
		infoFields...,
	)

	// Start all containers defined in the CellDoc
	for _, containerSpec := range doc.Spec.Containers {
		// Use container name directly for containerd operations
		specWithNamespaces := ctr.JoinContainerNamespaces(
			ctr.ContainerSpec{ID: containerSpec.ID},
			namespacePaths,
		)

		_, err = r.ctrClient.StartContainer(ctrCtx, specWithNamespaces, ctr.TaskSpec{})
		if err != nil {
			fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.ErrorContext(
				r.ctx,
				"failed to start container from CellDoc",
				fields...,
			)
			return fmt.Errorf("failed to start container %s: %w", containerSpec.ID, err)
		}

		fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"started container",
			fields...,
		)
	}

	return nil
}

// detachPauseContainerFromCNI detaches the pause container from the CNI network.
// It handles the case where the container might not exist or might already be detached.
// This function logs warnings for non-critical failures but continues execution.
func (r *Exec) detachPauseContainerFromCNI(
	ctrCtx context.Context,
	pauseContainerID, cniConfigPath, cellID, cellName, spaceID, realmID, realmNamespace string,
) {
	// Get pause container to check if it exists and get its task
	pauseContainer, err := r.ctrClient.GetContainer(ctrCtx, pauseContainerID)
	if err == nil {
		// Container exists, try to get its task to detach from CNI
		nsCtx := namespaces.WithNamespace(ctrCtx, realmNamespace)
		pauseTask, taskErr := pauseContainer.Task(nsCtx, nil)
		if taskErr == nil {
			// Task exists, get PID and detach from CNI
			pausePID := pauseTask.Pid()
			if pausePID > 0 {
				netnsPath := fmt.Sprintf("/proc/%d/ns/net", pausePID)

				// Create CNI manager and detach from network
				cniMgr, mgrErr := cni.NewManager(
					r.cniConf.CniBinDir,
					r.cniConf.CniConfigDir,
					r.cniConf.CniCacheDir,
				)
				if mgrErr == nil {
					if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr == nil {
						if delErr := cniMgr.DelContainerFromNetwork(ctrCtx, pauseContainerID, netnsPath); delErr != nil {
							// Log warning but continue - network might already be detached
							fields := appendCellLogFields([]any{"id", pauseContainerID}, cellID, cellName)
							fields = append(
								fields,
								"space",
								spaceID,
								"realm",
								realmID,
								"netns",
								netnsPath,
								"err",
								fmt.Sprintf("%v", delErr),
							)
							r.logger.WarnContext(
								r.ctx,
								"failed to detach pause container from network, continuing",
								fields...,
							)
						} else {
							fields := appendCellLogFields([]any{"id", pauseContainerID}, cellID, cellName)
							fields = append(fields, "space", spaceID, "realm", realmID, "netns", netnsPath)
							r.logger.InfoContext(
								r.ctx,
								"detached pause container from network",
								fields...,
							)
						}
					}
				}
			}
		}
	}
}

// StopCell stops all containers in the cell (workload containers first, then pause container).
// It detaches the pause container from the CNI network before stopping it.
func (r *Exec) StopCell(doc *v1beta1.CellDoc) error {
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

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(spaceID)
	if cniErr != nil {
		return fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	ctrCtx := context.Background()

	// Always create a fresh client with background context to avoid cancellation issues
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
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Stop all workload containers first
	for _, containerSpec := range doc.Spec.Containers {
		// Use container name directly for containerd operations
		timeout := 5 * time.Second
		_, err = r.ctrClient.StopContainer(ctrCtx, containerSpec.ID, ctr.StopContainerOptions{
			Force:   true,
			Timeout: &timeout,
		})
		if err != nil {
			// Log warning but continue with other containers
			fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container, continuing",
				fields...,
			)
			// Continue with other containers
			continue
		}

		fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"stopped container",
			fields...,
		)
	}

	// Stop pause container last and detach from CNI
	pauseContainerID := "pause"

	// Detach pause container from CNI network
	r.detachPauseContainerFromCNI(
		ctrCtx,
		pauseContainerID,
		cniConfigPath,
		cellID,
		cellName,
		spaceID,
		realmID,
		realmDoc.Spec.Namespace,
	)

	// Stop pause container
	timeout := 5 * time.Second
	_, err = r.ctrClient.StopContainer(ctrCtx, pauseContainerID, ctr.StopContainerOptions{
		Force:   true,
		Timeout: &timeout,
	})
	if err != nil {
		fields := appendCellLogFields([]any{"id", pauseContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.WarnContext(
			r.ctx,
			"failed to stop pause container",
			fields...,
		)
		// Don't fail the whole operation if pause container stop fails
	} else {
		fields := appendCellLogFields([]any{"id", pauseContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"stopped pause container",
			fields...,
		)
	}

	return nil
}

// KillCell immediately force-kills all containers in a cell (workload containers first, then pause container).
// It detaches the pause container from the CNI network before killing it.
func (r *Exec) KillCell(doc *v1beta1.CellDoc) error {
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

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(spaceID)
	if cniErr != nil {
		return fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	ctrCtx := context.Background()

	// Always create a fresh client with background context to avoid cancellation issues
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
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Kill all workload containers first
	for _, containerSpec := range doc.Spec.Containers {
		// Use container name directly for containerd operations
		err = r.killContainerTask(ctrCtx, containerSpec.ID, realmDoc.Spec.Namespace)
		if err != nil {
			// Log warning but continue with other containers
			fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.WarnContext(
				r.ctx,
				"failed to kill container, continuing",
				fields...,
			)
			// Continue with other containers
			continue
		}

		fields := appendCellLogFields([]any{"id", containerSpec.ID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"killed container",
			fields...,
		)
	}

	// Kill pause container last and detach from CNI
	pauseContainerID := "pause"

	// Detach pause container from CNI network
	r.detachPauseContainerFromCNI(
		ctrCtx,
		pauseContainerID,
		cniConfigPath,
		cellID,
		cellName,
		spaceID,
		realmID,
		realmDoc.Spec.Namespace,
	)

	// Kill pause container
	err = r.killContainerTask(ctrCtx, pauseContainerID, realmDoc.Spec.Namespace)
	if err != nil {
		fields := appendCellLogFields([]any{"id", pauseContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.WarnContext(
			r.ctx,
			"failed to kill pause container",
			fields...,
		)
		// Don't fail the whole operation if pause container kill fails
	} else {
		fields := appendCellLogFields([]any{"id", pauseContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"killed pause container",
			fields...,
		)
	}

	return nil
}

// killContainerTask directly kills a container task by sending SIGKILL immediately.
// This is a helper method used by KillCell and KillContainer.
func (r *Exec) killContainerTask(ctrCtx context.Context, containerID, realmNamespace string) error {
	// Get the container
	container, err := r.ctrClient.GetContainer(ctrCtx, containerID)
	if err != nil {
		return fmt.Errorf("failed to get container %s: %w", containerID, err)
	}

	// Create namespace context
	nsCtx := namespaces.WithNamespace(ctrCtx, realmNamespace)

	// Get the task
	task, err := container.Task(nsCtx, nil)
	if err != nil {
		// Task might not exist, which is fine for kill operation
		r.logger.DebugContext(
			r.ctx,
			"task not found for container, may already be stopped",
			"container",
			containerID,
			"error",
			err,
		)
		return nil // Don't fail if task doesn't exist
	}

	// Immediately send SIGKILL
	err = task.Kill(nsCtx, syscall.SIGKILL)
	if err != nil {
		// If task is already stopped, that's fine
		r.logger.DebugContext(
			r.ctx,
			"failed to kill task, may already be stopped",
			"container",
			containerID,
			"error",
			err,
		)
		// Don't return error if task is already stopped
		return nil
	}

	return nil
}

// StopContainer stops a specific container in a cell.
func (r *Exec) StopContainer(doc *v1beta1.CellDoc, containerID string) error {
	if doc == nil {
		return errdefs.ErrCellNotFound
	}

	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errors.New("container ID is required")
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

	// Create a background context for containerd operations
	ctrCtx := context.Background()

	// Always create a fresh client with background context to avoid cancellation issues
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
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Use container name directly for containerd operations
	timeout := 5 * time.Second
	_, err = r.ctrClient.StopContainer(ctrCtx, containerID, ctr.StopContainerOptions{
		Force:   true,
		Timeout: &timeout,
	})
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to stop container",
			fields...,
		)
		return fmt.Errorf("failed to stop container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	fields = append(fields, "space", spaceID, "realm", realmID)
	r.logger.InfoContext(
		r.ctx,
		"stopped container",
		fields...,
	)

	return nil
}

// KillContainer immediately force-kills a specific container in a cell.
func (r *Exec) KillContainer(doc *v1beta1.CellDoc, containerID string) error {
	if doc == nil {
		return errdefs.ErrCellNotFound
	}

	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errors.New("container ID is required")
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

	// Create a background context for containerd operations
	ctrCtx := context.Background()

	// Always create a fresh client with background context to avoid cancellation issues
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
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Use container name directly for containerd operations
	err = r.killContainerTask(ctrCtx, containerID, realmDoc.Spec.Namespace)
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to kill container",
			fields...,
		)
		return fmt.Errorf("failed to kill container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	fields = append(fields, "space", spaceID, "realm", realmID)
	r.logger.InfoContext(
		r.ctx,
		"killed container",
		fields...,
	)

	return nil
}

// DeleteContainer stops and deletes a specific container in a cell from containerd.
func (r *Exec) DeleteContainer(doc *v1beta1.CellDoc, containerID string) error {
	if doc == nil {
		return errdefs.ErrCellNotFound
	}

	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errors.New("container ID is required")
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

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Create a background context for containerd operations
	ctrCtx := context.Background()

	// Stop the container
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

	// Delete the container from containerd
	err = r.ctrClient.DeleteContainer(ctrCtx, containerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to delete container",
			fields...,
		)
		return fmt.Errorf("failed to delete container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	fields = append(fields, "space", spaceID, "realm", realmID)
	r.logger.InfoContext(
		r.ctx,
		"deleted container",
		fields...,
	)

	return nil
}

func (r *Exec) resolveSpaceCNIConfigPath(spaceID string) (string, error) {
	spaceDoc, err := r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: spaceID,
		},
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	confPath := strings.TrimSpace(spaceDoc.Spec.CNIConfigPath)
	if confPath != "" {
		return confPath, nil
	}

	confPath, err = fs.SpaceNetworkConfigPath(r.opts.RunPath, spaceDoc.Metadata.Name)
	if err != nil {
		return "", fmt.Errorf("failed to build default space CNI config path: %w", err)
	}
	return confPath, nil
}
