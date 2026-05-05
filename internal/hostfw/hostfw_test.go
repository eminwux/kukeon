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

package hostfw_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/hostfw"
)

// fakeRunner records every iptables invocation and returns canned
// responses keyed by the space-joined args. Unknown calls succeed
// silently — nil error, empty output. Mirrors the netpolicy fakeRunner.
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

func newInstaller(runner *fakeRunner) *hostfw.IptablesInstaller {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return hostfw.NewIptablesInstallerWithRunner(logger, runner)
}

func TestBuildChainRules_OrderAndContent(t *testing.T) {
	rules := hostfw.BuildChainRules()
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}

	// Rule 0: established/related must be first so reply traffic short-circuits.
	got := strings.Join(rules[0].Args, " ")
	if !strings.Contains(got, "--ctstate RELATED,ESTABLISHED") || !strings.Contains(got, "-j ACCEPT") {
		t.Errorf("rule 0 missing conntrack ACCEPT; got %v", rules[0].Args)
	}

	// Rule 1: -i k-+ ACCEPT (egress)
	got = strings.Join(rules[1].Args, " ")
	if !strings.Contains(got, "-i "+hostfw.BridgePrefix) || !strings.Contains(got, "-j ACCEPT") {
		t.Errorf("rule 1 missing -i %s ACCEPT; got %v", hostfw.BridgePrefix, rules[1].Args)
	}

	// Rule 2: -o k-+ ACCEPT (ingress)
	got = strings.Join(rules[2].Args, " ")
	if !strings.Contains(got, "-o "+hostfw.BridgePrefix) || !strings.Contains(got, "-j ACCEPT") {
		t.Errorf("rule 2 missing -o %s ACCEPT; got %v", hostfw.BridgePrefix, rules[2].Args)
	}

	// Every rule must carry a kukeon-forward comment so iptables -S is
	// self-documenting and the cleanup grep cannot collide with user
	// rules that happen to share the same chain name.
	for i, r := range rules {
		joined := strings.Join(r.Args, " ")
		if !strings.Contains(joined, "--comment kukeon-forward:") {
			t.Errorf("rule %d missing kukeon-forward comment tag; got %v", i, r.Args)
		}
		if r.Chain != hostfw.ChainName {
			t.Errorf("rule %d chain mismatch: want %q, got %q", i, hostfw.ChainName, r.Chain)
		}
	}
}

func TestApply_FreshHostInstallsChainAndJump(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Chain absent → -L fails → -N gets called.
			"-L KUKEON-FORWARD -n": {err: errors.New("absent")},
			// FORWARD jump absent → -C fails → -I gets called.
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
			// FORWARD listing returns no rules so position falls back to 1.
			"-S FORWARD": {out: []byte("-P FORWARD ACCEPT\n")},
		},
	}
	// Each rule's -C must fail so the corresponding -A is emitted.
	for _, r := range hostfw.BuildChainRules() {
		check := strings.Join(append([]string{"-C", r.Chain}, r.Args...), " ")
		runner.respond[check] = fakeResp{err: errors.New("absent")}
	}

	if err := newInstaller(runner).Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !wasCalled(runner, []string{"-N", "KUKEON-FORWARD"}) {
		t.Errorf("-N KUKEON-FORWARD not invoked; calls = %v", runner.calls)
	}
	if !wasCalled(runner, []string{"-I", "FORWARD", "1", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("expected FORWARD jump at position 1; calls = %v", runner.calls)
	}

	// Each chain rule must show up as an -A ... ACCEPT call.
	for _, r := range hostfw.BuildChainRules() {
		want := append([]string{"-A", r.Chain}, r.Args...)
		if !wasCalled(runner, want) {
			t.Errorf("expected rule install %v; calls = %v", want, runner.calls)
		}
	}
}

// TestApply_IsIdempotent verifies a re-run on a fully installed host
// emits no -N, no -A, no -I — only the read-only checks.
func TestApply_IsIdempotent(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Everything already present → all checks succeed.
			"-L KUKEON-FORWARD -n":         {},
			"-C FORWARD -j KUKEON-FORWARD": {},
		},
	}
	for _, r := range hostfw.BuildChainRules() {
		check := strings.Join(append([]string{"-C", r.Chain}, r.Args...), " ")
		runner.respond[check] = fakeResp{}
	}

	if err := newInstaller(runner).Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, c := range runner.calls {
		if len(c) == 0 {
			continue
		}
		switch c[0] {
		case "-N":
			t.Errorf("idempotent run must not -N a chain; got %v", c)
		case "-A":
			t.Errorf("idempotent run must not -A a rule; got %v", c)
		case "-I":
			t.Errorf("idempotent run must not -I a jump; got %v", c)
		}
	}
}

// TestApply_PlacesJumpAfterEgressChain is the regression guard for the
// ordering interaction with netpolicy: KUKEON-EGRESS at FORWARD pos 1
// must keep its slot so per-space DROP rules win over the blanket
// admission KUKEON-FORWARD provides. The installer must insert the
// KUKEON-FORWARD jump at position 2 in this case.
func TestApply_PlacesJumpAfterEgressChain(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-L KUKEON-FORWARD -n":         {err: errors.New("absent")},
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
			"-S FORWARD": {out: []byte(
				"-P FORWARD DROP\n" +
					"-A FORWARD -j KUKEON-EGRESS\n",
			)},
		},
	}
	for _, r := range hostfw.BuildChainRules() {
		check := strings.Join(append([]string{"-C", r.Chain}, r.Args...), " ")
		runner.respond[check] = fakeResp{err: errors.New("absent")}
	}

	if err := newInstaller(runner).Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !wasCalled(runner, []string{"-I", "FORWARD", "2", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("expected jump at position 2 (after KUKEON-EGRESS); calls = %v", runner.calls)
	}
	if wasCalled(runner, []string{"-I", "FORWARD", "1", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("must not insert at position 1 when KUKEON-EGRESS already holds it")
	}
}

// TestApply_FailureWrappedAsHostFwApply pins the public error contract:
// any iptables failure during Apply surfaces wrapped in
// errdefs.ErrHostFwApply so callers can match with errors.Is.
func TestApply_FailureWrappedAsHostFwApply(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-L KUKEON-FORWARD -n": {err: errors.New("absent")},
			"-N KUKEON-FORWARD":    {err: errors.New("permission denied")},
		},
	}
	err := newInstaller(runner).Apply(context.Background())
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !strings.Contains(err.Error(), "host firewall") {
		t.Errorf("error should mention host firewall: %v", err)
	}
}

func TestRemove_DeletesJumpAndChain(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-S FORWARD": {out: []byte(
				"-P FORWARD DROP\n" +
					"-A FORWARD -j KUKEON-FORWARD\n",
			)},
		},
	}

	if err := newInstaller(runner).Remove(context.Background()); err != nil {
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

// TestRemove_ToleratesMissingForwardListing exercises the case where
// `iptables -S FORWARD` itself errors (e.g., the FORWARD chain was
// somehow torn down). Remove must not propagate that error — it should
// proceed to flush+delete the chain best-effort.
func TestRemove_ToleratesMissingForwardListing(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-S FORWARD": {err: errors.New("FORWARD chain missing")},
		},
	}
	if err := newInstaller(runner).Remove(context.Background()); err != nil {
		t.Fatalf("Remove must tolerate missing FORWARD; got %v", err)
	}
}

// TestNoopInstallerSatisfiesInterface keeps the test fixture surface
// compatible with future Installer additions (compile-time check).
func TestNoopInstallerSatisfiesInterface(_ *testing.T) {
	var _ hostfw.Installer = hostfw.NoopInstaller{}
	var _ hostfw.Installer = (*hostfw.IptablesInstaller)(nil)
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
