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
	"fmt"
	"log/slog"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// KukeonV1Service implements the net/rpc server methods for the KukeonV1
// service. Each method always returns nil to net/rpc and carries structured
// errors inside the Reply envelope so errdefs sentinels survive the wire.
type KukeonV1Service struct {
	ctx    context.Context
	logger *slog.Logger
	core   kukeonv1.Client
}

// NewKukeonV1Service constructs a service bound to the given kukeon Client.
// The service borrows the client; the caller owns its lifecycle.
func NewKukeonV1Service(
	ctx context.Context,
	logger *slog.Logger,
	core kukeonv1.Client,
) *KukeonV1Service {
	return &KukeonV1Service{
		ctx:    ctx,
		logger: logger,
		core:   core,
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
// Cells created with Spec.AutoDelete=true (`kuke run --rm`) are not
// followed by a per-cell watcher goroutine — the daemon's reconcile loop
// observes the root task exit on a subsequent tick and runs the
// kill+delete sequence. That makes AutoDelete cleanup restart-resilient:
// a cell whose Spec.AutoDelete=true survives a daemon restart still gets
// cleaned up after the daemon is back, without per-cell startup wiring.
func (s *KukeonV1Service) CreateCell(args *kukeonv1.CreateCellArgs, reply *kukeonv1.CreateCellReply) error {
	result, err := s.core.CreateCell(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateCell returned error", "error", err)
	}
	return nil
}

// MaterializeCell is the net/rpc method for KukeonV1.MaterializeCell (#818).
// Mirrors CreateCell, but the daemon-side controller skips the StartCell
// step so the resulting cell is left in a stopped state — the substrate for
// `kuke create cell --from-blueprint` / `--from-config` scaffolding.
func (s *KukeonV1Service) MaterializeCell(
	args *kukeonv1.MaterializeCellArgs,
	reply *kukeonv1.MaterializeCellReply,
) error {
	result, err := s.core.MaterializeCell(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "MaterializeCell returned error", "error", err)
	}
	return nil
}

// PreviewCellFromConfig is the net/rpc method for
// KukeonV1.PreviewCellFromConfig (#1418).
func (s *KukeonV1Service) PreviewCellFromConfig(
	args *kukeonv1.PreviewCellFromConfigArgs,
	reply *kukeonv1.PreviewCellFromConfigReply,
) error {
	result, err := s.core.PreviewCellFromConfig(s.ctx, args.Request)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// DiffCellFromConfig is the net/rpc method for KukeonV1.DiffCellFromConfig
// (#1768).
func (s *KukeonV1Service) DiffCellFromConfig(
	args *kukeonv1.DiffCellFromConfigArgs,
	reply *kukeonv1.DiffCellFromConfigReply,
) error {
	result, err := s.core.DiffCellFromConfig(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// CreateCellFromConfig is the net/rpc method for
// KukeonV1.CreateCellFromConfig (#1418).
func (s *KukeonV1Service) CreateCellFromConfig(
	args *kukeonv1.CreateCellFromConfigArgs,
	reply *kukeonv1.CreateCellFromConfigReply,
) error {
	result, err := s.core.CreateCellFromConfig(s.ctx, args.Request)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateCellFromConfig returned error", "error", err)
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

func (s *KukeonV1Service) GetSecret(args *kukeonv1.GetSecretArgs, reply *kukeonv1.GetSecretReply) error {
	result, err := s.core.GetSecret(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ReadSecret(args *kukeonv1.ReadSecretArgs, reply *kukeonv1.ReadSecretReply) error {
	result, err := s.core.ReadSecret(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetBlueprint(args *kukeonv1.GetBlueprintArgs, reply *kukeonv1.GetBlueprintReply) error {
	result, err := s.core.GetBlueprint(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetVolume(args *kukeonv1.GetVolumeArgs, reply *kukeonv1.GetVolumeReply) error {
	result, err := s.core.GetVolume(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetCgroupDetail(
	args *kukeonv1.GetCgroupDetailArgs, reply *kukeonv1.GetCgroupDetailReply,
) error {
	result, err := s.core.GetCgroupDetail(s.ctx, args.Scope)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetNetworkDetail(
	args *kukeonv1.GetNetworkDetailArgs, reply *kukeonv1.GetNetworkDetailReply,
) error {
	result, err := s.core.GetNetworkDetail(s.ctx, args.Scope)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetConfig(args *kukeonv1.GetConfigArgs, reply *kukeonv1.GetConfigReply) error {
	result, err := s.core.GetConfig(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) CreateConfig(args *kukeonv1.CreateConfigArgs, reply *kukeonv1.CreateConfigReply) error {
	result, err := s.core.CreateConfig(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) CreateSecret(args *kukeonv1.CreateSecretArgs, reply *kukeonv1.CreateSecretReply) error {
	result, err := s.core.CreateSecret(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateSecret returned error", "error", err)
	}
	return nil
}

func (s *KukeonV1Service) CreateVolume(args *kukeonv1.CreateVolumeArgs, reply *kukeonv1.CreateVolumeReply) error {
	result, err := s.core.CreateVolume(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "CreateVolume returned error", "error", err)
	}
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

func (s *KukeonV1Service) ListSecrets(args *kukeonv1.ListSecretsArgs, reply *kukeonv1.ListSecretsReply) error {
	secrets, err := s.core.ListSecrets(s.ctx, args.RealmName, args.SpaceName, args.StackName, args.CellName)
	reply.Secrets = secrets
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListBlueprints(
	args *kukeonv1.ListBlueprintsArgs, reply *kukeonv1.ListBlueprintsReply,
) error {
	blueprints, err := s.core.ListBlueprints(s.ctx, args.RealmName, args.SpaceName, args.StackName)
	reply.Blueprints = blueprints
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListVolumes(
	args *kukeonv1.ListVolumesArgs, reply *kukeonv1.ListVolumesReply,
) error {
	volumes, err := s.core.ListVolumes(s.ctx, args.RealmName, args.SpaceName, args.StackName)
	reply.Volumes = volumes
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListConfigs(
	args *kukeonv1.ListConfigsArgs, reply *kukeonv1.ListConfigsReply,
) error {
	configs, err := s.core.ListConfigs(s.ctx, args.RealmName, args.SpaceName, args.StackName)
	reply.Configs = configs
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

// ConnectContainerTerminal applies AttachContainer's gates, dials the
// container's sbsh control socket, and hands the connected socket to the
// caller: reply.Conn is sent as SCM_RIGHTS by the server codec
// (kukeonv1.NewServerCodec), which closes the daemon's copy once the reply
// is written. Terminal bytes never traverse kukeond.
func (s *KukeonV1Service) ConnectContainerTerminal(
	args *kukeonv1.ConnectContainerTerminalArgs,
	reply *kukeonv1.ConnectContainerTerminalReply,
) error {
	conn, err := s.core.ConnectContainerTerminal(s.ctx, args.Doc)
	if err != nil {
		reply.Err = kukeonv1.ToAPIError(err)
		s.logger.DebugContext(s.ctx, "ConnectContainerTerminal returned error", "error", err)
		return nil
	}
	reply.Conn = conn
	return nil
}

// ExecContainer starts a process in the target container through the
// in-process client and hands the caller its end of the session's socket
// pair: reply.Conn is sent as SCM_RIGHTS by the server codec, which closes
// the daemon's copy of that end once the reply is written (issue #1432).
// The session runs on s.ctx, so it outlives this call. The caller uid the
// codec read from the socket is only recorded in the audit log: a
// connection whose peer is unknown is not refused (unlike StageGitFetch).
func (s *KukeonV1Service) ExecContainer(
	args *kukeonv1.ExecContainerArgs,
	reply *kukeonv1.ExecContainerReply,
) error {
	ctx := s.ctx
	if args.PeerUID != nil {
		ctx = kukeonv1.WithPeerUID(ctx, *args.PeerUID)
	}
	conn, err := s.core.ExecContainer(ctx, args.Doc, args.Request)
	if err != nil {
		reply.Err = kukeonv1.ToAPIError(err)
		s.logger.DebugContext(s.ctx, "ExecContainer returned error", "error", err)
		return nil
	}
	reply.Conn = conn
	return nil
}

// LogContainer returns the host path of the per-container output stream
// (sbsh capture or runtime-shim log). Bytes never traverse this RPC; a
// caller that cannot open the host path reads through ReadContainerLog.
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

// ReadContainerLog returns a bounded chunk of a container's output stream
// (issue #1537), so `kuke log` works from a cell that mounts only the
// kukeond socket. Socket access is the only gate, as for LogContainer.
func (s *KukeonV1Service) ReadContainerLog(
	args *kukeonv1.ReadContainerLogArgs,
	reply *kukeonv1.ReadContainerLogReply,
) error {
	result, err := s.core.ReadContainerLog(s.ctx, args.Doc, args.Request)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "ReadContainerLog returned error", "error", err)
	}
	return nil
}

// ListContainerRecording lists the retained segments of a container's
// terminal recording (issue #1783), so the web UI's Replay view can read a
// stopped cell's whole recording, which no sbsh process is left to serve.
func (s *KukeonV1Service) ListContainerRecording(
	args *kukeonv1.ListContainerRecordingArgs,
	reply *kukeonv1.ListContainerRecordingReply,
) error {
	result, err := s.core.ListContainerRecording(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "ListContainerRecording returned error", "error", err)
	}
	return nil
}

// ReadContainerRecordingSegment returns one bounded chunk of a recording
// segment's stored bytes (issue #1783). Socket access is the only gate, as
// for ReadContainerLog.
func (s *KukeonV1Service) ReadContainerRecordingSegment(
	args *kukeonv1.ReadContainerRecordingSegmentArgs,
	reply *kukeonv1.ReadContainerRecordingSegmentReply,
) error {
	result, err := s.core.ReadContainerRecordingSegment(s.ctx, args.Doc, args.Request)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "ReadContainerRecordingSegment returned error", "error", err)
	}
	return nil
}

// ReadHealthDigest evaluates kukeond's health in the daemon (issue #1509).
func (s *KukeonV1Service) ReadHealthDigest(
	args *kukeonv1.ReadHealthDigestArgs,
	reply *kukeonv1.ReadHealthDigestReply,
) error {
	result, err := s.core.ReadHealthDigest(s.ctx, args.Request)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "ReadHealthDigest returned error", "error", err)
	}
	return nil
}

// SubmitHealthReport stores a triaged health report (issue #1509).
func (s *KukeonV1Service) SubmitHealthReport(
	args *kukeonv1.SubmitHealthReportArgs,
	reply *kukeonv1.SubmitHealthReportReply,
) error {
	result, err := s.core.SubmitHealthReport(s.ctx, args.Report)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "SubmitHealthReport returned error", "error", err)
	}
	return nil
}

// ListHealthReports lists the stored health reports (issue #1509).
func (s *KukeonV1Service) ListHealthReports(
	_ *kukeonv1.ListHealthReportsArgs,
	reply *kukeonv1.ListHealthReportsReply,
) error {
	result, err := s.core.ListHealthReports(s.ctx)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "ListHealthReports returned error", "error", err)
	}
	return nil
}

// ReadHealthReport returns one stored health report (issue #1509).
func (s *KukeonV1Service) ReadHealthReport(
	args *kukeonv1.ReadHealthReportArgs,
	reply *kukeonv1.ReadHealthReportReply,
) error {
	result, err := s.core.ReadHealthReport(s.ctx, args.ID)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "ReadHealthReport returned error", "error", err)
	}
	return nil
}

// DeleteHealthReport deletes one stored health report (issue #1619).
func (s *KukeonV1Service) DeleteHealthReport(
	args *kukeonv1.DeleteHealthReportArgs,
	reply *kukeonv1.DeleteHealthReportReply,
) error {
	err := s.core.DeleteHealthReport(s.ctx, args.Name)
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "DeleteHealthReport returned error", "error", err)
	}
	return nil
}

// ReadDaemonLogs returns a bounded chunk of kukeond's own log stream
// (issue #1386, step 7). Socket access is the only gate, matching every
// other read RPC on this service.
func (s *KukeonV1Service) ReadDaemonLogs(
	args *kukeonv1.ReadDaemonLogsArgs,
	reply *kukeonv1.ReadDaemonLogsReply,
) error {
	result, err := s.core.ReadDaemonLogs(s.ctx, args.Request)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	if err != nil {
		s.logger.DebugContext(s.ctx, "ReadDaemonLogs returned error", "error", err)
	}
	return nil
}

func (s *KukeonV1Service) StopCell(args *kukeonv1.StopCellArgs, reply *kukeonv1.StopCellReply) error {
	result, err := s.core.StopCell(s.ctx, args.Doc)
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

func (s *KukeonV1Service) DeleteSecret(args *kukeonv1.DeleteSecretArgs, reply *kukeonv1.DeleteSecretReply) error {
	result, err := s.core.DeleteSecret(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteBlueprint(
	args *kukeonv1.DeleteBlueprintArgs, reply *kukeonv1.DeleteBlueprintReply,
) error {
	result, err := s.core.DeleteBlueprint(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteVolume(
	args *kukeonv1.DeleteVolumeArgs, reply *kukeonv1.DeleteVolumeReply,
) error {
	result, err := s.core.DeleteVolume(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteConfig(
	args *kukeonv1.DeleteConfigArgs, reply *kukeonv1.DeleteConfigReply,
) error {
	result, err := s.core.DeleteConfig(s.ctx, args.Doc)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Teams (issue #1618) ----

func (s *KukeonV1Service) GetTeam(args *kukeonv1.GetTeamArgs, reply *kukeonv1.GetTeamReply) error {
	result, err := s.core.GetTeam(s.ctx, args.Ref)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListTeams(args *kukeonv1.ListTeamsArgs, reply *kukeonv1.ListTeamsReply) error {
	teams, err := s.core.ListTeams(s.ctx, args.Realm)
	reply.Teams = teams
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteTeam(args *kukeonv1.DeleteTeamArgs, reply *kukeonv1.DeleteTeamReply) error {
	result, err := s.core.DeleteTeam(s.ctx, args.Ref)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// SetDisplayName sets or clears a Config's or a cell's display name (issue
// #1692).
func (s *KukeonV1Service) SetDisplayName(
	args *kukeonv1.SetDisplayNameArgs, reply *kukeonv1.SetDisplayNameReply,
) error {
	result, err := s.core.SetDisplayName(s.ctx, args.Request)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetTeamDefaults(
	args *kukeonv1.GetTeamDefaultsArgs, reply *kukeonv1.GetTeamDefaultsReply,
) error {
	result, err := s.core.GetTeamDefaults(s.ctx, args.Realm)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Sources (issue #1643) ----

func (s *KukeonV1Service) GetSource(args *kukeonv1.GetSourceArgs, reply *kukeonv1.GetSourceReply) error {
	result, err := s.core.GetSource(s.ctx, args.Ref)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ListSources(args *kukeonv1.ListSourcesArgs, reply *kukeonv1.ListSourcesReply) error {
	sources, err := s.core.ListSources(s.ctx, args.Realm)
	reply.Sources = sources
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) DeleteSource(args *kukeonv1.DeleteSourceArgs, reply *kukeonv1.DeleteSourceReply) error {
	result, err := s.core.DeleteSource(s.ctx, args.Ref)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Git fetch staging (issue #1643, D10) ----

// errPeerUnknown refuses a staging call on a connection whose peer uid the
// kernel did not report: the staged files are owned by the caller, so the
// caller must be known, never taken from the request.
var errPeerUnknown = fmt.Errorf("%w: cannot identify the calling user on this connection",
	errdefs.ErrGitFetchStaging)

func (s *KukeonV1Service) StageGitFetch(args *kukeonv1.StageGitFetchArgs, reply *kukeonv1.StageGitFetchReply) error {
	if args.PeerUID == nil {
		reply.Err = kukeonv1.ToAPIError(errPeerUnknown)
		return nil
	}
	result, err := s.core.StageGitFetch(kukeonv1.WithPeerUID(s.ctx, *args.PeerUID), args.Request)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) ReleaseGitFetch(
	args *kukeonv1.ReleaseGitFetchArgs, reply *kukeonv1.ReleaseGitFetchReply,
) error {
	if args.PeerUID == nil {
		reply.Err = kukeonv1.ToAPIError(errPeerUnknown)
		return nil
	}
	reply.Err = kukeonv1.ToAPIError(s.core.ReleaseGitFetch(kukeonv1.WithPeerUID(s.ctx, *args.PeerUID), args.ID))
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
	var (
		result kukeonv1.ApplyDocumentsResult
		err    error
	)
	if args.Team != "" {
		result, err = s.core.ApplyDocumentsForTeam(s.ctx, args.RawYAML, args.Team)
	} else {
		result, err = s.core.ApplyDocuments(s.ctx, args.RawYAML)
	}
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Delete (file-driven) ----

func (s *KukeonV1Service) DeleteDocuments(
	args *kukeonv1.DeleteDocumentsArgs,
	reply *kukeonv1.DeleteDocumentsReply,
) error {
	result, err := s.core.DeleteDocuments(s.ctx, args.RawYAML, args.Cascade, args.Force)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

// ---- Image (read-only) ----
//
// ListImages / GetImage are served so `kuke serve` can browse a realm's
// containerd image inventory through kukeond (#1406). Mutating image
// methods (LoadImage / DeleteImage / PruneImages) are intentionally not
// served — `kuke image *` and `kuke get image` stay daemon-independent by
// design (#226) and construct a local in-process client directly.

func (s *KukeonV1Service) ListImages(args *kukeonv1.ListImagesArgs, reply *kukeonv1.ListImagesReply) error {
	result, err := s.core.ListImages(s.ctx, args.Realm)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *KukeonV1Service) GetImage(args *kukeonv1.GetImageArgs, reply *kukeonv1.GetImageReply) error {
	result, err := s.core.GetImage(s.ctx, args.Realm, args.Ref)
	reply.Result = result
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}
