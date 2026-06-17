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

import "time"

type ContainerDoc struct {
	APIVersion Version           `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind              `json:"kind"       yaml:"kind"`
	Metadata   ContainerMetadata `json:"metadata"   yaml:"metadata"`
	Spec       ContainerSpec     `json:"spec"       yaml:"spec"`
	Status     ContainerStatus   `json:"status"     yaml:"status"`
}

type ContainerMetadata struct {
	Name   string            `json:"name"   yaml:"name"`
	Labels map[string]string `json:"labels" yaml:"labels"`
}

type ContainerSpec struct {
	ID           string   `json:"id"                               yaml:"id"`
	ContainerdID string   `json:"containerdId,omitempty"           yaml:"containerdId,omitempty"`
	RealmID      string   `json:"realmId"                          yaml:"realmId"`
	SpaceID      string   `json:"spaceId"                          yaml:"spaceId"`
	StackID      string   `json:"stackId"                          yaml:"stackId"`
	CellID       string   `json:"cellId"                           yaml:"cellId"`
	Root         bool     `json:"root,omitempty"                   yaml:"root,omitempty"`
	Image        string   `json:"image"                            yaml:"image"`
	Command      string   `json:"command"                          yaml:"command"`
	Args         []string `json:"args"                             yaml:"args"`
	// WorkingDir sets the cwd of the spawned container process — OCI
	// process.cwd, Docker WORKDIR, K8s Container.workingDir. Empty falls
	// back to the image's WORKDIR (no behavior change for existing specs).
	WorkingDir      string        `json:"workingDir,omitempty"             yaml:"workingDir,omitempty"`
	Env             []string      `json:"env"                              yaml:"env"`
	Ports           []string      `json:"ports"                            yaml:"ports"`
	Volumes         []VolumeMount `json:"volumes"                          yaml:"volumes"`
	Networks        []string      `json:"networks"                         yaml:"networks"`
	NetworksAliases []string      `json:"networksAliases"                  yaml:"networksAliases"`
	Privileged      bool          `json:"privileged"                       yaml:"privileged"`
	// HostNetwork opts the container into the host's network namespace.
	// When true, the runner omits the network LinuxNamespace from the OCI
	// spec (containerd's WithHostNamespace) and does not invoke CNI attach,
	// since a host-netns container has no per-container veth to wire up.
	// Used by the kukeond bootstrap so daemon-installed bridges, veths, and
	// iptables rules land in host scope where kubelet-style CNI plumbing
	// belongs. Default false — no behavior change for existing specs.
	HostNetwork bool `json:"hostNetwork,omitempty"            yaml:"hostNetwork,omitempty"`
	// HostPID opts the container into the host's PID namespace. When true,
	// the runner omits the PID LinuxNamespace from the OCI spec so /proc
	// inside the container reflects host PIDs. Used by the kukeond bootstrap
	// so the CNI bridge plugin running inside the daemon can resolve the
	// host PIDs containerd returns from task.Pid() — without this, attaching
	// user cells to a network fails with `Statfs /proc/<host-pid>/ns/net:
	// no such file or directory`. Default false — no behavior change for
	// existing specs.
	HostPID bool `json:"hostPID,omitempty"                yaml:"hostPID,omitempty"`
	// HostCgroup, when true, opts the container into its parent's cgroup
	// namespace. Default false unshares the cgroup-ns: the container sees
	// its own cgroup as / and any nested runtime (containerd, runc,
	// dockerd, kuke init) writes its cgroup tree under the cell — which
	// is the property that lets a nested kuke init complete the runc
	// task-create step that otherwise trips the "cgroup not empty"
	// precheck.
	//
	// Set true only for kukeond-style cells that need to write cgroups
	// *outside* their own subtree to manage user workloads. For ordinary
	// workload containers, leave false.
	//
	// Translates to omitting the LinuxNamespace{Type: cgroup} entry from
	// the OCI spec when true; appending it when false.
	HostCgroup             bool                   `json:"hostCgroup,omitempty"             yaml:"hostCgroup,omitempty"`
	User                   string                 `json:"user,omitempty"                   yaml:"user,omitempty"`
	ReadOnlyRootFilesystem bool                   `json:"readOnlyRootFilesystem,omitempty" yaml:"readOnlyRootFilesystem,omitempty"`
	Capabilities           *ContainerCapabilities `json:"capabilities,omitempty"           yaml:"capabilities,omitempty"`
	SecurityOpts           []string               `json:"securityOpts,omitempty"           yaml:"securityOpts,omitempty"`
	// Devices grants the container access to individual host device nodes —
	// the least-privilege alternative to Privileged (which exposes every host
	// device). Each entry is a host device path (short form, e.g. "/dev/kvm");
	// the node is replicated into the container at the same path with default
	// "rwm" cgroup access. The host node is stat'd at container *create* time
	// (type/major/minor snapshot) and materialises as a Linux.Devices entry
	// plus a matching Linux.Resources.Devices allow rule — the same pair
	// Docker's --device emits. A device that appears on the host after the cell
	// is created needs a cell recreate to be picked up; a missing host node
	// fails container create with a clear error. Issue #1252.
	Devices   []string              `json:"devices,omitempty"                yaml:"devices,omitempty"`
	Tmpfs     []ContainerTmpfsMount `json:"tmpfs,omitempty"                  yaml:"tmpfs,omitempty"`
	Resources *ContainerResources   `json:"resources,omitempty"              yaml:"resources,omitempty"`
	Secrets   []ContainerSecret     `json:"secrets,omitempty"                yaml:"secrets,omitempty"`
	// Repos declares git repositories the container depends on. The kuketty
	// wrapper clones (or fetches) each one in a pre-Serve step using the
	// container's own git identity (~/.ssh, ~/.gitconfig, GIT_SSH_COMMAND),
	// so the daemon never touches user-owned SSH keys. kuketty reads them
	// straight from this ContainerDoc.Spec (it owns the spec→TerminalSpec
	// build since issue #641) — there is no sidecar doc. Has no effect unless
	// Attachable=true (kuketty owns the resolution step). Per-repo outcome
	// surfaces in ContainerStatus.Repos over RPC in phase 1b (#642). Issue
	// #617, phase 1a of #423.
	Repos []ContainerRepo `json:"repos,omitempty"                  yaml:"repos,omitempty"`
	// Git declares the container's git identity and signing config as
	// declarative sugar over the GIT_AUTHOR_* / GIT_COMMITTER_* / GIT_CONFIG_*
	// environment-variable protocol git reads natively. The container runtime
	// expands it into that env-var block before container start (merged with
	// any explicit env: entries, which win on key collision), so cell
	// templates carry a four-line git: block instead of the hand-rolled
	// ~13-line GIT_* env duplication. The signingKey path stays per-container
	// (root-cell vs non-root-cell key paths are not globalised). Issue #618,
	// phase 2 of #423.
	Git           *ContainerGit `json:"git,omitempty"                    yaml:"git,omitempty"`
	CNIConfigPath string        `json:"cniConfigPath,omitempty"          yaml:"cniConfigPath,omitempty"`
	RestartPolicy string        `json:"restartPolicy"                    yaml:"restartPolicy"`
	// RestartBackoffSeconds is the minimum wall-clock interval, in seconds,
	// between successive reconciler-driven restarts of this non-root container
	// (#1235 parameterizes the hardcoded floor #1233 introduced). Nil/unset
	// falls back to the runner's built-in default (30s) — existing specs see no
	// behavior change. An explicit 0 disables the floor so a restart fires on
	// the next reconcile tick that observes the exit. Only meaningful under a
	// restarting policy (`always` or `on-failure`); setting it with `never` /
	// empty is a validation error since it can never take effect. Validation
	// also rejects a negative value.
	RestartBackoffSeconds *int64 `json:"restartBackoffSeconds,omitempty"  yaml:"restartBackoffSeconds,omitempty"`
	// RestartMaxRetries caps how many times the reconciler relaunches this
	// container under the `on-failure` policy before giving up and leaving it
	// terminal for the operator (#1235 parameterizes the hardcoded cap #1233
	// introduced). Nil/unset falls back to the runner's built-in default (5) —
	// existing specs see no behavior change. The counter resets whenever the
	// container is next observed running, so the cap bites a tight crash loop,
	// not a workload that runs for a while between crashes. `always` is uncapped
	// by contract, so this field is only meaningful under `on-failure`; setting
	// it with any other policy is a validation error. Validation rejects a value
	// below 1.
	RestartMaxRetries *int64 `json:"restartMaxRetries,omitempty"      yaml:"restartMaxRetries,omitempty"`
	// Attachable opts the container into kuketty-wrapper injection. When
	// true, the daemon rewrites process.args to a single element
	// [/.kukeon/bin/kuketty] — no CLI flags, every runtime input flows
	// through the bind-mounted metadata file — bind-mounts the kuketty
	// binary read-only at /.kukeon/bin/kuketty, bind-mounts a per-container
	// tty directory at /run/kukeon/tty (kuketty owns the attach socket
	// inside it; capture and log files land in later phases), and
	// bind-mounts the per-container metadata file read-only at
	// /.kukeon/kuketty/metadata.json (carries this ContainerDoc with the
	// resolved workload argv baked into Spec.Command / Spec.Args, from which
	// kuketty builds the sbsh TerminalSpec it serves — issue #641). The
	// host-visible peer of the tty directory lives in the per-container
	// metadata dir and its `socket` entry is what `kuke attach` connects
	// to. Default false — no behavior change for existing specs.
	Attachable bool `json:"attachable,omitempty"             yaml:"attachable,omitempty"`
	// Tty configures shell-UX (prompt, init scripts) for the kuketty
	// wrapper when Attachable=true. The container model already owns
	// command, args, workingDir, and env, so Tty intentionally only adds
	// layers the container model can't express. Setting any tty field with
	// Attachable=false is a validation error.
	Tty *ContainerTty `json:"tty,omitempty"                    yaml:"tty,omitempty"`
	// KukeonGroupGID is a daemon-stamped transport field, not user-authored
	// config. It carries the resolved kukeon-group GID into the ContainerDoc
	// the daemon mounts at /.kukeon/kuketty/metadata.json so kuketty can apply
	// the kukeon-group ownership (socket / capture / log GID + mode) the daemon
	// used to fold into the rendered TerminalSpec — a value not knowable from
	// inside the container. Zero means no kukeon group is configured (kuketty
	// then leaves OS-default permissions on the inodes it creates, matching the
	// no-group fallback). Always zero on the persisted ContainerDoc and on
	// `kuke get` output (omitempty): the daemon populates it only on the
	// kuketty-mounted doc, and the read path never round-trips it back into the
	// internal model. Issue #641.
	KukeonGroupGID int `json:"kukeonGroupGID,omitempty"         yaml:"kukeonGroupGID,omitempty"`
}

// ContainerTty carries per-attach shell-UX config that the daemon threads
// into kuketty on first attach. Has no effect unless Attachable=true.
//
// All fields are stamped directly onto the rendered sbsh TerminalSpec via
// sbsh's inline builder lane (sbsh v0.11.2+, kukeon #494). The pre-#494
// Profile / ProfilesDir fields that pointed at on-disk TerminalProfile YAML
// have been removed; cell YAML that still carries those keys is silently
// ignored by the default YAML decoder.
type ContainerTty struct {
	// Prompt is the literal prompt expression sbsh stamps onto
	// TerminalSpec.Prompt and flips SetPrompt on for. Empty leaves
	// SetPrompt off (sbsh's wrapper skips PS1 injection).
	Prompt string `json:"prompt,omitempty"   yaml:"prompt,omitempty"`
	// OnInit are scripts run once when the wrapped shell starts, in
	// order. Forwarded to TerminalSpec.Stages.OnInit via sbsh's
	// WithOnInit; an empty slice leaves Stages.OnInit zero.
	OnInit []TtyStage `json:"onInit,omitempty"   yaml:"onInit,omitempty"`
	// LogFile is an optional operator override for the in-container path
	// the kuketty wrapper writes its slog output to. Empty (the default)
	// makes the daemon stamp ctr.AttachableKukettyLogPath
	// (/run/kukeon/tty/kuketty.log inside the bind mount — peer to the
	// capture file), which is always present after first attach. Set this
	// to a different in-container path when the cell needs the log to
	// land somewhere else (custom bind mount, fixed external mount). Mode
	// and GID are not user-configurable — the daemon applies its
	// AttachableLogFileMode and the kukeon-group GID, gated on the
	// kukeon group being configured (matches socket/capture treatment).
	// Issue #599.
	LogFile string `json:"logFile,omitempty"  yaml:"logFile,omitempty"`
	// LogLevel controls the verbosity of the kuketty wrapper's own slog
	// output. Accepted values: "debug", "info", "warn", "error". Empty
	// falls through to the daemon-wide kuketty.logLevel set on
	// ServerConfigurationSpec, which itself defaults to "info". The path
	// the log lands at is daemon-controlled (peer to capture inside the
	// per-container tty directory — see ctr.AttachableKukettyLogPath and
	// fs.ContainerKukettyLogPath); operators only pick the verbosity.
	// Validation rejects unknown values at apply time rather than
	// silently coercing. Issue #599.
	LogLevel string `json:"logLevel,omitempty" yaml:"logLevel,omitempty"`
}

// TtyStage is a single onInit script entry. Wrapped in a struct rather than
// a bare string so future stage knobs (timeout, etc.) can land without
// breaking the YAML shape.
type TtyStage struct {
	Script string `json:"script,omitempty" yaml:"script,omitempty"`
	// RunOn controls when the stage runs. Empty or "start" (RunOnStart) keeps
	// today's behavior: the script is forwarded to sbsh's Stages.OnInit and
	// runs in the wrapped shell on every boot. "create" (RunOnCreate) routes
	// the script into kuketty's pre-Serve executor, where it runs to completion
	// as a separate step instead of being handed to sbsh — the foundation for
	// run-once-per-cell-instance setup (the run-once render gate itself lands
	// in phase C, #690; this phase adds the field + routing + executor). Any
	// other value is rejected at apply time by validateContainerTty. Issue #635.
	RunOn string `json:"runOn,omitempty"  yaml:"runOn,omitempty"`
}

// TtyStage.RunOn values. Empty deserializes as RunOnStart.
const (
	// RunOnStart forwards the stage to sbsh's Stages.OnInit (in-shell, every
	// boot). The default when RunOn is empty.
	RunOnStart = "start"
	// RunOnCreate routes the stage into kuketty's pre-Serve executor: it runs
	// to completion before the workload starts and is never handed to sbsh.
	RunOnCreate = "create"
)

// IsEmpty reports whether the tty block carries no user-supplied config —
// i.e. equivalent to omitting the block entirely. Used by validation to
// distinguish "explicitly empty" from "any field set".
func (t *ContainerTty) IsEmpty() bool {
	if t == nil {
		return true
	}
	if t.Prompt != "" {
		return false
	}
	if t.LogFile != "" {
		return false
	}
	if t.LogLevel != "" {
		return false
	}
	for _, s := range t.OnInit {
		if s.Script != "" || s.RunOn != "" {
			return false
		}
	}
	return true
}

// ContainerSecret references a credential that the daemon resolves at apply
// time and injects into the container — either as an environment variable
// (default) or as a read-only file when MountPath is set. Only the reference
// is persisted; the resolved value is never written to status, metadata, or
// logs.
//
// Exactly one source must be set: FromFile (host-path reference), FromEnv
// (daemon-host env var), or SecretRef (a daemon-managed kind: Secret resolved
// from its scope's storage tree, issue #623). The env-vs-file dispatch is the
// same for all three: empty MountPath injects an env var, a set MountPath
// stages a read-only file mount.
type ContainerSecret struct {
	Name      string              `json:"name"                yaml:"name"`
	FromFile  string              `json:"fromFile,omitempty"  yaml:"fromFile,omitempty"`
	FromEnv   string              `json:"fromEnv,omitempty"   yaml:"fromEnv,omitempty"`
	SecretRef *ContainerSecretRef `json:"secretRef,omitempty" yaml:"secretRef,omitempty"`
	MountPath string              `json:"mountPath,omitempty" yaml:"mountPath,omitempty"`
}

// ContainerSecretRef points at a daemon-managed kind: Secret (issue #619) by
// name and scope. The scope follows the same coordinate contract as
// SecretMetadata: Realm is always required and a deeper coordinate may only be
// set when every shallower one is (a cell-scoped reference must also name its
// stack, space, and realm). The reference resolves to the same on-disk path
// the Secret's bytes were written to, so a container in one realm may
// reference a Secret owned by another (e.g. a workload in `default` reading a
// `kuke-system`-scoped token). Issue #623.
type ContainerSecretRef struct {
	// Name is the referenced Secret's name within its scope. Required.
	Name string `json:"name"            yaml:"name"`
	// Realm is the always-required top-level scope coordinate.
	Realm string `json:"realm"           yaml:"realm"`
	// Space, when set, scopes the reference to a space within Realm.
	Space string `json:"space,omitempty" yaml:"space,omitempty"`
	// Stack, when set, scopes the reference to a stack within Space.
	Stack string `json:"stack,omitempty" yaml:"stack,omitempty"`
	// Cell, when set, scopes the reference to a cell within Stack.
	Cell string `json:"cell,omitempty"  yaml:"cell,omitempty"`
}

// VolumeKind discriminates between the supported VolumeMount kinds. Mirrors
// intmodel.VolumeKind. An empty value deserializes as VolumeKindBind so YAML
// authored before the discriminator existed keeps its bind semantics.
type VolumeKind string

const (
	// VolumeKindBind is a host bind mount. Source and Target are required.
	VolumeKindBind VolumeKind = "bind"
	// VolumeKindTmpfs is an in-memory tmpfs mount. Only Target is required;
	// Source must be empty. SizeBytes and Mode tune the standard tmpfs
	// size= and mode= options when non-zero.
	VolumeKindTmpfs VolumeKind = "tmpfs"
	// VolumeKindVolume references a daemon-managed kind: Volume (issue #1018)
	// by name and bind-mounts its on-disk directory read-write at Target. The
	// reference is resolved at container-create time: a bare `source: <name>`
	// names a Volume in the container's own scope (resolved by walking
	// realm/space/stack, most-specific first); a `volumeRef:` block names one
	// cross-scope. The referenced Volume's directory survives both container
	// recreation and the mounting cell's deletion — the cell references the
	// Volume, it does not own it. Step 4 of the volumes epic (#1016).
	VolumeKindVolume VolumeKind = "volume"
)

// VolumeMount is a mount entry attached to a container. The Kind discriminator
// selects the OCI mount type the runtime emits: bind (host path → container
// path), tmpfs (in-memory directory), or volume (a reference to a daemon-
// managed kind: Volume resolved to its on-disk directory and bind-mounted).
// Empty Kind means bind for back-compat with YAML authored before the
// discriminator existed.
type VolumeMount struct {
	Kind VolumeKind `json:"kind,omitempty"      yaml:"kind,omitempty"`
	// Source is the host path for a bind mount, or — when Kind is volume — the
	// name of a Volume in the container's own scope (mutually exclusive with
	// VolumeRef). Empty for tmpfs.
	Source string `json:"source,omitempty"    yaml:"source,omitempty"`
	Target string `json:"target"              yaml:"target"`
	// VolumeRef references a kind: Volume in another scope by name + scope
	// coordinates (mirrors ContainerSecretRef, minus the cell coordinate a
	// Volume can never carry). Only honored when Kind is volume, and mutually
	// exclusive with Source. Step 4 (#1016).
	VolumeRef *VolumeRef `json:"volumeRef,omitempty" yaml:"volumeRef,omitempty"`
	ReadOnly  bool       `json:"readOnly,omitempty"  yaml:"readOnly,omitempty"`
	SizeBytes int64      `json:"sizeBytes,omitempty" yaml:"sizeBytes,omitempty"`
	Mode      uint32     `json:"mode,omitempty"      yaml:"mode,omitempty"`
	// Ensure, when true on a kind: volume mount, auto-provisions the referenced
	// Volume at cell create/start if it does not already exist — Docker's
	// "create on first reference" semantics (umbrella #1015), the opt-in
	// counterpart to step 4's default "missing volume is a hard error". It is
	// the provisioning half of the per-cell volume claim: materialization sets
	// Ensure on any volume mount whose source embeds the ${CELL_NAME} template
	// (cellblueprint.ExpandPerCellVolumes), since a per-cell Volume cannot be
	// pre-created for a not-yet-named cell. Auto-create is idempotent (an
	// already-bound cell re-binds its existing Volume, never minting a fresh
	// one) so reconcile and recreate preserve the Volume's contents. Step 5
	// (#1017).
	Ensure bool `json:"ensure,omitempty"    yaml:"ensure,omitempty"`
}

// VolumeRef points at a daemon-managed kind: Volume (issue #1018) by name and
// scope, the cross-scope companion to a bare `source: <name>` on a volume-kind
// VolumeMount. The scope follows the Volume coordinate contract: Realm is
// always required and a deeper coordinate may only be set when every shallower
// one is. Unlike ContainerSecretRef there is no Cell coordinate — a Volume is
// never cell-scoped. The reference resolves to the same on-disk directory the
// Volume was provisioned at, so a container in one scope may mount a Volume
// owned by another (e.g. a workload reading a realm-scoped shared volume).
// Step 4 (#1016).
type VolumeRef struct {
	// Name is the referenced Volume's name within its scope. Required.
	Name string `json:"name"            yaml:"name"`
	// Realm is the always-required top-level scope coordinate.
	Realm string `json:"realm"           yaml:"realm"`
	// Space, when set, scopes the reference to a space within Realm.
	Space string `json:"space,omitempty" yaml:"space,omitempty"`
	// Stack, when set, scopes the reference to a stack within Space.
	Stack string `json:"stack,omitempty" yaml:"stack,omitempty"`
}

// ContainerRepo declares a git repository the container depends on. kuketty
// clones (or fetches) it into Target before the workload starts, replacing the
// hand-rolled `if [ ! -d $DIR/.git ]; then git clone …; fi` blocks that team
// blueprint templates duplicate in onInit scripts today. The clone state
// becomes daemon-observable via ContainerStatus.Repos (reported over RPC in
// phase 1b, #642) rather than buried in attach stderr. Issue #617, phase 1a of
// #423.
type ContainerRepo struct {
	// Name is the operator-facing identifier for the repo, echoed back in
	// per-repo status. Required.
	Name string `json:"name"               yaml:"name"`
	// Target is the absolute in-container path the repo is cloned into.
	// Required.
	Target string `json:"target"             yaml:"target"`
	// Branch is the branch to check out (moving target). Empty clones the
	// remote's default branch. Mutually exclusive with Ref.
	Branch string `json:"branch,omitempty"   yaml:"branch,omitempty"`
	// Ref is an immutable pin — a tag name or full commit SHA. When set,
	// kuketty checks out a detached HEAD at the resolved ref and skips the
	// fast-forward step on subsequent restarts, so an in-place restart of
	// an already-cloned cell stays idempotent instead of failing on
	// `git pull` against a detached HEAD. Mutually exclusive with Branch.
	Ref string `json:"ref,omitempty"      yaml:"ref,omitempty"`
	// URL is the clone URL. In phases 1–3 it is supplied via scalar
	// ${PARAM} substitution; phase 4 (#423) enables structural slot fill
	// from a CellConfig. Required.
	URL string `json:"url"                yaml:"url"`
	// Required gates failure handling. When true, a clone/fetch failure
	// makes kuketty exit non-zero before sbsh starts, so the daemon
	// observes the container task as Failed. When false (the default) the
	// failure is logged but the container proceeds.
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`
}

// GitSignTarget enumerates the artefacts ContainerGit.Sign can enable signing
// for. An entry maps to the matching git config key (commit.gpgsign /
// tag.gpgsign) in the expanded GIT_CONFIG_* block.
const (
	// GitSignCommits enables commit signing (commit.gpgsign=true).
	GitSignCommits = "commits"
	// GitSignTags enables tag signing (tag.gpgsign=true).
	GitSignTags = "tags"
)

// ContainerGit is declarative sugar over the GIT_AUTHOR_* / GIT_COMMITTER_* /
// GIT_CONFIG_* env-var protocol git reads natively. The container runtime
// expands it into that env block before container start. Author/Committer
// render the GIT_AUTHOR_*/GIT_COMMITTER_* identity vars; SigningKey, Sign, and
// AllowedSigners render the GIT_CONFIG_* signing pairs (user.signingkey,
// gpg.format=ssh, commit.gpgsign, tag.gpgsign, gpg.ssh.allowedSignersFile)
// with GIT_CONFIG_COUNT tracking the live pair count. Issue #618.
type ContainerGit struct {
	// Author sets git's author identity (GIT_AUTHOR_NAME / GIT_AUTHOR_EMAIL).
	// Both name and email are required when present.
	Author *GitIdentity `json:"author,omitempty"         yaml:"author,omitempty"`
	// Committer sets git's committer identity (GIT_COMMITTER_NAME /
	// GIT_COMMITTER_EMAIL). Both name and email are required when present.
	Committer *GitIdentity `json:"committer,omitempty"      yaml:"committer,omitempty"`
	// SigningKey is the absolute in-container path to the signing key
	// (user.signingkey). kukeon signs with SSH keys, so a non-empty
	// SigningKey also renders gpg.format=ssh. Per-container — root-cell vs
	// non-root-cell key paths stay local to the container, not globalised.
	SigningKey string `json:"signingKey,omitempty"     yaml:"signingKey,omitempty"`
	// Sign enables signing for the listed artefacts: "commits"
	// (commit.gpgsign=true) and/or "tags" (tag.gpgsign=true). Requires
	// SigningKey to be set.
	Sign []string `json:"sign,omitempty"           yaml:"sign,omitempty"`
	// AllowedSigners is the absolute in-container path to git's SSH
	// allowed-signers file (gpg.ssh.allowedSignersFile), used to verify
	// signatures. Optional; rendered only when set. Team blueprint templates
	// set this today, so the field exists for the env block to be a strict
	// superset of what those templates render.
	AllowedSigners string `json:"allowedSigners,omitempty" yaml:"allowedSigners,omitempty"`
}

// GitIdentity is a name/email pair for a git author or committer identity.
type GitIdentity struct {
	Name  string `json:"name"  yaml:"name"`
	Email string `json:"email" yaml:"email"`
}

// ContainerCapabilities groups Linux capability deltas applied to the
// container process relative to the image default set.
type ContainerCapabilities struct {
	Drop []string `json:"drop,omitempty" yaml:"drop,omitempty"`
	Add  []string `json:"add,omitempty"  yaml:"add,omitempty"`
}

// ContainerTmpfsMount declares a tmpfs mount inside the container.
type ContainerTmpfsMount struct {
	Path      string   `json:"path"                yaml:"path"`
	SizeBytes int64    `json:"sizeBytes,omitempty" yaml:"sizeBytes,omitempty"`
	Options   []string `json:"options,omitempty"   yaml:"options,omitempty"`
}

// ContainerResources exposes the cgroup v2 knobs the orchestrator supports for
// per-container resource limits.
type ContainerResources struct {
	MemoryLimitBytes *int64 `json:"memoryLimitBytes,omitempty" yaml:"memoryLimitBytes,omitempty"`
	CPUShares        *int64 `json:"cpuShares,omitempty"        yaml:"cpuShares,omitempty"`
	PidsLimit        *int64 `json:"pidsLimit,omitempty"        yaml:"pidsLimit,omitempty"`
}

type ContainerStatus struct {
	Name  string         `json:"name"                yaml:"name"`
	ID    string         `json:"id"                  yaml:"id"`
	State ContainerState `json:"state"               yaml:"state"`
	// CreatedAt is the wall-clock time of the first time the controller
	// observed this container in cell.Spec.Containers. Stamped on the
	// first populateCellContainerStatuses pass and preserved across
	// reconciliations (mirrors realm/space/stack/cell.Status.CreatedAt).
	// Sources the AGE column on `kuke get container`. Issue #605.
	CreatedAt    time.Time `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
	RestartCount int       `json:"restartCount"        yaml:"restartCount"`
	RestartTime  time.Time `json:"restartTime"         yaml:"restartTime"`
	StartTime    time.Time `json:"startTime"           yaml:"startTime"`
	FinishTime   time.Time `json:"finishTime"          yaml:"finishTime"`
	ExitCode     int       `json:"exitCode"            yaml:"exitCode"`
	ExitSignal   string    `json:"exitSignal"          yaml:"exitSignal"`
	// Repos reports the per-repo outcome of kuketty's pre-Serve clone/fetch
	// step for an Attachable container's Spec.Repos. Empty for containers
	// with no repos[] or that have not yet been provisioned. Populated over
	// the GetSetupStatus RPC in phase 1b (#642); phase 1a lands the schema
	// only. Issue #617.
	Repos []RepoStatus `json:"repos,omitempty"     yaml:"repos,omitempty"`
	// Stages reports the per-stage outcome of kuketty's pre-Serve execution of
	// the container's runOn: create TtyStages, in declaration order. Empty for
	// containers with no create stages or that have not yet been provisioned.
	// Populated over the GetSetupStatus RPC in phase B (#689); this phase (#635)
	// lands the schema only. Issue #635.
	Stages []StageStatus `json:"stages,omitempty"    yaml:"stages,omitempty"`
}

// RepoStatus is the resolved state of a single ContainerRepo after kuketty's
// pre-Serve step. Populated in phase 1b (#642). Issue #617.
type RepoStatus struct {
	Name   string `json:"name"             yaml:"name"`
	Target string `json:"target"           yaml:"target"`
	// State is one of "cloned", "fetched", or "failed".
	State string `json:"state"            yaml:"state"`
	// Commit is the resolved HEAD commit (full SHA) on success.
	Commit string `json:"commit,omitempty" yaml:"commit,omitempty"`
	// Error is the failure detail when State == "failed".
	Error string `json:"error,omitempty"  yaml:"error,omitempty"`
}

// StageStatus is the resolved state of a single runOn: create TtyStage after
// kuketty's pre-Serve execution. Populated in phase B (#689). Phase C1 (#690)
// adds the durable Hash key + merge that carries done records across
// stop/start; phase C2 (#737) lands the render-time gate that consumes them.
// Issue #635.
type StageStatus struct {
	// Index is the 0-based position of the stage within the container's full
	// Tty.OnInit list (not its position among create stages alone), in
	// declaration order.
	Index int `json:"index"           yaml:"index"`
	// State is the resolved outcome ("done" or "failed"); the daemon-side
	// populator (phase B, #689) sets it from the wire payload.
	State string `json:"state"           yaml:"state"`
	// Error is the failure detail when State reports a failure.
	Error string `json:"error,omitempty" yaml:"error,omitempty"`
	// Hash is the content hash of the stage at record time — the run-once
	// "done" key. The daemon stamps it from the live spec and the controller
	// preserves a done entry across stop/start only when its Hash still
	// matches the current spec's stage Hash at the same Index, so an edited
	// stage (new content) drops its prior done record on the next populate.
	// Phase C1 (#690).
	Hash string `json:"hash,omitempty"  yaml:"hash,omitempty"`
}

type ContainerState int

const (
	ContainerStatePending ContainerState = iota
	ContainerStateReady
	ContainerStateStopped
	ContainerStatePaused
	ContainerStatePausing
	ContainerStateFailed
	ContainerStateUnknown
	// ContainerStateNotCreated marks a container with no containerd record at
	// all — distinct from Stopped (a record that exists but whose task is
	// gone). Appended last to keep the ordinals in lockstep with the internal
	// modelhub.ContainerState enum, which scheme.go converts by direct int cast.
	ContainerStateNotCreated
	// ContainerStateExited is a containerd-stopped task that exited 0 — a
	// clean completion (#1267). Mirrors the cell-level CellStateExited split:
	// ContainerStateStopped no longer conflates a clean exit with a crash.
	// Appended last to keep the ordinals in lockstep with the internal enum.
	ContainerStateExited
	// ContainerStateError is a containerd-stopped task that exited non-zero —
	// a workload crash (#1267). Distinct from ContainerStateFailed, which
	// stays reserved for kukeon's own container bring-up failures. Appended
	// last (after ContainerStateExited) for the same ordinal-lockstep reason.
	ContainerStateError
)

func (c *ContainerState) String() string {
	switch *c {
	case ContainerStatePending:
		return StatePendingStr
	case ContainerStateReady:
		return StateReadyStr
	case ContainerStateStopped:
		return StateStoppedStr
	case ContainerStatePaused:
		return StatePausedStr
	case ContainerStatePausing:
		return StatePausingStr
	case ContainerStateFailed:
		return StateFailedStr
	case ContainerStateUnknown:
		return StateUnknownStr
	case ContainerStateNotCreated:
		return StateNotCreatedStr
	case ContainerStateExited:
		return StateExitedStr
	case ContainerStateError:
		return StateErrorStr
	}
	return StateUnknownStr
}

// NewContainerDoc creates a ContainerDoc ensuring all nested structs are initialized.
func NewContainerDoc(from *ContainerDoc) *ContainerDoc {
	if from == nil {
		return &ContainerDoc{
			APIVersion: "",
			Kind:       "",
			Metadata: ContainerMetadata{
				Name:   "",
				Labels: map[string]string{},
			},
			Spec: ContainerSpec{
				ID:              "",
				ContainerdID:    "",
				RealmID:         "",
				SpaceID:         "",
				StackID:         "",
				CellID:          "",
				Root:            false,
				Image:           "",
				Command:         "",
				Args:            []string{},
				Env:             []string{},
				Ports:           []string{},
				Volumes:         []VolumeMount{},
				Networks:        []string{},
				NetworksAliases: []string{},
				Privileged:      false,
				HostNetwork:     false,
				HostPID:         false,
				CNIConfigPath:   "",
				RestartPolicy:   "",
			},
			Status: ContainerStatus{
				Name:         "",
				ID:           "",
				State:        ContainerStatePending,
				RestartCount: 0,
				RestartTime:  time.Time{},
				StartTime:    time.Time{},
				FinishTime:   time.Time{},
				ExitCode:     0,
				ExitSignal:   "",
			},
		}
	}

	out := *from

	if out.Metadata.Labels == nil {
		out.Metadata.Labels = map[string]string{}
	} else {
		labels := make(map[string]string, len(out.Metadata.Labels))
		for k, v := range out.Metadata.Labels {
			labels[k] = v
		}
		out.Metadata.Labels = labels
	}

	out.Spec.Args = cloneSlice(out.Spec.Args)
	out.Spec.Env = cloneSlice(out.Spec.Env)
	out.Spec.Ports = cloneSlice(out.Spec.Ports)
	out.Spec.Volumes = cloneVolumeMounts(out.Spec.Volumes)
	out.Spec.Networks = cloneSlice(out.Spec.Networks)
	out.Spec.NetworksAliases = cloneSlice(out.Spec.NetworksAliases)
	out.Spec.SecurityOpts = cloneSlice(out.Spec.SecurityOpts)
	out.Spec.Devices = cloneSlice(out.Spec.Devices)
	out.Spec.Secrets = cloneSecrets(out.Spec.Secrets)
	out.Spec.Repos = cloneRepos(out.Spec.Repos)
	out.Spec.Git = cloneGit(out.Spec.Git)
	out.Status.Repos = cloneRepoStatuses(out.Status.Repos)
	out.Status.Stages = cloneStageStatuses(out.Status.Stages)

	if out.Spec.Capabilities != nil {
		caps := *out.Spec.Capabilities
		caps.Drop = cloneSlice(caps.Drop)
		caps.Add = cloneSlice(caps.Add)
		out.Spec.Capabilities = &caps
	}

	if len(out.Spec.Tmpfs) > 0 {
		mounts := make([]ContainerTmpfsMount, len(out.Spec.Tmpfs))
		for i, m := range out.Spec.Tmpfs {
			m.Options = cloneSlice(m.Options)
			mounts[i] = m
		}
		out.Spec.Tmpfs = mounts
	}

	if out.Spec.Resources != nil {
		res := *out.Spec.Resources
		out.Spec.Resources = &res
	}

	return &out
}

func cloneSlice(in []string) []string {
	if in == nil {
		return []string{}
	}

	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneVolumeMounts(in []VolumeMount) []VolumeMount {
	if in == nil {
		return []VolumeMount{}
	}

	out := make([]VolumeMount, len(in))
	copy(out, in)
	// copy() is shallow: deep-copy the VolumeRef pointer so a clone never
	// shares the referent struct with the original (mirrors cloneSecrets).
	for i := range out {
		if in[i].VolumeRef != nil {
			ref := *in[i].VolumeRef
			out[i].VolumeRef = &ref
		}
	}
	return out
}

func cloneSecrets(in []ContainerSecret) []ContainerSecret {
	if in == nil {
		return nil
	}

	out := make([]ContainerSecret, len(in))
	copy(out, in)
	// copy() is shallow: deep-copy the SecretRef pointer so a clone never
	// shares the referent struct with the original.
	for i := range out {
		if in[i].SecretRef != nil {
			ref := *in[i].SecretRef
			out[i].SecretRef = &ref
		}
	}
	return out
}

func cloneRepos(in []ContainerRepo) []ContainerRepo {
	if in == nil {
		return nil
	}

	out := make([]ContainerRepo, len(in))
	copy(out, in)
	return out
}

// cloneGit deep-copies a ContainerGit, including its Author/Committer
// pointers and Sign slice, so a cloned ContainerDoc shares no mutable state
// with its source.
func cloneGit(in *ContainerGit) *ContainerGit {
	if in == nil {
		return nil
	}
	out := *in
	if in.Author != nil {
		author := *in.Author
		out.Author = &author
	}
	if in.Committer != nil {
		committer := *in.Committer
		out.Committer = &committer
	}
	out.Sign = cloneSlice(in.Sign)
	return &out
}

func cloneRepoStatuses(in []RepoStatus) []RepoStatus {
	if in == nil {
		return nil
	}

	out := make([]RepoStatus, len(in))
	copy(out, in)
	return out
}

func cloneStageStatuses(in []StageStatus) []StageStatus {
	if in == nil {
		return nil
	}

	out := make([]StageStatus, len(in))
	copy(out, in)
	return out
}
