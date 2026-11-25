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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
)

const testPassword = "0123456789abcdef0123456789abcdef"

// loginSecretClient serves the web-ui-admin secret through ReadSecret and
// counts every dial, so a test can tell whether a request reached kukeond.
type loginSecretClient struct {
	kukeonv1.FakeClient

	mu       sync.Mutex
	username string
	password string
	absent   bool
	readErr  error
	dials    int
	reads    int
}

func (c *loginSecretClient) ReadSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	if doc.Metadata.Name != consts.WebUIAdminSecretName || doc.Metadata.Realm != consts.KukeSystemRealmName ||
		doc.Metadata.Space != "" {
		return kukeonv1.GetSecretResult{}, fmt.Errorf("unexpected secret %+v", doc.Metadata)
	}
	if c.readErr != nil {
		return kukeonv1.GetSecretResult{}, c.readErr
	}
	if c.absent {
		return kukeonv1.GetSecretResult{}, nil
	}
	return kukeonv1.GetSecretResult{
		MetadataExists: true,
		Secret: v1beta1.SecretDoc{
			Metadata: doc.Metadata,
			Spec: v1beta1.SecretSpec{
				Type:      v1beta1.SecretTypeBasicAuth,
				BasicAuth: &v1beta1.SecretBasicAuth{Username: c.username, Password: c.password},
			},
		},
	}, nil
}

func (c *loginSecretClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	return []v1beta1.RealmDoc{}, nil
}

func (c *loginSecretClient) Close() error { return nil }

func (c *loginSecretClient) set(fn func(*loginSecretClient)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}

func (c *loginSecretClient) dialCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dials
}

func (c *loginSecretClient) dial(context.Context) (kukeonv1.Client, error) {
	c.mu.Lock()
	c.dials++
	c.mu.Unlock()
	return c, nil
}

type authFixture struct {
	t       *testing.T
	client  *loginSecretClient
	auth    *authenticator
	handler http.Handler
	clock   time.Time
}

func newAuthFixture(t *testing.T, browserURL string) *authFixture {
	t.Helper()
	client := &loginSecretClient{username: consts.WebUIAdminUser, password: testPassword}
	f := &authFixture{t: t, client: client, clock: time.Unix(1_700_000_000, 0)}
	f.handler, f.auth = f.build(browserURL, client.dial)
	return f
}

func (f *authFixture) build(browserURL string, dial Dial) (http.Handler, *authenticator) {
	f.t.Helper()
	policy, err := parseAccessPolicy(browserURL)
	if err != nil {
		f.t.Fatal(err)
	}
	catalog, err := connector.Default()
	if err != nil {
		f.t.Fatal(err)
	}
	auth := newAuthenticator(dial, policy.secure())
	auth.now = func() time.Time { return f.clock }
	handler, err := buildHandler(policy, "unix:///test", dial, catalog, auth)
	if err != nil {
		f.t.Fatal(err)
	}
	return handler, auth
}

func (f *authFixture) do(handler http.Handler, method, target, body string, mutate func(*http.Request)) *http.Response {
	f.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, "http://localhost:5129"+target, reader)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", jsonContentType)
	}
	if mutate != nil {
		mutate(request)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Result()
}

func (f *authFixture) login(handler http.Handler, username, password string) *http.Response {
	f.t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return f.do(handler, http.MethodPost, "/api/login", string(body), nil)
}

func sessionCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", sessionCookieName, resp.Header.Values("Set-Cookie"))
	return nil
}

func withCookie(value string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: value}) }
}

func withBearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func errorBody(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("error body %q: %v", raw, err)
	}
	if strings.Contains(string(raw), testPassword) {
		t.Fatalf("error body carries the password: %s", raw)
	}
	return body.Error.Code, body.Error.Message
}

func TestAuthGatedRouteWithoutCredentials(t *testing.T) {
	f := newAuthFixture(t, "")
	for _, target := range []string{"/api/realms", "/api/connection", "/api/daemon/logs"} {
		resp := f.do(f.handler, http.MethodGet, target, "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", target, resp.StatusCode)
		}
		if code, _ := errorBody(t, resp); code != "unauthenticated" {
			t.Fatalf("%s code = %q, want unauthenticated", target, code)
		}
	}
	if n := f.client.dialCount(); n != 0 {
		t.Fatalf("dialed kukeond %d times without credentials", n)
	}
	// Static assets stay public.
	if resp := f.do(f.handler, http.MethodGet, "/", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("static asset status = %d, want 200", resp.StatusCode)
	}
}

func TestAuthLogin(t *testing.T) {
	f := newAuthFixture(t, "")
	resp := f.login(f.handler, "root", testPassword)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	var info sessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.User != "root" {
		t.Fatalf("login body = %+v (%v), want user root", info, err)
	}
	cookie := sessionCookie(t, resp)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Secure {
		t.Fatalf("cookie = %+v, want HttpOnly, SameSite=Strict, Path=/, not Secure over http", cookie)
	}
	if cookie.MaxAge != sessionCookieMaxAge {
		t.Fatalf("cookie MaxAge = %d, want %d", cookie.MaxAge, sessionCookieMaxAge)
	}
	if cookie.Value == testPassword || strings.Contains(cookie.Value, testPassword) {
		t.Fatal("cookie carries the password")
	}
	if got := f.do(f.handler, http.MethodGet, "/api/realms", "", withCookie(cookie.Value)); got.StatusCode != http.StatusOK {
		t.Fatalf("gated route with the session cookie = %d, want 200", got.StatusCode)
	}
}

func TestAuthLoginWrongUserOrPassword(t *testing.T) {
	f := newAuthFixture(t, "")
	wrongUser := f.login(f.handler, "admin", testPassword)
	wrongPassword := f.login(f.handler, "root", "nope")
	for _, resp := range []*http.Response{wrongUser, wrongPassword} {
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		if len(resp.Cookies()) != 0 {
			t.Fatalf("failed login set a cookie: %v", resp.Cookies())
		}
	}
	userCode, userMessage := errorBody(t, wrongUser)
	passwordCode, passwordMessage := errorBody(t, wrongPassword)
	if userCode != "invalid_credentials" || userCode != passwordCode || userMessage != passwordMessage {
		t.Fatalf("wrong user = %q %q, wrong password = %q %q; want identical invalid_credentials",
			userCode, userMessage, passwordCode, passwordMessage)
	}
}

func TestAuthLoginSecretAbsent(t *testing.T) {
	f := newAuthFixture(t, "")
	f.client.set(func(c *loginSecretClient) { c.absent = true })
	resp := f.login(f.handler, "root", testPassword)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	code, message := errorBody(t, resp)
	if code != "login_not_initialized" || !strings.Contains(message, "sudo kuke init") {
		t.Fatalf("body = %q %q, want login_not_initialized naming sudo kuke init", code, message)
	}
}

func TestAuthLoginOutdatedDaemon(t *testing.T) {
	f := newAuthFixture(t, "")
	f.client.set(func(c *loginSecretClient) {
		c.readErr = fmt.Errorf("%w: %s", errdefs.ErrDaemonMethodUnsupported, kukeonv1.MethodReadSecret)
	})
	resp := f.login(f.handler, "root", testPassword)
	if code, _ := errorBody(t, resp); code != "daemon_outdated" {
		t.Fatalf("code = %q, want daemon_outdated", code)
	}
}

func TestAuthLoginRequiresJSON(t *testing.T) {
	f := newAuthFixture(t, "")
	for _, target := range []string{"/api/login", "/api/logout"} {
		resp := f.do(f.handler, http.MethodPost, target, `{}`, func(r *http.Request) {
			r.Header.Set("Content-Type", "text/plain")
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s with text/plain = %d, want 400", target, resp.StatusCode)
		}
	}
}

func TestAuthCookieSurvivesRestart(t *testing.T) {
	f := newAuthFixture(t, "")
	cookie := sessionCookie(t, f.login(f.handler, "root", testPassword))
	restarted, _ := f.build("", f.client.dial)
	resp := f.do(restarted, http.MethodGet, "/api/session", "", withCookie(cookie.Value))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session after restart = %d, want 200", resp.StatusCode)
	}
}

func TestAuthPasswordChangeEndsSessions(t *testing.T) {
	f := newAuthFixture(t, "")
	cookie := sessionCookie(t, f.login(f.handler, "root", testPassword))
	f.client.set(func(c *loginSecretClient) { c.password = "fedcba9876543210fedcba9876543210" })

	// Within the cache window the old read still answers.
	f.clock = f.clock.Add(credentialCacheTTL - time.Second)
	if resp := f.do(f.handler, http.MethodGet, "/api/realms", "", withCookie(cookie.Value)); resp.StatusCode != http.StatusOK {
		t.Fatalf("inside the cache window = %d, want 200", resp.StatusCode)
	}
	f.clock = f.clock.Add(2 * time.Second)
	resp := f.do(f.handler, http.MethodGet, "/api/realms", "", withCookie(cookie.Value))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old cookie after the password change = %d, want 401", resp.StatusCode)
	}

	// A deleted secret also signs the session out.
	fresh := sessionCookie(t, f.login(f.handler, "root", "fedcba9876543210fedcba9876543210"))
	f.client.set(func(c *loginSecretClient) { c.absent = true })
	f.clock = f.clock.Add(credentialCacheTTL)
	if resp = f.do(f.handler, http.MethodGet, "/api/realms", "", withCookie(fresh.Value)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie after the secret was deleted = %d, want 401", resp.StatusCode)
	}
}

func TestAuthLoginRefreshesCache(t *testing.T) {
	f := newAuthFixture(t, "")
	sessionCookie(t, f.login(f.handler, "root", testPassword))
	newPassword := "fedcba9876543210fedcba9876543210"
	f.client.set(func(c *loginSecretClient) { c.password = newPassword })
	// A login always reads fresh, so the new cookie works at once.
	cookie := sessionCookie(t, f.login(f.handler, "root", newPassword))
	if resp := f.do(f.handler, http.MethodGet, "/api/realms", "", withCookie(cookie.Value)); resp.StatusCode != http.StatusOK {
		t.Fatalf("new cookie right after login = %d, want 200", resp.StatusCode)
	}
}

func TestAuthBearer(t *testing.T) {
	f := newAuthFixture(t, "")
	if resp := f.do(f.handler, http.MethodGet, "/api/realms", "", withBearer(testPassword)); resp.StatusCode != http.StatusOK {
		t.Fatalf("bearer = %d, want 200", resp.StatusCode)
	}
	resp := f.do(f.handler, http.MethodGet, "/api/realms", "", withBearer("wrong"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong bearer = %d, want 401", resp.StatusCode)
	}
	if code, _ := errorBody(t, resp); code != "unauthenticated" {
		t.Fatalf("wrong bearer code = %q, want unauthenticated", code)
	}
}

func TestAuthCredentialsWithDaemonDown(t *testing.T) {
	f := newAuthFixture(t, "")
	down := func(context.Context) (kukeonv1.Client, error) {
		return nil, fmt.Errorf("%w: dial unix", errdefs.ErrDaemonTransport)
	}
	handler, _ := f.build("", down)
	for _, mutate := range []func(*http.Request){withBearer(testPassword), withCookie("anything")} {
		for _, target := range []string{"/api/realms", "/api/session"} {
			resp := f.do(handler, http.MethodGet, target, "", mutate)
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("%s with the daemon down = %d, want 503", target, resp.StatusCode)
			}
			if code, _ := errorBody(t, resp); code != "unavailable" {
				t.Fatalf("%s code = %q, want unavailable", target, code)
			}
		}
	}
	if resp := f.login(handler, "root", testPassword); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("login with the daemon down = %d, want 503", resp.StatusCode)
	}
}

func TestAuthAttachRouteWithoutCookie(t *testing.T) {
	f := newAuthFixture(t, "")
	resp := f.do(f.handler, http.MethodGet,
		"/api/realms/r/spaces/s/stacks/st/cells/c/containers/n/attach", "", func(r *http.Request) {
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Sec-WebSocket-Version", "13")
			r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("attach without a cookie = %d, want 401 (no upgrade)", resp.StatusCode)
	}
	if resp.Header.Get("Upgrade") != "" {
		t.Fatal("attach without a cookie upgraded")
	}
}

func TestAuthSessionRoutesFollowAccessPolicy(t *testing.T) {
	f := newAuthFixture(t, "http://localhost:5129")
	foreign := func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }
	for _, route := range []struct{ method, target string }{
		{http.MethodGet, "/api/session"},
		{http.MethodPost, "/api/login"},
		{http.MethodPost, "/api/logout"},
	} {
		resp := f.do(f.handler, route.method, route.target, `{}`, foreign)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s with a foreign Origin = %d, want 403", route.method, route.target, resp.StatusCode)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Security-Policy") == "" {
			t.Fatalf("%s %s lacks the security headers", route.method, route.target)
		}
	}
}

func TestAuthSecureCookieOverHTTPS(t *testing.T) {
	f := newAuthFixture(t, "https://kukeon.example")
	body, _ := json.Marshal(map[string]string{"username": "root", "password": testPassword})
	request := httptest.NewRequest(http.MethodPost, "https://kukeon.example/api/login", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", jsonContentType)
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	resp := recorder.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", resp.StatusCode)
	}
	if !sessionCookie(t, resp).Secure {
		t.Fatal("cookie is not Secure under an https --url")
	}
}

func TestAuthLogoutAndSession(t *testing.T) {
	f := newAuthFixture(t, "")
	if resp := f.do(f.handler, http.MethodGet, "/api/session", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session without a cookie = %d, want 401", resp.StatusCode)
	}
	cookie := sessionCookie(t, f.login(f.handler, "root", testPassword))
	resp := f.do(f.handler, http.MethodGet, "/api/session", "", withCookie(cookie.Value))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session with a cookie = %d, want 200", resp.StatusCode)
	}
	var info sessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.User != "root" {
		t.Fatalf("session body = %+v (%v), want {\"user\":\"root\"}", info, err)
	}
	if refreshed := sessionCookie(t, resp); refreshed.MaxAge != sessionCookieMaxAge {
		t.Fatalf("session did not refresh the cookie: %+v", refreshed)
	}

	resp = f.do(f.handler, http.MethodPost, "/api/logout", `{}`, withCookie(cookie.Value))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", resp.StatusCode)
	}
	if expired := sessionCookie(t, resp); expired.MaxAge >= 0 || expired.Value != "" {
		t.Fatalf("logout cookie = %+v, want expired", expired)
	}
}

func TestAuthWrongSecretTypeIsNotInitialized(t *testing.T) {
	f := newAuthFixture(t, "")
	f.client.set(func(c *loginSecretClient) { c.username = "" })
	resp := f.login(f.handler, "root", testPassword)
	if code, _ := errorBody(t, resp); code != "login_not_initialized" {
		t.Fatalf("code = %q, want login_not_initialized", code)
	}
}
