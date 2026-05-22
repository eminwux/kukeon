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

package apischeme

import (
	"fmt"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// ConvertCellBlueprintDocToInternal converts an external CellBlueprintDoc to
// the internal hub carrier (issue #620). The body is serialized verbatim into
// the carrier's Document — the daemon stores and round-trips it without
// interpreting it — while the scope coordinates are lifted onto the metadata
// so the storage runner can resolve the on-disk path. The apiVersion/kind are
// normalized on the way in so the stored document is always canonical.
func ConvertCellBlueprintDocToInternal(in ext.CellBlueprintDoc) (intmodel.CellBlueprint, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		canonical := in
		canonical.APIVersion = ext.APIVersionV1Beta1
		canonical.Kind = ext.KindCellBlueprint
		document, err := yaml.Marshal(canonical)
		if err != nil {
			return intmodel.CellBlueprint{}, fmt.Errorf("marshal CellBlueprint document: %w", err)
		}
		return intmodel.CellBlueprint{
			Metadata: intmodel.CellBlueprintMetadata{
				Name:  in.Metadata.Name,
				Realm: in.Metadata.Realm,
				Space: in.Metadata.Space,
				Stack: in.Metadata.Stack,
			},
			Document: document,
		}, nil
	default:
		return intmodel.CellBlueprint{}, fmt.Errorf("unsupported apiVersion for CellBlueprint: %s", in.APIVersion)
	}
}

// NormalizeCellBlueprint takes an external CellBlueprintDoc request and returns
// an internal carrier and the chosen apiVersion.
func NormalizeCellBlueprint(req ext.CellBlueprintDoc) (intmodel.CellBlueprint, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertCellBlueprintDocToInternal(req)
	if err != nil {
		return intmodel.CellBlueprint{}, "", err
	}
	return internal, version, nil
}

// ConvertCellBlueprintToExternal reconstructs the external CellBlueprintDoc
// from the internal carrier's stored Document (issue #620). It is the inverse
// of ConvertCellBlueprintDocToInternal, used by GetBlueprint to hand the full
// template back to `kuke run -b` for materialization.
func ConvertCellBlueprintToExternal(in intmodel.CellBlueprint) (ext.CellBlueprintDoc, error) {
	var doc ext.CellBlueprintDoc
	if err := yaml.Unmarshal(in.Document, &doc); err != nil {
		return ext.CellBlueprintDoc{}, fmt.Errorf("unmarshal CellBlueprint document: %w", err)
	}
	return doc, nil
}
