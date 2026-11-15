// Copyright 2026 Emiliano Spinella (eminwux)
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

package initialize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

const defaultHost = "unix:///run/kukeon/kukeond.sock"

// Match kuke's precedence: flag > KUKEON_HOST > ClientConfiguration > default.
// The loader is local because kukeon's clientconfig package is internal.
func resolveHost(opts Options) (string, error) {
	path := opts.Configuration
	explicit := path != ""
	if path == "" {
		path = os.Getenv("KUKE_CONFIGURATION")
		explicit = path != ""
	}
	if path == "" {
		path = filepath.Join(opts.Root, "kuke.yaml")
	}
	var doc api.ClientConfigurationDoc
	raw, err := os.ReadFile(path)
	if err != nil && (!errors.Is(err, os.ErrNotExist) || explicit) {
		return "", fmt.Errorf("read client configuration %s: %w", path, err)
	}
	if err == nil {
		if yaml.Unmarshal(raw, &doc) != nil {
			return "", fmt.Errorf("invalid client configuration %s", path)
		}
		if (doc.Kind != "" && doc.Kind != api.KindClientConfiguration) ||
			(doc.APIVersion != "" && doc.APIVersion != api.APIVersionV1Beta1) {
			return "", fmt.Errorf("invalid client configuration kind/version in %s", path)
		}
	}
	host := opts.Host
	if host == "" {
		host = os.Getenv("KUKEON_HOST")
	}
	if host == "" {
		host = doc.Spec.Host
	}
	if host == "" {
		host = defaultHost
	}
	if !strings.HasPrefix(host, "unix:///") || !filepath.IsAbs(strings.TrimPrefix(host, "unix://")) {
		return "", errors.New("kukeond host must be unix:///absolute/path/to/socket")
	}
	return host, nil
}
