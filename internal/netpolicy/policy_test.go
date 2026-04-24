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
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/netpolicy"
)

func TestFromInternal_NilAndEffectivelyAllow(t *testing.T) {
	p, err := netpolicy.FromInternal("r", "s", "br", nil)
	if err != nil || p != nil {
		t.Fatalf("nil policy expected nil,nil; got %v, %v", p, err)
	}

	p, err = netpolicy.FromInternal("r", "s", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultAllow,
	})
	if err != nil || p != nil {
		t.Fatalf("default=allow + no allow rules expected nil policy; got %+v, %v", p, err)
	}
}

func TestFromInternal_DenyMinimum(t *testing.T) {
	p, err := netpolicy.FromInternal("main", "blog", "main-blog", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatalf("expected non-nil policy")
	}
	if p.Default != intmodel.EgressDefaultDeny {
		t.Fatalf("default: got %q, want deny", p.Default)
	}
	if p.Bridge != "main-blog" {
		t.Fatalf("bridge: got %q, want main-blog", p.Bridge)
	}
}

func TestFromInternal_InvalidDefault(t *testing.T) {
	_, err := netpolicy.FromInternal("r", "s", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefault("maybe"),
	})
	if !errors.Is(err, errdefs.ErrEgressInvalidDefault) {
		t.Fatalf("expected ErrEgressInvalidDefault, got %v", err)
	}
}

func TestFromInternal_RuleValidation(t *testing.T) {
	cases := []struct {
		name    string
		rule    intmodel.EgressAllowRule
		wantErr error
	}{
		{"empty", intmodel.EgressAllowRule{}, errdefs.ErrEgressRuleTargetRequired},
		{"both set", intmodel.EgressAllowRule{Host: "example.com", CIDR: "10.0.0.0/8"}, errdefs.ErrEgressRuleTargetConflict},
		{"bad cidr", intmodel.EgressAllowRule{CIDR: "not-a-cidr"}, errdefs.ErrEgressInvalidCIDR},
		{"bad host", intmodel.EgressAllowRule{Host: "has spaces.com"}, errdefs.ErrEgressInvalidHost},
		{"bad port low", intmodel.EgressAllowRule{CIDR: "10.0.0.0/8", Ports: []int{0}}, errdefs.ErrEgressInvalidPort},
		{"bad port high", intmodel.EgressAllowRule{CIDR: "10.0.0.0/8", Ports: []int{70000}}, errdefs.ErrEgressInvalidPort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := netpolicy.FromInternal("r", "s", "br", &intmodel.EgressPolicy{
				Default: intmodel.EgressDefaultDeny,
				Allow:   []intmodel.EgressAllowRule{tc.rule},
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestFromInternal_PortsDedupedAndSorted(t *testing.T) {
	p, err := netpolicy.FromInternal("r", "s", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
		Allow: []intmodel.EgressAllowRule{
			{CIDR: "10.0.0.0/8", Ports: []int{443, 80, 443, 22}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Allow) != 1 {
		t.Fatalf("expected 1 allow rule, got %d", len(p.Allow))
	}
	got := p.Allow[0].Ports
	want := []int{22, 80, 443}
	if len(got) != len(want) {
		t.Fatalf("ports: got %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Fatalf("ports[%d]: got %d, want %d", i, got[i], p)
		}
	}
}

func TestChainNameStableAndScoped(t *testing.T) {
	p1, _ := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	p2, _ := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	p3, _ := netpolicy.FromInternal("main", "shop", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})
	p4, _ := netpolicy.FromInternal("dev", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
	})

	if p1.ChainName() != p2.ChainName() {
		t.Fatalf("chain must be stable across identical inputs: %s vs %s", p1.ChainName(), p2.ChainName())
	}
	if p1.ChainName() == p3.ChainName() {
		t.Fatalf("chain must differ across spaces in same realm")
	}
	if p1.ChainName() == p4.ChainName() {
		t.Fatalf("chain must differ across realms with same space name")
	}
	if !strings.HasPrefix(p1.ChainName(), "KUKE-EGR-") {
		t.Fatalf("chain prefix: got %q", p1.ChainName())
	}
	if len(p1.ChainName()) > 20 {
		t.Fatalf("chain name too long for iptables: %q (%d chars)", p1.ChainName(), len(p1.ChainName()))
	}
}
