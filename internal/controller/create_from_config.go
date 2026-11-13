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

package controller

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// CellFromConfigRequest asks the daemon to render (and optionally create) a
// cell from a stored CellConfig (issue #1418).
type CellFromConfigRequest struct {
	// Config locates the CellConfig: name plus the scope to read it from.
	Config intmodel.CellConfigMetadata
	// Name is the explicit cell name; empty allocates <prefix>-<6hex>.
	Name string
	// EnvOverrides are per-cell `--env KEY=VALUE` overrides baked into the
	// attachable container and recorded in provenance.
	EnvOverrides []string
	// AutoDelete marks the cell for deletion after its attach session ends
	// (`kuke run --rm`).
	AutoDelete bool
	// IgnoreDiskPressure bypasses the create-time disk-pressure guard.
	IgnoreDiskPressure bool
	// Start starts the cell after creating it (`kuke run`); otherwise it is
	// left stopped (`kuke create cell`).
	Start bool
	// ExpectedDigest, when set, refuses the create unless the fresh render
	// has this digest (the preview → create handshake).
	ExpectedDigest string
}

// CellFromConfigPreview is the outcome of a render: the rendered cell, its
// digest and the merge explanation.
type CellFromConfigPreview struct {
	Cell   v1beta1.CellDoc
	Digest string
	Values []v1beta1.RenderedValue
}

// CellFromConfigResult is the outcome of CreateCellFromConfig.
type CellFromConfigResult struct {
	CreateCellResult

	Digest string
	Values []v1beta1.RenderedValue
}

// PreviewCellFromConfig renders the cell a CreateCellFromConfig with the same
// request would create, without writing anything (issue #1418). An omitted
// name is allocated against the cells currently in scope, so the preview shows
// the name a create would use; pass it back as Name to create exactly the
// previewed cell.
func (b *Exec) PreviewCellFromConfig(req CellFromConfigRequest) (CellFromConfigPreview, error) {
	cfg, err := b.lookupRequestConfig(req.Config)
	if err != nil {
		return CellFromConfigPreview{}, err
	}
	cfgDoc, err := apischeme.ConvertCellConfigToExternal(cfg)
	if err != nil {
		return CellFromConfigPreview{}, fmt.Errorf("%w: decode config: %w", errdefs.ErrConversionFailed, err)
	}

	realm, space, stack := cellconfig.CellScope(cfgDoc)
	name, err := b.allocateConfigCellName(req.Name, cellconfig.Prefix(cfgDoc), realm, space, stack)
	if err != nil {
		return CellFromConfigPreview{}, err
	}

	rendered, err := renderCellFromConfig(b.runner, cfg, name, req.EnvOverrides)
	if err != nil {
		return CellFromConfigPreview{}, err
	}
	if req.AutoDelete {
		rendered.Cell.Spec.AutoDelete = true
	}
	return CellFromConfigPreview(rendered), nil
}

// CreateCellFromConfig renders a cell from a stored CellConfig and creates it
// (issue #1418) — the only path a CellConfig becomes a Cell. It refuses when
// an explicit name already exists in scope, and when ExpectedDigest is set
// and the fresh render differs from it.
func (b *Exec) CreateCellFromConfig(req CellFromConfigRequest) (CellFromConfigResult, error) {
	preview, err := b.PreviewCellFromConfig(req)
	if err != nil {
		return CellFromConfigResult{}, err
	}
	if want := strings.TrimSpace(req.ExpectedDigest); want != "" && want != preview.Digest {
		return CellFromConfigResult{}, fmt.Errorf(
			"%w (previewed %s, now %s)", errdefs.ErrRenderDigestMismatch, want, preview.Digest)
	}

	cell, err := apischeme.ConvertCellDocToInternal(preview.Cell)
	if err != nil {
		return CellFromConfigResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	if exists, existsErr := b.cellExists(cell.Metadata.Name, cell.Spec.RealmName, cell.Spec.SpaceName,
		cell.Spec.StackName); existsErr != nil {
		return CellFromConfigResult{}, existsErr
	} else if exists {
		return CellFromConfigResult{}, fmt.Errorf(
			"%w: cell %q already exists in realm=%q space=%q stack=%q",
			errdefs.ErrCellAlreadyExists, cell.Metadata.Name,
			cell.Spec.RealmName, cell.Spec.SpaceName, cell.Spec.StackName,
		)
	}
	cell.Spec.IgnoreDiskPressure = req.IgnoreDiskPressure

	var created CreateCellResult
	if req.Start {
		created, err = b.CreateCell(cell)
	} else {
		created, err = b.MaterializeCell(cell)
	}
	if err != nil {
		return CellFromConfigResult{}, err
	}
	return CellFromConfigResult{CreateCellResult: created, Digest: preview.Digest, Values: preview.Values}, nil
}

// lookupRequestConfig reads the CellConfig a request names.
func (b *Exec) lookupRequestConfig(md intmodel.CellConfigMetadata) (intmodel.CellConfig, error) {
	if strings.TrimSpace(md.Name) == "" {
		return intmodel.CellConfig{}, fmt.Errorf("%w: config name is required", errdefs.ErrConfig)
	}
	if strings.TrimSpace(md.Realm) == "" {
		return intmodel.CellConfig{}, fmt.Errorf("%w: config realm is required", errdefs.ErrConfig)
	}
	cfg, err := b.runner.GetConfig(intmodel.CellConfig{Metadata: md})
	if err != nil {
		if errors.Is(err, errdefs.ErrConfigNotFound) {
			return intmodel.CellConfig{}, fmt.Errorf(
				"%w (config %q in scope realm=%q space=%q stack=%q)",
				errdefs.ErrConfigNotFound, md.Name, md.Realm, md.Space, md.Stack,
			)
		}
		return intmodel.CellConfig{}, err
	}
	return cfg, nil
}

// allocateConfigCellName validates an explicit name or allocates a free
// <prefix>-<6hex> one in the cell's scope.
func (b *Exec) allocateConfigCellName(explicit, prefix, realm, space, stack string) (string, error) {
	name, err := naming.AllocCellName(explicit, prefix, func(candidate string) (bool, error) {
		return b.cellExists(candidate, realm, space, stack)
	})
	if err != nil {
		return "", err
	}
	if validateErr := naming.ValidateHierarchyName("cell", name); validateErr != nil {
		return "", validateErr
	}
	return name, nil
}

// cellExists reports whether a cell with the given name lives in scope.
func (b *Exec) cellExists(name, realm, space, stack string) (bool, error) {
	_, err := b.runner.GetCell(intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: name},
		Spec: intmodel.CellSpec{
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
		},
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errdefs.ErrCellNotFound) || errors.Is(err, errdefs.ErrStackNotFound) ||
		errors.Is(err, errdefs.ErrSpaceNotFound) || errors.Is(err, errdefs.ErrRealmNotFound) {
		return false, nil
	}
	return false, err
}
