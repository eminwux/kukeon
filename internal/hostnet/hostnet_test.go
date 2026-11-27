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
	"net"
	"net/netip"
	"reflect"
	"testing"

	"github.com/jsimonetti/rtnetlink/v2"
	"golang.org/x/sys/unix"
)

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

// parentCell is the network state inside a kukeon-dev-root cell whose parent
// uses the default 10.88.0.0/16 space subnet range.
func parentCell() State {
	return State{
		Links: []Link{
			{Index: 1, Name: "lo", Up: true},
			{Index: 2, Name: "eth0", Kind: "veth", Up: true},
		},
		Addresses: []Address{
			{Link: "lo", Prefix: pfx("127.0.0.1/8")},
			{Link: "eth0", Prefix: pfx("10.88.0.15/24")},
		},
		Routes: []Route{
			{Table: MainTable, Link: "eth0", Dst: pfx("0.0.0.0/0"), Gateways: []netip.Addr{addr("10.88.0.1")}},
			{Table: MainTable, Link: "eth0", Dst: pfx("10.88.0.0/24")},
			{Table: unix.RT_TABLE_LOCAL, Link: "eth0", Dst: pfx("10.88.0.15/32")},
		},
	}
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		name   string
		subnet string
		st     State
		want   []string
	}{
		{
			name:   "parent space subnet range reused",
			subnet: "10.88.0.0/16",
			st:     parentCell(),
			want: []string{
				"gateway 10.88.0.1 dev eth0",
				"interface address 10.88.0.15/24 dev eth0",
				"route 10.88.0.0/24 dev eth0",
				"route 10.88.0.15/32 dev eth0 table 255",
			},
		},
		{name: "disjoint subnet", subnet: "10.89.0.0/16", st: parentCell()},
		{
			name:   "multipath gateway",
			subnet: "10.88.0.0/16",
			st: State{Routes: []Route{{
				Table: MainTable, Dst: pfx("0.0.0.0/0"),
				Gateways: []netip.Addr{addr("192.168.1.1"), addr("10.88.0.1")},
			}}},
			want: []string{"gateway 10.88.0.1"},
		},
		{
			name:   "specific route in another table",
			subnet: "10.192.0.0/12",
			st:     State{Routes: []Route{{Table: 100, Link: "wg0", Dst: pfx("10.192.1.0/24")}}},
			want:   []string{"route 10.192.1.0/24 dev wg0 table 100"},
		},
		{
			name:   "address on a down bridge",
			subnet: "10.192.0.0/12",
			st: State{
				Links:     []Link{{Name: "k-leaked", Kind: "bridge"}},
				Addresses: []Address{{Link: "k-leaked", Prefix: pfx("10.192.0.1/24")}},
			},
			want: []string{"interface address 10.192.0.1/24 dev k-leaked"},
		},
		{
			name:   "default route destination alone is not a conflict",
			subnet: "10.89.0.0/16",
			st:     State{Routes: []Route{{Table: MainTable, Link: "eth0", Dst: pfx("0.0.0.0/0")}}},
		},
		{
			name:   "unmasked subnet",
			subnet: "10.88.3.7/16",
			st:     State{Addresses: []Address{{Link: "eth0", Prefix: pfx("10.88.200.2/24")}}},
			want:   []string{"interface address 10.88.200.2/24 dev eth0"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, c := range Conflicts(pfx(tt.subnet), tt.st, nil) {
				got = append(got, c.String())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Conflicts = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConflictsOwnBridge(t *testing.T) {
	st := parentCell()
	// A previous dev-init run left its space bridge up with the gateway of
	// the space's subnet; the kernel adds the connected and local routes.
	st.Links = append(st.Links, Link{Index: 3, Name: "k-1234abcd", Kind: "bridge", Up: true})
	st.Addresses = append(st.Addresses, Address{Link: "k-1234abcd", Prefix: pfx("10.89.0.1/24")})
	st.Routes = append(st.Routes,
		Route{Table: MainTable, Link: "k-1234abcd", Dst: pfx("10.89.0.0/24")},
		Route{Table: unix.RT_TABLE_LOCAL, Link: "k-1234abcd", Dst: pfx("10.89.0.1/32")},
		Route{Table: unix.RT_TABLE_LOCAL, Link: "k-1234abcd", Dst: pfx("10.89.0.255/32")},
	)
	own := OwnBridges(map[string]netip.Prefix{"k-1234abcd": pfx("10.89.0.0/24")})

	if got := Conflicts(pfx("10.89.0.0/16"), st, own); len(got) != 0 {
		t.Fatalf("own bridge reported as conflicts: %v", got)
	}
	if got := Conflicts(pfx("10.89.0.0/16"), st, nil); len(got) == 0 {
		t.Fatal("without the own-bridge filter the bridge must conflict")
	}

	// Only the bridge's own subnet is exempt: a foreign address on it, or
	// the same subnet on a different link, still conflicts.
	st.Addresses = append(st.Addresses,
		Address{Link: "k-1234abcd", Prefix: pfx("10.89.7.1/24")},
		Address{Link: "k-other", Prefix: pfx("10.89.0.1/24")},
	)
	var got []string
	for _, c := range Conflicts(pfx("10.89.0.0/16"), st, own) {
		got = append(got, c.String())
	}
	want := []string{
		"interface address 10.89.0.1/24 dev k-other",
		"interface address 10.89.7.1/24 dev k-1234abcd",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Conflicts = %q, want %q", got, want)
	}
}

func TestUplinkDamage(t *testing.T) {
	base := UplinkOf(parentCell())
	want := Uplink{
		DefaultRoutes: []DefaultRoute{{Link: "eth0", Gateway: addr("10.88.0.1")}},
		Links:         []UplinkLink{{Name: "eth0", Up: true, Addresses: []netip.Prefix{pfx("10.88.0.15/24")}}},
	}
	if !reflect.DeepEqual(base, want) {
		t.Fatalf("UplinkOf = %+v, want %+v", base, want)
	}

	if damage := base.Damage(parentCell()); len(damage) != 0 {
		t.Fatalf("unchanged state reported damage: %v", damage)
	}

	grown := parentCell()
	grown.Addresses = append(grown.Addresses, Address{Link: "eth0", Prefix: pfx("10.88.0.99/24")})
	grown.Links = append(grown.Links, Link{Name: "k-1234abcd", Kind: "bridge"})
	if damage := base.Damage(grown); len(damage) != 0 {
		t.Fatalf("additions reported as damage: %v", damage)
	}

	cases := []struct {
		name   string
		mutate func(*State)
		want   []string
	}{
		{
			name:   "link deleted",
			mutate: func(s *State) { s.Links, s.Addresses, s.Routes = s.Links[:1], s.Addresses[:1], nil },
			want:   []string{`default route "default via 10.88.0.1 dev eth0" is gone`, "uplink link eth0 is gone"},
		},
		{
			name:   "link down",
			mutate: func(s *State) { s.Links[1].Up = false },
			want:   []string{"uplink link eth0 is down"},
		},
		{
			name:   "address removed",
			mutate: func(s *State) { s.Addresses = s.Addresses[:1] },
			want:   []string{"address 10.88.0.15/24 is gone from eth0"},
		},
		{
			name:   "default route replaced",
			mutate: func(s *State) { s.Routes[0].Gateways = []netip.Addr{addr("10.88.0.254")} },
			want:   []string{`default route "default via 10.88.0.1 dev eth0" is gone`},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			now := parentCell()
			tt.mutate(&now)
			if got := base.Damage(now); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Damage = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConvert(t *testing.T) {
	links := []rtnetlink.LinkMessage{
		{Index: 1, Flags: unix.IFF_UP, Attributes: &rtnetlink.LinkAttributes{Name: "lo"}},
		{Index: 2, Flags: unix.IFF_UP, Attributes: &rtnetlink.LinkAttributes{
			Name: "eth0", Info: &rtnetlink.LinkInfo{Kind: "veth"},
		}},
		{Index: 3, Attributes: &rtnetlink.LinkAttributes{Name: "k-0", Info: &rtnetlink.LinkInfo{Kind: "bridge"}}},
	}
	addrs := []rtnetlink.AddressMessage{
		{Family: unix.AF_INET, PrefixLength: 24, Index: 2, Attributes: &rtnetlink.AddressAttributes{
			Address: net.ParseIP("10.88.0.15"), Local: net.ParseIP("10.88.0.15"),
		}},
		{Family: unix.AF_INET6, PrefixLength: 64, Index: 2, Attributes: &rtnetlink.AddressAttributes{
			Address: net.ParseIP("fe80::1"),
		}},
	}
	routes := []rtnetlink.RouteMessage{
		{Family: unix.AF_INET, Table: unix.RT_TABLE_MAIN, Attributes: rtnetlink.RouteAttributes{
			Gateway: net.ParseIP("10.88.0.1"), OutIface: 2, Table: unix.RT_TABLE_MAIN,
		}},
		{Family: unix.AF_INET, DstLength: 24, Table: unix.RT_TABLE_MAIN, Attributes: rtnetlink.RouteAttributes{
			Dst: net.ParseIP("10.88.0.0"), OutIface: 2,
		}},
		{Family: unix.AF_INET, Attributes: rtnetlink.RouteAttributes{
			Table:     1000,
			Multipath: []rtnetlink.NextHop{{Gateway: net.ParseIP("192.168.0.1")}},
		}},
		{Family: unix.AF_INET6, DstLength: 64, Attributes: rtnetlink.RouteAttributes{Dst: net.ParseIP("fe80::")}},
	}
	got := convert(links, addrs, routes)
	want := State{
		Links: []Link{
			{Index: 1, Name: "lo", Up: true},
			{Index: 2, Name: "eth0", Kind: "veth", Up: true},
			{Index: 3, Name: "k-0", Kind: "bridge"},
		},
		Addresses: []Address{{Link: "eth0", Prefix: pfx("10.88.0.15/24")}},
		Routes: []Route{
			{Table: MainTable, Link: "eth0", Dst: pfx("0.0.0.0/0"), Gateways: []netip.Addr{addr("10.88.0.1")}},
			{Table: MainTable, Link: "eth0", Dst: pfx("10.88.0.0/24")},
			{Table: 1000, Dst: pfx("0.0.0.0/0"), Gateways: []netip.Addr{addr("192.168.0.1")}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("convert =\n%+v\nwant\n%+v", got, want)
	}
	if bridges := got.Bridges(); !reflect.DeepEqual(bridges, map[string]bool{"k-0": true}) {
		t.Fatalf("Bridges = %v", bridges)
	}
}

// TestReadLive reads the test process's own namespace: every namespace has
// a loopback link, up or not.
func TestReadLive(t *testing.T) {
	st, err := Read(t.Context())
	if err != nil {
		t.Skipf("route netlink unavailable: %v", err)
	}
	for _, link := range st.Links {
		if link.Name == "lo" {
			return
		}
	}
	t.Fatalf("no loopback link in %+v", st.Links)
}
