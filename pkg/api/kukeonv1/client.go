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
	CreateContainer(ctx context.Context, doc v1beta1.ContainerDoc) (CreateContainerResult, error)
	// CreateConfig atomically persists a new CellConfig document under
	// create-only semantics (issue #839): the daemon-side write fails with
	// errdefs.ErrConfigExists if a Config of the same name already lives in
	// scope. `kuke run <src> --clone`'s gap-fill counter loop retries on
	// that sentinel; the `--clone --name X` path surfaces it as a hard
	// collision. The same scope / blueprint / slot-fill validation as
	// ApplyDocuments runs first — the only difference is the atomic gate
	// (which ApplyDocuments doesn't need, since apply is intentionally
	// write-through).
	CreateConfig(ctx context.Context, doc v1beta1.CellConfigDoc) (CreateConfigResult, error)

	GetRealm(ctx context.Context, doc v1beta1.RealmDoc) (GetRealmResult, error)
	GetSpace(ctx context.Context, doc v1beta1.SpaceDoc) (GetSpaceResult, error)
	GetStack(ctx context.Context, doc v1beta1.StackDoc) (GetStackResult, error)
	GetCell(ctx context.Context, doc v1beta1.CellDoc) (GetCellResult, error)
	GetContainer(ctx context.Context, doc v1beta1.ContainerDoc) (GetContainerResult, error)
	// GetSecret reports the metadata-only view of a single named, scoped
	// `kind: Secret` (issue #622). Spec.data is never echoed — the bytes do
	// not traverse this RPC by design (#619).
	GetSecret(ctx context.Context, doc v1beta1.SecretDoc) (GetSecretResult, error)
	// GetBlueprint reads one named, scoped `kind: CellBlueprint`'s full
	// document from daemon storage (issue #620). Unlike GetSecret the whole
	// document is returned — a blueprint carries only template references —
	// so `kuke run -b` can materialize it. The input doc carries the name and
	// scope coordinates only; the spec is ignored.
	GetBlueprint(ctx context.Context, doc v1beta1.CellBlueprintDoc) (GetBlueprintResult, error)
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
	// ListConfigs enumerates the metadata of every CellConfig bound to the
	// filter scope or any scope nested within it (issue #644). An empty
	// realmName lists across all realms. A Config is never cell-scoped, so
	// there is no cell coordinate. The spec is not populated for a list.
	ListConfigs(ctx context.Context, realmName, spaceName, stackName string) ([]v1beta1.CellConfigDoc, error)

	StartCell(ctx context.Context, doc v1beta1.CellDoc) (StartCellResult, error)
	StartContainer(ctx context.Context, doc v1beta1.ContainerDoc) (StartContainerResult, error)
	// AttachContainer is the placeholder endpoint shipped in #57. It only
	// validates that the target container has Attachable=true; the
	// terminal-bridge client lands in #66.
	AttachContainer(ctx context.Context, doc v1beta1.ContainerDoc) (AttachContainerResult, error)
	// LogContainer validates that the target container has Attachable=true
	// and resolves the host-side path of the per-container sbsh capture
	// file. Bytes never traverse this RPC — the caller (`kuke log`) reads
	// the file directly. Same Attachable gate as AttachContainer: only
	// containers wrapped by sbsh have a capture file to surface.
	LogContainer(ctx context.Context, doc v1beta1.ContainerDoc) (LogContainerResult, error)
	StopCell(ctx context.Context, doc v1beta1.CellDoc) (StopCellResult, error)
	StopContainer(ctx context.Context, doc v1beta1.ContainerDoc) (StopContainerResult, error)
	KillCell(ctx context.Context, doc v1beta1.CellDoc) (KillCellResult, error)
	KillContainer(ctx context.Context, doc v1beta1.ContainerDoc) (KillContainerResult, error)

	DeleteRealm(ctx context.Context, doc v1beta1.RealmDoc, force, cascade bool) (DeleteRealmResult, error)
	DeleteSpace(ctx context.Context, doc v1beta1.SpaceDoc, force, cascade bool) (DeleteSpaceResult, error)
	DeleteStack(ctx context.Context, doc v1beta1.StackDoc, force, cascade bool) (DeleteStackResult, error)
	DeleteCell(ctx context.Context, doc v1beta1.CellDoc) (DeleteCellResult, error)
	DeleteContainer(ctx context.Context, doc v1beta1.ContainerDoc) (DeleteContainerResult, error)
	// DeleteSecret removes a single named, scoped Secret's daemon-stored
	// file (issue #622). The live-reference safety gate ships in phase 3c.
	DeleteSecret(ctx context.Context, doc v1beta1.SecretDoc) (DeleteSecretResult, error)
	// DeleteBlueprint removes a single named, scoped CellBlueprint's
	// daemon-stored document (issue #643). No live-reference gate: cells
	// materialized from a blueprint are independent copies (#620).
	DeleteBlueprint(ctx context.Context, doc v1beta1.CellBlueprintDoc) (DeleteBlueprintResult, error)
	// DeleteConfig removes a single named, scoped CellConfig's daemon-stored
	// document (issue #644). No live-reference gate: deleting a Config never
	// deletes the cell it materialized. The result reports any live cells that
	// still carry the back-reference label so the caller can emit a notice.
	DeleteConfig(ctx context.Context, doc v1beta1.CellConfigDoc) (DeleteConfigResult, error)

	PurgeRealm(ctx context.Context, doc v1beta1.RealmDoc, force, cascade bool) (PurgeRealmResult, error)
	PurgeSpace(ctx context.Context, doc v1beta1.SpaceDoc, force, cascade bool) (PurgeSpaceResult, error)
	PurgeStack(ctx context.Context, doc v1beta1.StackDoc, force, cascade bool) (PurgeStackResult, error)
	PurgeCell(ctx context.Context, doc v1beta1.CellDoc, force, cascade bool) (PurgeCellResult, error)
	PurgeContainer(ctx context.Context, doc v1beta1.ContainerDoc) (PurgeContainerResult, error)

	RefreshAll(ctx context.Context) (RefreshAllResult, error)
	ApplyDocuments(ctx context.Context, rawYAML []byte) (ApplyDocumentsResult, error)
	// DeleteDocuments is the file-driven counterpart to ApplyDocuments —
	// `kuke delete -f` sends the raw YAML over the wire so deletes honor
	// `--host` routing the same way applies do. Per-resource cascade/force
	// semantics match the single-kind Delete{Realm,Space,Stack} methods.
	DeleteDocuments(ctx context.Context, rawYAML []byte, cascade, force bool) (DeleteDocumentsResult, error)

	// NOTE: image operations (LoadImage / ListImages / GetImage /
	// DeleteImage) are intentionally NOT on this interface. They are
	// daemon-independent by design (#226) and live on the in-process
	// client (`*local.Client`) only — see the `cmd/kuke/image` package's
	// `Client` interface, which the image subcommands use directly. The
	// RPC daemon does not serve image methods.

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
	MethodCreateContainer = ServiceName + ".CreateContainer"
	MethodCreateConfig    = ServiceName + ".CreateConfig"

	MethodGetRealm     = ServiceName + ".GetRealm"
	MethodGetSpace     = ServiceName + ".GetSpace"
	MethodGetStack     = ServiceName + ".GetStack"
	MethodGetCell      = ServiceName + ".GetCell"
	MethodGetContainer = ServiceName + ".GetContainer"
	MethodGetSecret    = ServiceName + ".GetSecret"
	MethodGetBlueprint = ServiceName + ".GetBlueprint"
	MethodGetConfig    = ServiceName + ".GetConfig"

	MethodListRealms     = ServiceName + ".ListRealms"
	MethodListSpaces     = ServiceName + ".ListSpaces"
	MethodListStacks     = ServiceName + ".ListStacks"
	MethodListCells      = ServiceName + ".ListCells"
	MethodListContainers = ServiceName + ".ListContainers"
	MethodListSecrets    = ServiceName + ".ListSecrets"
	MethodListBlueprints = ServiceName + ".ListBlueprints"
	MethodListConfigs    = ServiceName + ".ListConfigs"

	MethodStartCell       = ServiceName + ".StartCell"
	MethodStartContainer  = ServiceName + ".StartContainer"
	MethodAttachContainer = ServiceName + ".AttachContainer"
	MethodLogContainer    = ServiceName + ".LogContainer"
	MethodStopCell        = ServiceName + ".StopCell"
	MethodStopContainer   = ServiceName + ".StopContainer"
	MethodKillCell        = ServiceName + ".KillCell"
	MethodKillContainer   = ServiceName + ".KillContainer"

	MethodDeleteRealm     = ServiceName + ".DeleteRealm"
	MethodDeleteSpace     = ServiceName + ".DeleteSpace"
	MethodDeleteStack     = ServiceName + ".DeleteStack"
	MethodDeleteCell      = ServiceName + ".DeleteCell"
	MethodDeleteContainer = ServiceName + ".DeleteContainer"
	MethodDeleteSecret    = ServiceName + ".DeleteSecret"
	MethodDeleteBlueprint = ServiceName + ".DeleteBlueprint"
	MethodDeleteConfig    = ServiceName + ".DeleteConfig"

	MethodPurgeRealm     = ServiceName + ".PurgeRealm"
	MethodPurgeSpace     = ServiceName + ".PurgeSpace"
	MethodPurgeStack     = ServiceName + ".PurgeStack"
	MethodPurgeCell      = ServiceName + ".PurgeCell"
	MethodPurgeContainer = ServiceName + ".PurgeContainer"

	MethodRefreshAll      = ServiceName + ".RefreshAll"
	MethodApplyDocuments  = ServiceName + ".ApplyDocuments"
	MethodDeleteDocuments = ServiceName + ".DeleteDocuments"

	MethodPing = ServiceName + ".Ping"
)
