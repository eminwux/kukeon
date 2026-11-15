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

package config_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

func TestTeamEntryRoundTripsPreserveInheritance(t *testing.T) {
	for _, lists := range []string{"", "  roles: []\n  harnesses: []\n", "  sources: []\n", "  sources:\n    - {name: custom, url: https://example.test/custom, tag: v2, clonePath: /opt/custom}\n"} {
		t.Run(lists, func(t *testing.T) {
			raw := "apiVersion: kuketeams.io/v1\nkind: Team\nmetadata:\n  name: root\nspec:\n" + lists
			if lists == "" {
				raw = "apiVersion: kuketeams.io/v1\nkind: Team\nmetadata:\n  name: root\n"
			}
			team, err := config.DecodeTeam(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			entry, err := config.Entry(team)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := yaml.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := config.DecodeEntry(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(team, decoded.Team()) {
				t.Fatalf("round trip changed selections: %s", encoded)
			}
			if strings.Contains(string(encoded), "image:") {
				t.Fatal("resolved images persisted")
			}
		})
	}
}

func TestEntryCopiesAuthoredImageCapabilities(t *testing.T) {
	team := config.Root()
	team.Spec.Roles = []config.Role{
		{Name: "operator", Source: "kukeon", Path: "team/roles/operator", Needs: &config.Needs{Image: []string{"go"}}},
	}
	entry, err := config.Entry(team)
	if err != nil {
		t.Fatal(err)
	}
	team.Spec.Roles[0].Needs.Image[0] = "root"
	if entry.Spec.Roles[0].Needs.Image[0] != "go" {
		t.Fatal("entry aliases authored declaration")
	}
	raw, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := config.DecodeEntry(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entry, decoded) {
		t.Fatal("authored image requirements lost")
	}
}

func TestStructuralSecretContract(t *testing.T) {
	const base = `apiVersion: kuketeams.io/v1
kind: TeamsConfig
spec:
  defaults: {}
  secrets:
    - name: provider-auth
      envName: PROVIDER_API_KEY
      secretRef: {name: account-key, realm: auth, space: shared, stack: operators}
`
	for _, src := range []string{"", "      source: {env: OPERATOR_PROVIDER_KEY}\n", "      source: {file: /no/such/credential}\n"} {
		cfg, err := config.Decode(strings.NewReader(base + src))
		if err != nil {
			t.Fatal(err)
		}
		var raw bytes.Buffer
		if encodeErr := config.Encode(&raw, cfg); encodeErr != nil {
			t.Fatal(encodeErr)
		}
		decoded, err := config.Decode(&raw)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg, decoded) {
			t.Fatal("secret declarations changed")
		}
	}
	for _, tc := range []struct{ name, raw, want string }{
		{"inline-value", base + "      value: secret-canary\n", "unknown field"},
		{"both-sources", base + "      source: {env: AUTH, file: /auth}\n", "exactly one"},
		{"empty-source", base + "      source: {}\n", "exactly one"},
		{"env-literal", base + "      source: {env: 'literal secret-canary'}\n", "identifier"},
		{"relative-file", base + "      source: {file: relative}\n", "absolute path"},
		{"missing-realm", strings.ReplaceAll(base, "realm: auth, ", ""), "scope"},
		{"missing-parent", strings.ReplaceAll(base, "space: shared, ", ""), "parent"},
		{"missing-destination", strings.ReplaceAll(base, "      envName: PROVIDER_API_KEY\n", ""), "identifier"},
		{"duplicate-name", base + strings.TrimPrefix(base, strings.Split(base, "  secrets:\n")[0]+"  secrets:\n"), "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Decode(strings.NewReader(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v; wanted %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "secret-canary") {
				t.Fatal("credential-shaped value leaked in error")
			}
		})
	}
}

func TestStrictTeamAndEntry(t *testing.T) {
	for _, raw := range []string{
		"apiVersion: kuketeams.io/v1\nkind: Team\nmetadata: {name: root}\nspec: {images: [resolved]}\n",
		"apiVersion: kuketeams.io/v1\nkind: Team\nmetadata: {name: root}\nspec: {roles: null}\n",
		"apiVersion: kuketeams.io/v1\nkind: Team\nmetadata: {name: root}\nspec: {roles: [], roles: []}\n",
		"apiVersion: kuketeams.io/v1\nkind: Team\nmetadata: {name: root}\n---\nkind: Team\n",
	} {
		if _, err := config.DecodeTeam(strings.NewReader(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestEntrySecretBindings(t *testing.T) {
	const head = "apiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata: {name: root}\n"
	raw := head + "secrets:\n  - {name: gh-token, secretRef: {name: gh-token, realm: default}, envName: GH_TOKEN}\nspec: {}\n"
	entry, err := config.DecodeEntry(strings.NewReader(raw))
	if err != nil || len(entry.Secrets) != 1 || entry.Secrets[0].EnvName != "GH_TOKEN" {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	encoded, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	again, err := config.DecodeEntry(bytes.NewReader(encoded))
	if err != nil || !reflect.DeepEqual(again, entry) {
		t.Fatalf("round trip: %v %s", err, encoded)
	}
	for want, raw := range map[string]string{
		"secrets[0].token: unknown field": head +
			"secrets:\n  - {name: gh-token, secretRef: {name: gh-token, realm: default}, envName: GH_TOKEN, token: x}\n",
		"secrets[1].name": head + "secrets:\n" +
			"  - {name: gh-token, secretRef: {name: a, realm: default}, envName: A}\n" +
			"  - {name: gh-token, secretRef: {name: b, realm: default}, envName: B}\n",
		"secrets[0].envName": head + "secrets:\n  - {name: gh-token, secretRef: {name: a, realm: default}, envName: 1A}\n",
	} {
		if _, err = config.DecodeEntry(strings.NewReader(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
}

func TestDefaultSecretBinding(t *testing.T) {
	for name, env := range map[string]string{
		"gh-token": "GH_TOKEN", "openrouter-api-key": "OPENROUTER_API_KEY", "a.b": "A_B",
	} {
		b, err := config.DefaultSecretBinding(name, "default")
		if err != nil || b.EnvName != env || b.Name != name ||
			b.SecretRef != (config.SecretRef{Name: name, Realm: "default"}) || b.Source != nil {
			t.Fatalf("%s: %+v %v", name, b, err)
		}
	}
	if _, err := config.DefaultSecretBinding("1password", "default"); err == nil ||
		!strings.Contains(err.Error(), "bind it explicitly") {
		t.Fatalf("underivable env accepted: %v", err)
	}
}

func TestSourceClonePathValidationAndRoundTrip(t *testing.T) {
	const base = `apiVersion: kuketeams.io/v1
kind: TeamsConfig
spec:
  defaults:
    sources:
      - name: roles
        url: https://example.test/roles
        tag: v2
`
	for _, value := range []string{"", "        clonePath: /opt/roles\n"} {
		cfg, err := config.Decode(strings.NewReader(base + value))
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err = config.Encode(&out, cfg); err != nil {
			t.Fatal(err)
		}
		decoded, err := config.Decode(&out)
		if err != nil || !reflect.DeepEqual(cfg, decoded) {
			t.Fatalf("round trip: %v", err)
		}
	}
	for _, value := range []string{"relative", "/opt/../roles", "/opt/with space", "/"} {
		if _, err := config.Decode(strings.NewReader(base + "        clonePath: " + value + "\n")); err == nil {
			t.Fatalf("accepted clone path %q", value)
		}
	}
	duplicate := base + `        clonePath: /opt/roles
      - name: other
        url: https://example.test/other
        branch: topic
        clonePath: /opt/roles
`
	if _, err := config.Decode(strings.NewReader(duplicate)); err == nil ||
		!strings.Contains(err.Error(), "duplicate clone") {
		t.Fatalf("duplicate destinations: %v", err)
	}
	team, err := config.DecodeTeam(strings.NewReader(`apiVersion: kuketeams.io/v1
kind: Team
metadata: {name: root}
spec:
  sources:
    - {name: roles, url: https://example.test/roles, tag: v2, clonePath: relative}
`))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Decode(strings.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = config.Resolve(cfg, team); err == nil || !strings.Contains(err.Error(), "clonePath") {
		t.Fatalf("effective team clone path not validated: %v", err)
	}
}

func TestProjectSourceIsAdditiveAndRetainsInheritance(t *testing.T) {
	cfg := &config.TeamsConfig{
		APIVersion: config.APIVersion,
		Kind:       config.Kind,
		Spec: config.Spec{
			Defaults: config.Defaults{
				Sources: []config.Source{{Name: "definitions", URL: "https://example.test/defs", Branch: "main"}},
			},
		},
	}
	team := config.Root()
	team.Metadata.Name = "example"
	team.ProjectSource = &config.Source{
		Name:      "project",
		URL:       "git@alias:owner/repo",
		Branch:    "trunk",
		ClonePath: "/home/user/example",
	}
	effective, err := config.Resolve(cfg, team)
	if err != nil || len(effective.Sources) != 2 || team.Spec.Sources != nil {
		t.Fatalf("error=%v", err)
	}
	entry, err := config.Entry(team)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := config.DecodeEntry(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Spec.Defaults.Sources[0].Branch = "next"
	effective, err = config.Resolve(cfg, accepted.Team())
	if err != nil || effective.Sources[0].Branch != "next" || effective.Sources[1].Branch != "trunk" {
		t.Fatal("defaults frozen")
	}
	team.ProjectSource.Branch = "changed"
	if entry.ProjectSource.Branch != "trunk" {
		t.Fatal("project declaration aliased")
	}
	// Authored lists still replace rather than merge the defaults.
	team.Spec.Sources = []config.Source{}
	effective, err = config.Resolve(cfg, team)
	if err != nil || len(effective.Sources) != 1 || effective.Sources[0].Name != "project" {
		t.Fatal("explicit-list replacement changed")
	}
	team.Spec.Sources = []config.Source{{Name: "project", URL: "https://example.test/other", Branch: "main"}}
	if _, err = config.Resolve(cfg, team); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatal("collision silently overwrote source")
	}
}

func TestInitialPathAndHarnessSelection(t *testing.T) {
	cfg, err := config.Decode(strings.NewReader(`apiVersion: kuketeams.io/v1
kind: TeamsConfig
spec:
  defaults:
    sources: [{name: defs, url: github.com/example/defs, branch: main}]
    roles:
      - {name: dev, source: defs, path: dev, initialPath: /home/user/work, harness: claude}
      - {name: pm, source: defs, path: pm, initialPath: $HOME}
    harnesses:
      - {name: codex, source: defs, path: harnesses/codex}
      - {name: claude, source: defs, path: harnesses/claude}
`))
	if err != nil {
		t.Fatal(err)
	}
	team := config.Root()
	inherited, err := config.Resolve(cfg, team)
	if err != nil || len(inherited.Roles) != 2 || inherited.Roles[0].InitialPath != "/home/user/work" ||
		inherited.Roles[1].InitialPath != "$HOME" {
		t.Fatalf("inheritance: %+v, %v", inherited, err)
	}
	for _, value := range []string{"", "$HOME", "$HOME/work", "/opt/work", "/"} {
		team.Spec.Roles = []config.Role{{Name: "other", Source: "defs", Path: "dev", InitialPath: value}}
		resolved, resolveErr := config.Resolve(cfg, team)
		if resolveErr != nil || len(resolved.Roles) != 1 || resolved.Roles[0].InitialPath != value {
			t.Fatalf("replacement: %+v, %v", resolved, resolveErr)
		}
		entry, entryErr := config.Entry(team)
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		raw, marshalErr := yaml.Marshal(entry)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		decoded, decodeErr := config.DecodeEntry(bytes.NewReader(raw))
		if decodeErr != nil || !reflect.DeepEqual(decoded.Team(), team) {
			t.Fatalf("round trip: %s: %v", raw, decodeErr)
		}
		raw, marshalErr = yaml.Marshal(team)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		parsed, parseErr := config.DecodeTeam(bytes.NewReader(raw))
		if parseErr != nil || !reflect.DeepEqual(parsed, team) {
			t.Fatalf("Team decoding: %v", parseErr)
		}
	}
	// A Team list selects both harnesses for roles without an explicit harness.
	team.Spec.Harnesses = []config.Harness{
		{Name: "codex", Source: "defs", Path: "codex"},
		{Name: "claude", Source: "defs", Path: "claude"},
	}
	if _, err = config.Resolve(cfg, team); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"relative", "~", "${HOME}", "$USER", "$HOME/../elsewhere", "/tmp/../work", "/tmp/a b", "/tmp/$(id)", "/tmp/a\n", "$HOME/", " "} {
		team.Spec.Roles[0].InitialPath = value
		if _, err = config.Resolve(cfg, team); err == nil || !strings.Contains(err.Error(), "initialPath") {
			t.Fatalf("accepted invalid path %q: %v", value, err)
		}
	}
	// The removed harness default is rejected everywhere with migration guidance.
	for _, kind := range []string{"TeamsConfig", "Team", "TeamEntry"} {
		for _, value := range []string{"true", "false"} {
			harnesses := "  harnesses: [{name: codex, source: defs, path: codex, default: " + value + "}]\n"
			raw := "apiVersion: kuketeams.io/v1\nkind: " + kind + "\nmetadata: {name: test}\nspec:\n" + harnesses
			switch kind {
			case "TeamsConfig":
				raw = "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec:\n  defaults:\n  " + harnesses
				_, err = config.Decode(strings.NewReader(raw))
			case "Team":
				_, err = config.DecodeTeam(strings.NewReader(raw))
			default:
				_, err = config.DecodeEntry(strings.NewReader(raw))
			}
			if err == nil || !strings.Contains(err.Error(), "harnesses[0].default: removed") ||
				!strings.Contains(err.Error(), "roles[].harness") {
				t.Fatalf("%s accepted harness default: %v", kind, err)
			}
		}
	}
}
