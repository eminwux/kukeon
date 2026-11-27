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

// Package hostnet reads the IPv4 links, addresses and routes of the current
// network namespace over rtnetlink, and checks a pod subnet against them.
// No iproute2 executable is needed, so the checks also work inside
// development cells that do not ship `ip`.
//
// Read only reads. The e2e harness and the `make dev-init` network guard
// (hack/netguard) are its callers.
package hostnet

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/jsimonetti/rtnetlink/v2"
	"golang.org/x/sys/unix"
)

// MainTable is the kernel's main routing table (RT_TABLE_MAIN).
const MainTable = unix.RT_TABLE_MAIN

// readTimeout bounds kernel IO when the caller supplied no earlier deadline.
const readTimeout = 5 * time.Second

// Link is one network interface.
type Link struct {
	Index uint32
	Name  string
	// Kind is the rtnetlink link kind ("bridge", "veth", ...); empty for
	// devices without one, such as loopback or a physical NIC.
	Kind string
	// Up reports the administrative IFF_UP flag.
	Up bool
}

// Address is one IPv4 interface address. Prefix keeps the host bits
// (10.88.0.15/24), so it names both the address and the interface network.
type Address struct {
	Link   string
	Prefix netip.Prefix
}

// Route is one IPv4 route from any table, including the local table.
type Route struct {
	Table uint32
	// Link is the output interface; empty for a multipath route.
	Link string
	// Dst has zero bits for a default route.
	Dst netip.Prefix
	// Gateways lists the route's gateway and every multipath next hop's.
	Gateways []netip.Addr
}

// IsDefault reports whether r is a default route.
func (r Route) IsDefault() bool { return r.Dst.Bits() == 0 }

// State is the IPv4 network state of one network namespace.
type State struct {
	Links     []Link
	Addresses []Address
	Routes    []Route
}

// Bridges returns the names of the bridge links in s.
func (s State) Bridges() map[string]bool {
	names := make(map[string]bool)
	for _, link := range s.Links {
		if link.Kind == "bridge" {
			names[link.Name] = true
		}
	}
	return names
}

// Read returns the IPv4 state of the caller's network namespace.
func Read(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return State{}, fmt.Errorf("open route netlink: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(readTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetReadDeadline(deadline); err != nil {
		return State{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	links, err := conn.Link.List()
	if err != nil {
		return State{}, fmt.Errorf("list links: %w", err)
	}
	addresses, err := conn.Address.List()
	if err != nil {
		return State{}, fmt.Errorf("list addresses: %w", err)
	}
	routes, err := conn.Route.List()
	if err != nil {
		return State{}, fmt.Errorf("list routes: %w", err)
	}
	return convert(links, addresses, routes), nil
}

func convert(
	linkMsgs []rtnetlink.LinkMessage,
	addrMsgs []rtnetlink.AddressMessage,
	routeMsgs []rtnetlink.RouteMessage,
) State {
	var st State
	names := make(map[uint32]string, len(linkMsgs))
	for _, msg := range linkMsgs {
		if msg.Attributes == nil {
			continue
		}
		link := Link{Index: msg.Index, Name: msg.Attributes.Name, Up: msg.Flags&unix.IFF_UP != 0}
		if msg.Attributes.Info != nil {
			link.Kind = msg.Attributes.Info.Kind
		}
		names[msg.Index] = link.Name
		st.Links = append(st.Links, link)
	}
	for _, msg := range addrMsgs {
		if msg.Family != unix.AF_INET || msg.Attributes == nil {
			continue
		}
		ip := msg.Attributes.Local
		if ip == nil {
			ip = msg.Attributes.Address
		}
		addr, ok := addr4(ip)
		if !ok {
			continue
		}
		st.Addresses = append(st.Addresses, Address{
			Link:   names[msg.Index],
			Prefix: netip.PrefixFrom(addr, int(msg.PrefixLength)),
		})
	}
	for _, msg := range routeMsgs {
		if msg.Family != unix.AF_INET {
			continue
		}
		st.Routes = append(st.Routes, convertRoute(msg, names))
	}
	return st
}

func convertRoute(msg rtnetlink.RouteMessage, names map[uint32]string) Route {
	attrs := msg.Attributes
	route := Route{Table: attrs.Table, Link: names[attrs.OutIface]}
	if route.Table == 0 {
		route.Table = uint32(msg.Table)
	}
	dst, ok := addr4(attrs.Dst)
	if !ok {
		dst = netip.IPv4Unspecified()
	}
	route.Dst = netip.PrefixFrom(dst, int(msg.DstLength))
	if gw, gwOK := addr4(attrs.Gateway); gwOK {
		route.Gateways = append(route.Gateways, gw)
	}
	for _, hop := range attrs.Multipath {
		if gw, gwOK := addr4(hop.Gateway); gwOK {
			route.Gateways = append(route.Gateways, gw)
		}
	}
	return route
}

func addr4(ip net.IP) (netip.Addr, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte(v4)), true
}
