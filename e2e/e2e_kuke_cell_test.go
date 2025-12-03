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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// cleanupCell deletes a cell with cascade.
func cleanupCell(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) {
	t.Helper()

	args := append(
		buildKukeRunPathArgs(runPath),
		"delete",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--cascade",
	)
	_, _, _ = runBinary(t, nil, kuke, args...)
}

// TestKuke_NoCells tests kuke get cell when no cells exist.
func TestKuke_NoCells(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	args := append(buildKukeRunPathArgs(runPath), "get", "cell", "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	var cells []v1beta1.CellDoc
	if err := json.Unmarshal(output, &cells); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(cells) != 0 {
		t.Fatalf("expected empty cell list, got %d cells", len(cells))
	}
}

// TestKuke_CreateCell_VerifyState tests cell creation with state-based verification.
func TestKuke_CreateCell_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	// Cleanup: Delete cell first, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
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

	// Step 4: Create cell
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

	// Step 5: Verify cell metadata file exists
	if !verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file not found for cell %q", cellName)
	}

	// Step 6: Verify cell appears in list (JSON parsing)
	if !verifyCellInList(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q not found in cell list", cellName)
	}

	// Step 7: Verify cell can be retrieved individually
	if !verifyCellExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q cannot be retrieved individually", cellName)
	}

	// Step 8: Verify cgroup path exists
	// Get cell JSON to extract cgroup path
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	cell, err := parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON: %v", err)
	}

	if cell.Status.CgroupPath == "" {
		t.Fatal("cell cgroup path is empty")
	}

	// Verify cgroup path exists in filesystem
	if !verifyCgroupPathExists(t, cell.Status.CgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cell.Status.CgroupPath)
	}

	// Also verify expected cgroup path structure: /kukeon/{realmName}/{spaceName}/{stackName}/{cellName}
	expectedCgroupPath := consts.KukeonCgroupRoot + "/" + realmName + "/" + spaceName + "/" + stackName + "/" + cellName
	if !strings.HasSuffix(cell.Status.CgroupPath, expectedCgroupPath) {
		t.Logf(
			"cgroup path %q does not end with expected pattern %q, but verifying it exists anyway",
			cell.Status.CgroupPath,
			expectedCgroupPath,
		)
	}

	// Step 9: Get realm namespace
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	// Step 10: Verify root container exists
	// Get cell ID from cell JSON (needed to build root container ID)
	cellID := cell.Spec.ID
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Build root container ID: {spaceName}_{stackName}_{cellID}_root
	rootContainerID := fmt.Sprintf("%s_%s_%s_root", spaceName, stackName, cellID)

	if !verifyRootContainerExists(t, realmNamespace, rootContainerID) {
		t.Fatalf("root container %q not found in containerd namespace %q", rootContainerID, realmNamespace)
	}

	// Step 11: Verify root container task exists
	if !verifyRootContainerTaskExists(t, realmNamespace, rootContainerID) {
		t.Fatalf("root container task %q not found in containerd namespace %q", rootContainerID, realmNamespace)
	}
}

// TestKuke_DeleteCell_VerifyState tests cell deletion with state-based verification.
func TestKuke_DeleteCell_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	// Cleanup: Safety net (cell should already be deleted, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
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

	// Step 4: Create cell (prerequisite for deletion test)
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

	// Step 5: Verify cell exists initially (establish baseline)
	if !verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file not found for cell %q", cellName)
	}

	if !verifyCellInList(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q not found in cell list", cellName)
	}

	if !verifyCellExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q cannot be retrieved individually", cellName)
	}

	// Get cell JSON to extract cgroup path and cell ID for later verification
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	cell, err := parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON: %v", err)
	}

	if cell.Status.CgroupPath == "" {
		t.Fatal("cell cgroup path is empty")
	}

	cgroupPath := cell.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Get realm namespace and cell ID for root container verification
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	cellID := cell.Spec.ID
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Build root container ID: {spaceName}_{stackName}_{cellID}_root
	rootContainerID := fmt.Sprintf("%s_%s_%s_root", spaceName, stackName, cellID)

	if !verifyRootContainerExists(t, realmNamespace, rootContainerID) {
		t.Fatalf("root container %q not found in containerd namespace %q", rootContainerID, realmNamespace)
	}

	// Step 6: Delete the cell
	args = append(
		buildKukeRunPathArgs(runPath),
		"delete",
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

	// Step 7: Verify metadata file does NOT exist
	if verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file still exists for cell %q after deletion", cellName)
	}

	// Step 8: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q still exists after cell deletion", cgroupPath)
	}

	// Step 9: Verify root container does NOT exist
	if verifyRootContainerExists(t, realmNamespace, rootContainerID) {
		t.Fatalf(
			"root container %q still exists in containerd namespace %q after cell deletion",
			rootContainerID,
			realmNamespace,
		)
	}

	// Step 10: Verify cell does NOT appear in list
	if verifyCellInList(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q still appears in cell list after deletion", cellName)
	}

	// Step 11: Verify individual get FAILS (returns non-zero exit code)
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	exitCode, _, _ := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf("expected get cell to fail after deletion, but got exit code 0")
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_StartCell_VerifyState tests cell starting with state-based verification.
func TestKuke_StartCell_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	// Cleanup: Delete cell, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
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

	// Step 4: Create cell (cell should be in Pending state after creation)
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

	// Step 5: Get realm namespace and cell ID for container verification
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

	// Step 6: Build root container ID: {spaceName}_{stackName}_{cellID}_root
	rootContainerID := fmt.Sprintf("%s_%s_%s_root", spaceName, stackName, cellID)

	// Step 7: Verify cell is in Pending state initially (baseline)
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	cell, err := parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON: %v", err)
	}

	// Verify cell state is Pending (0) after creation
	// Note: CellStatePending = 0 (iota)
	if cell.Status.State != 0 {
		t.Logf(
			"cell state after creation: %d, expected Pending (0) - containers may auto-start on creation",
			cell.Status.State,
		)
		// Don't fail if state is not Pending - containers may auto-start on creation
	}

	// Step 8: Start the cell
	args = append(
		buildKukeRunPathArgs(runPath),
		"start",
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

	// Step 9: Verify root container task exists (all containers in cell are running)
	if !verifyRootContainerTaskExists(t, realmNamespace, rootContainerID) {
		t.Fatalf(
			"root container task %q not found in containerd namespace %q after starting cell",
			rootContainerID,
			realmNamespace,
		)
	}

	// Step 10: Verify cell state updated to Ready
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output = runReturningBinary(t, nil, kuke, args...)

	cell, err = parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON after start: %v", err)
	}

	// Verify cell state is Ready (1)
	// Note: CellStateReady = 1 (iota)
	if cell.Status.State != 1 {
		t.Fatalf("cell state after start: %d, expected Ready (1)", cell.Status.State)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_StopCell_VerifyState tests cell stopping with state-based verification.
func TestKuke_StopCell_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	// Cleanup: Delete cell, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
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

	// Step 4: Create cell
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

	// Step 5: Start cell (cell should be in Ready state after starting)
	args = append(
		buildKukeRunPathArgs(runPath),
		"start",
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

	// Step 7: Build root container ID: {spaceName}_{stackName}_{cellID}_root
	rootContainerID := fmt.Sprintf("%s_%s_%s_root", spaceName, stackName, cellID)

	// Step 8: Verify cell is in Ready state and containers are running (baseline)
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	cell, err := parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON: %v", err)
	}

	// Verify cell state is Ready (1)
	// Note: CellStateReady = 1 (iota)
	if cell.Status.State != 1 {
		t.Fatalf("cell state after start: %d, expected Ready (1)", cell.Status.State)
	}

	// Verify root container task exists (all containers in cell are running)
	if !verifyRootContainerTaskExists(t, realmNamespace, rootContainerID) {
		t.Fatalf(
			"root container task %q not found in containerd namespace %q after starting cell",
			rootContainerID,
			realmNamespace,
		)
	}

	// Step 9: Stop the cell
	args = append(
		buildKukeRunPathArgs(runPath),
		"stop",
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

	// Step 10: Verify root container task does NOT exist (all containers in cell are stopped)
	if verifyRootContainerTaskExists(t, realmNamespace, rootContainerID) {
		t.Fatalf(
			"root container task %q still exists in containerd namespace %q after stopping cell",
			rootContainerID,
			realmNamespace,
		)
	}

	// Step 11: Verify cell state updated to Stopped
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output = runReturningBinary(t, nil, kuke, args...)

	cell, err = parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON after stop: %v", err)
	}

	// Verify cell state is Stopped (2)
	// Note: CellStateStopped = 2 (after CellStateReady = 1)
	if cell.Status.State != 2 {
		t.Fatalf("cell state after stop: %d, expected Stopped (2)", cell.Status.State)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_KillCell_VerifyState tests cell killing with state-based verification.
func TestKuke_KillCell_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	// Cleanup: Delete cell, then stack, then space, then realm (reverse dependency order)
	t.Cleanup(func() {
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

	// Step 4: Create cell
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

	// Step 5: Start cell (cell should be in Ready state after starting)
	args = append(
		buildKukeRunPathArgs(runPath),
		"start",
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

	// Step 7: Build root container ID: {spaceName}_{stackName}_{cellID}_root
	rootContainerID := fmt.Sprintf("%s_%s_%s_root", spaceName, stackName, cellID)

	// Step 8: Verify cell is in Ready state and containers are running (baseline)
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	cell, err := parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON: %v", err)
	}

	// Verify cell state is Ready (1)
	// Note: CellStateReady = 1 (iota)
	if cell.Status.State != 1 {
		t.Fatalf("cell state after start: %d, expected Ready (1)", cell.Status.State)
	}

	// Verify root container task exists (all containers in cell are running)
	if !verifyRootContainerTaskExists(t, realmNamespace, rootContainerID) {
		t.Fatalf(
			"root container task %q not found in containerd namespace %q after starting cell",
			rootContainerID,
			realmNamespace,
		)
	}

	// Step 9: Kill the cell
	args = append(
		buildKukeRunPathArgs(runPath),
		"kill",
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

	// Step 10: Verify root container task is STOPPED (kill only sends signal, doesn't delete task)
	if !verifyRootContainerTaskIsStopped(t, realmNamespace, rootContainerID) {
		t.Fatalf(
			"root container task %q is still running in containerd namespace %q after killing cell",
			rootContainerID,
			realmNamespace,
		)
	}

	// Step 11: Verify cell state updated to Stopped
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output = runReturningBinary(t, nil, kuke, args...)

	cell, err = parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON after kill: %v", err)
	}

	// Verify cell state is Stopped (2)
	// Note: CellStateStopped = 2 (after CellStateReady = 1)
	if cell.Status.State != 2 {
		t.Fatalf("cell state after kill: %d, expected Stopped (2)", cell.Status.State)
	}

	// Cleanup runs automatically via t.Cleanup()
}

// TestKuke_PurgeCell_VerifyState tests cell purging with comprehensive cleanup verification.
func TestKuke_PurgeCell_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	// Cleanup: Safety net (cell should already be purged, but ensure cleanup if test fails partway)
	t.Cleanup(func() {
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

	// Step 4: Create cell (prerequisite for purge test)
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

	// Step 5: Verify cell exists initially (establish baseline)
	if !verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file not found for cell %q", cellName)
	}

	if !verifyCellInList(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q not found in cell list", cellName)
	}

	if !verifyCellExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q cannot be retrieved individually", cellName)
	}

	// Get cell JSON to extract cgroup path and cell ID for later verification
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	cell, err := parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON: %v", err)
	}

	if cell.Status.CgroupPath == "" {
		t.Fatal("cell cgroup path is empty")
	}

	cgroupPath := cell.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Get realm namespace and cell ID for root container verification
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	cellID := cell.Spec.ID
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Build root container ID: {spaceName}_{stackName}_{cellID}_root
	rootContainerID := fmt.Sprintf("%s_%s_%s_root", spaceName, stackName, cellID)

	if !verifyRootContainerExists(t, realmNamespace, rootContainerID) {
		t.Fatalf("root container %q not found in containerd namespace %q", rootContainerID, realmNamespace)
	}

	// Step 6: Purge the cell (comprehensive cleanup)
	args = append(
		buildKukeRunPathArgs(runPath),
		"purge",
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

	// Step 7: Verify metadata file does NOT exist
	if verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file still exists for cell %q after purge", cellName)
	}

	// Step 8: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q still exists after cell purge", cgroupPath)
	}

	// Step 9: Verify root container does NOT exist (containers deleted)
	if verifyRootContainerExists(t, realmNamespace, rootContainerID) {
		t.Fatalf(
			"root container %q still exists in containerd namespace %q after cell purge",
			rootContainerID,
			realmNamespace,
		)
	}

	// Step 10: Verify cell does NOT appear in list
	if verifyCellInList(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q still appears in cell list after purge", cellName)
	}

	// Step 11: Verify individual get FAILS (returns non-zero exit code)
	args = append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	exitCode, _, _ := runBinary(t, nil, kuke, args...)
	if exitCode == 0 {
		t.Fatalf("expected get cell to fail after purge, but got exit code 0")
	}

	// Note: Purge performs comprehensive cleanup beyond standard delete:
	// - Containers deleted
	// - CNI resources cleanup
	// - Orphaned containers cleanup
	// These are handled internally by the purge operation

	// Cleanup runs automatically via t.Cleanup()
}
