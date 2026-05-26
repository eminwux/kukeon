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
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func (r *Exec) DeleteStack(stack intmodel.Stack) error {
	stackName := strings.TrimSpace(stack.Metadata.Name)
	if stackName == "" {
		return errdefs.ErrStackNameRequired
	}
	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}

	// Get the stack document
	internalStack, err := r.GetStack(stack)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			// Idempotent: stack doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}
	if err = r.ensureClientConnected(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	// Get realm and space for namespace and network access
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: internalStack.Spec.RealmName,
		},
	}
	internalRealm, realmErr := r.GetRealm(lookupRealm)
	if realmErr == nil {
		// Get space for network name
		lookupSpace := intmodel.Space{
			Metadata: intmodel.SpaceMetadata{
				Name: internalStack.Spec.SpaceName,
			},
			Spec: intmodel.SpaceSpec{
				RealmName: internalStack.Spec.RealmName,
			},
		}
		internalSpace, spaceErr := r.GetSpace(lookupSpace)
		if spaceErr == nil {
			networkName, _ := r.getSpaceNetworkName(internalSpace)

			// Find containers by pattern and purge CNI for each
			pattern := fmt.Sprintf(
				"%s-%s-%s",
				internalStack.Spec.RealmName,
				internalStack.Spec.SpaceName,
				internalStack.Metadata.Name,
			)
			containers, findErr := r.findContainersByPattern(internalRealm.Spec.Namespace, pattern)
			if findErr == nil {
				for _, containerID := range containers {
					netnsPath, _ := r.getContainerNetnsPath(internalRealm.Spec.Namespace, containerID)
					_ = r.purgeCNIForContainer(containerID, netnsPath, networkName)
				}
			}
		}
	}

	// Delete stack cgroup
	// Use the stored CgroupPath from stack status (includes full hierarchy path)
	// instead of rebuilding from DefaultStackSpec which only has the relative path
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	cgroupGroup := internalStack.Status.CgroupPath
	if cgroupGroup == "" {
		// Fallback to DefaultStackSpec if CgroupPath is not set (for backwards compatibility)
		spec := ctr.DefaultStackSpec(internalStack)
		cgroupGroup = spec.Group
	}
	err = r.ctrClient.DeleteCgroup(cgroupGroup, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete stack cgroup", "cgroup", cgroupGroup, "error", err)
		// Continue with metadata deletion
	}

	// Refuse to remove the stack while cell subdirectories survive under the
	// stack metadata dir. ListCells silently skips a cell whose metadata.json
	// is missing or unreadable (controller/runner/get.go), so the cascade
	// loop in controller.deleteStackCascade can complete with zero handled
	// cells while a partial cell subdir remains on disk. Removing the
	// stack's own metadata here would orphan that subdir behind a stack the
	// operator can no longer list — the failure surfaced as residue under
	// /opt/kukeon/data/<realm>/<space>/<stack>/ after `kuke del stack
	// --cascade` exited 0. Issue #905.
	metadataRunPath := fs.StackMetadataDir(
		r.opts.RunPath,
		internalStack.Spec.RealmName,
		internalStack.Spec.SpaceName,
		internalStack.Metadata.Name,
	)
	residue, residueErr := scanStackDirCellResidue(metadataRunPath)
	if residueErr != nil {
		return fmt.Errorf("%w: scan stack dir %q: %w", errdefs.ErrDeleteStack, metadataRunPath, residueErr)
	}
	if len(residue) > 0 {
		return fmt.Errorf(
			"%w: cell subdirectory residue under %s — cascade did not remove %s; inspect and remove by hand before re-running",
			errdefs.ErrDeleteStack, metadataRunPath, strings.Join(residue, ", "),
		)
	}

	// Delete stack metadata file
	metadataFilePath := fs.StackMetadataPath(
		r.opts.RunPath,
		internalStack.Spec.RealmName,
		internalStack.Spec.SpaceName,
		internalStack.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete stack metadata: %w", errdefs.ErrDeleteStack, err)
	}

	// Remove the now-residue-free stack metadata directory. metadata.DeleteMetadata
	// above removes the dir when empty, so this typically no-ops; surface any
	// error so the half-deleted state (metadata.json gone, dir surviving)
	// reported in issue #905 can never again be silently swallowed.
	if err = os.RemoveAll(metadataRunPath); err != nil {
		return fmt.Errorf("%w: failed to remove stack directory %s: %w", errdefs.ErrDeleteStack, metadataRunPath, err)
	}

	return nil
}

// scanStackDirCellResidue returns the names of any subdirectories under
// stackDir that look like surviving cell directories — anything that isn't
// one of the known scope-bound subdirs (secrets/blueprints/configs) and
// isn't the stack's own metadata.json or its lock sidecar. The names are
// sorted for stable error messages. A missing stackDir returns nil, nil.
func scanStackDirCellResidue(stackDir string) ([]string, error) {
	entries, err := os.ReadDir(stackDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	metaFile := consts.KukeonMetadataFile
	metaLock := metaFile + metadata.LockFileSuffix
	var residue []string
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case metaFile, metaLock,
			consts.KukeonSecretsSubdir,
			consts.KukeonBlueprintsSubdir,
			consts.KukeonConfigsSubdir:
			continue
		}
		residue = append(residue, name)
	}
	sort.Strings(residue)
	return residue, nil
}
