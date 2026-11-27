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

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/clientconfig"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/configrename"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/serverconfig"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// candidate is one space subnet range a kukeon process started by dev-init
// can put on a bridge, with where it comes from.
type candidate struct {
	Subnet netip.Prefix
	Source string
}

// ownSpace is a space this instance already provisioned: its bridge carries
// the space subnet, and a rerun reuses it.
type ownSpace struct {
	Realm, Space string
	Bridge       string
	Subnet       netip.Prefix
}

// resolution is the configuration resolved the way kuke init, the
// containerized kukeond and --no-daemon client paths resolve it.
type resolution struct {
	ServerConfig string
	RunPath      string
	Candidates   []candidate
	Spaces       []ownSpace
}

// resolveConfig mirrors cmd/kuke/shared.LoadServerConfigurationFromFlag and
// the client loader: an explicit path beats its env var, which beats the
// default file; the run path is flag > env > server runPath > client runPath >
// default; KUKEON_SPACE_SUBNET_CIDR beats the file's spaceSubnetCIDR, which
// beats the compiled-in default.
//
// Every value one of the processes can end up with is a candidate, because
// they do not all see the same inputs: `kuke init` reads the env var
// in-process, while the kukeond it starts reads only the configuration file.
// Persisted spaces keep the subnet they were given, whatever the
// configuration says now, so each is a candidate too.
func resolveConfig(lookupEnv func(string) (string, bool), serverPath, runPath string) (resolution, error) {
	env := func(key string) string {
		v, _ := lookupEnv(key)
		return strings.TrimSpace(v)
	}
	var res resolution
	if err := configrename.CheckEnv(lookupEnv); err != nil {
		return res, err
	}
	res.ServerConfig = serverPath
	if res.ServerConfig == "" {
		res.ServerConfig = env(config.KUKEOND_CONFIGURATION.Key)
	}
	if res.ServerConfig == "" {
		res.ServerConfig = config.DefaultServerConfigurationFile()
	}
	server, err := serverconfig.Load(res.ServerConfig)
	if err != nil {
		return res, err
	}

	if v := env(config.KUKEON_ROOT_SPACE_SUBNET_CIDR.Key); v != "" {
		if err = res.add(v, config.KUKEON_ROOT_SPACE_SUBNET_CIDR.Key+" environment variable"); err != nil {
			return res, err
		}
	}
	if v := strings.TrimSpace(server.Spec.SpaceSubnetCIDR); v != "" {
		err = res.add(v, "spaceSubnetCIDR in "+res.ServerConfig)
	} else {
		err = res.add(config.KUKEON_ROOT_SPACE_SUBNET_CIDR.Default,
			"compiled-in default; no spaceSubnetCIDR in "+res.ServerConfig)
	}
	if err != nil {
		return res, err
	}
	// The client configuration path resolves like kuke's
	// loadClientConfiguration: KUKE_CONFIGURATION, else the default
	// ~/.kuke/kuke.yaml. The default is read only when the home directory is
	// known; otherwise it is a relative path kuke ignores too (#1550).
	clientPath := env(config.KUKE_CONFIGURATION.Key)
	_, clientEnvSet := lookupEnv(config.KUKE_CONFIGURATION.Key)
	if clientPath == "" {
		clientPath = config.DefaultClientConfigurationFile()
	}
	var clientRunPath string
	if clientEnvSet || config.HomeDirKnown() {
		client, loadErr := clientconfig.Load(clientPath)
		if loadErr != nil {
			return res, loadErr
		}
		clientRunPath = strings.TrimSpace(client.Spec.RunPath)
		if v := strings.TrimSpace(client.Spec.SpaceSubnetCIDR); v != "" {
			if err = res.add(v, "spaceSubnetCIDR in "+clientPath); err != nil {
				return res, err
			}
		}
	}

	res.RunPath = runPath
	if res.RunPath == "" {
		res.RunPath = env(config.KUKEON_ROOT_RUN_PATH.Key)
	}
	if res.RunPath == "" {
		res.RunPath = strings.TrimSpace(server.Spec.RunPath)
	}
	if res.RunPath == "" {
		// kuke applies the client runPath first; the server configuration's
		// runPath, applied later by the admin verbs, overrides it.
		res.RunPath = clientRunPath
	}
	if res.RunPath == "" {
		res.RunPath = config.DefaultRunPath()
	}
	if res.Spaces, err = readSpaces(fs.MetadataRoot(res.RunPath)); err != nil {
		return res, err
	}
	for _, sp := range res.Spaces {
		res.Candidates = append(res.Candidates, candidate{
			Subnet: sp.Subnet,
			Source: fmt.Sprintf("persisted subnet of space %s/%s", sp.Realm, sp.Space),
		})
	}
	return res, nil
}

func (r *resolution) add(cidr, source string) error {
	subnet, err := netip.ParsePrefix(cidr)
	if err != nil || !subnet.Addr().Is4() {
		return fmt.Errorf("%s: %q is not an IPv4 CIDR", source, cidr)
	}
	r.Candidates = append(r.Candidates, candidate{Subnet: subnet.Masked(), Source: source})
	return nil
}

// ownBridges maps each provisioned space's bridge to its subnet.
func (r *resolution) ownBridges() map[string]netip.Prefix {
	bridges := make(map[string]netip.Prefix, len(r.Spaces))
	for _, sp := range r.Spaces {
		if sp.Bridge != "" {
			bridges[sp.Bridge] = sp.Subnet
		}
	}
	return bridges
}

// readSpaces lists the spaces under dataRoot (<run-path>/data/<realm>/<space>)
// that have a subnet: the conflist's IPAM subnet, which the bridge plugin
// applies, or else the allocator's network.json. A missing data root is a
// fresh host. An unreadable file fails the check: the guard must not pass on
// state it could not see.
func readSpaces(dataRoot string) ([]ownSpace, error) {
	realms, err := os.ReadDir(dataRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read metadata root %s: %w", dataRoot, err)
	}
	var spaces []ownSpace
	for _, realm := range realms {
		if !realm.IsDir() {
			continue
		}
		entries, readErr := os.ReadDir(filepath.Join(dataRoot, realm.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("read realm %s: %w", realm.Name(), readErr)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			sp, ok, spErr := readSpace(filepath.Join(dataRoot, realm.Name(), entry.Name()))
			if spErr != nil {
				return nil, spErr
			}
			if ok {
				sp.Realm, sp.Space = realm.Name(), entry.Name()
				spaces = append(spaces, sp)
			}
		}
	}
	sort.Slice(spaces, func(i, j int) bool {
		return spaces[i].Realm+"/"+spaces[i].Space < spaces[j].Realm+"/"+spaces[j].Space
	})
	return spaces, nil
}

func readSpace(dir string) (ownSpace, bool, error) {
	var (
		sp     ownSpace
		mgr    cni.Manager
		subnet string
	)
	conflist := filepath.Join(dir, "network.conflist")
	bridge, err := mgr.ReadBridgeName(conflist)
	switch {
	case errors.Is(err, errdefs.ErrNetworkNotFound):
		// No conflist: the allocator state alone may still hold a subnet.
	case errors.Is(err, errdefs.ErrBridgePluginMissing):
		return sp, false, nil // Not a bridge network.
	case err != nil:
		return sp, false, fmt.Errorf("%s: %w", conflist, err)
	default:
		sp.Bridge = bridge
		if subnet, err = mgr.ReadSubnetCIDR(conflist); err != nil {
			return sp, false, fmt.Errorf("%s: %w", conflist, err)
		}
	}
	if subnet == "" {
		if subnet, err = readAllocatorState(filepath.Join(dir, cni.SubnetStateFileName)); err != nil {
			return sp, false, err
		}
	}
	if subnet == "" {
		return sp, false, nil
	}
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil || !prefix.Addr().Is4() {
		return sp, false, fmt.Errorf("%s: subnet %q is not an IPv4 CIDR", dir, subnet)
	}
	sp.Subnet = prefix.Masked()
	return sp, true, nil
}

func readAllocatorState(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var st cni.SubnetState
	if err = json.Unmarshal(data, &st); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return strings.TrimSpace(st.SubnetCIDR), nil
}
