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

type cgroupRoute struct {
	pattern string
	kind    string
}

// cgroupRoutes maps each scoped resource route to the cgroup scope kind it
// reads (issue #1410). Each path is the resource's detail route plus /cgroup.
func cgroupRoutes() []cgroupRoute {
	return []cgroupRoute{
		{"GET /api/realms/{realm}/cgroup", kukeonv1.CgroupKindRealm},
		{"GET /api/realms/{realm}/spaces/{space}/cgroup", kukeonv1.CgroupKindSpace},
		{"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cgroup", kukeonv1.CgroupKindStack},
		{"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/cgroup", kukeonv1.CgroupKindCell},
		{
			"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{container}/cgroup",
			kukeonv1.CgroupKindContainer,
		},
	}
}

// registerCgroupRoutes proxies GetCgroupDetail. kuke serve never reads the
// cgroup filesystem itself; kukeond does, over its Unix-socket API.
func registerCgroupRoutes(mux *http.ServeMux, dial Dial) {
	for _, route := range cgroupRoutes() {
		kind := route.kind
		mux.HandleFunc(route.pattern, resourceRoute(dial,
			func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
				scope, err := cgroupScope(kind, r)
				if err != nil {
					return nil, err
				}
				return c.GetCgroupDetail(ctx, scope)
			}))
	}
}

func cgroupScope(kind string, r *http.Request) (kukeonv1.CgroupScope, error) {
	scope := kukeonv1.CgroupScope{
		Kind:      kind,
		Realm:     r.PathValue("realm"),
		Space:     r.PathValue("space"),
		Stack:     r.PathValue("stack"),
		Cell:      r.PathValue("cell"),
		Container: r.PathValue("container"),
	}
	for _, value := range []string{scope.Realm, scope.Space, scope.Stack, scope.Cell, scope.Container} {
		if !safePathSegment(value) {
			return scope, fmt.Errorf("%w: invalid cgroup scope", errdefs.ErrConfig)
		}
	}
	return scope, nil
}
