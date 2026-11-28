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

package e2e_test

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/cloudlink"
	"github.com/gorilla/websocket"
)

// cloudUserHeader names the user the test relay authenticates.
const cloudUserHeader = "X-E2e-User"

// cloudLinkTimeout bounds waiting for the link to come up or come back.
const cloudLinkTimeout = 30 * time.Second

// syncBuffer collects a subprocess's output for diagnostics and checks.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestKuke_CloudLink_ThroughReferenceRelay is the cloud link end to end
// (issue #1755): the reference relay from pkg/cloudlink runs in the test,
// `kuke cloud enroll` exchanges a one-time token for the host credential
// (stored as a Secret in kuke-system by a real kukeond), and `kuke serve
// --cloud-link` dials the relay. Through the relay the test reads
// GET /api/connection and a cell list, round-trips bytes through a cell
// terminal over the attach WebSocket, drops the link and checks that the
// host reconnects and serves again, and reads the state with `kuke cloud
// status`.
//
// Not run with t.Parallel(): it creates the kuke-system realm on the
// per-test daemon, whose containerd namespace is host-global, like
// TestKuke_DaemonReset_RoundTrip.
func TestKuke_CloudLink_ThroughReferenceRelay(t *testing.T) {
	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	t.Cleanup(func() {
		cleanupCellNoDaemon(t, runPath, realmName, spaceName, stackName, cellName)
		cleanupStackNoDaemon(t, runPath, realmName, spaceName, stackName)
		cleanupSpaceNoDaemon(t, runPath, realmName, spaceName)
		cleanupRealmNoDaemon(t, runPath, realmName)
		cleanupRealmNoDaemon(t, runPath, consts.KukeSystemRealmName)
	})

	// The credential Secret lives in kuke-system, which `kuke init` creates
	// on a real host.
	daemonArgs := buildKukeDaemonArgs(host)
	runReturningBinary(t, nil, kuke, append(daemonArgs, "create", "realm", consts.KukeSystemRealmName)...)
	runReturningBinary(t, nil, kuke, append(daemonArgs, "create", "realm", realmName)...)
	runReturningBinary(t, nil, kuke, append(daemonArgs, "create", "space", spaceName, "--realm", realmName)...)
	runReturningBinary(t, nil, kuke, append(daemonArgs, "create", "stack", stackName,
		"--realm", realmName, "--space", spaceName)...)
	applyAttachableCell(t, host, "attachable-shell-cell.yaml", realmName, spaceName, stackName, cellName)
	waitForSocket(t, fs.ContainerSocketSymlinkPath(runPath, realmName, spaceName, stackName, cellName, "work"),
		10*time.Second)

	relay, err := cloudlink.NewRelay()
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	relay.Authenticate = cloudlink.HeaderAuthenticator(cloudUserHeader)
	relay.Authorize = cloudlink.AllowAnyHost
	srv := httptest.NewUnstartedServer(relay.Handler())
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	relay.ConfigureTLS(srv.TLS)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caFile := filepath.Join(t.TempDir(), "relay-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err = os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	// Enroll: the token is spent once; the credential lands in kuke-system.
	token := relay.NewEnrollmentToken(0)
	enrollArgs := append(buildKukeDaemonArgs(host), "cloud", "enroll", token, "--url", srv.URL, "--ca-file", caFile)
	code, stdout, stderr := runBinary(t, nil, kuke, enrollArgs...)
	if code != 0 {
		t.Fatalf("kuke cloud enroll failed: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	linkURL := lineAfter(t, string(stdout), "Start the link: kuke serve --cloud-link ")
	hostID := strings.TrimSuffix(lineAfter(t, string(stdout), " as host "), ".")
	if code, _, _ = runBinary(t, nil, kuke, enrollArgs...); code == 0 {
		t.Fatal("a second kuke cloud enroll with the same token succeeded")
	}
	getSecret := append(buildKukeDaemonArgs(host), "get", "secret", consts.CloudLinkSecretName,
		"--realm", consts.KukeSystemRealmName)
	if code, stdout, stderr = runBinary(t, nil, kuke, getSecret...); code != 0 {
		t.Fatalf("credential Secret not stored: code=%d %s %s", code, stdout, stderr)
	}

	serveArgs := append(buildKukeDaemonArgs(host), "serve", "--listen", "127.0.0.1:0", "--cloud-link", linkURL)
	serveOut := startKukeServe(t, serveArgs)
	select {
	case <-relay.WaitConnected(hostID):
	case <-time.After(cloudLinkTimeout):
		t.Fatalf("kuke serve did not bring the link up within %s; output:\n%s", cloudLinkTimeout, serveOut)
	}

	hostAPI := srv.URL + cloudlink.HostsPath + hostID + "/api"
	status, body := relayRequest(t, srv, hostAPI+"/connection", "alice@example.com")
	if status != http.StatusOK || !strings.Contains(body, `"daemonVersion"`) {
		t.Fatalf("GET /api/connection through the relay = %d %s", status, body)
	}
	status, body = relayRequest(t, srv,
		hostAPI+"/realms/"+realmName+"/spaces/"+spaceName+"/stacks/"+stackName+"/cells", "alice@example.com")
	var cells []v1beta1.CellDoc
	if status != http.StatusOK || json.Unmarshal([]byte(body), &cells) != nil ||
		len(cells) != 1 || cells[0].Metadata.Name != cellName {
		t.Fatalf("cell list through the relay = %d %s, want the one cell %s", status, body, cellName)
	}
	if status, _ = relayRequest(t, srv, hostAPI+"/connection", ""); status != http.StatusUnauthorized {
		t.Fatalf("GET without a relay user = %d, want 401", status)
	}

	attachPath := "/realms/" + realmName + "/spaces/" + spaceName + "/stacks/" + stackName +
		"/cells/" + cellName + "/containers/work/attach"
	attachRoundTrip(t, srv, hostAPI+attachPath)

	if got := serveOut.String(); !strings.Contains(
		got,
		`msg="cloud link request" user=alice@example.com method=GET path=/api/connection`,
	) ||
		!strings.Contains(got, "path=/api"+attachPath) {
		t.Fatalf("kuke serve's audit log does not record the relay user:\n%s", got)
	}

	cloudStatus := append(buildKukeDaemonArgs(host), "cloud", "status")
	code, stdout, stderr = runBinary(t, nil, kuke, cloudStatus...)
	if code != 0 || !strings.Contains(string(stdout), "Enrollment:  host "+hostID) ||
		!strings.Contains(string(stdout), "Link:        connected") {
		t.Fatalf("kuke cloud status = %d:\n%s%s", code, stdout, stderr)
	}

	// Drop the link: kuke serve reconnects on its own and serves again.
	if !relay.Disconnect(hostID) {
		t.Fatal("the relay had no link to drop")
	}
	deadline := time.Now().Add(cloudLinkTimeout)
	for {
		if status, _ = relayRequest(t, srv, hostAPI+"/connection", "bob"); status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no successful request within %s of the link drop (last %d); output:\n%s",
				cloudLinkTimeout, status, serveOut)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// lineAfter returns the rest of the first output line containing marker.
func lineAfter(t *testing.T, output, marker string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if _, rest, found := strings.Cut(line, marker); found {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("output has no %q:\n%s", marker, output)
	return ""
}

// startKukeServe runs `kuke serve` until the test ends and returns its
// combined output.
func startKukeServe(t *testing.T, args []string) *syncBuffer {
	t.Helper()
	binDir := os.Getenv("E2E_BIN_DIR")
	if binDir == "" {
		binDir = ".."
	}
	out := &syncBuffer{}
	cmd := exec.Command(filepath.Join(binDir, kuke), args...)
	cmd.Env = e2eRunEnv(nil, args)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start kuke serve: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(daemonShutdownTimeout):
			_ = cmd.Process.Kill()
			<-exited
		}
		if t.Failed() {
			t.Logf("kuke serve output:\n%s", out)
		}
	})
	return out
}

func relayRequest(t *testing.T, srv *httptest.Server, url, user string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if user != "" {
		req.Header.Set(cloudUserHeader, user)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// attachRoundTrip opens the attach WebSocket through the relay, waits for
// ready, types a command into the cell's shell and reads its output back.
func attachRoundTrip(t *testing.T, srv *httptest.Server, url string) {
	t.Helper()
	dialer := websocket.Dialer{
		TLSClientConfig:  srv.Client().Transport.(*http.Transport).TLSClientConfig,
		HandshakeTimeout: 10 * time.Second,
	}
	conn, resp, err := dialer.Dial(
		"wss"+strings.TrimPrefix(url, "https"),
		http.Header{cloudUserHeader: {"alice@example.com"}},
	)
	if err != nil {
		t.Fatalf("attach WebSocket through the relay: %v (resp %v)", err, resp)
	}
	_ = resp.Body.Close()
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(attachExitTimeout))
	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.TextMessage || !strings.Contains(string(data), `"ready"`) {
		t.Fatalf("first attach message = %d %q, %v; want ready", kind, data, err)
	}
	// The shell prints the marker only by evaluating the expression, so the
	// echo of the typed line alone cannot satisfy the check.
	if err = conn.WriteMessage(websocket.BinaryMessage, []byte("echo cloud-$((40+2))-link\r")); err != nil {
		t.Fatalf("write to the terminal: %v", err)
	}
	var output strings.Builder
	for !strings.Contains(output.String(), "cloud-42-link") {
		kind, data, err = conn.ReadMessage()
		if err != nil {
			t.Fatalf("terminal output never showed the marker: %v\noutput:\n%s", err, output.String())
		}
		if kind == websocket.TextMessage && strings.Contains(string(data), `"error"`) {
			t.Fatalf("attach error: %s", data)
		}
		if kind == websocket.BinaryMessage {
			output.Write(data)
		}
	}
	_ = conn.WriteMessage(websocket.BinaryMessage, sbshDetachSequence)
}
