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

// Package kuketeams holds the kuketeams.io/v1 configuration and agent-source
// types. Workspace is the user configuration at ~/.kuke/workspace.yaml; Team
// is the composition model consumed by the team CLI, resolver and renderer.
// Their predecessors have been removed, with no compatibility aliases or kind
// conversions. See docs/workspace-team-configuration.md for the contract.
//
// TeamEntry is an existing generated record, rejected as configuration. Role, Harness
// and ImageCatalog are versioned agent-source definitions. These types render
// onto existing v1beta1 resources; they add no Workspace or Team daemon resource.
// Structural parsing lives in internal/kuketeams. User-owned Team discovery and
// cross-source/reference resolution remain follow-on work.
package kuketeams

// APIVersionV1 is the canonical API version (group/version) shared by every
// team-distribution kind. Unlike the v1beta1 runtime types, the team kinds use
// a group-qualified apiVersion so they never collide with the apply/get surface.
const APIVersionV1 = "kuketeams.io/v1"

// Kinds.
const (
	KindWorkspace = "Workspace"
	KindTeam      = "Team"
	// KindTeamEntry identifies a host-side, per-project composition record
	// (~/.kuke/kuketeam.d/<project>.yaml) written by `kuke team init` — one
	// document per project file.
	KindTeamEntry = "TeamEntry"
	// KindRole identifies a per-role document (role.yaml) in the agents source.
	KindRole = "Role"
	// KindHarness identifies a per-harness document (harness.yaml) in the
	// agents source.
	KindHarness = "Harness"
	// KindImageCatalog identifies the prebuilt-image → capability map
	// (harnesses/images.yaml) in the agents source — the v1 selector input.
	KindImageCatalog = "ImageCatalog"
)

// GitSignCommits and GitSignTags are the accepted entries in Workspace.git.sign,
// mirroring v1beta1's GitSignCommits/GitSignTags so the rendered git config keys
// (commit.gpgsign / tag.gpgsign) stay aligned with the runtime contract.
const (
	GitSignCommits = "commits"
	GitSignTags    = "tags"
)

// Secret source modes for Workspace.secrets — a secret declares where its
// value comes from, never an inline value.
const (
	// SecretFromEnv reads the secret value from a host environment variable
	// named by the entry's Key.
	SecretFromEnv = "env"
	// SecretFromFile reads the secret value from a host file path named by the
	// entry's Key.
	SecretFromFile = "file"
)

// Metadata is the name-only metadata block carried by Team, Role, and
// Harness. The team kinds intentionally omit a version field — the structured
// source ref (repo + one of tag/branch/commit) is the version authority.
type Metadata struct {
	Name string `json:"name" yaml:"name"`
}

// KnownHarnesses is the set of harness names the v1 contract recognizes. Role
// harness keys, Harness.metadata.name, and ImageCatalog images[].harness are all
// validated against this set.
//
//nolint:gochecknoglobals // package-level membership set, read-only.
var KnownHarnesses = map[string]bool{
	"claude":   true,
	"codex":    true,
	"opencode": true,
}

// IsKnownHarness reports whether name is a recognized harness.
func IsKnownHarness(name string) bool {
	return KnownHarnesses[name]
}
