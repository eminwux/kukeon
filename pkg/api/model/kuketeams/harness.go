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

// Harness is a per-harness document (harness.yaml) authored in the agents
// source. It declares the base image, in-container skill path, make target, and
// blueprint template a harness uses. metadata.name must be a known harness.
type Harness struct {
	APIVersion string      `json:"apiVersion" yaml:"apiVersion"`
	Kind       string      `json:"kind"       yaml:"kind"`
	Metadata   Metadata    `json:"metadata"   yaml:"metadata"`
	Spec       HarnessSpec `json:"spec"       yaml:"spec"`
}

// HarnessSpec carries the harness declaration.
type HarnessSpec struct {
	// BaseImage is the base image ref the harness builds on. Optional.
	BaseImage string `json:"baseImage,omitempty" yaml:"baseImage,omitempty"`
	// SkillPath is the in-container directory the harness loads skills from.
	// Required.
	SkillPath string `json:"skillPath"           yaml:"skillPath"`
	// MakeTarget is the make target that builds the harness image. Required.
	MakeTarget string `json:"makeTarget"          yaml:"makeTarget"`
	// Template is the blueprint template file the harness renders cells from.
	// Required.
	Template string `json:"template"            yaml:"template"`
	// Seeds declares per-(team,harness) state files the provisioning pass
	// writes when absent. Hand-edited files are never overwritten — a seed
	// that already exists is left untouched. Optional.
	Seeds []HarnessSeed `json:"seeds,omitempty"     yaml:"seeds,omitempty"`
}

// HarnessSeed declares one state file the provisioning pass writes when
// absent. Path supports `${TEAM_ROOT}` and `${HARNESS}` substitution
// (relative paths are anchored under the per-team root). A bare `${HARNESS}`
// expands to the harness's metadata.name. Mode is the on-disk file mode
// (octal in YAML — `0o644`); the zero value defaults to 0o644. Content is
// written verbatim.
type HarnessSeed struct {
	Path    string `json:"path"              yaml:"path"`
	Mode    int    `json:"mode,omitempty"    yaml:"mode,omitempty"`
	Content string `json:"content,omitempty" yaml:"content,omitempty"`
}
