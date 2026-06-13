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
)

// ConvertVolumeDocToInternal converts an external VolumeDoc to the internal hub
// carrier (issue #1018). The resource is the on-host directory, so this is a
// flat copy of the scope coordinates onto the metadata, plus the reclaim policy
// from the spec (step 3, #1237) — the one declarative field a Volume carries.
func ConvertVolumeDocToInternal(in ext.VolumeDoc) (intmodel.Volume, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		return intmodel.Volume{
			Metadata: intmodel.VolumeMetadata{
				Name:  in.Metadata.Name,
				Realm: in.Metadata.Realm,
				Space: in.Metadata.Space,
				Stack: in.Metadata.Stack,
			},
			Spec: intmodel.VolumeSpec{
				ReclaimPolicy: intmodel.ReclaimPolicy(in.Spec.ReclaimPolicy),
			},
		}, nil
	default:
		return intmodel.Volume{}, fmt.Errorf("unsupported apiVersion for Volume: %s", in.APIVersion)
	}
}

// NormalizeVolume takes an external VolumeDoc request and returns an internal
// carrier and the chosen apiVersion.
func NormalizeVolume(req ext.VolumeDoc) (intmodel.Volume, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertVolumeDocToInternal(req)
	if err != nil {
		return intmodel.Volume{}, "", err
	}
	return internal, version, nil
}

// ConvertVolumeToExternal builds an external VolumeDoc from the internal
// carrier (issue #1018). A Volume has no body, so the external doc is its
// canonical apiVersion/kind plus the scope coordinates and name, plus the
// reclaim policy the runner reads back from the volume's on-disk manifest
// (step 3, #1237) so `kuke get volume -o yaml` surfaces a retained volume's
// policy.
func ConvertVolumeToExternal(in intmodel.Volume) ext.VolumeDoc {
	return ext.VolumeDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindVolume,
		Metadata: ext.VolumeMetadata{
			Name:  in.Metadata.Name,
			Realm: in.Metadata.Realm,
			Space: in.Metadata.Space,
			Stack: in.Metadata.Stack,
		},
		Spec: ext.VolumeSpec{
			ReclaimPolicy: ext.ReclaimPolicy(in.Spec.ReclaimPolicy),
		},
	}
}

// ConvertVolumeListToExternal maps a slice of internal Volumes to metadata-only
// external VolumeDocs (issue #1018).
func ConvertVolumeListToExternal(in []intmodel.Volume) []ext.VolumeDoc {
	if in == nil {
		return nil
	}
	out := make([]ext.VolumeDoc, len(in))
	for i := range in {
		out[i] = ConvertVolumeToExternal(in[i])
	}
	return out
}
