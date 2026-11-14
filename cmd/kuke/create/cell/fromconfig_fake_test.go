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

package cell_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// fakeRenderDigest is the digest the test fake's stand-in renderer reports.
const fakeRenderDigest = "sha256:fake"

// renderFromConfig stands in for kukeond's CellConfig render (#1418) so the
// CLI tests keep asserting on the cell the daemon would produce: it reads the
// Config and Blueprint through the fake's GetConfig/GetBlueprint hooks and
// runs the same materialization the daemon does (with no scope defaults).
func (f *fakeClient) renderFromConfig(
	ctx context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.PreviewCellFromConfigResult, error) {
	cfgRes, err := f.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: req.Config})
	if err != nil {
		return kukeonv1.PreviewCellFromConfigResult{}, err
	}
	if !cfgRes.MetadataExists {
		return kukeonv1.PreviewCellFromConfigResult{}, fmt.Errorf(
			"%w (config %q)",
			errdefs.ErrConfigNotFound,
			req.Config.Name,
		)
	}
	ref := cfgRes.Config.Spec.Blueprint
	bpRes, err := f.GetBlueprint(ctx, v1beta1.CellBlueprintDoc{Metadata: v1beta1.CellBlueprintMetadata{
		Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack,
	}})
	if err != nil {
		return kukeonv1.PreviewCellFromConfigResult{}, err
	}
	if !bpRes.MetadataExists {
		return kukeonv1.PreviewCellFromConfigResult{}, fmt.Errorf(
			"%w (blueprint %q)",
			errdefs.ErrBlueprintNotFound,
			ref.Name,
		)
	}
	realm, space, stack := cellconfig.CellScope(cfgRes.Config)
	name, err := naming.AllocCellName(req.Name, cellconfig.Prefix(cfgRes.Config), func(candidate string) (bool, error) {
		return f.fakeCellExists(ctx, candidate, realm, space, stack)
	})
	if err != nil {
		return kukeonv1.PreviewCellFromConfigResult{}, err
	}
	doc, err := cellconfig.MaterializeWithName(cfgRes.Config, bpRes.Blueprint, name)
	if err != nil {
		return kukeonv1.PreviewCellFromConfigResult{}, err
	}
	cellconfig.ApplyEnvOverrides(&doc, req.EnvOverrides)
	if req.AutoDelete {
		doc.Spec.AutoDelete = true
	}
	return kukeonv1.PreviewCellFromConfigResult{Cell: doc, Digest: fakeRenderDigest}, nil
}

func (f *fakeClient) fakeCellExists(ctx context.Context, name, realm, space, stack string) (bool, error) {
	res, err := f.GetCell(ctx, v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: name},
		Spec:     v1beta1.CellSpec{RealmID: realm, SpaceID: space, StackID: stack},
	})
	if errors.Is(err, errdefs.ErrCellNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return res.MetadataExists, nil
}

func (f *fakeClient) PreviewCellFromConfig(
	ctx context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.PreviewCellFromConfigResult, error) {
	return f.renderFromConfig(ctx, req)
}

// CreateCellFromConfig renders like the daemon, refuses an existing name, and
// persists through the fake's CreateCell (Start) or MaterializeCell hook.
func (f *fakeClient) CreateCellFromConfig(
	ctx context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.CreateCellFromConfigResult, error) {
	preview, err := f.renderFromConfig(ctx, req)
	if err != nil {
		return kukeonv1.CreateCellFromConfigResult{}, err
	}
	doc := preview.Cell
	exists, err := f.fakeCellExists(ctx, doc.Metadata.Name, doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID)
	if err != nil {
		return kukeonv1.CreateCellFromConfigResult{}, err
	}
	if exists {
		return kukeonv1.CreateCellFromConfigResult{}, fmt.Errorf(
			"%w: cell %q",
			errdefs.ErrCellAlreadyExists,
			doc.Metadata.Name,
		)
	}
	doc.Spec.IgnoreDiskPressure = req.IgnoreDiskPressure
	var created kukeonv1.CreateCellResult
	if req.Start {
		created, err = f.CreateCell(ctx, doc)
	} else {
		created, err = f.materializeForConfig(ctx, doc)
	}
	if err != nil {
		return kukeonv1.CreateCellFromConfigResult{}, err
	}
	return kukeonv1.CreateCellFromConfigResult{CreateCellResult: created, Digest: preview.Digest}, nil
}

func (f *fakeClient) materializeForConfig(ctx context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	return f.MaterializeCell(ctx, doc)
}
