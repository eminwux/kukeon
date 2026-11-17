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

//nolint:testpackage // exercises *Exec.DeleteStack against an in-package ctr.Client fake
package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// seedDeleteStackStack writes a minimal stack metadata.json so runner.GetStack
// resolves the stack and runner.DeleteStack reaches the residue / removal
// branches under test. Returns the on-disk path of the stack metadata file.
func seedDeleteStackStack(t *testing.T, r *Exec, realm, space, stack string) string {
	t.Helper()
	doc := v1beta1.StackDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindStack,
		Metadata:   v1beta1.StackMetadata{Name: stack},
		Spec: v1beta1.StackSpec{
			ID:      stack,
			RealmID: realm,
			SpaceID: space,
		},
	}
	path := fs.StackMetadataPath(r.opts.RunPath, realm, space, stack)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir stack metadata dir: %v", err)
	}
	if err := metadata.WriteMetadata(r.ctx, r.logger, doc, path); err != nil {
		t.Fatalf("write stack metadata: %v", err)
	}
	return path
}

func buildDeleteStackRequest(realm, space, stack string) intmodel.Stack {
	return intmodel.Stack{
		Metadata: intmodel.StackMetadata{Name: stack},
		Spec: intmodel.StackSpec{
			RealmName: realm,
			SpaceName: space,
		},
	}
}

// TestDeleteStack_RefusesOnCellSubdirResidue is the regression guard for
// issue #905. ListCells silently skips a cell whose metadata.json was
// removed or corrupted by a partial earlier teardown; the cascade loop in
// controller.deleteStackCascade therefore processed 0 cells, runner.DeleteStack
// removed the stack's own metadata.json, and the swallowed os.RemoveAll
// failed to recursively remove the surviving cell subdir — leaving residue
// on disk while the CLI exited 0.
//
// The fixed runner.DeleteStack must refuse before removing the stack
// metadata when a cell-shaped subdir survives, wrap the error under
// ErrDeleteStack, and leave both the stack metadata and the cell subdir
// intact so a re-run after manual cleanup can complete normally.
func TestDeleteStack_RefusesOnCellSubdirResidue(t *testing.T) {
	realm, space, stack := "default", "default", "web"
	fake := &deleteCellFakeClient{}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	stackMetadataPath := seedDeleteStackStack(t, r, realm, space, stack)

	// Seed a cell-shaped subdirectory that ListCells would silently skip
	// (no metadata.json inside). This is the residue path #905 describes.
	residueDir := filepath.Join(filepath.Dir(stackMetadataPath), "api")
	if err := os.MkdirAll(residueDir, 0o755); err != nil {
		t.Fatalf("mkdir residue cell dir: %v", err)
	}

	err := r.DeleteStack(buildDeleteStackRequest(realm, space, stack))
	if err == nil {
		t.Fatal("DeleteStack: want non-nil error on cell subdir residue, got nil")
	}
	if !errors.Is(err, errdefs.ErrDeleteStack) {
		t.Errorf("DeleteStack error must wrap ErrDeleteStack so the CLI exits non-zero; got %v", err)
	}
	if !strings.Contains(err.Error(), "api") {
		t.Errorf("DeleteStack error must name the residue subdir so the operator can find it; got %v", err)
	}
	if _, statErr := os.Stat(stackMetadataPath); statErr != nil {
		t.Errorf(
			"stack metadata must remain on disk when residue is detected, so the operator can re-run cleanup after manual repair; stat err=%v",
			statErr,
		)
	}
	if _, statErr := os.Stat(residueDir); statErr != nil {
		t.Errorf(
			"residue subdir must remain on disk for operator inspection; stat err=%v",
			statErr,
		)
	}
}

// TestDeleteStack_AllowsKnownScopeSubdirs locks the residue check's
// allowlist: the per-stack secrets / blueprints / configs / volumes /
// volume-meta subdirs are legitimate children of the stack metadata dir
// (issue #619/#620/#624/#1018/#1237) and must not trigger the residue
// refusal, otherwise every stack that ever stamped a stack-scoped resource
// would become un-deletable. An empty volumes/ parent is exactly what
// DeleteVolume leaves behind (#1370). The only contract this test pins is
// that the residue check does not block the call and the dir is reclaimed.
func TestDeleteStack_AllowsKnownScopeSubdirs(t *testing.T) {
	realm, space, stack := "default", "default", "web"
	fake := &deleteCellFakeClient{}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	stackMetadataPath := seedDeleteStackStack(t, r, realm, space, stack)

	stackDir := filepath.Dir(stackMetadataPath)
	for _, name := range []string{
		consts.KukeonSecretsSubdir,
		consts.KukeonBlueprintsSubdir,
		consts.KukeonConfigsSubdir,
		consts.KukeonConfigStatusSubdir,
		consts.KukeonVolumesSubdir,
		consts.KukeonVolumeMetaSubdir,
	} {
		if err := os.MkdirAll(filepath.Join(stackDir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s subdir: %v", name, err)
		}
	}

	if err := r.DeleteStack(buildDeleteStackRequest(realm, space, stack)); err != nil {
		t.Fatalf("DeleteStack: unexpected error: %v", err)
	}
	if _, statErr := os.Stat(stackDir); !os.IsNotExist(statErr) {
		t.Errorf("stack dir must be fully removed when only known scope subdirs are present; stat err=%v", statErr)
	}
}

// TestDeleteStack_RemovesStackDirOnSuccess is the positive guard for the
// symptom described in issue #905: a successful `kuke del stack --cascade`
// must leave no trace of the stack's metadata dir on disk so a subsequent
// `kuke create stack` with the same name does not collide with leftover
// directories. Pre-fix, `_ = os.RemoveAll(metadataRunPath)` swallowed any
// failure and the CLI exited 0 anyway; the fixed path surfaces failure and
// the happy-path leaves the dir gone.
func TestDeleteStack_RemovesStackDirOnSuccess(t *testing.T) {
	realm, space, stack := "default", "default", "web"
	fake := &deleteCellFakeClient{}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	stackMetadataPath := seedDeleteStackStack(t, r, realm, space, stack)
	stackDir := filepath.Dir(stackMetadataPath)

	if err := r.DeleteStack(buildDeleteStackRequest(realm, space, stack)); err != nil {
		t.Fatalf("DeleteStack: unexpected error: %v", err)
	}
	if _, statErr := os.Stat(stackDir); !os.IsNotExist(statErr) {
		t.Errorf(
			"stack dir must be fully removed on success so kuke create stack with the same name does not collide; stat err=%v",
			statErr,
		)
	}
}

// TestDeleteStack_SucceedsAfterVolumeCreateDelete is the #1370 reproduction:
// create a stack-scoped volume, delete it (DeleteVolume keeps the now-empty
// volumes/ parent), then delete the stack. The empty volumes/ parent must not
// be reported as cell residue, and the stack dir must be fully reclaimed.
func TestDeleteStack_SucceedsAfterVolumeCreateDelete(t *testing.T) {
	realm, space, stack := "default", "default", "web"
	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedDeleteCellRealm(t, r, realm)
	stackDir := filepath.Dir(seedDeleteStackStack(t, r, realm, space, stack))

	md := intmodel.VolumeMetadata{Name: "scratch", Realm: realm, Space: space, Stack: stack}
	if _, err := r.WriteVolume(intmodel.Volume{Metadata: md}); err != nil {
		t.Fatalf("WriteVolume: %v", err)
	}
	if err := r.DeleteVolume(intmodel.Volume{Metadata: md}); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	assertExists(t, filepath.Join(stackDir, consts.KukeonVolumesSubdir), "empty volumes/ parent precondition")

	if err := r.DeleteStack(buildDeleteStackRequest(realm, space, stack)); err != nil {
		t.Fatalf("DeleteStack after volume create/delete: unexpected error: %v", err)
	}
	assertGone(t, stackDir, "stack dir")
}

// TestDeleteStack_ReclaimsVolumesPerPolicy pins that stack teardown treats
// stack-scoped volumes as managed scope contents and honors their
// reclaimPolicy: a default-policy volume is reclaimed with the stack, a Retain
// volume (and its manifest) survives at its original coordinates, and the
// stack itself reports gone.
func TestDeleteStack_ReclaimsVolumesPerPolicy(t *testing.T) {
	realm, space, stack := "default", "default", "web"
	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedDeleteCellRealm(t, r, realm)
	stackMetadataPath := seedDeleteStackStack(t, r, realm, space, stack)

	keep := intmodel.VolumeMetadata{Name: "keep", Realm: realm, Space: space, Stack: stack}
	drop := intmodel.VolumeMetadata{Name: "drop", Realm: realm, Space: space, Stack: stack}
	if _, err := r.WriteVolume(retainVol(keep)); err != nil {
		t.Fatalf("WriteVolume(retained): %v", err)
	}
	if _, err := r.WriteVolume(deleteVol(drop)); err != nil {
		t.Fatalf("WriteVolume(default): %v", err)
	}

	if err := r.DeleteStack(buildDeleteStackRequest(realm, space, stack)); err != nil {
		t.Fatalf("DeleteStack with stack-scoped volumes: unexpected error: %v", err)
	}

	rp := r.opts.RunPath
	assertGone(t, stackMetadataPath, "stack metadata.json")
	assertGone(t, fs.VolumePath(rp, realm, space, stack, drop.Name), "default-policy volume")
	assertExists(t, fs.VolumePath(rp, realm, space, stack, keep.Name), "retained volume dir")
	assertExists(t, fs.VolumeMetaPath(rp, realm, space, stack, keep.Name), "retained volume manifest")
	if _, err := r.GetStack(buildDeleteStackRequest(realm, space, stack)); !errors.Is(err, errdefs.ErrStackNotFound) {
		t.Errorf("GetStack after delete: want ErrStackNotFound, got %v", err)
	}
}

// TestDeleteStack_RefusesOnCellResidueAlongsideVolumes keeps the #905 guard
// effective once volumes/ is allowlisted: a genuine partial cell subdir next
// to a managed volume still refuses the delete, names only the cell dir, and
// leaves both the cell dir and the volume on disk.
func TestDeleteStack_RefusesOnCellResidueAlongsideVolumes(t *testing.T) {
	realm, space, stack := "default", "default", "web"
	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedDeleteCellRealm(t, r, realm)
	stackMetadataPath := seedDeleteStackStack(t, r, realm, space, stack)

	md := intmodel.VolumeMetadata{Name: "scratch", Realm: realm, Space: space, Stack: stack}
	if _, err := r.WriteVolume(intmodel.Volume{Metadata: md}); err != nil {
		t.Fatalf("WriteVolume: %v", err)
	}
	residueDir := filepath.Join(filepath.Dir(stackMetadataPath), "api")
	if err := os.MkdirAll(residueDir, 0o755); err != nil {
		t.Fatalf("mkdir residue cell dir: %v", err)
	}

	err := r.DeleteStack(buildDeleteStackRequest(realm, space, stack))
	if !errors.Is(err, errdefs.ErrDeleteStack) {
		t.Fatalf("DeleteStack: want ErrDeleteStack on cell residue, got %v", err)
	}
	if !strings.Contains(err.Error(), "remove api;") {
		t.Errorf("residue error must name only the cell subdir; got %v", err)
	}
	assertExists(t, stackMetadataPath, "stack metadata.json")
	assertExists(t, residueDir, "residue cell dir")
	assertExists(t, fs.VolumePath(r.opts.RunPath, realm, space, stack, md.Name), "volume dir")
}
