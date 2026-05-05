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
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// ForwardChainName is the kukeon-owned FORWARD admission chain.
//
// Relative ordering with KUKEON-EGRESS (netpolicy.MasterChainName) is
// implicit: kuke init inserts KUKEON-FORWARD at FORWARD position 1, then any
// later egress-policy install inserts KUKEON-EGRESS at position 1, pushing
// KUKEON-FORWARD down. The resulting chain — KUKEON-EGRESS first (may DROP),
// then KUKEON-FORWARD (admits surviving kukeon-bridge traffic) — is the
// intended order.
const ForwardChainName = "KUKEON-FORWARD"

// BridgeIfaceMatch is the iptables -i / -o interface match that scopes the
// admission rules to kukeon-managed bridges. The interface name is derived
// in internal/cni.SafeBridgeName as "k-<8 hex>" so the "+" wildcard matches
// the hex suffix and admits any kukeon bridge regardless of which space hash
// it represents.
const BridgeIfaceMatch = "k-+"

// AdmissionRules returns the ordered iptables rules that populate
// ForwardChainName. The generator is pure — no I/O, no iptables calls — so
// tests can verify rule order without fakes.
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
			"-j", "ACCEPT",
		},
		{"-A", ForwardChainName, "-i", BridgeIfaceMatch, "-j", "ACCEPT"},
		{"-A", ForwardChainName, "-o", BridgeIfaceMatch, "-j", "ACCEPT"},
	}
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
func (i *Installer) Install(ctx context.Context) error {
	if err := i.ensureChain(ctx, ForwardChainName); err != nil {
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

func (i *Installer) ensureForwardJump(ctx context.Context) error {
	if _, err := i.runner.Run(ctx, "-C", "FORWARD", "-j", ForwardChainName); err == nil {
		return nil
	}
	_, err := i.runner.Run(ctx, "-I", "FORWARD", "1", "-j", ForwardChainName)
	return err
}
