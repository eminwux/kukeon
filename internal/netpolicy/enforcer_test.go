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

package netpolicy_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/netpolicy"
)

// fakeRunner records every iptables invocation and returns canned responses
// keyed by the first few args. Unknown calls succeed silently.
type fakeRunner struct {
	calls [][]string
	// respond maps a space-joined prefix of args to (stdout, err).
	respond map[string]fakeResp
}

type fakeResp struct {
	out []byte
	err error
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	key := strings.Join(args, " ")
	if f.respond != nil {
		if r, ok := f.respond[key]; ok {
			return r.out, r.err
		}
	}
	return nil, nil
}

func newEnforcer(runner *fakeRunner) *netpolicy.IptablesEnforcer {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return netpolicy.NewIptablesEnforcerWithRunner(logger, runner)
}

func TestEnforcer_ApplyNilIsNoop(t *testing.T) {
	runner := &fakeRunner{}
	e := newEnforcer(runner)
	if err := e.Apply(context.Background(), nil); err != nil {
		t.Fatalf("nil Apply: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("nil Apply must not invoke iptables; got %v", runner.calls)
	}
}

func TestEnforcer_ApplyEmitsExpectedSequence(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Make -C and -L succeed where we want to skip the creation step.
			"-C FORWARD -j KUKEON-EGRESS": {err: errors.New("absent")},
			"-L KUKEON-EGRESS -n":         {err: errors.New("absent")},
		},
	}
	e := newEnforcer(runner)

	p, err := netpolicy.FromInternal("main", "blog", "br0", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
		Allow: []intmodel.EgressAllowRule{
			{CIDR: "10.0.0.0/8", Ports: []int{5432}},
		},
	})
	if err != nil {
		t.Fatalf("FromInternal: %v", err)
	}
	// Make the per-space chain appear absent so -N gets called.
	runner.respond["-L "+p.ChainName()+" -n"] = fakeResp{err: errors.New("absent")}

	if err = e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !wasCalled(runner, []string{"-N", "KUKEON-EGRESS"}) {
		t.Errorf("expected -N KUKEON-EGRESS; calls = %v", runner.calls)
	}
	if !wasCalled(runner, []string{"-I", "FORWARD", "1", "-j", "KUKEON-EGRESS"}) {
		t.Errorf("expected FORWARD jump to KUKEON-EGRESS; calls = %v", runner.calls)
	}
	if !wasCalled(runner, []string{"-N", p.ChainName()}) {
		t.Errorf("expected -N %s; calls = %v", p.ChainName(), runner.calls)
	}
	if !wasCalled(runner, []string{"-F", p.ChainName()}) {
		t.Errorf("expected -F %s (flush); calls = %v", p.ChainName(), runner.calls)
	}

	// Must include a DROP rule with the policy comment tag.
	foundDrop := false
	for _, c := range runner.calls {
		joined := strings.Join(c, " ")
		if strings.HasPrefix(joined, "-A "+p.ChainName()) && strings.Contains(joined, "-j DROP") {
			foundDrop = true
			break
		}
	}
	if !foundDrop {
		t.Errorf("expected terminal DROP rule; calls = %v", runner.calls)
	}
}

func TestEnforcer_ApplyIsIdempotentWhenMasterExists(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// Simulate master chain + FORWARD jump already present.
			"-L KUKEON-EGRESS -n":         {}, // nil err means "exists"
			"-C FORWARD -j KUKEON-EGRESS": {},
		},
	}
	e := newEnforcer(runner)

	p, _ := netpolicy.FromInternal("main", "blog", "br0", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	runner.respond["-L "+p.ChainName()+" -n"] = fakeResp{} // per-space chain exists too
	// Dispatch rule absent so we get one -A.
	dispatchCheckKey := "-C KUKEON-EGRESS -i br0 -m comment --comment kukeon:main:blog:dispatch -j " + p.ChainName()
	runner.respond[dispatchCheckKey] = fakeResp{err: errors.New("absent")}

	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// -N KUKEON-EGRESS must NOT be invoked since -L succeeded.
	if wasCalled(runner, []string{"-N", "KUKEON-EGRESS"}) {
		t.Errorf("must not re-create existing master chain")
	}
	// -I FORWARD jump also must not be re-inserted.
	if wasCalled(runner, []string{"-I", "FORWARD", "1", "-j", "KUKEON-EGRESS"}) {
		t.Errorf("must not re-insert FORWARD jump when -C succeeds")
	}
	// Dispatch append should have occurred.
	if !wasCalled(runner, []string{"-A", "KUKEON-EGRESS", "-i", "br0", "-m", "comment",
		"--comment", "kukeon:main:blog:dispatch", "-j", p.ChainName()}) {
		t.Errorf("expected dispatch -A; calls = %v", runner.calls)
	}
}

func TestEnforcer_RemoveFlushesAndDeletes(t *testing.T) {
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			// No dispatch rules — master list empty.
			"-S KUKEON-EGRESS": {out: []byte("-N KUKEON-EGRESS\n")},
		},
	}
	e := newEnforcer(runner)
	if err := e.Remove(context.Background(), "main", "blog"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	chain := chainFor("main", "blog")
	if !wasCalled(runner, []string{"-F", chain}) {
		t.Errorf("expected -F %s; calls = %v", chain, runner.calls)
	}
	if !wasCalled(runner, []string{"-X", chain}) {
		t.Errorf("expected -X %s; calls = %v", chain, runner.calls)
	}
}

func TestEnforcer_RemoveDeletesDispatchRules(t *testing.T) {
	// Build a Policy so we can get the chain name the enforcer will use.
	p, _ := netpolicy.FromInternal("main", "blog", "br0", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	chain := p.ChainName()

	// iptables -S output containing one matching rule.
	listOut := "-N KUKEON-EGRESS\n" +
		"-A KUKEON-EGRESS -i br0 -m comment --comment \"kukeon:main:blog:dispatch\" -j " + chain + "\n"
	runner := &fakeRunner{
		respond: map[string]fakeResp{
			"-S KUKEON-EGRESS": {out: []byte(listOut)},
		},
	}
	e := newEnforcer(runner)
	if err := e.Remove(context.Background(), "main", "blog"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Must emit a -D with the args from the -A line.
	foundDelete := false
	for _, c := range runner.calls {
		joined := strings.Join(c, " ")
		if strings.HasPrefix(joined, "-D KUKEON-EGRESS") && strings.Contains(joined, "-j "+chain) {
			foundDelete = true
			break
		}
	}
	if !foundDelete {
		t.Errorf("expected -D dispatch rule; calls = %v", runner.calls)
	}
}

func TestNoopEnforcerMatchesInterface(t *testing.T) {
	var _ netpolicy.Enforcer = netpolicy.NoopEnforcer{}
	var _ netpolicy.Enforcer = (*netpolicy.IptablesEnforcer)(nil)
}

// chainFor mirrors netpolicy.chainName for test-side lookups. The function
// is unexported in the package; we reuse FromInternal to reach ChainName.
func chainFor(realm, space string) string {
	p, _ := netpolicy.FromInternal(realm, space, "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	return p.ChainName()
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
