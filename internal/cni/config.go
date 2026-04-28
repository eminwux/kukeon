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
	"encoding/json"
	"hash/fnv"
)

// maxBridgeNameLen is the Linux IFNAMSIZ-1 limit for network interface names.
const maxBridgeNameLen = 15

// NewCNINetworkConfig builds a NetworkConfig with sensible defaults and the
// package-default subnet (10.88.0.0/16). Prefer NewCNINetworkConfigWithSubnet
// for runtime callers so each space can carry its allocator-assigned chunk;
// this constructor stays for tests and any legacy path that has no allocator
// in scope.
func NewCNINetworkConfig(name string) NetworkConfig {
	return NewCNINetworkConfigWithSubnet(name, defaultSubnetCIDR)
}

// NewCNINetworkConfigWithSubnet builds a NetworkConfig pinned to subnetCIDR.
// Empty subnetCIDR falls back to the package default so callers without an
// allocator handy still produce a valid config.
func NewCNINetworkConfigWithSubnet(name, subnetCIDR string) NetworkConfig {
	if subnetCIDR == "" {
		subnetCIDR = defaultSubnetCIDR
	}
	return NetworkConfig{
		Name:       name,
		BridgeName: SafeBridgeName(name),
		SubnetCIDR: subnetCIDR,
	}
}

// SafeBridgeName derives a Linux-legal bridge device name from a CNI network
// name. The output is always "k-{8-hex FNV-1a hash}" (10 chars, well under
// IFNAMSIZ-1=15) regardless of input length, so the bridge name never depends
// on user-controlled realm/space lengths or charset. Same input → same hash;
// the empty string is preserved so callers can detect "unset".
func SafeBridgeName(name string) string {
	if name == "" {
		return ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return hashBridgeName(h.Sum32())
}

func hashBridgeName(sum uint32) string {
	const (
		hexLen     = 8
		bitsPerHex = 4
		hexMask    = 0xf
		hex        = "0123456789abcdef"
		prefix     = "k-"
	)
	buf := make([]byte, 0, len(prefix)+hexLen)
	buf = append(buf, prefix...)
	for i := hexLen - 1; i >= 0; i-- {
		buf = append(buf, hex[(sum>>(uint(i)*bitsPerHex))&hexMask])
	}
	return string(buf)
}

// BuildDefaultConflist generates a default conflist JSON using provided parameters.
func BuildDefaultConflist(name, bridge, subnet string) ([]byte, error) {
	conf := ConflistModel{
		CNIVersion: defaultCNIVersion,
		Name:       name,
		Plugins: []interface{}{
			BridgePluginModel{
				Type:      "bridge",
				Bridge:    bridge,
				IsGateway: true,
				IPMasq:    true,
				IPAM: BridgeIPAMConfig{
					Type: "host-local",
					Ranges: [][]map[string]string{
						{
							{"subnet": subnet},
						},
					},
					Routes: []RouteModel{
						{Dst: "0.0.0.0/0"},
					},
				},
			},
			LoopbackPluginModel{
				Type: "loopback",
			},
		},
	}
	return json.MarshalIndent(conf, "", "  ")
}
