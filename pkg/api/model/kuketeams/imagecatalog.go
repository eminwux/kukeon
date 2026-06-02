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

package kuketeams

// ImageCatalog is the prebuilt-image → capability map (harnesses/images.yaml)
// authored in the agents source. It is the v1 image selector input: a role's
// needs.image capability names are matched against entries' capabilities to pick
// the image. Each entry also carries build provenance so "how the image is
// built" is defined at the contract layer. ImageCatalog is spec-only — it has no
// metadata block.
type ImageCatalog struct {
	APIVersion string           `json:"apiVersion" yaml:"apiVersion"`
	Kind       string           `json:"kind"       yaml:"kind"`
	Spec       ImageCatalogSpec `json:"spec"       yaml:"spec"`
}

// ImageCatalogSpec carries the image entries.
type ImageCatalogSpec struct {
	Images []ImageCatalogEntry `json:"images" yaml:"images"`
}

// ImageCatalogEntry is one prebuilt image and the capabilities it provides.
type ImageCatalogEntry struct {
	// Ref is the catalog-local identifier for the image (selector key).
	Ref string `json:"ref"          yaml:"ref"`
	// Harness is the harness this image is built for. Must be a known harness.
	Harness string `json:"harness"      yaml:"harness"`
	// Image is the registry-qualified image reference
	// (e.g. registry.eminwux.com/claude:latest).
	Image string `json:"image"        yaml:"image"`
	// Build carries the build provenance (context + dockerfile) for the image.
	Build ImageCatalogBuild `json:"build"        yaml:"build"`
	// Capabilities is the non-empty list of capability names this image
	// provides — the values matched against a role's needs.image.
	Capabilities []string `json:"capabilities" yaml:"capabilities"`
}

// ImageCatalogBuild is the build provenance for a catalog image.
type ImageCatalogBuild struct {
	// Context is the build context directory (relative to the agents source).
	Context string `json:"context"    yaml:"context"`
	// Dockerfile is the Dockerfile path within the context.
	Dockerfile string `json:"dockerfile" yaml:"dockerfile"`
}
