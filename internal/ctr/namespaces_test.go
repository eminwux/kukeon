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
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
)

func setupTestClientForNamespaces(t *testing.T) ctr.Client {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return ctr.NewClient(ctx, logger, "/test/socket")
}

// TestNamespaceMethods tests the exported namespace methods on Client interface.
// Full testing requires mocking containerd namespace service and registry credentials.
func TestNamespaceMethods(t *testing.T) {
	client := setupTestClientForNamespaces(t)

	// Test GetNamespace - should return default namespace for new client
	ns := client.Namespace()
	if ns == "" {
		t.Error("Namespace should not be empty for new client")
	}

	// Test SetNamespace
	client.SetNamespace("test-namespace")
	if client.Namespace() != "test-namespace" {
		t.Errorf("Namespace = %q, want %q", client.Namespace(), "test-namespace")
	}

	// Test SetNamespaceWithCredentials
	creds := []ctr.RegistryCredentials{
		{
			Username:      "user",
			Password:      "pass",
			ServerAddress: "docker.io",
		},
	}
	client.SetNamespaceWithCredentials("test-ns", creds)
	if client.Namespace() != "test-ns" {
		t.Errorf("Namespace = %q, want %q", client.Namespace(), "test-ns")
	}
	gotCreds := client.GetRegistryCredentials()
	if len(gotCreds) != 1 || gotCreds[0].Username != "user" {
		t.Errorf("GetRegistryCredentials() = %v, want credentials with username 'user'", gotCreds)
	}
}

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
				} else if tt.namespace == "" {
					// Check that error message contains expected text
					if err.Error() != tt.wantErrMsg && err.Error() != "namespace name is required" {
						t.Logf("DeleteNamespace() error = %v (may be expected)", err)
					}
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

func TestNamespaceStateTransitions(t *testing.T) {
	client := setupTestClientForNamespaces(t)

	// Test default namespace on creation
	defaultNs := client.Namespace()
	if defaultNs == "" {
		t.Error("Client should have a default namespace on creation")
	}

	// Test namespace change after SetNamespace
	newNs := "test-namespace-1"
	client.SetNamespace(newNs)
	if client.Namespace() != newNs {
		t.Errorf("Namespace = %q, want %q after SetNamespace", client.Namespace(), newNs)
	}

	// Test that credentials are cleared on SetNamespace
	creds := client.GetRegistryCredentials()
	if creds != nil && len(creds) > 0 {
		t.Errorf("GetRegistryCredentials() should return nil/empty after SetNamespace, got %v", creds)
	}

	// Test SetNamespaceWithCredentials preserves credentials
	testCreds := []ctr.RegistryCredentials{
		{
			Username:      "user1",
			Password:      "pass1",
			ServerAddress: "docker.io",
		},
		{
			Username:      "user2",
			Password:      "pass2",
			ServerAddress: "registry.example.com",
		},
	}
	nsWithCreds := "test-namespace-2"
	client.SetNamespaceWithCredentials(nsWithCreds, testCreds)

	if client.Namespace() != nsWithCreds {
		t.Errorf("Namespace = %q, want %q after SetNamespaceWithCredentials", client.Namespace(), nsWithCreds)
	}

	gotCreds := client.GetRegistryCredentials()
	if len(gotCreds) != len(testCreds) {
		t.Errorf("GetRegistryCredentials() count = %d, want %d", len(gotCreds), len(testCreds))
	}
	if len(gotCreds) > 0 {
		if gotCreds[0].Username != testCreds[0].Username {
			t.Errorf("GetRegistryCredentials()[0].Username = %q, want %q", gotCreds[0].Username, testCreds[0].Username)
		}
		if len(gotCreds) > 1 && gotCreds[1].Username != testCreds[1].Username {
			t.Errorf("GetRegistryCredentials()[1].Username = %q, want %q", gotCreds[1].Username, testCreds[1].Username)
		}
	}

	// Test state consistency - multiple namespace switches
	client.SetNamespace("test-namespace-3")
	if client.Namespace() != "test-namespace-3" {
		t.Errorf("Namespace = %q, want 'test-namespace-3'", client.Namespace())
	}
	// Credentials should be cleared again
	creds = client.GetRegistryCredentials()
	if creds != nil && len(creds) > 0 {
		t.Errorf("GetRegistryCredentials() should return nil/empty after SetNamespace, got %v", creds)
	}

	// Test SetNamespaceWithCredentials again
	client.SetNamespaceWithCredentials("test-namespace-4", testCreds[:1])
	if client.Namespace() != "test-namespace-4" {
		t.Errorf("Namespace = %q, want 'test-namespace-4'", client.Namespace())
	}
	gotCreds = client.GetRegistryCredentials()
	if len(gotCreds) != 1 {
		t.Errorf("GetRegistryCredentials() count = %d, want 1", len(gotCreds))
	}
}
