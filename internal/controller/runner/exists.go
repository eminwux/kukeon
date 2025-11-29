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

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

func (r *Exec) ExistsRealmContainerdNamespace(namespace string) (bool, error) {
	// Check if containerd socket exists before trying to connect
	// This avoids connection timeouts in test scenarios where containerd is not available
	if _, err := os.Stat(r.opts.ContainerdSocket); os.IsNotExist(err) {
		// Socket doesn't exist, return false (namespace doesn't exist) without error
		// This is appropriate for test scenarios and when containerd is not running
		return false, nil
	}

	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()
	return r.ctrClient.ExistsNamespace(namespace)
}

func (r *Exec) ExistsCellRootContainer(cell intmodel.Cell) (bool, error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return false, errdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return false, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return false, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return false, errdefs.ErrStackNameRequired
	}

	// Get the cell document to access cell ID
	internalCell, err := r.GetCell(cell)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	cellID := internalCell.Spec.ID
	if cellID == "" {
		return false, errdefs.ErrCellIDRequired
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}

	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return false, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return false, fmt.Errorf("realm %q has no namespace", realmName)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(namespace)

	// Generate containerd ID with cell identifier for uniqueness
	containerID, err := naming.BuildRootContainerdID(spaceName, stackName, cellID)
	if err != nil {
		return false, fmt.Errorf("failed to build root container containerd ID: %w", err)
	}

	// Check if container exists
	exists, err := r.ctrClient.ExistsContainer(r.ctx, containerID)
	if err != nil {
		return false, fmt.Errorf("failed to check if root container exists: %w", err)
	}

	return exists, nil
}

func (r *Exec) ExistsCgroup(doc any) (bool, error) {
	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	var spec ctr.CgroupSpec
	var err error

	// Build cgroup spec based on doc type
	switch d := doc.(type) {
	case intmodel.Realm:
		if d.Metadata.Name == "" {
			return false, errdefs.ErrRealmNotFound
		}
		spec = cgroups.DefaultRealmSpec(d)

	case intmodel.Space:
		if d.Metadata.Name == "" {
			return false, errdefs.ErrSpaceNotFound
		}
		if d.Spec.RealmName == "" {
			return false, errdefs.ErrRealmNameRequired
		}
		spec = cgroups.DefaultSpaceSpec(d)

	case intmodel.Stack:
		if d.Metadata.Name == "" {
			return false, errdefs.ErrStackNotFound
		}
		spec = cgroups.DefaultStackSpec(d)

	case intmodel.Cell:
		if d.Metadata.Name == "" {
			return false, errdefs.ErrCellNotFound
		}
		spec = cgroups.DefaultCellSpec(d)

	default:
		return false, fmt.Errorf("unsupported doc type: %T", doc)
	}

	// Build the cgroup path
	spec, _, err = r.buildCgroupPath(spec)
	if err != nil {
		return false, fmt.Errorf("failed to build cgroup path: %w", err)
	}

	// Check if cgroup exists
	_, err = r.ctrClient.LoadCgroup(spec.Group, spec.Mountpoint)
	if err != nil {
		// Check if error is "cgroup path does not exist"
		if err.Error() == "cgroup path does not exist" {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if cgroup exists: %w", err)
	}

	return true, nil
}

// ExistsSpaceCNIConfig checks if the CNI config for a space exists.
// It returns a bool and an error.
// The bool is true if the CNI config exists, false otherwise.
// The error is returned if the space name is required, the realm name is required, the CNI config does not exist, or the CNI config creation fails.
func (r *Exec) ExistsSpaceCNIConfig(space intmodel.Space) (bool, error) {
	spaceName := strings.TrimSpace(space.Metadata.Name)
	if spaceName == "" {
		return false, errdefs.ErrSpaceNameRequired
	}
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return false, errdefs.ErrRealmNameRequired
	}
	mgr, err := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, realmName, spaceName)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	networkName, err := naming.BuildSpaceNetworkName(realmName, spaceName)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	exists, _, err := mgr.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	return exists, nil
}
