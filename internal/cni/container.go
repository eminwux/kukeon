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
	"net"

	libcni "github.com/containernetworking/cni/libcni"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// AddContainerToNetwork adds a container to the CNI network and returns the
// container's IPv4 address as assigned by IPAM, or nil when no IPv4 address
// was returned (e.g. IPv6-only networks). The IP is the one the runner
// renders into the cell's /etc/hosts so tools that resolve the container's
// own hostname work without DNS-lookup timeouts (issue #345).
func (m *Manager) AddContainerToNetwork(ctx context.Context, containerID, netnsPath string) (net.IP, error) {
	if m.netConf == nil {
		return nil, errdefs.ErrNetworkConfigNotLoaded
	}

	rt := buildRuntimeConf(containerID, netnsPath)
	rawResult, err := m.cniConf.AddNetworkList(ctx, m.netConf, rt)
	if err != nil {
		return nil, translateCNIError(err, m.netConf.Name, bridgeNameFromNetConf(m.netConf))
	}
	return firstIPv4FromResult(rawResult), nil
}

// CachedIPv4ForContainer returns the IPv4 address libcni cached for the
// given containerID under the manager's current network config, or nil when
// no usable IPv4 entry is cached. Used by the runner on the idempotent-skip
// path (ErrCNIVethExists) — when AddContainerToNetwork failed because veth
// setup already ran, the IPAM allocation persisted in the cache and is the
// authoritative source for the cell IP without re-issuing CNI ADD.
func (m *Manager) CachedIPv4ForContainer(containerID, netnsPath string) net.IP {
	if m.netConf == nil {
		return nil
	}
	rt := buildRuntimeConf(containerID, netnsPath)
	rawResult, err := m.cniConf.GetNetworkListCachedResult(m.netConf, rt)
	if err != nil || rawResult == nil {
		return nil
	}
	return firstIPv4FromResult(rawResult)
}

// firstIPv4FromResult extracts the first IPv4 address from a CNI result,
// converting through the libcni current schema so callers can stay agnostic
// of which CNI version the bridge plugin emits.
func firstIPv4FromResult(rawResult cnitypes.Result) net.IP {
	if rawResult == nil {
		return nil
	}
	res, err := current.NewResultFromResult(rawResult)
	if err != nil || res == nil {
		return nil
	}
	for _, ipc := range res.IPs {
		if ipc == nil {
			continue
		}
		if v4 := ipc.Address.IP.To4(); v4 != nil {
			return v4
		}
	}
	return nil
}

// DelContainerFromNetwork removes a container from the CNI network.
func (m *Manager) DelContainerFromNetwork(ctx context.Context, containerID, netnsPath string) error {
	if m.netConf == nil {
		return errdefs.ErrNetworkConfigNotLoaded
	}

	rt := buildRuntimeConf(containerID, netnsPath)
	err := m.cniConf.DelNetworkList(ctx, m.netConf, rt)
	return translateCNIError(err, m.netConf.Name, bridgeNameFromNetConf(m.netConf))
}

// buildRuntimeConf builds a RuntimeConf for container network operations.
func buildRuntimeConf(containerID, netnsPath string) *libcni.RuntimeConf {
	return &libcni.RuntimeConf{
		ContainerID: containerID,
		NetNS:       netnsPath, // e.g. /proc/<pid>/ns/net
		IfName:      "eth0",
	}
}
