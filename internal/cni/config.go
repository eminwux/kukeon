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

import "encoding/json"

// NewCNINetworkConfig builds a NetworkConfig with sensible defaults.
func NewCNINetworkConfig(name string) NetworkConfig {
	return NetworkConfig{
		Name:       name,
		BridgeName: name,
		SubnetCIDR: defaultSubnetCIDR,
	}
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
