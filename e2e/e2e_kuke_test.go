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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestKuke_Help tests kuke help command.
func TestKuke_Help(t *testing.T) {
	t.Parallel()

	_ = runReturningBinary(t, nil, kuke, "-h")
	_ = runReturningBinary(t, nil, kuke, "--help")
}

// TestKuke_NoArgs tests kuke with no arguments (should show help).
func TestKuke_NoArgs(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Init_Help tests kuke init help.
func TestKuke_Init_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "init", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Create_Help tests kuke create help.
func TestKuke_Create_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "create", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Get_Help tests kuke get help.
func TestKuke_Get_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "get", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Delete_Help tests kuke delete help.
func TestKuke_Delete_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "delete", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Start_Help tests kuke start help.
func TestKuke_Start_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "start", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Daemon_Help tests `kuke daemon -h` and the `start`/`stop`/`kill`/`reset` subcommand help.
func TestKuke_Daemon_Help(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"daemon", "-h"},
		{"daemon", "--help"},
		{"daemon", "start", "-h"},
		{"daemon", "start", "--help"},
		{"daemon", "stop", "-h"},
		{"daemon", "stop", "--help"},
		{"daemon", "kill", "-h"},
		{"daemon", "kill", "--help"},
		{"daemon", "reset", "-h"},
		{"daemon", "reset", "--help"},
	} {
		exitCode, stdout, stderr := runBinary(t, nil, kuke, args...)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0 for %v, got %d (stderr: %s)", args, exitCode, string(stderr))
		}
		if len(stdout) == 0 {
			t.Fatalf("expected non-empty output for %v", args)
		}
	}
}

// TestKuke_DaemonStart_Uninitialized verifies that `kuke daemon start` fails
// with a friendly "host not initialized" message when the run-path has no
// kukeond cell metadata. Running against a fresh tmp run-path simulates the
// no-init state without needing to tear down a real init (which would
// require docker / kukeond image staging).
func TestKuke_DaemonStart_Uninitialized(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)

	args := append(buildKukeRunPathArgs(runPath), "daemon", "start")
	exitCode, stdout, stderr := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf(
			"expected non-zero exit code on uninitialized host; stdout=%s stderr=%s",
			string(stdout), string(stderr),
		)
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "kuke init") {
		t.Fatalf("expected error to mention `kuke init`, got: %s", combined)
	}
}

// TestKuke_DaemonStop_Uninitialized verifies that `kuke daemon stop` fails
// with the same friendly "host not initialized" message as `daemon start` when
// the run-path has no kukeond cell metadata. Mirrors the start-side guard so
// regressions in either branch surface immediately.
func TestKuke_DaemonStop_Uninitialized(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)

	args := append(buildKukeRunPathArgs(runPath), "daemon", "stop")
	exitCode, stdout, stderr := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf(
			"expected non-zero exit code on uninitialized host; stdout=%s stderr=%s",
			string(stdout), string(stderr),
		)
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "kuke init") {
		t.Fatalf("expected error to mention `kuke init`, got: %s", combined)
	}
}

// TestKuke_DaemonKill_Uninitialized verifies that `kuke daemon kill` fails
// with the same friendly "host not initialized" message as `daemon start`/`stop`
// when the run-path has no kukeond cell metadata.
func TestKuke_DaemonKill_Uninitialized(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)

	args := append(buildKukeRunPathArgs(runPath), "daemon", "kill")
	exitCode, stdout, stderr := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf(
			"expected non-zero exit code on uninitialized host; stdout=%s stderr=%s",
			string(stdout), string(stderr),
		)
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "kuke init") {
		t.Fatalf("expected error to mention `kuke init`, got: %s", combined)
	}
}

// TestKuke_DaemonRestart_Uninitialized verifies that `kuke daemon restart`
// fails with the same friendly "host not initialized" message as `daemon
// start`/`stop`/`kill` when the run-path has no kukeond cell metadata. The
// stop-then-start composition must surface the same precondition as its
// individual phases.
func TestKuke_DaemonRestart_Uninitialized(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)

	args := append(buildKukeRunPathArgs(runPath), "daemon", "restart")
	exitCode, stdout, stderr := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf(
			"expected non-zero exit code on uninitialized host; stdout=%s stderr=%s",
			string(stdout), string(stderr),
		)
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "kuke init") {
		t.Fatalf("expected error to mention `kuke init`, got: %s", combined)
	}
}

// TestKuke_DaemonRestart_TimeoutFlag verifies the `--timeout` flag is
// registered on `kuke daemon restart`. The flag is the user-facing override
// for the stop phase's grace period (#221 AC) — its presence in --help is the
// minimum guard that the wiring did not regress.
func TestKuke_DaemonRestart_TimeoutFlag(t *testing.T) {
	t.Parallel()

	exitCode, stdout, stderr := runBinary(t, nil, kuke, "daemon", "restart", "--help")
	if exitCode != 0 {
		t.Fatalf("expected exit 0 from --help, got %d; stderr=%s", exitCode, string(stderr))
	}
	if !strings.Contains(string(stdout), "--timeout") {
		t.Fatalf("expected --timeout in `kuke daemon restart --help`; got:\n%s", string(stdout))
	}
}

// TestKuke_DaemonReset_Uninitialized verifies that `kuke daemon reset` is
// idempotent on a host with no kukeond cell metadata: exit 0 with an
// "already torn down" notice rather than the "host not initialized" sentinel
// reserved for read/write verbs.
func TestKuke_DaemonReset_Uninitialized(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)

	args := append(buildKukeRunPathArgs(runPath), "daemon", "reset")
	exitCode, stdout, stderr := runBinary(t, nil, kuke, args...)
	if exitCode != 0 {
		t.Fatalf(
			"expected exit 0 on uninitialized host (idempotent teardown); code=%d stdout=%s stderr=%s",
			exitCode, string(stdout), string(stderr),
		)
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "already torn down") {
		t.Fatalf("expected output to mention 'already torn down', got: %s", combined)
	}
}

// TestKuke_DaemonReset_Flags verifies the `--timeout` and `--purge-system`
// flags are registered on `kuke daemon reset`. Their presence in --help is
// the minimum guard that the wiring did not regress (#199 AC).
func TestKuke_DaemonReset_Flags(t *testing.T) {
	t.Parallel()

	exitCode, stdout, stderr := runBinary(t, nil, kuke, "daemon", "reset", "--help")
	if exitCode != 0 {
		t.Fatalf("expected exit 0 from --help, got %d; stderr=%s", exitCode, string(stderr))
	}
	out := string(stdout)
	for _, want := range []string{"--timeout", "--purge-system"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in `kuke daemon reset --help`; got:\n%s", want, out)
		}
	}
}

// TestKuke_DaemonReset_RoundTrip exercises the AC: `kuke init` → `kuke daemon
// reset --purge-system` → `kuke init` produces a clean re-bootstrap. Skipped
// without the make-e2e harness env (KUKEON_E2E_IMAGE / docker / ctr); runs in
// the same environment that TestKuke_Init_VerifyState does.
//
// Not run with t.Parallel() so it does not race TestKuke_Init_VerifyState
// on the shared /run/kukeon socket dir — go test interleaves serial tests
// between parallel batches, which gives this test exclusive access to the
// host-level state init touches.
func TestKuke_DaemonReset_RoundTrip(t *testing.T) {
	runPath := getRandomRunPath(t)

	// PR #489 (2026-05-14) moved the on-disk layout to <runPath>/data/<realm>
	// via fs.MetadataRoot; the prior <runPath>/<realm> form silently passes
	// "must not exist" negative checks but always fails the positive stats
	// after `kuke init` populates the real path. Route through the helper
	// so a future layout change cannot drift the assertion silently.
	defaultRealmDir := fs.RealmMetadataDir(runPath, consts.KukeonDefaultRealmName)
	systemRealmDir := fs.RealmMetadataDir(runPath, consts.KukeSystemRealmName)

	t.Cleanup(func() {
		// Best-effort host-level cleanup: a final reset --purge-system if the
		// test exited mid-flow. Failures here are diagnostic only.
		args := append(buildKukeRunPathArgs(runPath), "daemon", "reset", "--purge-system")
		_, _, _ = runBinary(t, nil, kuke, args...)

		// Use the *NoDaemon variants here — by the time cleanup fires, the
		// init-spawned kukeond cell has already been torn down by the reset
		// above, so the daemon-mode cleanupCell/cleanupRealm (#565) would
		// dial a vanished socket and leave residue. In-process is the only
		// mode that can guarantee post-test removal here.
		cleanupCellNoDaemon(
			t, runPath,
			consts.KukeSystemRealmName,
			consts.KukeSystemSpaceName,
			consts.KukeSystemStackName,
			consts.KukeSystemCellName,
		)
		cleanupRealmNoDaemon(t, runPath, consts.KukeSystemRealmName)
		cleanupRealmNoDaemon(t, runPath, consts.KukeonDefaultRealmName)
	})

	kukeondImage := loadKukeondImageIntoContainerd(t)

	// Step 1: init.
	args := append(buildKukeRunPathArgs(runPath), "init", "--kukeond-image", kukeondImage)
	exitCode, stdout, stderr := runBinary(t, nil, kuke, args...)
	if exitCode != 0 {
		t.Fatalf("first init failed: code=%d stdout=%s stderr=%s", exitCode, string(stdout), string(stderr))
	}
	if _, statErr := os.Stat(defaultRealmDir); statErr != nil {
		t.Fatalf("default realm dir missing after init: %v", statErr)
	}
	if _, statErr := os.Stat(systemRealmDir); statErr != nil {
		t.Fatalf("kuke-system realm dir missing after init: %v", statErr)
	}

	// Seed a sentinel under default so the AC "user-realm data preserved" has
	// something concrete to check after reset.
	sentinelPath := filepath.Join(defaultRealmDir, "user-data.sentinel")
	if writeErr := os.WriteFile(sentinelPath, []byte("preserve-me"), 0o600); writeErr != nil {
		t.Fatalf("seed default-realm sentinel: %v", writeErr)
	}

	// Step 2: reset --purge-system.
	args = append(buildKukeRunPathArgs(runPath), "daemon", "reset", "--purge-system")
	exitCode, stdout, stderr = runBinary(t, nil, kuke, args...)
	if exitCode != 0 {
		t.Fatalf("daemon reset failed: code=%d stdout=%s stderr=%s", exitCode, string(stdout), string(stderr))
	}
	if _, statErr := os.Stat(systemRealmDir); !os.IsNotExist(statErr) {
		t.Fatalf("kuke-system dir was not removed under --purge-system: stat err=%v", statErr)
	}
	if _, statErr := os.Stat(sentinelPath); statErr != nil {
		t.Fatalf("default-realm sentinel must be preserved by reset: %v", statErr)
	}

	// Step 3: init again must produce a clean re-bootstrap.
	args = append(buildKukeRunPathArgs(runPath), "init", "--kukeond-image", kukeondImage)
	exitCode, stdout, stderr = runBinary(t, nil, kuke, args...)
	if exitCode != 0 {
		t.Fatalf("re-init after reset failed: code=%d stdout=%s stderr=%s", exitCode, string(stdout), string(stderr))
	}
	if _, statErr := os.Stat(systemRealmDir); statErr != nil {
		t.Fatalf("kuke-system realm dir missing after re-init: %v", statErr)
	}
	if _, statErr := os.Stat(sentinelPath); statErr != nil {
		t.Fatalf("default-realm sentinel must still be present after re-init: %v", statErr)
	}
	if !strings.Contains(string(stdout), "Initialized Kukeon runtime") {
		t.Fatalf("re-init output missing bootstrap header; stdout=%s", string(stdout))
	}
}

// TestKuke_Stop_Help tests kuke stop help.
func TestKuke_Stop_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "stop", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Kill_Help tests kuke kill help.
func TestKuke_Kill_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "kill", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Purge_Help tests kuke purge help.
func TestKuke_Purge_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "purge", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Autocomplete_Help tests kuke autocomplete help.
func TestKuke_Autocomplete_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "autocomplete", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Version_Help tests kuke version help.
func TestKuke_Version_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "version", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Init_VerifyState tests kuke init command with state-based verification.
func TestKuke_Init_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup: Use a fresh, isolated run path
	runPath := getRandomRunPath(t)

	// Step 0: Purge resources created by init in reverse dependency order
	// (cleanup from previous runs that may have left orphaned containerd containers)
	_, _, _ = runBinary(t, nil, kuke, append(
		buildKukeRunPathArgs(runPath),
		"purge", "cell", consts.KukeSystemCellName,
		"--realm", consts.KukeSystemRealmName,
		"--space", consts.KukeSystemSpaceName,
		"--stack", consts.KukeSystemStackName,
	)...)
	_, _, _ = runBinary(t, nil, kuke, append(
		buildKukeRunPathArgs(runPath),
		"purge", "stack", consts.KukeSystemStackName,
		"--realm", consts.KukeSystemRealmName,
		"--space", consts.KukeSystemSpaceName,
	)...)
	_, _, _ = runBinary(t, nil, kuke, append(
		buildKukeRunPathArgs(runPath),
		"purge", "space", consts.KukeSystemSpaceName,
		"--realm", consts.KukeSystemRealmName,
	)...)
	_, _, _ = runBinary(t, nil, kuke, append(
		buildKukeRunPathArgs(runPath),
		"purge", "realm", consts.KukeSystemRealmName,
	)...)
	_, _, _ = runBinary(t, nil, kuke, append(
		buildKukeRunPathArgs(runPath),
		"purge", "stack", "default",
		"--realm", "default", "--space", "default",
	)...)
	_, _, _ = runBinary(t, nil, kuke, append(
		buildKukeRunPathArgs(runPath),
		"purge", "space", "default",
		"--realm", "default",
	)...)
	_, _, _ = runBinary(t, nil, kuke, append(
		buildKukeRunPathArgs(runPath),
		"purge", "realm", "default",
	)...)

	// Cleanup: Clean up resources created by init in reverse dependency
	// order. Use the *NoDaemon variants because the cleanup chain ends with
	// `cleanupRealmNoDaemon(kuke-system) --cascade`, which tears down the
	// kukeond cell that init brought up; subsequent `cleanupRealm(default)`
	// would dial a dead socket if routed via daemon mode. In-process is the
	// reliable mode here for the same reason it is in
	// TestKuke_DaemonReset_RoundTrip and the attach test (#565 AC).
	t.Cleanup(func() {
		cleanupCellNoDaemon(
			t, runPath,
			consts.KukeSystemRealmName,
			consts.KukeSystemSpaceName,
			consts.KukeSystemStackName,
			consts.KukeSystemCellName,
		)
		cleanupStackNoDaemon(
			t, runPath,
			consts.KukeSystemRealmName,
			consts.KukeSystemSpaceName,
			consts.KukeSystemStackName,
		)
		cleanupSpaceNoDaemon(t, runPath, consts.KukeSystemRealmName, consts.KukeSystemSpaceName)
		cleanupRealmNoDaemon(t, runPath, consts.KukeSystemRealmName)
		cleanupStackNoDaemon(t, runPath, "default", "default", "default")
		cleanupSpaceNoDaemon(t, runPath, "default", "default")
		cleanupRealmNoDaemon(t, runPath, "default")
	})

	// Step 0.5: Stage the local kukeond image into containerd's kuke-system
	// namespace. The Step 0 purge above wipes any prior image, and dev builds
	// from a dirty/untagged tree resolve to a registry tag that doesn't exist,
	// so init would fail on image pull without this.
	kukeondImage := loadKukeondImageIntoContainerd(t)

	// Step 1: Run init command
	args := append(buildKukeRunPathArgs(runPath), "init", "--kukeond-image", kukeondImage)
	output := runReturningBinary(t, nil, kuke, args...)

	// Step 2: Verify bootstrap report output contains expected content
	if len(output) == 0 {
		t.Fatal("expected non-empty bootstrap report output")
	}

	outputStr := string(output)

	// Verify key phrases in output
	expectedPhrases := []string{
		"Initialized Kukeon runtime",
		"Realm:",
		"Run path:",
		"Actions:",
	}
	for _, phrase := range expectedPhrases {
		if !strings.Contains(outputStr, phrase) {
			t.Fatalf("bootstrap report output missing expected phrase: %q", phrase)
		}
	}

	// `kuke init --run-path X` brings up the kuke-system kukeond cell on a
	// socket derived from runPath (#569). Workload-command verifications
	// below dial that socket per the #565 AC so the daemon-routed view is
	// what we assert on, not the in-process controller's view.
	host := "unix://" + filepath.Join(runPath, "kukeond.sock")

	// Step 3: Verify default realm exists
	realmName := "default"
	if !verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file not found for default realm")
	}

	if !verifyRealmInList(t, host, realmName) {
		t.Fatalf("default realm not found in realm list")
	}

	if !verifyRealmExists(t, host, realmName) {
		t.Fatalf("default realm cannot be retrieved individually")
	}

	// Step 4: Verify containerd namespace exists
	namespace := consts.RealmNamespace(consts.KukeonDefaultRealmName)
	if !verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q not found after init", namespace)
	}

	// Step 5: Verify realm cgroup exists
	args = append(buildKukeDaemonArgs(host), "get", "realm", realmName, "--output", "json")
	realmOutput := runReturningBinary(t, nil, kuke, args...)

	realm, err := parseRealmJSON(t, realmOutput)
	if err != nil {
		t.Fatalf("failed to parse realm JSON: %v", err)
	}

	if realm.Status.CgroupPath == "" {
		t.Fatal("realm cgroup path is empty - cgroup path should be stored in metadata after init")
	}

	if !verifyCgroupPathExists(t, realm.Status.CgroupPath) {
		t.Fatalf("realm cgroup path %q does not exist in filesystem", realm.Status.CgroupPath)
	}

	// Step 6: Verify default space exists
	spaceName := "default"
	if !verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file not found for default space")
	}

	if !verifySpaceInList(t, host, realmName, spaceName) {
		t.Fatalf("default space not found in space list")
	}

	if !verifySpaceExists(t, host, realmName, spaceName) {
		t.Fatalf("default space cannot be retrieved individually")
	}

	// Step 7: Verify space CNI config exists
	if !verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file not found for default space")
	}

	// Step 8: Verify space cgroup exists
	args = append(buildKukeDaemonArgs(host), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	spaceOutput := runReturningBinary(t, nil, kuke, args...)

	space, err := parseSpaceJSON(t, spaceOutput)
	if err != nil {
		t.Fatalf("failed to parse space JSON: %v", err)
	}

	if space.Status.CgroupPath == "" {
		t.Fatal("space cgroup path is empty")
	}

	if !verifyCgroupPathExists(t, space.Status.CgroupPath) {
		t.Fatalf("space cgroup path %q does not exist in filesystem", space.Status.CgroupPath)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_Version_Output tests kuke version command output.
func TestKuke_Version_Output(t *testing.T) {
	t.Parallel()

	// Step 1: Run version command
	output := runReturningBinary(t, nil, kuke, "version")

	// Step 2: Verify output is non-empty
	if len(output) == 0 {
		t.Fatal("expected non-empty version output")
	}

	outputStr := strings.TrimSpace(string(output))

	// Step 3: Verify version string is non-empty after trimming whitespace
	if outputStr == "" {
		t.Fatal("version output is empty after trimming whitespace")
	}

	// Step 4: Verify version string format (should be a valid version)
	// Version can be in various formats:
	// - Semantic version: "0.1.0"
	// - Git tag: "v1.2.3"
	// - Git describe: "v1.2.3-5-gabc123"
	// - Dirty build: "v1.2.3-dirty"
	// So we just verify it's not empty and contains at least one character/digit
	if len(outputStr) < 1 {
		t.Fatalf("version string too short: %q", outputStr)
	}

	// Verify it contains at least one alphanumeric character or dot or dash
	hasValidChar := false
	for _, r := range outputStr {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			hasValidChar = true
			break
		}
	}
	if !hasValidChar {
		t.Fatalf("version string contains no valid characters: %q", outputStr)
	}
}

// TestKuke_Autocomplete_Bash tests kuke autocomplete bash command output.
func TestKuke_Autocomplete_Bash(t *testing.T) {
	t.Parallel()

	// Step 1: Run autocomplete bash command
	output := runReturningBinary(t, nil, kuke, "autocomplete", "bash")

	// Step 2: Verify output is non-empty
	if len(output) == 0 {
		t.Fatal("expected non-empty bash completion script output")
	}

	outputStr := string(output)

	// Step 3: Verify output contains bash completion script markers
	// Bash completion scripts typically contain "complete" function definitions
	// or "_kuke" function names
	hasBashMarker := strings.Contains(outputStr, "complete") || strings.Contains(outputStr, "_kuke")
	if !hasBashMarker && len(outputStr) > 0 {
		// If we got output but no standard markers, still verify it's substantial
		// (some bash completion scripts may have different structure)
		if len(outputStr) < 100 {
			t.Fatalf(
				"bash completion script seems too short (%d bytes) and contains no expected markers",
				len(outputStr),
			)
		}
	}

	// Step 4: Verify output is substantial (bash completion scripts are typically hundreds of lines)
	if len(outputStr) < 50 {
		t.Fatalf("bash completion script output too short: %d bytes", len(outputStr))
	}
}

// TestKuke_Autocomplete_Zsh tests kuke autocomplete zsh command output.
func TestKuke_Autocomplete_Zsh(t *testing.T) {
	t.Parallel()

	// Step 1: Run autocomplete zsh command
	output := runReturningBinary(t, nil, kuke, "autocomplete", "zsh")

	// Step 2: Verify output is non-empty
	if len(output) == 0 {
		t.Fatal("expected non-empty zsh completion script output")
	}

	outputStr := string(output)

	// Step 3: Verify output contains zsh completion script markers
	// Zsh completion scripts typically contain "compdef" or "#compdef" directives
	hasZshMarker := strings.Contains(outputStr, "compdef") || strings.Contains(outputStr, "#compdef")
	if !hasZshMarker && len(outputStr) > 0 {
		// If we got output but no standard markers, still verify it's substantial
		if len(outputStr) < 100 {
			t.Fatalf(
				"zsh completion script seems too short (%d bytes) and contains no expected markers",
				len(outputStr),
			)
		}
	}

	// Step 4: Verify output is substantial (zsh completion scripts are typically hundreds of lines)
	if len(outputStr) < 50 {
		t.Fatalf("zsh completion script output too short: %d bytes", len(outputStr))
	}
}

// TestKuke_Autocomplete_Fish tests kuke autocomplete fish command output.
func TestKuke_Autocomplete_Fish(t *testing.T) {
	t.Parallel()

	// Step 1: Run autocomplete fish command
	output := runReturningBinary(t, nil, kuke, "autocomplete", "fish")

	// Step 2: Verify output is non-empty
	if len(output) == 0 {
		t.Fatal("expected non-empty fish completion script output")
	}

	outputStr := string(output)

	// Step 3: Verify output contains fish completion script markers
	// Fish completion scripts typically contain "complete" commands
	hasFishMarker := strings.Contains(outputStr, "complete")
	if !hasFishMarker && len(outputStr) > 0 {
		// If we got output but no standard markers, still verify it's substantial
		if len(outputStr) < 100 {
			t.Fatalf(
				"fish completion script seems too short (%d bytes) and contains no expected markers",
				len(outputStr),
			)
		}
	}

	// Step 4: Verify output is substantial (fish completion scripts are typically hundreds of lines)
	if len(outputStr) < 50 {
		t.Fatalf("fish completion script output too short: %d bytes", len(outputStr))
	}
}

// TestKuke_Uninstall_VerifyState exercises the full init → create user-realm
// cell → uninstall round-trip and asserts the post-teardown state is
// genuinely clean. Sibling to TestKuke_Init_VerifyState (which is the
// init-side state check); this one is the teardown-side regression guard
// for the bug classes behind issues #193 (purged-but-namespace-survives),
// #194 (residual namespaces), and #196 (kukeond-running-during-purge) —
// each of which previously let `kuke uninstall` report success while
// leaving containerd namespaces, /run/kukeon, or /opt/kukeon stragglers
// on disk.
//
// Not run with t.Parallel(): uninstall touches host-global state (the
// kukeon system user/group, every kukeon-suffixed containerd namespace,
// the SocketDir + RunPath trees) so it must not race
// TestKuke_Init_VerifyState or TestKuke_DaemonReset_RoundTrip, which
// touch the same host-level surfaces.
func TestKuke_Uninstall_VerifyState(t *testing.T) {
	runPath := getRandomRunPath(t)
	kukeondImage := loadKukeondImageIntoContainerd(t)

	defaultNS := consts.RealmNamespace(consts.KukeonDefaultRealmName)
	systemNS := consts.RealmNamespace(consts.KukeSystemRealmName)
	// init brings up its own kukeond bound to <runPath>/kukeond.sock via
	// the root-level --run-path → KUKEOND_SOCKET derivation (see
	// applyRunPathImpliesKukeondSocket in cmd/kuke/kuke.go); subsequent
	// workload commands dial that socket so the daemon-routed view is
	// what we assert on. Matches the pattern in TestKuke_Init_VerifyState.
	host := "unix://" + filepath.Join(runPath, "kukeond.sock")

	t.Cleanup(func() {
		// Best-effort post-test cleanup: if the test exited mid-flow (e.g.
		// before the in-test uninstall fired), re-run uninstall so we don't
		// strand the host's /run/kukeon, /opt/kukeon, or kukeon-suffixed
		// containerd namespaces for the next test. Failures here are
		// diagnostic only — the in-test uninstall is what the AC asserts on.
		_, _, _ = runBinary(t, nil, kuke, append(
			buildKukeRunPathArgs(runPath), "uninstall", "--yes",
		)...)
	})

	// Step 1: init brings up the default + kuke-system realms and the
	// kukeond cell inside kuke-system. Pre-uninstall state must be
	// populated so the post-uninstall "gone" assertions below can't pass
	// vacuously and silently hide a regression.
	args := append(buildKukeRunPathArgs(runPath), "init", "--kukeond-image", kukeondImage)
	exitCode, stdout, stderr := runBinary(t, nil, kuke, args...)
	if exitCode != 0 {
		t.Fatalf("kuke init failed: code=%d stdout=%s stderr=%s",
			exitCode, string(stdout), string(stderr))
	}
	if !verifyContainerdNamespace(t, defaultNS) {
		t.Fatalf("pre-uninstall: containerd namespace %q missing after init", defaultNS)
	}
	if !verifyContainerdNamespace(t, systemNS) {
		t.Fatalf("pre-uninstall: containerd namespace %q missing after init", systemNS)
	}
	if _, statErr := os.Stat(runPath); statErr != nil {
		t.Fatalf("pre-uninstall: runPath %q missing after init: %v", runPath, statErr)
	}

	// Step 2: provision a user-realm cell under the pre-created `default`
	// space so uninstall has to drain both a kuke-system workload (the
	// kukeond cell init brought up) and a user-realm workload. Without
	// this, uninstall would only exercise the well-known-realm path and
	// could regress on user-created realms silently — that is the failure
	// mode the issue's AC cites for #194.
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	defaultRealm := consts.KukeonDefaultRealmName
	const defaultSpace = "default"

	args = append(buildKukeDaemonArgs(host),
		"create", "stack", stackName, "--realm", defaultRealm, "--space", defaultSpace)
	if exit, out, errOut := runBinary(t, nil, kuke, args...); exit != 0 {
		t.Fatalf("create stack failed: code=%d stdout=%s stderr=%s",
			exit, string(out), string(errOut))
	}

	args = append(buildKukeDaemonArgs(host),
		"create", "cell", cellName,
		"--realm", defaultRealm, "--space", defaultSpace, "--stack", stackName)
	if exit, out, errOut := runBinary(t, nil, kuke, args...); exit != 0 {
		t.Fatalf("create cell failed: code=%d stdout=%s stderr=%s",
			exit, string(out), string(errOut))
	}

	// Step 3: uninstall. Tears down systemd unit (no-op on dev hosts),
	// every realm cascade, /run/kukeon, /opt/kukeon, kukeon user/group.
	args = append(buildKukeRunPathArgs(runPath), "uninstall", "--yes")
	exitCode, stdout, stderr = runBinary(t, nil, kuke, args...)
	if exitCode != 0 {
		t.Fatalf("kuke uninstall failed: code=%d stdout=%s stderr=%s",
			exitCode, string(stdout), string(stderr))
	}

	// Step 4: assert the host is genuinely clean.
	//
	// 4a. No kukeon-suffixed containerd namespaces survive. Enumerate
	// every namespace via `ctr ns ls` and require none carry the
	// `.kukeon.io` suffix — guards #193 / #194 directly. Tasks living
	// in those namespaces are implicitly gone once the namespace is.
	if surviving := listKukeonContainerdNamespaces(t); len(surviving) > 0 {
		t.Fatalf("post-uninstall: kukeon-owned containerd namespaces survive: %v", surviving)
	}

	// 4b. runPath is gone. With --run-path X the root-level derivation
	// collapses SocketDir and RunPath to the same dir, so a single check
	// covers both AC items (`/run/kukeon` and `/opt/kukeon` analogues).
	if _, statErr := os.Stat(runPath); !os.IsNotExist(statErr) {
		t.Fatalf("post-uninstall: runPath %q must be gone, stat err=%v", runPath, statErr)
	}

	// 4c. kukeon system user/group are absent. The AC permits "gone or
	// absent if they never existed"; both reduce to "id/getent reports
	// absent" post-uninstall.
	if systemUserExists(t, consts.KukeonSystemUser) {
		t.Fatalf("post-uninstall: system user %q must be absent", consts.KukeonSystemUser)
	}
	if systemGroupExists(t, consts.KukeonSystemGroup) {
		t.Fatalf("post-uninstall: system group %q must be absent", consts.KukeonSystemGroup)
	}

	// Step 5: re-init must succeed on the now-clean host. Uninstall
	// dropped the kuke-system containerd namespace, which wiped the
	// staged kukeond image with it — unlike daemon reset --purge-system
	// (which only removes /opt/kukeon/data/kuke-system metadata). Reload
	// before re-init or the second init fails on image pull.
	_ = loadKukeondImageIntoContainerd(t)
	args = append(buildKukeRunPathArgs(runPath), "init", "--kukeond-image", kukeondImage)
	exitCode, stdout, stderr = runBinary(t, nil, kuke, args...)
	if exitCode != 0 {
		t.Fatalf("re-init after uninstall failed: code=%d stdout=%s stderr=%s",
			exitCode, string(stdout), string(stderr))
	}
	if !verifyContainerdNamespace(t, defaultNS) {
		t.Fatalf("post-reinit: containerd namespace %q missing", defaultNS)
	}
	if !verifyContainerdNamespace(t, systemNS) {
		t.Fatalf("post-reinit: containerd namespace %q missing", systemNS)
	}
}

// listKukeonContainerdNamespaces returns every containerd namespace whose
// name carries the canonical `.kukeon.io` suffix. Used by the uninstall
// VerifyState test to assert no kukeon-owned namespaces survived the
// teardown — the regression guard for #193 / #194.
//
// Skipped at the t.Skipf level when `ctr` is not on PATH (consistent with
// verifyContainerdNamespace's behavior); a missing binary cannot be
// distinguished from a clean host, so the test refuses to declare success.
func listKukeonContainerdNamespaces(t *testing.T) []string {
	t.Helper()

	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Skipf("ctr binary not found, cannot verify post-uninstall namespace state: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "ns", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ctr ns ls failed: %v, output: %s", err, string(out))
	}

	// `ctr ns ls` emits a `NAME LABELS` header followed by one namespace
	// per line. Filter to the kukeon-suffixed names so the report names
	// what survived without spamming "default", "moby", etc.
	var surviving []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "NAME" {
			continue
		}
		if consts.IsKukeonNamespace(fields[0]) {
			surviving = append(surviving, fields[0])
		}
	}
	return surviving
}

// systemUserExists reports whether a system account by this name resolves
// via `id -u`. Matches the contract used by controller/uninstall.go's
// lookupUser so the test's "absent" assertion lines up with what the
// production code's user-removal step considers absent.
func systemUserExists(t *testing.T, name string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "id", "-u", name).Run() == nil
}

// systemGroupExists reports whether a system group by this name resolves
// via `getent group`. Matches lookupGroup in controller/uninstall.go.
func systemGroupExists(t *testing.T, name string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "getent", "group", name).Run() == nil
}
