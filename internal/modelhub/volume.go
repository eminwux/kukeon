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
// volume (kind: Volume, issue #1018). The resource itself is the on-host
// directory the daemon provisions — not a serialized document round-tripped
// back out — so the carrier holds the scope coordinates and name (which the
// storage runner resolves to, and derives from, the on-disk path) plus the
// small Spec introduced in step 3 (#1237): the reclaim policy a cascade purge
// consults. Unlike CellBlueprint/CellConfig there is still no Document.
type Volume struct {
	Metadata VolumeMetadata
	Spec     VolumeSpec
}

// VolumeSpec carries the volume's declarative configuration. Its only field is
// the reclaim policy (step 3, #1237); an empty ReclaimPolicy means the step-1
// delete-with-scope behavior. See external v1beta1.VolumeSpec for the contract.
type VolumeSpec struct {
	ReclaimPolicy ReclaimPolicy
}

// ReclaimPolicy mirrors v1beta1.ReclaimPolicy: the per-Volume policy a cascade
// purge consults to decide whether the volume is reclaimed with its owning
// scope (ReclaimDelete, the empty-value default) or preserved (ReclaimRetain).
type ReclaimPolicy string

const (
	// ReclaimDelete reclaims the volume with its owning scope on cascade purge.
	ReclaimDelete ReclaimPolicy = "Delete"
	// ReclaimRetain preserves the volume across its owning scope's cascade purge.
	ReclaimRetain ReclaimPolicy = "Retain"
)

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
