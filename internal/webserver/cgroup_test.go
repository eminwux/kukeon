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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

type cgroupClient struct {
	kukeonv1.FakeClient

	scope  kukeonv1.CgroupScope
	err    error
	closed bool
}

func (c *cgroupClient) Close() error { c.closed = true; return nil }

func (c *cgroupClient) GetCgroupDetail(
	_ context.Context, scope kukeonv1.CgroupScope,
) (kukeonv1.GetCgroupDetailResult, error) {
	c.scope = scope
	if c.err != nil {
		return kukeonv1.GetCgroupDetailResult{}, c.err
	}
	return kukeonv1.GetCgroupDetailResult{
		Scope:      scope,
		CgroupPath: "/kukeon/r",
		Exists:     true,
		Live: &kukeonv1.CgroupLive{
			Memory: kukeonv1.CgroupMemory{Max: kukeonv1.CgroupValue{State: kukeonv1.CgroupValueMax, Raw: "max"}},
		},
	}, nil
}

func serveCgroup(t *testing.T, c *cgroupClient, url string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := newHandler(
		"localhost:5129",
		"unix:///test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+url, nil))
	return w
}

func TestCgroupRoutesProxyScope(t *testing.T) {
	cases := []struct {
		url  string
		want kukeonv1.CgroupScope
	}{
		{"/api/realms/r/cgroup", kukeonv1.CgroupScope{Kind: "realm", Realm: "r"}},
		{"/api/realms/r/spaces/s/cgroup", kukeonv1.CgroupScope{Kind: "space", Realm: "r", Space: "s"}},
		{
			"/api/realms/r/spaces/s/stacks/t/cgroup",
			kukeonv1.CgroupScope{Kind: "stack", Realm: "r", Space: "s", Stack: "t"},
		},
		{
			"/api/realms/r/spaces/s/stacks/t/cells/c/cgroup",
			kukeonv1.CgroupScope{Kind: "cell", Realm: "r", Space: "s", Stack: "t", Cell: "c"},
		},
		{
			"/api/realms/r/spaces/s/stacks/t/cells/c/containers/k/cgroup",
			kukeonv1.CgroupScope{Kind: "container", Realm: "r", Space: "s", Stack: "t", Cell: "c", Container: "k"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			c := &cgroupClient{}
			w := serveCgroup(t, c, tc.url)
			if w.Code != http.StatusOK || !c.closed {
				t.Fatalf("status=%d closed=%v body=%s", w.Code, c.closed, w.Body)
			}
			if c.scope != tc.want {
				t.Fatalf("scope=%+v want %+v", c.scope, tc.want)
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			live, _ := body["live"].(map[string]any)
			memory, _ := live["memory"].(map[string]any)
			memMax, _ := memory["max"].(map[string]any)
			if body["exists"] != true || memMax["state"] != "max" {
				t.Fatalf("body=%s", w.Body)
			}
		})
	}
}

func TestCgroupRoutesErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"not found", errdefs.ErrCellNotFound, http.StatusNotFound},
		{"daemon failure", io.EOF, http.StatusServiceUnavailable},
		{"permission", errors.Join(errdefs.ErrGetCell, fs.ErrPermission), http.StatusForbidden},
		{
			"daemon outdated",
			fmt.Errorf("%w: KukeonV1.GetCgroupDetail", errdefs.ErrDaemonMethodUnsupported),
			http.StatusNotImplemented,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveCgroup(t, &cgroupClient{err: tc.err}, "/api/realms/r/spaces/s/stacks/t/cells/c/cgroup")
			if w.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestCgroupScopeRejectsUnsafeSegment(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetPathValue("realm", "..")
	if _, err := cgroupScope(kukeonv1.CgroupKindRealm, r); !errors.Is(err, errdefs.ErrConfig) {
		t.Fatalf("got %v", err)
	}
}
