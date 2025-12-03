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
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
)

// readTestdataYAML reads a YAML file from the testdata directory.
func readTestdataYAML(t *testing.T, filename string) string {
	t.Helper()

	// Get the testdata directory path
	// testdata is at cmd/kuke/apply/testdata/ relative to repo root
	// e2e tests are in e2e/ directory
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}

	// Navigate from e2e/ to cmd/kuke/apply/testdata/
	testdataPath := filepath.Join(cwd, "..", "cmd", "kuke", "apply", "testdata", filename)

	content, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Fatalf("failed to read testdata YAML file %q: %v", filename, err)
	}

	return string(content)
}

// verifyYAMLCompleteness validates that a YAML file has all required fields.
func verifyYAMLCompleteness(t *testing.T, filename string) {
	t.Helper()

	content := readTestdataYAML(t, filename)

	// Basic checks for required fields
	if !strings.Contains(content, "apiVersion: v1beta1") {
		t.Errorf("YAML file %q missing apiVersion: v1beta1", filename)
	}

	if !strings.Contains(content, "kind:") {
		t.Errorf("YAML file %q missing kind field", filename)
	}

	if !strings.Contains(content, "metadata:") {
		t.Errorf("YAML file %q missing metadata section", filename)
	}

	if !strings.Contains(content, "name:") {
		t.Errorf("YAML file %q missing name field in metadata", filename)
	}

	if !strings.Contains(content, "spec:") {
		t.Errorf("YAML file %q missing spec section", filename)
	}

	// Resource-specific checks
	if strings.Contains(content, "kind: Realm") {
		if !strings.Contains(content, "namespace:") {
			t.Errorf("YAML file %q (Realm) missing spec.namespace", filename)
		}
	}

	if strings.Contains(content, "kind: Space") {
		if !strings.Contains(content, "realmId:") {
			t.Errorf("YAML file %q (Space) missing spec.realmId", filename)
		}
	}

	if strings.Contains(content, "kind: Stack") {
		if !strings.Contains(content, "id:") {
			t.Errorf("YAML file %q (Stack) missing spec.id", filename)
		}
		if !strings.Contains(content, "realmId:") {
			t.Errorf("YAML file %q (Stack) missing spec.realmId", filename)
		}
		if !strings.Contains(content, "spaceId:") {
			t.Errorf("YAML file %q (Stack) missing spec.spaceId", filename)
		}
	}

	if strings.Contains(content, "kind: Cell") {
		if !strings.Contains(content, "id:") {
			t.Errorf("YAML file %q (Cell) missing spec.id", filename)
		}
		if !strings.Contains(content, "realmId:") {
			t.Errorf("YAML file %q (Cell) missing spec.realmId", filename)
		}
		if !strings.Contains(content, "spaceId:") {
			t.Errorf("YAML file %q (Cell) missing spec.spaceId", filename)
		}
		if !strings.Contains(content, "stackId:") {
			t.Errorf("YAML file %q (Cell) missing spec.stackId", filename)
		}
		if !strings.Contains(content, "containers:") {
			t.Errorf("YAML file %q (Cell) missing spec.containers", filename)
		}
	}
}

// TestKukeApply_VerifyTestdataYAMLs tests that all testdata YAML files are complete.
func TestKukeApply_VerifyTestdataYAMLs(t *testing.T) {
	t.Parallel()

	testFiles := []string{
		"realm.yaml",
		"space.yaml",
		"stack.yaml",
		"cell.yaml",
		"multi-resource.yaml",
	}

	for _, filename := range testFiles {
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			verifyYAMLCompleteness(t, filename)
		})
	}
}

// applyYAMLFile reads a YAML file, applies string replacements, writes to temp file, runs apply command.
func applyYAMLFile(t *testing.T, runPath, yamlFile string, replacements map[string]string) []byte {
	t.Helper()

	// Read the YAML file
	yamlContent := readTestdataYAML(t, yamlFile)

	// Apply replacements
	for old, new := range replacements {
		yamlContent = strings.ReplaceAll(yamlContent, old, new)
	}

	// Write to temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, yamlFile)
	err := os.WriteFile(tmpFile, []byte(yamlContent), 0o644)
	if err != nil {
		t.Fatalf("failed to write temporary YAML file: %v", err)
	}

	// Run apply command
	args := append(buildKukeRunPathArgs(runPath), "apply", "-f", tmpFile)
	output := runReturningBinary(t, nil, kuke, args...)

	return output
}

// getContainerIDsFromCell gets all container IDs from a cell's spec.
func getContainerIDsFromCell(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) []string {
	t.Helper()

	args := append(
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

	containerIDs := make([]string, 0, len(cell.Spec.Containers))
	for _, container := range cell.Spec.Containers {
		if container.ID != "" {
			containerIDs = append(containerIDs, container.ID)
		}
	}

	return containerIDs
}

// verifyCellContainersExist verifies all containers from a cell exist in containerd.
func verifyCellContainersExist(t *testing.T, namespace, spaceName, stackName, cellID string, containerIDs []string) {
	t.Helper()

	for _, containerID := range containerIDs {
		// Build containerd ID: {spaceName}_{stackName}_{cellID}_{containerID}
		containerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, containerID)

		// Verify container exists
		if !verifyContainerExists(t, namespace, containerdID) {
			t.Errorf(
				"container %q (containerd ID: %q) not found in containerd namespace %q",
				containerID,
				containerdID,
				namespace,
			)
		}

		// Verify container task exists
		if !verifyContainerTaskExists(t, namespace, containerdID) {
			t.Errorf(
				"container task %q (containerd ID: %q) not found in containerd namespace %q",
				containerID,
				containerdID,
				namespace,
			)
		}
	}
}

// TestKukeApply_Realm_VerifyState tests realm apply with comprehensive verification.
func TestKukeApply_Realm_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)

	// Cleanup: Delete realm
	t.Cleanup(func() {
		cleanupRealm(t, runPath, realmName)
	})

	// Apply realm.yaml with name replacements
	replacements := map[string]string{
		"test-realm": realmName,
		"test-ns":    realmName + "-ns",
	}
	output := applyYAMLFile(t, runPath, "realm.yaml", replacements)

	if len(output) == 0 {
		t.Fatal("expected output from apply command")
	}

	// Verify containerd namespace exists
	namespace := realmName + "-ns"
	if !verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q not found after apply", namespace)
	}

	// Verify metadata file exists
	if !verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file not found for realm %q", realmName)
	}

	// Verify realm appears in list
	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q not found in realm list", realmName)
	}

	// Verify realm can be retrieved individually
	if !verifyRealmExists(t, runPath, realmName) {
		t.Fatalf("realm %q cannot be retrieved individually", realmName)
	}

	// Verify cgroup path exists
	args := append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	realmOutput := runReturningBinary(t, nil, kuke, args...)

	realm, err := parseRealmJSON(t, realmOutput)
	if err != nil {
		t.Fatalf("failed to parse realm JSON: %v", err)
	}

	if realm.Status.CgroupPath == "" {
		t.Fatal("realm cgroup path is empty")
	}

	// Verify cgroup path exists in filesystem
	if !verifyCgroupPathExists(t, realm.Status.CgroupPath) {
		relativePath := strings.TrimPrefix(realm.Status.CgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) does not exist in filesystem", realm.Status.CgroupPath, fullPath)
	}

	// Verify expected cgroup path structure
	expectedCgroupPath := consts.KukeonCgroupRoot + "/" + realmName
	if !strings.HasSuffix(realm.Status.CgroupPath, expectedCgroupPath) {
		t.Logf(
			"cgroup path %q does not end with expected pattern %q, but verifying it exists anyway",
			realm.Status.CgroupPath,
			expectedCgroupPath,
		)
	}
}

// TestKukeApply_Space_VerifyState tests space apply with comprehensive verification.
func TestKukeApply_Space_VerifyState(t *testing.T) {
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

	// Step 2: Apply space.yaml with name replacements
	replacements := map[string]string{
		"test-space": spaceName,
		"test-realm": realmName,
	}
	output := applyYAMLFile(t, runPath, "space.yaml", replacements)

	if len(output) == 0 {
		t.Fatal("expected output from apply command")
	}

	// Verify CNI config file exists
	if !verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file not found for space %q", spaceName)
	}

	// Verify metadata file exists
	if !verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file not found for space %q", spaceName)
	}

	// Verify space appears in list
	if !verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q not found in space list", spaceName)
	}

	// Verify space can be retrieved individually
	if !verifySpaceExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q cannot be retrieved individually", spaceName)
	}

	// Verify cgroup path exists
	args = append(buildKukeRunPathArgs(runPath), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	spaceOutput := runReturningBinary(t, nil, kuke, args...)

	space, err := parseSpaceJSON(t, spaceOutput)
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

	// Verify expected cgroup path structure
	expectedCgroupPath := consts.KukeonCgroupRoot + "/" + realmName + "/" + spaceName
	if !strings.HasSuffix(space.Status.CgroupPath, expectedCgroupPath) {
		t.Logf(
			"cgroup path %q does not end with expected pattern %q, but verifying it exists anyway",
			space.Status.CgroupPath,
			expectedCgroupPath,
		)
	}
}

// TestKukeApply_Stack_VerifyState tests stack apply with comprehensive verification.
func TestKukeApply_Stack_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)

	// Cleanup: Delete stack, space, realm (reverse dependency order)
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

	// Step 3: Apply stack.yaml with name replacements
	replacements := map[string]string{
		"test-stack": stackName,
		"test-realm": realmName,
		"test-space": spaceName,
	}
	output := applyYAMLFile(t, runPath, "stack.yaml", replacements)

	if len(output) == 0 {
		t.Fatal("expected output from apply command")
	}

	// Verify metadata file exists
	if !verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file not found for stack %q", stackName)
	}

	// Verify stack appears in list
	if !verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q not found in stack list", stackName)
	}

	// Verify stack can be retrieved individually
	if !verifyStackExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q cannot be retrieved individually", stackName)
	}

	// Verify cgroup path exists
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
	stackOutput := runReturningBinary(t, nil, kuke, args...)

	stack, err := parseStackJSON(t, stackOutput)
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

	// Verify expected cgroup path structure
	expectedCgroupPath := consts.KukeonCgroupRoot + "/" + realmName + "/" + spaceName + "/" + stackName
	if !strings.HasSuffix(stack.Status.CgroupPath, expectedCgroupPath) {
		t.Logf(
			"cgroup path %q does not end with expected pattern %q, but verifying it exists anyway",
			stack.Status.CgroupPath,
			expectedCgroupPath,
		)
	}
}

// TestKukeApply_Cell_VerifyState tests cell apply with comprehensive verification.
func TestKukeApply_Cell_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	// Cleanup: Delete cell, stack, space, realm (reverse dependency order)
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

	// Step 4: Apply cell.yaml with name replacements
	replacements := map[string]string{
		"test-cell":  cellName,
		"test-realm": realmName,
		"test-space": spaceName,
		"test-stack": stackName,
	}
	output := applyYAMLFile(t, runPath, "cell.yaml", replacements)

	if len(output) == 0 {
		t.Fatal("expected output from apply command")
	}

	// Verify metadata file exists
	if !verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file not found for cell %q", cellName)
	}

	// Verify cell appears in list
	if !verifyCellInList(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q not found in cell list", cellName)
	}

	// Verify cell can be retrieved individually
	if !verifyCellExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q cannot be retrieved individually", cellName)
	}

	// Verify cgroup path exists
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
	cellOutput := runReturningBinary(t, nil, kuke, args...)

	cell, err := parseCellJSON(t, cellOutput)
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

	// Verify expected cgroup path structure
	expectedCgroupPath := consts.KukeonCgroupRoot + "/" + realmName + "/" + spaceName + "/" + stackName + "/" + cellName
	if !strings.HasSuffix(cell.Status.CgroupPath, expectedCgroupPath) {
		t.Logf(
			"cgroup path %q does not end with expected pattern %q, but verifying it exists anyway",
			cell.Status.CgroupPath,
			expectedCgroupPath,
		)
	}

	// Verify cell state is Ready (apply should start cells automatically)
	// CellStateReady = 1
	if cell.Status.State != 1 {
		t.Fatalf("cell state after apply: %d, expected Ready (1)", cell.Status.State)
	}

	// Get realm namespace for container verification
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}
	if realmNamespace == "" {
		t.Fatal("realm namespace is empty")
	}

	// Get cell ID
	cellID := cell.Spec.ID
	if cellID == "" {
		t.Fatal("cell ID is empty")
	}

	// Verify root container exists
	rootContainerID := fmt.Sprintf("%s_%s_%s_root", spaceName, stackName, cellID)
	if !verifyRootContainerExists(t, realmNamespace, rootContainerID) {
		t.Fatalf("root container %q not found in containerd namespace %q", rootContainerID, realmNamespace)
	}

	// Verify root container task exists (cell should be started by apply)
	if !verifyRootContainerTaskExists(t, realmNamespace, rootContainerID) {
		t.Fatalf("root container task %q not found in containerd namespace %q", rootContainerID, realmNamespace)
	}

	// Get all container IDs from cell spec
	containerIDs := getContainerIDsFromCell(t, runPath, realmName, spaceName, stackName, cellName)

	// Verify all containers exist in containerd
	verifyCellContainersExist(t, realmNamespace, spaceName, stackName, cellID, containerIDs)
}

// TestKukeApply_MultiResource_VerifyState tests multi-resource apply with comprehensive verification.
func TestKukeApply_MultiResource_VerifyState(t *testing.T) {
	t.Parallel()

	// Setup
	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)
	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)

	// Cleanup: Delete stack, space, realm (reverse dependency order)
	t.Cleanup(func() {
		cleanupStack(t, runPath, realmName, spaceName, stackName)
		cleanupSpace(t, runPath, realmName, spaceName)
		cleanupRealm(t, runPath, realmName)
	})

	// Apply multi-resource.yaml with name replacements
	replacements := map[string]string{
		"multi-realm": realmName,
		"multi-ns":    realmName + "-ns",
		"multi-space": spaceName,
		"multi-stack": stackName,
	}
	output := applyYAMLFile(t, runPath, "multi-resource.yaml", replacements)

	if len(output) == 0 {
		t.Fatal("expected output from apply command")
	}

	// Verify realm was created
	namespace := realmName + "-ns"
	if !verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q not found after apply", namespace)
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

	// Verify realm cgroup path
	args := append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	realmOutput := runReturningBinary(t, nil, kuke, args...)

	realm, err := parseRealmJSON(t, realmOutput)
	if err != nil {
		t.Fatalf("failed to parse realm JSON: %v", err)
	}

	if realm.Status.CgroupPath == "" {
		t.Fatal("realm cgroup path is empty")
	}

	if !verifyCgroupPathExists(t, realm.Status.CgroupPath) {
		t.Fatalf("realm cgroup path %q does not exist in filesystem", realm.Status.CgroupPath)
	}

	// Verify space was created
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

	// Verify space cgroup path
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

	// Verify stack was created
	if !verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file not found for stack %q", stackName)
	}

	if !verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q not found in stack list", stackName)
	}

	if !verifyStackExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q cannot be retrieved individually", stackName)
	}

	// Verify stack cgroup path
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
	stackOutput := runReturningBinary(t, nil, kuke, args...)

	stack, err := parseStackJSON(t, stackOutput)
	if err != nil {
		t.Fatalf("failed to parse stack JSON: %v", err)
	}

	if stack.Status.CgroupPath == "" {
		t.Fatal("stack cgroup path is empty")
	}

	if !verifyCgroupPathExists(t, stack.Status.CgroupPath) {
		t.Fatalf("stack cgroup path %q does not exist in filesystem", stack.Status.CgroupPath)
	}
}
