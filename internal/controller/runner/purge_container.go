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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// PurgeContainer performs comprehensive cleanup of a container, including CNI resources.
func (r *Exec) PurgeContainer(realm intmodel.Realm, containerID string) error {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errdefs.ErrContainerNameRequired
	}

	realmName := strings.TrimSpace(realm.Metadata.Name)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	// Get realm to access namespace
	internalRealm, err := r.GetRealm(realm)
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return fmt.Errorf("realm %q has no namespace", realmName)
	}

	// Initialize ctr client
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	r.ctrClient.SetNamespace(namespace)

	ctrCtx := context.Background()

	// Try to stop and delete container
	_, _ = r.ctrClient.StopContainer(ctrCtx, containerID, ctr.StopContainerOptions{})
	_ = r.ctrClient.DeleteContainer(ctrCtx, containerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})

	// Get netns path if container is running
	netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)

	// Try to determine network name from container ID
	parts := strings.Split(containerID, "-")
	networkName := ""
	if len(parts) >= 2 {
		networkName = fmt.Sprintf("%s-%s", parts[0], parts[1])
	}

	// Purge CNI resources
	_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)

	return nil
}
