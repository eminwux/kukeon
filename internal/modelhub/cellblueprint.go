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

// CellBlueprint is the internal carrier for a daemon-stored cell template
// (kind: CellBlueprint, issue #620). Unlike the hierarchy kinds it has no
// Status: a Blueprint has no runtime, and the daemon never interprets its
// body. The body travels as an opaque serialized Document — the direct analog
// of how Secret carries opaque Spec.Data — because the only daemon-side
// operations are "write this document under the scope" and "read it back";
// materialization (scalar substitution + slot mapping) happens client-side in
// `kuke run -b`. Carrying a fully-typed mirror of the cell template here would
// duplicate the entire ContainerSpec conversion for no daemon-side benefit.
//
// Metadata's scope coordinates are extracted from the document so the storage
// runner can resolve the on-disk path without parsing the body; Document holds
// the canonical serialized CellBlueprintDoc that GetBlueprint round-trips back
// out for materialization.
type CellBlueprint struct {
	Metadata CellBlueprintMetadata
	Document []byte
}

// CellBlueprintMetadata identifies a Blueprint by name and the scope it binds
// to. A Blueprint is scopable at realm, space, or stack only — never cell. The
// scope is the deepest non-empty coordinate; a deeper coordinate requires
// every shallower one. See external v1beta1.CellBlueprintMetadata for the full
// contract.
type CellBlueprintMetadata struct {
	Name  string
	Realm string
	Space string
	Stack string
	// Labels lifts the blueprint document's metadata.labels onto the carrier
	// so daemon-side code can filter (`kukeon.io/team=<team>` for
	// per-team prune apply, #1027) without re-parsing the Document on every
	// access. ListBlueprints populates it; WriteBlueprint preserves it via
	// the Document round-trip — the labels travel inside the canonical
	// serialized doc, the carrier just lifts a typed view.
	Labels map[string]string
}
