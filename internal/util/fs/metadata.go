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

package fs

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/consts"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// RealmMetadataDir returns the metadata directory for the given realm.
func RealmMetadataDir(baseRunPath, realmName string) string {
	return filepath.Join(baseRunPath, realmName)
}

// RealmMetadataPath returns the metadata file path for the given realm.
func RealmMetadataPath(baseRunPath, realmName string) string {
	return filepath.Join(
		RealmMetadataDir(baseRunPath, realmName),
		consts.KukeonMetadataFile,
	)
}

// SpaceMetadataDir returns the metadata directory for the given space within a realm.
func SpaceMetadataDir(baseRunPath, realmName, spaceName string) string {
	return filepath.Join(RealmMetadataDir(baseRunPath, realmName), spaceName)
}

// SpaceMetadataPath returns the metadata file path for the given space within a realm.
func SpaceMetadataPath(baseRunPath, realmName, spaceName string) string {
	return filepath.Join(
		SpaceMetadataDir(baseRunPath, realmName, spaceName),
		consts.KukeonMetadataFile,
	)
}

// StackMetadataDir returns the metadata directory for the given stack within a realm and space.
func StackMetadataDir(baseRunPath, realmName, spaceName, stackName string) string {
	return filepath.Join(SpaceMetadataDir(baseRunPath, realmName, spaceName), stackName)
}

// StackMetadataPath returns the metadata file path for the given stack within a realm and space.
func StackMetadataPath(baseRunPath, realmName, spaceName, stackName string) string {
	return filepath.Join(
		StackMetadataDir(baseRunPath, realmName, spaceName, stackName),
		consts.KukeonMetadataFile,
	)
}

// CellMetadataDir returns the metadata directory for the given cell within a realm, space, and stack.
func CellMetadataDir(baseRunPath, realmName, spaceName, stackName, cellName string) string {
	return filepath.Join(StackMetadataDir(baseRunPath, realmName, spaceName, stackName), cellName)
}

// CellMetadataPath returns the metadata file path for the given cell within a realm, space, and stack.
func CellMetadataPath(baseRunPath, realmName, spaceName, stackName, cellName string) string {
	return filepath.Join(
		CellMetadataDir(baseRunPath, realmName, spaceName, stackName, cellName),
		consts.KukeonMetadataFile,
	)
}

type metadataHeader struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

// DetectMetadataVersion detects the API version from raw metadata bytes by parsing the apiVersion field.
// It returns the normalized version using apischeme.DefaultVersion.
func DetectMetadataVersion(raw []byte) (v1beta1.Version, error) {
	var header metadataHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", fmt.Errorf("metadata header: %w", err)
	}
	return apischeme.DefaultVersion(v1beta1.Version(header.APIVersion)), nil
}
