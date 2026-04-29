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
	Socket string `json:"socket,omitempty"           yaml:"socket,omitempty"`
	// SocketGID is the numeric group ID the daemon chowns its listener
	// socket to (mode 0o660 with group). Zero means root-only access.
	SocketGID int `json:"socketGID,omitempty"        yaml:"socketGID,omitempty"`
	// RunPath is the kukeon runtime root (e.g. /opt/kukeon).
	RunPath string `json:"runPath,omitempty"          yaml:"runPath,omitempty"`
	// ContainerdSocket is the path to the containerd unix socket the
	// daemon connects to.
	ContainerdSocket string `json:"containerdSocket,omitempty" yaml:"containerdSocket,omitempty"`
	// LogLevel is the daemon log level (debug, info, warn, error).
	LogLevel string `json:"logLevel,omitempty"         yaml:"logLevel,omitempty"`
	// KukeondImage is the container image `kuke init` provisions for the
	// kukeond system cell. Read by `kuke init` only; the daemon ignores it.
	KukeondImage string `json:"kukeondImage,omitempty"     yaml:"kukeondImage,omitempty"`
}
