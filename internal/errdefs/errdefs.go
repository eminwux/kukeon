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

package errdefs

import (
	"errors"
)

var (
	ErrWriteMetadata          = errors.New("failed to write metadata file")
	ErrConfig                 = errors.New("config error")
	ErrLoggerNotFound         = errors.New("logger not found in context")
	ErrNetworkNotFound        = errors.New("network not found")
	ErrCreateNetwork          = errors.New("failed to create network")
	ErrSubnetExhausted        = errors.New("no free subnet available in parent CIDR")
	ErrInvalidSubnetCIDR      = errors.New("invalid subnet CIDR")
	ErrSubnetStateCorrupt     = errors.New("subnet allocator state is malformed")
	ErrConnectContainerd      = errors.New("failed to connect to containerd")
	ErrCheckNamespaceExists   = errors.New("failed to check if namespace exists")
	ErrNamespaceAlreadyExists = errors.New("namespace already exists")
	ErrCreateNamespace        = errors.New("failed to create namespace")
	ErrCreateRealm            = errors.New("failed to create realm")
	ErrCreateRealmCgroup      = errors.New("failed to create realm cgroup")
	ErrInitCniManager         = errors.New("failed to initiate cni manager")
	ErrRealmNotFound          = errors.New("realm not found")
	ErrGetRealm               = errors.New("failed to get realm")
	ErrUpdateRealmMetadata    = errors.New("failed to update realm metadata")
	ErrCreateRealmNamespace   = errors.New("failed to create realm namespace")
	ErrMissingMetadataFile    = errors.New("missing metadata file")
	// ErrStaleResource fires from metadata.WriteMetadataCAS when the
	// on-disk bytes diverged between the caller's prior read and the
	// compare-and-swap write. Surfaces to a controller writer that read,
	// mutated, and tried to persist a metadata document while another
	// writer (daemon, `kuke --no-daemon`, or the reconciler) committed
	// an intervening write under the same exclusive flock. The caller
	// must re-read, re-apply the mutation, and retry — there is no
	// silent merge of disjoint edits.
	ErrStaleResource          = errors.New("metadata resource changed since prior read")
	ErrUnsupportedAPIVersion  = errors.New("unsupported apiVersion")
	ErrUnknownKind            = errors.New("unknown kind")
	ErrConversionFailed       = errors.New("conversion failed")
	ErrDefaultingFailed       = errors.New("defaulting failed")
	ErrCheckNetworkExists     = errors.New("failed to check if network exists")
	ErrBridgePluginMissing    = errors.New("bridge plugin missing from conflist")
	ErrGetSpace               = errors.New("failed to get space")
	ErrSpaceNotFound          = errors.New("space not found")
	ErrSpaceDocRequired       = errors.New("space document is required")
	ErrSpaceNameRequired      = errors.New("space name is required")
	ErrRealmNameRequired      = errors.New("realm name is required")
	ErrInvalidRealmName       = errors.New("realm name is invalid")
	ErrUpdateSpaceMetadata    = errors.New("failed to update space metadata")
	ErrCreateSpace            = errors.New("failed to create space")
	ErrCreateSpaceCgroup      = errors.New("failed to create space cgroup")
	ErrCreateStackCgroup      = errors.New("failed to create stack cgroup")
	ErrCreateCellCgroup       = errors.New("failed to create cell cgroup")
	ErrStackNotFound          = errors.New("stack not found")
	ErrGetStack               = errors.New("failed to get stack")
	ErrCreateStack            = errors.New("failed to create stack")
	ErrNetworkAlreadyExists   = errors.New("network already exists")
	ErrUpdateStackMetadata    = errors.New("failed to update stack metadata")
	ErrUpdateCellMetadata     = errors.New("failed to update cell metadata")
	ErrStackNameRequired      = errors.New("stack name is required")
	ErrCellNotFound           = errors.New("cell not found")
	ErrCellAlreadyExists      = errors.New("cell already exists")
	ErrCellIDRequired         = errors.New("cell id is required")
	ErrGetCell                = errors.New("failed to get cell")
	ErrCreateCell             = errors.New("failed to create cell")
	ErrDiskPressure           = errors.New("data volume is under disk pressure")
	ErrCreateRootContainer    = errors.New("failed to create root container")
	ErrNetworkConfigNotLoaded = errors.New("network config not loaded")
	// ErrExplicitRootHostNetworkMismatch fires when a cell pins its root via
	// rootContainerId but the chosen root has HostNetwork=false while at
	// least one peer container has HostNetwork=true. Because peers join the
	// root's netns via JoinContainerNamespaces, a per-container-netns root
	// would silently swallow the peer's host-network intent.
	ErrExplicitRootHostNetworkMismatch = errors.New(
		"explicit rootContainerId must have hostNetwork: true when any cell container has hostNetwork: true",
	)
	// ErrMultipleRootContainers fires when more than one container in a cell
	// has root: true. Only one container can be the cell's root; any extras
	// would either silently lose their Root intent or contradict an explicit
	// rootContainerId. Surfaced by apischeme normalization so the user sees
	// a hard error at apply time instead of a silent drop downstream.
	ErrMultipleRootContainers = errors.New(
		"only one container in a cell may have root: true",
	)
	// ErrRootContainerMismatch fires when rootContainerId is set on a cell
	// but a different container in the array carries root: true. The
	// Root-flagged container would otherwise silently end up as a non-root
	// peer; a hard error forces the user to pick one canonical root.
	ErrRootContainerMismatch = errors.New(
		"rootContainerId disagrees with container marked root: true",
	)
	ErrCellNameRequired      = errors.New("cell name is required")
	ErrContainerNameRequired = errors.New("container name is required")
	// ErrSelectorWithName fires from every `kuke get <kind>` verb when the
	// caller supplies both a positional resource name and -l/--selector.
	// The two paths are mutually exclusive (a name targets exactly one
	// resource; a selector filters a list), so the verb refuses up front
	// instead of silently honouring one and dropping the other. Shared so
	// future selector-aware verbs (`kuke describe`, `kuke delete`) reuse
	// the same surface text and errors.Is identity.
	ErrSelectorWithName        = errors.New("--selector cannot be combined with a resource name")
	ErrInvalidName             = errors.New("name is invalid")
	ErrInvalidImage            = errors.New("invalid image reference")
	ErrDeleteRealm             = errors.New("failed to delete realm")
	ErrDeleteSpace             = errors.New("failed to delete space")
	ErrDeleteStack             = errors.New("failed to delete stack")
	ErrDeleteCell              = errors.New("failed to delete cell")
	ErrDeleteContainer         = errors.New("failed to delete container")
	ErrResourceHasDependencies = errors.New("resource has child resources")

	// Cgroup-related errors.

	ErrEmptyGroupPath   = errors.New("cgroup group path is required")
	ErrInvalidPID       = errors.New("pid must be greater than zero")
	ErrInvalidLeafName  = errors.New("cgroup leaf name must be a single non-empty path segment")
	ErrInvalidCPUWeight = errors.New("cpu weight must be within [1, 10000]")
	ErrInvalidIOWeight  = errors.New("io weight must be within [1, 1000]")
	ErrInvalidThrottle  = errors.New("io throttle entries require type, major, minor and rate")
	// ErrCgroupScopeKind rejects a GetCgroupDetail scope whose kind is not
	// realm, space, stack, cell or container (issue #1410).
	ErrCgroupScopeKind = errors.New("unsupported cgroup scope kind")
	// ErrNetworkScopeKind rejects a GetNetworkDetail scope whose kind is not
	// space, cell or container (issue #1411).
	ErrNetworkScopeKind = errors.New("unsupported network scope kind")
	// ErrCellTaskStatus wraps a non-not-found containerd failure while
	// reading a cell root task's status for GetNetworkDetail (issue #1411).
	ErrCellTaskStatus = errors.New("failed to read cell root task status")
	// ErrDaemonLogUnavailable marks a ReadDaemonLogs request whose source —
	// the kukeond container's runtime-shim log file — does not exist or the
	// kukeond container is not provisioned (issue #1386, step 7). A kukeond
	// launched outside its kuke-system cell has no shim log to read.
	ErrDaemonLogUnavailable = errors.New("kukeond log source unavailable")
	// ErrDaemonMethodUnsupported marks an RPC the running kukeond does not
	// implement — the client is newer than the daemon and kukeond must be
	// rebuilt/redeployed (e.g. `kuke init --kukeond-image ...`).
	ErrDaemonMethodUnsupported = errors.New(
		"kukeond does not support this request; upgrade kukeond to match the client",
	)

	// Container-related errors.

	ErrEmptyContainerID  = errors.New("container id is required")
	ErrEmptyCellID       = errors.New("cell id is required")
	ErrEmptySpaceID      = errors.New("space id is required")
	ErrEmptyRealmID      = errors.New("realm id is required")
	ErrEmptyStackID      = errors.New("stack id is required")
	ErrContainerExists   = errors.New("container already exists")
	ErrContainerNotFound = errors.New("container not found")
	ErrTaskNotFound      = errors.New("task not found")
	ErrTaskNotRunning    = errors.New("task is not running")

	// ErrCellWindDownImmediate fires when StartCell / StartContainer's
	// post-start liveness check observes a just-started container task in a
	// non-Running state within the startup grace window — i.e. the task
	// failed (or exited cleanly) between containerd accepting task creation
	// and the controller stamping the cell Ready. Without this sentinel the
	// cell would briefly persist Ready, the next reconciler tick would
	// derive Stopped from the dead workload, shouldWindDownCell would fire
	// KillCell, and the operator's in-flight `kuke run --attach` would race
	// the teardown — printing `containers: started` and then dialing a
	// socket that no longer exists. Routed through markCellFailed via the
	// existing provisionStarted defer so the cell lands at Failed instead
	// of the misleading Ready→Stopped→reaped cycle. Issue #851.
	ErrCellWindDownImmediate = errors.New("cell wound down immediately after start")

	// ErrCellReconcileFailed is the apply-layer sentinel raised when a cell's
	// reconcile completed without a runner-level error yet left the cell in
	// CellStateFailed. A compatible-change apply that routes through UpdateCell
	// (or the spec-in-sync "unchanged" branch over an already-Failed cell)
	// persists the desired spec without bringing the workload up, so it would
	// otherwise return success and exit 0 while the cell stays dead. Converting
	// that end-state into this sentinel makes ApplyDocuments stamp the resource
	// `failed` and `kuke apply` exit non-zero — apply must never report
	// `updated`/`unchanged` over a Failed cell. Issue #1185.
	ErrCellReconcileFailed = errors.New("cell reconciled to Failed state")

	// ErrCellSpecHashDrift is the StartCell sentinel raised when the
	// existing containerd container record carries a `kukeon.io/spec-hash`
	// label whose value disagrees with the SHA-256 over the on-disk
	// CellSpec's "requires containerd recreate" field set (image, command,
	// args). Mismatch is only reachable via crash-mid-apply or an
	// unsupported hand-edit of `metadata.yaml` — the supported `kuke apply
	// -f` flow re-stamps the label inside the same RecreateCell /
	// UpdateCell transaction that rewrites containerd state. The sentinel
	// keeps the operator-facing error parseable while the wrapping
	// message names the diverged hash pair and points at `kuke apply -f`.
	// Issue #867.
	ErrCellSpecHashDrift = errors.New("cell spec hash diverges from containerd record")

	// Volume-related errors.

	ErrVolumeSourceRequired    = errors.New("volume source is required")
	ErrVolumeTargetRequired    = errors.New("volume target is required")
	ErrVolumeSourceNotAbsolute = errors.New("volume source must be an absolute host path")
	ErrVolumeTargetNotAbsolute = errors.New("volume target must be an absolute container path")
	ErrVolumeSourceNotFound    = errors.New("volume source does not exist on the host")
	ErrVolumeKindUnknown       = errors.New(
		"volume kind is not recognized; expected \"\", \"bind\", \"tmpfs\", or \"volume\"",
	)
	ErrVolumeTmpfsSourceForbidden = errors.New(
		"tmpfs volume must not set a source; tmpfs is in-memory and has no host backing",
	)
	// Volume-reference (kind: volume) mount errors — step 4 (#1016).
	ErrVolumeRefSourceExclusive = errors.New(
		"volume mount must set exactly one of source (same-scope name) or volumeRef (cross-scope)",
	)
	ErrVolumeRefSourceMissing = errors.New(
		"volume mount must set either source (same-scope name) or volumeRef (cross-scope)",
	)
	ErrVolumeSourceNotName = errors.New(
		"volume mount source must be a Volume name, not an absolute path",
	)
	ErrVolumeRefNameRequired    = errors.New("volumeRef.name is required")
	ErrVolumeRefRealmRequired   = errors.New("volumeRef.realm is required")
	ErrVolumeRefScopeIncomplete = errors.New(
		"volumeRef scope is incomplete; a deeper coordinate requires every shallower one",
	)
	// ErrVolumeInUse gates `kuke delete volume` against a live mount: a Volume
	// referenced by a volume-kind mount on a running cell cannot be deleted out
	// from under it. The wrapping message names the mounting cell. Step 4
	// (#1016).
	ErrVolumeInUse = errors.New("volume is in use by a running cell")

	// CNI-related errors.

	ErrBridgeNameTooLong = errors.New("bridge name exceeds Linux IFNAMSIZ limit")
	ErrCNIPluginNotFound = errors.New("cni plugin not found")
	// ErrCNIVethExists fires when CNI ADD reports the container-side veth
	// (eth0) is already present in the target netns — the bridge plugin's
	// "container veth name … already exists" path. This is the one genuinely
	// idempotent failure: the previous ADD reached veth setup before
	// crashing, so the iface and its IPAM record are intact and a retry can
	// proceed without re-running attach. All other "already exists" /
	// "file exists" errors from the plugin chain (IP-addr conflicts, route
	// duplicates, IPAM duplicate-allocation, iptables) are real failures
	// that must surface.
	ErrCNIVethExists = errors.New("cni container veth already exists in netns")

	// Network-policy-related errors.

	ErrEgressRuleTargetRequired = errors.New("egress allow rule requires exactly one of host or cidr")
	ErrEgressRuleTargetConflict = errors.New("egress allow rule must not set both host and cidr")
	ErrEgressInvalidCIDR        = errors.New("egress allow rule cidr is not a valid CIDR")
	ErrEgressInvalidHost        = errors.New("egress allow rule host is not a valid dns name")
	ErrEgressInvalidPort        = errors.New("egress allow rule port must be within [1, 65535]")
	ErrEgressInvalidDefault     = errors.New("egress default must be 'allow' or 'deny'")
	ErrEgressHostResolution     = errors.New("failed to resolve egress allow rule host")
	ErrEgressApply              = errors.New("failed to apply egress policy")
	ErrEgressRemove             = errors.New("failed to remove egress policy")

	// Firewall (FORWARD admission) errors.

	ErrForwardAdmissionApply  = errors.New("failed to apply forward admission rules")
	ErrForwardAdmissionRemove = errors.New("failed to remove forward admission rules")

	// Image-related errors.

	// ErrLoadImage is returned when importing an OCI/docker tarball into a
	// realm's containerd namespace fails. Wrapped with the underlying
	// containerd error so callers can inspect the cause.
	ErrLoadImage = errors.New("failed to load image")

	// ErrTarballRequired is returned when `kuke image load` receives an
	// empty tarball (the file was empty, stdin was closed without bytes,
	// or `docker save` produced no output).
	ErrTarballRequired = errors.New("image tarball is required")

	// ErrImageNotFound is returned when a named image ref does not exist
	// in the target realm's containerd namespace. Surfaces to operators
	// from `kuke get image <ref>`.
	ErrImageNotFound = errors.New("image not found")

	// ErrInternalImageNotBuilt is returned when a cell references an image
	// hosted under the local-only kukeon.internal registry (a locally-built
	// image) that is absent from the target realm's
	// containerd namespace. The resolver never pulls kukeon.internal/... refs
	// — they are built locally, not published — so the fix is to build the
	// image, not to retry a doomed network pull against the non-routable host.
	ErrInternalImageNotBuilt = errors.New(
		"local-only kukeon.internal image is not present in the realm; " +
			"build it with `kuke build --realm <realm>`",
	)

	// ErrGetImage wraps the underlying containerd error when fetching
	// one image's metadata fails for reasons other than not-found.
	ErrGetImage = errors.New("failed to get image")

	// ErrListImages wraps the underlying containerd error when
	// enumerating images in a realm's namespace fails.
	ErrListImages = errors.New("failed to list images")

	// ErrDeleteImage wraps the underlying containerd error when removing
	// one image from a realm's namespace fails for reasons other than
	// not-found. Not-found is reported via ErrImageNotFound.
	ErrDeleteImage = errors.New("failed to delete image")

	// ErrPruneImages wraps the underlying containerd error when reclaiming
	// dangling image layers and the orphaned leases pinning them in a
	// realm's namespace fails (`kuke image prune`).
	ErrPruneImages = errors.New("failed to prune images")

	// ErrImageTagRequired is returned when `kuke build` is invoked without a
	// -t/--tag — the built image needs a name to land under in the realm's
	// containerd namespace.
	ErrImageTagRequired = errors.New("image tag is required (-t name:tag)")

	// ErrImageRequired is returned when the imperative `--image` cell source
	// (`kuke run --image` / `kuke create cell --image`, epic:first-run) is asked
	// to synthesize a cell from an empty image ref.
	ErrImageRequired = errors.New("image ref is required (--image <ref>)")

	// ErrKukebuildNotFound is returned by `kuke build` when the standalone
	// `kukebuild` binary (which embeds BuildKit, issue #522) is not on PATH.
	// `kuke build` is a thin shim that exec's kukebuild; without it the build
	// cannot proceed.
	ErrKukebuildNotFound = errors.New("kukebuild binary not found")

	// Attach-related errors.

	// ErrAttachNotSupported is returned when an attach request targets a
	// container that was not created with Attachable=true. The sbsh wrapper
	// is only injected on opt-in; non-Attachable containers have no
	// /run/kukeon/tty/socket to connect to.
	ErrAttachNotSupported = errors.New("container is not attachable; recreate with attachable=true")

	// ErrAttachAmbiguous is returned by the `kuke attach` candidate picker
	// when --container is omitted and the target cell has more than one
	// non-root attachable container. The error message lists the candidates
	// so the operator can re-run with --container.
	ErrAttachAmbiguous = errors.New("multiple attachable containers in cell; specify --container")

	// ErrAttachNoCandidate is returned by the `kuke attach` candidate
	// picker when --container is omitted and the target cell has zero
	// non-root attachable containers.
	ErrAttachNoCandidate = errors.New("no attachable container in cell")

	// ErrAttachTaskNotRunning is returned by the daemon's AttachContainer
	// endpoint when the target attachable container exists in the cell spec
	// and is Attachable but its containerd task is not Running. Server-side
	// complement to cmd/kuke/shared.GuardCellTaskLiveness: any consumer that
	// bypasses the CLI guard (RPC clients, scripts, future API consumers,
	// in-process callers in alternative run-path branches) still gets a
	// typed refusal instead of an unbacked socket path that would surface
	// as `connection refused` at `connect(2)` time (#852).
	ErrAttachTaskNotRunning = errors.New("container task is not running; cannot attach to a dead socket")

	// ErrAttachPingTimeout is returned by the post-StartCell attach loop
	// in cmd/kuke/run when sbsh's 3 s control-socket ping deadline keeps
	// firing because kuketty has bound the control socket but not yet
	// entered Serve()'s Accept loop — a "socket present, accept loop not
	// yet running" race distinct from the EACCES window #918 closed.
	// Promoted from sbsh's untyped `ping failed: context deadline
	// exceeded` (clientrunner/io.go) at the kukeon boundary so callers
	// can errors.Is the timeout class without string-matching the sbsh
	// wrap (#926).
	ErrAttachPingTimeout = errors.New("attach ping timed out: kuketty control socket bound but not yet serving")

	// ErrAttachStaleSocket is returned by the post-StartCell attach loop
	// in cmd/kuke/run when dial(2) keeps failing with EACCES (or ENOENT)
	// past the retry budget because a stale tty socket from a kuketty
	// binary predating sbsh#361 still occupies the path with mode 0o640
	// (or has been unlinked but not yet re-bound). New kuketty's
	// listenUnixWithMode landed in v0.12.1 and closes the *new-socket*
	// EACCES race, but the *stale-socket* race remains until the new
	// kuketty inside the cell finishes its os.Remove + listenUnixWithMode
	// init path. Promoted from raw syscall.EACCES / syscall.ENOENT at the
	// kukeon boundary so callers can errors.Is the readiness-race class
	// without sniffing for raw errno values (#933).
	ErrAttachStaleSocket = errors.New("attach stale socket: kuketty has not yet unlinked the pre-fix tty socket")

	// ErrAttachPermissionDenied is returned by the post-StartCell attach
	// loop in cmd/kuke/run when dial(2) fails with EACCES against a tty
	// control socket that is *present on disk* — a permanent permission
	// condition (the socket inode carries the wrong mode, e.g. 0o640
	// without group-write for the operator), not the transient pre-fix
	// stale-socket race ErrAttachStaleSocket covers. Retrying cannot
	// resolve a wrong-mode inode, so the loop fails fast instead of
	// burning the full retry budget on a 10 s hang followed by a
	// misleading "stale socket" message. Split from ErrAttachStaleSocket
	// by stat(2) at classification time — socket absent (stat ENOENT) is
	// the transient class worth retrying; socket present + EACCES is this
	// permanent class — so callers can errors.Is the permanent failure
	// and operators get a message that names the socket mode and points
	// at the heal (`kuke restart <cell>`) rather than a stale socket
	// (#1170).
	ErrAttachPermissionDenied = errors.New(
		"attach permission denied: kuketty control socket is present but not accessible (wrong mode); " +
			"retrying cannot fix this — heal the socket with `kuke restart <cell>`",
	)

	// ErrSocketPathTooLong fires when the resolved host-side path of a
	// per-container kuketty control socket would overflow Linux's
	// sockaddr_un.sun_path buffer (consts.KukeonMaxSocketPath bytes plus
	// the terminating NUL — UNIX_PATH_MAX = 108). The daemon refuses to
	// provision the Attachable container so the failure surfaces at
	// provision time with the offending path named, instead of deferring
	// the failure to `kuke attach` where the kernel-level `connect(2)`
	// returns the cryptic `invalid argument` (issue #521).
	ErrSocketPathTooLong = errors.New("host-side kuketty socket path exceeds SUN_PATH")

	// (Profile-related sentinels — ErrProfileNotFound, ErrProfileInvalid —
	// were removed in #626 alongside the CellProfile kind. Blueprint
	// equivalents below cover the surviving paths.)

	// ErrServerConfigurationInvalid is returned when a ServerConfiguration
	// YAML fails to parse or violates the schema (wrong kind, etc.).
	ErrServerConfigurationInvalid = errors.New("server configuration is invalid")

	// ErrInstanceMismatch is returned when the configured runPath was
	// previously bootstrapped under a different containerdNamespaceSuffix
	// or cgroupRoot. Migration of an existing host between suffixes /
	// cgroup roots is out of scope; the operator must destroy the old run
	// path and re-run `kuke init` against the new config.
	ErrInstanceMismatch = errors.New("kukeon instance mismatch")

	// ErrClientConfigurationInvalid is returned when a ClientConfiguration
	// YAML fails to parse or violates the schema (wrong kind, etc.).
	ErrClientConfigurationInvalid = errors.New("client configuration is invalid")

	// Secret-related errors.

	ErrSecretNameRequired         = errors.New("secret name is required")
	ErrSecretSourceRequired       = errors.New("secret requires exactly one of fromFile, fromEnv, or secretRef")
	ErrSecretMultipleSources      = errors.New("secret must set exactly one of fromFile, fromEnv, or secretRef")
	ErrSecretMountPathNotAbsolute = errors.New("secret mountPath must be an absolute container path")
	ErrSecretFromFileNotFound     = errors.New("secret fromFile path does not exist on the host")
	ErrSecretFromEnvNotSet        = errors.New("secret fromEnv env var is not set on the daemon host")
	ErrSecretStagingFailed        = errors.New("failed to stage secret file for mount")

	// ErrSecretCoordUnsafe guards every secret name and scope coordinate that
	// flows into fs.SecretPath against path traversal: a "/" or "\" separator,
	// a "." or ".." element, or a NUL byte would let filepath.Join escape the
	// secrets tree (issue #673). Shared by both validateSecretRef and
	// validateSecretScope so the whole secret subsystem is covered.
	ErrSecretCoordUnsafe = errors.New(
		"secret name and scope coordinates must not contain path separators or '.'/'..' elements",
	)

	// ContainerSecret.secretRef source errors (issue #623).

	ErrSecretRefNameRequired    = errors.New("secret secretRef.name is required")
	ErrSecretRefRealmRequired   = errors.New("secret secretRef.realm is required")
	ErrSecretRefScopeIncomplete = errors.New(
		"secret secretRef scope is incomplete: a deeper scope coordinate requires all shallower ones",
	)
	ErrSecretRefNotFound = errors.New("referenced secret does not exist in the requested scope")
	ErrSecretInUse       = errors.New("secret is referenced by a live container via secretRef")

	// Secret kind (kind: Secret) storage-primitive errors (issue #619).

	ErrSecretRealmRequired   = errors.New("secret metadata.realm is required")
	ErrSecretScopeIncomplete = errors.New(
		"secret scope is incomplete: a deeper scope coordinate requires all shallower ones",
	)
	ErrSecretDataRequired  = errors.New("secret spec.data is required and must not be empty")
	ErrSecretScopeNotFound = errors.New("secret scope does not exist")
	ErrWriteSecret         = errors.New("failed to write secret bytes")

	// Secret kind read/delete-verb errors (issue #622). The bytes are never
	// surfaced by these paths — get reports metadata only, delete removes the
	// daemon-stored file.

	ErrSecretNotFound = errors.New("secret not found")
	ErrGetSecret      = errors.New("failed to get secret")
	ErrListSecrets    = errors.New("failed to list secrets")
	ErrDeleteSecret   = errors.New("failed to delete secret")

	// Repo-related errors (containers[].repos[], issue #617).

	ErrRepoNameRequired      = errors.New("repo name is required")
	ErrRepoTargetRequired    = errors.New("repo target is required")
	ErrRepoTargetNotAbsolute = errors.New("repo target must be an absolute container path")
	ErrRepoURLRequired       = errors.New("repo url is required")
	// ErrRepoBranchRefMutex fires when a ContainerRepo declares both
	// `branch` (moving target) and `ref` (immutable tag or commit SHA).
	// The two have different fetch-path semantics — `ref` skips the
	// fast-forward step that breaks on a detached HEAD — so the spec must
	// pick one (#1034).
	ErrRepoBranchRefMutex = errors.New("repo cannot set both branch and ref")

	// CellBlueprint kind (kind: CellBlueprint) storage-primitive errors
	// (issue #620, phase 4a-i of #423).

	ErrBlueprintNameRequired  = errors.New("blueprint metadata.name is required")
	ErrBlueprintRealmRequired = errors.New("blueprint metadata.realm is required")
	// ErrBlueprintScopeIncomplete fires when a deeper scope coordinate is set
	// without every shallower one. A Blueprint is scopable at realm/space/stack
	// only, so a set cell coordinate is itself a violation.
	ErrBlueprintScopeIncomplete = errors.New(
		"blueprint scope is incomplete: a deeper scope coordinate requires all shallower ones (and a Blueprint may not be cell-scoped)",
	)
	ErrBlueprintScopeNotFound = errors.New("blueprint scope does not exist")
	ErrBlueprintCellRequired  = errors.New("blueprint spec.cell.containers is required and cannot be empty")
	ErrWriteBlueprint         = errors.New("failed to write blueprint document")
	ErrBlueprintNotFound      = errors.New("blueprint not found")
	ErrGetBlueprint           = errors.New("failed to get blueprint")
	ErrListBlueprints         = errors.New("failed to list blueprints")
	ErrDeleteBlueprint        = errors.New("failed to delete blueprint document")
	// ErrBlueprintStructuralSlots fires when `kuke run -b` is asked to run a
	// blueprint that declares structural slots (secret slots, or repo slots
	// with no url) the inline scalar-param path cannot fill. Such blueprints
	// require a CellConfig and `kuke run -c` (#625).
	ErrBlueprintStructuralSlots = errors.New(
		"blueprint declares structural slots that require a CellConfig: inline `kuke run --from-blueprint` fills scalar parameters only",
	)
	// ErrBlueprintInvalid is returned when `kuke run -b` / `kuke apply -b`
	// cannot resolve a blueprint's scalar parameters (an undeclared --param
	// key, a required parameter left unset, or a malformed --param/--param-file
	// argument).
	ErrBlueprintInvalid = errors.New("blueprint is invalid")

	// CellBlueprint slot-declaration shape errors (issue #620).

	ErrBlueprintSecretSlotNameRequired = errors.New("blueprint secret slot name is required")
	ErrBlueprintSecretSlotMode         = errors.New("blueprint secret slot mode must be \"env\" or \"file\"")
	ErrBlueprintSecretSlotEnvName      = errors.New("blueprint secret slot with mode \"env\" requires envName")
	ErrBlueprintSecretSlotMountPath    = errors.New(
		"blueprint secret slot with mode \"file\" requires an absolute mountPath",
	)

	// Volume kind (kind: Volume) storage-primitive errors (issue #1018, step 1
	// of the volumes epic #1015).

	ErrVolumeNameRequired  = errors.New("volume metadata.name is required")
	ErrVolumeRealmRequired = errors.New("volume metadata.realm is required")
	// ErrVolumeScopeIncomplete fires when a deeper scope coordinate is set
	// without every shallower one. A Volume is scopable at realm/space/stack
	// only, so a set cell coordinate is itself a violation.
	ErrVolumeScopeIncomplete = errors.New(
		"volume scope is incomplete: a deeper scope coordinate requires all shallower ones (and a Volume may not be cell-scoped)",
	)
	// ErrVolumeCoordUnsafe rejects a volume name or scope coordinate that would
	// escape the volumes tree once fs.VolumePath filepath.Join's it into a host
	// path — a "/" or "\" separator, a "." or ".." element, or a NUL byte
	// (mirrors the Secret-coordinate guard from #673). A Volume provisions a
	// real directory, so an unguarded name is a path-traversal risk.
	ErrVolumeCoordUnsafe   = errors.New("volume name or scope coordinate is unsafe")
	ErrVolumeScopeNotFound = errors.New("volume scope does not exist")
	ErrWriteVolume         = errors.New("failed to write volume")
	ErrVolumeNotFound      = errors.New("volume not found")
	ErrGetVolume           = errors.New("failed to get volume")
	ErrListVolumes         = errors.New("failed to list volumes")
	ErrDeleteVolume        = errors.New("failed to delete volume")
	// ErrVolumeReclaimPolicyInvalid rejects a spec.reclaimPolicy that is neither
	// "Retain" nor "Delete" (an empty value is allowed and means Delete). The
	// selective cascade reclaim lands in step 3 (#1237).
	ErrVolumeReclaimPolicyInvalid = errors.New(
		`volume spec.reclaimPolicy must be "Retain" or "Delete" (or omitted)`,
	)

	// CellConfig kind (kind: CellConfig) errors (issue #624, phase 4b-i of #423).

	ErrConfigNameRequired  = errors.New("config metadata.name is required")
	ErrConfigRealmRequired = errors.New("config metadata.realm is required")
	// ErrConfigScopeIncomplete fires when a deeper scope coordinate is set
	// without every shallower one. A Config is scopable at realm/space/stack
	// only, so a set cell coordinate is itself a violation.
	ErrConfigScopeIncomplete = errors.New(
		"config scope is incomplete: a deeper scope coordinate requires all shallower ones (and a Config may not be cell-scoped)",
	)
	// ErrConfigBlueprintRefRequired fires when spec.blueprint omits the
	// referenced blueprint's name or realm — both are required to resolve it.
	ErrConfigBlueprintRefRequired = errors.New("config spec.blueprint requires name and realm")
	// ErrConfigBlueprintRefScopeIncomplete fires when the referenced blueprint's
	// scope coordinates are incomplete (a deeper coordinate without a shallower).
	ErrConfigBlueprintRefScopeIncomplete = errors.New(
		"config spec.blueprint scope is incomplete: a deeper coordinate requires all shallower ones",
	)
	// ErrConfigRepoFillURLRequired fires when a spec.repos fill omits its url —
	// the fill exists to supply the clone source the blueprint left open.
	ErrConfigRepoFillURLRequired = errors.New("config repo slot fill requires url")
	// ErrConfigSecretFillRefRequired fires when a spec.secrets fill omits its
	// secretRef (name + realm) — the fill exists to supply the secret source.
	ErrConfigSecretFillRefRequired = errors.New("config secret slot fill requires secretRef name and realm")
	// ErrConfigBlueprintNotFound fires at apply time when the referenced
	// blueprint cannot be read from daemon storage at its named scope.
	ErrConfigBlueprintNotFound = errors.New("config references a blueprint that does not exist")
	// ErrConfigUnknownRepoSlot fires when spec.repos names a slot the referenced
	// blueprint does not declare as a structural repo slot.
	ErrConfigUnknownRepoSlot = errors.New("config fills a repo slot the blueprint does not declare")
	// ErrConfigUnknownSecretSlot fires when spec.secrets names a slot the
	// referenced blueprint does not declare.
	ErrConfigUnknownSecretSlot = errors.New("config fills a secret slot the blueprint does not declare")
	// ErrConfigRequiredSlotUnfilled fires when a required structural slot the
	// blueprint declares is not filled by the config.
	ErrConfigRequiredSlotUnfilled = errors.New("config leaves a required blueprint slot unfilled")
	// ErrConfigScopeNotFound fires when the config's own scope (realm/space/
	// stack) does not exist on the host at apply time.
	ErrConfigScopeNotFound = errors.New("config scope does not exist")
	// ErrWriteConfig wraps a failure to persist a CellConfig document.
	ErrWriteConfig = errors.New("failed to write config document")

	// CellConfig read/delete-verb errors (issue #644, phase 4b-ii of #423).
	// Get returns the full document; list returns metadata only; delete
	// removes the daemon-stored file (and emits a back-reference notice, never
	// a refusal, when a live cell still carries the kukeon.io/config label).

	ErrConfigNotFound = errors.New("config not found")
	ErrGetConfig      = errors.New("failed to get config")
	ErrListConfigs    = errors.New("failed to list configs")
	ErrDeleteConfig   = errors.New("failed to delete config document")
	// ErrConfigExists is the atomic-create-only sentinel for CellConfig
	// writes (issue #839). Returned by the daemon's create-only path
	// (controller.CreateConfig / runner.WriteConfigIfAbsent) when a Config
	// of the same name already lives in the target scope — the caller
	// (`kuke create config`) surfaces it as a hard collision.
	ErrConfigExists = errors.New("config already exists")

	// ErrRenderDigestMismatch is returned by CreateCellFromConfig when the
	// caller pinned the digest of an earlier PreviewCellFromConfig and the
	// daemon's fresh render differs (the Config, its Blueprint, a defaults
	// level or a realm image changed in between). Issue #1418.
	ErrRenderDigestMismatch = errors.New("rendered cell changed since preview; preview again")
	// ErrCreateConfig wraps a failure on the controller-level atomic
	// create-only CellConfig endpoint. Scope/blueprint/slot validation
	// failures propagate their own sentinels (ErrConfigScopeNotFound,
	// ErrConfigBlueprintNotFound, …); this sentinel wraps everything else
	// the create path can surface.
	ErrCreateConfig = errors.New("failed to create config document")

	// ErrMustRunAsRoot is returned by direct-write subcommands (kuke init,
	// kuke daemon reset, kuke image load, kuke doctor cgroups --probe)
	// when invoked with a non-zero effective UID. Wrapped errors must
	// name the subcommand and suggest `sudo`. Daemon-routed verbs
	// (kuke get, kuke create, kuke apply, kuke delete, …) do not gate on
	// this — those are the supported `kukeon`-group rootless-client path.
	ErrMustRunAsRoot = errors.New("command must run as root")

	// kuke daemon lifecycle errors.

	// ErrHostNotInitialized fires from every `kuke daemon` read/write verb
	// (start, stop, kill, restart, reset, logs) when the kukeond cell
	// metadata is missing — i.e. the host has not been bootstrapped yet.
	// Centralised here so the surface text and errors.Is identity are
	// shared across verbs.
	ErrHostNotInitialized = errors.New(
		"kukeon host is not initialized: kukeond cell metadata is missing; run `kuke init` first",
	)

	// ErrControllerNoChange fires when a Start/Stop/Kill/Delete controller
	// call returns success but reports the cell was already in the target
	// state. Callers wrap with `fmt.Errorf("<verb> kukeond cell: %w",
	// errdefs.ErrControllerNoChange)` so `errors.Is` works across the
	// boundary without diverging the surface text.
	ErrControllerNoChange = errors.New("controller reported no change")
)

var (
	ErrInvalidCellState        = errors.New("invalid cell lifecycle state")
	ErrCellOperationInProgress = errors.New("cell lifecycle operation already in progress")
)
