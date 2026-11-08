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
	"net/http"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// resourceRoute owns the connection and deadline for one scoped read.
func resourceRoute(
	dial Dial,
	read func(context.Context, kukeonv1.Client, *http.Request) (any, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		timeout := requestTimeout
		if r.Method == http.MethodPost {
			timeout = creationTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()
		result, err := read(ctx, client, r)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func registerResourceRoutes(mux *http.ServeMux, dial Dial) {
	registerCatalogRoutes(mux, dial)
	registerCreateRoutes(mux, dial)
	registerSpaceRoutes(mux, dial)
	registerStackRoutes(mux, dial)
	registerCellRoutes(mux, dial)
	registerContainerRoutes(mux, dial)
}

func registerSpaceRoutes(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			docs, err := c.ListSpaces(ctx, r.PathValue("realm"))
			if docs == nil {
				docs = []v1beta1.SpaceDoc{}
			}
			return docs, err
		}),
	)
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{name}",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			result, err := c.GetSpace(ctx, v1beta1.SpaceDoc{
				Metadata: v1beta1.SpaceMetadata{Name: r.PathValue("name")},
				Spec:     v1beta1.SpaceSpec{RealmID: r.PathValue("realm")},
			})
			if err != nil {
				return nil, err
			}
			if !result.MetadataExists {
				return nil, errdefs.ErrSpaceNotFound
			}
			return result.Space, nil
		}),
	)
}

func registerStackRoutes(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			docs, err := c.ListStacks(ctx, r.PathValue("realm"), r.PathValue("space"))
			if docs == nil {
				docs = []v1beta1.StackDoc{}
			}
			return docs, err
		}),
	)
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{name}",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			result, err := c.GetStack(ctx, v1beta1.StackDoc{
				Metadata: v1beta1.StackMetadata{Name: r.PathValue("name")},
				Spec:     v1beta1.StackSpec{RealmID: r.PathValue("realm"), SpaceID: r.PathValue("space")},
			})
			if err != nil {
				return nil, err
			}
			if !result.MetadataExists {
				return nil, errdefs.ErrStackNotFound
			}
			return result.Stack, nil
		}),
	)
}

func registerCellRoutes(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			docs, err := c.ListCells(ctx, r.PathValue("realm"), r.PathValue("space"), r.PathValue("stack"))
			if docs == nil {
				docs = []v1beta1.CellDoc{}
			}
			for i := range docs {
				docs[i] = redactCell(docs[i])
			}
			return docs, err
		}),
	)
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{name}",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			result, err := c.GetCell(ctx, v1beta1.CellDoc{
				Metadata: v1beta1.CellMetadata{Name: r.PathValue("name")},
				Spec: v1beta1.CellSpec{
					RealmID: r.PathValue("realm"),
					SpaceID: r.PathValue("space"),
					StackID: r.PathValue("stack"),
				},
			})
			if err != nil {
				return nil, err
			}
			if !result.MetadataExists {
				return nil, errdefs.ErrCellNotFound
			}
			return redactCell(result.Cell), nil
		}),
	)
}

func registerContainerRoutes(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			docs, err := c.ListContainers(
				ctx,
				r.PathValue("realm"),
				r.PathValue("space"),
				r.PathValue("stack"),
				r.PathValue("cell"),
			)
			if docs == nil {
				docs = []v1beta1.ContainerSpec{}
			}
			docs = append([]v1beta1.ContainerSpec{}, docs...)
			for i := range docs {
				docs[i].Env = nil
			}
			return docs, err
		}),
	)
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{name}",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			result, err := c.GetContainer(ctx, v1beta1.ContainerDoc{
				Metadata: v1beta1.ContainerMetadata{Name: r.PathValue("name")},
				Spec: v1beta1.ContainerSpec{
					RealmID: r.PathValue("realm"),
					SpaceID: r.PathValue("space"),
					StackID: r.PathValue("stack"),
					CellID:  r.PathValue("cell"),
				},
			})
			if err != nil {
				return nil, err
			}
			if !result.CellMetadataExists {
				return nil, errdefs.ErrCellNotFound
			}
			if !result.ContainerExists {
				return nil, errdefs.ErrContainerNotFound
			}
			result.Container.Spec.Env = nil
			return result.Container, nil
		}),
	)
}

// redactCell removes environment and materialization inputs from browser responses.
func redactCell(doc v1beta1.CellDoc) v1beta1.CellDoc {
	doc.Spec.Containers = append([]v1beta1.ContainerSpec(nil), doc.Spec.Containers...)
	for i := range doc.Spec.Containers {
		doc.Spec.Containers[i].Env = nil
	}
	doc.Spec.RuntimeEnv = nil
	if doc.Spec.Provenance != nil {
		provenance := *doc.Spec.Provenance
		provenance.Params = nil
		provenance.EnvOverrides = nil
		doc.Spec.Provenance = &provenance
	}
	return doc
}
