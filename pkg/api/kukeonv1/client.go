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
	CreateContainer(ctx context.Context, doc v1beta1.ContainerDoc) (CreateContainerResult, error)

	GetRealm(ctx context.Context, doc v1beta1.RealmDoc) (GetRealmResult, error)
	GetSpace(ctx context.Context, doc v1beta1.SpaceDoc) (GetSpaceResult, error)
	GetStack(ctx context.Context, doc v1beta1.StackDoc) (GetStackResult, error)
	GetCell(ctx context.Context, doc v1beta1.CellDoc) (GetCellResult, error)
	GetContainer(ctx context.Context, doc v1beta1.ContainerDoc) (GetContainerResult, error)

	ListRealms(ctx context.Context) ([]v1beta1.RealmDoc, error)
	ListSpaces(ctx context.Context, realmName string) ([]v1beta1.SpaceDoc, error)
	ListStacks(ctx context.Context, realmName, spaceName string) ([]v1beta1.StackDoc, error)
	ListCells(ctx context.Context, realmName, spaceName, stackName string) ([]v1beta1.CellDoc, error)
	ListContainers(
		ctx context.Context,
		realmName, spaceName, stackName, cellName string,
	) ([]v1beta1.ContainerSpec, error)

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

	PurgeRealm(ctx context.Context, doc v1beta1.RealmDoc, force, cascade bool) (PurgeRealmResult, error)
	PurgeSpace(ctx context.Context, doc v1beta1.SpaceDoc, force, cascade bool) (PurgeSpaceResult, error)
	PurgeStack(ctx context.Context, doc v1beta1.StackDoc, force, cascade bool) (PurgeStackResult, error)
	PurgeCell(ctx context.Context, doc v1beta1.CellDoc, force, cascade bool) (PurgeCellResult, error)
	PurgeContainer(ctx context.Context, doc v1beta1.ContainerDoc) (PurgeContainerResult, error)

	RefreshAll(ctx context.Context) (RefreshAllResult, error)
	ApplyDocuments(ctx context.Context, rawYAML []byte) (ApplyDocumentsResult, error)

	// LoadImage imports an OCI/docker image tarball into the named realm's
	// containerd namespace. The tarball ships in-band; see LoadImageArgs.
	LoadImage(ctx context.Context, realm string, tarball []byte) (LoadImageResult, error)

	Ping(ctx context.Context) error
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
	MethodCreateContainer = ServiceName + ".CreateContainer"

	MethodGetRealm     = ServiceName + ".GetRealm"
	MethodGetSpace     = ServiceName + ".GetSpace"
	MethodGetStack     = ServiceName + ".GetStack"
	MethodGetCell      = ServiceName + ".GetCell"
	MethodGetContainer = ServiceName + ".GetContainer"

	MethodListRealms     = ServiceName + ".ListRealms"
	MethodListSpaces     = ServiceName + ".ListSpaces"
	MethodListStacks     = ServiceName + ".ListStacks"
	MethodListCells      = ServiceName + ".ListCells"
	MethodListContainers = ServiceName + ".ListContainers"

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

	MethodPurgeRealm     = ServiceName + ".PurgeRealm"
	MethodPurgeSpace     = ServiceName + ".PurgeSpace"
	MethodPurgeStack     = ServiceName + ".PurgeStack"
	MethodPurgeCell      = ServiceName + ".PurgeCell"
	MethodPurgeContainer = ServiceName + ".PurgeContainer"

	MethodRefreshAll     = ServiceName + ".RefreshAll"
	MethodApplyDocuments = ServiceName + ".ApplyDocuments"
	MethodLoadImage      = ServiceName + ".LoadImage"

	MethodPing = ServiceName + ".Ping"
)
