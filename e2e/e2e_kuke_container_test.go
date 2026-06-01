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

// cleanupContainer purges a container, handling running, stopped, or non-existent states.
func cleanupContainer(t *testing.T, runPath, realmName, spaceName, stackName, cellName, containerName string) {
	t.Helper()

	args := append(
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
	_, _, _ = runBinary(t, nil, kuke, args...)
}

// TestKuke_NoContainers tests kuke get container when no containers exist.
func TestKuke_NoContainers(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)

	args := append(buildKukeDaemonArgs(host), "get", "container", "--output", "json")
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
	host := startKukeondDaemon(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Delete container first, then cell, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, host, realmName, spaceName, stackName, cellName)
		cleanupStack(t, host, realmName, spaceName, stackName)
		cleanupSpace(t, host, realmName, spaceName)
		cleanupRealm(t, host, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeDaemonArgs(host), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space (prerequisite)
	args = append(buildKukeDaemonArgs(host), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Create stack (prerequisite)
	args = append(
		buildKukeDaemonArgs(host),
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
		buildKukeDaemonArgs(host),
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
		buildKukeDaemonArgs(host),
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
		"registry.eminwux.com/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace
	realmNamespace, err := getRealmNamespace(t, host, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	// Step 7: Get cell ID (needed to build container containerd ID)
	cellID, err := getCellID(t, host, realmName, spaceName, stackName, cellName)
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
	host := startKukeondDaemon(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Safety net (container should already be deleted, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, host, realmName, spaceName, stackName, cellName)
		cleanupStack(t, host, realmName, spaceName, stackName)
		cleanupSpace(t, host, realmName, spaceName)
		cleanupRealm(t, host, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeDaemonArgs(host), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space (prerequisite)
	args = append(buildKukeDaemonArgs(host), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Create stack (prerequisite)
	args = append(
		buildKukeDaemonArgs(host),
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
		buildKukeDaemonArgs(host),
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
		buildKukeDaemonArgs(host),
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
		"registry.eminwux.com/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace
	realmNamespace, err := getRealmNamespace(t, host, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	// Step 7: Get cell ID (needed to build container containerd ID)
	cellID, err := getCellID(t, host, realmName, spaceName, stackName, cellName)
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
		buildKukeDaemonArgs(host),
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

// TestKuke_PurgeContainer_VerifyState tests container purging with comprehensive cleanup verification.
func TestKuke_PurgeContainer_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := generateUniqueContainerName(t)

	// Cleanup: Safety net (container should already be purged, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
		cleanupContainer(t, runPath, realmName, spaceName, stackName, cellName, containerName)
		cleanupCell(t, host, realmName, spaceName, stackName, cellName)
		cleanupStack(t, host, realmName, spaceName, stackName)
		cleanupSpace(t, host, realmName, spaceName)
		cleanupRealm(t, host, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeDaemonArgs(host), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Create space (prerequisite)
	args = append(buildKukeDaemonArgs(host), "create", "space", spaceName, "--realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 3: Create stack (prerequisite)
	args = append(
		buildKukeDaemonArgs(host),
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
		buildKukeDaemonArgs(host),
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
		buildKukeDaemonArgs(host),
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
		"registry.eminwux.com/debian:latest",
		"--command",
		"sleep",
		"--args",
		"infinity",
	)
	runReturningBinary(t, nil, kuke, args...)

	// Step 6: Get realm namespace
	realmNamespace, err := getRealmNamespace(t, host, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	// Step 7: Get cell ID (needed to build container containerd ID)
	cellID, err := getCellID(t, host, realmName, spaceName, stackName, cellName)
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
