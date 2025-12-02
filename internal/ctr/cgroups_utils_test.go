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
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestDefaultRealmSpec(t *testing.T) {
	tests := []struct {
		name      string
		realm     intmodel.Realm
		wantGroup string
	}{
		{
			name: "simple realm name",
			realm: intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: "test-realm",
				},
			},
			wantGroup: "/kukeon/test-realm",
		},
		{
			name: "realm with special characters",
			realm: intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: "realm-123",
				},
			},
			wantGroup: "/kukeon/realm-123",
		},
		{
			name: "empty realm name",
			realm: intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: "",
				},
			},
			wantGroup: "/kukeon/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.DefaultRealmSpec(tt.realm)

			if spec.Group != tt.wantGroup {
				t.Errorf("Group = %q, want %q", spec.Group, tt.wantGroup)
			}

			if spec.Mountpoint != "" {
				t.Errorf("Mountpoint = %q, want empty", spec.Mountpoint)
			}

			// Verify Resources are all nil
			if spec.Resources.CPU != nil {
				t.Error("Resources.CPU should be nil")
			}
			if spec.Resources.Memory != nil {
				t.Error("Resources.Memory should be nil")
			}
			if spec.Resources.IO != nil {
				t.Error("Resources.IO should be nil")
			}
		})
	}
}

func TestDefaultSpaceSpec(t *testing.T) {
	tests := []struct {
		name      string
		space     intmodel.Space
		wantGroup string
	}{
		{
			name: "simple space with realm",
			space: intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: "test-space",
				},
				Spec: intmodel.SpaceSpec{
					RealmName: "test-realm",
				},
			},
			wantGroup: "/kukeon/test-realm/test-space",
		},
		{
			name: "space with multiple components",
			space: intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: "space-123",
				},
				Spec: intmodel.SpaceSpec{
					RealmName: "realm-456",
				},
			},
			wantGroup: "/kukeon/realm-456/space-123",
		},
		{
			name: "space with empty names",
			space: intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: "",
				},
				Spec: intmodel.SpaceSpec{
					RealmName: "",
				},
			},
			wantGroup: "/kukeon//",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.DefaultSpaceSpec(tt.space)

			if spec.Group != tt.wantGroup {
				t.Errorf("Group = %q, want %q", spec.Group, tt.wantGroup)
			}

			if spec.Mountpoint != "" {
				t.Errorf("Mountpoint = %q, want empty", spec.Mountpoint)
			}

			// Verify Resources are all nil
			if spec.Resources.CPU != nil {
				t.Error("Resources.CPU should be nil")
			}
			if spec.Resources.Memory != nil {
				t.Error("Resources.Memory should be nil")
			}
			if spec.Resources.IO != nil {
				t.Error("Resources.IO should be nil")
			}
		})
	}
}

func TestDefaultStackSpec(t *testing.T) {
	tests := []struct {
		name      string
		stack     intmodel.Stack
		wantGroup string
	}{
		{
			name: "simple stack with realm and space",
			stack: intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: "test-stack",
				},
				Spec: intmodel.StackSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
				},
			},
			wantGroup: "/kukeon/test-realm/test-space/test-stack",
		},
		{
			name: "stack with all components",
			stack: intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: "stack-123",
				},
				Spec: intmodel.StackSpec{
					RealmName: "realm-456",
					SpaceName: "space-789",
				},
			},
			wantGroup: "/kukeon/realm-456/space-789/stack-123",
		},
		{
			name: "stack with empty names",
			stack: intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: "",
				},
				Spec: intmodel.StackSpec{
					RealmName: "",
					SpaceName: "",
				},
			},
			wantGroup: "/kukeon///",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.DefaultStackSpec(tt.stack)

			if spec.Group != tt.wantGroup {
				t.Errorf("Group = %q, want %q", spec.Group, tt.wantGroup)
			}

			if spec.Mountpoint != "" {
				t.Errorf("Mountpoint = %q, want empty", spec.Mountpoint)
			}

			// Verify Resources are all nil
			if spec.Resources.CPU != nil {
				t.Error("Resources.CPU should be nil")
			}
			if spec.Resources.Memory != nil {
				t.Error("Resources.Memory should be nil")
			}
			if spec.Resources.IO != nil {
				t.Error("Resources.IO should be nil")
			}
		})
	}
}

func TestDefaultCellSpec(t *testing.T) {
	tests := []struct {
		name      string
		cell      intmodel.Cell
		wantGroup string
	}{
		{
			name: "simple cell with all hierarchy",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
				},
			},
			wantGroup: "/kukeon/test-realm/test-space/test-stack/test-cell",
		},
		{
			name: "cell with all components",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "cell-123",
				},
				Spec: intmodel.CellSpec{
					RealmName: "realm-456",
					SpaceName: "space-789",
					StackName: "stack-abc",
				},
			},
			wantGroup: "/kukeon/realm-456/space-789/stack-abc/cell-123",
		},
		{
			name: "cell with empty names",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "",
				},
				Spec: intmodel.CellSpec{
					RealmName: "",
					SpaceName: "",
					StackName: "",
				},
			},
			wantGroup: "/kukeon////", // 4 levels: realm/space/stack/cell
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.DefaultCellSpec(tt.cell)

			if spec.Group != tt.wantGroup {
				t.Errorf("Group = %q, want %q", spec.Group, tt.wantGroup)
			}

			if spec.Mountpoint != "" {
				t.Errorf("Mountpoint = %q, want empty", spec.Mountpoint)
			}

			// Verify Resources are all nil
			if spec.Resources.CPU != nil {
				t.Error("Resources.CPU should be nil")
			}
			if spec.Resources.Memory != nil {
				t.Error("Resources.Memory should be nil")
			}
			if spec.Resources.IO != nil {
				t.Error("Resources.IO should be nil")
			}
		})
	}
}
