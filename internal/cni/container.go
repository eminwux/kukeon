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

package cni

import (
	"context"

	libcni "github.com/containernetworking/cni/libcni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// AddContainerToNetwork adds a container to the CNI network.
func (m *Manager) AddContainerToNetwork(ctx context.Context, containerID, netnsPath string) error {
	if m.netConf == nil {
		return errdefs.ErrNetworkConfigNotLoaded
	}

	rt := buildRuntimeConf(containerID, netnsPath)
	_, err := m.cniConf.AddNetworkList(ctx, m.netConf, rt)
	return err
}

// DelContainerFromNetwork removes a container from the CNI network.
func (m *Manager) DelContainerFromNetwork(ctx context.Context, containerID, netnsPath string) error {
	if m.netConf == nil {
		return errdefs.ErrNetworkConfigNotLoaded
	}

	rt := buildRuntimeConf(containerID, netnsPath)
	return m.cniConf.DelNetworkList(ctx, m.netConf, rt)
}

// buildRuntimeConf builds a RuntimeConf for container network operations.
func buildRuntimeConf(containerID, netnsPath string) *libcni.RuntimeConf {
	return &libcni.RuntimeConf{
		ContainerID: containerID,
		NetNS:       netnsPath, // e.g. /proc/<pid>/ns/net
		IfName:      "eth0",
	}
}
