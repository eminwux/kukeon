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

//nolint:testpackage // tests the Source store against a temp RunPath
package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func testSource(realm, name, doc string) intmodel.Source {
	return intmodel.Source{Metadata: intmodel.TeamMetadata{Name: name, Realm: realm}, Document: []byte(doc)}
}

// TestSourceStore_RoundTrip pins the issue #1643 storage contract: a Source
// lands at <RunPath>/data/<realm>/sources/<name>.yaml, reads back byte for
// byte, lists per realm and across realms, and deletes.
func TestSourceStore_RoundTrip(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	doc := "apiVersion: v1beta1\nkind: Source\nmetadata:\n  name: agents\n  realm: default\n"
	created, err := r.WriteSource(testSource("default", "agents", doc))
	if err != nil || !created {
		t.Fatalf("WriteSource() created=%v err=%v, want created", created, err)
	}
	if created, err = r.WriteSource(testSource("default", "agents", doc)); err != nil || created {
		t.Fatalf("second WriteSource() created=%v err=%v, want an update", created, err)
	}
	if _, err = r.WriteSource(testSource("other", "kukeon", "k")); err != nil {
		t.Fatal(err)
	}
	path := fs.SourcePath(runPath, "default", "agents")
	if path != filepath.Join(runPath, "data", "default", "sources", "agents.yaml") {
		t.Fatalf("SourcePath = %q", path)
	}
	if info, statErr := os.Stat(path); statErr != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("source file stat = %v, %v; want 0644", info, statErr)
	}
	got, err := r.GetSource(testSource("default", "agents", ""))
	if err != nil || string(got.Document) != doc {
		t.Fatalf("GetSource() = %q, %v", got.Document, err)
	}
	if err = os.WriteFile(filepath.Join(fs.SourcesDir(runPath, "default"), ".source-x.tmp"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if listed, listErr := r.ListSources("default"); listErr != nil || len(listed) != 1 {
		t.Fatalf("ListSources(default) = %+v, %v", listed, listErr)
	}
	if all, listErr := r.ListSources(""); listErr != nil || len(all) != 2 {
		t.Fatalf("ListSources() = %+v, %v", all, listErr)
	}
	if err = r.DeleteSource(testSource("default", "agents", "")); err != nil {
		t.Fatal(err)
	}
	if _, err = r.GetSource(testSource("default", "agents", "")); !errors.Is(err, errdefs.ErrSourceNotFound) {
		t.Fatalf("GetSource after delete = %v", err)
	}
	if err = r.DeleteSource(testSource("default", "agents", "")); !errors.Is(err, errdefs.ErrSourceNotFound) {
		t.Fatalf("second DeleteSource = %v", err)
	}
	for _, ref := range []struct{ realm, name string }{{"default", "../x"}, {"..", "a"}, {"default", ""}} {
		if _, err = r.WriteSource(testSource(ref.realm, ref.name, "x")); !errors.Is(err, errdefs.ErrInvalidSource) {
			t.Errorf("WriteSource(%q/%q) = %v, want ErrInvalidSource", ref.realm, ref.name, err)
		}
	}
}

// TestSourceStore_RealmPurgeRemovesSources: a realm purge reclaims sources/
// with the rest of the realm's metadata, and sources/ is never a space.
func TestSourceStore_RealmPurgeRemovesSources(t *testing.T) {
	if _, ok := reservedScopeSubdirs[consts.KukeonSourcesSubdir]; !ok {
		t.Fatal("sources is not a reserved scope subdir")
	}
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())
	realmDir := fs.RealmMetadataDir(runPath, "default")
	seedScopeMetadataFile(t, realmDir)
	if _, err := r.WriteSource(testSource("default", "agents", "s")); err != nil {
		t.Fatal(err)
	}
	if err := r.reclaimScopeMetadata(realmDir); err != nil {
		t.Fatalf("reclaimScopeMetadata error = %v", err)
	}
	assertGone(t, fs.SourcesDir(runPath, "default"), "sources dir")
	if listed, err := r.ListSources(""); err != nil || len(listed) != 0 {
		t.Fatalf("ListSources after purge = %+v, %v", listed, err)
	}
}
