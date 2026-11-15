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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// PreviewCellFromConfig delegates to the controller's daemon-side CellConfig
// render (#1418).
func (c *Client) PreviewCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.PreviewCellFromConfigResult, error) {
	res, err := c.ctrl.PreviewCellFromConfig(cellFromConfigRequest(req))
	if err != nil {
		return kukeonv1.PreviewCellFromConfigResult{}, err
	}
	return kukeonv1.PreviewCellFromConfigResult{Cell: res.Cell, Digest: res.Digest, Values: res.Values}, nil
}

// CreateCellFromConfig delegates to the controller's daemon-side render +
// create (#1418) and reshapes the result into external types.
func (c *Client) CreateCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.CreateCellFromConfigResult, error) {
	res, err := c.ctrl.CreateCellFromConfig(cellFromConfigRequest(req))
	if err != nil {
		return kukeonv1.CreateCellFromConfigResult{}, err
	}
	created, err := createCellResultToExternal(res.CreateCellResult)
	if err != nil {
		return kukeonv1.CreateCellFromConfigResult{}, err
	}
	return kukeonv1.CreateCellFromConfigResult{
		CreateCellResult: created,
		Digest:           res.Digest,
		Values:           res.Values,
	}, nil
}

func cellFromConfigRequest(req kukeonv1.CellFromConfigRequest) controller.CellFromConfigRequest {
	return controller.CellFromConfigRequest{
		Config: intmodel.CellConfigMetadata{
			Name:  req.Config.Name,
			Realm: req.Config.Realm,
			Space: req.Config.Space,
			Stack: req.Config.Stack,
		},
		Name:               req.Name,
		EnvOverrides:       append([]string(nil), req.EnvOverrides...),
		AutoDelete:         req.AutoDelete,
		IgnoreDiskPressure: req.IgnoreDiskPressure,
		Start:              req.Start,
		ExpectedDigest:     req.ExpectedDigest,
	}
}

// createCellResultToExternal reshapes a controller CreateCellResult into the
// wire type, converting the cell to its external v1beta1 form.
func createCellResultToExternal(res controller.CreateCellResult) (kukeonv1.CreateCellResult, error) {
	extCell, err := apischeme.BuildCellExternalFromInternal(res.Cell, apischeme.VersionV1Beta1)
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
	for _, container := range res.Containers {
		out.Containers = append(out.Containers, kukeonv1.ContainerCreationOutcome{
			Name:       container.Name,
			ExistsPre:  container.ExistsPre,
			ExistsPost: container.ExistsPost,
			Created:    container.Created,
		})
	}
	return out, nil
}
