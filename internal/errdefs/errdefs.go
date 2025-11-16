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

import "errors"

var (
	ErrWriteMetadata          = errors.New("failed to write metadata file")
	ErrConfig                 = errors.New("config error")
	ErrLoggerNotFound         = errors.New("logger not found in context")
	ErrNetworkNotFound        = errors.New("network not found")
	ErrConnectContainerd      = errors.New("failed to connect to containerd")
	ErrCheckNamespaceExists   = errors.New("failed to check if namespace exists")
	ErrNamespaceAlreadyExists = errors.New("namespace already exists")
	ErrCreateNamespace        = errors.New("failed to create namespace")
	ErrCreateRealm            = errors.New("failed to create realm")
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
)
