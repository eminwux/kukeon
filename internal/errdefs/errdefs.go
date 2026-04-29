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

	// CNI-related errors.

	ErrBridgeNameTooLong = errors.New("bridge name exceeds Linux IFNAMSIZ limit")
	ErrCNIPluginNotFound = errors.New("cni plugin not found")

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

	// Secret-related errors.

	ErrSecretNameRequired         = errors.New("secret name is required")
	ErrSecretSourceRequired       = errors.New("secret requires exactly one of fromFile or fromEnv")
	ErrSecretMultipleSources      = errors.New("secret must not set both fromFile and fromEnv")
	ErrSecretMountPathNotAbsolute = errors.New("secret mountPath must be an absolute container path")
	ErrSecretFromFileNotFound     = errors.New("secret fromFile path does not exist on the host")
	ErrSecretFromEnvNotSet        = errors.New("secret fromEnv env var is not set on the daemon host")
	ErrSecretStagingFailed        = errors.New("failed to stage secret file for mount")
)
