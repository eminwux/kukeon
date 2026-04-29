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

package daemon

import (
	"context"
	"log/slog"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// AutoDeleteLauncher installs a background watcher that deletes the cell
// after its root containerd task exits. Called from CreateCell when the
// resulting cell has Spec.AutoDelete=true (i.e., the operator passed
// `kuke run --rm`). Returning an error logs and proceeds — auto-delete is
// best-effort by design.
type AutoDeleteLauncher func(ctx context.Context, doc v1beta1.CellDoc) error

// KukeonV1Service implements the net/rpc server methods for the KukeonV1
// service. Each method always returns nil to net/rpc and carries structured
// errors inside the Reply envelope so errdefs sentinels survive the wire.
type KukeonV1Service struct {
	ctx                context.Context
	logger             *slog.Logger
	core               kukeonv1.Client
	autoDeleteLauncher AutoDeleteLauncher
}

// NewKukeonV1Service constructs a service bound to the given kukeon Client.
// The service borrows the client; the caller owns its lifecycle. If
// autoDeleteLauncher is non-nil, CreateCell consults it for cells whose
// resulting spec asks for auto-delete.
func NewKukeonV1Service(
	ctx context.Context,
	logger *slog.Logger,
	core kukeonv1.Client,
	autoDeleteLauncher AutoDeleteLauncher,
) *KukeonV1Service {
	return &KukeonV1Service{
		ctx:                ctx,
		logger:             logger,
		core:               core,
		autoDeleteLauncher: autoDeleteLauncher,
	}
}

// CreateRealm is the net/rpc method for KukeonV1.CreateRealm.
func (s *KukeonV1Service) CreateRealm(args *kukeonv1.CreateRealmArgs, reply *kukeonv1.CreateRealmReply) error {
	result, err := s.core.CreateRealm(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateRealm returned error", "error", err)
	}
	return nil
}

// CreateSpace is the net/rpc method for KukeonV1.CreateSpace.
func (s *KukeonV1Service) CreateSpace(args *kukeonv1.CreateSpaceArgs, reply *kukeonv1.CreateSpaceReply) error {
	result, err := s.core.CreateSpace(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateSpace returned error", "error", err)
	}
	return nil
}

// CreateStack is the net/rpc method for KukeonV1.CreateStack.
func (s *KukeonV1Service) CreateStack(args *kukeonv1.CreateStackArgs, reply *kukeonv1.CreateStackReply) error {
	result, err := s.core.CreateStack(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateStack returned error", "error", err)
	}
	return nil
}

// CreateCell is the net/rpc method for KukeonV1.CreateCell. net/rpc requires
// exported pointer-to-struct args and reply plus a single error return.
//
// When the request asks for auto-delete (`kuke run --rm` set Spec.AutoDelete)
// and the cell was started, the service kicks off a best-effort watcher
// goroutine that waits for the root task to exit and then kills+deletes the
// cell. The watcher is owned by the daemon (s.ctx); a daemon crash before
// task exit leaves an orphan, matching the documented behavior.
func (s *KukeonV1Service) CreateCell(args *kukeonv1.CreateCellArgs, reply *kukeonv1.CreateCellReply) error {
	result, err := s.core.CreateCell(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateCell returned error", "error", err)
		return nil
	}

	if s.autoDeleteLauncher != nil && args.Doc.Spec.AutoDelete && result.Started {
		if launchErr := s.autoDeleteLauncher(s.ctx, args.Doc); launchErr != nil {
			s.logger.WarnContext(s.ctx, "failed to install --rm watcher; cell will not auto-delete",
				"cell", args.Doc.Metadata.Name,
				"realm", args.Doc.Spec.RealmID,
				"error", launchErr,
			)
		}
	}
	return nil
}

// CreateContainer is the net/rpc method for KukeonV1.CreateContainer.
func (s *KukeonV1Service) CreateContainer(
	args *kukeonv1.CreateContainerArgs,
	reply *kukeonv1.CreateContainerReply,
) error {
	result, err := s.core.CreateContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateContainer returned error", "error", err)
	}
	return nil
}

// ---- Get ----

func (s *KukeonV1Service) GetRealm(args *kukeonv1.GetRealmArgs, reply *kukeonv1.GetRealmReply) error {
	result, err := s.core.GetRealm(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetSpace(args *kukeonv1.GetSpaceArgs, reply *kukeonv1.GetSpaceReply) error {
	result, err := s.core.GetSpace(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetStack(args *kukeonv1.GetStackArgs, reply *kukeonv1.GetStackReply) error {
	result, err := s.core.GetStack(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetCell(args *kukeonv1.GetCellArgs, reply *kukeonv1.GetCellReply) error {
	result, err := s.core.GetCell(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetContainer(args *kukeonv1.GetContainerArgs, reply *kukeonv1.GetContainerReply) error {
	result, err := s.core.GetContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- List ----

func (s *KukeonV1Service) ListRealms(_ *kukeonv1.ListRealmsArgs, reply *kukeonv1.ListRealmsReply) error {
	realms, err := s.core.ListRealms(s.ctx)
	reply.Realms = realms
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListSpaces(args *kukeonv1.ListSpacesArgs, reply *kukeonv1.ListSpacesReply) error {
	spaces, err := s.core.ListSpaces(s.ctx, args.RealmName)
	reply.Spaces = spaces
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListStacks(args *kukeonv1.ListStacksArgs, reply *kukeonv1.ListStacksReply) error {
	stacks, err := s.core.ListStacks(s.ctx, args.RealmName, args.SpaceName)
	reply.Stacks = stacks
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListCells(args *kukeonv1.ListCellsArgs, reply *kukeonv1.ListCellsReply) error {
	cells, err := s.core.ListCells(s.ctx, args.RealmName, args.SpaceName, args.StackName)
	reply.Cells = cells
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListContainers(args *kukeonv1.ListContainersArgs, reply *kukeonv1.ListContainersReply) error {
	containers, err := s.core.ListContainers(s.ctx, args.RealmName, args.SpaceName, args.StackName, args.CellName)
	reply.Containers = containers
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Lifecycle ----

func (s *KukeonV1Service) StartCell(args *kukeonv1.StartCellArgs, reply *kukeonv1.StartCellReply) error {
	result, err := s.core.StartCell(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) StartContainer(args *kukeonv1.StartContainerArgs, reply *kukeonv1.StartContainerReply) error {
	result, err := s.core.StartContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// AttachContainer is the placeholder endpoint shipped in #57. It enforces
// the Attachable gate at the API boundary; the full client lands in #66.
func (s *KukeonV1Service) AttachContainer(
	args *kukeonv1.AttachContainerArgs,
	reply *kukeonv1.AttachContainerReply,
) error {
	result, err := s.core.AttachContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "AttachContainer returned error", "error", err)
	}
	return nil
}

// LogContainer enforces the Attachable gate at the API boundary and returns
// the host path of the per-container sbsh capture file. Bytes never
// traverse this RPC — the client tails HostCapturePath directly.
func (s *KukeonV1Service) LogContainer(
	args *kukeonv1.LogContainerArgs,
	reply *kukeonv1.LogContainerReply,
) error {
	result, err := s.core.LogContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "LogContainer returned error", "error", err)
	}
	return nil
}

func (s *KukeonV1Service) StopCell(args *kukeonv1.StopCellArgs, reply *kukeonv1.StopCellReply) error {
	result, err := s.core.StopCell(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) StopContainer(args *kukeonv1.StopContainerArgs, reply *kukeonv1.StopContainerReply) error {
	result, err := s.core.StopContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) KillCell(args *kukeonv1.KillCellArgs, reply *kukeonv1.KillCellReply) error {
	result, err := s.core.KillCell(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) KillContainer(args *kukeonv1.KillContainerArgs, reply *kukeonv1.KillContainerReply) error {
	result, err := s.core.KillContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Delete ----

func (s *KukeonV1Service) DeleteRealm(args *kukeonv1.DeleteRealmArgs, reply *kukeonv1.DeleteRealmReply) error {
	result, err := s.core.DeleteRealm(s.ctx, args.Doc, args.Force, args.Cascade)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteSpace(args *kukeonv1.DeleteSpaceArgs, reply *kukeonv1.DeleteSpaceReply) error {
	result, err := s.core.DeleteSpace(s.ctx, args.Doc, args.Force, args.Cascade)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteStack(args *kukeonv1.DeleteStackArgs, reply *kukeonv1.DeleteStackReply) error {
	result, err := s.core.DeleteStack(s.ctx, args.Doc, args.Force, args.Cascade)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteCell(args *kukeonv1.DeleteCellArgs, reply *kukeonv1.DeleteCellReply) error {
	result, err := s.core.DeleteCell(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteContainer(
	args *kukeonv1.DeleteContainerArgs,
	reply *kukeonv1.DeleteContainerReply,
) error {
	result, err := s.core.DeleteContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Purge ----

func (s *KukeonV1Service) PurgeRealm(args *kukeonv1.PurgeRealmArgs, reply *kukeonv1.PurgeRealmReply) error {
	result, err := s.core.PurgeRealm(s.ctx, args.Doc, args.Force, args.Cascade)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) PurgeSpace(args *kukeonv1.PurgeSpaceArgs, reply *kukeonv1.PurgeSpaceReply) error {
	result, err := s.core.PurgeSpace(s.ctx, args.Doc, args.Force, args.Cascade)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) PurgeStack(args *kukeonv1.PurgeStackArgs, reply *kukeonv1.PurgeStackReply) error {
	result, err := s.core.PurgeStack(s.ctx, args.Doc, args.Force, args.Cascade)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) PurgeCell(args *kukeonv1.PurgeCellArgs, reply *kukeonv1.PurgeCellReply) error {
	result, err := s.core.PurgeCell(s.ctx, args.Doc, args.Force, args.Cascade)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) PurgeContainer(args *kukeonv1.PurgeContainerArgs, reply *kukeonv1.PurgeContainerReply) error {
	result, err := s.core.PurgeContainer(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Refresh ----

func (s *KukeonV1Service) RefreshAll(_ *kukeonv1.RefreshAllArgs, reply *kukeonv1.RefreshAllReply) error {
	result, err := s.core.RefreshAll(s.ctx)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Ping ----

// Ping acks that the kukeonv1 RPC handler is serving and reports the daemon
// build version. Used by `kuke init` to wait until kukeond is ready.
func (s *KukeonV1Service) Ping(_ *kukeonv1.PingArgs, reply *kukeonv1.PingReply) error {
	reply.OK = true
	reply.Version = config.Version
	return nil
}

// ---- Apply ----

func (s *KukeonV1Service) ApplyDocuments(args *kukeonv1.ApplyDocumentsArgs, reply *kukeonv1.ApplyDocumentsReply) error {
	result, err := s.core.ApplyDocuments(s.ctx, args.RawYAML)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Image ----

func (s *KukeonV1Service) LoadImage(args *kukeonv1.LoadImageArgs, reply *kukeonv1.LoadImageReply) error {
	result, err := s.core.LoadImage(s.ctx, args.Realm, args.Tarball)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "LoadImage returned error", "error", err)
	}
	return nil
}

func (s *KukeonV1Service) ListImages(args *kukeonv1.ListImagesArgs, reply *kukeonv1.ListImagesReply) error {
	result, err := s.core.ListImages(s.ctx, args.Realm)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "ListImages returned error", "error", err)
	}
	return nil
}

func (s *KukeonV1Service) GetImage(args *kukeonv1.GetImageArgs, reply *kukeonv1.GetImageReply) error {
	result, err := s.core.GetImage(s.ctx, args.Realm, args.Ref)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "GetImage returned error", "error", err)
	}
	return nil
}
