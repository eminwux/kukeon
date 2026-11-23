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

package runner_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// reservedScopeSubdirNames mirrors runner.reservedScopeSubdirs; every name
// here is a per-scope resource dir that must never be listed as a child
// space, stack, or cell.
func reservedScopeSubdirNames() []string {
	return []string{
		consts.KukeonSecretsSubdir,
		consts.KukeonBlueprintsSubdir,
		consts.KukeonConfigsSubdir,
		consts.KukeonConfigStatusSubdir,
		consts.KukeonVolumesSubdir,
		consts.KukeonVolumeMetaSubdir,
		consts.KukeonHealthReportsSubdir,
	}
}

// seedReservedScopeTree writes realm/space/stack/cell metadata for one real
// cell (alpha/sp/st/cl) and plants every reserved subdir at the realm, space
// and stack levels — each with a nested child dir so a walker that descended
// into one would go a level deeper still. Mirrors the on-disk layout from
// #1602 (data/default/secrets, data/default/default/default/volumes, …).
func seedReservedScopeTree(t *testing.T) string {
	t.Helper()
	runPath := t.TempDir()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	const realm, space, stack, cell = "alpha", "sp", "st", "cl"
	write := func(doc any, path string) {
		t.Helper()
		if err := metadata.WriteMetadata(ctx, logger, doc, path); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	write(v1beta1.RealmDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindRealm,
		Metadata:   v1beta1.RealmMetadata{Name: realm},
		Spec:       v1beta1.RealmSpec{Namespace: "alpha.kukeon.io"},
	}, fs.RealmMetadataPath(runPath, realm))
	write(v1beta1.SpaceDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSpace,
		Metadata:   v1beta1.SpaceMetadata{Name: space},
		Spec:       v1beta1.SpaceSpec{RealmID: realm},
	}, fs.SpaceMetadataPath(runPath, realm, space))
	write(v1beta1.StackDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindStack,
		Metadata:   v1beta1.StackMetadata{Name: stack},
		Spec:       v1beta1.StackSpec{ID: stack, RealmID: realm, SpaceID: space},
	}, fs.StackMetadataPath(runPath, realm, space, stack))
	write(v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata:   v1beta1.CellMetadata{Name: cell},
		Spec:       v1beta1.CellSpec{ID: cell, RealmID: realm, SpaceID: space, StackID: stack},
	}, fs.CellMetadataPath(runPath, realm, space, stack, cell))

	scopeDirs := []string{
		fs.RealmMetadataDir(runPath, realm),
		fs.SpaceMetadataDir(runPath, realm, space),
		fs.StackMetadataDir(runPath, realm, space, stack),
	}
	for _, dir := range scopeDirs {
		for _, name := range reservedScopeSubdirNames() {
			if err := os.MkdirAll(filepath.Join(dir, name, "nested"), 0o750); err != nil {
				t.Fatalf("mkdir reserved subdir: %v", err)
			}
		}
	}
	return runPath
}

// assertNoReservedSubdirRead fails if the captured log shows any walker
// touched a reserved subdir as a resource, or any "metadata file does not
// exist" line at all (the #1602 symptom).
func assertNoReservedSubdirRead(t *testing.T, out string) {
	t.Helper()
	if strings.Contains(out, "level=ERROR") {
		t.Errorf("listing emitted ERROR-level log lines:\n%s", out)
	}
	if strings.Contains(out, "metadata file does not exist") {
		t.Errorf("listing tripped 'metadata file does not exist':\n%s", out)
	}
	for _, name := range reservedScopeSubdirNames() {
		if strings.Contains(out, string(filepath.Separator)+name+string(filepath.Separator)) {
			t.Errorf("listing read reserved subdir %q as a resource:\n%s", name, out)
		}
	}
}

func newCapturingRunner(runPath string) (runner.Runner, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return runner.NewRunner(context.Background(), logger, runner.Options{RunPath: runPath}), buf
}

// TestListSpaces_SkipsReservedScopeSubdirs is the regression guard for
// #1602 at the space level: <realm>/secrets and <realm>/blueprints (and the
// other reserved subdirs) must not be read as spaces.
func TestListSpaces_SkipsReservedScopeSubdirs(t *testing.T) {
	runPath := seedReservedScopeTree(t)
	for _, realmFilter := range []string{"", "alpha"} {
		r, buf := newCapturingRunner(runPath)
		spaces, err := r.ListSpaces(realmFilter)
		if err != nil {
			t.Fatalf("ListSpaces(%q): %v", realmFilter, err)
		}
		if len(spaces) != 1 || spaces[0].Metadata.Name != "sp" {
			t.Fatalf("ListSpaces(%q) = %v; want exactly [sp]", realmFilter, spaceNames(spaces))
		}
		assertNoReservedSubdirRead(t, buf.String())
	}
}

// TestListStacks_SkipsReservedScopeSubdirs covers the stack level: neither
// a reserved subdir under the realm (read as a space) nor one under the
// space (read as a stack) may surface or be read.
func TestListStacks_SkipsReservedScopeSubdirs(t *testing.T) {
	runPath := seedReservedScopeTree(t)
	for _, f := range [][2]string{{"", ""}, {"alpha", ""}, {"alpha", "sp"}} {
		r, buf := newCapturingRunner(runPath)
		stacks, err := r.ListStacks(f[0], f[1])
		if err != nil {
			t.Fatalf("ListStacks(%q, %q): %v", f[0], f[1], err)
		}
		if len(stacks) != 1 || stacks[0].Metadata.Name != "st" {
			names := make([]string, 0, len(stacks))
			for _, s := range stacks {
				names = append(names, s.Metadata.Name)
			}
			t.Fatalf("ListStacks(%q, %q) = %v; want exactly [st]", f[0], f[1], names)
		}
		assertNoReservedSubdirRead(t, buf.String())
	}
}

// TestListCells_SkipsReservedScopeSubdirs covers the cell level, the
// loudest #1602 path: <stack>/volumes, <stack>/configs and
// <stack>/config-status were each read as a cell on every reconcile tick.
func TestListCells_SkipsReservedScopeSubdirs(t *testing.T) {
	runPath := seedReservedScopeTree(t)
	for _, f := range [][3]string{{"", "", ""}, {"alpha", "", ""}, {"alpha", "sp", ""}, {"alpha", "sp", "st"}} {
		r, buf := newCapturingRunner(runPath)
		cells, err := r.ListCells(f[0], f[1], f[2])
		if err != nil {
			t.Fatalf("ListCells(%q, %q, %q): %v", f[0], f[1], f[2], err)
		}
		if len(cells) != 1 || cells[0].Metadata.Name != "cl" {
			names := make([]string, 0, len(cells))
			for _, c := range cells {
				names = append(names, c.Metadata.Name)
			}
			t.Fatalf("ListCells(%q, %q, %q) = %v; want exactly [cl]", f[0], f[1], f[2], names)
		}
		assertNoReservedSubdirRead(t, buf.String())
	}
}

// TestGetCell_MissingMetadataStillErrors covers the last #1602 AC: lowering
// the metadata reader's missing-file log to Debug must not hide a genuinely
// missing resource from a get-by-name caller.
func TestGetCell_MissingMetadataStillErrors(t *testing.T) {
	runPath := seedReservedScopeTree(t)
	r, _ := newCapturingRunner(runPath)
	_, err := r.GetCell(intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "ghost"},
		Spec:     intmodel.CellSpec{RealmName: "alpha", SpaceName: "sp", StackName: "st"},
	})
	if err == nil {
		t.Fatal("GetCell on a cell with no metadata.json returned nil error")
	}
	if !errors.Is(err, errdefs.ErrCellNotFound) {
		t.Errorf("GetCell error = %v; want ErrCellNotFound", err)
	}
}

func spaceNames(spaces []intmodel.Space) []string {
	names := make([]string, 0, len(spaces))
	for _, s := range spaces {
		names = append(names, s.Metadata.Name)
	}
	return names
}
