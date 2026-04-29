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
	"strings"
	"testing"
)

// TestKuke_CreateSpace_RejectsInvalidNames covers the AC for #180: invalid
// space names ("_" or "/") must be rejected end-to-end with a non-zero exit
// code and a clear error message that names the offending input. Run via the
// in-process controller (--no-daemon) so the test is self-contained — the
// validator is wired at both controller and runner boundaries, so the same
// rejection fires either way.
func TestKuke_CreateSpace_RejectsInvalidNames(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	tests := []struct {
		name      string
		spaceName string
	}{
		{name: "underscore in space name", spaceName: "bad_space"},
		{name: "slash in space name", spaceName: "bad/space"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := appendNoDaemonRunPath(runPath,
				"create", "space", tt.spaceName, "--realm", "default")
			exitCode, _, stderr := runBinary(t, nil, kuke, args...)
			if exitCode == 0 {
				t.Fatalf("expected non-zero exit rejecting space name %q", tt.spaceName)
			}
			combined := string(stderr)
			if !strings.Contains(combined, "space") || !strings.Contains(combined, tt.spaceName) {
				t.Errorf("expected error mentioning kind=space and name %q, got:\n%s", tt.spaceName, combined)
			}
		})
	}
}

// TestKuke_CreateStack_RejectsInvalidNames covers the AC for #180: invalid
// stack names must be rejected end-to-end.
func TestKuke_CreateStack_RejectsInvalidNames(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	tests := []struct {
		name      string
		stackName string
	}{
		{name: "underscore in stack name", stackName: "bad_stack"},
		{name: "slash in stack name", stackName: "bad/stack"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := appendNoDaemonRunPath(runPath,
				"create", "stack", tt.stackName,
				"--realm", "default", "--space", "default")
			exitCode, _, stderr := runBinary(t, nil, kuke, args...)
			if exitCode == 0 {
				t.Fatalf("expected non-zero exit rejecting stack name %q", tt.stackName)
			}
			combined := string(stderr)
			if !strings.Contains(combined, "stack") || !strings.Contains(combined, tt.stackName) {
				t.Errorf("expected error mentioning kind=stack and name %q, got:\n%s", tt.stackName, combined)
			}
		})
	}
}

// TestKuke_CreateCell_RejectsInvalidNames covers the AC for #180: invalid
// cell names must be rejected end-to-end.
func TestKuke_CreateCell_RejectsInvalidNames(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	tests := []struct {
		name     string
		cellName string
	}{
		{name: "underscore in cell name", cellName: "bad_cell"},
		{name: "slash in cell name", cellName: "bad/cell"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := appendNoDaemonRunPath(runPath,
				"create", "cell", tt.cellName,
				"--realm", "default", "--space", "default", "--stack", "default")
			exitCode, _, stderr := runBinary(t, nil, kuke, args...)
			if exitCode == 0 {
				t.Fatalf("expected non-zero exit rejecting cell name %q", tt.cellName)
			}
			combined := string(stderr)
			if !strings.Contains(combined, "cell") || !strings.Contains(combined, tt.cellName) {
				t.Errorf("expected error mentioning kind=cell and name %q, got:\n%s", tt.cellName, combined)
			}
		})
	}
}

// TestKuke_CreateContainer_RejectsInvalidNames covers the AC for #180:
// invalid container names must be rejected end-to-end.
func TestKuke_CreateContainer_RejectsInvalidNames(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	mkdirRunPath(t, runPath)

	tests := []struct {
		name          string
		containerName string
	}{
		{name: "underscore in container name", containerName: "bad_container"},
		{name: "slash in container name", containerName: "bad/container"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := appendNoDaemonRunPath(runPath,
				"create", "container", tt.containerName,
				"--realm", "default", "--space", "default",
				"--stack", "default", "--cell", "default",
				"--image", "registry.eminwux.com/library/alpine:3.19")
			exitCode, _, stderr := runBinary(t, nil, kuke, args...)
			if exitCode == 0 {
				t.Fatalf("expected non-zero exit rejecting container name %q", tt.containerName)
			}
			combined := string(stderr)
			if !strings.Contains(combined, "container") || !strings.Contains(combined, tt.containerName) {
				t.Errorf("expected error mentioning kind=container and name %q, got:\n%s", tt.containerName, combined)
			}
		})
	}
}
