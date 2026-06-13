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
//
// SPDX-License-Identifier: Apache-2.0

//nolint:testpackage // exercises the unexported VolumeMountedByLiveCell path against a temp RunPath
package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// seedVolumeRefCell writes a cell metadata doc with a single workload container
// that mounts the named same-scope Volume via a kind: volume mount, in the
// given persisted cell state. ListCells reads it back through the validating
// converter, so the mount must be a valid volume reference.
func seedVolumeRefCell(
	t *testing.T,
	r *Exec,
	realm, space, stack, cellName, volumeName string,
	state v1beta1.CellState,
) {
	t.Helper()
	doc := v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata:   v1beta1.CellMetadata{Name: cellName},
		Spec: v1beta1.CellSpec{
			ID:      cellName,
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      "workload",
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
					CellID:  cellName,
					Image:   "alpine:latest",
					Volumes: []v1beta1.VolumeMount{
						{Kind: v1beta1.VolumeKindVolume, Source: volumeName, Target: "/data"},
					},
				},
			},
		},
		Status: v1beta1.CellStatus{State: state},
	}
	path := fs.CellMetadataPath(r.opts.RunPath, realm, space, stack, cellName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir cell metadata dir: %v", err)
	}
	if err := metadata.WriteMetadata(r.ctx, r.logger, doc, path); err != nil {
		t.Fatalf("write cell metadata: %v", err)
	}
}

func TestVolumeMountedByLiveCell_ReadyCellMountingRefuses(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	vol := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default", Space: "app", Stack: "web"}}
	if _, err := r.WriteVolume(vol); err != nil {
		t.Fatalf("WriteVolume: %v", err)
	}
	seedVolumeRefCell(t, r, "default", "app", "web", "db", "data", v1beta1.CellStateReady)

	ref, mounted, err := r.VolumeMountedByLiveCell(vol)
	if err != nil {
		t.Fatalf("VolumeMountedByLiveCell: %v", err)
	}
	if !mounted {
		t.Fatal("mounted = false, want true for a Ready cell mounting the volume")
	}
	if want := "default/app/web/db"; ref != want {
		t.Errorf("cell ref = %q, want %q", ref, want)
	}
}

func TestVolumeMountedByLiveCell_StoppedCellAllows(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	vol := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default", Space: "app", Stack: "web"}}
	if _, err := r.WriteVolume(vol); err != nil {
		t.Fatalf("WriteVolume: %v", err)
	}
	// A stopped cell that references the volume must not gate its delete.
	seedVolumeRefCell(t, r, "default", "app", "web", "db", "data", v1beta1.CellStateStopped)

	_, mounted, err := r.VolumeMountedByLiveCell(vol)
	if err != nil {
		t.Fatalf("VolumeMountedByLiveCell: %v", err)
	}
	if mounted {
		t.Error("mounted = true for a stopped cell, want false (gate keys off running state)")
	}
}

func TestVolumeMountedByLiveCell_DifferentVolumeAllows(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	target := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default", Space: "app", Stack: "web"}}
	other := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "logs", Realm: "default", Space: "app", Stack: "web"}}
	for _, v := range []intmodel.Volume{target, other} {
		if _, err := r.WriteVolume(v); err != nil {
			t.Fatalf("WriteVolume %q: %v", v.Metadata.Name, err)
		}
	}
	// A Ready cell mounts "logs", not the "data" volume we attempt to delete.
	seedVolumeRefCell(t, r, "default", "app", "web", "db", "logs", v1beta1.CellStateReady)

	_, mounted, err := r.VolumeMountedByLiveCell(target)
	if err != nil {
		t.Fatalf("VolumeMountedByLiveCell: %v", err)
	}
	if mounted {
		t.Error("mounted = true, want false — the live cell mounts a different volume")
	}
}

// TestVolume_SurvivesCellDelete pins AC #5: a Volume and its contents persist
// when a cell that mounted it is deleted. The Volume directory lives under the
// scope's volumes/ tree, never under any cell's metadata subtree, so removing
// the cell (here, its on-disk metadata directory — the unit-level proxy for
// `kuke delete cell`) cannot reclaim it, and a new cell referencing the same
// Volume sees the prior sentinel contents.
func TestVolume_SurvivesCellDelete(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	vol := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default", Space: "app", Stack: "web"}}
	if _, err := r.WriteVolume(vol); err != nil {
		t.Fatalf("WriteVolume: %v", err)
	}
	volPath := fs.VolumePath(runPath, "default", "app", "web", "data")
	sentinel := filepath.Join(volPath, "sentinel")
	if err := os.WriteFile(sentinel, []byte("persisted"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// The volume directory must not live under the cell's metadata subtree.
	cellMetaDir := filepath.Dir(fs.CellMetadataPath(runPath, "default", "app", "web", "db"))
	if strings.HasPrefix(filepath.Clean(volPath), filepath.Clean(cellMetaDir)+string(filepath.Separator)) {
		t.Fatalf("volume dir %q lives under cell metadata dir %q — cell delete would reclaim it", volPath, cellMetaDir)
	}

	// Simulate the cell's deletion by removing its metadata subtree.
	seedVolumeRefCell(t, r, "default", "app", "web", "db", "data", v1beta1.CellStateReady)
	if err := os.RemoveAll(cellMetaDir); err != nil {
		t.Fatalf("remove cell metadata: %v", err)
	}

	// The volume and its sentinel survive, and remain resolvable for a new cell.
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel after cell delete: %v", err)
	}
	if string(got) != "persisted" {
		t.Errorf("sentinel contents = %q, want %q", got, "persisted")
	}
	if _, err := r.GetVolume(vol); err != nil {
		t.Errorf("GetVolume after cell delete: %v (volume must outlive the cell)", err)
	}
}
