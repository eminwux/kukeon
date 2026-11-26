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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/execstream"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

const (
	execRunPath      = "/api/realms/r/spaces/s/stacks/t/cells/c/containers/work/exec"
	execTerminalPath = execRunPath + "/terminal"
	execTestTimeout  = 5 * time.Second
)

// execClient is the kukeond behind the exec routes: GetCell answers with
// cell, and ExecContainer hands back one end of a unix socket pair whose
// other end session plays.
type execClient struct {
	kukeonv1.FakeClient

	cell    kukeonv1.GetCellResult
	execErr error
	// session plays the daemon end; it owns closing nothing (the fixture
	// closes peer when it returns).
	session func(peer *net.UnixConn, req kukeonv1.ExecRequest)

	mu      sync.Mutex
	docs    []v1.ContainerDoc
	reqs    []kukeonv1.ExecRequest
	running sync.WaitGroup
}

func newExecClient(session func(peer *net.UnixConn, req kukeonv1.ExecRequest)) *execClient {
	return &execClient{cell: liveCell(nil), session: session}
}

func (c *execClient) Close() error { return nil }

func (c *execClient) GetCell(context.Context, v1.CellDoc) (kukeonv1.GetCellResult, error) {
	return c.cell, nil
}

func (c *execClient) ExecContainer(
	_ context.Context, doc v1.ContainerDoc, req kukeonv1.ExecRequest,
) (*net.UnixConn, error) {
	c.mu.Lock()
	c.docs = append(c.docs, doc)
	c.reqs = append(c.reqs, req)
	c.mu.Unlock()
	if c.execErr != nil {
		return nil, c.execErr
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var ends [2]*net.UnixConn
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "pair")
		conn, connErr := net.FileConn(f)
		_ = f.Close()
		if connErr != nil {
			return nil, connErr
		}
		ends[i] = conn.(*net.UnixConn)
	}
	c.running.Go(func() {
		defer func() { _ = ends[1].Close() }()
		_ = ends[1].SetDeadline(time.Now().Add(execTestTimeout))
		c.session(ends[1], req)
	})
	return ends[0], nil
}

func (c *execClient) requests() []kukeonv1.ExecRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]kukeonv1.ExecRequest(nil), c.reqs...)
}

func execServer(t *testing.T, c *execClient, limits execLimits) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	registerExecRoutesWith(mux, func(context.Context) (kukeonv1.Client, error) { return c, nil }, limits)
	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		server.Close()
		c.running.Wait()
	})
	return server
}

func defaultExecLimits() execLimits {
	return execLimits{output: execOutputLimit, timeout: execTestTimeout}
}

func postExec(t *testing.T, server *httptest.Server, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(server.URL+execRunPath, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decodeExecRun(t *testing.T, resp *http.Response) execRunResponse {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body %s", resp.StatusCode, raw)
	}
	var out execRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertExecError(t *testing.T, resp *http.Response, status int, code string) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("error body %q: %v", raw, err)
	}
	if resp.StatusCode != status || body.Error.Code != code {
		t.Fatalf("got %d %q (%s), want %d %q", resp.StatusCode, body.Error.Code, body.Error.Message, status, code)
	}
	return body.Error.Message
}

func TestExecRunAsContainerUser(t *testing.T) {
	client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		w := execstream.NewWriter(peer)
		_ = w.WriteData(execstream.TypeStdout, []byte("1000\n"))
		_ = w.WriteData(execstream.TypeStderr, []byte("warning\n"))
		_ = w.Exit(execstream.Exit{Code: 3})
	})
	server := execServer(t, client, defaultExecLimits())

	for _, body := range []string{`{"command":"id -u"}`, `{"command":"id -u","identity":"container"}`} {
		got := decodeExecRun(t, postExec(t, server, body))
		want := execRunResponse{Stdout: "1000\n", Stderr: "warning\n", ExitCode: 3}
		if got != want {
			t.Fatalf("%s: response = %+v, want %+v", body, got, want)
		}
	}
	for _, req := range client.requests() {
		want := kukeonv1.ExecRequest{Command: []string{"sh", "-c", "id -u"}}
		if !reflect.DeepEqual(req, want) {
			t.Fatalf("ExecRequest = %+v, want %+v (no root, no stdin, no TTY)", req, want)
		}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	doc := client.docs[0]
	want := v1.ContainerSpec{ID: "work", RealmID: "r", SpaceID: "s", StackID: "t", CellID: "c"}
	if doc.Metadata.Name != "work" || !reflect.DeepEqual(doc.Spec, want) {
		t.Fatalf("target = %q %+v", doc.Metadata.Name, doc.Spec)
	}
}

func TestExecRunRootNeedsConfirmation(t *testing.T) {
	client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		w := execstream.NewWriter(peer)
		_ = w.WriteData(execstream.TypeStdout, []byte("0\n"))
		_ = w.Exit(execstream.Exit{})
	})
	dials := 0
	mux := http.NewServeMux()
	registerExecRoutesWith(mux, func(context.Context) (kukeonv1.Client, error) {
		dials++
		return client, nil
	}, defaultExecLimits())
	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		server.Close()
		client.running.Wait()
	})

	for _, body := range []string{
		`{"command":"id -u","identity":"root"}`,
		`{"command":"id -u","identity":"root","confirmRoot":false}`,
	} {
		assertExecError(t, postExec(t, server, body), http.StatusBadRequest, "root_not_confirmed")
	}
	if dials != 0 || len(client.requests()) != 0 {
		t.Fatalf("an unconfirmed root request reached kukeond (%d dials, %d execs)", dials, len(client.requests()))
	}

	got := decodeExecRun(t, postExec(t, server, `{"command":"id -u","identity":"root","confirmRoot":true}`))
	if got.Stdout != "0\n" || got.ExitCode != 0 {
		t.Fatalf("response = %+v, want uid 0", got)
	}
	if reqs := client.requests(); len(reqs) != 1 || !reqs[0].Root {
		t.Fatalf("requests = %+v, want one root exec", reqs)
	}
}

func TestExecRunRejectsBadRequests(t *testing.T) {
	client := newExecClient(func(*net.UnixConn, kukeonv1.ExecRequest) {})
	server := execServer(t, client, defaultExecLimits())
	for name, body := range map[string]string{
		"empty command":    `{"command":"  "}`,
		"no command":       `{}`,
		"unknown identity": `{"command":"id","identity":"admin"}`,
		"unknown field":    `{"command":"id","user":"0"}`,
		"not JSON":         `id -u`,
	} {
		t.Run(name, func(t *testing.T) {
			assertExecError(t, postExec(t, server, body), http.StatusBadRequest, "invalid_request")
		})
	}
	t.Run("two documents", func(t *testing.T) {
		resp := postExec(t, server, `{"command":"id"}{"command":"id","identity":"root","confirmRoot":true}`)
		assertExecError(t, resp, http.StatusBadRequest, "invalid_request")
	})
	t.Run("not declared as JSON", func(t *testing.T) {
		resp, err := http.Post(server.URL+execRunPath, "text/plain", strings.NewReader(`{"command":"id"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		assertExecError(t, resp, http.StatusBadRequest, "invalid_request")
	})
	if len(client.requests()) != 0 {
		t.Fatalf("a rejected request reached kukeond: %+v", client.requests())
	}
}

func TestExecRunTruncatesOutput(t *testing.T) {
	const limit = 1024
	client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		w := execstream.NewWriter(peer)
		for range 64 {
			if w.WriteData(execstream.TypeStdout, bytes.Repeat([]byte("o"), 4096)) != nil {
				return
			}
		}
		_ = w.WriteData(execstream.TypeStderr, []byte("short"))
		_ = w.Exit(execstream.Exit{Code: 0})
	})
	server := execServer(t, client, execLimits{output: limit, timeout: execTestTimeout})

	got := decodeExecRun(t, postExec(t, server, `{"command":"yes"}`))
	if !got.Truncated || len(got.Stdout) != limit || got.Stderr != "short" || got.ExitCode != 0 || got.TimedOut {
		t.Fatalf("response = truncated %t, %d stdout bytes, stderr %q, exit %d; want %d bytes kept and the run drained",
			got.Truncated, len(got.Stdout), got.Stderr, got.ExitCode, limit)
	}
}

func TestExecRunTimesOut(t *testing.T) {
	closed := make(chan struct{})
	client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		_ = execstream.NewWriter(peer).WriteData(execstream.TypeStdout, []byte("started\n"))
		// The server closing its end is what kills the process in kukeond.
		if _, _, err := execstream.ReadFrame(peer); errors.Is(err, io.EOF) {
			close(closed)
		}
	})
	server := execServer(t, client, execLimits{output: execOutputLimit, timeout: 100 * time.Millisecond})

	got := decodeExecRun(t, postExec(t, server, `{"command":"sleep 600"}`))
	if !got.TimedOut || got.ExitCode != -1 || got.Stdout != "started\n" {
		t.Fatalf("response = %+v, want timedOut with the output so far and exit code -1", got)
	}
	select {
	case <-closed:
	case <-time.After(execTestTimeout):
		t.Fatal("the exec connection was not closed after the timeout")
	}
}

func TestExecRunSessionFailures(t *testing.T) {
	t.Run("the process could not be started", func(t *testing.T) {
		client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
			_ = execstream.NewWriter(peer).Exit(execstream.Exit{Code: 1, Error: "sh: not found"})
		})
		server := execServer(t, client, defaultExecLimits())
		msg := assertExecError(
			t,
			postExec(t, server, `{"command":"id"}`),
			http.StatusInternalServerError,
			"exec_failed",
		)
		if !strings.Contains(msg, "sh: not found") {
			t.Fatalf("message = %q, want the daemon's reason", msg)
		}
	})
	t.Run("the session ended without an exit", func(t *testing.T) {
		client := newExecClient(func(*net.UnixConn, kukeonv1.ExecRequest) {})
		server := execServer(t, client, defaultExecLimits())
		assertExecError(t, postExec(t, server, `{"command":"id"}`), http.StatusBadGateway, "exec_session_lost")
	})
}

func TestExecRunMapsDaemonErrors(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"unknown container": {errdefs.ErrContainerNotFound, http.StatusNotFound, "not_found"},
		"task not running":  {errdefs.ErrExecTaskNotRunning, http.StatusConflict, "not_running"},
		"invalid request":   {errdefs.ErrExecInvalidRequest, http.StatusBadRequest, "invalid_request"},
		"outdated daemon":   {errdefs.ErrDaemonMethodUnsupported, http.StatusNotImplemented, "daemon_outdated"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client := newExecClient(nil)
			client.execErr = tc.err
			server := execServer(t, client, defaultExecLimits())
			assertExecError(t, postExec(t, server, `{"command":"id"}`), tc.status, tc.code)
		})
	}
}

func stoppedCell() kukeonv1.GetCellResult {
	return kukeonv1.GetCellResult{
		MetadataExists: true,
		Cell:           v1.CellDoc{Status: v1.CellStatus{State: v1.CellStateStopped}},
	}
}

func dialExecTerminal(t *testing.T, server *httptest.Server, query string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + execTerminalPath + query
	ws, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": []string{server.URL}})
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	if err == nil {
		t.Cleanup(func() { _ = ws.Close() })
		_ = ws.SetReadDeadline(time.Now().Add(execTestTimeout))
	}
	return ws, resp, err
}

// readText reads messages until a text one arrives, collecting the binary
// ones before it.
func readText(t *testing.T, ws *websocket.Conn) (map[string]any, []byte) {
	t.Helper()
	var output []byte
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v (output so far %q)", err, output)
		}
		if kind == websocket.BinaryMessage {
			output = append(output, data...)
			continue
		}
		var msg map[string]any
		if err = json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("status message %q: %v", data, err)
		}
		return msg, output
	}
}

func TestExecStoppedCell(t *testing.T) {
	client := newExecClient(nil)
	client.cell = stoppedCell()
	server := execServer(t, client, defaultExecLimits())

	msg := assertExecError(t, postExec(t, server, `{"command":"id"}`), http.StatusConflict, "not_running")
	if !strings.Contains(msg, "start it first with `kuke start c`") {
		t.Fatalf("message = %q, want the liveness guard's pointer", msg)
	}

	ws, _, err := dialExecTerminal(t, server, "")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := readText(t, ws)
	if status["type"] != "error" || !strings.Contains(status["message"].(string), "kuke start c") {
		t.Fatalf("terminal status = %v, want the liveness error", status)
	}
	if len(client.requests()) != 0 {
		t.Fatalf("exec reached kukeond for a stopped cell: %+v", client.requests())
	}

	client.cell = kukeonv1.GetCellResult{}
	assertExecError(t, postExec(t, server, `{"command":"id"}`), http.StatusNotFound, "not_found")
}

// TestExecTerminalBridge pins the WebSocket bridge end to end: ready, bytes
// both ways, a resize, the exit message, then the close.
func TestExecTerminalBridge(t *testing.T) {
	resized := make(chan [2]uint16, 1)
	client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		w := execstream.NewWriter(peer)
		_ = w.WriteData(execstream.TypeStdout, []byte("$ "))
		for {
			typ, payload, err := execstream.ReadFrame(peer)
			if err != nil {
				return
			}
			switch typ {
			case execstream.TypeResize:
				cols, rows, _ := execstream.DecodeResize(payload)
				resized <- [2]uint16{cols, rows}
			case execstream.TypeStdin:
				_ = w.WriteData(execstream.TypeStdout, bytes.ToUpper(payload))
				if bytes.Contains(payload, []byte("exit")) {
					_ = w.Exit(execstream.Exit{Code: 4})
					return
				}
			}
		}
	})
	server := execServer(t, client, defaultExecLimits())
	ws, _, err := dialExecTerminal(t, server, "?identity=container&cols=132&rows=43")
	if err != nil {
		t.Fatal(err)
	}

	if status, output := readText(t, ws); status["type"] != "ready" || len(output) != 0 {
		t.Fatalf("first message = %v after %q, want ready before any output", status, output)
	}
	if err = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":100,"rows":50}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-resized:
		if got != [2]uint16{100, 50} {
			t.Fatalf("resize = %v, want 100x50", got)
		}
	case <-time.After(execTestTimeout):
		t.Fatal("the resize did not reach the exec connection")
	}
	if err = ws.WriteMessage(websocket.BinaryMessage, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	status, output := readText(t, ws)
	if status["type"] != "exit" || status["code"] != float64(4) {
		t.Fatalf("status = %v, want exit with code 4", status)
	}
	if string(output) != "$ EXIT\n" {
		t.Fatalf("output = %q, want the prompt and the echoed input", output)
	}
	if _, _, err = ws.ReadMessage(); err == nil {
		t.Fatal("the server kept the WebSocket open after the exit message")
	}

	want := kukeonv1.ExecRequest{
		Command: []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"},
		Stdin:   true, TTY: true, Cols: 132, Rows: 43,
	}
	if reqs := client.requests(); len(reqs) != 1 || !reflect.DeepEqual(reqs[0], want) {
		t.Fatalf("requests = %+v, want %+v", reqs, want)
	}
}

// TestExecTerminalBrowserCloseEndsSession pins that closing the panel or the
// tab closes the exec connection, which is what kills the process.
func TestExecTerminalBrowserCloseEndsSession(t *testing.T) {
	closed := make(chan error, 1)
	client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		_, _, err := execstream.ReadFrame(peer)
		closed <- err
	})
	server := execServer(t, client, defaultExecLimits())
	ws, _, err := dialExecTerminal(t, server, "")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := readText(t, ws); status["type"] != "ready" {
		t.Fatalf("status = %v, want ready", status)
	}
	_ = ws.Close()
	select {
	case err = <-closed:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("daemon end read = %v, want EOF from the closed exec connection", err)
		}
	case <-time.After(execTestTimeout):
		t.Fatal("the exec connection stayed open after the browser closed the WebSocket")
	}
	if reqs := client.requests(); len(reqs) != 1 || reqs[0].Cols != 80 || reqs[0].Rows != 24 || reqs[0].Root {
		t.Fatalf("requests = %+v, want one 80x24 exec as the container user", reqs)
	}
}

func TestExecTerminalRootNeedsConfirmation(t *testing.T) {
	client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		_ = execstream.NewWriter(peer).Exit(execstream.Exit{})
	})
	server := execServer(t, client, defaultExecLimits())

	for _, query := range []string{"?identity=root", "?identity=root&confirmRoot=0", "?identity=root&confirmRoot=true"} {
		_, resp, err := dialExecTerminal(t, server, query)
		if err == nil {
			t.Fatalf("%s: the WebSocket was upgraded without the confirmation", query)
		}
		if resp == nil {
			t.Fatalf("%s: no HTTP response: %v", query, err)
		}
		assertExecError(t, resp, http.StatusBadRequest, "root_not_confirmed")
	}
	_, resp, err := dialExecTerminal(t, server, "?identity=wheel")
	if err == nil || resp == nil {
		t.Fatalf("an unknown identity was upgraded (%v)", err)
	}
	assertExecError(t, resp, http.StatusBadRequest, "invalid_request")
	if len(client.requests()) != 0 {
		t.Fatalf("a refused terminal reached kukeond: %+v", client.requests())
	}

	ws, _, err := dialExecTerminal(t, server, "?identity=root&confirmRoot=1")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := readText(t, ws); status["type"] != "ready" {
		t.Fatalf("status = %v, want ready", status)
	}
	if status, _ := readText(t, ws); status["type"] != "exit" || status["code"] != float64(0) {
		t.Fatalf("status = %v, want exit 0", status)
	}
	if reqs := client.requests(); len(reqs) != 1 || !reqs[0].Root {
		t.Fatalf("requests = %+v, want one root exec", reqs)
	}
}

func TestExecTerminalReportsDaemonRefusal(t *testing.T) {
	client := newExecClient(nil)
	client.execErr = errdefs.ErrExecTaskNotRunning
	server := execServer(t, client, defaultExecLimits())
	ws, _, err := dialExecTerminal(t, server, "")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := readText(t, ws)
	if status["type"] != "error" || !strings.Contains(status["message"].(string), "not running") {
		t.Fatalf("status = %v, want the daemon's refusal", status)
	}
}

func TestExecRoutesAreRegistered(t *testing.T) {
	client := newExecClient(func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		_ = execstream.NewWriter(peer).Exit(execstream.Exit{})
	})
	handler, err := newHandler("", "unix:///run/kukeon/kukeond.sock",
		func(context.Context) (kukeonv1.Client, error) { return client, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.running.Wait)
	w := httptest.NewRecorder()
	run := httptest.NewRequest(http.MethodPost, execRunPath, strings.NewReader(`{"command":"true"}`))
	run.Header.Set("Content-Type", jsonContentType)
	handler.ServeHTTP(w, run)
	if w.Code != http.StatusOK {
		t.Fatalf("run route = %d %s, want 200", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, execTerminalPath+"?identity=root", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("terminal route = %d, want the 400 of an unconfirmed root request", w.Code)
	}
}

// TestAuthExecRoutesWithoutCredentials pins that both exec routes sit behind
// the login gate (issue #1652): 401, no upgrade, and kukeond is never dialed.
func TestAuthExecRoutesWithoutCredentials(t *testing.T) {
	f := newAuthFixture(t, "")
	run := f.do(
		f.handler,
		http.MethodPost,
		execRunPath,
		`{"command":"id -u","identity":"root","confirmRoot":true}`,
		nil,
	)
	if run.StatusCode != http.StatusUnauthorized {
		t.Fatalf("exec run without credentials = %d, want 401", run.StatusCode)
	}
	if code, _ := errorBody(t, run); code != "unauthenticated" {
		t.Fatalf("exec run code = %q, want unauthenticated", code)
	}

	terminal := f.do(f.handler, http.MethodGet, execTerminalPath+"?identity=root&confirmRoot=1", "",
		func(r *http.Request) {
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Sec-WebSocket-Version", "13")
			r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		})
	if terminal.StatusCode != http.StatusUnauthorized {
		t.Fatalf("exec terminal without a cookie = %d, want 401 (no upgrade)", terminal.StatusCode)
	}
	if terminal.Header.Get("Upgrade") != "" {
		t.Fatal("exec terminal without a cookie upgraded")
	}
	if n := f.client.dialCount(); n != 0 {
		t.Fatalf("dialed kukeond %d times without credentials", n)
	}
}
