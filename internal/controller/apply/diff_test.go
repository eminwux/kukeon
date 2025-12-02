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

package apply_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/controller/apply"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestDiffRealm_NoChanges(t *testing.T) {
	desired := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name:   "test-realm",
			Labels: map[string]string{"env": "test"},
		},
		Spec: intmodel.RealmSpec{
			Namespace: "test-ns",
		},
	}

	actual := desired

	diff := apply.DiffRealm(desired, actual)
	if diff.HasChanges {
		t.Error("expected no changes")
	}
}

func TestDiffRealm_BreakingChange_Name(t *testing.T) {
	desired := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: "new-realm",
		},
	}

	actual := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: "old-realm",
		},
	}

	diff := apply.DiffRealm(desired, actual)
	if !diff.HasChanges {
		t.Error("expected changes")
	}
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Errorf("expected breaking change, got %v", diff.ChangeType)
	}
	if len(diff.BreakingChanges) == 0 {
		t.Error("expected breaking changes")
	}
}

func TestDiffRealm_CompatibleChange_Labels(t *testing.T) {
	desired := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name:   "test-realm",
			Labels: map[string]string{"env": "prod"},
		},
	}

	actual := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name:   "test-realm",
			Labels: map[string]string{"env": "test"},
		},
	}

	diff := apply.DiffRealm(desired, actual)
	if !diff.HasChanges {
		t.Error("expected changes")
	}
	if diff.ChangeType != apply.ChangeTypeCompatible {
		t.Errorf("expected compatible change, got %v", diff.ChangeType)
	}
}

func TestDiffCell_RootContainerChanged(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: "test-cell",
		},
		Spec: intmodel.CellSpec{
			RealmName: "realm",
			SpaceName: "space",
			StackName: "stack",
			Containers: []intmodel.ContainerSpec{
				{
					ID:    "root",
					Root:  true,
					Image: "busybox:latest",
				},
			},
		},
	}

	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: "test-cell",
		},
		Spec: intmodel.CellSpec{
			RealmName: "realm",
			SpaceName: "space",
			StackName: "stack",
			Containers: []intmodel.ContainerSpec{
				{
					ID:    "root",
					Root:  true,
					Image: "alpine:latest",
				},
			},
		},
	}

	diff := apply.DiffCell(desired, actual)
	if !diff.HasChanges {
		t.Error("expected changes")
	}
	if !diff.RootContainerChanged {
		t.Error("expected root container to be changed")
	}
	if diff.ChangeType != apply.ChangeTypeBreaking {
		t.Errorf("expected breaking change, got %v", diff.ChangeType)
	}
}
