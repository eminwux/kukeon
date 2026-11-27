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

package hostnet

import (
	"fmt"
	"net/netip"
	"sort"
)

// Conflict kinds reported by Conflicts.
const (
	ConflictAddress = "interface address"
	ConflictRoute   = "route"
	ConflictGateway = "gateway"
)

// Conflict is one piece of host network state a pod subnet overlaps.
type Conflict struct {
	Kind string
	// Link is the interface the address or route is on; empty for a
	// multipath route.
	Link string
	// Prefix is the address with its prefix length, the route destination,
	// or the gateway as a host prefix.
	Prefix netip.Prefix
	// Table is the routing table of a route or gateway conflict.
	Table uint32
}

func (c Conflict) String() string {
	s := c.Kind + " " + c.Prefix.String()
	if c.Kind == ConflictGateway {
		s = c.Kind + " " + c.Prefix.Addr().String()
	}
	if c.Link != "" {
		s += " dev " + c.Link
	}
	if c.Kind != ConflictAddress && c.Table != 0 && c.Table != MainTable {
		s += fmt.Sprintf(" table %d", c.Table)
	}
	return s
}

// OwnFunc reports whether prefix on link belongs to the instance being
// checked (for example its own space bridge carrying that space's subnet).
// Conflicts skips such entries so a rerun does not reject itself.
type OwnFunc func(link string, prefix netip.Prefix) bool

// Conflicts returns every entry of st that subnet overlaps: interface
// addresses (all links, including DOWN ones), route destinations in every
// table, and route gateways. A default route's destination is ignored, but
// its gateway is not. own may be nil. The result is sorted and has no
// duplicates.
func Conflicts(subnet netip.Prefix, st State, own OwnFunc) []Conflict {
	subnet = subnet.Masked()
	isOwn := func(link string, p netip.Prefix) bool { return own != nil && own(link, p) }
	seen := make(map[string]bool)
	var out []Conflict
	add := func(c Conflict) {
		if key := c.String(); !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	for _, a := range st.Addresses {
		if a.Prefix.IsValid() && subnet.Overlaps(a.Prefix) && !isOwn(a.Link, a.Prefix) {
			add(Conflict{Kind: ConflictAddress, Link: a.Link, Prefix: a.Prefix})
		}
	}
	for _, r := range st.Routes {
		for _, gw := range r.Gateways {
			host := netip.PrefixFrom(gw, gw.BitLen())
			if subnet.Contains(gw) && !isOwn(r.Link, host) {
				add(Conflict{Kind: ConflictGateway, Link: r.Link, Prefix: host, Table: r.Table})
			}
		}
		if !r.IsDefault() && subnet.Overlaps(r.Dst) && !isOwn(r.Link, r.Dst) {
			add(Conflict{Kind: ConflictRoute, Link: r.Link, Prefix: r.Dst, Table: r.Table})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// OwnBridges returns an OwnFunc that accepts entries on the named bridges
// that lie inside that bridge's subnet. Anything else on such a bridge (an
// address outside its subnet) still conflicts.
func OwnBridges(bridges map[string]netip.Prefix) OwnFunc {
	return func(link string, prefix netip.Prefix) bool {
		subnet, ok := bridges[link]
		if !ok || !subnet.IsValid() {
			return false
		}
		subnet = subnet.Masked()
		return prefix.Bits() >= subnet.Bits() && subnet.Contains(prefix.Addr())
	}
}
