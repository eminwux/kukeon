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

package v1beta1

// ServerConfigurationDoc is the kukeond-side configuration document. It is
// loaded by the kukeond daemon (via `kukeond --configuration <path>`) and by
// `kuke init --server-configuration <path>` when bootstrapping the daemon. It
// is not a server-side resource: `kuke apply` rejects it.
type ServerConfigurationDoc struct {
	APIVersion Version                     `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind                        `json:"kind"       yaml:"kind"`
	Metadata   ServerConfigurationMetadata `json:"metadata"   yaml:"metadata"`
	Spec       ServerConfigurationSpec     `json:"spec"       yaml:"spec"`
}

type ServerConfigurationMetadata struct {
	Name   string            `json:"name"             yaml:"name"`
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// ServerConfigurationSpec carries kukeond-only settings. Each field is
// optional: an absent or empty value falls back to the daemon's hardcoded
// default. Explicit command-line flags (`--socket`, `--run-path`, …) still
// take precedence over values loaded from this document.
type ServerConfigurationSpec struct {
	// Socket is the unix socket path the daemon listens on.
	Socket string `json:"socket,omitempty"                    yaml:"socket,omitempty"`
	// SocketGID is the numeric group ID the daemon chowns its listener
	// socket to (mode 0o660 with group). Zero means root-only access.
	SocketGID int `json:"socketGID,omitempty"                 yaml:"socketGID,omitempty"`
	// RunPath is the kukeon runtime root (e.g. /opt/kukeon).
	RunPath string `json:"runPath,omitempty"                   yaml:"runPath,omitempty"`
	// ContainerdSocket is the path to the containerd unix socket the
	// daemon connects to.
	ContainerdSocket string `json:"containerdSocket,omitempty"          yaml:"containerdSocket,omitempty"`
	// LogLevel is the daemon log level (debug, info, warn, error).
	LogLevel string `json:"logLevel,omitempty"                  yaml:"logLevel,omitempty"`
	// KukettyLogLevel is the daemon-wide default verbosity of the kuketty
	// wrapper's own slog output, applied to every Attachable container
	// whose cell schema does not pin a per-container ContainerTty.LogLevel.
	// Accepted values: "debug", "info", "warn", "error"; empty is treated
	// as "info" by the daemon. Lets operators flip every attachable cell
	// on the host to "debug" without editing each cell YAML. Issue #599.
	KukettyLogLevel string `json:"kukettyLogLevel,omitempty"           yaml:"kukettyLogLevel,omitempty"`
	// ReconcileInterval is the period of the daemon's background cell
	// reconciliation loop, expressed as a Go time.Duration string (e.g.
	// "30s", "1m"). Empty falls back to the in-binary default. A zero or
	// negative duration disables the loop.
	ReconcileInterval string `json:"reconcileInterval,omitempty"         yaml:"reconcileInterval,omitempty"`
	// KukeondImage is the container image `kuke init` provisions for the
	// kukeond system cell. Read by `kuke init` only; the daemon ignores it.
	KukeondImage string `json:"kukeondImage,omitempty"              yaml:"kukeondImage,omitempty"`
	// ContainerdNamespaceSuffix is the suffix appended to every realm name
	// to form its containerd namespace. Realm "default" + suffix
	// "kukeon.io" -> namespace "default.kukeon.io". Lets two kukeon
	// instances coexist on the same host under disjoint namespaces.
	// Default: kukeon.io.
	ContainerdNamespaceSuffix string `json:"containerdNamespaceSuffix,omitempty" yaml:"containerdNamespaceSuffix,omitempty"`
	// CgroupRoot is the cgroup root under which all realms / spaces /
	// stacks / cells live (e.g. /kukeon-dev for a parallel dev instance on
	// the same host). Default: /kukeon.
	CgroupRoot string `json:"cgroupRoot,omitempty"                yaml:"cgroupRoot,omitempty"`
	// PodSubnetCIDR is the parent block the per-space CNI subnet allocator
	// subdivides into /24 chunks. Set it to a non-overlapping block (e.g.
	// 10.89.0.0/16) when running a parallel or nested kukeon instance so its
	// allocator never lands on another instance's subnet — in particular a
	// nested `make dev-init` must avoid the parent host's 10.88.0.0/16 + .1
	// gateway, which is the dev-root cell's own default gateway (issue
	// #1079). Default: 10.88.0.0/16.
	PodSubnetCIDR string `json:"podSubnetCIDR,omitempty"             yaml:"podSubnetCIDR,omitempty"`
	// DefaultMemoryLimitBytes is the daemon-wide fallback memory limit
	// applied to every admitted container whose
	// ContainerSpec.Resources.MemoryLimitBytes is unset or zero. Closes the
	// host-wedge gap on no-swap, no-userspace-OOM hosts where an unbounded
	// container can consume enough RAM to evict the daemon and journald.
	// Zero (the default) preserves the prior behavior — no fallback.
	// Operators should set this on hosts without swap and without
	// systemd-oomd / earlyoom. An explicit per-container limit always wins.
	// Issue #531. Default: 0.
	DefaultMemoryLimitBytes int64 `json:"defaultMemoryLimitBytes,omitempty"   yaml:"defaultMemoryLimitBytes,omitempty"`
}
