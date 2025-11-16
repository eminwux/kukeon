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
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	libcni "github.com/containernetworking/cni/libcni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

const (
	defaultCniConfFile = "/etc/cni/net.d/10-kykeon-main-default.conflist"
	defaultCniConfDir  = "/etc/cni/net.d"
	defaultCniBinDir   = "/opt/cni/bin"
	defaultCniCacheDir = "/var/lib/kukeon/cni-cache"
)

type Manager struct {
	cniConf libcni.CNI
	netConf *libcni.NetworkConfigList
	conf    Conf
}

type Conf struct {
	CniConfigFile string
	CniConfigDir  string
	CniBinDir     string
	CniCacheDir   string
}

func NewManager(cniBinDir, cniConfFile, cniConfigDir, cniCacheDir string) (*Manager, error) {
	// cniBinDir: where plugins live, e.g. /opt/cni/bin
	// cacheDir: something like /var/lib/kukeon/cni-cache

	cniConf := libcni.NewCNIConfigWithCacheDir(
		[]string{cniBinDir},
		cniCacheDir,
		nil,
	)

	if cniConfFile == "" {
		cniConfFile = defaultCniConfFile
	}

	if cniConfigDir == "" {
		cniConfigDir = defaultCniConfDir
	}

	if cniBinDir == "" {
		cniBinDir = defaultCniBinDir
	}

	if cniCacheDir == "" {
		cniCacheDir = defaultCniCacheDir
	}

	netConf, err := libcni.ConfListFromFile(cniConfFile)
	if err != nil {
		return nil, err
	}

	return &Manager{
		cniConf: cniConf,
		netConf: netConf,
		conf: Conf{
			CniConfigFile: cniConfFile,
			CniConfigDir:  cniConfigDir,
			CniBinDir:     cniBinDir,
			CniCacheDir:   cniCacheDir,
		},
	}, nil
}

func (m *Manager) AddContainerToNetwork(ctx context.Context, containerID, netnsPath string) error {
	rt := &libcni.RuntimeConf{
		ContainerID: containerID,
		NetNS:       netnsPath, // e.g. /proc/<pid>/ns/net
		IfName:      "eth0",
	}

	_, err := m.cniConf.AddNetworkList(ctx, m.netConf, rt)
	return err
}

func (m *Manager) DelContainerFromNetwork(ctx context.Context, containerID, netnsPath string) error {
	rt := &libcni.RuntimeConf{
		ContainerID: containerID,
		NetNS:       netnsPath,
		IfName:      "eth0",
	}
	return m.cniConf.DelNetworkList(ctx, m.netConf, rt)
}

func (m *Manager) NetworkExists(networkName string) (bool, string, error) {
	var foundFile string

	err := filepath.WalkDir(m.conf.CniConfigDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		// Only parse files ending with .conflist or .conf
		if filepath.Ext(path) != ".conf" && filepath.Ext(path) != ".conflist" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var raw map[string]interface{}
		if err = json.Unmarshal(data, &raw); err != nil {
			return err
		}

		if name, ok := raw["name"].(string); ok {
			if name == networkName {
				foundFile = path
				return filepath.SkipDir // stop scanning early
			}
		}

		return nil
	})
	if err != nil {
		return false, "", err
	}
	if foundFile == "" {
		return false, "", errdefs.ErrNetworkNotFound
	}

	return true, foundFile, nil
}
