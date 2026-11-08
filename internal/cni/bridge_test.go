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

package cni_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cni "github.com/eminwux/kukeon/internal/cni"
	"github.com/jsimonetti/rtnetlink/v2"
)

// fakeBridgeRunner records DeleteBridge invocations and lets tests assert on
// the call sequence. notFoundOn names a bridge for which the runner returns
// an "already-absent" success — it never returns an error so the helper sees
// the same idempotent contract as the real NetlinkBridgeRunner does for missing
// links.
type fakeBridgeRunner struct {
	calls       []string
	notFoundOn  string
	failOn      string
	failWithErr error
}

func (f *fakeBridgeRunner) DeleteBridge(_ context.Context, name string) error {
	f.calls = append(f.calls, name)
	if f.failOn != "" && name == f.failOn {
		return f.failWithErr
	}
	// notFoundOn drains to nil — idempotent absent-bridge case.
	_ = f.notFoundOn
	return nil
}

func writeConflist(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("failed to write conflist: %v", err)
	}
	return path
}

const bridgeConflist = `{
  "cniVersion": "0.4.0",
  "name": "default-foo",
  "plugins": [
    {"type": "bridge", "bridge": "kuke-s-deadbeef"},
    {"type": "loopback"}
  ]
}`

const noBridgeConflist = `{
  "cniVersion": "0.4.0",
  "name": "default-bar",
  "plugins": [{"type": "loopback"}]
}`

func TestManager_TeardownNetwork_BridgePresent(t *testing.T) {
	dir := t.TempDir()
	confPath := writeConflist(t, dir, "default-foo.conflist", bridgeConflist)

	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	runner := &fakeBridgeRunner{}
	if err = mgr.TeardownNetwork(context.Background(), runner, "default-foo", confPath); err != nil {
		t.Fatalf("TeardownNetwork() error = %v, want nil", err)
	}

	if _, statErr := os.Stat(confPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("conflist still present after teardown: %v", statErr)
	}
	if got, want := runner.calls, []string{"kuke-s-deadbeef"}; !equalStrings(got, want) {
		t.Errorf("DeleteBridge calls = %v, want %v", got, want)
	}
}

func TestManager_TeardownNetwork_BridgeAlreadyAbsent(t *testing.T) {
	dir := t.TempDir()
	confPath := writeConflist(t, dir, "default-foo.conflist", bridgeConflist)

	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Runner returns nil (treats as absent) — TeardownNetwork must propagate
	// that as success, not error.
	runner := &fakeBridgeRunner{notFoundOn: "kuke-s-deadbeef"}
	if err = mgr.TeardownNetwork(context.Background(), runner, "default-foo", confPath); err != nil {
		t.Fatalf("TeardownNetwork() error = %v, want nil (absent bridge is idempotent)", err)
	}
	if len(runner.calls) != 1 {
		t.Errorf("DeleteBridge call count = %d, want 1", len(runner.calls))
	}
}

func TestManager_TeardownNetwork_ConflistMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "default-foo.conflist")

	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	runner := &fakeBridgeRunner{}
	if err = mgr.TeardownNetwork(context.Background(), runner, "default-foo", missing); err != nil {
		t.Fatalf("TeardownNetwork() error = %v, want nil (missing conflist is idempotent)", err)
	}
	if !equalStrings(runner.calls, []string{cni.SafeBridgeName("default-foo")}) {
		t.Errorf("DeleteBridge calls = %v, want recovered deterministic name", runner.calls)
	}
}

func TestManager_TeardownNetwork_ConflistNoBridgePlugin(t *testing.T) {
	dir := t.TempDir()
	confPath := writeConflist(t, dir, "default-bar.conflist", noBridgeConflist)

	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	runner := &fakeBridgeRunner{}
	if err = mgr.TeardownNetwork(context.Background(), runner, "default-bar", confPath); err != nil {
		t.Fatalf("TeardownNetwork() error = %v, want nil (no bridge plugin is idempotent)", err)
	}
	if _, statErr := os.Stat(confPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("conflist still present after teardown: %v", statErr)
	}
	if len(runner.calls) != 0 {
		t.Errorf("DeleteBridge calls = %v, want none (no bridge plugin)", runner.calls)
	}
}

func TestManager_TeardownNetwork_DefaultsConfigPathFromName(t *testing.T) {
	dir := t.TempDir()
	confPath := writeConflist(t, dir, "default-foo.conflist", bridgeConflist)

	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	runner := &fakeBridgeRunner{}
	// Empty configPath → manager derives <CniConfigDir>/<name>.conflist.
	if err = mgr.TeardownNetwork(context.Background(), runner, "default-foo", ""); err != nil {
		t.Fatalf("TeardownNetwork() error = %v, want nil", err)
	}
	if _, statErr := os.Stat(confPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("conflist still present after teardown via default path: %v", statErr)
	}
	if got, want := runner.calls, []string{"kuke-s-deadbeef"}; !equalStrings(got, want) {
		t.Errorf("DeleteBridge calls = %v, want %v", got, want)
	}
}

func TestManager_TeardownNetwork_EmptyNetworkName(t *testing.T) {
	dir := t.TempDir()
	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	if err = mgr.TeardownNetwork(context.Background(), nil, "", ""); err == nil {
		t.Fatal("TeardownNetwork(\"\") error = nil, want error")
	}
}

func TestManager_TeardownNetwork_MalformedConflistErrors(t *testing.T) {
	dir := t.TempDir()
	confPath := writeConflist(t, dir, "default-foo.conflist", "not json")

	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	runner := &fakeBridgeRunner{}
	if err = mgr.TeardownNetwork(context.Background(), runner, "default-foo", confPath); err == nil {
		t.Fatal("TeardownNetwork() error = nil on malformed JSON, want error")
	}
	if len(runner.calls) != 0 {
		t.Errorf("DeleteBridge called despite malformed conflist: %v", runner.calls)
	}
	// Conflist must remain so the operator can inspect/repair.
	if _, statErr := os.Stat(confPath); statErr != nil {
		t.Errorf("conflist removed despite parse error: %v", statErr)
	}
}

func TestManager_TeardownNetwork_NilRunnerDefaults(t *testing.T) {
	// Helper must not panic when caller passes nil; it falls back to
	// NetlinkBridgeRunner. With a no-bridge conflist there is no link to delete,
	// so the NetlinkBridgeRunner path is never exercised — exactly what we want
	// for a unit test that doesn't shell out to ip(8).
	dir := t.TempDir()
	confPath := writeConflist(t, dir, "default-bar.conflist", noBridgeConflist)

	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	if err = mgr.TeardownNetwork(context.Background(), nil, "default-bar", confPath); err != nil {
		t.Fatalf("TeardownNetwork(nil runner) error = %v, want nil", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestManager_TeardownNetwork_RetryAfterBridgeFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeConflist(t, dir, "default-foo.conflist", bridgeConflist)
	mgr, err := cni.NewManager("", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("temporary delete failure")
	runner := &fakeBridgeRunner{failOn: "kuke-s-deadbeef", failWithErr: failure}
	if err = mgr.TeardownNetwork(t.Context(), runner, "default-foo", path); !errors.Is(err, failure) {
		t.Fatalf("first teardown = %v, want deletion failure", err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("lost retry identity: %v", err)
	}
	runner.failOn = ""
	if err = mgr.TeardownNetwork(t.Context(), runner, "default-foo", path); err != nil {
		t.Fatal(err)
	}
	if !equalStrings(runner.calls, []string{"kuke-s-deadbeef", "kuke-s-deadbeef"}) {
		t.Fatalf("calls: %v", runner.calls)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conflist remains: %v", err)
	}
}

// Run in a disposable network namespace with KUKEON_CNI_NETLINK_TEST=1.
func TestNetlinkBridgeRunner_WithoutIP(t *testing.T) {
	if os.Getenv("KUKEON_CNI_NETLINK_TEST") != "1" {
		t.Skip("requires disposable network namespace")
	}
	t.Setenv("PATH", t.TempDir())
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const name = "k-netlink-test"
	if err = conn.Link.New(&rtnetlink.LinkMessage{Attributes: &rtnetlink.LinkAttributes{Name: name, Info: &rtnetlink.LinkInfo{Kind: "bridge"}}}); err != nil {
		t.Fatal(err)
	}
	runner := cni.NetlinkBridgeRunner{}
	t.Cleanup(func() { _ = runner.DeleteBridge(context.Background(), name) })
	if err = runner.DeleteBridge(t.Context(), name); err != nil {
		t.Fatal(err)
	}
	links, err := conn.Link.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.Attributes != nil && link.Attributes.Name == name {
			t.Fatal("bridge survived deletion")
		}
	}
	if err = runner.DeleteBridge(t.Context(), name); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	// A name that resolves to a non-bridge must never delete that interface.
	if err = runner.DeleteBridge(t.Context(), "lo"); err == nil {
		t.Fatal("accepted non-bridge interface")
	}
}
