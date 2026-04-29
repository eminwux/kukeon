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

// Package local provides an in-process kukeonv1.Client backed by a
// controller.Exec. Used by the CLI in --no-daemon mode and by the daemon
// itself when servicing RPC calls.
package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Client is an in-process kukeonv1.Client.
type Client struct {
	ctrl *controller.Exec
}

// New returns a Client that owns its controller.Exec. Close releases it.
func New(ctx context.Context, logger *slog.Logger, opts controller.Options) *Client {
	return &Client{ctrl: controller.NewControllerExec(ctx, logger, opts)}
}

// NewFromExec wraps an already-constructed controller.Exec. The caller
// retains ownership of ctrl; Close is a no-op in this mode.
func NewFromExec(ctrl *controller.Exec) *Client {
	return &Client{ctrl: ctrl}
}

// Close releases the underlying controller if this Client owns it.
func (c *Client) Close() error {
	if c.ctrl == nil {
		return nil
	}
	return c.ctrl.Close()
}

// CreateRealm normalizes the external doc, delegates to the controller, and
// reshapes the result back into external v1beta1 types.
func (c *Client) CreateRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error) {
	internal, version, err := apischeme.NormalizeRealm(doc)
	if err != nil {
		return kukeonv1.CreateRealmResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.CreateRealm(internal)
	if err != nil {
		return kukeonv1.CreateRealmResult{}, err
	}
	extRealm, err := apischeme.BuildRealmExternalFromInternal(res.Realm, version)
	if err != nil {
		return kukeonv1.CreateRealmResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.CreateRealmResult{
		Realm:                         extRealm,
		MetadataExistsPre:             res.MetadataExistsPre,
		MetadataExistsPost:            res.MetadataExistsPost,
		CgroupExistsPre:               res.CgroupExistsPre,
		CgroupExistsPost:              res.CgroupExistsPost,
		CgroupCreated:                 res.CgroupCreated,
		ContainerdNamespaceExistsPre:  res.ContainerdNamespaceExistsPre,
		ContainerdNamespaceExistsPost: res.ContainerdNamespaceExistsPost,
		ContainerdNamespaceCreated:    res.ContainerdNamespaceCreated,
		Created:                       res.Created,
	}, nil
}

// CreateSpace normalizes the external doc, delegates to the controller, and
// reshapes the result back into external v1beta1 types.
func (c *Client) CreateSpace(_ context.Context, doc v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error) {
	internal, version, err := apischeme.NormalizeSpace(doc)
	if err != nil {
		return kukeonv1.CreateSpaceResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.CreateSpace(internal)
	if err != nil {
		return kukeonv1.CreateSpaceResult{}, err
	}
	extSpace, err := apischeme.BuildSpaceExternalFromInternal(res.Space, version)
	if err != nil {
		return kukeonv1.CreateSpaceResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.CreateSpaceResult{
		Space:                extSpace,
		MetadataExistsPre:    res.MetadataExistsPre,
		MetadataExistsPost:   res.MetadataExistsPost,
		CgroupExistsPre:      res.CgroupExistsPre,
		CgroupExistsPost:     res.CgroupExistsPost,
		CgroupCreated:        res.CgroupCreated,
		CNINetworkExistsPre:  res.CNINetworkExistsPre,
		CNINetworkExistsPost: res.CNINetworkExistsPost,
		CNINetworkCreated:    res.CNINetworkCreated,
		Created:              res.Created,
	}, nil
}

// CreateStack normalizes the external doc, delegates to the controller, and
// reshapes the result back into external v1beta1 types.
func (c *Client) CreateStack(_ context.Context, doc v1beta1.StackDoc) (kukeonv1.CreateStackResult, error) {
	internal, version, err := apischeme.NormalizeStack(doc)
	if err != nil {
		return kukeonv1.CreateStackResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.CreateStack(internal)
	if err != nil {
		return kukeonv1.CreateStackResult{}, err
	}
	extStack, err := apischeme.BuildStackExternalFromInternal(res.Stack, version)
	if err != nil {
		return kukeonv1.CreateStackResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.CreateStackResult{
		Stack:              extStack,
		MetadataExistsPre:  res.MetadataExistsPre,
		MetadataExistsPost: res.MetadataExistsPost,
		CgroupExistsPre:    res.CgroupExistsPre,
		CgroupExistsPost:   res.CgroupExistsPost,
		CgroupCreated:      res.CgroupCreated,
		Created:            res.Created,
	}, nil
}

// CreateCell normalizes the external doc, delegates to the controller, and
// reshapes the result back into external v1beta1 types.
func (c *Client) CreateCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	internal, version, err := apischeme.NormalizeCell(doc)
	if err != nil {
		return kukeonv1.CreateCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	res, err := c.ctrl.CreateCell(internal)
	if err != nil {
		return kukeonv1.CreateCellResult{}, err
	}

	extCell, err := apischeme.BuildCellExternalFromInternal(res.Cell, version)
	if err != nil {
		return kukeonv1.CreateCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	out := kukeonv1.CreateCellResult{
		Cell:                    extCell,
		MetadataExistsPre:       res.MetadataExistsPre,
		MetadataExistsPost:      res.MetadataExistsPost,
		CgroupExistsPre:         res.CgroupExistsPre,
		CgroupExistsPost:        res.CgroupExistsPost,
		CgroupCreated:           res.CgroupCreated,
		RootContainerExistsPre:  res.RootContainerExistsPre,
		RootContainerExistsPost: res.RootContainerExistsPost,
		RootContainerCreated:    res.RootContainerCreated,
		StartedPre:              res.StartedPre,
		StartedPost:             res.StartedPost,
		Started:                 res.Started,
		Created:                 res.Created,
	}
	if len(res.Containers) > 0 {
		out.Containers = make([]kukeonv1.ContainerCreationOutcome, len(res.Containers))
		for i, container := range res.Containers {
			out.Containers[i] = kukeonv1.ContainerCreationOutcome{
				Name:       container.Name,
				ExistsPre:  container.ExistsPre,
				ExistsPost: container.ExistsPost,
				Created:    container.Created,
			}
		}
	}
	return out, nil
}

// CreateContainer normalizes the external doc, delegates to the controller,
// and reshapes the result back into external v1beta1 types.
func (c *Client) CreateContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.CreateContainerResult, error) {
	internal, version, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return kukeonv1.CreateContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.CreateContainer(internal)
	if err != nil {
		return kukeonv1.CreateContainerResult{}, err
	}
	extContainer, err := apischeme.BuildContainerExternalFromInternal(res.Container, version)
	if err != nil {
		return kukeonv1.CreateContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.CreateContainerResult{
		Container:              extContainer,
		CellMetadataExistsPre:  res.CellMetadataExistsPre,
		CellMetadataExistsPost: res.CellMetadataExistsPost,
		ContainerExistsPre:     res.ContainerExistsPre,
		ContainerExistsPost:    res.ContainerExistsPost,
		ContainerCreated:       res.ContainerCreated,
		Started:                res.Started,
	}, nil
}

// ---- Get ----

func (c *Client) GetRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	internal, _, err := apischeme.NormalizeRealm(doc)
	if err != nil {
		return kukeonv1.GetRealmResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.GetRealm(internal)
	if err != nil {
		return kukeonv1.GetRealmResult{}, err
	}
	extRealm, err := fs.ConvertRealmToExternal(res.Realm)
	if err != nil {
		return kukeonv1.GetRealmResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.GetRealmResult{
		Realm:                     *extRealm,
		MetadataExists:            res.MetadataExists,
		CgroupExists:              res.CgroupExists,
		ContainerdNamespaceExists: res.ContainerdNamespaceExists,
	}, nil
}

func (c *Client) GetSpace(_ context.Context, doc v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
	internal, _, err := apischeme.NormalizeSpace(doc)
	if err != nil {
		return kukeonv1.GetSpaceResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.GetSpace(internal)
	if err != nil {
		return kukeonv1.GetSpaceResult{}, err
	}
	extSpace, err := fs.ConvertSpaceToExternal(res.Space)
	if err != nil {
		return kukeonv1.GetSpaceResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.GetSpaceResult{
		Space:            *extSpace,
		MetadataExists:   res.MetadataExists,
		CgroupExists:     res.CgroupExists,
		CNINetworkExists: res.CNINetworkExists,
	}, nil
}

func (c *Client) GetStack(_ context.Context, doc v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
	internal, _, err := apischeme.NormalizeStack(doc)
	if err != nil {
		return kukeonv1.GetStackResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.GetStack(internal)
	if err != nil {
		return kukeonv1.GetStackResult{}, err
	}
	extStack, err := fs.ConvertStackToExternal(res.Stack)
	if err != nil {
		return kukeonv1.GetStackResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.GetStackResult{
		Stack:          *extStack,
		MetadataExists: res.MetadataExists,
		CgroupExists:   res.CgroupExists,
	}, nil
}

func (c *Client) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	internal, _, err := apischeme.NormalizeCell(doc)
	if err != nil {
		return kukeonv1.GetCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.GetCell(internal)
	if err != nil {
		return kukeonv1.GetCellResult{}, err
	}
	extCell, err := fs.ConvertCellToExternal(res.Cell)
	if err != nil {
		return kukeonv1.GetCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.GetCellResult{
		Cell:                *extCell,
		MetadataExists:      res.MetadataExists,
		CgroupExists:        res.CgroupExists,
		RootContainerExists: res.RootContainerExists,
	}, nil
}

func (c *Client) GetContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
	internal, _, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return kukeonv1.GetContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.GetContainer(internal)
	if err != nil {
		return kukeonv1.GetContainerResult{}, err
	}
	extContainer, err := fs.ConvertContainerToExternal(res.Container)
	if err != nil {
		return kukeonv1.GetContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.GetContainerResult{
		Container:          *extContainer,
		CellMetadataExists: res.CellMetadataExists,
		ContainerExists:    res.ContainerExists,
	}, nil
}

// ---- List ----

func (c *Client) ListRealms(_ context.Context) ([]v1beta1.RealmDoc, error) {
	realms, err := c.ctrl.ListRealms()
	if err != nil {
		return nil, err
	}
	ext, err := fs.ConvertRealmListToExternal(realms)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return derefDocs(ext), nil
}

func (c *Client) ListSpaces(_ context.Context, realmName string) ([]v1beta1.SpaceDoc, error) {
	spaces, err := c.ctrl.ListSpaces(realmName)
	if err != nil {
		return nil, err
	}
	ext, err := fs.ConvertSpaceListToExternal(spaces)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return derefDocs(ext), nil
}

func (c *Client) ListStacks(_ context.Context, realmName, spaceName string) ([]v1beta1.StackDoc, error) {
	stacks, err := c.ctrl.ListStacks(realmName, spaceName)
	if err != nil {
		return nil, err
	}
	ext, err := fs.ConvertStackListToExternal(stacks)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return derefDocs(ext), nil
}

func (c *Client) ListCells(_ context.Context, realmName, spaceName, stackName string) ([]v1beta1.CellDoc, error) {
	cells, err := c.ctrl.ListCells(realmName, spaceName, stackName)
	if err != nil {
		return nil, err
	}
	ext, err := fs.ConvertCellListToExternal(cells)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return derefDocs(ext), nil
}

func (c *Client) ListContainers(
	_ context.Context,
	realmName, spaceName, stackName, cellName string,
) ([]v1beta1.ContainerSpec, error) {
	specs, err := c.ctrl.ListContainers(realmName, spaceName, stackName, cellName)
	if err != nil {
		return nil, err
	}
	ext, err := fs.ConvertContainerSpecListToExternal(specs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return derefDocs(ext), nil
}

// derefDocs converts a []*T returned by fs.Convert* helpers into the
// wire-friendly []T form (pointer slices don't round-trip cleanly through
// net/rpc+jsonrpc — jsonrpc would encode them correctly but gob couldn't,
// and value slices keep the interface signature simpler).
func derefDocs[T any](in []*T) []T {
	out := make([]T, len(in))
	for i, p := range in {
		if p != nil {
			out[i] = *p
		}
	}
	return out
}

// ---- Lifecycle ----

func (c *Client) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	internal, version, err := apischeme.NormalizeCell(doc)
	if err != nil {
		return kukeonv1.StartCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.StartCell(internal)
	if err != nil {
		return kukeonv1.StartCellResult{}, err
	}
	ext, err := apischeme.BuildCellExternalFromInternal(res.Cell, version)
	if err != nil {
		return kukeonv1.StartCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.StartCellResult{Cell: ext, Started: res.Started}, nil
}

func (c *Client) StartContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.StartContainerResult, error) {
	internal, version, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return kukeonv1.StartContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.StartContainer(internal)
	if err != nil {
		return kukeonv1.StartContainerResult{}, err
	}
	ext, err := apischeme.BuildContainerExternalFromInternal(res.Container, version)
	if err != nil {
		return kukeonv1.StartContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.StartContainerResult{Container: ext, Started: res.Started}, nil
}

func (c *Client) StopCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
	internal, version, err := apischeme.NormalizeCell(doc)
	if err != nil {
		return kukeonv1.StopCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.StopCell(internal)
	if err != nil {
		return kukeonv1.StopCellResult{}, err
	}
	ext, err := apischeme.BuildCellExternalFromInternal(res.Cell, version)
	if err != nil {
		return kukeonv1.StopCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.StopCellResult{Cell: ext, Stopped: res.Stopped}, nil
}

func (c *Client) StopContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.StopContainerResult, error) {
	internal, version, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return kukeonv1.StopContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.StopContainer(internal)
	if err != nil {
		return kukeonv1.StopContainerResult{}, err
	}
	ext, err := apischeme.BuildContainerExternalFromInternal(res.Container, version)
	if err != nil {
		return kukeonv1.StopContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.StopContainerResult{Container: ext, Stopped: res.Stopped}, nil
}

func (c *Client) KillCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
	internal, version, err := apischeme.NormalizeCell(doc)
	if err != nil {
		return kukeonv1.KillCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.KillCell(internal)
	if err != nil {
		return kukeonv1.KillCellResult{}, err
	}
	ext, err := apischeme.BuildCellExternalFromInternal(res.Cell, version)
	if err != nil {
		return kukeonv1.KillCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.KillCellResult{Cell: ext, Killed: res.Killed}, nil
}

func (c *Client) KillContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.KillContainerResult, error) {
	internal, version, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return kukeonv1.KillContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.KillContainer(internal)
	if err != nil {
		return kukeonv1.KillContainerResult{}, err
	}
	ext, err := apischeme.BuildContainerExternalFromInternal(res.Container, version)
	if err != nil {
		return kukeonv1.KillContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.KillContainerResult{Container: ext, Killed: res.Killed}, nil
}

// ---- Delete ----

func (c *Client) DeleteRealm(
	_ context.Context,
	doc v1beta1.RealmDoc,
	force, cascade bool,
) (kukeonv1.DeleteRealmResult, error) {
	internal, version, err := apischeme.NormalizeRealm(doc)
	if err != nil {
		return kukeonv1.DeleteRealmResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.DeleteRealm(internal, force, cascade)
	if err != nil {
		return kukeonv1.DeleteRealmResult{}, err
	}
	ext, err := apischeme.BuildRealmExternalFromInternal(res.Realm, version)
	if err != nil {
		return kukeonv1.DeleteRealmResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.DeleteRealmResult{
		Realm:                      ext,
		Deleted:                    res.Deleted,
		MetadataDeleted:            res.MetadataDeleted,
		CgroupDeleted:              res.CgroupDeleted,
		ContainerdNamespaceDeleted: res.ContainerdNamespaceDeleted,
	}, nil
}

func (c *Client) DeleteSpace(
	_ context.Context,
	doc v1beta1.SpaceDoc,
	force, cascade bool,
) (kukeonv1.DeleteSpaceResult, error) {
	internal, version, err := apischeme.NormalizeSpace(doc)
	if err != nil {
		return kukeonv1.DeleteSpaceResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.DeleteSpace(internal, force, cascade)
	if err != nil {
		return kukeonv1.DeleteSpaceResult{}, err
	}
	ext, err := apischeme.BuildSpaceExternalFromInternal(res.Space, version)
	if err != nil {
		return kukeonv1.DeleteSpaceResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.DeleteSpaceResult{
		Space:             ext,
		SpaceName:         res.SpaceName,
		RealmName:         res.RealmName,
		MetadataDeleted:   res.MetadataDeleted,
		CgroupDeleted:     res.CgroupDeleted,
		CNINetworkDeleted: res.CNINetworkDeleted,
		Deleted:           res.Deleted,
	}, nil
}

func (c *Client) DeleteStack(
	_ context.Context,
	doc v1beta1.StackDoc,
	force, cascade bool,
) (kukeonv1.DeleteStackResult, error) {
	internal, version, err := apischeme.NormalizeStack(doc)
	if err != nil {
		return kukeonv1.DeleteStackResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.DeleteStack(internal, force, cascade)
	if err != nil {
		return kukeonv1.DeleteStackResult{}, err
	}
	ext, err := apischeme.BuildStackExternalFromInternal(res.Stack, version)
	if err != nil {
		return kukeonv1.DeleteStackResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.DeleteStackResult{
		Stack:           ext,
		StackName:       res.StackName,
		RealmName:       res.RealmName,
		SpaceName:       res.SpaceName,
		MetadataDeleted: res.MetadataDeleted,
		CgroupDeleted:   res.CgroupDeleted,
		Deleted:         res.Deleted,
	}, nil
}

func (c *Client) DeleteCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
	internal, version, err := apischeme.NormalizeCell(doc)
	if err != nil {
		return kukeonv1.DeleteCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.DeleteCell(internal)
	if err != nil {
		return kukeonv1.DeleteCellResult{}, err
	}
	ext, err := apischeme.BuildCellExternalFromInternal(res.Cell, version)
	if err != nil {
		return kukeonv1.DeleteCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.DeleteCellResult{
		Cell:              ext,
		ContainersDeleted: res.ContainersDeleted,
		CgroupDeleted:     res.CgroupDeleted,
		MetadataDeleted:   res.MetadataDeleted,
	}, nil
}

func (c *Client) DeleteContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.DeleteContainerResult, error) {
	internal, version, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return kukeonv1.DeleteContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.DeleteContainer(internal)
	if err != nil {
		return kukeonv1.DeleteContainerResult{}, err
	}
	ext, err := apischeme.BuildContainerExternalFromInternal(res.Container, version)
	if err != nil {
		return kukeonv1.DeleteContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.DeleteContainerResult{
		Container:          ext,
		CellMetadataExists: res.CellMetadataExists,
		ContainerExists:    res.ContainerExists,
		Deleted:            res.Deleted,
	}, nil
}

// ---- Purge ----

func (c *Client) PurgeRealm(
	_ context.Context,
	doc v1beta1.RealmDoc,
	force, cascade bool,
) (kukeonv1.PurgeRealmResult, error) {
	internal, version, err := apischeme.NormalizeRealm(doc)
	if err != nil {
		return kukeonv1.PurgeRealmResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.PurgeRealm(internal, force, cascade)
	if err != nil {
		return kukeonv1.PurgeRealmResult{}, err
	}
	ext, err := apischeme.BuildRealmExternalFromInternal(res.Realm, version)
	if err != nil {
		return kukeonv1.PurgeRealmResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.PurgeRealmResult{
		Realm:          ext,
		RealmDeleted:   res.RealmDeleted,
		PurgeSucceeded: res.PurgeSucceeded,
		Force:          res.Force,
		Cascade:        res.Cascade,
		Deleted:        res.Deleted,
		Purged:         res.Purged,
	}, nil
}

func (c *Client) PurgeSpace(
	_ context.Context,
	doc v1beta1.SpaceDoc,
	force, cascade bool,
) (kukeonv1.PurgeSpaceResult, error) {
	internal, version, err := apischeme.NormalizeSpace(doc)
	if err != nil {
		return kukeonv1.PurgeSpaceResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.PurgeSpace(internal, force, cascade)
	if err != nil {
		return kukeonv1.PurgeSpaceResult{}, err
	}
	ext, err := apischeme.BuildSpaceExternalFromInternal(res.Space, version)
	if err != nil {
		return kukeonv1.PurgeSpaceResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.PurgeSpaceResult{
		Space:             ext,
		MetadataDeleted:   res.MetadataDeleted,
		CgroupDeleted:     res.CgroupDeleted,
		CNINetworkDeleted: res.CNINetworkDeleted,
		PurgeSucceeded:    res.PurgeSucceeded,
		Force:             res.Force,
		Cascade:           res.Cascade,
		Deleted:           res.Deleted,
		Purged:            res.Purged,
	}, nil
}

func (c *Client) PurgeStack(
	_ context.Context,
	doc v1beta1.StackDoc,
	force, cascade bool,
) (kukeonv1.PurgeStackResult, error) {
	internal, version, err := apischeme.NormalizeStack(doc)
	if err != nil {
		return kukeonv1.PurgeStackResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.PurgeStack(internal, force, cascade)
	if err != nil {
		return kukeonv1.PurgeStackResult{}, err
	}
	ext, err := apischeme.BuildStackExternalFromInternal(res.Stack, version)
	if err != nil {
		return kukeonv1.PurgeStackResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.PurgeStackResult{
		Stack:   ext,
		Deleted: res.Deleted,
		Purged:  res.Purged,
	}, nil
}

func (c *Client) PurgeCell(
	_ context.Context,
	doc v1beta1.CellDoc,
	force, cascade bool,
) (kukeonv1.PurgeCellResult, error) {
	internal, version, err := apischeme.NormalizeCell(doc)
	if err != nil {
		return kukeonv1.PurgeCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.PurgeCell(internal, force, cascade)
	if err != nil {
		return kukeonv1.PurgeCellResult{}, err
	}
	ext, err := apischeme.BuildCellExternalFromInternal(res.Cell, version)
	if err != nil {
		return kukeonv1.PurgeCellResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.PurgeCellResult{
		Cell:              ext,
		ContainersDeleted: res.ContainersDeleted,
		CgroupDeleted:     res.CgroupDeleted,
		MetadataDeleted:   res.MetadataDeleted,
		PurgeSucceeded:    res.PurgeSucceeded,
		Force:             res.Force,
		Cascade:           res.Cascade,
		Deleted:           res.Deleted,
		Purged:            res.Purged,
	}, nil
}

func (c *Client) PurgeContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.PurgeContainerResult, error) {
	internal, version, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return kukeonv1.PurgeContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.PurgeContainer(internal)
	if err != nil {
		return kukeonv1.PurgeContainerResult{}, err
	}
	ext, err := apischeme.BuildContainerExternalFromInternal(res.Container, version)
	if err != nil {
		return kukeonv1.PurgeContainerResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	return kukeonv1.PurgeContainerResult{
		Container:          ext,
		CellMetadataExists: res.CellMetadataExists,
		ContainerExists:    res.ContainerExists,
		Deleted:            res.Deleted,
		Purged:             res.Purged,
	}, nil
}

// ---- Refresh ----

func (c *Client) RefreshAll(_ context.Context) (kukeonv1.RefreshAllResult, error) {
	res, err := c.ctrl.RefreshAll()
	if err != nil {
		return kukeonv1.RefreshAllResult{}, err
	}
	return kukeonv1.RefreshAllResult{
		RealmsFound:       res.RealmsFound,
		SpacesFound:       res.SpacesFound,
		StacksFound:       res.StacksFound,
		CellsFound:        res.CellsFound,
		ContainersFound:   res.ContainersFound,
		RealmsUpdated:     res.RealmsUpdated,
		SpacesUpdated:     res.SpacesUpdated,
		StacksUpdated:     res.StacksUpdated,
		CellsUpdated:      res.CellsUpdated,
		ContainersUpdated: res.ContainersUpdated,
		Errors:            res.Errors,
	}, nil
}

// ---- Apply ----

func (c *Client) ApplyDocuments(_ context.Context, rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error) {
	docs, validationErrors, err := parseAndValidate(rawYAML)
	if err != nil {
		return kukeonv1.ApplyDocumentsResult{}, err
	}
	if len(validationErrors) > 0 {
		return kukeonv1.ApplyDocumentsResult{}, formatValidationErrors(validationErrors)
	}
	if len(docs) == 0 {
		return kukeonv1.ApplyDocumentsResult{}, errors.New("no valid documents found in input")
	}

	res, err := c.ctrl.ApplyDocuments(docs)
	if err != nil {
		return kukeonv1.ApplyDocumentsResult{}, err
	}

	out := kukeonv1.ApplyDocumentsResult{
		Resources: make([]kukeonv1.ApplyResourceResult, 0, len(res.Resources)),
	}
	for _, r := range res.Resources {
		item := kukeonv1.ApplyResourceResult{
			Index:   r.Index,
			Kind:    r.Kind,
			Name:    r.Name,
			Action:  r.Action,
			Changes: r.Changes,
			Details: r.Details,
		}
		if r.Error != nil {
			item.Error = r.Error.Error()
		}
		out.Resources = append(out.Resources, item)
	}
	return out, nil
}

// parseAndValidate mirrors cmd/kuke/shared.ParseAndValidateDocuments, but
// takes a byte slice so it works both server-side (from the wire) and in
// the --no-daemon CLI path.
func parseAndValidate(rawYAML []byte) ([]parser.Document, []*parser.ValidationError, error) {
	rawDocs, err := parser.ParseDocuments(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	docs := make([]parser.Document, 0, len(rawDocs))
	var validationErrors []*parser.ValidationError

	for i, rawDoc := range rawDocs {
		doc, parseErr := parser.ParseDocument(i, rawDoc)
		if parseErr != nil {
			validationErrors = append(validationErrors, &parser.ValidationError{Index: i, Err: parseErr})
			continue
		}
		validationErr := parser.ValidateDocument(doc)
		if validationErr != nil {
			validationErrors = append(validationErrors, validationErr)
			continue
		}
		docs = append(docs, *doc)
	}

	return docs, validationErrors, nil
}

func formatValidationErrors(validationErrors []*parser.ValidationError) error {
	msgs := make([]string, 0, len(validationErrors))
	for _, ve := range validationErrors {
		msgs = append(msgs, ve.Error())
	}
	return fmt.Errorf("validation failed:\n  %s", strings.Join(msgs, "\n  "))
}

// ---- Image ----

// LoadImage imports an OCI/docker image tarball into the realm's containerd
// namespace. The byte slice is wrapped in a bytes.Reader so the controller
// can stream it through to containerd's Import API.
func (c *Client) LoadImage(_ context.Context, realm string, tarball []byte) (kukeonv1.LoadImageResult, error) {
	if len(tarball) == 0 {
		return kukeonv1.LoadImageResult{}, errdefs.ErrTarballRequired
	}
	res, err := c.ctrl.LoadImage(realm, bytes.NewReader(tarball))
	if err != nil {
		return kukeonv1.LoadImageResult{}, err
	}
	return kukeonv1.LoadImageResult{
		Realm:     res.Realm,
		Namespace: res.Namespace,
		Images:    res.Images,
	}, nil
}

// ListImages enumerates images in the realm's containerd namespace. The
// realm is validated by the controller layer; this wrapper only re-encodes
// the controller's ImageInfo onto the wire type so callers never import
// the controller package.
func (c *Client) ListImages(_ context.Context, realm string) (kukeonv1.ListImagesResult, error) {
	res, err := c.ctrl.ListImages(realm)
	if err != nil {
		return kukeonv1.ListImagesResult{}, err
	}
	out := kukeonv1.ListImagesResult{
		Realm:     res.Realm,
		Namespace: res.Namespace,
		Images:    make([]kukeonv1.ImageInfo, 0, len(res.Images)),
	}
	for _, img := range res.Images {
		out.Images = append(out.Images, controllerImageToWire(img))
	}
	return out, nil
}

// GetImage returns metadata for the named image ref in the realm.
// errdefs.ErrImageNotFound is propagated unchanged so the wire layer can
// emit the matching APIError Kind.
func (c *Client) GetImage(_ context.Context, realm, ref string) (kukeonv1.GetImageResult, error) {
	res, err := c.ctrl.GetImage(realm, ref)
	if err != nil {
		return kukeonv1.GetImageResult{}, err
	}
	return kukeonv1.GetImageResult{
		Realm:     res.Realm,
		Namespace: res.Namespace,
		Image:     controllerImageToWire(res.Image),
	}, nil
}

// DeleteImage removes the named image ref from the realm's containerd
// namespace. errdefs.ErrImageNotFound is propagated unchanged so the wire
// layer can emit the matching APIError Kind.
func (c *Client) DeleteImage(_ context.Context, realm, ref string) (kukeonv1.DeleteImageResult, error) {
	res, err := c.ctrl.DeleteImage(realm, ref)
	if err != nil {
		return kukeonv1.DeleteImageResult{}, err
	}
	return kukeonv1.DeleteImageResult{
		Realm:     res.Realm,
		Namespace: res.Namespace,
		Ref:       res.Ref,
	}, nil
}

func controllerImageToWire(img controller.ImageInfo) kukeonv1.ImageInfo {
	return kukeonv1.ImageInfo{
		Name:      img.Name,
		Size:      img.Size,
		CreatedAt: img.CreatedAt,
		Digest:    img.Digest,
		MediaType: img.MediaType,
		Labels:    img.Labels,
	}
}

// ---- Attach ----

// AttachContainer enforces the Attachable gate and resolves the host-side
// sbsh control-socket path. Bytes never traverse this RPC — the caller
// (`kuke attach`) opens HostSocketPath directly and runs the sbsh client
// loop against it.
func (c *Client) AttachContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
	spec, err := c.resolveAttachable(doc)
	if err != nil {
		return kukeonv1.AttachContainerResult{}, err
	}
	return kukeonv1.AttachContainerResult{
		HostSocketPath: fs.ContainerSocketPath(
			c.ctrl.RunPath(),
			spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
		),
	}, nil
}

// ---- Log ----

// LogContainer enforces the Attachable gate and resolves the host-side path
// of the per-container sbsh capture file. The Attachable gate is the same
// invariant AttachContainer uses: only sbsh-wrapped containers have a
// capture file. Bytes never traverse this RPC — the caller (`kuke log`)
// opens HostCapturePath directly.
func (c *Client) LogContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
	spec, err := c.resolveAttachable(doc)
	if err != nil {
		return kukeonv1.LogContainerResult{}, err
	}
	return kukeonv1.LogContainerResult{
		HostCapturePath: fs.ContainerCapturePath(
			c.ctrl.RunPath(),
			spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
		),
	}, nil
}

// resolveAttachable normalizes doc, looks up the container, and returns its
// internal spec only when Attachable=true. It surfaces ErrConversionFailed
// for malformed docs, ErrContainerNotFound when the container does not
// exist, and ErrAttachNotSupported for non-Attachable targets — the
// invariants both AttachContainer and LogContainer require before handing a
// host path back to the caller.
func (c *Client) resolveAttachable(doc v1beta1.ContainerDoc) (intmodel.ContainerSpec, error) {
	internal, _, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return intmodel.ContainerSpec{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.GetContainer(internal)
	if err != nil {
		return intmodel.ContainerSpec{}, err
	}
	if !res.ContainerExists {
		return intmodel.ContainerSpec{}, errdefs.ErrContainerNotFound
	}
	if !res.Container.Spec.Attachable {
		return intmodel.ContainerSpec{}, errdefs.ErrAttachNotSupported
	}
	return res.Container.Spec, nil
}

// ---- Ping ----

// Ping is a no-op in the in-process client: the controller is alive if this
// method can run. Implemented so *local.Client satisfies kukeonv1.Client.
func (c *Client) Ping(_ context.Context) error {
	return nil
}

// Verify interface compliance at compile time.
var _ kukeonv1.Client = (*Client)(nil)
