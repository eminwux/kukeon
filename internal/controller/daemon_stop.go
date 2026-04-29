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

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DaemonStopReport summarizes the outcome of the daemon-stop step run at the
// top of Uninstall.
//
// The fields cover the three observable states the operator cares about:
// (1) was a PID file present at all (PIDFilePresent), (2) was a signal
// actually delivered (Signalled — false when the PID was already dead by the
// time we read the file, or the file held garbage), and (3) did the daemon
// require SIGKILL (ForceKilled) — that is the operator-facing tell that the
// daemon ignored SIGTERM during the grace window.
type DaemonStopReport struct {
	PIDFilePresent bool
	PIDFile        string
	PID            int
	Signalled      bool
	ForceKilled    bool
}

// DaemonStopper is the injection point for the daemon-stop step. The default
// implementation reads the PID file, sends SIGTERM, waits up to gracePeriod
// for the process to exit, then escalates to SIGKILL. Tests stub this to
// avoid touching real processes.
type DaemonStopper func(ctx context.Context, pidFile string, gracePeriod time.Duration) (DaemonStopReport, error)

// DefaultDaemonStopGracePeriod is the SIGTERM-to-SIGKILL grace window. Five
// seconds matches the issue's "wait up to ~5s" — long enough for a clean
// JSON-RPC drain on a quiescent daemon, short enough that a wedged daemon
// does not block uninstall noticeably.
const DefaultDaemonStopGracePeriod = 5 * time.Second

// daemonStopPollInterval is how often waitForProcessExit polls signal-0.
// 50ms balances responsiveness (a clean daemon usually exits within one tick)
// against syscall overhead during the full grace window.
const daemonStopPollInterval = 50 * time.Millisecond

// stopDaemonByPIDFile is the production DaemonStopper. It treats every error
// path that resolves to "no live daemon to stop" as success (missing file,
// unparseable PID, ESRCH on signal) — uninstall is best-effort and a stale
// PID file must not block the rest of the teardown.
func stopDaemonByPIDFile(ctx context.Context, pidFile string, gracePeriod time.Duration) (DaemonStopReport, error) {
	report := DaemonStopReport{PIDFile: pidFile}
	if pidFile == "" {
		return report, nil
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Partial-uninstall path from #193: PID file already gone.
			return report, nil
		}
		return report, fmt.Errorf("read pid file %q: %w", pidFile, err)
	}
	report.PIDFilePresent = true

	pidStr := strings.TrimSpace(string(raw))
	pid, parseErr := strconv.Atoi(pidStr)
	if parseErr != nil {
		// Garbage in the file — treat as "no live daemon" rather than fail
		// the whole uninstall. The error itself is uninteresting here; the
		// PID/Signalled fields on the report communicate "we did nothing".
		return report, nil //nolint:nilerr // intentional silence: garbage PID is best-effort no-op.
	}
	if pid <= 1 {
		// PID 1 would target init; refuse and no-op.
		return report, nil
	}
	report.PID = pid

	proc, findErr := os.FindProcess(pid)
	if findErr != nil {
		// On unix os.FindProcess always succeeds; this branch is defensive.
		return report, nil //nolint:nilerr // intentional silence: defensive no-op on unreachable error.
	}

	if signalErr := proc.Signal(syscall.SIGTERM); signalErr != nil {
		if errors.Is(signalErr, os.ErrProcessDone) || errors.Is(signalErr, syscall.ESRCH) {
			return report, nil
		}
		return report, fmt.Errorf("send SIGTERM to %d: %w", pid, signalErr)
	}
	report.Signalled = true

	if waitForProcessExit(ctx, pid, gracePeriod) {
		return report, nil
	}

	if killErr := proc.Signal(syscall.SIGKILL); killErr != nil {
		if errors.Is(killErr, os.ErrProcessDone) || errors.Is(killErr, syscall.ESRCH) {
			return report, nil
		}
		return report, fmt.Errorf("send SIGKILL to %d: %w", pid, killErr)
	}
	report.ForceKilled = true
	// Brief follow-up wait so callers can downstream the assumption that the
	// daemon is gone before they touch its containers.
	_ = waitForProcessExit(ctx, pid, gracePeriod)
	return report, nil
}

// waitForProcessExit polls signal-0 to detect process exit. signal-0 is the
// canonical "is this PID still alive?" probe on unix — it returns ESRCH iff
// the process has been reaped (or is owned by a different user, but uninstall
// runs as root). Returns true when the process has gone away within timeout.
func waitForProcessExit(ctx context.Context, pid int, timeout time.Duration) bool {
	if pid <= 0 {
		return true
	}
	deadline := time.Now().Add(timeout)
	tick := daemonStopPollInterval
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(tick):
		}
	}
}
