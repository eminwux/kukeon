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
}

// apply runs the preflight and the commit of one composition, the part of a
// team operation after compose.
func apply(ctx context.Context, client Client, result *compose.Result) (applied, error) {
	p := &Plan{Command: "kuke team init", Team: result.Team, composed: result}
	if err := preflight(ctx, client, p); err != nil {
		return applied{}, err
	}
	res, err := commit(ctx, client, p)
	return applied{resources: res.Resources, missing: res.MissingSecrets, notes: res.Notes}, err
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

// storeDefaults stores cfg as the realm default's TeamDefaults in f.
func storeDefaults(t *testing.T, f *fakeClient, cfg *config.TeamDefaults) {
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
	cfg, err := config.DecodeDefaults(bytes.NewReader(f.defaults))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// storeTeam stores team in f as kukeond would after a team apply.
func storeTeam(t *testing.T, f *fakeClient, team *config.Team) {
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
