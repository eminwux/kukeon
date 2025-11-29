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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// PurgeStack performs comprehensive cleanup of a stack, including CNI resources and orphaned containers.
func (r *Exec) PurgeStack(stack intmodel.Stack) error {
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

	// First, perform standard delete
	if err := r.DeleteStack(stack); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get stack document via internal model to ensure metadata accuracy
	internalStack, getStackErr := r.GetStack(stack)
	stackForOps := stack
	if getStackErr == nil {
		stackForOps = internalStack
	} else if !errors.Is(getStackErr, errdefs.ErrStackNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetStack, getStackErr)
	}

	// Get realm and space
	lookupRealmForStack := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: stackForOps.Spec.RealmName,
		},
	}
	internalRealmForStack, err := r.GetRealm(lookupRealmForStack)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", err)
		return nil
	}
	realmNamespace := internalRealmForStack.Spec.Namespace

	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: stackForOps.Spec.SpaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: stackForOps.Spec.RealmName,
		},
	}
	internalSpace, err := r.GetSpace(lookupSpace)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get space for purge", "error", err)
		return nil
	}
	networkName, _ := r.getSpaceNetworkName(internalSpace)

	// Find all containers in stack
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err == nil {
		defer r.ctrClient.Close()
		r.ctrClient.SetNamespace(realmNamespace)

		pattern := fmt.Sprintf(
			"%s-%s-%s",
			stackForOps.Spec.RealmName,
			stackForOps.Spec.SpaceName,
			stackForOps.Metadata.Name,
		)
		var containers []string
		containers, err = r.findContainersByPattern(r.ctx, realmNamespace, pattern)
		if err == nil {
			ctrCtx := context.Background()
			for _, containerID := range containers {
				netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)
				_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)
			}
		}
	}

	// Force remove stack cgroup
	spec := cgroups.DefaultStackSpec(stackForOps)
	if r.ctrClient != nil {
		mountpoint := r.ctrClient.GetCgroupMountpoint()
		_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.StackMetadataDir(
		r.opts.RunPath,
		stackForOps.Spec.RealmName,
		stackForOps.Spec.SpaceName,
		stackForOps.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}
