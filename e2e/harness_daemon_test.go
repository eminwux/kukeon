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

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// daemonStartupTimeout bounds how long startKukeondDaemon waits for the
// listener socket to appear after `kukeond serve` is exec'd. Cold-start on a
// loaded host is typically sub-second; the budget is generous so a slow CI
// box does not flake the harness before the daemon has bound its listener.
const daemonStartupTimeout = 10 * time.Second

// daemonShutdownTimeout bounds the SIGTERM-and-wait phase of the per-test
// cleanup. If the daemon does not exit within this window the cleanup
// escalates to SIGKILL so a hung daemon never strands `go test` past its
// per-test 60s allowance.
const daemonShutdownTimeout = 5 * time.Second

// startKukeondDaemon brings up a per-test kukeond bound to the given
// run-path, listening on a SUN_PATH-safe socket under a short /tmp prefix.
// It blocks until the socket appears (or daemonStartupTimeout elapses),
// registers t.Cleanup that SIGTERM's the daemon and waits for exit, and
// returns the `unix:///…` address suitable for `--host`.
//
// This is the daemon-mode counterpart to buildKukeRunPathArgs's in-process
// promotion. Workload-command tests call this once per test (paired with
// getRandomRunPath for the on-disk tree), then pass the returned host
// through buildKukeDaemonArgs into every `kuke …` invocation so the client
// dials the per-test daemon rather than the host's production socket.
//
// The kukeond binary is resolved from E2E_BIN_DIR (set by `make e2e`), the
// same way runBinary resolves `kuke`. The test is skipped — not failed —
// when the binary or env is missing so `go test ./e2e` outside `make e2e`
// behaves consistently with the rest of the harness.
//
// Notable kukeond flags passed:
//   - --socket <X>: per-test SUN_PATH-safe listener path
//   - --run-path <Y>: matches the test's runPath so daemon-written metadata
//     lives where the fs.* verification helpers look for it
//   - --reconcile-interval 0: disables the background ticker so a per-test
//     daemon does not log noise (or race the test's create/delete sequence)
//   - --configuration <runPath>/kukeond.yaml: path does not exist, so
//     serverconfig.Load returns a zero doc and the daemon falls through to
//     hardcoded defaults — keeps the harness off /etc/kukeon/kukeond.yaml
//     even when the dev host has one
func startKukeondDaemon(t *testing.T, runPath string) string {
	t.Helper()

	binDir := os.Getenv("E2E_BIN_DIR")
	if binDir == "" {
		binDir = ".."
	}
	bin := filepath.Join(binDir, "kukeond")
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("kukeond binary %s not found, skipping daemon-mode test", bin)
	}

	// Pick a short /tmp prefix for the socket dir — t.TempDir() leans on the
	// test function name and easily blows the 107-byte SUN_PATH budget when
	// kukeond opens its listener.
	sockDir, err := os.MkdirTemp("/tmp", "kd-") //nolint:usetesting // intentional shorter prefix; see comment
	if err != nil {
		t.Fatalf("MkdirTemp(/tmp, kd-): %v", err)
	}
	sockPath := filepath.Join(sockDir, "k.sock")

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin,
		"serve",
		"--socket", sockPath,
		"--run-path", runPath,
		"--reconcile-interval", "0",
		"--configuration", filepath.Join(runPath, "kukeond.yaml"),
	)
	// Capture daemon output for diagnostics on cleanup failure.
	logFile, logErr := os.CreateTemp("", "kukeond-*.log")
	if logErr != nil {
		cancel()
		t.Fatalf("CreateTemp kukeond log: %v", logErr)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if startErr := cmd.Start(); startErr != nil {
		cancel()
		_ = logFile.Close()
		_ = os.RemoveAll(sockDir)
		t.Fatalf("start kukeond serve: %v", startErr)
	}

	// Wait for the listener socket to appear. `kukeond serve` writes the
	// socket synchronously inside Serve before entering Accept, so the file's
	// presence is a reliable readiness signal.
	deadline := time.Now().Add(daemonStartupTimeout)
	for {
		if _, statErr := os.Stat(sockPath); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
			cancel()
			logBytes, _ := os.ReadFile(logFile.Name())
			_ = logFile.Close()
			_ = os.Remove(logFile.Name())
			_ = os.RemoveAll(sockDir)
			t.Fatalf(
				"kukeond did not create socket %s within %s; daemon log:\n%s",
				sockPath, daemonStartupTimeout, string(logBytes),
			)
		}
		// Detect early exit so the test fails fast with the daemon log
		// instead of waiting out the timeout.
		if exitedErr := cmd.Process.Signal(syscall.Signal(0)); exitedErr != nil {
			cancel()
			logBytes, _ := os.ReadFile(logFile.Name())
			_ = logFile.Close()
			_ = os.Remove(logFile.Name())
			_ = os.RemoveAll(sockDir)
			t.Fatalf(
				"kukeond exited before socket %s appeared; daemon log:\n%s",
				sockPath, string(logBytes),
			)
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Cleanup(func() {
		// Graceful shutdown: SIGTERM and wait. Escalate to SIGKILL if the
		// daemon ignores the term within daemonShutdownTimeout.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(daemonShutdownTimeout):
			_ = cmd.Process.Signal(syscall.SIGKILL)
			<-done
		}
		cancel()
		_ = logFile.Close()
		_ = os.Remove(logFile.Name())
		_ = os.RemoveAll(sockDir)
	})

	return fmt.Sprintf("unix://%s", sockPath)
}

// buildKukeDaemonArgs returns the `--host <addr>` prefix every daemon-mode
// e2e invocation must carry. Pair with startKukeondDaemon to route a
// workload command to the per-test daemon — required for apply, create *,
// run, attach, delete *, kill * after #566 made the workload verbs
// daemon-only at the code-path level (the `--run-path` promotion no longer
// reaches an in-process branch for them).
//
// The in-process counterpart (buildKukeRunPathArgs) survives for the
// daemon-parity check (`get realms --no-daemon`) and host-mutating verbs
// (purge, init, uninstall) — those must not depend on a per-test daemon.
func buildKukeDaemonArgs(host string) []string {
	return []string{"--host", host}
}
