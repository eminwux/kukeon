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
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cni "github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

func TestManager_LoadNetworkConfigList(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string) string // Returns config path
		wantErr   bool
		checkConf func(t *testing.T, mgr *cni.Manager)
	}{
		{
			name: "error with empty config path",
			setup: func(_ *testing.T, _ string) string {
				return ""
			},
			wantErr: true,
		},
		{
			name: "error with non-existent file",
			setup: func(_ *testing.T, dir string) string {
				return filepath.Join(dir, "nonexistent.conflist")
			},
			wantErr: true,
		},
		{
			name: "error with invalid JSON",
			setup: func(t *testing.T, dir string) string {
				configPath := filepath.Join(dir, "invalid.conflist")
				err := os.WriteFile(configPath, []byte("invalid json"), 0o600)
				if err != nil {
					t.Fatalf("failed to write invalid JSON file: %v", err)
				}
				return configPath
			},
			wantErr: true,
		},
		{
			name: "success with valid conflist file",
			setup: func(t *testing.T, dir string) string {
				configPath := filepath.Join(dir, "test.conflist")
				conflist := `{
  "cniVersion": "0.4.0",
  "name": "test-network",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "cni0",
      "isGateway": true,
      "ipMasq": true,
      "ipam": {
        "type": "host-local",
        "ranges": [
          [
            {
              "subnet": "10.88.0.0/16"
            }
          ]
        ],
        "routes": [
          {
            "dst": "0.0.0.0/0"
          }
        ]
      }
    },
    {
      "type": "loopback"
    }
  ]
}`
				err := os.WriteFile(configPath, []byte(conflist), 0o600)
				if err != nil {
					t.Fatalf("failed to write conflist file: %v", err)
				}
				return configPath
			},
			wantErr: false,
			checkConf: func(t *testing.T, mgr *cni.Manager) {
				// Verify config was loaded by trying to use it
				// We can't directly access netConf, but we can verify it's loaded
				// by checking that AddContainerToNetwork doesn't return ErrNetworkConfigNotLoaded
				err := mgr.AddContainerToNetwork(context.Background(), "test-container", "/proc/123/ns/net")
				if err != nil && !errors.Is(err, errdefs.ErrNetworkConfigNotLoaded) {
					// Config is loaded (error is from libcni, not from missing config)
					return
				}
				if errors.Is(err, errdefs.ErrNetworkConfigNotLoaded) {
					t.Error("LoadNetworkConfigList() did not load config")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := tt.setup(t, dir)

			mgr, err := cni.NewManager("", dir, "")
			if err != nil {
				t.Fatalf("failed to create manager: %v", err)
			}

			err = mgr.LoadNetworkConfigList(configPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("LoadNetworkConfigList() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("LoadNetworkConfigList() error = %v, want nil", err)
			}

			if tt.checkConf != nil {
				tt.checkConf(t, mgr)
			}
		})
	}
}

func TestManager_ExistsNetworkConfig(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, dir string) // Sets up test files
		networkName string
		configPath  string
		wantExists  bool
		wantPath    string
		wantErr     error
	}{
		{
			name: "network not found (file doesn't exist)",
			setup: func(_ *testing.T, _ string) {
				// No files created
			},
			networkName: "test-network",
			configPath:  "",
			wantExists:  false,
			wantErr:     errdefs.ErrNetworkNotFound,
		},
		{
			name: "network not found (name mismatch)",
			setup: func(t *testing.T, dir string) {
				configPath := filepath.Join(dir, "other-network.conflist")
				conflist := `{
  "cniVersion": "0.4.0",
  "name": "other-network",
  "plugins": []
}`
				err := os.WriteFile(configPath, []byte(conflist), 0o600)
				if err != nil {
					t.Fatalf("failed to write conflist file: %v", err)
				}
			},
			networkName: "test-network",
			configPath:  "",
			wantExists:  false,
			wantErr:     errdefs.ErrNetworkNotFound,
		},
		{
			name: "network not found (invalid JSON)",
			setup: func(t *testing.T, dir string) {
				configPath := filepath.Join(dir, "test-network.conflist")
				err := os.WriteFile(configPath, []byte("invalid json"), 0o600)
				if err != nil {
					t.Fatalf("failed to write invalid JSON file: %v", err)
				}
			},
			networkName: "test-network",
			configPath:  "",
			wantExists:  false,
			wantErr:     nil, // JSON unmarshal error, not ErrNetworkNotFound
		},
		{
			name: "success with matching network",
			setup: func(t *testing.T, dir string) {
				configPath := filepath.Join(dir, "test-network.conflist")
				conflist := `{
  "cniVersion": "0.4.0",
  "name": "test-network",
  "plugins": []
}`
				err := os.WriteFile(configPath, []byte(conflist), 0o600)
				if err != nil {
					t.Fatalf("failed to write conflist file: %v", err)
				}
			},
			networkName: "test-network",
			configPath:  "",
			wantExists:  true,
			wantPath:    "", // Will be auto-constructed
			wantErr:     nil,
		},
		{
			name: "success with explicit config path",
			setup: func(t *testing.T, dir string) {
				configPath := filepath.Join(dir, "custom.conflist")
				conflist := `{
  "cniVersion": "0.4.0",
  "name": "test-network",
  "plugins": []
}`
				err := os.WriteFile(configPath, []byte(conflist), 0o600)
				if err != nil {
					t.Fatalf("failed to write conflist file: %v", err)
				}
			},
			networkName: "test-network",
			configPath:  "", // Will be set to dir/custom.conflist in test
			wantExists:  true,
			wantErr:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}

			mgr, err := cni.NewManager("", dir, "")
			if err != nil {
				t.Fatalf("failed to create manager: %v", err)
			}

			// Update configPath if it's empty to use the test directory
			configPath := tt.configPath
			if configPath == "" {
				if tt.name == "success with explicit config path" {
					configPath = filepath.Join(dir, "custom.conflist")
				} else if tt.networkName != "" {
					configPath = filepath.Join(dir, tt.networkName+".conflist")
				}
			}

			exists, path, err := mgr.ExistsNetworkConfig(tt.networkName, configPath)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("ExistsNetworkConfig() error = nil, want %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("ExistsNetworkConfig() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil && tt.wantErr == nil {
				// Allow JSON unmarshal errors in invalid JSON test case
				if tt.name != "network not found (invalid JSON)" {
					t.Fatalf("ExistsNetworkConfig() error = %v, want nil", err)
				}
				return
			}

			if exists != tt.wantExists {
				t.Errorf("ExistsNetworkConfig() exists = %v, want %v", exists, tt.wantExists)
			}

			if tt.wantExists && path == "" {
				t.Error("ExistsNetworkConfig() path = empty, want non-empty")
			}
		})
	}
}

func TestManager_CreateNetworkWithConfig(t *testing.T) {
	tests := []struct {
		name       string
		cfg        cni.NetworkConfig
		wantBridge string
		wantSubnet string
		validate   func(t *testing.T, path string, data []byte)
	}{
		{
			name: "success with all fields provided",
			cfg: cni.NetworkConfig{
				Name:       "test-network",
				BridgeName: "test-bridge",
				SubnetCIDR: "10.1.0.0/16",
			},
			wantBridge: "test-bridge",
			wantSubnet: "10.1.0.0/16",
			validate: func(t *testing.T, path string, data []byte) {
				// Verify file exists
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("config file does not exist: %v", err)
				}

				// Verify file permissions (0o600)
				info, err := os.Stat(path)
				if err != nil {
					t.Fatalf("failed to stat file: %v", err)
				}
				perm := info.Mode().Perm()
				if perm != 0o600 {
					t.Errorf("file permissions = %o, want 0o600", perm)
				}

				// Verify JSON structure
				var conf cni.ConflistModel
				if err = json.Unmarshal(data, &conf); err != nil {
					t.Fatalf("failed to unmarshal JSON: %v", err)
				}

				if conf.Name != "test-network" {
					t.Errorf("config name = %q, want %q", conf.Name, "test-network")
				}

				if len(conf.Plugins) < 1 {
					t.Fatal("config plugins is empty")
				}

				// Verify bridge plugin
				bridgePlugin, ok := conf.Plugins[0].(map[string]interface{})
				if !ok {
					t.Fatal("first plugin is not a map")
				}

				if bridgePlugin["bridge"] != "test-bridge" {
					t.Errorf("bridge = %v, want %q", bridgePlugin["bridge"], "test-bridge")
				}
			},
		},
		{
			name: "success with empty BridgeName (uses default)",
			cfg: cni.NetworkConfig{
				Name:       "test-network",
				BridgeName: "",
				SubnetCIDR: "10.1.0.0/16",
			},
			wantBridge: "cni0",
			wantSubnet: "10.1.0.0/16",
			validate: func(t *testing.T, _ string, data []byte) {
				var conf cni.ConflistModel
				if err := json.Unmarshal(data, &conf); err != nil {
					t.Fatalf("failed to unmarshal JSON: %v", err)
				}

				bridgePlugin, ok := conf.Plugins[0].(map[string]interface{})
				if !ok {
					t.Fatal("first plugin is not a map")
				}

				if bridgePlugin["bridge"] != "cni0" {
					t.Errorf("bridge = %v, want %q", bridgePlugin["bridge"], "cni0")
				}
			},
		},
		{
			name: "success with empty SubnetCIDR (uses default)",
			cfg: cni.NetworkConfig{
				Name:       "test-network",
				BridgeName: "test-bridge",
				SubnetCIDR: "",
			},
			wantBridge: "test-bridge",
			wantSubnet: "10.88.0.0/16",
			validate: func(t *testing.T, _ string, data []byte) {
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

				if subnetMap["subnet"] != "10.88.0.0/16" {
					t.Errorf("subnet = %v, want %q", subnetMap["subnet"], "10.88.0.0/16")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mgr, err := cni.NewManager("", dir, "")
			if err != nil {
				t.Fatalf("failed to create manager: %v", err)
			}

			path, err := mgr.CreateNetworkWithConfig(tt.cfg)
			if err != nil {
				t.Fatalf("CreateNetworkWithConfig() error = %v, want nil", err)
			}

			if path == "" {
				t.Fatal("CreateNetworkWithConfig() path = empty, want non-empty")
			}

			// Read file content
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read config file: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, path, data)
			}
		})
	}
}

func TestManager_CreateNetwork(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		bridgeName  string
		subnetCIDR  string
		wantBridge  string
		wantSubnet  string
	}{
		{
			name:        "success with all parameters",
			networkName: "test-network",
			bridgeName:  "test-bridge",
			subnetCIDR:  "10.1.0.0/16",
			wantBridge:  "test-bridge",
			wantSubnet:  "10.1.0.0/16",
		},
		{
			name:        "success with empty bridgeName (uses default)",
			networkName: "test-network",
			bridgeName:  "",
			subnetCIDR:  "10.1.0.0/16",
			wantBridge:  "cni0",
			wantSubnet:  "10.1.0.0/16",
		},
		{
			name:        "success with empty subnetCIDR (uses default)",
			networkName: "test-network",
			bridgeName:  "test-bridge",
			subnetCIDR:  "",
			wantBridge:  "test-bridge",
			wantSubnet:  "10.88.0.0/16",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mgr, err := cni.NewManager("", dir, "")
			if err != nil {
				t.Fatalf("failed to create manager: %v", err)
			}

			path, err := mgr.CreateNetwork(tt.networkName, tt.bridgeName, tt.subnetCIDR)
			if err != nil {
				t.Fatalf("CreateNetwork() error = %v, want nil", err)
			}

			if path == "" {
				t.Fatal("CreateNetwork() path = empty, want non-empty")
			}

			// Verify it calls CreateNetworkWithConfig correctly by reading the file
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read config file: %v", err)
			}

			var conf cni.ConflistModel
			if err = json.Unmarshal(data, &conf); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}

			if conf.Name != tt.networkName {
				t.Errorf("config name = %q, want %q", conf.Name, tt.networkName)
			}

			bridgePlugin, ok := conf.Plugins[0].(map[string]interface{})
			if !ok {
				t.Fatal("first plugin is not a map")
			}

			if bridgePlugin["bridge"] != tt.wantBridge {
				t.Errorf("bridge = %v, want %q", bridgePlugin["bridge"], tt.wantBridge)
			}
		})
	}
}

func TestManager_DeleteNetwork(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, dir string)
		networkName string
		configPath  string
		wantErr     bool
		verify      func(t *testing.T, dir string, networkName string)
	}{
		{
			name:        "error with empty network name",
			setup:       func(_ *testing.T, _ string) {},
			networkName: "",
			configPath:  "",
			wantErr:     true,
		},
		{
			name: "success when file doesn't exist (idempotent)",
			setup: func(_ *testing.T, _ string) {
				// No files created
			},
			networkName: "test-network",
			configPath:  "",
			wantErr:     false,
			verify: func(t *testing.T, dir string, networkName string) {
				// File should not exist
				path := filepath.Join(dir, networkName+".conflist")
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("file should not exist: %v", err)
				}
			},
		},
		{
			name: "success when network name doesn't match (idempotent)",
			setup: func(t *testing.T, dir string) {
				configPath := filepath.Join(dir, "other-network.conflist")
				conflist := `{
  "cniVersion": "0.4.0",
  "name": "other-network",
  "plugins": []
}`
				err := os.WriteFile(configPath, []byte(conflist), 0o600)
				if err != nil {
					t.Fatalf("failed to write conflist file: %v", err)
				}
			},
			networkName: "test-network",
			configPath:  "",
			wantErr:     false,
			verify: func(t *testing.T, dir string, _ string) {
				// Other file should still exist
				otherPath := filepath.Join(dir, "other-network.conflist")
				if _, err := os.Stat(otherPath); err != nil {
					t.Errorf("other file should still exist: %v", err)
				}
			},
		},
		{
			name: "success deleting existing network",
			setup: func(t *testing.T, dir string) {
				configPath := filepath.Join(dir, "test-network.conflist")
				conflist := `{
  "cniVersion": "0.4.0",
  "name": "test-network",
  "plugins": []
}`
				err := os.WriteFile(configPath, []byte(conflist), 0o600)
				if err != nil {
					t.Fatalf("failed to write conflist file: %v", err)
				}
			},
			networkName: "test-network",
			configPath:  "",
			wantErr:     false,
			verify: func(t *testing.T, dir string, networkName string) {
				// File should be deleted
				path := filepath.Join(dir, networkName+".conflist")
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("file should be deleted: %v", err)
				}
			},
		},
		{
			name: "success with explicit config path",
			setup: func(t *testing.T, dir string) {
				configPath := filepath.Join(dir, "custom.conflist")
				conflist := `{
  "cniVersion": "0.4.0",
  "name": "test-network",
  "plugins": []
}`
				err := os.WriteFile(configPath, []byte(conflist), 0o600)
				if err != nil {
					t.Fatalf("failed to write conflist file: %v", err)
				}
			},
			networkName: "test-network",
			configPath:  "", // Will be set in test to use dir/custom.conflist
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}

			mgr, err := cni.NewManager("", dir, "")
			if err != nil {
				t.Fatalf("failed to create manager: %v", err)
			}

			// Update configPath if it's empty to use the test directory
			configPath := tt.configPath
			if configPath == "" {
				if tt.name == "success with explicit config path" {
					configPath = filepath.Join(dir, "custom.conflist")
				} else if tt.networkName != "" {
					configPath = filepath.Join(dir, tt.networkName+".conflist")
				}
			}

			err = mgr.DeleteNetwork(tt.networkName, configPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("DeleteNetwork() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("DeleteNetwork() error = %v, want nil", err)
			}

			if tt.verify != nil {
				tt.verify(t, dir, tt.networkName)
			}
		})
	}
}
