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
	Tmpfs                  []ContainerTmpfsMount  `json:"tmpfs,omitempty"                  yaml:"tmpfs,omitempty"`
	Resources              *ContainerResources    `json:"resources,omitempty"              yaml:"resources,omitempty"`
	Secrets                []ContainerSecret      `json:"secrets,omitempty"                yaml:"secrets,omitempty"`
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
	Prompt string `json:"prompt,omitempty"  yaml:"prompt,omitempty"`
	// OnInit are scripts run once when the wrapped shell starts, in
	// order. Forwarded to TerminalSpec.Stages.OnInit via sbsh's
	// WithOnInit; an empty slice leaves Stages.OnInit zero.
	OnInit []TtyStage `json:"onInit,omitempty"  yaml:"onInit,omitempty"`
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
	LogFile string `json:"logFile,omitempty" yaml:"logFile,omitempty"`
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
// a bare string so future stage knobs (timeout, runOn, etc.) can land
// without breaking the YAML shape.
type TtyStage struct {
	Script string `json:"script,omitempty" yaml:"script,omitempty"`
}

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
		if s.Script != "" {
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
)

// VolumeMount is a mount entry attached to a container. The Kind discriminator
// selects the OCI mount type the runtime emits: bind (host path → container
// path) or tmpfs (in-memory directory). Empty Kind means bind for back-compat
// with YAML authored before the discriminator existed.
type VolumeMount struct {
	Kind      VolumeKind `json:"kind,omitempty"      yaml:"kind,omitempty"`
	Source    string     `json:"source,omitempty"    yaml:"source,omitempty"`
	Target    string     `json:"target"              yaml:"target"`
	ReadOnly  bool       `json:"readOnly,omitempty"  yaml:"readOnly,omitempty"`
	SizeBytes int64      `json:"sizeBytes,omitempty" yaml:"sizeBytes,omitempty"`
	Mode      uint32     `json:"mode,omitempty"      yaml:"mode,omitempty"`
}

// ContainerRepo declares a git repository the container depends on. kuketty
// clones (or fetches) it into Target before the workload starts, replacing the
// hand-rolled `if [ ! -d $DIR/.git ]; then git clone …; fi` blocks that crew
// templates duplicate in onInit scripts today. The clone state becomes daemon-
// observable via ContainerStatus.Repos (reported over RPC in phase 1b, #642)
// rather than buried in attach stderr. Issue #617, phase 1a of #423.
type ContainerRepo struct {
	// Name is the operator-facing identifier for the repo, echoed back in
	// per-repo status. Required.
	Name string `json:"name"               yaml:"name"`
	// Target is the absolute in-container path the repo is cloned into.
	// Required.
	Target string `json:"target"             yaml:"target"`
	// Branch is the branch to check out. Empty clones the remote's default
	// branch.
	Branch string `json:"branch,omitempty"   yaml:"branch,omitempty"`
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
	// signatures. Optional; rendered only when set. Crew sets this today, so
	// the field exists for the env block to be a strict superset of crew's.
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
	Name         string         `json:"name"            yaml:"name"`
	ID           string         `json:"id"              yaml:"id"`
	State        ContainerState `json:"state"           yaml:"state"`
	RestartCount int            `json:"restartCount"    yaml:"restartCount"`
	RestartTime  time.Time      `json:"restartTime"     yaml:"restartTime"`
	StartTime    time.Time      `json:"startTime"       yaml:"startTime"`
	FinishTime   time.Time      `json:"finishTime"      yaml:"finishTime"`
	ExitCode     int            `json:"exitCode"        yaml:"exitCode"`
	ExitSignal   string         `json:"exitSignal"      yaml:"exitSignal"`
	// Repos reports the per-repo outcome of kuketty's pre-Serve clone/fetch
	// step for an Attachable container's Spec.Repos. Empty for containers
	// with no repos[] or that have not yet been provisioned. Populated over
	// the GetSetupStatus RPC in phase 1b (#642); phase 1a lands the schema
	// only. Issue #617.
	Repos []RepoStatus `json:"repos,omitempty" yaml:"repos,omitempty"`
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
	out.Spec.Secrets = cloneSecrets(out.Spec.Secrets)
	out.Spec.Repos = cloneRepos(out.Spec.Repos)
	out.Spec.Git = cloneGit(out.Spec.Git)
	out.Status.Repos = cloneRepoStatuses(out.Status.Repos)

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
