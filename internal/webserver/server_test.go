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
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"net/rpc/jsonrpc"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestListenAddress(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "127.0.0.2", "::1"} {
		if _, err := listenAddress(host, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"", "0.0.0.0", "::", "example.com", "localhost.evil", "192.168.0.1"} {
		if _, err := listenAddress(host, 5129); err == nil {
			t.Errorf("accepted %q", host)
		}
	}
	for _, port := range []int{-1, 65536} {
		if _, err := listenAddress("localhost", port); err == nil {
			t.Errorf("accepted %d", port)
		}
	}
}

func TestAccessPolicy(t *testing.T) {
	handler, err := newHandler("localhost:5129", "unix:///missing", func(context.Context) (kukeonv1.Client, error) {
		t.Fatal("access rejection must precede RPC")
		return nil, io.EOF
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host, origin, method, path string
		status                     int
	}{
		{"localhost:5129", "", "GET", "/", 200},
		{"localhost:5129", "http://localhost:5129", "GET", "/api/connection", 200},
		{"evil:5129", "", "GET", "/api/realms", 403},
		{"localhost:1234", "", "GET", "/", 403},
		{"127.0.0.1:5129", "", "GET", "/", 403},
		{"localhost:5129", "null", "GET", "/", 403},
		{"localhost:5129", "https://localhost:5129", "GET", "/", 403},
		{"localhost:5129", "http://evil", "GET", "/api/realms", 403},
		{"localhost:5129", "", "POST", "/api/realms", 405},
	} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%+v: got %d", tc, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "http://localhost:5129/", nil)
	r.Header.Add("Origin", "http://localhost:5129")
	r.Header.Add("Origin", "http://localhost:5129")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("duplicate Origin accepted")
	}
}

type realmClient struct {
	kukeonv1.FakeClient

	doc                    v1beta1.RealmDoc
	err                    error
	empty, missing, closed bool
	name                   string
}

func (c *realmClient) Close() error { c.closed = true; return nil }
func (c *realmClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.RealmDoc{c.doc}, c.err
}

func (c *realmClient) GetRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	c.name = doc.Metadata.Name
	return kukeonv1.GetRealmResult{Realm: c.doc, MetadataExists: !c.missing}, c.err
}

func TestRealmAPI(t *testing.T) {
	doc := v1beta1.RealmDoc{
		APIVersion: v1beta1.Version("v1beta1"),
		Metadata:   v1beta1.RealmMetadata{Name: "demo", Labels: map[string]string{"owner": "user"}, Generation: 7},
	}
	for _, tc := range []struct {
		name, path     string
		err            error
		empty, missing bool
		status         int
		code           string
	}{
		{name: "list", path: "/api/realms", status: 200},
		{name: "detail", path: "/api/realms/demo", status: 200},
		{name: "empty", path: "/api/realms", empty: true, status: 200},
		{name: "permission", path: "/api/realms", err: fmt.Errorf("socket: %w", fs.ErrPermission), status: 403, code: "permission_denied"},
		{name: "unavailable", path: "/api/realms", err: io.EOF, status: 503, code: "unavailable"},
		{name: "not found", path: "/api/realms/demo", err: errdefs.ErrRealmNotFound, status: 404, code: "not_found"},
		{name: "no metadata", path: "/api/realms/demo", missing: true, status: 404, code: "not_found"},
		{name: "invalid", path: "/api/realms/demo", err: errdefs.ErrInvalidRealmName, status: 400, code: "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &realmClient{doc: doc, err: tc.err, empty: tc.empty, missing: tc.missing}
			handler, err := newHandler(
				"localhost:5129",
				"unix:///test",
				func(context.Context) (kukeonv1.Client, error) { return client, nil },
			)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+tc.path, nil))
			if w.Code != tc.status || !client.closed {
				t.Fatalf("status=%d closed=%v: %s", w.Code, client.closed, w.Body.String())
			}
			if tc.code != "" && !strings.Contains(w.Body.String(), tc.code) {
				t.Fatal(w.Body.String())
			}
			if tc.status != 200 {
				return
			}
			if tc.empty {
				if strings.TrimSpace(w.Body.String()) != "[]" {
					t.Fatal(w.Body.String())
				}
				return
			}
			var got v1beta1.RealmDoc
			if tc.name == "detail" {
				if decodeErr := json.Unmarshal(w.Body.Bytes(), &got); decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if client.name != "demo" {
					t.Fatal(client.name)
				}
			} else {
				var docs []v1beta1.RealmDoc
				if decodeErr := json.Unmarshal(w.Body.Bytes(), &docs); decodeErr != nil {
					t.Fatal(decodeErr)
				}
				got = docs[0]
			}
			if !reflect.DeepEqual(got, doc) {
				t.Fatalf("document changed: %#v", got)
			}
		})
	}
}

// RealmRPC is a real JSON-RPC peer used to exercise reconnect and transport boundaries.
type RealmRPC struct{}

func (*RealmRPC) ListRealms(_ *kukeonv1.ListRealmsArgs, reply *kukeonv1.ListRealmsReply) error {
	reply.Realms = []v1beta1.RealmDoc{{Metadata: v1beta1.RealmMetadata{Name: "over-rpc"}}}
	return nil
}

func TestDaemonRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	handler, err := newHandler(
		"localhost:5129",
		"unix://"+path,
		func(ctx context.Context) (kukeonv1.Client, error) { return kukeonv1.Dial(ctx, "unix://"+path) },
	)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+path, nil))
		return w
	}
	if w := request("/api/realms"); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if w := request("/"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := rpc.NewServer()
	if registerErr := server.RegisterName(kukeonv1.ServiceName, &RealmRPC{}); registerErr != nil {
		t.Fatal(registerErr)
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go server.ServeCodec(jsonrpc.NewServerCodec(conn))
		}
	}()
	if w := request("/api/realms"); w.Code != 200 || !strings.Contains(w.Body.String(), "over-rpc") {
		t.Fatal(w.Body.String())
	}
	// Each HTTP request reconnects; no stale RPC connection survives a restart.
	if w := request("/api/realms"); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

type urlWriter chan string

func (w urlWriter) Write(p []byte) (int, error) { w <- string(p); return len(p), nil }

func TestRunLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := make(urlWriter, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, "127.0.0.1", 0, "unix:///missing", func(context.Context) (kukeonv1.Client, error) { return nil, io.EOF }, output)
	}()
	var url string
	select {
	case line := <-output:
		url = strings.TrimSpace(strings.TrimPrefix(line, "Listening on "))
	case <-time.After(5 * time.Second):
		t.Fatal("no bound URL")
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode)
	}
	// An occupied address fails before printing a successful URL.
	_, port, err := net.SplitHostPort(strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	var p int
	if _, scanErr := fmt.Sscan(port, &p); scanErr != nil {
		t.Fatal(scanErr)
	}
	if runErr := Run(ctx, "127.0.0.1", p, "", nil, io.Discard); runErr == nil {
		t.Fatal("accepted occupied port")
	}
	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown hung")
	}
	if conn, dialErr := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second); dialErr == nil {
		_ = conn.Close()
		t.Fatal("listener survived shutdown")
	}
}

// A pending RPC must receive cancellation and release its owned client.
type blockingClient struct {
	kukeonv1.FakeClient

	started chan struct{}
	closed  chan struct{}
}

func (c *blockingClient) ListRealms(ctx context.Context) ([]v1beta1.RealmDoc, error) {
	close(c.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (c *blockingClient) Close() error { close(c.closed); return nil }

func TestRequestCancellationClosesClient(t *testing.T) {
	client := &blockingClient{started: make(chan struct{}), closed: make(chan struct{})}
	handler, err := newHandler(
		"localhost:5129",
		"unix:///test",
		func(context.Context) (kukeonv1.Client, error) { return client, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "http://localhost:5129/api/realms", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(httptest.NewRecorder(), request) }()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("RPC not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request did not cancel")
	}
	select {
	case <-client.closed:
	default:
		t.Fatal("client not closed")
	}
}
