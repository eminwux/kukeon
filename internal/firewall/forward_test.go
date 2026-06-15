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

	// Rule 0: established/related ACCEPT, tagged "kukeon-forward:established".
	want0 := []string{
		"-A", firewall.ForwardChainName,
		"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED",
		"-m", "comment", "--comment", "kukeon-forward:established",
		"-j", "ACCEPT",
	}
	if !sliceEq(rules[0], want0) {
		t.Errorf("rule 0: want %v; got %v", want0, rules[0])
	}

	// Rule 1: -i k-+ ACCEPT, tagged "kukeon-forward:egress".
	want1 := []string{
		"-A", firewall.ForwardChainName,
		"-i", firewall.BridgeIfaceMatch,
		"-m", "comment", "--comment", "kukeon-forward:egress",
		"-j", "ACCEPT",
	}
	if !sliceEq(rules[1], want1) {
		t.Errorf("rule 1: want %v; got %v", want1, rules[1])
	}

	// Rule 2: -o k-+ ACCEPT, tagged "kukeon-forward:ingress".
	want2 := []string{
		"-A", firewall.ForwardChainName,
		"-o", firewall.BridgeIfaceMatch,
		"-m", "comment", "--comment", "kukeon-forward:ingress",
		"-j", "ACCEPT",
	}
	if !sliceEq(rules[2], want2) {
		t.Errorf("rule 2: want %v; got %v", want2, rules[2])
	}
}

// TestAdmissionRules_CommentTags pins the kukeon-forward:<role> comment
// tags as a public contract — the migration check in Install greps for
// the prefix, and any tooling that filters kukeon-installed rules in
// `iptables -S` output relies on the prefix being stable.
func TestAdmissionRules_CommentTags(t *testing.T) {
	wantRoles := []string{
		"kukeon-forward:established",
		"kukeon-forward:egress",
		"kukeon-forward:ingress",
	}
	rules := firewall.AdmissionRules()
	if len(rules) != len(wantRoles) {
		t.Fatalf("want %d rules; got %d", len(wantRoles), len(rules))
	}
	for i, r := range rules {
		joined := strings.Join(r, " ")
		if !strings.Contains(joined, "-m comment --comment "+wantRoles[i]) {
			t.Errorf("rule %d missing %q tag; got %v", i, wantRoles[i], r)
		}
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

// installFreshHostRunner builds a fakeRunner that simulates a host with no
// pre-existing KUKEON-FORWARD state: chain absent, each rule absent, jump
// absent. Shared by the install-path tests below.
func installFreshHostRunner() *fakeRunner {
	r := &fakeRunner{
		respond: map[string]fakeResp{
			"-L KUKEON-FORWARD -n":         {err: errors.New("absent")},
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
			// Chain newly created → -S returns no -A lines, so migration
			// finds nothing to flush.
			"-S KUKEON-FORWARD": {out: []byte("-N KUKEON-FORWARD\n")},
			// FORWARD chain has no KUKEON-EGRESS jump → position falls
			// back to 1.
			"-S FORWARD": {out: []byte("-P FORWARD ACCEPT\n")},
		},
	}
	for _, rule := range firewall.AdmissionRules() {
		check := strings.Join(append([]string{"-C"}, rule[1:]...), " ")
		r.respond[check] = fakeResp{err: errors.New("absent")}
	}
	return r
}

// TestInstall_OnFreshHost emits -N, three -A rules in order, and the FORWARD
// position-1 jump when nothing is already in place.
func TestInstall_OnFreshHost(t *testing.T) {
	runner := installFreshHostRunner()
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
	// On a freshly created chain (no pre-existing -A lines), the migration
	// must not flush — flushing here would be a write churn on every fresh
	// install for no reason.
	if wasCalledWithVerb(runner, "-F") {
		t.Errorf("freshly-created chain must not be flushed; calls = %v", runner.calls)
	}
}

// TestInstall_IsIdempotent verifies a second install on a healthy host
// performs zero -N, -A, -I, or -F operations — only the read checks
// (-L/-C/-S). This is the "no rule churn" guarantee the issue calls out.
func TestInstall_IsIdempotent(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// -L succeeds → chain present.
			// Migration check: chain populated with already-tagged rules → no flush.
			"-S KUKEON-FORWARD": {out: []byte(
				"-N KUKEON-FORWARD\n" +
					"-A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment \"kukeon-forward:established\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD -i k-+ -m comment --comment \"kukeon-forward:egress\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD -o k-+ -m comment --comment \"kukeon-forward:ingress\" -j ACCEPT\n",
			)},
			// FORWARD jump present and correctly ordered after KUKEON-EGRESS →
			// ensureForwardJump finds it healthy and re-claims nothing.
			"-S FORWARD": {out: []byte(
				"-P FORWARD DROP\n" +
					"-A FORWARD -j KUKEON-EGRESS\n" +
					"-A FORWARD -j KUKEON-FORWARD\n",
			)},
		},
	}
	i := newInstaller(runner)

	if err := i.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for _, c := range runner.calls {
		switch c[0] {
		case "-N", "-I", "-A", "-F":
			t.Errorf("idempotent install must not invoke %s; got call %v", c[0], c)
		}
	}
}

// TestInstall_MigratesUntaggedRules locks in the upgrade-path migration:
// when the chain already exists with the pre-#315 bare rules, Install must
// flush it once before -A-ing the tagged variants so the chain does not end
// up carrying both rule sets side by side.
func TestInstall_MigratesUntaggedRules(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Chain present (older bare-rule install).
			// Migration check sees three untagged rules → must flush.
			"-S KUKEON-FORWARD": {out: []byte(
				"-N KUKEON-FORWARD\n" +
					"-A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT\n" +
					"-A KUKEON-FORWARD -i k-+ -j ACCEPT\n" +
					"-A KUKEON-FORWARD -o k-+ -j ACCEPT\n",
			)},
			// Jump already present (upgrade path migrates the child chain's
			// rules, not the FORWARD jump) → ensureForwardJump no-ops.
			"-S FORWARD": {out: []byte(
				"-P FORWARD DROP\n" +
					"-A FORWARD -j KUKEON-FORWARD\n",
			)},
		},
	}
	// After the flush, every -C against a tagged rule fails so each gets -A'd.
	for _, rule := range firewall.AdmissionRules() {
		check := strings.Join(append([]string{"-C"}, rule[1:]...), " ")
		runner.respond[check] = fakeResp{err: errors.New("absent")}
	}

	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if !wasCalled(runner, []string{"-F", "KUKEON-FORWARD"}) {
		t.Errorf("expected -F KUKEON-FORWARD on upgrade path; calls = %v", runner.calls)
	}
	// After the flush, every tagged rule must be -A'd.
	for _, r := range firewall.AdmissionRules() {
		if !wasCalled(runner, r) {
			t.Errorf("expected tagged rule install %v after migration flush; calls = %v", r, runner.calls)
		}
	}
}

// TestInstall_NoFlushOnFreshChain pins the no-double-append AC item: a
// freshly created chain (no -A lines yet) must not trigger the migration
// flush. Run the same fresh-host harness and assert -F is never called.
func TestInstall_NoFlushOnFreshChain(t *testing.T) {
	runner := installFreshHostRunner()
	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if wasCalledWithVerb(runner, "-F") {
		t.Errorf("freshly-created chain must not be flushed; calls = %v", runner.calls)
	}
	// And no double-append: each rule appears exactly once.
	for _, r := range firewall.AdmissionRules() {
		got := countCalls(runner, r)
		if got != 1 {
			t.Errorf("rule %v -A'd %d times; want 1", r, got)
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

// TestInstall_JumpAfterEgressChain is the regression guard for the ordering
// interaction with netpolicy: when KUKEON-EGRESS already lives in FORWARD,
// KUKEON-FORWARD must be inserted at position EGRESS+1 so per-space DROP
// rules win over the blanket admission. This guards the scenario where the
// runner restart path reapplies netpolicy before forward admission.
func TestInstall_JumpAfterEgressChain(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-L KUKEON-FORWARD -n":         {err: errors.New("absent")},
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
			"-S KUKEON-FORWARD":            {out: []byte("-N KUKEON-FORWARD\n")},
			"-S FORWARD": {out: []byte(
				"-P FORWARD DROP\n" +
					"-A FORWARD -j KUKEON-EGRESS\n",
			)},
		},
	}
	for _, rule := range firewall.AdmissionRules() {
		check := strings.Join(append([]string{"-C"}, rule[1:]...), " ")
		runner.respond[check] = fakeResp{err: errors.New("absent")}
	}

	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !wasCalled(runner, []string{"-I", "FORWARD", "2", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("expected jump at position 2 (after KUKEON-EGRESS); calls = %v", runner.calls)
	}
	if wasCalled(runner, []string{"-I", "FORWARD", "1", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("must not insert at position 1 when KUKEON-EGRESS already holds it")
	}
}

// TestInstall_JumpAtPositionNplus1 covers the deeper case: KUKEON-EGRESS
// at a non-1 position (after some unrelated rules) must still anchor the
// KUKEON-FORWARD jump immediately after it.
func TestInstall_JumpAtPositionNplus1(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-L KUKEON-FORWARD -n":         {err: errors.New("absent")},
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
			"-S KUKEON-FORWARD":            {out: []byte("-N KUKEON-FORWARD\n")},
			"-S FORWARD": {out: []byte(
				"-P FORWARD DROP\n" +
					"-A FORWARD -i docker0 -j ACCEPT\n" +
					"-A FORWARD -j KUKEON-EGRESS\n",
			)},
		},
	}
	for _, rule := range firewall.AdmissionRules() {
		check := strings.Join(append([]string{"-C"}, rule[1:]...), " ")
		runner.respond[check] = fakeResp{err: errors.New("absent")}
	}

	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !wasCalled(runner, []string{"-I", "FORWARD", "3", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("expected jump at position 3 (after KUKEON-EGRESS at 2); calls = %v", runner.calls)
	}
}

// TestInstall_DoesNotMatchEgressLikeSiblingChain pins the token-aware
// match in findEgressPosition: a chain named like KUKEON-EGRESS-FOO must
// not be mistaken for KUKEON-EGRESS. With only the sibling chain at
// FORWARD pos 1 and KUKEON-EGRESS absent, the installer must insert
// KUKEON-FORWARD at position 1, not 2.
func TestInstall_DoesNotMatchEgressLikeSiblingChain(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-L KUKEON-FORWARD -n":         {err: errors.New("absent")},
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
			"-S KUKEON-FORWARD":            {out: []byte("-N KUKEON-FORWARD\n")},
			"-S FORWARD": {out: []byte(
				"-P FORWARD DROP\n" +
					"-A FORWARD -j KUKEON-EGRESS-FOO\n",
			)},
		},
	}
	for _, rule := range firewall.AdmissionRules() {
		check := strings.Join(append([]string{"-C"}, rule[1:]...), " ")
		runner.respond[check] = fakeResp{err: errors.New("absent")}
	}

	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !wasCalled(runner, []string{"-I", "FORWARD", "1", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("expected jump at position 1 when only a sibling chain is present; calls = %v", runner.calls)
	}
	if wasCalled(runner, []string{"-I", "FORWARD", "2", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("must not match KUKEON-EGRESS-FOO as KUKEON-EGRESS")
	}
}

// TestInstall_SurvivesDockerChurnWithoutReorder is #1075 AC #4: when Docker
// re-asserts its own chains (DOCKER-USER / DOCKER-FORWARD) at the top of
// FORWARD it pushes kukeon's jumps down but never deletes them and never
// inverts their relative order. Because KUKEON-FORWARD is ACCEPT-only and
// position relative to Docker is immaterial to correctness, the self-assert
// must treat this as healthy — no delete, no re-insert, no churn.
func TestInstall_SurvivesDockerChurnWithoutReorder(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Docker pushed kukeon's jumps down the chain; KUKEON-EGRESS is
			// still ahead of KUKEON-FORWARD, so the ordering invariant holds.
			"-S FORWARD": {out: []byte(
				"-P FORWARD DROP\n" +
					"-A FORWARD -j DOCKER-USER\n" +
					"-A FORWARD -j DOCKER-FORWARD\n" +
					"-A FORWARD -j KUKEON-EGRESS\n" +
					"-A FORWARD -j KUKEON-FORWARD\n",
			)},
			// Chain + tagged rules already present → no migration/rule churn.
			"-S KUKEON-FORWARD": {out: []byte(
				"-N KUKEON-FORWARD\n" +
					"-A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment \"kukeon-forward:established\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD -i k-+ -m comment --comment \"kukeon-forward:egress\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD -o k-+ -m comment --comment \"kukeon-forward:ingress\" -j ACCEPT\n",
			)},
		},
	}

	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if wasCalledWithVerb(runner, "-D") {
		t.Errorf("must not delete the jump when Docker merely reordered it; calls = %v", runner.calls)
	}
	if wasCalledWithVerb(runner, "-I") {
		t.Errorf("must not re-insert the jump under harmless Docker churn; calls = %v", runner.calls)
	}
}

// invertedForwardRunner simulates a FORWARD chain where KUKEON-FORWARD sits
// *ahead* of KUKEON-EGRESS — the displacement that lets the blanket admission
// shadow per-space egress DROP rules. After the self-assert deletes the
// misplaced jump, the second `-S FORWARD` read reflects the post-delete state
// (KUKEON-EGRESS shifted up to position 1) so the re-insert lands in the
// correct slot. The stateless fakeRunner cannot model that shift, hence this
// purpose-built stateful runner.
type invertedForwardRunner struct {
	calls   [][]string
	deleted bool
}

func (r *invertedForwardRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	joined := strings.Join(args, " ")
	switch {
	case joined == "-S FORWARD":
		if r.deleted {
			// KUKEON-FORWARD removed; KUKEON-EGRESS now at position 1.
			return []byte("-P FORWARD DROP\n-A FORWARD -j KUKEON-EGRESS\n"), nil
		}
		// Inverted: KUKEON-FORWARD (1) ahead of KUKEON-EGRESS (2).
		return []byte(
			"-P FORWARD DROP\n" +
				"-A FORWARD -j KUKEON-FORWARD\n" +
				"-A FORWARD -j KUKEON-EGRESS\n",
		), nil
	case joined == "-D FORWARD -j KUKEON-FORWARD":
		r.deleted = true
		return nil, nil
	default:
		// All existence probes (-L chain, -C rule, -S KUKEON-FORWARD) report
		// "present/empty" so Install skips migration and rule churn and
		// exercises only the jump self-assert.
		return nil, nil
	}
}

func (r *invertedForwardRunner) ran(prefix ...string) bool {
	for _, got := range r.calls {
		if len(got) < len(prefix) {
			continue
		}
		match := true
		for i := range prefix {
			if got[i] != prefix[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestInstall_ReclaimsDisplacedJump is #1075 AC #1 (the "displaced" arm):
// when KUKEON-FORWARD is present but ahead of KUKEON-EGRESS, the self-assert
// deletes the misplaced jump and re-inserts it immediately after
// KUKEON-EGRESS so per-space egress DROP rules win over the blanket admission.
func TestInstall_ReclaimsDisplacedJump(t *testing.T) {
	runner := &invertedForwardRunner{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	installer := firewall.NewInstallerWithRunner(logger, runner)
	if err := installer.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !runner.ran("-D", "FORWARD", "-j", "KUKEON-FORWARD") {
		t.Errorf("expected the displaced jump to be deleted; calls = %v", runner.calls)
	}
	// After the delete, KUKEON-EGRESS is at position 1, so the jump must be
	// re-inserted at position 2 (immediately after it).
	if !runner.ran("-I", "FORWARD", "2", "-j", "KUKEON-FORWARD") {
		t.Errorf("expected re-insert at position 2 (after KUKEON-EGRESS); calls = %v", runner.calls)
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

// TestIsIptablesAvailable_FalseWhenAbsent pins the helper's behavior on
// hosts without the binary on PATH: by clearing PATH the LookPath call
// must miss, so the helper returns false. This is the signal init.go
// uses to log WARN and continue instead of aborting bring-up.
func TestIsIptablesAvailable_FalseWhenAbsent(t *testing.T) {
	t.Setenv("PATH", "")
	if firewall.IsIptablesAvailable() {
		t.Error("IsIptablesAvailable must return false with empty PATH")
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

func wasCalledWithVerb(f *fakeRunner, verb string) bool {
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == verb {
			return true
		}
	}
	return false
}

func countCalls(f *fakeRunner, want []string) int {
	n := 0
	for _, got := range f.calls {
		if sliceEq(got, want) {
			n++
		}
	}
	return n
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
