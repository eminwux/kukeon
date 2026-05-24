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
	"errors"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
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

// TestWriteConfigIfAbsent_CreatesAndRejectsCollision pins the issue #839
// atomic-create-only contract: the first WriteConfigIfAbsent persists the
// document under the same path layout as WriteConfig, and a second call to
// the same name returns errdefs.ErrConfigExists without overwriting the
// stored body. The `kuke run <src> --clone` gap-fill counter loop relies on
// the EEXIST sentinel to retry on the next free N.
func TestWriteConfigIfAbsent_CreatesAndRejectsCollision(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	first := intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "kukeon-dev-0", Realm: "kuke-system"},
		Document: []byte("first"),
	}
	if err := r.WriteConfigIfAbsent(first); err != nil {
		t.Fatalf("first WriteConfigIfAbsent() error = %v", err)
	}

	path := fs.ConfigPath(runPath, "kuke-system", "", "", "kukeon-dev-0")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written config: %v", err)
	}
	if string(got) != "first" {
		t.Errorf("config bytes = %q, want first", got)
	}

	// A second WriteConfigIfAbsent must NOT overwrite — the AC's concurrency
	// guarantee depends on this.
	second := intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "kukeon-dev-0", Realm: "kuke-system"},
		Document: []byte("second"),
	}
	err = r.WriteConfigIfAbsent(second)
	if !errors.Is(err, errdefs.ErrConfigExists) {
		t.Fatalf("second WriteConfigIfAbsent() error = %v, want ErrConfigExists", err)
	}

	// Stored bytes must be unchanged.
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config after collision: %v", err)
	}
	if string(got) != "first" {
		t.Errorf("config bytes after collision = %q, want first (must not overwrite)", got)
	}
}

// TestWriteConfigIfAbsent_LeavesNoTempFiles confirms the temp file written
// inside ConfigsDir is cleaned up on both success and EEXIST paths — a leak
// would surface as a `.config-*.tmp` entry that ListConfigs already skips
// but is still a sign of broken hygiene.
func TestWriteConfigIfAbsent_LeavesNoTempFiles(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	cfg := intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "alpha", Realm: "kuke-system"},
		Document: []byte("x"),
	}
	if err := r.WriteConfigIfAbsent(cfg); err != nil {
		t.Fatalf("WriteConfigIfAbsent (success) error = %v", err)
	}
	if err := r.WriteConfigIfAbsent(cfg); !errors.Is(err, errdefs.ErrConfigExists) {
		t.Fatalf("WriteConfigIfAbsent (collision) error = %v, want ErrConfigExists", err)
	}

	dir := fs.ConfigsDir(runPath, "kuke-system", "", "")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read configs dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) > len(".config-") && name[:len(".config-")] == ".config-" {
			t.Errorf("temp file leaked: %s", name)
		}
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

// TestGetConfig_ReturnsFullDocument confirms GetConfig reads the full document
// back (like GetBlueprint, a Config carries no credential bytes) and reports
// ErrConfigNotFound for an absent name.
func TestGetConfig_ReturnsFullDocument(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	stored := intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "web", Realm: "default", Space: "team-a"},
		Document: []byte("the-config-body"),
	}
	if _, err := r.WriteConfig(stored); err != nil {
		t.Fatalf("WriteConfig() error = %v", err)
	}

	got, err := r.GetConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "web", Realm: "default", Space: "team-a"},
	})
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if string(got.Document) != "the-config-body" {
		t.Errorf("Document = %q, want the-config-body (full body must round-trip)", got.Document)
	}
}

func TestGetConfig_NotFound(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	_, err := r.GetConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "ghost", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrConfigNotFound) {
		t.Errorf("GetConfig() error = %v, want ErrConfigNotFound", err)
	}
}

// configScopeKey is a stable identity for a listed config, used to compare
// ListConfigs output independent of walk order.
func configScopeKey(c intmodel.CellConfig) string {
	return c.Metadata.Realm + "/" + c.Metadata.Space + "/" + c.Metadata.Stack + "/" + c.Metadata.Name
}

func listedConfigKeys(t *testing.T, r *Exec, realm, space, stack string) []string {
	t.Helper()
	got, err := r.ListConfigs(realm, space, stack)
	if err != nil {
		t.Fatalf("ListConfigs(%q,%q,%q) error = %v", realm, space, stack, err)
	}
	keys := make([]string, 0, len(got))
	for _, c := range got {
		keys = append(keys, configScopeKey(c))
	}
	sort.Strings(keys)
	return keys
}

// seedConfig writes a metadata-only config at the given scope for list/delete
// walk tests; the document body is irrelevant to those paths.
func seedConfig(t *testing.T, r *Exec, name, realm, space, stack string) {
	t.Helper()
	cfg := intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: name, Realm: realm, Space: space, Stack: stack},
		Document: []byte("x"),
	}
	if _, err := r.WriteConfig(cfg); err != nil {
		t.Fatalf("seed WriteConfig(%q @ %s/%s/%s) error = %v", name, realm, space, stack, err)
	}
}

// TestListConfigs_SubtreeFilterSemantics pins the issue #644 list contract: an
// empty filter lists the whole subtree, a realm filter scopes to that realm,
// and a deeper coordinate excludes shallower scopes — mirroring ListBlueprints,
// bounded at stack (a Config is never cell-scoped).
func TestListConfigs_SubtreeFilterSemantics(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	seedConfig(t, r, "realm-cfg", "default", "", "")
	seedConfig(t, r, "space-cfg", "default", "team-a", "")
	seedConfig(t, r, "stack-cfg", "default", "team-a", "web")
	seedConfig(t, r, "other-realm-cfg", "prod", "", "")

	all := listedConfigKeys(t, r, "", "", "")
	wantAll := []string{
		"default///realm-cfg",
		"default/team-a//space-cfg",
		"default/team-a/web/stack-cfg",
		"prod///other-realm-cfg",
	}
	if !equalStrings(all, wantAll) {
		t.Errorf("ListConfigs(all) = %v, want %v", all, wantAll)
	}

	realmFiltered := listedConfigKeys(t, r, "default", "", "")
	wantRealm := []string{
		"default///realm-cfg",
		"default/team-a//space-cfg",
		"default/team-a/web/stack-cfg",
	}
	if !equalStrings(realmFiltered, wantRealm) {
		t.Errorf("ListConfigs(default) = %v, want %v", realmFiltered, wantRealm)
	}

	spaceFiltered := listedConfigKeys(t, r, "default", "team-a", "")
	wantSpace := []string{
		"default/team-a//space-cfg",
		"default/team-a/web/stack-cfg",
	}
	if !equalStrings(spaceFiltered, wantSpace) {
		t.Errorf("ListConfigs(default,team-a) = %v, want %v", spaceFiltered, wantSpace)
	}

	stackFiltered := listedConfigKeys(t, r, "default", "team-a", "web")
	wantStack := []string{"default/team-a/web/stack-cfg"}
	if !equalStrings(stackFiltered, wantStack) {
		t.Errorf("ListConfigs(default,team-a,web) = %v, want %v", stackFiltered, wantStack)
	}
}

// TestListConfigs_IgnoresReservedSubdirs confirms the walk never mistakes a
// sibling secrets/, blueprints/, or configs/ reserved subdirectory for a child
// space or stack, and that an in-flight temp file is skipped. The configs/
// exclusion is the case the blueprint walker misses (it predates #624's configs
// subdir); listing configs across the realm subtree must not recurse into the
// realm's own configs/ dir as a phantom space.
func TestListConfigs_IgnoresReservedSubdirs(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	seedConfig(t, r, "realm-cfg", "default", "", "")
	// A realm-scoped secret creates default/secrets/; a realm-scoped blueprint
	// creates default/blueprints/; the seeded config already created
	// default/configs/. None must surface as a child space.
	if _, err := r.WriteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
		Spec:     intmodel.SecretSpec{Data: "v"},
	}); err != nil {
		t.Fatalf("seed WriteSecret error = %v", err)
	}
	seedBlueprint(t, r, "bp", "default", "", "")
	// Drop an in-flight temp file alongside the realm config; it must be
	// skipped, not surfaced as a config named ".config-xyz.tmp".
	tmpPath := fs.ConfigPath(runPath, "default", "", "", ".config-xyz.tmp")
	if err := os.WriteFile(tmpPath, []byte("partial"), 0o644); err != nil {
		t.Fatalf("seed temp file error = %v", err)
	}

	got := listedConfigKeys(t, r, "", "", "")
	want := []string{"default///realm-cfg"}
	if !equalStrings(got, want) {
		t.Errorf("ListConfigs(all) = %v, want %v (reserved subdirs + temp file must be ignored)", got, want)
	}
}

func TestDeleteConfig_RemovesFile(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	seedConfig(t, r, "web", "default", "team-a", "")
	path := fs.ConfigPath(runPath, "default", "team-a", "", "web")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("precondition: config not seeded: %v", err)
	}

	if err := r.DeleteConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "web", Realm: "default", Space: "team-a"},
	}); err != nil {
		t.Fatalf("DeleteConfig() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config file still present after delete (err=%v)", err)
	}
}

func TestDeleteConfig_NotFound(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	err := r.DeleteConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "ghost", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrConfigNotFound) {
		t.Errorf("DeleteConfig() error = %v, want ErrConfigNotFound", err)
	}
}
