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

// Package kukeonv1 is the public client SDK for the kukeon daemon.
// It exposes a transport-agnostic Client interface; Dial picks a concrete
// implementation (unix socket JSON-RPC today, ssh tunnel in the future)
// based on the address scheme.
package kukeonv1

import (
	"context"
	"io"
	"net"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Client is the transport-agnostic interface for programmatic access to
// kukeon primitives. All wire types are drawn from pkg/api/model/v1beta1.
type Client interface {
	io.Closer

	CreateRealm(ctx context.Context, doc v1beta1.RealmDoc) (CreateRealmResult, error)
	CreateSpace(ctx context.Context, doc v1beta1.SpaceDoc) (CreateSpaceResult, error)
	CreateStack(ctx context.Context, doc v1beta1.StackDoc) (CreateStackResult, error)
	CreateCell(ctx context.Context, doc v1beta1.CellDoc) (CreateCellResult, error)
	// MaterializeCell creates a cell record (or ensures an existing cell's
	// resources exist) without starting any container tasks (#818). The
	// resulting cell is left stopped; the operator runs `kuke start <name>`
	// to start it. Distinct from CreateCell (which always starts) — used by
	// `kuke create cell --from-blueprint` / `--from-config` for the
	// materialise-but-don't-start scaffolding modes. Result shape matches
	// CreateCell so the same printer renders the outcome (Started will be
	// false because the start step was skipped).
	MaterializeCell(ctx context.Context, doc v1beta1.CellDoc) (CreateCellResult, error)
	// PreviewCellFromConfig renders the cell a stored CellConfig would
	// create — Realm → Space → Stack defaults merged under the Config's own
	// fill, realm images resolved — and returns it with its digest and the
	// origin of every merged value, without writing anything (#1418). The
	// daemon is the only CellConfig renderer.
	PreviewCellFromConfig(ctx context.Context, req CellFromConfigRequest) (PreviewCellFromConfigResult, error)
	// CreateCellFromConfig renders a cell from a stored CellConfig and creates
	// it, started (req.Start) or stopped (#1418). A non-empty
	// req.ExpectedDigest refuses the create unless the fresh render matches
	// it (errdefs.ErrRenderDigestMismatch).
	CreateCellFromConfig(ctx context.Context, req CellFromConfigRequest) (CreateCellFromConfigResult, error)
	// DiffCellFromConfig renders a Config-lineage cell from its lineage
	// Config, as the drift check does, and returns the stored cell beside the
	// render overlaid on it, without writing anything (#1768). The documents
	// are unmasked. A cell without a lineage Config returns
	// errdefs.ErrCellNotFromConfig.
	DiffCellFromConfig(ctx context.Context, doc v1beta1.CellDoc) (DiffCellFromConfigResult, error)
	// CreateConfig atomically persists a new CellConfig document under
	// create-only semantics (issue #839): the daemon-side write fails with
	// errdefs.ErrConfigExists if a Config of the same name already lives in
	// scope. This is the create-or-fail sibling of ApplyDocuments — used by
	// `kuke create config` and any future client that needs "first writer
	// wins" rather than the write-through apply path. The same scope /
	// blueprint / slot-fill validation as ApplyDocuments runs first — the
	// only difference is the atomic gate (which ApplyDocuments doesn't need,
	// since apply is intentionally write-through).
	CreateConfig(ctx context.Context, doc v1beta1.CellConfigDoc) (CreateConfigResult, error)
	// CreateSecret persists a new Secret document to daemon storage. The
	// write is create-or-overwrite (the daemon's runner.WriteSecret is
	// write-through). The resulting SecretDoc is metadata-only (Spec.Data
	// is never echoed back).
	CreateSecret(ctx context.Context, doc v1beta1.SecretDoc) (CreateSecretResult, error)
	// CreateVolume provisions a new Volume's daemon-managed directory (issue
	// #1236). The write is idempotent (the daemon's runner.WriteVolume re-
	// asserts the directory mode/owner), and the resulting VolumeDoc is
	// metadata-only. A Volume is never cell-scoped, so the doc carries
	// realm/space/stack coordinates only — cell scope is unrepresentable.
	CreateVolume(ctx context.Context, doc v1beta1.VolumeDoc) (CreateVolumeResult, error)

	GetRealm(ctx context.Context, doc v1beta1.RealmDoc) (GetRealmResult, error)
	GetSpace(ctx context.Context, doc v1beta1.SpaceDoc) (GetSpaceResult, error)
	GetStack(ctx context.Context, doc v1beta1.StackDoc) (GetStackResult, error)
	GetCell(ctx context.Context, doc v1beta1.CellDoc) (GetCellResult, error)
	GetContainer(ctx context.Context, doc v1beta1.ContainerDoc) (GetContainerResult, error)
	// GetCgroupDetail reads one realm, space, stack, cell or container
	// cgroup's declared configuration plus its live cgroup v2 interface
	// values (issue #1410). Read-only; callers never touch /sys/fs/cgroup.
	GetCgroupDetail(ctx context.Context, scope CgroupScope) (GetCgroupDetailResult, error)
	// GetNetworkDetail reads one space, cell or container's declared
	// networking plus the runtime facts kukeond knows: subnet, bridge,
	// conflist presence and the cell IPv4 (issue #1411). Read-only.
	GetNetworkDetail(ctx context.Context, scope NetworkScope) (GetNetworkDetailResult, error)
	// ListImages and GetImage read a realm's containerd image inventory
	// (issue #1406). Read-only and served over RPC only so `kuke serve`
	// can browse images through kukeond; the `kuke image *` / `kuke get
	// image` CLI paths stay daemon-independent and in-process (#226).
	ListImages(ctx context.Context, realm string) (ListImagesResult, error)
	GetImage(ctx context.Context, realm, ref string) (GetImageResult, error)
	// GetSecret reports the metadata-only view of a single named, scoped
	// `kind: Secret` (issue #622). The material is never set; ReadSecret is
	// the only method that carries it.
	GetSecret(ctx context.Context, doc v1beta1.SecretDoc) (GetSecretResult, error)
	// ReadSecret returns a single named, scoped Secret with its material
	// (issue #1652): the read-back behind a named `kuke get secret -o
	// yaml|json` and the `kuke serve` login check. An absent secret reports
	// MetadataExists=false. A daemon that predates it answers with
	// errdefs.ErrDaemonMethodUnsupported.
	ReadSecret(ctx context.Context, doc v1beta1.SecretDoc) (GetSecretResult, error)
	// GetBlueprint reads one named, scoped `kind: CellBlueprint`'s full
	// document from daemon storage (issue #620). Unlike GetSecret the whole
	// document is returned — a blueprint carries only template references —
	// so `kuke run -b` can materialize it. The input doc carries the name and
	// scope coordinates only; the spec is ignored.
	GetBlueprint(ctx context.Context, doc v1beta1.CellBlueprintDoc) (GetBlueprintResult, error)
	// GetVolume reports the metadata-only view of a single named, scoped
	// `kind: Volume` (issue #1236). A Volume carries no body, so only its
	// scope coordinates and name are returned.
	GetVolume(ctx context.Context, doc v1beta1.VolumeDoc) (GetVolumeResult, error)
	// GetConfig reads one named, scoped `kind: CellConfig`'s full document
	// from daemon storage (issue #644). Like GetBlueprint the whole document
	// is returned — a Config carries no credential bytes — so the blueprint
	// ref, values, and slot fills surface to `kuke get config`. The input doc
	// carries the name and scope coordinates only; the spec is ignored.
	GetConfig(ctx context.Context, doc v1beta1.CellConfigDoc) (GetConfigResult, error)

	ListRealms(ctx context.Context) ([]v1beta1.RealmDoc, error)
	ListSpaces(ctx context.Context, realmName string) ([]v1beta1.SpaceDoc, error)
	ListStacks(ctx context.Context, realmName, spaceName string) ([]v1beta1.StackDoc, error)
	ListCells(ctx context.Context, realmName, spaceName, stackName string) ([]v1beta1.CellDoc, error)
	ListContainers(
		ctx context.Context,
		realmName, spaceName, stackName, cellName string,
	) ([]v1beta1.ContainerSpec, error)
	// ListSecrets enumerates the metadata of every Secret bound to the
	// filter scope or any scope nested within it (issue #622). An empty
	// realmName lists across all realms. spec.data is never echoed.
	ListSecrets(ctx context.Context, realmName, spaceName, stackName, cellName string) ([]v1beta1.SecretDoc, error)
	// ListBlueprints enumerates the metadata of every CellBlueprint bound to
	// the filter scope or any scope nested within it (issue #643). An empty
	// realmName lists across all realms. A Blueprint is never cell-scoped, so
	// there is no cell coordinate. The spec is not populated for a list.
	ListBlueprints(ctx context.Context, realmName, spaceName, stackName string) ([]v1beta1.CellBlueprintDoc, error)
	// ListVolumes enumerates the metadata of every Volume bound to the filter
	// scope or any scope nested within it (issue #1236). An empty realmName
	// lists across all realms. A Volume is never cell-scoped, so there is no
	// cell coordinate.
	ListVolumes(ctx context.Context, realmName, spaceName, stackName string) ([]v1beta1.VolumeDoc, error)
	// ListConfigs enumerates the metadata of every CellConfig bound to the
	// filter scope or any scope nested within it (issue #644). An empty
	// realmName lists across all realms. A Config is never cell-scoped, so
	// there is no cell coordinate. The spec is not populated for a list.
	ListConfigs(ctx context.Context, realmName, spaceName, stackName string) ([]v1beta1.CellConfigDoc, error)

	StartCell(ctx context.Context, doc v1beta1.CellDoc) (StartCellResult, error)
	// AttachContainer is the placeholder endpoint shipped in #57. It only
	// validates that the target container has Attachable=true; the
	// terminal-bridge client lands in #66.
	AttachContainer(ctx context.Context, doc v1beta1.ContainerDoc) (AttachContainerResult, error)
	// LogContainer validates that the target container has Attachable=true
	// and resolves the host-side path of the per-container sbsh capture
	// file (or, for a non-Attachable container, the runtime-shim log).
	// Bytes never traverse this RPC — a caller with filesystem access to
	// the daemon's storage reads the file directly. `kuke log` uses
	// ReadContainerLog instead and falls back to this only against a
	// kukeond that predates it.
	LogContainer(ctx context.Context, doc v1beta1.ContainerDoc) (LogContainerResult, error)
	// ReadContainerLog returns one bounded chunk of the target container's
	// output stream — the file LogContainer resolves — as raw bytes, so a
	// caller without filesystem access to the daemon's storage (`kuke log`
	// in a cell that mounts only the kukeond socket) can read and follow it
	// over the socket (issue #1537). It surfaces the same not-found errors
	// as LogContainer, and ErrContainerLogUnavailable when the container
	// exists but its stream file does not. Read-only; see
	// ContainerLogRequest for the cursor contract.
	ReadContainerLog(ctx context.Context, doc v1beta1.ContainerDoc, req ContainerLogRequest) (ContainerLogResult, error)
	// ConnectContainerTerminal returns a connection to the target
	// container's sbsh control socket, so a caller without filesystem
	// access to the socket (a cell holding only the kukeond socket mount)
	// can still speak sbsh's protocol to it. The daemon applies the same
	// gates as AttachContainer (ErrAttachNotSupported,
	// ErrAttachTaskNotRunning), dials the socket itself, and passes the
	// connected descriptor back over the kukeond socket (SCM_RIGHTS);
	// terminal bytes never traverse kukeond. A failed dial surfaces as
	// ErrAttachStaleSocket (socket absent) or ErrAttachPermissionDenied
	// (socket present but not dialable) so callers can errors.Is the
	// class without stat'ing a path they cannot see. Each returned
	// connection carries a single sbsh call; the caller owns and closes
	// it. Local transport only.
	ConnectContainerTerminal(ctx context.Context, doc v1beta1.ContainerDoc) (*net.UnixConn, error)
	// ExecContainer runs a new process inside the running task of the
	// target container and returns one end of a connected socket pair that
	// carries the session (issue #1432). The daemon keeps the other end,
	// starts the process, and passes this end back over the kukeond socket
	// (SCM_RIGHTS), so it works from a cell that mounts only that socket.
	//
	// Gates, in order: an invalid request (no command, Root together with
	// User, an env entry without "=", a terminal size without TTY) is
	// ErrExecInvalidRequest; a container the cell spec does not have is
	// ErrContainerNotFound; a container whose task is not running is
	// ErrExecTaskNotRunning. Any container of the cell may be named,
	// Attachable or not, the root container included.
	//
	// The connection speaks the frame protocol of internal/execstream:
	// stdin, stdin EOF and resize frames from the caller; stdout, stderr
	// and a final exit frame from the daemon. The process is killed when
	// the connection closes before the exit frame; the session does not
	// end with the call's ctx. The caller owns and closes the connection.
	// Local transport only.
	ExecContainer(ctx context.Context, doc v1beta1.ContainerDoc, req ExecRequest) (*net.UnixConn, error)
	// ReadDaemonLogs returns a bounded chunk of kukeond's own stdout/stderr
	// stream — the same runtime-shim log file `kuke daemon logs` reads —
	// so callers without filesystem access to the daemon's storage (the
	// `kuke serve` web UI) can read and follow it over the socket (issue
	// #1386, step 7). Read-only; see DaemonLogRequest for the cursor contract.
	ReadDaemonLogs(ctx context.Context, req DaemonLogRequest) (DaemonLogResult, error)
	// ReadHealthDigest evaluates kukeond's health checks in the daemon and
	// returns them with the error-level log entries of the window and the
	// cells in a terminal failure state (issue #1509). Read-only.
	ReadHealthDigest(ctx context.Context, req HealthDigestRequest) (HealthDigest, error)
	// SubmitHealthReport validates and stores a triaged health report,
	// keeping only the newest reports. It returns the report as stored.
	SubmitHealthReport(ctx context.Context, report HealthReport) (HealthReport, error)
	// ListHealthReports lists the stored reports, newest first.
	ListHealthReports(ctx context.Context) ([]HealthReportSummary, error)
	// ReadHealthReport returns one stored report by name, or the newest for
	// HealthReportLatest.
	ReadHealthReport(ctx context.Context, id string) (HealthReport, error)
	// DeleteHealthReport deletes one stored report by name (issue #1619).
	DeleteHealthReport(ctx context.Context, name string) error
	StopCell(ctx context.Context, doc v1beta1.CellDoc) (StopCellResult, error)
	KillCell(ctx context.Context, doc v1beta1.CellDoc) (KillCellResult, error)

	DeleteRealm(ctx context.Context, doc v1beta1.RealmDoc, force, cascade bool) (DeleteRealmResult, error)
	DeleteSpace(ctx context.Context, doc v1beta1.SpaceDoc, force, cascade bool) (DeleteSpaceResult, error)
	DeleteStack(ctx context.Context, doc v1beta1.StackDoc, force, cascade bool) (DeleteStackResult, error)
	DeleteCell(ctx context.Context, doc v1beta1.CellDoc) (DeleteCellResult, error)
	// DeleteSecret removes a single named, scoped Secret's daemon-stored
	// file (issue #622). The live-reference safety gate ships in phase 3c.
	DeleteSecret(ctx context.Context, doc v1beta1.SecretDoc) (DeleteSecretResult, error)
	// DeleteBlueprint removes a single named, scoped CellBlueprint's
	// daemon-stored document (issue #643). No live-reference gate: cells
	// materialized from a blueprint are independent copies (#620).
	DeleteBlueprint(ctx context.Context, doc v1beta1.CellBlueprintDoc) (DeleteBlueprintResult, error)
	// DeleteVolume removes a single named, scoped Volume's daemon-provisioned
	// directory (issue #1236). No live-mount gate yet: the container-side mount
	// kind that would reference a Volume lands in step 4 (#1016), where the
	// delete gate against a live mount is exercised.
	DeleteVolume(ctx context.Context, doc v1beta1.VolumeDoc) (DeleteVolumeResult, error)
	// DeleteConfig removes a single named, scoped CellConfig's daemon-stored
	// document (issue #644). No live-reference gate: deleting a Config never
	// deletes the cell it materialized. The result reports any live cells that
	// still carry the back-reference label so the caller can emit a notice.
	DeleteConfig(ctx context.Context, doc v1beta1.CellConfigDoc) (DeleteConfigResult, error)

	// GetTeam reads one stored Team's document and summary (issue #1618).
	// Exists is false, with no error, when none of the name is stored.
	GetTeam(ctx context.Context, ref TeamRef) (GetTeamResult, error)
	// ListTeams lists the stored Teams of a realm, or of every realm when
	// realm is empty.
	ListTeams(ctx context.Context, realm string) ([]GetTeamResult, error)
	// DeleteTeam deletes a team's Configs, the shared blueprints no other
	// Config references, and then its Team document. Cells and volumes stay.
	// The kuke system team is refused.
	DeleteTeam(ctx context.Context, ref TeamRef) (DeleteTeamResult, error)
	// SetDisplayName sets, or for an empty name clears, the display name
	// (v1beta1.AnnotationDisplayName) of a CellConfig or a cell (issue
	// #1692). Only that annotation changes: no cell is restarted, recreated
	// or marked OutOfSync. A daemon that predates it answers with
	// errdefs.ErrDaemonMethodUnsupported.
	SetDisplayName(ctx context.Context, req SetDisplayNameRequest) (SetDisplayNameResult, error)
	// GetTeamDefaults reads a realm's stored TeamDefaults document. Exists is
	// false, with no error, when the realm stores none.
	GetTeamDefaults(ctx context.Context, realm string) (GetTeamDefaultsResult, error)
	// GetSource reads one stored Source's document and summary (issue
	// #1643). Exists is false, with no error, when none of the name is
	// stored.
	GetSource(ctx context.Context, ref SourceRef) (GetSourceResult, error)
	// ListSources lists the stored Sources of a realm, or of every realm
	// when realm is empty.
	ListSources(ctx context.Context, realm string) ([]GetSourceResult, error)
	// DeleteSource deletes a stored Source. It is refused with
	// ErrSourceInUse while a default role or harness, or a stored team,
	// references it.
	DeleteSource(ctx context.Context, ref SourceRef) (DeleteSourceResult, error)
	// StageGitFetch stages the stored git credential a fetch on the host
	// uses (issue #1643, D10) in a directory only the calling user can read,
	// and says where; it never returns the credential's bytes.
	// ReleaseGitFetch removes the staging.
	StageGitFetch(ctx context.Context, req GitFetchRequest) (StageGitFetchResult, error)
	ReleaseGitFetch(ctx context.Context, id string) error

	PurgeRealm(ctx context.Context, doc v1beta1.RealmDoc, force, cascade bool) (PurgeRealmResult, error)
	PurgeSpace(ctx context.Context, doc v1beta1.SpaceDoc, force, cascade bool) (PurgeSpaceResult, error)
	PurgeStack(ctx context.Context, doc v1beta1.StackDoc, force, cascade bool) (PurgeStackResult, error)
	PurgeCell(ctx context.Context, doc v1beta1.CellDoc, force, cascade bool) (PurgeCellResult, error)

	RefreshAll(ctx context.Context) (RefreshAllResult, error)
	ApplyDocuments(ctx context.Context, rawYAML []byte) (ApplyDocumentsResult, error)
	// ApplyDocumentsForTeam is the per-team prune-apply sibling of
	// ApplyDocuments (issue #1027). It stamps every applied CellBlueprint /
	// CellConfig with `kukeon.io/team=<team>` and, after the apply loop,
	// deletes daemon-stored Blueprint / Config objects carrying the same
	// team label that the applied set did not include. Used by `kuke team
	// init` (#796) to make project rosters converge: re-running on a
	// shrunken roster prunes the orphans without touching other teams or
	// running cells (deleting a Blueprint or Config never deletes the cell
	// materialized from it). Empty team is rejected — use ApplyDocuments
	// for the no-team path.
	ApplyDocumentsForTeam(ctx context.Context, rawYAML []byte, team string) (ApplyDocumentsResult, error)
	// DeleteDocuments is the file-driven counterpart to ApplyDocuments —
	// `kuke delete -f` sends the raw YAML over the wire so deletes honor
	// `--host` routing the same way applies do. Per-resource cascade/force
	// semantics match the single-kind Delete{Realm,Space,Stack} methods.
	DeleteDocuments(ctx context.Context, rawYAML []byte, cascade, force bool) (DeleteDocumentsResult, error)

	// NOTE: mutating image operations (LoadImage / DeleteImage /
	// PruneImages) are intentionally NOT on this interface. They are
	// daemon-independent by design (#226) and live on the in-process
	// client (`*local.Client`) only — see the `cmd/kuke/image` package's
	// `Client` interface, which the image subcommands use directly. The
	// RPC daemon serves only the read-only ListImages / GetImage pair
	// above, for the web UI (#1406).

	Ping(ctx context.Context) error
	// PingVersion is the Ping sibling that surfaces the daemon's reported
	// build version alongside the ack. `kuke status` uses it to render the
	// daemon row; the in-process client returns the current process's
	// version (config.Version), so the value is well-defined on both
	// transports. New callers preferring the existing ack-only contract
	// stay on Ping.
	PingVersion(ctx context.Context) (string, error)
}

// CreateCellArgs is the wire request for CreateCell.
type CreateCellArgs struct {
	Doc v1beta1.CellDoc
}

// CreateCellReply is the wire response for CreateCell. Err is non-nil on
// failure so structured errdefs.* sentinels survive the wire roundtrip.
type CreateCellReply struct {
	Result CreateCellResult
	Err    *APIError
}

// MaterializeCellArgs is the wire request for MaterializeCell. The same
// CellDoc shape as CreateCellArgs — the difference is purely server-side
// (the daemon skips the StartCell step). Kept as a distinct type so the
// wire-method dispatch stays unambiguous and the JSON-RPC server's
// reflection-based registration picks it up alongside the other methods.
type MaterializeCellArgs struct {
	Doc v1beta1.CellDoc
}

// MaterializeCellReply is the wire response for MaterializeCell. Mirrors
// CreateCellReply so the same printer renders the outcome.
type MaterializeCellReply struct {
	Result CreateCellResult
	Err    *APIError
}

// CellFromConfigRequest names the stored CellConfig a daemon-side render
// reads, plus the per-cell inputs the caller supplies (#1418).
type CellFromConfigRequest struct {
	// Config locates the CellConfig: name plus the scope it is stored in.
	Config v1beta1.CellConfigMetadata
	// Name is the explicit cell name; empty allocates <prefix>-<6hex>.
	Name string
	// EnvOverrides are per-cell `--env KEY=VALUE` overrides.
	EnvOverrides []string
	// AutoDelete marks the cell for deletion when its attach session ends.
	AutoDelete bool
	// IgnoreDiskPressure bypasses the create-time disk-pressure guard.
	IgnoreDiskPressure bool
	// Start starts the cell after creating it.
	Start bool
	// ExpectedDigest pins the digest of an earlier preview.
	ExpectedDigest string
	// Mode is the harness mode to create the cell in; empty selects the
	// Config's default. Must be declared by the blueprint and enabled by the
	// Config (#1472).
	Mode string
	// Params are per-cell values for the parameters the picked harness mode
	// declares (e.g. the PROMPT of a headless run). They override the
	// Config's values for this cell only; any other key is refused.
	Params map[string]string
	// Draft, on a preview, is an unsaved CellConfig to render instead of the
	// stored one Config names. A create refuses it.
	Draft *v1beta1.CellConfigDoc
	// ConnectorOverrides pick, for this cell only, the identity of a
	// connector the Config requires — `--connector <connector>=<secret>` —
	// keyed by connector name (issue #1548). A reference without a realm is
	// looked up in the cell's own realm. A connector the Config does not
	// require, or a Secret tagged for another connector, is refused. For a
	// method that keeps a login in the state volume the reference names a
	// Volume instead — a login identity (issue #1685). The
	// overrides are recorded in the cell's provenance, so a restart and a
	// recreate keep them.
	ConnectorOverrides map[string]v1beta1.ContainerSecretRef
	// ConnectorMethods pick, for this cell only, the method of a connector
	// the Config requires — `--connector-method <connector>=<method>` —
	// keyed by connector name (issue #1551). The blueprint component of that
	// method is enabled in place of the one the Config enables, so the
	// connector's identity then fills that method's slot. An unknown
	// connector or method, or a connector the Config does not require, is
	// refused. Recorded in the cell's provenance, so a restart and a
	// recreate keep it.
	ConnectorMethods map[string]string
	// State picks, for this cell only, where the harness keeps its state —
	// `--state persistent|ephemeral` (issue #1685). Empty keeps the Config's
	// choice. `ephemeral` mounts no state volume and is refused with a
	// method that keeps a login there. Recorded in the cell's provenance.
	State string
	// TargetSpace and TargetStack place the cell in a space and stack below
	// the Config's own scope (issue #1603). A coordinate the Config sets is
	// pinned: a different target is refused. Empty keeps the Config's scope
	// with empty coordinates defaulted to "default". The realm is always the
	// Config's.
	TargetSpace string
	TargetStack string
}

// PreviewCellFromConfigArgs is the wire request for PreviewCellFromConfig.
type PreviewCellFromConfigArgs struct {
	Request CellFromConfigRequest
}

// PreviewCellFromConfigResult is the rendered cell, its digest and the merge
// explanation.
type PreviewCellFromConfigResult struct {
	Cell   v1beta1.CellDoc
	Digest string
	Values []v1beta1.RenderedValue
	// Connectors is how each connector the Config requires resolved for
	// this cell: the identity and the level it came from (#1548).
	Connectors []v1beta1.ConnectorStatus
}

// PreviewCellFromConfigReply is the wire response for PreviewCellFromConfig.
type PreviewCellFromConfigReply struct {
	Result PreviewCellFromConfigResult
	Err    *APIError
}

// CreateCellFromConfigArgs is the wire request for CreateCellFromConfig.
type CreateCellFromConfigArgs struct {
	Request CellFromConfigRequest
}

// CreateCellFromConfigResult is the create outcome plus the render's digest
// and merge explanation.
type CreateCellFromConfigResult struct {
	CreateCellResult

	Digest string
	Values []v1beta1.RenderedValue
	// Connectors is how each connector the Config requires resolved for the
	// cell; a connector's Warning names a risk the cell was created with
	// (issue #1789).
	Connectors []v1beta1.ConnectorStatus
}

// CreateCellFromConfigReply is the wire response for CreateCellFromConfig.
type CreateCellFromConfigReply struct {
	Result CreateCellFromConfigResult
	Err    *APIError
}

// DiffCellFromConfigArgs is the wire request for DiffCellFromConfig. Doc
// names the cell: metadata.name and spec.realmId/spaceId/stackId.
type DiffCellFromConfigArgs struct {
	Doc v1beta1.CellDoc
}

// DiffCellFromConfigResult is the stored cell beside the cell its lineage
// Config renders to now (#1768). Desired is the stored cell with only the
// fields the drift check reports replaced by the render's values. Both
// documents carry no status, runtime env or provenance.
type DiffCellFromConfigResult struct {
	Live v1beta1.CellDoc
	// Desired is nil when the lineage Config was deleted.
	Desired     *v1beta1.CellDoc
	ConfigFound bool
	// Reasons are the drift check's reasons, in its order.
	Reasons []string
	// Paths are the spec field paths that differ.
	Paths []string
	// ChangeType is how the next start applies the render: "none"
	// (provenance only), "additive" or "compatible" (in place), or
	// "breaking" (the cell is recreated). Empty when the Config was deleted.
	ChangeType string
}

// DiffCellFromConfigReply is the wire response for DiffCellFromConfig.
type DiffCellFromConfigReply struct {
	Result DiffCellFromConfigResult
	Err    *APIError
}

// CreateCellResult mirrors internal/controller.CreateCellResult using
// external v1beta1 types, so it is safe to serialize and return to
// non-privileged callers.
type CreateCellResult struct {
	Cell v1beta1.CellDoc

	MetadataExistsPre       bool
	MetadataExistsPost      bool
	CgroupExistsPre         bool
	CgroupExistsPost        bool
	CgroupCreated           bool
	RootContainerExistsPre  bool
	RootContainerExistsPost bool
	RootContainerCreated    bool
	StartedPre              bool
	StartedPost             bool
	Started                 bool
	Created                 bool

	Containers []ContainerCreationOutcome
}

type ContainerCreationOutcome struct {
	Name       string
	ExistsPre  bool
	ExistsPost bool
	Created    bool
}

// ServiceName is the net/rpc service name registered by the daemon. The "V1"
// suffix is the wire version prefix; breaking changes land on "KukeonV2".
const ServiceName = "KukeonV1"

// Fully-qualified method names used on the wire.
const (
	MethodCreateRealm     = ServiceName + ".CreateRealm"
	MethodCreateSpace     = ServiceName + ".CreateSpace"
	MethodCreateStack     = ServiceName + ".CreateStack"
	MethodCreateCell      = ServiceName + ".CreateCell"
	MethodMaterializeCell = ServiceName + ".MaterializeCell"

	MethodPreviewCellFromConfig = ServiceName + ".PreviewCellFromConfig"
	MethodCreateCellFromConfig  = ServiceName + ".CreateCellFromConfig"
	MethodDiffCellFromConfig    = ServiceName + ".DiffCellFromConfig"
	MethodCreateConfig          = ServiceName + ".CreateConfig"
	MethodCreateSecret          = ServiceName + ".CreateSecret"
	MethodCreateVolume          = ServiceName + ".CreateVolume"

	MethodGetRealm     = ServiceName + ".GetRealm"
	MethodGetSpace     = ServiceName + ".GetSpace"
	MethodGetStack     = ServiceName + ".GetStack"
	MethodGetCell      = ServiceName + ".GetCell"
	MethodGetContainer = ServiceName + ".GetContainer"
	MethodGetSecret    = ServiceName + ".GetSecret"
	MethodReadSecret   = ServiceName + ".ReadSecret"
	MethodGetBlueprint = ServiceName + ".GetBlueprint"
	MethodGetConfig    = ServiceName + ".GetConfig"
	MethodGetVolume    = ServiceName + ".GetVolume"

	MethodGetCgroupDetail  = ServiceName + ".GetCgroupDetail"
	MethodGetNetworkDetail = ServiceName + ".GetNetworkDetail"

	MethodListImages = ServiceName + ".ListImages"
	MethodGetImage   = ServiceName + ".GetImage"

	MethodListRealms     = ServiceName + ".ListRealms"
	MethodListSpaces     = ServiceName + ".ListSpaces"
	MethodListStacks     = ServiceName + ".ListStacks"
	MethodListCells      = ServiceName + ".ListCells"
	MethodListContainers = ServiceName + ".ListContainers"
	MethodListSecrets    = ServiceName + ".ListSecrets"
	MethodListBlueprints = ServiceName + ".ListBlueprints"
	MethodListConfigs    = ServiceName + ".ListConfigs"
	MethodListVolumes    = ServiceName + ".ListVolumes"

	MethodStartCell       = ServiceName + ".StartCell"
	MethodAttachContainer = ServiceName + ".AttachContainer"
	MethodLogContainer    = ServiceName + ".LogContainer"

	MethodReadContainerLog = ServiceName + ".ReadContainerLog"

	MethodConnectContainerTerminal = ServiceName + ".ConnectContainerTerminal"
	MethodExecContainer            = ServiceName + ".ExecContainer"
	MethodReadDaemonLogs           = ServiceName + ".ReadDaemonLogs"

	MethodReadHealthDigest   = ServiceName + ".ReadHealthDigest"
	MethodSubmitHealthReport = ServiceName + ".SubmitHealthReport"
	MethodListHealthReports  = ServiceName + ".ListHealthReports"
	MethodReadHealthReport   = ServiceName + ".ReadHealthReport"
	MethodDeleteHealthReport = ServiceName + ".DeleteHealthReport"

	MethodStopCell = ServiceName + ".StopCell"
	MethodKillCell = ServiceName + ".KillCell"

	MethodDeleteRealm     = ServiceName + ".DeleteRealm"
	MethodDeleteSpace     = ServiceName + ".DeleteSpace"
	MethodDeleteStack     = ServiceName + ".DeleteStack"
	MethodDeleteCell      = ServiceName + ".DeleteCell"
	MethodDeleteSecret    = ServiceName + ".DeleteSecret"
	MethodDeleteBlueprint = ServiceName + ".DeleteBlueprint"
	MethodDeleteConfig    = ServiceName + ".DeleteConfig"
	MethodDeleteVolume    = ServiceName + ".DeleteVolume"

	MethodGetTeam         = ServiceName + ".GetTeam"
	MethodListTeams       = ServiceName + ".ListTeams"
	MethodDeleteTeam      = ServiceName + ".DeleteTeam"
	MethodSetDisplayName  = ServiceName + ".SetDisplayName"
	MethodGetTeamDefaults = ServiceName + ".GetTeamDefaults"

	MethodGetSource    = ServiceName + ".GetSource"
	MethodListSources  = ServiceName + ".ListSources"
	MethodDeleteSource = ServiceName + ".DeleteSource"

	MethodStageGitFetch   = ServiceName + ".StageGitFetch"
	MethodReleaseGitFetch = ServiceName + ".ReleaseGitFetch"

	MethodPurgeRealm = ServiceName + ".PurgeRealm"
	MethodPurgeSpace = ServiceName + ".PurgeSpace"
	MethodPurgeStack = ServiceName + ".PurgeStack"
	MethodPurgeCell  = ServiceName + ".PurgeCell"

	MethodRefreshAll      = ServiceName + ".RefreshAll"
	MethodApplyDocuments  = ServiceName + ".ApplyDocuments"
	MethodDeleteDocuments = ServiceName + ".DeleteDocuments"

	MethodPing = ServiceName + ".Ping"
)
