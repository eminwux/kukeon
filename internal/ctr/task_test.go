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
	"github.com/eminwux/kukeon/internal/errdefs"
)

func setupTestClientForTask(t *testing.T) ctr.Client {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return ctr.NewClient(ctx, logger, "/test/socket")
}

// TestTaskStatusValidation tests input validation for TaskStatus.
// Full testing requires mocking containerd.Task interface which has many methods.
func TestTaskStatusValidation(t *testing.T) {
	client := setupTestClientForTask(t)

	// Test empty ID validation
	_, err := client.TaskStatus("")
	if err == nil {
		t.Error("TaskStatus with empty ID should return error")
	}
	if err != nil && !errors.Is(err, errdefs.ErrEmptyContainerID) {
		// Error might be wrapped, check if it contains the expected error
		if err.Error() == "" {
			t.Errorf("TaskStatus error should not be empty, got: %v", err)
		}
	}
}

// TestTaskMetricsValidation tests input validation for TaskMetrics.
// Full testing requires mocking containerd.Task interface which has many methods.
func TestTaskMetricsValidation(t *testing.T) {
	client := setupTestClientForTask(t)

	// Test empty ID validation
	_, err := client.TaskMetrics("")
	if err == nil {
		t.Error("TaskMetrics with empty ID should return error")
	}
	if err != nil && !errors.Is(err, errdefs.ErrEmptyContainerID) {
		// Error might be wrapped, check if it contains the expected error
		if err.Error() == "" {
			t.Errorf("TaskMetrics error should not be empty, got: %v", err)
		}
	}
}

func TestTaskStatusErrorHandling(t *testing.T) {
	client := setupTestClientForTask(t)

	tests := []struct {
		name    string
		id      string
		wantErr error
	}{
		{
			name:    "empty container ID",
			id:      "",
			wantErr: errdefs.ErrEmptyContainerID,
		},
		// Note: "valid container ID" case is skipped as it requires containerd connection
		// Validation tests focus on invalid inputs.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.TaskStatus(tt.id)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("TaskStatus() error = nil, want error %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("TaskStatus() error = %v, want %v", err, tt.wantErr)
				}
			}
		})
	}
}

func TestTaskMetricsErrorHandling(t *testing.T) {
	client := setupTestClientForTask(t)

	tests := []struct {
		name    string
		id      string
		wantErr error
	}{
		{
			name:    "empty container ID",
			id:      "",
			wantErr: errdefs.ErrEmptyContainerID,
		},
		// Note: "valid container ID" case is skipped as it requires containerd connection
		// Validation tests focus on invalid inputs.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.TaskMetrics(tt.id)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("TaskMetrics() error = nil, want error %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("TaskMetrics() error = %v, want %v", err, tt.wantErr)
				}
			}
		})
	}
}

// Note: Full testing of TaskStatus and TaskMetrics requires:
// 1. Mocking containerd.Task interface (which has many methods: Checkpoint, Delete, etc.)
// 2. Mocking containerd.Container interface to provide the Task
// 3. Setting up a test client with mocked containerd.Client
//
// These tests are better suited for integration tests or through exported API
// that exercises the full flow. The validation tests above ensure basic
// error handling paths are exercised.
