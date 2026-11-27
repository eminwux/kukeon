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
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
	"gopkg.in/yaml.v3"
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
	// A new team's roles start with every per-role connector on (#1745).
	if switches := draft.Form.Roles.Items[0].Connectors; len(switches) == 0 ||
		slices.ContainsFunc(switches, func(item formRoleConnector) bool { return !item.Enabled }) {
		t.Fatalf("draft switches = %+v", switches)
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
	// The draft turns on every connector of the fixture's catalog; compose
	// knows only the built-in ones, as the server's own catalog does.
	for i := range form.Roles.Items[0].Connectors {
		if item := &form.Roles.Items[0].Connectors[i]; item.Name == connectortest.Name {
			item.Enabled = false
		}
	}
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
	if preview.Connectors == nil || len(*preview.Connectors) != 0 || len(c.rendered) == 0 {
		t.Fatalf("preview connectors = %v, rendered %v", preview.Connectors, c.rendered)
	}
	// The preview carries what each composed Config resolves (#1745), from a
	// draft render: before the team is stored, so Resolves to is filled. A
	// Config that does not render is a note.
	c.rendered = nil
	c.preview = func(req kukeonv1.CellFromConfigRequest) (kukeonv1.PreviewCellFromConfigResult, error) {
		if req.Draft == nil || req.Draft.Metadata.Labels[v1beta1.LabelTeam] != "proj" {
			return kukeonv1.PreviewCellFromConfigResult{}, fmt.Errorf("not a draft of team proj: %+v", req.Draft)
		}
		if req.Draft.Metadata.Labels[v1beta1.LabelTask] != "" {
			return kukeonv1.PreviewCellFromConfigResult{}, errors.New("blueprint not stored")
		}
		return kukeonv1.PreviewCellFromConfigResult{Connectors: []v1beta1.ConnectorStatus{{
			Connector: "github", Method: "token", State: v1beta1.ConnectorUnresolved, Required: true,
		}}}, nil
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/preview", jsonBody(t, body))
	preview = decodeBody[teamOutcomeView](t, w)
	if w.Code != http.StatusOK || c.writes != 0 || preview.Connectors == nil {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	rows := *preview.Connectors
	if len(rows) == 0 || rows[0].Role != "lead" || rows[0].Connector != "github" || rows[0].Fill != "MISSING" ||
		!strings.HasPrefix(rows[0].Config, "proj-lead-") {
		t.Fatalf("preview rows = %+v", rows)
	}
	if len(preview.ConnectorNotes) == 0 || !strings.Contains(preview.ConnectorNotes[0], "blueprint not stored") {
		t.Fatalf("preview notes = %q", preview.ConnectorNotes)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams", jsonBody(t, body))
	if got := decodeBody[teamOutcomeView](t, w); w.Code != http.StatusOK || got.Result == nil ||
		got.Result.Team != "proj" || got.Connectors != nil {
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
	// The switches the draft turned on are stored in the role entry.
	if _, ok = team.Spec.Roles[0].Connectors["github"]; !ok {
		t.Fatalf("stored role connectors = %+v", team.Spec.Roles[0].Connectors)
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
	if v.Roles.Inherited || len(v.Roles.Items) != 1 || !v.Harnesses.Inherited || len(v.Harnesses.Items) != 3 ||
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

// TestTeamTaskHarnessOverrides is issue #1668 AC7 over the routes: the form
// posted with a per-harness override is previewed and stored, GET returns it
// in the form, and the view names each task Config's harness.
func TestTeamTaskHarnessOverrides(t *testing.T) {
	c, h := teamFixture(t)
	c.putProj(t)
	w := connectorRequest(t, h, http.MethodGet, "/api/teams/proj", "")
	got := decodeBody[teamView](t, w)
	if w.Code != http.StatusOK || got.Form == nil {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	form := *got.Form
	on := false
	form.Roles.Items[0].Tasks = formTasks{Items: []formTask{{
		Name: "health-sweep", Harnesses: map[string]formTaskHarness{"claude": {Suspend: &on}},
	}}}
	body := map[string]any{"name": "proj", "form": form}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/preview", jsonBody(t, body))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "proj-old-claude-health-sweep") {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	writes := c.writes
	w = connectorRequest(t, h, http.MethodPost, "/api/teams", jsonBody(t, body))
	if w.Code != http.StatusOK || c.writes == writes {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	team, err := config.DecodeTeam(bytes.NewReader(c.teams["proj"]))
	if err != nil {
		t.Fatal(err)
	}
	task, ok := team.Spec.Roles[0].TaskSelection("health-sweep")
	if override := task.Harnesses["claude"]; !ok || override.Suspend == nil || *override.Suspend ||
		len(task.Harnesses) != 1 || task.Suspend != nil {
		t.Fatalf("stored entry = %+v", task)
	}

	c.configs = []v1beta1.CellConfigDoc{{
		Metadata: v1beta1.CellConfigMetadata{
			Name: "proj-old-claude-health-sweep", Realm: "default",
			Labels: map[string]string{
				v1beta1.LabelTeam: "proj", v1beta1.LabelRole: "old", v1beta1.LabelTask: "health-sweep",
				v1beta1.LabelHarness: "claude",
			},
		},
		Spec: v1beta1.CellConfigSpec{Schedule: &v1beta1.CellConfigSchedule{Cron: "@hourly"}},
	}}
	w = connectorRequest(t, h, http.MethodGet, "/api/teams/proj", "")
	got = decodeBody[teamView](t, w)
	if w.Code != http.StatusOK || got.Form == nil || got.View == nil {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	items := got.Form.Roles.Items[0].Tasks.Items
	if len(items) != 1 || items[0].Harnesses["claude"].Suspend == nil || *items[0].Harnesses["claude"].Suspend {
		t.Fatalf("form tasks = %+v", items)
	}
	if len(got.View.Tasks) != 1 || got.View.Tasks[0].Harness != "claude" {
		t.Fatalf("view tasks = %+v", got.View.Tasks)
	}
}

// inheritingDefaults are TeamDefaults whose role sets every field the form
// models and some it does not (needs.secrets), with a connector choice
// (#1746).
func inheritingDefaults(t *testing.T, c *teamClient) *config.TeamDefaults {
	t.Helper()
	on := false
	defaults := initialize.InitialDefaults("default")
	defaults.Spec.Defaults.Roles = []config.Role{{
		Name: "lead", Source: "kukeon", Path: "team/roles/majordomo", Harness: "claude", InitialPath: "$HOME/work",
		Needs:      &config.Needs{Image: []string{"go", "node"}, Secrets: []string{"gh-token"}},
		Connectors: map[string]config.ConnectorChoice{"github": {Identity: "bot-token"}},
		Tasks:      &[]config.TaskSelection{{Name: "health-sweep", Schedule: "@daily", Suspend: &on}},
	}}
	defaults.Spec.Defaults.Connectors = map[string]config.ConnectorChoice{"git": {Identity: "git-ssh"}}
	raw, err := yaml.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	c.defaults = raw
	return defaults
}

// TestTeamDefaultsInForm is #1746 part 2 over the routes: /api/sources gives
// the default roles and connector choices in the form's shape, and an edit
// that declares them unchanged composes what inheriting them did.
func TestTeamDefaultsInForm(t *testing.T) {
	c, h := teamFixture(t)
	c.putProj(t)
	defaults := inheritingDefaults(t, c)
	inheriting := storedProj()
	inheriting.Spec.Roles = nil
	c.putTeam(t, inheriting)

	w := connectorRequest(t, h, http.MethodGet, "/api/sources", "")
	list := decodeBody[sourcesListView](t, w)
	roles := list.DefaultForm.Roles
	if w.Code != http.StatusOK || len(roles) != 1 {
		t.Fatalf("sources: %d %s", w.Code, w.Body)
	}
	lead := roles[0]
	if lead.Harness != "claude" || lead.InitialPath != "$HOME/work" || strings.Join(lead.Image, ",") != "go,node" ||
		lead.Tasks.All || len(lead.Tasks.Items) != 1 || lead.Tasks.Items[0].Schedule != "@daily" {
		t.Fatalf("default role = %+v", lead)
	}
	if github := roleSwitch(t, &teamForm{Roles: formRoles{Items: roles}}, "github"); !github.Enabled ||
		github.Identity != "bot-token" {
		t.Fatalf("github switch = %+v", github)
	}
	if got := list.DefaultForm.Connectors; len(got) != 1 || got["git"].Identity != "git-ssh" {
		t.Fatalf("default connectors = %+v", got)
	}

	w = connectorRequest(t, h, http.MethodGet, "/api/teams/proj", "")
	view := decodeBody[teamView](t, w)
	if w.Code != http.StatusOK || view.Form == nil || !view.Form.Roles.Inherit || !view.Form.Connectors.Inherit {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	preview := func(form teamForm) teamOutcomeView {
		t.Helper()
		got := connectorRequest(t, h, http.MethodPost, "/api/teams/preview",
			jsonBody(t, map[string]any{"name": "proj", "form": form}))
		if got.Code != http.StatusOK {
			t.Fatalf("preview: %d %s", got.Code, got.Body)
		}
		return decodeBody[teamOutcomeView](t, got)
	}
	inherited := preview(*view.Form)
	declared := *view.Form
	declared.Roles = formRoles{Items: roles}
	declared.Connectors = formConnectors{Items: list.DefaultForm.Connectors}
	got := preview(declared)
	if len(inherited.Plan.Pairs) == 0 || jsonBody(t, got.Plan.Pairs) != jsonBody(t, inherited.Plan.Pairs) {
		t.Fatalf("pairs:\n declared  %s\n inherited %s", jsonBody(t, got.Plan.Pairs), jsonBody(t, inherited.Plan.Pairs))
	}
	if slices.Compare(got.Plan.MissingSecrets, inherited.Plan.MissingSecrets) != 0 {
		t.Fatalf("missing secrets: %v, inheriting %v", got.Plan.MissingSecrets, inherited.Plan.MissingSecrets)
	}
	team, err := config.DecodeTeamDeclaration(strings.NewReader(got.Plan.Declaration))
	if err != nil {
		t.Fatal(err)
	}
	if want := defaults.Spec.Defaults; jsonBody(t, team.Spec.Roles) != jsonBody(t, want.Roles) ||
		jsonBody(t, team.Spec.Connectors) != jsonBody(t, want.Connectors) {
		t.Fatalf("declared roles %s connectors %s, want the defaults'", jsonBody(t, team.Spec.Roles),
			jsonBody(t, team.Spec.Connectors))
	}
}
