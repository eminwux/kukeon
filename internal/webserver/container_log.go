// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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
	"strconv"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// containerLogChunk is the JSON rendering of kukeonv1.ContainerLogResult.
// Data is raw stream bytes; encoding/json renders []byte as base64, so a
// terminal capture reaches the browser verbatim (escape sequences and
// non-UTF-8 bytes included).
type containerLogChunk struct {
	Source    string `json:"source"`
	Data      []byte `json:"data"`
	Start     int64  `json:"start"`
	Next      int64  `json:"next"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
	More      bool   `json:"more"`
	Stream    string `json:"stream"`
}

// registerContainerLogRoute exposes ReadContainerLog (issue #1620) so the
// cell terminal can show a container's output when no live terminal is
// available: a non-attachable container, or any container of a dead cell.
func registerContainerLogRoute(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{name}/log",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			req, err := containerLogRequest(r)
			if err != nil {
				return nil, err
			}
			name := r.PathValue("name")
			result, err := c.ReadContainerLog(ctx, v1beta1.ContainerDoc{
				Metadata: v1beta1.ContainerMetadata{Name: name},
				Spec: v1beta1.ContainerSpec{
					ID:      name,
					RealmID: r.PathValue("realm"),
					SpaceID: r.PathValue("space"),
					StackID: r.PathValue("stack"),
					CellID:  r.PathValue("cell"),
				},
			}, req)
			if err != nil {
				return nil, err
			}
			return containerLogChunk(result), nil
		}),
	)
}

func containerLogRequest(r *http.Request) (kukeonv1.ContainerLogRequest, error) {
	req := kukeonv1.ContainerLogRequest{Offset: -1, MaxBytes: kukeonv1.ContainerLogDefaultMaxBytes}
	query := r.URL.Query()
	if raw := query.Get("offset"); raw != "" {
		offset, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || offset < -1 {
			return req, fmt.Errorf("%w: offset must be -1 (tail) or a byte offset", errdefs.ErrConfig)
		}
		req.Offset = offset
	}
	if raw := query.Get("maxBytes"); raw != "" {
		maxBytes, err := strconv.Atoi(raw)
		if err != nil || maxBytes < 1 || maxBytes > kukeonv1.ContainerLogMaxBytes {
			return req, fmt.Errorf(
				"%w: maxBytes must be between 1 and %d", errdefs.ErrConfig, kukeonv1.ContainerLogMaxBytes,
			)
		}
		req.MaxBytes = maxBytes
	}
	// stream=supervisor reads the terminal supervisor's log (issue #1721).
	switch raw := query.Get("stream"); raw {
	case "", kukeonv1.ContainerLogStreamTerminal, kukeonv1.ContainerLogStreamSupervisor:
		req.Stream = raw
	default:
		return req, fmt.Errorf(
			"%w: stream must be %q or %q", errdefs.ErrConfig,
			kukeonv1.ContainerLogStreamTerminal, kukeonv1.ContainerLogStreamSupervisor,
		)
	}
	return req, nil
}
