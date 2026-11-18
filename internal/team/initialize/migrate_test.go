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

package initialize

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// teamConfig is a stored CellConfig labelled with team.
func teamConfig(name, team string) api.CellConfigDoc {
	return api.CellConfigDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellConfig,
		Metadata: api.CellConfigMetadata{
			Name: name, Realm: "default", Space: "default", Stack: "default",
			Labels: map[string]string{api.LabelTeam: team, api.LabelRole: "majordomo"},
		},
	}
}

func writeEntry(t *testing.T, path string, entry config.TeamEntry) []byte {
	t.Helper()
	raw, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

// legacyEntry is the init team as registered before #1527: the majordomo
// declaration under the kukeon name.
func legacyEntry() config.TeamEntry {
	return config.TeamEntry{
		APIVersion: config.APIVersion, Kind: config.KindTeamEntry,
		Metadata: config.Metadata{Name: legacyName}, Spec: config.Majordomo().Spec,
	}
}

func legacyConfigs() []api.CellConfigDoc {
	return []api.CellConfigDoc{
		teamConfig("kukeon-majordomo-codex", legacyName),
		teamConfig("kukeon-majordomo-claude", legacyName),
		teamConfig("kukeon-majordomo-claude-health-sweep", legacyName),
		teamConfig("platform-dev-codex", "platform"),
	}
}

// TestForceMigratesTheLegacyInitTeam: init --force on a host initialized under
// the kukeon name removes that team's configs and entry, and registers kuke.
func TestForceMigratesTheLegacyInitTeam(t *testing.T) {
	opts := setupRun(t)
	fake := opts.Client.(*fakeClient)
	fake.configs = legacyConfigs()
	legacyPath := filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")
	writeEntry(t, legacyPath, legacyEntry())

	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fake.team != "kuke" || result.EntryPath != filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml") {
		t.Fatalf("team=%q entry=%s", fake.team, result.EntryPath)
	}
	slices.Sort(fake.deleted)
	want := []string{"kukeon-majordomo-claude", "kukeon-majordomo-claude-health-sweep", "kukeon-majordomo-codex"}
	if !slices.Equal(fake.deleted, want) {
		t.Fatalf("deleted %v, want %v", fake.deleted, want)
	}
	var names []string
	for _, cfg := range fake.configs {
		names = append(names, cfg.Metadata.Name)
		if cfg.Metadata.Labels[api.LabelTeam] == legacyName {
			t.Fatalf("legacy config %s survived", cfg.Metadata.Name)
		}
	}
	if !slices.Contains(names, "platform-dev-codex") || !slices.Contains(names, "kuke-majordomo-codex") {
		t.Fatalf("configs after migration: %v", names)
	}
	if _, err = os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy entry kept: %v", err)
	}
	if _, err = os.Stat(result.EntryPath); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(result.Notes, func(n string) bool {
		return strings.Contains(n, "migrated the init team from kukeon to kuke") && strings.Contains(n, "Log in")
	}) {
		t.Fatalf("no migration note: %v", result.Notes)
	}
}

// TestForceLeavesAProjectTeamNamedKukeon: a kukeon.yaml holding any other
// declaration is a project team (kukeon's own) and is never migrated.
func TestForceLeavesAProjectTeamNamedKukeon(t *testing.T) {
	project := legacyEntry()
	project.ProjectSource = &config.Source{
		Name: "project", URL: "git@github.com:eminwux/kukeon.git", Branch: "main", ClonePath: "/home/user/kukeon",
	}
	project.Spec = config.Defaults{}
	edited := legacyEntry()
	edited.Spec.Roles = append(edited.Spec.Roles, config.Role{Name: "dev", Source: "kukeon", Path: "team/roles/dev"})
	for name, entry := range map[string]config.TeamEntry{"project": project, "extra-role": edited} {
		t.Run(name, func(t *testing.T) {
			opts := setupRun(t)
			fake := opts.Client.(*fakeClient)
			fake.configs = legacyConfigs()
			path := filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")
			before := writeEntry(t, path, entry)
			result, err := Run(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("project entry changed: %v", err)
			}
			if len(fake.deleted) != 0 || fake.team != "kuke" {
				t.Fatalf("deleted=%v team=%q", fake.deleted, fake.team)
			}
			for _, note := range result.Notes {
				if strings.Contains(note, "migrated") {
					t.Fatalf("unexpected migration note: %s", note)
				}
			}
		})
	}
}

// TestFailedMigrationKeepsTheLegacyEntry: the legacy entry goes only after the
// kuke team is applied and registered, so rerunning --force converges.
func TestFailedMigrationKeepsTheLegacyEntry(t *testing.T) {
	opts := setupRun(t)
	fake := opts.Client.(*fakeClient)
	fake.configs = legacyConfigs()
	fake.applyErr = errors.New("daemon refused")
	legacyPath := filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")
	before := writeEntry(t, legacyPath, legacyEntry())
	if _, err := Run(t.Context(), opts); err == nil {
		t.Fatal("apply failure accepted")
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("legacy entry changed on failure: %v", err)
	}
	if _, err = os.Stat(filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("kuke registered despite failure: %v", err)
	}
	fake.applyErr = nil
	if _, err = Run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rerun did not finish the migration: %v", err)
	}
}

// TestIsLegacyEntry: only the init declaration under the kukeon name, from
// either role era, counts as legacy; an unreadable file is left alone.
func TestIsLegacyEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeon.yaml")
	if got, err := isLegacyEntry(path); got || err != nil {
		t.Fatalf("absent: %v %v", got, err)
	}
	writeEntry(t, path, legacyEntry())
	if got, err := isLegacyEntry(path); !got || err != nil {
		t.Fatalf("legacy: %v %v", got, err)
	}
	operator := legacyEntry()
	operator.Spec.Roles[0].Name = "operator"
	writeEntry(t, path, operator)
	if got, err := isLegacyEntry(path); !got || err != nil {
		t.Fatalf("operator-era legacy: %v %v", got, err)
	}
	if err := os.WriteFile(path, []byte("[not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := isLegacyEntry(path); got || err != nil {
		t.Fatalf("malformed: %v %v", got, err)
	}
}
