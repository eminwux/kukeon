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

// CellConfig is the internal carrier for a daemon-stored cell identity (kind:
// CellConfig, issue #624). Like CellBlueprint it has no Status and the daemon
// never interprets its body for storage: the canonical CellConfigDoc travels as
// an opaque serialized Document — the daemon writes it under the scope and
// reads it back. Scope coordinates are lifted onto the metadata so the storage
// runner can resolve the on-disk path without parsing the body. Apply-time
// slot-fill validation (which does need the body and the referenced blueprint)
// re-derives the typed view from Document via apischeme.
type CellConfig struct {
	Metadata CellConfigMetadata
	Document []byte
}

// CellConfigMetadata identifies a Config by name and the scope it binds to. A
// Config is scopable at realm, space, or stack only — never cell. The scope is
// the deepest non-empty coordinate; a deeper coordinate requires every
// shallower one. See external v1beta1.CellConfigMetadata for the full contract.
type CellConfigMetadata struct {
	Name  string
	Realm string
	Space string
	Stack string
}
