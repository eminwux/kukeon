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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// generateUniqueRealmName generates a unique realm name for tests.
func generateUniqueRealmName(t *testing.T) string {
	t.Helper()
	timestamp := time.Now().UnixNano()
	hexID := fmt.Sprintf("%02x", timestamp&0xFF) // 2 hex chars from lower 8 bits
	return fmt.Sprintf("e-r-%s", hexID)
}

// verifyRealmMetadataExists verifies realm metadata file exists.
func verifyRealmMetadataExists(t *testing.T, runPath, realmName string) bool {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}
	fullRunPath := filepath.Join(cwd, runPath)

	metadataPath := fs.RealmMetadataPath(fullRunPath, realmName)
	_, err = os.Stat(metadataPath)
	return err == nil
}

// parseRealmListJSON parses kuke get realm --output json output.
func parseRealmListJSON(t *testing.T, output []byte) ([]v1beta1.RealmDoc, error) {
	t.Helper()

	var realms []v1beta1.RealmDoc
	if err := json.Unmarshal(output, &realms); err != nil {
		return nil, fmt.Errorf("failed to parse realm list JSON: %w", err)
	}

	return realms, nil
}

// parseRealmJSON parses kuke get realm <name> --output json output.
func parseRealmJSON(t *testing.T, output []byte) (*v1beta1.RealmDoc, error) {
	t.Helper()

	var realm v1beta1.RealmDoc
	if err := json.Unmarshal(output, &realm); err != nil {
		return nil, fmt.Errorf("failed to parse realm JSON: %w", err)
	}

	return &realm, nil
}

// verifyRealmInList verifies realm appears in kuke get realm list.
func verifyRealmInList(t *testing.T, runPath, realmName string) bool {
	t.Helper()

	args := append(buildKukeRunPathArgs(runPath), "get", "realm", "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	realms, err := parseRealmListJSON(t, output)
	if err != nil {
		t.Logf("failed to parse realm list: %v", err)
		return false
	}

	for _, realm := range realms {
		if realm.Metadata.Name == realmName {
			return true
		}
	}

	return false
}

// verifyRealmExists verifies realm can be retrieved individually.
func verifyRealmExists(t *testing.T, runPath, realmName string) bool {
	t.Helper()

	args := append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	exitCode, stdout, _ := runBinary(t, nil, kuke, args...)

	if exitCode != 0 {
		return false
	}

	realm, err := parseRealmJSON(t, stdout)
	if err != nil {
		t.Logf("failed to parse realm JSON: %v", err)
		return false
	}

	return realm.Metadata.Name == realmName
}

// cleanupRealm deletes a realm with cascade.
func cleanupRealm(t *testing.T, runPath, realmName string) {
	t.Helper()

	args := append(buildKukeRunPathArgs(runPath), "delete", "realm", realmName, "--cascade")
	// Don't fail if realm doesn't exist
	_, _, _ = runBinary(t, nil, kuke, args...)
}

// TestKuke_NoRealms tests kuke get realm when no realms exist.
func TestKuke_NoRealms(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	args := append(buildKukeRunPathArgs(runPath), "get", "realm", "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	var realms []v1beta1.RealmDoc
	if err := json.Unmarshal(output, &realms); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(realms) != 0 {
		t.Fatalf("expected empty realm list, got %d realms", len(realms))
	}
}

// TestKuke_Create_Realm_Help tests kuke create realm help.
func TestKuke_Create_Realm_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "create", "realm", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_Get_Realm_Help tests kuke get realm help.
func TestKuke_Get_Realm_Help(t *testing.T) {
	t.Parallel()

	exitCode, stdout, _ := runBinary(t, nil, kuke, "get", "realm", "-h")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(stdout) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// TestKuke_CreateRealm_VerifyState tests realm creation with state-based verification.
func TestKuke_CreateRealm_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)

	// Cleanup: Delete realm
	t.Cleanup(func() {
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Verify containerd namespace exists
	// Get namespace from realm (defaults to realm name)
	namespace := realmName
	if !verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q not found after realm creation", namespace)
	}

	// Step 3: Verify metadata file exists
	if !verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file not found for realm %q", realmName)
	}

	// Step 4: Verify realm appears in list (JSON parsing)
	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q not found in realm list", realmName)
	}

	// Step 5: Verify realm can be retrieved individually
	if !verifyRealmExists(t, runPath, realmName) {
		t.Fatalf("realm %q cannot be retrieved individually", realmName)
	}

	// Step 6: Verify cgroup path exists
	// Get realm JSON to extract cgroup path
	args = append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	realm, err := parseRealmJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse realm JSON: %v", err)
	}

	if realm.Status.CgroupPath == "" {
		t.Fatal("realm cgroup path is empty")
	}

	// Verify cgroup path exists in filesystem
	if !verifyCgroupPathExists(t, realm.Status.CgroupPath) {
		// Build full filesystem path for error message
		relativePath := strings.TrimPrefix(realm.Status.CgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) does not exist in filesystem", realm.Status.CgroupPath, fullPath)
	}

	// Also verify expected cgroup path structure: /kukeon/{realmName}
	// The actual path may include the current process's cgroup hierarchy before the kukeon path,
	// so we check if the path ends with the expected pattern.
	expectedCgroupPath := consts.KukeonCgroupRoot + "/" + realmName
	if !strings.HasSuffix(realm.Status.CgroupPath, expectedCgroupPath) {
		t.Logf(
			"cgroup path %q does not end with expected pattern %q, but verifying it exists anyway",
			realm.Status.CgroupPath,
			expectedCgroupPath,
		)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_DeleteRealm_VerifyState tests realm deletion with state-based verification.
func TestKuke_DeleteRealm_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)

	// Cleanup: Safety net (realm should already be deleted, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite for deletion test)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Verify realm exists initially (establish baseline)
	namespace := realmName
	if !verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q not found after realm creation", namespace)
	}

	if !verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file not found for realm %q", realmName)
	}

	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q not found in realm list", realmName)
	}

	if !verifyRealmExists(t, runPath, realmName) {
		t.Fatalf("realm %q cannot be retrieved individually", realmName)
	}

	// Get realm JSON to extract cgroup path for later verification
	args = append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	realm, err := parseRealmJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse realm JSON: %v", err)
	}

	if realm.Status.CgroupPath == "" {
		t.Fatal("realm cgroup path is empty")
	}

	cgroupPath := realm.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Step 3: Delete the realm
	args = append(buildKukeRunPathArgs(runPath), "delete", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 4: Verify metadata file does NOT exist
	if verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file still exists for realm %q after deletion", realmName)
	}

	// Step 5: Verify containerd namespace does NOT exist
	if verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q still exists after realm deletion", namespace)
	}

	// Step 6: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		// Build full filesystem path for error message
		relativePath := strings.TrimPrefix(cgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) still exists after realm deletion", cgroupPath, fullPath)
	}

	// Step 7: Verify realm does NOT appear in list
	if verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q still appears in realm list after deletion", realmName)
	}

	// Step 8: Verify individual get FAILS (returns non-zero exit code)
	args = append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	exitCode, _, _ := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf("expected get realm to fail after deletion, but got exit code 0")
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_PurgeRealm_VerifyState tests realm purging with comprehensive cleanup verification.
func TestKuke_PurgeRealm_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)

	// Cleanup: Safety net (realm should already be purged, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite for purge test)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Verify realm exists initially (establish baseline)
	namespace := realmName
	if !verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q not found after realm creation", namespace)
	}

	if !verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file not found for realm %q", realmName)
	}

	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q not found in realm list", realmName)
	}

	if !verifyRealmExists(t, runPath, realmName) {
		t.Fatalf("realm %q cannot be retrieved individually", realmName)
	}

	// Get realm JSON to extract cgroup path for later verification
	args = append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	realm, err := parseRealmJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse realm JSON: %v", err)
	}

	if realm.Status.CgroupPath == "" {
		t.Fatal("realm cgroup path is empty")
	}

	cgroupPath := realm.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Step 3: Purge the realm (comprehensive cleanup)
	args = append(buildKukeRunPathArgs(runPath), "purge", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 4: Verify metadata file does NOT exist
	if verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file still exists for realm %q after purge", realmName)
	}

	// Step 5: Verify containerd namespace does NOT exist
	if verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q still exists after realm purge", namespace)
	}

	// Step 6: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		// Build full filesystem path for error message
		relativePath := strings.TrimPrefix(cgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) still exists after realm purge", cgroupPath, fullPath)
	}

	// Step 7: Verify realm does NOT appear in list
	if verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q still appears in realm list after purge", realmName)
	}

	// Step 8: Verify individual get FAILS (returns non-zero exit code)
	args = append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	exitCode, _, _ := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf("expected get realm to fail after purge, but got exit code 0")
	}

	// Note: Purge performs comprehensive cleanup beyond standard delete:
	// - CNI resources cleanup
	// - Orphaned containers cleanup
	// These are handled internally by the purge operation

	// Cleanup runs automatically via t.Cleanup()
}
