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

//nolint:testpackage // tests the unexported WriteBlueprint path against a temp RunPath
package runner

import (
	"errors"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestWriteBlueprint_CreatesWorldReadableFile pins the issue #620 storage
// contract: the document lands at <RunPath>/data/<scope>/blueprints/<name>,
// the file is 0o644 and the blueprints/ dir is 0o755 (world-readable, unlike
// the root-only 0o700/0o600 secrets path), and the first write reports
// created=true.
func TestWriteBlueprint_CreatesWorldReadableFile(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	bp := intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default"},
		Document: []byte("apiVersion: v1beta1\nkind: CellBlueprint\n"),
	}

	created, err := r.WriteBlueprint(bp)
	if err != nil {
		t.Fatalf("WriteBlueprint() error = %v", err)
	}
	if !created {
		t.Errorf("created = false, want true on first write")
	}

	path := fs.BlueprintPath(runPath, "default", "", "", "web")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written blueprint: %v", err)
	}
	if string(got) != string(bp.Document) {
		t.Errorf("blueprint bytes = %q, want %q", got, bp.Document)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat blueprint file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o644 {
		t.Errorf("blueprint file mode = %o, want 644 (world-readable)", perm)
	}

	dirInfo, err := os.Stat(fs.BlueprintsDir(runPath, "default", "", ""))
	if err != nil {
		t.Fatalf("stat blueprints dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o755 {
		t.Errorf("blueprints dir mode = %o, want 755 (world-readable)", perm)
	}
}

// TestWriteBlueprint_OverwriteReportsUpdated confirms a re-apply overwrites the
// document and reports created=false.
func TestWriteBlueprint_OverwriteReportsUpdated(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	bp := intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default"},
		Document: []byte("v1"),
	}
	if _, err := r.WriteBlueprint(bp); err != nil {
		t.Fatalf("first WriteBlueprint() error = %v", err)
	}

	bp.Document = []byte("v2")
	created, err := r.WriteBlueprint(bp)
	if err != nil {
		t.Fatalf("second WriteBlueprint() error = %v", err)
	}
	if created {
		t.Errorf("created = true, want false on overwrite")
	}

	got, err := os.ReadFile(fs.BlueprintPath(runPath, "default", "", "", "web"))
	if err != nil {
		t.Fatalf("reading overwritten blueprint: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("blueprint bytes = %q, want v2", got)
	}
}

// TestGetBlueprint_ReturnsFullDocument confirms GetBlueprint reads the full
// document back (unlike GetSecret, which is metadata-only) and reports
// ErrBlueprintNotFound for an absent name.
func TestGetBlueprint_ReturnsFullDocument(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	stored := intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default", Space: "team-a"},
		Document: []byte("the-template-body"),
	}
	if _, err := r.WriteBlueprint(stored); err != nil {
		t.Fatalf("WriteBlueprint() error = %v", err)
	}

	got, err := r.GetBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default", Space: "team-a"},
	})
	if err != nil {
		t.Fatalf("GetBlueprint() error = %v", err)
	}
	if string(got.Document) != "the-template-body" {
		t.Errorf("Document = %q, want the-template-body (full body must round-trip)", got.Document)
	}
}

func TestGetBlueprint_NotFound(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	_, err := r.GetBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "ghost", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrBlueprintNotFound) {
		t.Errorf("GetBlueprint() error = %v, want ErrBlueprintNotFound", err)
	}
}

// TestWriteBlueprint_DeeperScopeNestsUnderScopeDir confirms a space-scoped
// blueprint lands under the space metadata dir, not the realm dir.
func TestWriteBlueprint_DeeperScopeNestsUnderScopeDir(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	bp := intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default", Space: "team-a"},
		Document: []byte("x"),
	}
	if _, err := r.WriteBlueprint(bp); err != nil {
		t.Fatalf("WriteBlueprint() error = %v", err)
	}

	spaceScoped := fs.BlueprintPath(runPath, "default", "team-a", "", "web")
	if _, err := os.Stat(spaceScoped); err != nil {
		t.Errorf("space-scoped blueprint not found at %s: %v", spaceScoped, err)
	}
	realmScoped := fs.BlueprintPath(runPath, "default", "", "", "web")
	if _, err := os.Stat(realmScoped); !os.IsNotExist(err) {
		t.Errorf("blueprint leaked into realm scope at %s (err=%v)", realmScoped, err)
	}
}

// blueprintScopeKey is a stable identity for a listed blueprint, used to
// compare ListBlueprints output independent of walk order.
func blueprintScopeKey(bp intmodel.CellBlueprint) string {
	return bp.Metadata.Realm + "/" + bp.Metadata.Space + "/" + bp.Metadata.Stack + "/" + bp.Metadata.Name
}

func listedKeys(t *testing.T, r *Exec, realm, space, stack string) []string {
	t.Helper()
	got, err := r.ListBlueprints(realm, space, stack)
	if err != nil {
		t.Fatalf("ListBlueprints(%q,%q,%q) error = %v", realm, space, stack, err)
	}
	keys := make([]string, 0, len(got))
	for _, bp := range got {
		keys = append(keys, blueprintScopeKey(bp))
	}
	sort.Strings(keys)
	return keys
}

// seedBlueprint writes a metadata-only blueprint at the given scope for list/
// delete walk tests; the document body is irrelevant to those paths.
func seedBlueprint(t *testing.T, r *Exec, name, realm, space, stack string) {
	t.Helper()
	bp := intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: name, Realm: realm, Space: space, Stack: stack},
		Document: []byte("x"),
	}
	if _, err := r.WriteBlueprint(bp); err != nil {
		t.Fatalf("seed WriteBlueprint(%q @ %s/%s/%s) error = %v", name, realm, space, stack, err)
	}
}

// TestListBlueprints_SubtreeFilterSemantics pins the issue #643 list contract:
// an empty filter lists the whole subtree, a realm filter scopes to that realm,
// and a deeper coordinate excludes shallower scopes — mirroring ListSecrets but
// bounded at stack (a Blueprint is never cell-scoped).
func TestListBlueprints_SubtreeFilterSemantics(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	seedBlueprint(t, r, "realm-bp", "default", "", "")
	seedBlueprint(t, r, "space-bp", "default", "team-a", "")
	seedBlueprint(t, r, "stack-bp", "default", "team-a", "web")
	seedBlueprint(t, r, "other-realm-bp", "prod", "", "")

	all := listedKeys(t, r, "", "", "")
	wantAll := []string{
		"default///realm-bp",
		"default/team-a//space-bp",
		"default/team-a/web/stack-bp",
		"prod///other-realm-bp",
	}
	if !equalStrings(all, wantAll) {
		t.Errorf("ListBlueprints(all) = %v, want %v", all, wantAll)
	}

	realmFiltered := listedKeys(t, r, "default", "", "")
	wantRealm := []string{
		"default///realm-bp",
		"default/team-a//space-bp",
		"default/team-a/web/stack-bp",
	}
	if !equalStrings(realmFiltered, wantRealm) {
		t.Errorf("ListBlueprints(default) = %v, want %v", realmFiltered, wantRealm)
	}

	// A set space coordinate excludes the realm-scoped blueprint.
	spaceFiltered := listedKeys(t, r, "default", "team-a", "")
	wantSpace := []string{
		"default/team-a//space-bp",
		"default/team-a/web/stack-bp",
	}
	if !equalStrings(spaceFiltered, wantSpace) {
		t.Errorf("ListBlueprints(default,team-a) = %v, want %v", spaceFiltered, wantSpace)
	}

	// A set stack coordinate excludes the realm- and space-scoped blueprints.
	stackFiltered := listedKeys(t, r, "default", "team-a", "web")
	wantStack := []string{"default/team-a/web/stack-bp"}
	if !equalStrings(stackFiltered, wantStack) {
		t.Errorf("ListBlueprints(default,team-a,web) = %v, want %v", stackFiltered, wantStack)
	}
}

// TestListBlueprints_IgnoresReservedSubdirs confirms the walk never mistakes a
// sibling secrets/ or blueprints/ reserved subdirectory for a child space or
// stack, and that an in-flight temp file is skipped.
func TestListBlueprints_IgnoresReservedSubdirs(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	seedBlueprint(t, r, "realm-bp", "default", "", "")
	// A realm-scoped secret creates default/secrets/; a realm-scoped blueprint
	// already created default/blueprints/. Neither must surface as a space.
	if _, err := r.WriteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
		Spec:     intmodel.SecretSpec{Data: "v"},
	}); err != nil {
		t.Fatalf("seed WriteSecret error = %v", err)
	}
	// Drop an in-flight temp file alongside the realm blueprint; it must be
	// skipped, not surfaced as a blueprint named ".blueprint-xyz.tmp".
	tmpPath := fs.BlueprintPath(runPath, "default", "", "", ".blueprint-xyz.tmp")
	if err := os.WriteFile(tmpPath, []byte("partial"), 0o644); err != nil {
		t.Fatalf("seed temp file error = %v", err)
	}

	got := listedKeys(t, r, "", "", "")
	want := []string{"default///realm-bp"}
	if !equalStrings(got, want) {
		t.Errorf("ListBlueprints(all) = %v, want %v (reserved subdirs + temp file must be ignored)", got, want)
	}
}

func TestDeleteBlueprint_RemovesFile(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	seedBlueprint(t, r, "web", "default", "team-a", "")
	path := fs.BlueprintPath(runPath, "default", "team-a", "", "web")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("precondition: blueprint not seeded: %v", err)
	}

	if err := r.DeleteBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default", Space: "team-a"},
	}); err != nil {
		t.Fatalf("DeleteBlueprint() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("blueprint file still present after delete (err=%v)", err)
	}
}

func TestDeleteBlueprint_NotFound(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	err := r.DeleteBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "ghost", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrBlueprintNotFound) {
		t.Errorf("DeleteBlueprint() error = %v, want ErrBlueprintNotFound", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
