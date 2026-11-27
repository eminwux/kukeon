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

package webserver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
	"gopkg.in/yaml.v3"
)

// everyField is a Team that sets every field the form models and every one
// it keeps (D2): spec.secrets, a role's connectors and needs.secrets, a
// source's long-form git block, an explicit empty task list and a task
// switched on.
const everyField = `apiVersion: v1beta1
kind: Team
metadata:
  name: web
  realm: default
spec:
  projectSource:
    name: project
    source: web
    branch: main
    clonePath: /home/user/web
  secrets:
    - name: gh
      secretRef: {name: gh-token, realm: default}
      envName: GH_TOKEN
  connectors: {}
  sources:
    - name: agents
      url: github.com/eminwux/agents
      tag: v1
      clonePath: /opt/agents
      git:
        identityRef: {name: git-ssh, realm: default, space: dev}
        author: {name: Bot, email: bot@example.test}
    - {name: tools, source: shared-tools}
  roles:
    - name: dev
      source: agents
      path: dev
      initialPath: /home/user/web
      needs: {image: [go, node], secrets: [npm]}
      harness: claude
      connectors:
        github: {method: app, identity: gh-app}
      tasks:
        - pick-issue
        - {name: pick-bug, schedule: "*/15 * * * *", suspend: false, args: --label bug}
    - {name: pm, source: agents, path: pm, needs: {}, tasks: []}
  harnesses:
    - {name: claude, source: kukeon, path: team/harnesses/claude}
`

const legacyOptIn = `apiVersion: v1beta1
kind: Team
metadata:
  name: kuke
spec:
  roles:
    - name: majordomo
      source: kukeon
      path: team/roles/majordomo
      needs: {image: [go], secrets: [gh-token]}
`

const roleConnector = `apiVersion: v1beta1
kind: Team
metadata:
  name: kuke
spec:
  roles:
    - name: majordomo
      source: kukeon
      path: team/roles/majordomo
      connectors: {github: {}}
`

// taskHarnesses is a Team whose task entries override their schedule per
// harness (issue #1668, AC7): Apply of an unchanged form keeps every override.
const taskHarnesses = `apiVersion: v1beta1
kind: Team
metadata:
  name: kuke
spec:
  roles:
    - name: majordomo
      source: kukeon
      path: team/roles/majordomo
      tasks:
        - name: health-sweep
          harnesses:
            claude: {suspend: false}
            codex: {schedule: "@daily", suspend: true}
        - {name: pick-bug, suspend: true, harnesses: {codex: {schedule: "@weekly"}}}
  harnesses:
    - {name: claude, source: kukeon, path: team/harnesses/claude}
    - {name: codex, source: kukeon, path: team/harnesses/codex}
`

// formFixtures are the Teams the round trip covers: the two repository
// files of #1643 AC5, the kuke team, everyField, and the defaults of every
// TeamDefaults fixture of pkg/team/config/testdata as a team's lists.
func formFixtures(t *testing.T) map[string]*config.Team {
	t.Helper()
	out := map[string]*config.Team{"kuke": config.Majordomo()}
	decode := func(name string, raw []byte) {
		team, err := config.DecodeTeamDeclaration(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = team
	}
	decode("every-field", []byte(everyField))
	// #1649 AC7: a legacy needs.secrets opt-in and a role-entry connector
	// round-trip unchanged.
	decode("legacy-opt-in", []byte(legacyOptIn))
	decode("role-connector", []byte(roleConnector))
	decode("task-harnesses", []byte(taskHarnesses))
	for _, file := range []string{"kukeon.kuketeam.yaml", "agents.kuketeam.yaml"} {
		raw, err := os.ReadFile(filepath.Join("..", "team", "initialize", "testdata", "fixtures", file))
		if err != nil {
			t.Fatal(err)
		}
		decode(file, raw)
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "pkg", "team", "config", "testdata", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("testdata: %v", err)
	}
	for _, file := range files {
		raw, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var head struct {
			Kind string `yaml:"kind"`
		}
		if yaml.Unmarshal(raw, &head) != nil {
			continue
		}
		switch head.Kind {
		case config.KindTeam:
			decode(filepath.Base(file), raw)
		case config.KindTeamDefaults:
			var defaults config.TeamDefaults
			if yaml.Unmarshal(raw, &defaults) != nil {
				continue
			}
			d := defaults.Spec.Defaults
			out[filepath.Base(file)] = &config.Team{
				APIVersion: config.APIVersion, Kind: config.KindTeam,
				Metadata: config.Metadata{Name: "t"},
				Spec: config.TeamSpec{Defaults: config.Defaults{
					Sources: d.Sources, Roles: d.Roles, Harnesses: d.Harnesses, Connectors: d.Connectors,
				}},
			}
		}
	}
	return out
}

func marshalTeam(t *testing.T, team *config.Team) string {
	t.Helper()
	raw, err := yaml.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestFormRoundTrip: the form of a Team, sent through JSON as the browser
// sends it back, applied to that Team gives the same bytes.
func TestFormRoundTrip(t *testing.T) {
	for name, team := range formFixtures(t) {
		raw, err := json.Marshal(formOf(team, connectortest.Catalog(t)))
		if err != nil {
			t.Fatal(err)
		}
		var form teamForm
		if err = json.Unmarshal(raw, &form); err != nil {
			t.Fatal(err)
		}
		got, err := form.apply(team, nil, connectortest.Catalog(t))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want, have := marshalTeam(t, team), marshalTeam(t, got); want != have {
			t.Errorf("%s: round trip changed the declaration:\nwant:\n%s\ngot:\n%s", name, want, have)
		}
	}
}

// TestFormMapsInheritanceAndTasks: all tasks is a nil Tasks, an empty list
// is tasks: [], inherit leaves a list out, and a schedule switch is suspend.
func TestFormMapsInheritanceAndTasks(t *testing.T) {
	base, err := config.DecodeTeamDeclaration(strings.NewReader(everyField))
	if err != nil {
		t.Fatal(err)
	}
	form := formOf(base, connectortest.Catalog(t))
	off := true
	form.Roles.Items[0].Tasks.Items[1].Suspend = &off
	form.Roles.Items[0].Tasks.Items[1].Schedule = "@daily"
	form.Roles.Items[1].Tasks = formTasks{All: true, Items: []formTask{}}
	form.Roles.Items[1].Image = []string{" rust "}
	form.Harnesses.Inherit = true
	form.Connectors = formConnectors{Items: map[string]formConnector{"claude": {Method: "api-key"}}}
	got, err := form.apply(base, nil, connectortest.Catalog(t))
	if err != nil {
		t.Fatal(err)
	}
	dev, pm := got.Spec.Roles[0], got.Spec.Roles[1]
	if task, _ := dev.TaskSelection("pick-bug"); task.Suspend == nil || !*task.Suspend || task.Schedule != "@daily" ||
		task.Args != "--label bug" {
		t.Fatalf("pick-bug = %+v", task)
	}
	if dev.Connectors["github"].Identity != "gh-app" || dev.Needs.Secrets[0] != "npm" {
		t.Fatalf("dev lost what the form keeps: %+v", dev)
	}
	if pm.Tasks != nil || pm.Needs == nil || pm.Needs.Image[0] != "rust" {
		t.Fatalf("pm = %+v", pm)
	}
	if got.Spec.Harnesses != nil || got.Spec.Connectors["claude"].Method != "api-key" || len(got.Spec.Secrets) != 1 {
		t.Fatalf("spec = %+v", got.Spec)
	}
	if got.Spec.Sources[0].Git.Author == nil || got.Spec.Sources[0].Git.IdentityRef.Space != "dev" {
		t.Fatalf("the source's git block was not kept: %+v", got.Spec.Sources[0].Git)
	}
	if base.Spec.Roles[0].Tasks == nil || len(*base.Spec.Roles[1].Tasks) != 0 {
		t.Fatal("apply changed its base")
	}

	// A definition's identity is the form's; a reference's belongs to its
	// stored Source.
	form = formOf(base, connectortest.Catalog(t))
	form.Sources[0].Identity = ""
	if got, err = form.apply(base, nil, connectortest.Catalog(t)); err != nil ||
		got.Spec.Sources[0].Git.IdentityRef != nil ||
		got.Spec.Sources[0].Git.Author == nil {
		t.Fatalf("clear identity: %v %+v", err, got.Spec.Sources[0].Git)
	}
	form.Sources[1].Identity = "other"
	if _, err = form.apply(base, nil, connectortest.Catalog(t)); err == nil ||
		!strings.Contains(err.Error(), "stored Source") {
		t.Fatalf("identity on a reference: %v", err)
	}
	form = formOf(base, connectortest.Catalog(t))
	form.Project.RefKind, form.Project.Ref = "", "main"
	if _, err = form.apply(base, nil, connectortest.Catalog(t)); err == nil {
		t.Fatal("a ref without a kind was accepted")
	}
	form = formOf(base, connectortest.Catalog(t))
	form.Project.RefKind, form.Project.Ref = "tag", "v2"
	if got, err = form.apply(base, nil, connectortest.Catalog(t)); err != nil || got.Spec.ProjectSource.Tag != "v2" ||
		got.Spec.ProjectSource.Branch != "" || got.Spec.ProjectSource.Stored != "web" {
		t.Fatalf("project ref: %v %+v", err, got.Spec.ProjectSource)
	}
}

// roleSwitch is the form item of connector name on the form's first role.
func roleSwitch(t *testing.T, form *teamForm, name string) *formRoleConnector {
	t.Helper()
	role := &form.Roles.Items[0]
	for j := range role.Connectors {
		if item := &role.Connectors[j]; item.Name == name {
			return item
		}
	}
	t.Fatalf("role %s has no %s switch: %+v", role.Name, name, role.Connectors)
	return nil
}

// TestFormRoleConnectors is #1649 AC6: a switch sends the same change as kuke
// team connector enable and disable; an untouched one writes nothing.
func TestFormRoleConnectors(t *testing.T) {
	catalog := connectortest.Catalog(t)
	decode := func(raw string) *config.Team {
		team, err := config.DecodeTeamDeclaration(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return team
	}
	t.Run("switches", func(t *testing.T) {
		form := formOf(decode(legacyOptIn), catalog)
		var names []string
		for _, item := range form.Roles.Items[0].Connectors {
			names = append(names, item.Name)
		}
		if strings.Join(names, ",") != "example-token,git,github" {
			t.Fatalf("switches = %v, want the togglable connectors only", names)
		}
		if !roleSwitch(t, &form, "github").Enabled || roleSwitch(t, &form, "git").Enabled {
			t.Fatalf("a legacy opt-in must show as on: %+v", form.Roles.Items[0].Connectors)
		}
	})
	t.Run("enable", func(t *testing.T) {
		base := config.Majordomo()
		form := formOf(base, catalog)
		roleSwitch(t, &form, "github").Enabled = true
		got, err := form.apply(base, nil, catalog)
		if err != nil {
			t.Fatal(err)
		}
		want := config.Majordomo()
		want.Spec.Roles[0].Connectors = map[string]config.ConnectorChoice{"github": {}}
		if marshalTeam(t, got) != marshalTeam(t, want) {
			t.Fatalf("got:\n%s", marshalTeam(t, got))
		}
		if base.Spec.Roles[0].Connectors != nil {
			t.Fatal("apply changed its base")
		}
	})
	t.Run("identity", func(t *testing.T) {
		base := decode(roleConnector)
		form := formOf(base, catalog)
		roleSwitch(t, &form, "github").Identity = "bot-token"
		got, err := form.apply(base, nil, catalog)
		if err != nil || got.Spec.Roles[0].Connectors["github"].Identity != "bot-token" {
			t.Fatalf("%v %+v", err, got.Spec.Roles[0].Connectors)
		}
	})
	t.Run("disable clears both spellings", func(t *testing.T) {
		base := decode(legacyOptIn)
		base.Spec.Roles[0].Connectors = map[string]config.ConnectorChoice{"github": {}}
		form := formOf(base, catalog)
		roleSwitch(t, &form, "github").Enabled = false
		got, err := form.apply(base, nil, catalog)
		if err != nil {
			t.Fatal(err)
		}
		role := got.Spec.Roles[0]
		if role.Connectors != nil || role.Needs == nil || len(role.Needs.Secrets) != 0 || role.Needs.Image[0] != "go" {
			t.Fatalf("role = %+v needs = %+v", role, role.Needs)
		}
	})
	t.Run("an inherited role list changes nothing", func(t *testing.T) {
		base := decode(roleConnector)
		form := formOf(base, catalog)
		form.Roles.Inherit = true
		if got, err := form.apply(base, nil, catalog); err != nil || got.Spec.Roles != nil {
			t.Fatalf("%v %+v", err, got.Spec.Roles)
		}
	})
	t.Run("a harness connector is refused", func(t *testing.T) {
		base := decode(roleConnector)
		form := formOf(base, catalog)
		form.Roles.Items[0].Connectors = append(form.Roles.Items[0].Connectors,
			formRoleConnector{Name: "claude", Enabled: true})
		if _, err := form.apply(base, nil, catalog); err == nil || !strings.Contains(err.Error(), "spec.connectors") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestFormTaskHarnesses: the form carries a task entry's per-harness
// overrides (issue #1668, AC7). A switch set in the form is an override's
// suspend; an override left with neither a schedule nor a switch is dropped,
// and an entry left with none is written as before.
func TestFormTaskHarnesses(t *testing.T) {
	base, err := config.DecodeTeamDeclaration(strings.NewReader(taskHarnesses))
	if err != nil {
		t.Fatal(err)
	}
	form := formOf(base, connectortest.Catalog(t))
	sweep := form.Roles.Items[0].Tasks.Items[0]
	if len(sweep.Harnesses) != 2 || sweep.Harnesses["codex"].Schedule != "@daily" {
		t.Fatalf("form overrides = %+v", sweep.Harnesses)
	}
	// Switch codex on, keeping its schedule; clear claude's switch.
	on := false
	sweep.Harnesses["codex"] = formTaskHarness{Schedule: "@daily", Suspend: &on}
	sweep.Harnesses["claude"] = formTaskHarness{}
	form.Roles.Items[0].Tasks.Items[0] = sweep
	got, err := form.apply(base, nil, connectortest.Catalog(t))
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := got.Spec.Roles[0].TaskSelection("health-sweep")
	if _, ok := entry.Harnesses["claude"]; ok || len(entry.Harnesses) != 1 {
		t.Fatalf("overrides = %+v", entry.Harnesses)
	}
	if codex := entry.Harnesses["codex"]; codex.Schedule != "@daily" || codex.Suspend == nil || *codex.Suspend {
		t.Fatalf("codex override = %+v", codex)
	}
	// Clearing every override writes a nil map.
	form.Roles.Items[0].Tasks.Items[0].Harnesses = map[string]formTaskHarness{"codex": {}}
	if got, err = form.apply(base, nil, connectortest.Catalog(t)); err != nil {
		t.Fatal(err)
	}
	if entry, _ = got.Spec.Roles[0].TaskSelection("health-sweep"); entry.Harnesses != nil {
		t.Fatalf("overrides = %+v, want nil", entry.Harnesses)
	}
	if !strings.Contains(marshalTeam(t, got), "        - health-sweep\n") {
		t.Fatalf("an entry left with no override is not the bare name:\n%s", marshalTeam(t, got))
	}
	// apply did not change its base.
	if before, _ := base.Spec.Roles[0].TaskSelection("health-sweep"); len(before.Harnesses) != 2 ||
		*before.Harnesses["codex"].Suspend != true {
		t.Fatalf("base changed: %+v", before.Harnesses)
	}
}

// TestDefaultsForm is #1746: the defaults' connector choices come in the
// form's shape with their harness state (#1685), and with no defaults the
// lists are empty, not null.
func TestDefaultsForm(t *testing.T) {
	got := defaultsFormOf(config.Defaults{Connectors: map[string]config.ConnectorChoice{
		"claude": {Method: "api-key", Identity: "claude-key", State: config.StateEphemeral},
	}}, connectortest.Catalog(t))
	if want := (formConnector{Method: "api-key", Identity: "claude-key", State: config.StateEphemeral}); got.Connectors["claude"] != want {
		t.Fatalf("connectors = %+v", got.Connectors)
	}
	if raw := jsonBody(t, defaultsFormOf(config.Defaults{}, nil)); raw != `{"roles":[],"connectors":{}}` {
		t.Fatalf("empty = %s", raw)
	}
}
