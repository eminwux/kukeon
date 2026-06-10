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

// Package status implements `kuke status`, the consolidated health report
// that absorbs the original gaps doc's three separate proposals — `kuke
// doctor` (host pre-flight is now `kuke doctor cgroups`), `kuke ping`
// (daemon liveness), and `kuke selftest` (parity + state consistency) —
// into one post-init smoke command. Replaces the manual
// `kuke get realms` vs `kuke get realms --no-daemon` diff ritual the
// project AGENTS.md spells out (issue #202).
package status

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cgroupcheck"
	"github.com/eminwux/kukeon/internal/client/local"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Status classifies one check's outcome. OK is the silent pass, WARN is
// informational (the check found something unusual but the system is
// still serving), FAIL is a regression signal — the exit code is non-zero
// when any row carries FAIL, matching the issue #202 contract.
type Status int

const (
	StatusOK Status = iota
	StatusWARN
	StatusFAIL
)

// String returns the short label rendered in the human output and the JSON
// `status` field. Kept short so the human table column doesn't widen.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusWARN:
		return "WARN"
	case StatusFAIL:
		return "FAIL"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

// MarshalJSON renders the human label rather than the integer so the
// `--json` output is stable across enum reordering.
func (s Status) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// Result is one row in the report — every section emits one Result per
// check. Detail is the short one-line summary printed next to the status;
// Remediation is the optional one-line fix hint surfaced when --verbose is
// set or whenever the status is WARN/FAIL.
type Result struct {
	Section     string `json:"section"`
	Name        string `json:"name"`
	Status      Status `json:"status"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
	// Storage carries the structured per-realm counts the storage
	// section surfaces — populated only on storage rows. CI tooling
	// parses these to alert on accumulation before the data volume
	// fills (issue #1039); the human Detail string carries the same
	// figures pre-formatted.
	Storage *StorageStats `json:"storage,omitempty"`
}

// StorageStats mirrors ctr.StorageStats on the wire — the per-realm
// snapshot/lease/blob figures `kuke status` exposes for parsing. The
// status package owns the JSON tags so a future internal/ctr struct
// rename doesn't ripple into the public CLI contract.
type StorageStats struct {
	Snapshots  int   `json:"snapshots"`
	Leases     int   `json:"leases"`
	Blobs      int   `json:"blobs"`
	BlobsBytes int64 `json:"blobsBytes"`
}

// Report is the full top-level payload — what --json marshals, and what the
// human renderer iterates over to print the section table.
type Report struct {
	OK     bool     `json:"ok"`
	Checks []Result `json:"checks"`
}

// runCtx threads the configuration and pre-constructed clients through the
// individual check functions. Constructed once per `kuke status` invocation
// so the daemon and in-process branches are dialed at most once each, even
// when the parity walk fans out across resource kinds.
type runCtx struct {
	daemonHost       string
	runPath          string
	containerdSocket string
	cgroupRoot       string
	cniBinDir        string

	logger *slog.Logger

	// daemonClient is the JSON-RPC client dialing kukeond. nil when the
	// dial failed at runStatus startup; the daemon section's checks
	// degrade to FAIL with the dial error in that case.
	daemonClient kukeonv1.Client
	// localClient is the in-process kukeonv1.Client backed by a fresh
	// controller.Exec. Always constructible (it is just struct
	// initialization) — first-call errors surface inside the parity walk.
	localClient kukeonv1.Client
	// ctrClient lazily wraps the containerd socket. Connect()-ed on the
	// host section's containerd check and reused by the state section's
	// residual-namespace probe. Narrowed to the three-method subset
	// status actually calls so tests can mock it without dragging the
	// full ctr.Client surface in.
	ctrClient ctrConn
}

// ctrConn is the minimal ctr.Client subset status calls — the host's
// containerd reachability check (Connect), the state section's
// residual-namespace probe (ListNamespaces), and the storage section's
// per-namespace footprint probe (NamespaceStorage). Close releases the
// dial. internal/ctr's concrete *client satisfies this trivially; the
// test mock implements only these four methods.
type ctrConn interface {
	Connect() error
	Close() error
	ListNamespaces() ([]string, error)
	NamespaceStorage(namespace string) (ctr.StorageStats, error)
}

// MockRunCtxKey is the context key tests use to inject a pre-built runCtx
// (mock clients, temp paths) so the command can be exercised end-to-end
// without dialing a real daemon, opening containerd, or walking
// /sys/fs/cgroup. Mirrors the MockControllerKey pattern the get
// subcommands use.
type MockRunCtxKey struct{}

// NewStatusCmd builds the `kuke status` command.
func NewStatusCmd() *cobra.Command {
	var (
		jsonOut bool
		verbose bool
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report kukeon daemon, host, state, and parity health",
		Long: "Run the consolidated health report that replaces the manual\n" +
			"`kuke get realms` vs `kuke get realms --no-daemon` diff ritual.\n\n" +
			"Sections: daemon (socket dialable, round-trip, version), host\n" +
			"(containerd, cgroup-v2, CNI plugins), state (orphan sockets,\n" +
			"residual containerd namespaces), parity (every `kuke get <kind>`\n" +
			"agrees daemon-side vs in-process). Each line is OK / WARN / FAIL\n" +
			"with a one-line remediation hint when the status is not OK.\n\n" +
			"Exit code 0 when every check is OK or WARN; non-zero when any line\n" +
			"is FAIL. The `--json` form is the machine-readable shape for CI\n" +
			"integration; --verbose surfaces the remediation hint on OK rows\n" +
			"too.",
		SilenceUsage: true,
		// SilenceErrors keeps cobra from re-printing the sentinel after
		// runChecks already wrote the structured report (text or JSON)
		// to stdout. The non-zero exit code from RunE flows through
		// either way; an "Error:" line on stderr would just duplicate
		// information already present in the report's bottom-line
		// "Status: FAIL" / JSON `"ok": false`.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, ownsClose := buildRunCtx(cmd)
			if ownsClose {
				defer closeRunCtx(rc)
			}

			report := runChecks(cmd.Context(), rc)

			if jsonOut {
				if err := renderJSON(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			} else {
				renderText(cmd.OutOrStdout(), report, verbose)
			}

			if !report.OK {
				// Returning a sentinel rather than a freeform error keeps the
				// stderr clean — the structured report on stdout is what the
				// operator reads, and SilenceUsage above stops cobra from
				// re-printing usage. The exit code is non-zero per AC.
				return errFailingChecks
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false,
		"emit a JSON document instead of the human-readable table")
	cmd.Flags().BoolVar(&verbose, "verbose", false,
		"print the remediation hint on every row, not just WARN/FAIL")

	return cmd
}

// errFailingChecks is the sentinel returned by RunE when the report carries
// a FAIL, producing cobra's non-zero exit. SilenceErrors on the command
// suppresses cobra's "Error:" line on stderr — the structured report
// already on stdout (text or JSON) is the operator-visible failure marker.
var errFailingChecks = errors.New("kuke status: one or more checks reported FAIL")

// buildRunCtx assembles the runCtx from viper + flags, or returns the
// mock injected via MockRunCtxKey for tests. The second return value is
// true when buildRunCtx constructed the clients itself (and therefore
// owns Close); mock-injected runCtxs are owned by the test.
//
// No error return: every viper read here is total (defaults fall back
// to the config-package defaults) and the daemon dial is intentionally
// soft — a dial failure sets daemonClient=nil so the daemon section
// surfaces the cause; it does not abort the report.
func buildRunCtx(cmd *cobra.Command) (*runCtx, bool) {
	if mock, ok := cmd.Context().Value(MockRunCtxKey{}).(*runCtx); ok {
		return mock, false
	}

	logger, err := shared.LoggerFromCmd(cmd)
	if err != nil {
		// `kuke status` is rarely invoked with --verbose root logging;
		// fall back to a discard logger so the controller doesn't trip
		// on a nil receiver. The status output itself doesn't need slog.
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}

	host := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey)
	if host == "" {
		host = config.KUKEON_ROOT_HOST.Default
	}
	runPath := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
	}
	containerdSocket := viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey)
	if containerdSocket == "" {
		containerdSocket = config.KUKEON_ROOT_CONTAINERD_SOCKET.Default
	}

	rc := &runCtx{
		daemonHost:       host,
		runPath:          runPath,
		containerdSocket: containerdSocket,
		cgroupRoot:       cgroupcheck.DefaultHostRoot(),
		cniBinDir:        defaultCniBinDir,
		logger:           logger,
	}

	// Dial the daemon up front so the daemon section's round-trip is the
	// dial too, not a second connection. A dial failure is non-fatal — we
	// surface it on the daemon row and let the rest of the report run.
	rc.daemonClient, err = kukeonv1.Dial(cmd.Context(), host)
	if err != nil {
		rc.logger.DebugContext(cmd.Context(), "kuke status: daemon dial failed",
			"host", host, "error", err)
		rc.daemonClient = nil
	}

	// The in-process client is just struct init — the controller.Exec it
	// wraps does no I/O until first use. First-call errors fall out of
	// the parity walk itself.
	rc.localClient = local.New(cmd.Context(), logger, controller.Options{
		RunPath:          runPath,
		ContainerdSocket: containerdSocket,
	})

	// ctrClient is constructed but not connected here — the host
	// containerd check is where Connect() runs and decides reachability.
	rc.ctrClient = ctr.NewClient(cmd.Context(), logger, containerdSocket)

	return rc, true
}

// closeRunCtx releases the resources buildRunCtx allocated. Safe to call
// when fields are nil (mock paths leave them unset).
func closeRunCtx(rc *runCtx) {
	if rc.daemonClient != nil {
		_ = rc.daemonClient.Close()
	}
	if rc.localClient != nil {
		_ = rc.localClient.Close()
	}
	if rc.ctrClient != nil {
		_ = rc.ctrClient.Close()
	}
}

// runChecks invokes every section in order and assembles the Report. Order
// is fixed (daemon → host → state → parity) so the human output reads
// top-down from the most likely fault (daemon down) to the most expensive
// to run (parity walks every resource kind).
func runChecks(ctx context.Context, rc *runCtx) Report {
	var results []Result

	results = append(results, checkDaemon(ctx, rc)...)
	results = append(results, checkHost(rc)...)
	results = append(results, checkState(ctx, rc)...)
	results = append(results, checkStorage(ctx, rc)...)
	results = append(results, checkParity(ctx, rc)...)

	ok := true
	for _, r := range results {
		if r.Status == StatusFAIL {
			ok = false
			break
		}
	}
	return Report{OK: ok, Checks: results}
}

// discardWriter is a no-op io.Writer used by the fallback slog handler in
// buildRunCtx when the root cmd never installed a logger (the verbose
// gate). Keeps the controller's slog calls cheap without pulling
// io.Discard's package-level var indirection into hot paths.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// defaultCniBinDir mirrors internal/cni/types.go:98's defaultCniBinDir,
// duplicated here so the status check doesn't force `internal/cni` to
// export a constant just for this one consumer. The CNI bootstrap path
// is the source of truth; this string must move in lockstep if that
// constant ever moves.
const defaultCniBinDir = "/opt/cni/bin"

// requiredCNIPlugins lists the binaries CNI execution needs under
// defaultCniBinDir — the kukeond image bundles them in-image (Dockerfile
// `cni-plugins` stage) and runs CNI from there; `kuke init` does not lay
// them onto the host. Sourced from internal/cni/{bridge,types}.go —
// `bridge` lays the per-realm bridge network and `loopback` populates the
// per-container lo interface (the minimum a `kuke create container` netns
// needs). Any future required plugin lands here so the host check surfaces
// it when it disappears.
//
// Function rather than a package-level var so the list isn't mutable
// post-init (gochecknoglobals) and tests can call it for parity with
// what the host check stats. Returns a fresh slice per call.
func requiredCNIPlugins() []string {
	return []string{"bridge", "loopback"}
}
