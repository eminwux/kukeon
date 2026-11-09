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
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	catalogBlueprints = "blueprints"
	catalogConfigs    = "configs"
	catalogVolumes    = "volumes"
	catalogSecrets    = "secrets"
)

// catalogScope validates coordinates before forwarding them to the daemon.
func catalogScope(r *http.Request, secret bool, detail bool) ([4]string, error) {
	scope := [4]string{
		r.URL.Query().Get("realm"),
		r.URL.Query().Get("space"),
		r.URL.Query().Get("stack"),
		r.URL.Query().Get("cell"),
	}
	for i := range scope {
		scope[i] = strings.TrimSpace(scope[i])
		if scope[i] != "" && (!filepath.IsLocal(scope[i]) || !safePathSegment(scope[i])) {
			return scope, fmt.Errorf("%w: unsafe scope coordinate", errdefs.ErrConfig)
		}
	}
	if detail && scope[0] == "" {
		return scope, errdefs.ErrRealmNameRequired
	}
	if !secret && scope[3] != "" {
		return scope, fmt.Errorf("%w: this resource cannot be cell-scoped", errdefs.ErrConfig)
	}
	for i := 1; i < len(scope); i++ {
		if scope[i] != "" && scope[i-1] == "" {
			return scope, fmt.Errorf("%w: scope coordinates cannot contain gaps", errdefs.ErrConfig)
		}
	}
	return scope, nil
}

func registerCatalogRoutes(mux *http.ServeMux, dial Dial) {
	for _, kind := range []string{catalogBlueprints, catalogConfigs, catalogVolumes, catalogSecrets} {
		mux.HandleFunc("GET /api/catalog/"+kind, catalogHandler(dial, kind, false))
		mux.HandleFunc("GET /api/catalog/"+kind+"/{name}", catalogHandler(dial, kind, true))
	}
}

func catalogHandler(dial Dial, kind string, detail bool) http.HandlerFunc {
	return resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
		scope, err := catalogScope(r, kind == catalogSecrets, detail)
		if err != nil {
			return nil, err
		}
		if detail {
			name := r.PathValue("name")
			if !filepath.IsLocal(name) || !safePathSegment(name) {
				return nil, fmt.Errorf("%w: unsafe resource name", errdefs.ErrConfig)
			}
			return getCatalog(ctx, c, kind, scope, name)
		}
		return listCatalog(ctx, c, kind, scope)
	})
}

func catalogList[T any](docs []T, err error) (any, error) {
	if docs == nil {
		docs = []T{}
	}
	return docs, err
}

func catalogResult[T any](doc T, exists bool, err error, notFound error) (any, error) {
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, notFound
	}
	return doc, nil
}

func listCatalog(ctx context.Context, c kukeonv1.Client, kind string, scope [4]string) (any, error) {
	switch kind {
	case catalogBlueprints:
		return catalogList(c.ListBlueprints(ctx, scope[0], scope[1], scope[2]))
	case catalogConfigs:
		return catalogList(c.ListConfigs(ctx, scope[0], scope[1], scope[2]))
	case catalogVolumes:
		return catalogList(c.ListVolumes(ctx, scope[0], scope[1], scope[2]))
	case catalogSecrets:
		docs, err := c.ListSecrets(ctx, scope[0], scope[1], scope[2], scope[3])
		for i := range docs {
			docs[i].Spec.Data = ""
		}
		return catalogList(docs, err)
	default:
		return nil, errdefs.ErrConfig
	}
}

func getCatalog(ctx context.Context, c kukeonv1.Client, kind string, scope [4]string, name string) (any, error) {
	switch kind {
	case catalogBlueprints:
		result, err := c.GetBlueprint(
			ctx,
			v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2]},
			},
		)
		return catalogResult(result.Blueprint, result.MetadataExists, err, errdefs.ErrBlueprintNotFound)
	case catalogConfigs:
		result, err := c.GetConfig(
			ctx,
			v1beta1.CellConfigDoc{
				Metadata: v1beta1.CellConfigMetadata{Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2]},
			},
		)
		return catalogResult(result.Config, result.MetadataExists, err, errdefs.ErrConfigNotFound)
	case catalogVolumes:
		result, err := c.GetVolume(
			ctx,
			v1beta1.VolumeDoc{
				Metadata: v1beta1.VolumeMetadata{Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2]},
			},
		)
		return catalogResult(result.Volume, result.MetadataExists, err, errdefs.ErrVolumeNotFound)
	case catalogSecrets:
		result, err := c.GetSecret(
			ctx,
			v1beta1.SecretDoc{
				Metadata: v1beta1.SecretMetadata{
					Name:  name,
					Realm: scope[0],
					Space: scope[1],
					Stack: scope[2],
					Cell:  scope[3],
				},
			},
		)
		result.Secret.Spec.Data = ""
		return catalogResult(result.Secret, result.MetadataExists, err, errdefs.ErrSecretNotFound)
	default:
		return nil, errdefs.ErrConfig
	}
}
