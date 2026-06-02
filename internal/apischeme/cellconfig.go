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

// ConvertCellConfigDocToInternal converts an external CellConfigDoc to the
// internal hub carrier (issue #624). The body is serialized verbatim into the
// carrier's Document — the daemon stores and round-trips it without
// interpreting it — while the scope coordinates are lifted onto the metadata so
// the storage runner can resolve the on-disk path. The apiVersion/kind are
// normalized on the way in so the stored document is always canonical.
func ConvertCellConfigDocToInternal(in ext.CellConfigDoc) (intmodel.CellConfig, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		canonical := in
		canonical.APIVersion = ext.APIVersionV1Beta1
		canonical.Kind = ext.KindCellConfig
		document, err := yaml.Marshal(canonical)
		if err != nil {
			return intmodel.CellConfig{}, fmt.Errorf("marshal CellConfig document: %w", err)
		}
		return intmodel.CellConfig{
			Metadata: intmodel.CellConfigMetadata{
				Name:   in.Metadata.Name,
				Realm:  in.Metadata.Realm,
				Space:  in.Metadata.Space,
				Stack:  in.Metadata.Stack,
				Labels: copyLabels(in.Metadata.Labels),
			},
			Document: document,
		}, nil
	default:
		return intmodel.CellConfig{}, fmt.Errorf("unsupported apiVersion for CellConfig: %s", in.APIVersion)
	}
}

// NormalizeCellConfig takes an external CellConfigDoc request and returns an
// internal carrier and the chosen apiVersion.
func NormalizeCellConfig(req ext.CellConfigDoc) (intmodel.CellConfig, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertCellConfigDocToInternal(req)
	if err != nil {
		return intmodel.CellConfig{}, "", err
	}
	return internal, version, nil
}

// ConvertCellConfigToExternal reconstructs the external CellConfigDoc from the
// internal carrier's stored Document (issue #624). It is the inverse of
// ConvertCellConfigDocToInternal, used by ReconcileConfig to validate slot fills
// against the referenced blueprint and (in #625) by `kuke run -c`.
func ConvertCellConfigToExternal(in intmodel.CellConfig) (ext.CellConfigDoc, error) {
	var doc ext.CellConfigDoc
	if err := yaml.Unmarshal(in.Document, &doc); err != nil {
		return ext.CellConfigDoc{}, fmt.Errorf("unmarshal CellConfig document: %w", err)
	}
	return doc, nil
}

// ConvertCellConfigMetadataToExternal builds a metadata-only external
// CellConfigDoc from the internal carrier's metadata (issue #644). Unlike
// ConvertCellConfigToExternal it does not read the stored Document — the list
// and delete verbs surface scope coordinates and name only, never the config
// body (blueprint ref, values, slot fills).
func ConvertCellConfigMetadataToExternal(in intmodel.CellConfig) ext.CellConfigDoc {
	return ext.CellConfigDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellConfig,
		Metadata: ext.CellConfigMetadata{
			Name:   in.Metadata.Name,
			Realm:  in.Metadata.Realm,
			Space:  in.Metadata.Space,
			Stack:  in.Metadata.Stack,
			Labels: copyLabels(in.Metadata.Labels),
		},
	}
}

// ConvertCellConfigListToExternal maps a slice of internal CellConfigs to
// metadata-only external CellConfigDocs (issue #644).
func ConvertCellConfigListToExternal(in []intmodel.CellConfig) []ext.CellConfigDoc {
	if in == nil {
		return nil
	}
	out := make([]ext.CellConfigDoc, len(in))
	for i := range in {
		out[i] = ConvertCellConfigMetadataToExternal(in[i])
	}
	return out
}
