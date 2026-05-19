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
// FORWARD admission chain that admits traffic to/from kukeon bridges. It is
// distinct from internal/netpolicy, which installs per-space egress filters:
// admission lives at host scope and is set up once at `kuke init`, while
// egress is per-space and applied by the runner when a Space carries an
// EgressPolicy.
package firewall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/netpolicy"
)

// ForwardChainName is the kukeon-owned FORWARD admission chain.
//
// Relative ordering with KUKEON-EGRESS (netpolicy.MasterChainName) is
// enforced by ensureForwardJump: when KUKEON-EGRESS already lives in
// FORWARD, the jump to KUKEON-FORWARD is placed immediately after it so
// per-space egress DROP rules win over the blanket admission. When
// KUKEON-EGRESS is absent the jump goes to position 1; a later
// netpolicy.Apply() will insert KUKEON-EGRESS at 1, pushing this jump
// to 2 (still correct).
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
// to the prefix: ":established", ":egress", ":ingress".
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
//  2. -i k-+ ACCEPT — admit egress originating on a kukeon bridge.
//  3. -o k-+ ACCEPT — admit ingress destined to a kukeon bridge.
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
			"-i", BridgeIfaceMatch,
			"-m", "comment", "--comment", commentTagPrefix + ":egress",
			"-j", "ACCEPT",
		},
		{
			"-A", ForwardChainName,
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
// the tagged variants side by side.
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

// ensureForwardJump idempotently inserts the FORWARD → KUKEON-FORWARD
// jump. When KUKEON-EGRESS (netpolicy.MasterChainName) already lives in
// FORWARD, the jump is placed at that position + 1 so per-space egress
// DROP rules win over the blanket admission KUKEON-FORWARD provides.
// Otherwise the jump goes to position 1; a later netpolicy.Apply() will
// push it to position 2 by inserting KUKEON-EGRESS at 1.
func (i *Installer) ensureForwardJump(ctx context.Context) error {
	if _, err := i.runner.Run(ctx, "-C", "FORWARD", "-j", ForwardChainName); err == nil {
		return nil
	}
	insertAt := "1"
	if pos := i.findEgressPosition(ctx); pos > 0 {
		insertAt = strconv.Itoa(pos + 1)
	}
	_, err := i.runner.Run(ctx, "-I", "FORWARD", insertAt, "-j", ForwardChainName)
	return err
}

// findEgressPosition returns the 1-based position of the
// `-j KUKEON-EGRESS` rule in FORWARD, or 0 when absent / unparseable.
// Failures are non-fatal — callers fall back to position 1, which is
// safe when KUKEON-EGRESS is not yet installed.
//
// The token-aware match prevents a sibling chain named like
// "KUKEON-EGRESS-FOO" from being mistaken for "KUKEON-EGRESS".
func (i *Installer) findEgressPosition(ctx context.Context) int {
	out, err := i.runner.Run(ctx, "-S", "FORWARD")
	if err != nil {
		return 0
	}
	pos := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-A FORWARD") {
			continue
		}
		pos++
		if lineJumpsTo(line, netpolicy.MasterChainName) {
			return pos
		}
	}
	return 0
}

// lineJumpsTo reports whether an `iptables -S` line jumps to exactly the
// named chain. Token-aware so a chain like "KUKEON-EGRESS-FOO" doesn't
// match "KUKEON-EGRESS" the way a plain substring check would. A parse
// failure is treated as a non-match — the caller falls back to its safe
// default rather than aborting.
func lineJumpsTo(line, target string) bool {
	tokens, err := parseRuleLine(line)
	if err != nil {
		return false
	}
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == "-j" && tokens[i+1] == target {
			return true
		}
	}
	return false
}

// parseRuleLine splits an "-A <chain> ..." line from `iptables -S` into
// its argument vector. Double-quoted substrings (iptables emits comment
// values in quotes when they contain spaces) are preserved as a single
// arg with the quotes stripped.
func parseRuleLine(line string) ([]string, error) {
	var (
		out     []string
		cur     strings.Builder
		inQuote bool
	)
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		out = append(out, cur.String())
		cur.Reset()
	}
	for i := range len(line) {
		c := line[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ' ' && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	if inQuote {
		return nil, errors.New("unterminated quote in iptables -S line")
	}
	return out, nil
}
