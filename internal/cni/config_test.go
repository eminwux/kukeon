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

package cni_test

import (
	"encoding/json"
	"testing"

	cni "github.com/eminwux/kukeon/internal/cni"
)

func TestNewCNINetworkConfig(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		wantBridge  string
		wantSubnet  string
	}{
		{
			name:        "success with name provided",
			networkName: "test-network",
			wantBridge:  "test-network",
			wantSubnet:  "10.88.0.0/16",
		},
		{
			name:        "success with empty name",
			networkName: "",
			wantBridge:  "",
			wantSubnet:  "10.88.0.0/16",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := cni.NewCNINetworkConfig(tt.networkName)

			if cfg.Name != tt.networkName {
				t.Errorf("NewCNINetworkConfig() name = %q, want %q", cfg.Name, tt.networkName)
			}

			if cfg.BridgeName != tt.wantBridge {
				t.Errorf("NewCNINetworkConfig() bridgeName = %q, want %q", cfg.BridgeName, tt.wantBridge)
			}

			if cfg.SubnetCIDR != tt.wantSubnet {
				t.Errorf("NewCNINetworkConfig() subnetCIDR = %q, want %q", cfg.SubnetCIDR, tt.wantSubnet)
			}
		})
	}
}

func TestBuildDefaultConflist(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		bridge      string
		subnet      string
		validate    func(t *testing.T, data []byte)
	}{
		{
			name:        "success with all parameters",
			networkName: "test-network",
			bridge:      "test-bridge",
			subnet:      "10.1.0.0/16",
			validate: func(t *testing.T, data []byte) {
				var conf cni.ConflistModel
				if err := json.Unmarshal(data, &conf); err != nil {
					t.Fatalf("failed to unmarshal JSON: %v", err)
				}

				// Verify CNI version
				if conf.CNIVersion != "0.4.0" {
					t.Errorf("CNIVersion = %q, want %q", conf.CNIVersion, "0.4.0")
				}

				// Verify name
				if conf.Name != "test-network" {
					t.Errorf("name = %q, want %q", conf.Name, "test-network")
				}

				// Verify plugins array
				if len(conf.Plugins) < 2 {
					t.Fatalf("plugins length = %d, want at least 2", len(conf.Plugins))
				}

				// Verify bridge plugin
				bridgePlugin, ok := conf.Plugins[0].(map[string]interface{})
				if !ok {
					t.Fatal("first plugin is not a map")
				}

				if bridgePlugin["type"] != "bridge" {
					t.Errorf("bridge plugin type = %v, want %q", bridgePlugin["type"], "bridge")
				}

				if bridgePlugin["bridge"] != "test-bridge" {
					t.Errorf("bridge = %v, want %q", bridgePlugin["bridge"], "test-bridge")
				}

				if bridgePlugin["isGateway"] != true {
					t.Errorf("isGateway = %v, want true", bridgePlugin["isGateway"])
				}

				if bridgePlugin["ipMasq"] != true {
					t.Errorf("ipMasq = %v, want true", bridgePlugin["ipMasq"])
				}

				// Verify IPAM configuration
				ipam, ok := bridgePlugin["ipam"].(map[string]interface{})
				if !ok {
					t.Fatal("ipam is not a map")
				}

				if ipam["type"] != "host-local" {
					t.Errorf("ipam type = %v, want %q", ipam["type"], "host-local")
				}

				ranges, ok := ipam["ranges"].([]interface{})
				if !ok || len(ranges) == 0 {
					t.Fatal("ranges is empty or not an array")
				}

				range0, ok := ranges[0].([]interface{})
				if !ok || len(range0) == 0 {
					t.Fatal("first range is empty or not an array")
				}

				subnetMap, ok := range0[0].(map[string]interface{})
				if !ok {
					t.Fatal("subnet is not a map")
				}

				if subnetMap["subnet"] != "10.1.0.0/16" {
					t.Errorf("subnet = %v, want %q", subnetMap["subnet"], "10.1.0.0/16")
				}

				// Verify routes
				routes, ok := ipam["routes"].([]interface{})
				if !ok || len(routes) == 0 {
					t.Fatal("routes is empty or not an array")
				}

				route0, ok := routes[0].(map[string]interface{})
				if !ok {
					t.Fatal("first route is not a map")
				}

				if route0["dst"] != "0.0.0.0/0" {
					t.Errorf("route dst = %v, want %q", route0["dst"], "0.0.0.0/0")
				}

				// Verify loopback plugin
				loopbackPlugin, ok := conf.Plugins[1].(map[string]interface{})
				if !ok {
					t.Fatal("second plugin is not a map")
				}

				if loopbackPlugin["type"] != "loopback" {
					t.Errorf("loopback plugin type = %v, want %q", loopbackPlugin["type"], "loopback")
				}

				// Verify JSON is properly formatted (indented)
				// Re-marshal to check indentation
				indented, err := json.MarshalIndent(conf, "", "  ")
				if err != nil {
					t.Fatalf("failed to marshal: %v", err)
				}

				// Compare lengths - indented should be longer
				if len(data) < len(indented)/2 {
					t.Error("JSON appears to not be indented")
				}
			},
		},
		{
			name:        "success with empty bridge name",
			networkName: "test-network",
			bridge:      "",
			subnet:      "10.1.0.0/16",
			validate: func(t *testing.T, data []byte) {
				var conf cni.ConflistModel
				if err := json.Unmarshal(data, &conf); err != nil {
					t.Fatalf("failed to unmarshal JSON: %v", err)
				}

				bridgePlugin, ok := conf.Plugins[0].(map[string]interface{})
				if !ok {
					t.Fatal("first plugin is not a map")
				}

				if bridgePlugin["bridge"] != "" {
					t.Errorf("bridge = %v, want empty string", bridgePlugin["bridge"])
				}
			},
		},
		{
			name:        "success with empty subnet",
			networkName: "test-network",
			bridge:      "test-bridge",
			subnet:      "",
			validate: func(t *testing.T, data []byte) {
				var conf cni.ConflistModel
				if err := json.Unmarshal(data, &conf); err != nil {
					t.Fatalf("failed to unmarshal JSON: %v", err)
				}

				bridgePlugin, ok := conf.Plugins[0].(map[string]interface{})
				if !ok {
					t.Fatal("first plugin is not a map")
				}

				ipam, ok := bridgePlugin["ipam"].(map[string]interface{})
				if !ok {
					t.Fatal("ipam is not a map")
				}

				ranges, ok := ipam["ranges"].([]interface{})
				if !ok || len(ranges) == 0 {
					t.Fatal("ranges is empty or not an array")
				}

				range0, ok := ranges[0].([]interface{})
				if !ok || len(range0) == 0 {
					t.Fatal("first range is empty or not an array")
				}

				subnetMap, ok := range0[0].(map[string]interface{})
				if !ok {
					t.Fatal("subnet is not a map")
				}

				if subnetMap["subnet"] != "" {
					t.Errorf("subnet = %v, want empty string", subnetMap["subnet"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := cni.BuildDefaultConflist(tt.networkName, tt.bridge, tt.subnet)
			if err != nil {
				t.Fatalf("BuildDefaultConflist() error = %v, want nil", err)
			}

			if len(data) == 0 {
				t.Fatal("BuildDefaultConflist() returned empty data")
			}

			if tt.validate != nil {
				tt.validate(t, data)
			}
		})
	}
}
