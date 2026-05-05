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

// Package hostfw owns the kukeon FORWARD-chain admission rules that
// `kuke init` installs once at bring-up. The rules admit traffic to and
// from kukeon-managed bridges (matched by interface-name prefix `k-+`)
// when the host's FORWARD default policy is DROP — the typical state
// after Docker, firewalld, ufw, or some hardened distro defaults have
// run. Without these rules, cells get an IP and route table but every
// egress packet is silently dropped on the host.
//
// The chain layout is:
//
//	KUKEON-FORWARD:
//	  -A KUKEON-FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
//	  -A KUKEON-FORWARD -i k-+ -j ACCEPT
//	  -A KUKEON-FORWARD -o k-+ -j ACCEPT
//
//	FORWARD:
//	  ... -j KUKEON-FORWARD (inserted near the head, after KUKEON-EGRESS)
//
// Ordering w.r.t. KUKEON-EGRESS matters: the per-space egress policy
// chain may DROP traffic before KUKEON-FORWARD admits it. The installer
// always places the FORWARD jump immediately after KUKEON-EGRESS when
// that chain is present so per-space DROP rules win over the blanket
// admission. When KUKEON-EGRESS is absent (no policies set), the jump
// goes to position 1 — and a later `netpolicy.Apply()` will insert
// KUKEON-EGRESS at position 1, pushing this jump to 2 (still correct).
package hostfw

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

// ChainName is the kukeon-owned FORWARD admission chain name.
const ChainName = "KUKEON-FORWARD"

// BridgePrefix is the iptables interface-wildcard that matches every kukeon
// bridge. kukeon bridges are named `k-<8 hex>` (see internal/cni/config.go's
// SafeBridgeName), so `k-+` covers them all without needing per-bridge rules.
const BridgePrefix = "k-+"

// commentTagPrefix is the --comment prefix on every rule the installer
// owns so `iptables -S` is self-documenting and operators can grep for
// kukeon-installed rules.
const commentTagPrefix = "kukeon-forward"

// Rule is one iptables invocation in a form the installer can execute or a
// test can inspect. Op is the leading iptables verb (e.g. "-A", "-I 1").
type Rule struct {
	Op    string
	Chain string
	Args  []string
}

// CommandRunner executes an iptables invocation and returns its combined
// stdout+stderr. Tests inject a fake to capture invocations and return
// canned output for read-only calls like "-S" or "-L".
type CommandRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// Installer applies and removes the host FORWARD admission rules. The
// interface is narrow so test fixtures and `--no-daemon` paths can
// substitute a no-op implementation.
type Installer interface {
	// Apply installs the chain and all rules idempotently. Re-running on
	// a healthy host produces no rule churn.
	Apply(ctx context.Context) error
	// Remove tears the chain down. Safe to call when the chain is absent.
	Remove(ctx context.Context) error
}

// NoopInstaller satisfies Installer without touching the host firewall.
// It is the safe default for test fixtures.
type NoopInstaller struct{}

func (NoopInstaller) Apply(_ context.Context) error  { return nil }
func (NoopInstaller) Remove(_ context.Context) error { return nil }

type execRunner struct {
	binary string
}

func (e *execRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	// Binary is the hard-coded "iptables" string set in NewIptablesInstaller;
	// args come from BuildChainRules() and ensureForwardJump(), both of which
	// produce only static strings or chain-name constants. No user input
	// reaches the exec call, so the gosec G204 warning is intentional here.
	cmd := exec.CommandContext(ctx, e.binary, args...) //nolint:gosec // see comment above
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("iptables %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// IptablesInstaller invokes the iptables command to install/remove the
// FORWARD admission rules.
type IptablesInstaller struct {
	runner CommandRunner
	logger *slog.Logger
}

// NewIptablesInstaller returns an installer that shells out to iptables
// on PATH. Logger is required and should be the daemon-scoped slog.
func NewIptablesInstaller(logger *slog.Logger) *IptablesInstaller {
	return &IptablesInstaller{
		runner: &execRunner{binary: "iptables"},
		logger: logger,
	}
}

// NewIptablesInstallerWithRunner is the test-hook constructor.
func NewIptablesInstallerWithRunner(logger *slog.Logger, runner CommandRunner) *IptablesInstaller {
	return &IptablesInstaller{runner: runner, logger: logger}
}

// IsIptablesAvailable reports whether the iptables binary can be located
// on PATH. `kuke init` calls this before attempting Apply so the operator
// gets a clear "install iptables-nft" error rather than a cryptic exec
// failure.
func IsIptablesAvailable() bool {
	_, err := exec.LookPath("iptables")
	return err == nil
}

// BuildChainRules returns the ordered rules that fill KUKEON-FORWARD.
// Rule ordering matters: established/related first to short-circuit
// reply traffic, then the two interface-wildcard ACCEPT rules. Pure
// (no I/O), so unit tests can assert the exact rule set the installer
// will emit.
func BuildChainRules() []Rule {
	return []Rule{
		{
			Op:    "-A",
			Chain: ChainName,
			Args: []string{
				"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED",
				"-m", "comment", "--comment", commentTagPrefix + ":established",
				"-j", "ACCEPT",
			},
		},
		{
			Op:    "-A",
			Chain: ChainName,
			Args: []string{
				"-i", BridgePrefix,
				"-m", "comment", "--comment", commentTagPrefix + ":egress",
				"-j", "ACCEPT",
			},
		},
		{
			Op:    "-A",
			Chain: ChainName,
			Args: []string{
				"-o", BridgePrefix,
				"-m", "comment", "--comment", commentTagPrefix + ":ingress",
				"-j", "ACCEPT",
			},
		},
	}
}

// Apply installs the KUKEON-FORWARD chain, populates it, and wires the
// FORWARD-chain jump so kukeon-bridge traffic is admitted past a
// restrictive FORWARD default policy. Idempotent. Callers should run
// IsIptablesAvailable first to surface a clear bring-up blocker on
// nftables-only hosts; Apply itself just wraps the underlying exec
// failure.
func (i *IptablesInstaller) Apply(ctx context.Context) error {
	if err := i.ensureChain(ctx, ChainName); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrHostFwApply, err)
	}

	for _, rule := range BuildChainRules() {
		if err := i.ensureRule(ctx, rule); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrHostFwApply, err)
		}
	}

	if err := i.ensureForwardJump(ctx); err != nil {
		return fmt.Errorf("%w: dispatch: %w", errdefs.ErrHostFwApply, err)
	}

	i.logger.InfoContext(ctx, "installed host firewall admission rules",
		"chain", ChainName,
		"bridge_prefix", BridgePrefix,
	)
	return nil
}

// Remove tears down the FORWARD jump and the chain itself. It tolerates
// a missing chain (DEBUG-logged, not surfaced) so daemon reset stays
// usable on hosts that already deviate from the expected state. Callers
// should run IsIptablesAvailable first and skip the call when iptables
// is absent — Remove on a fake runner has no way to detect that.
func (i *IptablesInstaller) Remove(ctx context.Context) error {
	if err := i.deleteJumpsToChain(ctx); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrHostFwRemove, err)
	}
	if _, err := i.runner.Run(ctx, "-F", ChainName); err != nil {
		i.logger.DebugContext(ctx, "flush chain (likely absent)", "chain", ChainName, "err", err)
	}
	if _, err := i.runner.Run(ctx, "-X", ChainName); err != nil {
		i.logger.DebugContext(ctx, "delete chain (likely absent)", "chain", ChainName, "err", err)
	}
	i.logger.InfoContext(ctx, "removed host firewall admission rules", "chain", ChainName)
	return nil
}

func (i *IptablesInstaller) ensureChain(ctx context.Context, chain string) error {
	if _, err := i.runner.Run(ctx, "-L", chain, "-n"); err == nil {
		return nil
	}
	_, err := i.runner.Run(ctx, "-N", chain)
	return err
}

func (i *IptablesInstaller) ensureRule(ctx context.Context, r Rule) error {
	check := append([]string{"-C", r.Chain}, r.Args...)
	if _, err := i.runner.Run(ctx, check...); err == nil {
		return nil
	}
	_, err := i.runner.Run(ctx, ruleArgs(r)...)
	return err
}

// ensureForwardJump idempotently inserts the FORWARD → KUKEON-FORWARD
// jump. If KUKEON-EGRESS already lives in FORWARD, the jump is placed
// immediately after it so per-space egress DROP rules win over the
// blanket admission KUKEON-FORWARD provides. Otherwise the jump goes to
// position 1.
func (i *IptablesInstaller) ensureForwardJump(ctx context.Context) error {
	if _, err := i.runner.Run(ctx, "-C", "FORWARD", "-j", ChainName); err == nil {
		return nil
	}
	pos := i.findEgressPosition(ctx)
	insertAt := "1"
	if pos > 0 {
		insertAt = strconv.Itoa(pos + 1)
	}
	_, err := i.runner.Run(ctx, "-I", "FORWARD", insertAt, "-j", ChainName)
	return err
}

// findEgressPosition returns the 1-based position of the
// `-j KUKEON-EGRESS` rule in FORWARD, or 0 when absent / unparseable.
// Failures here are non-fatal: callers fall back to position 1.
func (i *IptablesInstaller) findEgressPosition(ctx context.Context) int {
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
		if strings.Contains(line, "-j "+netpolicy.MasterChainName) {
			return pos
		}
	}
	return 0
}

// deleteJumpsToChain enumerates FORWARD and deletes every rule that
// jumps to KUKEON-FORWARD. Tolerates a missing FORWARD listing as
// nothing-to-do — the chain is already absent and we have nothing to
// clean up.
func (i *IptablesInstaller) deleteJumpsToChain(ctx context.Context) error {
	out, err := i.runner.Run(ctx, "-S", "FORWARD")
	if err != nil {
		i.logger.DebugContext(ctx, "list FORWARD chain (likely absent)", "err", err)
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-A FORWARD") || !strings.Contains(line, "-j "+ChainName) {
			continue
		}
		args, parseErr := parseRuleLine(line)
		if parseErr != nil {
			return parseErr
		}
		if len(args) == 0 || args[0] != "-A" {
			continue
		}
		delArgs := append([]string{"-D"}, args[1:]...)
		if _, delErr := i.runner.Run(ctx, delArgs...); delErr != nil {
			return delErr
		}
	}
	return nil
}

// ruleArgs flattens a Rule into an iptables argument vector.
func ruleArgs(r Rule) []string {
	opFields := strings.Fields(r.Op)
	out := make([]string, 0, len(opFields)+1+len(r.Args))
	out = append(out, opFields...)
	out = append(out, r.Chain)
	out = append(out, r.Args...)
	return out
}

// parseRuleLine splits an "-A <chain> ..." line from `iptables -S` into
// its argument vector. Double-quoted substrings (iptables emits comment
// values in quotes when they contain spaces) are preserved as a single
// arg with the quotes stripped. Mirrors the parser in netpolicy.
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
