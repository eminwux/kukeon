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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	ctrpkg "github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// sbshDetachSequence is the keystroke pair that the sbsh client filter
// recognises as a request to detach (Ctrl+] Ctrl+], adjacent). Forwarded
// verbatim by the test so the in-process pkg/attach filter triggers.
var sbshDetachSequence = []byte{0x1d, 0x1d}

// attachConnectGrace is how long the test waits between starting `kuke attach`
// and sending the detach sequence. Long enough for the in-process pkg/attach
// loop to enter raw mode and complete the unix-socket handshake on a busy CI
// runner; short enough that a hung scenario surfaces quickly.
const attachConnectGrace = 2 * time.Second

// attachExitTimeout caps how long the test waits for `kuke attach` to exit
// after the detach keystroke is sent. A clean detach lands in well under a
// second; the cap is set so a regression that swallows the keystroke fails
// loudly instead of hanging the suite.
const attachExitTimeout = 15 * time.Second

// stageHostSbsh copies the on-host sbsh binary into the per-test sbsh cache
// at the path the runner will resolve for the workload image. Required
// because the runner only computes the cache path; populating it is an
// out-of-band concern (today the test, in production the operator).
func stageHostSbsh(t *testing.T, runPath string) {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}
	fullRunPath := filepath.Join(cwd, runPath)

	hostSbsh, err := exec.LookPath("sbsh")
	if err != nil {
		t.Skipf("sbsh binary not found on PATH: %v", err)
	}
	requireSbshHasPostMergeFlags(t, hostSbsh)

	dstDir := filepath.Join(fullRunPath, ctrpkg.SbshCacheSubdir, runtime.GOARCH)
	if err = os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("failed to create sbsh cache dir %q: %v", dstDir, err)
	}
	dst := filepath.Join(dstDir, ctrpkg.SbshBinaryName)

	src, err := os.Open(hostSbsh)
	if err != nil {
		t.Fatalf("open host sbsh %q: %v", hostSbsh, err)
	}
	defer func() { _ = src.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("open dst sbsh %q: %v", dst, err)
	}
	defer func() { _ = out.Close() }()

	if _, err = io.Copy(out, src); err != nil {
		t.Fatalf("copy sbsh %q -> %q: %v", hostSbsh, dst, err)
	}
}

// requireSbshHasPostMergeFlags ensures the host sbsh carries the flag set
// the wrapper injected at #69 assumes (post sbsh#153, which added
// `--capture-file` to the `sbsh terminal` subcommand). Without this guard a
// stale sbsh on PATH manifests as `unknown flag: ...` deep inside the work
// container's task, surfaced here only as a `waitForSocket` timeout — hard
// to attribute. Skipping with a named cause saves the dig.
func requireSbshHasPostMergeFlags(t *testing.T, hostSbsh string) {
	t.Helper()
	out, err := exec.Command(hostSbsh, "terminal", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("sbsh terminal --help failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), "--capture-file") {
		t.Skipf("host sbsh %q lacks `sbsh terminal --capture-file` (sbsh#153); "+
			"upgrade sbsh on PATH and retry", hostSbsh)
	}
}

// TestKuke_AttachDetach_KeepsTaskRunning is the phase 2a end-to-end scenario
// for `kuke attach`: it boots a cell that contains an Attachable container,
// drives `kuke attach` over a real PTY, sends sbsh's detach keystroke
// (Ctrl+] Ctrl+]), and verifies that `kuke attach` exits cleanly while the
// underlying container task remains RUNNING. Requires the host `sbsh`
// binary on PATH (still used to populate the per-container sbsh cache);
// the test is skipped if it is missing. No on-host `sb` binary is needed
// — `kuke attach` drives the attach loop in-process via sbsh's pkg/attach.
func TestKuke_AttachDetach_KeepsTaskRunning(t *testing.T) {
	t.Parallel()

	// Setup: isolated runPath, unique resource names, in-process controller
	// (`--no-daemon`) so the sbsh cache and per-container socket live under
	// the test's runPath rather than the host daemon's.
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	stageHostSbsh(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	t.Cleanup(func() {
		cleanupCellNoDaemon(t, runPath, realmName, spaceName, stackName, cellName)
		cleanupStackNoDaemon(t, runPath, realmName, spaceName, stackName)
		cleanupSpaceNoDaemon(t, runPath, realmName, spaceName)
		cleanupRealmNoDaemon(t, runPath, realmName)
	})

	// Step 1: provision realm/space/stack as plain create commands so the
	// remaining apply call exercises the cell path and only the cell path.
	runReturningBinary(t, nil, kuke,
		appendNoDaemonRunPath(runPath, "create", "realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		appendNoDaemonRunPath(runPath, "create", "space", spaceName, "--realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		appendNoDaemonRunPath(runPath, "create", "stack", stackName,
			"--realm", realmName, "--space", spaceName)...)

	// Step 2: apply the attachable cell fixture. Auto-starts the workload
	// (apply is the path that drives Cell to Ready), so by the time it
	// returns the container task should be RUNNING. The per-container
	// sbsh socket appears thanks to #77's per-container tty/ directory bind
	// mount, which makes sbsh's listener inode host-visible. This scenario
	// does not pre-create any placeholder socket file (the old workaround
	// was broken because sbsh unlinks-and-recreates the destination,
	// producing a fresh inode invisible to the host).
	applyAttachableCell(t, runPath, realmName, spaceName, stackName, cellName)

	// Step 3: confirm the per-container sbsh socket is in place. The runner
	// creates the metadata dir at provision time and sbsh binds the socket
	// inside the container at task start. If this is missing, pkg/attach
	// would fail in a way that's hard to attribute, so check explicitly.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}
	socketPath := fs.ContainerSocketPath(filepath.Join(cwd, runPath),
		realmName, spaceName, stackName, cellName, "work")
	waitForSocket(t, socketPath, 10*time.Second)

	// Step 4: drive `kuke attach` over a real PTY and detach.
	session := startPTY(t, nil, kuke,
		appendNoDaemonRunPath(runPath,
			"attach",
			"--realm", realmName,
			"--space", spaceName,
			"--stack", stackName,
			"--cell", cellName,
			"--container", "work",
		)...)
	t.Cleanup(session.Close)

	// Wait for the in-process pkg/attach loop to take over the PTY before
	// sending the keystroke. Without this grace, the bytes can race
	// pkg/attach's raw-mode setup and land before its filter is wired up,
	// in which case the detach sequence is forwarded verbatim instead of
	// triggering a clean exit.
	time.Sleep(attachConnectGrace)

	if err = session.Write(sbshDetachSequence); err != nil {
		t.Fatalf("write sbsh detach sequence: %v", err)
	}

	exitCode, output, waitErr := session.Wait(attachExitTimeout)
	if waitErr != nil && exitCode == -1 {
		t.Fatalf("kuke attach did not exit cleanly within %s: %v\noutput:\n%s",
			attachExitTimeout, waitErr, output)
	}
	if exitCode != 0 {
		t.Fatalf("kuke attach exited with code %d (want 0)\noutput:\n%s",
			exitCode, output)
	}

	// Step 5: assert the container task is still RUNNING. The detach should
	// only tear down the in-process pkg/attach loop; the workload (sleep
	// wrapped by sbsh terminal) keeps running inside the container.
	cellID, err := getCellIDNoDaemon(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("get cell ID: %v", err)
	}
	containerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, "work")
	if !verifyContainerTaskIsRunning(t, realmName, containerdID) {
		t.Fatalf("container task %q in namespace %q is not RUNNING after detach",
			containerdID, realmName)
	}
}

// applyAttachableCell substitutes test-side names into the
// attachable-cell.yaml fixture and runs `kuke apply` against it.
func applyAttachableCell(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) {
	t.Helper()

	yamlContent := readTestdataYAML(t, "attachable-cell.yaml")
	for old, replacement := range map[string]string{
		"test-realm": realmName,
		"test-space": spaceName,
		"test-stack": stackName,
		"test-cell":  cellName,
	} {
		yamlContent = strings.ReplaceAll(yamlContent, old, replacement)
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "attachable-cell.yaml")
	if err := os.WriteFile(tmpFile, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write tmp yaml: %v", err)
	}

	args := appendNoDaemonRunPath(runPath, "apply", "-f", tmpFile)
	runReturningBinary(t, nil, kuke, args...)
}

// appendNoDaemonRunPath returns the standard `--no-daemon --run-path X`
// prefix concatenated with the supplied args. Used by the attach scenario so
// every kuke invocation hits the in-process controller backed by the test's
// runPath, isolating from any host daemon. The runPath is resolved to an
// absolute path so OCI bind-mount sources stay valid even when containerd
// chroot-resolves them from its own work directory.
func appendNoDaemonRunPath(runPath string, args ...string) []string {
	abs := absRunPath(runPath)
	out := make([]string, 0, len(args)+3)
	out = append(out, "--no-daemon", "--run-path", abs)
	return append(out, args...)
}

// absRunPath returns runPath rewritten as an absolute path under the test's
// working directory if it is not already absolute. Pure path math; nothing
// on disk is touched.
func absRunPath(runPath string) string {
	if filepath.IsAbs(runPath) {
		return runPath
	}
	cwd, err := os.Getwd()
	if err != nil {
		// Fallback: pass through as-is. Hard-fails the kuke invocation
		// downstream with a clear error rather than masking it.
		return runPath
	}
	return filepath.Join(cwd, runPath)
}

// waitForSocket polls until the per-container sbsh control socket appears
// or the timeout expires. Polling rather than fixed-sleep so a fast machine
// is not penalised and a stuck task fails with a useful message.
func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && (info.Mode()&os.ModeSocket) != 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("sbsh socket %q did not appear within %s", path, timeout)
}

// verifyContainerTaskIsRunning returns true iff `ctr task ls` reports the
// task in RUNNING state. Distinct from verifyContainerTaskExists, which is
// satisfied even by STOPPED tasks; the detach scenario specifically needs
// to assert the workload survived the client disconnect.
func verifyContainerTaskIsRunning(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping task running check")
		return false
	}

	cmd := exec.Command(ctrPath, "--namespace", namespace, "task", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("ctr task ls failed: %v, output: %s", err, out)
		return false
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, containerID) && strings.Contains(line, "RUNNING") {
			return true
		}
	}
	return false
}

// cleanupRealmNoDaemon, cleanupSpaceNoDaemon, cleanupStackNoDaemon, and
// cleanupCellNoDaemon mirror the existing cleanup helpers but route through
// `--no-daemon` so they use the same in-process controller as the test
// itself. Without this the cleanup goes via the host daemon (different
// runPath) and silently no-ops, leaving resources behind.
func cleanupRealmNoDaemon(t *testing.T, runPath, realmName string) {
	t.Helper()
	args := appendNoDaemonRunPath(runPath, "delete", "realm", realmName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

func cleanupSpaceNoDaemon(t *testing.T, runPath, realmName, spaceName string) {
	t.Helper()
	args := appendNoDaemonRunPath(runPath, "delete", "space", spaceName,
		"--realm", realmName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

func cleanupStackNoDaemon(t *testing.T, runPath, realmName, spaceName, stackName string) {
	t.Helper()
	args := appendNoDaemonRunPath(runPath, "delete", "stack", stackName,
		"--realm", realmName, "--space", spaceName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

func cleanupCellNoDaemon(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) {
	t.Helper()
	args := appendNoDaemonRunPath(runPath, "delete", "cell", cellName,
		"--realm", realmName, "--space", spaceName, "--stack", stackName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

// getCellIDNoDaemon mirrors getCellID but goes through the in-process
// controller. The realm namespace argument used elsewhere collapses to the
// realm name when --no-daemon owns the metadata.
func getCellIDNoDaemon(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) (string, error) {
	t.Helper()

	args := appendNoDaemonRunPath(runPath,
		"get", "cell", cellName,
		"--realm", realmName, "--space", spaceName, "--stack", stackName,
		"--output", "json",
	)
	output := runReturningBinary(t, nil, kuke, args...)
	cell, err := parseCellJSON(t, output)
	if err != nil {
		return "", err
	}
	if cell.Spec.ID == "" {
		return "", fmt.Errorf("cell %q has empty Spec.ID", cellName)
	}
	return cell.Spec.ID, nil
}
