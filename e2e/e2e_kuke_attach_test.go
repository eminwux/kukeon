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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
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

// TestKuke_AttachDetach_KeepsTaskRunning is the phase 2a end-to-end scenario
// for `kuke attach`: it boots a cell that contains an Attachable container,
// drives `kuke attach` over a real PTY, sends sbsh's detach keystroke
// (Ctrl+] Ctrl+]), and verifies that `kuke attach` exits cleanly while the
// underlying container task remains RUNNING. No on-host `sbsh` binary is
// needed — the in-container attach server is kuketty (staged from the
// runner's own /bin/kuketty), and `kuke attach` drives the attach loop
// in-process via sbsh's pkg/attach.
func TestKuke_AttachDetach_KeepsTaskRunning(t *testing.T) {
	// Phase 1b (#410) lands the kuketty attach-socket RPC server: the
	// in-container kuketty wraps sbsh's pkg/terminal/server facade so
	// the same JSON-RPC + SCM_RIGHTS protocol `kuke attach` consumes
	// via github.com/eminwux/sbsh/pkg/attach is served over the
	// per-container socket. With #410 merged, this scenario re-enters
	// the suite.
	t.Parallel()

	// Setup: isolated runPath + per-test kukeond bound to that runPath.
	// Workload commands route through the daemon (#566 made them RPC-only),
	// but because the daemon's --run-path matches the test's the daemon
	// still lays down the per-container kuketty socket under the test's
	// runPath — which is what `kuke attach` and the post-detach `ctr task
	// ls` need to inspect.
	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)

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
		append(buildKukeDaemonArgs(host), "create", "realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "space", spaceName, "--realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "stack", stackName,
			"--realm", realmName, "--space", spaceName)...)

	// Step 2: apply the attachable cell fixture. Auto-starts the workload
	// (apply is the path that drives Cell to Ready), so by the time it
	// returns the container task should be RUNNING. The per-container
	// kuketty socket appears thanks to #77's per-container tty/ directory bind
	// mount, which makes sbsh's listener inode host-visible. This scenario
	// does not pre-create any placeholder socket file (the old workaround
	// was broken because sbsh unlinks-and-recreates the destination,
	// producing a fresh inode invisible to the host).
	applyAttachableCell(t, host, "attachable-cell.yaml", realmName, spaceName, stackName, cellName)

	// Step 3: confirm the per-container kuketty socket is in place. The runner
	// creates the metadata dir at provision time and sbsh binds the socket
	// inside the container at task start. If this is missing, pkg/attach
	// would fail in a way that's hard to attribute, so check explicitly.
	// Polls the SUN_PATH-safe symlink the runner stages (issue #521) — the
	// same handle `kuke attach` connects through. `os.Stat` follows the
	// symlink, so the loop succeeds once kuketty creates the deep socket
	// inode at the resolved target.
	socketPath := fs.ContainerSocketSymlinkPath(runPath,
		realmName, spaceName, stackName, cellName, "work")
	waitForSocket(t, socketPath, 10*time.Second)

	// Step 4: drive `kuke attach` over a real PTY and detach.
	session := startPTY(t, nil, kuke,
		append(buildKukeDaemonArgs(host),
			"attach", cellName,
			"--realm", realmName,
			"--space", spaceName,
			"--stack", stackName,
			"--container", "work",
		)...)
	t.Cleanup(session.Close)

	// Wait for the in-process pkg/attach loop to take over the PTY before
	// sending the keystroke. Without this grace, the bytes can race
	// pkg/attach's raw-mode setup and land before its filter is wired up,
	// in which case the detach sequence is forwarded verbatim instead of
	// triggering a clean exit.
	time.Sleep(attachConnectGrace)

	if err := session.Write(sbshDetachSequence); err != nil {
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
	realmNamespace := consts.RealmNamespace(realmName)
	if !verifyContainerTaskIsRunning(t, realmNamespace, containerdID) {
		t.Fatalf("container task %q in namespace %q is not RUNNING after detach",
			containerdID, realmNamespace)
	}
}

// reattachMarkerSettle gives sh enough time to read and process the marker
// assignment off the PTY before the test sends the detach keystroke. Without
// this grace, a detach can race the assignment line and the variable never
// makes it into sh's environment — session 2 then expands $MARKER to the
// empty string and the continuity check fails for the wrong reason.
const reattachMarkerSettle = 500 * time.Millisecond

// reattachMarkerTimeout caps how long session 2 waits for sh's expansion of
// $MARKER to surface on the PTY. The expansion is one local echo plus one
// fork-free builtin call, so it lands in tens of milliseconds on a healthy
// runner; the cap is set so a regression that breaks continuity fails loudly
// instead of hanging the suite.
const reattachMarkerTimeout = 10 * time.Second

// TestKuke_AttachReattach_TerminalContinuity is the phase 2b scenario
// (issue #72): after the phase 2a detach asserted in
// TestKuke_AttachDetach_KeepsTaskRunning, a second `kuke attach` session
// must reconnect to the *same* sbsh terminal — i.e. the same in-container
// sh process — and observe state planted by session 1. Session 1 sets a
// shell variable to a unique marker; session 2 reads it back via `echo`.
// The marker is a sh variable (not a filesystem sentinel) so the check
// asserts terminal continuity specifically — a freshly respawned sh after
// detach would have lost the variable even with the container filesystem
// intact.
func TestKuke_AttachReattach_TerminalContinuity(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)

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

	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "space", spaceName, "--realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "stack", stackName,
			"--realm", realmName, "--space", spaceName)...)

	// Apply the shell-workload variant: same cell shape as the phase 2a
	// fixture, but the `work` container's command is `sh -i` rather than
	// `sleep`, so the attached PTY exposes an interactive shell whose
	// in-memory state the reattach check can probe.
	applyAttachableCell(t, host, "attachable-shell-cell.yaml",
		realmName, spaceName, stackName, cellName)

	socketPath := fs.ContainerSocketSymlinkPath(runPath,
		realmName, spaceName, stackName, cellName, "work")
	waitForSocket(t, socketPath, 10*time.Second)

	attachArgs := append(buildKukeDaemonArgs(host),
		"attach", cellName,
		"--realm", realmName,
		"--space", spaceName,
		"--stack", stackName,
		"--container", "work",
	)

	// Unique per-run marker so a buffer leftover from any prior test run
	// (or from sbsh's session-history replay on attach) cannot satisfy the
	// substring check and mask a regression.
	marker := fmt.Sprintf("KUKE_REATTACH_MARKER_%d", time.Now().UnixNano())

	// Session 1: attach, plant the marker, detach.
	session1 := startPTY(t, nil, kuke, attachArgs...)
	t.Cleanup(session1.Close)

	time.Sleep(attachConnectGrace)

	if err := session1.Write([]byte(fmt.Sprintf("MARKER=%s\n", marker))); err != nil {
		t.Fatalf("session 1: write marker assignment: %v", err)
	}
	time.Sleep(reattachMarkerSettle)

	if err := session1.Write(sbshDetachSequence); err != nil {
		t.Fatalf("session 1: write detach sequence: %v", err)
	}

	exitCode, output, waitErr := session1.Wait(attachExitTimeout)
	if waitErr != nil && exitCode == -1 {
		t.Fatalf("session 1: kuke attach did not exit cleanly within %s: %v\noutput:\n%s",
			attachExitTimeout, waitErr, output)
	}
	if exitCode != 0 {
		t.Fatalf("session 1: kuke attach exited with code %d (want 0)\noutput:\n%s",
			exitCode, output)
	}

	// Session 2: reattach, expand $MARKER, confirm continuity.
	session2 := startPTY(t, nil, kuke, attachArgs...)
	t.Cleanup(session2.Close)

	time.Sleep(attachConnectGrace)

	if err := session2.Write([]byte("echo PROOF[$MARKER]\n")); err != nil {
		t.Fatalf("session 2: write read command: %v", err)
	}

	// PROOF[<marker>] uniquely identifies the *expansion* — neither the
	// PTY-local echo of session 1's assignment line nor the PTY-local
	// echo of session 2's read command contains the literal "PROOF[..."
	// wrapper around the marker value, so this substring only matches if
	// sh actually expanded $MARKER inside session 2.
	expected := fmt.Sprintf("PROOF[%s]", marker)
	if err := session2.WaitForOutput([]byte(expected), reattachMarkerTimeout); err != nil {
		t.Fatalf("session 2: marker continuity check failed: %v", err)
	}

	if err := session2.Write(sbshDetachSequence); err != nil {
		t.Fatalf("session 2: write detach sequence: %v", err)
	}
	exitCode, output, waitErr = session2.Wait(attachExitTimeout)
	if waitErr != nil && exitCode == -1 {
		t.Fatalf("session 2: kuke attach did not exit cleanly within %s: %v\noutput:\n%s",
			attachExitTimeout, waitErr, output)
	}
	if exitCode != 0 {
		t.Fatalf("session 2: kuke attach exited with code %d (want 0)\noutput:\n%s",
			exitCode, output)
	}
}

// applyAttachableCell substitutes test-side names into the named attachable
// cell fixture (under cmd/kuke/apply/testdata) and runs `kuke apply` against
// it via the per-test daemon (apply is daemon-only after #566). Fixture
// names are passed in so phase 2a's sleep-workload scenario and phase 2b's
// shell-workload reattach scenario can share the substitution / temp-write
// plumbing without copy-paste.
func applyAttachableCell(t *testing.T, host, fixture, realmName, spaceName, stackName, cellName string) {
	t.Helper()

	yamlContent := readTestdataYAML(t, fixture)
	for old, replacement := range map[string]string{
		"test-realm": realmName,
		"test-space": spaceName,
		"test-stack": stackName,
		"test-cell":  cellName,
	} {
		yamlContent = strings.ReplaceAll(yamlContent, old, replacement)
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, fixture)
	if err := os.WriteFile(tmpFile, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write tmp yaml: %v", err)
	}

	args := append(buildKukeDaemonArgs(host), "apply", "-f", tmpFile)
	runReturningBinary(t, nil, kuke, args...)
}

// waitForSocket polls until the per-container kuketty control socket appears
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
	t.Fatalf("kuketty socket %q did not appear within %s", path, timeout)
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
// cleanupCellNoDaemon route through `purge --cascade` in-process (via the
// `--run-path` promotion). Purge — not delete — because #566 made the
// workload `delete *` verbs daemon-only, and these helpers must work
// when no daemon is alive (post-reset, post-cascade-realm-teardown of the
// kuke-system cell that ran kukeond). Purge keeps the in-process branch
// per the issue's surviving-callers enumeration, so cleanup runs even
// against a runPath whose owning daemon (if any) has already been torn
// down by the test body.
func cleanupRealmNoDaemon(t *testing.T, runPath, realmName string) {
	t.Helper()
	args := append(buildKukeRunPathArgs(runPath), "purge", "realm", realmName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

func cleanupSpaceNoDaemon(t *testing.T, runPath, realmName, spaceName string) {
	t.Helper()
	args := append(buildKukeRunPathArgs(runPath), "purge", "space", spaceName,
		"--realm", realmName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

func cleanupStackNoDaemon(t *testing.T, runPath, realmName, spaceName, stackName string) {
	t.Helper()
	args := append(buildKukeRunPathArgs(runPath), "purge", "stack", stackName,
		"--realm", realmName, "--space", spaceName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

func cleanupCellNoDaemon(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) {
	t.Helper()
	args := append(buildKukeRunPathArgs(runPath), "purge", "cell", cellName,
		"--realm", realmName, "--space", spaceName, "--stack", stackName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

// getCellIDNoDaemon mirrors getCellID but goes through the in-process
// controller. The realm namespace argument used elsewhere collapses to the
// realm name when the in-process controller owns the metadata.
func getCellIDNoDaemon(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) (string, error) {
	t.Helper()

	args := append(buildKukeRunPathArgs(runPath),
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
