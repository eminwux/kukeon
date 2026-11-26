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

func TestTeamRoundTripsPreserveInheritance(t *testing.T) {
	for _, lists := range []string{"", "  roles: []\n  harnesses: []\n", "  sources: []\n", "  connectors: {}\n", "  sources:\n    - {name: custom, url: https://example.test/custom, tag: v2, clonePath: /opt/custom}\n"} {
		t.Run(lists, func(t *testing.T) {
			raw := "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: root\nspec:\n" + lists
			if lists == "" {
				raw = "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: root\n"
			}
			team, err := config.DecodeTeam(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			entry, err := config.Declaration(team)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := yaml.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := config.DecodeTeam(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			// The declaration defaults an omitted realm; nothing else changes.
			team.Metadata.Realm = config.DefaultRealm
			if !reflect.DeepEqual(team, decoded) {
				t.Fatalf("round trip changed selections: %s", encoded)
			}
			if strings.Contains(string(encoded), "image:") {
				t.Fatal("resolved images persisted")
			}
		})
	}
}

func TestEntryCopiesAuthoredImageCapabilities(t *testing.T) {
	team := inheritingTeam()
	team.Spec.Roles = []config.Role{
		{
			Name: "majordomo", Source: "kukeon", Path: "team/roles/majordomo",
			Needs: &config.Needs{Image: []string{"go"}},
		},
	}
	entry, err := config.Declaration(team)
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
	decoded, err := config.DecodeTeam(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entry, decoded) {
		t.Fatal("authored image requirements lost")
	}
}

// A source's own git identity is part of the authored declaration: the entry
// records it without aliasing, it survives the registration round trip, and it
// names secrets only.
func TestEntryCopiesSourceGitIdentity(t *testing.T) {
	deploy := config.SecretRef{Name: "deploy", Realm: "default"}
	identity := func() *config.SourceGit {
		signers := config.SecretRef{Name: "signers", Realm: "default"}
		return &config.SourceGit{
			Author:    &config.Identity{Name: "bot", Email: "bot@example.com"},
			Committer: &config.Identity{Name: "bot", Email: "bot@example.com"},
			Credential: &config.SourceCredential{
				SecretRef:     deploy,
				User:          "git",
				Port:          2222,
				HostKeyPolicy: "accept-new",
			},
			Signing: &config.GitSigning{SecretRef: deploy, AllowedSignersRef: &signers},
			Sign:    []string{"commits"},
		}
	}
	team := inheritingTeam()
	team.Spec.Sources = []config.Source{
		{Name: "kukeon", URL: "https://example.test/roles", Branch: "main", ClonePath: "/opt/roles", Git: identity()},
	}
	team.Spec.ProjectSource = &config.Source{
		Name: "project", URL: "git@example.test:team/project", Branch: "main", ClonePath: "/opt/project", Git: identity(),
	}
	entry, err := config.Declaration(team)
	if err != nil {
		t.Fatal(err)
	}
	for _, authored := range []*config.SourceGit{team.Spec.Sources[0].Git, team.Spec.ProjectSource.Git} {
		authored.Author.Name = "changed"
		authored.Committer.Email = "changed"
		authored.Credential.SecretRef.Name = "changed"
		authored.Signing.SecretRef.Name = "changed"
		authored.Signing.AllowedSignersRef.Name = "changed"
		authored.Sign[0] = "tags"
	}
	for _, recorded := range []*config.SourceGit{entry.Spec.Sources[0].Git, entry.Spec.ProjectSource.Git} {
		if !reflect.DeepEqual(recorded, identity()) {
			t.Fatalf("entry aliases the authored source identity: %+v", recorded)
		}
	}
	raw, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := config.DecodeTeam(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entry, decoded) || !reflect.DeepEqual(decoded.Spec.ProjectSource.Git, identity()) {
		t.Fatalf("source identity lost in the registration round trip:\n%s", raw)
	}
	// A source without its own identity records none.
	team.Spec.Sources[0].Git = nil
	if entry, err = config.Declaration(team); err != nil || entry.Spec.Sources[0].Git != nil {
		t.Fatalf("entry invented a source identity: %v", err)
	}
	if raw, err = yaml.Marshal(entry.Spec.Sources[0]); err != nil || bytes.Contains(raw, []byte("git:")) {
		t.Fatalf("empty identity serialized: %s", raw)
	}
}

func TestStructuralSecretContract(t *testing.T) {
	const base = `apiVersion: v1beta1
kind: TeamDefaults
spec:
  defaults: {}
  secrets:
    - name: provider-auth
      envName: PROVIDER_API_KEY
      secretRef: {name: account-key, realm: auth, space: shared, stack: operators}
`
	for _, src := range []string{"", "      source: {env: OPERATOR_PROVIDER_KEY}\n", "      source: {file: /no/such/credential}\n"} {
		cfg, err := config.DecodeDefaults(strings.NewReader(base + src))
		if err != nil {
			t.Fatal(err)
		}
		var raw bytes.Buffer
		if encodeErr := config.Encode(&raw, cfg); encodeErr != nil {
			t.Fatal(encodeErr)
		}
		decoded, err := config.DecodeDefaults(&raw)
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
			_, err := config.DecodeDefaults(strings.NewReader(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v; wanted %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "secret-canary") {
				t.Fatal("credential-shaped value leaked in error")
			}
		})
	}
}

func TestStrictTeam(t *testing.T) {
	for _, raw := range []string{
		"apiVersion: v1beta1\nkind: Team\nmetadata: {name: root}\nspec: {images: [resolved]}\n",
		"apiVersion: v1beta1\nkind: Team\nmetadata: {name: root}\nspec: {roles: null}\n",
		"apiVersion: v1beta1\nkind: Team\nmetadata: {name: root}\nspec: {roles: [], roles: []}\n",
		"apiVersion: v1beta1\nkind: Team\nmetadata: {name: root}\n---\nkind: Team\n",
	} {
		if _, err := config.DecodeTeam(strings.NewReader(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestTeamSecretBindings(t *testing.T) {
	const head = "apiVersion: v1beta1\nkind: Team\nmetadata: {name: root}\nspec:\n"
	raw := head + "  secrets:\n  - {name: gh-token, secretRef: {name: gh-token, realm: default}, envName: GH_TOKEN}\n"
	entry, err := config.DecodeTeam(strings.NewReader(raw))
	if err != nil || len(entry.Spec.Secrets) != 1 || entry.Spec.Secrets[0].EnvName != "GH_TOKEN" {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	encoded, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	again, err := config.DecodeTeam(bytes.NewReader(encoded))
	if err != nil || !reflect.DeepEqual(again, entry) {
		t.Fatalf("round trip: %v %s", err, encoded)
	}
	for want, raw := range map[string]string{
		"secrets[0].token: unknown field": head +
			"  secrets:\n  - {name: gh-token, secretRef: {name: gh-token, realm: default}, envName: GH_TOKEN, token: x}\n",
		"secrets[1].name": head + "  secrets:\n" +
			"  - {name: gh-token, secretRef: {name: a, realm: default}, envName: A}\n" +
			"  - {name: gh-token, secretRef: {name: b, realm: default}, envName: B}\n",
		"secrets[0].envName": head + "  secrets:\n  - {name: gh-token, secretRef: {name: a, realm: default}, envName: 1A}\n",
	} {
		if _, err = config.DecodeTeam(strings.NewReader(raw)); err == nil || !strings.Contains(err.Error(), want) {
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
	const base = `apiVersion: v1beta1
kind: TeamDefaults
spec:
  defaults:
    sources:
      - name: roles
        url: https://example.test/roles
        tag: v2
`
	for _, value := range []string{"", "        clonePath: /opt/roles\n"} {
		cfg, err := config.DecodeDefaults(strings.NewReader(base + value))
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err = config.Encode(&out, cfg); err != nil {
			t.Fatal(err)
		}
		decoded, err := config.DecodeDefaults(&out)
		if err != nil || !reflect.DeepEqual(cfg, decoded) {
			t.Fatalf("round trip: %v", err)
		}
	}
	for _, value := range []string{"relative", "/opt/../roles", "/opt/with space", "/"} {
		if _, err := config.DecodeDefaults(strings.NewReader(base + "        clonePath: " + value + "\n")); err == nil {
			t.Fatalf("accepted clone path %q", value)
		}
	}
	duplicate := base + `        clonePath: /opt/roles
      - name: other
        url: https://example.test/other
        branch: topic
        clonePath: /opt/roles
`
	if _, err := config.DecodeDefaults(strings.NewReader(duplicate)); err == nil ||
		!strings.Contains(err.Error(), "duplicate clone") {
		t.Fatalf("duplicate destinations: %v", err)
	}
	// A team's own sources are checked when the declaration is decoded
	// (#1643): a bad clone path never reaches composition.
	_, err := config.DecodeTeam(strings.NewReader(`apiVersion: v1beta1
kind: Team
metadata: {name: root}
spec:
  sources:
    - {name: roles, url: https://example.test/roles, tag: v2, clonePath: relative}
`))
	if err == nil || !strings.Contains(err.Error(), "spec.sources[0].clonePath") {
		t.Fatalf("team clone path not validated: %v", err)
	}
}

func TestProjectSourceIsAdditiveAndRetainsInheritance(t *testing.T) {
	cfg := &config.TeamDefaults{
		APIVersion: config.APIVersion,
		Kind:       config.KindTeamDefaults,
		Spec: config.Spec{
			Defaults: config.Defaults{
				Sources: []config.Source{{Name: "definitions", URL: "https://example.test/defs", Branch: "main"}},
			},
		},
	}
	team := inheritingTeam()
	team.Metadata.Name = "example"
	team.Spec.ProjectSource = &config.Source{
		Name:      "project",
		URL:       "git@alias:owner/repo",
		Branch:    "trunk",
		ClonePath: "/home/user/example",
	}
	effective, err := config.Resolve(cfg, team)
	if err != nil || len(effective.Sources) != 2 || team.Spec.Sources != nil {
		t.Fatalf("error=%v", err)
	}
	entry, err := config.Declaration(team)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := config.DecodeTeam(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Spec.Defaults.Sources[0].Branch = "next"
	effective, err = config.Resolve(cfg, accepted)
	if err != nil || effective.Sources[0].Branch != "next" || effective.Sources[1].Branch != "trunk" {
		t.Fatal("defaults frozen")
	}
	team.Spec.ProjectSource.Branch = "changed"
	if entry.Spec.ProjectSource.Branch != "trunk" {
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
	cfg, err := config.DecodeDefaults(strings.NewReader(`apiVersion: v1beta1
kind: TeamDefaults
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
	team := inheritingTeam()
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
		entry, entryErr := config.Declaration(team)
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		raw, marshalErr := yaml.Marshal(entry)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		decoded, decodeErr := config.DecodeTeam(bytes.NewReader(raw))
		if decodeErr != nil || !reflect.DeepEqual(decoded, team) {
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
	for _, kind := range []string{"TeamDefaults", "Team"} {
		for _, value := range []string{"true", "false"} {
			harnesses := "  harnesses: [{name: codex, source: defs, path: codex, default: " + value + "}]\n"
			raw := "apiVersion: v1beta1\nkind: " + kind + "\nmetadata: {name: test}\nspec:\n" + harnesses
			switch kind {
			case "TeamDefaults":
				raw = "apiVersion: v1beta1\nkind: TeamDefaults\nspec:\n  defaults:\n  " + harnesses
				_, err = config.DecodeDefaults(strings.NewReader(raw))
			default:
				_, err = config.DecodeTeam(strings.NewReader(raw))
			}
			if err == nil || !strings.Contains(err.Error(), "harnesses[0].default: removed") ||
				!strings.Contains(err.Error(), "roles[].harness") {
				t.Fatalf("%s accepted harness default: %v", kind, err)
			}
		}
	}
}

// TestRoleTaskSelectionRoundTrips pins issue #1491: an omitted `tasks`
// (every task) and an explicit `tasks: []` (none) stay distinct through the
// Team round-trip, and selection names are validated.
func TestRoleTaskSelectionRoundTrips(t *testing.T) {
	cfg, err := config.DecodeDefaults(strings.NewReader(`apiVersion: v1beta1
kind: TeamDefaults
spec:
  defaults:
    sources: [{name: defs, url: github.com/example/defs, branch: main}]
    harnesses: [{name: claude, source: defs, path: harnesses/claude}]
`))
	if err != nil {
		t.Fatal(err)
	}
	off, on := true, false
	for _, tasks := range []*[]config.TaskSelection{
		nil,
		{},
		{{Name: "pick-issue"}, {Name: "pick-bug"}},
		// A task entry may also set the task's schedule for this team (#1503).
		{
			{Name: "pick-issue"},
			{Name: "pick-bug", Schedule: "*/15 * * * *"},
			{Name: "pick-nits", Schedule: "@daily", Suspend: &off},
			{Name: "health-sweep", Suspend: &on},
		},
		// ... and override the schedule per harness (#1668).
		{
			{Name: "pick-bug"},
			{Name: "health-sweep", Suspend: &off, Harnesses: map[string]config.TaskHarness{
				"claude": {Suspend: &on},
				"codex":  {Schedule: "@daily"},
			}},
		},
	} {
		team := inheritingTeam()
		team.Spec.Roles = []config.Role{{Name: "dev", Source: "defs", Path: "dev", Tasks: tasks}}
		if _, err = config.Resolve(cfg, team); err != nil {
			t.Fatal(err)
		}
		entry, entryErr := config.Declaration(team)
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		raw, marshalErr := yaml.Marshal(entry)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		decoded, decodeErr := config.DecodeTeam(bytes.NewReader(raw))
		if decodeErr != nil || !reflect.DeepEqual(decoded, team) {
			t.Fatalf("round trip of %v: %s: %v", tasks, raw, decodeErr)
		}
		got := decoded.Spec.Roles[0]
		if got.SelectsTask("pick-bug") != (tasks == nil || len(*tasks) > 0) {
			t.Fatalf("SelectsTask(pick-bug) with %v = %v", tasks, got.SelectsTask("pick-bug"))
		}
	}
	for _, bad := range [][]config.TaskSelection{
		{{Name: "Bad_Name"}},
		{{Name: "a"}, {Name: "a"}},
		{{Name: "a"}, {Name: "a", Schedule: "@daily"}},
		{{Name: ""}},
		{{Name: "a", Harnesses: map[string]config.TaskHarness{"claude": {}}}},
		{{Name: "a", Harnesses: map[string]config.TaskHarness{"Bad_Name": {Schedule: "@daily"}}}},
	} {
		team := inheritingTeam()
		team.Spec.Roles = []config.Role{{Name: "dev", Source: "defs", Path: "dev", Tasks: &bad}}
		if _, err = config.Resolve(cfg, team); err == nil {
			t.Fatalf("tasks %+v accepted", bad)
		}
	}
}

// TestRoleTaskEntryYAMLForms pins the two spellings of a role entry's `tasks`
// items (issue #1503): a bare task name, and a mapping that also sets the
// task's schedule. A bare name is written back as a bare name.
func TestRoleTaskEntryYAMLForms(t *testing.T) {
	const teamYAML = `apiVersion: v1beta1
kind: Team
metadata:
  name: root
spec:
  roles:
    - name: dev
      source: defs
      path: dev
      tasks:
        - pick-issue
        - {name: pick-bug, schedule: "*/15 * * * *"}
        - {name: pick-nits, schedule: "@daily", suspend: true}
        - {name: health-sweep, suspend: false}
`
	team, err := config.DecodeTeam(strings.NewReader(teamYAML))
	if err != nil {
		t.Fatalf("DecodeTeam() error = %v", err)
	}
	tasks := *team.Spec.Roles[0].Tasks
	if len(tasks) != 4 {
		t.Fatalf("tasks = %+v, want 4 entries", tasks)
	}
	if !reflect.DeepEqual(tasks[0], config.TaskSelection{Name: "pick-issue"}) {
		t.Errorf("bare name entry = %+v", tasks[0])
	}
	if tasks[1].Name != "pick-bug" || tasks[1].Schedule != "*/15 * * * *" || tasks[1].Suspend != nil {
		t.Errorf("schedule entry = %+v", tasks[1])
	}
	if tasks[2].Schedule != "@daily" || tasks[2].Suspend == nil || !*tasks[2].Suspend {
		t.Errorf("suspended entry = %+v", tasks[2])
	}
	// An explicit `suspend: false` is kept distinct from an omitted one: it is
	// what turns on a schedule the role ships switched off.
	if tasks[3].Schedule != "" || tasks[3].Suspend == nil || *tasks[3].Suspend {
		t.Errorf("suspend: false entry = %+v", tasks[3])
	}

	role := team.Spec.Roles[0]
	for _, name := range []string{"pick-issue", "pick-bug", "pick-nits", "health-sweep"} {
		if !role.SelectsTask(name) {
			t.Errorf("SelectsTask(%q) = false", name)
		}
	}
	if role.SelectsTask("unlisted") {
		t.Error("SelectsTask(unlisted) = true")
	}
	if entry, ok := role.TaskSelection("pick-bug"); !ok || entry.Schedule != "*/15 * * * *" {
		t.Errorf("TaskSelection(pick-bug) = %+v, %v", entry, ok)
	}

	raw, err := yaml.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "- pick-issue\n") {
		t.Errorf("a bare task name was not written back as one:\n%s", raw)
	}
	if !strings.Contains(string(raw), "name: pick-bug") || !strings.Contains(string(raw), "suspend: false") {
		t.Errorf("mapping entries were not written back:\n%s", raw)
	}

	for name, entry := range map[string]string{
		"unknown field":       `{name: pick-bug, cron: "@daily"}`,
		"non-string name":     `42`,
		"non-boolean suspend": `{name: pick-bug, suspend: "yes"}`,
		"non-string schedule": `{name: pick-bug, schedule: 5}`,
		"a list":              `[pick-bug]`,
	} {
		bad := strings.Replace(teamYAML, "- pick-issue", "- "+entry, 1)
		if _, err = config.DecodeTeam(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: task entry %s accepted", name, entry)
		}
	}
}

// TestRoleTaskHarnessOverrides pins issue #1668 AC1: a task entry's
// `harnesses` map round-trips byte for byte, an empty map is written as the
// bare task name, and an override's shape is decoded strictly.
func TestRoleTaskHarnessOverrides(t *testing.T) {
	const teamYAML = `apiVersion: v1beta1
kind: Team
metadata:
    name: kuke
    realm: default
spec:
    roles:
        - name: majordomo
          source: kukeon
          path: team/roles/majordomo
          tasks:
            - name: health-sweep
              harnesses:
                claude:
                    suspend: false
                codex:
                    schedule: '@daily'
            - pick-issue
`
	team, err := config.DecodeTeam(strings.NewReader(teamYAML))
	if err != nil {
		t.Fatalf("DecodeTeam() error = %v", err)
	}
	if err = config.ValidateTeam(team); err != nil {
		t.Fatalf("ValidateTeam() error = %v", err)
	}
	entry, err := config.Declaration(team)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err = enc.Encode(entry); err != nil {
		t.Fatal(err)
	}
	if buf.String() != teamYAML {
		t.Errorf("round trip changed the document:\n%s\nwant:\n%s", buf.String(), teamYAML)
	}
	// Declaration deep-copies the overrides.
	*(*entry.Spec.Roles[0].Tasks)[0].Harnesses["claude"].Suspend = true
	if *(*team.Spec.Roles[0].Tasks)[0].Harnesses["claude"].Suspend {
		t.Error("Declaration shares the override's suspend with the input")
	}

	off := false
	empty := config.TaskSelection{Name: "x", Harnesses: map[string]config.TaskHarness{}}
	raw, err := yaml.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "x\n" {
		t.Errorf("entry with an empty harnesses map = %q, want the bare name", raw)
	}

	for name, tc := range map[string]struct{ entry, want string }{
		"unknown field": {
			`{name: health-sweep, harnesses: {claude: {args: "a"}}}`,
			"spec.roles[0].tasks[0].harnesses.claude.args: unknown field",
		},
		"empty override": {
			`{name: health-sweep, harnesses: {claude: {}}}`,
			"spec.roles[0].tasks[0].harnesses.claude: set schedule or suspend",
		},
		"invalid harness name": {
			`{name: health-sweep, harnesses: {"Bad Name": {suspend: true}}}`,
			`invalid harness name "Bad Name"`,
		},
		"non-boolean suspend": {
			`{name: health-sweep, harnesses: {claude: {suspend: "yes"}}}`,
			"harnesses.claude.suspend",
		},
	} {
		bad := "apiVersion: v1beta1\nkind: Team\nmetadata: {name: kuke}\nspec:\n  roles:\n" +
			"    - name: majordomo\n      source: kukeon\n      path: p\n      tasks:\n        - " + tc.entry + "\n"
		_, decodeErr := config.DecodeTeam(strings.NewReader(bad))
		if decodeErr == nil || !strings.Contains(decodeErr.Error(), tc.want) {
			t.Errorf("%s: DecodeTeam() error = %v, want it to contain %q", name, decodeErr, tc.want)
		}
	}

	both := config.TaskSelection{
		Name: "health-sweep", Schedule: "@hourly", Args: "a",
		Harnesses: map[string]config.TaskHarness{"claude": {Suspend: &off}, "codex": {Schedule: "@daily"}},
	}
	claude := both.ForHarness("claude")
	if claude.Suspend == nil || *claude.Suspend || claude.Schedule != "@hourly" || claude.Args != "a" ||
		claude.Harnesses != nil {
		t.Errorf("ForHarness(claude) = %+v", claude)
	}
	codex := both.ForHarness("codex")
	if codex.Suspend != nil || codex.Schedule != "@daily" {
		t.Errorf("ForHarness(codex) = %+v", codex)
	}
	other := both.ForHarness("gemini")
	if other.Suspend != nil || other.Schedule != "@hourly" || other.Harnesses != nil {
		t.Errorf("ForHarness(gemini) = %+v", other)
	}
	if both.Harnesses == nil {
		t.Error("ForHarness changed its receiver's map")
	}
}

// inheritingTeam is a team whose declaration omits every list.
func inheritingTeam() *config.Team {
	return &config.Team{
		APIVersion: config.APIVersion,
		Kind:       config.KindTeam,
		Metadata:   config.Metadata{Name: "root", Realm: config.DefaultRealm},
	}
}

func TestDefaultTeamDeclaration(t *testing.T) {
	team := config.DefaultTeam()
	if team.Metadata.Name != config.DefaultTeamName || team.Metadata.Realm != config.DefaultRealm {
		t.Fatalf("metadata = %+v", team.Metadata)
	}
	if !config.IsSystemDefaultTeam(team) || team.Spec.ProjectSource != nil {
		t.Fatalf("spec = %+v, want a template team without a project", team.Spec)
	}
	if err := config.ValidateTeam(team); err != nil {
		t.Fatalf("ValidateTeam: %v", err)
	}
	want := []config.Role{{Name: "agent", Source: "kukeon", Path: "team/roles/agent", InitialPath: "$HOME"}}
	if !reflect.DeepEqual(team.Spec.Roles, want) {
		t.Fatalf("roles = %+v", team.Spec.Roles)
	}
	if !reflect.DeepEqual(team.Spec.Harnesses, config.MajordomoHarnesses()) {
		t.Fatalf("harnesses = %+v", team.Spec.Harnesses)
	}
	raw, err := yaml.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "configs: template") {
		t.Fatalf("encoded team lacks spec.configs:\n%s", raw)
	}
	back, err := config.DecodeTeam(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := config.Declaration(back)
	if err != nil {
		t.Fatal(err)
	}
	if !config.IsSystemDefaultTeam(entry) {
		t.Fatalf("spec.configs lost on round trip: %+v", entry.Spec)
	}
}

func TestTeamConfigsField(t *testing.T) {
	base := "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: default\n"
	legacy, err := config.DecodeTeam(strings.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}
	if config.IsSystemDefaultTeam(legacy) {
		t.Fatal("a team named default without spec.configs is a user team")
	}
	if _, err = config.DecodeTeam(strings.NewReader(base + "spec:\n  configs: agents\n")); err == nil ||
		!strings.Contains(err.Error(), "spec.configs") {
		t.Fatalf("unsupported spec.configs: err = %v", err)
	}
	other, err := config.DecodeTeam(strings.NewReader(
		"apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: other\nspec:\n  configs: template\n"))
	if err != nil {
		t.Fatal(err)
	}
	if config.IsSystemDefaultTeam(other) {
		t.Fatal("only the team named default is the system default team")
	}
}
