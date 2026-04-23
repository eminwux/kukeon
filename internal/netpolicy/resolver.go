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

package netpolicy

import (
	"context"
	"fmt"
	"net"
	"sort"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// HostResolver resolves a hostname to its IP addresses at policy-apply time.
// The interface is explicit so tests can substitute deterministic lookups.
type HostResolver interface {
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
}

// NetHostResolver uses net.DefaultResolver. IPv6 addresses are filtered out
// because the iptables applier only emits IPv4 rules today; IPv6 support is
// out of scope for the MVP.
type NetHostResolver struct{}

func (NetHostResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	return addrs, nil
}

// Resolve expands every Allow rule that uses a Host into a rule populated
// with IPs. CIDR-based rules are passed through unchanged. A host that
// resolves to zero IPv4 addresses is a fatal error so operators don't
// silently end up with a deny-only policy.
func (p *Policy) Resolve(ctx context.Context, resolver HostResolver) error {
	if resolver == nil {
		resolver = NetHostResolver{}
	}
	for i := range p.Allow {
		rule := &p.Allow[i]
		if rule.OriginalHost == "" {
			continue
		}
		ips, err := resolver.LookupIP(ctx, rule.OriginalHost)
		if err != nil {
			return fmt.Errorf("%w: %q: %w", errdefs.ErrEgressHostResolution, rule.OriginalHost, err)
		}
		ips = dedupAndSortIPs(ips)
		if len(ips) == 0 {
			return fmt.Errorf("%w: %q resolved to no IPv4 addresses",
				errdefs.ErrEgressHostResolution, rule.OriginalHost)
		}
		rule.IPs = ips
	}
	return nil
}

func dedupAndSortIPs(in []net.IP) []net.IP {
	seen := make(map[string]struct{}, len(in))
	out := make([]net.IP, 0, len(in))
	for _, ip := range in {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		key := v4.String()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v4)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
