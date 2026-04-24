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
	"net"
	"strings"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/netpolicy"
)

func TestBuildRules_DenyDefaultHasTerminalDrop(t *testing.T) {
	p, err := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rules := netpolicy.BuildRules(p)
	if len(rules) < 2 {
		t.Fatalf("expected at least 2 rules (conntrack + default), got %d", len(rules))
	}

	last := rules[len(rules)-1]
	joined := strings.Join(last.Args, " ")
	if !strings.Contains(joined, "-j DROP") {
		t.Fatalf("deny default must terminate in DROP; terminal = %q", joined)
	}
	if !strings.Contains(joined, ":default") {
		t.Fatalf("terminal rule must carry :default comment; got %q", joined)
	}

	first := rules[0]
	firstJoined := strings.Join(first.Args, " ")
	if !strings.Contains(firstJoined, "RELATED,ESTABLISHED") {
		t.Fatalf("first rule must allow RELATED,ESTABLISHED; got %q", firstJoined)
	}
	if !strings.Contains(firstJoined, "-j RETURN") {
		t.Fatalf("first rule must RETURN; got %q", firstJoined)
	}
}

func TestBuildRules_AllowDefaultHasTerminalReturn(t *testing.T) {
	// An explicit allow default with at least one allow rule (otherwise
	// FromInternal collapses to nil).
	p, err := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultAllow,
		Allow: []intmodel.EgressAllowRule{
			{CIDR: "10.0.0.0/8", Ports: []int{5432}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rules := netpolicy.BuildRules(p)
	last := rules[len(rules)-1]
	joined := strings.Join(last.Args, " ")
	if !strings.Contains(joined, "-j RETURN") {
		t.Fatalf("allow default must terminate in RETURN; got %q", joined)
	}
}

func TestBuildRules_CIDRAllowExpandsByPort(t *testing.T) {
	p, err := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
		Allow: []intmodel.EgressAllowRule{
			{CIDR: "10.0.0.0/8", Ports: []int{443, 5432}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rules := netpolicy.BuildRules(p)
	// conntrack + 2 port rules + terminal = 4
	if len(rules) != 4 {
		t.Fatalf("expected 4 rules, got %d: %+v", len(rules), rules)
	}
	// Rules 1 and 2 should target the CIDR with distinct dports.
	for i, idx := range []int{1, 2} {
		joined := strings.Join(rules[idx].Args, " ")
		if !strings.Contains(joined, "-d 10.0.0.0/8") {
			t.Errorf("rule %d: missing -d 10.0.0.0/8: %q", i, joined)
		}
		if !strings.Contains(joined, "-p tcp") {
			t.Errorf("rule %d: missing -p tcp: %q", i, joined)
		}
		if !strings.Contains(joined, "--dport") {
			t.Errorf("rule %d: missing --dport: %q", i, joined)
		}
		if !strings.Contains(joined, "-j RETURN") {
			t.Errorf("rule %d: missing -j RETURN: %q", i, joined)
		}
	}
}

func TestBuildRules_HostAllowUsesResolvedIPs(t *testing.T) {
	p, err := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
		Allow: []intmodel.EgressAllowRule{
			{Host: "api.example.com", Ports: []int{443}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stub resolver returning two IPs.
	p.Allow[0].IPs = []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("192.0.2.11")}

	rules := netpolicy.BuildRules(p)
	// conntrack + 2 per-IP rules + terminal = 4
	if len(rules) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(rules))
	}
	found10, found11 := false, false
	for _, r := range rules[1:3] {
		joined := strings.Join(r.Args, " ")
		if strings.Contains(joined, "-d 192.0.2.10/32") {
			found10 = true
		}
		if strings.Contains(joined, "-d 192.0.2.11/32") {
			found11 = true
		}
		if !strings.Contains(joined, "host=api.example.com") {
			t.Errorf("allow rule missing host comment: %q", joined)
		}
	}
	if !found10 || !found11 {
		t.Fatalf("both resolved IPs must appear; found10=%v found11=%v", found10, found11)
	}
}

func TestBuildRules_NoPortsMeansAnyDest(t *testing.T) {
	p, err := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
		Allow: []intmodel.EgressAllowRule{
			{CIDR: "172.16.0.0/12"}, // no ports
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rules := netpolicy.BuildRules(p)
	// conntrack + 1 allow rule + terminal = 3
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}
	joined := strings.Join(rules[1].Args, " ")
	if strings.Contains(joined, "-p tcp") || strings.Contains(joined, "--dport") {
		t.Fatalf("no-ports rule must not constrain protocol/port; got %q", joined)
	}
	if !strings.Contains(joined, "-d 172.16.0.0/12") {
		t.Fatalf("missing -d target: %q", joined)
	}
}

func TestDispatchRule(t *testing.T) {
	p, _ := netpolicy.FromInternal("main", "blog", "main-blog", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	d := netpolicy.DispatchRule(p)
	if d.Chain != netpolicy.MasterChainName {
		t.Fatalf("dispatch chain: got %q, want %q", d.Chain, netpolicy.MasterChainName)
	}
	joined := strings.Join(d.Args, " ")
	if !strings.Contains(joined, "-i main-blog") {
		t.Fatalf("dispatch must match bridge -i; got %q", joined)
	}
	if !strings.Contains(joined, "-j "+p.ChainName()) {
		t.Fatalf("dispatch must jump to per-space chain; got %q", joined)
	}
}
