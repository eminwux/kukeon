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

// Package serverconfig loads a kukeond ServerConfiguration document from
// disk. Used by `kukeond --configuration` (daemon root command) and
// `kuke init --server-configuration` to feed defaults into viper before
// the daemon starts. An absent file returns a zero-value document so the
// caller can fall back to its existing defaults without an error.
package serverconfig

import (
	"errors"
	"fmt"
	"os"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// Load reads path and returns the parsed ServerConfiguration. When the file
// does not exist, returns a zero-value document and no error — the absent
// case is normal (callers fall back to hardcoded defaults). Any other read
// or parse failure is wrapped with errdefs.ErrServerConfigurationInvalid.
func Load(path string) (*v1beta1.ServerConfigurationDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &v1beta1.ServerConfigurationDoc{}, nil
		}
		return nil, fmt.Errorf(
			"read server configuration %q: %w: %w",
			path, errdefs.ErrServerConfigurationInvalid, err,
		)
	}

	var doc v1beta1.ServerConfigurationDoc
	if unmarshalErr := yaml.Unmarshal(raw, &doc); unmarshalErr != nil {
		return nil, fmt.Errorf(
			"parse server configuration %q: %w: %w",
			path, errdefs.ErrServerConfigurationInvalid, unmarshalErr,
		)
	}
	if doc.Kind != "" && doc.Kind != v1beta1.KindServerConfiguration {
		return nil, fmt.Errorf(
			"server configuration %q has kind %q, want %q: %w",
			path, doc.Kind, v1beta1.KindServerConfiguration,
			errdefs.ErrServerConfigurationInvalid,
		)
	}
	return &doc, nil
}
