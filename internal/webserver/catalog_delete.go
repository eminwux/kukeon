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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const maxCatalogDeleteBody = 64

// catalogDeleteResult is the delete route's response. Secret values are
// never part of it: only the coordinates the caller already sent come back.
type catalogDeleteResult struct {
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	Scope        [4]string `json:"scope"`
	Deleted      bool      `json:"deleted"`
	BackRefCells []string  `json:"backRefCells,omitempty"`
}

func registerCatalogDeleteRoutes(mux *http.ServeMux, dial Dial) {
	for _, kind := range []string{catalogBlueprints, catalogConfigs, catalogVolumes, catalogSecrets} {
		mux.HandleFunc("POST /api/catalog/"+kind+"/{name}/delete", catalogDeleteHandler(dial, kind))
	}
}

// catalogDeleteHandler mirrors `kuke delete config|blueprint|secret|volume`.
// The shared access policy guards Host/Origin; requiring a JSON content type
// keeps the route out of reach of plain cross-site form posts.
func catalogDeleteHandler(dial Dial, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := decodeCatalogDeleteRequest(r); err != nil {
			writeAPIError(w, err)
			return
		}
		scope, err := catalogScope(r, kind == catalogSecrets, true)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		name := r.PathValue("name")
		if !filepath.IsLocal(name) || !safePathSegment(name) {
			writeAPIError(w, fmt.Errorf("%w: unsafe resource name", errdefs.ErrConfig))
			return
		}
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, _ *http.Request) (any, error) {
			// The daemon reports a missing document as an untyped message, so
			// existence is checked first to answer a stale row with 404.
			if _, getErr := getCatalog(ctx, c, kind, scope, name); getErr != nil {
				return nil, getErr
			}
			return deleteCatalog(ctx, c, kind, scope, name)
		})(w, r)
	}
}

func decodeCatalogDeleteRequest(r *http.Request) error {
	if r.Header.Get("Content-Type") != jsonContentType {
		return fmt.Errorf("%w: JSON request required", errdefs.ErrConfig)
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCatalogDeleteBody))
	decoder.DisallowUnknownFields()
	var options struct{}
	if err := decoder.Decode(&options); err != nil && err != io.EOF {
		return fmt.Errorf("%w: delete requests accept only an empty JSON object", errdefs.ErrConfig)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
	}
	return nil
}

func deleteCatalog(ctx context.Context, c kukeonv1.Client, kind string, scope [4]string, name string) (any, error) {
	result := catalogDeleteResult{Kind: kind, Name: name, Scope: scope}
	switch kind {
	case catalogBlueprints:
		res, err := c.DeleteBlueprint(ctx, v1beta1.CellBlueprintDoc{
			Metadata: v1beta1.CellBlueprintMetadata{Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2]},
		})
		if err != nil {
			return nil, err
		}
		result.Deleted = res.Deleted
	case catalogConfigs:
		res, err := c.DeleteConfig(ctx, v1beta1.CellConfigDoc{
			Metadata: v1beta1.CellConfigMetadata{Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2]},
		})
		if err != nil {
			return nil, err
		}
		result.Deleted = res.Deleted
		result.BackRefCells = res.BackRefCells
	case catalogVolumes:
		res, err := c.DeleteVolume(ctx, v1beta1.VolumeDoc{
			Metadata: v1beta1.VolumeMetadata{Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2]},
		})
		if err != nil {
			return nil, err
		}
		result.Deleted = res.Deleted
	case catalogSecrets:
		res, err := c.DeleteSecret(ctx, v1beta1.SecretDoc{
			Metadata: v1beta1.SecretMetadata{
				Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2], Cell: scope[3],
			},
		})
		if err != nil {
			return nil, err
		}
		result.Deleted = res.Deleted
	default:
		return nil, errdefs.ErrConfig
	}
	return result, nil
}
