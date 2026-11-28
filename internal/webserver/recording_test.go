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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const recordingURL = "/api/realms/dev/spaces/shared/stacks/services/cells/api/containers/web/recording"

type recordingClient struct {
	kukeonv1.FakeClient

	listed  bool
	read    bool
	doc     v1beta1.ContainerDoc
	req     kukeonv1.RecordingSegmentRequest
	list    kukeonv1.ContainerRecording
	segment kukeonv1.RecordingSegmentResult
	err     error
}

func (c *recordingClient) Close() error { return nil }

func (c *recordingClient) ListContainerRecording(
	_ context.Context, doc v1beta1.ContainerDoc,
) (kukeonv1.ContainerRecording, error) {
	c.listed, c.doc = true, doc
	return c.list, c.err
}

func (c *recordingClient) ReadContainerRecordingSegment(
	_ context.Context, doc v1beta1.ContainerDoc, req kukeonv1.RecordingSegmentRequest,
) (kukeonv1.RecordingSegmentResult, error) {
	c.read, c.doc, c.req = true, doc, req
	return c.segment, c.err
}

func recordingDial(c *recordingClient) Dial {
	return func(context.Context) (kukeonv1.Client, error) { return c, nil }
}

func checkRecordingDoc(t *testing.T, doc v1beta1.ContainerDoc) {
	t.Helper()
	if doc.Metadata.Name != "web" || doc.Spec.ID != "web" || doc.Spec.RealmID != "dev" ||
		doc.Spec.SpaceID != "shared" || doc.Spec.StackID != "services" || doc.Spec.CellID != "api" {
		t.Fatalf("doc %+v", doc)
	}
}

func TestRecordingListsSegments(t *testing.T) {
	c := &recordingClient{list: kukeonv1.ContainerRecording{
		Format: kukeonv1.RecordingFormatAsciicast,
		Segments: []kukeonv1.RecordingSegment{
			{Seq: 3, Size: 600000, Compressed: true},
			{Seq: 4, Size: 1200, Live: true},
		},
	}}
	w := serveDaemon(t, recordingDial(c), recordingURL)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	checkRecordingDoc(t, c.doc)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := `{"format":"asciicast","segments":[` +
		`{"compressed":true,"live":false,"seq":3,"size":600000},` +
		`{"compressed":false,"live":true,"seq":4,"size":1200}]}`
	if got, _ := json.Marshal(body); string(got) != want {
		t.Fatalf("body %s, want %s", got, want)
	}
}

func TestRecordingEmptyListIsAnArray(t *testing.T) {
	c := &recordingClient{list: kukeonv1.ContainerRecording{Format: kukeonv1.RecordingFormatRaw}}
	w := serveDaemon(t, recordingDial(c), recordingURL)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"segments":[]`)) ||
		!bytes.Contains(w.Body.Bytes(), []byte(`"format":"raw"`)) {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
}

func TestRecordingReadsSegmentAsStored(t *testing.T) {
	stored := []byte{0x1f, 0x8b, 0x08, 0x00, 0xff}
	c := &recordingClient{segment: kukeonv1.RecordingSegmentResult{
		Data: stored, Start: 10, Next: 15, Size: 40, More: true, Compressed: true,
	}}
	w := serveDaemon(t, recordingDial(c), recordingURL+"/segments/3?offset=10&maxBytes=5")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	checkRecordingDoc(t, c.doc)
	if c.req != (kukeonv1.RecordingSegmentRequest{Seq: 3, Offset: 10, MaxBytes: 5}) {
		t.Fatalf("req %+v", c.req)
	}
	var chunk struct {
		Data       string `json:"data"`
		Start      int64  `json:"start"`
		Next       int64  `json:"next"`
		Size       int64  `json:"size"`
		More       bool   `json:"more"`
		Compressed bool   `json:"compressed"`
		Live       *bool  `json:"live"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &chunk); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(chunk.Data)
	if err != nil || !bytes.Equal(decoded, stored) {
		t.Fatalf("data %q (%v)", chunk.Data, err)
	}
	if chunk.Start != 10 || chunk.Next != 15 || chunk.Size != 40 || !chunk.More || !chunk.Compressed ||
		chunk.Live == nil || *chunk.Live {
		t.Fatalf("chunk %+v", chunk)
	}
}

func TestRecordingSegmentDefaults(t *testing.T) {
	c := &recordingClient{}
	w := serveDaemon(t, recordingDial(c), recordingURL+"/segments/0")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if c.req != (kukeonv1.RecordingSegmentRequest{MaxBytes: kukeonv1.ContainerLogDefaultMaxBytes}) {
		t.Fatalf("default req %+v", c.req)
	}
}

func TestRecordingSegmentRejectsInvalidRequests(t *testing.T) {
	for _, path := range []string{
		"/segments/x", "/segments/-1", "/segments/1.5",
		"/segments/0?offset=-1", "/segments/0?offset=x",
		"/segments/0?maxBytes=0", "/segments/0?maxBytes=x",
		fmt.Sprintf("/segments/0?maxBytes=%d", kukeonv1.ContainerLogMaxBytes+1),
	} {
		c := &recordingClient{}
		w := serveDaemon(t, recordingDial(c), recordingURL+path)
		body := decodeBody[struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}](t, w)
		if w.Code != http.StatusBadRequest || body.Error.Code != "invalid_request" {
			t.Fatalf("%s: status %d body %s", path, w.Code, w.Body)
		}
		if c.read {
			t.Fatalf("%s: daemon was called with %+v", path, c.req)
		}
	}
	c := &recordingClient{}
	_ = serveDaemon(
		t,
		recordingDial(c),
		recordingURL+fmt.Sprintf("/segments/0?maxBytes=%d", kukeonv1.ContainerLogMaxBytes),
	)
	if !c.read || c.req.MaxBytes != kukeonv1.ContainerLogMaxBytes {
		t.Fatalf("maxBytes at the bound: read=%v req %+v", c.read, c.req)
	}
}

func TestRecordingErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{
			fmt.Errorf("%w: container \"web\" is not attachable", errdefs.ErrRecordingUnavailable),
			http.StatusNotFound, "recording_unavailable",
		},
		{fmt.Errorf("%w: segment 3", errdefs.ErrRecordingSegmentGone), http.StatusNotFound, "segment_gone"},
		{fmt.Errorf("%w: web", errdefs.ErrContainerNotFound), http.StatusNotFound, "not_found"},
		{fmt.Errorf("%w: api", errdefs.ErrCellNotFound), http.StatusNotFound, "not_found"},
		{
			fmt.Errorf("%w: KukeonV1.ListContainerRecording", errdefs.ErrDaemonMethodUnsupported),
			http.StatusNotImplemented, "daemon_outdated",
		},
	}
	for _, path := range []string{"", "/segments/3"} {
		for _, tc := range cases {
			w := serveDaemon(t, recordingDial(&recordingClient{err: tc.err}), recordingURL+path)
			body := decodeBody[struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}](t, w)
			if w.Code != tc.status || body.Error.Code != tc.code || body.Error.Message != tc.err.Error() {
				t.Fatalf("%q %v: status %d body %+v", path, tc.err, w.Code, body)
			}
		}
	}
}
