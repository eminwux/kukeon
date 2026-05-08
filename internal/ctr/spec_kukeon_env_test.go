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

package ctr_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// applyBuiltRootSpec is the BuildRootContainerSpec counterpart of
// applyBuiltSpec — composes the SpecOpts produced by the root-container
// builder into an empty runtime spec so tests can assert on the resulting
// OCI Process.Env without touching containerd.
func applyBuiltRootSpec(t *testing.T, in intmodel.ContainerSpec) *runtimespec.Spec {
	t.Helper()
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux:   &runtimespec.Linux{},
	}
	built := ctr.BuildRootContainerSpec(in, nil)
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}
	return spec
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

func envKeyOccurrences(env []string, key string) int {
	n := 0
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == key {
			n++
		}
	}
	return n
}

// TestBuildContainerSpec_KukeonEnv asserts that BuildContainerSpec emits the
// KUKEON_* identity entries for every populated cell-context field on
// ContainerSpec, with KUKEON_CELL_PROFILE_NAME tracking the runner-stamped
// CellProfileName. Issue #351.
func TestBuildContainerSpec_KukeonEnv(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:              "work",
		Image:           "registry.eminwux.com/busybox:latest",
		CellName:        "kukeon-pr-a4aaab",
		RealmName:       "default",
		SpaceName:       "default",
		StackName:       "default",
		CellCgroupPath:  "/kukeon/default/default/default/kukeon-pr-a4aaab",
		CellProfileName: "kukeon-pr",
	})

	want := map[string]string{
		"KUKEON_CELL_PROFILE_NAME": "kukeon-pr",
		"KUKEON_CELL_NAME":         "kukeon-pr-a4aaab",
		"KUKEON_CONTAINER_ID":      "work",
		"KUKEON_REALM":             "default",
		"KUKEON_SPACE":             "default",
		"KUKEON_STACK":             "default",
		"KUKEON_CGROUP_PATH":       "/kukeon/default/default/default/kukeon-pr-a4aaab",
	}
	got := envMap(spec.Process.Env)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Process.Env[%q] = %q, want %q (full env: %v)", k, got[k], v, spec.Process.Env)
		}
	}
}

// TestBuildContainerSpec_KukeonEnv_SkipsEmptyFields locks in that an empty
// cell-context field produces no corresponding KUKEON_* entry — so test specs
// and edge cases that omit, say, CellProfileName or CellCgroupPath don't
// leak `KUKEON_CELL_PROFILE_NAME=` empty-value entries into the container.
func TestBuildContainerSpec_KukeonEnv_SkipsEmptyFields(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:        "work",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell-1",
		RealmName: "realm-1",
		SpaceName: "space-1",
		StackName: "stack-1",
		// CellProfileName and CellCgroupPath intentionally empty.
	})

	got := envMap(spec.Process.Env)
	if _, ok := got["KUKEON_CELL_PROFILE_NAME"]; ok {
		t.Errorf("KUKEON_CELL_PROFILE_NAME present with empty CellProfileName: %q", got["KUKEON_CELL_PROFILE_NAME"])
	}
	if _, ok := got["KUKEON_CGROUP_PATH"]; ok {
		t.Errorf("KUKEON_CGROUP_PATH present with empty CellCgroupPath: %q", got["KUKEON_CGROUP_PATH"])
	}
	// The other vars must still be present so the partial-population case
	// is useful (a cell created without a profile still surfaces its own
	// realm/space/stack/cell name to the workload).
	for _, k := range []string{"KUKEON_CELL_NAME", "KUKEON_CONTAINER_ID", "KUKEON_REALM", "KUKEON_SPACE", "KUKEON_STACK"} {
		if _, ok := got[k]; !ok {
			t.Errorf("Process.Env missing %q (full env: %v)", k, spec.Process.Env)
		}
	}
}

// TestBuildContainerSpec_KukeonEnv_UserEnvWinsOnCollision asserts the
// override rule the issue calls out: a user-supplied entry in
// containerSpec.Env with the same key as a KUKEON_* default takes precedence
// in the final OCI Process.Env, and no duplicate KUKEON_* entry is left
// behind. Issue #351.
func TestBuildContainerSpec_KukeonEnv_UserEnvWinsOnCollision(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:              "work",
		Image:           "registry.eminwux.com/busybox:latest",
		CellName:        "cell-1",
		RealmName:       "realm-1",
		SpaceName:       "space-1",
		StackName:       "stack-1",
		CellProfileName: "profile-1",
		// User explicitly overrides one of the KUKEON_* defaults plus
		// declares an unrelated AGENT_NAME var.
		Env: []string{
			"KUKEON_CELL_NAME=user-override",
			"AGENT_NAME=kukeon-pr",
		},
	})

	got := envMap(spec.Process.Env)
	if got["KUKEON_CELL_NAME"] != "user-override" {
		t.Errorf("KUKEON_CELL_NAME = %q, want %q (user override should win)", got["KUKEON_CELL_NAME"], "user-override")
	}
	if got["AGENT_NAME"] != "kukeon-pr" {
		t.Errorf("AGENT_NAME = %q, want %q", got["AGENT_NAME"], "kukeon-pr")
	}
	if got["KUKEON_CELL_PROFILE_NAME"] != "profile-1" {
		t.Errorf(
			"KUKEON_CELL_PROFILE_NAME = %q, want %q (untouched default)",
			got["KUKEON_CELL_PROFILE_NAME"],
			"profile-1",
		)
	}
	// No duplicate entries — the merge happens before oci.WithEnv so two
	// entries with the same key never reach replaceOrAppendEnvValues.
	if n := envKeyOccurrences(spec.Process.Env, "KUKEON_CELL_NAME"); n != 1 {
		keys := make([]string, 0, len(spec.Process.Env))
		for _, kv := range spec.Process.Env {
			k, _, _ := strings.Cut(kv, "=")
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Errorf("KUKEON_CELL_NAME appeared %d times, want 1 (keys: %v)", n, keys)
	}
}

// TestBuildContainerSpec_KukeonEnv_NoFieldsNoEntries locks the empty case:
// a ContainerSpec without any cell-context fields and no user env produces
// no Env entries — so legacy unit tests that build minimal specs don't gain
// surprise environment leaks.
func TestBuildContainerSpec_KukeonEnv_NoFieldsNoEntries(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		// Only ID is set. ID alone produces KUKEON_CONTAINER_ID, which is
		// the documented behaviour, but no other vars and no leaked
		// empty-value entries.
		ID:    "c1",
		Image: "registry.eminwux.com/busybox:latest",
	})
	got := envMap(spec.Process.Env)
	if got["KUKEON_CONTAINER_ID"] != "c1" {
		t.Errorf("KUKEON_CONTAINER_ID = %q, want %q", got["KUKEON_CONTAINER_ID"], "c1")
	}
	for _, k := range []string{
		"KUKEON_CELL_PROFILE_NAME",
		"KUKEON_CELL_NAME",
		"KUKEON_REALM",
		"KUKEON_SPACE",
		"KUKEON_STACK",
		"KUKEON_CGROUP_PATH",
	} {
		if _, ok := got[k]; ok {
			t.Errorf("Process.Env carries %q with empty source field: %q", k, got[k])
		}
	}
}

// TestBuildRootContainerSpec_KukeonEnv mirrors the BuildContainerSpec
// coverage on the root-container builder so the kukeond cell and any
// user-supplied root container also receive KUKEON_* identity vars.
// Issue #351.
func TestBuildRootContainerSpec_KukeonEnv(t *testing.T) {
	spec := applyBuiltRootSpec(t, intmodel.ContainerSpec{
		ID:              "root",
		Root:            true,
		Image:           "registry.eminwux.com/busybox:latest",
		CellName:        "kukeon-pr-a4aaab",
		RealmName:       "default",
		SpaceName:       "default",
		StackName:       "default",
		CellCgroupPath:  "/kukeon/default/default/default/kukeon-pr-a4aaab",
		CellProfileName: "kukeon-pr",
	})

	want := map[string]string{
		"KUKEON_CELL_PROFILE_NAME": "kukeon-pr",
		"KUKEON_CELL_NAME":         "kukeon-pr-a4aaab",
		"KUKEON_CONTAINER_ID":      "root",
		"KUKEON_REALM":             "default",
		"KUKEON_SPACE":             "default",
		"KUKEON_STACK":             "default",
		"KUKEON_CGROUP_PATH":       "/kukeon/default/default/default/kukeon-pr-a4aaab",
	}
	got := envMap(spec.Process.Env)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Process.Env[%q] = %q, want %q (full env: %v)", k, got[k], v, spec.Process.Env)
		}
	}
}

// TestBuildRootContainerSpec_KukeonEnv_UserEnvWinsOnCollision mirrors the
// override semantics on the root builder.
func TestBuildRootContainerSpec_KukeonEnv_UserEnvWinsOnCollision(t *testing.T) {
	spec := applyBuiltRootSpec(t, intmodel.ContainerSpec{
		ID:        "root",
		Root:      true,
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell-1",
		RealmName: "realm-1",
		SpaceName: "space-1",
		StackName: "stack-1",
		Env:       []string{"KUKEON_CELL_NAME=root-override"},
	})
	got := envMap(spec.Process.Env)
	if got["KUKEON_CELL_NAME"] != "root-override" {
		t.Errorf("KUKEON_CELL_NAME = %q, want %q", got["KUKEON_CELL_NAME"], "root-override")
	}
	if n := envKeyOccurrences(spec.Process.Env, "KUKEON_CELL_NAME"); n != 1 {
		t.Errorf("KUKEON_CELL_NAME appeared %d times, want 1 (env: %v)", n, spec.Process.Env)
	}
}
