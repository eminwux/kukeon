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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

type daemonClient struct {
	kukeonv1.FakeClient

	version string
	pingErr error
	req     kukeonv1.DaemonLogRequest
	logs    kukeonv1.DaemonLogResult
	logErr  error
	closed  bool
}

func (c *daemonClient) Close() error { c.closed = true; return nil }

func (c *daemonClient) PingVersion(context.Context) (string, error) { return c.version, c.pingErr }

func (c *daemonClient) ReadDaemonLogs(
	_ context.Context, req kukeonv1.DaemonLogRequest,
) (kukeonv1.DaemonLogResult, error) {
	c.req = req
	return c.logs, c.logErr
}

func serveDaemon(t *testing.T, dial Dial, url string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := newHandler("http://localhost:5129", "unix:///run/kukeon/kukeond.sock", dial)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+url, nil))
	return w
}

func clientDial(c *daemonClient) Dial {
	return func(context.Context) (kukeonv1.Client, error) { return c, nil }
}

func decodeHealth(t *testing.T, w *httptest.ResponseRecorder) daemonHealth {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("health status %d: %s", w.Code, w.Body)
	}
	var health daemonHealth
	if err := json.NewDecoder(w.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	return health
}

func TestDaemonHealthReachableReportsDaemonVersionAndSocket(t *testing.T) {
	c := &daemonClient{version: "v1.2.3"}
	health := decodeHealth(t, serveDaemon(t, clientDial(c), "/api/daemon/health"))
	if health.Status != healthReachable || health.DaemonVersion != "v1.2.3" || health.Error != nil {
		t.Fatalf("got %+v", health)
	}
	if health.Endpoint != "unix:///run/kukeon/kukeond.sock" || health.CheckedAt.IsZero() || health.ServerVersion == "" {
		t.Fatalf("missing connection fields: %+v", health)
	}
	if !c.closed {
		t.Fatal("client was not closed")
	}
}

func TestDaemonHealthClassifiesFailures(t *testing.T) {
	opErr := &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	permErr := &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", syscall.EACCES)}
	cases := []struct {
		name    string
		dialErr error
		pingErr error
		want    string
	}{
		{"permission on dial", permErr, nil, healthPermissionDenied},
		{"wire permission", nil, fmt.Errorf("ping: %w", fs.ErrPermission), healthPermissionDenied},
		{"refused", opErr, nil, healthUnavailable},
		{"missing socket", nil, fmt.Errorf("dial: %w", os.ErrNotExist), healthUnavailable},
		{"timeout", nil, context.DeadlineExceeded, healthUnavailable},
		{"outdated", nil, errdefs.ErrDaemonMethodUnsupported, healthOutdated},
		{"answered with error", nil, errors.New("ping handler failed"), healthFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &daemonClient{pingErr: tc.pingErr}
			dial := func(context.Context) (kukeonv1.Client, error) {
				if tc.dialErr != nil {
					return nil, tc.dialErr
				}
				return c, nil
			}
			health := decodeHealth(t, serveDaemon(t, dial, "/api/daemon/health"))
			if health.Status != tc.want || health.Error == nil || health.Error.Code != tc.want ||
				health.DaemonVersion != "" {
				t.Fatalf("got %+v", health)
			}
		})
	}
}

func TestDaemonLogsProxiesBoundedRequest(t *testing.T) {
	c := &daemonClient{logs: kukeonv1.DaemonLogResult{
		Source: "/opt/kukeon/log", Data: "a\n", Start: 3, Next: 5, Size: 9, More: true,
	}}
	w := serveDaemon(t, clientDial(c), "/api/daemon/logs?offset=3&maxBytes=4096")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var chunk map[string]any
	if err := json.NewDecoder(w.Body).Decode(&chunk); err != nil {
		t.Fatal(err)
	}
	if chunk["data"] != "a\n" || chunk["next"] != float64(5) || chunk["more"] != true ||
		chunk["source"] != "/opt/kukeon/log" {
		t.Fatalf("chunk %v", chunk)
	}
	if c.req != (kukeonv1.DaemonLogRequest{Offset: 3, MaxBytes: 4096}) || !c.closed {
		t.Fatalf("req %+v closed=%v", c.req, c.closed)
	}

	_ = serveDaemon(t, clientDial(c), "/api/daemon/logs")
	if c.req != (kukeonv1.DaemonLogRequest{Offset: -1, MaxBytes: kukeonv1.DaemonLogDefaultMaxBytes}) {
		t.Fatalf("default req %+v", c.req)
	}
}

func TestDaemonLogsRejectsInvalidBounds(t *testing.T) {
	for _, query := range []string{
		"offset=-2", "offset=x", "maxBytes=0", fmt.Sprintf("maxBytes=%d", kukeonv1.DaemonLogMaxBytes+1),
	} {
		c := &daemonClient{}
		w := serveDaemon(t, clientDial(c), "/api/daemon/logs?"+query)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", query, w.Code)
		}
		if c.req != (kukeonv1.DaemonLogRequest{}) {
			t.Fatalf("%s: daemon was called with %+v", query, c.req)
		}
	}
}

func TestDaemonLogsErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: no log file", errdefs.ErrDaemonLogUnavailable), http.StatusNotFound, "log_unavailable"},
		{fmt.Errorf("read: %w", fs.ErrPermission), http.StatusForbidden, "permission_denied"},
		{
			fmt.Errorf("%w: KukeonV1.ReadDaemonLogs", errdefs.ErrDaemonMethodUnsupported),
			http.StatusNotImplemented,
			"daemon_outdated",
		},
		{errors.New("connection reset"), http.StatusServiceUnavailable, "unavailable"},
	}
	for _, tc := range cases {
		w := serveDaemon(t, clientDial(&daemonClient{logErr: tc.err}), "/api/daemon/logs")
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || body.Error.Code != tc.code || body.Error.Message != tc.err.Error() {
			t.Fatalf("%v: status %d body %+v", tc.err, w.Code, body)
		}
	}
}
