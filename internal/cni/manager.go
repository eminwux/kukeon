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

// NewManager creates a new CNI manager with the provided directories.
// cniBinDir: where plugins live, e.g. /opt/cni/bin
// cniConfigDir: where network configs live, e.g. /opt/cni/net.d
// cniCacheDir: where CNI stores cache, e.g. /opt/cni/cache.
func NewManager(cniBinDir, cniConfigDir, cniCacheDir string) (*Manager, error) {
	// Apply defaults BEFORE creating the CNI config to ensure non-empty paths
	if cniConfigDir == "" {
		cniConfigDir = defaultCniConfDir
	}

	if cniBinDir == "" {
		cniBinDir = defaultCniBinDir
	}

	if cniCacheDir == "" {
		cniCacheDir = defaultCniCacheDir
	}

	cniConf := libcni.NewCNIConfigWithCacheDir(
		[]string{cniBinDir},
		cniCacheDir,
		nil,
	)

	var netConf *libcni.NetworkConfigList

	return &Manager{
		cniConf: cniConf,
		netConf: netConf,
		conf: Conf{
			CniConfigDir: cniConfigDir,
			CniBinDir:    cniBinDir,
			CniCacheDir:  cniCacheDir,
		},
	}, nil
}
