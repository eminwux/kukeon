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

package local

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/cgroupinspect"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// GetCgroupDetail implements kukeonv1.Client (issue #1410). It resolves the
// scoped resource's cgroup path from its persisted status (a container sits
// at <cell status.cgroupPath>/<containerd id>, the same placement
// ctr.cellCgroupsPath gives runc) and reads the live interface files under
// the daemon's cgroup mountpoint.
func (c *Client) GetCgroupDetail(
	ctx context.Context, scope kukeonv1.CgroupScope,
) (kukeonv1.GetCgroupDetailResult, error) {
	res, err := c.resolveCgroupScope(ctx, scope)
	if err != nil {
		return kukeonv1.GetCgroupDetailResult{}, err
	}
	if strings.TrimSpace(res.CgroupPath) == "" {
		return res, nil
	}
	mountpoint, err := c.ctrl.CgroupMountpoint()
	if err != nil {
		return kukeonv1.GetCgroupDetailResult{}, err
	}
	return inspectCgroup(os.DirFS(mountpoint), mountpoint, res)
}

// inspectCgroup fills the absolute path, existence and live values of res by
// reading fsys, which is rooted at mountpoint.
func inspectCgroup(
	fsys fs.FS, mountpoint string, res kukeonv1.GetCgroupDetailResult,
) (kukeonv1.GetCgroupDetailResult, error) {
	res.AbsolutePath = filepath.Join(mountpoint, cgroupinspect.FSPath(res.CgroupPath))
	exists, live, err := cgroupinspect.Inspect(fsys, res.CgroupPath)
	if err != nil {
		return kukeonv1.GetCgroupDetailResult{}, err
	}
	res.Exists = exists
	res.Live = live
	return res, nil
}

// resolveCgroupScope reads the scoped resource and copies its declared cgroup
// configuration into a result. A resource whose cgroup path is not recorded
// yet resolves with an empty CgroupPath.
func (c *Client) resolveCgroupScope(
	ctx context.Context, scope kukeonv1.CgroupScope,
) (kukeonv1.GetCgroupDetailResult, error) {
	res := kukeonv1.GetCgroupDetailResult{Scope: scope}
	switch scope.Kind {
	case kukeonv1.CgroupKindRealm:
		got, err := c.GetRealm(ctx, v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: scope.Realm}})
		if err != nil {
			return res, err
		}
		if !got.MetadataExists {
			return res, errdefs.ErrRealmNotFound
		}
		s := got.Realm.Status
		res.CgroupPath, res.Ready, res.Reason, res.SubtreeControllers = s.CgroupPath, s.CgroupReady, s.Reason, s.SubtreeControllers
	case kukeonv1.CgroupKindSpace:
		space, err := c.cgroupSpace(ctx, scope)
		if err != nil {
			return res, err
		}
		s := space.Status
		res.CgroupPath, res.Ready, res.Reason, res.SubtreeControllers = s.CgroupPath, s.CgroupReady, s.Reason, s.SubtreeControllers
	case kukeonv1.CgroupKindStack:
		got, err := c.GetStack(ctx, v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{Name: scope.Stack},
			Spec:     v1beta1.StackSpec{RealmID: scope.Realm, SpaceID: scope.Space},
		})
		if err != nil {
			return res, err
		}
		if !got.MetadataExists {
			return res, errdefs.ErrStackNotFound
		}
		s := got.Stack.Status
		res.CgroupPath, res.Ready, res.Reason, res.SubtreeControllers = s.CgroupPath, s.CgroupReady, s.Reason, s.SubtreeControllers
	case kukeonv1.CgroupKindCell:
		cell, err := c.cgroupCell(ctx, scope)
		if err != nil {
			return res, err
		}
		s := cell.Status
		res.CgroupPath, res.Ready, res.Reason, res.SubtreeControllers = s.CgroupPath, s.CgroupReady, s.Reason, s.SubtreeControllers
		nested := cell.Spec.NestedCgroupRuntime
		res.NestedCgroupRuntime = &nested
	case kukeonv1.CgroupKindContainer:
		return c.resolveContainerCgroup(ctx, scope)
	default:
		return res, fmt.Errorf("%w: %q", errdefs.ErrCgroupScopeKind, scope.Kind)
	}
	return res, nil
}

func (c *Client) resolveContainerCgroup(
	ctx context.Context, scope kukeonv1.CgroupScope,
) (kukeonv1.GetCgroupDetailResult, error) {
	res := kukeonv1.GetCgroupDetailResult{Scope: scope}
	if strings.TrimSpace(scope.Container) == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	cell, err := c.cgroupCell(ctx, scope)
	if err != nil {
		return res, err
	}
	var container *v1beta1.ContainerSpec
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == scope.Container {
			container = &cell.Spec.Containers[i]
			break
		}
	}
	if container == nil {
		return res, errdefs.ErrContainerNotFound
	}
	space, err := c.cgroupSpace(ctx, scope)
	if err != nil {
		return res, err
	}
	var spaceDefault *v1beta1.ContainerResources
	if space.Spec.Defaults != nil && space.Spec.Defaults.Container != nil {
		spaceDefault = space.Spec.Defaults.Container.Resources
	}
	res.Declared = cgroupinspect.Declared(container.Resources, spaceDefault)
	hostCgroup := container.HostCgroup
	res.HostCgroup = &hostCgroup
	containerdID := strings.TrimSpace(container.ContainerdID)
	if containerdID == "" {
		if container.Root {
			containerdID, err = naming.BuildRootContainerdID(scope.Space, scope.Stack, scope.Cell)
		} else {
			containerdID, err = naming.BuildContainerdID(scope.Space, scope.Stack, scope.Cell, container.ID)
		}
		if err != nil {
			return res, err
		}
	}
	if cellPath := strings.TrimSpace(cell.Status.CgroupPath); cellPath != "" {
		res.CgroupPath = filepath.Join(cellPath, containerdID)
	}
	// A container has no cgroup readiness of its own; it inherits the cell's.
	res.Ready = cell.Status.CgroupReady
	if !res.Ready {
		res.Reason = "CellCgroupNotReady"
	}
	return res, nil
}

func (c *Client) cgroupSpace(ctx context.Context, scope kukeonv1.CgroupScope) (v1beta1.SpaceDoc, error) {
	got, err := c.GetSpace(ctx, v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{Name: scope.Space},
		Spec:     v1beta1.SpaceSpec{RealmID: scope.Realm},
	})
	if err != nil {
		return v1beta1.SpaceDoc{}, err
	}
	if !got.MetadataExists {
		return v1beta1.SpaceDoc{}, errdefs.ErrSpaceNotFound
	}
	return got.Space, nil
}

func (c *Client) cgroupCell(ctx context.Context, scope kukeonv1.CgroupScope) (v1beta1.CellDoc, error) {
	got, err := c.GetCell(ctx, v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: scope.Cell},
		Spec: v1beta1.CellSpec{
			RealmID: scope.Realm,
			SpaceID: scope.Space,
			StackID: scope.Stack,
		},
	})
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !got.MetadataExists {
		return v1beta1.CellDoc{}, errdefs.ErrCellNotFound
	}
	return got.Cell, nil
}
