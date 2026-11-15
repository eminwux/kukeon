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
	"errors"
	"fmt"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// Subnet sources reported by InspectSpaceNetwork.
const (
	// SubnetSourceAllocator: the subnet is persisted in the space's
	// allocator state file (network.json).
	SubnetSourceAllocator = "allocator"
	// SubnetSourceConflist: a legacy space created before the allocator; the
	// subnet is read from the conflist's IPAM range and no state file exists.
	SubnetSourceConflist = "conflist"
	// SubnetSourceUnallocated: neither a state file nor a conflist subnet
	// exists yet.
	SubnetSourceUnallocated = "unallocated"
)

// SpaceNetworkInfo is the runtime view of one space's CNI network (issue
// #1411): whether the conflist exists, the bridge it names, and the subnet
// in effect. ParentCIDR is the allocator block new subnets are carved from.
// ConfigPath is the conflist in effect; DefaultConfigPath is the daemon's
// default location, so callers can tell an override from the default.
type SpaceNetworkInfo struct {
	Exists            bool
	BridgeName        string
	SubnetCIDR        string
	SubnetSource      string
	ParentCIDR        string
	ConfigPath        string
	DefaultConfigPath string
}

// InspectSpaceNetwork reads the space's conflist at confPath and its
// allocator state, without modifying either. The conflist's IPAM subnet is
// what the bridge plugin applies, so it wins over the allocator state when
// both are present; the source still reads allocator when a state file
// exists.
func (m *Manager) InspectSpaceNetwork(
	alloc *SubnetAllocator, realm, space, networkName, confPath string,
) (SpaceNetworkInfo, error) {
	info := SpaceNetworkInfo{SubnetSource: SubnetSourceUnallocated, ConfigPath: confPath}
	exists, _, err := m.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return info, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	info.Exists = exists

	var conflistSubnet string
	if exists {
		bridge, bErr := m.ReadBridgeName(confPath)
		if bErr != nil && !errors.Is(bErr, errdefs.ErrBridgePluginMissing) {
			return info, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, bErr)
		}
		info.BridgeName = bridge
		subnet, sErr := m.ReadSubnetCIDR(confPath)
		if sErr != nil && !errors.Is(sErr, errdefs.ErrBridgePluginMissing) {
			return info, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, sErr)
		}
		conflistSubnet = subnet
	}

	persisted := ""
	if alloc != nil {
		info.ParentCIDR = alloc.ParentCIDR()
		persisted, err = alloc.LoadAssigned(realm, space)
		if err != nil {
			return info, err
		}
	}
	switch {
	case persisted != "":
		info.SubnetSource = SubnetSourceAllocator
		info.SubnetCIDR = persisted
		if conflistSubnet != "" {
			info.SubnetCIDR = conflistSubnet
		}
	case conflistSubnet != "":
		info.SubnetSource = SubnetSourceConflist
		info.SubnetCIDR = conflistSubnet
	}
	return info, nil
}
