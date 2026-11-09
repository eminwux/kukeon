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
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const maxCreateBody = 64 << 10

type createConfigRequest struct {
	Config             v1beta1.CellConfigMetadata `json:"config"`
	Name               string                     `json:"name"`
	Env                []string                   `json:"env"`
	IgnoreDiskPressure bool                       `json:"ignoreDiskPressure"`
	Preview            string                     `json:"preview"`
}

func registerCreateRoutes(mux *http.ServeMux, dial Dial) {
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
					return nil, err
				}
				data, err := json.Marshal(doc)
				if err != nil {
					return nil, err
				}
				digest := fmt.Sprintf("%x", sha256.Sum256(append(data, byteBool(input.IgnoreDiskPressure))))
				if strings.HasSuffix(r.URL.Path, "/preview") {
					return map[string]any{"document": doc, "preview": digest}, nil
				}
				if input.Preview != digest {
					return nil, fmt.Errorf("%w: inputs or config changed; preview again", errdefs.ErrConfig)
				}
				doc.Spec.IgnoreDiskPressure = input.IgnoreDiskPressure
				if _, err = c.MaterializeCell(ctx, doc); err != nil {
					return nil, err
				}
				return doc, nil
			}),
		)
	}
}

func configCell(ctx context.Context, c kukeonv1.Client, input createConfigRequest) (v1beta1.CellDoc, error) {
	var empty v1beta1.CellDoc
	if strings.TrimSpace(input.Name) == "" {
		return empty, errdefs.ErrCellNameRequired
	}
	if !filepath.IsLocal(input.Name) || !safePathSegment(input.Name) {
		return empty, fmt.Errorf("%w: unsafe cell name", errdefs.ErrConfig)
	}
	if input.Config.Realm == "" || input.Config.Name == "" || (input.Config.Stack != "" && input.Config.Space == "") {
		return empty, fmt.Errorf("%w: select a scoped config", errdefs.ErrConfig)
	}
	if !filepath.IsLocal(input.Config.Name) || !safePathSegment(input.Config.Name) ||
		!filepath.IsLocal(input.Config.Realm) || !safePathSegment(input.Config.Realm) ||
		(input.Config.Space != "" && (!filepath.IsLocal(input.Config.Space) || !safePathSegment(input.Config.Space))) ||
		(input.Config.Stack != "" && (!filepath.IsLocal(input.Config.Stack) || !safePathSegment(input.Config.Stack))) {
		return empty, fmt.Errorf("%w: unsafe config coordinate", errdefs.ErrConfig)
	}
	cfg, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: input.Config})
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
	doc, err := cellconfig.MaterializeWithName(cfg.Config, bp.Blueprint, input.Name)
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
		return empty, fmt.Errorf("%w: cell already exists; choose another name", errdefs.ErrConfig)
	}
	return doc, nil
}

func byteBool(value bool) byte {
	if value {
		return 1
	}
	return 0
}
