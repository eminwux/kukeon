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

// Package clientconfig loads a kuke ClientConfiguration document from disk.
// Used by `kuke --configuration` to feed defaults into viper before any
// subcommand runs. An absent file returns a zero-value document so the
// caller can fall back to its existing defaults without an error.
package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// Load reads path and returns the parsed ClientConfiguration. When the file
// does not exist, returns a zero-value document and no error — the absent
// case is normal (callers fall back to hardcoded defaults). Any other read
// or parse failure is wrapped with errdefs.ErrClientConfigurationInvalid.
func Load(path string) (*v1beta1.ClientConfigurationDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &v1beta1.ClientConfigurationDoc{}, nil
		}
		return nil, fmt.Errorf(
			"read client configuration %q: %w: %w",
			path, errdefs.ErrClientConfigurationInvalid, err,
		)
	}

	var doc v1beta1.ClientConfigurationDoc
	if unmarshalErr := yaml.Unmarshal(raw, &doc); unmarshalErr != nil {
		return nil, fmt.Errorf(
			"parse client configuration %q: %w: %w",
			path, errdefs.ErrClientConfigurationInvalid, unmarshalErr,
		)
	}
	if doc.Kind != "" && doc.Kind != v1beta1.KindClientConfiguration {
		return nil, fmt.Errorf(
			"client configuration %q has kind %q, want %q: %w",
			path, doc.Kind, v1beta1.KindClientConfiguration,
			errdefs.ErrClientConfigurationInvalid,
		)
	}
	return &doc, nil
}

// defaultDocument is the commented YAML written by WriteDefault on first
// kuke invocation. Every spec field is present with its in-binary default and
// a header comment explaining its purpose so the operator can tweak the file
// without consulting source. The string round-trips through Load.
const defaultDocument = `# kuke ClientConfiguration — auto-generated default.
# kuke reads this file via --configuration (default ~/.kuke/kuke.yaml).
# Precedence: explicit --flag > KUKEON_* env > this file > hardcoded default.
# Existing files are never overwritten; delete this file to regenerate.
apiVersion: v1beta1
kind: ClientConfiguration
metadata:
  name: default
spec:
  # kukeond endpoint kuke dials by default.
  # Examples: unix:///run/kukeon/kukeond.sock, ssh://user@host
  # Default: unix:///run/kukeon/kukeond.sock
  host: unix:///run/kukeon/kukeond.sock

  # Kukeon runtime root used by --no-daemon operations that read /opt/kukeon
  # directly instead of going through kukeond.
  # Default: /opt/kukeon
  runPath: /opt/kukeon

  # Containerd unix socket --no-daemon operations connect to.
  # Default: /run/containerd/containerd.sock
  containerdSocket: /run/containerd/containerd.sock

  # Client log level when --verbose is on (debug, info, warn, error).
  # Default: info
  logLevel: info
`

// WriteDefault writes the commented default ClientConfiguration to path when
// the file is absent. Returns true only when this call created the file; an
// existing file (any contents) is left untouched, satisfying the "first-write
// only" rule. Creates the parent directory if missing. Any other failure is
// returned wrapped.
func WriteDefault(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, fmt.Errorf("create parent directory for %q: %w", path, err)
	}
	// O_EXCL closes the TOCTOU race between Stat and Create — two concurrent
	// kuke invocations can't both believe they wrote the file.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("create %q: %w", path, err)
	}
	defer f.Close()
	if _, writeErr := f.WriteString(defaultDocument); writeErr != nil {
		return false, fmt.Errorf("write %q: %w", path, writeErr)
	}
	return true, nil
}
