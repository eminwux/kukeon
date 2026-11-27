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
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/gorilla/websocket"
)

func TestParseListen(t *testing.T) {
	for _, tc := range []struct {
		raw, bind, host string
		loopback        bool
	}{
		{"localhost:5129", "127.0.0.1:5129", "localhost", true},
		{"localhost:0", "127.0.0.1:0", "localhost", true},
		{"127.0.0.2:80", "127.0.0.2:80", "127.0.0.2", true},
		{"[::1]:5129", "[::1]:5129", "::1", true},
		{"0.0.0.0:5129", "0.0.0.0:5129", "0.0.0.0", false},
		{"[::]:5129", "[::]:5129", "::", false},
		{"192.168.1.5:65535", "192.168.1.5:65535", "192.168.1.5", false},
	} {
		got, err := parseListen(tc.raw)
		if err != nil {
			t.Errorf("%s: %v", tc.raw, err)
			continue
		}
		if got.bind != tc.bind || got.host != tc.host || got.loopback != tc.loopback {
			t.Errorf("%s: got %+v", tc.raw, got)
		}
	}
	for _, raw := range []string{
		"", "localhost", "127.0.0.1", "::1", "::1:5129", "[::1]", ":5129",
		"example.com:5129", "localhost.evil:5129", "localhost:", "localhost:-1",
		"localhost:65536", "localhost:http", "[fe80::1%eth0]:5129",
	} {
		_, err := parseListen(raw)
		if err == nil {
			t.Errorf("accepted %q", raw)
			continue
		}
		if !errors.Is(err, errdefs.ErrConfig) {
			t.Errorf("%q: error %v is not ErrConfig", raw, err)
		}
	}
}

func TestParseAccessPolicy(t *testing.T) {
	for _, tc := range []struct{ raw, authority, origin string }{
		{"", "", ""},
		{"http://localhost:5129", "localhost:5129", "http://localhost:5129"},
		{"HTTP://LocalHost:5129", "localhost:5129", "http://localhost:5129"},
		{"https://ui.example.com", "ui.example.com", "https://ui.example.com"},
		{"https://ui.example.com:443", "ui.example.com", "https://ui.example.com"},
		{"http://ui.example.com:80", "ui.example.com", "http://ui.example.com"},
		{"http://ui.example.com:8443", "ui.example.com:8443", "http://ui.example.com:8443"},
		{"http://[::1]:5129", "[::1]:5129", "http://[::1]:5129"},
		{"http://[::1]", "[::1]", "http://[::1]"},
	} {
		got, err := parseAccessPolicy(tc.raw)
		if err != nil {
			t.Errorf("%s: %v", tc.raw, err)
			continue
		}
		if got.authority != tc.authority || got.origin != tc.origin {
			t.Errorf("%s: got %+v", tc.raw, got)
		}
	}
	for _, raw := range []string{
		"localhost:5129", "ftp://localhost", "ws://localhost:5129", "http://",
		"http://localhost:5129/", "http://localhost:5129/ui", "http://localhost:5129?x=1",
		"http://localhost:5129?", "http://localhost:5129#top", "http://localhost:5129#",
		"http://user@localhost:5129", "http://user:pw@localhost:5129", "http://localhost:",
		"http://localhost:0", "http://localhost:65536", "mailto:a@b",
	} {
		_, err := parseAccessPolicy(raw)
		if err == nil {
			t.Errorf("accepted %q", raw)
			continue
		}
		if !errors.Is(err, errdefs.ErrConfig) {
			t.Errorf("%q: error %v is not ErrConfig", raw, err)
		}
	}
}

// TestAccessPolicyMatrix covers --url set vs. unset × matching vs. foreign
// Host/Origin across static assets, API GETs, and JSON mutations.
func TestAccessPolicyMatrix(t *testing.T) {
	const local = "localhost:5129"
	routes := []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/api/realms"},
		{http.MethodGet, "/api/realms/r/spaces/s/stacks/t/cells/c/drift"},
		{http.MethodPost, "/api/cells/manual/preview"},
		{http.MethodPost, "/api/configs/from-blueprint/preview"},
		{http.MethodPost, "/api/realms/dev/registry-credentials"},
	}
	for _, tc := range []struct {
		name, url, host string
		origins         []string
		allowed         bool
	}{
		{"unset/local no origin", "", local, nil, true},
		{"unset/local same origin", "", local, []string{"http://" + local}, true},
		{"unset/foreign host", "", "192.168.1.5:5129", nil, true},
		{"unset/foreign host and origin", "", "evil.example", []string{"http://evil.example"}, true},
		{"unset/foreign origin", "", local, []string{"https://evil.example"}, true},
		{"unset/duplicate origin", "", local, []string{"http://" + local, "http://" + local}, false},
		{"set/match no origin", "http://" + local, local, nil, true},
		{"set/match origin", "http://" + local, local, []string{"http://" + local}, true},
		{"set/foreign host", "http://" + local, "192.168.1.5:5129", nil, false},
		{"set/foreign host matching origin", "http://" + local, "192.168.1.5:5129", []string{"http://" + local}, false},
		{"set/foreign origin", "http://" + local, local, []string{"http://evil.example"}, false},
		{"set/scheme mismatch", "http://" + local, local, []string{"https://" + local}, false},
		{"set/null origin", "http://" + local, local, []string{"null"}, false},
		{"set/duplicate origin", "http://" + local, local, []string{"http://" + local, "http://" + local}, false},
		{"set/proxy port", "https://ui.example.com", "ui.example.com", []string{"https://ui.example.com"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := newHandler(tc.url, "unix:///missing", func(context.Context) (kukeonv1.Client, error) {
				if !tc.allowed {
					t.Error("rejected request reached the daemon")
				}
				return nil, io.EOF
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, route := range routes {
				var body io.Reader
				if route.method == http.MethodPost {
					body = bytes.NewBufferString(`{}`)
				}
				r := httptest.NewRequest(route.method, "http://"+tc.host+route.path, body)
				for _, origin := range tc.origins {
					r.Header.Add("Origin", origin)
				}
				if route.method == http.MethodPost {
					r.Header.Set("Content-Type", "application/json")
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if forbidden := w.Code == http.StatusForbidden; forbidden == tc.allowed {
					t.Errorf(
						"%s %s: status=%d allowed=%v body=%s",
						route.method,
						route.path,
						w.Code,
						tc.allowed,
						w.Body,
					)
				}
			}
		})
	}
}

// TestAccessPolicyWebSocket drives the attach upgrade through the full
// handler: the shared policy is the only Host/Origin gate.
func TestAccessPolicyWebSocket(t *testing.T) {
	const path = "/api/realms/r/spaces/s/stacks/t/cells/c/containers/work/attach"
	for _, tc := range []struct {
		name, url, host, origin string
		allowed                 bool
	}{
		{"unset/no origin", "", "", "", true},
		{"unset/foreign origin", "", "evil.example", "http://evil.example", true},
		{"set/match", "http://ui.test:5129", "ui.test:5129", "http://ui.test:5129", true},
		{"set/match no origin", "http://ui.test:5129", "ui.test:5129", "", true},
		{"set/foreign origin", "http://ui.test:5129", "ui.test:5129", "http://evil.example", false},
		{"set/foreign host", "http://ui.test:5129", "", "http://ui.test:5129", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := newHandler(tc.url, "unix:///missing", func(context.Context) (kukeonv1.Client, error) {
				return nil, io.EOF
			})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			header := http.Header{}
			if tc.host != "" {
				header.Set("Host", tc.host)
			}
			if tc.origin != "" {
				header.Set("Origin", tc.origin)
			}
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
			ws, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, header)
			if tc.allowed {
				if err != nil {
					t.Fatalf("upgrade rejected: %v", err)
				}
				_ = ws.Close()
				return
			}
			if err == nil {
				_ = ws.Close()
				t.Fatal("upgrade accepted")
			}
			if response == nil || response.StatusCode != http.StatusForbidden {
				t.Fatalf("want 403, got response=%v err=%v", response, err)
			}
		})
	}
}

func TestRunWarnsOnNonLoopbackBind(t *testing.T) {
	for _, tc := range []struct {
		listen, url, origin string
		warn                bool
	}{
		{"127.0.0.1:0", "", "any", false},
		{"localhost:0", "http://localhost:5129", "http://localhost:5129", false},
		{"0.0.0.0:0", "", "any", true},
	} {
		t.Run(tc.listen, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stdout := make(urlWriter, 4)
			var stderr bytes.Buffer
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, tc.listen, tc.url, "", nil, stdout, &stderr)
			}()
			var lines []string
			for len(lines) < 3 {
				select {
				case line := <-stdout:
					lines = append(lines, strings.TrimSpace(line))
				case err := <-done:
					t.Fatalf("early exit: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("startup timeout")
				}
			}
			address := strings.TrimPrefix(lines[0], "Listening on http://")
			if _, port, err := net.SplitHostPort(address); err != nil || port == "0" {
				t.Fatalf("listening line %q does not carry the bound port", lines[0])
			}
			if lines[1] != "Accepted browser origin: "+tc.origin {
				t.Fatalf("origin line = %q", lines[1])
			}
			wantHint := "Login: user root; print the password with " +
				"`kuke get secret web-ui-admin --realm kuke-system -o yaml`"
			if lines[2] != wantHint {
				t.Fatalf("login hint = %q, want %q", lines[2], wantHint)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if warned := strings.Contains(stderr.String(), "serves plain HTTP"); warned != tc.warn {
				t.Fatalf("warning=%v want %v: %q", warned, tc.warn, stderr.String())
			}
			if strings.Contains(stderr.String(), "no authentication") {
				t.Fatalf("warning still claims no authentication: %q", stderr.String())
			}
		})
	}
}

func TestRunRejectsInvalidFlagsBeforeBinding(t *testing.T) {
	for _, tc := range []struct{ listen, url string }{
		{"example.com:5129", ""},
		{"localhost", ""},
		{"localhost:0", "http://localhost:5129/path"},
	} {
		err := Run(context.Background(), tc.listen, tc.url, "", nil, io.Discard, io.Discard)
		if !errors.Is(err, errdefs.ErrConfig) {
			t.Errorf("%+v: err=%v", tc, err)
		}
	}
}
