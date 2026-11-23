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

package team_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	team "github.com/eminwux/kukeon/cmd/kuke/get/team"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/viper"
)

const alphaDoc = "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: alpha\n  realm: default\nspec:\n  roles: []\n"

type fakeClient struct {
	kukeonv1.FakeClient

	teams    []kukeonv1.GetTeamResult
	gotRef   kukeonv1.TeamRef
	gotRealm string
}

func (f *fakeClient) ListTeams(_ context.Context, realm string) ([]kukeonv1.GetTeamResult, error) {
	f.gotRealm = realm
	return f.teams, nil
}

func (f *fakeClient) GetTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.GetTeamResult, error) {
	f.gotRef = ref
	for _, t := range f.teams {
		if t.Summary.Name == ref.Name && t.Summary.Realm == ref.Realm {
			return t, nil
		}
	}
	return kukeonv1.GetTeamResult{}, nil
}

func stored() *fakeClient {
	return &fakeClient{teams: []kukeonv1.GetTeamResult{
		{
			Exists: true, Document: []byte(alphaDoc),
			Summary: kukeonv1.TeamSummary{
				Name: "alpha", Realm: "default", ProjectURL: "https://example.test/alpha", ProjectRef: "main",
				Roles: []string{"dev", "ops"}, Harnesses: []string{"claude"},
				Configs: []string{"alpha-dev-claude", "alpha-ops-claude"},
			},
		},
		{
			Exists: true, Document: []byte("apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: kuke\n"),
			Summary: kukeonv1.TeamSummary{
				Name: "kuke", Realm: "default", RolesInherited: true, HarnessesInherited: true,
			},
		},
	}}
}

func run(t *testing.T, fake *fakeClient, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(viper.Reset)
	cmd := team.NewTeamCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.WithValue(context.Background(), team.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// TestGetTeams pins AC5's list: realm, project URL and ref, roles and the
// number of Configs.
func TestGetTeams(t *testing.T) {
	fake := stored()
	out, err := run(t, fake)
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotRealm != "" {
		t.Errorf("list without --realm passed realm %q, want every realm", fake.gotRealm)
	}
	for _, want := range []string{
		"NAME", "REALM", "PROJECT", "REF", "ROLES", "CONFIGS",
		"alpha", "https://example.test/alpha", "main", "dev,ops", "(defaults)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 || !strings.HasSuffix(strings.TrimSpace(lines[len(lines)-2]), "2") {
		t.Errorf("alpha's row should count 2 Configs:\n%s", out)
	}

	if _, err = run(t, fake, "--realm", "other"); err != nil || fake.gotRealm != "other" {
		t.Fatalf("--realm not passed: %q, %v", fake.gotRealm, err)
	}
}

func TestGetTeamsEmpty(t *testing.T) {
	out, err := run(t, &fakeClient{})
	if err != nil || !strings.Contains(out, "No teams found.") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestGetTeamsYAMLPrintsStoredDocuments(t *testing.T) {
	out, err := run(t, stored(), "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, alphaDoc) || !strings.Contains(out, "---\n") {
		t.Fatalf("-o yaml should print every stored document:\n%s", out)
	}
}

func TestGetTeam(t *testing.T) {
	fake := stored()
	out, err := run(t, fake, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotRef.Realm != "default" {
		t.Errorf("named get realm = %q, want default", fake.gotRef.Realm)
	}
	for _, want := range []string{
		"Team:      alpha", "Roles:     dev,ops", "Harnesses: claude", "alpha-dev-claude", "alpha-ops-claude",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	out, err = run(t, fake, "kuke")
	if err != nil || !strings.Contains(out, "Roles:     (defaults)") || !strings.Contains(out, "Configs:   none") {
		t.Fatalf("kuke team output = %q, %v", out, err)
	}
}

func TestGetTeamYAMLIsTheStoredDocument(t *testing.T) {
	out, err := run(t, stored(), "alpha", "-o", "yaml")
	if err != nil || out != alphaDoc {
		t.Fatalf("-o yaml = %q, %v; want the stored document byte for byte", out, err)
	}
}

func TestGetTeamJSONIsTheSummary(t *testing.T) {
	out, err := run(t, stored(), "alpha", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var summary kukeonv1.TeamSummary
	if err = json.Unmarshal([]byte(out), &summary); err != nil || summary.Name != "alpha" {
		t.Fatalf("-o json = %q (%v)", out, err)
	}
}

func TestGetTeamNotFound(t *testing.T) {
	_, err := run(t, stored(), "gamma")
	if err == nil || !strings.Contains(err.Error(), `team "gamma" not found`) ||
		!strings.Contains(err.Error(), "kuke get teams") {
		t.Fatalf("err = %v", err)
	}
}
