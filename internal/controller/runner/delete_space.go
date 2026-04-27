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

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

func (r *Exec) DeleteSpace(space intmodel.Space) error {
	spaceName := strings.TrimSpace(space.Metadata.Name)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	// Get the space document
	internalSpace, err := r.GetSpace(space)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			// Idempotent: space doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}
	if err = r.ensureClientConnected(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	// Get realm to build cgroup spec and for namespace access
	realmName = internalSpace.Spec.RealmName
	if realmName == "" && internalSpace.Metadata.Labels != nil {
		if realmLabel, ok := internalSpace.Metadata.Labels[consts.KukeonRealmLabelKey]; ok &&
			strings.TrimSpace(realmLabel) != "" {
			realmName = strings.TrimSpace(realmLabel)
		}
	}

	// Get realm for namespace access
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, realmErr := r.GetRealm(lookupRealm)
	if realmErr != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for CNI cleanup", "error", realmErr)
	} else {
		// Set namespace for container operations
		r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

		// Find containers by pattern and purge CNI for each
		pattern := fmt.Sprintf("%s-%s", realmName, internalSpace.Metadata.Name)
		containers, findErr := r.findContainersByPattern(internalRealm.Spec.Namespace, pattern)
		if findErr == nil {
			networkName, _ := r.getSpaceNetworkName(internalSpace)
			for _, containerID := range containers {
				netnsPath, _ := r.getContainerNetnsPath(containerID)
				_ = r.purgeCNIForContainer(containerID, netnsPath, networkName)
			}
		}
	}

	// Remove egress policy (iptables chain + dispatch) before tearing
	// down the bridge itself. Idempotent: safe when no policy was ever
	// applied.
	if policyErr := r.removeSpaceEgressPolicy(internalSpace); policyErr != nil {
		r.logger.WarnContext(r.ctx, "failed to remove space egress policy", "error", policyErr)
		// Continue teardown even if policy removal fails.
	}

	// Delete CNI network config (and the bridge link it references) and
	// perform comprehensive CNI cleanup.
	var networkName string
	networkName, err = naming.BuildSpaceNetworkName(realmName, internalSpace.Metadata.Name)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to build network name, skipping CNI config deletion", "error", err)
	} else {
		confPath, _ := r.resolveSpaceCNIConfigPath(realmName, internalSpace.Metadata.Name)
		// teardownSpaceCNI reads the bridge name from confPath BEFORE removing
		// the conflist, then deletes the bridge link. Safe when confPath is "".
		r.teardownSpaceCNI(networkName, confPath)
		// Perform comprehensive CNI network cleanup (IPAM, cache entries, network directory)
		_ = r.purgeCNIForNetwork(networkName)
	}

	// Release the subnet allocation so the /24 becomes available for the
	// next space create. Best-effort: a leftover network.json blocks future
	// reuse but does not break the running daemon, so log and continue.
	if relErr := r.subnetAlloc().Release(realmName, internalSpace.Metadata.Name); relErr != nil {
		r.logger.WarnContext(r.ctx, "failed to release space subnet allocation",
			"realm", realmName, "space", internalSpace.Metadata.Name, "error", relErr)
	}

	// Delete space cgroup
	// Use the stored CgroupPath from space status (includes full hierarchy path)
	// instead of rebuilding from DefaultSpaceSpec which only has the relative path
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	cgroupGroup := internalSpace.Status.CgroupPath
	if cgroupGroup == "" {
		// Fallback to DefaultSpaceSpec if CgroupPath is not set (for backwards compatibility)
		spec := ctr.DefaultSpaceSpec(internalSpace)
		cgroupGroup = spec.Group
	}
	err = r.ctrClient.DeleteCgroup(cgroupGroup, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete space cgroup", "cgroup", cgroupGroup, "error", err)
		// Continue with metadata deletion
	}

	// Delete space metadata file
	metadataFilePath := fs.SpaceMetadataPath(
		r.opts.RunPath,
		internalSpace.Spec.RealmName,
		internalSpace.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete space metadata: %w", errdefs.ErrDeleteSpace, err)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.SpaceMetadataDir(
		r.opts.RunPath,
		internalSpace.Spec.RealmName,
		internalSpace.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}
