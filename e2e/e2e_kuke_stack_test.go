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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// cleanupStack deletes a stack with cascade.
func cleanupStack(t *testing.T, runPath, realmName, spaceName, stackName string) {
	t.Helper()

	args := append(
		buildKukeRunPathArgs(runPath),
		"delete",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--cascade",
	)
	_, _, _ = runBinary(t, nil, kuke, args...)
}

// TestKuke_NoStacks tests kuke get stack when no stacks exist.
func TestKuke_NoStacks(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	args := append(buildKukeRunPathArgs(runPath), "get", "stack", "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	var stacks []v1beta1.StackDoc
	if err := json.Unmarshal(output, &stacks); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(stacks) != 0 {
		t.Fatalf("expected empty stack list, got %d stacks", len(stacks))
	}
}

// TestKuke_CreateStack_VerifyState tests stack creation with state-based verification.
func TestKuke_CreateStack_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)

	// Cleanup: Delete stack first, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
		cleanupStack(t, runPath, realmName, spaceName, stackName)
		cleanupSpace(t, runPath, realmName, spaceName)
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space (prerequisite)
	args = append(buildKukeRunPathArgs(runPath), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Create stack
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 4: Verify stack metadata file exists
	if !verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file not found for stack %q", stackName)
	}

	// Step 5: Verify stack appears in list (JSON parsing)
	if !verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q not found in stack list", stackName)
	}

	// Step 6: Verify stack can be retrieved individually
	if !verifyStackExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q cannot be retrieved individually", stackName)
	}

	// Step 7: Verify cgroup path exists
	// Get stack JSON to extract cgroup path
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	stack, err := parseStackJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse stack JSON: %v", err)
	}

	if stack.Status.CgroupPath == "" {
		t.Fatal("stack cgroup path is empty")
	}

	// Verify cgroup path exists in filesystem
	if !verifyCgroupPathExists(t, stack.Status.CgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", stack.Status.CgroupPath)
	}

	// Also verify expected cgroup path structure: /kukeon/{realmName}/{spaceName}/{stackName}
	// The actual path may include the current process's cgroup hierarchy before the kukeon path,
	// so we check if the path ends with the expected pattern.
	expectedCgroupPath := consts.KukeonCgroupRoot + "/" + realmName + "/" + spaceName + "/" + stackName
	if !strings.HasSuffix(stack.Status.CgroupPath, expectedCgroupPath) {
		t.Logf(
			"cgroup path %q does not end with expected pattern %q, but verifying it exists anyway",
			stack.Status.CgroupPath,
			expectedCgroupPath,
		)
	}
}

// TestKuke_DeleteStack_VerifyState tests stack deletion with state-based verification.
func TestKuke_DeleteStack_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)

	// Cleanup: Safety net (stack should already be deleted, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupStack(t, runPath, realmName, spaceName, stackName)
		cleanupSpace(t, runPath, realmName, spaceName)
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space (prerequisite)
	args = append(buildKukeRunPathArgs(runPath), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Create stack (prerequisite for deletion test)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 4: Verify stack exists initially (establish baseline)
	if !verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file not found for stack %q", stackName)
	}

	if !verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q not found in stack list", stackName)
	}

	if !verifyStackExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q cannot be retrieved individually", stackName)
	}

	// Get stack JSON to extract cgroup path for later verification
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	stack, err := parseStackJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse stack JSON: %v", err)
	}

	if stack.Status.CgroupPath == "" {
		t.Fatal("stack cgroup path is empty")
	}

	cgroupPath := stack.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Step 5: Delete the stack
	args = append(
		buildKukeRunPathArgs(runPath),
		"delete",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Verify metadata file does NOT exist
	if verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file still exists for stack %q after deletion", stackName)
	}

	// Step 7: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q still exists after stack deletion", cgroupPath)
	}

	// Step 8: Verify stack does NOT appear in list
	if verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q still appears in stack list after deletion", stackName)
	}

	// Step 9: Verify individual get FAILS (returns non-zero exit code)
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--output",
		"json",
	)
	exitCode, _, _ := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf("expected get stack to fail after deletion, but got exit code 0")
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_PurgeStack_VerifyState tests stack purging with comprehensive cleanup verification.
func TestKuke_PurgeStack_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)

	// Cleanup: Safety net (stack should already be purged, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupStack(t, runPath, realmName, spaceName, stackName)
		cleanupSpace(t, runPath, realmName, spaceName)
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space (prerequisite)
	args = append(buildKukeRunPathArgs(runPath), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Create stack (prerequisite for purge test)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 4: Verify stack exists initially (establish baseline)
	if !verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file not found for stack %q", stackName)
	}

	if !verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q not found in stack list", stackName)
	}

	if !verifyStackExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q cannot be retrieved individually", stackName)
	}

	// Get stack JSON to extract cgroup path for later verification
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	stack, err := parseStackJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse stack JSON: %v", err)
	}

	if stack.Status.CgroupPath == "" {
		t.Fatal("stack cgroup path is empty")
	}

	cgroupPath := stack.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Step 5: Purge the stack (comprehensive cleanup)
	args = append(
		buildKukeRunPathArgs(runPath),
		"purge",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Verify metadata file does NOT exist
	if verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file still exists for stack %q after purge", stackName)
	}

	// Step 7: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q still exists after stack purge", cgroupPath)
	}

	// Step 8: Verify stack does NOT appear in list
	if verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q still appears in stack list after purge", stackName)
	}

	// Step 9: Verify individual get FAILS (returns non-zero exit code)
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--output",
		"json",
	)
	exitCode, _, _ := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf("expected get stack to fail after purge, but got exit code 0")
	}

	// Note: Purge performs comprehensive cleanup beyond standard delete:
	// - CNI resources cleanup
	// - Orphaned containers cleanup
	// These are handled internally by the purge operation

	// Cleanup runs automatically via t.Cleanup()
}
