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
	"fmt"
	"strconv"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// MasterChainName is the top-level FORWARD dispatch chain that holds one
// "jump to per-space chain" entry per space with an active policy. It is
// shared across all spaces; the per-space chain names differ.
const MasterChainName = "KUKEON-EGRESS"

// Rule represents a single iptables command in a form the applier can
// execute or a test can compare. It is chain-qualified ("-A <chain>" or
// "-I <chain> 1") plus arguments.
type Rule struct {
	// Op is "-A" (append), "-I 1" (insert at head), etc.
	Op string
	// Chain is the target chain for Op.
	Chain string
	// Args is the remaining iptables argument list.
	Args []string
}

// BuildRules returns the ordered iptables rules that implement Policy on the
// per-space chain. The caller is responsible for ensuring the chain exists
// (flushed) and for wiring the dispatch jump from the master chain.
//
// Rule ordering matters:
//  1. Return on RELATED/ESTABLISHED so reply traffic is never dropped.
//  2. Each allowlist entry emits one or more RETURN rules.
//  3. Final action: DROP (when Default=deny) or RETURN (when Default=allow).
//
// The generator is pure: no I/O, no iptables invocations.
func BuildRules(p *Policy) []Rule {
	chain := p.ChainName()
	tag := p.CommentTag()
	rules := make([]Rule, 0, 2+len(p.Allow)+1)

	rules = append(rules, Rule{
		Op:    "-A",
		Chain: chain,
		Args: []string{
			"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED",
			"-m", "comment", "--comment", tag + ":established",
			"-j", "RETURN",
		},
	})

	for i, r := range p.Allow {
		rules = append(rules, buildAllowRules(chain, tag, i, r)...)
	}

	terminal := Rule{
		Op:    "-A",
		Chain: chain,
		Args: []string{
			"-m", "comment", "--comment", tag + ":default",
		},
	}
	if p.Default == intmodel.EgressDefaultDeny {
		terminal.Args = append(terminal.Args, "-j", "DROP")
	} else {
		terminal.Args = append(terminal.Args, "-j", "RETURN")
	}
	rules = append(rules, terminal)

	return rules
}

func buildAllowRules(chain, tag string, idx int, r ResolvedRule) []Rule {
	targets := make([]string, 0)
	if r.CIDR != "" {
		targets = append(targets, r.CIDR)
	} else {
		for _, ip := range r.IPs {
			targets = append(targets, ip.String()+"/32")
		}
	}

	ruleCap := len(targets)
	if len(r.Ports) > 0 {
		ruleCap *= len(r.Ports)
	}
	out := make([]Rule, 0, ruleCap)
	for _, dst := range targets {
		if len(r.Ports) == 0 {
			out = append(out, Rule{
				Op:    "-A",
				Chain: chain,
				Args: []string{
					"-d", dst,
					"-m", "comment", "--comment", tag + ":" + allowComment(idx, r),
					"-j", "RETURN",
				},
			})
			continue
		}
		for _, port := range r.Ports {
			out = append(out, Rule{
				Op:    "-A",
				Chain: chain,
				Args: []string{
					"-d", dst,
					"-p", "tcp",
					"--dport", strconv.Itoa(port),
					"-m", "comment", "--comment", tag + ":" + allowComment(idx, r),
					"-j", "RETURN",
				},
			})
		}
	}
	return out
}

func allowComment(idx int, r ResolvedRule) string {
	if r.OriginalHost != "" {
		return fmt.Sprintf("allow[%d]:host=%s", idx, r.OriginalHost)
	}
	return fmt.Sprintf("allow[%d]:cidr=%s", idx, r.CIDR)
}

// DispatchRule returns the single "-A <master> -i <bridge> -j <per-space>"
// rule that funnels bridge-sourced traffic into the per-space chain. It is
// emitted on Apply and removed on Remove.
func DispatchRule(p *Policy) Rule {
	return Rule{
		Op:    "-A",
		Chain: MasterChainName,
		Args: []string{
			"-i", p.Bridge,
			"-m", "comment", "--comment", p.CommentTag() + ":dispatch",
			"-j", p.ChainName(),
		},
	}
}

