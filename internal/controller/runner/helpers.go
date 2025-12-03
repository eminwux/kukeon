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
	"regexp"
	"strings"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// purgeCNIForContainer removes all CNI-related resources for a specific container.
// It always attempts cleanup even if the container is already deleted or network namespace is invalid.
func (r *Exec) purgeCNIForContainer(containerID, netnsPath, networkName string) error {
	var purged []string

	// Always try to call CNI DEL if network name is available
	// Some CNI plugins may handle empty/invalid netns gracefully
	if networkName != "" {
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
					// Attempt CNI DEL even if netnsPath is empty (best-effort cleanup)
					// Some CNI plugins may handle this gracefully
					if netnsPath != "" {
						if err = cniMgr.DelContainerFromNetwork(r.ctx, containerID, netnsPath); err == nil {
							purged = append(purged, "cni-del")
							r.logger.DebugContext(r.ctx, "called CNI DEL for container", "container", containerID)
						} else {
							r.logger.DebugContext(r.ctx, "CNI DEL failed (netns may be invalid)", "container", containerID, "error", err)
						}
					} else {
						r.logger.DebugContext(r.ctx, "skipping CNI DEL (no netns path available)", "container", containerID)
					}
				}
			}
		}
	}

	// Remove IPAM allocation files
	if networkName != "" {
		ipamDir := filepath.Join(cni.CNINetworksDir, networkName)

		// Try removing file named by container ID
		ipamFile := filepath.Join(ipamDir, containerID)
		if err := os.Remove(ipamFile); err == nil {
			purged = append(purged, "ipam-allocation")
			r.logger.DebugContext(r.ctx, "removed IPAM allocation", "container", containerID, "file", ipamFile)
		} else if !errors.Is(err, os.ErrNotExist) {
			r.logger.DebugContext(r.ctx, "failed to remove IPAM allocation file", "container", containerID, "file", ipamFile, "error", err)
		}

		// Also scan network directory for files that might contain this container ID
		// CNI may store IPAM allocations in files named by IP address that contain container IDs
		entries, err := os.ReadDir(ipamDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				fileName := entry.Name()
				filePath := filepath.Join(ipamDir, fileName)

				// Read file to check if it contains our container ID
				content, readErr := os.ReadFile(filePath)
				if readErr == nil {
					if containsExactContainerID(string(content), containerID) {
						if removeErr := os.Remove(filePath); removeErr == nil {
							purged = append(purged, fmt.Sprintf("ipam-file:%s", fileName))
							r.logger.DebugContext(
								r.ctx,
								"removed IPAM allocation file (found container ID in file)",
								"container",
								containerID,
								"file",
								filePath,
							)
						} else if !errors.Is(removeErr, os.ErrNotExist) {
							r.logger.DebugContext(r.ctx, "failed to remove IPAM allocation file", "container", containerID, "file", filePath, "error", removeErr)
						}
					}
				}
			}
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

// containsExactContainerID checks if the content contains the exact container ID,
// preventing false matches when container IDs share common prefixes.
// It first checks for an exact match (after trimming whitespace), then falls back
// to token-boundary matching for cases where the container ID appears in structured content.
func containsExactContainerID(content, containerID string) bool {
	if containerID == "" {
		return false
	}

	// First, check for exact match after trimming whitespace (most common case for CNI host-local IPAM)
	trimmed := strings.TrimSpace(content)
	if trimmed == containerID {
		return true
	}

	// If not exact match, use regexp to match container ID as a complete token
	// This handles cases where container ID appears in structured content (JSON, etc.)
	// We match container ID surrounded by whitespace, quotes, commas, or start/end of string
	// QuoteMeta escapes special regexp characters in containerID
	escapedID := regexp.QuoteMeta(containerID)
	// Pattern: start of string or non-word character (whitespace, quote, comma, etc.), then exact container ID, then end of string or non-word character
	// Note: We use [^\w] instead of \W to avoid issues with Unicode word boundaries
	pattern := `(^|[^\w])` + escapedID + `([^\w]|$)`
	matched, err := regexp.MatchString(pattern, content)
	if err != nil {
		// If regexp fails, fall back to exact substring match as last resort
		// (though this should never happen with valid container IDs)
		return strings.Contains(content, containerID)
	}

	return matched
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
	if err := r.ensureClientConnected(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

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
	containers, err := r.ctrClient.ListContainers()
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
	if err := r.ensureClientConnected(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	container, err := r.ctrClient.GetContainer(containerID)
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

// detachRootContainerFromNetwork detaches a root container from the CNI network.
// It gets the container's PID and network namespace path, then calls CNI DEL to detach it.
// This should be called before killing or stopping the container to ensure the namespace is still valid.
func (r *Exec) detachRootContainerFromNetwork(
	rootContainerID, cniConfigPath, namespace, cellID, cellName, spaceID, realmID string,
) {
	var netnsPath string
	rootContainer, err := r.ctrClient.GetContainer(rootContainerID)
	if err == nil {
		nsCtx := namespaces.WithNamespace(r.ctx, namespace)
		rootTask, taskErr := rootContainer.Task(nsCtx, nil)
		if taskErr == nil {
			rootPID := rootTask.Pid()
			if rootPID > 0 {
				netnsPath = fmt.Sprintf("/proc/%d/ns/net", rootPID)

				// Detach root container from CNI network BEFORE killing/stopping
				// This ensures the namespace is still valid
				cniMgr, mgrErr := cni.NewManager(
					r.cniConf.CniBinDir,
					r.cniConf.CniConfigDir,
					r.cniConf.CniCacheDir,
				)
				if mgrErr == nil {
					if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr == nil {
						if delErr := cniMgr.DelContainerFromNetwork(r.ctx, rootContainerID, netnsPath); delErr != nil {
							// Log warning but continue - will try comprehensive cleanup after deletion
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
								"failed to detach root container from network, will try comprehensive cleanup after deletion",
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
		}
	}
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

// isValidationError checks if an error is a validation error that should cause
// operations to fail fast rather than being logged and ignored.
func isValidationError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errdefs.ErrCellNameRequired) ||
		errors.Is(err, errdefs.ErrCellIDRequired) ||
		errors.Is(err, errdefs.ErrRealmNameRequired) ||
		errors.Is(err, errdefs.ErrSpaceNameRequired) ||
		errors.Is(err, errdefs.ErrStackNameRequired) ||
		errors.Is(err, errdefs.ErrContainerNameRequired)
}

// ContainerIDMinimumParts is the minimum number of parts needed in a container ID
// to extract the network name. Container ID format: realm-space-cell-container
// We need at least realm and space to form the network name: realm-space.
const ContainerIDMinimumParts = 2

// getRootContainerContainerdID extracts the containerd ID of the root container from a cell spec.
// It finds the container in cell.Spec.Containers where container.ID == cell.Spec.RootContainerID
// and returns its ContainerdID field.
func (r *Exec) getRootContainerContainerdID(cell intmodel.Cell) (string, error) {
	cellName := cell.Metadata.Name
	if cell.Spec.RootContainerID == "" {
		return "", fmt.Errorf("cell %q has no RootContainerID set", cellName)
	}

	var rootContainerSpec *intmodel.ContainerSpec
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == cell.Spec.RootContainerID {
			rootContainerSpec = &cell.Spec.Containers[i]
			break
		}
	}

	if rootContainerSpec == nil {
		return "", fmt.Errorf("root container %q not found in cell %q containers", cell.Spec.RootContainerID, cellName)
	}

	rootContainerID := rootContainerSpec.ContainerdID
	if rootContainerID == "" {
		return "", fmt.Errorf("root container %q has empty ContainerdID", cell.Spec.RootContainerID)
	}

	return rootContainerID, nil
}

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
		_, _ = r.ctrClient.StopContainer(containerID, ctr.StopContainerOptions{})
		r.logger.DebugContext(ctx, "deleting container", "id", containerID)
		_ = r.ctrClient.DeleteContainer(containerID, ctr.ContainerDeleteOptions{
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
