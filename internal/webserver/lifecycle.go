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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/eminwux/kukeon/internal/celllifecycle"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type cellScope struct {
	realm string
	space string
	stack string
	cell  string
}

type cellOperationLocks struct {
	mu     sync.Mutex
	active map[cellScope]struct{}
}

func (l *cellOperationLocks) tryAcquire(scope cellScope) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil {
		l.active = make(map[cellScope]struct{})
	}
	if _, exists := l.active[scope]; exists {
		return nil, false
	}
	l.active[scope] = struct{}{}
	return func() {
		l.mu.Lock()
		delete(l.active, scope)
		l.mu.Unlock()
	}, true
}

func registerCellLifecycleRoutes(mux *http.ServeMux, dial Dial) {
	locks := &cellOperationLocks{}
	for _, action := range []string{"start", "stop", "restart", "kill", "delete", "purge"} {
		pattern := "POST /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/" + action
		mux.HandleFunc(pattern, cellLifecycleRoute(dial, locks, action))
	}
}

func cellLifecycleRoute(dial Dial, locks *cellOperationLocks, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		force, cascade := false, false
		if action == "purge" {
			var err error
			force, cascade, err = decodePurgeCellOptions(w, r)
			if err != nil {
				writeAPIError(w, err)
				return
			}
		} else if r.ContentLength != 0 {
			writeAPIError(w, fmt.Errorf("%w: lifecycle requests do not accept a body", errdefs.ErrConfig))
			return
		}
		doc, scope, err := lifecycleCellDoc(r)
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

		var cell v1beta1.CellDoc
		response := map[string]any{"action": action}
		switch action {
		case "start":
			result, callErr := client.StartCell(ctx, doc)
			if callErr != nil {
				writeAPIError(w, callErr)
				return
			}
			cell = result.Cell
		case "kill":
			result, callErr := client.KillCell(ctx, doc)
			if callErr != nil {
				writeAPIError(w, callErr)
				return
			}
			cell = result.Cell
			response["killed"] = result.Killed
		case "delete":
			result, callErr := client.DeleteCell(ctx, doc)
			if callErr != nil {
				writeAPIError(w, callErr)
				return
			}
			cell = result.Cell
			response["containersDeleted"] = result.ContainersDeleted
			response["cgroupDeleted"] = result.CgroupDeleted
			response["metadataDeleted"] = result.MetadataDeleted
		case "purge":
			result, callErr := client.PurgeCell(ctx, doc, force, cascade)
			if callErr != nil {
				writeAPIError(w, callErr)
				return
			}
			cell = result.Cell
			response["containersDeleted"] = result.ContainersDeleted
			response["cgroupDeleted"] = result.CgroupDeleted
			response["metadataDeleted"] = result.MetadataDeleted
			response["purgeSucceeded"] = result.PurgeSucceeded
			response["force"] = result.Force
			response["cascade"] = result.Cascade
			response["deleted"] = result.Deleted
			response["purged"] = result.Purged
		case "stop":
			result, callErr := client.StopCell(ctx, doc)
			if callErr != nil {
				writeAPIError(w, callErr)
				return
			}
			cell = result.Cell
		case "restart":
			result, callErr := celllifecycle.Restart(ctx, client, doc)
			if callErr != nil {
				writeAPIError(w, callErr)
				return
			}
			cell = result.Cell
		default:
			writeAPIError(w, fmt.Errorf("%w: unknown lifecycle operation", errdefs.ErrConfig))
			return
		}
		if cell.Metadata.Name == "" {
			cell = doc
		}
		response["cell"] = redactCell(cell)
		writeJSON(w, http.StatusOK, response)
	}
}

type purgeCellOptions struct {
	Force   bool `json:"force"`
	Cascade bool `json:"cascade"`
}

func decodePurgeCellOptions(w http.ResponseWriter, r *http.Request) (bool, bool, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return false, false, nil
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return false, false, fmt.Errorf("%w: purge options must be application/json", errdefs.ErrConfig)
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var options purgeCellOptions
	if err := decoder.Decode(&options); err != nil {
		return false, false, fmt.Errorf("%w: invalid purge options: %v", errdefs.ErrConfig, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return false, false, fmt.Errorf("%w: purge options must contain one JSON object", errdefs.ErrConfig)
		}
		return false, false, fmt.Errorf("%w: invalid purge options: %v", errdefs.ErrConfig, err)
	}
	return options.Force, options.Cascade, nil
}

func lifecycleCellDoc(r *http.Request) (v1beta1.CellDoc, cellScope, error) {
	scope := cellScope{
		realm: r.PathValue("realm"),
		space: r.PathValue("space"),
		stack: r.PathValue("stack"),
		cell:  r.PathValue("cell"),
	}
	for _, value := range []string{scope.realm, scope.space, scope.stack, scope.cell} {
		if value == "" || !filepath.IsLocal(value) || !safePathSegment(value) {
			return v1beta1.CellDoc{}, scope, fmt.Errorf("%w: invalid cell scope", errdefs.ErrConfig)
		}
	}
	doc := v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata:   v1beta1.CellMetadata{Name: scope.cell, Labels: map[string]string{}},
		Spec: v1beta1.CellSpec{
			ID:      scope.cell,
			RealmID: scope.realm,
			SpaceID: scope.space,
			StackID: scope.stack,
		},
	}
	return doc, scope, nil
}

func lifecycleErrorStatus(err error) (int, string) {
	if errors.Is(err, errdefs.ErrInvalidCellState) {
		return http.StatusConflict, "invalid_state"
	}
	if errors.Is(err, errdefs.ErrCellOperationInProgress) {
		return http.StatusConflict, "operation_in_progress"
	}
	return apiErrorStatus(err)
}
