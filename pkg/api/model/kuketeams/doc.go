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

// Package kuketeams holds the Go types for the team-distribution contract
// (issue #793, epic #792). The kinds — ProjectTeam, TeamsConfig, TeamEntry,
// Role, Harness, and ImageCatalog — model in-repo team composition:
//
//   - ProjectTeam (committed in each project repo, `kuketeam.yaml`) declares the
//     roster: which pinned agents source, which roles, which harness defaults.
//   - TeamsConfig (host, operator-owned, `~/.kuke/kuketeams.yaml`) carries
//     operator facts only — git identity/signing, registry, source clone URLs,
//     secret sources.
//   - TeamEntry (host, `~/.kuke/kuketeam.d/<project>.yaml`, one per project)
//     carries the per-project composition record `kuke team init` writes — a
//     drop-in directory so a corrupt write touches one project, not all (#796).
//   - Role / Harness / ImageCatalog live in the pinned agents source. They
//     declare, respectively, a role's skills + harness configs + needs, a
//     harness's base image + skill path + make target, and the prebuilt
//     image → capability map that is the v1 image selector input.
//
// All five kinds are GVK objects sharing one API group, kuketeams.io/v1. The
// agents-side kinds (Role / Harness / ImageCatalog) are authored in the agents
// repo but deserialized here — kukeon's parser owns the schema shape of all
// five. Content versioning is carried solely by the structured `source`
// reference (TeamSource: repo + one of tag/branch/commit) in
// ProjectTeam/TeamEntry; the agents kinds carry no in-file version field (it
// would be redundant with the ref and drift-prone).
//
// The team-layer types are declarative sugar that render onto the existing
// v1beta1 runtime: TeamsConfig.git is a strict superset of v1beta1.ContainerGit,
// Role.needs.repos render to []v1beta1.ContainerRepo, Role.needs.mounts render
// to []v1beta1.VolumeMount. This package defines the parsed surface only; no
// CLI verb consumes it yet (the verbs land in #796 and later).
package kuketeams

// APIVersionV1 is the canonical API version (group/version) shared by every
// team-distribution kind. Unlike the v1beta1 runtime types, the team kinds use
// a group-qualified apiVersion so they never collide with the apply/get surface.
const APIVersionV1 = "kuketeams.io/v1"

// Kinds.
const (
	// KindProjectTeam identifies the per-project roster committed as
	// kuketeam.yaml in each project repo.
	KindProjectTeam = "ProjectTeam"
	// KindTeamsConfig identifies the host-side, operator-owned global-facts
	// document (~/.kuke/kuketeams.yaml) maintained by `kuke team init`.
	KindTeamsConfig = "TeamsConfig"
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

// GitSignCommits and GitSignTags are the accepted entries in TeamsConfig.git.sign,
// mirroring v1beta1's GitSignCommits/GitSignTags so the rendered git config keys
// (commit.gpgsign / tag.gpgsign) stay aligned with the runtime contract.
const (
	GitSignCommits = "commits"
	GitSignTags    = "tags"
)

// Secret source modes for TeamsConfig.secrets — a secret declares where its
// value comes from, never an inline value.
const (
	// SecretFromEnv reads the secret value from a host environment variable
	// named by the entry's Key.
	SecretFromEnv = "env"
	// SecretFromFile reads the secret value from a host file path named by the
	// entry's Key.
	SecretFromFile = "file"
)

// Metadata is the name-only metadata block carried by ProjectTeam, Role, and
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
