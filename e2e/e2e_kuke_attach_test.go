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
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

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

// runFromStoppedRaceIterations is how many Stopped → kuke run cycles
// TestKuke_RunFromStopped_NonRootKukeonGroup_NoEACCES drives. The issue
// (#912) calls for ≥20 to catch the bind-then-chmod EACCES window even
// after the upstream sbsh fix closed it — a future regression that
// re-opens the window would surface on at least one of these dial
// attempts in normal CI load. Per iteration cost is ~1 stop + ~1 start
// + a short attach grace, so 20 iterations stay inside the e2e test's
// 60s budget on a healthy runner.
const runFromStoppedRaceIterations = 20

// runFromStoppedExitTimeout caps the wait for a single `kuke run`
// invocation to return after its detach keystroke. The healthy path
// (post-v0.12.1) returns within a second; the cap is generous so a
// genuinely stuck attach loop fails loudly instead of hanging the suite.
const runFromStoppedExitTimeout = 20 * time.Second

// TestKuke_RunFromStopped_NonRootKukeonGroup_NoEACCES is the e2e
// regression test that locks down eminwux/sbsh#361 (closed in sbsh
// v0.12.1) from the kukeon side. The race lived inside sbsh's
// OpenSocketCtrl: net.ListenConfig.Listen bound the unix socket at
// mode 0o666 & ~umask (effectively 0o600 under kukeond's 0o077 umask),
// then applySocketPerms chowned :kukeon and chmod'd 0o660 *after*
// Listen returned. A non-root operator in the kukeon group that
// dialed inside that window — which is exactly what `kuke run`
// against a Stopped cell does, since StartCell returns once the work
// task is Running and the client immediately attaches — saw EACCES.
//
// The test drives the original failure path verbatim: a kukeon-group
// non-root caller launching `kuke run <Stopped cell>` 20 times in
// succession. With sbsh ≥ v0.12.1 each iteration's dial sees the
// socket at 0o660 :kukeon (the bind itself lands at the configured
// mode now), the in-process pkg/attach loop enters normally, and the
// PTY detach keystroke walks the loop back out with exit 0. A
// regression that re-opens the bind/chmod window would resurface as
// `permission denied` on stderr and a non-zero exit on at least one
// of the iterations.
//
// Preconditions the test skips on (so the suite stays portable):
//   - euid != 0 (the test must drop privilege to a non-root caller)
//   - host has no `kukeon` system group (kuke init has not run, or the
//     group was removed)
//   - host has no `nobody` user (rare; only some minimal containers)
//
// Divergence from the issue body: #912 claimed the harness "has the
// group plumbed — see harness_daemon_test.go". It did not at the time
// — startKukeondDaemon dropped to socketModeRootOnly because no
// --socket-gid was passed. This test ships its own daemon variant
// (startKukeondDaemonWithSocketGID) and PTY launcher
// (startPTYWithCredential) for the privilege drop, so the harness
// gains the plumbing as part of this fix rather than depending on a
// separately landed change.
func TestKuke_RunFromStopped_NonRootKukeonGroup_NoEACCES(t *testing.T) {
	// Serial: the test drops privilege and chmods the runPath; it does
	// not parallelise meaningfully and any t.Parallel sibling running
	// as root inside the same runPath would muddy the EACCES signal.
	if os.Geteuid() != 0 {
		t.Skip("requires root to drop privilege to a non-root caller")
	}

	kukeonGrp, err := user.LookupGroup(consts.KukeonSystemGroup)
	if err != nil {
		t.Skipf("kukeon system group %q not present on host (run `kuke init` first): %v",
			consts.KukeonSystemGroup, err)
	}
	kukeonGID64, err := strconv.ParseUint(kukeonGrp.Gid, 10, 32)
	if err != nil {
		t.Fatalf("parse kukeon GID %q: %v", kukeonGrp.Gid, err)
	}
	kukeonGID := uint32(kukeonGID64)

	unpriv, err := user.Lookup("nobody")
	if err != nil {
		t.Skipf("nobody user not present on host: %v", err)
	}
	unprivUID64, err := strconv.ParseUint(unpriv.Uid, 10, 32)
	if err != nil {
		t.Fatalf("parse nobody UID %q: %v", unpriv.Uid, err)
	}
	unprivGID64, err := strconv.ParseUint(unpriv.Gid, 10, 32)
	if err != nil {
		t.Fatalf("parse nobody GID %q: %v", unpriv.Gid, err)
	}
	unprivCred := &syscall.Credential{
		Uid:    uint32(unprivUID64),
		Gid:    uint32(unprivGID64),
		Groups: []uint32{kukeonGID},
	}

	runPath := getRandomRunPath(t)
	// Make the runPath traversable for the unprivileged caller. The
	// per-container TTY inode already lands at 0o660 root:kukeon (via
	// the chown plumbing in #911), but every parent dir on the path
	// from / down to the socket symlink must grant search to the
	// caller or connect(2) ENOENTs out before the EACCES race could
	// surface. The kukeon-group operator on a real host reaches the
	// symlink via `/opt/kukeon/s/<id>` which is created by the daemon
	// at 0o755; per-test runPaths under /tmp inherit 0o700 from
	// MkdirTemp, so an explicit relax is needed here.
	if chmodErr := os.Chmod(runPath, 0o755); chmodErr != nil {
		t.Fatalf("chmod runPath %s: %v", runPath, chmodErr)
	}

	host := startKukeondDaemonWithSocketGID(t, runPath, int(kukeonGID))

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

	// Apply the attachable cell (sleep workload) to drive it to Ready.
	applyAttachableCell(t, host, "attachable-cell.yaml", realmName, spaceName, stackName, cellName)

	socketPath := fs.ContainerSocketSymlinkPath(runPath,
		realmName, spaceName, stackName, cellName, "work")
	waitForSocket(t, socketPath, 10*time.Second)

	// Relax search bits on `<runPath>/s/` after the runner has created
	// the symlink dir — the daemon creates it as root with the default
	// umask, which is too restrictive for `nobody` to traverse.
	symlinkDir := fs.ContainerSocketSymlinkDir(runPath)
	if chmodErr := os.Chmod(symlinkDir, 0o755); chmodErr != nil {
		t.Fatalf("chmod symlink dir %s: %v", symlinkDir, chmodErr)
	}

	stopArgs := append(buildKukeDaemonArgs(host),
		"stop", cellName,
		"--realm", realmName,
		"--space", spaceName,
		"--stack", stackName,
	)
	runArgs := append(buildKukeDaemonArgs(host),
		"run", cellName,
		"--realm", realmName,
		"--space", spaceName,
		"--stack", stackName,
		"--container", "work",
	)

	for iter := 0; iter < runFromStoppedRaceIterations; iter++ {
		// Drop the cell into Stopped so the next `kuke run` re-enters
		// the StartCell → attach branch (the path that races).
		runReturningBinary(t, nil, kuke, stopArgs...)

		session := startPTYWithCredential(t, nil, unprivCred, kuke, runArgs...)

		// Wait long enough for `kuke run`'s in-process pkg/attach loop
		// to either succeed (sbsh ≥ v0.12.1) or surface EACCES (sbsh
		// ≤ v0.12.0). A v0.12.0 race returns the error immediately;
		// a v0.12.1 healthy attach blocks on stdin until the detach
		// keystroke arrives. Reuses attachConnectGrace (the peer
		// attach scenarios' grace): the 2s span covers both the dial
		// (EACCES window) and the raw-mode + filter-wire setup that
		// must complete before the detach keystroke is forwarded so
		// the in-process pkg/attach filter triggers — both apply
		// here since the loop dials AND consumes a detach byte.
		time.Sleep(attachConnectGrace)
		if writeErr := session.Write(sbshDetachSequence); writeErr != nil {
			// Best-effort: a session that has already exited (race
			// fired) closes the PTY master and returns an error here.
			// Don't fail the test on the write — let Wait surface the
			// real failure mode below.
			t.Logf("iter %d: write detach sequence: %v (proceeding to Wait)", iter, writeErr)
		}

		exitCode, output, waitErr := session.Wait(runFromStoppedExitTimeout)
		if waitErr != nil && exitCode == -1 {
			session.Close()
			t.Fatalf("iter %d: kuke run did not exit within %s: %v\noutput:\n%s",
				iter, runFromStoppedExitTimeout, waitErr, output)
		}
		if exitCode != 0 {
			session.Close()
			t.Fatalf("iter %d: kuke run exited %d (want 0); sbsh#361 race may have re-opened\noutput:\n%s",
				iter, exitCode, output)
		}
		if strings.Contains(strings.ToLower(string(output)), "permission denied") {
			session.Close()
			t.Fatalf("iter %d: kuke run output contains 'permission denied'; sbsh#361 race re-opened\noutput:\n%s",
				iter, output)
		}
		if !strings.Contains(string(output), "containers: started") {
			session.Close()
			t.Fatalf("iter %d: kuke run output missing 'containers: started'\noutput:\n%s",
				iter, output)
		}
		session.Close()
	}

	// Final assertion: the per-container kuketty socket inode lands
	// at mode 0o660 with gid=kukeon. This is the steady-state
	// post-chmod shape; if a regression silently dropped the chown
	// or the chmod, the iterations above might still pass if they
	// raced just right (root-owned 0o600 succeeds for the root
	// daemon), but this stat would catch it.
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat work socket %s: %v", socketPath, err)
	}
	if got := info.Mode().Perm(); got != 0o660 {
		t.Errorf("work socket mode: got %#o want 0o660", got)
	}
	sysStat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("work socket stat: unexpected Sys() type %T", info.Sys())
	}
	if sysStat.Gid != kukeonGID {
		t.Errorf("work socket gid: got %d want %d (kukeon)", sysStat.Gid, kukeonGID)
	}
	if sysStat.Uid != 0 {
		t.Errorf("work socket uid: got %d want 0 (root)", sysStat.Uid)
	}
}

// TestKuke_RunFromStopped_RequiredRepoUnresolvable_NoEACCES locks issue
// #916: kuketty's claimSocketListener must apply the configured mode +
// GID at bind time, *before* sbshserver.Serve runs applySocketPerms.
// sbsh v0.12.1 (issue #912 / sbsh#361) closed the bind→chmod window
// inside its own OpenSocketCtrl, but kuketty pre-binds via
// claimSocketListener and hands the listener to sbsh through
// UseListener — sbsh's UseListener → bringUp → applySocketPerms only
// fires inside sbshserver.Serve, after kuketty's pre-Serve work
// (processRepos #617, processStages #635) has already run. Pre-fix,
// the socket lived at 0o777 & ~umask (typically 0o755 under the
// container's 0o022 umask) for the entire pre-Serve window, so a
// kukeon-group operator dialing the socket after the daemon reported
// "containers: started" — exactly what `kuke run` does against a
// Stopped cell — saw EACCES.
//
// The cell fixture declares a required=true repo with an unresolvable
// hostname (RFC 6761 reserves .invalid). DNS yields NXDOMAIN, git
// clone fails, kuketty exits non-zero in processRepos — but the
// socket inode is born inside claimSocketListener *before*
// processRepos runs. The test polls for the socket to appear (the
// bind happens) and stats it immediately. With the fix, mode is
// 0o660 and group is kukeon — the on-disk shape that admits a
// kukeon-group dial during the pre-Serve window. Pre-fix, this stat
// would show 0o755 group=kukeon (group ownership came from the SGID
// parent dir, mode was wrong).
//
// Preconditions mirror TestKuke_RunFromStopped_NonRootKukeonGroup_NoEACCES
// (euid 0, kukeon system group present). The test does not need a
// privilege-dropped client because asserting the inode mode/gid
// directly is a stronger and more deterministic signal than racing a
// dial against the brief pre-Serve window.
func TestKuke_RunFromStopped_RequiredRepoUnresolvable_NoEACCES(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root for kukeon-group --socket-gid daemon mode")
	}

	kukeonGrp, err := user.LookupGroup(consts.KukeonSystemGroup)
	if err != nil {
		t.Skipf("kukeon system group %q not present on host (run `kuke init` first): %v",
			consts.KukeonSystemGroup, err)
	}
	kukeonGID64, err := strconv.ParseUint(kukeonGrp.Gid, 10, 32)
	if err != nil {
		t.Fatalf("parse kukeon GID %q: %v", kukeonGrp.Gid, err)
	}
	kukeonGID := uint32(kukeonGID64)

	runPath := getRandomRunPath(t)
	if chmodErr := os.Chmod(runPath, 0o755); chmodErr != nil {
		t.Fatalf("chmod runPath %s: %v", runPath, chmodErr)
	}

	host := startKukeondDaemonWithSocketGID(t, runPath, int(kukeonGID))

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

	applyAttachableCell(t, host, "attachable-cell-required-repo-unresolvable.yaml",
		realmName, spaceName, stackName, cellName)

	socketPath := fs.ContainerSocketSymlinkPath(runPath,
		realmName, spaceName, stackName, cellName, "work")

	// Poll for the socket to appear. kuketty binds it inside
	// claimSocketListener — the moment the post-#916 bind site applies
	// mode + gid — *before* entering processRepos. The unresolvable
	// .invalid host yields NXDOMAIN quickly, so kuketty exits within a
	// second or two; the wait+stat loop succeeds the first iteration
	// the bind completes, which is well before that exit.
	waitForSocket(t, socketPath, 15*time.Second)

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat work socket %s during pre-Serve window: %v", socketPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("path %s is not a socket: mode=%v", socketPath, info.Mode())
	}
	if got := info.Mode().Perm(); got != 0o660 {
		t.Errorf("pre-Serve work socket mode: got %#o want 0o660 — #916 race re-opened",
			got)
	}
	sysStat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("work socket stat: unexpected Sys() type %T", info.Sys())
	}
	if sysStat.Gid != kukeonGID {
		t.Errorf("pre-Serve work socket gid: got %d want %d (kukeon) — #916 race re-opened",
			sysStat.Gid, kukeonGID)
	}
	if sysStat.Uid != 0 {
		t.Errorf("pre-Serve work socket uid: got %d want 0 (root)", sysStat.Uid)
	}
}

// startKukeondDaemonWithSocketGID is the kukeon-group-aware variant of
// startKukeondDaemon. It passes `--socket-gid <gid>` so the daemon
// applies socketModeGroupReadable (0o660 :kukeon) to its listener
// inode, making the socket dial-able by a non-root caller in the
// kukeon group — required for the privilege-dropped client side of
// TestKuke_RunFromStopped_NonRootKukeonGroup_NoEACCES. The rest of the
// startup (SUN_PATH-safe sockDir, sync wait on the socket file,
// SIGTERM-then-SIGKILL cleanup) mirrors startKukeondDaemon; this
// helper exists rather than threading a variadic arg through the
// default startKukeondDaemon because the kukeon-group path is the only
// caller and a single-purpose helper is easier to read at the test
// site.
func startKukeondDaemonWithSocketGID(t *testing.T, runPath string, socketGID int) string {
	t.Helper()

	binDir := os.Getenv("E2E_BIN_DIR")
	if binDir == "" {
		binDir = ".."
	}
	bin := filepath.Join(binDir, "kukeond")
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("kukeond binary %s not found, skipping daemon-mode test", bin)
	}

	sockDir, err := os.MkdirTemp("/tmp", "kd-") //nolint:usetesting // intentional shorter prefix; see startKukeondDaemon
	if err != nil {
		t.Fatalf("MkdirTemp(/tmp, kd-): %v", err)
	}
	// The sockDir parent must grant search to the unprivileged caller
	// so connect(2) can resolve the socket basename. The default
	// MkdirTemp mode (0o700) blocks `nobody`.
	if chmodErr := os.Chmod(sockDir, 0o755); chmodErr != nil {
		_ = os.RemoveAll(sockDir)
		t.Fatalf("chmod sockDir %s: %v", sockDir, chmodErr)
	}
	sockPath := filepath.Join(sockDir, "k.sock")

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin,
		"serve",
		"--socket", sockPath,
		"--socket-gid", strconv.Itoa(socketGID),
		"--run-path", runPath,
		"--reconcile-interval", "0",
		"--configuration", filepath.Join(runPath, "kukeond.yaml"),
	)
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

	exit := make(chan error, 1)
	go func() { exit <- cmd.Wait() }()

	deadline := time.NewTimer(daemonStartupTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()

	started := false
	for !started {
		if _, statErr := os.Stat(sockPath); statErr == nil {
			started = true
			continue
		}
		select {
		case waitErr := <-exit:
			cancel()
			logBytes, _ := os.ReadFile(logFile.Name())
			_ = logFile.Close()
			_ = os.Remove(logFile.Name())
			_ = os.RemoveAll(sockDir)
			t.Fatalf(
				"kukeond exited before socket %s appeared (wait=%v); daemon log:\n%s",
				sockPath, waitErr, string(logBytes),
			)
		case <-deadline.C:
			_ = cmd.Process.Signal(syscall.SIGKILL)
			<-exit
			cancel()
			logBytes, _ := os.ReadFile(logFile.Name())
			_ = logFile.Close()
			_ = os.Remove(logFile.Name())
			_ = os.RemoveAll(sockDir)
			t.Fatalf(
				"kukeond did not create socket %s within %s; daemon log:\n%s",
				sockPath, daemonStartupTimeout, string(logBytes),
			)
		case <-poll.C:
		}
	}

	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exit:
		case <-time.After(daemonShutdownTimeout):
			_ = cmd.Process.Signal(syscall.SIGKILL)
			<-exit
		}
		cancel()
		_ = logFile.Close()
		_ = os.Remove(logFile.Name())
		_ = os.RemoveAll(sockDir)
	})

	return fmt.Sprintf("unix://%s", sockPath)
}

// startPTYWithCredential is the privilege-dropping variant of startPTY.
// The child process is exec'd inside a fresh PTY with the given
// SysProcAttr.Credential applied — used by
// TestKuke_RunFromStopped_NonRootKukeonGroup_NoEACCES to launch `kuke
// run` as a kukeon-group non-root caller. creack/pty's StartWithSize
// preserves any pre-set SysProcAttr fields and only adds Setsid /
// Setctty, so the Credential survives the fork; the PTY master/slave
// pair is opened by the test (root) and inherited as the child's
// stdio, which side-steps the slave-open DAC check.
func startPTYWithCredential(
	t *testing.T,
	env []string,
	cred *syscall.Credential,
	command string,
	args ...string,
) *ptySession {
	t.Helper()

	dir := os.Getenv("E2E_BIN_DIR")
	if dir == "" {
		dir = ".."
	}
	bin := filepath.Join(dir, command)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("binary %s not found, skipping", bin)
	}

	cmd := exec.Command(bin, args...)
	if env != nil {
		cmd.Env = env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start %s as uid=%d gid=%d groups=%v: %v",
			bin, cred.Uid, cred.Gid, cred.Groups, err)
	}

	s := &ptySession{
		t:        t,
		cmd:      cmd,
		pty:      ptmx,
		waitDone: make(chan error, 1),
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.out.Write(buf[:n])
				s.mu.Unlock()
			}
			if readErr != nil {
				return
			}
		}
	}()

	go func() {
		s.waitDone <- cmd.Wait()
	}()

	return s
}
