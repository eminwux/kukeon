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
	"strings"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
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
	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

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
		containers, findErr := r.findContainersByPattern(r.ctx, internalRealm.Spec.Namespace, pattern)
		if findErr == nil {
			ctrCtx := context.Background()
			networkName, _ := r.getSpaceNetworkName(internalSpace)
			for _, containerID := range containers {
				netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)
				_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)
			}
		}
	}

	// Delete CNI network config and perform comprehensive CNI cleanup
	var networkName string
	networkName, err = naming.BuildSpaceNetworkName(realmName, internalSpace.Metadata.Name)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to build network name, skipping CNI config deletion", "error", err)
	} else {
		var confPath string
		confPath, err = r.resolveSpaceCNIConfigPath(realmName, internalSpace.Metadata.Name)
		if err == nil {
			var mgr *cni.Manager
			mgr, err = cni.NewManager(
				r.cniConf.CniBinDir,
				r.cniConf.CniConfigDir,
				r.cniConf.CniCacheDir,
			)
			if err == nil {
				if err = mgr.DeleteNetwork(networkName, confPath); err != nil {
					r.logger.WarnContext(r.ctx, "failed to delete CNI network config", "network", networkName, "error", err)
					// Continue with comprehensive cleanup
				}
			}
		}
		// Perform comprehensive CNI network cleanup (IPAM, cache entries, network directory)
		_ = r.purgeCNIForNetwork(r.ctx, networkName)
	}

	// Delete space cgroup
	// Use the stored CgroupPath from space status (includes full hierarchy path)
	// instead of rebuilding from DefaultSpaceSpec which only has the relative path
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	cgroupGroup := internalSpace.Status.CgroupPath
	if cgroupGroup == "" {
		// Fallback to DefaultSpaceSpec if CgroupPath is not set (for backwards compatibility)
		spec := cgroups.DefaultSpaceSpec(internalSpace)
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
