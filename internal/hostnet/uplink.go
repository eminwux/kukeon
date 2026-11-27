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
	"slices"
)

// Uplink is the part of a namespace's network state that carries its
// egress: the main-table default routes and the links they leave through.
type Uplink struct {
	DefaultRoutes []DefaultRoute `json:"defaultRoutes"`
	Links         []UplinkLink   `json:"links"`
}

// DefaultRoute is one main-table default route, or one next hop of a
// multipath default route (Link is then empty).
type DefaultRoute struct {
	Link    string     `json:"link"`
	Gateway netip.Addr `json:"gateway"`
}

func (r DefaultRoute) String() string {
	s := "default"
	if r.Gateway.IsValid() {
		s += " via " + r.Gateway.String()
	}
	if r.Link != "" {
		s += " dev " + r.Link
	}
	return s
}

// UplinkLink is a link a default route leaves through, with its IPv4
// addresses.
type UplinkLink struct {
	Name      string         `json:"name"`
	Up        bool           `json:"up"`
	Addresses []netip.Prefix `json:"addresses"`
}

// UplinkOf extracts the uplink from st.
func UplinkOf(st State) Uplink {
	var u Uplink
	linkNames := make(map[string]bool)
	for _, r := range st.Routes {
		if r.Table != MainTable || !r.IsDefault() {
			continue
		}
		if len(r.Gateways) == 0 {
			u.DefaultRoutes = append(u.DefaultRoutes, DefaultRoute{Link: r.Link})
		}
		for _, gw := range r.Gateways {
			u.DefaultRoutes = append(u.DefaultRoutes, DefaultRoute{Link: r.Link, Gateway: gw})
		}
		if r.Link != "" {
			linkNames[r.Link] = true
		}
	}
	for _, link := range st.Links {
		if !linkNames[link.Name] {
			continue
		}
		ul := UplinkLink{Name: link.Name, Up: link.Up}
		for _, a := range st.Addresses {
			if a.Link == link.Name {
				ul.Addresses = append(ul.Addresses, a.Prefix)
			}
		}
		u.Links = append(u.Links, ul)
	}
	return u
}

// Damage lists what baseline u had that now lacks: a default route, an
// uplink link, its up state, or one of its addresses. Additions are not
// damage. It only compares; it repairs nothing.
func (u Uplink) Damage(now State) []string {
	var damage []string
	routes := UplinkOf(now).DefaultRoutes
	for _, r := range u.DefaultRoutes {
		if !slices.Contains(routes, r) {
			damage = append(damage, fmt.Sprintf("default route %q is gone", r))
		}
	}
	for _, base := range u.Links {
		i := slices.IndexFunc(now.Links, func(l Link) bool { return l.Name == base.Name })
		if i < 0 {
			damage = append(damage, "uplink link "+base.Name+" is gone")
			continue
		}
		if base.Up && !now.Links[i].Up {
			damage = append(damage, "uplink link "+base.Name+" is down")
		}
		for _, addr := range base.Addresses {
			if !slices.Contains(now.Addresses, Address{Link: base.Name, Prefix: addr}) {
				damage = append(damage, fmt.Sprintf("address %s is gone from %s", addr, base.Name))
			}
		}
	}
	return damage
}
