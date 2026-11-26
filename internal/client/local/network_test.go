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

package local

import (
	"reflect"
	"testing"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const testDefaultConflist = "/opt/kukeon/data/default/web/network.conflist"

func TestSpaceNetworkDetail(t *testing.T) {
	tests := []struct {
		name       string
		spec       v1beta1.SpaceSpec
		info       cni.SpaceNetworkInfo
		wantSubnet string
		wantSource string
		wantPath   string
		wantEgress kukeonv1.NetworkEgress
	}{
		{
			name: "allocated subnet, default conflist, egress not set",
			spec: v1beta1.SpaceSpec{CNIConfigPath: testDefaultConflist},
			info: cni.SpaceNetworkInfo{
				Exists: true, SubnetCIDR: "10.88.1.0/24", SubnetSource: cni.SubnetSourceAllocator,
				ConfigPath: testDefaultConflist, DefaultConfigPath: testDefaultConflist,
			},
			wantSubnet: "10.88.1.0/24",
			wantSource: kukeonv1.NetworkSubnetAllocator,
			wantPath:   kukeonv1.NetworkSourceDefault,
			wantEgress: kukeonv1.NetworkEgress{Default: "allow", DefaultSource: kukeonv1.NetworkSourceDefault},
		},
		{
			name: "legacy conflist subnet, overridden path, egress empty default",
			spec: v1beta1.SpaceSpec{
				CNIConfigPath: "/etc/cni/net.d/custom.conflist",
				Network:       &v1beta1.SpaceNetwork{Egress: &v1beta1.EgressPolicy{}},
			},
			info: cni.SpaceNetworkInfo{
				Exists: true, SubnetCIDR: "10.88.0.0/16", SubnetSource: cni.SubnetSourceConflist,
				ConfigPath: "/etc/cni/net.d/custom.conflist", DefaultConfigPath: testDefaultConflist,
			},
			wantSubnet: "10.88.0.0/16",
			wantSource: kukeonv1.NetworkSubnetConflist,
			wantPath:   kukeonv1.NetworkSourceSpec,
			wantEgress: kukeonv1.NetworkEgress{Default: "allow", DefaultSource: kukeonv1.NetworkSourceDefault},
		},
		{
			name: "unallocated, deny with rules",
			spec: v1beta1.SpaceSpec{Network: &v1beta1.SpaceNetwork{Egress: &v1beta1.EgressPolicy{
				Default: v1beta1.EgressDefaultDeny,
				Allow: []v1beta1.EgressAllowRule{
					{Host: "example.com", Ports: []int{443}},
					{CIDR: "10.0.0.0/8"},
				},
			}}},
			info: cni.SpaceNetworkInfo{
				SubnetSource: cni.SubnetSourceUnallocated,
				ConfigPath:   testDefaultConflist, DefaultConfigPath: testDefaultConflist,
			},
			wantSource: kukeonv1.NetworkSubnetUnallocated,
			wantPath:   kukeonv1.NetworkSourceDefault,
			wantEgress: kukeonv1.NetworkEgress{
				Default: "deny", DefaultSource: kukeonv1.NetworkSourceSpec,
				Allow: []kukeonv1.NetworkEgressRule{
					{Host: "example.com", Ports: []int{443}},
					{CIDR: "10.0.0.0/8"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := spaceNetworkDetail("default-web", tt.spec, tt.info)
			if got.NetworkName != "default-web" {
				t.Errorf("NetworkName = %q", got.NetworkName)
			}
			if got.SubnetCIDR != tt.wantSubnet || got.SubnetSource != tt.wantSource {
				t.Errorf(
					"subnet = %q (%s), want %q (%s)",
					got.SubnetCIDR,
					got.SubnetSource,
					tt.wantSubnet,
					tt.wantSource,
				)
			}
			if got.ConfigPathSource != tt.wantPath {
				t.Errorf("ConfigPathSource = %q, want %q", got.ConfigPathSource, tt.wantPath)
			}
			if got.CNINetworkExists != tt.info.Exists {
				t.Errorf("CNINetworkExists = %v, want %v", got.CNINetworkExists, tt.info.Exists)
			}
			if !reflect.DeepEqual(got.Egress, tt.wantEgress) {
				t.Errorf("Egress = %+v, want %+v", got.Egress, tt.wantEgress)
			}
		})
	}
}

func TestCellNetworkDetail(t *testing.T) {
	bridged := v1beta1.CellDoc{
		Spec: v1beta1.CellSpec{Containers: []v1beta1.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "app"},
		}},
		Status: v1beta1.CellStatus{Network: v1beta1.CellNetworkStatus{BridgeName: "k-0123abcd"}},
	}
	host := v1beta1.CellDoc{Spec: v1beta1.CellSpec{Containers: []v1beta1.ContainerSpec{
		{ID: "kukeond", HostNetwork: true, HostPID: true},
	}}}
	tests := []struct {
		name      string
		cell      v1beta1.CellDoc
		running   bool
		ipv4      string
		wantHost  bool
		wantState string
		wantIPv4  string
		wantRoot  string
	}{
		{"running bridged", bridged, true, "10.88.1.2", false, kukeonv1.NetworkIPv4Assigned, "10.88.1.2", "root"},
		{"running without cached attachment", bridged, true, "", false, kukeonv1.NetworkIPv4NotAssigned, "", "root"},
		{"stopped", bridged, false, "", false, kukeonv1.NetworkIPv4NotRunning, "", "root"},
		{"host network", host, true, "", true, kukeonv1.NetworkIPv4HostNetwork, "", defaultRootContainerID},
		{"host network stopped", host, false, "", true, kukeonv1.NetworkIPv4NotRunning, "", defaultRootContainerID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostNetwork := cellHostNetwork(tt.cell.Spec.Containers)
			if hostNetwork != tt.wantHost {
				t.Fatalf("cellHostNetwork = %v, want %v", hostNetwork, tt.wantHost)
			}
			got := cellNetworkDetail(tt.cell, hostNetwork, tt.running, tt.ipv4)
			if got.IPv4State != tt.wantState || got.IPv4 != tt.wantIPv4 {
				t.Errorf("IPv4 = %q (%s), want %q (%s)", got.IPv4, got.IPv4State, tt.wantIPv4, tt.wantState)
			}
			if got.RootContainer != tt.wantRoot {
				t.Errorf("RootContainer = %q, want %q", got.RootContainer, tt.wantRoot)
			}
			if got.BridgeName != tt.cell.Status.Network.BridgeName {
				t.Errorf("BridgeName = %q", got.BridgeName)
			}
			if len(got.Containers) != len(tt.cell.Spec.Containers) {
				t.Fatalf("Containers = %d entries, want %d", len(got.Containers), len(tt.cell.Spec.Containers))
			}
		})
	}
}

func TestContainerNetworkDetail(t *testing.T) {
	const spacePath = testDefaultConflist
	tests := []struct {
		name       string
		spec       v1beta1.ContainerSpec
		wantPath   string
		wantSource string
	}{
		{"empty path inherits", v1beta1.ContainerSpec{ID: "app"}, spacePath, kukeonv1.NetworkSourceSpace},
		{
			"stamped space path inherits",
			v1beta1.ContainerSpec{ID: "app", CNIConfigPath: spacePath},
			spacePath,
			kukeonv1.NetworkSourceSpace,
		},
		{
			"own path",
			v1beta1.ContainerSpec{ID: "app", CNIConfigPath: "/x.conflist"},
			"/x.conflist",
			kukeonv1.NetworkSourceContainer,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containerNetworkDetail(tt.spec, "root", spacePath)
			if got.CNIConfigPath != tt.wantPath || got.CNIConfigPathSource != tt.wantSource {
				t.Errorf(
					"cniConfigPath = %q (%s), want %q (%s)",
					got.CNIConfigPath,
					got.CNIConfigPathSource,
					tt.wantPath,
					tt.wantSource,
				)
			}
			if got.Root {
				t.Errorf("Root = true for non-root container")
			}
		})
	}
	if got := containerNetworkDetail(v1beta1.ContainerSpec{ID: "root"}, "root", spacePath); !got.Root {
		t.Errorf("Root = false for the root container: %+v", got)
	}
}
