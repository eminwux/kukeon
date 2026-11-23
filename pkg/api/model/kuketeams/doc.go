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

// Package kuketeams holds the Go types of the source documents a team is
// composed from (issue #793, epic #792; issue #1618). Role, Harness and
// ImageCatalog live in a source repository and are read by kuke from the
// fetched tree:
//
//   - Role (role.yaml) declares a role's skills, per-harness configs, tasks
//     and needs.
//   - Harness (harness.yaml) declares a harness's base image, skill path and
//     make target.
//   - ImageCatalog (harnesses/images.yaml) is the prebuilt image to
//     capability map that is the image selector input.
//
// Every team document is apiVersion v1beta1 (issue #1618). The documents
// kukeond stores — Team and TeamDefaults — are declared in
// pkg/api/model/v1beta1 over the types in pkg/team/config; the source
// documents here are never sent to kukeond, and `kuke apply` rejects them.
// The source documents carry no in-file version field: the ref a source is
// read at (one of branch, tag or commit) is the version authority.
//
// The team-layer types are declarative sugar that render onto the existing
// v1beta1 runtime: Role.needs.repos fill the shared blueprint's repo slots,
// Role.needs.mounts select the blueprint's security profile. This package
// defines the parsed surface only; composition lives in pkg/team/compose and
// application in internal/team/initialize.
package kuketeams

// APIVersionV1 is the apiVersion every team document carries.
const APIVersionV1 = "v1beta1"

// Kinds.
const (
	// KindRole identifies a per-role document (role.yaml) in a source.
	KindRole = "Role"
	// KindHarness identifies a per-harness document (harness.yaml) in a
	// source.
	KindHarness = "Harness"
	// KindImageCatalog identifies the prebuilt-image → capability map
	// (harnesses/images.yaml) in a source — the image selector input.
	KindImageCatalog = "ImageCatalog"
)

// Metadata is the name-only metadata block carried by Role and Harness. The
// source documents intentionally omit a version field — the source's ref is
// the version authority.
type Metadata struct {
	Name string `json:"name" yaml:"name"`
}

// KnownHarnesses is the set of harness names the v1 contract recognizes. Role
// harness keys, Harness.metadata.name, and ImageCatalog images[].harness are all
// validated against this set.
//
//nolint:gochecknoglobals // package-level membership set, read-only.
var KnownHarnesses = map[string]bool{
	"claude":   true,
	"codex":    true,
	"opencode": true,
	"script":   true,
}

// IsKnownHarness reports whether name is a recognized harness.
func IsKnownHarness(name string) bool {
	return KnownHarnesses[name]
}
