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

package kukeonv1

import (
	"net"
	"time"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ---- Realm ----

type CreateRealmArgs struct {
	Doc v1beta1.RealmDoc
}

type CreateRealmReply struct {
	Result CreateRealmResult
	Err    *APIError
}

type CreateRealmResult struct {
	Realm v1beta1.RealmDoc

	MetadataExistsPre             bool
	MetadataExistsPost            bool
	CgroupExistsPre               bool
	CgroupExistsPost              bool
	CgroupCreated                 bool
	ContainerdNamespaceExistsPre  bool
	ContainerdNamespaceExistsPost bool
	ContainerdNamespaceCreated    bool
	Created                       bool
}

// ---- Space ----

type CreateSpaceArgs struct {
	Doc v1beta1.SpaceDoc
}

type CreateSpaceReply struct {
	Result CreateSpaceResult
	Err    *APIError
}

type CreateSpaceResult struct {
	Space v1beta1.SpaceDoc

	MetadataExistsPre    bool
	MetadataExistsPost   bool
	CgroupExistsPre      bool
	CgroupExistsPost     bool
	CgroupCreated        bool
	CNINetworkExistsPre  bool
	CNINetworkExistsPost bool
	CNINetworkCreated    bool
	Created              bool
}

// ---- Stack ----

type CreateStackArgs struct {
	Doc v1beta1.StackDoc
}

type CreateStackReply struct {
	Result CreateStackResult
	Err    *APIError
}

type CreateStackResult struct {
	Stack v1beta1.StackDoc

	MetadataExistsPre  bool
	MetadataExistsPost bool
	CgroupExistsPre    bool
	CgroupExistsPost   bool
	CgroupCreated      bool
	Created            bool
}

// ---- Get ----

type GetRealmArgs struct {
	Doc v1beta1.RealmDoc
}

type GetRealmReply struct {
	Result GetRealmResult
	Err    *APIError
}

type GetRealmResult struct {
	Realm                     v1beta1.RealmDoc
	MetadataExists            bool
	CgroupExists              bool
	ContainerdNamespaceExists bool
	// Images is the daemon's resolution of Realm.Spec.Images: each entry's
	// reference and its digest in the realm's containerd namespace (#1418).
	Images []v1beta1.RealmImageStatus
}

type GetSpaceArgs struct {
	Doc v1beta1.SpaceDoc
}

type GetSpaceReply struct {
	Result GetSpaceResult
	Err    *APIError
}

type GetSpaceResult struct {
	Space            v1beta1.SpaceDoc
	MetadataExists   bool
	CgroupExists     bool
	CNINetworkExists bool
}

type GetStackArgs struct {
	Doc v1beta1.StackDoc
}

type GetStackReply struct {
	Result GetStackResult
	Err    *APIError
}

type GetStackResult struct {
	Stack          v1beta1.StackDoc
	MetadataExists bool
	CgroupExists   bool
}

type GetCellArgs struct {
	Doc v1beta1.CellDoc
}

type GetCellReply struct {
	Result GetCellResult
	Err    *APIError
}

type GetCellResult struct {
	Cell                v1beta1.CellDoc
	MetadataExists      bool
	CgroupExists        bool
	RootContainerExists bool
	// RootContainerTaskRunning reports whether the cell's root container has a
	// live containerd task. Distinct from RootContainerExists, which keys on the
	// containerd record that survives a host/daemon restart while the task does
	// not (#654, #683). Attach gating must consult task liveness, not record
	// existence, to avoid handing back a dead socket.
	RootContainerTaskRunning bool
}

type GetContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type GetContainerReply struct {
	Result GetContainerResult
	Err    *APIError
}

type GetContainerResult struct {
	Container          v1beta1.ContainerDoc
	CellMetadataExists bool
	ContainerExists    bool
}

type GetSecretArgs struct {
	Doc v1beta1.SecretDoc
}

type GetSecretReply struct {
	Result GetSecretResult
	Err    *APIError
}

// GetSecretResult carries one Secret. From GetSecret it is the metadata-only
// view: the material is never populated (issue #619/#622). From ReadSecret it
// carries the material too (issue #1652).
type GetSecretResult struct {
	Secret         v1beta1.SecretDoc
	MetadataExists bool
}

// ReadSecretArgs is the wire request for ReadSecret (issue #1652): the name
// and scope coordinates of the secret; the spec is ignored.
type ReadSecretArgs struct {
	Doc v1beta1.SecretDoc
}

// ReadSecretReply is the wire reply for ReadSecret. Result.Secret carries the
// material.
type ReadSecretReply struct {
	Result GetSecretResult
	Err    *APIError
}

type GetBlueprintArgs struct {
	Doc v1beta1.CellBlueprintDoc
}

type GetBlueprintReply struct {
	Result GetBlueprintResult
	Err    *APIError
}

// GetBlueprintResult carries the full CellBlueprintDoc read from daemon
// storage (issue #620). Unlike GetSecretResult the whole document is
// populated — a blueprint carries only template references, no secret bytes —
// so `kuke run -b` can materialize it.
type GetBlueprintResult struct {
	Blueprint      v1beta1.CellBlueprintDoc
	MetadataExists bool
}

type GetConfigArgs struct {
	Doc v1beta1.CellConfigDoc
}

type GetConfigReply struct {
	Result GetConfigResult
	Err    *APIError
}

// GetConfigResult carries the full CellConfigDoc read from daemon storage
// (issue #644). Unlike GetSecretResult the whole document is populated — a
// Config carries only a blueprint ref, scalar values, and slot fills, no
// credential bytes — so `kuke get config` can surface the binding.
type GetConfigResult struct {
	Config         v1beta1.CellConfigDoc
	MetadataExists bool
}

// CreateConfigArgs is the wire request for CreateConfig (issue #839). The
// document carries the candidate CellConfig the daemon writes atomically.
type CreateConfigArgs struct {
	Doc v1beta1.CellConfigDoc
}

// CreateConfigReply is the wire response for CreateConfig. Err is non-nil on
// failure so the errdefs.ErrConfigExists sentinel survives the wire
// roundtrip — the CLI's gap-fill counter loop reads it to retry.
type CreateConfigReply struct {
	Result CreateConfigResult
	Err    *APIError
}

// CreateConfigResult reports the outcome of an atomic create-only CellConfig
// write (issue #839). Created mirrors the daemon-side flag; Config is the
// persisted document echoed back so the CLI can chain into the cell-start
// path without re-issuing GetConfig.
type CreateConfigResult struct {
	Config  v1beta1.CellConfigDoc
	Created bool
}

// ---- List ----

type ListRealmsArgs struct{}

type ListRealmsReply struct {
	Realms []v1beta1.RealmDoc
	Err    *APIError
}

type ListSpacesArgs struct {
	RealmName string
}

type ListSpacesReply struct {
	Spaces []v1beta1.SpaceDoc
	Err    *APIError
}

type ListStacksArgs struct {
	RealmName string
	SpaceName string
}

type ListStacksReply struct {
	Stacks []v1beta1.StackDoc
	Err    *APIError
}

type ListCellsArgs struct {
	RealmName string
	SpaceName string
	StackName string
}

type ListCellsReply struct {
	Cells []v1beta1.CellDoc
	Err   *APIError
}

type ListContainersArgs struct {
	RealmName string
	SpaceName string
	StackName string
	CellName  string
}

type ListContainersReply struct {
	Containers []v1beta1.ContainerSpec
	Err        *APIError
}

type ListSecretsArgs struct {
	RealmName string
	SpaceName string
	StackName string
	CellName  string
}

// ListSecretsReply carries metadata-only SecretDocs — Spec.Data is never set.
type ListSecretsReply struct {
	Secrets []v1beta1.SecretDoc
	Err     *APIError
}

type ListBlueprintsArgs struct {
	RealmName string
	SpaceName string
	StackName string
}

// ListBlueprintsReply carries metadata-only CellBlueprintDocs — the spec (cell
// template, parameters, slots) is never populated for a list (issue #643).
type ListBlueprintsReply struct {
	Blueprints []v1beta1.CellBlueprintDoc
	Err        *APIError
}

type ListConfigsArgs struct {
	RealmName string
	SpaceName string
	StackName string
}

// ListConfigsReply carries metadata-only CellConfigDocs — the spec (blueprint
// ref, values, slot fills) is never populated for a list (issue #644).
type ListConfigsReply struct {
	Configs []v1beta1.CellConfigDoc
	Err     *APIError
}

// ---- Lifecycle (Start/Stop/Kill) ----

type StartCellArgs struct {
	Doc v1beta1.CellDoc
}

type StartCellReply struct {
	Result StartCellResult
	Err    *APIError
}

type StartCellResult struct {
	Cell    v1beta1.CellDoc
	Started bool
}

type StopCellArgs struct {
	Doc v1beta1.CellDoc
}

type StopCellReply struct {
	Result StopCellResult
	Err    *APIError
}

type StopCellResult struct {
	Cell    v1beta1.CellDoc
	Stopped bool
}

type KillCellArgs struct {
	Doc v1beta1.CellDoc
}

type KillCellReply struct {
	Result KillCellResult
	Err    *APIError
}

type KillCellResult struct {
	Cell   v1beta1.CellDoc
	Killed bool
}

// ---- Delete ----

type DeleteRealmArgs struct {
	Doc     v1beta1.RealmDoc
	Force   bool
	Cascade bool
}

type DeleteRealmReply struct {
	Result DeleteRealmResult
	Err    *APIError
}

type DeleteRealmResult struct {
	Realm                      v1beta1.RealmDoc
	Deleted                    []string
	MetadataDeleted            bool
	CgroupDeleted              bool
	ContainerdNamespaceDeleted bool
}

type DeleteSpaceArgs struct {
	Doc     v1beta1.SpaceDoc
	Force   bool
	Cascade bool
}

type DeleteSpaceReply struct {
	Result DeleteSpaceResult
	Err    *APIError
}

type DeleteSpaceResult struct {
	Space             v1beta1.SpaceDoc
	SpaceName         string
	RealmName         string
	MetadataDeleted   bool
	CgroupDeleted     bool
	CNINetworkDeleted bool
	Deleted           []string
}

type DeleteStackArgs struct {
	Doc     v1beta1.StackDoc
	Force   bool
	Cascade bool
}

type DeleteStackReply struct {
	Result DeleteStackResult
	Err    *APIError
}

type DeleteStackResult struct {
	Stack           v1beta1.StackDoc
	StackName       string
	RealmName       string
	SpaceName       string
	MetadataDeleted bool
	CgroupDeleted   bool
	Deleted         []string
}

type DeleteCellArgs struct {
	Doc v1beta1.CellDoc
}

type DeleteCellReply struct {
	Result DeleteCellResult
	Err    *APIError
}

type DeleteCellResult struct {
	Cell              v1beta1.CellDoc
	ContainersDeleted bool
	CgroupDeleted     bool
	MetadataDeleted   bool
}

type DeleteSecretArgs struct {
	Doc v1beta1.SecretDoc
}

type DeleteSecretReply struct {
	Result DeleteSecretResult
	Err    *APIError
}

// DeleteSecretResult reports the removed Secret (metadata only) and whether the
// file existed to delete.
type DeleteSecretResult struct {
	Secret  v1beta1.SecretDoc
	Deleted bool
}

// CreateSecretArgs is the wire request for CreateSecret.
type CreateSecretArgs struct {
	Doc v1beta1.SecretDoc
}

// CreateSecretReply is the wire response for CreateSecret.
type CreateSecretReply struct {
	Result CreateSecretResult
	Err    *APIError
}

// CreateSecretResult reports the outcome of a Secret write.
type CreateSecretResult struct {
	Secret  v1beta1.SecretDoc
	Created bool
}

// CreateVolumeArgs is the wire request for CreateVolume (issue #1236).
type CreateVolumeArgs struct {
	Doc v1beta1.VolumeDoc
}

// CreateVolumeReply is the wire response for CreateVolume.
type CreateVolumeReply struct {
	Result CreateVolumeResult
	Err    *APIError
}

// CreateVolumeResult reports the outcome of provisioning a Volume's directory.
type CreateVolumeResult struct {
	Volume  v1beta1.VolumeDoc
	Created bool
}

type GetVolumeArgs struct {
	Doc v1beta1.VolumeDoc
}

type GetVolumeReply struct {
	Result GetVolumeResult
	Err    *APIError
}

// GetVolumeResult carries the metadata-only view of a Volume (issue #1236). A
// Volume has no body, so this is its scope coordinates and name only.
type GetVolumeResult struct {
	Volume         v1beta1.VolumeDoc
	MetadataExists bool
}

type ListVolumesArgs struct {
	RealmName string
	SpaceName string
	StackName string
}

// ListVolumesReply carries metadata-only VolumeDocs — a Volume is never
// cell-scoped, so the filter bottoms out at stack (issue #1236).
type ListVolumesReply struct {
	Volumes []v1beta1.VolumeDoc
	Err     *APIError
}

type DeleteVolumeArgs struct {
	Doc v1beta1.VolumeDoc
}

type DeleteVolumeReply struct {
	Result DeleteVolumeResult
	Err    *APIError
}

// DeleteVolumeResult reports the removed Volume (metadata only) and whether the
// directory existed to delete.
type DeleteVolumeResult struct {
	Volume  v1beta1.VolumeDoc
	Deleted bool
}

type DeleteBlueprintArgs struct {
	Doc v1beta1.CellBlueprintDoc
}

type DeleteBlueprintReply struct {
	Result DeleteBlueprintResult
	Err    *APIError
}

// DeleteBlueprintResult reports the removed Blueprint (metadata only) and
// whether the file existed to delete.
type DeleteBlueprintResult struct {
	Blueprint v1beta1.CellBlueprintDoc
	Deleted   bool
}

type DeleteConfigArgs struct {
	Doc v1beta1.CellConfigDoc
}

type DeleteConfigReply struct {
	Result DeleteConfigResult
	Err    *APIError
}

// DeleteConfigResult reports the removed Config (metadata only), whether the
// file existed to delete, and the scope paths of any live cells that still
// carry the kukeon.io/config back-reference label (issue #644). BackRefCells is
// informational — deleting a Config never deletes the cell it materialized — so
// the CLI surfaces a one-line notice pointing at `kuke delete cell <name>`.
type DeleteConfigResult struct {
	Config       v1beta1.CellConfigDoc
	Deleted      bool
	BackRefCells []string
}

// ---- Purge ----

type PurgeRealmArgs struct {
	Doc     v1beta1.RealmDoc
	Force   bool
	Cascade bool
}

type PurgeRealmReply struct {
	Result PurgeRealmResult
	Err    *APIError
}

type PurgeRealmResult struct {
	Realm          v1beta1.RealmDoc
	RealmDeleted   bool
	PurgeSucceeded bool
	Force          bool
	Cascade        bool
	Deleted        []string
	Purged         []string
}

type PurgeSpaceArgs struct {
	Doc     v1beta1.SpaceDoc
	Force   bool
	Cascade bool
}

type PurgeSpaceReply struct {
	Result PurgeSpaceResult
	Err    *APIError
}

type PurgeSpaceResult struct {
	Space             v1beta1.SpaceDoc
	MetadataDeleted   bool
	CgroupDeleted     bool
	CNINetworkDeleted bool
	PurgeSucceeded    bool
	Force             bool
	Cascade           bool
	Deleted           []string
	Purged            []string
}

type PurgeStackArgs struct {
	Doc     v1beta1.StackDoc
	Force   bool
	Cascade bool
}

type PurgeStackReply struct {
	Result PurgeStackResult
	Err    *APIError
}

type PurgeStackResult struct {
	Stack   v1beta1.StackDoc
	Deleted []string
	Purged  []string
}

type PurgeCellArgs struct {
	Doc     v1beta1.CellDoc
	Force   bool
	Cascade bool
}

type PurgeCellReply struct {
	Result PurgeCellResult
	Err    *APIError
}

type PurgeCellResult struct {
	Cell              v1beta1.CellDoc
	ContainersDeleted bool
	CgroupDeleted     bool
	MetadataDeleted   bool
	PurgeSucceeded    bool
	Force             bool
	Cascade           bool
	Deleted           []string
	Purged            []string
}

// ---- Attach ----

// AttachContainerArgs identifies the target container for an attach request.
type AttachContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type AttachContainerReply struct {
	Result AttachContainerResult
	Err    *APIError
}

// AttachContainerResult carries the host-side coordinates the `kuke attach`
// client needs to drive the sbsh terminal. Bytes never traverse this RPC —
// the client opens HostSocketPath directly.
type AttachContainerResult struct {
	// HostSocketPath is the host path of the per-container sbsh terminal
	// socket. Inside the container the same inode is reachable at
	// /run/kukeon/tty/socket via the tty directory bind mount. Returned only
	// when the target container has Attachable=true and its task is Running;
	// the daemon errors with ErrAttachNotSupported when the target is not
	// Attachable, or ErrAttachTaskNotRunning when the task is not Running.
	HostSocketPath string
}

// ConnectContainerTerminalArgs identifies the target container whose sbsh
// control socket the daemon should connect to on the caller's behalf.
type ConnectContainerTerminalArgs struct {
	Doc v1beta1.ContainerDoc
}

// ConnectContainerTerminalReply is the wire reply for
// ConnectContainerTerminal. On success the connected control socket does
// not travel in the JSON body: the daemon's fd-aware server codec (see
// NewServerCodec) sends Conn's descriptor as SCM_RIGHTS ancillary data with
// the reply bytes and closes its own copy once the reply is written. Err is
// set, and no descriptor is sent, when a gate or the dial fails.
type ConnectContainerTerminalReply struct {
	Err *APIError

	// Conn is the daemon-side connected sbsh control socket. Never
	// serialized; consumed by the server codec when writing the reply.
	Conn *net.UnixConn `json:"-"`
}

// takePassedConn hands Conn to the server codec and clears it so the reply
// owns the descriptor exactly once.
func (r *ConnectContainerTerminalReply) takePassedConn() *net.UnixConn {
	c := r.Conn
	r.Conn = nil
	return c
}

// ---- Exec ----

// ExecRequest describes the process ExecContainer runs (issue #1432).
type ExecRequest struct {
	// Command is the argv, at least one element.
	Command []string
	// Root runs the process as uid 0 / gid 0. Mutually exclusive with User.
	Root bool
	// User is "uid", "uid:gid" or a name looked up in the container's
	// rootfs. Empty runs the process as the container's own user.
	User string
	// Env is KEY=VAL entries layered over the container's environment.
	Env []string
	// Workdir overrides the container's working directory when non-empty.
	Workdir string
	// Stdin attaches the caller's stdin frames to the process. Without it
	// the process reads an already-ended stdin.
	Stdin bool
	// TTY allocates a PTY inside the container.
	TTY bool
	// Cols and Rows are the initial terminal size; TTY only.
	Cols uint16
	Rows uint16
}

// ExecContainerArgs is the wire request of ExecContainer.
type ExecContainerArgs struct {
	Doc     v1beta1.ContainerDoc
	Request ExecRequest
	// PeerUID is the uid of the process on the other end of the socket. The
	// server codec fills it from SO_PEERCRED; it never travels on the wire.
	// It is only recorded in the audit log: nil is not a refusal.
	PeerUID *int `json:"-"`
}

// ExecContainerReply is the wire reply of ExecContainer. Like
// ConnectContainerTerminalReply, the connection does not travel in the JSON
// body: the server codec sends Conn's descriptor as SCM_RIGHTS with the
// reply bytes and closes the daemon's copy of that end. Err is set, and no
// descriptor is sent, when a gate fails.
type ExecContainerReply struct {
	Err *APIError

	// Conn is the caller's end of the session's socket pair. Never
	// serialized; consumed by the server codec when writing the reply.
	Conn *net.UnixConn `json:"-"`
}

// takePassedConn hands Conn to the server codec and clears it so the reply
// owns the descriptor exactly once.
func (r *ExecContainerReply) takePassedConn() *net.UnixConn {
	c := r.Conn
	r.Conn = nil
	return c
}

// ---- Log ----

// LogContainerArgs identifies the target container for a log request.
type LogContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type LogContainerReply struct {
	Result LogContainerResult
	Err    *APIError
}

// LogContainerResult carries the host-side coordinates of the per-container
// output stream. Bytes never traverse this RPC — a caller with filesystem
// access to the daemon's storage opens the returned host path directly.
// `kuke log` reads through ReadContainerLog instead, so it also works from a
// cell that does not mount /opt/kukeon (issue #1537).
//
// Exactly one of HostCapturePath or HostLogPath is non-empty:
//
//   - Attachable containers route stdout/stderr through the sbsh terminal
//     wrapper, which writes a tty byte stream to HostCapturePath.
//   - Non-Attachable containers (including kukeond) have the runtime shim
//     append stdout/stderr to HostLogPath via cio.LogFile.
type LogContainerResult struct {
	// HostCapturePath is the host path of the per-container sbsh capture
	// file. Inside the container the same inode is reachable at
	// /run/kukeon/tty/capture via the tty directory bind mount. Set only
	// when the target container has Attachable=true.
	HostCapturePath string

	// HostLogPath is the host path of the per-container log file written
	// by the containerd runtime shim (cio.LogFile mode). Set only for
	// non-Attachable containers; the file is shim-owned, kuke only reads.
	HostLogPath string
}

// Bounds for ReadContainerLog (issue #1537). A request with MaxBytes <= 0
// reads ContainerLogDefaultMaxBytes; larger values are clamped to
// ContainerLogMaxBytes so one reply never carries an unbounded payload.
const (
	ContainerLogDefaultMaxBytes = 64 << 10
	ContainerLogMaxBytes        = 1 << 20
)

// ContainerLogRequest selects a byte range of a container's output stream —
// the same file LogContainer resolves (the sbsh capture of an Attachable
// container, the runtime-shim log of a non-Attachable one).
//
// A non-negative Offset reads forward from that byte; callers pass the
// previous result's Next to continue. A negative Offset reads the newest
// MaxBytes ending at the current end of the stream (tail). An Offset beyond
// the current end returns no data and leaves Next at Offset: the stream was
// truncated or replaced, which the caller detects as Size < its cursor and
// answers by reading again from 0.
type ContainerLogRequest struct {
	Offset   int64
	MaxBytes int
}

// ContainerLogResult is one bounded chunk of a container's output stream.
//
// Unlike DaemonLogResult, Data is raw bytes, verbatim: an sbsh capture is a
// terminal byte stream (escape sequences, prompts without a trailing
// newline, non-UTF-8 input), so nothing is held back waiting for a newline
// and nothing is re-encoded.
type ContainerLogResult struct {
	// Source is the daemon-side path of the stream read (informational).
	// It is also set when the stream file does not exist, so callers can
	// name the missing path.
	Source string
	// Data is the chunk's bytes, exactly as stored.
	Data []byte
	// Start is the byte offset of Data's first byte in the stream.
	Start int64
	// Next is the offset to pass as ContainerLogRequest.Offset to continue.
	Next int64
	// Size is the stream's size in bytes when it was read.
	Size int64
	// Truncated reports that earlier bytes exist before Start that this
	// chunk omits (a tail read of a stream larger than the bound).
	Truncated bool
	// More reports that data beyond Next was already available when the
	// chunk was read (a forward read that hit the bound).
	More bool
}

// ReadContainerLogArgs is the wire request for ReadContainerLog.
type ReadContainerLogArgs struct {
	Doc     v1beta1.ContainerDoc
	Request ContainerLogRequest
}

// ReadContainerLogReply is the wire reply for ReadContainerLog.
type ReadContainerLogReply struct {
	Result ContainerLogResult
	Err    *APIError
}

// ---- Refresh ----

type RefreshAllArgs struct{}

type RefreshAllReply struct {
	Result RefreshAllResult
	Err    *APIError
}

type RefreshAllResult struct {
	RealmsFound       []string
	SpacesFound       []string
	StacksFound       []string
	CellsFound        []string
	ContainersFound   []string
	RealmsUpdated     []string
	SpacesUpdated     []string
	StacksUpdated     []string
	CellsUpdated      []string
	ContainersUpdated []string
	Errors            []string
}

// ---- Ping ----

// Bounds for ReadDaemonLogs (issue #1386, step 7). A request with
// MaxBytes <= 0 reads DaemonLogDefaultMaxBytes; larger values are clamped to
// DaemonLogMaxBytes so one reply never carries an unbounded payload.
const (
	DaemonLogDefaultMaxBytes = 64 << 10
	DaemonLogMaxBytes        = 1 << 20
)

// DaemonLogRequest selects a byte range of kukeond's log stream.
//
// A negative Offset reads the newest MaxBytes ending at the current end of
// the stream (tail). A non-negative Offset reads forward from that byte —
// callers pass the previous result's Next to follow the stream. An Offset
// beyond the current end means the source was truncated or replaced; the
// daemon then answers with a tail read and sets Reset.
type DaemonLogRequest struct {
	Offset   int64
	MaxBytes int
}

// DaemonLogResult is one bounded, ordered chunk of kukeond's log stream.
//
// Data holds only complete, newline-terminated lines in the order the
// source received them, verbatim: no timestamps, levels or other fields are
// added or parsed. A trailing line that is still being written is held back
// until its newline arrives; Next then points at its first byte. A single
// line longer than the read bound is returned in bound-sized pieces rather
// than stalling the cursor.
type DaemonLogResult struct {
	// Source is the daemon-side path of the log file read (informational).
	Source string
	// Data is the chunk's text; invalid UTF-8 is replaced with U+FFFD.
	Data string
	// Start is the byte offset of Data's first byte in the source.
	Start int64
	// Next is the offset to pass as DaemonLogRequest.Offset to continue.
	Next int64
	// Size is the source's size in bytes when it was read.
	Size int64
	// Truncated reports that earlier bytes exist before Start that this
	// chunk omits (a tail read of a stream larger than the bound).
	Truncated bool
	// More reports that complete data beyond Next was already available
	// when the chunk was read (a forward read that hit the bound).
	More bool
	// Reset reports that the requested Offset was beyond the source's end,
	// so the cursor no longer applied and a tail read was returned instead.
	Reset bool
}

// ReadDaemonLogsArgs is the wire request for ReadDaemonLogs.
type ReadDaemonLogsArgs struct {
	Request DaemonLogRequest
}

// ReadDaemonLogsReply is the wire reply for ReadDaemonLogs.
type ReadDaemonLogsReply struct {
	Result DaemonLogResult
	Err    *APIError
}

// PingArgs is the empty request payload for the Ping RPC.
type PingArgs struct{}

// PingReply carries the daemon's ack plus the daemon build version. Clients
// use Ping to confirm the RPC handler is serving (not just that the socket
// exists).
type PingReply struct {
	OK      bool
	Version string
	Err     *APIError
}

// ---- Apply ----

// ApplyDocumentsArgs carries a raw multi-document YAML blob. The server
// parses and validates; validation errors are returned in the Reply.Err.
//
// Team, when non-empty, switches the apply into per-team prune mode
// (issue #1027): every applied CellBlueprint / CellConfig has its
// `metadata.labels[kukeon.io/team]` set to Team before persistence, and
// after the apply loop the daemon enumerates daemon-stored Blueprint /
// Config objects carrying `kukeon.io/team=<Team>` and deletes those not
// in the applied set. The empty-string default preserves the historical
// no-team, no-prune `kuke apply -f` behavior.
type ApplyDocumentsArgs struct {
	RawYAML []byte
	Team    string
}

type ApplyDocumentsReply struct {
	Result ApplyDocumentsResult
	Err    *APIError
}

type ApplyDocumentsResult struct {
	Resources []ApplyResourceResult
}

// ApplyResourceResult is the per-resource outcome of an ApplyDocuments call.
// JSON/YAML tags preserve the lowercase `kuke apply -f -o json` shape that
// matches the sibling `kuke delete -f -o json` contract on
// [DeleteResourceResult] (the wire is gob and ignores these tags).
type ApplyResourceResult struct {
	Index   int               `json:"index"             yaml:"index"`
	Kind    string            `json:"kind"              yaml:"kind"`
	Name    string            `json:"name"              yaml:"name"`
	Action  string            `json:"action"            yaml:"action"`
	Error   string            `json:"error,omitempty"   yaml:"error,omitempty"`
	Changes []string          `json:"changes,omitempty" yaml:"changes,omitempty"`
	Details map[string]string `json:"details,omitempty" yaml:"details,omitempty"`
}

// ---- Delete ----

// DeleteDocumentsArgs carries a raw multi-document YAML blob plus the same
// cascade/force flags exposed on `kuke delete -f`. The server parses and
// validates; validation errors are returned in the Reply.Err.
type DeleteDocumentsArgs struct {
	RawYAML []byte
	Cascade bool
	Force   bool
}

type DeleteDocumentsReply struct {
	Result DeleteDocumentsResult
	Err    *APIError
}

type DeleteDocumentsResult struct {
	Resources []DeleteResourceResult
}

// DeleteResourceResult is the per-resource outcome of a DeleteDocuments call.
// JSON/YAML tags preserve the lowercase `kuke delete -f -o json` shape that
// previously came from internal/controller.ResourceDeleteResult's custom
// marshalers (the wire is gob and ignores these tags).
type DeleteResourceResult struct {
	Index    int               `json:"index"              yaml:"index"`
	Kind     string            `json:"kind"               yaml:"kind"`
	Name     string            `json:"name"               yaml:"name"`
	Action   string            `json:"action"             yaml:"action"`
	Error    string            `json:"error,omitempty"    yaml:"error,omitempty"`
	Cascaded []string          `json:"cascaded,omitempty" yaml:"cascaded,omitempty"`
	Details  map[string]string `json:"details,omitempty"  yaml:"details,omitempty"`
}

// ---- Image ----
//
// Image result types live here so the in-process `*local.Client` returned
// by the daemon-independent `kuke image *` path (#226) can keep using a
// wire-compatible shape without depending on internal/controller. Only the
// read-only ListImages / GetImage pair has wire envelopes: kukeond serves
// it for the web UI (#1406); load/delete/prune are not served over RPC.

// LoadImageResult reports the outcome of a `kuke image load` import: the
// realm/namespace it landed in and the canonical image refs containerd
// recorded.
type LoadImageResult struct {
	Realm     string
	Namespace string
	Images    []string
}

// ImageInfo is the rendered view of one containerd image. Size is best-
// effort: -1 is emitted when containerd cannot resolve the size locally so
// the CLI renders "-" rather than "0 B".
type ImageInfo struct {
	Name      string            `json:"name"                   yaml:"name"`
	Size      int64             `json:"size"                   yaml:"size"`
	CreatedAt time.Time         `json:"createdAt"              yaml:"createdAt"`
	Digest    string            `json:"digest"                 yaml:"digest"`
	MediaType string            `json:"mediaType"              yaml:"mediaType"`
	Labels    map[string]string `json:"labels"                 yaml:"labels"`
	// Capabilities are the capabilities the image declares in its
	// io.kukeon.capabilities config label; empty when it declares none.
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

// ListImagesResult lists the images present in a realm's containerd
// namespace.
type ListImagesResult struct {
	Realm     string
	Namespace string
	Images    []ImageInfo
}

// ListImagesArgs is the wire request for ListImages.
type ListImagesArgs struct {
	Realm string
}

// ListImagesReply is the wire response for ListImages.
type ListImagesReply struct {
	Result ListImagesResult
	Err    *APIError
}

// GetImageArgs is the wire request for GetImage.
type GetImageArgs struct {
	Realm string
	Ref   string
}

// GetImageReply is the wire response for GetImage.
type GetImageReply struct {
	Result GetImageResult
	Err    *APIError
}

// GetImageResult carries the metadata of one named image in a realm.
type GetImageResult struct {
	Realm     string
	Namespace string
	Image     ImageInfo
}

// DeleteImageResult reports the outcome of a `kuke image delete` removal.
type DeleteImageResult struct {
	Realm     string
	Namespace string
	Ref       string
}

// PruneImagesResult reports the outcome of a `kuke image prune`: the realm /
// namespace it ran against and the count of leases released vs. retained.
type PruneImagesResult struct {
	Realm          string
	Namespace      string
	LeasesDeleted  int
	LeasesRetained int
}

// ---- Cgroup detail (issue #1410) ----

// Cgroup scope kinds accepted by GetCgroupDetail.
const (
	CgroupKindRealm     = "realm"
	CgroupKindSpace     = "space"
	CgroupKindStack     = "stack"
	CgroupKindCell      = "cell"
	CgroupKindContainer = "container"
)

// CgroupValue states. A value the kernel does not report is never collapsed
// to 0: an absent interface file is CgroupValueAbsent, a kernel "max" is
// CgroupValueMax, and a read failure other than "not found" is
// CgroupValueUnreadable.
const (
	CgroupValueSet        = "set"
	CgroupValueMax        = "max"
	CgroupValueAbsent     = "absent"
	CgroupValueUnreadable = "unreadable"
)

// Declared resource sources for a container's spec.resources fields.
const (
	CgroupSourceContainer    = "container"
	CgroupSourceSpaceDefault = "space-default"
	CgroupSourceUnset        = "unset"
)

// CgroupScope names the realm, space, stack, cell or container whose cgroup
// GetCgroupDetail reads. Kind selects which coordinates are required.
type CgroupScope struct {
	Kind      string `json:"kind"`
	Realm     string `json:"realm"`
	Space     string `json:"space,omitempty"`
	Stack     string `json:"stack,omitempty"`
	Cell      string `json:"cell,omitempty"`
	Container string `json:"container,omitempty"`
}

type GetCgroupDetailArgs struct {
	Scope CgroupScope
}

type GetCgroupDetailReply struct {
	Result GetCgroupDetailResult
	Err    *APIError
}

// CgroupValue is one cgroup v2 interface value. Raw is the trimmed file (or
// key) content; Value is set only when State is CgroupValueSet and Raw
// parses as an integer.
type CgroupValue struct {
	State string `json:"state"`
	Raw   string `json:"raw,omitempty"`
	Value *int64 `json:"value,omitempty"`
}

// CgroupList is a whitespace-separated interface file such as
// cgroup.controllers.
type CgroupList struct {
	State  string   `json:"state"`
	Values []string `json:"values,omitempty"`
}

// CgroupCPUMax is cpu.max. QuotaMicros is nil when the quota is "max".
type CgroupCPUMax struct {
	State        string `json:"state"`
	Raw          string `json:"raw,omitempty"`
	QuotaMicros  *int64 `json:"quotaMicros,omitempty"`
	PeriodMicros *int64 `json:"periodMicros,omitempty"`
}

// CgroupIOMaxEntry is one device line of io.max.
type CgroupIOMaxEntry struct {
	Device string            `json:"device"`
	Limits map[string]string `json:"limits"`
}

// CgroupIOMax is io.max. An empty Entries with State CgroupValueSet means the
// file exists but no device carries a limit.
type CgroupIOMax struct {
	State   string             `json:"state"`
	Entries []CgroupIOMaxEntry `json:"entries,omitempty"`
}

type CgroupMemory struct {
	Max     CgroupValue `json:"max"`
	High    CgroupValue `json:"high"`
	SwapMax CgroupValue `json:"swapMax"`
	Current CgroupValue `json:"current"`
	Peak    CgroupValue `json:"peak"`
	OOM     CgroupValue `json:"oom"`
	OOMKill CgroupValue `json:"oomKill"`
}

type CgroupCPU struct {
	Max        CgroupCPUMax `json:"max"`
	Weight     CgroupValue  `json:"weight"`
	UsageUsec  CgroupValue  `json:"usageUsec"`
	UserUsec   CgroupValue  `json:"userUsec"`
	SystemUsec CgroupValue  `json:"systemUsec"`
}

type CgroupPids struct {
	Max     CgroupValue `json:"max"`
	Current CgroupValue `json:"current"`
}

// CgroupLive carries the point-in-time values kukeond read from the cgroup's
// interface files.
type CgroupLive struct {
	Controllers    CgroupList   `json:"controllers"`
	SubtreeControl CgroupList   `json:"subtreeControl"`
	Populated      CgroupValue  `json:"populated"`
	Memory         CgroupMemory `json:"memory"`
	CPU            CgroupCPU    `json:"cpu"`
	Pids           CgroupPids   `json:"pids"`
	IO             CgroupIOMax  `json:"io"`
}

// CgroupDeclaredResource is one container spec.resources field and where its
// value came from.
type CgroupDeclaredResource struct {
	Field  string `json:"field"`
	Value  *int64 `json:"value,omitempty"`
	Source string `json:"source"`
}

// GetCgroupDetailResult combines what a resource declares about its cgroup
// with the live values kukeond read from the hierarchy. Live is nil when the
// cgroup directory does not exist.
type GetCgroupDetailResult struct {
	Scope               CgroupScope              `json:"scope"`
	CgroupPath          string                   `json:"cgroupPath"`
	AbsolutePath        string                   `json:"absolutePath"`
	Exists              bool                     `json:"exists"`
	Ready               bool                     `json:"ready"`
	Reason              string                   `json:"reason,omitempty"`
	SubtreeControllers  []string                 `json:"subtreeControllers,omitempty"`
	NestedCgroupRuntime *bool                    `json:"nestedCgroupRuntime,omitempty"`
	HostCgroup          *bool                    `json:"hostCgroup,omitempty"`
	Declared            []CgroupDeclaredResource `json:"declared,omitempty"`
	Live                *CgroupLive              `json:"live,omitempty"`
}

// ---- Network detail (issue #1411) ----

// Network scope kinds accepted by GetNetworkDetail. Realms and stacks carry
// no networking fields, so they have no network scope.
const (
	NetworkKindSpace     = "space"
	NetworkKindCell      = "cell"
	NetworkKindContainer = "container"
)

// Space subnet sources. NetworkSubnetUnallocated means neither an allocator
// state file nor a conflist subnet exists yet.
const (
	NetworkSubnetAllocator   = "allocator"
	NetworkSubnetConflist    = "conflist"
	NetworkSubnetUnallocated = "unallocated"
)

// Sources for a value that may be set on the resource itself or fall back.
const (
	NetworkSourceSpec      = "spec"
	NetworkSourceDefault   = "default"
	NetworkSourceSpace     = "space"
	NetworkSourceContainer = "container"
)

// Cell IPv4 states. NetworkIPv4NotRunning means the root container's task
// is not running; NetworkIPv4HostNetwork means the cell shares the host
// netns and has no CNI attachment; NetworkIPv4NotAssigned means the task
// runs but no CNI result is cached.
const (
	NetworkIPv4Assigned    = "assigned"
	NetworkIPv4NotRunning  = "not-running"
	NetworkIPv4HostNetwork = "host-network"
	NetworkIPv4NotAssigned = "not-assigned"
)

// NetworkScope names the space, cell or container whose networking
// GetNetworkDetail reads. Kind selects which coordinates are required.
type NetworkScope struct {
	Kind      string `json:"kind"`
	Realm     string `json:"realm"`
	Space     string `json:"space"`
	Stack     string `json:"stack,omitempty"`
	Cell      string `json:"cell,omitempty"`
	Container string `json:"container,omitempty"`
}

type GetNetworkDetailArgs struct {
	Scope NetworkScope
}

type GetNetworkDetailReply struct {
	Result GetNetworkDetailResult
	Err    *APIError
}

// NetworkEgressRule is one space egress allow rule. Empty Ports means any
// port on the destination.
type NetworkEgressRule struct {
	Host  string `json:"host,omitempty"`
	CIDR  string `json:"cidr,omitempty"`
	Ports []int  `json:"ports,omitempty"`
}

// NetworkEgress is the space's declared egress policy. DefaultSource is
// NetworkSourceDefault when spec.network.egress is absent (admit-all).
type NetworkEgress struct {
	Default       string              `json:"default"`
	DefaultSource string              `json:"defaultSource"`
	Allow         []NetworkEgressRule `json:"allow,omitempty"`
}

// SpaceNetworkDetail is what the daemon knows about a space's CNI network.
type SpaceNetworkDetail struct {
	NetworkName      string        `json:"networkName"`
	BridgeName       string        `json:"bridgeName,omitempty"`
	SubnetCIDR       string        `json:"subnetCIDR,omitempty"`
	SubnetSource     string        `json:"subnetSource"`
	ParentCIDR       string        `json:"parentCIDR,omitempty"`
	ConfigPath       string        `json:"configPath"`
	ConfigPathSource string        `json:"configPathSource"`
	CNINetworkExists bool          `json:"cniNetworkExists"`
	Egress           NetworkEgress `json:"egress"`
}

// ContainerNetworkSummary is one container's network-relevant spec fields.
type ContainerNetworkSummary struct {
	ID          string   `json:"id"`
	Root        bool     `json:"root"`
	HostNetwork bool     `json:"hostNetwork"`
	HostPID     bool     `json:"hostPID"`
	Ports       []string `json:"ports,omitempty"`
}

// CellNetworkDetail is a cell's bridge, IPv4 and network mode. HostNetwork
// is true when any container sets hostNetwork, the same rule the runner uses
// to put the root container (and so the whole cell) on the host netns.
type CellNetworkDetail struct {
	BridgeName    string                    `json:"bridgeName,omitempty"`
	HostNetwork   bool                      `json:"hostNetwork"`
	IPv4          string                    `json:"ipv4,omitempty"`
	IPv4State     string                    `json:"ipv4State"`
	RootContainer string                    `json:"rootContainer,omitempty"`
	Containers    []ContainerNetworkSummary `json:"containers,omitempty"`
}

// ContainerNetworkDetail is a container's declared network spec. Ports,
// Networks and NetworksAliases are stored but not applied at runtime yet
// (#1002).
type ContainerNetworkDetail struct {
	ID                  string   `json:"id"`
	Root                bool     `json:"root"`
	HostNetwork         bool     `json:"hostNetwork"`
	HostPID             bool     `json:"hostPID"`
	CNIConfigPath       string   `json:"cniConfigPath"`
	CNIConfigPathSource string   `json:"cniConfigPathSource"`
	Ports               []string `json:"ports,omitempty"`
	Networks            []string `json:"networks,omitempty"`
	NetworksAliases     []string `json:"networksAliases,omitempty"`
}

// GetNetworkDetailResult combines what a resource declares about its
// networking with the runtime facts kukeond reads. Space is always the
// enclosing space; Cell is set for cell and container scopes; Container for
// container scopes.
type GetNetworkDetailResult struct {
	Scope     NetworkScope            `json:"scope"`
	Space     SpaceNetworkDetail      `json:"space"`
	Cell      *CellNetworkDetail      `json:"cell,omitempty"`
	Container *ContainerNetworkDetail `json:"container,omitempty"`
}

// ---- Teams (issue #1618) ----

// Team and TeamDefaults documents travel as YAML bytes, never as JSON-encoded
// structs: the declaration types keep an omitted list apart from an empty one
// only in their YAML form. Writes go through ApplyDocuments; these are the
// reads and the delete.

// TeamRef names one stored Team. An empty Realm means realm default.
type TeamRef struct {
	Name  string
	Realm string
}

// TeamSummary is what kukeond reads out of a stored Team for a list: where
// its project comes from, what it selects, and the Configs it has now,
// counted from the kukeon.io/team label.
type TeamSummary struct {
	Name  string `json:"name"`
	Realm string `json:"realm"`
	// ProjectURL and ProjectRef are the team's spec.projectSource; empty for
	// a team without one (the kuke team).
	ProjectURL string `json:"projectUrl,omitempty"`
	ProjectRef string `json:"projectRef,omitempty"`
	// Roles and Harnesses are the names the declaration selects. Inherited
	// reports that the declaration omits the list and takes the defaults'.
	Roles              []string `json:"roles"`
	RolesInherited     bool     `json:"rolesInherited"`
	Harnesses          []string `json:"harnesses"`
	HarnessesInherited bool     `json:"harnessesInherited"`
	// Configs are the names of the stored CellConfigs labelled with the team.
	Configs []string `json:"configs"`
}

type GetTeamArgs struct {
	Ref TeamRef
}

type GetTeamReply struct {
	Result GetTeamResult
	Err    *APIError
}

// GetTeamResult carries a stored Team's document and its summary. Exists is
// false, with no error, when no Team of the name is stored.
type GetTeamResult struct {
	Exists   bool
	Document []byte
	Summary  TeamSummary
}

type ListTeamsArgs struct {
	// Realm filters the list; empty lists every realm.
	Realm string
}

type ListTeamsReply struct {
	Teams []GetTeamResult
	Err   *APIError
}

type DeleteTeamArgs struct {
	Ref TeamRef
}

type DeleteTeamReply struct {
	Result DeleteTeamResult
	Err    *APIError
}

// DeleteTeamResult reports what deleting a team removed and what it left: the
// team's Configs and the shared blueprints nothing else references go; cells
// and volumes stay.
type DeleteTeamResult struct {
	Deleted bool
	// Removed are the Configs and blueprints deleted, as an apply reports a
	// prune.
	Removed []ApplyResourceResult
	// LiveCells are the scope paths of the cells still running from a
	// removed Config.
	LiveCells []string
	// KeptVolumes are the scope paths of the removed Configs' state volumes,
	// which stay until deleted with kuke delete volume.
	KeptVolumes []string
}

type GetTeamDefaultsArgs struct {
	// Realm is the realm whose TeamDefaults to read; empty means default.
	Realm string
}

type GetTeamDefaultsReply struct {
	Result GetTeamDefaultsResult
	Err    *APIError
}

// GetTeamDefaultsResult carries a realm's stored TeamDefaults document.
// Exists is false, with no error, when the realm stores none.
type GetTeamDefaultsResult struct {
	Exists   bool
	Realm    string
	Document []byte
}

// ---- Sources (issue #1643) ----

// Source documents travel as YAML bytes like Team documents. Writes go
// through ApplyDocuments; these are the reads and the delete.

// SourceRef names one stored Source. An empty Realm means realm default.
type SourceRef struct {
	Name  string
	Realm string
}

// SourceSummary is what kukeond reads out of a stored Source for a list.
type SourceSummary struct {
	Name  string `json:"name"`
	Realm string `json:"realm"`
	// URL is the Source's address as written.
	URL string `json:"url"`
	// Ref is its revision: "branch main", "tag v1" or "commit <sha>".
	Ref string `json:"ref"`
	// Identity names the stored git identity the Source states, written
	// name[@realm]; empty when it states none.
	Identity string `json:"identity,omitempty"`
}

type GetSourceArgs struct {
	Ref SourceRef
}

type GetSourceReply struct {
	Result GetSourceResult
	Err    *APIError
}

// GetSourceResult carries a stored Source's document and its summary.
// Exists is false, with no error, when no Source of the name is stored.
type GetSourceResult struct {
	Exists   bool
	Document []byte
	Summary  SourceSummary
}

type ListSourcesArgs struct {
	// Realm filters the list; empty lists every realm.
	Realm string
}

type ListSourcesReply struct {
	Sources []GetSourceResult
	Err     *APIError
}

type DeleteSourceArgs struct {
	Ref SourceRef
}

type DeleteSourceReply struct {
	Result DeleteSourceResult
	Err    *APIError
}

// DeleteSourceResult reports a deleted Source. A Source something references
// is refused with ErrSourceInUse, naming the references.
type DeleteSourceResult struct {
	Deleted bool
}

// ---- Git fetch staging (issue #1643, D10) ----

// GitFetchRequest asks kukeond to stage the stored git credential a fetch kuke
// makes on the host uses for Address: IdentityRef's when set, else the
// realm's default git identity when it has a credential for the address's
// host.
type GitFetchRequest struct {
	// Realm is the realm whose default git identity applies; empty means
	// default.
	Realm string
	// Address is the repository address, in any form a Source accepts.
	Address string
	// IdentityRef is the Source's own git identity, when it states one.
	IdentityRef *v1beta1.ContainerSecretRef
}

type StageGitFetchArgs struct {
	Request GitFetchRequest
	// PeerUID is the uid of the process on the other end of the socket. The
	// server codec fills it from SO_PEERCRED; it never travels on the wire,
	// so a caller cannot name another user's uid.
	PeerUID *int `json:"-"`
}

type StageGitFetchReply struct {
	Result StageGitFetchResult
	Err    *APIError
}

// StageGitFetchResult says where the staged credential files are. The reply
// never carries their content (#619). Staged is false, with no error, when
// neither the Source nor the realm default names a credential for the
// address: kuke then fetches with the caller's own git configuration.
type StageGitFetchResult struct {
	Staged bool
	// ID names the staging for ReleaseGitFetch.
	ID string
	// Dir is the staged directory, owned by the calling user and readable by
	// it only. It holds the bundle a cell's git directory holds, with every
	// path pointing inside Dir.
	Dir string
}

type ReleaseGitFetchArgs struct {
	ID string
	// PeerUID is filled by the server codec, as for StageGitFetchArgs.
	PeerUID *int `json:"-"`
}

type ReleaseGitFetchReply struct {
	Err *APIError
}
