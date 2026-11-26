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
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
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

// wrapDialError converts a unix-socket dial error into a user-facing error.
// EACCES on the kukeond socket almost always means the caller is not a member
// of the `kukeon` group — surface the remediation instead of a raw syscall
// error. Other failure modes (socket missing, daemon down, timeout) pass
// through unchanged so their existing messages are preserved.
func wrapDialError(sockPath string, err error) error {
	if errors.Is(err, syscall.EACCES) || errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf(
			"dial kukeond at %s: permission denied — add yourself to the kukeon group "+
				"(sudo usermod -aG kukeon $USER), then log out and back in: %w",
			sockPath, err,
		)
	}
	return fmt.Errorf("dial kukeond at %s: %w", sockPath, err)
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

// ensureConn returns the live rpc.Client, dialing on first use. The caller
// uses the returned pointer rather than re-reading c.rpcc, which a concurrent
// Close (the ctx-cancel path in call) may nil out.
func (c *UnixClient) ensureConn(ctx context.Context) (*rpc.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpcc != nil {
		return c.rpcc, nil
	}
	d := net.Dialer{Timeout: c.dialTimeout}
	conn, err := d.DialContext(ctx, "unix", c.sockPath)
	if err != nil {
		return nil, wrapDialError(c.sockPath, err)
	}
	c.conn = conn
	c.rpcc = rpc.NewClientWithCodec(jsonrpc.NewClientCodec(conn))
	return c.rpcc, nil
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
	rpcc, err := c.ensureConn(ctx)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- rpcc.Call(method, args, reply) }()
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
		return translateCallError(method, err)
	}
}

// translateCallError maps net/rpc's "can't find method" server error — the
// signature of a client newer than the running daemon — to
// errdefs.ErrDaemonMethodUnsupported so callers can tell version skew apart
// from a daemon that is down.
func translateCallError(method string, err error) error {
	var serverErr rpc.ServerError
	if errors.As(err, &serverErr) && strings.HasPrefix(string(serverErr), "rpc: can't find ") {
		return fmt.Errorf("%w: %s (%s)", errdefs.ErrDaemonMethodUnsupported, method, string(serverErr))
	}
	return err
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

// MaterializeCell implements Client.
func (c *UnixClient) MaterializeCell(ctx context.Context, doc v1beta1.CellDoc) (CreateCellResult, error) {
	args := &MaterializeCellArgs{Doc: doc}
	reply := &MaterializeCellReply{}
	if err := c.call(ctx, MethodMaterializeCell, args, reply); err != nil {
		return CreateCellResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// PreviewCellFromConfig implements Client.
func (c *UnixClient) PreviewCellFromConfig(
	ctx context.Context, req CellFromConfigRequest,
) (PreviewCellFromConfigResult, error) {
	args := &PreviewCellFromConfigArgs{Request: req}
	reply := &PreviewCellFromConfigReply{}
	if err := c.call(ctx, MethodPreviewCellFromConfig, args, reply); err != nil {
		return PreviewCellFromConfigResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// CreateCellFromConfig implements Client.
func (c *UnixClient) CreateCellFromConfig(
	ctx context.Context, req CellFromConfigRequest,
) (CreateCellFromConfigResult, error) {
	args := &CreateCellFromConfigArgs{Request: req}
	reply := &CreateCellFromConfigReply{}
	if err := c.call(ctx, MethodCreateCellFromConfig, args, reply); err != nil {
		return CreateCellFromConfigResult{}, err
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

// GetSecret implements Client.
func (c *UnixClient) GetSecret(ctx context.Context, doc v1beta1.SecretDoc) (GetSecretResult, error) {
	args := &GetSecretArgs{Doc: doc}
	reply := &GetSecretReply{}
	if err := c.call(ctx, MethodGetSecret, args, reply); err != nil {
		return GetSecretResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ReadSecret implements Client (#1652).
func (c *UnixClient) ReadSecret(ctx context.Context, doc v1beta1.SecretDoc) (GetSecretResult, error) {
	args := &ReadSecretArgs{Doc: doc}
	reply := &ReadSecretReply{}
	if err := c.call(ctx, MethodReadSecret, args, reply); err != nil {
		return GetSecretResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetVolume implements Client (#1236).
func (c *UnixClient) GetVolume(ctx context.Context, doc v1beta1.VolumeDoc) (GetVolumeResult, error) {
	args := &GetVolumeArgs{Doc: doc}
	reply := &GetVolumeReply{}
	if err := c.call(ctx, MethodGetVolume, args, reply); err != nil {
		return GetVolumeResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ListImages implements Client (#1406). Read-only; see the Client note on
// why the mutating image methods stay in-process.
func (c *UnixClient) ListImages(ctx context.Context, realm string) (ListImagesResult, error) {
	args := &ListImagesArgs{Realm: realm}
	reply := &ListImagesReply{}
	if err := c.call(ctx, MethodListImages, args, reply); err != nil {
		return ListImagesResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetImage implements Client (#1406).
func (c *UnixClient) GetImage(ctx context.Context, realm, ref string) (GetImageResult, error) {
	args := &GetImageArgs{Realm: realm, Ref: ref}
	reply := &GetImageReply{}
	if err := c.call(ctx, MethodGetImage, args, reply); err != nil {
		return GetImageResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetCgroupDetail implements Client (#1410).
func (c *UnixClient) GetCgroupDetail(ctx context.Context, scope CgroupScope) (GetCgroupDetailResult, error) {
	args := &GetCgroupDetailArgs{Scope: scope}
	reply := &GetCgroupDetailReply{}
	if err := c.call(ctx, MethodGetCgroupDetail, args, reply); err != nil {
		return GetCgroupDetailResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetNetworkDetail implements Client (#1411).
func (c *UnixClient) GetNetworkDetail(ctx context.Context, scope NetworkScope) (GetNetworkDetailResult, error) {
	args := &GetNetworkDetailArgs{Scope: scope}
	reply := &GetNetworkDetailReply{}
	if err := c.call(ctx, MethodGetNetworkDetail, args, reply); err != nil {
		return GetNetworkDetailResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetBlueprint implements Client.
func (c *UnixClient) GetBlueprint(
	ctx context.Context, doc v1beta1.CellBlueprintDoc,
) (GetBlueprintResult, error) {
	args := &GetBlueprintArgs{Doc: doc}
	reply := &GetBlueprintReply{}
	if err := c.call(ctx, MethodGetBlueprint, args, reply); err != nil {
		return GetBlueprintResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// GetConfig implements Client.
func (c *UnixClient) GetConfig(
	ctx context.Context, doc v1beta1.CellConfigDoc,
) (GetConfigResult, error) {
	args := &GetConfigArgs{Doc: doc}
	reply := &GetConfigReply{}
	if err := c.call(ctx, MethodGetConfig, args, reply); err != nil {
		return GetConfigResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// CreateConfig implements Client (#839).
func (c *UnixClient) CreateConfig(
	ctx context.Context, doc v1beta1.CellConfigDoc,
) (CreateConfigResult, error) {
	args := &CreateConfigArgs{Doc: doc}
	reply := &CreateConfigReply{}
	if err := c.call(ctx, MethodCreateConfig, args, reply); err != nil {
		return CreateConfigResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// CreateSecret implements Client (#815).
func (c *UnixClient) CreateSecret(
	ctx context.Context, doc v1beta1.SecretDoc,
) (CreateSecretResult, error) {
	args := &CreateSecretArgs{Doc: doc}
	reply := &CreateSecretReply{}
	if err := c.call(ctx, MethodCreateSecret, args, reply); err != nil {
		return CreateSecretResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// CreateVolume implements Client (#1236).
func (c *UnixClient) CreateVolume(
	ctx context.Context, doc v1beta1.VolumeDoc,
) (CreateVolumeResult, error) {
	args := &CreateVolumeArgs{Doc: doc}
	reply := &CreateVolumeReply{}
	if err := c.call(ctx, MethodCreateVolume, args, reply); err != nil {
		return CreateVolumeResult{}, err
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

// ListSecrets implements Client.
func (c *UnixClient) ListSecrets(
	ctx context.Context,
	realmName, spaceName, stackName, cellName string,
) ([]v1beta1.SecretDoc, error) {
	args := &ListSecretsArgs{RealmName: realmName, SpaceName: spaceName, StackName: stackName, CellName: cellName}
	reply := &ListSecretsReply{}
	if err := c.call(ctx, MethodListSecrets, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Secrets, FromAPIError(reply.Err)
	}
	return reply.Secrets, nil
}

// ListVolumes implements Client (#1236).
func (c *UnixClient) ListVolumes(
	ctx context.Context,
	realmName, spaceName, stackName string,
) ([]v1beta1.VolumeDoc, error) {
	args := &ListVolumesArgs{RealmName: realmName, SpaceName: spaceName, StackName: stackName}
	reply := &ListVolumesReply{}
	if err := c.call(ctx, MethodListVolumes, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Volumes, FromAPIError(reply.Err)
	}
	return reply.Volumes, nil
}

// ListBlueprints implements Client.
func (c *UnixClient) ListBlueprints(
	ctx context.Context,
	realmName, spaceName, stackName string,
) ([]v1beta1.CellBlueprintDoc, error) {
	args := &ListBlueprintsArgs{RealmName: realmName, SpaceName: spaceName, StackName: stackName}
	reply := &ListBlueprintsReply{}
	if err := c.call(ctx, MethodListBlueprints, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Blueprints, FromAPIError(reply.Err)
	}
	return reply.Blueprints, nil
}

// ListConfigs implements Client.
func (c *UnixClient) ListConfigs(
	ctx context.Context,
	realmName, spaceName, stackName string,
) ([]v1beta1.CellConfigDoc, error) {
	args := &ListConfigsArgs{RealmName: realmName, SpaceName: spaceName, StackName: stackName}
	reply := &ListConfigsReply{}
	if err := c.call(ctx, MethodListConfigs, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Configs, FromAPIError(reply.Err)
	}
	return reply.Configs, nil
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

// ConnectContainerTerminal implements Client. Each call opens its own
// connection to kukeond so the reply's SCM_RIGHTS descriptor can only belong
// to this call; the shared, multiplexed connection the other methods use is
// left alone. The returned connection is the daemon's connected sbsh control
// socket, owned by the caller.
func (c *UnixClient) ConnectContainerTerminal(
	ctx context.Context,
	doc v1beta1.ContainerDoc,
) (*net.UnixConn, error) {
	conn, err := c.dialDedicated(ctx)
	if err != nil {
		return nil, err
	}
	reply := &ConnectContainerTerminalReply{}
	return callReceivingConn(ctx, conn, MethodConnectContainerTerminal,
		&ConnectContainerTerminalArgs{Doc: doc}, reply,
		func() *APIError { return reply.Err })
}

// ExecContainer implements Client. Like ConnectContainerTerminal it opens
// its own connection for the call, so the passed descriptor can only belong
// to this reply. The returned connection is the caller's end of the exec
// session's socket pair, owned by the caller.
func (c *UnixClient) ExecContainer(
	ctx context.Context,
	doc v1beta1.ContainerDoc,
	req ExecRequest,
) (*net.UnixConn, error) {
	conn, err := c.dialDedicated(ctx)
	if err != nil {
		return nil, err
	}
	reply := &ExecContainerReply{}
	return callReceivingConn(ctx, conn, MethodExecContainer,
		&ExecContainerArgs{Doc: doc, Request: req}, reply,
		func() *APIError { return reply.Err })
}

// dialDedicated opens a connection to kukeond for a single call whose reply
// passes a descriptor back.
func (c *UnixClient) dialDedicated(ctx context.Context) (*net.UnixConn, error) {
	d := net.Dialer{Timeout: c.dialTimeout}
	raw, err := d.DialContext(ctx, "unix", c.sockPath)
	if err != nil {
		return nil, wrapDialError(c.sockPath, err)
	}
	conn, ok := raw.(*net.UnixConn)
	if !ok {
		_ = raw.Close()
		return nil, fmt.Errorf("dial kukeond at %s: got %T, want a unix socket", c.sockPath, raw)
	}
	return conn, nil
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

// ReadContainerLog implements Client (issue #1537).
func (c *UnixClient) ReadContainerLog(
	ctx context.Context,
	doc v1beta1.ContainerDoc,
	req ContainerLogRequest,
) (ContainerLogResult, error) {
	args := &ReadContainerLogArgs{Doc: doc, Request: req}
	reply := &ReadContainerLogReply{}
	if err := c.call(ctx, MethodReadContainerLog, args, reply); err != nil {
		return ContainerLogResult{}, err
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

// DeleteSecret implements Client.
func (c *UnixClient) DeleteSecret(ctx context.Context, doc v1beta1.SecretDoc) (DeleteSecretResult, error) {
	args := &DeleteSecretArgs{Doc: doc}
	reply := &DeleteSecretReply{}
	if err := c.call(ctx, MethodDeleteSecret, args, reply); err != nil {
		return DeleteSecretResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteVolume implements Client (#1236).
func (c *UnixClient) DeleteVolume(ctx context.Context, doc v1beta1.VolumeDoc) (DeleteVolumeResult, error) {
	args := &DeleteVolumeArgs{Doc: doc}
	reply := &DeleteVolumeReply{}
	if err := c.call(ctx, MethodDeleteVolume, args, reply); err != nil {
		return DeleteVolumeResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteBlueprint implements Client.
func (c *UnixClient) DeleteBlueprint(
	ctx context.Context, doc v1beta1.CellBlueprintDoc,
) (DeleteBlueprintResult, error) {
	args := &DeleteBlueprintArgs{Doc: doc}
	reply := &DeleteBlueprintReply{}
	if err := c.call(ctx, MethodDeleteBlueprint, args, reply); err != nil {
		return DeleteBlueprintResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteConfig implements Client.
func (c *UnixClient) DeleteConfig(
	ctx context.Context, doc v1beta1.CellConfigDoc,
) (DeleteConfigResult, error) {
	args := &DeleteConfigArgs{Doc: doc}
	reply := &DeleteConfigReply{}
	if err := c.call(ctx, MethodDeleteConfig, args, reply); err != nil {
		return DeleteConfigResult{}, err
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
	return c.applyDocuments(ctx, rawYAML, "")
}

// ApplyDocumentsForTeam implements Client.
func (c *UnixClient) ApplyDocumentsForTeam(
	ctx context.Context, rawYAML []byte, team string,
) (ApplyDocumentsResult, error) {
	if team == "" {
		return ApplyDocumentsResult{}, errors.New("apply for team: team is required")
	}
	return c.applyDocuments(ctx, rawYAML, team)
}

func (c *UnixClient) applyDocuments(
	ctx context.Context, rawYAML []byte, team string,
) (ApplyDocumentsResult, error) {
	args := &ApplyDocumentsArgs{RawYAML: rawYAML, Team: team}
	reply := &ApplyDocumentsReply{}
	if err := c.call(ctx, MethodApplyDocuments, args, reply); err != nil {
		return ApplyDocumentsResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteDocuments implements Client.
func (c *UnixClient) DeleteDocuments(
	ctx context.Context,
	rawYAML []byte,
	cascade, force bool,
) (DeleteDocumentsResult, error) {
	args := &DeleteDocumentsArgs{RawYAML: rawYAML, Cascade: cascade, Force: force}
	reply := &DeleteDocumentsReply{}
	if err := c.call(ctx, MethodDeleteDocuments, args, reply); err != nil {
		return DeleteDocumentsResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ReadDaemonLogs implements Client (issue #1386, step 7).
func (c *UnixClient) ReadDaemonLogs(ctx context.Context, req DaemonLogRequest) (DaemonLogResult, error) {
	args := &ReadDaemonLogsArgs{Request: req}
	reply := &ReadDaemonLogsReply{}
	if err := c.call(ctx, MethodReadDaemonLogs, args, reply); err != nil {
		return DaemonLogResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ReadHealthDigest implements Client (issue #1509).
func (c *UnixClient) ReadHealthDigest(ctx context.Context, req HealthDigestRequest) (HealthDigest, error) {
	args := &ReadHealthDigestArgs{Request: req}
	reply := &ReadHealthDigestReply{}
	if err := c.call(ctx, MethodReadHealthDigest, args, reply); err != nil {
		return HealthDigest{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// SubmitHealthReport implements Client (issue #1509).
func (c *UnixClient) SubmitHealthReport(ctx context.Context, report HealthReport) (HealthReport, error) {
	args := &SubmitHealthReportArgs{Report: report}
	reply := &SubmitHealthReportReply{}
	if err := c.call(ctx, MethodSubmitHealthReport, args, reply); err != nil {
		return HealthReport{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ListHealthReports implements Client (issue #1509).
func (c *UnixClient) ListHealthReports(ctx context.Context) ([]HealthReportSummary, error) {
	args := &ListHealthReportsArgs{}
	reply := &ListHealthReportsReply{}
	if err := c.call(ctx, MethodListHealthReports, args, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ReadHealthReport implements Client (issue #1509).
func (c *UnixClient) ReadHealthReport(ctx context.Context, id string) (HealthReport, error) {
	args := &ReadHealthReportArgs{ID: id}
	reply := &ReadHealthReportReply{}
	if err := c.call(ctx, MethodReadHealthReport, args, reply); err != nil {
		return HealthReport{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// DeleteHealthReport implements Client (issue #1619).
func (c *UnixClient) DeleteHealthReport(ctx context.Context, name string) error {
	args := &DeleteHealthReportArgs{Name: name}
	reply := &DeleteHealthReportReply{}
	if err := c.call(ctx, MethodDeleteHealthReport, args, reply); err != nil {
		return err
	}
	if reply.Err != nil {
		return FromAPIError(reply.Err)
	}
	return nil
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

// PingVersion returns the daemon's reported build version. The wire reply
// has carried Version since the RPC's introduction; the helper exposes it
// to callers (notably `kuke status`) without forcing them to hand-roll the
// Ping wire.
func (c *UnixClient) PingVersion(ctx context.Context) (string, error) {
	args := &PingArgs{}
	reply := &PingReply{}
	if err := c.call(ctx, MethodPing, args, reply); err != nil {
		return "", err
	}
	if reply.Err != nil {
		return "", FromAPIError(reply.Err)
	}
	return reply.Version, nil
}

// ---- Teams (issue #1618) ----

func (c *UnixClient) GetTeam(ctx context.Context, ref TeamRef) (GetTeamResult, error) {
	reply := &GetTeamReply{}
	if err := c.call(ctx, MethodGetTeam, &GetTeamArgs{Ref: ref}, reply); err != nil {
		return GetTeamResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

func (c *UnixClient) ListTeams(ctx context.Context, realm string) ([]GetTeamResult, error) {
	reply := &ListTeamsReply{}
	if err := c.call(ctx, MethodListTeams, &ListTeamsArgs{Realm: realm}, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return nil, FromAPIError(reply.Err)
	}
	return reply.Teams, nil
}

func (c *UnixClient) DeleteTeam(ctx context.Context, ref TeamRef) (DeleteTeamResult, error) {
	reply := &DeleteTeamReply{}
	if err := c.call(ctx, MethodDeleteTeam, &DeleteTeamArgs{Ref: ref}, reply); err != nil {
		return DeleteTeamResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

// ---- Sources (issue #1643) ----

func (c *UnixClient) GetSource(ctx context.Context, ref SourceRef) (GetSourceResult, error) {
	reply := &GetSourceReply{}
	if err := c.call(ctx, MethodGetSource, &GetSourceArgs{Ref: ref}, reply); err != nil {
		return GetSourceResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

func (c *UnixClient) ListSources(ctx context.Context, realm string) ([]GetSourceResult, error) {
	reply := &ListSourcesReply{}
	if err := c.call(ctx, MethodListSources, &ListSourcesArgs{Realm: realm}, reply); err != nil {
		return nil, err
	}
	if reply.Err != nil {
		return nil, FromAPIError(reply.Err)
	}
	return reply.Sources, nil
}

func (c *UnixClient) DeleteSource(ctx context.Context, ref SourceRef) (DeleteSourceResult, error) {
	reply := &DeleteSourceReply{}
	if err := c.call(ctx, MethodDeleteSource, &DeleteSourceArgs{Ref: ref}, reply); err != nil {
		return DeleteSourceResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

func (c *UnixClient) StageGitFetch(ctx context.Context, req GitFetchRequest) (StageGitFetchResult, error) {
	reply := &StageGitFetchReply{}
	if err := c.call(ctx, MethodStageGitFetch, &StageGitFetchArgs{Request: req}, reply); err != nil {
		return StageGitFetchResult{}, err
	}
	if reply.Err != nil {
		return StageGitFetchResult{}, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}

func (c *UnixClient) ReleaseGitFetch(ctx context.Context, id string) error {
	reply := &ReleaseGitFetchReply{}
	if err := c.call(ctx, MethodReleaseGitFetch, &ReleaseGitFetchArgs{ID: id}, reply); err != nil {
		return err
	}
	if reply.Err != nil {
		return FromAPIError(reply.Err)
	}
	return nil
}

func (c *UnixClient) GetTeamDefaults(ctx context.Context, realm string) (GetTeamDefaultsResult, error) {
	reply := &GetTeamDefaultsReply{}
	if err := c.call(ctx, MethodGetTeamDefaults, &GetTeamDefaultsArgs{Realm: realm}, reply); err != nil {
		return GetTeamDefaultsResult{}, err
	}
	if reply.Err != nil {
		return reply.Result, FromAPIError(reply.Err)
	}
	return reply.Result, nil
}
