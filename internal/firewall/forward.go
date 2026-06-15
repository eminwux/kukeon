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

// Package firewall manages host-level iptables state owned by kukeon — the
// FORWARD admission chain that admits *ingress* (and stateful return traffic)
// to/from kukeon bridges. It is distinct from internal/netpolicy, which
// installs the per-space chains that now own *egress* admission as well as
// egress filtering: this host-scope chain is set up once at `kuke init` (and
// re-asserted by the daemon), while each space's chain is applied per-space by
// the runner. Since #1076 egress is no longer admitted by a host-global
// blanket here — that fails open — so egress admission lives entirely in the
// per-space chains, which fail closed when missing.
package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// ForwardChainName is the kukeon-owned FORWARD admission chain. It now carries
// only ingress admission (`! -i k-+ -o k-+ ACCEPT`, scoped to non-bridge
// sources) plus the stateful return-traffic ACCEPT; egress admission moved
// per-space in #1076.
//
// There is no longer any ordering contract with KUKEON-EGRESS
// (netpolicy.MasterChainName). Egress is decided entirely within each space's
// own KUKEON-EGRESS chain, which terminates every packet itself (ACCEPT/DROP)
// rather than RETURNing to a blanket admission here — so this chain's position
// relative to KUKEON-EGRESS is immaterial to correctness. ensureForwardJump
// only guarantees the jump *exists* (re-inserting it after a reboot flush or
// Docker churn); it no longer reorders it.
//
// The one rule here that *could* shadow an egress decision is the ingress
// `-o k-+ ACCEPT`: an inter-bridge packet (`-i k-A -o k-B`) is both ingress to
// B and egress from A, so a bare `-o k-+` would ACCEPT a deny-space A's egress
// to B before A's chain runs — fail-open whenever this chain sits ahead of
// KUKEON-EGRESS. The ingress rule is therefore scoped `! -i k-+`: it admits
// only traffic that did *not* originate on a kukeon bridge (true external
// ingress), so a space's egress (always bridge-sourced) can never match it and
// always falls through to KUKEON-EGRESS. That is what keeps the position
// genuinely immaterial — including for the inter-bridge case.
const ForwardChainName = "KUKEON-FORWARD"

// BridgeIfaceMatch is the iptables -i / -o interface match that scopes the
// admission rules to kukeon-managed bridges. The interface name is derived
// in internal/cni.SafeBridgeName as "k-<8 hex>" so the "+" wildcard matches
// the hex suffix and admits any kukeon bridge regardless of which space hash
// it represents.
const BridgeIfaceMatch = "k-+"

// commentTagPrefix is the --comment prefix on every rule the installer
// owns so `iptables -S` is self-documenting and any future cleanup-by-grep
// cannot collide with user-added rules in the same chain. Roles appended
// to the prefix: ":established", ":ingress". The retired ":egress" role
// (host-global egress blanket, removed in #1076) is pruned from upgraded
// hosts by pruneObsoleteEgressAdmission.
const commentTagPrefix = "kukeon-forward"

// AdmissionRules returns the ordered iptables rules that populate
// ForwardChainName. The generator is pure — no I/O, no iptables calls — so
// tests can verify rule order without fakes.
//
// Each rule carries a -m comment --comment "kukeon-forward:<role>" tag so
// `iptables -S` is self-documenting and the Install migration path can
// distinguish kukeon-installed rules from any user rules that happen to
// share the same chain name.
//
// Rule order:
//  1. RELATED,ESTABLISHED ACCEPT — return-traffic for already-admitted flows
//     so reply packets cannot be dropped by FORWARD's default policy.
//  2. ! -i k-+ -o k-+ ACCEPT — admit *external* ingress destined to a kukeon
//     bridge. The `! -i k-+` qualifier scopes this to traffic that did not
//     originate on a kukeon bridge, so it admits the internet/host→bridge case
//     without ever matching a space's egress.
//
// Why the `! -i k-+` scope matters: an inter-bridge packet (`-i k-A -o k-B`)
// is simultaneously ingress to B and egress from A. A bare `-o k-+ ACCEPT`
// would admit it here before A's KUKEON-EGRESS chain ever runs — fail-open for
// cross-space egress whenever this chain sits ahead of KUKEON-EGRESS in
// FORWARD, which nothing orders post-#1076. Excluding bridge-sourced traffic
// (`! -i k-+`) sends every space's egress — internet-bound and inter-bridge
// alike — through KUKEON-EGRESS, so the deny-space decision is never shadowed.
//
// Egress admission (`-i k-+ ACCEPT`) is deliberately absent since #1076: a
// host-global egress blanket fails *open* — a Default=deny space whose
// per-space KUKEON-EGRESS chain went missing (e.g. post-reboot, pre-reconcile)
// would have its traffic ACCEPTed here. Egress is now admitted per-space inside
// each space's own KUKEON-EGRESS chain (see internal/netpolicy.BuildRules), so
// a missing chain fails *closed*. pruneObsoleteEgressAdmission strips a
// leftover `:egress` rule from upgraded hosts.
func AdmissionRules() [][]string {
	return [][]string{
		{
			"-A", ForwardChainName,
			"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED",
			"-m", "comment", "--comment", commentTagPrefix + ":established",
			"-j", "ACCEPT",
		},
		{
			"-A", ForwardChainName,
			"!", "-i", BridgeIfaceMatch,
			"-o", BridgeIfaceMatch,
			"-m", "comment", "--comment", commentTagPrefix + ":ingress",
			"-j", "ACCEPT",
		},
	}
}

// IsIptablesAvailable reports whether the iptables binary can be located
// on PATH. Callers should invoke this before Install on hosts that may
// not carry iptables (minimal containers, nftables-only distros without
// the iptables-nft compat shim): without the binary in place every
// runner call would fail and abort bring-up. The intended caller-side
// pattern is log WARN and continue, treating absence as "the host owner
// has opted out of kukeon-managed FORWARD admission".
func IsIptablesAvailable() bool {
	_, err := exec.LookPath("iptables")
	return err == nil
}

// CommandRunner executes an iptables invocation and returns its combined
// stdout+stderr. Tests inject a fake to capture invocations and return
// canned output for read-only calls like "-C" or "-L". Mirrors
// netpolicy.CommandRunner.
type CommandRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type execRunner struct {
	binary string
}

func (e *execRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	// #nosec G204 -- args are internally constructed from this package's
	// constants (chain names, fixed flags); never user-supplied.
	cmd := exec.CommandContext(ctx, e.binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("iptables %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Installer applies and removes the KUKEON-FORWARD admission chain.
type Installer struct {
	runner CommandRunner
	logger *slog.Logger
}

// NewInstaller returns an Installer that shells out to the iptables binary
// on PATH. Logger is required.
func NewInstaller(logger *slog.Logger) *Installer {
	return &Installer{runner: &execRunner{binary: "iptables"}, logger: logger}
}

// NewInstallerWithRunner is the test-hook constructor.
func NewInstallerWithRunner(logger *slog.Logger, runner CommandRunner) *Installer {
	return &Installer{runner: runner, logger: logger}
}

// Install ensures KUKEON-FORWARD exists, contains the admission rules in the
// expected order, and is jumped to from FORWARD. Idempotent — re-running on
// a healthy host produces no rule churn (every install step does -C before
// -I/-A, mirroring the netpolicy pattern).
//
// Upgrade-path migration: when the chain already exists with untagged rules
// from an older kukeon version, Install flushes it once before re-installing
// the tagged rules so the chain does not end up carrying both the bare and
// the tagged variants side by side. A host upgraded from a pre-#1076 layout
// also carries the retired `:egress` blanket rule; pruneObsoleteEgressAdmission
// strips it so the fail-open hole #1076 closes does not survive the upgrade.
// A host upgraded from an intermediate #1076 layout carries the *unscoped*
// ingress rule (`-o k-+ ...:ingress`, no `! -i k-+`); pruneUnscopedIngressAdmission
// strips that one so the inter-bridge fail-open it allowed does not survive
// either — leaving only the scoped `! -i k-+ -o k-+` ingress rule.
func (i *Installer) Install(ctx context.Context) error {
	if err := i.ensureChain(ctx, ForwardChainName); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrForwardAdmissionApply, err)
	}
	if err := i.migrateUntaggedRules(ctx); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrForwardAdmissionApply, err)
	}
	for _, args := range AdmissionRules() {
		if err := i.ensureRule(ctx, args); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrForwardAdmissionApply, err)
		}
	}
	i.pruneObsoleteEgressAdmission(ctx)
	i.pruneUnscopedIngressAdmission(ctx)
	if err := i.ensureForwardJump(ctx); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrForwardAdmissionApply, err)
	}
	i.logger.InfoContext(ctx, "installed forward admission chain",
		"chain", ForwardChainName,
		"iface_match", BridgeIfaceMatch,
	)
	return nil
}

// Remove deletes the FORWARD jump, flushes, and deletes KUKEON-FORWARD.
// Safe to call when the chain does not exist; missing-chain failures from
// flush/delete are demoted to debug logs so reset --purge-system on a host
// that never installed the chain (or already removed it) does not error.
func (i *Installer) Remove(ctx context.Context) error {
	if _, err := i.runner.Run(ctx, "-C", "FORWARD", "-j", ForwardChainName); err == nil {
		if _, delErr := i.runner.Run(ctx, "-D", "FORWARD", "-j", ForwardChainName); delErr != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrForwardAdmissionRemove, delErr)
		}
	}
	if _, err := i.runner.Run(ctx, "-F", ForwardChainName); err != nil {
		i.logger.DebugContext(ctx, "flush chain (likely absent)",
			"chain", ForwardChainName, "err", err)
	}
	if _, err := i.runner.Run(ctx, "-X", ForwardChainName); err != nil {
		i.logger.DebugContext(ctx, "delete chain (likely absent)",
			"chain", ForwardChainName, "err", err)
	}
	i.logger.InfoContext(ctx, "removed forward admission chain", "chain", ForwardChainName)
	return nil
}

func (i *Installer) ensureChain(ctx context.Context, chain string) error {
	if _, err := i.runner.Run(ctx, "-L", chain, "-n"); err == nil {
		return nil
	}
	_, err := i.runner.Run(ctx, "-N", chain)
	return err
}

// ensureRule rewrites an "-A <chain> ..." rule as "-C <chain> ..." for the
// existence check, then runs the original "-A" if -C reports absence.
func (i *Installer) ensureRule(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "-A" {
		return fmt.Errorf("admission rule must start with -A <chain>; got %v", args)
	}
	check := append([]string{"-C"}, args[1:]...)
	if _, err := i.runner.Run(ctx, check...); err == nil {
		return nil
	}
	_, err := i.runner.Run(ctx, args...)
	return err
}

// migrateUntaggedRules flushes ForwardChainName when it carries any rule
// without the kukeon-forward: comment tag. This handles the one-time
// upgrade from the pre-#315 bare-rule layout: -C cannot match the new
// tagged rules against the old bare ones, so without this flush Install
// would -A the tagged variants alongside the bare ones (functionally
// harmless but cosmetically noisy). On a freshly created chain (no -A
// lines) or a chain already populated with tagged rules, this is a no-op.
// A -S read failure is non-fatal: the subsequent ensureRule loop still
// converges, just possibly with duplicate rules on the upgrade path.
func (i *Installer) migrateUntaggedRules(ctx context.Context) error {
	out, err := i.runner.Run(ctx, "-S", ForwardChainName)
	if err != nil {
		i.logger.DebugContext(ctx, "list chain for migration check (skipping)",
			"chain", ForwardChainName, "err", err,
		)
		return nil
	}
	needsFlush := false
	prefix := "-A " + ForwardChainName
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if !strings.Contains(line, commentTagPrefix+":") {
			needsFlush = true
			break
		}
	}
	if !needsFlush {
		return nil
	}
	i.logger.InfoContext(ctx, "flushing pre-tag rules from forward admission chain",
		"chain", ForwardChainName,
	)
	_, flushErr := i.runner.Run(ctx, "-F", ForwardChainName)
	return flushErr
}

// ensureForwardJump idempotently self-asserts the FORWARD → KUKEON-FORWARD
// jump. It is the per-tick self-assert the daemon's network reconcile pass
// drives via Install (#1074/#1075): a reboot wipes the jump and Docker churn
// can shuffle it, and this re-inserts it on the next tick without ever
// touching a Docker-owned chain.
//
// Since #1076 the jump only needs to *exist* — its position relative to
// KUKEON-EGRESS no longer matters. Egress is decided entirely within each
// space's own KUKEON-EGRESS chain (self-terminating ACCEPT/DROP), and this
// chain now carries only stateful admission and ingress *scoped to non-bridge
// sources* (`! -i k-+`). Neither can shadow a per-space egress decision: the
// stateful rule only matches flows a per-space chain already admitted (a denied
// NEW packet creates no conntrack entry), and the `! -i k-+` ingress rule
// excludes all bridge-sourced traffic, so a space's egress — internet-bound or
// inter-bridge — always reaches KUKEON-EGRESS. So the three-way missing/
// displaced/healthy dance the pre-#1076 ordering contract required collapses to:
//
//   - Present (anywhere in FORWARD): no-op, no churn.
//   - Absent (fresh host or post-reboot flush): insert at position 1.
func (i *Installer) ensureForwardJump(ctx context.Context) error {
	if _, err := i.runner.Run(ctx, "-C", "FORWARD", "-j", ForwardChainName); err == nil {
		return nil
	}
	_, err := i.runner.Run(ctx, "-I", "FORWARD", "1", "-j", ForwardChainName)
	return err
}

// pruneObsoleteEgressAdmission deletes the retired host-global egress blanket
// (`-i k-+ ... kukeon-forward:egress -j ACCEPT`) from ForwardChainName. Before
// #1076 that rule admitted all kukeon-bridge egress; leaving it on an upgraded
// host would re-open the fail-open hole #1076 closes (a Default=deny space
// whose per-space chain is missing would be ACCEPTed here). Egress admission is
// now per-space, so this rule must go.
//
// It reads `-S ForwardChainName` once and issues one canonical -D per matching
// line — never an unbounded delete-until-absent loop, so a runner that reports
// the rule present cannot livelock. Best-effort: a -S read failure or a -D
// failure is logged at debug and tolerated, mirroring migrateUntaggedRules; the
// chain converges on a later tick.
func (i *Installer) pruneObsoleteEgressAdmission(ctx context.Context) {
	out, err := i.runner.Run(ctx, "-S", ForwardChainName)
	if err != nil {
		i.logger.DebugContext(ctx, "list chain for egress-prune check (skipping)",
			"chain", ForwardChainName, "err", err)
		return
	}
	del := []string{
		"-D", ForwardChainName,
		"-i", BridgeIfaceMatch,
		"-m", "comment", "--comment", commentTagPrefix + ":egress",
		"-j", "ACCEPT",
	}
	prefix := "-A " + ForwardChainName
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if !strings.Contains(line, "-i "+BridgeIfaceMatch) ||
			!strings.Contains(line, commentTagPrefix+":egress") {
			continue
		}
		i.logger.InfoContext(ctx, "pruning obsolete host-global egress admission rule",
			"chain", ForwardChainName)
		if _, delErr := i.runner.Run(ctx, del...); delErr != nil {
			i.logger.DebugContext(ctx, "prune obsolete egress admission (delete failed)",
				"chain", ForwardChainName, "err", delErr)
			return
		}
	}
}

// pruneUnscopedIngressAdmission deletes the *unscoped* ingress rule
// (`-o k-+ ... kukeon-forward:ingress -j ACCEPT`, with no `! -i k-+` qualifier)
// from ForwardChainName. That shape shipped in the intermediate #1076 layout
// before the ingress rule was scoped to exclude bridge-sourced traffic; leaving
// it on an upgraded host re-opens the inter-bridge fail-open hole (a deny-space
// A's egress to bridge B matches the bare `-o k-+` ACCEPT before A's
// KUKEON-EGRESS chain runs). The current scoped rule (`! -i k-+ -o k-+ ...`) is
// deliberately left in place — it is matched and skipped below.
//
// Same shape as pruneObsoleteEgressAdmission: one `-S` read, one canonical -D
// per matching line, no delete-until-absent loop, all failures tolerated at
// debug so the chain converges on a later tick.
func (i *Installer) pruneUnscopedIngressAdmission(ctx context.Context) {
	out, err := i.runner.Run(ctx, "-S", ForwardChainName)
	if err != nil {
		i.logger.DebugContext(ctx, "list chain for ingress-prune check (skipping)",
			"chain", ForwardChainName, "err", err)
		return
	}
	del := []string{
		"-D", ForwardChainName,
		"-o", BridgeIfaceMatch,
		"-m", "comment", "--comment", commentTagPrefix + ":ingress",
		"-j", "ACCEPT",
	}
	prefix := "-A " + ForwardChainName
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		// Match the ingress rule, but skip the current scoped form: a line
		// carrying the `! -i k-+` negation is the rule we want to keep.
		if !strings.Contains(line, "-o "+BridgeIfaceMatch) ||
			!strings.Contains(line, commentTagPrefix+":ingress") ||
			strings.Contains(line, "! -i "+BridgeIfaceMatch) {
			continue
		}
		i.logger.InfoContext(ctx, "pruning obsolete unscoped ingress admission rule",
			"chain", ForwardChainName)
		if _, delErr := i.runner.Run(ctx, del...); delErr != nil {
			i.logger.DebugContext(ctx, "prune unscoped ingress admission (delete failed)",
				"chain", ForwardChainName, "err", delErr)
			return
		}
	}
}
