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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/api"
)

const screenshotURL = "/api/realms/dev/spaces/shared/stacks/services/cells/api/screenshot"

type screenshotClient struct {
	kukeonv1.FakeClient

	cell       kukeonv1.GetCellResult
	containers []v1beta1.ContainerSpec
	listed     bool
	closed     bool
}

func (c *screenshotClient) Close() error { c.closed = true; return nil }

func (c *screenshotClient) GetCell(context.Context, v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	return c.cell, nil
}

func (c *screenshotClient) ListContainers(
	_ context.Context, realm, space, stack, cell string,
) ([]v1beta1.ContainerSpec, error) {
	c.listed = true
	if realm != "dev" || space != "shared" || stack != "services" || cell != "api" {
		return nil, fmt.Errorf("unexpected scope %s/%s/%s/%s", realm, space, stack, cell)
	}
	return c.containers, nil
}

// fakeShooter records the container it was asked for and returns res/err.
type fakeShooter struct {
	called bool
	doc    v1beta1.ContainerDoc
	res    api.ScreenshotResult
	err    error
}

func (f *fakeShooter) shoot(
	_ context.Context,
	_ kukeonv1.Client,
	doc v1beta1.ContainerDoc,
) (api.ScreenshotResult, error) {
	f.called, f.doc = true, doc
	return f.res, f.err
}

func liveCell(tty *v1beta1.CellTty) kukeonv1.GetCellResult {
	return kukeonv1.GetCellResult{
		MetadataExists:           true,
		RootContainerTaskRunning: true,
		Cell: v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: "api"},
			Spec:     v1beta1.CellSpec{RealmID: "dev", SpaceID: "shared", StackID: "services", Tty: tty},
			Status:   v1beta1.CellStatus{State: v1beta1.CellStateReady},
		},
	}
}

func container(id string, attachable, root bool) v1beta1.ContainerSpec {
	return v1beta1.ContainerSpec{ID: id, Attachable: attachable, Root: root}
}

func serveScreenshot(t *testing.T, c *screenshotClient, f *fakeShooter, url string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerScreenshotRouteWith(mux, func(context.Context) (kukeonv1.Client, error) { return c, nil }, f.shoot)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func decodeScreen(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func assertScreenError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if w.Code != status || body.Error.Code != code {
		t.Fatalf("got %d %q, want %d %q (%s)", w.Code, body.Error.Code, status, code, w.Body.String())
	}
}

func TestScreenshotOneAttachableContainer(t *testing.T) {
	c := &screenshotClient{cell: liveCell(nil), containers: []v1beta1.ContainerSpec{
		container("root", true, true), container("shell", true, false),
	}}
	f := &fakeShooter{res: api.ScreenshotResult{
		Cols: 80, Rows: 24, CursorX: 3, CursorY: 4, CursorVisible: true, AltScreen: true,
		Text: "$ ls\n", ANSI: "\x1b[0m$ ls\n",
	}}
	body := decodeScreen(t, serveScreenshot(t, c, f, screenshotURL))
	want := map[string]any{
		"container": "shell", "cols": float64(80), "rows": float64(24), "cursorX": float64(3),
		"cursorY": float64(4), "cursorVisible": true, "altScreen": true, "text": "$ ls\n",
	}
	if len(body) != len(want) {
		t.Fatalf("body %v, want exactly %v", body, want)
	}
	for key, value := range want {
		if body[key] != value {
			t.Fatalf("%s = %v, want %v", key, body[key], value)
		}
	}
	if _, ok := body["ansi"]; ok {
		t.Fatal("response must not carry the ansi rendering")
	}
	if f.doc.Metadata.Name != "shell" || f.doc.Spec.ID != "shell" || f.doc.Spec.RealmID != "dev" ||
		f.doc.Spec.SpaceID != "shared" || f.doc.Spec.StackID != "services" || f.doc.Spec.CellID != "api" {
		t.Fatalf("doc %+v", f.doc)
	}
	if !c.closed {
		t.Fatal("client was not closed")
	}
}

func TestScreenshotPrefersTtyDefault(t *testing.T) {
	c := &screenshotClient{cell: liveCell(&v1beta1.CellTty{Default: "agent"}), containers: []v1beta1.ContainerSpec{
		container("shell", true, false), container("agent", true, false),
	}}
	f := &fakeShooter{}
	if got := decodeScreen(t, serveScreenshot(t, c, f, screenshotURL))["container"]; got != "agent" {
		t.Fatalf("container %v, want agent", got)
	}
}

func TestScreenshotTtyDefaultMustBeAttachable(t *testing.T) {
	c := &screenshotClient{cell: liveCell(&v1beta1.CellTty{Default: "sidecar"}), containers: []v1beta1.ContainerSpec{
		container("sidecar", false, false), container("shell", true, false), container("agent", true, false),
	}}
	f := &fakeShooter{}
	if got := decodeScreen(t, serveScreenshot(t, c, f, screenshotURL))["container"]; got != "shell" {
		t.Fatalf("container %v, want shell", got)
	}
}

func TestScreenshotFirstAttachableWithoutDefault(t *testing.T) {
	c := &screenshotClient{cell: liveCell(nil), containers: []v1beta1.ContainerSpec{
		container("worker", false, false), container("shell", true, false), container("agent", true, false),
	}}
	f := &fakeShooter{}
	if got := decodeScreen(t, serveScreenshot(t, c, f, screenshotURL))["container"]; got != "shell" {
		t.Fatalf("container %v, want shell", got)
	}
}

func TestScreenshotExplicitContainer(t *testing.T) {
	c := &screenshotClient{cell: liveCell(nil)}
	f := &fakeShooter{}
	if got := decodeScreen(t, serveScreenshot(t, c, f, screenshotURL+"?container=shell"))["container"]; got != "shell" {
		t.Fatalf("container %v, want shell", got)
	}
	if c.listed {
		t.Fatal("ListContainers must not be called for an explicit container")
	}
	if f.doc.Spec.ID != "shell" {
		t.Fatalf("doc %+v", f.doc)
	}
}

func TestScreenshotRejectsUnsafeContainerName(t *testing.T) {
	c := &screenshotClient{cell: liveCell(nil)}
	f := &fakeShooter{}
	w := serveScreenshot(t, c, f, screenshotURL+"?container=..")
	if w.Code != http.StatusBadRequest || f.called {
		t.Fatalf("status %d, called %v", w.Code, f.called)
	}
}

func TestScreenshotNoAttachableContainer(t *testing.T) {
	c := &screenshotClient{cell: liveCell(nil), containers: []v1beta1.ContainerSpec{
		container("root", true, true), container("worker", false, false),
	}}
	f := &fakeShooter{}
	assertScreenError(t, serveScreenshot(t, c, f, screenshotURL), http.StatusNotFound, "screen_unavailable")
	if f.called {
		t.Fatal("screenshooter must not be called without an attachable container")
	}
}

func TestScreenshotCellNotLive(t *testing.T) {
	for _, state := range []v1beta1.CellState{v1beta1.CellStateStopped, v1beta1.CellStateExited} {
		cell := liveCell(nil)
		cell.Cell.Status.State = state
		c := &screenshotClient{cell: cell, containers: []v1beta1.ContainerSpec{container("shell", true, false)}}
		f := &fakeShooter{}
		assertScreenError(t, serveScreenshot(t, c, f, screenshotURL), http.StatusNotFound, "screen_unavailable")
		if f.called || c.listed {
			t.Fatalf("%s: called %v, listed %v", state.String(), f.called, c.listed)
		}
	}
}

func TestScreenshotReadyCellWithoutRootTask(t *testing.T) {
	cell := liveCell(nil)
	cell.RootContainerTaskRunning = false
	c := &screenshotClient{cell: cell}
	f := &fakeShooter{}
	assertScreenError(t, serveScreenshot(t, c, f, screenshotURL), http.StatusNotFound, "screen_unavailable")
}

func TestScreenshotMissingCell(t *testing.T) {
	c := &screenshotClient{cell: kukeonv1.GetCellResult{}}
	f := &fakeShooter{}
	assertScreenError(t, serveScreenshot(t, c, f, screenshotURL), http.StatusNotFound, "not_found")
	if f.called {
		t.Fatal("screenshooter must not be called for a missing cell")
	}
}

func TestScreenshotGateErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not attachable", errdefs.ErrAttachNotSupported, http.StatusNotFound, "screen_unavailable"},
		{"task not running", errdefs.ErrAttachTaskNotRunning, http.StatusNotFound, "screen_unavailable"},
		{"stale socket", errdefs.ErrAttachStaleSocket, http.StatusNotFound, "screen_unavailable"},
		{"container missing", errdefs.ErrContainerNotFound, http.StatusNotFound, "not_found"},
		{"permission denied", fs.ErrPermission, http.StatusForbidden, "permission_denied"},
		{"untyped", errors.New("rpc failed"), http.StatusServiceUnavailable, "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &screenshotClient{
				cell:       liveCell(nil),
				containers: []v1beta1.ContainerSpec{container("shell", true, false)},
			}
			f := &fakeShooter{err: fmt.Errorf("screenshot: %w", tc.err)}
			assertScreenError(t, serveScreenshot(t, c, f, screenshotURL), tc.status, tc.code)
		})
	}
}

func TestScreenshotRouteIsRegistered(t *testing.T) {
	c := &screenshotClient{cell: kukeonv1.GetCellResult{}}
	w := serveDaemon(t, func(context.Context) (kukeonv1.Client, error) { return c, nil }, screenshotURL)
	assertScreenError(t, w, http.StatusNotFound, "not_found")
}
