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

// Package netpolicy renders space-level egress policies into iptables rules
// and applies them on the host firewall. The public surface is:
//
//   - Policy: an already-validated policy-for-a-space, derived from a Space's
//     intmodel.EgressPolicy.
//   - BuildRules: pure rule generator — no I/O, no iptables invocations.
//   - Enforcer: interface for applying/removing rules on the host.
//   - IptablesEnforcer: concrete enforcer that shells out to iptables.
//
// Design choice (per issue #45): host entries are resolved to IPs at apply
// time. The resolved IPs are embedded in iptables rules, so hostname-based
// entries do not reflect DNS changes until the space is re-applied. See the
// Space manifest docs for the operator-visible caveat.
package netpolicy

import (
	"fmt"
	"hash/fnv"
	"net"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// Policy is the validated, per-space egress policy ready for rule generation.
// Use FromInternal to build one from the internal Space model.
type Policy struct {
	// Bridge is the Linux bridge device name (as written in the CNI
	// conflist) that the policy guards.
	Bridge string
	// RealmName and SpaceName identify the space for chain naming and log
	// tagging.
	RealmName string
	SpaceName string
	// Default is the fallthrough action when no Allow rule matches.
	Default intmodel.EgressDefault
	// Allow is the list of validated allowlist entries.
	Allow []ResolvedRule
}

// ResolvedRule is a single allowlist entry where Host (if any) has already
// been resolved to IPs. Callers constructing Policy by hand must fill either
// CIDR or IPs (the resolver populates IPs from a Host lookup).
type ResolvedRule struct {
	// OriginalHost is the user-supplied host, kept so rule comments can
	// name the DNS origin. Empty when the rule came from a CIDR entry.
	OriginalHost string
	// CIDR is set when the rule was supplied as a literal CIDR.
	CIDR string
	// IPs is the list of IPs the rule targets. When CIDR is set, IPs is
	// empty and CIDR is used as the iptables -d target.
	IPs []net.IP
	// Ports is the list of TCP destination ports. Empty means "any port".
	Ports []int
}

// FromInternal validates an internal EgressPolicy and returns an intermediate
// Policy with hosts *not yet resolved*. Call Resolve to populate IPs.
//
// Returns (nil, nil) when egress is effectively a no-op: either the policy
// pointer is nil, or Default=allow with no Allow entries. Callers treat a nil
// result as "nothing to enforce".
func FromInternal(realmName, spaceName, bridge string, in *intmodel.EgressPolicy) (*Policy, error) {
	if in == nil {
		return nil, nil
	}
	def, err := normalizeDefault(in.Default)
	if err != nil {
		return nil, err
	}
	if def == intmodel.EgressDefaultAllow && len(in.Allow) == 0 {
		return nil, nil
	}
	rules := make([]ResolvedRule, 0, len(in.Allow))
	for i, r := range in.Allow {
		rule, vErr := validateAllowRule(r)
		if vErr != nil {
			return nil, fmt.Errorf("egress allow rule %d: %w", i, vErr)
		}
		rules = append(rules, rule)
	}
	return &Policy{
		Bridge:    bridge,
		RealmName: realmName,
		SpaceName: spaceName,
		Default:   def,
		Allow:     rules,
	}, nil
}

func normalizeDefault(d intmodel.EgressDefault) (intmodel.EgressDefault, error) {
	switch strings.ToLower(strings.TrimSpace(string(d))) {
	case "", string(intmodel.EgressDefaultAllow):
		return intmodel.EgressDefaultAllow, nil
	case string(intmodel.EgressDefaultDeny):
		return intmodel.EgressDefaultDeny, nil
	default:
		return "", errdefs.ErrEgressInvalidDefault
	}
}

func validateAllowRule(r intmodel.EgressAllowRule) (ResolvedRule, error) {
	host := strings.TrimSpace(r.Host)
	cidr := strings.TrimSpace(r.CIDR)
	switch {
	case host == "" && cidr == "":
		return ResolvedRule{}, errdefs.ErrEgressRuleTargetRequired
	case host != "" && cidr != "":
		return ResolvedRule{}, errdefs.ErrEgressRuleTargetConflict
	}
	if cidr != "" {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return ResolvedRule{}, fmt.Errorf("%w: %q: %w", errdefs.ErrEgressInvalidCIDR, cidr, err)
		}
	}
	if host != "" && !isLikelyDNSName(host) {
		return ResolvedRule{}, fmt.Errorf("%w: %q", errdefs.ErrEgressInvalidHost, host)
	}
	ports := make([]int, 0, len(r.Ports))
	seen := make(map[int]struct{}, len(r.Ports))
	for _, p := range r.Ports {
		if p < 1 || p > 65535 {
			return ResolvedRule{}, fmt.Errorf("%w: %d", errdefs.ErrEgressInvalidPort, p)
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ResolvedRule{
		OriginalHost: host,
		CIDR:         cidr,
		Ports:        ports,
	}, nil
}

// isLikelyDNSName is a loose syntactic check that rejects obvious garbage
// (spaces, empty labels, leading/trailing dots) without trying to reimplement
// the full RFC 1035 grammar. The authoritative check happens at Resolve time.
func isLikelyDNSName(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t/") {
		return false
	}
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" {
			return false
		}
	}
	return true
}

// ChainName returns the deterministic iptables chain name for this policy.
// Format: "KUKE-EGR-<8-hex-fnv>". The FNV-1a hash of "<realm>/<space>" keeps
// the name short enough for any iptables version while avoiding collisions
// across spaces in different realms.
func (p *Policy) ChainName() string {
	return chainName(p.RealmName, p.SpaceName)
}

func chainName(realmName, spaceName string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(realmName + "/" + spaceName))
	return fmt.Sprintf("KUKE-EGR-%08x", h.Sum32())
}

// CommentTag returns the short identifier embedded in every rule's
// --comment argument so operators can grep iptables output by space.
func (p *Policy) CommentTag() string {
	return fmt.Sprintf("kukeon:%s:%s", p.RealmName, p.SpaceName)
}
