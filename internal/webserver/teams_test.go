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
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
	"gopkg.in/yaml.v3"
)

// teamClient stands in for kukeond behind the teams routes: the stored
// Teams, TeamDefaults and Configs, the default scope, and every write.
type teamClient struct {
	kukeonv1.FakeClient

	teams    map[string][]byte
	sources  map[string][]byte
	defaults []byte
	configs  []v1beta1.CellConfigDoc
	writes   int
	team     string
	deleted  []string

	listErr error
	// preview answers a draft render (#1745); nil renders every Config
	// with no connectors.
	preview  func(kukeonv1.CellFromConfigRequest) (kukeonv1.PreviewCellFromConfigResult, error)
	rendered []string
}

func (c *teamClient) PreviewCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.PreviewCellFromConfigResult, error) {
	c.rendered = append(c.rendered, req.Config.Name)
	if c.preview == nil {
		return kukeonv1.PreviewCellFromConfigResult{}, nil
	}
	return c.preview(req)
}

func newTeamClient() *teamClient {
	return &teamClient{teams: map[string][]byte{}, sources: map[string][]byte{}}
}

func (c *teamClient) storeSource(t *testing.T, name string, spec config.SourceSpec) {
	t.Helper()
	raw, err := yaml.Marshal(v1beta1.SourceDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindSource,
		Metadata: v1beta1.TeamMetadata{Name: name, Realm: "default"}, Spec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.sources[name] = raw
}

func (c *teamClient) sourceResult(name string) kukeonv1.GetSourceResult {
	return kukeonv1.GetSourceResult{
		Exists: true, Document: c.sources[name], Summary: kukeonv1.SourceSummary{Name: name, Realm: "default"},
	}
}

func (c *teamClient) GetSource(_ context.Context, ref kukeonv1.SourceRef) (kukeonv1.GetSourceResult, error) {
	if _, ok := c.sources[ref.Name]; !ok {
		return kukeonv1.GetSourceResult{}, nil
	}
	return c.sourceResult(ref.Name), nil
}

func (c *teamClient) ListSources(context.Context, string) ([]kukeonv1.GetSourceResult, error) {
	names := make([]string, 0, len(c.sources))
	for name := range c.sources {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]kukeonv1.GetSourceResult, 0, len(names))
	for _, name := range names {
		out = append(out, c.sourceResult(name))
	}
	return out, nil
}

// DeleteSource refuses a referenced Source as kukeond does (AC7).
func (c *teamClient) DeleteSource(ctx context.Context, ref kukeonv1.SourceRef) (kukeonv1.DeleteSourceResult, error) {
	if _, ok := c.sources[ref.Name]; !ok {
		return kukeonv1.DeleteSourceResult{}, nil
	}
	users, err := initialize.SourceUsers(ctx, c)
	if err != nil {
		return kukeonv1.DeleteSourceResult{}, err
	}
	if uses := users[ref.Name]; len(uses) > 0 {
		names := make([]string, 0, len(uses))
		for _, use := range uses {
			names = append(names, use.String())
		}
		return kukeonv1.DeleteSourceResult{}, fmt.Errorf("%w: source %s is referenced by %s",
			errdefs.ErrSourceInUse, ref.Name, strings.Join(names, ", "))
	}
	c.writes++
	delete(c.sources, ref.Name)
	return kukeonv1.DeleteSourceResult{Deleted: true}, nil
}

// StageGitFetch stages nothing: the fetch uses the caller's own git, as for
// a public source with no stored identity.
func (c *teamClient) StageGitFetch(context.Context, kukeonv1.GitFetchRequest) (kukeonv1.StageGitFetchResult, error) {
	return kukeonv1.StageGitFetchResult{}, nil
}

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
			if p.URL == "" {
				// kukeond reads the project's address from its stored Source.
				stored := p.Stored
				if stored == "" {
					stored = name
				}
				var doc v1beta1.SourceDoc
				if yaml.Unmarshal(c.sources[stored], &doc) == nil {
					summary.ProjectURL = doc.Spec.URL
				}
			}
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
		case string(v1beta1.KindSource):
			c.sources[head.Metadata.Name] = doc
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
	got, err := config.DecodeStoredDefaults(bytes.NewReader(c.defaults))
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

// teamFixture is a daemon with the initial TeamDefaults, the stored kukeon
// Source, and the definition trees composition reads
// in place of git: this repository as kukeon and extra, and a project whose
// kuketeam.yaml is teamRepoFile.
func teamFixture(t *testing.T) (*teamClient, http.Handler) {
	t.Helper()
	c := newTeamClient()
	defaults := initialize.InitialDefaults("default")
	c.storeSource(t, config.KukeonSourceName, config.KukeonSourceSpec())
	raw, err := yaml.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	c.defaults = raw
	repo := os.DirFS("../..")
	api := &teamAPI{sources: map[string]fs.FS{
		"kukeon": repo, "extra": repo,
		"project": fstest.MapFS{"kuketeam.yaml": {Data: []byte(teamRepoFile)}},
	}, catalog: connectortest.Catalog(t)}
	mux := http.NewServeMux()
	api.register(mux, func(context.Context) (kukeonv1.Client, error) { return c, nil })
	return c, mux
}

// putProj stores team proj as kukeond stores it since #1643: its project a
// reference to the Source proj, which is stored too.
func (c *teamClient) putProj(t *testing.T) {
	t.Helper()
	c.storeSource(t, "proj", config.SourceSpec{URL: teamProjectURL, Branch: "main"})
	c.putTeam(t, storedProj())
}

func storedProj() *config.Team {
	return &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: "proj", Realm: "default"},
		Spec: config.TeamSpec{
			ProjectSource: &config.Source{
				Name:      "project",
				Stored:    "proj",
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
	c.putProj(t)
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
		!strings.Contains(got.Document, "source: proj") {
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
	if err != nil || team.Metadata.Name != "proj" || team.Spec.ProjectSource.Stored != "proj" ||
		team.Spec.ProjectSource.URL != "" {
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
		team.Spec.ProjectSource == nil || team.Spec.ProjectSource.Stored != "proj" || c.sources["proj"] == nil {
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
	c.putProj(t)
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

// TestTeamDefaults is AC14 over HTTP: the list mode is kuke team defaults
// set, the document mode applies a whole TeamDefaults, and both name the
// inheriting teams.
func TestTeamDefaults(t *testing.T) {
	c, h := teamFixture(t)
	c.putProj(t)
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
	if d := c.storedDefaults(t).Spec.Defaults; len(d.Roles) != 1 || d.Roles[0].Name != "lead" || len(d.Harnesses) != 3 {
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
	c.putProj(t)
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
	c.putProj(t)
	writes, defaults := c.writes, string(c.defaults)
	routes := []string{
		"/api/teams/proj/reapply",
		"/api/teams/proj/delete",
		"/api/sources/kukeon/inspect",
		"/api/sources/extra/delete",
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

// initFixture is a fresh host behind the teams routes: no Team, no
// TeamDefaults and no Source. With sources it composes from this repository
// in place of git; without, it fetches like kuke serve does.
func initFixture(t *testing.T, sources bool) (*teamClient, http.Handler) {
	t.Helper()
	c := newTeamClient()
	api := &teamAPI{catalog: connectortest.Catalog(t)}
	if sources {
		api.sources = map[string]fs.FS{config.KukeonSourceName: os.DirFS("../..")}
	}
	mux := http.NewServeMux()
	api.register(mux, func(context.Context) (kukeonv1.Client, error) { return c, nil })
	return c, mux
}

func postInit(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	return connectorRequest(t, h, http.MethodPost, "/api/teams/init", "{}")
}

// TestTeamInit is POST /api/teams/init (issue #1694): kuke team init with the
// server's client. It stores the kuke and default system teams and returns
// what each applied; a second call is a 409 that names the reapply.
func TestTeamInit(t *testing.T) {
	c, h := initFixture(t, true)
	w := postInit(t, h)
	if w.Code != http.StatusOK {
		t.Fatalf("init: %d %s", w.Code, w.Body)
	}
	got := decodeBody[initView](t, w)
	if len(got.Teams) != 2 || got.Teams[0].Team != config.SystemTeamName ||
		got.Teams[1].Team != config.DefaultTeamName {
		t.Fatalf("teams = %+v", got.Teams)
	}
	for _, team := range got.Teams {
		if team.Resources == 0 || len(team.Configs) == 0 || len(team.Blueprints) == 0 {
			t.Fatalf("team %s summary = %+v", team.Team, team)
		}
	}
	if !slices.Contains(got.Teams[0].Configs, "kuke-majordomo-claude") ||
		!slices.Contains(got.Teams[0].Configs, "kuke-majordomo-claude-health-sweep") ||
		!slices.Contains(got.Teams[1].Configs, "default-agent-claude") {
		t.Fatalf("configs = %v / %v", got.Teams[0].Configs, got.Teams[1].Configs)
	}
	if c.teams[config.SystemTeamName] == nil || c.teams[config.DefaultTeamName] == nil || c.defaults == nil ||
		c.sources[config.KukeonSourceName] == nil {
		t.Fatalf("stored: teams=%d defaults=%t sources=%d", len(c.teams), c.defaults != nil, len(c.sources))
	}

	writes := c.writes
	w = postInit(t, h)
	expectError(t, w, w.Body.Bytes(), http.StatusConflict, "already_initialized")
	if body := w.Body.String(); !strings.Contains(body, "reapply") || strings.Contains(body, "--force") {
		t.Fatalf("409 message = %s", body)
	}
	if c.writes != writes {
		t.Fatalf("a refused init wrote %d times", c.writes-writes)
	}
}

// TestTeamInitRequiresJSON: like reapply and delete, the route cannot be
// driven by a cross-site form.
func TestTeamInitRequiresJSON(t *testing.T) {
	c, h := initFixture(t, true)
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5129/api/teams/init", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	expectError(t, w, w.Body.Bytes(), http.StatusBadRequest, "invalid_request")
	w = connectorRequest(t, h, http.MethodPost, "/api/teams/init", `{"force":true}`)
	expectError(t, w, w.Body.Bytes(), http.StatusBadRequest, "invalid_request")
	if c.writes != 0 || len(c.teams) != 0 {
		t.Fatalf("a refused request stored something: writes=%d teams=%d", c.writes, len(c.teams))
	}
}

// TestTeamInitWithoutGit: on a host with no git the fetch of the kukeon
// source cannot start, and the reply says where to install it.
func TestTeamInitWithoutGit(t *testing.T) {
	c, h := initFixture(t, false)
	t.Setenv("PATH", t.TempDir())
	w := postInit(t, h)
	expectError(t, w, w.Body.Bytes(), http.StatusUnprocessableEntity, "git_missing")
	if body := w.Body.String(); !strings.Contains(body, "install git on the host running kuke serve") {
		t.Fatalf("message = %s", body)
	}
	if c.writes != 0 || len(c.teams) != 0 {
		t.Fatalf("a failed init stored something: writes=%d teams=%d", c.writes, len(c.teams))
	}
}

// TestTeamInitSourceFetchFailure: a git that cannot reach the kukeon source
// is a team failure with the fetch's guidance, never git's own output, and
// nothing is stored.
func TestTeamInitSourceFetchFailure(t *testing.T) {
	c, h := initFixture(t, false)
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'fatal: canary-git-output' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GIT_SSH_COMMAND", "ssh")
	w := postInit(t, h)
	expectError(t, w, w.Body.Bytes(), http.StatusUnprocessableEntity, "team_failed")
	body := w.Body.String()
	if !strings.Contains(body, "git source access failed") || strings.Contains(body, "canary-git-output") {
		t.Fatalf("message = %s", body)
	}
	if c.writes != 0 || len(c.teams) != 0 {
		t.Fatalf("a failed init stored something: writes=%d teams=%d", c.writes, len(c.teams))
	}
}
