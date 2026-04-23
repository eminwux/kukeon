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
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// Enforcer applies and removes per-space egress policies on the host
// firewall. The interface is narrow so tests and `--no-daemon` paths can
// substitute a no-op implementation.
type Enforcer interface {
	// Apply installs the policy idempotently. A nil policy is a no-op.
	Apply(ctx context.Context, p *Policy) error
	// Remove tears down the policy for the given realm+space. It is safe
	// to call when no policy was installed.
	Remove(ctx context.Context, realmName, spaceName string) error
}

// NoopEnforcer satisfies Enforcer without touching the host firewall. It is
// the safe default for test fixtures and for code paths that must never
// mutate iptables (e.g., `--no-daemon` read-only clients).
type NoopEnforcer struct{}

func (NoopEnforcer) Apply(_ context.Context, _ *Policy) error         { return nil }
func (NoopEnforcer) Remove(_ context.Context, _, _ string) error      { return nil }

// CommandRunner executes an iptables invocation and returns its combined
// stdout+stderr. Tests inject a fake to capture invocations and return
// canned output for read-only calls like "-S" or "-L".
type CommandRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type execRunner struct {
	binary string
}

func (e *execRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("iptables %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// IptablesEnforcer invokes the iptables command to realize policies.
type IptablesEnforcer struct {
	runner CommandRunner
	logger *slog.Logger
}

// NewIptablesEnforcer returns an enforcer that shells out to the iptables
// binary on PATH. Logger is required and should be the daemon-scoped slog.
func NewIptablesEnforcer(logger *slog.Logger) *IptablesEnforcer {
	return &IptablesEnforcer{
		runner: &execRunner{binary: "iptables"},
		logger: logger,
	}
}

// NewIptablesEnforcerWithRunner is the test-hook constructor.
func NewIptablesEnforcerWithRunner(logger *slog.Logger, runner CommandRunner) *IptablesEnforcer {
	return &IptablesEnforcer{runner: runner, logger: logger}
}

// Apply creates/flushes the per-space chain, loads the rules, and wires the
// dispatch jump from the master chain. A nil policy is a no-op.
func (e *IptablesEnforcer) Apply(ctx context.Context, p *Policy) error {
	if p == nil {
		return nil
	}
	if err := e.ensureMasterChain(ctx); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrEgressApply, err)
	}
	chain := p.ChainName()

	if err := e.ensureChain(ctx, chain); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrEgressApply, err)
	}
	if _, err := e.runner.Run(ctx, "-F", chain); err != nil {
		return fmt.Errorf("%w: flush %s: %w", errdefs.ErrEgressApply, chain, err)
	}

	for _, rule := range BuildRules(p) {
		if _, err := e.runner.Run(ctx, ruleArgs(rule)...); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrEgressApply, err)
		}
	}

	dispatch := DispatchRule(p)
	if err := e.ensureRule(ctx, dispatch); err != nil {
		return fmt.Errorf("%w: dispatch: %w", errdefs.ErrEgressApply, err)
	}

	e.logger.InfoContext(ctx, "applied egress policy",
		"realm", p.RealmName,
		"space", p.SpaceName,
		"bridge", p.Bridge,
		"chain", chain,
		"default", string(p.Default),
		"allow_count", len(p.Allow),
	)
	return nil
}

// Remove deletes every dispatch rule targeting the per-space chain, then
// flushes and deletes the chain itself. Idempotent.
func (e *IptablesEnforcer) Remove(ctx context.Context, realmName, spaceName string) error {
	chain := chainName(realmName, spaceName)

	e.logDropCounter(ctx, chain, realmName, spaceName)

	if err := e.deleteJumpsToChain(ctx, chain); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrEgressRemove, err)
	}

	if _, err := e.runner.Run(ctx, "-F", chain); err != nil {
		e.logger.DebugContext(ctx, "flush chain (likely absent)", "chain", chain, "err", err)
	}
	if _, err := e.runner.Run(ctx, "-X", chain); err != nil {
		e.logger.DebugContext(ctx, "delete chain (likely absent)", "chain", chain, "err", err)
	}
	return nil
}

func (e *IptablesEnforcer) ensureMasterChain(ctx context.Context) error {
	if err := e.ensureChain(ctx, MasterChainName); err != nil {
		return err
	}
	if _, err := e.runner.Run(ctx, "-C", "FORWARD", "-j", MasterChainName); err == nil {
		return nil
	}
	_, err := e.runner.Run(ctx, "-I", "FORWARD", "1", "-j", MasterChainName)
	return err
}

func (e *IptablesEnforcer) ensureChain(ctx context.Context, chain string) error {
	if _, err := e.runner.Run(ctx, "-L", chain, "-n"); err == nil {
		return nil
	}
	_, err := e.runner.Run(ctx, "-N", chain)
	return err
}

func (e *IptablesEnforcer) ensureRule(ctx context.Context, r Rule) error {
	check := append([]string{"-C", r.Chain}, r.Args...)
	if _, err := e.runner.Run(ctx, check...); err == nil {
		return nil
	}
	_, err := e.runner.Run(ctx, ruleArgs(r)...)
	return err
}

// deleteJumpsToChain enumerates FORWARD/KUKEON-EGRESS rules and deletes
// every entry that jumps to the given per-space chain. It tolerates a
// missing master chain (treats it as nothing to remove).
func (e *IptablesEnforcer) deleteJumpsToChain(ctx context.Context, chain string) error {
	out, err := e.runner.Run(ctx, "-S", MasterChainName)
	if err != nil {
		// Master chain likely absent — nothing to remove.
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "-A "+MasterChainName+" ") {
			continue
		}
		if !strings.Contains(line, " -j "+chain) {
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
		if _, delErr := e.runner.Run(ctx, delArgs...); delErr != nil {
			return delErr
		}
	}
	return nil
}

func (e *IptablesEnforcer) logDropCounter(ctx context.Context, chain, realmName, spaceName string) {
	out, err := e.runner.Run(ctx, "-L", chain, "-n", "-v", "-x")
	if err != nil {
		return
	}
	packets, bytes, ok := parseDropCounter(string(out))
	if !ok {
		return
	}
	e.logger.InfoContext(ctx, "egress policy drop counter",
		"realm", realmName,
		"space", spaceName,
		"chain", chain,
		"drop_packets", packets,
		"drop_bytes", bytes,
	)
}

// parseDropCounter finds the terminal DROP row in `iptables -L <chain> -n -v -x`
// output and returns its (packets, bytes) counters. Returns ok=false on any
// format surprise.
func parseDropCounter(output string) (uint64, uint64, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[2] != "DROP" {
			continue
		}
		packets, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		bytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		return packets, bytes, true
	}
	return 0, 0, false
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

// parseRuleLine splits an "-A <chain> ..." line from `iptables -S` into its
// argument vector. Double-quoted substrings (iptables emits --comment values
// in quotes when they contain spaces) are preserved as a single arg with the
// quotes stripped.
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
	for i := 0; i < len(line); i++ {
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
