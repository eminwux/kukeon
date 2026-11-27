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
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"net/rpc/jsonrpc"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/logging"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/api"
	rpcterminal "github.com/eminwux/sbsh/pkg/rpcclient/terminal"
)

// activityClient answers GetCell and ListContainers per cell name, so one
// request can mix live, stopped and unknown cells.
type activityClient struct {
	kukeonv1.FakeClient

	cells      map[string]kukeonv1.GetCellResult
	containers map[string][]v1beta1.ContainerSpec
	dials      atomic.Int32
	closes     atomic.Int32
}

func (c *activityClient) Close() error { c.closes.Add(1); return nil }

func (c *activityClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	return c.cells[doc.Metadata.Name], nil
}

func (c *activityClient) ListContainers(
	_ context.Context, _, _, _, cell string,
) ([]v1beta1.ContainerSpec, error) {
	return c.containers[cell], nil
}

func (c *activityClient) dial(context.Context) (kukeonv1.Client, error) {
	c.dials.Add(1)
	return c, nil
}

func liveActivityCell(name string, tty *v1beta1.CellTty) kukeonv1.GetCellResult {
	cell := liveCell(tty)
	cell.Cell.Metadata.Name = name
	return cell
}

// fakeActivity answers per cell: a result, an error, or a hang until the
// request's per-cell deadline. It records the peak number of reads in
// flight.
type fakeActivity struct {
	results map[string]api.ActivityResult
	errs    map[string]error
	hang    map[string]bool
	delay   time.Duration

	mu       sync.Mutex
	docs     []v1beta1.ContainerDoc
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (f *fakeActivity) read(
	ctx context.Context,
	_ kukeonv1.Client,
	doc v1beta1.ContainerDoc,
) (api.ActivityResult, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		peak := f.peak.Load()
		if n <= peak || f.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	f.mu.Lock()
	f.docs = append(f.docs, doc)
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.hang[doc.Spec.CellID] {
		<-ctx.Done()
		return api.ActivityResult{}, ctx.Err()
	}
	if err := f.errs[doc.Spec.CellID]; err != nil {
		return api.ActivityResult{}, err
	}
	return f.results[doc.Spec.CellID], nil
}

func activityURL(cells ...string) string {
	query := url.Values{}
	for _, cell := range cells {
		query.Add("cell", cell)
	}
	return "/api/terminal-activity?" + query.Encode()
}

func serveActivity(t *testing.T, c *activityClient, f *fakeActivity, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerTerminalActivityRouteWith(mux, c.dial, f.read)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func decodeActivity(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Cells []map[string]any `json:"cells"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Cells
}

func scopeOf(t *testing.T, cell map[string]any) string {
	t.Helper()
	raw, ok := cell["scope"].([]any)
	if !ok {
		t.Fatalf("scope %v", cell["scope"])
	}
	parts := make([]string, len(raw))
	for i, part := range raw {
		parts[i], _ = part.(string)
	}
	return strings.Join(parts, "/")
}

func activityStarted() time.Time { return time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC) }

func TestTerminalActivityTwoLiveCellsInRequestOrder(t *testing.T) {
	c := &activityClient{
		cells: map[string]kukeonv1.GetCellResult{
			"agent": liveActivityCell("agent", &v1beta1.CellTty{Default: "main"}),
			"shell": liveActivityCell("shell", nil),
		},
		containers: map[string][]v1beta1.ContainerSpec{
			"agent": {container("root", true, true), container("side", true, false), container("main", true, false)},
			"shell": {container("root", true, true), container("sh", true, false)},
		},
	}
	f := &fakeActivity{results: map[string]api.ActivityResult{
		"agent": {StartedAt: activityStarted(), BytesOut: 48213, BytesIn: 311, IdleFor: 12840 * time.Millisecond},
		"shell": {StartedAt: activityStarted().Add(time.Hour), BytesOut: 7, BytesIn: 0, IdleFor: 0},
	}}
	cells := decodeActivity(
		t,
		serveActivity(t, c, f, activityURL("dev/shared/services/shell", "dev/shared/services/agent")),
	)
	if len(cells) != 2 {
		t.Fatalf("cells %v", cells)
	}
	want := []map[string]any{
		{
			"container": "sh",
			"startedAt": "2026-11-26T11:00:00Z",
			"bytesOut":  float64(7),
			"bytesIn":   float64(0),
			"idleMs":    float64(0),
		},
		{
			"container": "main",
			"startedAt": "2026-11-26T10:00:00Z",
			"bytesOut":  float64(48213),
			"bytesIn":   float64(311),
			"idleMs":    float64(12840),
		},
	}
	for i, scope := range []string{"dev/shared/services/shell", "dev/shared/services/agent"} {
		if got := scopeOf(t, cells[i]); got != scope {
			t.Fatalf("cells[%d] scope %s, want %s", i, got, scope)
		}
		if len(cells[i]) != len(want[i])+1 {
			t.Fatalf("cells[%d] = %v, want exactly scope and %v", i, cells[i], want[i])
		}
		for key, value := range want[i] {
			if cells[i][key] != value {
				t.Fatalf("cells[%d].%s = %v, want %v", i, key, cells[i][key], value)
			}
		}
	}
	for _, doc := range f.docs {
		if doc.Spec.RealmID != "dev" || doc.Spec.SpaceID != "shared" || doc.Spec.StackID != "services" ||
			doc.Metadata.Name != doc.Spec.ID {
			t.Fatalf("doc %+v", doc)
		}
	}
	if c.dials.Load() != 2 || c.closes.Load() != 2 {
		t.Fatalf("dials %d closes %d, want one client per cell, each closed", c.dials.Load(), c.closes.Load())
	}
}

func TestTerminalActivityUnavailableCells(t *testing.T) {
	stopped := liveActivityCell("stopped", nil)
	stopped.Cell.Status.State = v1beta1.CellStateStopped
	noRootTask := liveActivityCell("gone", nil)
	noRootTask.RootContainerTaskRunning = false
	c := &activityClient{
		cells: map[string]kukeonv1.GetCellResult{
			"live":     liveActivityCell("live", nil),
			"stopped":  stopped,
			"gone":     noRootTask,
			"worker":   liveActivityCell("worker", nil),
			"old":      liveActivityCell("old", nil),
			"stale":    liveActivityCell("stale", nil),
			"notattch": liveActivityCell("notattch", nil),
			"halted":   liveActivityCell("halted", nil),
			"broken":   liveActivityCell("broken", nil),
		},
		containers: map[string][]v1beta1.ContainerSpec{
			"live":     {container("sh", true, false)},
			"stopped":  {container("sh", true, false)},
			"worker":   {container("root", true, true), container("job", false, false)},
			"old":      {container("sh", true, false)},
			"stale":    {container("sh", true, false)},
			"notattch": {container("sh", true, false)},
			"halted":   {container("sh", true, false)},
			"broken":   {container("sh", true, false)},
		},
	}
	f := &fakeActivity{
		results: map[string]api.ActivityResult{"live": {StartedAt: activityStarted(), BytesOut: 1}},
		errs: map[string]error{
			"old": fmt.Errorf(
				"Activity call failed: %w",
				rpc.ServerError("rpc: can't find method TerminalController.Activity"),
			),
			"stale":    fmt.Errorf("connect: %w", errdefs.ErrAttachStaleSocket),
			"notattch": fmt.Errorf("connect: %w", errdefs.ErrAttachNotSupported),
			"halted":   fmt.Errorf("connect: %w", errdefs.ErrAttachTaskNotRunning),
			"broken":   errors.New("rpc failed"),
		},
	}
	order := []struct{ cell, want string }{
		{"live", ""},
		{"stopped", activityNotLive},
		{"missing", activityNotFound},
		{"gone", activityNotLive},
		{"worker", activityNotLive},
		{"old", activityUnsupported},
		{"stale", activityNotLive},
		{"notattch", activityNotLive},
		{"halted", activityNotLive},
		{"broken", activityError},
	}
	params := make([]string, len(order))
	for i, o := range order {
		params[i] = "dev/shared/services/" + o.cell
	}
	cells := decodeActivity(t, serveActivity(t, c, f, activityURL(params...)))
	if len(cells) != len(order) {
		t.Fatalf("cells %v", cells)
	}
	for i, o := range order {
		cell := cells[i]
		if got := scopeOf(t, cell); got != params[i] {
			t.Fatalf("cells[%d] scope %s, want %s", i, got, params[i])
		}
		if o.want == "" {
			if _, ok := cell["unavailable"]; ok || cell["bytesOut"] != float64(1) {
				t.Fatalf("%s = %v, want counters", o.cell, cell)
			}
			continue
		}
		if cell["unavailable"] != o.want || len(cell) != 2 {
			t.Fatalf("%s = %v, want only scope and unavailable %q", o.cell, cell, o.want)
		}
	}
}

func TestTerminalActivityHangingCellTimesOut(t *testing.T) {
	c := &activityClient{
		cells: map[string]kukeonv1.GetCellResult{
			"slow": liveActivityCell("slow", nil),
			"fast": liveActivityCell("fast", nil),
		},
		containers: map[string][]v1beta1.ContainerSpec{
			"slow": {container("sh", true, false)},
			"fast": {container("sh", true, false)},
		},
	}
	f := &fakeActivity{
		hang:    map[string]bool{"slow": true},
		results: map[string]api.ActivityResult{"fast": {StartedAt: activityStarted(), BytesOut: 3}},
	}
	started := time.Now()
	cells := decodeActivity(
		t,
		serveActivity(t, c, f, activityURL("dev/shared/services/slow", "dev/shared/services/fast")),
	)
	elapsed := time.Since(started)
	if cells[0]["unavailable"] != activityError {
		t.Fatalf("slow cell %v, want unavailable error", cells[0])
	}
	if cells[1]["bytesOut"] != float64(3) {
		t.Fatalf("fast cell %v, want its counters", cells[1])
	}
	if elapsed < activityCellTimeout || elapsed > activityCellTimeout+time.Second {
		t.Fatalf("request took %s, want about the %s cell timeout", elapsed, activityCellTimeout)
	}
}

func TestTerminalActivityAtMostEightInFlight(t *testing.T) {
	c := &activityClient{cells: map[string]kukeonv1.GetCellResult{}, containers: map[string][]v1beta1.ContainerSpec{}}
	params := make([]string, maxActivityCells)
	for i := range params {
		name := fmt.Sprintf("c%d", i)
		c.cells[name] = liveActivityCell(name, nil)
		c.containers[name] = []v1beta1.ContainerSpec{container("sh", true, false)}
		params[i] = "dev/shared/services/" + name
	}
	f := &fakeActivity{delay: 20 * time.Millisecond, results: map[string]api.ActivityResult{}}
	cells := decodeActivity(t, serveActivity(t, c, f, activityURL(params...)))
	if len(cells) != maxActivityCells {
		t.Fatalf("%d cells, want %d", len(cells), maxActivityCells)
	}
	if peak := f.peak.Load(); peak != activityConcurrency {
		t.Fatalf("peak in-flight reads %d, want %d", peak, activityConcurrency)
	}
}

func TestTerminalActivityRejectsBadRequests(t *testing.T) {
	tooMany := make([]string, maxActivityCells+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("dev/shared/services/c%d", i)
	}
	cases := map[string]string{
		"101 cells":     activityURL(tooMany...),
		"three segment": activityURL("dev/shared/services"),
		"five segment":  activityURL("dev/shared/services/api/extra"),
		"empty segment": activityURL("dev//services/api"),
		"dot segment":   activityURL("dev/shared/../api"),
		"unsafe mixed":  activityURL("dev/shared/services/api", `dev/shared/services/a\\b`),
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			c := &activityClient{}
			f := &fakeActivity{}
			assertScreenError(t, serveActivity(t, c, f, target), http.StatusBadRequest, codeInvalidRequest)
			if c.dials.Load() != 0 || len(f.docs) != 0 {
				t.Fatalf("dialed %d, read %d on a rejected request", c.dials.Load(), len(f.docs))
			}
		})
	}
}

func TestTerminalActivityNoCells(t *testing.T) {
	c := &activityClient{}
	w := serveActivity(t, c, &fakeActivity{}, "/api/terminal-activity")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"cells":[]}` {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestTerminalActivityRouteIsRegistered(t *testing.T) {
	c := &activityClient{}
	w := serveDaemon(t, c.dial, activityURL("dev/shared/services/missing"))
	cells := decodeActivity(t, w)
	if len(cells) != 1 || cells[0]["unavailable"] != activityNotFound {
		t.Fatalf("cells %v", cells)
	}
}

func TestTerminalActivityBehindLoginAndAccessPolicy(t *testing.T) {
	f := newAuthFixture(t, "http://localhost:5129")
	target := activityURL("dev/shared/services/api")
	resp := f.do(f.handler, http.MethodGet, target, "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without credentials = %d, want 401", resp.StatusCode)
	}
	if code, _ := errorBody(t, resp); code != "unauthenticated" {
		t.Fatalf("code %q, want unauthenticated", code)
	}
	resp = f.do(f.handler, http.MethodGet, target, "", func(r *http.Request) {
		r.Header.Set("Origin", "http://evil.example")
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Origin = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if n := f.client.dialCount(); n != 0 {
		t.Fatalf("dialed kukeond %d times", n)
	}
}

// currentTerminal is an sbsh terminal that serves Activity; it records the
// methods it was called with, so the test can tell the route never attaches.
type currentTerminal struct {
	mu    sync.Mutex
	calls []string
}

func (t *currentTerminal) Activity(_ *api.ActivityArgs, reply *api.ActivityResult) error {
	t.mu.Lock()
	t.calls = append(t.calls, "Activity")
	t.mu.Unlock()
	*reply = api.ActivityResult{StartedAt: activityStarted(), BytesOut: 42, BytesIn: 2, IdleFor: 3 * time.Second}
	return nil
}

func (t *currentTerminal) Attach(_ *api.Empty, _ *api.Empty) error {
	t.mu.Lock()
	t.calls = append(t.calls, "Attach")
	t.mu.Unlock()
	return nil
}

// legacyTerminal is an sbsh terminal from before the Activity RPC.
type legacyTerminal struct{}

func (legacyTerminal) Ping(_ *api.Empty, _ *api.Empty) error { return nil }

// terminalServer serves receiver as sbsh's terminal service over JSON-RPC,
// the codec rpcterminal's plain calls use, and returns a client for it.
func terminalServer(t *testing.T, receiver any) rpcterminal.Client {
	t.Helper()
	srv := rpc.NewServer()
	if err := srv.RegisterName(api.TerminalService, receiver); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go srv.ServeCodec(jsonrpc.NewServerCodec(conn))
		}
	}()
	return rpcterminal.NewWithDialer(func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", ln.Addr().String())
	}, logging.NewNoopLogger())
}

func TestTerminalActivityRPCAgainstCurrentTerminal(t *testing.T) {
	term := &currentTerminal{}
	res, err := terminalActivityRPC(context.Background(), terminalServer(t, term))
	if err != nil {
		t.Fatal(err)
	}
	if !res.StartedAt.Equal(activityStarted()) || res.BytesOut != 42 || res.BytesIn != 2 ||
		res.IdleFor != 3*time.Second {
		t.Fatalf("result %+v", res)
	}
	if strings.Join(term.calls, ",") != "Activity" {
		t.Fatalf("terminal calls %v, want Activity only (never Attach)", term.calls)
	}
}

func TestTerminalActivityRPCAgainstLegacyTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), activityCellTimeout)
	defer cancel()
	_, err := terminalActivityRPC(ctx, terminalServer(t, legacyTerminal{}))
	if err == nil {
		t.Fatal("legacy terminal answered Activity")
	}
	if got := activityUnavailable(err); got != activityUnsupported {
		t.Fatalf("unavailable %q for %v, want %q", got, err, activityUnsupported)
	}
}

func TestActivityUnavailableMapping(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("get: %w", errdefs.ErrCellNotFound), activityNotFound},
		{fmt.Errorf("%w: no attachable container", errScreenUnavailable), activityNotLive},
		{context.DeadlineExceeded, activityError},
		{rpc.ServerError("cell exploded"), activityError},
	}
	for _, tc := range cases {
		if got := activityUnavailable(tc.err); got != tc.want {
			t.Fatalf("activityUnavailable(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
