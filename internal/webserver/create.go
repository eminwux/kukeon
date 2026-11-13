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

package webserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const maxCreateBody = 64 << 10

type createConfigRequest struct {
	Config             v1beta1.CellConfigMetadata `json:"config"`
	Name               string                     `json:"name"`
	ResolvedName       string                     `json:"resolvedName,omitempty"`
	Env                []string                   `json:"env"`
	IgnoreDiskPressure bool                       `json:"ignoreDiskPressure"`
	Preview            string                     `json:"preview"`
}

func registerCreateRoutes(mux *http.ServeMux, dial Dial) {
	registerManualCreateRoutes(mux, dial)
	for _, action := range []string{"preview", "create"} {
		mux.HandleFunc(
			"POST /api/cells/from-config/"+action,
			resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
				// Mutations require a same-origin browser request and JSON, including preview.
				if r.Header.Get("Origin") != "http://"+r.Host || r.Header.Get("Content-Type") != "application/json" {
					return nil, fmt.Errorf("%w: same-origin JSON request required", errdefs.ErrConfig)
				}
				var input createConfigRequest
				decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCreateBody))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&input); err != nil {
					return nil, fmt.Errorf("%w: invalid request: %w", errdefs.ErrConfig, err)
				}
				if err := decoder.Decode(&struct{}{}); err != io.EOF {
					return nil, fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
				}
				doc, err := configCell(ctx, c, input)
				if err != nil {
					return nil, redactCreationError(err, envSecrets(input.Env))
				}
				data, err := json.Marshal(doc)
				if err != nil {
					return nil, err
				}
				digest := fmt.Sprintf("%x", sha256.Sum256(append(data, byteBool(input.IgnoreDiskPressure))))
				if strings.HasSuffix(r.URL.Path, "/preview") {
					return map[string]any{"document": redactCell(doc), "preview": digest}, nil
				}
				if input.Preview != digest {
					return nil, fmt.Errorf("%w: inputs or config changed; preview again", errdefs.ErrConfig)
				}
				doc.Spec.IgnoreDiskPressure = input.IgnoreDiskPressure
				created, createErr := c.MaterializeCell(ctx, doc)
				if createErr != nil {
					return nil, redactCreationError(createErr, envSecrets(input.Env))
				}
				if !created.Created {
					return nil, errdefs.ErrCellAlreadyExists
				}
				return redactCell(created.Cell), nil
			}),
		)
	}
}

func configCell(ctx context.Context, c kukeonv1.Client, input createConfigRequest) (v1beta1.CellDoc, error) {
	var empty v1beta1.CellDoc
	name := strings.TrimSpace(input.Name)
	resolvedName := strings.TrimSpace(input.ResolvedName)
	if name != "" && resolvedName != "" && name != resolvedName {
		return empty, fmt.Errorf("%w: the reviewed cell name changed; preview again", errdefs.ErrConfig)
	}
	if name != "" && (!filepath.IsLocal(name) || !safePathSegment(name)) {
		return empty, fmt.Errorf("%w: unsafe cell name", errdefs.ErrConfig)
	}
	if resolvedName != "" && (!filepath.IsLocal(resolvedName) || !safePathSegment(resolvedName)) {
		return empty, fmt.Errorf("%w: unsafe resolved cell name", errdefs.ErrConfig)
	}
	if input.Config.Realm == "" || input.Config.Name == "" || (input.Config.Stack != "" && input.Config.Space == "") {
		return empty, fmt.Errorf("%w: select a scoped config", errdefs.ErrConfig)
	}
	configName := input.Config.Name
	configRealm := input.Config.Realm
	configSpace := ""
	if rawSpace := input.Config.Space; rawSpace != "" {
		if !filepath.IsLocal(rawSpace) {
			return empty, fmt.Errorf("%w: unsafe config space", errdefs.ErrConfig)
		}
		if !safePathSegment(rawSpace) {
			return empty, fmt.Errorf("%w: unsafe config space", errdefs.ErrConfig)
		}
		configSpace = rawSpace
	}
	configStack := ""
	if rawStack := input.Config.Stack; rawStack != "" {
		if !filepath.IsLocal(rawStack) {
			return empty, fmt.Errorf("%w: unsafe config stack", errdefs.ErrConfig)
		}
		if !safePathSegment(rawStack) {
			return empty, fmt.Errorf("%w: unsafe config stack", errdefs.ErrConfig)
		}
		configStack = rawStack
	}
	if !filepath.IsLocal(configName) {
		return empty, fmt.Errorf("%w: unsafe config name", errdefs.ErrConfig)
	}
	if !safePathSegment(configName) {
		return empty, fmt.Errorf("%w: unsafe config name", errdefs.ErrConfig)
	}
	if !filepath.IsLocal(configRealm) {
		return empty, fmt.Errorf("%w: unsafe config realm", errdefs.ErrConfig)
	}
	if !safePathSegment(configRealm) {
		return empty, fmt.Errorf("%w: unsafe config realm", errdefs.ErrConfig)
	}
	configMetadata := v1beta1.CellConfigMetadata{
		Name:  configName,
		Realm: configRealm,
		Space: configSpace,
		Stack: configStack,
	}
	cfg, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: configMetadata})
	if err != nil {
		return empty, err
	}
	if !cfg.MetadataExists {
		return empty, errdefs.ErrConfigNotFound
	}
	ref := cfg.Config.Spec.Blueprint
	if !filepath.IsLocal(ref.Name) || !safePathSegment(ref.Name) ||
		!filepath.IsLocal(ref.Realm) || !safePathSegment(ref.Realm) ||
		(ref.Space != "" && (!filepath.IsLocal(ref.Space) || !safePathSegment(ref.Space))) ||
		(ref.Stack != "" && (!filepath.IsLocal(ref.Stack) || !safePathSegment(ref.Stack))) {
		return empty, fmt.Errorf("%w: unsafe blueprint reference", errdefs.ErrConfig)
	}
	bp, err := c.GetBlueprint(
		ctx,
		v1beta1.CellBlueprintDoc{
			Metadata: v1beta1.CellBlueprintMetadata{
				Name:  ref.Name,
				Realm: ref.Realm,
				Space: ref.Space,
				Stack: ref.Stack,
			},
		},
	)
	if err != nil {
		return empty, err
	}
	if !bp.MetadataExists {
		return empty, errdefs.ErrBlueprintNotFound
	}
	requestedName := name
	if requestedName == "" {
		requestedName = resolvedName
	}
	if requestedName == "" {
		realm := defaultScope(cfg.Config.Metadata.Realm, consts.KukeonDefaultRealmName)
		space := defaultScope(cfg.Config.Metadata.Space, consts.KukeonDefaultSpaceName)
		stack := defaultScope(cfg.Config.Metadata.Stack, consts.KukeonDefaultStackName)
		var nameErr error
		requestedName, nameErr = allocateCellName(ctx, c, "", cellconfig.Prefix(cfg.Config), realm, space, stack)
		if nameErr != nil {
			return empty, fmt.Errorf("%w: allocate cell name: %w", errdefs.ErrConfig, nameErr)
		}
	}
	if validationErr := naming.ValidateHierarchyName("cell", requestedName); validationErr != nil {
		return empty, fmt.Errorf("%w: %w", errdefs.ErrConfig, validationErr)
	}
	doc, err := cellconfig.MaterializeWithName(cfg.Config, bp.Blueprint, requestedName)
	if err != nil {
		return empty, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	if !filepath.IsLocal(doc.Metadata.Name) || !safePathSegment(doc.Metadata.Name) ||
		!filepath.IsLocal(doc.Spec.RealmID) || !safePathSegment(doc.Spec.RealmID) ||
		(doc.Spec.SpaceID != "" && (!filepath.IsLocal(doc.Spec.SpaceID) || !safePathSegment(doc.Spec.SpaceID))) ||
		(doc.Spec.StackID != "" && (!filepath.IsLocal(doc.Spec.StackID) || !safePathSegment(doc.Spec.StackID))) {
		return empty, fmt.Errorf("%w: unsafe materialized cell coordinate", errdefs.ErrConfig)
	}
	seen := map[string]string{}
	normalized := []string{}
	for _, entry := range input.Env {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || strings.TrimSpace(key) == "" {
			return empty, fmt.Errorf("%w: environment entries require KEY=VALUE", errdefs.ErrConfig)
		}
		if old, exists := seen[key]; exists {
			if old != value {
				return empty, fmt.Errorf("%w: conflicting environment values for %s", errdefs.ErrConfig, key)
			}
			continue
		}
		seen[key] = value
		normalized = append(normalized, key+"="+value)
	}
	cellconfig.ApplyEnvOverrides(&doc, normalized)
	stack, err := c.GetStack(
		ctx,
		v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{Name: doc.Spec.StackID},
			Spec:     v1beta1.StackSpec{RealmID: doc.Spec.RealmID, SpaceID: doc.Spec.SpaceID},
		},
	)
	if err != nil {
		return empty, err
	}
	if !stack.MetadataExists {
		return empty, errdefs.ErrStackNotFound
	}
	existing, err := c.GetCell(ctx, doc)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return empty, err
	}
	if err == nil && existing.MetadataExists {
		return empty, fmt.Errorf(
			"%w: cell %q already exists in %s / %s / %s; preview again or choose another name",
			errdefs.ErrCellAlreadyExists, doc.Metadata.Name, doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID,
		)
	}
	return doc, nil
}

func defaultScope(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func allocateCellName(
	ctx context.Context,
	c kukeonv1.Client,
	explicit, prefix, realm, space, stack string,
) (string, error) {
	return naming.AllocCellName(explicit, prefix, func(name string) (bool, error) {
		result, err := c.GetCell(ctx, v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: name},
			Spec:     v1beta1.CellSpec{RealmID: realm, SpaceID: space, StackID: stack},
		})
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return result.MetadataExists, nil
	})
}

func byteBool(value bool) byte {
	if value {
		return 1
	}
	return 0
}
