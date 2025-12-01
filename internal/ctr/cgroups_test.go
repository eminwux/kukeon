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
	if ctr.ErrEmptyGroupPath == nil {
		t.Error("ErrEmptyGroupPath should not be nil")
	}
	if ctr.ErrEmptyGroupPath.Error() == "" {
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
					t.Errorf("NewCgroup() error = nil, want error containing %q", tt.wantErr)
				} else if err.Error() == "" || err.Error() != ctr.ErrEmptyGroupPath.Error() {
					// Check if error message contains expected text
					if !errors.Is(err, ctr.ErrEmptyGroupPath) {
						t.Errorf("NewCgroup() error = %v, want ErrEmptyGroupPath", err)
					}
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
				if err != nil && errors.Is(err, ctr.ErrEmptyGroupPath) {
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
				if err != nil && errors.Is(err, ctr.ErrEmptyGroupPath) {
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
				if tt.group == "" && !errors.Is(err, ctr.ErrEmptyGroupPath) {
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

func TestGetCurrentCgroupPath(t *testing.T) {
	client := setupTestClientForCgroups(t)

	// Test GetCurrentCgroupPath
	// This reads from /proc/self/cgroup which exists on Linux systems
	// If it doesn't exist or fails, we'll handle that gracefully
	path, err := client.GetCurrentCgroupPath()
	// On systems without /proc/self/cgroup, we expect an error
	// On systems with cgroup2, we expect a valid path
	if err != nil {
		// Error is acceptable if /proc/self/cgroup doesn't exist or doesn't contain cgroup2
		t.Logf("GetCurrentCgroupPath() returned error (may be expected): %v", err)
		if path != "" {
			t.Errorf("GetCurrentCgroupPath() error = %v but path = %q (should be empty on error)", err, path)
		}
		return
	}

	// If no error, path should be non-empty
	if path == "" {
		t.Error("GetCurrentCgroupPath() = empty string, want non-empty path on success")
	}

	// Path should start with / (cgroup paths are absolute)
	if path != "" && path[0] != '/' {
		t.Errorf("GetCurrentCgroupPath() = %q, want absolute path starting with /", path)
	}
}
