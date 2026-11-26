//go:build !integration

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

//nolint:testpackage // drives the unexported stored-cell read path
package runner

import (
	"encoding/json"
	"os"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestStoredCellWithRemovedContainerFieldsLoadsAndDropsThem covers a cell
// stored before ports, networks and networksAliases left the container spec:
// its metadata file still has the three keys. The cell must load, keep its
// spec hash and generation (so it is neither recreated nor reported out of
// sync), and lose the keys on its next write.
func TestStoredCellWithRemovedContainerFieldsLoadsAndDropsThem(t *testing.T) {
	r := newGenerationTestExec(t, t.TempDir())
	container := intmodel.ContainerSpec{
		ID: "main", RealmName: "r1", SpaceName: "s1", StackName: "st1", CellName: "c1",
		Root: true, Image: "alpine", Command: "sleep", Args: []string{"infinity"}, Env: []string{"A=b"},
	}
	cell := seedCell(t, r)
	cell.Spec.Containers = []intmodel.ContainerSpec{container}
	if err := r.UpdateCellMetadata(cell); err != nil {
		t.Fatalf("UpdateCellMetadata: %v", err)
	}
	// The first read fills spec.rootContainerId; write it back so the stored
	// document is the stable form a running daemon leaves on disk.
	if err := r.UpdateCellMetadata(readCell(t, r)); err != nil {
		t.Fatalf("UpdateCellMetadata (settle): %v", err)
	}
	before := readCell(t, r)

	path := fs.CellMetadataPath(r.opts.RunPath, "r1", "s1", "st1", "c1")
	removed := map[string]any{
		"ports":           []string{"8080:80"},
		"networks":        []string{"data-plane"},
		"networksAliases": []string{"web"},
	}
	rewriteStoredContainer(t, path, func(stored map[string]any) {
		for key, value := range removed {
			stored[key] = value
		}
	})

	loaded, err := r.readCellInternal(path)
	if err != nil {
		t.Fatalf("stored cell with the removed keys did not load: %v", err)
	}
	if len(loaded.Spec.Containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(loaded.Spec.Containers))
	}
	if got, want := ComputeContainerSpecHash(loaded.Spec.Containers[0]), ComputeContainerSpecHash(container); got != want {
		t.Errorf("spec hash = %s, want %s (unchanged)", got, want)
	}

	if err = r.UpdateCellMetadata(loaded); err != nil {
		t.Fatalf("write of the loaded cell: %v", err)
	}
	rewriteStoredContainer(t, path, func(stored map[string]any) {
		for key := range removed {
			if _, present := stored[key]; present {
				t.Errorf("stored container still has %q after a write", key)
			}
		}
		if stored["image"] != "alpine" {
			t.Errorf("stored image = %v, want alpine", stored["image"])
		}
	})
	if after := readCell(t, r); after.Metadata.Generation != before.Metadata.Generation {
		t.Errorf("generation = %d, want %d: dropping the keys must not count as a spec change",
			after.Metadata.Generation, before.Metadata.Generation)
	}
}

// rewriteStoredContainer decodes the cell metadata file as untyped JSON, hands
// the first container to edit, and writes the document back.
func rewriteStoredContainer(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	spec, _ := doc["spec"].(map[string]any)
	containers, _ := spec["containers"].([]any)
	if len(containers) == 0 {
		t.Fatalf("no containers in %s", path)
	}
	stored, _ := containers[0].(map[string]any)
	edit(stored)
	if raw, err = json.Marshal(doc); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
