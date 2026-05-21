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

package runner

import (
	"context"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
)

// stubSetupService mirrors kuketty's setupStatusHandler: a net/rpc receiver
// registered under setupstatus.ServiceName whose GetSetupStatus method returns
// a fixed Reply. Serving it over the stdlib jsonrpc server codec proves the
// daemon-side pullSetupStatus client speaks the same wire protocol kuketty's
// handler is served over (both are net/rpc + JSON-RPC for this non-FD verb),
// without standing up a full sbsh server.
type stubSetupService struct {
	repos []setupstatus.Repo
}

func (s *stubSetupService) GetSetupStatus(_ setupstatus.Args, reply *setupstatus.Reply) error {
	reply.Repos = s.repos
	return nil
}

// serveStubSetup registers svc under setupstatus.ServiceName on a fresh
// rpc.Server, listens on a unix socket inside a temp dir, and serves
// connections with the stdlib jsonrpc server codec until the test ends.
// Returns the socket path the client should dial.
func serveStubSetup(t *testing.T, svc *stubSetupService) string {
	t.Helper()
	srv := rpc.NewServer()
	if err := srv.RegisterName(setupstatus.ServiceName, svc); err != nil {
		t.Fatalf("RegisterName: %v", err)
	}

	// A short socket path (temp dir) so we stay well inside SUN_PATH.
	socketPath := filepath.Join(t.TempDir(), "socket")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return // listener closed by cleanup
			}
			go srv.ServeCodec(jsonrpc.NewServerCodec(conn))
		}
	}()
	return socketPath
}

func TestPullSetupStatus_RoundTrip(t *testing.T) {
	want := []setupstatus.Repo{
		{Name: "app", Target: "/work/app", State: setupstatus.StateCloned, Commit: "abc123"},
		{Name: "lib", Target: "/work/lib", State: setupstatus.StateFailed, Error: "auth failed"},
	}
	socketPath := serveStubSetup(t, &stubSetupService{repos: want})

	got, err := pullSetupStatus(context.Background(), socketPath)
	if err != nil {
		t.Fatalf("pullSetupStatus: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("want %d repos, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("repo[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPullSetupStatus_EmptyReply(t *testing.T) {
	socketPath := serveStubSetup(t, &stubSetupService{repos: nil})

	got, err := pullSetupStatus(context.Background(), socketPath)
	if err != nil {
		t.Fatalf("pullSetupStatus: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty repos, got %+v", got)
	}
}

func TestPullSetupStatus_DialError(t *testing.T) {
	// No server listening at this path — the dial must fail fast (bounded by
	// setupStatusDialTimeout) rather than block, so the caller falls back to
	// an empty Repos.
	missing := filepath.Join(t.TempDir(), "absent")
	if _, err := pullSetupStatus(context.Background(), missing); err == nil {
		t.Fatal("want a dial error for an absent socket, got nil")
	}
}

func TestSetupStatusToInternal(t *testing.T) {
	if got := setupStatusToInternal(nil); got != nil {
		t.Errorf("nil input should map to nil, got %+v", got)
	}
	in := []setupstatus.Repo{
		{Name: "app", Target: "/work/app", State: setupstatus.StateCloned, Commit: "abc123"},
	}
	got := setupStatusToInternal(in)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].Name != "app" || got[0].Target != "/work/app" ||
		got[0].State != setupstatus.StateCloned || got[0].Commit != "abc123" {
		t.Errorf("unexpected mapping: %+v", got[0])
	}
}
