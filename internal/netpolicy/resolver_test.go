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
	"net"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/netpolicy"
)

type stubResolver struct {
	table map[string][]net.IP
	err   error
}

func (s stubResolver) LookupIP(_ context.Context, host string) ([]net.IP, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.table[host], nil
}

func TestResolve_PopulatesHostIPs(t *testing.T) {
	p, err := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
		Allow: []intmodel.EgressAllowRule{
			{Host: "api.example.com", Ports: []int{443}},
			{CIDR: "10.0.0.0/8", Ports: []int{5432}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	resolver := stubResolver{
		table: map[string][]net.IP{
			"api.example.com": {net.ParseIP("192.0.2.10"), net.ParseIP("192.0.2.11")},
		},
	}
	if err = p.Resolve(context.Background(), resolver); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := len(p.Allow[0].IPs); got != 2 {
		t.Fatalf("host rule: got %d IPs, want 2", got)
	}
	// CIDR rule untouched.
	if p.Allow[1].CIDR != "10.0.0.0/8" {
		t.Fatalf("cidr rule mutated: %+v", p.Allow[1])
	}
	if len(p.Allow[1].IPs) != 0 {
		t.Fatalf("cidr rule must not get IPs: %+v", p.Allow[1])
	}
}

func TestResolve_EmptyResultIsFatal(t *testing.T) {
	p, _ := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
		Allow: []intmodel.EgressAllowRule{
			{Host: "nothing.example.com", Ports: []int{443}},
		},
	})
	resolver := stubResolver{table: map[string][]net.IP{}}
	err := p.Resolve(context.Background(), resolver)
	if !errors.Is(err, errdefs.ErrEgressHostResolution) {
		t.Fatalf("want ErrEgressHostResolution, got %v", err)
	}
}

func TestResolve_FiltersV6AndDedups(t *testing.T) {
	p, _ := netpolicy.FromInternal("main", "blog", "br", &intmodel.EgressPolicy{
		Default: intmodel.EgressDefaultDeny,
		Allow: []intmodel.EgressAllowRule{
			{Host: "example.com", Ports: []int{443}},
		},
	})
	resolver := stubResolver{
		table: map[string][]net.IP{
			"example.com": {
				net.ParseIP("2001:db8::1"), // IPv6 — filtered
				net.ParseIP("192.0.2.10"),
				net.ParseIP("192.0.2.10"), // dup
			},
		},
	}
	if err := p.Resolve(context.Background(), resolver); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := len(p.Allow[0].IPs); got != 1 {
		t.Fatalf("after filter+dedup want 1 IP, got %d: %+v", got, p.Allow[0].IPs)
	}
	if p.Allow[0].IPs[0].String() != "192.0.2.10" {
		t.Fatalf("wrong IP kept: %s", p.Allow[0].IPs[0])
	}
}
