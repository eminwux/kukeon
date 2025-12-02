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
	"os"
	"path/filepath"
	"strings"
	"testing"
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
