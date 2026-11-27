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
	"github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Change classes CellDriftDiff.ChangeType reports, from the point of view of
// the next start of the cell (reapplyLineageOnStart).
const (
	// DriftChangeNone: only render provenance moved; the spec is unchanged.
	DriftChangeNone = "none"
	// DriftChangeAdditive: containers added or removed; applied in place.
	DriftChangeAdditive = "additive"
	// DriftChangeCompatible: fields a restart applies in place.
	DriftChangeCompatible = "compatible"
	// DriftChangeBreaking: a restart recreates the cell — a breaking field
	// changed, or a realm image was rebuilt under the same reference.
	DriftChangeBreaking = "breaking"
)

// CellDriftDiff is the stored cell next to the cell its lineage Config renders
// to now (issue #1768). Desired is the stored cell with only the fields the
// drift check reports replaced by the render's values (overlayDrift), so
// Live and Desired differ exactly where the cell is out of sync. Both
// documents are unmasked; the caller decides what to hide.
type CellDriftDiff struct {
	Live v1beta1.CellDoc
	// Desired is nil when the lineage Config was deleted.
	Desired     *v1beta1.CellDoc
	ConfigFound bool
	// Reasons are the drift check's reasons, in its order: the provenance
	// reasons, then the "spec differs" summary.
	Reasons []string
	// Paths are the field paths the spec diff names (collectDiffFieldPaths).
	Paths []string
	// ChangeType is one of the DriftChange* classes; empty when the lineage
	// Config was deleted.
	ChangeType string
}

// DiffCellFromConfig renders a Config-lineage cell from its lineage Config,
// as the drift check does, and returns the stored cell beside the overlaid
// render (issue #1768). It is computed on request and writes nothing.
//
// A cell with no `kukeon.io/config` label returns errdefs.ErrCellNotFromConfig.
// A deleted lineage Config is not an error: ConfigFound is false, Desired is
// nil and Reasons is the drift check's "lineage Config deleted". A lookup or
// render failure returns the error whose text the drift check stores as
// OutOfSyncError.
func (b *Exec) DiffCellFromConfig(cell intmodel.Cell) (CellDriftDiff, error) {
	live, err := b.lookupCell(cell)
	if err != nil {
		return CellDriftDiff{}, err
	}
	configName, ok := configLineage(live)
	if !ok {
		return CellDriftDiff{}, fmt.Errorf("cell %q: %w", live.Metadata.Name, errdefs.ErrCellNotFromConfig)
	}

	render, err := renderLineageDiff(b.runner, live, configName)
	if err != nil {
		return CellDriftDiff{}, err
	}
	liveDoc, err := driftDoc(live)
	if err != nil {
		return CellDriftDiff{}, err
	}
	if !render.ConfigFound {
		return CellDriftDiff{Live: liveDoc, Reasons: []string{outOfSyncReasonConfigDeleted}}, nil
	}

	shown := overlayDrift(live, render.Desired, render.Diff, render.Drift)
	desiredDoc, err := driftDoc(shown)
	if err != nil {
		return CellDriftDiff{}, err
	}
	return CellDriftDiff{
		Live:        liveDoc,
		Desired:     &desiredDoc,
		ConfigFound: true,
		Reasons:     render.reasons(),
		Paths:       collectDiffFieldPaths(render.Diff),
		ChangeType:  driftChangeType(render),
	}, nil
}

// lookupCell reads the stored cell named by the cell's name, realm, space and
// stack, as GetCell does. A missing cell returns errdefs.ErrCellNotFound.
func (b *Exec) lookupCell(cell intmodel.Cell) (intmodel.Cell, error) {
	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return intmodel.Cell{}, errdefs.ErrCellNameRequired
	}
	realm := strings.TrimSpace(cell.Spec.RealmName)
	if realm == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(cell.Spec.SpaceName)
	if space == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(cell.Spec.StackName)
	if stack == "" {
		return intmodel.Cell{}, errdefs.ErrStackNameRequired
	}

	live, err := b.runner.GetCell(intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: name},
		Spec:     intmodel.CellSpec{RealmName: realm, SpaceName: space, StackName: stack},
	})
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return intmodel.Cell{}, err
		}
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}
	if live.Spec.RealmName != realm || live.Spec.SpaceName != space || live.Spec.StackName != stack {
		return intmodel.Cell{}, fmt.Errorf(
			"cell %q in %s/%s/%s: %w",
			name,
			realm,
			space,
			stack,
			errdefs.ErrCellNotFound,
		)
	}
	return live, nil
}

// driftDoc converts a cell for the diff: the status, the CLI-injected
// RuntimeEnv and the provenance are cleared, since none of them is part of
// what the render produces (a provenance values change already shows as the
// values-hash annotation).
func driftDoc(cell intmodel.Cell) (v1beta1.CellDoc, error) {
	cell.Status = intmodel.CellStatus{}
	cell.Spec.RuntimeEnv = nil
	cell.Spec.Provenance = nil
	doc, err := apischeme.BuildCellExternalFromInternal(cell, apischeme.VersionV1Beta1)
	if err != nil {
		return v1beta1.CellDoc{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	doc.Status = v1beta1.CellStatus{}
	return doc, nil
}

// driftChangeType classifies the render the way the next start routes it
// (reapplyLineageOnStart): a breaking spec change or a rebuilt realm image
// recreates the cell, any other spec change is applied in place, and
// provenance-only drift changes no spec.
func driftChangeType(render lineageDiff) string {
	for _, d := range render.Drift {
		if d.Key == cellconfig.AnnotationRenderImageDigests {
			return DriftChangeBreaking
		}
	}
	switch render.Diff.ChangeType {
	case apply.ChangeTypeBreaking:
		return DriftChangeBreaking
	case apply.ChangeTypeCompatible:
		return DriftChangeCompatible
	case apply.ChangeTypeAdditive:
		return DriftChangeAdditive
	case apply.ChangeTypeNone:
		return DriftChangeNone
	}
	return DriftChangeNone
}
