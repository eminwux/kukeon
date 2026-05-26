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
// allowlist: the per-stack secrets / blueprints / configs subdirs are
// legitimate children of the stack metadata dir (issue #619/#620/#624) and
// must not trigger the residue refusal, otherwise every stack that ever
// stamped a stack-scoped secret/blueprint/config would become un-deletable.
// The post-delete cleanup currently relies on os.RemoveAll to reclaim them,
// matching the pre-#905 behavior — the only contract this test pins is
// that the residue check does not block the call.
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
