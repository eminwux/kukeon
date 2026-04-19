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
	"strings"
)

// maxBridgeNameLen is the Linux IFNAMSIZ-1 limit for network interface names.
const maxBridgeNameLen = 15

// NewCNINetworkConfig builds a NetworkConfig with sensible defaults.
func NewCNINetworkConfig(name string) NetworkConfig {
	return NetworkConfig{
		Name:       name,
		BridgeName: SafeBridgeName(name),
		SubnetCIDR: defaultSubnetCIDR,
	}
}

// SafeBridgeName derives a Linux-legal bridge device name from a CNI network
// name. Names within the IFNAMSIZ limit pass through unchanged; longer ones
// are deterministically truncated to a 6-char prefix plus an 8-hex-char FNV-1a
// suffix (total 15 chars) to preserve readability while staying unique.
// The empty string is preserved so callers can detect "unset".
func SafeBridgeName(name string) string {
	if len(name) <= maxBridgeNameLen {
		return name
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	sum := h.Sum32()
	prefix := strings.ReplaceAll(name[:6], "/", "-")
	return prefixHashBridgeName(prefix, sum)
}

func prefixHashBridgeName(prefix string, sum uint32) string {
	const (
		hexLen     = 8
		bitsPerHex = 4
		hexMask    = 0xf
		hex        = "0123456789abcdef"
	)
	buf := make([]byte, 0, len(prefix)+1+hexLen)
	buf = append(buf, prefix...)
	buf = append(buf, '-')
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
