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
	opts := ConnectOptions{Root: root}
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

func (s *WireService) GetBlueprint(a *kukeonv1.GetBlueprintArgs, r *kukeonv1.GetBlueprintReply) error {
	r.Result, _ = s.Client.GetBlueprint(context.Background(), a.Doc)
	return nil
}

func (s *WireService) ListConfigs(a *kukeonv1.ListConfigsArgs, r *kukeonv1.ListConfigsReply) error {
	r.Configs, _ = s.Client.ListConfigs(context.Background(), a.RealmName, a.SpaceName, a.StackName)
	return nil
}

func (s *WireService) GetConfig(a *kukeonv1.GetConfigArgs, r *kukeonv1.GetConfigReply) error {
	r.Result, _ = s.Client.GetConfig(context.Background(), a.Doc)
	return nil
}

func (s *WireService) ApplyDocuments(a *kukeonv1.ApplyDocumentsArgs, r *kukeonv1.ApplyDocumentsReply) error {
	var err error
	if a.Team == "" {
		r.Result, err = s.Client.ApplyDocuments(context.Background(), a.RawYAML)
	} else {
		r.Result, err = s.Client.ApplyDocumentsForTeam(context.Background(), a.RawYAML, a.Team)
	}
	r.Err = kukeonv1.ToAPIError(err)
	return nil
}

func (s *WireService) GetTeam(a *kukeonv1.GetTeamArgs, r *kukeonv1.GetTeamReply) error {
	r.Result, _ = s.Client.GetTeam(context.Background(), a.Ref)
	return nil
}

func (s *WireService) ListTeams(a *kukeonv1.ListTeamsArgs, r *kukeonv1.ListTeamsReply) error {
	r.Teams, _ = s.Client.ListTeams(context.Background(), a.Realm)
	return nil
}

func (s *WireService) ListSources(a *kukeonv1.ListSourcesArgs, r *kukeonv1.ListSourcesReply) error {
	r.Sources, _ = s.Client.ListSources(context.Background(), a.Realm)
	return nil
}

func (s *WireService) GetTeamDefaults(a *kukeonv1.GetTeamDefaultsArgs, r *kukeonv1.GetTeamDefaultsReply) error {
	r.Result, _ = s.Client.GetTeamDefaults(context.Background(), a.Realm)
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

// TestInitOverUnixSocketAndClientConfiguration: Connect resolves the host
// from the client configuration, and the stored Team and TeamDefaults travel
// over the wire as YAML bytes.
func TestInitOverUnixSocketAndClientConfiguration(t *testing.T) {
	f := newFake()
	host := serveWire(t, f)
	t.Setenv("KUKE_CONFIGURATION", "")
	t.Setenv("KUKEON_HOST", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "kuke.yaml"), []byte("kind: ClientConfiguration\nspec:\n  host: "+host+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := Connect(t.Context(), ConnectOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	opts := InitOptions{Options: Options{Client: client, Sources: sourceFS()}}
	if _, err = Init(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	// The second init reads the stored Team back over the wire.
	if _, err = Init(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "already initialized") {
		t.Fatalf("second init: %v", err)
	}
	opts.Reapply = true
	if _, err = Init(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	// Each init applies the kuke team, then the default team.
	if f.applies != 4 || f.byTeam["kuke"] == nil || f.byTeam["default"] == nil || f.defaults == nil {
		t.Fatalf("wire did not carry stable team identity: applies=%d team=%q", f.applies, f.team)
	}
}

// TestUnavailableSocketFailsSafely: an absent daemon fails the first read
// with the transport class, never a partial write.
func TestUnavailableSocketFailsSafely(t *testing.T) {
	client, err := Connect(t.Context(), ConnectOptions{Host: "unix://" + filepath.Join(t.TempDir(), "absent.sock")})
	if err != nil {
		return // Refused at connect: nothing could be written.
	}
	defer func() { _ = client.Close() }()
	_, err = Init(t.Context(), InitOptions{Options: Options{Client: client, Sources: sourceFS()}})
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("error=%v", err)
	}
}
