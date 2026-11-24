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
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/team/initialize"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

func draftOf(t *testing.T, h http.Handler, body map[string]any) draftView {
	t.Helper()
	w := connectorRequest(t, h, http.MethodPost, "/api/teams/draft", jsonBody(t, body))
	if w.Code != http.StatusOK {
		t.Fatalf("draft: %d %s", w.Code, w.Body)
	}
	return decodeBody[draftView](t, w)
}

// TestTeamDraft is POST /api/teams/draft: the project is fetched and its
// kuketeam.yaml read, nothing is written, and the reply carries the form, the
// opaque declaration and each source's state.
func TestTeamDraft(t *testing.T) {
	c, h := teamFixture(t)
	draft := draftOf(t, h, map[string]any{"source": teamProjectURL, "branch": "main", "name": "proj"})
	if c.writes != 0 {
		t.Fatalf("a draft wrote %d times", c.writes)
	}
	if draft.Name != "proj" || draft.Origin.Kind != initialize.OriginRepository || draft.Form.Name != "proj" ||
		len(draft.Form.Roles.Items) != 1 || draft.Form.Roles.Items[0].Name != "lead" || draft.Form.Roles.Inherit ||
		!draft.Form.Harnesses.Inherit || draft.Form.Project == nil || draft.Form.Project.URL != teamProjectURL {
		t.Fatalf("draft = %+v", draft)
	}
	if len(draft.Sources) != 1 || draft.Sources[0].Alias != "project" ||
		draft.Sources[0].State != initialize.DraftNew ||
		draft.Sources[0].Contents == nil {
		t.Fatalf("sources = %+v", draft.Sources)
	}
	if _, err := config.DecodeTeamDeclaration(strings.NewReader(draft.Declaration)); err != nil {
		t.Fatalf("declaration: %v\n%s", err, draft.Declaration)
	}
	// The defaults apply when the file is ignored; a stored Source is named
	// by its name.
	c.storeSource(t, "proj", config.SourceSpec{URL: teamProjectURL, Branch: "main"})
	draft = draftOf(t, h, map[string]any{"source": "proj", "useRepoFile": false})
	if draft.Origin.Kind != initialize.OriginDefaults || !draft.Form.Roles.Inherit ||
		draft.Sources[0].State != initialize.DraftStored || draft.Form.Project.Stored != "proj" {
		t.Fatalf("stored draft = %+v", draft)
	}
	w := connectorRequest(t, h, http.MethodPost, "/api/teams/draft", jsonBody(t, map[string]any{"name": "x"}))
	expectError(t, w, w.Body.Bytes(), http.StatusUnprocessableEntity, "team_failed")
}

// TestTeamCreateFromForm is the wizard's Review step: the form over the
// draft's declaration is previewed, then added with the CLI's add.
func TestTeamCreateFromForm(t *testing.T) {
	c, h := teamFixture(t)
	draft := draftOf(t, h, map[string]any{"source": teamProjectURL, "branch": "main", "name": "proj"})
	on := false
	form := draft.Form
	form.Roles.Items[0].Tasks = formTasks{Items: []formTask{{Name: "health-sweep", Suspend: &on, Schedule: "@daily"}}}
	form.Roles.Items[0].Image = []string{"go"}
	body := map[string]any{
		"name": "proj", "source": teamProjectURL, "branch": "main", "form": form, "declaration": draft.Declaration,
	}
	w := connectorRequest(t, h, http.MethodPost, "/api/teams/preview", jsonBody(t, body))
	preview := decodeBody[teamOutcomeView](t, w)
	if w.Code != http.StatusOK || c.writes != 0 || preview.Result != nil || len(preview.Plan.Pairs) == 0 {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	if preview.Plan.Origin.Kind != initialize.OriginFile || !strings.Contains(preview.Plan.Declaration, "@daily") {
		t.Fatalf("plan = %+v", preview.Plan)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams", jsonBody(t, body))
	if got := decodeBody[teamOutcomeView](t, w); w.Code != http.StatusOK || got.Result == nil ||
		got.Result.Team != "proj" {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	team, err := config.DecodeTeam(bytes.NewReader(c.teams["proj"]))
	if err != nil {
		t.Fatal(err)
	}
	task, ok := team.Spec.Roles[0].TaskSelection("health-sweep")
	if !ok || task.Suspend == nil || *task.Suspend || task.Schedule != "@daily" ||
		team.Spec.Roles[0].Needs.Image[0] != "go" ||
		team.Spec.ProjectSource.Stored != "proj" ||
		c.sources["proj"] == nil {
		t.Fatalf("stored team = %+v", team.Spec)
	}
	// A second add asks for force, as kuke create team does.
	w = connectorRequest(t, h, http.MethodPost, "/api/teams", jsonBody(t, body))
	expectError(t, w, w.Body.Bytes(), http.StatusConflict, "already_exists")
	for _, bad := range []map[string]any{
		{"name": "proj", "form": form, "declaration": draft.Declaration, "roles": []string{"x"}},
		{"name": "other", "form": form, "declaration": draft.Declaration, "source": teamProjectURL},
		{"name": "proj", "form": form, "declaration": "kind: [", "source": teamProjectURL},
	} {
		w = connectorRequest(t, h, http.MethodPost, "/api/teams/preview", jsonBody(t, bad))
		expectError(t, w, w.Body.Bytes(), http.StatusBadRequest, "invalid_request")
	}
}

// TestTeamViewAndEdit is AC3 and AC4 over HTTP: GET carries the view (the
// hydrated team, its task Configs with schedules) and the form (the stored
// team); an edit switches one schedule off without YAML.
//
//nolint:gocyclo,cyclop // one HTTP sequence over the same stored team
func TestTeamViewAndEdit(t *testing.T) {
	c, h := teamFixture(t)
	c.putProj(t)
	last := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	c.configs = []v1beta1.CellConfigDoc{
		{Metadata: v1beta1.CellConfigMetadata{Name: "proj-old-codex", Realm: "default", Labels: map[string]string{
			v1beta1.LabelTeam: "proj", v1beta1.LabelRole: "old",
		}}},
		{
			Metadata: v1beta1.CellConfigMetadata{
				Name:  "proj-old-codex-health-sweep",
				Realm: "default",
				Space: "proj",
				Labels: map[string]string{
					v1beta1.LabelTeam: "proj", v1beta1.LabelRole: "old", v1beta1.LabelTask: "health-sweep",
				},
			},
			Spec:   v1beta1.CellConfigSpec{Schedule: &v1beta1.CellConfigSchedule{Cron: "@hourly", Suspend: true}},
			Status: &v1beta1.CellConfigStatus{Schedule: &v1beta1.CellConfigScheduleStatus{LastScheduleTime: &last}},
		},
	}
	w := connectorRequest(t, h, http.MethodGet, "/api/teams/proj", "")
	got := decodeBody[teamView](t, w)
	if w.Code != http.StatusOK || got.View == nil || got.Form == nil || got.Problem != "" {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	v := got.View
	if v.Project == nil || v.Project.Stored != "proj" || !strings.Contains(v.Project.URL, "example.test/proj") ||
		v.Project.Ref != "branch main" || v.Project.ClonePath != "/home/user/proj" {
		t.Fatalf("project = %+v", v.Project)
	}
	if v.Roles.Inherited || len(v.Roles.Items) != 1 || !v.Harnesses.Inherited || len(v.Harnesses.Items) != 2 ||
		!v.Connectors.Inherited || !v.Sources.Inherited {
		t.Fatalf("lists = %+v", v)
	}
	if len(v.Sources.Items) != 1 || v.Sources.Items[0].Alias != "kukeon" || v.Sources.Items[0].Declared ||
		v.Sources.Items[0].ClonePath != "/home/user/kukeon" {
		t.Fatalf("sources = %+v", v.Sources.Items)
	}
	if len(v.Tasks) != 1 || v.Tasks[0].Task != "health-sweep" || v.Tasks[0].Schedule == nil ||
		!v.Tasks[0].Schedule.Suspend || v.Tasks[0].Status == nil || !v.Tasks[0].Status.LastScheduleTime.Equal(last) ||
		strings.Join(v.Tasks[0].Scope, "/") != "default/proj/default" || len(v.Configs) != 2 {
		t.Fatalf("tasks = %+v, configs = %+v", v.Tasks, v.Configs)
	}
	if got.Form.Project == nil || got.Form.Project.Stored != "proj" || got.Form.Roles.Items[0].Name != "old" {
		t.Fatalf("form = %+v", got.Form)
	}

	// Edit: switch the task's schedule on and move the project ref.
	form := *got.Form
	on := false
	form.Roles.Items[0].Tasks = formTasks{Items: []formTask{{Name: "health-sweep", Suspend: &on}}}
	form.Project.RefKind, form.Project.Ref = "tag", "v1"
	w = connectorRequest(t, h, http.MethodPost, "/api/teams", jsonBody(t, map[string]any{"name": "proj", "form": form}))
	if w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	team, err := config.DecodeTeam(bytes.NewReader(c.teams["proj"]))
	if err != nil {
		t.Fatal(err)
	}
	if task, ok := team.Spec.Roles[0].TaskSelection("health-sweep"); !ok || task.Suspend == nil || *task.Suspend ||
		team.Spec.ProjectSource.Tag != "v1" || team.Spec.ProjectSource.Stored != "proj" {
		t.Fatalf("edited = %+v", team.Spec)
	}
	writes := c.writes
	for _, tc := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{"name": "missing", "form": teamForm{Name: "missing", Roles: formRoles{Inherit: true}}}, http.StatusNotFound, "not_found"},
		{map[string]any{"name": "proj", "form": teamForm{Name: "renamed"}}, http.StatusBadRequest, "invalid_request"},
		{map[string]any{"name": "proj", "form": form, "source": teamProjectURL}, http.StatusBadRequest, "invalid_request"},
		{map[string]any{"form": teamForm{}}, http.StatusBadRequest, "invalid_request"},
	} {
		w = connectorRequest(t, h, http.MethodPost, "/api/teams", jsonBody(t, tc.body))
		expectError(t, w, w.Body.Bytes(), tc.status, tc.code)
	}
	if c.writes != writes {
		t.Fatal("a refused edit wrote")
	}
	// A team that does not resolve still shows its summary and form.
	delete(c.sources, "proj")
	w = connectorRequest(t, h, http.MethodGet, "/api/teams/proj", "")
	got = decodeBody[teamView](t, w)
	if w.Code != http.StatusOK || got.View == nil || got.View.Unresolved == "" || got.Form == nil {
		t.Fatalf("unresolved: %d %s", w.Code, w.Body)
	}
}

// TestTeamExport is AC5: the route serves kuke team export's bytes, with
// its two options.
func TestTeamExport(t *testing.T) {
	c, h := teamFixture(t)
	c.putProj(t)
	for _, tc := range []struct {
		query string
		opts  initialize.ExportOptions
	}{
		{"", initialize.ExportOptions{}},
		{"?hostFields=1", initialize.ExportOptions{HostFields: true}},
		{"?explicit=true&hostFields=1", initialize.ExportOptions{HostFields: true, ExplicitDefaults: true}},
	} {
		want, err := initialize.Export(t.Context(), c, "proj", tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		w := connectorRequest(t, h, http.MethodGet, "/api/teams/proj/export"+tc.query, "")
		got := decodeBody[map[string]string](t, w)
		if w.Code != http.StatusOK || got["document"] != string(want) {
			t.Fatalf("%q: %d %s\nwant:\n%s", tc.query, w.Code, w.Body, want)
		}
	}
	w := connectorRequest(t, h, http.MethodGet, "/api/teams/proj/export?explicit=1", "")
	if got := decodeBody[map[string]string](t, w); !strings.Contains(got["document"], "harnesses:") {
		t.Fatalf("explicit defaults not written: %s", got["document"])
	}
	w = connectorRequest(t, h, http.MethodGet, "/api/teams/missing/export", "")
	expectError(t, w, w.Body.Bytes(), http.StatusNotFound, "not_found")
}

// TestTeamRoleConnectors is #1649 AC8 and AC6 over HTTP: the view lists what
// each Config resolves, and the form's per-role switch enables a connector
// through the same apply as an edit.
func TestTeamRoleConnectors(t *testing.T) {
	c, h := teamFixture(t)
	c.putProj(t)
	c.configs = []v1beta1.CellConfigDoc{{
		Metadata: v1beta1.CellConfigMetadata{Name: "proj-old-codex", Realm: "default", Labels: map[string]string{
			v1beta1.LabelTeam: "proj", v1beta1.LabelRole: "old",
		}},
		Status: &v1beta1.CellConfigStatus{Connectors: []v1beta1.ConnectorStatus{{
			Connector: "github", Method: "token", State: v1beta1.ConnectorUnresolved, Required: true,
		}}},
	}}
	w := connectorRequest(t, h, http.MethodGet, "/api/teams/proj", "")
	got := decodeBody[teamView](t, w)
	if w.Code != http.StatusOK || got.View == nil || got.Form == nil {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	rows := got.View.Connectors.Roles
	if len(rows) != 1 || rows[0].Role != "old" || rows[0].Config != "proj-old-codex" || rows[0].Fill != "MISSING" ||
		rows[0].SetByTeam {
		t.Fatalf("rows = %+v", rows)
	}
	form := *got.Form
	for i := range form.Roles.Items[0].Connectors {
		if item := &form.Roles.Items[0].Connectors[i]; item.Name == "github" {
			item.Enabled = true
		}
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams", jsonBody(t, map[string]any{"name": "proj", "form": form}))
	if w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	team, err := config.DecodeTeam(bytes.NewReader(c.teams["proj"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := team.Spec.Roles[0].Connectors["github"]; !ok {
		t.Fatalf("stored roles = %+v", team.Spec.Roles)
	}
	w = connectorRequest(t, h, http.MethodGet, "/api/teams/proj", "")
	if got = decodeBody[teamView](t, w); !got.View.Connectors.Roles[0].SetByTeam {
		t.Fatalf("after the edit the team sets github: %+v", got.View.Connectors.Roles)
	}
}
