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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	kuke = "kuke"
	ctr  = "ctr"
)

// runReturningBinary runs the provided binary with args, fails the test on non-zero exit or empty output.
// If the binary file does not exist, the test is skipped.
func runReturningBinary(t *testing.T, _ []string, command string, args ...string) []byte {
	t.Helper()

	dir := os.Getenv("E2E_BIN_DIR")
	if dir == "" {
		dir = ".." // or detect repo root
	}
	bin := filepath.Join(dir, command)

	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("binary %s not found, skipping", bin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %s %v failed: %v\noutput:\n%s", bin, args, err, string(out))
	}
	if len(out) == 0 {
		t.Fatalf("no output from %s %v", bin, args)
	}

	return out
}

// runBinary executes binary and returns exit code, stdout, stderr separately.
func runBinary(t *testing.T, env []string, command string, args ...string) (int, []byte, []byte) {
	t.Helper()

	dir := os.Getenv("E2E_BIN_DIR")
	if dir == "" {
		dir = ".."
	}
	bin := filepath.Join(dir, command)

	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("binary %s not found, skipping", bin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	if env != nil {
		cmd.Env = env
	}

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitError := &exec.ExitError{}
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			t.Fatalf("failed to run %s %v: %v", bin, args, err)
		}
	}

	return exitCode, []byte(stdoutBuf.String()), []byte(stderrBuf.String())
}

// getRandomRunPath generates a temporary run path for test isolation.
func getRandomRunPath(t *testing.T) string {
	t.Helper()
	// Use timestamp-based ID for uniqueness
	timestamp := time.Now().UnixNano()
	rndDir := fmt.Sprintf("e-%d", timestamp)
	fullDir := path.Join("tmp", rndDir)
	return fullDir
}

// mkdirRunPath creates the temporary run path directory.
func mkdirRunPath(t *testing.T, fullDir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}
	fullDir = filepath.Join(cwd, fullDir)
	if err = os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatalf("could not create dir %s: %v", fullDir, err)
	}

	// Register cleanup to remove the tmp directory after test
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(fullDir); removeErr != nil {
			t.Logf("failed to cleanup tmp directory %q: %v", fullDir, removeErr)
		}

		// Also remove parent tmp directory if it's empty
		tmpDir := filepath.Dir(fullDir)
		if tmpDir != "" && filepath.Base(tmpDir) == "tmp" {
			entries, readErr := os.ReadDir(tmpDir)
			if readErr == nil && len(entries) == 0 {
				// tmp directory is empty, remove it
				if removeErr := os.Remove(tmpDir); removeErr != nil {
					t.Logf("failed to cleanup parent tmp directory %q: %v", tmpDir, removeErr)
				}
			}
		}
	})
}

// buildKukeRunPathArgs builds --run-path flag arguments.
func buildKukeRunPathArgs(runPath string) []string {
	return []string{"--run-path", runPath}
}

// verifyContainerdNamespace verifies containerd namespace exists by running ctr ns ls.
func verifyContainerdNamespace(t *testing.T, namespace string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping containerd namespace verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "ns", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr ns ls: %v, output: %s", err, string(out))
		return false
	}

	// Check if namespace appears in output
	output := string(out)
	return strings.Contains(output, namespace)
}

// verifyCgroupPathExists verifies cgroup path exists in filesystem.
func verifyCgroupPathExists(t *testing.T, cgroupPath string) bool {
	t.Helper()

	if cgroupPath == "" {
		return false
	}

	// Build full filesystem path by joining mountpoint with group path
	// Pattern: filepath.Join(mountpoint, strings.TrimPrefix(group, "/"))
	// as used in internal/ctr/cgroups.go:129
	mountpoint := consts.CgroupFilesystemPath
	relativePath := strings.TrimPrefix(cgroupPath, "/")
	fullPath := filepath.Join(mountpoint, relativePath)

	// Check if cgroup path exists as a directory
	info, err := os.Stat(fullPath)
	if err != nil {
		t.Logf("cgroup path check failed: fullPath=%q, err=%v", fullPath, err)
		return false
	}

	if !info.IsDir() {
		t.Logf("cgroup path exists but is not a directory: fullPath=%q", fullPath)
		return false
	}

	return true
}

// generateUniqueSpaceName generates a unique space name for tests.
func generateUniqueSpaceName(t *testing.T) string {
	t.Helper()
	timestamp := time.Now().UnixNano()
	hexID := fmt.Sprintf("%02x", timestamp&0xFF) // 2 hex chars from lower 8 bits
	return fmt.Sprintf("e-sp-%s", hexID)
}

// verifySpaceCNIConfigExists verifies CNI config file exists.
func verifySpaceCNIConfigExists(t *testing.T, runPath, realmName, spaceName string) bool {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}
	fullRunPath := filepath.Join(cwd, runPath)

	confPath, err := fs.SpaceNetworkConfigPath(fullRunPath, realmName, spaceName)
	if err != nil {
		t.Logf("failed to build CNI config path: %v", err)
		return false
	}

	_, err = os.Stat(confPath)
	return err == nil
}

// verifySpaceMetadataExists verifies space metadata file exists.
func verifySpaceMetadataExists(t *testing.T, runPath, realmName, spaceName string) bool {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}
	fullRunPath := filepath.Join(cwd, runPath)

	metadataPath := fs.SpaceMetadataPath(fullRunPath, realmName, spaceName)
	_, err = os.Stat(metadataPath)
	return err == nil
}

// parseSpaceListJSON parses kuke get space --output json output.
func parseSpaceListJSON(t *testing.T, output []byte) ([]v1beta1.SpaceDoc, error) {
	t.Helper()

	var spaces []v1beta1.SpaceDoc
	if err := json.Unmarshal(output, &spaces); err != nil {
		return nil, fmt.Errorf("failed to parse space list JSON: %w", err)
	}

	return spaces, nil
}

// parseSpaceJSON parses kuke get space <name> --output json output.
func parseSpaceJSON(t *testing.T, output []byte) (*v1beta1.SpaceDoc, error) {
	t.Helper()

	var space v1beta1.SpaceDoc
	if err := json.Unmarshal(output, &space); err != nil {
		return nil, fmt.Errorf("failed to parse space JSON: %w", err)
	}

	return &space, nil
}

// verifySpaceInList verifies space appears in kuke get space list.
func verifySpaceInList(t *testing.T, runPath, realmName, spaceName string) bool {
	t.Helper()

	args := append(buildKukeRunPathArgs(runPath), "get", "space", "--realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	spaces, err := parseSpaceListJSON(t, output)
	if err != nil {
		t.Logf("failed to parse space list: %v", err)
		return false
	}

	for _, space := range spaces {
		if space.Metadata.Name == spaceName {
			return true
		}
	}

	return false
}

// verifySpaceExists verifies space can be retrieved individually.
func verifySpaceExists(t *testing.T, runPath, realmName, spaceName string) bool {
	t.Helper()

	args := append(buildKukeRunPathArgs(runPath), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	exitCode, stdout, _ := runBinary(t, nil, kuke, args...)

	if exitCode != 0 {
		return false
	}

	space, err := parseSpaceJSON(t, stdout)
	if err != nil {
		t.Logf("failed to parse space JSON: %v", err)
		return false
	}

	return space.Metadata.Name == spaceName
}

// generateUniqueStackName generates a unique stack name for tests.
func generateUniqueStackName(t *testing.T) string {
	t.Helper()
	timestamp := time.Now().UnixNano()
	hexID := fmt.Sprintf("%02x", timestamp&0xFF) // 2 hex chars from lower 8 bits
	return fmt.Sprintf("e-st-%s", hexID)
}

// verifyStackMetadataExists verifies stack metadata file exists.
func verifyStackMetadataExists(t *testing.T, runPath, realmName, spaceName, stackName string) bool {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}
	fullRunPath := filepath.Join(cwd, runPath)

	metadataPath := fs.StackMetadataPath(fullRunPath, realmName, spaceName, stackName)
	_, err = os.Stat(metadataPath)
	return err == nil
}

// parseStackListJSON parses kuke get stack --output json output.
func parseStackListJSON(t *testing.T, output []byte) ([]v1beta1.StackDoc, error) {
	t.Helper()

	var stacks []v1beta1.StackDoc
	if err := json.Unmarshal(output, &stacks); err != nil {
		return nil, fmt.Errorf("failed to parse stack list JSON: %w", err)
	}

	return stacks, nil
}

// parseStackJSON parses kuke get stack <name> --output json output.
func parseStackJSON(t *testing.T, output []byte) (*v1beta1.StackDoc, error) {
	t.Helper()

	var stack v1beta1.StackDoc
	if err := json.Unmarshal(output, &stack); err != nil {
		return nil, fmt.Errorf("failed to parse stack JSON: %w", err)
	}

	return &stack, nil
}

// verifyStackInList verifies stack appears in kuke get stack list.
func verifyStackInList(t *testing.T, runPath, realmName, spaceName, stackName string) bool {
	t.Helper()

	args := append(
		buildKukeRunPathArgs(runPath),
		"get",
		"stack",
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	stacks, err := parseStackListJSON(t, output)
	if err != nil {
		t.Logf("failed to parse stack list: %v", err)
		return false
	}

	for _, stack := range stacks {
		if stack.Metadata.Name == stackName {
			return true
		}
	}

	return false
}

// verifyStackExists verifies stack can be retrieved individually.
func verifyStackExists(t *testing.T, runPath, realmName, spaceName, stackName string) bool {
	t.Helper()

	args := append(
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
	exitCode, stdout, _ := runBinary(t, nil, kuke, args...)

	if exitCode != 0 {
		return false
	}

	stack, err := parseStackJSON(t, stdout)
	if err != nil {
		t.Logf("failed to parse stack JSON: %v", err)
		return false
	}

	return stack.Metadata.Name == stackName
}

// generateUniqueCellName generates a unique cell name for tests.
func generateUniqueCellName(t *testing.T) string {
	t.Helper()
	timestamp := time.Now().UnixNano()
	hexID := fmt.Sprintf("%02x", timestamp&0xFF) // 2 hex chars from lower 8 bits
	return fmt.Sprintf("e-ce-%s", hexID)
}

// generateUniqueContainerName generates a unique container name for tests.
func generateUniqueContainerName(t *testing.T) string {
	t.Helper()
	timestamp := time.Now().UnixNano()
	hexID := fmt.Sprintf("%02x", timestamp&0xFF) // 2 hex chars from lower 8 bits
	return fmt.Sprintf("e-co-%s", hexID)
}

// verifyCellMetadataExists verifies cell metadata file exists.
func verifyCellMetadataExists(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) bool {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not get working dir: %v", err)
	}
	fullRunPath := filepath.Join(cwd, runPath)

	metadataPath := fs.CellMetadataPath(fullRunPath, realmName, spaceName, stackName, cellName)
	_, err = os.Stat(metadataPath)
	return err == nil
}

// parseCellListJSON parses kuke get cell --output json output.
func parseCellListJSON(t *testing.T, output []byte) ([]v1beta1.CellDoc, error) {
	t.Helper()

	var cells []v1beta1.CellDoc
	if err := json.Unmarshal(output, &cells); err != nil {
		return nil, fmt.Errorf("failed to parse cell list JSON: %w", err)
	}

	return cells, nil
}

// parseCellJSON parses kuke get cell <name> --output json output.
func parseCellJSON(t *testing.T, output []byte) (*v1beta1.CellDoc, error) {
	t.Helper()

	var cell v1beta1.CellDoc
	if err := json.Unmarshal(output, &cell); err != nil {
		return nil, fmt.Errorf("failed to parse cell JSON: %w", err)
	}

	return &cell, nil
}

// verifyCellInList verifies cell appears in kuke get cell list.
func verifyCellInList(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) bool {
	t.Helper()

	args := append(
		buildKukeRunPathArgs(runPath),
		"get",
		"cell",
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

	cells, err := parseCellListJSON(t, output)
	if err != nil {
		t.Logf("failed to parse cell list: %v", err)
		return false
	}

	for _, cell := range cells {
		if cell.Metadata.Name == cellName {
			return true
		}
	}

	return false
}

// verifyCellExists verifies cell can be retrieved individually.
func verifyCellExists(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) bool {
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
	exitCode, stdout, _ := runBinary(t, nil, kuke, args...)

	if exitCode != 0 {
		return false
	}

	cell, err := parseCellJSON(t, stdout)
	if err != nil {
		t.Logf("failed to parse cell JSON: %v", err)
		return false
	}

	return cell.Metadata.Name == cellName
}

// getRealmNamespace gets realm namespace from realm JSON.
func getRealmNamespace(t *testing.T, runPath, realmName string) (string, error) {
	t.Helper()

	args := append(buildKukeRunPathArgs(runPath), "get", "realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	realm, err := parseRealmJSON(t, output)
	if err != nil {
		return "", fmt.Errorf("failed to parse realm JSON: %w", err)
	}

	return realm.Spec.Namespace, nil
}

// verifyRootContainerExists verifies root container exists in containerd namespace.
func verifyRootContainerExists(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping root container verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "--namespace", namespace, "container", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr container ls: %v, output: %s", err, string(out))
		return false
	}

	// Check if container ID appears in output
	output := string(out)
	return strings.Contains(output, containerID)
}

// verifyRootContainerTaskExists verifies root container task exists in containerd namespace.
func verifyRootContainerTaskExists(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping root container task verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "--namespace", namespace, "task", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr task ls: %v, output: %s", err, string(out))
		return false
	}

	// Check if container ID appears in output
	output := string(out)
	return strings.Contains(output, containerID)
}

// verifyContainerExists verifies container exists in containerd namespace.
func verifyContainerExists(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping container verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "--namespace", namespace, "container", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr container ls: %v, output: %s", err, string(out))
		return false
	}

	// Check if container ID appears in output
	output := string(out)
	return strings.Contains(output, containerID)
}

// verifyContainerTaskExists verifies container task exists in containerd namespace.
func verifyContainerTaskExists(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping container task verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "--namespace", namespace, "task", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr task ls: %v, output: %s", err, string(out))
		return false
	}

	// Check if container ID appears in output
	output := string(out)
	return strings.Contains(output, containerID)
}

// verifyRootContainerTaskIsStopped verifies root container task is stopped (not running) in containerd namespace.
// It returns true if the task is stopped or doesn't exist, false if the task is still running.
func verifyRootContainerTaskIsStopped(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping root container task status verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use ctr task ls to check task status
	// Tasks that are stopped still appear in the list but with STOPPED status
	cmd := exec.CommandContext(ctx, ctrPath, "--namespace", namespace, "task", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr task ls: %v, output: %s", err, string(out))
		return false
	}

	output := string(out)
	lines := strings.Split(output, "\n")

	// Look for the container ID in the task list
	for _, line := range lines {
		if !strings.Contains(line, containerID) {
			continue
		}

		// Task exists - check if it's running
		// ctr task ls output format: TASK PID STATUS
		// Status can be RUNNING, STOPPED, etc.
		// If line contains RUNNING, task is still running
		if strings.Contains(line, "RUNNING") {
			return false // Task is still running
		}

		// Task exists but is not RUNNING (could be STOPPED, CREATED, etc.)
		// For a kill operation, the task should be STOPPED
		return true // Task is stopped
	}

	// Task not found in list - either doesn't exist or was deleted
	// After kill, task might not exist if it was cleaned up, which is also considered stopped
	return true
}

// getCellID gets cell ID from cell JSON.
func getCellID(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) (string, error) {
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
		return "", fmt.Errorf("failed to parse cell JSON: %w", err)
	}

	if cell.Spec.ID == "" {
		return "", errors.New("cell ID is empty")
	}

	return cell.Spec.ID, nil
}
