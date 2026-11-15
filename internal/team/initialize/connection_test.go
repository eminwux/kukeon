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

package initialize

import (
	"context"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestHostPrecedence(t *testing.T) {
	t.Setenv("KUKE_CONFIGURATION", "")
	t.Setenv("KUKEON_HOST", "")
	root := t.TempDir()
	opts := Options{Root: root}
	host, err := resolveHost(opts)
	if err != nil || host != defaultHost {
		t.Fatalf("host=%s error=%v", host, err)
	}
	path := filepath.Join(root, "kuke.yaml")
	if err = os.WriteFile(path, []byte("apiVersion: v1beta1\nkind: ClientConfiguration\nspec:\n  host: unix:///from-file.sock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err = resolveHost(opts)
	if err != nil || host != "unix:///from-file.sock" {
		t.Fatalf("host=%s error=%v", host, err)
	}
	t.Setenv("KUKEON_HOST", "unix:///from-env.sock")
	host, err = resolveHost(opts)
	if err != nil || host != "unix:///from-env.sock" {
		t.Fatalf("host=%s error=%v", host, err)
	}
	opts.Host = "unix:///from-flag.sock"
	host, err = resolveHost(opts)
	if err != nil || host != opts.Host {
		t.Fatalf("host=%s error=%v", host, err)
	}
	opts.Host = "ssh://example"
	if _, err = resolveHost(opts); err == nil {
		t.Fatal("accepted unsupported host")
	}
	opts.Host = ""
	opts.Configuration = filepath.Join(root, "missing.yaml")
	if _, err = resolveHost(opts); err == nil {
		t.Fatal("missing explicit file accepted")
	}
	opts.Configuration = ""
	t.Setenv("KUKEON_HOST", "")
	t.Setenv("KUKE_CONFIGURATION", path)
	host, err = resolveHost(opts)
	if err != nil || host != "unix:///from-file.sock" {
		t.Fatalf("host=%s error=%v", host, err)
	}
	for _, raw := range []string{"kind: Wrong\n", "[invalid", "apiVersion: v999\nkind: ClientConfiguration\n"} {
		if err = os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = resolveHost(opts); err == nil {
			t.Fatal("invalid client config accepted")
		}
	}
}

// WireService serves the real SDK request/reply types. This checks transport and
// Team propagation; daemon storage is checked separately by TestKukeondIntegration.
type WireService struct{ Client *fakeClient }

func (s *WireService) Ping(_ *kukeonv1.PingArgs, _ *kukeonv1.PingReply) error { return nil }
func (s *WireService) ListRealms(_ *kukeonv1.ListRealmsArgs, r *kukeonv1.ListRealmsReply) error {
	r.Realms, _ = s.Client.ListRealms(context.Background())
	return nil
}

func (s *WireService) ListSpaces(a *kukeonv1.ListSpacesArgs, r *kukeonv1.ListSpacesReply) error {
	r.Spaces, _ = s.Client.ListSpaces(context.Background(), a.RealmName)
	return nil
}

func (s *WireService) ListStacks(a *kukeonv1.ListStacksArgs, r *kukeonv1.ListStacksReply) error {
	r.Stacks, _ = s.Client.ListStacks(context.Background(), a.RealmName, a.SpaceName)
	return nil
}

func (s *WireService) ApplyDocuments(a *kukeonv1.ApplyDocumentsArgs, r *kukeonv1.ApplyDocumentsReply) error {
	var err error
	r.Result, err = s.Client.ApplyDocumentsForTeam(context.Background(), a.RawYAML, a.Team)
	r.Err = kukeonv1.ToAPIError(err)
	return nil
}

func serveWire(t *testing.T, f *fakeClient) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kukeond.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer()
	if err = server.RegisterName(kukeonv1.ServiceName, &WireService{Client: f}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); server.ServeCodec(jsonrpc.NewServerCodec(conn)) }()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); wg.Wait() })
	return "unix://" + path
}

func TestInitOverUnixSocketAndClientConfiguration(t *testing.T) {
	opts := setupRun(t)
	f := newFake()
	host := serveWire(t, f)
	opts.Client = nil
	t.Setenv("KUKE_CONFIGURATION", "")
	t.Setenv("KUKEON_HOST", "")
	if err := os.WriteFile(filepath.Join(opts.Root, "kuke.yaml"), []byte("kind: ClientConfiguration\nspec:\n  host: "+host+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := Run(t.Context(), opts)
		if err != nil || result.EntryPath == "" {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	}
	if f.applies != 2 || f.team != "kukeon" {
		t.Fatal("wire did not carry stable team identity")
	}
}

func TestUnavailableSocketDoesNotRegister(t *testing.T) {
	opts := setupRun(t)
	opts.Client = nil
	opts.Host = "unix://" + filepath.Join(t.TempDir(), "absent.sock")
	_, err := Run(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "connection/preflight") {
		t.Fatalf("error=%v", err)
	}
	if _, err = os.Stat(filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")); !os.IsNotExist(err) {
		t.Fatal("registered on transport failure")
	}
}
