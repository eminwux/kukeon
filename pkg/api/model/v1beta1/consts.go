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

import "github.com/eminwux/kukeon/pkg/api/version"

const (
	// APIVersionV1Beta1 is the canonical API version for this package. It is
	// declared once, in pkg/api/version.
	APIVersionV1Beta1 Version = version.V1Beta1
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
	// KindVolume identifies a standalone, daemon-managed storage volume
	// (issue #1018, step 1 of the volumes epic #1015). A Volume is decoupled
	// from any cell: `kuke apply` creates a root-owned, container-writable
	// directory under the scope's metadata tree, scopable at realm/space/stack
	// only — never cell — so it outlives the cells that mount it. Reclaimed by
	// owning-scope (stack/space/realm) cascade purge, not by cell deletion. The
	// imperative create/get/delete verbs are step 2 (#1236), `reclaimPolicy:
	// Retain` is step 3 (#1237), and the container-side mount kind that
	// references a Volume is step 4 (#1016).
	KindVolume Kind = "Volume"
	// KindHealthReport identifies one triaged health sweep (issue #1619). A
	// report lives in the kuke-system realm and is stored as the validated
	// document under that realm's metadata tree. It is immutable: kukeond
	// assigns its name and createdAt when they are left out, and a stored
	// name cannot be applied again. kukeond keeps the newest 50.
	KindHealthReport Kind = "HealthReport"
	// KindHealthDigest identifies kukeond's deterministic health digest
	// (`kuke health -o json|yaml`, issue #1633). Computed by kukeond on
	// request, never stored. Not a server-side resource — `kuke apply`
	// rejects it.
	KindHealthDigest Kind = "HealthDigest"
	// KindTeam identifies a team's declaration (issue #1618): its project
	// source, recorded secret bindings and role, harness, source and
	// connector selections. Realm-scoped. kukeond validates and stores it;
	// kuke composes it into CellBlueprints and CellConfigs, and kukeond
	// accepts it only in the team apply that carries them. A repository's
	// kuketeam.yaml is a Team document.
	KindTeam Kind = "Team"
	// KindTeamDefaults identifies the settings every team of a realm
	// inherits (issue #1618): default roles and harnesses, connectors,
	// parameters and secret bindings. Realm-scoped, at most one per realm,
	// named default.
	KindTeamDefaults Kind = "TeamDefaults"
	// KindSource identifies a stored repository (issue #1643): an address,
	// a ref and an optional git identity. Realm-scoped, owned by no team;
	// a Team, a default role or a default harness names it.
	KindSource Kind = "Source"
	// KindRole identifies a role definition (role.yaml) kuke reads from a
	// fetched team source. Not a server-side resource — `kuke apply`
	// rejects it.
	KindRole Kind = "Role"
	// KindHarness identifies a harness definition (harness.yaml) kuke reads
	// from a fetched team source. Not a server-side resource — `kuke apply`
	// rejects it.
	KindHarness Kind = "Harness"
	// KindHarnessImageCatalog identifies a harness's image catalog
	// (images.yaml) kuke reads from a fetched team source. Not a
	// server-side resource — `kuke apply` rejects it.
	KindHarnessImageCatalog Kind = "HarnessImageCatalog"
	// KindComponent identifies a component definition kuke reads from its
	// component library. Not a server-side resource — `kuke apply` rejects
	// it.
	KindComponent Kind = "Component"
	// KindConnector identifies a connector definition kuke reads from its
	// connector catalog. Not a server-side resource — `kuke apply` rejects
	// it.
	KindConnector Kind = "Connector"
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
	// LabelIdentityCell marks an identity cell: the short-lived cell that
	// mounts one login identity — a Volume tagged with its connector — and
	// runs the harness login (issue #1685). Its value is the Volume's name
	// and scope, `name@realm[/space[/stack]]`, so a client finds a login
	// session already running for that Volume and attaches to it instead of
	// starting a second one, and two Volumes of one name at different scopes
	// never share a session.
	LabelIdentityCell = "kukeon.io/identity-cell"
	// LabelIdentitySession tells apart the starts of one identity cell: the
	// client that starts it stamps a value of its own, so a start that fails
	// removes only the cell it created, never one another client started.
	LabelIdentitySession = "kukeon.io/identity-session"
	// LabelIdentityConnector names, on an identity cell, the connector whose
	// login the cell runs.
	LabelIdentityConnector = "kukeon.io/identity-connector"
	// LabelTask names the role task a `kuke team` task CellConfig runs
	// headless (issue #1491). It sits beside LabelTeam, so a task config is
	// pruned with the team's other configs once its task is deselected.
	LabelTask = "kukeon.io/task"
	// LabelRole names the team role a `kuke team` CellConfig runs (issue
	// #1509): the role's name in the team declaration. Pair and task configs
	// both carry it, so a role's configs are selected by label, not by parsing
	// the config name.
	LabelRole = "kukeon.io/role"
	// LabelHarness names the team harness a `kuke team` CellConfig runs on
	// (issue #1668): the harness's name in the team declaration. Pair and
	// task configs both carry it beside LabelRole, so a task's per-harness
	// Configs are told apart by label, not by parsing the config name.
	LabelHarness = "kukeon.io/harness"
	// LabelSchedule names the CellConfig whose spec.schedule fired the cell
	// (issue #1503). Only cells kukeond's scheduler creates carry it: a cell
	// started by hand from the same Config does not, so it neither blocks a
	// Forbid schedule nor is blocked by one.
	LabelSchedule = "kukeon.io/schedule"
	// LabelShared marks a CellBlueprint as shared: a realm-wide form any
	// team's (or hand-written) CellConfig may reference, e.g. the one
	// blueprint per harness × security profile `kuke team` renders (issue
	// #1472). A shared blueprint is never stamped with LabelTeam; a team apply
	// removes it only once no CellConfig references it any more.
	LabelShared = "kukeon.io/shared"
	// LabelContentDigest carries a digest of a shared blueprint's rendered
	// content so `kuke team` can tell "same blueprint, already applied" from
	// "same name, different content" without comparing documents.
	LabelContentDigest = "kukeon.io/content-digest"
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
	// StateDegradedStr is the display label for a cell whose root/sandbox is up
	// but at least one non-root workload is down in a non-clean way — partial
	// health, non-terminal (see CellStateDegraded).
	StateDegradedStr = "Degraded"
)

// ImageLabelCapabilities is the OCI image config label listing the
// capabilities an image provides, comma-separated (e.g. "claude,git,gh,go").
// Harness image builds stamp it from harness.yaml images[].capabilities; the
// daemon checks a cell's requires against it at container create, and the web
// UI filters its image picker on it. Issue #1472, step 9.
const ImageLabelCapabilities = "io.kukeon.capabilities"
