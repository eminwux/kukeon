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
	"fmt"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// InspectSpaceNetwork returns the runtime view of a space's CNI network,
// read from the conflist in effect (spec.cniConfigPath, else the daemon
// default) (issue #1411). Read-only: it never allocates a subnet or writes a
// conflist.
func (r *Exec) InspectSpaceNetwork(space intmodel.Space) (cni.SpaceNetworkInfo, error) {
	realmName := strings.TrimSpace(space.Spec.RealmName)
	spaceName := strings.TrimSpace(space.Metadata.Name)
	if realmName == "" {
		return cni.SpaceNetworkInfo{}, errdefs.ErrRealmNameRequired
	}
	if spaceName == "" {
		return cni.SpaceNetworkInfo{}, errdefs.ErrSpaceNameRequired
	}
	networkName, err := naming.BuildSpaceNetworkName(realmName, spaceName)
	if err != nil {
		return cni.SpaceNetworkInfo{}, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	confPath, err := r.ResolveSpaceCNIConfigPath(realmName, spaceName)
	if err != nil {
		return cni.SpaceNetworkInfo{}, err
	}
	mgr, err := cni.NewManager(r.cniConf.CniBinDir, r.cniConf.CniConfigDir, r.cniConf.CniCacheDir)
	if err != nil {
		return cni.SpaceNetworkInfo{}, fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}
	defaultPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, realmName, spaceName)
	if err != nil {
		return cni.SpaceNetworkInfo{}, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	info, err := mgr.InspectSpaceNetwork(r.subnetAllocator, realmName, spaceName, networkName, confPath)
	info.DefaultConfigPath = defaultPath
	return info, err
}

// InspectCellNetwork reports whether the cell's root container task is
// running and, for a running bridged cell, the IPv4 libcni cached for the
// root container on CNI ADD (issue #1411). Read-only: it never runs CNI ADD.
// An empty ipv4 means no attachment is cached.
func (r *Exec) InspectCellNetwork(cell intmodel.Cell, hostNetwork bool) (bool, string, error) {
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	stackName := strings.TrimSpace(cell.Spec.StackName)
	cellID := strings.TrimSpace(cell.Spec.ID)
	if cellID == "" {
		cellID = strings.TrimSpace(cell.Metadata.Name)
	}
	rootID, err := naming.BuildRootContainerdID(spaceName, stackName, cellID)
	if err != nil {
		return false, "", err
	}
	if err = r.ensureClientConnected(); err != nil {
		return false, "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	realm, err := r.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: realmName}})
	if err != nil {
		return false, "", fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}
	namespace := realm.Spec.Namespace
	if namespace == "" {
		namespace = consts.RealmNamespace(realmName)
	}
	if !r.rootTaskRunning(namespace, rootID) {
		return false, "", nil
	}
	if hostNetwork {
		return true, "", nil
	}
	confPath, err := r.ResolveSpaceCNIConfigPath(realmName, spaceName)
	if err != nil {
		return true, "", err
	}
	mgr, err := cni.NewManager(r.cniConf.CniBinDir, r.cniConf.CniConfigDir, r.cniConf.CniCacheDir)
	if err != nil {
		return true, "", fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}
	if err = mgr.LoadNetworkConfigList(confPath); err != nil {
		// A running cell whose conflist is gone has no readable attachment.
		return true, "", nil //nolint:nilerr // reported as no cached IPv4
	}
	// libcni keys its result cache by network, container ID and interface;
	// the netns path is not part of the lookup.
	if ip := mgr.CachedIPv4ForContainer(rootID, ""); ip != nil {
		return true, ip.String(), nil
	}
	return true, "", nil
}

func (r *Exec) rootTaskRunning(namespace, containerdID string) bool {
	container, err := r.ctrClient.GetContainer(namespace, containerdID)
	if err != nil {
		return false
	}
	task, err := container.Task(r.ctx, nil)
	if err != nil {
		return false
	}
	status, err := task.Status(r.ctx)
	return err == nil && status.Status == containerd.Running
}
