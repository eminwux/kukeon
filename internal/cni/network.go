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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	libcni "github.com/containernetworking/cni/libcni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// LoadNetworkConfigList loads a CNI network config list from the given path.
func (m *Manager) LoadNetworkConfigList(configPath string) error {
	if configPath == "" {
		return errors.New("network config path is required")
	}

	conf, err := libcni.ConfListFromFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to load CNI config list %s: %w", configPath, err)
	}

	m.netConf = conf
	return nil
}

// ExistsNetworkConfig checks if a network config exists and matches the expected network name.
func (m *Manager) ExistsNetworkConfig(networkName, configPath string) (bool, string, error) {
	if configPath == "" {
		configPath = filepath.Join(m.conf.CniConfigDir, networkName+".conflist")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "", errdefs.ErrNetworkNotFound
		}
		return false, "", err
	}

	var raw map[string]interface{}
	if uErr := json.Unmarshal(data, &raw); uErr != nil {
		return false, "", uErr
	}

	if name, ok := raw["name"].(string); !ok || name != networkName {
		return false, "", errdefs.ErrNetworkNotFound
	}

	return true, configPath, nil
}

// CreateNetworkWithConfig creates a CNI network conflist file using the provided NetworkConfig.
func (m *Manager) CreateNetworkWithConfig(cfg NetworkConfig) (string, error) {
	bridge := cfg.BridgeName
	if bridge == "" {
		bridge = defaultBridgeName
	}
	subnet := cfg.SubnetCIDR
	if subnet == "" {
		subnet = defaultSubnetCIDR
	}
	out, err := BuildDefaultConflist(cfg.Name, bridge, subnet)
	if err != nil {
		return "", err
	}
	target := filepath.Join(m.conf.CniConfigDir, cfg.Name+".conflist")
	if writeErr := os.WriteFile(target, out, 0o600); writeErr != nil {
		return "", writeErr
	}
	return target, nil
}

// CreateNetwork is a backward-compatible helper using discrete params.
func (m *Manager) CreateNetwork(networkName, bridgeName, subnetCIDR string) (string, error) {
	return m.CreateNetworkWithConfig(NetworkConfig{
		Name:       networkName,
		BridgeName: bridgeName,
		SubnetCIDR: subnetCIDR,
	})
}

// DeleteNetwork removes a CNI network config file from the filesystem.
func (m *Manager) DeleteNetwork(networkName, configPath string) error {
	if networkName == "" {
		return errors.New("network name is required")
	}

	if configPath == "" {
		configPath = filepath.Join(m.conf.CniConfigDir, networkName+".conflist")
	}

	// Check if file exists
	_, err := os.Stat(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Idempotent: file doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("failed to check network config file: %w", err)
	}

	// Verify the file contains the expected network name
	exists, actualPath, err := m.ExistsNetworkConfig(networkName, configPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return fmt.Errorf("failed to verify network config: %w", err)
	}
	if !exists {
		// Network config doesn't match or doesn't exist
		return nil
	}

	// Delete the config file
	if err = os.Remove(actualPath); err != nil {
		return fmt.Errorf("failed to delete network config file %s: %w", actualPath, err)
	}

	return nil
}
