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
	"time"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// StopCell stops all containers in the cell (workload containers first, then root container).
// It detaches the root container from the CNI network before stopping it.
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

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmID, spaceID)
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
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmID,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

	// Stop all workload containers first
	for _, containerSpec := range doc.Spec.Containers {
		// Build container ID using hierarchical format
		var containerID string
		containerID, err = naming.BuildContainerName(
			containerSpec.SpaceID,
			containerSpec.StackID,
			containerSpec.CellID,
			containerSpec.ID,
		)
		if err != nil {
			return fmt.Errorf("failed to build container name: %w", err)
		}

		// Use container name with UUID for containerd operations
		timeout := 5 * time.Second
		_, err = r.ctrClient.StopContainer(ctrCtx, containerID, ctr.StopContainerOptions{
			Force:   true,
			Timeout: &timeout,
		})
		if err != nil {
			// Log warning but continue with other containers
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container, continuing",
				fields...,
			)
			// Continue with other containers
			continue
		}

		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"stopped container",
			fields...,
		)
	}

	// Stop root container last (after all workload containers are stopped)
	rootContainerID, err := naming.BuildRootContainerName(spaceID, stackID, cellID)
	if err != nil {
		return fmt.Errorf("failed to build root container name: %w", err)
	}

	// Get root container's PID before stopping (needed for CNI detach)
	var rootPID uint32
	rootContainer, err := r.ctrClient.GetContainer(ctrCtx, rootContainerID)
	if err == nil {
		nsCtx := namespaces.WithNamespace(ctrCtx, internalRealm.Spec.Namespace)
		rootTask, taskErr := rootContainer.Task(nsCtx, nil)
		if taskErr == nil {
			rootPID = rootTask.Pid()
		}
	}

	// Stop root container
	timeout := 5 * time.Second
	_, err = r.ctrClient.StopContainer(ctrCtx, rootContainerID, ctr.StopContainerOptions{
		Force:   true,
		Timeout: &timeout,
	})
	if err != nil {
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.WarnContext(
			r.ctx,
			"failed to stop root container",
			fields...,
		)
		// Don't fail the whole operation if root container stop fails
	} else {
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"stopped root container",
			fields...,
		)
	}

	// Detach root container from CNI network after it's stopped
	// Use the PID we captured before stopping
	if rootPID > 0 {
		netnsPath := fmt.Sprintf("/proc/%d/ns/net", rootPID)
		cniMgr, mgrErr := cni.NewManager(
			r.cniConf.CniBinDir,
			r.cniConf.CniConfigDir,
			r.cniConf.CniCacheDir,
		)
		if mgrErr == nil {
			if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr == nil {
				if delErr := cniMgr.DelContainerFromNetwork(ctrCtx, rootContainerID, netnsPath); delErr != nil {
					// Log warning but continue - network might already be detached
					fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
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
						"failed to detach root container from network, continuing",
						fields...,
					)
				} else {
					fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
					fields = append(fields, "space", spaceID, "realm", realmID, "netns", netnsPath)
					r.logger.InfoContext(
						r.ctx,
						"detached root container from network",
						fields...,
					)
				}
			}
		}
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
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmID,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

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
