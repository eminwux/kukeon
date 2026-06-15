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

// TestAdmissionRules_Order locks in the rule order and content. Since #1076
// the chain carries only the stateful return-traffic ACCEPT and the ingress
// admission — the host-global egress blanket (`-i k-+ ACCEPT`) is gone because
// it fails open (a Default=deny space whose per-space chain is missing would be
// ACCEPTed here). Egress admission is now per-space (internal/netpolicy).
func TestAdmissionRules_Order(t *testing.T) {
	rules := firewall.AdmissionRules()
	if len(rules) != 2 {
		t.Fatalf("want 2 admission rules; got %d: %v", len(rules), rules)
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

	// Rule 1: ! -i k-+ -o k-+ ACCEPT, tagged "kukeon-forward:ingress". The
	// `! -i k-+` scope excludes bridge-sourced traffic so a space's egress
	// (always bridge-sourced) can never be admitted here — only true external
	// ingress destined to a kukeon bridge.
	want1 := []string{
		"-A", firewall.ForwardChainName,
		"!", "-i", firewall.BridgeIfaceMatch,
		"-o", firewall.BridgeIfaceMatch,
		"-m", "comment", "--comment", "kukeon-forward:ingress",
		"-j", "ACCEPT",
	}
	if !sliceEq(rules[1], want1) {
		t.Errorf("rule 1: want %v; got %v", want1, rules[1])
	}
}

// TestAdmissionRules_NoEgressBlanket is the fail-closed guard for #1076: the
// host-global egress admission rule must never reappear in AdmissionRules. A
// *positive* blanket `-i k-+ ACCEPT` here is exactly the fail-open hole the
// per-space admission model closes, so its absence is a contract, not an
// accident. The *negated* `! -i k-+` qualifier on the ingress rule is the
// opposite — it excludes bridge-sourced traffic so a space's egress can never
// be admitted — and must be allowed.
func TestAdmissionRules_NoEgressBlanket(t *testing.T) {
	for _, r := range firewall.AdmissionRules() {
		joined := strings.Join(r, " ")
		// A positive `-i k-+` match (not the negated `! -i k-+` scope) is the
		// fail-open blanket. The negated form admits the inter-bridge fix, so
		// only flag `-i k-+` when it is not preceded by the `!` negation.
		if strings.Contains(joined, "-i "+firewall.BridgeIfaceMatch) &&
			!strings.Contains(joined, "! -i "+firewall.BridgeIfaceMatch) {
			t.Errorf("admission rules must not carry a host-global egress (-i %s) blanket; got %v",
				firewall.BridgeIfaceMatch, r)
		}
		if strings.Contains(joined, "kukeon-forward:egress") {
			t.Errorf("admission rules must not carry the retired :egress role; got %v", r)
		}
	}
}

// TestAdmissionRules_IngressExcludesBridgeSources is the inter-bridge
// fail-closed guard. The ingress ACCEPT must carry the `! -i k-+` scope so it
// admits only traffic that did *not* originate on a kukeon bridge. Without it,
// an inter-bridge packet (`-i k-A -o k-B`) — egress from deny-space A, ingress
// to B — would match a bare `-o k-+ ACCEPT` and be admitted here before A's
// KUKEON-EGRESS chain runs, a fail-open hole whenever KUKEON-FORWARD sits ahead
// of KUKEON-EGRESS (nothing orders them post-#1076). The negation routes every
// space's egress through KUKEON-EGRESS instead, so its position stays immaterial.
func TestAdmissionRules_IngressExcludesBridgeSources(t *testing.T) {
	var ingress []string
	for _, r := range firewall.AdmissionRules() {
		if strings.Contains(strings.Join(r, " "), "kukeon-forward:ingress") {
			ingress = r
			break
		}
	}
	if ingress == nil {
		t.Fatal("no ingress rule found in AdmissionRules")
	}
	// The rule must negate the bridge-source match: the tokens "!", "-i",
	// "k-+" must appear in that order so a space's (bridge-sourced) egress
	// cannot be admitted by the ingress ACCEPT.
	found := false
	for i := 0; i+2 < len(ingress); i++ {
		if ingress[i] == "!" && ingress[i+1] == "-i" && ingress[i+2] == firewall.BridgeIfaceMatch {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ingress rule must scope ingress with `! -i %s` to exclude bridge-sourced (egress) traffic; got %v",
			firewall.BridgeIfaceMatch, ingress)
	}
	// And it must still admit the external→bridge ingress it exists for.
	joined := strings.Join(ingress, " ")
	if !strings.Contains(joined, "-o "+firewall.BridgeIfaceMatch) || !strings.Contains(joined, "-j ACCEPT") {
		t.Errorf("ingress rule must still ACCEPT external traffic destined to a kukeon bridge (-o %s); got %v",
			firewall.BridgeIfaceMatch, ingress)
	}
}

// TestAdmissionRules_CommentTags pins the kukeon-forward:<role> comment
// tags as a public contract — the migration check in Install greps for
// the prefix, and any tooling that filters kukeon-installed rules in
// `iptables -S` output relies on the prefix being stable.
func TestAdmissionRules_CommentTags(t *testing.T) {
	wantRoles := []string{
		"kukeon-forward:established",
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

// TestInstall_IsIdempotent verifies a second install on a healthy post-#1076
// host performs zero -N, -A, -I, -D, or -F operations — only the read checks
// (-L/-C/-S). This is the "no rule churn" guarantee the issue calls out. The
// chain carries the new two-rule layout (established + ingress, no egress
// blanket), so the egress-prune finds nothing to delete.
func TestInstall_IsIdempotent(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// -L succeeds → chain present.
			// Migration + prune check: chain populated with the already-tagged
			// post-#1076 rules (no :egress) → no flush, no -D.
			"-S KUKEON-FORWARD": {out: []byte(
				"-N KUKEON-FORWARD\n" +
					"-A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment \"kukeon-forward:established\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD ! -i k-+ -o k-+ -m comment --comment \"kukeon-forward:ingress\" -j ACCEPT\n",
			)},
			// FORWARD jump present → ensureForwardJump's -C succeeds and it
			// re-inserts nothing. Position relative to KUKEON-EGRESS no longer
			// matters post-#1076.
			"-C FORWARD -j KUKEON-FORWARD": {},
		},
	}
	i := newInstaller(runner)

	if err := i.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for _, c := range runner.calls {
		switch c[0] {
		case "-N", "-I", "-A", "-D", "-F":
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

// TestInstall_JumpInsertedAtPositionOneWhenAbsent is the post-#1076 jump
// contract: when the FORWARD → KUKEON-FORWARD jump is absent (fresh host or
// post-reboot flush), it is inserted at position 1 — unconditionally, with no
// regard for where KUKEON-EGRESS sits. The ordering contract that once pinned
// it after KUKEON-EGRESS is gone, because egress is now decided inside each
// space's self-terminating chain.
func TestInstall_JumpInsertedAtPositionOneWhenAbsent(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-L KUKEON-FORWARD -n":         {err: errors.New("absent")},
			"-C FORWARD -j KUKEON-FORWARD": {err: errors.New("absent")},
			"-S KUKEON-FORWARD":            {out: []byte("-N KUKEON-FORWARD\n")},
			// KUKEON-EGRESS already at FORWARD position 1 — pre-#1076 this would
			// have forced the jump to position 2; now it must not.
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
	if !wasCalled(runner, []string{"-I", "FORWARD", "1", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("expected jump at position 1 regardless of KUKEON-EGRESS; calls = %v", runner.calls)
	}
	if wasCalled(runner, []string{"-I", "FORWARD", "2", "-j", "KUKEON-FORWARD"}) {
		t.Errorf("must not anchor the jump to KUKEON-EGRESS's position anymore; calls = %v", runner.calls)
	}
}

// TestInstall_JumpNoChurnWhenPresent verifies the jump is left untouched
// wherever it already sits. With egress decided per-space, the jump only needs
// to *exist* — Docker pushing it down the chain (DOCKER-USER / DOCKER-FORWARD
// at the top) or sitting it ahead of KUKEON-EGRESS is no longer churn-worthy,
// so the self-assert must neither delete nor re-insert it.
func TestInstall_JumpNoChurnWhenPresent(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// `-C FORWARD -j KUKEON-FORWARD` not canned → default success →
			// jump present → ensureForwardJump no-ops. Chain carries the
			// post-#1076 two-rule layout so prune finds nothing.
			"-S KUKEON-FORWARD": {out: []byte(
				"-N KUKEON-FORWARD\n" +
					"-A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment \"kukeon-forward:established\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD ! -i k-+ -o k-+ -m comment --comment \"kukeon-forward:ingress\" -j ACCEPT\n",
			)},
		},
	}

	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if wasCalledWithVerb(runner, "-I") {
		t.Errorf("must not re-insert the jump when it is already present; calls = %v", runner.calls)
	}
	if wasCalledWithVerb(runner, "-D") {
		t.Errorf("must not delete the jump when it is already present; calls = %v", runner.calls)
	}
}

// TestInstall_PrunesObsoleteEgressAdmission is the upgrade-path guard for
// #1076: a host upgraded from the pre-#1076 layout still carries the
// host-global egress blanket (`-i k-+ ... :egress -j ACCEPT`) in
// KUKEON-FORWARD. Leaving it would re-open the fail-open hole, so Install must
// -D it. The established + ingress rules already present must not be touched.
func TestInstall_PrunesObsoleteEgressAdmission(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Healthy chain carrying the retired :egress rule alongside the
			// current tagged rules → migration sees only tagged rules (no
			// flush), the ensureRule loop finds established+ingress present,
			// and the prune deletes the egress blanket.
			"-S KUKEON-FORWARD": {out: []byte(
				"-N KUKEON-FORWARD\n" +
					"-A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment \"kukeon-forward:established\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD -i k-+ -m comment --comment \"kukeon-forward:egress\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD ! -i k-+ -o k-+ -m comment --comment \"kukeon-forward:ingress\" -j ACCEPT\n",
			)},
			"-C FORWARD -j KUKEON-FORWARD": {},
		},
	}

	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	wantDel := []string{
		"-D", firewall.ForwardChainName,
		"-i", firewall.BridgeIfaceMatch,
		"-m", "comment", "--comment", "kukeon-forward:egress",
		"-j", "ACCEPT",
	}
	if !wasCalled(runner, wantDel) {
		t.Errorf("expected the obsolete egress blanket to be pruned via %v; calls = %v",
			wantDel, runner.calls)
	}
	// The chain must not be flushed (the current rules are tagged) and the jump
	// is present so nothing is re-inserted.
	if wasCalledWithVerb(runner, "-F") {
		t.Errorf("must not flush a chain carrying current tagged rules; calls = %v", runner.calls)
	}
}

// TestInstall_PrunesUnscopedIngressAdmission is the upgrade-path guard for the
// inter-bridge fix: a host upgraded from the intermediate #1076 layout carries
// the *unscoped* ingress rule (`-o k-+ ... :ingress`, no `! -i k-+`) that admits
// a deny-space's inter-bridge egress before its KUKEON-EGRESS chain runs.
// Install must -D that rule and leave only the scoped `! -i k-+ -o k-+` form.
func TestInstall_PrunesUnscopedIngressAdmission(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Intermediate-#1076 host: established (tagged) + the unscoped
			// ingress rule. Migration sees only tagged rules (no flush); the
			// scoped replacement is absent so it gets -A'd, and the prune -D's
			// the unscoped one.
			"-S KUKEON-FORWARD": {out: []byte(
				"-N KUKEON-FORWARD\n" +
					"-A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment \"kukeon-forward:established\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD -o k-+ -m comment --comment \"kukeon-forward:ingress\" -j ACCEPT\n",
			)},
			"-C FORWARD -j KUKEON-FORWARD": {},
		},
	}
	// Every current admission rule is absent in its current shape → each gets
	// -A'd (the scoped ingress form in particular).
	for _, rule := range firewall.AdmissionRules() {
		check := strings.Join(append([]string{"-C"}, rule[1:]...), " ")
		runner.respond[check] = fakeResp{err: errors.New("absent")}
	}

	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	wantDel := []string{
		"-D", firewall.ForwardChainName,
		"-o", firewall.BridgeIfaceMatch,
		"-m", "comment", "--comment", "kukeon-forward:ingress",
		"-j", "ACCEPT",
	}
	if !wasCalled(runner, wantDel) {
		t.Errorf("expected the unscoped ingress rule to be pruned via %v; calls = %v",
			wantDel, runner.calls)
	}
	// The scoped replacement must be installed.
	var scoped []string
	for _, r := range firewall.AdmissionRules() {
		if strings.Contains(strings.Join(r, " "), "kukeon-forward:ingress") {
			scoped = r
		}
	}
	if !wasCalled(runner, scoped) {
		t.Errorf("expected the scoped ingress rule %v to be installed; calls = %v", scoped, runner.calls)
	}
	// The chain must not be flushed (the existing rules are tagged).
	if wasCalledWithVerb(runner, "-F") {
		t.Errorf("must not flush a chain carrying tagged rules; calls = %v", runner.calls)
	}
}

// TestInstall_DoesNotPruneScopedIngress guards the idempotency of the prune:
// on a healthy post-fix host the ingress rule carries the `! -i k-+` scope, and
// the unscoped-ingress prune must leave it alone (it is the rule we want).
func TestInstall_DoesNotPruneScopedIngress(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-S KUKEON-FORWARD": {out: []byte(
				"-N KUKEON-FORWARD\n" +
					"-A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment \"kukeon-forward:established\" -j ACCEPT\n" +
					"-A KUKEON-FORWARD ! -i k-+ -o k-+ -m comment --comment \"kukeon-forward:ingress\" -j ACCEPT\n",
			)},
			"-C FORWARD -j KUKEON-FORWARD": {},
		},
	}
	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if wasCalledWithVerb(runner, "-D") {
		t.Errorf("must not -D the scoped ingress rule; calls = %v", runner.calls)
	}
}

// TestInstall_PruneNoopWhenNoEgressRule confirms the prune is a no-op on a
// host that never carried (or already shed) the egress blanket: no -D against
// KUKEON-FORWARD.
func TestInstall_PruneNoopWhenNoEgressRule(t *testing.T) {
	runner := installFreshHostRunner()
	if err := newInstaller(runner).Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if wasCalledWithVerb(runner, "-D") {
		t.Errorf("prune must not -D when no egress blanket is present; calls = %v", runner.calls)
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
