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
	ErrGetSpace               = errors.New("failed to get space")
	ErrSpaceNotFound          = errors.New("space not found")
	ErrSpaceDocRequired       = errors.New("space document is required")
	ErrSpaceNameRequired      = errors.New("space name is required")
	ErrRealmNameRequired      = errors.New("realm name is required")
	ErrUpdateSpaceMetadata    = errors.New("failed to update space metadata")
	ErrCreateSpace            = errors.New("failed to create space")
	ErrCreateSpaceCgroup      = errors.New("failed to create space cgroup")
	ErrCreateStackCgroup      = errors.New("failed to create stack cgroup")
	ErrStackNotFound          = errors.New("stack not found")
	ErrGetStack               = errors.New("failed to get stack")
	ErrCreateStack            = errors.New("failed to create stack")
	ErrNetworkAlreadyExists   = errors.New("network already exists")
	ErrUpdateStackMetadata    = errors.New("failed to update stack metadata")
	ErrUpdateCellMetadata     = errors.New("failed to update cell metadata")
	ErrStackNameRequired      = errors.New("stack name is required")
	ErrCellNotFound           = errors.New("cell not found")
	ErrGetCell                = errors.New("failed to get cell")
	ErrCreateCell             = errors.New("failed to create cell")
	ErrCreatePauseContainer   = errors.New("failed to create pause container")
)
