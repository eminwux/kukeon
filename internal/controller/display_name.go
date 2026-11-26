// Copyright 2026 Emiliano Spinella (eminwux)
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
	"maps"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Display-name target kinds (DisplayNameTarget.Kind).
const (
	DisplayNameConfig = "config"
	DisplayNameCell   = "cell"
)

// DisplayNameTarget names the CellConfig or cell a SetDisplayName renames.
// A Config may be realm-, space- or stack-scoped; a cell always has all three.
type DisplayNameTarget struct {
	Kind  string
	Name  string
	Realm string
	Space string
	Stack string
}

// SetDisplayNameResult reports the display name a target has after the call
// (empty: none) and the target's annotations, and whether the stored document
// changed.
type SetDisplayNameResult struct {
	DisplayName string
	Annotations map[string]string
	Changed     bool
}

// SetDisplayName sets or, for an empty name, clears the display name of a
// CellConfig or a cell (v1beta1.AnnotationDisplayName, issue #1692). Only the
// annotation changes: a Config's display name is left out of the config
// revision its cells are compared against, and a cell's annotations are never
// compared, so no cell is restarted, recreated or marked OutOfSync.
func (b *Exec) SetDisplayName(target DisplayNameTarget, displayName string) (SetDisplayNameResult, error) {
	name, err := v1beta1.NormalizeDisplayName(displayName)
	if err != nil {
		return SetDisplayNameResult{}, fmt.Errorf("%w: %w", errdefs.ErrInvalidDisplayName, err)
	}
	if err = validateDisplayNameTarget(target); err != nil {
		return SetDisplayNameResult{}, err
	}
	switch target.Kind {
	case DisplayNameConfig:
		return b.setConfigDisplayName(target, name)
	case DisplayNameCell:
		return b.setCellDisplayName(target, name)
	default:
		return SetDisplayNameResult{}, fmt.Errorf("%w: display name target must be %s or %s",
			errdefs.ErrConfig, DisplayNameConfig, DisplayNameCell)
	}
}

func validateDisplayNameTarget(target DisplayNameTarget) error {
	segments := []struct{ kind, value string }{
		{target.Kind, target.Name}, {"realm", target.Realm}, {"space", target.Space}, {"stack", target.Stack},
	}
	for i, s := range segments {
		if s.value == "" {
			// A cell has the full scope; a Config's scope stops at any level.
			if i == 0 || i == 1 || target.Kind == DisplayNameCell {
				return fmt.Errorf("%w: %s name is required", errdefs.ErrConfig, s.kind)
			}
			continue
		}
		if !filepath.IsLocal(s.value) || strings.ContainsAny(s.value, `/\`) || s.value != strings.TrimSpace(s.value) {
			return fmt.Errorf("%w: unsafe %s name %q", errdefs.ErrConfig, s.kind, s.value)
		}
		if err := naming.ValidateHierarchyName(s.kind, s.value); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
		}
	}
	if target.Stack != "" && target.Space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrConfigScopeIncomplete)
	}
	return nil
}

// withDisplayName returns annotations with the display name set to name, or
// removed when name is empty, and whether that changed anything. The input is
// not modified; an empty result is nil.
func withDisplayName(annotations map[string]string, name string) (map[string]string, bool) {
	current, set := annotations[v1beta1.AnnotationDisplayName]
	if (name == "" && !set) || (name != "" && set && current == name) {
		return annotations, false
	}
	out := maps.Clone(annotations)
	if name == "" {
		delete(out, v1beta1.AnnotationDisplayName)
	} else {
		if out == nil {
			out = map[string]string{}
		}
		out[v1beta1.AnnotationDisplayName] = name
	}
	if len(out) == 0 {
		out = nil
	}
	return out, true
}

func (b *Exec) setConfigDisplayName(target DisplayNameTarget, name string) (SetDisplayNameResult, error) {
	lookup := intmodel.CellConfig{Metadata: intmodel.CellConfigMetadata{
		Name: target.Name, Realm: target.Realm, Space: target.Space, Stack: target.Stack,
	}}
	stored, err := b.runner.GetConfig(lookup)
	if err != nil {
		if errors.Is(err, errdefs.ErrConfigNotFound) {
			return SetDisplayNameResult{}, fmt.Errorf("%w: %q", errdefs.ErrConfigNotFound, target.Name)
		}
		return SetDisplayNameResult{}, fmt.Errorf("%w: %w", errdefs.ErrGetConfig, err)
	}
	doc, err := apischeme.ConvertCellConfigToExternal(stored)
	if err != nil {
		return SetDisplayNameResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	annotations, changed := withDisplayName(doc.Metadata.Annotations, name)
	result := SetDisplayNameResult{DisplayName: name, Annotations: maps.Clone(annotations), Changed: changed}
	if !changed {
		return result, nil
	}
	doc.Metadata.Annotations = annotations
	desired, _, err := apischeme.NormalizeCellConfig(doc)
	if err != nil {
		return SetDisplayNameResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	if _, err = b.runner.WriteConfig(desired); err != nil {
		return SetDisplayNameResult{}, err
	}
	return result, nil
}

func (b *Exec) setCellDisplayName(target DisplayNameTarget, name string) (SetDisplayNameResult, error) {
	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: target.Name},
		Spec:     intmodel.CellSpec{RealmName: target.Realm, SpaceName: target.Space, StackName: target.Stack},
	}
	var result SetDisplayNameResult
	err := b.runner.PatchCellMetadata(cell, func(fresh *intmodel.Cell) bool {
		annotations, changed := withDisplayName(fresh.Metadata.Annotations, name)
		result = SetDisplayNameResult{DisplayName: name, Annotations: maps.Clone(annotations), Changed: changed}
		if changed {
			fresh.Metadata.Annotations = annotations
		}
		return changed
	})
	if err != nil {
		return SetDisplayNameResult{}, err
	}
	return result, nil
}
