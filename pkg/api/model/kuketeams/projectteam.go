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

// ProjectTeam is the per-project roster, committed as kuketeam.yaml in each
// project repo. It pins the agents source, declares harness defaults, and lists
// the roles the project runs. It travels with the project — so its agents
// `source` carries an explicit ref intent (a pinned tag/commit for a
// reproducible roster, or a floating branch tracked across inits).
type ProjectTeam struct {
	APIVersion string          `json:"apiVersion" yaml:"apiVersion"`
	Kind       string          `json:"kind"       yaml:"kind"`
	Metadata   Metadata        `json:"metadata"   yaml:"metadata"`
	Spec       ProjectTeamSpec `json:"spec"       yaml:"spec"`
}

// ProjectTeamSpec carries the roster.
type ProjectTeamSpec struct {
	// Source is the structured agents-repository reference (host-explicit repo
	// plus exactly one of tag/branch/commit). See TeamSource. The legacy
	// `<owner>/<repo>@vX.Y.Z` string form is rejected at parse time with a
	// migration error.
	Source TeamSource `json:"source"             yaml:"source"`
	// Realm, Space, and Stack are the optional scope coordinates the team's
	// rendered cells bind to. Each defaults to `default` when omitted (the
	// renderer stamps the defaulted value onto every rendered Blueprint/Config
	// so the persisted Config scope matches the live cell — see teamrender
	// DefaultRealm / DefaultSpace / DefaultStack and #1133).
	Realm string `json:"realm,omitempty"    yaml:"realm,omitempty"`
	Space string `json:"space,omitempty"    yaml:"space,omitempty"`
	Stack string `json:"stack,omitempty"    yaml:"stack,omitempty"`
	// Defaults supplies project-wide harness defaults applied to every role
	// that does not pin its own.
	Defaults ProjectTeamDefaults `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	// Roles is the roster: each entry references a role declared in the agents
	// source and may add project-specific capability needs.
	Roles []ProjectTeamRole `json:"roles"              yaml:"roles"`
}

// ProjectTeamDefaults holds project-wide defaults.
type ProjectTeamDefaults struct {
	// Harnesses lists the harness names enabled by default for this project's
	// roles. Entries must be known harnesses.
	Harnesses []string `json:"harnesses,omitempty" yaml:"harnesses,omitempty"`
}

// ProjectTeamRole references a role from the agents source and optionally adds
// project-specific capability needs on top of the role's own declaration.
type ProjectTeamRole struct {
	// Ref is the role name (matches a Role.metadata.name in the agents source).
	Ref string `json:"ref"             yaml:"ref"`
	// Needs adds project-specific image capabilities for this role in this
	// project. The entries are capability NAMES (selector input), never image
	// tags or digests.
	Needs *ProjectRoleNeeds `json:"needs,omitempty" yaml:"needs,omitempty"`
}

// ProjectRoleNeeds is the project-side capability override for a role. Only the
// image-capability dimension is project-tunable; repos/mounts/params/secrets are
// declared by the role itself in the agents source.
type ProjectRoleNeeds struct {
	// Image lists additional capability names this project's instance of the
	// role requires (e.g. [go]). Capability names, never image tags/digests.
	Image []string `json:"image,omitempty" yaml:"image,omitempty"`
}
