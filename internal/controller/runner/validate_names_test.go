// Copyright 2026 Emiliano Spinella (eminwux)
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

package runner

import (
	"errors"
	iofs "io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// badNames are the names #1719's acceptance criteria require every hierarchy
// entry point to reject. The empty name is covered separately: it fails with
// each entry point's own "required" sentinel.
func badNames() []string { return []string{".", "..", ".x", "A", "a b", `a\b`, "a_b", "a/b"} }

// snapshotTree records every path under root with its size and mode, so a test
// can assert a refused call neither wrote nor removed anything.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = info.Mode().String()
		if !d.IsDir() {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			out[path] += ":" + string(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func assertTreeUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	after := snapshotTree(t, root)
	for path, v := range before {
		if after[path] != v {
			t.Errorf("%s changed or removed", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s was written", path)
		}
	}
}

// seedNameTestTree is a host with two realms, kuke-system and alpha, alpha
// holding space s, stack st and cell c, and a stray <RunPath>/data/metadata.json
// plus <RunPath>/metadata.json standing in for what a ".." space or realm
// create used to write (so a Get of ".." finds something to act on).
func seedNameTestTree(t *testing.T, r *Exec) {
	t.Helper()
	seedDeleteCellRealm(t, r, "kuke-system")
	seedDeleteCellRealm(t, r, "alpha")
	seedDeleteCellCell(t, r, "alpha", "s", "st", "c")
	for _, path := range []string{
		fs.SpaceMetadataPath(r.opts.RunPath, "alpha", "s"),
		fs.StackMetadataPath(r.opts.RunPath, "alpha", "s", "st"),
		filepath.Join(fs.MetadataRoot(r.opts.RunPath), "metadata.json"),
		filepath.Join(r.opts.RunPath, "metadata.json"),
	} {
		if err := os.WriteFile(path, []byte(`{"apiVersion":"v1beta1"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func realmReq(name string) intmodel.Realm {
	return intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: name}}
}

func spaceReq(realm, name string) intmodel.Space {
	return intmodel.Space{Metadata: intmodel.SpaceMetadata{Name: name}, Spec: intmodel.SpaceSpec{RealmName: realm}}
}

func stackReq(space, name string) intmodel.Stack {
	return intmodel.Stack{
		Metadata: intmodel.StackMetadata{Name: name},
		Spec:     intmodel.StackSpec{RealmName: "alpha", SpaceName: space},
	}
}

func cellReq(stack, name string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: name},
		Spec:     intmodel.CellSpec{ID: name, RealmName: "alpha", SpaceName: "s", StackName: stack},
	}
}

// TestHierarchyEntryPoints_RejectInvalidNames is #1719's AC: realm, space,
// stack, cell and container create, get, delete and purge reject every bad
// name with the existing invalid-name sentinels, before any file is written or
// removed. Each coordinate is tried in its own position and, for the deeper
// kinds, as a parent coordinate too.
func TestHierarchyEntryPoints_RejectInvalidNames(t *testing.T) {
	type call struct {
		name string
		fn   func(r *Exec, bad string) error
		want error
	}
	calls := []call{
		{
			"CreateRealm", func(r *Exec, b string) error { _, err := r.CreateRealm(realmReq(b)); return err },
			errdefs.ErrInvalidRealmName,
		},
		{
			"GetRealm", func(r *Exec, b string) error { _, err := r.GetRealm(realmReq(b)); return err },
			errdefs.ErrInvalidRealmName,
		},
		{
			"DeleteRealm", func(r *Exec, b string) error { return r.DeleteRealm(realmReq(b)) },
			errdefs.ErrInvalidRealmName,
		},
		{
			"PurgeRealm", func(r *Exec, b string) error { _, err := r.PurgeRealm(realmReq(b)); return err },
			errdefs.ErrInvalidRealmName,
		},
		{
			"PurgeContainer realm", func(r *Exec, b string) error { return r.PurgeContainer(realmReq(b), "s_st_c_w") },
			errdefs.ErrInvalidRealmName,
		},
		{
			"ListSpaces realm", func(r *Exec, b string) error { _, err := r.ListSpaces(b); return err },
			errdefs.ErrInvalidRealmName,
		},

		{
			"CreateSpace", func(r *Exec, b string) error { _, err := r.CreateSpace(spaceReq("alpha", b)); return err },
			errdefs.ErrInvalidName,
		},
		{
			"CreateSpace realm", func(r *Exec, b string) error { _, err := r.CreateSpace(spaceReq(b, "x")); return err },
			errdefs.ErrInvalidRealmName,
		},
		{
			"GetSpace", func(r *Exec, b string) error { _, err := r.GetSpace(spaceReq("alpha", b)); return err },
			errdefs.ErrInvalidName,
		},
		{
			"DeleteSpace", func(r *Exec, b string) error { return r.DeleteSpace(spaceReq("alpha", b)) },
			errdefs.ErrInvalidName,
		},
		{
			"PurgeSpace", func(r *Exec, b string) error { return r.PurgeSpace(spaceReq("alpha", b)) },
			errdefs.ErrInvalidName,
		},
		{
			"ListStacks space", func(r *Exec, b string) error { _, err := r.ListStacks("alpha", b); return err },
			errdefs.ErrInvalidName,
		},

		{
			"CreateStack", func(r *Exec, b string) error { _, err := r.CreateStack(stackReq("s", b)); return err },
			errdefs.ErrInvalidName,
		},
		{"CreateStack space", func(r *Exec, b string) error {
			_, err := r.CreateStack(stackReq(b, "x"))
			return err
		}, errdefs.ErrInvalidName},
		{
			"GetStack", func(r *Exec, b string) error { _, err := r.GetStack(stackReq("s", b)); return err },
			errdefs.ErrInvalidName,
		},
		{
			"DeleteStack", func(r *Exec, b string) error { return r.DeleteStack(stackReq("s", b)) },
			errdefs.ErrInvalidName,
		},
		{
			"PurgeStack", func(r *Exec, b string) error { return r.PurgeStack(stackReq("s", b)) },
			errdefs.ErrInvalidName,
		},
		{
			"ListCells stack", func(r *Exec, b string) error { _, err := r.ListCells("alpha", "s", b); return err },
			errdefs.ErrInvalidName,
		},

		{"CreateCell", func(r *Exec, b string) error {
			_, err := r.CreateCell(cellReq("st", b))
			return err
		}, errdefs.ErrInvalidName},
		{"CreateCell stack", func(r *Exec, b string) error {
			_, err := r.CreateCell(cellReq(b, "x"))
			return err
		}, errdefs.ErrInvalidName},
		{
			"GetCell", func(r *Exec, b string) error { _, err := r.GetCell(cellReq("st", b)); return err },
			errdefs.ErrInvalidName,
		},
		{
			"DeleteCell", func(r *Exec, b string) error { return r.DeleteCell(cellReq("st", b)) },
			errdefs.ErrInvalidName,
		},
		{"PurgeCell", func(r *Exec, b string) error {
			_, err := r.PurgeCell(cellReq("st", b))
			return err
		}, errdefs.ErrInvalidName},
		{"ListContainers cell", func(r *Exec, b string) error {
			_, err := r.ListContainers("alpha", "s", "st", b)
			return err
		}, errdefs.ErrInvalidName},

		{"CreateContainer", func(r *Exec, b string) error {
			_, err := r.CreateContainer(cellReq("st", "c"), intmodel.ContainerSpec{ID: b})
			return err
		}, errdefs.ErrInvalidName},
		{"CreateContainer cell", func(r *Exec, b string) error {
			_, err := r.CreateContainer(cellReq("st", b), intmodel.ContainerSpec{ID: "w"})
			return err
		}, errdefs.ErrInvalidName},
		{"DeleteContainer", func(r *Exec, b string) error {
			return r.DeleteContainer(cellReq("st", "c"), b)
		}, errdefs.ErrInvalidName},
		{"DeleteContainer cell", func(r *Exec, b string) error {
			return r.DeleteContainer(cellReq("st", b), "workload")
		}, errdefs.ErrInvalidName},
	}

	for _, c := range calls {
		for _, bad := range append([]string{""}, badNames()...) {
			t.Run(c.name+"/"+bad, func(t *testing.T) {
				r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
				seedNameTestTree(t, r)
				before := snapshotTree(t, r.opts.RunPath)

				err := c.fn(r, bad)
				switch {
				case bad == "" && c.name != "ListSpaces realm" && c.name != "ListStacks space" &&
					c.name != "ListCells stack" && c.name != "ListContainers cell":
					// Empty is "required" on every non-list entry point;
					// on a list it is the unset filter and lists everything.
					if err == nil {
						t.Fatalf("%s(%q) = nil, want an error", c.name, bad)
					}
				case bad == "":
					return
				case !errors.Is(err, c.want):
					t.Fatalf("%s(%q) = %v, want errors.Is(_, %v)", c.name, bad, err, c.want)
				}
				assertTreeUnchanged(t, r.opts.RunPath, before)
			})
		}
	}
}

// TestDeleteSpaceDotDot_LeavesTreeIntact reproduces #1719's first Context case:
// a space ".." in realm alpha used to resolve to <RunPath>/data, and DeleteSpace
// reclaimed it, removing every realm including kuke-system.
func TestDeleteSpaceDotDot_LeavesTreeIntact(t *testing.T) {
	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedNameTestTree(t, r)
	before := snapshotTree(t, r.opts.RunPath)

	if _, err := r.CreateSpace(spaceReq("alpha", "..")); !errors.Is(err, errdefs.ErrInvalidName) {
		t.Fatalf("CreateSpace(alpha/..) = %v, want ErrInvalidName", err)
	}
	if err := r.DeleteSpace(spaceReq("alpha", "..")); !errors.Is(err, errdefs.ErrInvalidName) {
		t.Fatalf("DeleteSpace(alpha/..) = %v, want ErrInvalidName", err)
	}
	if err := r.PurgeSpace(spaceReq("alpha", "..")); !errors.Is(err, errdefs.ErrInvalidName) {
		t.Fatalf("PurgeSpace(alpha/..) = %v, want ErrInvalidName", err)
	}
	assertTreeUnchanged(t, r.opts.RunPath, before)
	if _, err := os.Stat(fs.RealmMetadataPath(r.opts.RunPath, "kuke-system")); err != nil {
		t.Fatalf("kuke-system realm metadata gone: %v", err)
	}
}

// TestPurgeRealmDotDot_LeavesTreeIntact reproduces #1719's second Context case:
// CreateRealm("..") wrote <RunPath>/metadata.json, GetRealm("..") then found it,
// and PurgeRealm("..") removed all of <RunPath>.
func TestPurgeRealmDotDot_LeavesTreeIntact(t *testing.T) {
	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedDeleteCellRealm(t, r, "kuke-system")
	before := snapshotTree(t, r.opts.RunPath)

	if _, err := r.CreateRealm(realmReq("..")); !errors.Is(err, errdefs.ErrInvalidRealmName) {
		t.Fatalf("CreateRealm(..) = %v, want ErrInvalidRealmName", err)
	}
	assertTreeUnchanged(t, r.opts.RunPath, before)

	// The state the old create left behind: <RunPath>/metadata.json.
	if err := os.WriteFile(filepath.Join(r.opts.RunPath, "metadata.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, r.opts.RunPath)
	if _, err := r.GetRealm(realmReq("..")); !errors.Is(err, errdefs.ErrInvalidRealmName) {
		t.Fatalf("GetRealm(..) = %v, want ErrInvalidRealmName", err)
	}
	if _, err := r.PurgeRealm(realmReq("..")); !errors.Is(err, errdefs.ErrInvalidRealmName) {
		t.Fatalf("PurgeRealm(..) = %v, want ErrInvalidRealmName", err)
	}
	if err := r.DeleteRealm(realmReq("..")); !errors.Is(err, errdefs.ErrInvalidRealmName) {
		t.Fatalf("DeleteRealm(..) = %v, want ErrInvalidRealmName", err)
	}
	assertTreeUnchanged(t, r.opts.RunPath, before)
}

// TestDeleteConfigTraversal_LeavesTreeIntact reproduces #1719's third Context
// case at the runner: a Config named "../metadata.json" in realm alpha used to
// resolve to data/alpha/metadata.json, so DeleteConfig removed the realm's own
// document. The controller rejects the name first (see the controller test);
// the runner's containment guard refuses it on its own.
func TestDeleteConfigTraversal_LeavesTreeIntact(t *testing.T) {
	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedNameTestTree(t, r)
	before := snapshotTree(t, r.opts.RunPath)

	cfg := intmodel.CellConfig{Metadata: intmodel.CellConfigMetadata{Name: "../metadata.json", Realm: "alpha"}}
	if err := r.DeleteConfig(cfg); !errors.Is(err, errdefs.ErrPathEscapesScope) {
		t.Fatalf("DeleteConfig(../metadata.json) = %v, want ErrPathEscapesScope", err)
	}
	assertTreeUnchanged(t, r.opts.RunPath, before)
	if _, err := os.Stat(fs.RealmMetadataPath(r.opts.RunPath, "alpha")); err != nil {
		t.Fatalf("realm alpha metadata gone: %v", err)
	}
}

// TestContainmentGuard_DestructiveHelpers is #1719's AC that each destructive
// helper refuses a ".." target on its own, calling each one directly so the
// guard, not the name validators in front of it, is what is exercised.
func TestContainmentGuard_DestructiveHelpers(t *testing.T) {
	tests := []struct {
		name string
		fn   func(r *Exec) error
	}{
		{"reclaimScopeMetadata space ..", func(r *Exec) error {
			return r.reclaimScopeMetadata(fs.SpaceMetadataDir(r.opts.RunPath, "alpha", ".."), fs.SpaceDepth)
		}},
		{"reclaimScopeMetadata stack ..", func(r *Exec) error {
			return r.reclaimScopeMetadata(fs.StackMetadataDir(r.opts.RunPath, "alpha", "s", ".."), fs.StackDepth)
		}},
		{"reclaimScopeMetadata realm ..", func(r *Exec) error {
			return r.reclaimScopeMetadata(fs.RealmMetadataDir(r.opts.RunPath, ".."), fs.RealmDepth)
		}},
		{"deleteCell dir ..", func(r *Exec) error {
			return r.removeCellDir(fs.CellMetadataDir(r.opts.RunPath, "alpha", "s", "st", ".."))
		}},
		{"purgeCellMetadata ..", func(r *Exec) error {
			_, err := r.purgeCellMetadata(cellReq("st", ".."))
			return err
		}},
		{"DeleteSecret ..", func(r *Exec) error {
			return r.DeleteSecret(intmodel.Secret{Metadata: intmodel.SecretMetadata{Name: "..", Realm: "alpha"}})
		}},
		{"DeleteSecret space ..", func(r *Exec) error {
			return r.DeleteSecret(intmodel.Secret{Metadata: intmodel.SecretMetadata{
				Name: "metadata.json", Realm: "alpha", Space: "..",
			}})
		}},
		{"DeleteBlueprint ..", func(r *Exec) error {
			return r.DeleteBlueprint(intmodel.CellBlueprint{
				Metadata: intmodel.CellBlueprintMetadata{Name: "../../../metadata.json", Realm: "alpha"},
			})
		}},
		{"DeleteConfig ..", func(r *Exec) error {
			return r.DeleteConfig(
				intmodel.CellConfig{Metadata: intmodel.CellConfigMetadata{Name: "..", Realm: "alpha"}},
			)
		}},
		{"DeleteConfigScheduleStatus ..", func(r *Exec) error {
			return r.DeleteConfigScheduleStatus(intmodel.CellConfig{
				Metadata: intmodel.CellConfigMetadata{Name: "../metadata.json", Realm: "alpha"},
			})
		}},
		{"DeleteVolume ..", func(r *Exec) error {
			return r.DeleteVolume(intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "..", Realm: "alpha"}})
		}},
		{"DeleteTeam ..", func(r *Exec) error {
			return r.DeleteTeam(intmodel.Team{Metadata: intmodel.TeamMetadata{Name: "..", Realm: "alpha"}})
		}},
		{"DeleteSource ..", func(r *Exec) error {
			return r.DeleteSource(intmodel.Source{Metadata: intmodel.TeamMetadata{Name: "..", Realm: "alpha"}})
		}},
		// DeleteTeam/DeleteSource also refuse ".." with their own segment
		// check, so call the guard they share directly as well.
		{"guardKindRemove team realm ..", func(r *Exec) error {
			return r.guardKindRemove(
				fs.TeamsDir(r.opts.RunPath, ".."),
				fs.RealmDepth+1,
				fs.TeamPath(r.opts.RunPath, "..", "x"),
			)
		}},
		{"guardKindRemove source ..", func(r *Exec) error {
			dir := fs.SourcesDir(r.opts.RunPath, "alpha")
			return r.guardKindRemove(dir, fs.RealmDepth+1, filepath.Join(dir, ".."))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
			seedNameTestTree(t, r)
			before := snapshotTree(t, r.opts.RunPath)
			if err := tt.fn(r); err == nil {
				t.Fatalf("%s = nil, want a refusal", tt.name)
			}
			assertTreeUnchanged(t, r.opts.RunPath, before)
		})
	}
}

// TestContainmentGuard_AllowsValidTargets keeps the guard from refusing the
// legitimate removes it sits in front of.
func TestContainmentGuard_AllowsValidTargets(t *testing.T) {
	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	seedNameTestTree(t, r)
	if err := r.removeCellDir(fs.CellMetadataDir(r.opts.RunPath, "alpha", "s", "st", "c")); err != nil {
		t.Fatalf("removeCellDir(valid) = %v", err)
	}
	if _, err := os.Stat(fs.CellMetadataDir(r.opts.RunPath, "alpha", "s", "st", "c")); !os.IsNotExist(err) {
		t.Fatalf("cell dir still present: %v", err)
	}
	if err := r.reclaimScopeMetadata(fs.StackMetadataDir(r.opts.RunPath, "alpha", "s", "st"), fs.StackDepth); err != nil {
		t.Fatalf("reclaimScopeMetadata(valid stack) = %v", err)
	}
	if _, err := os.Stat(fs.SpaceMetadataPath(r.opts.RunPath, "alpha", "s")); err != nil {
		t.Fatalf("space metadata removed with its stack: %v", err)
	}
	for depth, want := range map[int]string{
		kindDirDepth("", "", ""):     filepath.Join(fs.RealmMetadataDir(r.opts.RunPath, "alpha"), "secrets"),
		kindDirDepth("s", "st", "c"): filepath.Join(fs.CellMetadataDir(r.opts.RunPath, "alpha", "s", "st", "c"), "secrets"),
	} {
		if err := fs.EnsureContained(fs.MetadataRoot(r.opts.RunPath), want, depth); err != nil {
			t.Fatalf("kindDirDepth(%d) does not match %s: %v", depth, want, err)
		}
	}
}
