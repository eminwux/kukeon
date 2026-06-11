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
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// purgeCNIForContainer removes all CNI-related resources for a specific container.
// It always attempts cleanup even if the container is already deleted or network namespace is invalid.
func (r *Exec) purgeCNIForContainer(containerID, netnsPath, networkName string) error {
	var purged []string

	// Never run the plugin-chain DEL against the daemon's own netns (issue
	// #1219); the IPAM-file purge below is unaffected and still releases the
	// lease.
	netnsPath = r.sanitizeDELNetns(netnsPath)

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
					// Attempt CNI DEL even when netnsPath is empty. libcni's
					// DelNetworkList is netns-optional: it reconstructs teardown
					// (including the bridge plugin's ipMasq iptables chains/rules)
					// from the cached CNI result, which the cache-removal block
					// below only clears *after* this DEL has run. A stopped cell
					// has no containerd task and thus no netns path, so gating DEL
					// on netnsPath != "" permanently leaked the per-cell CNI-*
					// masquerade chains on every delete-while-stopped (issue #1174).
					if err = cniMgr.DelContainerFromNetwork(r.ctx, containerID, netnsPath); err == nil {
						purged = append(purged, "cni-del")
						r.logger.DebugContext(r.ctx, "called CNI DEL for container", "container", containerID)
					} else {
						r.logger.DebugContext(r.ctx, "CNI DEL failed (netns may be invalid)", "container", containerID, "error", err)
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

// teardownSpaceCNI runs the conflist+bridge teardown for a single network.
// It is best-effort: failures are logged but do not propagate so callers can
// continue with other cleanup steps. configPath may be empty; the cni.Manager
// derives the default location from networkName.
func (r *Exec) teardownSpaceCNI(networkName, configPath string) {
	if networkName == "" {
		return
	}
	mgr, err := cni.NewManager(r.cniConf.CniBinDir, r.cniConf.CniConfigDir, r.cniConf.CniCacheDir)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to create CNI manager for teardown", "network", networkName, "error", err)
		return
	}
	if err = mgr.TeardownNetwork(r.ctx, cni.IPBridgeRunner{}, networkName, configPath); err != nil {
		r.logger.WarnContext(r.ctx, "failed to teardown CNI network", "network", networkName, "error", err)
	}
}

// teardownRealmCNI enumerates each space directory under <RunPath>/<realm>/
// and tears its network down (conflist + bridge link). The conflist is the
// source of truth for the bridge name, so this is the only enumeration that
// reliably finds every leaked bridge. Best-effort: per-entry failures are
// logged and skipped.
func (r *Exec) teardownRealmCNI(realmName string) {
	if realmName == "" {
		return
	}
	realmDir := fs.RealmMetadataDir(r.opts.RunPath, realmName)
	entries, err := os.ReadDir(realmDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.logger.WarnContext(r.ctx, "failed to read realm metadata dir", "dir", realmDir, "error", err)
		}
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		spaceName := entry.Name()
		confPath, perr := fs.SpaceNetworkConfigPath(r.opts.RunPath, realmName, spaceName)
		if perr != nil {
			continue
		}
		if _, statErr := os.Stat(confPath); statErr != nil {
			// No conflist (and therefore no bridge) for this space dir.
			continue
		}
		networkName, nerr := naming.BuildSpaceNetworkName(realmName, spaceName)
		if nerr != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to derive network name",
				"realm",
				realmName,
				"space",
				spaceName,
				"error",
				nerr,
			)
			continue
		}
		r.teardownSpaceCNI(networkName, confPath)
	}
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

	// List all containers
	r.logger.DebugContext(r.ctx, "listing containers from containerd", "namespace", namespace, "pattern", pattern)
	containers, err := r.ctrClient.ListContainers(namespace)
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
func (r *Exec) getContainerNetnsPath(namespace, containerID string) (string, error) {
	if err := r.ensureClientConnected(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	container, err := r.ctrClient.GetContainer(namespace, containerID)
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

// buildRootCNINetworkName derives the CNI network name for a cell's space
// deterministically from the realm and space names, without consulting space
// metadata. Teardown paths (stop/kill/delete) use this for the post-delete
// IPAM-file purge safety net (purgeCNIForContainer) so the purge runs even when
// space metadata is gone or corrupt — the network name is a pure function of
// (realm, space) per naming.BuildSpaceNetworkName, so a metadata-consulting
// GetSpace round-trip is unnecessary here and its failure must not silently
// skip the purge (issue #685). Returns "" only when realm/space are empty,
// which the teardown callers already guard against.
func (r *Exec) buildRootCNINetworkName(realmName, spaceName string) string {
	networkName, err := naming.BuildSpaceNetworkName(realmName, spaceName)
	if err != nil {
		return ""
	}
	return networkName
}

// detachRootContainerFromNetwork detaches a root container from the CNI network.
// It resolves the root task's network namespace and calls CNI DEL to detach it.
// This should be called before killing or stopping the container so a live netns
// is used when one exists.
//
// The CNI DEL is issued unconditionally — with an empty netns when the root task
// is already dead or absent (OOM, crash, host reboot left the container record
// but no task). Previously the whole DEL was nested behind a live-task / PID>0
// guard, so the dead-task path was a silent no-op and the clean plugin-chain
// release never ran; only the post-delete IP-file purge safety net did (#715,
// the clean-DEL residual of the file-purge fix #708). host-local IPAM DEL keys
// off the container ID and tolerates a missing netns, and chained plugins are
// required by the CNI spec to complete DEL without error when their resources
// are already gone — so a best-effort empty-netns DEL still releases the
// reservation rather than leaking it.
func (r *Exec) detachRootContainerFromNetwork(
	rootContainerID, cniConfigPath, namespace, cellID, cellName, spaceID, realmID string,
) {
	netnsPath := r.sanitizeDELNetns(r.rootContainerNetnsPath(namespace, rootContainerID))

	cniMgr, mgrErr := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if mgrErr != nil {
		return
	}
	if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr != nil {
		return
	}

	fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
	fields = append(fields, "space", spaceID, "realm", realmID, "netns", netnsPath)
	if delErr := cniMgr.DelContainerFromNetwork(r.ctx, rootContainerID, netnsPath); delErr != nil {
		// Log warning but continue - will try comprehensive cleanup after deletion
		fields = append(fields, "err", fmt.Sprintf("%v", delErr))
		r.logger.WarnContext(
			r.ctx,
			"failed to detach root container from network, will try comprehensive cleanup after deletion",
			fields...,
		)
		return
	}
	r.logger.InfoContext(r.ctx, "detached root container from network", fields...)
}

// rootContainerNetnsPath resolves the network-namespace path of a cell's root
// task, or "" when the container record is absent, its task is already dead, or
// the PID is unset. The "" cases are the dead/absent-task path detachRootContainerFromNetwork
// still issues a best-effort CNI DEL on (#715).
func (r *Exec) rootContainerNetnsPath(namespace, rootContainerID string) string {
	rootContainer, err := r.ctrClient.GetContainer(namespace, rootContainerID)
	if err != nil {
		return ""
	}
	nsCtx := namespaces.WithNamespace(r.ctx, namespace)
	rootTask, taskErr := rootContainer.Task(nsCtx, nil)
	if taskErr != nil {
		return ""
	}
	if rootPID := rootTask.Pid(); rootPID > 0 {
		return fmt.Sprintf("/proc/%d/ns/net", rootPID)
	}
	return ""
}

// sanitizeDELNetns drops a CNI DEL target netns that resolves to the daemon's
// own network namespace, returning "" so libcni issues its netns-optional DEL
// (which still releases the host-local IPAM lease and any chained-plugin
// resources from the cached ADD result) instead of running the plugin chain
// against the runner's own netns.
//
// A HostNetwork root container (kukeond, and any future host-scope cell) shares
// the daemon host's netns, so its live-task netns path resolves to the runner's
// own. Running the conflist's DEL there makes the bridge plugin delete the
// interface hard-coded as IfName ("eth0") in that netns and the loopback plugin
// set "lo" DOWN — on a bare host that is latent, but in nested mode (the agent's
// `make dev-init` inside a kukeon-dev-root cell) the "host netns" is the parent
// cell's, whose uplink veth is literally named eth0, so `kuke daemon reset`
// severs the parent cell's network (issue #1219). This is defense in depth
// behind the call-site HostNetwork skip: it also guards every other teardown
// path (purge/delete/recreate) and any future caller that resolves a self netns.
func (r *Exec) sanitizeDELNetns(netnsPath string) string {
	if delNetnsIsRunnerOwn(netnsPath) {
		r.logger.WarnContext(
			r.ctx,
			"refusing CNI DEL against the daemon's own netns; issuing a netns-less DEL instead",
			"netns", netnsPath,
		)
		return ""
	}
	return netnsPath
}

// delNetnsIsRunnerOwn reports whether netnsPath refers to the same network
// namespace as the daemon's own (/proc/self/ns/net), compared by (device,
// inode) — the canonical way to test two /proc/<pid>/ns/net handles for
// namespace identity. Returns false on an empty path or any stat error: an
// unstattable path cannot be confirmed equal to self, and preserving the
// resolved netns keeps the pre-existing DEL behavior for every non-self path.
func delNetnsIsRunnerOwn(netnsPath string) bool {
	if netnsPath == "" {
		return false
	}
	var target, self syscall.Stat_t
	if err := syscall.Stat(netnsPath, &target); err != nil {
		return false
	}
	if err := syscall.Stat("/proc/self/ns/net", &self); err != nil {
		return false
	}
	return target.Dev == self.Dev && target.Ino == self.Ino
}

// rootContainerHostNetwork reports whether the cell's root container runs on the
// host netns. It mirrors the ADD-side rootContainerWantsCNI guard for the DEL
// side: the stop/kill teardown paths skip CNI detach for a host-network root so
// the DEL never targets the daemon's own netns (issue #1219).
func rootContainerHostNetwork(cell intmodel.Cell) bool {
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == cell.Spec.RootContainerID {
			return cell.Spec.Containers[i].HostNetwork
		}
	}
	return false
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
//
// Stop/Delete failures are logged at WARN with the container ID and underlying
// error so an operator can tell which container survived the drain. The drain
// is best-effort by design — we keep going past per-container failures so a
// single stuck container does not strand the rest — but the log line is the
// only signal the caller has to surface the residual containers in the
// "namespace not empty" precondition error from DeleteNamespace.
func (r *Exec) processOrphanedContainers(ctx context.Context, namespace string, containers []string) {
	if len(containers) == 0 {
		return
	}

	r.logger.InfoContext(ctx, "processing orphaned containers for deletion", "count", len(containers))
	for i, containerID := range containers {
		r.logger.DebugContext(ctx, "processing container", "index", i+1, "total", len(containers), "id", containerID)
		// Force-stop: realm/space teardown is destructive by intent. Default
		// SIGTERM with a 10s grace period leaves SIGKILL on the table for any
		// stuck task, and a still-running task pins its snapshot mount —
		// blocking CleanupNamespaceResources and the subsequent
		// DeleteNamespace with "namespace not empty".
		r.logger.DebugContext(ctx, "stopping container", "id", containerID)
		if _, stopErr := r.ctrClient.StopContainer(namespace, containerID, ctr.StopContainerOptions{Force: true}); stopErr != nil {
			r.logger.WarnContext(
				ctx,
				"failed to stop orphaned container; continuing drain",
				"id",
				containerID,
				"error",
				stopErr,
			)
		}
		r.logger.DebugContext(ctx, "deleting container", "id", containerID)
		if delErr := r.ctrClient.DeleteContainer(namespace, containerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		}); delErr != nil {
			r.logger.WarnContext(
				ctx,
				"failed to delete orphaned container; continuing drain",
				"id",
				containerID,
				"error",
				delErr,
			)
		}

		// Get netns and purge CNI
		r.logger.DebugContext(ctx, "getting container netns path", "id", containerID)
		netnsPath, _ := r.getContainerNetnsPath(namespace, containerID)
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

// populateCellContainerStatuses queries containerd for the status of all containers
// in a cell and populates cell.Status.Containers array.
//
// Stage carry-forward (phase C1, #690): per-create-stage done records are
// merged with the previously-persisted Stages snapshot before the overwrite,
// so a stop -> start cycle preserves the run-once "done" set the
// (phase C2, #737) render gate consumes — the live pull is empty while a
// container is non-Ready and would otherwise wipe the records on every stop
// (see mergeStageStatuses for the Index + Hash gate). The merge is anchored
// on the live cell.Spec, so an edited create stage's prior done drops on the
// next populate.
func (r *Exec) populateCellContainerStatuses(cell *intmodel.Cell) error {
	if len(cell.Spec.Containers) == 0 {
		cell.Status.Containers = []intmodel.ContainerStatus{}
		return nil
	}

	// Snapshot prior Stages keyed by container ID so the post-pull merge can
	// re-thread done records across a stop -> start. The unconditional
	// cell.Status.Containers overwrite at the end of this function would
	// otherwise drop them whenever the live pull returns nil (container not
	// Ready, or pull failed) and strand phase C2's render gate.
	priorStages := make(map[string][]intmodel.StageStatus, len(cell.Status.Containers))
	// Snapshot prior CreatedAt by container ID so the AGE column on
	// `kuke get container` survives the unconditional overwrite below.
	// Issue #605.
	priorCreatedAt := make(map[string]time.Time, len(cell.Status.Containers))
	// Snapshot prior StartTime/FinishTime by container ID, same observe-and-
	// preserve contract as CreatedAt: containerd's task status carries no start
	// time, and a Stopped task's ExitTime is lost once its record is reaped, so
	// both timestamps must survive the unconditional overwrite below rather than
	// reset to the zero value on every pull. Issue #1137.
	priorStartTime := make(map[string]time.Time, len(cell.Status.Containers))
	priorFinishTime := make(map[string]time.Time, len(cell.Status.Containers))
	// Snapshot prior ExitCode under the same contract as FinishTime: once a
	// Stopped task's record is reaped, the ErrTaskNotFound -> Stopped branch
	// reports a zero ExitCode/ExitTime, so a preserved FinishTime would otherwise
	// pair with a reset ExitCode/ExitSignal (a SIGKILLed container showing
	// FinishTime=T with exit code 0 / no signal — a self-contradictory status).
	// Preserving ExitCode in lockstep keeps the exit triple consistent. Issue #1137.
	priorExitCode := make(map[string]int, len(cell.Status.Containers))
	for _, prev := range cell.Status.Containers {
		priorStages[prev.ID] = prev.Stages
		priorCreatedAt[prev.ID] = prev.CreatedAt
		priorStartTime[prev.ID] = prev.StartTime
		priorFinishTime[prev.ID] = prev.FinishTime
		priorExitCode[prev.ID] = prev.ExitCode
	}

	statuses := make([]intmodel.ContainerStatus, 0, len(cell.Spec.Containers))
	now := r.nowUTC()

	for _, containerSpec := range cell.Spec.Containers {
		// Get container observation (state + exit code) from containerd.
		// ExitCode flows into ContainerStatus so the RestartPolicy gate in
		// refresh.go can distinguish clean exits (0) from failures on
		// `on-failure` policy. The state-only failure mode is preserved —
		// any error here zeros both fields and we log + continue, same as
		// the pre-#1003 behavior.
		obs, err := r.GetContainerObservation(*cell, containerSpec.ID)
		if err != nil {
			r.logger.DebugContext(r.ctx, "failed to get container observation",
				"container", containerSpec.ID,
				"error", err)
			obs = ContainerObservation{State: intmodel.ContainerStateUnknown}
		}

		// CreatedAt: stamp on the first observation, preserve thereafter.
		// Mirrors the realm/space/stack/cell lifecycle-stamp pattern in
		// metadata.go's stamp*Lifecycle helpers. Issue #605.
		createdAt := priorCreatedAt[containerSpec.ID]
		if createdAt.IsZero() {
			createdAt = now
		}

		// StartTime: containerd's task status exposes no start time, so stamp
		// it the first time the container is observed Ready (Running) and
		// preserve thereafter — the same observe-and-preserve contract as
		// CreatedAt (#605). A container that has never been Ready keeps a zero
		// StartTime. Issue #1137.
		startTime := priorStartTime[containerSpec.ID]
		if startTime.IsZero() && obs.State == intmodel.ContainerStateReady {
			startTime = now
		}

		// FinishTime / ExitCode move in lockstep: the wall-clock time containerd
		// recorded the task's death (obs.ExitTime, non-zero only once Stopped) and
		// the matching exit code. Both are cleared when the container is Ready
		// again (a running task has not finished and has no meaningful exit code);
		// both are refreshed only on a genuine exit observation (non-zero ExitTime,
		// i.e. the TaskStatus-success Stopped branch); otherwise both are preserved
		// across transient NotCreated/Unknown/reaped-task observations. Preserving
		// ExitCode here — not re-reading the obs value below — is what keeps a
		// reaped SIGKILL from showing FinishTime=T with exit code 0. Issue #1137.
		finishTime := priorFinishTime[containerSpec.ID]
		exitCode := priorExitCode[containerSpec.ID]
		switch {
		case obs.State == intmodel.ContainerStateReady:
			finishTime = time.Time{}
			exitCode = 0
		case !obs.ExitTime.IsZero():
			finishTime = obs.ExitTime
			exitCode = obs.ExitCode
		}

		// RestartCount/RestartTime require restart bookkeeping the runner does
		// not yet track; deferred to #1146 per issue #1137's "scope that part
		// separately". They stay at their zero values for now.
		status := intmodel.ContainerStatus{
			Name:         containerSpec.ID,
			ID:           containerSpec.ID,
			CreatedAt:    createdAt,
			State:        obs.State,
			RestartCount: 0,           // TODO(#1146): restart bookkeeping
			RestartTime:  time.Time{}, // TODO(#1146): restart bookkeeping
			StartTime:    startTime,
			FinishTime:   finishTime,
			ExitCode:     exitCode,
			ExitSignal:   exitSignalName(exitCode),
		}
		// Pull per-repo clone/fetch and per-create-stage outcomes over the
		// kuketty control socket (issues #642, #689) in a single dial.
		// Best-effort: only Attachable containers that declared repos[] or
		// create stages and are Ready can serve the verb, and any failure leaves
		// Repos/Stages empty rather than blocking the status read.
		var liveStages []intmodel.StageStatus
		status.Repos, liveStages = r.setupStatuses(cell, containerSpec, obs.State)
		// Merge live + prior stages against the current spec so done
		// records survive stop/start and edited stages drop their prior
		// done. See mergeStageStatuses for the Index + Hash contract.
		status.Stages = mergeStageStatuses(containerSpec, priorStages[containerSpec.ID], liveStages)
		statuses = append(statuses, status)
	}

	cell.Status.Containers = statuses
	return nil
}

// setupStatuses pulls the per-repo clone/fetch outcome and the per-create-stage
// outcome from a container's kuketty control socket via the GetSetupStatus RPC
// (issues #642, #689) in a single dial, mapping the wire payload into the
// internal RepoStatus / StageStatus the controller persists in
// ContainerStatus.Repos / .Stages. ContainerStatus is the single source of
// truth — there is no status file in the container.
//
// The pull is skipped (returns nil, nil) unless the container is Attachable,
// Ready, and declared at least one repo[] or runOn: create stage: a kuketty
// that has not reached Serve does not yet serve the verb, one that exited on a
// required-repo or create-stage failure never will (its outcome comes from the
// task-failed signal + kuketty log instead — AC #5), and a container with
// neither repos nor create stages has nothing to report. Any dial/call error is
// logged at debug and yields nil so a wedged or still-starting kuketty never
// stalls `kuke get`.
func (r *Exec) setupStatuses(
	cell *intmodel.Cell,
	containerSpec intmodel.ContainerSpec,
	state intmodel.ContainerState,
) ([]intmodel.RepoStatus, []intmodel.StageStatus) {
	if !containerSpec.Attachable || state != intmodel.ContainerStateReady {
		return nil, nil
	}
	if len(containerSpec.Repos) == 0 && !hasCreateStages(containerSpec) {
		return nil, nil
	}

	socketPath := fs.ContainerSocketSymlinkPath(
		r.opts.RunPath,
		cell.Spec.RealmName, cell.Spec.SpaceName, cell.Spec.StackName, cell.Metadata.Name,
		containerSpec.ID,
	)
	reply, err := pullSetupStatus(r.ctx, socketPath)
	if err != nil {
		r.logger.DebugContext(r.ctx, "failed to pull setup status",
			"container", containerSpec.ID,
			"socket", socketPath,
			"error", err)
		return nil, nil
	}
	return repoStatusToInternal(reply.Repos), stageStatusToInternal(reply.Stages)
}

// hasCreateStages reports whether the container declares at least one runOn:
// create TtyStage — the gate (alongside repos[]) for dialing kuketty's setup
// status verb. Mirrors cmd/kuketty's createStages selection.
func hasCreateStages(containerSpec intmodel.ContainerSpec) bool {
	if containerSpec.Tty == nil {
		return false
	}
	for _, s := range containerSpec.Tty.OnInit {
		if s.RunOn == v1beta1.RunOnCreate {
			return true
		}
	}
	return false
}

// PopulateAndPersistCellContainerStatuses populates container statuses from containerd
// and persists them by updating cell metadata. This should be used when the cell status
// changes need to be persisted to disk.
func (r *Exec) PopulateAndPersistCellContainerStatuses(cell *intmodel.Cell) error {
	if err := r.populateCellContainerStatuses(cell); err != nil {
		return err
	}
	return r.UpdateCellMetadata(*cell)
}
