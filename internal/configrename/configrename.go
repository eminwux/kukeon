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

// Package configrename rejects settings that were renamed. A retired name is
// an error naming its replacement, never an alias: the configuration loaders
// ignore unknown keys, so an old key that was simply dropped would let the
// setting fall back to its default without a word (#1760).
//
// The retired spellings appear only here, once per surface (configuration
// key, environment variable). Callers and tests use the exported constants.
package configrename

import (
	"fmt"

	"github.com/eminwux/kukeon/internal/errdefs"
	"gopkg.in/yaml.v3"
)

const (
	// RetiredSpaceSubnetKey is the old spelling of the spaceSubnetCIDR
	// ServerConfiguration / ClientConfiguration spec key.
	RetiredSpaceSubnetKey = "podSubnetCIDR"
	// SpaceSubnetKey is the spec key that replaced RetiredSpaceSubnetKey.
	SpaceSubnetKey = "spaceSubnetCIDR"

	// RetiredSpaceSubnetEnv is the old name of the KUKEON_SPACE_SUBNET_CIDR
	// environment variable.
	RetiredSpaceSubnetEnv = "KUKEON_POD_SUBNET_CIDR"
	// SpaceSubnetEnv is the environment variable that replaced
	// RetiredSpaceSubnetEnv.
	SpaceSubnetEnv = "KUKEON_SPACE_SUBNET_CIDR"
)

// CheckSpec rejects a configuration document whose spec still sets a retired
// key. raw is the file's content and path names it in the error. A document
// that does not parse is left to the caller's own decode, which reports it.
func CheckSpec(raw []byte, path string) error {
	var probe struct {
		Spec map[string]any `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return nil //nolint:nilerr // the caller's decode reports parse errors.
	}
	if _, ok := probe.Spec[RetiredSpaceSubnetKey]; ok {
		return fmt.Errorf("%s was renamed to %s; update %s: %w",
			RetiredSpaceSubnetKey, SpaceSubnetKey, path, errdefs.ErrSettingRenamed)
	}
	return nil
}

// CheckEnv rejects a set retired environment variable. Any value, the empty
// string included, counts as set, matching viper's BindEnv semantics.
func CheckEnv(lookupEnv func(string) (string, bool)) error {
	if _, ok := lookupEnv(RetiredSpaceSubnetEnv); ok {
		return fmt.Errorf("%s was renamed to %s: %w",
			RetiredSpaceSubnetEnv, SpaceSubnetEnv, errdefs.ErrSettingRenamed)
	}
	return nil
}
