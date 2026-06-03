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

package teamhost_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/teamhost"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"gopkg.in/yaml.v3"
)

func globalConfig() *model.TeamsConfig {
	return &model.TeamsConfig{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindTeamsConfig,
		Spec:       model.TeamsConfigSpec{Registry: "registry.example.com"},
	}
}

func teamEntry(name string) *model.TeamEntry {
	return &model.TeamEntry{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindTeamEntry,
		Metadata:   model.Metadata{Name: name},
		Spec:       model.TeamEntrySpec{Path: "/home/op/src/" + name, Source: "eminwux/agents@v1.4.0"},
	}
}

func TestEnsureGlobalConfigScaffoldsThenSkips(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	created, err := teamhost.EnsureGlobalConfig(l, globalConfig())
	if err != nil {
		t.Fatalf("first EnsureGlobalConfig: %v", err)
	}
	if !created {
		t.Fatalf("first call created = false, want true")
	}
	if _, statErr := os.Stat(l.GlobalConfigPath()); statErr != nil {
		t.Fatalf("global config not written: %v", statErr)
	}

	// Mutate on disk, then re-run: a re-run must NOT overwrite existing content.
	if writeErr := os.WriteFile(l.GlobalConfigPath(), []byte("sentinel: kept\n"), 0o600); writeErr != nil {
		t.Fatalf("seed sentinel: %v", writeErr)
	}
	created, err = teamhost.EnsureGlobalConfig(l, globalConfig())
	if err != nil {
		t.Fatalf("second EnsureGlobalConfig: %v", err)
	}
	if created {
		t.Errorf("second call created = true, want false (file already present)")
	}
	got, readErr := os.ReadFile(l.GlobalConfigPath())
	if readErr != nil {
		t.Fatalf("read after second call: %v", readErr)
	}
	if string(got) != "sentinel: kept\n" {
		t.Errorf("re-run overwrote existing global config: %q", got)
	}
}

func TestWriteEntryCreatesDirAndFileWithPerms(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	if err := teamhost.WriteEntry(l, teamEntry("sbsh")); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}

	dirInfo, err := os.Stat(l.DropInDir())
	if err != nil {
		t.Fatalf("drop-in dir not created: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("drop-in dir perm = %o, want 700", perm)
	}

	fileInfo, err := os.Stat(l.EntryPath("sbsh"))
	if err != nil {
		t.Fatalf("entry file not created: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("entry file perm = %o, want 600", perm)
	}

	raw, err := os.ReadFile(l.EntryPath("sbsh"))
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	var got model.TeamEntry
	if unmarshalErr := yaml.Unmarshal(raw, &got); unmarshalErr != nil {
		t.Fatalf("entry does not round-trip: %v", unmarshalErr)
	}
	if got.Kind != model.KindTeamEntry || got.Metadata.Name != "sbsh" ||
		got.Spec.Source != "eminwux/agents@v1.4.0" {
		t.Errorf("entry content lost on disk: %+v", got)
	}
}

// TestWriteEntryIsolatesProjects is the AC's failure-isolation guarantee: each
// project owns one file, so rewriting one or removing one leaves the others
// untouched.
func TestWriteEntryIsolatesProjects(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	if err := teamhost.WriteEntry(l, teamEntry("alpha")); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	if err := teamhost.WriteEntry(l, teamEntry("beta")); err != nil {
		t.Fatalf("write beta: %v", err)
	}

	// Rewrite alpha; beta's file must be byte-identical afterwards.
	betaBefore, err := os.ReadFile(l.EntryPath("beta"))
	if err != nil {
		t.Fatalf("read beta before: %v", err)
	}
	if rwErr := teamhost.WriteEntry(l, teamEntry("alpha")); rwErr != nil {
		t.Fatalf("rewrite alpha: %v", rwErr)
	}
	betaAfter, err := os.ReadFile(l.EntryPath("beta"))
	if err != nil {
		t.Fatalf("read beta after: %v", err)
	}
	if string(betaBefore) != string(betaAfter) {
		t.Errorf("rewriting alpha disturbed beta: before=%q after=%q", betaBefore, betaAfter)
	}

	// Removing alpha leaves beta intact.
	if rmErr := os.Remove(l.EntryPath("alpha")); rmErr != nil {
		t.Fatalf("remove alpha: %v", rmErr)
	}
	if _, statErr := os.Stat(l.EntryPath("beta")); statErr != nil {
		t.Errorf("removing alpha affected beta: %v", statErr)
	}
}

func TestWriteEntryEmptyNameErrors(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	err := teamhost.WriteEntry(l, teamEntry("   "))
	if !errors.Is(err, errdefs.ErrTeamEntryNameRequired) {
		t.Errorf("err = %v, want ErrTeamEntryNameRequired", err)
	}
}

// TestWriteEntryUnsafeNameRefused is the defense-in-depth guard against path
// traversal: the parser already rejects unsafe metadata.name values, but a
// caller building a TeamEntry directly (no parser hop) must not be able to
// escape the drop-in directory and clobber the operator's global facts file.
func TestWriteEntryUnsafeNameRefused(t *testing.T) {
	t.Parallel()
	base := filepath.Join(t.TempDir(), ".kuke")
	l := teamhost.NewLayout(base)

	// Pre-seed the global facts file so the traversal target would clobber a
	// real, distinguishable byte sequence on a successful escape.
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	const sentinel = "sentinel: kept\n"
	if err := os.WriteFile(l.GlobalConfigPath(), []byte(sentinel), 0o600); err != nil {
		t.Fatalf("seed global: %v", err)
	}

	cases := []string{"../kuketeams", "a/b", "a\\b", ".hidden", ".."}
	for _, name := range cases {
		err := teamhost.WriteEntry(l, teamEntry(name))
		if !errors.Is(err, errdefs.ErrTeamMetadataNameUnsafe) {
			t.Errorf("WriteEntry(%q) err = %v, want ErrTeamMetadataNameUnsafe", name, err)
		}
	}

	got, err := os.ReadFile(l.GlobalConfigPath())
	if err != nil {
		t.Fatalf("read global after refusal: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("global facts file was clobbered: %q", got)
	}
}

func TestLayoutPaths(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout("/base/.kuke")
	if got, want := l.GlobalConfigPath(), "/base/.kuke/kuketeams.yaml"; got != want {
		t.Errorf("GlobalConfigPath = %q, want %q", got, want)
	}
	if got, want := l.DropInDir(), "/base/.kuke/kuketeam.d"; got != want {
		t.Errorf("DropInDir = %q, want %q", got, want)
	}
	if got, want := l.EntryPath("sbsh"), "/base/.kuke/kuketeam.d/sbsh.yaml"; got != want {
		t.Errorf("EntryPath = %q, want %q", got, want)
	}
}
