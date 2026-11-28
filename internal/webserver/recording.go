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
	"strconv"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// containerRecording is the JSON rendering of kukeonv1.ContainerRecording.
type containerRecording struct {
	Format   string             `json:"format"`
	Segments []recordingSegment `json:"segments"`
}

type recordingSegment struct {
	Seq        uint64 `json:"seq"`
	Size       int64  `json:"size"`
	Compressed bool   `json:"compressed"`
	Live       bool   `json:"live"`
}

// recordingSegmentChunk is the JSON rendering of
// kukeonv1.RecordingSegmentResult. Data is the segment's stored bytes, gzip
// for a compressed segment; encoding/json renders []byte as base64.
type recordingSegmentChunk struct {
	Data       []byte `json:"data"`
	Start      int64  `json:"start"`
	Next       int64  `json:"next"`
	Size       int64  `json:"size"`
	More       bool   `json:"more"`
	Compressed bool   `json:"compressed"`
	Live       bool   `json:"live"`
}

// registerRecordingRoutes exposes a container's whole terminal recording
// (issue #1783) for the cell terminal's Replay view: the list of retained
// segments, and one segment's bytes as stored. Both go through kukeond, so
// the web server reads no host file and the routes work through
// `kuke serve --host`.
func registerRecordingRoutes(mux *http.ServeMux, dial Dial) {
	const base = "GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{name}/recording"
	mux.HandleFunc(base,
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			result, err := c.ListContainerRecording(ctx, recordingContainerDoc(r))
			if err != nil {
				return nil, err
			}
			out := containerRecording{
				Format:   result.Format,
				Segments: make([]recordingSegment, 0, len(result.Segments)),
			}
			for _, s := range result.Segments {
				out.Segments = append(out.Segments, recordingSegment(s))
			}
			return out, nil
		}),
	)
	mux.HandleFunc(base+"/segments/{seq}",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			req, err := recordingSegmentRequest(r)
			if err != nil {
				return nil, err
			}
			result, err := c.ReadContainerRecordingSegment(ctx, recordingContainerDoc(r), req)
			if err != nil {
				return nil, err
			}
			return recordingSegmentChunk(result), nil
		}),
	)
}

func recordingContainerDoc(r *http.Request) v1beta1.ContainerDoc {
	name := r.PathValue("name")
	return v1beta1.ContainerDoc{
		Metadata: v1beta1.ContainerMetadata{Name: name},
		Spec: v1beta1.ContainerSpec{
			ID:      name,
			RealmID: r.PathValue("realm"),
			SpaceID: r.PathValue("space"),
			StackID: r.PathValue("stack"),
			CellID:  r.PathValue("cell"),
		},
	}
}

// recordingSegmentRequest parses {seq}, offset (default 0) and maxBytes (the
// log route's default and bounds).
func recordingSegmentRequest(r *http.Request) (kukeonv1.RecordingSegmentRequest, error) {
	req := kukeonv1.RecordingSegmentRequest{MaxBytes: kukeonv1.ContainerLogDefaultMaxBytes}
	seq, err := strconv.ParseUint(r.PathValue("seq"), 10, 64)
	if err != nil {
		return req, fmt.Errorf("%w: seq must be an unsigned integer", errdefs.ErrConfig)
	}
	req.Seq = seq
	query := r.URL.Query()
	if raw := query.Get("offset"); raw != "" {
		offset, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || offset < 0 {
			return req, fmt.Errorf("%w: offset must be a byte offset", errdefs.ErrConfig)
		}
		req.Offset = offset
	}
	if raw := query.Get("maxBytes"); raw != "" {
		maxBytes, perr := strconv.Atoi(raw)
		if perr != nil || maxBytes < 1 || maxBytes > kukeonv1.ContainerLogMaxBytes {
			return req, fmt.Errorf(
				"%w: maxBytes must be between 1 and %d", errdefs.ErrConfig, kukeonv1.ContainerLogMaxBytes,
			)
		}
		req.MaxBytes = maxBytes
	}
	return req, nil
}
