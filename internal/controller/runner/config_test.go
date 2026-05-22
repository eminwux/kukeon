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

//nolint:testpackage // tests the unexported WriteConfig path against a temp RunPath
package runner

import (
	"os"
	"testing"
	"time"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestWriteConfig_CreatesWorldReadableFile pins the issue #624 storage contract:
// the document lands at <RunPath>/data/<scope>/configs/<name>, the file is 0o644
// and the configs/ dir is 0o755 (world-readable, like blueprints and unlike the
// root-only secrets path), and the first write reports created=true.
func TestWriteConfig_CreatesWorldReadableFile(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	cfg := intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "kukeon-dev", Realm: "kuke-system"},
		Document: []byte("apiVersion: v1beta1\nkind: CellConfig\n"),
	}

	created, err := r.WriteConfig(cfg)
	if err != nil {
		t.Fatalf("WriteConfig() error = %v", err)
	}
	if !created {
		t.Errorf("created = false, want true on first write")
	}

	path := fs.ConfigPath(runPath, "kuke-system", "", "", "kukeon-dev")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written config: %v", err)
	}
	if string(got) != string(cfg.Document) {
		t.Errorf("config bytes = %q, want %q", got, cfg.Document)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o644 {
		t.Errorf("config file mode = %o, want 644 (world-readable)", perm)
	}

	dirInfo, err := os.Stat(fs.ConfigsDir(runPath, "kuke-system", "", ""))
	if err != nil {
		t.Fatalf("stat configs dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o755 {
		t.Errorf("configs dir mode = %o, want 755 (world-readable)", perm)
	}
}

// TestWriteConfig_OverwriteReportsUpdated confirms a re-apply overwrites the
// document and reports created=false.
func TestWriteConfig_OverwriteReportsUpdated(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	cfg := intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "kukeon-dev", Realm: "kuke-system"},
		Document: []byte("v1"),
	}
	if _, err := r.WriteConfig(cfg); err != nil {
		t.Fatalf("first WriteConfig() error = %v", err)
	}

	cfg.Document = []byte("v2")
	created, err := r.WriteConfig(cfg)
	if err != nil {
		t.Fatalf("second WriteConfig() error = %v", err)
	}
	if created {
		t.Errorf("created = true, want false on overwrite")
	}

	got, err := os.ReadFile(fs.ConfigPath(runPath, "kuke-system", "", "", "kukeon-dev"))
	if err != nil {
		t.Fatalf("reading overwritten config: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("config bytes = %q, want v2", got)
	}
}

// TestWriteConfig_DeeperScopeNestsUnderScopeDir confirms a space-scoped config
// lands under the space metadata dir, not the realm dir.
func TestWriteConfig_DeeperScopeNestsUnderScopeDir(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	cfg := intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "dev", Realm: "kuke-system", Space: "team-a"},
		Document: []byte("x"),
	}
	if _, err := r.WriteConfig(cfg); err != nil {
		t.Fatalf("WriteConfig() error = %v", err)
	}

	spaceScoped := fs.ConfigPath(runPath, "kuke-system", "team-a", "", "dev")
	if _, err := os.Stat(spaceScoped); err != nil {
		t.Errorf("space-scoped config not found at %s: %v", spaceScoped, err)
	}
	realmScoped := fs.ConfigPath(runPath, "kuke-system", "", "", "dev")
	if _, err := os.Stat(realmScoped); !os.IsNotExist(err) {
		t.Errorf("config leaked into realm scope at %s (err=%v)", realmScoped, err)
	}
}
