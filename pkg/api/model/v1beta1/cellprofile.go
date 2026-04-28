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

package v1beta1

// CellProfileDoc is a per-user reusable cell template loaded from
// $HOME/.kuke/profiles.d/<name>.yaml (or $KUKE_PROFILES_DIR). It is never sent
// to the daemon: `kuke run -p` materializes a CellDoc from it locally and
// proceeds along the same `-f` path.
type CellProfileDoc struct {
	APIVersion Version             `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind                `json:"kind"       yaml:"kind"`
	Metadata   CellProfileMetadata `json:"metadata"   yaml:"metadata"`
	Spec       CellProfileSpec     `json:"spec"       yaml:"spec"`
}

type CellProfileMetadata struct {
	Name   string            `json:"name"             yaml:"name"`
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// CellProfileSpec carries the location triple (realm/space/stack) plus a
// CellSpec body. The location uses the user-facing names rather than internal
// IDs so a profile is portable between hosts.
//
// NamePrefix toggles the profile between singleton and template modes:
// when empty, `kuke run -p` materializes a cell named after metadata.name and
// the same invocation is idempotent. When set, every invocation generates a
// fresh `<NamePrefix>-<6hex>` cell so the profile behaves like a template
// (mirrors K8s `metadata.generateName` and sbsh's "every start is fresh").
type CellProfileSpec struct {
	Realm      string   `json:"realm,omitempty"      yaml:"realm,omitempty"`
	Space      string   `json:"space,omitempty"      yaml:"space,omitempty"`
	Stack      string   `json:"stack,omitempty"      yaml:"stack,omitempty"`
	NamePrefix string   `json:"namePrefix,omitempty" yaml:"namePrefix,omitempty"`
	Cell       CellSpec `json:"cell"                 yaml:"cell"`
}
