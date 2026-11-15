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

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// networkRoutes maps each scoped resource route to the network scope kind it
// reads (issue #1411). Each path is the resource's detail route plus
// /network. Realms and stacks have no networking fields.
func networkRoutes() []cgroupRoute {
	return []cgroupRoute{
		{"GET /api/realms/{realm}/spaces/{space}/network", kukeonv1.NetworkKindSpace},
		{"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/network", kukeonv1.NetworkKindCell},
		{
			"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{container}/network",
			kukeonv1.NetworkKindContainer,
		},
	}
}

// registerNetworkRoutes proxies GetNetworkDetail. kuke serve never reads CNI
// conflists, allocator state or the CNI cache itself; kukeond does, over its
// Unix-socket API.
func registerNetworkRoutes(mux *http.ServeMux, dial Dial) {
	for _, route := range networkRoutes() {
		kind := route.kind
		mux.HandleFunc(route.pattern, resourceRoute(dial,
			func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
				scope, err := networkScope(kind, r)
				if err != nil {
					return nil, err
				}
				return c.GetNetworkDetail(ctx, scope)
			}))
	}
}

func networkScope(kind string, r *http.Request) (kukeonv1.NetworkScope, error) {
	scope := kukeonv1.NetworkScope{
		Kind:      kind,
		Realm:     r.PathValue("realm"),
		Space:     r.PathValue("space"),
		Stack:     r.PathValue("stack"),
		Cell:      r.PathValue("cell"),
		Container: r.PathValue("container"),
	}
	for _, value := range []string{scope.Realm, scope.Space, scope.Stack, scope.Cell, scope.Container} {
		if !safePathSegment(value) {
			return scope, fmt.Errorf("%w: invalid network scope", errdefs.ErrConfig)
		}
	}
	return scope, nil
}
