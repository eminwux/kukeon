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
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// cleanupContainer deletes a container.
func cleanupContainer(t *testing.T, runPath, realmName, spaceName, stackName, cellName, containerName string) {
	t.Helper()

	args := append(
		buildKukeRunPathArgs(runPath),
		"delete",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
	)
	_, _, _ = runBinary(t, nil, kuke, args...)
}

// TestKuke_NoContainers tests kuke get container when no containers exist.
func TestKuke_NoContainers(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	args := append(buildKukeRunPathArgs(runPath), "get", "container", "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	var containers []v1beta1.ContainerSpec
	if err := json.Unmarshal(output, &containers); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(containers) != 0 {
		t.Fatalf("expected empty container list, got %d containers", len(containers))
	}
}

// TestKuke_CreateContainer_VerifyState tests container creation with state-based verification.
func TestKuke_CreateContainer_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Delete container first, then cell, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, runPath, realmName, spaceName, stackName, cellName)
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

	// Step 3: Create stack (prerequisite)
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

	// Step 4: Create cell (prerequisite)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 5: Create container
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
		"--image",
		"docker.io/library/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	// Step 7: Get cell ID (needed to build container containerd ID)
	cellID, err := getCellID(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("failed to get cell ID: %v", err)
	}
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Step 8: Build container containerd ID: {spaceName}_{stackName}_{cellID}_{containerName}
	containerContainerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, containerName)

	// Step 9: Verify container exists in containerd namespace
	if !verifyContainerExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf("container %q not found in containerd namespace %q", containerContainerdID, realmNamespace)
	}

	// Step 10: Verify container task exists in containerd namespace
	if !verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf("container task %q not found in containerd namespace %q", containerContainerdID, realmNamespace)
	}
}

// TestKuke_DeleteContainer_VerifyState tests container deletion with state-based verification.
func TestKuke_DeleteContainer_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Safety net (container should already be deleted, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, runPath, realmName, spaceName, stackName, cellName)
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

	// Step 3: Create stack (prerequisite)
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

	// Step 4: Create cell (prerequisite)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 5: Create container (prerequisite for deletion test)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
		"--image",
		"docker.io/library/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	// Step 7: Get cell ID (needed to build container containerd ID)
	cellID, err := getCellID(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("failed to get cell ID: %v", err)
	}
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Step 8: Build container containerd ID: {spaceName}_{stackName}_{cellID}_{containerName}
	containerContainerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, containerName)

	// Step 9: Verify container exists initially (establish baseline)
	if !verifyContainerExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf("container %q not found in containerd namespace %q", containerContainerdID, realmNamespace)
	}

	if !verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf("container task %q not found in containerd namespace %q", containerContainerdID, realmNamespace)
	}

	// Step 10: Delete the container
	args = append(
		buildKukeRunPathArgs(runPath),
		"delete",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 11: Verify container does NOT exist in containerd namespace
	if verifyContainerExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container %q still exists in containerd namespace %q after deletion",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Step 12: Verify container task does NOT exist in containerd namespace
	if verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q still exists in containerd namespace %q after deletion",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_StartContainer_VerifyState tests container starting with state-based verification.
func TestKuke_StartContainer_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Delete container, then cell, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, runPath, realmName, spaceName, stackName, cellName)
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

	// Step 3: Create stack (prerequisite)
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

	// Step 4: Create cell (prerequisite)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 5: Create container (container is automatically started after creation)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
		"--image",
		"docker.io/library/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace and cell ID for container verification
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	cellID, err := getCellID(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("failed to get cell ID: %v", err)
	}
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Step 7: Build container containerd ID: {spaceName}_{stackName}_{cellID}_{containerName}
	containerContainerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, containerName)

	// Step 8: Verify container task exists initially (container is auto-started on creation)
	// This establishes baseline that container is running
	if !verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q not found in containerd namespace %q after creation",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Step 9: Stop the container first (to test starting it)
	args = append(
		buildKukeRunPathArgs(runPath),
		"stop",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 10: Verify container task does NOT exist after stopping
	if verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q still exists in containerd namespace %q after stopping",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Step 11: Start the container
	args = append(
		buildKukeRunPathArgs(runPath),
		"start",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 12: Verify container task exists after starting
	if !verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q not found in containerd namespace %q after starting",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_StopContainer_VerifyState tests container stopping with state-based verification.
func TestKuke_StopContainer_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Delete container, then cell, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, runPath, realmName, spaceName, stackName, cellName)
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

	// Step 3: Create stack (prerequisite)
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

	// Step 4: Create cell (prerequisite)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 5: Create container (container is automatically started after creation)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
		"--image",
		"docker.io/library/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace and cell ID for container verification
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	cellID, err := getCellID(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("failed to get cell ID: %v", err)
	}
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Step 7: Build container containerd ID: {spaceName}_{stackName}_{cellID}_{containerName}
	containerContainerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, containerName)

	// Step 8: Verify container task exists initially (container is running after creation)
	// This establishes baseline that container is running
	if !verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q not found in containerd namespace %q after creation",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Step 9: Stop the container
	args = append(
		buildKukeRunPathArgs(runPath),
		"stop",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 10: Verify container task does NOT exist after stopping
	if verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q still exists in containerd namespace %q after stopping",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_KillContainer_VerifyState tests container killing with state-based verification.
func TestKuke_KillContainer_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Delete container, then cell, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, runPath, realmName, spaceName, stackName, cellName)
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

	// Step 3: Create stack (prerequisite)
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

	// Step 4: Create cell (prerequisite)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 5: Create container (container is automatically started after creation)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
		"--image",
		"docker.io/library/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace and cell ID for container verification
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	cellID, err := getCellID(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("failed to get cell ID: %v", err)
	}
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Step 7: Build container containerd ID: {spaceName}_{stackName}_{cellID}_{containerName}
	containerContainerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, containerName)

	// Step 8: Verify container task exists initially (container is running after creation)
	// This establishes baseline that container is running
	if !verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q not found in containerd namespace %q after creation",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Step 9: Kill the container
	args = append(
		buildKukeRunPathArgs(runPath),
		"kill",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 10: Verify container task is STOPPED (kill only sends signal, doesn't delete task)
	// Use verifyRootContainerTaskIsStopped which works for any container task
	if !verifyRootContainerTaskIsStopped(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q is still running in containerd namespace %q after killing",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_PurgeContainer_VerifyState tests container purging with comprehensive cleanup verification.
func TestKuke_PurgeContainer_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Safety net (container should already be purged, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, runPath, realmName, spaceName, stackName, cellName)
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

	// Step 3: Create stack (prerequisite)
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

	// Step 4: Create cell (prerequisite)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 5: Create container (prerequisite for purge test)
	args = append(
		buildKukeRunPathArgs(runPath),
		"create",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
		"--image",
		"docker.io/library/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	// Step 7: Get cell ID (needed to build container containerd ID)
	cellID, err := getCellID(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("failed to get cell ID: %v", err)
	}
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Step 8: Build container containerd ID: {spaceName}_{stackName}_{cellID}_{containerName}
	containerContainerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, containerName)

	// Step 9: Verify container exists initially (establish baseline)
	if !verifyContainerExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf("container %q not found in containerd namespace %q", containerContainerdID, realmNamespace)
	}

	if !verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf("container task %q not found in containerd namespace %q", containerContainerdID, realmNamespace)
	}

	// Step 10: Purge the container (comprehensive cleanup)
	args = append(
		buildKukeRunPathArgs(runPath),
		"purge",
		"container",
		containerName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cell",
		cellName,
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 11: Verify container does NOT exist in containerd namespace
	if verifyContainerExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container %q still exists in containerd namespace %q after purge",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Step 12: Verify container task does NOT exist in containerd namespace
	if verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q still exists in containerd namespace %q after purge",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Note: Purge performs comprehensive cleanup beyond standard delete:
	// - Container and task deleted
	// - CNI resources cleanup
	// - Snapshot cleanup
	// These are handled internally by the purge operation

	// Cleanup runs automatically via t.Cleanup()
}
