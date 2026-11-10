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

	mu          sync.Mutex
	cell        v1beta1.CellDoc
	state       v1beta1.CellState
	getErr      error
	startErr    error
	stopErr     error
	startCalls  int
	stopCalls   int
	getCalls    int
	closed      bool
	received    []v1beta1.CellDoc
	stopEntered chan struct{}
	stopRelease chan struct{}
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
