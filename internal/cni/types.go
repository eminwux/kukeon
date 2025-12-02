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
	libcni "github.com/containernetworking/cni/libcni"
)

// Manager manages CNI network operations.
type Manager struct {
	cniConf libcni.CNI
	netConf *libcni.NetworkConfigList
	conf    Conf
}

// Conf holds CNI configuration paths.
type Conf struct {
	CniConfigDir string
	CniBinDir    string
	CniCacheDir  string
}

// NetworkConfig represents the desired parameters for creating a CNI network.
type NetworkConfig struct {
	// Name is the CNI network name and the filename (without extension).
	Name string
	// BridgeName is the bridge device name. Defaults to "cni0" when empty.
	BridgeName string
	// SubnetCIDR is the IPAM subnet CIDR. Defaults to "10.88.0.0/16" when empty.
	SubnetCIDR string
}

// BootstrapReport captures CNI environment checks and actions.
type BootstrapReport struct {
	CniConfigDir        string
	CniCacheDir         string
	CniBinDir           string
	ConfigDirExistsPre  bool
	CacheDirExistsPre   bool
	BinDirExistsPre     bool
	ConfigDirCreated    bool
	CacheDirCreated     bool
	BinDirCreated       bool
	ConfigDirExistsPost bool
	CacheDirExistsPost  bool
	BinDirExistsPost    bool
}

// ConflistModel represents the structure of a CNI conflist file.
type ConflistModel struct {
	CNIVersion string        `json:"cniVersion"`
	Name       string        `json:"name"`
	Plugins    []interface{} `json:"plugins"`
}

// BridgePluginModel represents the bridge plugin configuration in a conflist.
type BridgePluginModel struct {
	Type      string           `json:"type"`
	Bridge    string           `json:"bridge"`
	IsGateway bool             `json:"isGateway"`
	IPMasq    bool             `json:"ipMasq"`
	IPAM      BridgeIPAMConfig `json:"ipam"`
}

// BridgeIPAMConfig represents the IPAM configuration for the bridge plugin.
type BridgeIPAMConfig struct {
	Type   string                `json:"type"`
	Ranges [][]map[string]string `json:"ranges"`
	Routes []RouteModel          `json:"routes"`
}

// RouteModel represents a route in the IPAM configuration.
type RouteModel struct {
	Dst string `json:"dst"`
}

// LoopbackPluginModel represents the loopback plugin configuration in a conflist.
type LoopbackPluginModel struct {
	Type string `json:"type"`
}

const (
	defaultCniConfDir  = "/opt/cni/net.d"
	defaultCniBinDir   = "/opt/cni/bin"
	defaultCniCacheDir = "/opt/cni/cache"
	defaultCNIVersion  = "0.4.0"
	defaultBridgeName  = "cni0"
	defaultSubnetCIDR  = "10.88.0.0/16"
)

// CNINetworksDir is the standard directory where CNI stores network state and IPAM allocations.
// This is the default location used by CNI plugins for host-local IPAM.
const CNINetworksDir = "/var/lib/cni/networks"
