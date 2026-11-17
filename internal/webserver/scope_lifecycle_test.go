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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type parentCall struct {
	method  string
	realm   string
	space   string
	stack   string
	force   bool
	cascade bool
}

type parentLifecycleClient struct {
	kukeonv1.FakeClient

	mu      sync.Mutex
	err     error
	calls   []parentCall
	closed  bool
	entered chan struct{}
	release chan struct{}
}

func (c *parentLifecycleClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func (c *parentLifecycleClient) record(call parentCall) error {
	c.mu.Lock()
	c.calls = append(c.calls, call)
	entered, release, err := c.entered, c.release, c.err
	c.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	return err
}

func (c *parentLifecycleClient) DeleteRealm(
	_ context.Context, doc v1beta1.RealmDoc, force, cascade bool,
) (kukeonv1.DeleteRealmResult, error) {
	err := c.record(parentCall{"DeleteRealm", doc.Metadata.Name, "", "", force, cascade})
	if err != nil {
		return kukeonv1.DeleteRealmResult{}, err
	}
	realm := doc
	realm.Spec.RegistryCredentials = []v1beta1.RegistryCredentials{{ServerAddress: "r.example", Password: "hunter2"}}
	return kukeonv1.DeleteRealmResult{
		Realm: realm, Deleted: []string{"metadata"}, MetadataDeleted: true, CgroupDeleted: false,
	}, nil
}

func (c *parentLifecycleClient) PurgeRealm(
	_ context.Context, doc v1beta1.RealmDoc, force, cascade bool,
) (kukeonv1.PurgeRealmResult, error) {
	err := c.record(parentCall{"PurgeRealm", doc.Metadata.Name, "", "", force, cascade})
	if err != nil {
		return kukeonv1.PurgeRealmResult{}, err
	}
	return kukeonv1.PurgeRealmResult{
		Realm: doc, RealmDeleted: true, PurgeSucceeded: false, Purged: []string{"purge-error:busy"},
	}, nil
}

func (c *parentLifecycleClient) DeleteSpace(
	_ context.Context, doc v1beta1.SpaceDoc, force, cascade bool,
) (kukeonv1.DeleteSpaceResult, error) {
	err := c.record(parentCall{"DeleteSpace", doc.Spec.RealmID, doc.Metadata.Name, "", force, cascade})
	if err != nil {
		return kukeonv1.DeleteSpaceResult{}, err
	}
	return kukeonv1.DeleteSpaceResult{Space: doc, MetadataDeleted: true, CgroupDeleted: true}, nil
}

func (c *parentLifecycleClient) PurgeSpace(
	_ context.Context, doc v1beta1.SpaceDoc, force, cascade bool,
) (kukeonv1.PurgeSpaceResult, error) {
	err := c.record(parentCall{"PurgeSpace", doc.Spec.RealmID, doc.Metadata.Name, "", force, cascade})
	if err != nil {
		return kukeonv1.PurgeSpaceResult{}, err
	}
	return kukeonv1.PurgeSpaceResult{
		Space: doc, MetadataDeleted: true, PurgeSucceeded: true, Deleted: []string{"stack:t1"},
	}, nil
}

func (c *parentLifecycleClient) DeleteStack(
	_ context.Context, doc v1beta1.StackDoc, force, cascade bool,
) (kukeonv1.DeleteStackResult, error) {
	err := c.record(parentCall{"DeleteStack", doc.Spec.RealmID, doc.Spec.SpaceID, doc.Metadata.Name, force, cascade})
	if err != nil {
		return kukeonv1.DeleteStackResult{}, err
	}
	return kukeonv1.DeleteStackResult{Stack: doc, MetadataDeleted: true}, nil
}

func (c *parentLifecycleClient) PurgeStack(
	_ context.Context, doc v1beta1.StackDoc, force, cascade bool,
) (kukeonv1.PurgeStackResult, error) {
	err := c.record(parentCall{"PurgeStack", doc.Spec.RealmID, doc.Spec.SpaceID, doc.Metadata.Name, force, cascade})
	if err != nil {
		return kukeonv1.PurgeStackResult{}, err
	}
	return kukeonv1.PurgeStackResult{Stack: doc, Purged: []string{"cell:c1"}}, nil
}

func postParent(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(http.MethodPost, "http://localhost:5129"+path, nil)
	} else {
		req = httptest.NewRequest(http.MethodPost, "http://localhost:5129"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	handler.ServeHTTP(w, req)
	return w
}

func TestParentLifecycleRoutesCallScopedDaemonMethods(t *testing.T) {
	for _, tc := range []struct {
		path  string
		body  string
		want  parentCall
		check func(*testing.T, map[string]any)
	}{
		{
			path: "/api/realms/r1/delete",
			want: parentCall{method: "DeleteRealm", realm: "r1"},
			check: func(t *testing.T, body map[string]any) {
				if body["metadataDeleted"] != true || body["cgroupDeleted"] != false {
					t.Fatalf("partial realm delete not preserved: %+v", body)
				}
			},
		},
		{
			path: "/api/realms/r1/purge",
			body: `{"force":true,"cascade":true}`,
			want: parentCall{method: "PurgeRealm", realm: "r1", force: true, cascade: true},
			check: func(t *testing.T, body map[string]any) {
				if body["purgeSucceeded"] != false || body["realmDeleted"] != true {
					t.Fatalf("partial realm purge not preserved: %+v", body)
				}
				if got := body["purged"].([]any); len(got) != 1 || got[0] != "purge-error:busy" {
					t.Fatalf("purged=%v", got)
				}
			},
		},
		{
			path: "/api/realms/r1/spaces/s1/delete",
			body: `{"force":false,"cascade":true}`,
			want: parentCall{method: "DeleteSpace", realm: "r1", space: "s1", cascade: true},
		},
		{
			path: "/api/realms/r1/spaces/s1/purge",
			want: parentCall{method: "PurgeSpace", realm: "r1", space: "s1"},
			check: func(t *testing.T, body map[string]any) {
				if got := body["deleted"].([]any); len(got) != 1 || got[0] != "stack:t1" {
					t.Fatalf("deleted=%v", got)
				}
			},
		},
		{
			path: "/api/realms/r1/spaces/s1/stacks/t1/delete",
			body: `{"force":true}`,
			want: parentCall{method: "DeleteStack", realm: "r1", space: "s1", stack: "t1", force: true},
		},
		{
			path: "/api/realms/r1/spaces/s1/stacks/t1/purge",
			body: `{"cascade":true}`,
			want: parentCall{method: "PurgeStack", realm: "r1", space: "s1", stack: "t1", cascade: true},
		},
	} {
		t.Run(tc.want.method, func(t *testing.T) {
			client := &parentLifecycleClient{}
			handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
			w := postParent(handler, tc.path, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if len(client.calls) != 1 || client.calls[0] != tc.want {
				t.Fatalf("daemon calls=%+v, want %+v", client.calls, tc.want)
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			wantAction := "delete"
			if strings.HasSuffix(tc.path, "/purge") {
				wantAction = "purge"
			}
			if body["action"] != wantAction || body["force"] != tc.want.force || body["cascade"] != tc.want.cascade {
				t.Fatalf("response action/options=%v/%v/%v", body["action"], body["force"], body["cascade"])
			}
			if strings.Contains(w.Body.String(), "hunter2") {
				t.Fatalf("response leaked registry password: %s", w.Body)
			}
			if tc.check != nil {
				tc.check(t, body)
			}
			if !client.closed {
				t.Fatal("request client was not closed")
			}
		})
	}
}

func TestParentLifecycleErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		err    error
		status int
		code   string
	}{
		{
			name: "children refuse", path: "/api/realms/r1/spaces/s1/delete",
			err: fmt.Errorf(
				"%w: space %q has %d stack(s). Use --cascade to delete them or --force to skip validation",
				errdefs.ErrResourceHasDependencies, "s1", 2,
			),
			status: http.StatusConflict, code: "has_dependencies",
		},
		{
			name: "realm not found", path: "/api/realms/missing/purge",
			err: errdefs.ErrRealmNotFound, status: http.StatusNotFound, code: "not_found",
		},
		{
			name: "stack not found", path: "/api/realms/r1/spaces/s1/stacks/gone/delete",
			err: errdefs.ErrStackNotFound, status: http.StatusNotFound, code: "not_found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &parentLifecycleClient{err: tc.err}
			handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
			w := postParent(handler, tc.path, "")
			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d body=%s", w.Code, tc.status, w.Body)
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tc.code || body.Error.Message != tc.err.Error() {
				t.Fatalf("error=%+v, want code %q message %q", body.Error, tc.code, tc.err.Error())
			}
		})
	}
}

func TestParentLifecycleRejectsOverlappingOperations(t *testing.T) {
	release := make(chan struct{})
	client := &parentLifecycleClient{entered: make(chan struct{}, 1), release: release}
	handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- postParent(handler, "/api/realms/r1/spaces/s1/purge", "") }()
	<-client.entered

	for _, path := range []string{
		"/api/realms/r1/spaces/s1/delete",
		"/api/realms/r1/purge",
		"/api/realms/r1/spaces/s1/stacks/t1/delete",
	} {
		if w := postParent(handler, path, ""); w.Code != http.StatusConflict ||
			!strings.Contains(w.Body.String(), "operation_in_progress") {
			t.Fatalf("%s: status=%d body=%s, want 409 operation_in_progress", path, w.Code, w.Body)
		}
	}

	// A sibling space lies outside the locked scope and proceeds.
	client.mu.Lock()
	client.entered, client.release = nil, nil
	client.mu.Unlock()
	if w := postParent(handler, "/api/realms/r1/spaces/s2/delete", ""); w.Code != http.StatusOK {
		t.Fatalf("sibling space: status=%d body=%s", w.Code, w.Body)
	}

	close(release)
	if w := <-done; w.Code != http.StatusOK {
		t.Fatalf("first purge: status=%d body=%s", w.Code, w.Body)
	}
	// The lock is released once the first action completes.
	if w := postParent(handler, "/api/realms/r1/purge", ""); w.Code != http.StatusOK {
		t.Fatalf("after release: status=%d body=%s", w.Code, w.Body)
	}
	if len(client.calls) != 3 {
		t.Fatalf("daemon calls=%+v, want 3 (rejected requests must not reach the daemon)", client.calls)
	}
}

func TestParentLifecycleKeepsSameOriginProtection(t *testing.T) {
	handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) {
		t.Fatal("cross-origin request reached the daemon")
		return nil, kukeonv1.ErrUnexpectedCall
	})
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5129/api/realms/r1/spaces/s1/purge", nil)
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", w.Code)
	}
}

func TestParentLifecycleRejectsInvalidOptionsBeforeDial(t *testing.T) {
	handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) {
		t.Fatal("invalid request reached the daemon")
		return nil, kukeonv1.ErrUnexpectedCall
	})
	for _, body := range []string{`{"force":true,"extra":1}`, `{"force":true}{}`, `not json`} {
		if w := postParent(handler, "/api/realms/r1/delete", body); w.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status=%d, want 400", body, w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5129/api/realms/r1/delete", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("text/plain body: status=%d, want 400", w.Code)
	}
}

func TestParentScopeContainment(t *testing.T) {
	realm := parentScope{realm: "r1"}
	space := parentScope{realm: "r1", space: "s1"}
	stack := parentScope{realm: "r1", space: "s1", stack: "t1"}
	if !realm.contains(space) || !realm.contains(stack) || !space.contains(stack) {
		t.Fatal("ancestor scope must contain descendants")
	}
	if space.contains(realm) || stack.contains(space) {
		t.Fatal("descendant scope must not contain ancestors")
	}
	if (parentScope{realm: "r2"}).contains(space) || (parentScope{realm: "r1", space: "s2"}).contains(stack) {
		t.Fatal("sibling scopes must not overlap")
	}
}
