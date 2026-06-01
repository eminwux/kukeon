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
// A Blueprint declares two fill channels (see #423 "L1↔L2 interface"):
//
//  1. Scalar parameters — `${KEY}` substitution using the CellBlueprintParameter
//     shape. Filled inline by `kuke run -b --param K=V`.
//  2. Structural slots — named repo/secret slots on each container that a
//     CellConfig fills with structured values (repo URLs, secret sources).
//     This kind ships the slot *declarations* only; the Config-side fill
//     machinery lands in #624.
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
type CellBlueprintSpec struct {
	Prefix     string                   `json:"prefix,omitempty"     yaml:"prefix,omitempty"`
	Parameters []CellBlueprintParameter `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Cell       BlueprintCellSpec        `json:"cell"                 yaml:"cell"`
}

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
	Ports                  []string               `json:"ports,omitempty"                  yaml:"ports,omitempty"`
	Volumes                []VolumeMount          `json:"volumes,omitempty"                yaml:"volumes,omitempty"`
	Networks               []string               `json:"networks,omitempty"               yaml:"networks,omitempty"`
	NetworksAliases        []string               `json:"networksAliases,omitempty"        yaml:"networksAliases,omitempty"`
	Privileged             bool                   `json:"privileged,omitempty"             yaml:"privileged,omitempty"`
	HostNetwork            bool                   `json:"hostNetwork,omitempty"            yaml:"hostNetwork,omitempty"`
	HostPID                bool                   `json:"hostPID,omitempty"                yaml:"hostPID,omitempty"`
	HostCgroup             bool                   `json:"hostCgroup,omitempty"             yaml:"hostCgroup,omitempty"`
	User                   string                 `json:"user,omitempty"                   yaml:"user,omitempty"`
	ReadOnlyRootFilesystem bool                   `json:"readOnlyRootFilesystem,omitempty" yaml:"readOnlyRootFilesystem,omitempty"`
	Capabilities           *ContainerCapabilities `json:"capabilities,omitempty"           yaml:"capabilities,omitempty"`
	SecurityOpts           []string               `json:"securityOpts,omitempty"           yaml:"securityOpts,omitempty"`
	Tmpfs                  []ContainerTmpfsMount  `json:"tmpfs,omitempty"                  yaml:"tmpfs,omitempty"`
	Resources              *ContainerResources    `json:"resources,omitempty"              yaml:"resources,omitempty"`
	Repos                  []ContainerRepo        `json:"repos,omitempty"                  yaml:"repos,omitempty"`
	Git                    *ContainerGit          `json:"git,omitempty"                    yaml:"git,omitempty"`
	RestartPolicy          string                 `json:"restartPolicy,omitempty"          yaml:"restartPolicy,omitempty"`
	Attachable             bool                   `json:"attachable,omitempty"             yaml:"attachable,omitempty"`
	Tty                    *ContainerTty          `json:"tty,omitempty"                    yaml:"tty,omitempty"`
	Secrets                []BlueprintSecretSlot  `json:"secrets,omitempty"                yaml:"secrets,omitempty"`
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
