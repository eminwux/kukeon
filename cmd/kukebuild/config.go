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
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultKukeondConfigFile mirrors kukeond's DefaultServerConfigurationFile()
// (cmd/config/defaults.go) — the on-disk path the daemon and `kuke init` read
// the ServerConfiguration from. kukebuild reads the same file to resolve the
// realm->namespace suffix so an image built under a non-default suffix lands
// in the containerd namespace `kuke create` resolves. Hardcoded here (not
// imported from internal/consts or cmd/config) so kukebuild's module pulls no
// kukeon internal package: the cross-module coupling lives on the public
// kukeond.yaml config key, not a private Go symbol (issue #655).
const defaultKukeondConfigFile = "/etc/kukeon/kukeond.yaml"

// defaultRealmNamespaceSuffix is kukebuild's in-binary fallback for the
// containerd namespace suffix, kept equal to kukeond's
// consts.DefaultRealmNamespaceSuffix. Used when no kukeond.yaml is present, or
// it omits containerdNamespaceSuffix.
const defaultRealmNamespaceSuffix = "kukeon.io"

// serverConfigDoc is the minimal subset of kukeond's ServerConfiguration
// document kukebuild parses: only spec.containerdNamespaceSuffix. Kept local
// (rather than importing pkg/api/model/v1beta1) so kukebuild's module stays
// free of any kukeon package import; the `containerdNamespaceSuffix` YAML key
// must stay in lockstep with the public contract in
// pkg/api/model/v1beta1.ServerConfigurationSpec.
type serverConfigDoc struct {
	Spec struct {
		ContainerdNamespaceSuffix string `yaml:"containerdNamespaceSuffix"`
	} `yaml:"spec"`
}

// resolveNamespaceSuffix returns the containerd namespace suffix kukebuild
// joins to the realm name. Precedence (issue #655):
//
//  1. the kukeond.yaml at configPath (the --kukeond-config flag), if set;
//  2. else /etc/kukeon/kukeond.yaml (DefaultServerConfigurationFile);
//  3. else (file absent / key empty) the hardcoded default "kukeon.io".
//
// An explicitly-supplied configPath that does not exist is an error — the
// operator named a file kukebuild could not read. The implicit default path
// being absent is the normal fall-through to the hardcoded default, matching
// serverconfig.Load's absent-doc handling on the kukeond side.
func resolveNamespaceSuffix(configPath string) (string, error) {
	configPath = strings.TrimSpace(configPath)
	explicit := configPath != ""
	if !explicit {
		configPath = defaultKukeondConfigFile
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return defaultRealmNamespaceSuffix, nil
		}
		return "", fmt.Errorf("read kukeond configuration %q: %w", configPath, err)
	}

	var doc serverConfigDoc
	if unmarshalErr := yaml.Unmarshal(raw, &doc); unmarshalErr != nil {
		return "", fmt.Errorf("parse kukeond configuration %q: %w", configPath, unmarshalErr)
	}

	suffix := strings.TrimSpace(doc.Spec.ContainerdNamespaceSuffix)
	if suffix == "" {
		return defaultRealmNamespaceSuffix, nil
	}
	if validateErr := validateSuffix(suffix); validateErr != nil {
		return "", fmt.Errorf("kukeond configuration %q: %w", configPath, validateErr)
	}
	return suffix, nil
}

// validateSuffix mirrors kukeond's consts.ConfigureRuntime validation so
// kukebuild rejects a malformed containerdNamespaceSuffix the same way the
// daemon refuses to start under it — a suffix kukeond would not accept can
// never name a real namespace, so building into it would only orphan the
// resulting image.
func validateSuffix(suffix string) error {
	if strings.HasPrefix(suffix, ".") || strings.HasSuffix(suffix, ".") {
		return fmt.Errorf("containerdNamespaceSuffix %q must not start or end with '.'", suffix)
	}
	if strings.ContainsAny(suffix, "/ \t\n") {
		return fmt.Errorf("containerdNamespaceSuffix %q contains a disallowed character", suffix)
	}
	return nil
}

// realmNamespace joins a realm and the resolved suffix into the containerd
// namespace, mirroring kukeond's consts.RealmNamespace (<realm>.<suffix>).
func realmNamespace(realm, suffix string) string {
	return realm + "." + suffix
}
