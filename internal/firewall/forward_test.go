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

package firewall_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/firewall"
)

// fakeRunner records every iptables invocation and returns canned responses
// keyed by the space-joined args. Unknown calls succeed silently. Mirrors
// the netpolicy enforcer test harness so the two packages stay legible
// side-by-side.
type fakeRunner struct {
	calls   [][]string
	respond map[string]fakeResp
}

type fakeResp struct {
	out []byte
	err error
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.respond != nil {
		if r, ok := f.respond[strings.Join(args, " ")]; ok {
			return r.out, r.err
		}
	}
	return nil, nil
}

func newInstaller(runner *fakeRunner) *firewall.Installer {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return firewall.NewInstallerWithRunner(logger, runner)
}

// TestAdmissionRules_Order locks in the rule order and content. The chain
// must start with a stateful ACCEPT for return-traffic, then admit -i and -o
// kukeon-bridge traffic. Catching a future reorder here is the regression
// guard for the silent-egress-loss bug fixed by #293.
func TestAdmissionRules_Order(t *testing.T) {
	rules := firewall.AdmissionRules()
	if len(rules) != 3 {
		t.Fatalf("want 3 admission rules; got %d: %v", len(rules), rules)
	}

	// Rule 0: -A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
	want0 := []string{
		"-A", firewall.ForwardChainName,
		"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED",
		"-j", "ACCEPT",
	}
	if !sliceEq(rules[0], want0) {
		t.Errorf("rule 0: want %v; got %v", want0, rules[0])
	}

	// Rule 1: -A KUKEON-FORWARD -i k-+ -j ACCEPT
	want1 := []string{"-A", firewall.ForwardChainName, "-i", firewall.BridgeIfaceMatch, "-j", "ACCEPT"}
	if !sliceEq(rules[1], want1) {
		t.Errorf("rule 1: want %v; got %v", want1, rules[1])
	}

	// Rule 2: -A KUKEON-FORWARD -o k-+ -j ACCEPT
	want2 := []string{"-A", firewall.ForwardChainName, "-o", firewall.BridgeIfaceMatch, "-j", "ACCEPT"}
	if !sliceEq(rules[2], want2) {
		t.Errorf("rule 2: want %v; got %v", want2, rules[2])
	}
}

// TestBridgeIfaceMatch_CoversCNINames asserts the wildcard match used in the
// admission rules covers the bridge names produced by internal/cni so the
// firewall and CNI packages cannot drift apart.
func TestBridgeIfaceMatch_CoversCNINames(t *testing.T) {
	const wildcard = "k-+" // iptables prefix-match
	if firewall.BridgeIfaceMatch != wildcard {
		t.Fatalf("BridgeIfaceMatch must be %q to cover all kukeon bridges; got %q",
			wildcard, firewall.BridgeIfaceMatch)
	}
	// Sample bridge name produced by the CNI helper. It must start with
	// "k-" and have at most 15 chars (IFNAMSIZ-1).
	bridge := cni.SafeBridgeName("default-default")
	if !strings.HasPrefix(bridge, "k-") {
		t.Errorf("cni.SafeBridgeName must start with k-; got %q", bridge)
	}
	if len(bridge) > 15 {
		t.Errorf("cni.SafeBridgeName must fit IFNAMSIZ-1=15; got %q (%d)", bridge, len(bridge))
	}
}

// TestInstall_OnFreshHost emits -N, three -A rules in order, and the FORWARD
// position-1 jump when nothing is already in place.
func TestInstall_OnFreshHost(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Chain absent.
			"-L KUKEON-FORWARD -n": {err: errors.New("absent")},
			// Each rule absent (so -C fails and -A runs).
			"-C KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT": {err: errors.New("absent")},
			"-C KUKEON-FORWARD -i k-+ -j ACCEPT":                                     {err: errors.New("absent")},
			"-C KUKEON-FORWARD -o k-+ -j ACCEPT":                                     {err: errors.New("absent")},
			// FORWARD jump absent.
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
		},
	}
	i := newInstaller(runner)

	if err := i.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if !wasCalled(runner, []string{"-N", "KUKEON-FORWARD"}) {
		t.Errorf("expected -N KUKEON-FORWARD; calls = %v", runner.calls)
	}
	for _, r := range firewall.AdmissionRules() {
		if !wasCalled(runner, r) {
			t.Errorf("expected admission rule %v; calls = %v", r, runner.calls)
		}
	}
	if !wasCalled(runner, []string{"-I", "FORWARD", "1", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("expected -I FORWARD 1 -j KUKEON-FORWARD; calls = %v", runner.calls)
	}
}

// TestInstall_IsIdempotent verifies a second install on a healthy host
// performs zero -N, -A, or -I operations — only the -L/-C existence checks.
// This is the "no rule churn" guarantee the issue calls out.
func TestInstall_IsIdempotent(t *testing.T) {
	// All -L/-C succeed → everything already in place.
	runner := &fakeRunner{respond: map[string]fakeResp{}}
	i := newInstaller(runner)

	if err := i.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for _, c := range runner.calls {
		switch c[0] {
		case "-N", "-I", "-A":
			t.Errorf("idempotent install must not invoke %s; got call %v", c[0], c)
		}
	}
}

// TestInstall_ChainCreateFailureWrapsSentinel surfaces -N failures with the
// ErrForwardAdmissionApply sentinel so callers can errors.Is them.
func TestInstall_ChainCreateFailureWrapsSentinel(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-L KUKEON-FORWARD -n": {err: errors.New("absent")},
			"-N KUKEON-FORWARD":    {err: errors.New("denied")},
		},
	}
	i := newInstaller(runner)

	err := i.Install(context.Background())
	if err == nil {
		t.Fatal("expected error from -N failure")
	}
	if !errors.Is(err, errdefs.ErrForwardAdmissionApply) {
		t.Errorf("expected ErrForwardAdmissionApply wrap; got %v", err)
	}
}

// TestRemove_OnInstalledHost deletes the FORWARD jump, flushes, and deletes
// the chain.
func TestRemove_OnInstalledHost(t *testing.T) {
	// -C FORWARD jump succeeds (jump present), so -D should be invoked.
	runner := &fakeRunner{respond: map[string]fakeResp{}}
	i := newInstaller(runner)

	if err := i.Remove(context.Background()); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if !wasCalled(runner, []string{"-D", "FORWARD", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("expected -D FORWARD -j KUKEON-FORWARD; calls = %v", runner.calls)
	}
	if !wasCalled(runner, []string{"-F", "KUKEON-FORWARD"}) {
		t.Errorf("expected -F KUKEON-FORWARD; calls = %v", runner.calls)
	}
	if !wasCalled(runner, []string{"-X", "KUKEON-FORWARD"}) {
		t.Errorf("expected -X KUKEON-FORWARD; calls = %v", runner.calls)
	}
}

// TestRemove_TolerantWhenChainAbsent leaves -F/-X failures as a debug log,
// not a returned error, so reset --purge-system on a host that never had
// the chain installed (or already removed it) is a no-op.
func TestRemove_TolerantWhenChainAbsent(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Jump absent → no -D should be issued.
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
			// Chain absent → -F/-X return errors but Remove must swallow them.
			"-F KUKEON-FORWARD": {err: errors.New("no such chain")},
			"-X KUKEON-FORWARD": {err: errors.New("no such chain")},
		},
	}
	i := newInstaller(runner)

	if err := i.Remove(context.Background()); err != nil {
		t.Fatalf("Remove on absent chain must not error: %v", err)
	}
	for _, c := range runner.calls {
		if c[0] == "-D" {
			t.Errorf("must not -D when -C reports absence; got %v", c)
		}
	}
}

// TestRemove_DeleteJumpFailureWrapsSentinel surfaces a -D failure (jump was
// detected but couldn't be removed — e.g. permission revoked between -C and
// -D) with the ErrForwardAdmissionRemove sentinel.
func TestRemove_DeleteJumpFailureWrapsSentinel(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-D FORWARD -j KUKEON-FORWARD": {err: errors.New("denied")},
		},
	}
	i := newInstaller(runner)

	err := i.Remove(context.Background())
	if err == nil {
		t.Fatal("expected error from -D failure")
	}
	if !errors.Is(err, errdefs.ErrForwardAdmissionRemove) {
		t.Errorf("expected ErrForwardAdmissionRemove wrap; got %v", err)
	}
}

func wasCalled(f *fakeRunner, want []string) bool {
	for _, got := range f.calls {
		if sliceEq(got, want) {
			return true
		}
	}
	return false
}

func sliceEq(a, b []string) bool {
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
