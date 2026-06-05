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
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

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

// defaultDocumentTemplate is the commented YAML rendered by WriteDefault on
// first kukeond start. Every spec field is present with its in-binary default
// surfaced in a `# Default: …` comment so the operator can tweak the file
// without consulting source, and the live value is rendered from the spec the
// caller passes in — so the on-disk document reflects what the daemon
// actually bound to, not the compile-time defaults (issue #581). The string
// round-trips through Load.
const defaultDocumentTemplate = `# kukeond ServerConfiguration — auto-generated default.
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
  socket: {{printf "%q" .Socket}}

  # Numeric group ID the daemon chowns its listener socket to (mode 0o660 with
  # group). Zero means root-only access. ` + "`kuke init`" + ` plumbs the kukeon GID here
  # so non-root group members can dial the daemon after a kukeond restart.
  # Default: 0
  socketGID: {{.SocketGID}}

  # Kukeon runtime root — the on-disk hierarchy under which realms, spaces,
  # stacks, and cells live.
  # Default: /opt/kukeon
  runPath: {{printf "%q" .RunPath}}

  # Path to the containerd unix socket the daemon connects to.
  # Default: /run/containerd/containerd.sock
  containerdSocket: {{printf "%q" .ContainerdSocket}}

  # Daemon log level (debug, info, warn, error).
  # Default: info
  logLevel: {{printf "%q" .LogLevel}}

  # Daemon-wide default verbosity for the kuketty wrapper's own slog output
  # (debug, info, warn, error), applied when an Attachable cell omits the
  # per-container ` + "`spec.containers[].tty.logLevel`" + ` knob. A per-container
  # override always wins. Issue #599.
  # Default: info
  kukettyLogLevel: {{printf "%q" .KukettyLogLevel}}

  # Period of the daemon's background cell-reconciliation loop, expressed as a
  # Go time.Duration string. The loop walks every cell and re-derives
  # ` + "`status.state`" + ` against observed container state once per tick. Zero or
  # negative disables the loop.
  # Default: 30s
  reconcileInterval: {{printf "%q" .ReconcileInterval}}

  # Container image ` + "`kuke init`" + ` provisions for the kukeond system cell.
  # Read by ` + "`kuke init`" + ` only; the daemon ignores it. Empty means kuke init
  # picks the release-matching ghcr.io/eminwux/kukeon image automatically.
  # Default: ""
  kukeondImage: {{printf "%q" .KukeondImage}}

  # Suffix appended to every realm name to form its containerd namespace.
  # Realm "default" + suffix "kukeon.io" -> namespace "default.kukeon.io".
  # Set to a different suffix (e.g. "dev.kukeon.io") to run a parallel
  # kukeon instance on the same host under a disjoint containerd namespace.
  # Default: kukeon.io
  containerdNamespaceSuffix: {{printf "%q" .ContainerdNamespaceSuffix}}

  # Cgroup root under which all realms / spaces / stacks / cells live.
  # Set to a different root (e.g. "/kukeon-dev") to run a parallel kukeon
  # instance on the same host under a disjoint cgroup tree.
  # Default: /kukeon
  cgroupRoot: {{printf "%q" .CgroupRoot}}

  # Parent CIDR the per-space CNI subnet allocator subdivides into /24 chunks.
  # Set to a non-overlapping block (e.g. "10.89.0.0/16") for a parallel or
  # nested kukeon instance so its allocator never lands on another instance's
  # subnet — a nested ` + "`make dev-init`" + ` must avoid the parent host's
  # 10.88.0.0/16 + .1 gateway, which is the dev-root cell's own default gateway.
  # Default: 10.88.0.0/16
  podSubnetCIDR: {{printf "%q" .PodSubnetCIDR}}

  # Daemon-wide fallback memory limit (in bytes) applied to every admitted
  # container whose Resources.MemoryLimitBytes is unset or zero. An explicit
  # per-container limit always wins. Recommended on hosts without swap and
  # without a userspace OOM guard (systemd-oomd / earlyoom), where an
  # unbounded workload can wedge the whole host. Zero disables the fallback.
  # Default: 0
  defaultMemoryLimitBytes: {{.DefaultMemoryLimitBytes}}
`

// defaultDocumentTmpl is the parsed template used by WriteDefault. Parsing
// once at package init keeps the per-call cost down and surfaces any
// future template-syntax breakage at process start rather than first write.
//
//nolint:gochecknoglobals // package-level parsed template — see godoc above.
var defaultDocumentTmpl = template.Must(
	template.New("kukeond-serverconfig-default").Parse(defaultDocumentTemplate),
)

// WriteDefault renders the commented default ServerConfiguration with the
// resolved spec the caller passes and writes it to path when the file is
// absent. Returns true only when this call created the file; an existing
// file (any contents) is left untouched, satisfying the "first-write only"
// rule. The spec's values are interpolated into the YAML so the on-disk
// document reflects the values the daemon actually started with, not a
// hardcoded snapshot of the compile-time defaults (issue #581). Creates
// the parent directory if missing. Any other failure is returned wrapped.
func WriteDefault(path string, spec v1beta1.ServerConfigurationSpec) (bool, error) {
	var rendered bytes.Buffer
	if err := defaultDocumentTmpl.Execute(&rendered, spec); err != nil {
		return false, fmt.Errorf("render default server configuration: %w", err)
	}

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
	if _, writeErr := f.Write(rendered.Bytes()); writeErr != nil {
		return false, fmt.Errorf("write %q: %w", path, writeErr)
	}
	return true, nil
}
