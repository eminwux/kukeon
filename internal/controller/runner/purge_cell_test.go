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

//nolint:testpackage // exercises *Exec.PurgeCell against an in-package ctr.Client fake
package runner

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// writePurgeCellConflist installs a no-op CNI plugin and a conflist for the
// cell's space at the run-path fallback location, so the purge's CNI DEL
// succeeds and the runner can record "cni-resources".
func writePurgeCellConflist(t *testing.T, r *Exec, realm, space string) {
	t.Helper()
	networkName, err := naming.BuildSpaceNetworkName(realm, space)
	if err != nil {
		t.Fatalf("BuildSpaceNetworkName: %v", err)
	}
	binDir := t.TempDir()
	if err = os.WriteFile(filepath.Join(binDir, "noop"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake plugin: %v", err)
	}
	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, realm, space)
	if err != nil {
		t.Fatalf("SpaceNetworkConfigPath: %v", err)
	}
	if err = os.MkdirAll(filepath.Dir(confPath), 0o750); err != nil {
		t.Fatalf("mkdir conflist dir: %v", err)
	}
	conflist := `{"cniVersion": "0.4.0", "name": "` + networkName + `", "plugins": [{"type": "noop"}]}`
	if err = os.WriteFile(confPath, []byte(conflist), 0o600); err != nil {
		t.Fatalf("write conflist: %v", err)
	}
	r.cniConf = &cni.Conf{CniConfigDir: t.TempDir(), CniBinDir: binDir, CniCacheDir: t.TempDir()}
}

// loadPurgeCell returns the stored cell, matching the controller, which passes
// the loaded cell (with its container specs) to the runner's PurgeCell.
func loadPurgeCell(t *testing.T, r *Exec, realm, space, stack, cellName string) intmodel.Cell {
	t.Helper()
	cell, err := r.GetCell(buildDeleteCellRequest(realm, space, stack, cellName))
	if err != nil {
		t.Fatalf("GetCell: %v", err)
	}
	return cell
}

// TestPurgeCell_PreservesMetadataWhenContainersRemain guards the purge
// ordering: when a cell container survives the delete step, PurgeCell must
// leave the cell document on disk so `kuke purge`/`kuke delete` can find the
// cell and retry, instead of orphaning the container behind a "not found".
func TestPurgeCell_PreservesMetadataWhenContainersRemain(t *testing.T) {
	realm, space, stack, cellName := "default", "default", "default", "stuck"
	workloadID := space + "_" + stack + "_" + cellName + "_workload"

	fake := &deleteCellFakeClient{
		deleteContainerFn: func(_, id string, _ ctr.ContainerDeleteOptions) error {
			if id == workloadID {
				return errors.New("container is still running")
			}
			return nil
		},
		listContainersFn: func(string, ...string) ([]containerd.Container, error) {
			return []containerd.Container{stubContainer{id: workloadID}}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	metadataPath := seedDeleteCellCell(t, r, realm, space, stack, cellName)
	writePurgeCellConflist(t, r, realm, space)

	outcome, err := r.PurgeCell(loadPurgeCell(t, r, realm, space, stack, cellName))
	if err == nil {
		t.Fatal("PurgeCell: want an error when a cell container remains, got nil")
	}
	if outcome.ContainersDeleted {
		t.Error("ContainersDeleted = true, want false when a container remains")
	}
	if outcome.MetadataDeleted {
		t.Error("MetadataDeleted = true, want false when a container remains")
	}
	for _, marker := range []string{"orphaned-containers", "all-metadata"} {
		if slices.Contains(outcome.Purged, marker) {
			t.Errorf("Purged = %v, must not contain %q when a container remains", outcome.Purged, marker)
		}
	}
	if _, statErr := os.Stat(metadataPath); statErr != nil {
		t.Errorf("cell metadata must remain on disk for a retry; stat err=%v", statErr)
	}
}

// TestPurgeCell_ReportsCompleteOutcome covers the success path: every cleanup
// step is confirmed, so all outcome flags and markers are set and the cell
// metadata is removed.
func TestPurgeCell_ReportsCompleteOutcome(t *testing.T) {
	realm, space, stack, cellName := "default", "default", "default", "clean"

	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedDeleteCellRealm(t, r, realm)
	metadataPath := seedDeleteCellCell(t, r, realm, space, stack, cellName)
	writePurgeCellConflist(t, r, realm, space)

	outcome, err := r.PurgeCell(loadPurgeCell(t, r, realm, space, stack, cellName))
	if err != nil {
		t.Fatalf("PurgeCell: unexpected error: %v", err)
	}
	if !outcome.ContainersDeleted || !outcome.CgroupDeleted || !outcome.MetadataDeleted {
		t.Errorf(
			"outcome flags = containers:%v cgroup:%v metadata:%v, want all true",
			outcome.ContainersDeleted, outcome.CgroupDeleted, outcome.MetadataDeleted,
		)
	}
	for _, marker := range []string{"cni-resources", "cgroup", "orphaned-containers", "all-metadata"} {
		if !slices.Contains(outcome.Purged, marker) {
			t.Errorf("Purged = %v, want %q", outcome.Purged, marker)
		}
	}
	if _, statErr := os.Stat(metadataPath); !os.IsNotExist(statErr) {
		t.Errorf("cell metadata should be removed on full success; stat err=%v", statErr)
	}
}

// TestPurgeCell_MarksIncompleteCNICleanup confirms a failed CNI DEL (no
// conflist resolvable) is reported as "cni-resources-incomplete" with an
// error, never as "cni-resources".
func TestPurgeCell_MarksIncompleteCNICleanup(t *testing.T) {
	realm, space, stack, cellName := "default", "default", "default", "nocni"

	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedDeleteCellRealm(t, r, realm)
	seedDeleteCellCell(t, r, realm, space, stack, cellName)

	outcome, err := r.PurgeCell(loadPurgeCell(t, r, realm, space, stack, cellName))
	if err == nil {
		t.Fatal("PurgeCell: want an error when CNI cleanup fails, got nil")
	}
	if !slices.Contains(outcome.Purged, "cni-resources-incomplete") {
		t.Errorf("Purged = %v, want cni-resources-incomplete", outcome.Purged)
	}
	if slices.Contains(outcome.Purged, "cni-resources") {
		t.Errorf("Purged = %v, must not contain cni-resources after a failed DEL", outcome.Purged)
	}
}
