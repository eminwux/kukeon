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

package modelhub

// Volume is the internal carrier for a standalone, daemon-managed storage
// volume (kind: Volume, issue #1018). Unlike CellBlueprint/CellConfig it
// carries no Document: a Volume's spec is empty in step 1, and the resource
// itself is the on-host directory the daemon provisions — not a serialized
// document round-tripped back out. Like a Secret's metadata-only view, the
// carrier holds only the scope coordinates and name, which the storage runner
// resolves to (and derives from) the on-disk path. When `reclaimPolicy` lands
// (step 3, #1237) a Spec field will join the carrier.
type Volume struct {
	Metadata VolumeMetadata
}

// VolumeMetadata identifies a Volume by name and the scope it binds to. A
// Volume is scopable at realm, space, or stack only — never cell. The scope is
// the deepest non-empty coordinate; a deeper coordinate requires every
// shallower one. See external v1beta1.VolumeMetadata for the full contract.
type VolumeMetadata struct {
	Name  string
	Realm string
	Space string
	Stack string
}
