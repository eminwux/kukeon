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

const (
	// APIVersionV1Beta1 is the canonical API version for this package.
	APIVersionV1Beta1 Version = "v1beta1"
)

// Kinds.
const (
	// KindCell identifies cell documents.
	KindCell Kind = "Cell"
	// KindContainer identifies container documents.
	KindContainer Kind = "Container"
	// KindRealm identifies realm documents.
	KindRealm Kind = "Realm"
	// KindSpace identifies space documents.
	KindSpace Kind = "Space"
	// KindStack identifies stack documents.
	KindStack Kind = "Stack"
	// KindSecret identifies named, scoped, daemon-managed credential
	// documents (issue #619). `kuke apply` writes the secret bytes to a
	// root-owned file under the scope's metadata tree; phase 3a ships no
	// `get`/`delete`/referencing surface (tracked in #622 and #623).
	KindSecret Kind = "Secret"
	// KindCellProfile identifies per-user cell-template documents loaded by
	// `kuke run -p`. Profiles are not server-side resources — `kuke apply`
	// rejects them.
	KindCellProfile Kind = "CellProfile"
	// KindCellBlueprint identifies daemon-stored, scopable parametrized cell
	// templates (issue #620, phase 4a-i of #423). A *new* kind introduced
	// alongside CellProfile — not a rename. `kuke apply` writes the blueprint
	// to a root-owned, world-readable file under the scope's metadata tree;
	// `kuke run -b` resolves it from daemon storage and materializes a fresh
	// `<prefix>-<6hex>` cell. The get/delete verbs (#643), CellConfig (#624),
	// and `kuke run -c` (#625) build on this foundation.
	KindCellBlueprint Kind = "CellBlueprint"
	// KindServerConfiguration identifies the kukeond daemon's configuration
	// document loaded via `kukeond --configuration` (and consumed by
	// `kuke init --server-configuration`). Not a server-side resource —
	// `kuke apply` rejects it.
	KindServerConfiguration Kind = "ServerConfiguration"
	// KindClientConfiguration identifies the kuke client's configuration
	// document loaded via `kuke --configuration` (default
	// `~/.kuke/kuke.yaml`). Not a server-side resource — `kuke apply`
	// rejects it.
	KindClientConfiguration Kind = "ClientConfiguration"
)

// Common printable state strings.
const (
	StatePendingStr  = "Pending"
	StateReadyStr    = "Ready"
	StateStoppedStr  = "Stopped"
	StatePausedStr   = "Paused"
	StatePausingStr  = "Pausing"
	StateFailedStr   = "Failed"
	StateUnknownStr  = "Unknown"
	StateCreatingStr = "Creating"
	StateDeletingStr = "Deleting"
	// StateNotCreatedStr is the display label for a container with no
	// containerd record at all (see ContainerStateNotCreated).
	StateNotCreatedStr = "NotCreated"
)
