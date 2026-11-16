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
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

type networkClient struct {
	kukeonv1.FakeClient

	scope  kukeonv1.NetworkScope
	err    error
	closed bool
}

func (c *networkClient) Close() error { c.closed = true; return nil }

func (c *networkClient) GetNetworkDetail(
	_ context.Context, scope kukeonv1.NetworkScope,
) (kukeonv1.GetNetworkDetailResult, error) {
	c.scope = scope
	if c.err != nil {
		return kukeonv1.GetNetworkDetailResult{}, c.err
	}
	return kukeonv1.GetNetworkDetailResult{
		Scope: scope,
		Space: kukeonv1.SpaceNetworkDetail{
			SubnetCIDR: "10.88.1.0/24", SubnetSource: kukeonv1.NetworkSubnetAllocator, CNINetworkExists: true,
		},
		Cell: &kukeonv1.CellNetworkDetail{IPv4State: kukeonv1.NetworkIPv4NotRunning},
	}, nil
}

func serveNetwork(t *testing.T, c *networkClient, url string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := newHandler(
		"http://localhost:5129",
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

func TestNetworkRoutesProxyScope(t *testing.T) {
	cases := []struct {
		url  string
		want kukeonv1.NetworkScope
	}{
		{"/api/realms/r/spaces/s/network", kukeonv1.NetworkScope{Kind: "space", Realm: "r", Space: "s"}},
		{
			"/api/realms/r/spaces/s/stacks/t/cells/c/network",
			kukeonv1.NetworkScope{Kind: "cell", Realm: "r", Space: "s", Stack: "t", Cell: "c"},
		},
		{
			"/api/realms/r/spaces/s/stacks/t/cells/c/containers/k/network",
			kukeonv1.NetworkScope{Kind: "container", Realm: "r", Space: "s", Stack: "t", Cell: "c", Container: "k"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			c := &networkClient{}
			w := serveNetwork(t, c, tc.url)
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
			space, _ := body["space"].(map[string]any)
			cell, _ := body["cell"].(map[string]any)
			if space["subnetCIDR"] != "10.88.1.0/24" || space["cniNetworkExists"] != true ||
				cell["ipv4State"] != "not-running" {
				t.Fatalf("body=%s", w.Body)
			}
		})
	}
}

func TestNetworkRoutesHaveNoRealmOrStackScope(t *testing.T) {
	for _, url := range []string{"/api/realms/r/network", "/api/realms/r/spaces/s/stacks/t/network"} {
		c := &networkClient{}
		if w := serveNetwork(t, c, url); w.Code == http.StatusOK || c.scope.Kind != "" {
			t.Fatalf("%s: status=%d scope=%+v", url, w.Code, c.scope)
		}
	}
}

func TestNetworkRoutesErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"not found", errdefs.ErrSpaceNotFound, http.StatusNotFound},
		{"daemon failure", io.EOF, http.StatusServiceUnavailable},
		{"permission", errors.Join(errdefs.ErrGetCell, fs.ErrPermission), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveNetwork(t, &networkClient{err: tc.err}, "/api/realms/r/spaces/s/network")
			if w.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestNetworkScopeRejectsUnsafeSegment(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetPathValue("space", "..")
	if _, err := networkScope(kukeonv1.NetworkKindSpace, r); !errors.Is(err, errdefs.ErrConfig) {
		t.Fatalf("got %v", err)
	}
}
