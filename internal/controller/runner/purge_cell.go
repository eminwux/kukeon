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
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// PurgeCell performs comprehensive cleanup of a cell, including CNI resources and orphaned containers.
func (r *Exec) PurgeCell(cell intmodel.Cell) (PurgeCellOutcome, error) {
	result := PurgeCellOutcome{Purged: []string{}}
	defer r.lockCell(cell)()

	if err := validatePurgeCell(cell); err != nil {
		return result, err
	}

	// Run the normal delete first. The later checks retry cleanup and report its
	// final state, so a recovered transient error does not produce a false failure.
	if err := r.deleteCellLocked(cell); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	cellForOps := cell
	if internalCell, err := r.GetCell(cell); err == nil {
		cellForOps = internalCell
	} else if !errors.Is(err, errdefs.ErrCellNotFound) {
		r.logger.WarnContext(r.ctx, "failed to reload cell for purge", "error", err)
	}

	if err := r.ensureClientConnected(); err != nil {
		return result, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	var cleanupErrors []error
	namespace, realmErr := r.purgeCellNamespace(cellForOps)
	if realmErr != nil {
		cleanupErrors = append(cleanupErrors, realmErr)
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", realmErr)
	}

	cellID := purgeCellID(cellForOps)
	networkName := r.buildRootCNINetworkName(cellForOps.Spec.RealmName, cellForOps.Spec.SpaceName)

	containerIDs, idErr := purgeCellContainerIDs(cellForOps, cellID)
	if idErr != nil {
		cleanupErrors = append(cleanupErrors, idErr)
	}

	cniErr := r.purgeCellCNIResources(namespace, networkName, containerIDs)
	if cniErr == nil && idErr == nil && networkName != "" {
		result.Purged = append(result.Purged, "cni-resources")
	} else {
		result.Purged = append(result.Purged, "cni-resources-incomplete")
		if cniErr != nil {
			cleanupErrors = append(cleanupErrors, cniErr)
		}
	}

	if symlinkErr := purgeCellSocketSymlinks(r.opts.RunPath, cellForOps); symlinkErr != nil {
		cleanupErrors = append(cleanupErrors, symlinkErr)
	}

	cgroupDeleted, cgroupErr := r.purgeCellCgroup(cellForOps)
	result.CgroupDeleted = cgroupDeleted
	if cgroupErr != nil {
		cleanupErrors = append(cleanupErrors, cgroupErr)
	} else {
		result.Purged = append(result.Purged, "cgroup")
	}

	metadataDeleted, metadataErr := r.purgeCellMetadata(cellForOps)
	result.MetadataDeleted = metadataDeleted
	if metadataErr != nil {
		cleanupErrors = append(cleanupErrors, metadataErr)
	} else {
		result.Purged = append(result.Purged, "all-metadata")
	}

	containersDeleted, containersErr := r.confirmPurgeCellContainers(namespace, cellForOps, cellID, containerIDs)
	result.ContainersDeleted = containersDeleted
	if containersErr != nil {
		cleanupErrors = append(cleanupErrors, containersErr)
	} else {
		result.Purged = append(result.Purged, "orphaned-containers")
	}

	return result, errors.Join(cleanupErrors...)
}

func validatePurgeCell(cell intmodel.Cell) error {
	if strings.TrimSpace(cell.Metadata.Name) == "" {
		return errdefs.ErrCellNameRequired
	}
	if strings.TrimSpace(cell.Spec.RealmName) == "" {
		return errdefs.ErrRealmNameRequired
	}
	if strings.TrimSpace(cell.Spec.SpaceName) == "" {
		return errdefs.ErrSpaceNameRequired
	}
	if strings.TrimSpace(cell.Spec.StackName) == "" {
		return errdefs.ErrStackNameRequired
	}
	return nil
}

func (r *Exec) purgeCellNamespace(cell intmodel.Cell) (string, error) {
	lookup := intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: cell.Spec.RealmName}}
	realm, err := r.GetRealm(lookup)
	if err != nil {
		return "", fmt.Errorf("get realm for purge: %w", err)
	}
	if realm.Spec.Namespace == "" {
		return "", errors.New("realm has no containerd namespace")
	}
	return realm.Spec.Namespace, nil
}

func purgeCellID(cell intmodel.Cell) string {
	if id := strings.TrimSpace(cell.Spec.ID); id != "" {
		return id
	}
	return cell.Metadata.Name
}

func purgeCellContainerIDs(cell intmodel.Cell, cellID string) ([]string, error) {
	containerIDs := make([]string, 0, len(cell.Spec.Containers)+1)
	rootID, rootErr := naming.BuildRootContainerdID(cell.Spec.SpaceName, cell.Spec.StackName, cellID)
	if rootErr == nil {
		containerIDs = append(containerIDs, rootID)
	}

	var idErrors []error
	if rootErr != nil {
		idErrors = append(idErrors, fmt.Errorf("build root container ID: %w", rootErr))
	}
	for _, spec := range cell.Spec.Containers {
		containerID := strings.TrimSpace(spec.ContainerdID)
		if containerID == "" && strings.TrimSpace(spec.ID) != "" {
			spaceName := spec.SpaceName
			if spaceName == "" {
				spaceName = cell.Spec.SpaceName
			}
			stackName := spec.StackName
			if stackName == "" {
				stackName = cell.Spec.StackName
			}
			containerCell := spec.CellName
			if containerCell == "" {
				containerCell = cellID
			}
			var err error
			containerID, err = naming.BuildContainerdID(spaceName, stackName, containerCell, spec.ID)
			if err != nil {
				idErrors = append(idErrors, fmt.Errorf("build container ID for %q: %w", spec.ID, err))
				continue
			}
		}
		if containerID != "" {
			containerIDs = append(containerIDs, containerID)
		}
	}

	seen := make(map[string]struct{}, len(containerIDs))
	unique := containerIDs[:0]
	for _, containerID := range containerIDs {
		if _, ok := seen[containerID]; ok {
			continue
		}
		seen[containerID] = struct{}{}
		unique = append(unique, containerID)
	}
	return unique, errors.Join(idErrors...)
}

func (r *Exec) purgeCellCNIResources(namespace, networkName string, containerIDs []string) error {
	if networkName == "" {
		return errors.New("could not derive the cell CNI network name")
	}
	var cleanupErrors []error
	for _, containerID := range containerIDs {
		netnsPath := ""
		if namespace != "" {
			netnsPath, _ = r.getContainerNetnsPath(namespace, containerID)
		}
		if err := r.purgeCNIForContainer(containerID, netnsPath, networkName); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("purge CNI for container %q: %w", containerID, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func purgeCellSocketSymlinks(runPath string, cell intmodel.Cell) error {
	var cleanupErrors []error
	for _, spec := range cell.Spec.Containers {
		if err := removeAttachableSocketSymlink(runPath, spec); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (r *Exec) purgeCellCgroup(cell intmodel.Cell) (bool, error) {
	group := cell.Status.CgroupPath
	if group == "" {
		group = ctr.DefaultCellSpec(cell).Group
	}
	if err := r.ctrClient.DeleteCgroup(group, r.ctrClient.GetCgroupMountpoint()); err != nil {
		return false, fmt.Errorf("delete cell cgroup %q: %w", group, err)
	}
	return true, nil
}

func (r *Exec) purgeCellMetadata(cell intmodel.Cell) (bool, error) {
	metadataRunPath := fs.CellMetadataDir(
		r.opts.RunPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	if err := os.RemoveAll(metadataRunPath); err != nil {
		return false, fmt.Errorf("remove cell metadata directory %q: %w", metadataRunPath, err)
	}
	return true, nil
}

func (r *Exec) confirmPurgeCellContainers(
	namespace string,
	cell intmodel.Cell,
	cellID string,
	expectedContainerIDs []string,
) (bool, error) {
	if namespace == "" {
		return false, errors.New("cannot confirm cell container removal without a realm namespace")
	}
	containers, err := r.ctrClient.ListContainers(namespace)
	if err != nil {
		return false, fmt.Errorf("list containers after purge: %w", err)
	}

	expected := make(map[string]struct{}, len(expectedContainerIDs))
	for _, id := range expectedContainerIDs {
		expected[id] = struct{}{}
	}
	prefix := fmt.Sprintf("%s_%s_%s_", cell.Spec.SpaceName, cell.Spec.StackName, cellID)
	var remaining []string
	for _, container := range containers {
		id := container.ID()
		_, expectedID := expected[id]
		if expectedID || strings.HasPrefix(id, prefix) {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) > 0 {
		return false, fmt.Errorf("containers remain after purge: %s", strings.Join(remaining, ", "))
	}
	return len(cell.Spec.Containers) > 0, nil
}
