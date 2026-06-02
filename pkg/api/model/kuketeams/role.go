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

package kuketeams

// Role is a per-role document (role.yaml) authored in the agents source. It
// declares the role's skills, per-harness configuration, and the capabilities,
// repos, mounts, params, and secrets the role needs. metadata is name-only: the
// source pin is the version authority, so role.yaml carries no version field.
type Role struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind"       yaml:"kind"`
	Metadata   Metadata `json:"metadata"   yaml:"metadata"`
	Spec       RoleSpec `json:"spec"       yaml:"spec"`
}

// RoleSpec carries the role declaration.
type RoleSpec struct {
	// Skills lists skill directories (relative paths) the role loads.
	Skills []string `json:"skills,omitempty"    yaml:"skills,omitempty"`
	// Harnesses maps a harness name to its per-role configuration. Keys must be
	// known harnesses.
	Harnesses map[string]RoleHarness `json:"harnesses,omitempty" yaml:"harnesses,omitempty"`
	// Needs declares what the role depends on, split by kind so each dimension
	// renders onto the right runtime type.
	Needs RoleNeeds `json:"needs,omitempty"     yaml:"needs,omitempty"`
}

// RoleHarness is the per-role configuration of a single harness. Different
// harnesses use different keys (claude reads Settings; codex reads
// Sandbox/Approval; opencode reads Permissions), so this is a permissive union
// of the recognized keys — an empty config (harness enabled with defaults) is
// valid.
type RoleHarness struct {
	// Settings is the path to a harness settings file (claude).
	Settings string `json:"settings,omitempty"    yaml:"settings,omitempty"`
	// Sandbox selects the sandbox mode (codex, e.g. workspace-write).
	Sandbox string `json:"sandbox,omitempty"     yaml:"sandbox,omitempty"`
	// Approval selects the approval policy (codex, e.g. on-request).
	Approval string `json:"approval,omitempty"    yaml:"approval,omitempty"`
	// Permissions selects the permission mode (opencode, e.g. skip).
	Permissions string `json:"permissions,omitempty" yaml:"permissions,omitempty"`
}

// RoleNeeds separates the role's dependencies by kind. Each list holds NAMES
// (selector inputs / identifiers), never inline values or image tags:
//
//   - Image: capability names (selector input for the ImageCatalog).
//   - Repos: repo names rendered to []v1beta1.ContainerRepo (git clones).
//     `project` and `agents` are repos.
//   - Mounts: mount names rendered to []v1beta1.VolumeMount (bind mounts).
//     `ssh` is a mount.
//   - Params: parameter names supplied at materialization time.
//   - Secrets: secret names resolved against TeamsConfig.secrets.
type RoleNeeds struct {
	Image   []string `json:"image,omitempty"   yaml:"image,omitempty"`
	Repos   []string `json:"repos,omitempty"   yaml:"repos,omitempty"`
	Mounts  []string `json:"mounts,omitempty"  yaml:"mounts,omitempty"`
	Params  []string `json:"params,omitempty"  yaml:"params,omitempty"`
	Secrets []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}
