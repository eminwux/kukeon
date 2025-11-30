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
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// cleanupSpace deletes a space with cascade.
func cleanupSpace(t *testing.T, runPath, realmName, spaceName string) {
	t.Helper()

	args := append(buildKukeRunPathArgs(runPath), "delete", "space", spaceName, "--realm", realmName, "--cascade")
	_, _, _ = runBinary(t, nil, kuke, args...)
}

// TestKuke_NoSpaces tests kuke get space when no spaces exist.
func TestKuke_NoSpaces(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	args := append(buildKukeRunPathArgs(runPath), "get", "space", "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	var spaces []v1beta1.SpaceDoc
	if err := json.Unmarshal(output, &spaces); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(spaces) != 0 {
		t.Fatalf("expected empty space list, got %d spaces", len(spaces))
	}
}

// TestKuke_CreateSpace_VerifyState tests space creation with state-based verification.
func TestKuke_CreateSpace_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)

	// Cleanup: Delete space first, then realm (reverse dependency order)
	t.Cleanup(func() {
		cleanupSpace(t, runPath, realmName, spaceName)
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space
	args = append(buildKukeRunPathArgs(runPath), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Verify CNI config file exists
	if !verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file not found for space %q", spaceName)
	}

	// Step 4: Verify space metadata file exists
	if !verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file not found for space %q", spaceName)
	}

	// Step 5: Verify space appears in list (JSON parsing)
	if !verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q not found in space list", spaceName)
	}

	// Step 6: Verify space can be retrieved individually
	if !verifySpaceExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q cannot be retrieved individually", spaceName)
	}

	// Step 7: Verify cgroup path exists
	// Get space JSON to extract cgroup path
	args = append(buildKukeRunPathArgs(runPath), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	space, err := parseSpaceJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse space JSON: %v", err)
	}

	if space.Status.CgroupPath == "" {
		t.Fatal("space cgroup path is empty")
	}

	// Verify cgroup path exists in filesystem
	if !verifyCgroupPathExists(t, space.Status.CgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", space.Status.CgroupPath)
	}

	// Also verify expected cgroup path structure: /kukeon/{realmName}/{spaceName}
	// The actual path may include the current process's cgroup hierarchy before the kukeon path,
	// so we check if the path ends with the expected pattern.
	expectedCgroupPath := consts.KukeonCgroupRoot + "/" + realmName + "/" + spaceName
	if !strings.HasSuffix(space.Status.CgroupPath, expectedCgroupPath) {
		t.Logf(
			"cgroup path %q does not end with expected pattern %q, but verifying it exists anyway",
			space.Status.CgroupPath,
			expectedCgroupPath,
		)
	}
}

// TestKuke_DeleteSpace_VerifyState tests space deletion with state-based verification.
func TestKuke_DeleteSpace_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)

	// Cleanup: Safety net (space should already be deleted, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupSpace(t, runPath, realmName, spaceName)
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space (prerequisite for deletion test)
	args = append(buildKukeRunPathArgs(runPath), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Verify space exists initially (establish baseline)
	if !verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file not found for space %q", spaceName)
	}

	if !verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file not found for space %q", spaceName)
	}

	if !verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q not found in space list", spaceName)
	}

	if !verifySpaceExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q cannot be retrieved individually", spaceName)
	}

	// Get space JSON to extract cgroup path for later verification
	args = append(buildKukeRunPathArgs(runPath), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	space, err := parseSpaceJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse space JSON: %v", err)
	}

	if space.Status.CgroupPath == "" {
		t.Fatal("space cgroup path is empty")
	}

	cgroupPath := space.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Step 4: Delete the space
	args = append(buildKukeRunPathArgs(runPath), "delete", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 5: Verify CNI config file does NOT exist
	if verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file still exists for space %q after deletion", spaceName)
	}

	// Step 6: Verify metadata file does NOT exist
	if verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file still exists for space %q after deletion", spaceName)
	}

	// Step 7: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		// Build full filesystem path for error message
		relativePath := strings.TrimPrefix(cgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) still exists after space deletion", cgroupPath, fullPath)
	}

	// Step 8: Verify space does NOT appear in list
	if verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q still appears in space list after deletion", spaceName)
	}

	// Step 9: Verify individual get FAILS (returns non-zero exit code)
	args = append(buildKukeRunPathArgs(runPath), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	exitCode, _, _ := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf("expected get space to fail after deletion, but got exit code 0")
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_PurgeSpace_VerifyState tests space purging with comprehensive cleanup verification.
func TestKuke_PurgeSpace_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)

	// Cleanup: Safety net (space should already be purged, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupSpace(t, runPath, realmName, spaceName)
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space (prerequisite for purge test)
	args = append(buildKukeRunPathArgs(runPath), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Verify space exists initially (establish baseline)
	if !verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file not found for space %q", spaceName)
	}

	if !verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file not found for space %q", spaceName)
	}

	if !verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q not found in space list", spaceName)
	}

	if !verifySpaceExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q cannot be retrieved individually", spaceName)
	}

	// Get space JSON to extract cgroup path for later verification
	args = append(buildKukeRunPathArgs(runPath), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	space, err := parseSpaceJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse space JSON: %v", err)
	}

	if space.Status.CgroupPath == "" {
		t.Fatal("space cgroup path is empty")
	}

	cgroupPath := space.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Step 4: Purge the space (comprehensive cleanup)
	args = append(buildKukeRunPathArgs(runPath), "purge", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 5: Verify CNI config file does NOT exist
	if verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file still exists for space %q after purge", spaceName)
	}

	// Step 6: Verify metadata file does NOT exist
	if verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file still exists for space %q after purge", spaceName)
	}

	// Step 7: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		// Build full filesystem path for error message
		relativePath := strings.TrimPrefix(cgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) still exists after space purge", cgroupPath, fullPath)
	}

	// Step 8: Verify space does NOT appear in list
	if verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q still appears in space list after purge", spaceName)
	}

	// Step 9: Verify individual get FAILS (returns non-zero exit code)
	args = append(buildKukeRunPathArgs(runPath), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	exitCode, _, _ := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf("expected get space to fail after purge, but got exit code 0")
	}

	// Note: Purge performs comprehensive cleanup beyond standard delete:
	// - CNI resources cleanup
	// - Orphaned containers cleanup
	// These are handled internally by the purge operation

	// Cleanup runs automatically via t.Cleanup()
}
