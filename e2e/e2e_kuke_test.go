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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
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

// TestKuke_Daemon_Help tests `kuke daemon -h` and the `start`/`stop` subcommand help.
func TestKuke_Daemon_Help(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"daemon", "-h"},
		{"daemon", "--help"},
		{"daemon", "start", "-h"},
		{"daemon", "start", "--help"},
		{"daemon", "stop", "-h"},
		{"daemon", "stop", "--help"},
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
	mkdirRunPath(t, runPath)

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
	mkdirRunPath(t, runPath)

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
	mkdirRunPath(t, runPath)

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

	// Cleanup: Clean up resources created by init in reverse dependency order
	t.Cleanup(func() {
		cleanupCell(
			t, runPath,
			consts.KukeSystemRealmName,
			consts.KukeSystemSpaceName,
			consts.KukeSystemStackName,
			consts.KukeSystemCellName,
		)
		cleanupStack(
			t, runPath,
			consts.KukeSystemRealmName,
			consts.KukeSystemSpaceName,
			consts.KukeSystemStackName,
		)
		cleanupSpace(t, runPath, consts.KukeSystemRealmName, consts.KukeSystemSpaceName)
		cleanupRealm(t, runPath, consts.KukeSystemRealmName)
		cleanupStack(t, runPath, "default", "default", "default")
		cleanupSpace(t, runPath, "default", "default")
		cleanupRealm(t, runPath, "default")
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

	// Step 3: Verify default realm exists
	realmName := "default"
	if !verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file not found for default realm")
	}

	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("default realm not found in realm list")
	}

	if !verifyRealmExists(t, runPath, realmName) {
		t.Fatalf("default realm cannot be retrieved individually")
	}

	// Step 4: Verify containerd namespace exists
	namespace := consts.RealmNamespace(consts.KukeonDefaultRealmName)
	if !verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q not found after init", namespace)
	}

	// Step 5: Verify realm cgroup exists
	args = append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
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

	if !verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("default space not found in space list")
	}

	if !verifySpaceExists(t, runPath, realmName, spaceName) {
		t.Fatalf("default space cannot be retrieved individually")
	}

	// Step 7: Verify space CNI config exists
	if !verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file not found for default space")
	}

	// Step 8: Verify space cgroup exists
	args = append(buildKukeRunPathArgs(runPath), "get", "space", spaceName, "--realm", realmName, "--output", "json")
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
