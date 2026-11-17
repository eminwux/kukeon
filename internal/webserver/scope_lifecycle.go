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

package webserver

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// parentScope names a realm, space or stack; unused trailing fields are empty.
type parentScope struct {
	realm string
	space string
	stack string
}

// contains reports whether other is s or lies beneath it.
func (s parentScope) contains(other parentScope) bool {
	if s.realm != other.realm {
		return false
	}
	if s.space == "" {
		return true
	}
	if s.space != other.space {
		return false
	}
	return s.stack == "" || s.stack == other.stack
}

// parentOperationLocks rejects an action whose scope overlaps an active one,
// so a realm purge cannot race a delete of one of its spaces.
type parentOperationLocks struct {
	mu     sync.Mutex
	active map[parentScope]struct{}
}

func (l *parentOperationLocks) tryAcquire(scope parentScope) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil {
		l.active = make(map[parentScope]struct{})
	}
	for active := range l.active {
		if active.contains(scope) || scope.contains(active) {
			return nil, false
		}
	}
	l.active[scope] = struct{}{}
	return func() {
		l.mu.Lock()
		delete(l.active, scope)
		l.mu.Unlock()
	}, true
}

type parentAction func(ctx context.Context, client kukeonv1.Client, scope parentScope, force, cascade bool) (map[string]any, error)

func registerParentLifecycleRoutes(mux *http.ServeMux, dial Dial) {
	locks := &parentOperationLocks{}
	routes := []struct {
		pattern string
		depth   int
		action  string
		call    parentAction
	}{
		{"POST /api/realms/{realm}/delete", 1, "delete", deleteRealmAction},
		{"POST /api/realms/{realm}/purge", 1, "purge", purgeRealmAction},
		{"POST /api/realms/{realm}/spaces/{space}/delete", 2, "delete", deleteSpaceAction},
		{"POST /api/realms/{realm}/spaces/{space}/purge", 2, "purge", purgeSpaceAction},
		{"POST /api/realms/{realm}/spaces/{space}/stacks/{stack}/delete", 3, "delete", deleteStackAction},
		{"POST /api/realms/{realm}/spaces/{space}/stacks/{stack}/purge", 3, "purge", purgeStackAction},
	}
	for _, route := range routes {
		mux.HandleFunc(route.pattern, parentLifecycleRoute(dial, locks, route.depth, route.action, route.call))
	}
}

func parentLifecycleRoute(
	dial Dial, locks *parentOperationLocks, depth int, action string, call parentAction,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		force, cascade, err := decodePurgeCellOptions(w, r)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		scope, err := requestParentScope(r, depth)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		release, acquired := locks.tryAcquire(scope)
		if !acquired {
			writeAPIError(w, errdefs.ErrCellOperationInProgress)
			return
		}
		defer release()

		ctx, cancel := context.WithTimeout(r.Context(), lifecycleTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()

		response, err := call(ctx, client, scope, force, cascade)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		response["action"] = action
		response["force"] = force
		response["cascade"] = cascade
		writeJSON(w, http.StatusOK, response)
	}
}

// requestParentScope reads the first depth scope segments (realm, space,
// stack) from the matched route.
func requestParentScope(r *http.Request, depth int) (parentScope, error) {
	scope := parentScope{realm: r.PathValue("realm"), space: r.PathValue("space"), stack: r.PathValue("stack")}
	values := []string{scope.realm, scope.space, scope.stack}[:depth]
	for _, value := range values {
		if value == "" || !filepath.IsLocal(value) || !safePathSegment(value) {
			return scope, fmt.Errorf("%w: invalid scope", errdefs.ErrConfig)
		}
	}
	return scope, nil
}

// The documents mirror what `kuke delete|purge realm|space|stack` sends.
func realmDoc(scope parentScope) v1beta1.RealmDoc {
	return v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: scope.realm}}
}

func spaceDoc(scope parentScope) v1beta1.SpaceDoc {
	return v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{Name: scope.space},
		Spec:     v1beta1.SpaceSpec{RealmID: scope.realm},
	}
}

func stackDoc(scope parentScope) v1beta1.StackDoc {
	return v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{Name: scope.stack},
		Spec:     v1beta1.StackSpec{RealmID: scope.realm, SpaceID: scope.space},
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func deleteRealmAction(
	ctx context.Context, client kukeonv1.Client, scope parentScope, force, cascade bool,
) (map[string]any, error) {
	result, err := client.DeleteRealm(ctx, realmDoc(scope), force, cascade)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"realm":                      redactRealm(result.Realm),
		"deleted":                    nonNil(result.Deleted),
		"metadataDeleted":            result.MetadataDeleted,
		"cgroupDeleted":              result.CgroupDeleted,
		"containerdNamespaceDeleted": result.ContainerdNamespaceDeleted,
	}, nil
}

func purgeRealmAction(
	ctx context.Context, client kukeonv1.Client, scope parentScope, force, cascade bool,
) (map[string]any, error) {
	result, err := client.PurgeRealm(ctx, realmDoc(scope), force, cascade)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"realm":          redactRealm(result.Realm),
		"realmDeleted":   result.RealmDeleted,
		"purgeSucceeded": result.PurgeSucceeded,
		"deleted":        nonNil(result.Deleted),
		"purged":         nonNil(result.Purged),
	}, nil
}

func deleteSpaceAction(
	ctx context.Context, client kukeonv1.Client, scope parentScope, force, cascade bool,
) (map[string]any, error) {
	result, err := client.DeleteSpace(ctx, spaceDoc(scope), force, cascade)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"space":             result.Space,
		"deleted":           nonNil(result.Deleted),
		"metadataDeleted":   result.MetadataDeleted,
		"cgroupDeleted":     result.CgroupDeleted,
		"cniNetworkDeleted": result.CNINetworkDeleted,
	}, nil
}

func purgeSpaceAction(
	ctx context.Context, client kukeonv1.Client, scope parentScope, force, cascade bool,
) (map[string]any, error) {
	result, err := client.PurgeSpace(ctx, spaceDoc(scope), force, cascade)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"space":             result.Space,
		"metadataDeleted":   result.MetadataDeleted,
		"cgroupDeleted":     result.CgroupDeleted,
		"cniNetworkDeleted": result.CNINetworkDeleted,
		"purgeSucceeded":    result.PurgeSucceeded,
		"deleted":           nonNil(result.Deleted),
		"purged":            nonNil(result.Purged),
	}, nil
}

func deleteStackAction(
	ctx context.Context, client kukeonv1.Client, scope parentScope, force, cascade bool,
) (map[string]any, error) {
	result, err := client.DeleteStack(ctx, stackDoc(scope), force, cascade)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"stack":           result.Stack,
		"deleted":         nonNil(result.Deleted),
		"metadataDeleted": result.MetadataDeleted,
		"cgroupDeleted":   result.CgroupDeleted,
	}, nil
}

func purgeStackAction(
	ctx context.Context, client kukeonv1.Client, scope parentScope, force, cascade bool,
) (map[string]any, error) {
	result, err := client.PurgeStack(ctx, stackDoc(scope), force, cascade)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"stack":   result.Stack,
		"deleted": nonNil(result.Deleted),
		"purged":  nonNil(result.Purged),
	}, nil
}
