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
	"net/http"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// TestSourcesListCreateInspectDelete is AC1 and AC6 over HTTP: the stored
// Sources with their users and defaults, create with an identity, inspect
// stored and unstored, and delete refused while in use.
//
//nolint:gocyclo,cyclop // one HTTP sequence over the same stored state
func TestSourcesListCreateInspectDelete(t *testing.T) {
	c, h := teamFixture(t)
	c.putProj(t)
	w := connectorRequest(t, h, http.MethodGet, "/api/sources", "")
	list := decodeBody[sourcesListView](t, w)
	if w.Code != http.StatusOK || len(list.Sources) != 2 || list.Sources[0].Name != "kukeon" ||
		len(list.DefaultHarnesses) != 3 || list.DefaultRoles == nil {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	kukeon, proj := list.Sources[0], list.Sources[1]
	if len(kukeon.DefaultHarnesses) != 3 || len(kukeon.DefaultRoles) != 0 || proj.Name != "proj" {
		t.Fatalf("sources = %+v", list.Sources)
	}
	uses := map[string]string{}
	for _, use := range proj.UsedBy {
		uses[use.As] = use.Team
	}
	if uses["project"] != "proj" {
		t.Fatalf("proj used by = %+v", proj.UsedBy)
	}
	if strings.Contains(w.Body.String(), "clonePath") {
		t.Fatalf("a stored Source has no clone path: %s", w.Body)
	}

	w = connectorRequest(t, h, http.MethodPost, "/api/sources", jsonBody(t, map[string]any{
		"name": "extra", "url": "https://example.test/extra.git", "branch": "main", "identity": "git-ssh",
	}))
	created := decodeBody[sourceChangeView](t, w)
	if w.Code != http.StatusOK || created.Action != "created" || created.Source.Identity != "git-ssh" ||
		created.Discovered == nil || len(created.Discovered.Roles) == 0 || created.UsedBy == nil {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var doc struct {
		Spec config.SourceSpec `yaml:"spec"`
	}
	if err := yaml.Unmarshal(c.sources["extra"], &doc); err != nil || doc.Spec.Git == nil ||
		doc.Spec.Git.IdentityRef.Name != "git-ssh" || doc.Spec.Git.IdentityRef.Realm != "default" {
		t.Fatalf("stored extra = %+v (%v)", doc.Spec, err)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/sources",
		jsonBody(t, map[string]any{"name": "extra", "url": "https://example.test/extra.git", "branch": "main"}))
	expectError(t, w, w.Body.Bytes(), http.StatusConflict, "already_exists")
	w = connectorRequest(t, h, http.MethodPost, "/api/sources", jsonBody(t, map[string]any{
		"name": "extra", "url": "https://example.test/extra.git", "tag": "v1", "force": true,
	}))
	if replaced := decodeBody[sourceChangeView](t, w); w.Code != http.StatusOK || replaced.Action != "updated" {
		t.Fatalf("force: %d %s", w.Code, w.Body)
	}

	w = connectorRequest(t, h, http.MethodPost, "/api/sources/kukeon/inspect", "{}")
	contents := decodeBody[contentsView](t, w)
	if w.Code != http.StatusOK || contents.Source.Name != "kukeon" || len(contents.Harnesses) == 0 {
		t.Fatalf("inspect: %d %s", w.Code, w.Body)
	}
	defaults := 0
	for _, harness := range contents.Harnesses {
		if harness.Default {
			defaults++
		}
	}
	var scheduled []string
	for _, role := range contents.Roles {
		for _, task := range role.Tasks {
			if task.Schedule != "" {
				scheduled = append(scheduled, task.Name+"@"+task.Schedule)
			}
		}
	}
	tasks := strings.Join(scheduled, ",")
	if defaults != 3 || !strings.Contains(tasks, "health-sweep@@hourly") {
		t.Fatalf("defaults marked = %d, tasks = %q: %s", defaults, tasks, w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/sources/inspect", jsonBody(t, map[string]any{
		"url": "https://example.test/extra.git", "branch": "main", "identity": "git-ssh",
	}))
	contents = decodeBody[contentsView](t, w)
	if w.Code != http.StatusOK || contents.Source.Identity != "git-ssh" || len(contents.Roles) == 0 {
		t.Fatalf("inspect address: %d %s", w.Code, w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/sources/inspect", jsonBody(t, map[string]any{"url": "x y"}))
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("inspect of a bad address: %d %s", w.Code, w.Body)
	}

	w = connectorRequest(t, h, http.MethodPost, "/api/sources/kukeon/delete", "{}")
	expectError(t, w, w.Body.Bytes(), http.StatusConflict, "in_use")
	if !strings.Contains(w.Body.String(), "default harness") {
		t.Fatalf("refusal does not name the use: %s", w.Body)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/sources/extra/delete", "{}")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":true`) || c.sources["extra"] != nil {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/sources/nothing/inspect", "/api/sources/nothing/delete"} {
		w = connectorRequest(t, h, http.MethodPost, path, "{}")
		expectError(t, w, w.Body.Bytes(), http.StatusNotFound, "not_found")
	}
}

// TestSourcesListsATeamsSource: a Source only a team's declaration defines
// is stored, listed with the team that uses it, and not deleted while used.
func TestSourcesListsATeamsSource(t *testing.T) {
	c, h := teamFixture(t)
	c.putProj(t)
	declaration := `apiVersion: v1beta1
kind: Team
metadata:
  name: proj
spec:
  sources:
    - {name: extra, url: https://example.test/extra.git, branch: main}
  roles:
    - {name: lead, source: extra, path: team/roles/majordomo}
`
	w := connectorRequest(t, h, http.MethodPost, "/api/teams",
		jsonBody(t, map[string]any{"name": "proj", "declaration": declaration}))
	if w.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", w.Code, w.Body)
	}
	w = connectorRequest(t, h, http.MethodGet, "/api/sources", "")
	list := decodeBody[sourcesListView](t, w)
	names := make([]string, 0, len(list.Sources))
	for _, source := range list.Sources {
		names = append(names, source.Name)
		if source.Name == "extra" && (len(source.UsedBy) != 2 || source.UsedBy[0].Team != "proj") {
			t.Fatalf("extra used by = %+v", source.UsedBy)
		}
	}
	if strings.Join(names, ",") != "extra,kukeon,proj" {
		t.Fatalf("sources = %v", names)
	}
	w = connectorRequest(t, h, http.MethodPost, "/api/sources/extra/delete", "{}")
	expectError(t, w, w.Body.Bytes(), http.StatusConflict, "in_use")
	if !strings.Contains(w.Body.String(), "team proj") {
		t.Fatalf("refusal does not name the team: %s", w.Body)
	}
}
