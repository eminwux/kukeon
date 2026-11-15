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
	"os"
	"path/filepath"
	"testing"

	cni "github.com/eminwux/kukeon/internal/cni"
)

func writeSpaceConflist(t *testing.T, dir, name, subnet string) string {
	t.Helper()
	data, err := cni.BuildDefaultConflist(name, cni.SafeBridgeName(name), subnet)
	if err != nil {
		t.Fatalf("BuildDefaultConflist: %v", err)
	}
	path := filepath.Join(dir, "network.conflist")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write conflist: %v", err)
	}
	return path
}

func TestManager_InspectSpaceNetwork(t *testing.T) {
	const realm, space, network = "default", "web", "default-web"
	tests := []struct {
		name       string
		allocate   bool
		subnet     string // conflist subnet; "" means no conflist
		wantExists bool
		wantSubnet string
		wantSource string
		wantBridge bool
	}{
		{
			name:       "allocated subnet",
			allocate:   true,
			subnet:     "10.88.0.0/24",
			wantExists: true,
			wantSubnet: "10.88.0.0/24",
			wantSource: cni.SubnetSourceAllocator,
			wantBridge: true,
		},
		{
			name:       "legacy conflist without allocator state",
			subnet:     "10.88.0.0/16",
			wantExists: true,
			wantSubnet: "10.88.0.0/16",
			wantSource: cni.SubnetSourceConflist,
			wantBridge: true,
		},
		{
			name:       "unallocated",
			wantSource: cni.SubnetSourceUnallocated,
		},
		{
			name:       "allocated before the conflist is written",
			allocate:   true,
			wantSubnet: "10.88.0.0/24",
			wantSource: cni.SubnetSourceAllocator,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			alloc, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
			if err != nil {
				t.Fatalf("NewSubnetAllocator: %v", err)
			}
			if tt.allocate {
				if _, err = alloc.Allocate(realm, space); err != nil {
					t.Fatalf("Allocate: %v", err)
				}
			}
			confDir := t.TempDir()
			confPath := filepath.Join(confDir, "network.conflist")
			if tt.subnet != "" {
				confPath = writeSpaceConflist(t, confDir, network, tt.subnet)
			}
			mgr := setupTestManager(t, confDir)
			info, err := mgr.InspectSpaceNetwork(alloc, realm, space, network, confPath)
			if err != nil {
				t.Fatalf("InspectSpaceNetwork: %v", err)
			}
			if info.Exists != tt.wantExists {
				t.Errorf("Exists = %v, want %v", info.Exists, tt.wantExists)
			}
			if info.SubnetCIDR != tt.wantSubnet {
				t.Errorf("SubnetCIDR = %q, want %q", info.SubnetCIDR, tt.wantSubnet)
			}
			if info.SubnetSource != tt.wantSource {
				t.Errorf("SubnetSource = %q, want %q", info.SubnetSource, tt.wantSource)
			}
			wantBridge := ""
			if tt.wantBridge {
				wantBridge = cni.SafeBridgeName(network)
			}
			if info.BridgeName != wantBridge {
				t.Errorf("BridgeName = %q, want %q", info.BridgeName, wantBridge)
			}
		})
	}
}
