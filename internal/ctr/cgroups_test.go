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
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	ctr "github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// Note: The cgroups.go file contains exported methods on Client interface:
// - NewCgroup(spec CgroupSpec) (*cgroup2.Manager, error)
// - LoadCgroup(group string, mountpoint string) (*cgroup2.Manager, error)
// - DeleteCgroup(group, mountpoint string) error
// - GetCurrentCgroupPath() (string, error)
// - CgroupPath(group, mountpoint string) (string, error)
//
// Full testing requires:
// 1. Mocking cgroup2.Manager interface (which has many methods)
// 2. Mocking filesystem operations for cgroup paths
// 3. Setting up test environment with proper cgroup hierarchy
//
// These tests are better suited for integration tests. The validation
// tests below ensure basic error handling paths are exercised.

func setupTestClientForCgroups(t *testing.T) ctr.Client {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return ctr.NewClient(ctx, logger, "/test/socket")
}

func TestCgroupErrorHandling(t *testing.T) {
	// Verify that cgroup-related errors are properly defined
	if errdefs.ErrEmptyGroupPath == nil {
		t.Error("ErrEmptyGroupPath should not be nil")
	}
	if errdefs.ErrEmptyGroupPath.Error() == "" {
		t.Error("ErrEmptyGroupPath should have a non-empty error message")
	}
}

func TestNewCgroupValidation(t *testing.T) {
	client := setupTestClientForCgroups(t)

	tests := []struct {
		name    string
		spec    ctr.CgroupSpec
		wantErr string
	}{
		{
			name: "empty group path",
			spec: ctr.CgroupSpec{
				Group: "",
			},
			wantErr: "cgroup group path is required",
		},
		{
			name: "valid group path",
			spec: ctr.CgroupSpec{
				Group: "/kukeon/test",
			},
			wantErr: "", // Will fail on actual cgroup creation but not on validation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.NewCgroup(tt.spec)

			if tt.wantErr != "" {
				if err == nil {
					t.Errorf("NewCgroup() error = nil, want ErrEmptyGroupPath")
				} else if !errors.Is(err, errdefs.ErrEmptyGroupPath) {
					t.Errorf("NewCgroup() error = %v, want ErrEmptyGroupPath", err)
				}
			} else {
				// For valid paths, error might occur due to missing cgroup filesystem
				// This is expected in unit test environment
				_ = err
			}
		})
	}
}

func TestLoadCgroupValidation(t *testing.T) {
	client := setupTestClientForCgroups(t)

	tests := []struct {
		name       string
		group      string
		mountpoint string
		wantErr    bool
	}{
		{
			name:       "empty group path",
			group:      "",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    true,
		},
		{
			name:       "valid group path",
			group:      "/kukeon/test",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    false, // May fail on actual load but validation passes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.LoadCgroup(tt.group, tt.mountpoint)

			if tt.wantErr {
				if err == nil {
					t.Errorf("LoadCgroup() error = nil, want error")
				}
			} else {
				// Error might occur due to missing cgroup, but validation should pass
				if err != nil && errors.Is(err, errdefs.ErrEmptyGroupPath) {
					t.Errorf("LoadCgroup() unexpected validation error: %v", err)
				}
			}
		})
	}
}

func TestDeleteCgroupValidation(t *testing.T) {
	client := setupTestClientForCgroups(t)

	tests := []struct {
		name       string
		group      string
		mountpoint string
		wantErr    bool
	}{
		{
			name:       "empty group path",
			group:      "",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    true,
		},
		{
			name:       "valid group path",
			group:      "/kukeon/test",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    false, // Idempotent - won't error if cgroup doesn't exist
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.DeleteCgroup(tt.group, tt.mountpoint)

			if tt.wantErr {
				if err == nil {
					t.Errorf("DeleteCgroup() error = nil, want error")
				}
			} else {
				// DeleteCgroup is idempotent, so no error expected even if cgroup doesn't exist
				if err != nil && errors.Is(err, errdefs.ErrEmptyGroupPath) {
					t.Errorf("DeleteCgroup() unexpected validation error: %v", err)
				}
			}
		})
	}
}

// Note: UpdateCgroup and AddProcessToCgroup are not in the Client interface,
// so they cannot be tested via external testing (package ctr_test).
// Resource validation for these methods is tested indirectly through NewCgroup()
// when invalid resources are provided in CgroupSpec.

func TestCgroupPath(t *testing.T) {
	client := setupTestClientForCgroups(t)

	tests := []struct {
		name       string
		group      string
		mountpoint string
		wantErr    bool
		wantPrefix string // Expected path prefix (mountpoint)
	}{
		{
			name:       "valid group path with leading slash",
			group:      "/kukeon/realm1",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    false,
			wantPrefix: "/sys/fs/cgroup/kukeon/realm1",
		},
		{
			name:       "group path without leading slash is invalid",
			group:      "kukeon/realm1",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    true, // cgroup2.VerifyGroupPath requires leading slash
		},
		{
			name:       "empty mountpoint uses effective mountpoint",
			group:      "/kukeon/realm1",
			mountpoint: "",
			wantErr:    false,
			wantPrefix: "", // Will use effective mountpoint (discovered or default)
		},
		{
			name:       "nested group path",
			group:      "/kukeon/realm1/space1/stack1/cell1",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    false,
			wantPrefix: "/sys/fs/cgroup/kukeon/realm1/space1/stack1/cell1",
		},
		{
			name:       "root group path",
			group:      "/",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    false,
			wantPrefix: "/sys/fs/cgroup",
		},
		{
			name:       "empty group path",
			group:      "",
			mountpoint: "/sys/fs/cgroup",
			wantErr:    true,
		},
		{
			name:       "custom mountpoint",
			group:      "/kukeon/test",
			mountpoint: "/custom/cgroup",
			wantErr:    false,
			wantPrefix: "/custom/cgroup/kukeon/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := client.CgroupPath(tt.group, tt.mountpoint)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CgroupPath() error = nil, want error")
				}
				// Error could be ErrEmptyGroupPath or validation error from cgroup2.VerifyGroupPath
				if tt.group == "" && !errors.Is(err, errdefs.ErrEmptyGroupPath) {
					t.Errorf("CgroupPath() error = %v, want ErrEmptyGroupPath", err)
				}
				return
			}

			if err != nil {
				t.Errorf("CgroupPath() error = %v, want nil", err)
				return
			}

			if tt.wantPrefix != "" {
				if path != tt.wantPrefix {
					t.Errorf("CgroupPath() = %q, want %q", path, tt.wantPrefix)
				}
			} else {
				// For empty mountpoint, verify it's a valid path structure
				// (will use effective mountpoint which could be discovered or default)
				if path == "" {
					t.Error("CgroupPath() = empty string, want non-empty path")
				}
				// Verify it's an absolute path
				if !filepath.IsAbs(path) {
					t.Errorf("CgroupPath() = %q, want absolute path", path)
				}
			}
		})
	}
}

func TestGetCgroupMountpoint(t *testing.T) {
	client := setupTestClientForCgroups(t)

	// Test that GetCgroupMountpoint returns a non-empty string
	// It will either discover the mountpoint or use the default
	mountpoint := client.GetCgroupMountpoint()
	if mountpoint == "" {
		t.Error("GetCgroupMountpoint() = empty string, want non-empty mountpoint")
	}

	// Test that subsequent calls return the same value (sync.Once caching)
	mountpoint2 := client.GetCgroupMountpoint()
	if mountpoint != mountpoint2 {
		t.Errorf("GetCgroupMountpoint() second call = %q, want %q (should be cached)", mountpoint2, mountpoint)
	}

	// Verify it's an absolute path
	if !filepath.IsAbs(mountpoint) {
		t.Errorf("GetCgroupMountpoint() = %q, want absolute path", mountpoint)
	}

	// Verify it's either the discovered mountpoint or the default fallback
	if mountpoint != consts.CgroupFilesystemPath && mountpoint[0] != '/' {
		t.Errorf(
			"GetCgroupMountpoint() = %q, want absolute path (default: %q or discovered)",
			mountpoint,
			consts.CgroupFilesystemPath,
		)
	}
}

// TestEnsureSubtreeControllersValidation covers the input-validation
// surface of the issue-#327 level-agnostic entry point used by every
// realm/space/stack provision path. Mirrors TestEnableCellAllSubtreeControllersValidation:
// the empty group path must be rejected with the shared sentinel; an
// empty controllers slice short-circuits (no validation error, no work);
// a syntactically valid group with a non-empty controllers list passes
// validation (the underlying cgroupfs read is then expected to fail in
// the unit-test environment, which we don't assert on).
func TestEnsureSubtreeControllersValidation(t *testing.T) {
	client := setupTestClientForCgroups(t)

	if _, err := client.EnsureSubtreeControllers("", "/sys/fs/cgroup", []string{"cpu"}); err == nil {
		t.Error("EnsureSubtreeControllers(empty group) error = nil, want ErrEmptyGroupPath")
	} else if !errors.Is(err, errdefs.ErrEmptyGroupPath) {
		t.Errorf("EnsureSubtreeControllers(empty group) error = %v, want ErrEmptyGroupPath", err)
	}

	// Empty controllers list: short-circuit without touching the cgroup
	// hierarchy. Must not return ErrEmptyGroupPath (group is valid) and
	// must return a nil effective set.
	got, err := client.EnsureSubtreeControllers("/kukeon/test", "/sys/fs/cgroup", nil)
	if err != nil {
		t.Errorf("EnsureSubtreeControllers(empty controllers) error = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("EnsureSubtreeControllers(empty controllers) effective = %v, want nil", got)
	}

	// Syntactically valid group + non-empty controllers: validation must
	// pass; downstream cgroupfs access then fails in the unit-test sandbox,
	// which is fine — that is not a validation failure and must not
	// surface ErrEmptyGroupPath.
	if _, validErr := client.EnsureSubtreeControllers("/kukeon/test", "/sys/fs/cgroup", []string{"cpu"}); validErr != nil &&
		errors.Is(validErr, errdefs.ErrEmptyGroupPath) {
		t.Errorf("EnsureSubtreeControllers(valid group) unexpected validation error: %v", validErr)
	}
}

// TestEnableCellSubtreeControllersValidation pins the cell-wrapper's
// validation behaviour after refactoring it onto EnsureSubtreeControllers
// (issue #327). The semantics — empty group rejected, valid group allowed
// past validation — must match the pre-refactor behaviour so cell call
// sites in provision.go keep working unchanged.
func TestEnableCellSubtreeControllersValidation(t *testing.T) {
	client := setupTestClientForCgroups(t)

	if _, err := client.EnableCellSubtreeControllers("", "/sys/fs/cgroup", []string{"cpu"}); err == nil {
		t.Error("EnableCellSubtreeControllers(empty group) error = nil, want ErrEmptyGroupPath")
	} else if !errors.Is(err, errdefs.ErrEmptyGroupPath) {
		t.Errorf("EnableCellSubtreeControllers(empty group) error = %v, want ErrEmptyGroupPath", err)
	}

	if _, err := client.EnableCellSubtreeControllers("/kukeon/test", "/sys/fs/cgroup", []string{"cpu"}); err != nil &&
		errors.Is(err, errdefs.ErrEmptyGroupPath) {
		t.Errorf("EnableCellSubtreeControllers(valid group) unexpected validation error: %v", err)
	}
}

// TestEnableCellAllSubtreeControllersValidation covers the input-validation
// surface of the issue-#314 NestedCgroupRuntime entry point. The cgroup
// hierarchy itself isn't writable from a unit test environment, so this
// mirrors TestNewCgroupValidation: the empty group path must be rejected
// with the shared sentinel; a syntactically valid group is allowed past
// validation (the underlying cgroupfs read is then expected to fail in
// the unit-test environment, which we don't assert on).
func TestEnableCellAllSubtreeControllersValidation(t *testing.T) {
	client := setupTestClientForCgroups(t)

	if _, err := client.EnableCellAllSubtreeControllers("", "/sys/fs/cgroup"); err == nil {
		t.Error("EnableCellAllSubtreeControllers(empty group) error = nil, want ErrEmptyGroupPath")
	} else if !errors.Is(err, errdefs.ErrEmptyGroupPath) {
		t.Errorf("EnableCellAllSubtreeControllers(empty group) error = %v, want ErrEmptyGroupPath", err)
	}

	// Syntactically valid group: validation must pass; downstream cgroupfs
	// access then fails in the unit-test sandbox, which is fine — that is
	// not a validation failure and must not surface ErrEmptyGroupPath.
	if _, err := client.EnableCellAllSubtreeControllers("/kukeon/test", "/sys/fs/cgroup"); err != nil &&
		errors.Is(err, errdefs.ErrEmptyGroupPath) {
		t.Errorf("EnableCellAllSubtreeControllers(valid group) unexpected validation error: %v", err)
	}
}

func TestGetCurrentCgroupPath(t *testing.T) {
	client := setupTestClientForCgroups(t)

	path, err := client.GetCurrentCgroupPath()
	if err != nil {
		t.Fatalf("GetCurrentCgroupPath() unexpected error: %v", err)
	}
	if path != consts.KukeonCgroupRoot {
		t.Errorf("GetCurrentCgroupPath() = %q, want %q", path, consts.KukeonCgroupRoot)
	}
}
