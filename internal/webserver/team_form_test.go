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
		raw, err := json.Marshal(formOf(team))
		if err != nil {
			t.Fatal(err)
		}
		var form teamForm
		if err = json.Unmarshal(raw, &form); err != nil {
			t.Fatal(err)
		}
		got, err := form.apply(team)
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
	form := formOf(base)
	off := true
	form.Roles.Items[0].Tasks.Items[1].Suspend = &off
	form.Roles.Items[0].Tasks.Items[1].Schedule = "@daily"
	form.Roles.Items[1].Tasks = formTasks{All: true, Items: []formTask{}}
	form.Roles.Items[1].Image = []string{" rust "}
	form.Harnesses.Inherit = true
	form.Connectors = formConnectors{Items: map[string]formConnector{"claude": {Method: "api-key"}}}
	got, err := form.apply(base)
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
	form = formOf(base)
	form.Sources[0].Identity = ""
	if got, err = form.apply(base); err != nil || got.Spec.Sources[0].Git.IdentityRef != nil ||
		got.Spec.Sources[0].Git.Author == nil {
		t.Fatalf("clear identity: %v %+v", err, got.Spec.Sources[0].Git)
	}
	form.Sources[1].Identity = "other"
	if _, err = form.apply(base); err == nil || !strings.Contains(err.Error(), "stored Source") {
		t.Fatalf("identity on a reference: %v", err)
	}
	form = formOf(base)
	form.Project.RefKind, form.Project.Ref = "", "main"
	if _, err = form.apply(base); err == nil {
		t.Fatal("a ref without a kind was accepted")
	}
	form = formOf(base)
	form.Project.RefKind, form.Project.Ref = "tag", "v2"
	if got, err = form.apply(base); err != nil || got.Spec.ProjectSource.Tag != "v2" ||
		got.Spec.ProjectSource.Branch != "" || got.Spec.ProjectSource.Stored != "web" {
		t.Fatalf("project ref: %v %+v", err, got.Spec.ProjectSource)
	}
}
