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

func TestKukeDeleteF_Realm(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)

	// First, create a realm using apply
	tmpDir := t.TempDir()
	applyYamlFile := filepath.Join(tmpDir, "realm-apply.yaml")
	applyYaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ` + realmName + `
spec:
  namespace: ` + realmName + `-ns
`

	err := os.WriteFile(applyYamlFile, []byte(applyYaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write YAML file: %v", err)
	}

	// Apply to create the realm
	args := append(buildKukeRunPathArgs(runPath), "apply", "-f", applyYamlFile)
	_ = runReturningBinary(t, nil, kuke, args...)

	// Verify realm was created
	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q not found after apply", realmName)
	}

	// Now delete it using delete -f
	deleteYamlFile := filepath.Join(tmpDir, "realm-delete.yaml")
	deleteYaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ` + realmName + `
spec:
  namespace: ` + realmName + `-ns
`

	err = os.WriteFile(deleteYamlFile, []byte(deleteYaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write delete YAML file: %v", err)
	}

	// Delete the realm
	args = append(buildKukeRunPathArgs(runPath), "delete", "-f", deleteYamlFile)
	output := runReturningBinary(t, nil, kuke, args...)

	if len(output) == 0 {
		t.Fatal("expected output from delete command")
	}

	// Verify realm was deleted
	if verifyRealmInList(t, runPath, realmName) {
		t.Errorf("realm %q still found in list after delete", realmName)
	}

	// Verify metadata is removed
	if verifyRealmMetadataExists(t, runPath, realmName) {
		t.Errorf("realm metadata file still exists for %q", realmName)
	}
}

func TestKukeDeleteF_MultiResource(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := "delete-space-" + strings.TrimPrefix(realmName, "e-r-")

	// First, create resources using apply
	tmpDir := t.TempDir()
	applyYamlFile := filepath.Join(tmpDir, "multi-apply.yaml")
	applyYaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ` + realmName + `
spec:
  namespace: ` + realmName + `-ns
---
apiVersion: v1beta1
kind: Space
metadata:
  name: ` + spaceName + `
spec:
  realmId: ` + realmName + `
`

	err := os.WriteFile(applyYamlFile, []byte(applyYaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write YAML file: %v", err)
	}

	// Apply to create resources
	args := append(buildKukeRunPathArgs(runPath), "apply", "-f", applyYamlFile)
	_ = runReturningBinary(t, nil, kuke, args...)

	// Verify resources were created
	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q not found after apply", realmName)
	}
	if !verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q not found after apply", spaceName)
	}

	// Now delete them using delete -f (in reverse order)
	deleteYamlFile := filepath.Join(tmpDir, "multi-delete.yaml")
	deleteYaml := `apiVersion: v1beta1
kind: Space
metadata:
  name: ` + spaceName + `
spec:
  realmId: ` + realmName + `
---
apiVersion: v1beta1
kind: Realm
metadata:
  name: ` + realmName + `
spec:
  namespace: ` + realmName + `-ns
`

	err = os.WriteFile(deleteYamlFile, []byte(deleteYaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write delete YAML file: %v", err)
	}

	// Delete resources (should delete in reverse dependency order: Space first, then Realm)
	args = append(buildKukeRunPathArgs(runPath), "delete", "-f", deleteYamlFile)
	output := runReturningBinary(t, nil, kuke, args...)

	if len(output) == 0 {
		t.Fatal("expected output from delete command")
	}

	// Verify resources were deleted
	if verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Errorf("space %q still found in list after delete", spaceName)
	}
	if verifyRealmInList(t, runPath, realmName) {
		t.Errorf("realm %q still found in list after delete", realmName)
	}
}

func TestKukeDeleteF_Cascade(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := "delete-cascade-space-" + strings.TrimPrefix(realmName, "e-r-")

	// First, create resources using apply
	tmpDir := t.TempDir()
	applyYamlFile := filepath.Join(tmpDir, "cascade-apply.yaml")
	applyYaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ` + realmName + `
spec:
  namespace: ` + realmName + `-ns
---
apiVersion: v1beta1
kind: Space
metadata:
  name: ` + spaceName + `
spec:
  realmId: ` + realmName + `
`

	err := os.WriteFile(applyYamlFile, []byte(applyYaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write YAML file: %v", err)
	}

	// Apply to create resources
	args := append(buildKukeRunPathArgs(runPath), "apply", "-f", applyYamlFile)
	_ = runReturningBinary(t, nil, kuke, args...)

	// Verify resources were created
	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q not found after apply", realmName)
	}
	if !verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q not found after apply", spaceName)
	}

	// Now delete realm with cascade
	deleteYamlFile := filepath.Join(tmpDir, "cascade-delete.yaml")
	deleteYaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ` + realmName + `
spec:
  namespace: ` + realmName + `-ns
`

	err = os.WriteFile(deleteYamlFile, []byte(deleteYaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write delete YAML file: %v", err)
	}

	// Delete realm with cascade flag
	args = append(buildKukeRunPathArgs(runPath), "delete", "-f", deleteYamlFile, "--cascade")
	output := runReturningBinary(t, nil, kuke, args...)

	if len(output) == 0 {
		t.Fatal("expected output from delete command")
	}

	// Verify both resources were deleted (cascade)
	if verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Errorf("space %q still found in list after cascade delete", spaceName)
	}
	if verifyRealmInList(t, runPath, realmName) {
		t.Errorf("realm %q still found in list after delete", realmName)
	}
}

func TestKukeDeleteF_Idempotent(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)

	// Create a delete YAML file for a non-existent realm
	tmpDir := t.TempDir()
	deleteYamlFile := filepath.Join(tmpDir, "nonexistent-delete.yaml")
	deleteYaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ` + realmName + `
spec:
  namespace: ` + realmName + `-ns
`

	err := os.WriteFile(deleteYamlFile, []byte(deleteYaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write delete YAML file: %v", err)
	}

	// Try to delete non-existent realm (should be idempotent, not an error)
	args := append(buildKukeRunPathArgs(runPath), "delete", "-f", deleteYamlFile)
	output1 := runReturningBinary(t, nil, kuke, args...)

	if len(output1) == 0 {
		t.Fatal("expected output from delete command")
	}

	// Try again (should still be idempotent)
	output2 := runReturningBinary(t, nil, kuke, args...)

	if len(output2) == 0 {
		t.Fatal("expected output from second delete command")
	}

	// Both should report "not found" (idempotent)
	output1Str := string(output1)
	output2Str := string(output2)
	if !strings.Contains(output1Str, "not found") && !strings.Contains(output1Str, "Not found") {
		t.Errorf("expected 'not found' in first delete output, got: %q", output1Str)
	}
	if !strings.Contains(output2Str, "not found") && !strings.Contains(output2Str, "Not found") {
		t.Errorf("expected 'not found' in second delete output, got: %q", output2Str)
	}
}

// TestDeleteF_VerifyTestdataYAMLsComplete tests that all testdata YAML files are complete.
func TestDeleteF_VerifyTestdataYAMLsComplete(t *testing.T) {
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

// writeTempYAMLFile reads a YAML file from testdata, applies string replacements, and writes to a temporary file.
func writeTempYAMLFile(t *testing.T, yamlFile string, replacements map[string]string) string {
	t.Helper()

	// Read the YAML file from testdata
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

	return tmpFile
}

// applyYAMLFileFromTestdata reads a YAML file from testdata, applies replacements, and runs apply command.
func applyYAMLFileFromTestdata(t *testing.T, runPath, yamlFile string, replacements map[string]string) []byte {
	t.Helper()

	tmpFile := writeTempYAMLFile(t, yamlFile, replacements)

	// Run apply command
	args := append(buildKukeRunPathArgs(runPath), "apply", "-f", tmpFile)
	output := runReturningBinary(t, nil, kuke, args...)

	return output
}

// deleteYAMLFileFromTestdata reads a YAML file from testdata, applies replacements, and runs delete command.
func deleteYAMLFileFromTestdata(
	t *testing.T,
	runPath, yamlFile string,
	replacements map[string]string,
	cascade bool,
) []byte {
	t.Helper()

	tmpFile := writeTempYAMLFile(t, yamlFile, replacements)

	// Build delete command arguments
	args := append(buildKukeRunPathArgs(runPath), "delete", "-f", tmpFile)
	if cascade {
		args = append(args, "--cascade")
	}

	// Run delete command
	output := runReturningBinary(t, nil, kuke, args...)

	return output
}

// TestDeleteF_Realm_VerifyAllResourcesDeleted tests realm deletion with comprehensive verification.
func TestDeleteF_Realm_VerifyAllResourcesDeleted(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)
	namespace := realmName + "-ns"

	// Cleanup: Safety net
	t.Cleanup(func() {
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Apply realm.yaml to create the realm
	replacements := map[string]string{
		"test-realm": realmName,
		"test-ns":    namespace,
	}
	_ = applyYAMLFileFromTestdata(t, runPath, "realm.yaml", replacements)

	// Step 2: Verify realm was created
	if !verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q not found after apply", namespace)
	}

	if !verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file not found for realm %q", realmName)
	}

	if !verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q not found in realm list", realmName)
	}

	// Get realm JSON to extract cgroup path
	args := append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
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

	// Step 3: Delete using delete -f
	_ = deleteYAMLFileFromTestdata(t, runPath, "realm.yaml", replacements, false)

	// Step 4: Verify containerd namespace does NOT exist
	if verifyContainerdNamespace(t, namespace) {
		t.Fatalf("containerd namespace %q still exists after deletion", namespace)
	}

	// Step 5: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		relativePath := strings.TrimPrefix(cgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) still exists after realm deletion", cgroupPath, fullPath)
	}

	// Step 6: Verify metadata file does NOT exist
	if verifyRealmMetadataExists(t, runPath, realmName) {
		t.Fatalf("realm metadata file still exists for realm %q after deletion", realmName)
	}

	// Step 7: Verify realm does NOT appear in list
	if verifyRealmInList(t, runPath, realmName) {
		t.Fatalf("realm %q still appears in realm list after deletion", realmName)
	}
}

// TestDeleteF_Space_VerifyAllResourcesDeleted tests space deletion with comprehensive verification.
func TestDeleteF_Space_VerifyAllResourcesDeleted(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)

	// Cleanup: Safety net
	t.Cleanup(func() {
		cleanupSpace(t, runPath, realmName, spaceName)
		cleanupRealm(t, runPath, realmName)
	})

	// Step 1: Create realm (prerequisite)
	args := append(buildKukeRunPathArgs(runPath), "create", "realm", realmName)
	runReturningBinary(t, nil, kuke, args...)

	// Step 2: Apply space.yaml to create the space
	replacements := map[string]string{
		"test-space": spaceName,
		"test-realm": realmName,
	}
	_ = applyYAMLFileFromTestdata(t, runPath, "space.yaml", replacements)

	// Step 3: Verify space was created
	if !verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file not found for space %q", spaceName)
	}

	if !verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file not found for space %q", spaceName)
	}

	if !verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q not found in space list", spaceName)
	}

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

	cgroupPath := space.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Step 4: Delete using delete -f
	_ = deleteYAMLFileFromTestdata(t, runPath, "space.yaml", replacements, false)

	// Step 5: Verify CNI config file does NOT exist (network)
	if verifySpaceCNIConfigExists(t, runPath, realmName, spaceName) {
		t.Fatalf("CNI config file still exists for space %q after deletion", spaceName)
	}

	// Step 6: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		relativePath := strings.TrimPrefix(cgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) still exists after space deletion", cgroupPath, fullPath)
	}

	// Step 7: Verify metadata file does NOT exist
	if verifySpaceMetadataExists(t, runPath, realmName, spaceName) {
		t.Fatalf("space metadata file still exists for space %q after deletion", spaceName)
	}

	// Step 8: Verify space does NOT appear in list
	if verifySpaceInList(t, runPath, realmName, spaceName) {
		t.Fatalf("space %q still appears in space list after deletion", spaceName)
	}
}

// TestDeleteF_Stack_VerifyAllResourcesDeleted tests stack deletion with comprehensive verification.
func TestDeleteF_Stack_VerifyAllResourcesDeleted(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)

	// Cleanup: Safety net
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

	// Step 3: Apply stack.yaml to create the stack
	replacements := map[string]string{
		"test-stack": stackName,
		"test-realm": realmName,
		"test-space": spaceName,
	}
	_ = applyYAMLFileFromTestdata(t, runPath, "stack.yaml", replacements)

	// Step 4: Verify stack was created
	if !verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file not found for stack %q", stackName)
	}

	if !verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q not found in stack list", stackName)
	}

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

	cgroupPath := stack.Status.CgroupPath

	if !verifyCgroupPathExists(t, cgroupPath) {
		t.Fatalf("cgroup path %q does not exist in filesystem", cgroupPath)
	}

	// Step 5: Delete using delete -f
	_ = deleteYAMLFileFromTestdata(t, runPath, "stack.yaml", replacements, false)

	// Step 6: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		relativePath := strings.TrimPrefix(cgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) still exists after stack deletion", cgroupPath, fullPath)
	}

	// Step 7: Verify metadata file does NOT exist
	if verifyStackMetadataExists(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack metadata file still exists for stack %q after deletion", stackName)
	}

	// Step 8: Verify stack does NOT appear in list
	if verifyStackInList(t, runPath, realmName, spaceName, stackName) {
		t.Fatalf("stack %q still appears in stack list after deletion", stackName)
	}
}

// TestDeleteF_Cell_VerifyAllResourcesDeleted tests cell deletion with comprehensive verification.
func TestDeleteF_Cell_VerifyAllResourcesDeleted(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	// Cleanup: Safety net
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

	// Step 4: Apply cell.yaml to create the cell
	replacements := map[string]string{
		"test-cell":  cellName,
		"test-realm": realmName,
		"test-space": spaceName,
		"test-stack": stackName,
	}
	_ = applyYAMLFileFromTestdata(t, runPath, "cell.yaml", replacements)

	// Step 5: Verify cell was created
	if !verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file not found for cell %q", cellName)
	}

	if !verifyCellInList(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell %q not found in cell list", cellName)
	}

	// Get cell JSON to extract cgroup path and cell ID
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

	cellID, err := getCellID(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("failed to get cell ID: %v", err)
	}

	// Build root container ID: {spaceName}_{stackName}_{cellID}_root
	rootContainerID := fmt.Sprintf("%s_%s_%s_root", spaceName, stackName, cellID)

	// Verify root container exists initially
	if !verifyRootContainerExists(t, realmNamespace, rootContainerID) {
		t.Fatalf("root container %q not found in containerd namespace %q", rootContainerID, realmNamespace)
	}

	// Step 6: Delete using delete -f
	_ = deleteYAMLFileFromTestdata(t, runPath, "cell.yaml", replacements, false)

	// Step 7: Verify cgroup path does NOT exist
	if verifyCgroupPathExists(t, cgroupPath) {
		relativePath := strings.TrimPrefix(cgroupPath, "/")
		fullPath := filepath.Join(consts.CgroupFilesystemPath, relativePath)
		t.Fatalf("cgroup path %q (full path: %q) still exists after cell deletion", cgroupPath, fullPath)
	}

	// Step 8: Verify metadata file does NOT exist
	if verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file still exists for cell %q after deletion", cellName)
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
}

// TestDeleteF_Container_VerifyAllResourcesDeleted tests container deletion with comprehensive verification.
func TestDeleteF_Container_VerifyAllResourcesDeleted(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)
	containerName := "worker" // Use a non-root container from cell.yaml

	// Cleanup: Safety net
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

	// Step 4: Apply cell.yaml to create the cell with containers
	replacements := map[string]string{
		"test-cell":  cellName,
		"test-realm": realmName,
		"test-space": spaceName,
		"test-stack": stackName,
	}
	_ = applyYAMLFileFromTestdata(t, runPath, "cell.yaml", replacements)

	// Step 5: Verify cell and container were created
	if !verifyCellMetadataExists(t, runPath, realmName, spaceName, stackName, cellName) {
		t.Fatalf("cell metadata file not found for cell %q", cellName)
	}

	// Get realm namespace and cell ID
	realmNamespace, err := getRealmNamespace(t, runPath, realmName)
	if err != nil {
		t.Fatalf("failed to get realm namespace: %v", err)
	}

	cellID, err := getCellID(t, runPath, realmName, spaceName, stackName, cellName)
	if err != nil {
		t.Fatalf("failed to get cell ID: %v", err)
	}

	// Build container containerd ID: {spaceName}_{stackName}_{cellID}_{containerName}
	containerContainerdID := fmt.Sprintf("%s_%s_%s_%s", spaceName, stackName, cellID, containerName)

	// Verify container exists initially
	if !verifyContainerExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf("container %q not found in containerd namespace %q", containerContainerdID, realmNamespace)
	}

	// Verify container task exists initially
	if !verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf("container task %q not found in containerd namespace %q", containerContainerdID, realmNamespace)
	}

	// Get cell JSON to verify container is in cell metadata
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

	cellBefore, err := parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON: %v", err)
	}

	// Verify container exists in cell spec and get its image
	containerFound := false
	var containerImage string
	for _, container := range cellBefore.Spec.Containers {
		if container.ID == containerName {
			containerFound = true
			containerImage = container.Image
			break
		}
	}
	if !containerFound {
		t.Fatalf("container %q not found in cell %q spec", containerName, cellName)
	}
	if containerImage == "" {
		t.Fatalf("container %q image is empty in cell %q spec", containerName, cellName)
	}

	// Step 6: Create container YAML file for deletion (including image field required by validation)
	containerYAML := fmt.Sprintf(`apiVersion: v1beta1
kind: Container
metadata:
  name: %s
spec:
  realmId: %s
  spaceId: %s
  stackId: %s
  cellId: %s
  image: %s
`, containerName, realmName, spaceName, stackName, cellName, containerImage)

	tmpDir := t.TempDir()
	containerYAMLFile := filepath.Join(tmpDir, "container-delete.yaml")
	err = os.WriteFile(containerYAMLFile, []byte(containerYAML), 0o644)
	if err != nil {
		t.Fatalf("failed to write container YAML file: %v", err)
	}

	// Step 7: Delete using delete -f
	args = append(buildKukeRunPathArgs(runPath), "delete", "-f", containerYAMLFile)
	_ = runReturningBinary(t, nil, kuke, args...)

	// Step 8: Verify container does NOT exist in containerd
	if verifyContainerExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container %q still exists in containerd namespace %q after deletion",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Step 9: Verify container task does NOT exist
	if verifyContainerTaskExists(t, realmNamespace, containerContainerdID) {
		t.Fatalf(
			"container task %q still exists in containerd namespace %q after deletion",
			containerContainerdID,
			realmNamespace,
		)
	}

	// Step 10: Verify container removed from cell metadata
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

	cellAfter, err := parseCellJSON(t, output)
	if err != nil {
		t.Fatalf("failed to parse cell JSON after deletion: %v", err)
	}

	// Verify container is NOT in cell spec anymore
	for _, container := range cellAfter.Spec.Containers {
		if container.ID == containerName {
			t.Fatalf("container %q still found in cell %q spec after deletion", containerName, cellName)
		}
	}
}
