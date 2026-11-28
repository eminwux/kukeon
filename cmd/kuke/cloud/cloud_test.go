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

package cloud_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/cmd/kuke/cloud"
	"github.com/eminwux/kukeon/internal/cloudhost"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/cloudlink"
)

// fakeDaemon stores the credential Secret in memory.
type fakeDaemon struct {
	kukeonv1.FakeClient

	secret *v1beta1.SecretDoc
	writes int
}

func (f *fakeDaemon) ReadSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	if f.secret == nil || doc.Metadata != f.secret.Metadata {
		return kukeonv1.GetSecretResult{}, nil
	}
	return kukeonv1.GetSecretResult{MetadataExists: true, Secret: *f.secret}, nil
}

func (f *fakeDaemon) CreateSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
	f.writes++
	f.secret = &doc
	return kukeonv1.CreateSecretResult{Created: true}, nil
}

func run(t *testing.T, daemon *fakeDaemon, statusFile string, args ...string) (string, error) {
	t.Helper()
	cmd := cloud.NewCloudCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	ctx := context.WithValue(context.Background(), cloud.MockControllerKey{}, cloud.Client(daemon))
	cmd.SetContext(context.WithValue(ctx, cloud.MockStatusFileKey{}, statusFile))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func startRelay(t *testing.T) (*cloudlink.Relay, *httptest.Server, string) {
	t.Helper()
	relay, err := cloudlink.NewRelay()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(relay.Handler())
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	relay.ConfigureTLS(srv.TLS)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caFile := filepath.Join(t.TempDir(), "relay-ca.pem")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err = os.WriteFile(caFile, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	return relay, srv, caFile
}

func TestEnrollStoresCredentialSecret(t *testing.T) {
	relay, srv, caFile := startRelay(t)
	daemon := &fakeDaemon{}
	statusFile := filepath.Join(t.TempDir(), cloudhost.StatusFileName)

	out, err := run(t, daemon, statusFile,
		"enroll", relay.NewEnrollmentToken(0), "--url", srv.URL, "--ca-file", caFile, "--hostname", "box")
	if err != nil {
		t.Fatalf("enroll: %v\n%s", err, out)
	}
	if daemon.secret == nil || daemon.secret.Metadata.Name != consts.CloudLinkSecretName ||
		daemon.secret.Metadata.Realm != consts.KukeSystemRealmName || daemon.secret.Spec.Type != v1beta1.SecretTypeOpaque {
		t.Fatalf("stored secret = %+v, want opaque %s", daemon.secret, cloudhost.SecretRef())
	}
	var cred cloudlink.Credential
	if err = json.Unmarshal([]byte(daemon.secret.Spec.Data), &cred); err != nil || cred.Validate() != nil {
		t.Fatalf("stored credential does not validate: %v / %v", err, cred.Validate())
	}
	if cred.ServerCA == "" || cred.RelayURL != srv.URL {
		t.Fatalf(
			"credential = relay %q, serverCA %d bytes; want the relay and its CA kept",
			cred.RelayURL,
			len(cred.ServerCA),
		)
	}
	for _, want := range []string{
		"Enrolled with " + srv.URL + " as host " + cred.HostID,
		"Credential stored in Secret " + cloudhost.SecretRef(),
		"kuke serve --cloud-link " + cred.LinkURL,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("enroll output lacks %q:\n%s", want, out)
		}
	}

	// Enrolling again replaces the credential and says so.
	out, err = run(t, daemon, statusFile, "enroll", relay.NewEnrollmentToken(0), "--url", srv.URL, "--ca-file", caFile)
	if err != nil || !strings.Contains(out, "Replaced the enrollment of host "+cred.HostID) {
		t.Fatalf("re-enroll = %v:\n%s", err, out)
	}

	// A spent token is refused and leaves the stored credential alone.
	writes := daemon.writes
	token := relay.NewEnrollmentToken(0)
	if _, err = run(t, daemon, statusFile, "enroll", token, "--url", srv.URL, "--ca-file", caFile); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, daemon, statusFile, "enroll", token, "--url", srv.URL, "--ca-file", caFile); err == nil {
		t.Fatal("enroll with a spent token succeeded")
	}
	if daemon.writes != writes+1 {
		t.Fatalf("secret writes = %d, want %d: a refused enrollment must not write", daemon.writes, writes+1)
	}
}

func TestStatus(t *testing.T) {
	relay, srv, caFile := startRelay(t)
	daemon := &fakeDaemon{}
	statusFile := filepath.Join(t.TempDir(), cloudhost.StatusFileName)

	out, err := run(t, daemon, statusFile, "status")
	if err != nil || !strings.Contains(out, "not enrolled") || !strings.Contains(out, "Link:        not running") {
		t.Fatalf("status before enrollment = %v:\n%s", err, out)
	}

	if out, err = run(t, daemon, statusFile, "enroll", relay.NewEnrollmentToken(0), "--url", srv.URL, "--ca-file", caFile); err != nil {
		t.Fatalf("enroll: %v\n%s", err, out)
	}
	out, err = run(t, daemon, statusFile, "status")
	if err != nil || !strings.Contains(out, "Enrollment:  host h-") ||
		!strings.Contains(out, "start it with `kuke serve --cloud-link wss://") {
		t.Fatalf("status of an enrolled host with no link = %v:\n%s", err, out)
	}

	// A running kuke serve (this test process) reports a connected link.
	status := cloudhost.Status{
		PID: os.Getpid(), URL: "wss://relay/v1/link", HostID: "h-1",
		State: cloudlink.State{Status: cloudlink.StateConnected, Since: time.Now().Add(-time.Minute)},
	}
	if err = cloudhost.WriteStatus(statusFile, status); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, daemon, statusFile, "status")
	if err != nil || !strings.Contains(out, "Link:        connected for 1m0s (kuke serve pid") {
		t.Fatalf("status with a connected link = %v:\n%s", err, out)
	}

	// A status file left by a process that is gone reads as not running.
	status.PID = 1 << 30
	status.Status, status.Error = cloudlink.StateDisconnected, "dial failed"
	if err = cloudhost.WriteStatus(statusFile, status); err != nil {
		t.Fatal(err)
	}
	if out, err = run(t, daemon, statusFile, "status"); err != nil ||
		!strings.Contains(out, "Link:        not running") {
		t.Fatalf("status with a stale status file = %v:\n%s", err, out)
	}
}
