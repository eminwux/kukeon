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
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const lifecyclePath = "/api/realms/r1/spaces/s1/stacks/t1/cells/c1/"

type lifecycleClient struct {
	kukeonv1.FakeClient

	mu           sync.Mutex
	cell         v1beta1.CellDoc
	state        v1beta1.CellState
	getErr       error
	startErr     error
	stopErr      error
	killErr      error
	deleteErr    error
	purgeErr     error
	startCalls   int
	stopCalls    int
	killCalls    int
	deleteCalls  int
	purgeCalls   int
	getCalls     int
	closed       bool
	received     []v1beta1.CellDoc
	killResult   *kukeonv1.KillCellResult
	deleteResult *kukeonv1.DeleteCellResult
	purgeResult  *kukeonv1.PurgeCellResult
	purgeForce   bool
	purgeCascade bool
	stopEntered  chan struct{}
	stopRelease  chan struct{}
}

func (c *lifecycleClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func (c *lifecycleClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	c.received = append(c.received, doc)
	if c.getErr != nil {
		return kukeonv1.GetCellResult{}, c.getErr
	}
	return kukeonv1.GetCellResult{
		MetadataExists: true,
		Cell: v1beta1.CellDoc{
			Metadata: doc.Metadata,
			Spec:     doc.Spec,
			Status:   v1beta1.CellStatus{State: c.state},
		},
	}, nil
}

func (c *lifecycleClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.startCalls++
	c.received = append(c.received, doc)
	if c.startErr != nil {
		return kukeonv1.StartCellResult{}, c.startErr
	}
	c.state = v1beta1.CellStateReady
	cell := c.cell
	cell.Metadata = doc.Metadata
	cell.Spec.ID = doc.Spec.ID
	cell.Spec.RealmID = doc.Spec.RealmID
	cell.Spec.SpaceID = doc.Spec.SpaceID
	cell.Spec.StackID = doc.Spec.StackID
	cell.Status.State = c.state
	return kukeonv1.StartCellResult{Cell: cell, Started: true}, nil
}

func (c *lifecycleClient) StopCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
	c.mu.Lock()
	c.stopCalls++
	c.received = append(c.received, doc)
	entered, release, err := c.stopEntered, c.stopRelease, c.stopErr
	c.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return kukeonv1.StopCellResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = v1beta1.CellStateStopped
	cell := c.cell
	cell.Metadata = doc.Metadata
	cell.Spec.ID = doc.Spec.ID
	cell.Spec.RealmID = doc.Spec.RealmID
	cell.Spec.SpaceID = doc.Spec.SpaceID
	cell.Spec.StackID = doc.Spec.StackID
	cell.Status.State = c.state
	return kukeonv1.StopCellResult{Cell: cell, Stopped: true}, nil
}

func (c *lifecycleClient) KillCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killCalls++
	c.received = append(c.received, doc)
	if c.killErr != nil {
		return kukeonv1.KillCellResult{}, c.killErr
	}
	if c.killResult != nil {
		return *c.killResult, nil
	}
	return kukeonv1.KillCellResult{Cell: doc, Killed: true}, nil
}

func (c *lifecycleClient) DeleteCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteCalls++
	c.received = append(c.received, doc)
	if c.deleteErr != nil {
		return kukeonv1.DeleteCellResult{}, c.deleteErr
	}
	if c.deleteResult != nil {
		return *c.deleteResult, nil
	}
	return kukeonv1.DeleteCellResult{Cell: doc, ContainersDeleted: true, CgroupDeleted: true, MetadataDeleted: true}, nil
}

func (c *lifecycleClient) PurgeCell(_ context.Context, doc v1beta1.CellDoc, force, cascade bool) (kukeonv1.PurgeCellResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeCalls++
	c.received = append(c.received, doc)
	c.purgeForce = force
	c.purgeCascade = cascade
	if c.purgeErr != nil {
		return kukeonv1.PurgeCellResult{}, c.purgeErr
	}
	if c.purgeResult != nil {
		return *c.purgeResult, nil
	}
	return kukeonv1.PurgeCellResult{
		Cell: doc, ContainersDeleted: true, CgroupDeleted: true, MetadataDeleted: true,
		PurgeSucceeded: true, Force: force, Cascade: cascade,
	}, nil
}

func lifecycleHandler(t *testing.T, dial Dial) http.Handler {
	t.Helper()
	handler, err := newHandler("localhost:5129", "unix:///test", dial)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func postLifecycle(handler http.Handler, action string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://localhost:5129"+lifecyclePath+action, nil))
	return w
}

func postLifecycleJSON(handler http.Handler, action, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5129"+lifecyclePath+action, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, req)
	return w
}

func TestCellLifecycleDestructiveRoutesUseScopedDaemonOperations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		body   string
		client *lifecycleClient
		check  func(*testing.T, map[string]any)
	}{
		{
			name:   "kill",
			action: "kill",
			client: &lifecycleClient{killResult: &kukeonv1.KillCellResult{
				Cell: v1beta1.CellDoc{Spec: v1beta1.CellSpec{RuntimeEnv: []string{"TOKEN=private"}}}, Killed: true,
			}},
			check: func(t *testing.T, body map[string]any) {
				if body["killed"] != true {
					t.Fatalf("killed=%v, want true", body["killed"])
				}
			},
		},
		{
			name:   "delete",
			action: "delete",
			client: &lifecycleClient{deleteResult: &kukeonv1.DeleteCellResult{
				ContainersDeleted: true, CgroupDeleted: true, MetadataDeleted: true,
			}},
			check: func(t *testing.T, body map[string]any) {
				for _, field := range []string{"containersDeleted", "cgroupDeleted", "metadataDeleted"} {
					if body[field] != true {
						t.Fatalf("%s=%v, want true", field, body[field])
					}
				}
			},
		},
		{
			name:   "delete partial result",
			action: "delete",
			client: &lifecycleClient{deleteResult: &kukeonv1.DeleteCellResult{
				ContainersDeleted: true, CgroupDeleted: false, MetadataDeleted: false,
			}},
			check: func(t *testing.T, body map[string]any) {
				if body["containersDeleted"] != true || body["cgroupDeleted"] != false || body["metadataDeleted"] != false {
					t.Fatalf("partial delete result was not preserved: %+v", body)
				}
			},
		},
		{
			name:   "purge explicit options and partial result",
			action: "purge",
			body:   `{"force":true,"cascade":false}`,
			client: &lifecycleClient{purgeResult: &kukeonv1.PurgeCellResult{
				PurgeSucceeded: false, Force: true, Cascade: false, MetadataDeleted: false,
				Deleted: []string{"containers"}, Purged: []string{"purge-error:permission denied"},
			}},
			check: func(t *testing.T, body map[string]any) {
				if body["purgeSucceeded"] != false || body["force"] != true || body["cascade"] != false || body["metadataDeleted"] != false {
					t.Fatalf("partial purge result was not preserved: %+v", body)
				}
				if got := body["purged"].([]any); len(got) != 1 || got[0] != "purge-error:permission denied" {
					t.Fatalf("purged=%v", got)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := tc.client
			handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
			var w *httptest.ResponseRecorder
			if tc.body == "" {
				w = postLifecycle(handler, tc.action)
			} else {
				w = postLifecycleJSON(handler, tc.action, tc.body)
			}
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["action"] != tc.action {
				t.Fatalf("action=%v, want %q", body["action"], tc.action)
			}
			tc.check(t, body)
			if strings.Contains(w.Body.String(), "TOKEN=private") {
				t.Fatalf("response leaked cell environment: %s", w.Body)
			}
			if len(client.received) != 1 {
				t.Fatalf("daemon call documents=%d, want 1", len(client.received))
			}
			doc := client.received[0]
			if doc.Metadata.Name != "c1" || doc.Spec.ID != "c1" || doc.Spec.RealmID != "r1" || doc.Spec.SpaceID != "s1" || doc.Spec.StackID != "t1" {
				t.Fatalf("daemon received incompletely scoped cell: %+v", doc)
			}
			if tc.action == "purge" && (client.purgeForce != true || client.purgeCascade != false) {
				t.Fatalf("purge options = force:%v cascade:%v", client.purgeForce, client.purgeCascade)
			}
			if !client.closed {
				t.Fatal("request client was not closed")
			}
		})
	}
}

func TestPurgeLifecycleDefaultsOptionsToFalse(t *testing.T) {
	client := &lifecycleClient{}
	handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
	w := postLifecycle(handler, "purge")
	if w.Code != http.StatusOK || client.purgeForce || client.purgeCascade {
		t.Fatalf("status=%d options force:%v cascade:%v body=%s", w.Code, client.purgeForce, client.purgeCascade, w.Body)
	}
}

func TestPurgeLifecycleRejectsInvalidOptionsBeforeDial(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "unknown option", contentType: "application/json", body: `{"force":true,"other":true}`},
		{name: "multiple objects", contentType: "application/json", body: `{} {}`},
		{name: "invalid JSON", contentType: "application/json", body: `{`},
		{name: "wrong content type", contentType: "text/plain", body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialed := false
			handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) {
				dialed = true
				return &lifecycleClient{}, nil
			})
			req := httptest.NewRequest(http.MethodPost, "http://localhost:5129"+lifecyclePath+"purge", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest || dialed || !strings.Contains(w.Body.String(), "invalid_request") {
				t.Fatalf("status=%d dialed=%v body=%s", w.Code, dialed, w.Body)
			}
		})
	}
}

func TestCellLifecycleRoutesUseFullyScopedDaemonOperations(t *testing.T) {
	for _, tc := range []struct {
		name      string
		action    string
		state     v1beta1.CellState
		wantGet   int
		wantStart int
		wantStop  int
	}{
		{name: "start", action: "start", state: v1beta1.CellStateStopped, wantStart: 1},
		{name: "stop", action: "stop", state: v1beta1.CellStateReady, wantStop: 1},
		{name: "restart ready", action: "restart", state: v1beta1.CellStateReady, wantGet: 1, wantStart: 1, wantStop: 1},
		{name: "restart stopped", action: "restart", state: v1beta1.CellStateStopped, wantGet: 1, wantStart: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &lifecycleClient{
				state: tc.state,
				cell: v1beta1.CellDoc{Spec: v1beta1.CellSpec{
					RuntimeEnv: []string{"PASSWORD=must-not-leak"},
					Containers: []v1beta1.ContainerSpec{{Env: []string{"TOKEN=private"}}},
				}},
			}
			handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
			w := postLifecycle(handler, tc.action)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if got := [3]int{client.getCalls, client.stopCalls, client.startCalls}; got != [3]int{
				tc.wantGet,
				tc.wantStop,
				tc.wantStart,
			} {
				t.Fatalf("Get/Stop/Start calls = %v", got)
			}
			if !client.closed {
				t.Fatal("request client was not closed")
			}
			if strings.Contains(w.Body.String(), "must-not-leak") || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("response leaked cell inputs: %s", w.Body)
			}
			wantScope := v1beta1.CellDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindCell,
				Metadata:   v1beta1.CellMetadata{Name: "c1"},
				Spec: v1beta1.CellSpec{
					ID: "c1", RealmID: "r1", SpaceID: "s1", StackID: "t1",
				},
			}
			if len(client.received) != tc.wantGet+tc.wantStop+tc.wantStart {
				t.Fatalf("received docs=%d, want %d", len(client.received), tc.wantGet+tc.wantStop+tc.wantStart)
			}
			for _, got := range client.received {
				if got.APIVersion != wantScope.APIVersion || got.Kind != wantScope.Kind ||
					got.Metadata.Name != wantScope.Metadata.Name || got.Spec.ID != wantScope.Spec.ID ||
					got.Spec.RealmID != wantScope.Spec.RealmID || got.Spec.SpaceID != wantScope.Spec.SpaceID ||
					got.Spec.StackID != wantScope.Spec.StackID {
					t.Fatalf("daemon received incompletely scoped cell doc: %+v", got)
				}
			}
		})
	}
}

func TestCellLifecycleErrorsAndRestartFailurePhase(t *testing.T) {
	for _, tc := range []struct {
		name             string
		action           string
		client           *lifecycleClient
		dialErr          error
		wantCode         int
		wantError        string
		wantPhase        string
		wantStoppedFirst bool
	}{
		{name: "permission", action: "start", client: &lifecycleClient{startErr: fmt.Errorf("%w", fs.ErrPermission)}, wantCode: http.StatusForbidden, wantError: "permission_denied"},
		{name: "kill permission", action: "kill", client: &lifecycleClient{killErr: fmt.Errorf("%w", fs.ErrPermission)}, wantCode: http.StatusForbidden, wantError: "permission_denied"},
		{name: "delete missing cell", action: "delete", client: &lifecycleClient{deleteErr: errdefs.ErrCellNotFound}, wantCode: http.StatusNotFound, wantError: "not_found"},
		{name: "purge permission", action: "purge", client: &lifecycleClient{purgeErr: fmt.Errorf("%w", fs.ErrPermission)}, wantCode: http.StatusForbidden, wantError: "permission_denied"},
		{name: "kill invalid state", action: "kill", client: &lifecycleClient{killErr: errdefs.ErrInvalidCellState}, wantCode: http.StatusConflict, wantError: "invalid_state"},
		{name: "kill timeout", action: "kill", client: &lifecycleClient{killErr: context.DeadlineExceeded}, wantCode: http.StatusServiceUnavailable, wantError: "unavailable"},
		{name: "daemon unavailable", action: "start", dialErr: errors.New("socket unavailable"), wantCode: http.StatusServiceUnavailable, wantError: "unavailable"},
		{name: "invalid restart state", action: "restart", client: &lifecycleClient{state: v1beta1.CellStatePending}, wantCode: http.StatusConflict, wantError: "invalid_state", wantPhase: "validation"},
		{name: "restart stop failure", action: "restart", client: &lifecycleClient{state: v1beta1.CellStateReady, stopErr: errors.New("stop failed")}, wantCode: http.StatusServiceUnavailable, wantError: "unavailable", wantPhase: "stopping"},
		{name: "start invalid state", action: "start", client: &lifecycleClient{startErr: errdefs.ErrInvalidCellState}, wantCode: http.StatusConflict, wantError: "invalid_state"},
		{name: "restart start failure", action: "restart", client: &lifecycleClient{state: v1beta1.CellStateReady, startErr: errors.New("start failed")}, wantCode: http.StatusServiceUnavailable, wantError: "unavailable", wantPhase: "starting", wantStoppedFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) {
				if tc.dialErr != nil {
					return nil, tc.dialErr
				}
				return tc.client, nil
			})
			w := postLifecycle(handler, tc.action)
			if w.Code != tc.wantCode {
				t.Fatalf("status=%d want %d body=%s", w.Code, tc.wantCode, w.Body)
			}
			var body struct {
				Error struct {
					Code         string `json:"code"`
					Phase        string `json:"phase"`
					Message      string `json:"message"`
					StoppedFirst bool   `json:"stoppedFirst"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tc.wantError || body.Error.Phase != tc.wantPhase {
				t.Fatalf("error = %+v", body.Error)
			}
			if body.Error.StoppedFirst != tc.wantStoppedFirst {
				t.Fatalf("stoppedFirst = %v, want %v", body.Error.StoppedFirst, tc.wantStoppedFirst)
			}
			if tc.wantPhase == "starting" && tc.client.stopCalls != 1 {
				t.Fatalf("start failure did not follow successful stop: stop=%d", tc.client.stopCalls)
			}
		})
	}
}

func TestCellLifecycleRejectsConcurrentOperationForSameCell(t *testing.T) {
	client := &lifecycleClient{
		state:       v1beta1.CellStateReady,
		stopEntered: make(chan struct{}, 1),
		stopRelease: make(chan struct{}),
	}
	handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- postLifecycle(handler, "stop") }()
	<-client.stopEntered
	second := postLifecycle(handler, "restart")
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "operation_in_progress") {
		t.Fatalf("second operation status=%d body=%s", second.Code, second.Body)
	}
	close(client.stopRelease)
	first := <-firstDone
	if first.Code != http.StatusOK {
		t.Fatalf("first operation status=%d body=%s", first.Code, first.Body)
	}
	if client.stopCalls != 1 || client.getCalls != 0 || client.startCalls != 0 {
		t.Fatalf(
			"calls after duplicate request: get=%d stop=%d start=%d",
			client.getCalls,
			client.stopCalls,
			client.startCalls,
		)
	}
}

func TestCellLifecycleDestructiveOperationsConflictWithActiveLifecycleOperation(t *testing.T) {
	for _, action := range []string{"kill", "delete", "purge"} {
		t.Run(action, func(t *testing.T) {
			client := &lifecycleClient{
				state:       v1beta1.CellStateReady,
				stopEntered: make(chan struct{}, 1),
				stopRelease: make(chan struct{}),
			}
			handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
			firstDone := make(chan *httptest.ResponseRecorder, 1)
			go func() { firstDone <- postLifecycle(handler, "stop") }()
			<-client.stopEntered
			second := postLifecycle(handler, action)
			if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "operation_in_progress") {
				t.Fatalf("second operation status=%d body=%s", second.Code, second.Body)
			}
			close(client.stopRelease)
			if first := <-firstDone; first.Code != http.StatusOK {
				t.Fatalf("first operation status=%d body=%s", first.Code, first.Body)
			}
			if client.stopCalls != 1 || client.killCalls != 0 || client.deleteCalls != 0 || client.purgeCalls != 0 {
				t.Fatalf("calls stop/kill/delete/purge = %d/%d/%d/%d", client.stopCalls, client.killCalls, client.deleteCalls, client.purgeCalls)
			}
		})
	}
}

func TestCellOperationLocksAreScopedToTheFullCellIdentity(t *testing.T) {
	locks := &cellOperationLocks{}
	scopeA := cellScope{realm: "r1", space: "s1", stack: "t1", cell: "c1"}
	scopeB := cellScope{realm: "r1", space: "s1", stack: "t1", cell: "c2"}
	releaseA, acquired := locks.tryAcquire(scopeA)
	if !acquired {
		t.Fatal("first cell operation was not acquired")
	}
	defer releaseA()
	if _, acquired = locks.tryAcquire(scopeA); acquired {
		t.Fatal("duplicate operation for the same cell was acquired")
	}
	releaseB, acquired := locks.tryAcquire(scopeB)
	if !acquired {
		t.Fatal("operation for a different cell was blocked")
	}
	releaseB()
}

func TestCellLifecycleRoutesKeepSameOriginProtection(t *testing.T) {
	client := &lifecycleClient{state: v1beta1.CellStateStopped}
	handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) { return client, nil })
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5129"+lifecyclePath+"start", nil)
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || client.startCalls != 0 {
		t.Fatalf("status=%d start calls=%d body=%s", w.Code, client.startCalls, w.Body)
	}
}

func TestCellLifecycleRejectsRequestBody(t *testing.T) {
	dialed := false
	handler := lifecycleHandler(t, func(context.Context) (kukeonv1.Client, error) {
		dialed = true
		return &lifecycleClient{state: v1beta1.CellStateStopped}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5129"+lifecyclePath+"start", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || dialed || !strings.Contains(w.Body.String(), "invalid_request") {
		t.Fatalf("status=%d dialed=%v body=%s", w.Code, dialed, w.Body)
	}
}

func TestLifecycleCellDocUsesAllScopeCoordinates(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5129"+lifecyclePath+"start", nil)
	req.SetPathValue("realm", "r1")
	req.SetPathValue("space", "s1")
	req.SetPathValue("stack", "t1")
	req.SetPathValue("cell", "c1")
	doc, scope, err := lifecycleCellDoc(req)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []string{scope.realm, scope.space, scope.stack, scope.cell}, []string{"r1", "s1", "t1", "c1"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("scope=%v want %v", got, want)
	}
	if doc.Spec.ID != "c1" || doc.Spec.RealmID != "r1" || doc.Spec.SpaceID != "s1" || doc.Spec.StackID != "t1" {
		t.Fatalf("cell doc scope = %+v", doc.Spec)
	}
	req.SetPathValue("cell", "../other")
	if _, _, traversalErr := lifecycleCellDoc(req); traversalErr == nil {
		t.Fatal("expected traversal cell id to be rejected")
	}
}
