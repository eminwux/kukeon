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

func TestKukeApply_Realm(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)

	// Create a temporary YAML file
	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "realm.yaml")
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ` + realmName + `
spec:
  namespace: ` + realmName + `-ns
`

	err := os.WriteFile(yamlFile, []byte(yaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write YAML file: %v", err)
	}

	// Run apply command
	args := append(buildKukeRunPathArgs(runPath), "apply", "-f", yamlFile)
	output := runReturningBinary(t, nil, kuke, args...)

	if len(output) == 0 {
		t.Fatal("expected output from apply command")
	}

	// Verify realm was created
	if !verifyRealmInList(t, runPath, realmName) {
		t.Errorf("realm %q not found in list after apply", realmName)
	}

	// Verify metadata exists
	if !verifyRealmMetadataExists(t, runPath, realmName) {
		t.Errorf("realm metadata file not found for %q", realmName)
	}
}

func TestKukeApply_MultiResource(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := "apply-space-" + strings.TrimPrefix(realmName, "e-r-")

	// Create a temporary YAML file with multiple resources
	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "multi.yaml")
	yaml := `apiVersion: v1beta1
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

	err := os.WriteFile(yamlFile, []byte(yaml), 0o644)
	if err != nil {
		t.Fatalf("failed to write YAML file: %v", err)
	}

	// Run apply command
	args := append(buildKukeRunPathArgs(runPath), "apply", "-f", yamlFile)
	output := runReturningBinary(t, nil, kuke, args...)

	if len(output) == 0 {
		t.Fatal("expected output from apply command")
	}

	// Verify realm was created
	if !verifyRealmInList(t, runPath, realmName) {
		t.Errorf("realm %q not found in list after apply", realmName)
	}

	// Verify space was created
	spaceArgs := append(
		buildKukeRunPathArgs(runPath),
		"get",
		"space",
		spaceName,
		"--realm",
		realmName,
		"--output",
		"json",
	)
	spaceOutput := runReturningBinary(t, nil, kuke, spaceArgs...)

	if len(spaceOutput) == 0 {
		t.Errorf("space %q not found after apply", spaceName)
	}
}
