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

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

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
	// create team` renders each one a team selects as its own headless-only,
	// self-deleting CellConfig (`<team>-<role>-<harness>-<task>`).
	Tasks []RoleTask `json:"tasks,omitempty"      yaml:"tasks,omitempty"`
}

// RoleTask is one named task of a role, run headless with an optional fixed
// argument string. On a model harness the task names a skill the role loads
// and the rendered prompt is `Use the <skill> skill`, followed by
// ` with arguments: <args>` when Args is set. On the script harness the task
// names no skill: the cell runs the role's command with Args appended. A task
// may also carry a default schedule (issue #1503).
type RoleTask struct {
	// Name is the task's resource-name segment, unique within the role.
	Name string `json:"name"               yaml:"name"`
	// Skill is the base name of one of the role's spec.skills entries. A
	// model-harness task requires it; a script-harness task must leave it
	// empty.
	Skill string `json:"skill,omitempty"    yaml:"skill,omitempty"`
	// Args is optional free text appended to the prompt (model harnesses) or
	// to the role's command (script harness).
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
// Sandbox/Approval; opencode reads Permissions; script reads Command), so this is a permissive union
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
	// Command is the command line a script-harness cell runs (script). It is
	// run by the cell's shell, so it references environment variables as
	// $VAR, never ${VAR}.
	Command string `json:"command,omitempty"     yaml:"command,omitempty"`
	// Secrets are the secret NAMES (resolved against the TeamDefaults spec.secrets) this
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
//   - Params: parameters supplied at materialization time. An entry is a bare
//     name or a {name, default} mapping (see RoleParam).
//   - Secrets: secret names resolved against the TeamDefaults spec.secrets.
type RoleNeeds struct {
	Image   []string    `json:"image,omitempty"   yaml:"image,omitempty"`
	Repos   []string    `json:"repos,omitempty"   yaml:"repos,omitempty"`
	Mounts  []string    `json:"mounts,omitempty"  yaml:"mounts,omitempty"`
	Params  []RoleParam `json:"params,omitempty"  yaml:"params,omitempty"`
	Secrets []string    `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// RoleParam is one `needs.params` entry: a bare parameter name, which the
// operator must supply in the TeamDefaults spec.parameters unless kukeon already
// knows it, or a mapping that also carries the role's default:
//
//	params:
//	  - PROMPT
//	  - { name: FEEDBACK_REPO, default: "${ROLE_SOURCE_REPO}" }
//
// The default may reference composed facts as ${FACT}; a value in
// the TeamDefaults spec.parameters overrides it.
type RoleParam struct {
	// Name is the parameter name.
	Name string `json:"name"              yaml:"name"`
	// Default is the role's value for the parameter, used when the operator
	// supplies none. Nil means the role declares no default.
	Default *string `json:"default,omitempty" yaml:"default,omitempty"`
}

// roleParamFields is RoleParam without its marshaling methods.
type roleParamFields RoleParam

// UnmarshalYAML accepts a bare name or a {name, default} mapping.
func (p *RoleParam) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*p = RoleParam{Name: node.Value}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return errors.New("needs.params entry: require a name or a {name, default} mapping")
	}
	// node.Decode does not inherit the document decoder's KnownFields, so
	// the mapping's keys are checked here.
	for i := 0; i < len(node.Content); i += 2 {
		if key := node.Content[i].Value; key != "name" && key != "default" {
			return fmt.Errorf("needs.params entry: unknown field %q", key)
		}
	}
	var fields roleParamFields
	if err := node.Decode(&fields); err != nil {
		return fmt.Errorf("needs.params entry: %w", err)
	}
	*p = RoleParam(fields)
	return nil
}

// MarshalYAML writes an entry with no default as the bare name, so a role
// authored with names round-trips unchanged.
func (p RoleParam) MarshalYAML() (any, error) {
	if p.Default == nil {
		return p.Name, nil
	}
	return roleParamFields(p), nil
}

// UnmarshalJSON accepts a bare name or a {name, default} object.
func (p *RoleParam) UnmarshalJSON(raw []byte) error {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		*p = RoleParam{Name: name}
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var fields roleParamFields
	if err := d.Decode(&fields); err != nil {
		return fmt.Errorf("needs.params entry: %w", err)
	}
	*p = RoleParam(fields)
	return nil
}

// MarshalJSON writes an entry with no default as the bare name.
func (p RoleParam) MarshalJSON() ([]byte, error) {
	if p.Default == nil {
		return json.Marshal(p.Name)
	}
	return json.Marshal(roleParamFields(p))
}

// ParamNames lists the names of the role's needs.params entries.
func (n RoleNeeds) ParamNames() []string {
	out := make([]string, 0, len(n.Params))
	for _, p := range n.Params {
		out = append(out, p.Name)
	}
	return out
}
