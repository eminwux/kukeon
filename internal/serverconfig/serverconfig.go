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
	"path/filepath"

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

// defaultDocument is the commented YAML written by WriteDefault on first
// kukeond start. Every spec field is present with its in-binary default and
// a header comment explaining its purpose so the operator can tweak the file
// without consulting source. The string round-trips through Load.
const defaultDocument = `# kukeond ServerConfiguration — auto-generated default.
# kukeond reads this file via --configuration (default /etc/kukeon/kukeond.yaml).
# Precedence: explicit --flag > KUKEON_*/KUKEOND_* env > this file > hardcoded default.
# Existing files are never overwritten; delete this file to regenerate.
apiVersion: v1beta1
kind: ServerConfiguration
metadata:
  name: default
spec:
  # Unix socket path the daemon listens on.
  # Default: /run/kukeon/kukeond.sock
  socket: /run/kukeon/kukeond.sock

  # Numeric group ID the daemon chowns its listener socket to (mode 0o660 with
  # group). Zero means root-only access. ` + "`kuke init`" + ` plumbs the kukeon GID here
  # so non-root group members can dial the daemon after a kukeond restart.
  # Default: 0
  socketGID: 0

  # Kukeon runtime root — the on-disk hierarchy under which realms, spaces,
  # stacks, and cells live.
  # Default: /opt/kukeon
  runPath: /opt/kukeon

  # Path to the containerd unix socket the daemon connects to.
  # Default: /run/containerd/containerd.sock
  containerdSocket: /run/containerd/containerd.sock

  # Daemon log level (debug, info, warn, error).
  # Default: info
  logLevel: info

  # Container image ` + "`kuke init`" + ` provisions for the kukeond system cell.
  # Read by ` + "`kuke init`" + ` only; the daemon ignores it. Empty means kuke init
  # picks the release-matching ghcr.io/eminwux/kukeon image automatically.
  # Default: ""
  kukeondImage: ""
`

// WriteDefault writes the commented default ServerConfiguration to path when
// the file is absent. Returns true only when this call created the file; an
// existing file (any contents) is left untouched, satisfying the "first-write
// only" rule. Creates the parent directory if missing. Any other failure is
// returned wrapped.
func WriteDefault(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, fmt.Errorf("create parent directory for %q: %w", path, err)
	}
	// O_EXCL closes the TOCTOU race between Stat and Create — two concurrent
	// kukeond starts can't both believe they wrote the file.
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
