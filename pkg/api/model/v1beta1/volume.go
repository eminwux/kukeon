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

// VolumeDoc is the top-level document for a standalone, daemon-managed storage
// volume (kind: Volume, issue #1018, step 1 of the volumes epic #1015).
// Reshaped to the Docker model: a Volume is decoupled from any cell and owns
// its own lifecycle. Like the realm/space/stack/cell hierarchy it carries no
// Status — its on-host presence is the directory the daemon creates under the
// scope's metadata tree, container-writable so a mounting container can write
// into it (the mount kind that references a Volume is step 4, #1016). This
// step ships only the schema + storage primitive + `kuke apply`; the
// imperative `kuke create/get/delete volume` verbs (#1236) and `reclaimPolicy:
// Retain` + selective cascade (#1237) build on this foundation.
type VolumeDoc struct {
	APIVersion Version        `json:"apiVersion"     yaml:"apiVersion"`
	Kind       Kind           `json:"kind"           yaml:"kind"`
	Metadata   VolumeMetadata `json:"metadata"       yaml:"metadata"`
	Spec       VolumeSpec     `json:"spec,omitempty" yaml:"spec,omitempty"`
}

// VolumeMetadata identifies a Volume by name and the scope it is bound to. Like
// a CellBlueprint — and unlike a Secret — a Volume is scopable at realm, space,
// or stack only, never cell: a volume must outlive the cells that mount it, so
// binding it to a single cell would let the cell's deletion reclaim it (#1018).
// The scope is the deepest non-empty coordinate; a deeper coordinate requires
// every shallower one (a stack-scoped Volume must also name its space and
// realm). Realm is always required. There is deliberately no Cell field — a
// `cell:` coordinate is structurally unrepresentable, the same way
// CellBlueprintMetadata rejects cell scope.
type VolumeMetadata struct {
	// Name is the volume's name, unique within its scope.
	Name string `json:"name"            yaml:"name"`
	// Realm is the always-required top-level scope coordinate.
	Realm string `json:"realm"           yaml:"realm"`
	// Space, when set, scopes the volume to a space within Realm.
	Space string `json:"space,omitempty" yaml:"space,omitempty"`
	// Stack, when set, scopes the volume to a stack within Space.
	Stack string `json:"stack,omitempty" yaml:"stack,omitempty"`
}

// VolumeSpec carries the volume's declarative configuration. It is empty in
// step 1 (#1018): the volume's identity is its scope plus its name, and the
// resource is the directory the daemon provisions. The first spec field —
// `reclaimPolicy: Retain` — lands in step 3 (#1237), where it reworks cascade
// purge from the blunt scope-dir RemoveAll to enumerate-and-selectively-
// preserve. The struct exists now so that addition is a field append rather
// than a schema reshape.
type VolumeSpec struct{}
