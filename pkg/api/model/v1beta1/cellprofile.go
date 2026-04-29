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
// Prefix is an optional override for the prefix used when generating cell
// names; when unset, the prefix defaults to metadata.name. Every Materialize
// call appends a `-<6hex>` suffix so each invocation produces a fresh cell —
// CellProfile is always a template. Use the Cell kind for singleton workloads.
type CellProfileSpec struct {
	Realm  string   `json:"realm,omitempty"  yaml:"realm,omitempty"`
	Space  string   `json:"space,omitempty"  yaml:"space,omitempty"`
	Stack  string   `json:"stack,omitempty"  yaml:"stack,omitempty"`
	Prefix string   `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Cell   CellSpec `json:"cell"             yaml:"cell"`
}
