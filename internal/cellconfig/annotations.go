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

package cellconfig

import (
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Render provenance annotations the daemon stamps on every cell it renders
// from a CellConfig (issue #1418). Annotations are never compared by
// apply.DiffCell; the OutOfSync reconciler reads the defaults and image ones
// explicitly to name the level that drifted.
const (
	// AnnotationRenderDigest is the digest of the rendered cell (identity,
	// labels and spec). Preview and create of the same inputs produce the
	// same value.
	AnnotationRenderDigest = "kukeon.io/render-digest"
	// AnnotationRenderConfigRevision is a content hash of the CellConfig
	// document the cell was rendered from.
	AnnotationRenderConfigRevision = "kukeon.io/render-config-revision"
	// AnnotationRenderRealmDefaults, AnnotationRenderSpaceDefaults and
	// AnnotationRenderStackDefaults record each level's defaults as
	// "generation=<n> hash=<h>"; hash is a content hash of the level's
	// spec.defaults fill ("none" when the level has none).
	AnnotationRenderRealmDefaults = "kukeon.io/render-realm-defaults"
	AnnotationRenderSpaceDefaults = "kukeon.io/render-space-defaults"
	AnnotationRenderStackDefaults = "kukeon.io/render-stack-defaults"
	// AnnotationRenderImageDigests records the realm images the cell uses as
	// a sorted, comma-separated "<name>=<digest>" list.
	AnnotationRenderImageDigests = "kukeon.io/render-image-digests"
	// AnnotationRenderValuesHash is a content hash of the merged values,
	// repo fills and secret fills.
	AnnotationRenderValuesHash = "kukeon.io/render-values-hash"
	// AnnotationRenderOrigins records which level set each merged value, as
	// a sorted, comma-separated "<kind>:<key>=<origin>" list.
	AnnotationRenderOrigins = "kukeon.io/render-origins"
	// AnnotationRenderKukeonVersion is the kukeon version that rendered the
	// cell.
	AnnotationRenderKukeonVersion = "kukeon.io/render-kukeon-version"
)

// RealmImageRef returns the image reference a RealmImage resolves to: the
// entry's Ref, its Build.Tag, or kukeon.internal/<realm>/<name>:latest for a
// Dockerfile entry with no explicit tag.
func RealmImageRef(realm string, img v1beta1.RealmImage) string {
	if ref := strings.TrimSpace(img.Ref); ref != "" {
		return ref
	}
	if img.Build != nil {
		if tag := strings.TrimSpace(img.Build.Tag); tag != "" {
			return tag
		}
	}
	return consts.InternalImageRef(strings.TrimSpace(realm)+"/"+strings.TrimSpace(img.Name), "latest")
}

// FindRealmImage returns the realm image entry named name, if any.
func FindRealmImage(images []v1beta1.RealmImage, name string) (v1beta1.RealmImage, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return v1beta1.RealmImage{}, false
	}
	for _, img := range images {
		if strings.TrimSpace(img.Name) == name {
			return img, true
		}
	}
	return v1beta1.RealmImage{}, false
}
