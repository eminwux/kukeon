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

package cloudlink

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testUserHeader = "X-Test-User"

// testRelay starts the reference relay over TLS and returns it, its URL and
// the PEM of its server certificate.
func testRelay(t *testing.T) (*Relay, *httptest.Server, string) {
	t.Helper()
	relay, err := NewRelay()
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	relay.Authenticate = HeaderAuthenticator(testUserHeader)
	relay.Authorize = AllowAnyHost
	srv := httptest.NewUnstartedServer(relay.Handler())
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	relay.ConfigureTLS(srv.TLS)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	return relay, srv, ca
}

// hostHandler is a stand-in for kuke serve's link handler: it verifies the
// identity and serves a few routes.
type hostHandler struct {
	pub    ed25519.PublicKey
	hostID string

	mu      sync.Mutex
	users   []string
	release chan struct{}
	started chan struct{}
}

func (h *hostHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, err := VerifyRequest(r, h.pub, h.hostID, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	h.mu.Lock()
	h.users = append(h.users, user)
	h.mu.Unlock()
	switch r.URL.Path {
	case "/api/hello":
		_, _ = io.WriteString(w, "hello "+user+" "+r.URL.RawQuery)
	case "/api/slow":
		close(h.started)
		select {
		case <-h.release:
		case <-r.Context().Done():
		}
	case "/api/echo":
		conn, upErr := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if upErr != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}
			if conn.WriteMessage(kind, append([]byte("echo:"), data...)) != nil {
				return
			}
		}
	default:
		http.NotFound(w, r)
	}
}

func enrollTestHost(t *testing.T, relay *Relay, srv *httptest.Server, ca string) *Credential {
	t.Helper()
	cred, err := Enroll(context.Background(), EnrollOptions{
		RelayURL: srv.URL, Token: relay.NewEnrollmentToken(0), Hostname: "test", ServerCA: ca,
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return cred
}

func startTestClient(t *testing.T, cred *Credential, handler http.Handler) (*[]State, *sync.Mutex) {
	t.Helper()
	tlsConfig, err := cred.ClientTLSConfig()
	if err != nil {
		t.Fatalf("ClientTLSConfig: %v", err)
	}
	var mu sync.Mutex
	states := &[]State{}
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		URL: cred.LinkURL, TLSConfig: tlsConfig, Handler: handler,
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		OnState: func(s State) {
			mu.Lock()
			*states = append(*states, s)
			mu.Unlock()
		},
	}
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if runErr := <-done; runErr != nil {
			t.Errorf("Run: %v", runErr)
		}
	})
	return states, &mu
}

func waitConnected(t *testing.T, relay *Relay, hostID string) {
	t.Helper()
	select {
	case <-relay.WaitConnected(hostID):
	case <-time.After(10 * time.Second):
		t.Fatalf("host %s did not connect", hostID)
	}
}

func relayGet(t *testing.T, srv *httptest.Server, path, user string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if user != "" {
		req.Header.Set(testUserHeader, user)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestRelayProxiesHTTPAndWebSocketOverLink(t *testing.T) {
	relay, srv, ca := testRelay(t)
	cred := enrollTestHost(t, relay, srv, ca)
	if cred.LinkURL != "wss://"+strings.TrimPrefix(srv.URL, "https://")+LinkPath {
		t.Fatalf("LinkURL = %q", cred.LinkURL)
	}
	pub, _ := cred.IdentityPublicKey()
	host := &hostHandler{pub: pub, hostID: cred.HostID}
	startTestClient(t, cred, host)
	waitConnected(t, relay, cred.HostID)

	status, body := relayGet(t, srv, HostsPath+cred.HostID+"/api/hello?x=1%202", "alice")
	if status != http.StatusOK || body != "hello alice x=1%202" {
		t.Fatalf("GET through relay = %d %q", status, body)
	}
	if status, _ = relayGet(t, srv, HostsPath+cred.HostID+"/api/hello", ""); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET = %d, want 401", status)
	}
	if status, _ = relayGet(t, srv, HostsPath+cred.HostID+"/other", "alice"); status != http.StatusNotFound {
		t.Fatalf("non-/api GET = %d, want 404", status)
	}
	if status, _ = relayGet(t, srv, HostsPath+"h-unknown/api/hello", "alice"); status != http.StatusServiceUnavailable {
		t.Fatalf("GET to an offline host = %d, want 503", status)
	}

	dialer := websocket.Dialer{TLSClientConfig: srv.Client().Transport.(*http.Transport).TLSClientConfig}
	wsURL := "wss://" + strings.TrimPrefix(srv.URL, "https://") + HostsPath + cred.HostID + "/api/echo"
	conn, resp, err := dialer.Dial(wsURL, http.Header{testUserHeader: {"bob"}})
	if err != nil {
		t.Fatalf("WebSocket through relay: %v", err)
	}
	_ = resp.Body.Close()
	defer conn.Close()
	for _, msg := range []string{"one", strings.Repeat("x", 3*Window)} {
		if err = conn.WriteMessage(websocket.BinaryMessage, []byte(msg)); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, got, readErr := conn.ReadMessage()
		if readErr != nil || string(got) != "echo:"+msg {
			t.Fatalf("echo of %d bytes: got %d bytes, %v", len(msg), len(got), readErr)
		}
	}
	host.mu.Lock()
	users := strings.Join(host.users, ",")
	host.mu.Unlock()
	if users != "alice,bob" {
		t.Fatalf("host saw users %q, want alice,bob", users)
	}
}

func TestRelayRefusesUserNotAuthorizedForHost(t *testing.T) {
	relay, srv, ca := testRelay(t)
	alice := enrollTestHost(t, relay, srv, ca)
	bob := enrollTestHost(t, relay, srv, ca)
	relay.Authorize = func(user, hostID string) error {
		if user == "alice" && hostID == alice.HostID {
			return nil
		}
		return errors.New("user " + user + " may not reach host " + hostID)
	}
	for _, cred := range []*Credential{alice, bob} {
		pub, _ := cred.IdentityPublicKey()
		startTestClient(t, cred, &hostHandler{pub: pub, hostID: cred.HostID})
		waitConnected(t, relay, cred.HostID)
	}

	if status, body := relayGet(t, srv, HostsPath+alice.HostID+"/api/hello", "alice"); status != http.StatusOK {
		t.Fatalf("GET to the user's own host = %d %q, want 200", status, body)
	}
	status, body := relayGet(t, srv, HostsPath+bob.HostID+"/api/hello", "alice")
	if status != http.StatusForbidden || !strings.Contains(body, "forbidden") {
		t.Fatalf("GET to another user's host = %d %q, want 403 forbidden", status, body)
	}
	// An unauthorized user cannot tell an offline host from a forbidden one.
	if status, _ = relayGet(t, srv, HostsPath+"h-unknown/api/hello", "alice"); status != http.StatusForbidden {
		t.Fatalf("GET to an unauthorized offline host = %d, want 403", status)
	}

	dialer := websocket.Dialer{TLSClientConfig: srv.Client().Transport.(*http.Transport).TLSClientConfig}
	wsURL := "wss://" + strings.TrimPrefix(srv.URL, "https://") + HostsPath + bob.HostID + "/api/echo"
	conn, resp, err := dialer.Dial(wsURL, http.Header{testUserHeader: {"alice"}})
	if err == nil {
		_ = conn.Close()
		t.Fatal("a WebSocket to another user's host was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("WebSocket to another user's host: resp=%v err=%v, want 403", resp, err)
	}
	_ = resp.Body.Close()
}

func TestRelayWithoutAuthorizeRefusesProxy(t *testing.T) {
	relay, srv, ca := testRelay(t)
	relay.Authorize = nil
	cred := enrollTestHost(t, relay, srv, ca)
	pub, _ := cred.IdentityPublicKey()
	startTestClient(t, cred, &hostHandler{pub: pub, hostID: cred.HostID})
	waitConnected(t, relay, cred.HostID)

	status, body := relayGet(t, srv, HostsPath+cred.HostID+"/api/hello", "alice")
	if status != http.StatusNotImplemented || !strings.Contains(body, "no_authorizer") {
		t.Fatalf("GET through a relay without Authorize = %d %q, want 501 no_authorizer", status, body)
	}
}

func TestLinkDropFailsInFlightAndReconnects(t *testing.T) {
	relay, srv, ca := testRelay(t)
	cred := enrollTestHost(t, relay, srv, ca)
	pub, _ := cred.IdentityPublicKey()
	host := &hostHandler{pub: pub, hostID: cred.HostID, release: make(chan struct{}), started: make(chan struct{})}
	states, mu := startTestClient(t, cred, host)
	waitConnected(t, relay, cred.HostID)

	type result struct {
		status int
		body   string
	}
	inflight := make(chan result, 1)
	go func() {
		status, body := relayGet(t, srv, HostsPath+cred.HostID+"/api/slow", "alice")
		inflight <- result{status, body}
	}()
	select {
	case <-host.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the slow request never reached the host")
	}
	if !relay.Disconnect(cred.HostID) {
		t.Fatal("Disconnect found no link")
	}
	select {
	case got := <-inflight:
		if got.status != http.StatusBadGateway || !strings.Contains(got.body, "link_failed") {
			t.Fatalf("in-flight request after the drop = %d %q, want 502 link_failed", got.status, got.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the in-flight request hung after the link dropped")
	}

	waitConnected(t, relay, cred.HostID)
	if status, body := relayGet(t, srv, HostsPath+cred.HostID+"/api/hello", "carol"); status != http.StatusOK {
		t.Fatalf("GET after reconnect = %d %q", status, body)
	}
	mu.Lock()
	defer mu.Unlock()
	var seen []string
	for _, s := range *states {
		seen = append(seen, s.Status)
	}
	if !strings.Contains(strings.Join(seen, ","), "connected,disconnected,connecting,connected") {
		t.Fatalf("states = %v, want a connected → disconnected → connecting → connected cycle", seen)
	}
}

func TestEnrollmentTokenIsOneTime(t *testing.T) {
	relay, srv, ca := testRelay(t)
	token := relay.NewEnrollmentToken(0)
	opts := EnrollOptions{RelayURL: srv.URL, Token: token, ServerCA: ca}
	if _, err := Enroll(context.Background(), opts); err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	_, err := Enroll(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("second enroll with the same token = %v, want a refusal", err)
	}
	expired := relay.NewEnrollmentToken(time.Nanosecond)
	time.Sleep(time.Millisecond)
	if _, err = Enroll(context.Background(), EnrollOptions{RelayURL: srv.URL, Token: expired, ServerCA: ca}); err == nil {
		t.Fatal("enroll with an expired token succeeded")
	}
	if _, err = Enroll(context.Background(), EnrollOptions{RelayURL: "http://relay.invalid", Token: token}); err == nil {
		t.Fatal("enroll over plain http succeeded")
	}
}

func TestLinkRequiresClientCertificate(t *testing.T) {
	_, srv, _ := testRelay(t)
	dialer := websocket.Dialer{TLSClientConfig: srv.Client().Transport.(*http.Transport).TLSClientConfig}
	_, resp, err := dialer.Dial("wss://"+strings.TrimPrefix(srv.URL, "https://")+LinkPath, nil)
	if err == nil {
		t.Fatal("a link without a client certificate was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("link without a certificate: resp=%v err=%v, want 401", resp, err)
	}
	_ = resp.Body.Close()
	if !errors.Is(err, websocket.ErrBadHandshake) {
		t.Fatalf("err = %v, want a bad handshake", err)
	}
}
