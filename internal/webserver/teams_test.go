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

package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// teamClient stands in for kukeond behind the teams routes: the stored
// Teams, TeamDefaults and Configs, the default scope, and every write.
type teamClient struct {
	kukeonv1.FakeClient

	teams    map[string][]byte
	defaults []byte
	configs  []v1beta1.CellConfigDoc
	writes   int
	team     string
	deleted  []string

	listErr error
}

func newTeamClient() *teamClient { return &teamClient{teams: map[string][]byte{}} }

func (c *teamClient) Close() error               { return nil }
func (c *teamClient) Ping(context.Context) error { return nil }

func (c *teamClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	return []v1beta1.RealmDoc{{
		Metadata: v1beta1.RealmMetadata{Name: "default"}, Spec: v1beta1.RealmSpec{Namespace: "default.kukeon.io"},
	}}, nil
}

func (c *teamClient) ListSpaces(context.Context, string) ([]v1beta1.SpaceDoc, error) {
	return []v1beta1.SpaceDoc{{Metadata: v1beta1.SpaceMetadata{Name: "default"}}}, nil
}

func (c *teamClient) ListStacks(context.Context, string, string) ([]v1beta1.StackDoc, error) {
	return []v1beta1.StackDoc{{
		Metadata: v1beta1.StackMetadata{Name: "default"},
		Spec:     v1beta1.StackSpec{ID: "default", RealmID: "default", SpaceID: "default"},
	}}, nil
}

func (c *teamClient) ListCells(context.Context, string, string, string) ([]v1beta1.CellDoc, error) {
	return nil, nil
}

func (c *teamClient) GetSecret(context.Context, v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	return kukeonv1.GetSecretResult{}, nil
}

func (c *teamClient) GetBlueprint(context.Context, v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
	return kukeonv1.GetBlueprintResult{}, nil
}

func (c *teamClient) ListConfigs(context.Context, string, string, string) ([]v1beta1.CellConfigDoc, error) {
	return c.configs, nil
}

func (c *teamClient) GetConfig(context.Context, v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	return kukeonv1.GetConfigResult{}, nil
}

func (c *teamClient) result(name string, raw []byte) kukeonv1.GetTeamResult {
	summary := kukeonv1.TeamSummary{Name: name, Realm: "default"}
	if team, err := config.DecodeTeam(bytes.NewReader(raw)); err == nil {
		summary.RolesInherited = team.Spec.Roles == nil
		for _, r := range team.Spec.Roles {
			summary.Roles = append(summary.Roles, r.Name)
		}
		if p := team.Spec.ProjectSource; p != nil {
			summary.ProjectURL, summary.ProjectRef = p.URL, "branch "+p.Branch
		}
	}
	for _, cfg := range c.configs {
		if cfg.Metadata.Labels[v1beta1.LabelTeam] == name {
			summary.Configs = append(summary.Configs, cfg.Metadata.Name)
		}
	}
	return kukeonv1.GetTeamResult{Exists: true, Document: raw, Summary: summary}
}

func (c *teamClient) GetTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.GetTeamResult, error) {
	raw, ok := c.teams[ref.Name]
	if !ok {
		return kukeonv1.GetTeamResult{}, nil
	}
	return c.result(ref.Name, raw), nil
}

func (c *teamClient) ListTeams(context.Context, string) ([]kukeonv1.GetTeamResult, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	names := make([]string, 0, len(c.teams))
	for name := range c.teams {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]kukeonv1.GetTeamResult, 0, len(names))
	for _, name := range names {
		out = append(out, c.result(name, c.teams[name]))
	}
	return out, nil
}

func (c *teamClient) GetTeamDefaults(context.Context, string) (kukeonv1.GetTeamDefaultsResult, error) {
	if c.defaults == nil {
		return kukeonv1.GetTeamDefaultsResult{Realm: "default"}, nil
	}
	return kukeonv1.GetTeamDefaultsResult{Exists: true, Realm: "default", Document: c.defaults}, nil
}

func (c *teamClient) DeleteTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.DeleteTeamResult, error) {
	if ref.Name == config.SystemTeamName {
		return kukeonv1.DeleteTeamResult{}, fmt.Errorf("%w", errdefs.ErrTeamReserved)
	}
	if _, ok := c.teams[ref.Name]; !ok {
		return kukeonv1.DeleteTeamResult{}, fmt.Errorf("%w: %s", errdefs.ErrTeamNotFound, ref.Name)
	}
	c.writes++
	delete(c.teams, ref.Name)
	c.deleted = append(c.deleted, ref.Name)
	return kukeonv1.DeleteTeamResult{
		Deleted: true,
		Removed: []kukeonv1.ApplyResourceResult{
			{Kind: "CellConfig", Name: ref.Name + "-lead-codex", Action: "deleted"},
		},
		LiveCells: []string{"default/default/default/" + ref.Name + "-lead-codex"},
	}, nil
}

func (c *teamClient) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	return c.apply(raw, "")
}

func (c *teamClient) ApplyDocumentsForTeam(
	_ context.Context, raw []byte, team string,
) (kukeonv1.ApplyDocumentsResult, error) {
	return c.apply(raw, team)
}

func (c *teamClient) apply(raw []byte, team string) (kukeonv1.ApplyDocumentsResult, error) {
	c.writes++
	c.team = team
	var result kukeonv1.ApplyDocumentsResult
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return result, err
		}
		var head struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := node.Decode(&head); err != nil {
			return result, err
		}
		doc, err := yaml.Marshal(&node)
		if err != nil {
			return result, err
		}
		switch head.Kind {
		case string(v1beta1.KindTeam):
			c.teams[head.Metadata.Name] = doc
		case string(v1beta1.KindTeamDefaults):
			c.defaults = doc
		}
		result.Resources = append(result.Resources, kukeonv1.ApplyResourceResult{
			Index: len(result.Resources), Kind: head.Kind, Name: head.Metadata.Name, Action: "created",
		})
	}
	return result, nil
}

func (c *teamClient) putTeam(t *testing.T, team *config.Team) {
	t.Helper()
	raw, err := yaml.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	c.teams[team.Metadata.Name] = raw
}

func (c *teamClient) storedDefaults(t *testing.T) *config.TeamDefaults {
	t.Helper()
	got, err := config.DecodeDefaults(bytes.NewReader(c.defaults))
	if err != nil {
		t.Fatalf("stored TeamDefaults: %v", err)
	}
	return got
}

const (
	teamProjectURL = "https://example.test/proj.git"
	teamRepoFile   = `apiVersion: v1beta1
kind: Team
metadata:
  name: other
spec:
  roles:
    - {name: lead, source: kukeon, path: team/roles/majordomo}
`
)

// teamFixture is a daemon with the initial TeamDefaults (the kukeon source
// cloned, so its roles can run) and the definition trees composition reads
// in place of git: this repository as kukeon and extra, and a project whose
// kuketeam.yaml is teamRepoFile.
func teamFixture(t *testing.T) (*teamClient, http.Handler) {
	t.Helper()
	c := newTeamClient()
	defaults := initialize.InitialDefaults("default")
	defaults.Spec.Defaults.Sources[0].ClonePath = "/home/user/kukeon-src"
	raw, err := yaml.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	c.defaults = raw
	repo := os.DirFS("../..")
	api := &teamAPI{sources: map[string]fs.FS{
		"kukeon": repo, "extra": repo,
		"project": fstest.MapFS{"kuketeam.yaml": {Data: []byte(teamRepoFile)}},
	}}
	mux := http.NewServeMux()
	api.register(mux, func(context.Context) (kukeonv1.Client, error) { return c, nil })
	return c, mux
}

func storedProj() *config.Team {
	return &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: "proj", Realm: "default"},
		Spec: config.TeamSpec{
			ProjectSource: &config.Source{
				Name:      "project",
				URL:       teamProjectURL,
				Branch:    "main",
				ClonePath: "/home/user/proj",
			},
			Defaults: config.Defaults{
				Roles: []config.Role{{Name: "old", Source: "kukeon", Path: "team/roles/majordomo"}},
			},
		},
	}
}

func jsonBody(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func expectError(t *testing.T, w interface {
	Result() *http.Response
}, body []byte, status int, code string,
) {
	t.Helper()
	if got := w.Result().StatusCode; got != status {
		t.Fatalf("status = %d, want %d: %s", got, status, body)
	}
	var e apiErrorBody
	if err := json.Unmarshal(body, &e); err != nil || e.Error.Code != code {
		t.Fatalf("code = %q, want %q (%v): %s", e.Error.Code, code, err, body)
	}
}

func TestTeamsListAndGet(t *testing.T) {
	c, h := teamFixture(t)
	c.putTeam(t, config.Majordomo())
	c.putTeam(t, storedProj())
	c.configs = []v1beta1.CellConfigDoc{{Metadata: v1beta1.CellConfigMetadata{
		Name: "proj-old-codex", Labels: map[string]string{v1beta1.LabelTeam: "proj"},
	}}}
	w := connectorRequest(t, h, http.MethodGet, "/api/teams", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	list := decodeBody[struct {
		Teams []teamSummaryView `json:"teams"`
	}](t, w)
	if len(list.Teams) != 2 || list.Teams[0].Name != "kuke" || !list.Teams[0].System || list.Teams[1].System ||
		list.Teams[1].ProjectURL != teamProjectURL || strings.Join(list.Teams[1].Configs, ",") != "proj-old-codex" ||
		list.Teams[0].Configs == nil || list.Teams[0].Harnesses == nil {
		t.Fatalf("teams = %+v", list.Teams)
	}
	w = connectorRequest(t, h, http.MethodGet, "/api/teams/proj", "")
	got := decodeBody[teamView](t, w)
	if w.Code != http.StatusOK || got.Summary.Name != "proj" ||
		!strings.Contains(got.Document, "url: "+teamProjectURL) {
		t.Fatalf("get: %d %+v", w.Code, got)
	}
	w = connectorRequest(t, h, http.MethodGet, "/api/teams/missing", "")
	expectError(t, w, w.Body.Bytes(), http.StatusNotFound, "not_found")
}

// TestTeamsPreviewWritesNothing is AC10 over HTTP: the plan and the
// declaration as YAML, and no write.
func TestTeamsPreviewWritesNothing(t *testing.T) {
	c, h := teamFixture(t)
	w := connectorRequest(t, h, http.MethodPost, "/api/teams/preview",
		jsonBody(t, map[string]any{"name": "proj", "source": teamProjectURL, "branch": "main"}))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	if c.writes != 0 || len(c.teams) != 0 {
		t.Fatalf("preview wrote: %d %v", c.writes, c.teams)
	}
	var got struct {
		Plan struct {
			Origin      initialize.Origin `json:"origin"`
			Declaration string            `json:"declaration"`
			Pairs       []struct {
				Role   string `json:"role"`
				Config string `json:"config"`
			} `json:"pairs"`
			Create []string `json:"create"`
		} `json:"plan"`
		Result *teamResultView `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Result != nil || got.Plan.Origin.Kind != initialize.OriginRepository || len(got.Plan.Pairs) == 0 ||
		got.Plan.Pairs[0].Role != "lead" || len(got.Plan.Create) == 0 {
		t.Fatalf("preview = %+v", got)
	}
	team, err := config.DecodeTeam(strings.NewReader(got.Plan.Declaration))
	if err != nil || team.Metadata.Name != "proj" || team.Spec.ProjectSource.URL != teamProjectURL {
		t.Fatalf("declaration %v:\n%s", err, got.Plan.Declaration)
	}
	// The overrides and the repository-file toggle reach the same code as
	// the CLI flags.
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/preview", jsonBody(t, map[string]any{
		"name": "proj", "source": teamProjectURL, "branch": "main", "useRepoFile": false,
	}))
	expectError(t, w, w.Body.Bytes(), http.StatusUnprocessableEntity, "team_failed")
	if !strings.Contains(w.Body.String(), "--role") {
		t.Fatalf("empty selection does not explain itself: %s", w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/preview", jsonBody(t, map[string]any{
		"name": "proj", "source": teamProjectURL, "branch": "main", "roles": []string{"boss=kukeon:team/roles/majordomo"},
	}))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"role":"boss"`) {
		t.Fatalf("override preview: %d %s", w.Code, w.Body)
	}
}

func TestTeamsApplyAddAndEdit(t *testing.T) {
	c, h := teamFixture(t)
	w := connectorRequest(t, h, http.MethodPost, "/api/teams",
		jsonBody(t, map[string]any{"name": "proj", "source": teamProjectURL, "branch": "main"}))
	if w.Code != http.StatusOK {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	got := decodeBody[teamOutcomeView](t, w)
	if got.Result == nil || got.Result.Team != "proj" || c.team != "proj" || c.teams["proj"] == nil {
		t.Fatalf("add = %+v", got)
	}
	// AC8a: the web UI asks for the same confirmation as --force.
	w = connectorRequest(t, h, http.MethodPost, "/api/teams",
		jsonBody(t, map[string]any{"name": "proj", "source": teamProjectURL, "branch": "main"}))
	expectError(t, w, w.Body.Bytes(), http.StatusConflict, "already_exists")
	if !strings.Contains(w.Body.String(), "kuke team apply proj") {
		t.Fatalf("conflict message: %s", w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams",
		jsonBody(t, map[string]any{"name": "proj", "source": teamProjectURL, "branch": "main", "force": true}))
	if w.Code != http.StatusOK {
		t.Fatalf("force: %d %s", w.Code, w.Body)
	}
	// An edit is a declaration without a source: kuke team apply -f.
	edited := strings.Replace(
		strings.Replace(teamRepoFile, "name: other", "name: proj", 1),
		"name: lead",
		"name: chief",
		1,
	)
	w = connectorRequest(
		t,
		h,
		http.MethodPost,
		"/api/teams",
		jsonBody(t, map[string]any{"name": "proj", "declaration": edited}),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	team, err := config.DecodeTeam(bytes.NewReader(c.teams["proj"]))
	if err != nil || len(team.Spec.Roles) != 1 || team.Spec.Roles[0].Name != "chief" ||
		team.Spec.ProjectSource == nil || team.Spec.ProjectSource.URL != teamProjectURL {
		t.Fatalf("edited team %v: %+v", err, team)
	}
	writes := c.writes
	for _, body := range []map[string]any{
		{"name": "other", "declaration": edited},
		{"name": "proj", "declaration": edited, "roles": []string{"x"}},
		{"name": "proj"},
		{"declaration": "apiVersion: v1beta1\nkind: Team\nmetadata: {name: proj}\nspec: {bogus: 1}\n"},
	} {
		w = connectorRequest(t, h, http.MethodPost, "/api/teams", jsonBody(t, body))
		if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body)
		}
	}
	if c.writes != writes {
		t.Fatal("a rejected request wrote")
	}
	w = connectorRequest(
		t,
		h,
		http.MethodPost,
		"/api/teams",
		jsonBody(t, map[string]any{"name": "other", "declaration": edited}),
	)
	expectError(t, w, w.Body.Bytes(), http.StatusBadRequest, "invalid_request")
	w = connectorRequest(t, h, http.MethodPost, "/api/teams", `{"name":`)
	expectError(t, w, w.Body.Bytes(), http.StatusBadRequest, "invalid_request")
	w = connectorRequest(t, h, http.MethodPost, "/api/teams", `{"unknown":true}`)
	expectError(t, w, w.Body.Bytes(), http.StatusBadRequest, "invalid_request")
}

func TestTeamsReapplyAndDelete(t *testing.T) {
	c, h := teamFixture(t)
	c.putTeam(t, storedProj())
	w := connectorRequest(t, h, http.MethodPost, "/api/teams/proj/reapply", "")
	if got := decodeBody[teamOutcomeView](t, w); w.Code != http.StatusOK || got.Result == nil ||
		got.Result.Team != "proj" ||
		got.Plan.Origin.Kind != initialize.OriginStored {
		t.Fatalf("reapply: %d %s", w.Code, w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/missing/reapply", "")
	expectError(t, w, w.Body.Bytes(), http.StatusNotFound, "not_found")
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/kuke/delete", "")
	expectError(t, w, w.Body.Bytes(), http.StatusConflict, "reserved")
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/proj/delete", "")
	got := decodeBody[deleteTeamView](t, w)
	if w.Code != http.StatusOK || !got.Deleted || len(got.Removed) != 1 || len(got.LiveCells) != 1 ||
		got.KeptVolumes == nil || c.teams["proj"] != nil {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/proj/delete", "")
	expectError(t, w, w.Body.Bytes(), http.StatusNotFound, "not_found")
}

// TestTeamSources is AC17 over HTTP.
func TestTeamSources(t *testing.T) {
	c, h := teamFixture(t)
	w := connectorRequest(t, h, http.MethodGet, "/api/team-sources", "")
	list := decodeBody[sourcesView](t, w)
	if w.Code != http.StatusOK || len(list.Sources) != 1 || list.Sources[0].Name != "kukeon" ||
		len(list.DefaultHarnesses) != 2 || list.DefaultRoles == nil {
		t.Fatalf("sources: %d %s", w.Code, w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/team-sources",
		jsonBody(t, map[string]any{"name": "extra", "url": "https://example.test/extra.git", "branch": "main"}))
	added := decodeBody[defaultsChangeView](t, w)
	if w.Code != http.StatusOK || added.Action != "created" || added.Discovered == nil ||
		added.Discovered.Source.ClonePath != "/home/user/extra" || len(added.Discovered.Roles) == 0 || added.Inheriting == nil {
		t.Fatalf("add source: %d %s", w.Code, w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/team-sources/kukeon/inspect", "")
	contents := decodeBody[contentsView](t, w)
	if w.Code != http.StatusOK || contents.Source.Name != "kukeon" || len(contents.Harnesses) == 0 {
		t.Fatalf("inspect: %d %s", w.Code, w.Body)
	}
	defaultHarness := 0
	for _, h := range contents.Harnesses {
		if h.Default {
			defaultHarness++
		}
	}
	if defaultHarness != 2 {
		t.Fatalf("default harnesses marked = %d: %s", defaultHarness, w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/team-sources/kukeon/delete", "")
	expectError(t, w, w.Body.Bytes(), http.StatusUnprocessableEntity, "team_failed")
	if !strings.Contains(w.Body.String(), "default harness") {
		t.Fatalf("refusal does not name the reference: %s", w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/team-sources/extra/delete", "")
	if removed := decodeBody[defaultsChangeView](t, w); w.Code != http.StatusOK || removed.Action != "removed" {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	if len(c.storedDefaults(t).Spec.Defaults.Sources) != 1 {
		t.Fatalf("defaults after remove: %s", c.defaults)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/team-sources/nothing/inspect", "")
	expectError(t, w, w.Body.Bytes(), http.StatusUnprocessableEntity, "team_failed")
}

// TestTeamDefaults is AC14 over HTTP: the list mode is kuke team defaults
// set, the document mode applies a whole TeamDefaults, and both name the
// inheriting teams.
func TestTeamDefaults(t *testing.T) {
	c, h := teamFixture(t)
	c.putTeam(t, storedProj())
	w := connectorRequest(t, h, http.MethodGet, "/api/team-defaults", "")
	got := decodeBody[defaultsView](t, w)
	if w.Code != http.StatusOK || !got.Exists || !strings.Contains(got.Document, "kind: TeamDefaults") {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/team-defaults",
		jsonBody(t, map[string]any{"roles": []string{"lead=kukeon:team/roles/majordomo"}}))
	change := decodeBody[defaultsChangeView](t, w)
	if w.Code != http.StatusOK || change.Action != "updated" || strings.Join(change.Inheriting, ",") != "proj" {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	if d := c.storedDefaults(t).Spec.Defaults; len(d.Roles) != 1 || d.Roles[0].Name != "lead" || len(d.Harnesses) != 2 {
		t.Fatalf("defaults = %+v", d)
	}
	doc := c.storedDefaults(t)
	doc.Spec.Parameters = map[string]string{}
	doc.Spec.Defaults.Harnesses = doc.Spec.Defaults.Harnesses[:1]
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	w = connectorRequest(
		t,
		h,
		http.MethodPost,
		"/api/team-defaults",
		jsonBody(t, map[string]any{"document": string(raw)}),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("document: %d %s", w.Code, w.Body)
	}
	if d := c.storedDefaults(t).Spec.Defaults; len(d.Harnesses) != 1 {
		t.Fatalf("document not applied: %+v", d)
	}
	writes := c.writes
	for _, body := range []string{
		`{}`,
		jsonBody(t, map[string]any{"document": string(raw), "roles": []string{"lead"}}),
	} {
		w = connectorRequest(t, h, http.MethodPost, "/api/team-defaults", body)
		expectError(t, w, w.Body.Bytes(), http.StatusBadRequest, "invalid_request")
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/team-defaults",
		jsonBody(t, map[string]any{"document": "apiVersion: v1beta1\nkind: TeamDefaults\nspec: {bogus: 1}\n"}))
	expectError(t, w, w.Body.Bytes(), http.StatusUnprocessableEntity, "team_failed")
	if c.writes != writes {
		t.Fatal("a rejected defaults request wrote")
	}
}

// TestTeamErrorClassification: a team operation's own failure is a 422
// with its message; a sentinel keeps its status; a transport failure and a
// daemon read error stay unavailable.
func TestTeamErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{errors.New("compose: no pair"), http.StatusUnprocessableEntity, "team_failed"},
		{fmt.Errorf("x: %w", errdefs.ErrInvalidTeam), http.StatusBadRequest, "invalid_request"},
		{fmt.Errorf("x: %w", errdefs.ErrTeamApplyOnly), http.StatusBadRequest, "invalid_request"},
		{fmt.Errorf("x: %w", errdefs.ErrTeamNameInUse), http.StatusConflict, "in_use"},
		{fmt.Errorf("x: %w", errdefs.ErrTeamExists), http.StatusConflict, "already_exists"},
		{fmt.Errorf("x: %w", errdefs.ErrTeamDefaultsNotFound), http.StatusNotFound, "not_found"},
		{fmt.Errorf("x: %w", errdefs.ErrDaemonMethodUnsupported), http.StatusNotImplemented, "daemon_outdated"},
		{&net.OpError{Op: "dial", Err: errors.New("refused")}, http.StatusServiceUnavailable, "unavailable"},
		{context.DeadlineExceeded, http.StatusServiceUnavailable, "unavailable"},
		// A team operation's own lookups report the class without the
		// remote text (initialize.requestError), and keep it reachable.
		{fmt.Errorf("teams lookup: %w", errdefs.ErrDaemonTransport), http.StatusServiceUnavailable, "unavailable"},
	} {
		classified := teamError(tc.err)
		if classified.Error() != tc.err.Error() {
			t.Fatalf("message changed: %q → %q", tc.err, classified)
		}
		if status, code := lifecycleErrorStatus(classified); status != tc.status || code != tc.code {
			t.Fatalf("%v: %d %s, want %d %s", tc.err, status, code, tc.status, tc.code)
		}
	}
	c, h := teamFixture(t)
	c.listErr = &net.OpError{Op: "read", Err: errors.New("reset")}
	w := connectorRequest(t, h, http.MethodGet, "/api/teams", "")
	expectError(t, w, w.Body.Bytes(), http.StatusServiceUnavailable, "unavailable")
}

// TestTeamRoutesAreRegistered: the server serves the teams routes behind
// its access policy.
func TestTeamRoutesAreRegistered(t *testing.T) {
	c := newTeamClient()
	c.putTeam(t, storedProj())
	policy, err := parseAccessPolicy("http://localhost:5129")
	if err != nil {
		t.Fatal(err)
	}
	served, err := newHandlerWithPolicy(
		policy,
		"test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	w := connectorRequest(t, served, http.MethodGet, "/api/teams/proj", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"proj"`) {
		t.Fatalf("GET /api/teams/proj: %d %s", w.Code, w.Body)
	}
	req := connectorRequest(t, served, http.MethodGet, "/api/team-defaults", "")
	if req.Code != http.StatusOK {
		t.Fatalf("GET /api/team-defaults: %d %s", req.Code, req.Body)
	}
}

// TestTeamActionsRequireJSON: the action routes whose request carries only a
// path refuse a request a cross-site HTML form can send (no JSON
// Content-Type), and a body other than empty or {}, before they touch
// kukeond; the web UI's {} body is accepted.
func TestTeamActionsRequireJSON(t *testing.T) {
	c, h := teamFixture(t)
	c.putTeam(t, storedProj())
	writes, defaults := c.writes, string(c.defaults)
	routes := []string{
		"/api/teams/proj/reapply",
		"/api/teams/proj/delete",
		"/api/team-sources/kukeon/inspect",
		"/api/team-sources/extra/delete",
	}
	for _, route := range routes {
		for _, tc := range []struct{ contentType, body string }{
			{"", ""},
			{"application/x-www-form-urlencoded", "confirm=1"},
			{"text/plain", "{}"},
			{"application/json", `{"force":true}`},
			{"application/json", "{}{}"},
		} {
			req := httptest.NewRequest(http.MethodPost, "http://localhost:5129"+route, strings.NewReader(tc.body))
			req.Header.Set("Origin", "http://evil.example")
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			expectError(t, w, w.Body.Bytes(), http.StatusBadRequest, "invalid_request")
		}
	}
	if c.writes != writes || c.teams["proj"] == nil || len(c.deleted) != 0 || string(c.defaults) != defaults {
		t.Fatalf("a refused request changed state: writes=%d deleted=%v", c.writes, c.deleted)
	}
	w := connectorRequest(t, h, http.MethodPost, "/api/teams/proj/reapply", "{}")
	if w.Code != http.StatusOK {
		t.Fatalf("reapply with {}: %d %s", w.Code, w.Body)
	}
}
