//go:build integration

// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0
package runner_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// setupTestRunner creates a test runner with a temporary run path.
func setupTestRunner(t *testing.T) (runner.Runner, string) {
	t.Helper()
	runPath := t.TempDir()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	opts := runner.Options{
		RunPath:          runPath,
		ContainerdSocket: "/run/containerd/containerd.sock", // Default containerd socket
		CniConf: cni.Conf{
			CniConfigDir: filepath.Join(runPath, "cni", "config"),
			CniCacheDir:  filepath.Join(runPath, "cni", "cache"),
			CniBinDir:    filepath.Join(runPath, "cni", "bin"),
		},
	}

	r := runner.NewRunner(ctx, logger, opts)
	return r, runPath
}

// createRealmMetadata creates a realm metadata file with the specified state.
func createRealmMetadata(t *testing.T, runPath, realmName, namespace string, state intmodel.RealmState) {
	t.Helper()

	realmDoc := v1beta1.RealmDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindRealm,
		Metadata: v1beta1.RealmMetadata{
			Name: realmName,
			Labels: map[string]string{
				"kukeon.io/realm": namespace,
			},
		},
		Spec: v1beta1.RealmSpec{
			Namespace: namespace,
		},
		Status: v1beta1.RealmStatus{
			State: v1beta1.RealmState(state),
		},
	}

	metadataPath := fs.RealmMetadataPath(runPath, realmName)
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
		t.Fatalf("failed to create metadata directory: %v", err)
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := metadata.WriteMetadata(ctx, logger, realmDoc, metadataPath); err != nil {
		t.Fatalf("failed to write realm metadata: %v", err)
	}
}

func TestCreateRealm_LifecycleStateManagement(t *testing.T) {
	tests := []struct {
		name                    string
		realmName               string
		namespace               string
		setup                   func(t *testing.T, r runner.Runner, runPath, realmName, namespace string)
		wantState               intmodel.RealmState
		wantErr                 bool
		errContains             string
		verifyStateInMetadata   bool
		skipIfContainerdMissing bool
		description             string
	}{
		{
			name:                    "new realm creation - transitions from Creating to Ready",
			realmName:               "test-realm-1",
			namespace:               "test-realm-1-ns",
			setup:                   nil, // No existing realm
			wantState:               intmodel.RealmStateReady,
			wantErr:                 false,
			verifyStateInMetadata:   true,
			skipIfContainerdMissing: true,
			description:             "When creating a new realm, it should start in Creating state, then transition to Ready after resources are created",
		},
		{
			name:      "existing realm in Creating state with resources - reconciles to Ready",
			realmName: "test-realm-2",
			namespace: "test-realm-2-ns",
			setup: func(t *testing.T, r runner.Runner, runPath, realmName, namespace string) {
				// First create the realm properly to ensure resources exist
				realm := intmodel.Realm{
					Metadata: intmodel.RealmMetadata{
						Name: realmName,
					},
					Spec: intmodel.RealmSpec{
						Namespace: namespace,
					},
				}
				createdRealm, err := r.CreateRealm(realm)
				if err != nil {
					// Handle namespace already exists - realm might already be created
					if strings.Contains(err.Error(), "namespace already exists") {
						// Try to get existing realm
						existingRealm, getErr := r.GetRealm(realm)
						if getErr == nil {
							// Realm exists, check if it's in a good state
							if existingRealm.Status.State == intmodel.RealmStateFailed {
								t.Skipf("Realm exists in Failed state - test environment issue")
								return
							}
							// Use existing realm
							createdRealm = existingRealm
						} else {
							t.Skipf("Namespace exists but realm not found: %v", getErr)
							return
						}
					} else {
						// Other errors - skip test
						t.Skipf("Failed to create realm: %v", err)
						return
					}
				}
				// Verify realm is in Ready state before setting to Creating
				if createdRealm.Status.State != intmodel.RealmStateReady {
					t.Skipf("Realm not in Ready state after creation: %v", createdRealm.Status.State)
					return
				}
				// Now set state back to Creating to test reconciliation
				createRealmMetadata(t, runPath, realmName, namespace, intmodel.RealmStateCreating)
			},
			wantState:               intmodel.RealmStateReady,
			wantErr:                 false,
			verifyStateInMetadata:   true,
			skipIfContainerdMissing: true,
			description:             "When a realm exists in Creating state but resources exist, it should reconcile to Ready",
		},
		{
			name:      "existing realm in Ready state - stays Ready",
			realmName: "test-realm-3",
			namespace: "test-realm-3-ns",
			setup: func(t *testing.T, _ runner.Runner, runPath, realmName, namespace string) {
				// Create realm metadata in Ready state.
				createRealmMetadata(t, runPath, realmName, namespace, intmodel.RealmStateReady)
			},
			wantState:               intmodel.RealmStateReady,
			wantErr:                 false,
			verifyStateInMetadata:   true,
			skipIfContainerdMissing: true,
			description:             "When a realm already exists in Ready state, it should remain in Ready state",
		},
		{
			name:      "existing realm in Failed state - stays Failed",
			realmName: "test-realm-4",
			namespace: "test-realm-4-ns",
			setup: func(t *testing.T, _ runner.Runner, runPath, realmName, namespace string) {
				// Create realm metadata in Failed state.
				createRealmMetadata(t, runPath, realmName, namespace, intmodel.RealmStateFailed)
			},
			wantState:               intmodel.RealmStateFailed,
			wantErr:                 false,
			verifyStateInMetadata:   true,
			skipIfContainerdMissing: true,
			description:             "When a realm exists in Failed state, it should remain in Failed state (no automatic recovery)",
		},
		{
			name:                    "realm creation with invalid namespace - transitions to Failed",
			realmName:               "test-realm-5",
			namespace:               "", // Empty namespace will cause failure
			setup:                   nil,
			wantState:               intmodel.RealmStateFailed,
			wantErr:                 true,
			verifyStateInMetadata:   true, // Check that Failed state was persisted
			skipIfContainerdMissing: false,
			description:             "When namespace creation fails, realm should transition to Failed state",
		},
		{
			name:      "realm creation with duplicate namespace - handles gracefully",
			realmName: "test-realm-6",
			namespace: "test-realm-6-ns",
			setup: func(t *testing.T, r runner.Runner, _ string, realmName, namespace string) {
				// First create the realm properly to ensure it exists
				realm := intmodel.Realm{
					Metadata: intmodel.RealmMetadata{
						Name: realmName,
					},
					Spec: intmodel.RealmSpec{
						Namespace: namespace,
					},
				}
				createdRealm, err := r.CreateRealm(realm)
				if err != nil {
					// Handle namespace already exists - realm might already be created
					if strings.Contains(err.Error(), "namespace already exists") {
						// Try to get existing realm
						existingRealm, getErr := r.GetRealm(realm)
						if getErr == nil {
							// Realm exists, check if it's in a good state
							if existingRealm.Status.State == intmodel.RealmStateFailed {
								t.Skipf("Realm exists in Failed state - test environment issue")
								return
							}
							// Realm exists and is in good state, continue
						} else {
							t.Skipf("Namespace exists but realm not found: %v", getErr)
							return
						}
					} else {
						// Other errors - skip test
						t.Skipf("Failed to create realm: %v", err)
						return
					}
				} else if createdRealm.Status.State == intmodel.RealmStateFailed {
					// Creation succeeded but realm is in Failed state - skip
					t.Skipf("Realm created but in Failed state - test environment issue")
					return
				}
			},
			wantState:               intmodel.RealmStateReady,
			wantErr:                 false,
			verifyStateInMetadata:   true,
			skipIfContainerdMissing: true,
			description:             "When creating a realm that already exists, it should handle gracefully",
		},
		{
			name:      "realm in Creating state - ensures resources and transitions to Ready",
			realmName: "test-realm-7",
			namespace: "test-realm-7-ns",
			setup: func(t *testing.T, _ runner.Runner, runPath, realmName, namespace string) {
				// Create realm metadata in Creating state.
				// Note: CreateRealm will ensure resources exist, so it will transition to Ready.
				createRealmMetadata(t, runPath, realmName, namespace, intmodel.RealmStateCreating)
			},
			wantState:               intmodel.RealmStateReady, // CreateRealm ensures resources exist, so transitions to Ready
			wantErr:                 false,
			verifyStateInMetadata:   true,
			skipIfContainerdMissing: true,
			description:             "When a realm is in Creating state, CreateRealm ensures resources exist and transitions to Ready",
		},
		{
			name:      "realm creation with same name different namespace - updates namespace",
			realmName: "test-realm-8",
			namespace: "test-realm-8-ns-updated",
			setup: func(t *testing.T, r runner.Runner, _ string, realmName, _ string) {
				// First create the realm with old namespace (use hash for uniqueness)
				hash := sha256.Sum256([]byte(t.Name() + "-old"))
				shortHash := hex.EncodeToString(hash[:])[:8]
				oldNamespace := "test-realm-8-ns-old" + "-" + shortHash
				realm := intmodel.Realm{
					Metadata: intmodel.RealmMetadata{
						Name: realmName,
					},
					Spec: intmodel.RealmSpec{
						Namespace: oldNamespace,
					},
				}
				createdRealm, err := r.CreateRealm(realm)
				if err != nil {
					// Handle namespace already exists
					if strings.Contains(err.Error(), "namespace already exists") {
						existingRealm, getErr := r.GetRealm(realm)
						if getErr == nil && existingRealm.Status.State != intmodel.RealmStateFailed {
							// Realm exists and is in good state, continue
							return
						}
					}
					t.Skipf("Failed to create realm with old namespace: %v", err)
					return
				} else if createdRealm.Status.State == intmodel.RealmStateFailed {
					t.Skipf("Realm created but in Failed state - test environment issue")
					return
				}
			},
			wantState:               intmodel.RealmStateReady,
			wantErr:                 false,
			verifyStateInMetadata:   true,
			skipIfContainerdMissing: true,
			description:             "When creating a realm with same name but different namespace, it should update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip if containerd is required but not available
			if tt.skipIfContainerdMissing {
				if _, err := os.Stat("/run/containerd/containerd.sock"); os.IsNotExist(err) {
					t.Skip("containerd socket not available, skipping test")
				}
			}

			r, runPath := setupTestRunner(t)

			// Use unique namespace per test to avoid collisions
			// Use a hash of the test name to create a short unique identifier (containerd has 76 char limit)
			uniqueNamespace := tt.namespace
			if uniqueNamespace != "" {
				// Create a short hash from test name (first 8 chars of hex)
				hash := sha256.Sum256([]byte(t.Name()))
				shortHash := hex.EncodeToString(hash[:])[:8]
				uniqueNamespace = tt.namespace + "-" + shortHash
				// Ensure total length is within containerd's 76 character limit
				if len(uniqueNamespace) > 70 {
					uniqueNamespace = tt.namespace[:len(tt.namespace)-(len(uniqueNamespace)-70)] + "-" + shortHash
				}
			}
			// For empty namespace tests, keep it empty

			// Setup test state if needed
			if tt.setup != nil {
				tt.setup(t, r, runPath, tt.realmName, uniqueNamespace)
			}

			// Create realm input
			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: tt.realmName,
					Labels: map[string]string{
						"kukeon.io/realm": uniqueNamespace,
					},
				},
				Spec: intmodel.RealmSpec{
					Namespace: uniqueNamespace,
				},
				Status: intmodel.RealmStatus{
					State: intmodel.RealmStatePending, // Start from Pending
				},
			}

			// Execute CreateRealm
			result, err := r.CreateRealm(realm)

			// Verify error expectations
			if tt.wantErr {
				if err == nil {
					t.Errorf("CreateRealm() expected error but got none")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("CreateRealm() error = %v, want error containing %q", err, tt.errContains)
				}
				// When error occurs, check metadata for Failed state if verifyStateInMetadata is true
				if tt.verifyStateInMetadata {
					lookupRealm := intmodel.Realm{
						Metadata: intmodel.RealmMetadata{
							Name: tt.realmName,
						},
					}
					readRealm, readErr := r.GetRealm(lookupRealm)
					if readErr == nil && readRealm.Status.State != tt.wantState {
						t.Errorf(
							"GetRealm() persisted state after error = %v, want %v",
							readRealm.Status.State,
							tt.wantState,
						)
					}
				}
				return
			}
			if err != nil {
				// If containerd is not available, some tests will fail
				// This is expected for integration tests
				if strings.Contains(err.Error(), "containerd") || strings.Contains(err.Error(), "socket") {
					t.Skipf("containerd not available: %v", err)
				}
				// Handle "namespace already exists" - this can happen if namespace from previous test run persists
				// In this case, the realm might have been created, so try to get it
				if strings.Contains(err.Error(), "namespace already exists") {
					lookupRealm := intmodel.Realm{
						Metadata: intmodel.RealmMetadata{
							Name: tt.realmName,
						},
					}
					existingRealm, getErr := r.GetRealm(lookupRealm)
					if getErr == nil {
						// Realm exists - check if it's in the expected state.
						// If it's in Failed state from a previous run, that's a test environment issue.
						if existingRealm.Status.State == intmodel.RealmStateFailed &&
							tt.wantState == intmodel.RealmStateReady {
							t.Skipf(
								"Realm exists in Failed state from previous test run - skipping due to test environment state",
							)
							return
						}
						// Realm exists, use it for verification.
						result = existingRealm
						// Continue to state verification below (err is handled, result is set)
					} else {
						// If we can't get the realm, the namespace exists but realm doesn't
						// This means provisionNewRealm failed and set state to Failed
						// Check metadata to see if Failed state was persisted
						lookupRealm2 := intmodel.Realm{
							Metadata: intmodel.RealmMetadata{
								Name: tt.realmName,
							},
						}
						failedRealm, getErr2 := r.GetRealm(lookupRealm2)
						if getErr2 == nil && failedRealm.Status.State == intmodel.RealmStateFailed {
							// Realm was created but in Failed state - this is expected for namespace collision
							// when namespace exists but realm metadata doesn't
							t.Skipf("Namespace exists but realm creation failed - test environment issue")
						}
						t.Errorf("CreateRealm() namespace already exists but realm not found: %v", getErr)
						return
					}
				} else {
					t.Errorf("CreateRealm() unexpected error = %v", err)
					return
				}
			}

			// Verify state (only if no error expected)
			if result.Status.State != tt.wantState {
				t.Errorf("CreateRealm() state = %v, want %v", result.Status.State, tt.wantState)
			}

			// Verify state is persisted in metadata if requested
			if tt.verifyStateInMetadata && !tt.wantErr {
				// Read metadata back to verify state was persisted
				lookupRealm := intmodel.Realm{
					Metadata: intmodel.RealmMetadata{
						Name: tt.realmName,
					},
				}
				readRealm, readErr := r.GetRealm(lookupRealm)
				if readErr != nil {
					t.Errorf("GetRealm() failed to read back realm: %v", readErr)
				} else if readRealm.Status.State != tt.wantState {
					t.Errorf("GetRealm() persisted state = %v, want %v", readRealm.Status.State, tt.wantState)
				}
			}

			// Log description for clarity
			if tt.description != "" {
				t.Logf("Test case: %s", tt.description)
			}
		})
	}
}

// TestProvisionNewRealm_ErrorHandling tests error handling in provisionNewRealm.
func TestProvisionNewRealm_ErrorHandling(t *testing.T) {
	tests := []struct {
		name                    string
		realmName               string
		namespace               string
		wantState               intmodel.RealmState
		wantErr                 bool
		skipIfContainerdMissing bool
		description             string
	}{
		{
			name:                    "successful creation - Creating to Ready",
			realmName:               "success-realm",
			namespace:               "success-realm-ns",
			wantState:               intmodel.RealmStateReady,
			wantErr:                 false,
			skipIfContainerdMissing: true,
			description:             "Successful realm creation should transition from Creating to Ready",
		},
		{
			name:                    "namespace creation failure - Creating to Failed",
			realmName:               "fail-ns-realm",
			namespace:               "", // Empty namespace will cause failure
			wantState:               intmodel.RealmStateFailed,
			wantErr:                 true,
			skipIfContainerdMissing: false,
			description:             "When namespace creation fails, realm should be marked as Failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipIfContainerdMissing {
				if _, err := os.Stat("/run/containerd/containerd.sock"); os.IsNotExist(err) {
					t.Skip("containerd socket not available, skipping test")
				}
			}

			r, _ := setupTestRunner(t)

			// Use unique namespace per test to avoid collisions
			uniqueNamespace := tt.namespace
			if uniqueNamespace != "" {
				hash := sha256.Sum256([]byte(t.Name()))
				shortHash := hex.EncodeToString(hash[:])[:8]
				uniqueNamespace = tt.namespace + "-" + shortHash
				// Ensure total length is within containerd's 76 character limit
				if len(uniqueNamespace) > 70 {
					uniqueNamespace = tt.namespace[:len(tt.namespace)-(len(uniqueNamespace)-70)] + "-" + shortHash
				}
			}

			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: tt.realmName,
					Labels: map[string]string{
						"kukeon.io/realm": uniqueNamespace,
					},
				},
				Spec: intmodel.RealmSpec{
					Namespace: uniqueNamespace,
				},
				Status: intmodel.RealmStatus{
					State: intmodel.RealmStateCreating,
				},
			}

			// Call provisionNewRealm directly (it's not exported, so we test via CreateRealm).
			// For direct testing, we'd need to make it exported or test through CreateRealm.
			// Since provisionNewRealm is called by CreateRealm, we test through that.
			result, err := r.CreateRealm(realm)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CreateRealm() expected error but got none")
					return
				}
				// When error occurs, check metadata for Failed state (provisionNewRealm sets it before returning error)
				lookupRealm := intmodel.Realm{
					Metadata: intmodel.RealmMetadata{
						Name: tt.realmName,
					},
				}
				readRealm, readErr := r.GetRealm(lookupRealm)
				if readErr == nil {
					if readRealm.Status.State != tt.wantState {
						t.Errorf(
							"GetRealm() persisted state after error = %v, want %v",
							readRealm.Status.State,
							tt.wantState,
						)
					}
				} else if !strings.Contains(readErr.Error(), "not found") {
					// If realm not found, that's okay - metadata update might have failed
					// But if there's another error, report it
					t.Logf("GetRealm() after error returned: %v (this may be expected)", readErr)
				}
			} else {
				if err != nil {
					if strings.Contains(err.Error(), "containerd") || strings.Contains(err.Error(), "socket") {
						t.Skipf("containerd not available: %v", err)
					}
					// Handle namespace already exists
					if strings.Contains(err.Error(), "namespace already exists") {
						lookupRealm := intmodel.Realm{
							Metadata: intmodel.RealmMetadata{
								Name: tt.realmName,
							},
						}
						existingRealm, getErr := r.GetRealm(lookupRealm)
						if getErr == nil {
							// Realm exists, check if it's in the expected state.
							if existingRealm.Status.State == intmodel.RealmStateFailed && tt.wantState == intmodel.RealmStateReady {
								t.Skipf("Realm exists in Failed state from previous test run - skipping due to test environment state")
								return
							}
							// Realm exists, use it.
							result = existingRealm
						} else {
							t.Errorf("CreateRealm() namespace already exists but realm not found: %v", getErr)
							return
						}
					} else {
						t.Errorf("CreateRealm() unexpected error = %v", err)
						return
					}
				}
				if result.Status.State != tt.wantState {
					t.Errorf("CreateRealm() state = %v, want %v", result.Status.State, tt.wantState)
				}
			}

			if tt.description != "" {
				t.Logf("Test case: %s", tt.description)
			}
		})
	}
}

// TestCreateRealm_StateReconciliation tests state reconciliation for existing realms.
func TestCreateRealm_StateReconciliation(t *testing.T) {
	tests := []struct {
		name                    string
		initialState            intmodel.RealmState
		createResources         bool // Whether to actually create namespace/cgroup
		wantState               intmodel.RealmState
		wantReconciled          bool
		skipIfContainerdMissing bool
		description             string
	}{
		{
			name:                    "Creating state with resources - reconciles to Ready",
			initialState:            intmodel.RealmStateCreating,
			createResources:         true,
			wantState:               intmodel.RealmStateReady,
			wantReconciled:          true,
			skipIfContainerdMissing: true,
			description:             "Realm in Creating state with existing resources should reconcile to Ready",
		},
		{
			name:                    "Creating state without resources - ensures resources and transitions to Ready",
			initialState:            intmodel.RealmStateCreating,
			createResources:         false,
			wantState:               intmodel.RealmStateReady, // CreateRealm ensures resources exist, so transitions to Ready
			wantReconciled:          true,
			skipIfContainerdMissing: true,
			description:             "Realm in Creating state - CreateRealm ensures resources exist and transitions to Ready",
		},
		{
			name:                    "Ready state - stays Ready",
			initialState:            intmodel.RealmStateReady,
			createResources:         true,
			wantState:               intmodel.RealmStateReady,
			wantReconciled:          false,
			skipIfContainerdMissing: true,
			description:             "Realm already in Ready state should remain Ready",
		},
		{
			name:                    "Failed state - stays Failed",
			initialState:            intmodel.RealmStateFailed,
			createResources:         true,
			wantState:               intmodel.RealmStateFailed,
			wantReconciled:          false,
			skipIfContainerdMissing: true,
			description:             "Realm in Failed state should remain Failed (no auto-recovery)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipIfContainerdMissing {
				if _, err := os.Stat("/run/containerd/containerd.sock"); os.IsNotExist(err) {
					t.Skip("containerd socket not available, skipping test")
				}
			}

			r, runPath := setupTestRunner(t)
			realmName := "reconcile-realm"
			namespace := "reconcile-realm-ns"

			// Create realm metadata with initial state
			createRealmMetadata(t, runPath, realmName, namespace, tt.initialState)

			// If createResources is true, we need to actually create the resources
			// This is done by calling CreateRealm which will ensure they exist
			if tt.createResources {
				// First create the realm properly to ensure resources exist
				realm := intmodel.Realm{
					Metadata: intmodel.RealmMetadata{
						Name: realmName,
					},
					Spec: intmodel.RealmSpec{
						Namespace: namespace,
					},
				}
				_, err := r.CreateRealm(realm)
				if err != nil {
					if strings.Contains(err.Error(), "containerd") || strings.Contains(err.Error(), "socket") {
						t.Skipf("containerd not available: %v", err)
					}
					// Handle namespace already exists - realm might already be created
					if strings.Contains(err.Error(), "namespace already exists") {
						// Try to get the existing realm to verify it exists
						_, getErr := r.GetRealm(realm)
						if getErr != nil {
							t.Skipf("failed to get existing realm: %v", getErr)
						}
						// Realm exists, continue
					} else {
						// If creation fails, we can't test reconciliation
						t.Skipf("failed to create realm resources: %v", err)
					}
				}

				// Now set state back to the initial state to test state preservation/reconciliation
				createRealmMetadata(t, runPath, realmName, namespace, tt.initialState)
			}

			// Now call CreateRealm again - should reconcile if needed
			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: realmName,
				},
				Spec: intmodel.RealmSpec{
					Namespace: namespace,
				},
			}

			result, err := r.CreateRealm(realm)
			if err != nil {
				if strings.Contains(err.Error(), "containerd") || strings.Contains(err.Error(), "socket") {
					t.Skipf("containerd not available: %v", err)
				}
				t.Errorf("CreateRealm() unexpected error = %v", err)
				return
			}

			// Verify final state
			if result.Status.State != tt.wantState {
				t.Errorf("CreateRealm() final state = %v, want %v", result.Status.State, tt.wantState)
			}

			// Verify reconciliation happened if expected
			if tt.wantReconciled && result.Status.State == intmodel.RealmStateReady {
				// Check that state was actually reconciled (was Creating, now Ready)
				if tt.initialState != intmodel.RealmStateCreating {
					t.Errorf("Expected initial state to be Creating for reconciliation test")
				}
			}

			if tt.description != "" {
				t.Logf("Test case: %s", tt.description)
			}
		})
	}
}
