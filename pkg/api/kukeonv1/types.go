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

// ---- Container ----

type CreateContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type CreateContainerReply struct {
	Result CreateContainerResult
	Err    *APIError
}

type CreateContainerResult struct {
	Container v1beta1.ContainerDoc

	CellMetadataExistsPre  bool
	CellMetadataExistsPost bool
	ContainerExistsPre     bool
	ContainerExistsPost    bool
	ContainerCreated       bool
	Started                bool
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

// GetSecretResult carries the metadata-only view of a Secret. Secret.Spec.Data
// is never populated — the bytes do not traverse this RPC (issue #619/#622).
type GetSecretResult struct {
	Secret         v1beta1.SecretDoc
	MetadataExists bool
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

type DeleteContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type DeleteContainerReply struct {
	Result DeleteContainerResult
	Err    *APIError
}

type DeleteContainerResult struct {
	Container          v1beta1.ContainerDoc
	CellMetadataExists bool
	ContainerExists    bool
	Deleted            []string
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

type PurgeContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type PurgeContainerReply struct {
	Result PurgeContainerResult
	Err    *APIError
}

type PurgeContainerResult struct {
	Container          v1beta1.ContainerDoc
	CellMetadataExists bool
	ContainerExists    bool
	Deleted            []string
	Purged             []string
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

// ---- Log ----

// LogContainerArgs identifies the target container for a log request.
type LogContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type LogContainerReply struct {
	Result LogContainerResult
	Err    *APIError
}

// LogContainerResult carries the host-side coordinates the `kuke log` client
// needs to read the per-container output stream. Bytes never traverse this
// RPC — the client opens the returned host path directly.
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
// Image result types live here (and not on the RPC interface) so the in-
// process `*local.Client` returned by the daemon-independent `kuke image *`
// path (#226) can keep using a wire-compatible shape without depending on
// internal/controller. The Args/Reply wire envelopes were retired with the
// RPC handlers — the daemon does not serve image methods.

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
	Name      string
	Size      int64
	CreatedAt time.Time
	Digest    string
	MediaType string
	Labels    map[string]string
}

// ListImagesResult lists the images present in a realm's containerd
// namespace.
type ListImagesResult struct {
	Realm     string
	Namespace string
	Images    []ImageInfo
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
