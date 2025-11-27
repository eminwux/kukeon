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
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
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

// ConvertCellListToExternal converts a slice of internal cells to a slice of external cell docs.
func ConvertCellListToExternal(internalCells []intmodel.Cell) ([]*v1beta1.CellDoc, error) {
	externalCells := make([]*v1beta1.CellDoc, 0, len(internalCells))
	for _, cell := range internalCells {
		cellDoc, convertErr := apischeme.BuildCellExternalFromInternal(cell, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		externalCells = append(externalCells, &cellDoc)
	}
	return externalCells, nil
}

// ConvertStackListToExternal converts a slice of internal stacks to a slice of external stack docs.
func ConvertStackListToExternal(internalStacks []intmodel.Stack) ([]*v1beta1.StackDoc, error) {
	externalStacks := make([]*v1beta1.StackDoc, 0, len(internalStacks))
	for _, stack := range internalStacks {
		stackDoc, convertErr := apischeme.BuildStackExternalFromInternal(stack, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		externalStacks = append(externalStacks, &stackDoc)
	}
	return externalStacks, nil
}

// ConvertSpaceListToExternal converts a slice of internal spaces to a slice of external space docs.
func ConvertSpaceListToExternal(internalSpaces []intmodel.Space) ([]*v1beta1.SpaceDoc, error) {
	externalSpaces := make([]*v1beta1.SpaceDoc, 0, len(internalSpaces))
	for _, space := range internalSpaces {
		spaceDoc, convertErr := apischeme.BuildSpaceExternalFromInternal(space, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		externalSpaces = append(externalSpaces, &spaceDoc)
	}
	return externalSpaces, nil
}

// ConvertRealmListToExternal converts a slice of internal realms to a slice of external realm docs.
func ConvertRealmListToExternal(internalRealms []intmodel.Realm) ([]*v1beta1.RealmDoc, error) {
	externalRealms := make([]*v1beta1.RealmDoc, 0, len(internalRealms))
	for _, realm := range internalRealms {
		realmDoc, convertErr := apischeme.BuildRealmExternalFromInternal(realm, apischeme.VersionV1Beta1)
		if convertErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
		}
		externalRealms = append(externalRealms, &realmDoc)
	}
	return externalRealms, nil
}

// ConvertContainerSpecListToExternal converts a slice of internal container specs to a slice of external specs.
func ConvertContainerSpecListToExternal(internalSpecs []intmodel.ContainerSpec) ([]*v1beta1.ContainerSpec, error) {
	externalSpecs := make([]*v1beta1.ContainerSpec, 0, len(internalSpecs))
	for _, spec := range internalSpecs {
		containerSpec := apischeme.BuildContainerSpecExternalFromInternal(spec)
		externalSpecs = append(externalSpecs, &containerSpec)
	}
	return externalSpecs, nil
}
