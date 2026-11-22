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

const containerLogURL = "/api/realms/dev/spaces/shared/stacks/services/cells/api/containers/web/log"

type containerLogClient struct {
	kukeonv1.FakeClient

	called bool
	doc    v1beta1.ContainerDoc
	req    kukeonv1.ContainerLogRequest
	res    kukeonv1.ContainerLogResult
	err    error
	closed bool
}

func (c *containerLogClient) Close() error { c.closed = true; return nil }

func (c *containerLogClient) ReadContainerLog(
	_ context.Context, doc v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest,
) (kukeonv1.ContainerLogResult, error) {
	c.called, c.doc, c.req = true, doc, req
	return c.res, c.err
}

func containerLogDial(c *containerLogClient) Dial {
	return func(context.Context) (kukeonv1.Client, error) { return c, nil }
}

func decodeContainerLog(t *testing.T, w interface{ Result() *http.Response }) containerLogChunk {
	t.Helper()
	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var chunk containerLogChunk
	if err := json.NewDecoder(res.Body).Decode(&chunk); err != nil {
		t.Fatal(err)
	}
	return chunk
}

func TestContainerLogTailsByDefault(t *testing.T) {
	c := &containerLogClient{res: kukeonv1.ContainerLogResult{
		Source: "/opt/kukeon/web.log", Data: []byte("ready\n"), Start: 10, Next: 16, Size: 16, Truncated: true,
	}}
	w := serveDaemon(t, containerLogDial(c), containerLogURL)
	chunk := decodeContainerLog(t, w)
	if c.req != (kukeonv1.ContainerLogRequest{Offset: -1, MaxBytes: kukeonv1.ContainerLogDefaultMaxBytes}) {
		t.Fatalf("default req %+v", c.req)
	}
	if c.req.MaxBytes != 65536 {
		t.Fatalf("default maxBytes %d", c.req.MaxBytes)
	}
	want := v1beta1.ContainerDoc{
		Metadata: v1beta1.ContainerMetadata{Name: "web"},
		Spec: v1beta1.ContainerSpec{
			ID: "web", RealmID: "dev", SpaceID: "shared", StackID: "services", CellID: "api",
		},
	}
	if c.doc.Metadata.Name != want.Metadata.Name || c.doc.Spec.ID != want.Spec.ID ||
		c.doc.Spec.RealmID != want.Spec.RealmID || c.doc.Spec.SpaceID != want.Spec.SpaceID ||
		c.doc.Spec.StackID != want.Spec.StackID || c.doc.Spec.CellID != want.Spec.CellID {
		t.Fatalf("doc %+v", c.doc)
	}
	if !c.closed {
		t.Fatal("client was not closed")
	}
	if chunk.Source != "/opt/kukeon/web.log" || string(chunk.Data) != "ready\n" ||
		chunk.Start != 10 || chunk.Next != 16 || chunk.Size != 16 || !chunk.Truncated || chunk.More {
		t.Fatalf("chunk %+v", chunk)
	}
}

func TestContainerLogReadsForward(t *testing.T) {
	c := &containerLogClient{res: kukeonv1.ContainerLogResult{
		Source: "/opt/kukeon/web.log", Data: []byte("abcd"), Start: 10, Next: 14, Size: 40, More: true,
	}}
	chunk := decodeContainerLog(t, serveDaemon(t, containerLogDial(c), containerLogURL+"?offset=10&maxBytes=4"))
	if c.req != (kukeonv1.ContainerLogRequest{Offset: 10, MaxBytes: 4}) {
		t.Fatalf("req %+v", c.req)
	}
	if chunk.Next != 14 || !chunk.More || chunk.Size != 40 || chunk.Start != 10 {
		t.Fatalf("chunk %+v", chunk)
	}
}

func TestContainerLogEncodesRawBytesAsBase64(t *testing.T) {
	raw := []byte{0x1b, '[', '0', 'm', 0xff}
	c := &containerLogClient{res: kukeonv1.ContainerLogResult{Data: raw, Next: 5, Size: 5}}
	w := serveDaemon(t, containerLogDial(c), containerLogURL)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	encoded, ok := body["data"].(string)
	if !ok {
		t.Fatalf("data %T %v", body["data"], body["data"])
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatalf("decoded %v, want %v", decoded, raw)
	}
}

func TestContainerLogRejectsInvalidBounds(t *testing.T) {
	for _, query := range []string{
		"offset=-2", "offset=x", "maxBytes=0", "maxBytes=x", fmt.Sprintf("maxBytes=%d", kukeonv1.ContainerLogMaxBytes+1),
	} {
		c := &containerLogClient{}
		w := serveDaemon(t, containerLogDial(c), containerLogURL+"?"+query)
		body := decodeBody[struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}](t, w)
		if w.Code != http.StatusBadRequest || body.Error.Code != "invalid_request" {
			t.Fatalf("%s: status %d body %s", query, w.Code, w.Body)
		}
		if c.called {
			t.Fatalf("%s: daemon was called with %+v", query, c.req)
		}
	}
	c := &containerLogClient{}
	_ = serveDaemon(t, containerLogDial(c), containerLogURL+fmt.Sprintf("?maxBytes=%d", kukeonv1.ContainerLogMaxBytes))
	if !c.called || c.req.MaxBytes != kukeonv1.ContainerLogMaxBytes {
		t.Fatalf("maxBytes at the bound: called=%v req %+v", c.called, c.req)
	}
}

func TestContainerLogErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{
			fmt.Errorf("%w: no log file at /opt/kukeon/web.log", errdefs.ErrContainerLogUnavailable),
			http.StatusNotFound, "log_unavailable",
		},
		{fmt.Errorf("%w: web", errdefs.ErrContainerNotFound), http.StatusNotFound, "not_found"},
		{fmt.Errorf("%w: api", errdefs.ErrCellNotFound), http.StatusNotFound, "not_found"},
		{
			fmt.Errorf("%w: KukeonV1.ReadContainerLog", errdefs.ErrDaemonMethodUnsupported),
			http.StatusNotImplemented, "daemon_outdated",
		},
	}
	for _, tc := range cases {
		w := serveDaemon(t, containerLogDial(&containerLogClient{err: tc.err}), containerLogURL)
		body := decodeBody[struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}](t, w)
		if w.Code != tc.status || body.Error.Code != tc.code || body.Error.Message != tc.err.Error() {
			t.Fatalf("%v: status %d body %+v", tc.err, w.Code, body)
		}
	}
}
