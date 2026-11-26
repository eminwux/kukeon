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

// CellBlueprintDoc is a daemon-stored, scopable parametrized cell template
// (kind: CellBlueprint, issue #620, phase 4a-i of #423). The client-side
// CellProfile kind that originally co-existed with it was removed in #626 —
// `kuke apply` is now the single entry point and `kuke run -b` / `kuke run -c`
// are the run-time consumers.
//
// A Blueprint declares the fill channels a CellConfig answers (see #423
// "L1↔L2 interface", extended by #1472):
//  1. Scalar parameters — typed `${KEY}` substitution using the
//     CellBlueprintParameter shape. Filled inline by `kuke run -b --param K=V`
//     or by a CellConfig's values.
//  2. Structural slots — named repo/secret slots on each container that a
//     CellConfig fills with structured values (repo URLs, secret sources).
//  3. Components — optional bundles a CellConfig enables by name.
//  4. Harness modes — the start mode picked per cell at create time.
//
// What a blueprint never carries is identity: git author, credentials and
// signing keys come from Realm/Space/Stack defaults, the CellConfig and its
// repo fills.
type CellBlueprintDoc struct {
	APIVersion Version               `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind                  `json:"kind"       yaml:"kind"`
	Metadata   CellBlueprintMetadata `json:"metadata"   yaml:"metadata"`
	Spec       CellBlueprintSpec     `json:"spec"       yaml:"spec"`
}

// CellBlueprintMetadata identifies a Blueprint by name and the scope it is
// bound to. Unlike a Secret, a Blueprint is scopable at realm, space, or
// stack only — never cell: a template scoped to a single cell is nonsensical
// (#423). The scope is the deepest non-empty coordinate; a deeper coordinate
// requires every shallower one (a stack-scoped Blueprint must also name its
// space and realm). Realm is always required.
type CellBlueprintMetadata struct {
	// Name is the blueprint's name, unique within its scope.
	Name string `json:"name"             yaml:"name"`
	// Realm is the always-required top-level scope coordinate.
	Realm string `json:"realm"            yaml:"realm"`
	// Space, when set, scopes the blueprint to a space within Realm.
	Space string `json:"space,omitempty"  yaml:"space,omitempty"`
	// Stack, when set, scopes the blueprint to a stack within Space.
	Stack string `json:"stack,omitempty"  yaml:"stack,omitempty"`
	// Labels are copied onto every cell materialized from this blueprint, in
	// addition to the kukeon.io/blueprint back-reference label.
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// CellBlueprintSpec carries the scalar parameter declarations plus the cell
// template body. Prefix overrides the cell-name prefix used when generating
// the `<prefix>-<6hex>` name on each `kuke run -b`; when unset it defaults to
// metadata.name. Every run produces a fresh hex-suffixed cell — the
// "Blueprint = always fresh" invariant.
//
// A blueprint is a form and a CellConfig is the answers (issue #1472): typed
// Parameters, repo and secret slots, optional Components and harness modes are
// everything a consumer — `kuke team` or the web UI — needs to build a Config
// for it without knowing the template body.
type CellBlueprintSpec struct {
	Prefix     string                   `json:"prefix,omitempty"     yaml:"prefix,omitempty"`
	Parameters []CellBlueprintParameter `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	// Requires lists the image capabilities every cell stamped from this
	// blueprint needs (e.g. [claude, git]). The full requirement of a cell is
	// this list ∪ the requires of its enabled components ∪ the CellConfig's
	// own requires; the daemon checks it against each container image's
	// io.kukeon.capabilities label at create.
	Requires []string `json:"requires,omitempty"   yaml:"requires,omitempty"`
	// Components bundle the parameters, secrets, env and requires of one
	// concern — a harness, a connector, a forge — so a CellConfig can switch
	// an optional one on by name. The daemon flattens the enabled components
	// into Cell at render time.
	Components []BlueprintComponent `json:"components,omitempty" yaml:"components,omitempty"`
	Cell       BlueprintCellSpec    `json:"cell"                 yaml:"cell"`
}

// Parameter types (CellBlueprintParameter.Type). An untyped parameter is a
// string, so blueprints written before types existed stay valid.
const (
	// ParameterTypeString accepts any value. The default.
	ParameterTypeString = "string"
	// ParameterTypeEnum accepts one of the values listed in Enum.
	ParameterTypeEnum = "enum"
	// ParameterTypePath accepts a filesystem path (absolute or relative).
	ParameterTypePath = "path"
	// ParameterTypeImage accepts a container image reference or a realm
	// image name.
	ParameterTypeImage = "image"
	// ParameterTypeBool accepts "true" or "false".
	ParameterTypeBool = "bool"
)

// CellBlueprintParameter declares one `${KEY}` substitution variable used by a
// CellBlueprint's body. Default is a pointer so YAML/JSON can distinguish "no
// default" (nil) from an explicit empty default (""). The substitution engine
// treats them differently: a missing default falls through to the env-var
// lookup, while an explicit empty default short-circuits there.
type CellBlueprintParameter struct {
	Name        string  `json:"name"                  yaml:"name"`
	Description string  `json:"description,omitempty" yaml:"description,omitempty"`
	Default     *string `json:"default,omitempty"     yaml:"default,omitempty"`
	Required    bool    `json:"required,omitempty"    yaml:"required,omitempty"`
	// Type is one of string (default when empty), enum, path, image, bool. A
	// resolved value that does not fit the type is rejected at render.
	Type string `json:"type,omitempty"        yaml:"type,omitempty"`
	// Enum lists the accepted values of an enum parameter.
	Enum []string `json:"enum,omitempty"        yaml:"enum,omitempty"`
	// Group labels the form section a consumer shows the parameter under
	// (e.g. "role", "harness"). Presentation only.
	Group string `json:"group,omitempty"       yaml:"group,omitempty"`
}

// Component kinds.
const (
	// ComponentKindHarness is the agent harness: its env, parameters, secrets
	// and the modes it can be started in.
	ComponentKindHarness = "harness"
	// ComponentKindConnector connects the cell to an external service through
	// that service's API credentials (e.g. the GitHub token for `gh`). A
	// connector never contributes a git credential.
	ComponentKindConnector = "connector"
	// ComponentKindForge binds the cell to an issue/change tracker.
	ComponentKindForge = "forge"
)

// Well-known harness mode names.
const (
	// ModeInteractive is an attachable TTY session.
	ModeInteractive = "interactive"
	// ModeHeadless is a dispatched, unattended run.
	ModeHeadless = "headless"
)

// BlueprintComponent is one concern a blueprint is assembled from. A component
// that is not Optional is always part of the cell; an Optional one is included
// only when the CellConfig lists it in spec.components. Its Parameters join the
// blueprint's, and its Env and Secrets are added to one container. Two enabled
// components (or a component and the template) that set the same env key or
// the same secret destination fail the render.
type BlueprintComponent struct {
	// Kind is harness, connector or forge. Required.
	Kind string `json:"kind"                  yaml:"kind"`
	// Name identifies the component within the blueprint; a CellConfig
	// enables an optional component by this name. Required.
	Name        string `json:"name"                  yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Optional marks a component a CellConfig must enable explicitly.
	Optional bool `json:"optional,omitempty"    yaml:"optional,omitempty"`
	// Group names a set of mutually exclusive components (for example
	// "claude-auth": the ways a claude cell authenticates). A CellConfig that
	// enables two components of one group is rejected (issue #1548).
	Group string `json:"group,omitempty"       yaml:"group,omitempty"`
	// Connector is the connector definition this component delivers, copied
	// from the catalog when the blueprint is rendered. Connector components
	// only.
	Connector *BlueprintConnector `json:"connector,omitempty"   yaml:"connector,omitempty"`
	// Container is the id of the container the Env, Secrets and mode onInit
	// land on. Empty selects the cell's default attach container
	// (spec.cell.tty.default), else the first attachable container, else the
	// first non-root container.
	Container string `json:"container,omitempty"   yaml:"container,omitempty"`
	// Requires lists the image capabilities the component needs.
	Requires   []string                 `json:"requires,omitempty"    yaml:"requires,omitempty"`
	Parameters []CellBlueprintParameter `json:"parameters,omitempty"  yaml:"parameters,omitempty"`
	// Env are `KEY=value` entries added to the container; values may
	// reference `${PARAM}`s.
	Env []string `json:"env,omitempty"         yaml:"env,omitempty"`
	// Secrets are secret slots added to the container; a CellConfig fills
	// them by name like any other slot.
	Secrets []BlueprintSecretSlot `json:"secrets,omitempty"     yaml:"secrets,omitempty"`
	// Modes are the ways a harness can be started, keyed by mode name
	// ("interactive", "headless", …). Harness components only; at most one
	// component of a blueprint may declare modes. The mode is picked per
	// cell at create time.
	Modes map[string]BlueprintComponentMode `json:"modes,omitempty"       yaml:"modes,omitempty"`
}

// BlueprintComponentMode is one way to start a harness. Everything it declares
// applies only to cells created in that mode — in particular a required
// parameter or secret is only required then.
type BlueprintComponentMode struct {
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// OnInit replaces the container's tty.onInit stages for this mode.
	OnInit     []TtyStage               `json:"onInit,omitempty"      yaml:"onInit,omitempty"`
	Parameters []CellBlueprintParameter `json:"parameters,omitempty"  yaml:"parameters,omitempty"`
	Env        []string                 `json:"env,omitempty"         yaml:"env,omitempty"`
	Secrets    []BlueprintSecretSlot    `json:"secrets,omitempty"     yaml:"secrets,omitempty"`
}

// CellProfileParameter is the pre-#986 name for CellBlueprintParameter, kept as
// a deprecated alias for one release so sibling projects (sbsh, sbcrew, …) can
// rename on their own cadence. The on-wire YAML/JSON shape is unchanged.
//
// Deprecated: use CellBlueprintParameter. Scheduled for removal in v0.7.0.
type CellProfileParameter = CellBlueprintParameter

// BlueprintCellSpec is the cell template body of a CellBlueprint. It mirrors
// the runtime CellSpec's user-authorable surface but is a deliberately
// decoupled type: its containers carry structural slot declarations
// (BlueprintContainer.Secrets) whose shape differs from the runtime
// ContainerSpec, and it omits the daemon-stamped identity fields (id, realmId,
// spaceId, stackId) that materialization fills from the blueprint's metadata
// and the generated cell name. Materialization (internal/cellblueprint) maps
// it to a CellSpec.
type BlueprintCellSpec struct {
	Tty                 *CellTty             `json:"tty,omitempty"                 yaml:"tty,omitempty"`
	Containers          []BlueprintContainer `json:"containers"                    yaml:"containers"`
	AutoDelete          bool                 `json:"autoDelete,omitempty"          yaml:"autoDelete,omitempty"`
	NestedCgroupRuntime bool                 `json:"nestedCgroupRuntime,omitempty" yaml:"nestedCgroupRuntime,omitempty"`
}

// BlueprintContainer is one container in a blueprint's cell template. It
// carries the user-authorable subset of ContainerSpec verbatim (so a blueprint
// can express the same workload a hand-written Cell can), plus two structural
// slot channels:
//
//   - Repos reuses the runtime ContainerRepo shape, but unlike `kuke apply`'s
//     Cell/Container path the url is *not* required at apply time: a repo with
//     no url is a structural slot a CellConfig fills (#624). A repo whose url
//     is supplied inline (directly or via a `${PARAM}`) runs as-is under
//     `kuke run -b`.
//   - Secrets is a slot-only channel (BlueprintSecretSlot): the blueprint
//     declares the consumption side (env vs file, the env var / mount path);
//     the source side (which kind: Secret provides the bytes) is filled by a
//     CellConfig (#624). Because the source is never part of a blueprint, a
//     blueprint that declares secret slots cannot be run inline with `-b` —
//     it requires `kuke run -c` (#625).
//
// Daemon-stamped / runtime-resolved fields (containerdId, realmId, spaceId,
// stackId, cellId, kukeonGroupGID) are intentionally absent: materialization
// and the runner fill them.
type BlueprintContainer struct {
	ID                     string                 `json:"id"                               yaml:"id"`
	Root                   bool                   `json:"root,omitempty"                   yaml:"root,omitempty"`
	Image                  string                 `json:"image"                            yaml:"image"`
	Command                string                 `json:"command,omitempty"                yaml:"command,omitempty"`
	Args                   []string               `json:"args,omitempty"                   yaml:"args,omitempty"`
	WorkingDir             string                 `json:"workingDir,omitempty"             yaml:"workingDir,omitempty"`
	Env                    []string               `json:"env,omitempty"                    yaml:"env,omitempty"`
	Volumes                []VolumeMount          `json:"volumes,omitempty"                yaml:"volumes,omitempty"`
	Privileged             bool                   `json:"privileged,omitempty"             yaml:"privileged,omitempty"`
	HostNetwork            bool                   `json:"hostNetwork,omitempty"            yaml:"hostNetwork,omitempty"`
	HostPID                bool                   `json:"hostPID,omitempty"                yaml:"hostPID,omitempty"`
	HostCgroup             bool                   `json:"hostCgroup,omitempty"             yaml:"hostCgroup,omitempty"`
	User                   string                 `json:"user,omitempty"                   yaml:"user,omitempty"`
	ReadOnlyRootFilesystem bool                   `json:"readOnlyRootFilesystem,omitempty" yaml:"readOnlyRootFilesystem,omitempty"`
	Capabilities           *ContainerCapabilities `json:"capabilities,omitempty"           yaml:"capabilities,omitempty"`
	SecurityOpts           []string               `json:"securityOpts,omitempty"           yaml:"securityOpts,omitempty"`
	// Devices grants per-host-device passthrough (short form, e.g. "/dev/kvm")
	// — the least-privilege alternative to Privileged. Mirrors
	// ContainerSpec.Devices; see that field for semantics. Issue #1252.
	Devices       []string              `json:"devices,omitempty"                yaml:"devices,omitempty"`
	Tmpfs         []ContainerTmpfsMount `json:"tmpfs,omitempty"                  yaml:"tmpfs,omitempty"`
	Resources     *ContainerResources   `json:"resources,omitempty"              yaml:"resources,omitempty"`
	Repos         []ContainerRepo       `json:"repos,omitempty"                  yaml:"repos,omitempty"`
	Git           *ContainerGit         `json:"git,omitempty"                    yaml:"git,omitempty"`
	RestartPolicy string                `json:"restartPolicy,omitempty"          yaml:"restartPolicy,omitempty"`
	Attachable    bool                  `json:"attachable,omitempty"             yaml:"attachable,omitempty"`
	Tty           *ContainerTty         `json:"tty,omitempty"                    yaml:"tty,omitempty"`
	Secrets       []BlueprintSecretSlot `json:"secrets,omitempty"                yaml:"secrets,omitempty"`
}

// BlueprintSecretSlot is a structural secret slot declared on a blueprint
// container. It is the consumption side of the two-channel interface (#423):
// the blueprint owns where the secret lands inside the container (env var or
// file mount), and a CellConfig owns the source side (which kind: Secret
// provides the bytes, #624). Name is the slot identity a CellConfig matches
// against; it is independent of the env var name so a Config can fill the same
// slot regardless of how the container consumes it.
type BlueprintSecretSlot struct {
	// Name is the slot's identity, unique within the container. A CellConfig
	// fills the slot by this name (#624). Required.
	Name string `json:"name"                yaml:"name"`
	// Mode selects how the resolved secret is injected: "env" (default) sets
	// an environment variable named EnvName; "file" stages a read-only file
	// mount at MountPath.
	Mode string `json:"mode,omitempty"      yaml:"mode,omitempty"`
	// EnvName is the environment variable name for Mode "env". Required when
	// Mode is "env".
	EnvName string `json:"envName,omitempty"   yaml:"envName,omitempty"`
	// MountPath is the absolute in-container path for Mode "file". Required
	// when Mode is "file".
	MountPath string `json:"mountPath,omitempty" yaml:"mountPath,omitempty"`
	// Required gates whether a CellConfig must fill this slot.
	Required bool `json:"required,omitempty"  yaml:"required,omitempty"`
}

// Secret-slot modes.
const (
	// BlueprintSecretModeEnv injects the resolved secret as an environment
	// variable named EnvName. The default when Mode is empty.
	BlueprintSecretModeEnv = "env"
	// BlueprintSecretModeFile stages the resolved secret as a read-only file
	// mount at MountPath.
	BlueprintSecretModeFile = "file"
)
