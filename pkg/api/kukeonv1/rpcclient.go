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
	"context"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"sync"
	"time"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const defaultDialTimeout = 5 * time.Second

// UnixClient is a Client backed by a persistent net/rpc connection over a
// unix socket using the JSON-RPC codec. The connection is lazily established
// on first call and reused for subsequent calls on the same instance.
type UnixClient struct {
	sockPath    string
	dialTimeout time.Duration

	mu   sync.Mutex
	conn net.Conn
	rpcc *rpc.Client
}

// UnixOption configures a UnixClient.
type UnixOption func(*UnixClient)

// WithDialTimeout overrides the dial timeout for NewUnixClient.
func WithDialTimeout(d time.Duration) UnixOption {
	return func(c *UnixClient) { c.dialTimeout = d }
}

// NewUnixClient returns a ctx-aware Client that dials the given unix socket
// path on first use and reuses the connection for subsequent calls.
func NewUnixClient(sockPath string, opts ...UnixOption) *UnixClient {
	c := &UnixClient{sockPath: sockPath, dialTimeout: defaultDialTimeout}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *UnixClient) ensureConn(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpcc != nil {
		return nil
	}
	d := net.Dialer{Timeout: c.dialTimeout}
	conn, err := d.DialContext(ctx, "unix", c.sockPath)
	if err != nil {
		return fmt.Errorf("dial kukeond at %s: %w", c.sockPath, err)
	}
	c.conn = conn
	c.rpcc = rpc.NewClientWithCodec(jsonrpc.NewClientCodec(conn))
	return nil
}

// Close terminates the underlying rpc.Client connection if open. Safe to call
// multiple times.
func (c *UnixClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpcc == nil {
		return nil
	}
	err := c.rpcc.Close()
	c.rpcc = nil
	c.conn = nil
	return err
}

// call performs a single RPC with ctx-aware cancellation. When ctx is
// cancelled we nudge the conn with a short deadline so the blocked call
// returns promptly, then drop the rpc.Client so the next call reconnects.
func (c *UnixClient) call(ctx context.Context, method string, args, reply any) error {
	if err := c.ensureConn(ctx); err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- c.rpcc.Call(method, args, reply) }()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		if c.conn != nil {
			//nolint:mnd // short deadline to unblock a pending call
			_ = c.conn.SetDeadline(time.Now().Add(10 * time.Millisecond))
		}
		c.mu.Unlock()
		_ = c.Close()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// CreateRealm implements Client.
func (c *UnixClient) CreateRealm(ctx context.Context, doc v1beta1.RealmDoc) (CreateRealmResult, error) {
	args := &CreateRealmArgs{Doc: doc}
	reply := &CreateRealmReply{}
	if err := c.call(ctx, MethodCreateRealm, args, reply); err != nil {
		return CreateRealmResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// CreateSpace implements Client.
func (c *UnixClient) CreateSpace(ctx context.Context, doc v1beta1.SpaceDoc) (CreateSpaceResult, error) {
	args := &CreateSpaceArgs{Doc: doc}
	reply := &CreateSpaceReply{}
	if err := c.call(ctx, MethodCreateSpace, args, reply); err != nil {
		return CreateSpaceResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// CreateStack implements Client.
func (c *UnixClient) CreateStack(ctx context.Context, doc v1beta1.StackDoc) (CreateStackResult, error) {
	args := &CreateStackArgs{Doc: doc}
	reply := &CreateStackReply{}
	if err := c.call(ctx, MethodCreateStack, args, reply); err != nil {
		return CreateStackResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// CreateCell implements Client.
func (c *UnixClient) CreateCell(ctx context.Context, doc v1beta1.CellDoc) (CreateCellResult, error) {
	args := &CreateCellArgs{Doc: doc}
	reply := &CreateCellReply{}
	if err := c.call(ctx, MethodCreateCell, args, reply); err != nil {
		return CreateCellResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// CreateContainer implements Client.
func (c *UnixClient) CreateContainer(ctx context.Context, doc v1beta1.ContainerDoc) (CreateContainerResult, error) {
	args := &CreateContainerArgs{Doc: doc}
	reply := &CreateContainerReply{}
	if err := c.call(ctx, MethodCreateContainer, args, reply); err != nil {
		return CreateContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetRealm implements Client.
func (c *UnixClient) GetRealm(ctx context.Context, doc v1beta1.RealmDoc) (GetRealmResult, error) {
	args := &GetRealmArgs{Doc: doc}
	reply := &GetRealmReply{}
	if err := c.call(ctx, MethodGetRealm, args, reply); err != nil {
		return GetRealmResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetSpace implements Client.
func (c *UnixClient) GetSpace(ctx context.Context, doc v1beta1.SpaceDoc) (GetSpaceResult, error) {
	args := &GetSpaceArgs{Doc: doc}
	reply := &GetSpaceReply{}
	if err := c.call(ctx, MethodGetSpace, args, reply); err != nil {
		return GetSpaceResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetStack implements Client.
func (c *UnixClient) GetStack(ctx context.Context, doc v1beta1.StackDoc) (GetStackResult, error) {
	args := &GetStackArgs{Doc: doc}
	reply := &GetStackReply{}
	if err := c.call(ctx, MethodGetStack, args, reply); err != nil {
		return GetStackResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetCell implements Client.
func (c *UnixClient) GetCell(ctx context.Context, doc v1beta1.CellDoc) (GetCellResult, error) {
	args := &GetCellArgs{Doc: doc}
	reply := &GetCellReply{}
	if err := c.call(ctx, MethodGetCell, args, reply); err != nil {
		return GetCellResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetContainer implements Client.
func (c *UnixClient) GetContainer(ctx context.Context, doc v1beta1.ContainerDoc) (GetContainerResult, error) {
	args := &GetContainerArgs{Doc: doc}
	reply := &GetContainerReply{}
	if err := c.call(ctx, MethodGetContainer, args, reply); err != nil {
		return GetContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ListRealms implements Client.
func (c *UnixClient) ListRealms(ctx context.Context) ([]v1beta1.RealmDoc, error) {
	args := &ListRealmsArgs{}
	reply := &ListRealmsReply{}
	if err := c.call(ctx, MethodListRealms, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Realms, FromAPIError(reply.Err)
	}
	return reply.Realms, nil
}

// ListSpaces implements Client.
func (c *UnixClient) ListSpaces(ctx context.Context, realmName string) ([]v1beta1.SpaceDoc, error) {
	args := &ListSpacesArgs{RealmName: realmName}
	reply := &ListSpacesReply{}
	if err := c.call(ctx, MethodListSpaces, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Spaces, FromAPIError(reply.Err)
	}
	return reply.Spaces, nil
}

// ListStacks implements Client.
func (c *UnixClient) ListStacks(ctx context.Context, realmName, spaceName string) ([]v1beta1.StackDoc, error) {
	args := &ListStacksArgs{RealmName: realmName, SpaceName: spaceName}
	reply := &ListStacksReply{}
	if err := c.call(ctx, MethodListStacks, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Stacks, FromAPIError(reply.Err)
	}
	return reply.Stacks, nil
}

// ListCells implements Client.
func (c *UnixClient) ListCells(ctx context.Context, realmName, spaceName, stackName string) ([]v1beta1.CellDoc, error) {
	args := &ListCellsArgs{RealmName: realmName, SpaceName: spaceName, StackName: stackName}
	reply := &ListCellsReply{}
	if err := c.call(ctx, MethodListCells, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Cells, FromAPIError(reply.Err)
	}
	return reply.Cells, nil
}

// ListContainers implements Client.
func (c *UnixClient) ListContainers(
	ctx context.Context,
	realmName, spaceName, stackName, cellName string,
) ([]v1beta1.ContainerSpec, error) {
	args := &ListContainersArgs{RealmName: realmName, SpaceName: spaceName, StackName: stackName, CellName: cellName}
	reply := &ListContainersReply{}
	if err := c.call(ctx, MethodListContainers, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Containers, FromAPIError(reply.Err)
	}
	return reply.Containers, nil
}

// StartCell implements Client.
func (c *UnixClient) StartCell(ctx context.Context, doc v1beta1.CellDoc) (StartCellResult, error) {
	args := &StartCellArgs{Doc: doc}
	reply := &StartCellReply{}
	if err := c.call(ctx, MethodStartCell, args, reply); err != nil {
		return StartCellResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// StartContainer implements Client.
func (c *UnixClient) StartContainer(ctx context.Context, doc v1beta1.ContainerDoc) (StartContainerResult, error) {
	args := &StartContainerArgs{Doc: doc}
	reply := &StartContainerReply{}
	if err := c.call(ctx, MethodStartContainer, args, reply); err != nil {
		return StartContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// AttachContainer implements Client.
func (c *UnixClient) AttachContainer(
	ctx context.Context,
	doc v1beta1.ContainerDoc,
) (AttachContainerResult, error) {
	args := &AttachContainerArgs{Doc: doc}
	reply := &AttachContainerReply{}
	if err := c.call(ctx, MethodAttachContainer, args, reply); err != nil {
		return AttachContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// LogContainer implements Client.
func (c *UnixClient) LogContainer(
	ctx context.Context,
	doc v1beta1.ContainerDoc,
) (LogContainerResult, error) {
	args := &LogContainerArgs{Doc: doc}
	reply := &LogContainerReply{}
	if err := c.call(ctx, MethodLogContainer, args, reply); err != nil {
		return LogContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// StopCell implements Client.
func (c *UnixClient) StopCell(ctx context.Context, doc v1beta1.CellDoc) (StopCellResult, error) {
	args := &StopCellArgs{Doc: doc}
	reply := &StopCellReply{}
	if err := c.call(ctx, MethodStopCell, args, reply); err != nil {
		return StopCellResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// StopContainer implements Client.
func (c *UnixClient) StopContainer(ctx context.Context, doc v1beta1.ContainerDoc) (StopContainerResult, error) {
	args := &StopContainerArgs{Doc: doc}
	reply := &StopContainerReply{}
	if err := c.call(ctx, MethodStopContainer, args, reply); err != nil {
		return StopContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// KillCell implements Client.
func (c *UnixClient) KillCell(ctx context.Context, doc v1beta1.CellDoc) (KillCellResult, error) {
	args := &KillCellArgs{Doc: doc}
	reply := &KillCellReply{}
	if err := c.call(ctx, MethodKillCell, args, reply); err != nil {
		return KillCellResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// KillContainer implements Client.
func (c *UnixClient) KillContainer(ctx context.Context, doc v1beta1.ContainerDoc) (KillContainerResult, error) {
	args := &KillContainerArgs{Doc: doc}
	reply := &KillContainerReply{}
	if err := c.call(ctx, MethodKillContainer, args, reply); err != nil {
		return KillContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteRealm implements Client.
func (c *UnixClient) DeleteRealm(
	ctx context.Context,
	doc v1beta1.RealmDoc,
	force, cascade bool,
) (DeleteRealmResult, error) {
	args := &DeleteRealmArgs{Doc: doc, Force: force, Cascade: cascade}
	reply := &DeleteRealmReply{}
	if err := c.call(ctx, MethodDeleteRealm, args, reply); err != nil {
		return DeleteRealmResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteSpace implements Client.
func (c *UnixClient) DeleteSpace(
	ctx context.Context,
	doc v1beta1.SpaceDoc,
	force, cascade bool,
) (DeleteSpaceResult, error) {
	args := &DeleteSpaceArgs{Doc: doc, Force: force, Cascade: cascade}
	reply := &DeleteSpaceReply{}
	if err := c.call(ctx, MethodDeleteSpace, args, reply); err != nil {
		return DeleteSpaceResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteStack implements Client.
func (c *UnixClient) DeleteStack(
	ctx context.Context,
	doc v1beta1.StackDoc,
	force, cascade bool,
) (DeleteStackResult, error) {
	args := &DeleteStackArgs{Doc: doc, Force: force, Cascade: cascade}
	reply := &DeleteStackReply{}
	if err := c.call(ctx, MethodDeleteStack, args, reply); err != nil {
		return DeleteStackResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteCell implements Client.
func (c *UnixClient) DeleteCell(ctx context.Context, doc v1beta1.CellDoc) (DeleteCellResult, error) {
	args := &DeleteCellArgs{Doc: doc}
	reply := &DeleteCellReply{}
	if err := c.call(ctx, MethodDeleteCell, args, reply); err != nil {
		return DeleteCellResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteContainer implements Client.
func (c *UnixClient) DeleteContainer(ctx context.Context, doc v1beta1.ContainerDoc) (DeleteContainerResult, error) {
	args := &DeleteContainerArgs{Doc: doc}
	reply := &DeleteContainerReply{}
	if err := c.call(ctx, MethodDeleteContainer, args, reply); err != nil {
		return DeleteContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// PurgeRealm implements Client.
func (c *UnixClient) PurgeRealm(
	ctx context.Context,
	doc v1beta1.RealmDoc,
	force, cascade bool,
) (PurgeRealmResult, error) {
	args := &PurgeRealmArgs{Doc: doc, Force: force, Cascade: cascade}
	reply := &PurgeRealmReply{}
	if err := c.call(ctx, MethodPurgeRealm, args, reply); err != nil {
		return PurgeRealmResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// PurgeSpace implements Client.
func (c *UnixClient) PurgeSpace(
	ctx context.Context,
	doc v1beta1.SpaceDoc,
	force, cascade bool,
) (PurgeSpaceResult, error) {
	args := &PurgeSpaceArgs{Doc: doc, Force: force, Cascade: cascade}
	reply := &PurgeSpaceReply{}
	if err := c.call(ctx, MethodPurgeSpace, args, reply); err != nil {
		return PurgeSpaceResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// PurgeStack implements Client.
func (c *UnixClient) PurgeStack(
	ctx context.Context,
	doc v1beta1.StackDoc,
	force, cascade bool,
) (PurgeStackResult, error) {
	args := &PurgeStackArgs{Doc: doc, Force: force, Cascade: cascade}
	reply := &PurgeStackReply{}
	if err := c.call(ctx, MethodPurgeStack, args, reply); err != nil {
		return PurgeStackResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// PurgeCell implements Client.
func (c *UnixClient) PurgeCell(ctx context.Context, doc v1beta1.CellDoc, force, cascade bool) (PurgeCellResult, error) {
	args := &PurgeCellArgs{Doc: doc, Force: force, Cascade: cascade}
	reply := &PurgeCellReply{}
	if err := c.call(ctx, MethodPurgeCell, args, reply); err != nil {
		return PurgeCellResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// PurgeContainer implements Client.
func (c *UnixClient) PurgeContainer(ctx context.Context, doc v1beta1.ContainerDoc) (PurgeContainerResult, error) {
	args := &PurgeContainerArgs{Doc: doc}
	reply := &PurgeContainerReply{}
	if err := c.call(ctx, MethodPurgeContainer, args, reply); err != nil {
		return PurgeContainerResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// RefreshAll implements Client.
func (c *UnixClient) RefreshAll(ctx context.Context) (RefreshAllResult, error) {
	args := &RefreshAllArgs{}
	reply := &RefreshAllReply{}
	if err := c.call(ctx, MethodRefreshAll, args, reply); err != nil {
		return RefreshAllResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ApplyDocuments implements Client.
func (c *UnixClient) ApplyDocuments(ctx context.Context, rawYAML []byte) (ApplyDocumentsResult, error) {
	args := &ApplyDocumentsArgs{RawYAML: rawYAML}
	reply := &ApplyDocumentsReply{}
	if err := c.call(ctx, MethodApplyDocuments, args, reply); err != nil {
		return ApplyDocumentsResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// LoadImage implements Client.
func (c *UnixClient) LoadImage(ctx context.Context, realm string, tarball []byte) (LoadImageResult, error) {
	args := &LoadImageArgs{Realm: realm, Tarball: tarball}
	reply := &LoadImageReply{}
	if err := c.call(ctx, MethodLoadImage, args, reply); err != nil {
		return LoadImageResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// Ping implements Client.
func (c *UnixClient) Ping(ctx context.Context) error {
	args := &PingArgs{}
	reply := &PingReply{}
	if err := c.call(ctx, MethodPing, args, reply); err != nil {
		return err
	}
	if reply.Err != nil {
		return FromAPIError(reply.Err)
	}
	return nil
}
