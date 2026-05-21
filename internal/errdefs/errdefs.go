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
	ErrCellIDRequired         = errors.New("cell id is required")
	ErrGetCell                = errors.New("failed to get cell")
	ErrCreateCell             = errors.New("failed to create cell")
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
	ErrCellNameRequired        = errors.New("cell name is required")
	ErrContainerNameRequired   = errors.New("container name is required")
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

	// Volume-related errors.

	ErrVolumeSourceRequired    = errors.New("volume source is required")
	ErrVolumeTargetRequired    = errors.New("volume target is required")
	ErrVolumeSourceNotAbsolute = errors.New("volume source must be an absolute host path")
	ErrVolumeTargetNotAbsolute = errors.New("volume target must be an absolute container path")
	ErrVolumeSourceNotFound    = errors.New("volume source does not exist on the host")
	ErrVolumeNamedNotSupported = errors.New(
		"named or managed volumes are not supported; use an absolute host path as source",
	)
	ErrVolumeKindUnknown = errors.New(
		"volume kind is not recognized; expected \"\", \"bind\", or \"tmpfs\"",
	)
	ErrVolumeTmpfsSourceForbidden = errors.New(
		"tmpfs volume must not set a source; tmpfs is in-memory and has no host backing",
	)

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
	// from `kuke image get <ref>`.
	ErrImageNotFound = errors.New("image not found")

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

	// ErrSocketPathTooLong fires when the resolved host-side path of a
	// per-container kuketty control socket would overflow Linux's
	// sockaddr_un.sun_path buffer (consts.KukeonMaxSocketPath bytes plus
	// the terminating NUL — UNIX_PATH_MAX = 108). The daemon refuses to
	// provision the Attachable container so the failure surfaces at
	// provision time with the offending path named, instead of deferring
	// the failure to `kuke attach` where the kernel-level `connect(2)`
	// returns the cryptic `invalid argument` (issue #521).
	ErrSocketPathTooLong = errors.New("host-side kuketty socket path exceeds SUN_PATH")

	// Profile-related errors.

	// ErrProfileNotFound is returned when `kuke run -p <name>` cannot find
	// a CellProfile under the active profiles directory. Wrapped errors
	// must name the profile and the directory searched so the operator
	// knows which file to drop.
	ErrProfileNotFound = errors.New("profile not found")

	// ErrProfileInvalid is returned when a CellProfile YAML fails to parse
	// or violates the schema (missing kind, missing metadata.name, etc.).
	ErrProfileInvalid = errors.New("profile is invalid")

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
	ErrSecretSourceRequired       = errors.New("secret requires exactly one of fromFile or fromEnv")
	ErrSecretMultipleSources      = errors.New("secret must not set both fromFile and fromEnv")
	ErrSecretMountPathNotAbsolute = errors.New("secret mountPath must be an absolute container path")
	ErrSecretFromFileNotFound     = errors.New("secret fromFile path does not exist on the host")
	ErrSecretFromEnvNotSet        = errors.New("secret fromEnv env var is not set on the daemon host")
	ErrSecretStagingFailed        = errors.New("failed to stage secret file for mount")

	// Repo-related errors (containers[].repos[], issue #617).

	ErrRepoNameRequired      = errors.New("repo name is required")
	ErrRepoTargetRequired    = errors.New("repo target is required")
	ErrRepoTargetNotAbsolute = errors.New("repo target must be an absolute container path")
	ErrRepoURLRequired       = errors.New("repo url is required")

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
