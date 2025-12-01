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
)

// PurgeSpace performs comprehensive cleanup of a space, including CNI resources and orphaned containers.
func (r *Exec) PurgeSpace(space intmodel.Space) error {
	spaceName := strings.TrimSpace(space.Metadata.Name)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	// First, perform standard delete
	if err := r.DeleteSpace(space); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get space via internal model to ensure metadata accuracy
	internalSpace, err := r.GetSpace(space)
	if err != nil && !errors.Is(err, errdefs.ErrSpaceNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	// Use internalSpace if available, otherwise use provided space as fallback
	spaceForOps := internalSpace
	spaceNotFound := errors.Is(err, errdefs.ErrSpaceNotFound)
	if spaceNotFound {
		spaceForOps = space
	}

	// Get realm
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: spaceForOps.Spec.RealmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", err)
		return nil
	}

	// Get network name
	var networkName string
	if !spaceNotFound {
		networkName, err = r.getSpaceNetworkName(internalSpace)
		if err == nil {
			// Purge entire network
			_ = r.purgeCNIForNetwork(networkName)
		}
	}

	// Find all containers in space
	if err = r.ensureClientConnected(); err == nil {
		r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

		pattern := fmt.Sprintf("%s-%s", spaceForOps.Spec.RealmName, spaceForOps.Metadata.Name)
		var containers []string
		containers, err = r.findContainersByPattern(internalRealm.Spec.Namespace, pattern)
		if err == nil {
			for _, containerID := range containers {
				netnsPath, _ := r.getContainerNetnsPath(containerID)
				_ = r.purgeCNIForContainer(containerID, netnsPath, networkName)
			}
		}
	}

	// Force remove space cgroup
	spec := ctr.DefaultSpaceSpec(spaceForOps)
	if r.ctrClient != nil {
		mountpoint := r.ctrClient.GetCgroupMountpoint()
		_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.SpaceMetadataDir(
		r.opts.RunPath,
		spaceForOps.Spec.RealmName,
		spaceForOps.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}
