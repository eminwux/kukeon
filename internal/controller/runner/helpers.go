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
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// purgeCNIForContainer removes all CNI-related resources for a specific container.
func (r *Exec) purgeCNIForContainer(containerID, netnsPath, networkName string) error {
	var purged []string

	// Try to call CNI DEL if netns is available
	if netnsPath != "" {
		cniConfigPath, err := r.findCNIConfigPath(networkName)
		if err == nil {
			var cniMgr *cni.Manager
			cniMgr, err = cni.NewManager(
				r.cniConf.CniBinDir,
				r.cniConf.CniConfigDir,
				r.cniConf.CniCacheDir,
			)
			if err == nil {
				if err = cniMgr.LoadNetworkConfigList(cniConfigPath); err == nil {
					if err = cniMgr.DelContainerFromNetwork(r.ctx, containerID, netnsPath); err == nil {
						purged = append(purged, "cni-del")
						r.logger.DebugContext(r.ctx, "called CNI DEL for container", "container", containerID)
					} else {
						r.logger.WarnContext(r.ctx, "failed to call CNI DEL", "container", containerID, "error", err)
					}
				}
			}
		}
	}

	// Remove IPAM allocation files
	if networkName != "" {
		ipamDir := filepath.Join(cni.CNINetworksDir, networkName)
		ipamFile := filepath.Join(ipamDir, containerID)
		if err := os.Remove(ipamFile); err == nil {
			purged = append(purged, "ipam-allocation")
			r.logger.DebugContext(r.ctx, "removed IPAM allocation", "container", containerID, "file", ipamFile)
		} else if !errors.Is(err, os.ErrNotExist) {
			r.logger.WarnContext(r.ctx, "failed to remove IPAM allocation", "container", containerID, "error", err)
		}
	}

	// Remove CNI cache entries
	cacheDirs := []string{
		filepath.Join(r.cniConf.CniCacheDir, "results"),
		"/var/lib/cni/results",
		"/opt/cni/cache/results",
	}

	for _, cacheDir := range cacheDirs {
		if cacheDir == "" {
			continue
		}
		// CNI results files are typically named with container ID
		// Pattern: {containerID} or {containerID}-{networkName}
		patterns := []string{
			containerID,
			fmt.Sprintf("%s-*", containerID),
		}

		for _, pattern := range patterns {
			matches, err := filepath.Glob(filepath.Join(cacheDir, pattern))
			if err == nil {
				for _, match := range matches {
					if err = os.Remove(match); err == nil {
						purged = append(purged, fmt.Sprintf("cache-entry:%s", filepath.Base(match)))
						r.logger.DebugContext(r.ctx, "removed CNI cache entry", "container", containerID, "file", match)
					} else if !errors.Is(err, os.ErrNotExist) {
						r.logger.WarnContext(r.ctx, "failed to remove CNI cache entry", "container", containerID, "file", match, "error", err)
					}
				}
			}
		}
	}

	if len(purged) > 0 {
		r.logger.InfoContext(r.ctx, "purged CNI resources for container", "container", containerID, "purged", purged)
	}

	return nil
}

// purgeCNIForNetwork removes all CNI-related resources for an entire network.
func (r *Exec) purgeCNIForNetwork(networkName string) error {
	if networkName == "" {
		return nil
	}

	var purged []string

	// Remove entire network directory from /var/lib/cni/networks/
	networkDir := filepath.Join(cni.CNINetworksDir, networkName)
	if err := os.RemoveAll(networkDir); err == nil {
		purged = append(purged, "network-directory")
		r.logger.DebugContext(r.ctx, "removed CNI network directory", "network", networkName, "dir", networkDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		r.logger.WarnContext(r.ctx, "failed to remove CNI network directory", "network", networkName, "error", err)
	}

	// Remove all related cache entries (files containing network name)
	cacheDirs := []string{
		filepath.Join(r.cniConf.CniCacheDir, "results"),
		"/var/lib/cni/results",
		"/opt/cni/cache/results",
	}

	for _, cacheDir := range cacheDirs {
		if cacheDir == "" {
			continue
		}
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			r.logger.WarnContext(r.ctx, "failed to read cache directory", "dir", cacheDir, "error", err)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			// Check if filename contains network name
			if strings.Contains(entry.Name(), networkName) {
				filePath := filepath.Join(cacheDir, entry.Name())
				if err = os.Remove(filePath); err == nil {
					purged = append(purged, fmt.Sprintf("cache-entry:%s", entry.Name()))
					r.logger.DebugContext(
						r.ctx,
						"removed CNI cache entry for network",
						"network",
						networkName,
						"file",
						filePath,
					)
				} else if !errors.Is(err, os.ErrNotExist) {
					r.logger.WarnContext(r.ctx, "failed to remove CNI cache entry", "network", networkName, "file", filePath, "error", err)
				}
			}
		}
	}

	if len(purged) > 0 {
		r.logger.InfoContext(r.ctx, "purged CNI resources for network", "network", networkName, "purged", purged)
	}

	return nil
}

// findOrphanedContainers lists all containers in containerd namespace matching a pattern,
// and returns container IDs that are not tracked in metadata.
func (r *Exec) findOrphanedContainers(namespace, pattern string) ([]string, error) {
	if r.ctrClient == nil {
		r.logger.DebugContext(r.ctx, "initializing containerd client for finding orphaned containers")
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Set namespace
	oldNamespace := r.ctrClient.Namespace()
	r.logger.DebugContext(
		r.ctx,
		"setting namespace for container listing",
		"namespace",
		namespace,
		"old_namespace",
		oldNamespace,
	)
	r.ctrClient.SetNamespace(namespace)
	defer r.ctrClient.SetNamespace(oldNamespace)

	// List all containers
	r.logger.DebugContext(r.ctx, "listing containers from containerd", "namespace", namespace, "pattern", pattern)
	containers, err := r.ctrClient.ListContainers(r.ctx)
	if err != nil {
		r.logger.ErrorContext(r.ctx, "failed to list containers", "error", err)
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}
	r.logger.DebugContext(r.ctx, "listed containers from containerd", "count", len(containers))

	var orphaned []string
	for _, container := range containers {
		containerID := container.ID()
		// Check if container ID matches the pattern
		if pattern != "" && !strings.Contains(containerID, pattern) {
			continue
		}
		orphaned = append(orphaned, containerID)
	}
	r.logger.DebugContext(
		r.ctx,
		"filtered orphaned containers",
		"total_listed",
		len(containers),
		"matching_pattern",
		len(orphaned),
		"pattern",
		pattern,
	)

	return orphaned, nil
}

// findCNIConfigPath attempts to find the CNI config path for a network.
// It tries to resolve by network name or by searching known locations.
func (r *Exec) findCNIConfigPath(networkName string) (string, error) {
	// Try to find config in standard location
	configPath := filepath.Join(r.cniConf.CniConfigDir, networkName+".conflist")
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	}

	// Try to find in run path (space network configs)
	// This requires listing spaces, which we'll do as a fallback
	return "", fmt.Errorf("CNI config not found for network %q", networkName)
}

// getContainerNetnsPath attempts to get the network namespace path for a container.
func (r *Exec) getContainerNetnsPath(containerID string) (string, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	container, err := r.ctrClient.GetContainer(r.ctx, containerID)
	if err != nil {
		return "", err
	}

	task, err := container.Task(r.ctx, nil)
	if err != nil {
		return "", err
	}

	pid := task.Pid()
	if pid > 0 {
		return fmt.Sprintf("/proc/%d/ns/net", pid), nil
	}

	return "", errors.New("container task has no PID")
}

// findContainersByPattern finds all containers matching a naming pattern.
// Pattern format: realm-space-stack-cell or realm-space-stack-cell-container.
func (r *Exec) findContainersByPattern(namespace, pattern string) ([]string, error) {
	r.logger.DebugContext(r.ctx, "finding containers by pattern", "namespace", namespace, "pattern", pattern)
	containers, err := r.findOrphanedContainers(namespace, pattern)
	if err != nil {
		r.logger.ErrorContext(r.ctx, "failed to find orphaned containers for pattern", "pattern", pattern, "error", err)
		return nil, err
	}

	// Filter containers that match the exact pattern
	var matched []string
	patternParts := strings.Split(pattern, "-")
	for _, containerID := range containers {
		containerParts := strings.Split(containerID, "-")
		if len(containerParts) < len(patternParts) {
			continue
		}
		// Check if container ID starts with pattern
		if strings.HasPrefix(containerID, pattern) {
			matched = append(matched, containerID)
		}
	}
	r.logger.DebugContext(
		r.ctx,
		"filtered containers by pattern",
		"total_found",
		len(containers),
		"matched",
		len(matched),
		"pattern",
		pattern,
	)

	return matched, nil
}

// getSpaceNetworkName gets the network name for a space.
func (r *Exec) getSpaceNetworkName(space intmodel.Space) (string, error) {
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" && space.Metadata.Labels != nil {
		if realmLabel, ok := space.Metadata.Labels[consts.KukeonRealmLabelKey]; ok &&
			strings.TrimSpace(realmLabel) != "" {
			realmName = strings.TrimSpace(realmLabel)
		}
	}
	return naming.BuildSpaceNetworkName(realmName, space.Metadata.Name)
}

func appendCellLogFields(fields []any, cellID, cellName string) []any {
	fields = append(fields, "cell", cellID)
	if cellName != "" && cellName != cellID {
		fields = append(fields, "cellName", cellName)
	}
	return fields
}

func buildRootContainerLabels(cell intmodel.Cell) map[string]string {
	labels := make(map[string]string)
	if cell.Metadata.Labels != nil {
		for k, v := range cell.Metadata.Labels {
			labels[k] = v
		}
	}
	labels["kukeon.io/cell"] = cell.Spec.ID
	if cell.Metadata.Name != "" {
		labels["kukeon.io/cell-name"] = cell.Metadata.Name
	}
	labels["kukeon.io/space"] = cell.Spec.SpaceName
	labels["kukeon.io/realm"] = cell.Spec.RealmName
	labels["kukeon.io/stack"] = cell.Spec.StackName
	return labels
}

func wrapConversionErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
}

// ContainerIDMinimumParts is the minimum number of parts needed in a container ID
// to extract the network name. Container ID format: realm-space-cell-container
// We need at least realm and space to form the network name: realm-space.
const ContainerIDMinimumParts = 2

// processOrphanedContainers processes a list of orphaned containers by stopping,
// deleting them, and purging their CNI resources.
func (r *Exec) processOrphanedContainers(ctx context.Context, containers []string) {
	if len(containers) == 0 {
		return
	}

	r.logger.InfoContext(ctx, "processing orphaned containers for deletion", "count", len(containers))
	for i, containerID := range containers {
		r.logger.DebugContext(ctx, "processing container", "index", i+1, "total", len(containers), "id", containerID)
		// Try to delete container
		r.logger.DebugContext(ctx, "stopping container", "id", containerID)
		_, _ = r.ctrClient.StopContainer(ctx, containerID, ctr.StopContainerOptions{})
		r.logger.DebugContext(ctx, "deleting container", "id", containerID)
		_ = r.ctrClient.DeleteContainer(ctx, containerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})

		// Get netns and purge CNI
		r.logger.DebugContext(ctx, "getting container netns path", "id", containerID)
		netnsPath, _ := r.getContainerNetnsPath(containerID)
		// Try to determine network name from container ID pattern
		// Container ID format: realm-space-cell-container
		parts := strings.Split(containerID, "-")
		if len(parts) >= ContainerIDMinimumParts {
			networkName := fmt.Sprintf("%s-%s", parts[0], parts[1])
			r.logger.DebugContext(ctx, "purging CNI resources for container", "id", containerID, "network", networkName)
			_ = r.purgeCNIForContainer(containerID, netnsPath, networkName)
		}
		r.logger.DebugContext(ctx, "completed processing container", "id", containerID)
	}
	r.logger.InfoContext(ctx, "completed processing all orphaned containers", "count", len(containers))
}
