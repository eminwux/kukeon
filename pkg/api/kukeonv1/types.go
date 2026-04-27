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

import v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"

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

type StartContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type StartContainerReply struct {
	Result StartContainerResult
	Err    *APIError
}

type StartContainerResult struct {
	Container v1beta1.ContainerDoc
	Started   bool
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

type StopContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type StopContainerReply struct {
	Result StopContainerResult
	Err    *APIError
}

type StopContainerResult struct {
	Container v1beta1.ContainerDoc
	Stopped   bool
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

type KillContainerArgs struct {
	Doc v1beta1.ContainerDoc
}

type KillContainerReply struct {
	Result KillContainerResult
	Err    *APIError
}

type KillContainerResult struct {
	Container v1beta1.ContainerDoc
	Killed    bool
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
	// when the target container has Attachable=true; otherwise the daemon
	// errors with ErrAttachNotSupported.
	HostSocketPath string
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
type ApplyDocumentsArgs struct {
	RawYAML []byte
}

type ApplyDocumentsReply struct {
	Result ApplyDocumentsResult
	Err    *APIError
}

type ApplyDocumentsResult struct {
	Resources []ApplyResourceResult
}

type ApplyResourceResult struct {
	Index   int
	Kind    string
	Name    string
	Action  string
	Error   string
	Changes []string
	Details map[string]string
}
