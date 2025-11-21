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

package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// SpaceNetworkConfigPath returns the path to the space's network conflist file.
func SpaceNetworkConfigPath(baseRunPath, realmName, spaceName string) (string, error) {
	if strings.TrimSpace(spaceName) == "" {
		return "", fmt.Errorf("%w: space name is required", errdefs.ErrConfig)
	}
	if strings.TrimSpace(realmName) == "" {
		return "", fmt.Errorf("%w: realm name is required", errdefs.ErrConfig)
	}
	return filepath.Join(RealmMetadataDir(baseRunPath, realmName), spaceName, "network.conflist"), nil
}

// WriteSpaceNetworkConfig writes the network conflist at the provided path.
func WriteSpaceNetworkConfig(confPath, networkName string) error {
	cfg := cni.NewCNINetworkConfig(networkName)
	data, err := cni.BuildDefaultConflist(cfg.Name, cfg.BridgeName, cfg.SubnetCIDR)
	if err != nil {
		return err
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(confPath), 0o750); mkdirErr != nil {
		return mkdirErr
	}
	return os.WriteFile(confPath, data, 0o600)
}
