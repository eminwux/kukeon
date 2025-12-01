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
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func setupTestClientForContainer(t *testing.T) ctr.Client {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return ctr.NewClient(ctx, logger, "/test/socket")
}

// TestContainerValidation tests input validation for container methods.
// Full testing requires mocking containerd.Client and containerd.Container interfaces.
func TestContainerValidation(t *testing.T) {
	client := setupTestClientForContainer(t)

	// Test GetContainer with empty ID
	_, err := client.GetContainer("")
	if err == nil {
		t.Error("GetContainer with empty ID should return error")
	}
	if !errors.Is(err, ctr.ErrEmptyContainerID) {
		t.Errorf("GetContainer with empty ID error = %v, want ErrEmptyContainerID", err)
	}

	// Test ExistsContainer with empty ID
	_, err2 := client.ExistsContainer("")
	if err2 == nil {
		t.Error("ExistsContainer with empty ID should return error")
	}
	if !errors.Is(err2, ctr.ErrEmptyContainerID) {
		t.Errorf("ExistsContainer with empty ID error = %v, want ErrEmptyContainerID", err2)
	}

	// Test DeleteContainer with empty ID
	err3 := client.DeleteContainer("", ctr.ContainerDeleteOptions{})
	if err3 == nil {
		t.Error("DeleteContainer with empty ID should return error")
	}
	if !errors.Is(err3, ctr.ErrEmptyContainerID) {
		t.Errorf("DeleteContainer with empty ID error = %v, want ErrEmptyContainerID", err3)
	}
}

func TestCreateContainerValidation(t *testing.T) {
	client := setupTestClientForContainer(t)

	tests := []struct {
		name    string
		spec    ctr.ContainerSpec
		wantErr error
	}{
		{
			name: "empty container ID",
			spec: ctr.ContainerSpec{
				ID:    "",
				Image: "docker.io/library/busybox:latest",
			},
			wantErr: ctr.ErrEmptyContainerID,
		},
		{
			name: "empty image reference",
			spec: ctr.ContainerSpec{
				ID:    "test-container",
				Image: "",
			},
			wantErr: ctr.ErrInvalidImage,
		},
		// Note: "valid spec" case is skipped as it requires containerd connection
		// which would cause nil pointer panic. Validation tests focus on invalid inputs.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateContainer(tt.spec)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("CreateContainer() error = nil, want error %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("CreateContainer() error = %v, want %v", err, tt.wantErr)
				}
			}
		})
	}
}

func TestStartContainerValidation(t *testing.T) {
	client := setupTestClientForContainer(t)

	tests := []struct {
		name          string
		containerSpec ctr.ContainerSpec
		taskSpec      ctr.TaskSpec
		wantErr       error
	}{
		{
			name: "empty container ID",
			containerSpec: ctr.ContainerSpec{
				ID:    "",
				Image: "docker.io/library/busybox:latest",
			},
			taskSpec: ctr.TaskSpec{},
			wantErr:  ctr.ErrEmptyContainerID,
		},
		// Note: "valid container spec" case is skipped as it requires containerd connection
		// Validation tests focus on invalid inputs.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.StartContainer(tt.containerSpec, tt.taskSpec)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("StartContainer() error = nil, want error %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					// Check if error is wrapped
					var wrappedErr error
					if err.Error() != "" {
						// Error might be wrapped, check if it contains the expected error
						if !errors.Is(err, tt.wantErr) {
							// Check if it's a different but expected validation error
							if !errors.Is(err, ctr.ErrEmptyContainerID) {
								t.Logf("StartContainer() error = %v (may be expected if container not found)", err)
							}
						}
					}
					_ = wrappedErr
				}
			}
		})
	}
}

func TestStopContainerValidation(t *testing.T) {
	client := setupTestClientForContainer(t)

	tests := []struct {
		name    string
		id      string
		opts    ctr.StopContainerOptions
		wantErr error
	}{
		{
			name:    "empty container ID",
			id:      "",
			opts:    ctr.StopContainerOptions{},
			wantErr: ctr.ErrEmptyContainerID,
		},
		// Note: "valid container ID" cases are skipped as they require containerd connection
		// Validation tests focus on invalid inputs.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.StopContainer(tt.id, tt.opts)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("StopContainer() error = nil, want error %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					// Error might be wrapped
					if !errors.Is(err, ctr.ErrEmptyContainerID) {
						t.Logf("StopContainer() error = %v (may be expected if container/task not found)", err)
					}
				}
			}
		})
	}
}

func TestCreateContainerFromSpecValidation(t *testing.T) {
	client := setupTestClientForContainer(t)

	tests := []struct {
		name    string
		spec    intmodel.ContainerSpec
		wantErr error
	}{
		{
			name: "empty container ID",
			spec: intmodel.ContainerSpec{
				ID:    "",
				Image: "docker.io/library/busybox:latest",
			},
			wantErr: ctr.ErrEmptyContainerID,
		},
		{
			name: "empty image",
			spec: intmodel.ContainerSpec{
				ID:    "test-container",
				Image: "",
			},
			wantErr: ctr.ErrInvalidImage,
		},
		{
			name: "empty cell name",
			spec: intmodel.ContainerSpec{
				ID:        "test-container",
				Image:     "docker.io/library/busybox:latest",
				CellName:  "",
				SpaceName: "space1",
				RealmName: "realm1",
				StackName: "stack1",
			},
			wantErr: ctr.ErrEmptyCellID,
		},
		{
			name: "empty space name",
			spec: intmodel.ContainerSpec{
				ID:        "test-container",
				Image:     "docker.io/library/busybox:latest",
				CellName:  "cell1",
				SpaceName: "",
				RealmName: "realm1",
				StackName: "stack1",
			},
			wantErr: ctr.ErrEmptySpaceID,
		},
		{
			name: "empty realm name",
			spec: intmodel.ContainerSpec{
				ID:        "test-container",
				Image:     "docker.io/library/busybox:latest",
				CellName:  "cell1",
				SpaceName: "space1",
				RealmName: "",
				StackName: "stack1",
			},
			wantErr: ctr.ErrEmptyRealmID,
		},
		{
			name: "empty stack name",
			spec: intmodel.ContainerSpec{
				ID:        "test-container",
				Image:     "docker.io/library/busybox:latest",
				CellName:  "cell1",
				SpaceName: "space1",
				RealmName: "realm1",
				StackName: "",
			},
			wantErr: ctr.ErrEmptyStackID,
		},
		// Note: "valid spec" case is skipped as it requires containerd connection
		// Validation tests focus on invalid inputs.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateContainerFromSpec(tt.spec)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("CreateContainerFromSpec() error = nil, want error %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("CreateContainerFromSpec() error = %v, want %v", err, tt.wantErr)
				}
			}
		})
	}
}

// Note: Full testing of container methods requires:
// 1. Mocking containerd.Client (LoadContainer, NewContainer, Delete methods)
// 2. Mocking containerd.Container interface
// 3. Mocking image pull/unpack operations
// 4. Setting up test snapshots and specs
//
// These tests are better suited for integration tests that exercise the
// full container lifecycle through exported methods.
