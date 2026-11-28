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
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/cloudhost"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/cloudlink"
	"github.com/eminwux/kukeon/pkg/team/connector"
)

// cloudClient serves the cloud link credential and a daemon version.
type cloudClient struct {
	kukeonv1.FakeClient

	credential string
}

func (c *cloudClient) ReadSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	if doc.Metadata.Name != consts.CloudLinkSecretName || doc.Metadata.Realm != consts.KukeSystemRealmName {
		return kukeonv1.GetSecretResult{}, errors.New("unexpected secret " + doc.Metadata.Name)
	}
	if c.credential == "" {
		return kukeonv1.GetSecretResult{}, nil
	}
	return kukeonv1.GetSecretResult{
		MetadataExists: true,
		Secret:         v1beta1.SecretDoc{Spec: v1beta1.SecretSpec{Data: c.credential}},
	}, nil
}

func (*cloudClient) PingVersion(context.Context) (string, error) { return "v-daemon", nil }

// lockedBuffer is a bytes.Buffer safe for a writer goroutine and a reader.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testCatalog(t *testing.T) *connector.Catalog {
	t.Helper()
	catalog, err := connector.Default()
	if err != nil {
		t.Fatalf("connector catalog: %v", err)
	}
	return catalog
}

func TestLinkHandlerVerifiesAndAuditsIdentity(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	var audit lockedBuffer
	client := &cloudClient{}
	handler := newLinkHandler("unix:///test.sock",
		func(context.Context) (kukeonv1.Client, error) { return client, nil },
		testCatalog(t), "h-1", pub, slog.New(slog.NewTextHandler(&audit, nil)), time.Now)

	request := func(method, target string, signWith ed25519.PrivateKey, user string) *httptest.ResponseRecorder {
		out, _ := http.NewRequest(method, "http://kukeon-host"+target, nil)
		if signWith != nil {
			if err := cloudlink.SignRequest(out, signWith, "h-1", user, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		in := httptest.NewRequest(method, target, nil)
		in.Header = out.Header.Clone()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, in)
		return rec
	}

	rec := request("GET", "/api/connection", key, "alice@example.com")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"daemonVersion":"v-daemon"`) {
		t.Fatalf("signed GET /api/connection = %d %s", rec.Code, rec.Body)
	}
	if got := audit.String(); !strings.Contains(
		got,
		`msg="cloud link request" user=alice@example.com method=GET path=/api/connection`,
	) {
		t.Fatalf("audit log does not record the user:\n%s", got)
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"unsigned":  request("GET", "/api/connection", nil, ""),
		"wrong key": request("GET", "/api/connection", otherKey, "alice"),
	} {
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"unauthenticated"`) {
			t.Errorf("%s request = %d %s, want 401 unauthenticated", name, rec.Code, rec.Body)
		}
	}
	if got := audit.String(); strings.Count(got, `msg="cloud link request rejected"`) != 2 ||
		strings.Contains(got, "user=alice ") {
		t.Fatalf("audit log should record two rejections and no rejected user:\n%s", got)
	}

	rec = request("GET", "/api/session", key, "bob")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"user":"bob"}` {
		t.Fatalf("GET /api/session over the link = %d %s", rec.Code, rec.Body)
	}
	if rec = request("POST", "/api/login", key, "bob"); rec.Code == http.StatusOK {
		t.Fatalf("POST /api/login over the link = %d, want it unrouted", rec.Code)
	}
	if rec = request("GET", "/", key, "bob"); rec.Code != http.StatusNotFound {
		t.Fatalf("GET / over the link = %d, want 404: the link serves only /api", rec.Code)
	}
}

func TestRunWithCloudLinkServesThroughRelay(t *testing.T) {
	relay, err := cloudlink.NewRelay()
	if err != nil {
		t.Fatal(err)
	}
	relay.Authenticate = cloudlink.HeaderAuthenticator("X-User")
	srv := httptest.NewUnstartedServer(relay.Handler())
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	relay.ConfigureTLS(srv.TLS)
	srv.StartTLS()
	defer srv.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	cred, err := cloudlink.Enroll(context.Background(), cloudlink.EnrollOptions{
		RelayURL: srv.URL, Token: relay.NewEnrollmentToken(0), ServerCA: ca,
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	data, _ := json.Marshal(cred)
	client := &cloudClient{credential: string(data)}
	statusFile := filepath.Join(t.TempDir(), cloudhost.StatusFileName)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, "127.0.0.1:0", "", "unix:///test.sock",
			func(context.Context) (kukeonv1.Client, error) { return client, nil },
			&out, &errOut, &CloudLink{URL: cred.LinkURL, StatusFile: statusFile})
	}()
	select {
	case <-relay.WaitConnected(cred.HostID):
	case runErr := <-done:
		t.Fatalf("Run returned early: %v\n%s", runErr, errOut.String())
	case <-time.After(10 * time.Second):
		t.Fatalf("the link did not come up:\n%s", errOut.String())
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+cloudlink.HostsPath+cred.HostID+"/api/connection", nil)
	req.Header.Set("X-User", "alice")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"daemonVersion":"v-daemon"`) {
		t.Fatalf("GET /api/connection through the relay = %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(out.String(), "Cloud link: "+cred.LinkURL+" (host "+cred.HostID+")") {
		t.Fatalf("startup output does not name the link:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "user=alice") {
		t.Fatalf("audit log does not record the user:\n%s", errOut.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, statusErr := cloudhost.ReadStatus(statusFile)
		if statusErr == nil && status.Status == cloudlink.StateConnected && status.HostID == cred.HostID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status file = %+v, %v; want connected", status, statusErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if runErr := <-done; runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if _, statErr := os.Stat(statusFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("status file left after shutdown: %v", statErr)
	}
}

func TestRunWithCloudLinkNeedsEnrollment(t *testing.T) {
	var out lockedBuffer
	err := Run(context.Background(), "127.0.0.1:0", "", "unix:///test.sock",
		func(context.Context) (kukeonv1.Client, error) { return &cloudClient{}, nil },
		&out, io.Discard, &CloudLink{URL: "wss://relay.example/v1/link"})
	if !errors.Is(err, cloudlink.ErrNotEnrolled) || !strings.Contains(err.Error(), "kuke cloud enroll") {
		t.Fatalf("Run on a host that is not enrolled = %v, want ErrNotEnrolled naming kuke cloud enroll", err)
	}
	if strings.Contains(out.String(), "Listening") {
		t.Fatalf("Run bound before checking the enrollment:\n%s", out.String())
	}
	err = Run(context.Background(), "127.0.0.1:0", "", "", nil, io.Discard, io.Discard,
		&CloudLink{URL: "https://relay.example/v1/link"})
	if err == nil || !strings.Contains(err.Error(), "must use wss") {
		t.Fatalf("Run with an https link URL = %v, want a wss error", err)
	}
}
