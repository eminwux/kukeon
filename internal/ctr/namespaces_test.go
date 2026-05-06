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
	"io"
	"log/slog"
	"strings"
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
)

func setupTestClientForNamespaces(t *testing.T) ctr.Client {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return ctr.NewClient(ctx, logger, "/test/socket")
}

// TestDeleteNamespaceValidation tests DeleteNamespace input validation.
func TestDeleteNamespaceValidation(t *testing.T) {
	client := setupTestClientForNamespaces(t)

	tests := []struct {
		name       string
		namespace  string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:       "empty namespace name",
			namespace:  "",
			wantErr:    true,
			wantErrMsg: "namespace name is required",
		},
		{
			name:      "valid namespace name",
			namespace: "test-namespace",
			wantErr:   false, // Validation passes, actual deletion may fail without containerd
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.DeleteNamespace(tt.namespace)

			if tt.wantErr {
				if err == nil {
					t.Errorf("DeleteNamespace() error = nil, want error containing %q", tt.wantErrMsg)
				} else if err.Error() == "" {
					t.Errorf("DeleteNamespace() error message is empty, want error containing %q", tt.wantErrMsg)
				} else if !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("DeleteNamespace() error = %v, want message containing %q", err, tt.wantErrMsg)
				}
			} else {
				// Validation passes, but actual deletion may fail without containerd connection
				// We just verify it's not a validation error
				if err != nil && err.Error() == "namespace name is required" {
					t.Errorf("DeleteNamespace() unexpected validation error: %v", err)
				}
			}
		})
	}
}

// TestCleanupNamespaceResourcesValidation tests CleanupNamespaceResources input handling.
func TestCleanupNamespaceResourcesValidation(t *testing.T) {
	client := setupTestClientForNamespaces(t)

	// CleanupNamespaceResources doesn't have explicit validation for empty namespace,
	// but we can test that it handles it appropriately
	tests := []struct {
		name        string
		namespace   string
		snapshotter string
		wantErr     bool
	}{
		{
			name:        "empty namespace",
			namespace:   "",
			snapshotter: "overlayfs",
			wantErr:     false, // No validation, will fail on actual cleanup
		},
		{
			name:        "valid namespace",
			namespace:   "test-namespace",
			snapshotter: "overlayfs",
			wantErr:     false, // Validation passes, actual cleanup may fail without containerd
		},
		{
			name:        "empty snapshotter uses default",
			namespace:   "test-namespace",
			snapshotter: "",
			wantErr:     false, // Empty snapshotter defaults to overlayfs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.CleanupNamespaceResources(tt.namespace, tt.snapshotter)
			// CleanupNamespaceResources doesn't validate inputs, so errors are from containerd operations
			// We just verify it doesn't panic
			if err != nil {
				// Errors are expected without containerd connection
				t.Logf("CleanupNamespaceResources() error = %v (expected without containerd)", err)
			}
		})
	}
}
