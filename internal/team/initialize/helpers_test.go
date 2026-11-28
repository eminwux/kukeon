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
	"context"
	"io/fs"
	"testing"

	"github.com/eminwux/kukeon/internal/team/teamtest"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// sourceFS is the repository's team definitions with the majordomo role made
// plain (no host mounts, no task), so these tests count only what they add.
func sourceFS() map[string]fs.FS {
	source, err := teamtest.PlainSource("../../..")
	if err != nil {
		panic(err)
	}
	return map[string]fs.FS{"kukeon": source}
}

// inheritingTeam is a team whose declaration omits every list.
func inheritingTeam(name string) *config.Team {
	return &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: name, Realm: config.DefaultRealm},
	}
}

// applied is what applying a composition reported.
type applied struct {
	resources int
	missing   []string
	notes     []string
	warnings  []string
}

// apply runs the preflight and the commit of one composition, the part of a
// team operation after compose.
func apply(ctx context.Context, client Client, result *compose.Result) (applied, error) {
	p := &Plan{Command: "kuke team init", Team: result.Team, composed: result}
	if err := preflight(ctx, client, p); err != nil {
		return applied{}, err
	}
	res, err := commit(ctx, client, p)
	return applied{
		resources: res.Resources, missing: res.MissingSecrets, notes: res.Notes, warnings: res.Warnings,
	}, err
}

// applyMissing applies a composition and returns the missing-secret lines.
func applyMissing(ctx context.Context, client Client, result *compose.Result) ([]string, error) {
	out, err := apply(ctx, client, result)
	return out.missing, err
}

// materialize fetches the sources a composition of d reads.
func materialize(ctx context.Context, d config.Defaults) (map[string]fs.FS, func(), error) {
	return materializeSources(ctx, d, false)
}

func materializeSources(ctx context.Context, d config.Defaults, project bool) (map[string]fs.FS, func(), error) {
	got, err := fetchSources(ctx, noIdentity, d, neededSources(d, project), nil)
	if err != nil {
		return nil, nil, err
	}
	return got.trees, got.cleanup, nil
}

// storeDefaults stores cfg as the realm default's TeamDefaults in f, in the
// shape kukeond stores since #1643: the sources it lists are stored as
// Sources (unless one of the name is) and the list is cleared.
func storeDefaults(t *testing.T, f *fakeClient, cfg *config.TeamDefaults) {
	t.Helper()
	if cfg.Spec.Defaults.Sources != nil {
		converted := *cfg
		for _, source := range cfg.Spec.Defaults.Sources {
			if _, ok := f.sources[source.Name]; ok {
				continue
			}
			switch {
			case source.URL != "":
				f.storeSource(source.Name, specOf(source))
			case source.Name == config.KukeonSourceName:
				f.storeSource(source.Name, config.KukeonSourceSpec())
			}
		}
		converted.Spec.Defaults.Sources = nil
		cfg = &converted
	}
	storeLegacyDefaults(t, f, cfg)
}

// storeLegacyDefaults stores cfg as it is, sources included: a TeamDefaults
// written before #1643.
func storeLegacyDefaults(t *testing.T, f *fakeClient, cfg *config.TeamDefaults) {
	t.Helper()
	if cfg.Metadata.Name == "" {
		cfg.Metadata = config.Metadata{Name: config.DefaultsName, Realm: config.DefaultRealm}
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.defaults = raw
}

// storedDefaults decodes f's TeamDefaults.
func storedDefaults(t *testing.T, f *fakeClient) *config.TeamDefaults {
	t.Helper()
	cfg, err := config.DecodeStoredDefaults(bytes.NewReader(f.defaults))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// storedSource decodes f's stored Source name, failing when none is stored.
func storedSource(t *testing.T, f *fakeClient, name string) config.SourceSpec {
	t.Helper()
	raw, ok := f.sources[name]
	if !ok {
		t.Fatalf("Source %s is not stored", name)
	}
	var doc api.SourceDoc
	if err := config.DecodeStrict(bytes.NewReader(raw), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Spec
}

// storedSources decodes every Source stored in f.
func storedSources(t *testing.T, f *fakeClient) map[string]config.SourceSpec {
	t.Helper()
	out := map[string]config.SourceSpec{}
	for name := range f.sources {
		out[name] = storedSource(t, f, name)
	}
	return out
}

// projectURL is the address a stored team's project resolves to.
func projectURL(t *testing.T, f *fakeClient, team *config.Team) string {
	t.Helper()
	project := team.Spec.ProjectSource
	if project == nil {
		t.Fatalf("team %s has no project", team.Metadata.Name)
	}
	if project.URL != "" {
		return project.URL
	}
	return storedSource(t, f, storedName(*project, team.Metadata.Name, true)).URL
}

// resolveStored resolves a stored team as composition does: hydrated from
// the stored Sources over the stored defaults.
func resolveStored(t *testing.T, f *fakeClient, team *config.Team) (config.Defaults, error) {
	t.Helper()
	defaults, hydrated, err := hydrate(storedDefaults(t, f), team, storedSources(t, f))
	if err != nil {
		return config.Defaults{}, err
	}
	return config.Resolve(defaults, hydrated)
}

// storeTeam stores team in f as kukeond would after a team apply: its
// source definitions stored as Sources and its entries as references. A
// kukeon entry that names no address reads the built-in kukeon Source,
// which is stored when it is not.
func storeTeam(t *testing.T, f *fakeClient, team *config.Team) {
	t.Helper()
	normalized, created, _, err := normalize(team, storedSources(t, f))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range created {
		f.storeSource(source.name, source.spec)
	}
	for _, entry := range normalized.Spec.Sources {
		if storedName(entry, team.Metadata.Name, false) == config.KukeonSourceName {
			if _, ok := f.sources[config.KukeonSourceName]; !ok {
				f.storeSource(config.KukeonSourceName, config.KukeonSourceSpec())
			}
		}
	}
	storeLegacyTeam(t, f, normalized)
}

// storeLegacyTeam stores team as it is: a Team written before #1643, whose
// entries may carry their address.
func storeLegacyTeam(t *testing.T, f *fakeClient, team *config.Team) {
	t.Helper()
	raw, err := yaml.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	f.teams[team.Metadata.Name] = raw
}

// storedTeam decodes the stored Team of name, failing when none is stored.
func storedTeam(t *testing.T, f *fakeClient, name string) *config.Team {
	t.Helper()
	raw, ok := f.teams[name]
	if !ok {
		t.Fatalf("team %s is not stored", name)
	}
	team, err := config.DecodeTeam(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return team
}

// editTeam rewrites the stored Team of name.
func editTeam(t *testing.T, f *fakeClient, name string, edit func(*config.Team)) {
	t.Helper()
	team := storedTeam(t, f, name)
	edit(team)
	storeTeam(t, f, team)
}

// sourceURL is the address of f's stored Source name.
func (f *fakeClient) sourceURL(t *testing.T, name string) string {
	t.Helper()
	return storedSource(t, f, name).URL
}
