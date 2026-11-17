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
	Skills []string `json:"skills,omitempty"     yaml:"skills,omitempty"`
	// Harnesses maps a harness name to its per-role configuration. Keys must be
	// known harnesses.
	Harnesses map[string]RoleHarness `json:"harnesses,omitempty"  yaml:"harnesses,omitempty"`
	// Connectors lists the connector components the role's cells enable, by
	// name (e.g. `github`: the GitHub API token as GH_TOKEN plus the `gh`
	// capability). A connector never supplies a git credential.
	Connectors []string `json:"connectors,omitempty" yaml:"connectors,omitempty"`
	// Forges lists the forge components (issue/change trackers) the role's
	// cells enable, by name (e.g. `github-issues`).
	Forges []string `json:"forges,omitempty"     yaml:"forges,omitempty"`
	// Needs declares what the role depends on, split by kind so each dimension
	// renders onto the right runtime type.
	Needs RoleNeeds `json:"needs,omitempty"      yaml:"needs,omitempty"`
	// Tasks are the named, argument-free unattended runs of the role. `kuke
	// team add` renders each one a team selects as its own headless-only,
	// self-deleting CellConfig (`<team>-<role>-<harness>-<task>`).
	Tasks []RoleTask `json:"tasks,omitempty"      yaml:"tasks,omitempty"`
}

// RoleTask is one named task of a role: a skill the role loads, run headless
// with an optional fixed argument string. The rendered prompt is
// `Use the <skill> skill`, followed by ` with arguments: <args>` when Args is
// set. A task may also carry a default schedule (issue #1503).
type RoleTask struct {
	// Name is the task's resource-name segment, unique within the role.
	Name string `json:"name"               yaml:"name"`
	// Skill is the base name of one of the role's spec.skills entries.
	Skill string `json:"skill"              yaml:"skill"`
	// Args is optional free text appended to the prompt.
	Args string `json:"args,omitempty"     yaml:"args,omitempty"`
	// Schedule is the role's default cadence for the task: a five-field cron
	// expression or @hourly/@daily/@weekly/@monthly, read in UTC. `kuke team
	// add` renders it as the task config's spec.schedule, so kukeond runs the
	// task on it. A team's task entry may override it. Unset, the task runs
	// only when started by hand.
	Schedule string `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	// Suspend ships the schedule switched off: the task config is rendered
	// with spec.schedule.suspend set, and fires only once a team's task entry
	// says `suspend: false`. It needs a schedule, here or from the team.
	Suspend bool `json:"suspend,omitempty"  yaml:"suspend,omitempty"`
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
	// Secrets are the secret NAMES (resolved against TeamsConfig.secrets) this
	// harness needs, declared per-harness so a role's Claude cell no longer
	// carries secret slots only its Codex/OpenCode cells use (agents#750). A
	// name the harness or one of its modes declares fills that slot; `gh-token`
	// is the transition-era spelling of `connectors: [github]`; any other name
	// becomes an optional slot on the shared blueprint.
	Secrets []string `json:"secrets,omitempty"     yaml:"secrets,omitempty"`
}

// RoleNeeds separates the role's dependencies by kind. Each list holds NAMES
// (selector inputs / identifiers), never inline values or image tags:
//
//   - Image: capability names (selector input for the ImageCatalog).
//   - Repos: repo names rendered to []v1beta1.ContainerRepo (git clones).
//     `project` and `agents` are repos.
//   - Mounts: host-mount selectors (`kukeond-socket`, `kuke-binary`,
//     `kukeon-install`). They are part of the security posture, so they pick
//     the blueprint variant rather than a config toggle. `ssh` is still
//     accepted but mounts nothing: cells use stored git credentials.
//   - Params: parameter names supplied at materialization time.
//   - Secrets: secret names resolved against TeamsConfig.secrets.
type RoleNeeds struct {
	Image   []string `json:"image,omitempty"   yaml:"image,omitempty"`
	Repos   []string `json:"repos,omitempty"   yaml:"repos,omitempty"`
	Mounts  []string `json:"mounts,omitempty"  yaml:"mounts,omitempty"`
	Params  []string `json:"params,omitempty"  yaml:"params,omitempty"`
	Secrets []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}
