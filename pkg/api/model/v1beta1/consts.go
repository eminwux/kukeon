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
	// KindCellBlueprint identifies daemon-stored, scopable parametrized cell
	// templates (issue #620, phase 4a-i of #423). `kuke apply` writes the
	// blueprint to a root-owned, world-readable file under the scope's
	// metadata tree; `kuke run -b` resolves it from daemon storage and
	// materializes a fresh `<prefix>-<6hex>` cell. The get/delete verbs
	// (#643), CellConfig (#624), and `kuke run -c` (#625) build on this
	// foundation. The legacy client-side CellProfile kind (`kuke run -p`)
	// that originally co-existed was removed in #626 — Blueprint + Config are
	// the only template path now.
	KindCellBlueprint Kind = "CellBlueprint"
	// KindCellConfig identifies a daemon-stored cell identity that binds a
	// CellBlueprint to a concrete instance (issue #624, phase 4b-i of #423). A
	// CellConfig references a Blueprint by name+scope, supplies the scalar
	// values and the structural repo/secret slot fills, and owns the
	// deterministic name of the at-most-one live cell it materializes.
	// `kuke apply` writes it to a root-owned, world-readable file under the
	// scope's metadata tree; the `kuke run -c` verb + identity state machine
	// that runs it land in #625, and the get/delete verbs in #644.
	KindCellConfig Kind = "CellConfig"
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

// Label keys with reserved kukeon.io semantics. Other label keys on a
// resource's metadata.labels are user-controlled and carry no daemon meaning.
const (
	// LabelTeam scopes an applied CellBlueprint / CellConfig to a project
	// (issue #1027). `kuke team init` (#796) stamps it on every object it
	// applies for one project so a repeat init can prune the team's prior
	// objects that the new roster no longer declares — converging the
	// project's slice without touching other teams' objects.
	LabelTeam = "kukeon.io/team"
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
	// StateExitedStr is the display label for a clean self-exit terminal — a
	// cell whose workloads all exited 0, or a container task that exited 0
	// (see CellStateExited / ContainerStateExited, #1267).
	StateExitedStr = "Exited"
	// StateErrorStr is the display label for a workload-crash terminal — a
	// cell with a non-zero workload exit, or a container task that exited
	// non-zero (see CellStateError / ContainerStateError, #1267). Distinct
	// from StateFailedStr, which is reserved for kukeon's own bring-up faults.
	StateErrorStr = "Error"
)
