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

// ClientConfigurationDoc is the kuke-side configuration document. It is
// loaded by the `kuke` cobra root via `kuke --configuration <path>` (default
// `~/.kuke/kuke.yaml`) and seeds defaults for client-only settings such as
// the daemon endpoint and the default output format. It is not a server-side
// resource: `kuke apply` rejects it.
type ClientConfigurationDoc struct {
	APIVersion Version                     `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind                        `json:"kind"       yaml:"kind"`
	Metadata   ClientConfigurationMetadata `json:"metadata"   yaml:"metadata"`
	Spec       ClientConfigurationSpec     `json:"spec"       yaml:"spec"`
}

type ClientConfigurationMetadata struct {
	Name   string            `json:"name"             yaml:"name"`
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// ClientConfigurationSpec carries kuke-only settings. Each field is optional:
// an absent or empty value falls back to the client's hardcoded default.
// Explicit command-line flags (`--host`, `--log-level`, …) and matching env
// vars (`KUKEON_HOST`, `KUKEON_LOG_LEVEL`, …) still take precedence over
// values loaded from this document.
type ClientConfigurationSpec struct {
	// Host is the kukeond endpoint kuke dials by default
	// (`unix:///run/kukeon/kukeond.sock` or `ssh://user@host`).
	Host string `json:"host,omitempty"             yaml:"host,omitempty"`
	// RunPath is the kukeon runtime root used by `--no-daemon` operations
	// that read /opt/kukeon directly instead of going through kukeond.
	RunPath string `json:"runPath,omitempty"          yaml:"runPath,omitempty"`
	// ContainerdSocket is the containerd unix socket `--no-daemon`
	// operations connect to.
	ContainerdSocket string `json:"containerdSocket,omitempty" yaml:"containerdSocket,omitempty"`
	// LogLevel is the client log level when `--verbose` is on
	// (debug, info, warn, error).
	LogLevel string `json:"logLevel,omitempty"         yaml:"logLevel,omitempty"`
}
