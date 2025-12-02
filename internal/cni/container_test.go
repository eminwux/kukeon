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

package cni_test

import (
	"context"
	"errors"
	"testing"

	cni "github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

func setupTestManager(t *testing.T, configDir string) *cni.Manager {
	t.Helper()
	mgr, err := cni.NewManager("", configDir, "")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	return mgr
}

func TestManager_AddContainerToNetwork(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*testing.T) *cni.Manager
		containerID string
		netnsPath   string
		wantErr     error
	}{
		{
			name: "network config not loaded",
			setup: func(t *testing.T) *cni.Manager {
				// Create manager without loading config
				return setupTestManager(t, t.TempDir())
			},
			containerID: "test-container",
			netnsPath:   "/proc/123/ns/net",
			wantErr:     errdefs.ErrNetworkConfigNotLoaded,
		},
		// Note: Full integration test with loaded config requires mocking libcni.CNI interface.
		// This is complex and better suited for integration tests. We focus on validation here.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := tt.setup(t)
			err := mgr.AddContainerToNetwork(context.Background(), tt.containerID, tt.netnsPath)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("AddContainerToNetwork() error = nil, want %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("AddContainerToNetwork() error = %v, want %v", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("AddContainerToNetwork() error = %v, want nil", err)
				}
			}
		})
	}
}

func TestManager_DelContainerFromNetwork(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*testing.T) *cni.Manager
		containerID string
		netnsPath   string
		wantErr     error
	}{
		{
			name: "network config not loaded",
			setup: func(t *testing.T) *cni.Manager {
				// Create manager without loading config
				return setupTestManager(t, t.TempDir())
			},
			containerID: "test-container",
			netnsPath:   "/proc/123/ns/net",
			wantErr:     errdefs.ErrNetworkConfigNotLoaded,
		},
		// Note: Full integration test with loaded config requires mocking libcni.CNI interface.
		// This is complex and better suited for integration tests. We focus on validation here.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := tt.setup(t)
			err := mgr.DelContainerFromNetwork(context.Background(), tt.containerID, tt.netnsPath)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("DelContainerFromNetwork() error = nil, want %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("DelContainerFromNetwork() error = %v, want %v", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("DelContainerFromNetwork() error = %v, want nil", err)
				}
			}
		})
	}
}
