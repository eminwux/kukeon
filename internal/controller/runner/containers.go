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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// createCellContainers creates the pause container and all containers defined in the CellDoc.
// The pause container is created first, then all containers in doc.Spec.Containers are created.
func (r *Exec) createCellContainers(doc *v1beta1.CellDoc) (containerd.Container, error) {
	if doc == nil {
		return nil, errdefs.ErrCellNotFound
	}

	cellName := doc.Metadata.Name
	if cellName == "" {
		return nil, errdefs.ErrCellNotFound
	}

	realmID := doc.Spec.RealmID
	if realmID == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	spaceID := doc.Spec.SpaceID
	if spaceID == "" {
		return nil, errdefs.ErrSpaceNameRequired
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

	// Generate container ID
	containerID := naming.BuildContainerName(realmID, spaceID, cellName, "pause")

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
	labels["kukeon.io/cell"] = cellName
	labels["kukeon.io/space"] = spaceID
	labels["kukeon.io/realm"] = realmID

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
		ID:       containerID,
		Image:    image,
		Labels:   labels,
		SpecOpts: specOpts,
	}

	container, err := r.ctrClient.CreateContainer(ctrCtx, containerSpec)
	if err != nil {
		r.logger.ErrorContext(
			r.ctx,
			"failed to create pause container",
			"id",
			containerID,
			"cell",
			cellName,
			"err",
			fmt.Sprintf("%v", err),
		)
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCreatePauseContainer, err)
	}

	r.logger.InfoContext(
		r.ctx,
		"created pause container",
		"id",
		containerID,
		"cell",
		cellName,
		"space",
		spaceID,
		"realm",
		realmID,
	)

	// Create all containers defined in the CellDoc
	for _, containerSpec := range doc.Spec.Containers {
		_, err = r.ctrClient.CreateContainerFromSpec(
			ctrCtx,
			&containerSpec,
			cellName,
			realmID,
			spaceID,
		)
		if err != nil {
			r.logger.ErrorContext(
				r.ctx,
				"failed to create container from CellDoc",
				"id",
				containerSpec.ID,
				"cell",
				cellName,
				"err",
				fmt.Sprintf("%v", err),
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

	cellName := doc.Metadata.Name
	if cellName == "" {
		return nil, errdefs.ErrCellNotFound
	}

	realmID := doc.Spec.RealmID
	if realmID == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	spaceID := doc.Spec.SpaceID
	if spaceID == "" {
		return nil, errdefs.ErrSpaceNameRequired
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

	// Generate container ID (same as createCellContainers)
	containerID := naming.BuildContainerName(realmID, spaceID, cellName, "pause")

	// Check if container exists
	exists, err := r.ctrClient.ExistsContainer(ctrCtx, containerID)
	if err != nil {
		r.logger.ErrorContext(
			r.ctx,
			"failed to check if pause container exists",
			"id",
			containerID,
			"cell",
			cellName,
			"err",
			fmt.Sprintf("%v", err),
		)
		return nil, fmt.Errorf("failed to check if pause container exists: %w", err)
	}

	if exists {
		// Container exists, load and return it
		container, loadErr := r.ctrClient.GetContainer(ctrCtx, containerID)
		if loadErr != nil {
			r.logger.WarnContext(
				r.ctx,
				"pause container exists but failed to load",
				"id",
				containerID,
				"cell",
				cellName,
				"err",
				fmt.Sprintf("%v", loadErr),
			)
			return nil, fmt.Errorf("failed to load existing pause container: %w", loadErr)
		}
		r.logger.DebugContext(
			r.ctx,
			"pause container exists",
			"id",
			containerID,
			"cell",
			cellName,
			"space",
			spaceID,
			"realm",
			realmID,
		)
		return container, nil
	}

	// Container doesn't exist, create it
	r.logger.InfoContext(
		r.ctx,
		"pause container does not exist, creating",
		"id",
		containerID,
		"cell",
		cellName,
		"space",
		spaceID,
		"realm",
		realmID,
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
	labels["kukeon.io/cell"] = cellName
	labels["kukeon.io/space"] = spaceID
	labels["kukeon.io/realm"] = realmID

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
		ID:       containerID,
		Image:    image,
		Labels:   labels,
		SpecOpts: specOpts,
	}

	container, createErr := r.ctrClient.CreateContainer(ctrCtx, containerSpec)
	if createErr != nil {
		r.logger.ErrorContext(
			r.ctx,
			"failed to create pause container",
			"id",
			containerID,
			"cell",
			cellName,
			"err",
			fmt.Sprintf("%v", createErr),
		)
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCreatePauseContainer, createErr)
	}

	r.logger.InfoContext(
		r.ctx,
		"ensured pause container exists",
		"id",
		containerID,
		"cell",
		cellName,
		"space",
		spaceID,
		"realm",
		realmID,
	)

	// Ensure all containers defined in the CellDoc exist
	for _, containerSpec := range doc.Spec.Containers {
		exists, err = r.ctrClient.ExistsContainer(ctrCtx, containerSpec.ID)
		if err != nil {
			r.logger.ErrorContext(
				r.ctx,
				"failed to check if container exists",
				"id",
				containerSpec.ID,
				"cell",
				cellName,
				"err",
				fmt.Sprintf("%v", err),
			)
			return nil, fmt.Errorf("failed to check if container %s exists: %w", containerSpec.ID, err)
		}

		if !exists {
			r.logger.InfoContext(
				r.ctx,
				"container does not exist, creating",
				"id",
				containerSpec.ID,
				"cell",
				cellName,
				"space",
				spaceID,
				"realm",
				realmID,
			)
			_, err = r.ctrClient.CreateContainerFromSpec(
				ctrCtx,
				&containerSpec,
				cellName,
				realmID,
				spaceID,
			)
			if err != nil {
				// Check if the error indicates the container already exists
				// This can happen due to race conditions where the container
				// was created between the existence check and creation attempt
				errMsg := err.Error()
				if strings.Contains(errMsg, "container already exists") {
					r.logger.DebugContext(
						r.ctx,
						"container already exists (race condition), skipping",
						"id",
						containerSpec.ID,
						"cell",
						cellName,
						"space",
						spaceID,
						"realm",
						realmID,
					)
					continue
				}
				r.logger.ErrorContext(
					r.ctx,
					"failed to create container from CellDoc",
					"id",
					containerSpec.ID,
					"cell",
					cellName,
					"err",
					fmt.Sprintf("%v", err),
				)
				return nil, fmt.Errorf("failed to create container %s: %w", containerSpec.ID, err)
			}
		} else {
			r.logger.DebugContext(
				r.ctx,
				"container exists",
				"id",
				containerSpec.ID,
				"cell",
				cellName,
				"space",
				spaceID,
				"realm",
				realmID,
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

	cellName := doc.Metadata.Name
	if cellName == "" {
		return errdefs.ErrCellNotFound
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

	// Generate pause container ID
	containerID := naming.BuildContainerName(realmID, spaceID, cellName, "pause")

	// Start pause container
	_, err = r.ctrClient.StartContainer(ctrCtx, containerID, ctr.TaskSpec{})
	if err != nil {
		r.logger.ErrorContext(
			r.ctx,
			"failed to start pause container",
			"id",
			containerID,
			"cell",
			cellName,
			"err",
			fmt.Sprintf("%v", err),
		)
		return fmt.Errorf("failed to start pause container %s: %w", containerID, err)
	}

	r.logger.InfoContext(
		r.ctx,
		"started pause container",
		"id",
		containerID,
		"cell",
		cellName,
		"space",
		spaceID,
		"realm",
		realmID,
	)

	// Start all containers defined in the CellDoc
	for _, containerSpec := range doc.Spec.Containers {
		_, err = r.ctrClient.StartContainer(ctrCtx, containerSpec.ID, ctr.TaskSpec{})
		if err != nil {
			r.logger.ErrorContext(
				r.ctx,
				"failed to start container from CellDoc",
				"id",
				containerSpec.ID,
				"cell",
				cellName,
				"err",
				fmt.Sprintf("%v", err),
			)
			return fmt.Errorf("failed to start container %s: %w", containerSpec.ID, err)
		}

		r.logger.InfoContext(
			r.ctx,
			"started container",
			"id",
			containerSpec.ID,
			"cell",
			cellName,
			"space",
			spaceID,
			"realm",
			realmID,
		)
	}

	return nil
}
