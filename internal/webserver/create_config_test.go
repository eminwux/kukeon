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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// configFormClient stands in for kukeond behind the "create a config from a
// blueprint form" routes: it records the draft a preview rendered and the
// document a create saved.
type configFormClient struct {
	kukeonv1.FakeClient

	existing     bool
	digest       string
	getConfigs   int
	previews     []kukeonv1.CellFromConfigRequest
	created      []v1beta1.CellConfigDoc
	createExists bool
}

func (c *configFormClient) Close() error { return nil }

func (c *configFormClient) GetConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	c.getConfigs++
	return kukeonv1.GetConfigResult{Config: doc, MetadataExists: c.existing}, nil
}

func (c *configFormClient) PreviewCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.PreviewCellFromConfigResult, error) {
	c.previews = append(c.previews, req)
	digest := c.digest
	if digest == "" {
		digest = "sha256:render"
	}
	return kukeonv1.PreviewCellFromConfigResult{
		Cell: v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: req.Config.Name + "-abc123"},
			Spec: v1beta1.CellSpec{
				RealmID:    req.Config.Realm,
				Containers: []v1beta1.ContainerSpec{{ID: "work", Env: []string{"TOKEN=leak"}}},
			},
		},
		Digest: digest,
		Values: []v1beta1.RenderedValue{
			{Kind: v1beta1.RenderKindValue, Key: "ROLE_PATH", Value: "dev", Origin: v1beta1.RenderOriginConfig},
			{
				Kind:   v1beta1.RenderKindGit,
				Key:    "project.author",
				Value:  "bot <bot@example.com>",
				Origin: v1beta1.RenderOriginConfig,
			},
			{
				Kind:   v1beta1.RenderKindGit,
				Key:    "project.credential",
				Value:  "git-ssh (ssh-key)",
				Origin: v1beta1.RenderOriginRealm,
			},
			{
				Kind:   v1beta1.RenderKindGit,
				Key:    "project.url",
				Value:  "git@github.com:o/p.git",
				Origin: v1beta1.RenderOriginConfig,
			},
		},
	}, nil
}

func (c *configFormClient) CreateConfig(
	_ context.Context, doc v1beta1.CellConfigDoc,
) (kukeonv1.CreateConfigResult, error) {
	c.created = append(c.created, doc)
	return kukeonv1.CreateConfigResult{Config: doc, Created: !c.createExists}, nil
}

func configFormHandler(t *testing.T, c *configFormClient) http.Handler {
	t.Helper()
	handler, err := newHandler(
		"http://localhost:5129",
		"test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func formRequest() createFromBlueprintRequest {
	return createFromBlueprintRequest{
		Config: v1beta1.CellConfigMetadata{Name: "kukeon-dev", Realm: "default", Space: "default", Stack: "kukeon"},
		Mode:   " headless ",
		Spec: v1beta1.CellConfigSpec{
			Blueprint:  v1beta1.CellConfigBlueprintRef{Name: "claude", Realm: "default"},
			Values:     map[string]string{"ROLE_PATH": "dev"},
			Components: []string{"github"},
			Requires:   []string{"go"},
			Modes:      &v1beta1.CellConfigModes{Enabled: []string{"interactive", "headless"}, Default: "interactive"},
			Repos: map[string]v1beta1.CellConfigRepoFill{"project": {
				URL: "git@github.com:o/p.git", Branch: "main",
				Git: &v1beta1.RepoGit{
					Author: &v1beta1.GitIdentity{Name: "bot", Email: "bot@example.com"},
					Credential: &v1beta1.GitCredential{
						SecretRef: &v1beta1.ContainerSecretRef{Name: "deploy", Realm: "default"},
					},
					Signing: &v1beta1.GitSigning{
						SecretRef: &v1beta1.ContainerSecretRef{Name: "deploy", Realm: "default"},
					},
				},
			}},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"github-token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "gh-token", Realm: "default"}},
			},
		},
	}
}

type formPreview struct {
	Config   v1beta1.CellConfigDoc   `json:"config"`
	Document v1beta1.CellDoc         `json:"document"`
	Preview  string                  `json:"preview"`
	Digest   string                  `json:"digest"`
	Values   []v1beta1.RenderedValue `json:"values"`
}

func previewForm(t *testing.T, handler http.Handler, input createFromBlueprintRequest) formPreview {
	t.Helper()
	w := postCreate(t, handler, "/api/configs/from-blueprint/preview", input)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	var preview formPreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	return preview
}

// TestConfigFromBlueprintPreviewThenCreate walks the reviewed path: the draft
// is rendered by the daemon without being saved, the browser gets the merge
// explanation including each repo's git identity, and the create saves exactly
// the reviewed document once.
//
//nolint:gocyclo,cyclop // a flat list of independent assertions on one result.
func TestConfigFromBlueprintPreviewThenCreate(t *testing.T) {
	c := &configFormClient{}
	handler := configFormHandler(t, c)
	input := formRequest()

	preview := previewForm(t, handler, input)
	if len(c.previews) != 1 || len(c.created) != 0 {
		t.Fatalf("preview made %d renders and %d saves, want 1 and 0", len(c.previews), len(c.created))
	}
	req := c.previews[0]
	if req.Draft == nil || req.Draft.Metadata.Name != "kukeon-dev" || req.Draft.Kind != v1beta1.KindCellConfig ||
		req.Draft.APIVersion != v1beta1.APIVersionV1Beta1 || req.Draft.Spec.Blueprint.Name != "claude" {
		t.Fatalf("preview did not render the unsaved draft: %+v", req.Draft)
	}
	if req.Mode != "headless" || req.Config.Name != "kukeon-dev" || req.Config.Stack != "kukeon" {
		t.Fatalf("preview request = mode %q config %+v", req.Mode, req.Config)
	}
	if got := req.Draft.Spec.Repos["project"].Git; got == nil || got.Credential.SecretRef.Name != "deploy" {
		t.Fatalf("per-source identity lost on the way to the daemon: %+v", got)
	}
	if preview.Preview == "" || preview.Digest != "sha256:render" || preview.Config.Spec.Components[0] != "github" {
		t.Fatalf("preview response = %+v", preview)
	}
	// Container env is redacted and scalar values are withheld, but the git
	// identity each repo ends up with is shown with its origin.
	if len(preview.Document.Spec.Containers) != 1 || preview.Document.Spec.Containers[0].Env != nil {
		t.Fatalf("preview leaked container env: %+v", preview.Document.Spec.Containers)
	}
	shown := map[string]v1beta1.RenderedValue{}
	for _, v := range preview.Values {
		shown[v.Kind+":"+v.Key] = v
	}
	if shown["value:ROLE_PATH"].Value != "" {
		t.Fatalf("scalar value shown in the browser: %+v", shown["value:ROLE_PATH"])
	}
	if got := shown["git:project.credential"]; got.Value != "git-ssh (ssh-key)" ||
		got.Origin != v1beta1.RenderOriginRealm {
		t.Fatalf("git credential explanation = %+v", got)
	}
	if got := shown["git:project.author"]; got.Value == "" || got.Origin != v1beta1.RenderOriginConfig {
		t.Fatalf("git author explanation = %+v", got)
	}

	// An unreviewed create is refused before anything is saved.
	if w := postCreate(t, handler, "/api/configs/from-blueprint/create", input); w.Code != http.StatusBadRequest ||
		len(c.created) != 0 {
		t.Fatalf("unreviewed create: %d saves=%d %s", w.Code, len(c.created), w.Body)
	}

	input.Preview = preview.Preview
	w := postCreate(t, handler, "/api/configs/from-blueprint/create", input)
	if w.Code != http.StatusOK || len(c.created) != 1 {
		t.Fatalf("create: %d saves=%d %s", w.Code, len(c.created), w.Body)
	}
	var saved v1beta1.CellConfigDoc
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	doc := c.created[0]
	if saved.Metadata.Name != "kukeon-dev" || doc.Metadata.Stack != "kukeon" || doc.Kind != v1beta1.KindCellConfig ||
		doc.Spec.Secrets["github-token"].SecretRef.Name != "gh-token" || doc.Spec.Modes.Default != "interactive" ||
		doc.Spec.Requires[0] != "go" || doc.Spec.Values["ROLE_PATH"] != "dev" {
		t.Fatalf("saved config = %+v", doc)
	}
}

// TestConfigFromBlueprintStaleTokenRejected covers both ways a reviewed
// preview goes stale: the form changed, or what the daemon renders changed
// (a blueprint or inherited default was edited meanwhile).
func TestConfigFromBlueprintStaleTokenRejected(t *testing.T) {
	c := &configFormClient{}
	handler := configFormHandler(t, c)
	input := formRequest()
	input.Preview = previewForm(t, handler, input).Preview

	changed := input
	changed.Spec.Values = map[string]string{"ROLE_PATH": "pm"}
	if w := postCreate(t, handler, "/api/configs/from-blueprint/create", changed); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "preview again") {
		t.Fatalf("changed form: %d %s", w.Code, w.Body)
	}
	c.digest = "sha256:other"
	if w := postCreate(t, handler, "/api/configs/from-blueprint/create", input); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "preview again") {
		t.Fatalf("changed render: %d %s", w.Code, w.Body)
	}
	if len(c.created) != 0 {
		t.Fatalf("stale token saved %d configs", len(c.created))
	}
}

func TestConfigFromBlueprintExistingNameRejected(t *testing.T) {
	c := &configFormClient{existing: true}
	handler := configFormHandler(t, c)
	for _, action := range []string{"preview", "create"} {
		w := postCreate(t, handler, "/api/configs/from-blueprint/"+action, formRequest())
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "already exists") {
			t.Fatalf("%s over an existing config: %d %s", action, w.Code, w.Body)
		}
	}
	if len(c.previews) != 0 || len(c.created) != 0 {
		t.Fatalf("existing name reached render (%d) or save (%d)", len(c.previews), len(c.created))
	}

	// A config that appears between the review and the save is not overwritten.
	c = &configFormClient{createExists: true}
	handler = configFormHandler(t, c)
	input := formRequest()
	input.Preview = previewForm(t, handler, input).Preview
	if w := postCreate(t, handler, "/api/configs/from-blueprint/create", input); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "already exists") {
		t.Fatalf("lost create race: %d %s", w.Code, w.Body)
	}
}

// TestConfigFromBlueprintRejectsUnsafeCoordinates locks that no coordinate of
// the form — the Config's, its blueprint's, or any secret's — reaches the
// daemon when it would escape the metadata tree.
func TestConfigFromBlueprintRejectsUnsafeCoordinates(t *testing.T) {
	unsafe := &v1beta1.ContainerSecretRef{Name: "../secret", Realm: "default"}
	for _, tc := range []struct {
		name   string
		mutate func(*createFromBlueprintRequest)
	}{
		{"config name", func(r *createFromBlueprintRequest) { r.Config.Name = "../cfg" }},
		{"config name is not a hierarchy name", func(r *createFromBlueprintRequest) { r.Config.Name = "has_underscore" }},
		{"config realm", func(r *createFromBlueprintRequest) { r.Config.Realm = ".." }},
		{"config realm missing", func(r *createFromBlueprintRequest) { r.Config.Realm = "" }},
		{"config scope gap", func(r *createFromBlueprintRequest) { r.Config.Space = "" }},
		{"config stack", func(r *createFromBlueprintRequest) { r.Config.Stack = "nested/stack" }},
		{"blueprint name", func(r *createFromBlueprintRequest) { r.Spec.Blueprint.Name = ".." }},
		{"blueprint missing", func(r *createFromBlueprintRequest) { r.Spec.Blueprint = v1beta1.CellConfigBlueprintRef{} }},
		{"blueprint space", func(r *createFromBlueprintRequest) { r.Spec.Blueprint.Space = `..\up` }},
		{"secret fill", func(r *createFromBlueprintRequest) {
			r.Spec.Secrets["github-token"] = v1beta1.CellConfigSecretFill{SecretRef: unsafe}
		}},
		{"secret fill realm", func(r *createFromBlueprintRequest) {
			r.Spec.Secrets["github-token"] = v1beta1.CellConfigSecretFill{
				SecretRef: &v1beta1.ContainerSecretRef{Name: "gh-token", Realm: "a/b"},
			}
		}},
		{"repo credential", func(r *createFromBlueprintRequest) {
			fill := r.Spec.Repos["project"]
			fill.Git = &v1beta1.RepoGit{Credential: &v1beta1.GitCredential{SecretRef: unsafe}}
			r.Spec.Repos["project"] = fill
		}},
		{"repo signing", func(r *createFromBlueprintRequest) {
			fill := r.Spec.Repos["project"]
			fill.Git = &v1beta1.RepoGit{Signing: &v1beta1.GitSigning{SecretRef: unsafe}}
			r.Spec.Repos["project"] = fill
		}},
		{"repo allowed signers", func(r *createFromBlueprintRequest) {
			fill := r.Spec.Repos["project"]
			fill.Git = &v1beta1.RepoGit{Signing: &v1beta1.GitSigning{
				SecretRef:         &v1beta1.ContainerSecretRef{Name: "deploy", Realm: "default"},
				AllowedSignersRef: unsafe,
			}}
			r.Spec.Repos["project"] = fill
		}},
		{"default credential", func(r *createFromBlueprintRequest) {
			r.Spec.Git = &v1beta1.ContainerGit{Credentials: []v1beta1.GitCredential{{Host: "github.com", SecretRef: unsafe}}}
		}},
		{"default signing", func(r *createFromBlueprintRequest) {
			r.Spec.Git = &v1beta1.ContainerGit{Signing: &v1beta1.GitSigning{SecretRef: unsafe}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &configFormClient{}
			handler := configFormHandler(t, c)
			input := formRequest()
			tc.mutate(&input)
			for _, action := range []string{"preview", "create"} {
				w := postCreate(t, handler, "/api/configs/from-blueprint/"+action, input)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("%s: %d %s", action, w.Code, w.Body)
				}
			}
			if c.getConfigs != 0 || len(c.previews) != 0 || len(c.created) != 0 {
				t.Fatalf("unsafe input reached the daemon: lookups=%d renders=%d saves=%d",
					c.getConfigs, len(c.previews), len(c.created))
			}
		})
	}
}

// TestConfigFromBlueprintRequiresJSON locks the mutation guard on both
// actions, preview included, and the strict one-document body. A missing
// Origin is a same-origin request under the shared access policy.
func TestConfigFromBlueprintRequiresJSON(t *testing.T) {
	data, err := json.Marshal(formRequest())
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, tc := range []struct {
		name, origin, contentType, body string
	}{
		{"foreign origin", "http://evil.example", "application/json", body},
		{"form content type", "http://localhost:5129", "application/x-www-form-urlencoded", body},
		{"missing content type", "http://localhost:5129", "", body},
		{
			"unknown top-level field", "http://localhost:5129", "application/json",
			strings.Replace(body, `{"config":`, `{"extra":true,"config":`, 1),
		},
		{
			"unknown spec field", "http://localhost:5129", "application/json",
			strings.Replace(body, `"spec":{`, `"spec":{"privileged":true,`, 1),
		},
		{"two documents", "http://localhost:5129", "application/json", body + body},
		{"not json", "http://localhost:5129", "application/json", "name=x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.body == body && (strings.HasPrefix(tc.name, "unknown")) {
				t.Fatal("fixture did not inject the unknown field")
			}
			c := &configFormClient{}
			handler := configFormHandler(t, c)
			for _, action := range []string{"preview", "create"} {
				r := httptest.NewRequest(
					http.MethodPost,
					"http://localhost:5129/api/configs/from-blueprint/"+action,
					strings.NewReader(tc.body),
				)
				if tc.origin != "" {
					r.Header.Set("Origin", tc.origin)
				}
				if tc.contentType != "" {
					r.Header.Set("Content-Type", tc.contentType)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				// The server's own Host/Origin guard answers a foreign origin
				// with 403 before the route sees it.
				if w.Code != http.StatusBadRequest && w.Code != http.StatusForbidden {
					t.Fatalf("%s: %d %s", action, w.Code, w.Body)
				}
			}
			if c.getConfigs != 0 || len(c.previews) != 0 || len(c.created) != 0 {
				t.Fatal("rejected request reached the daemon")
			}
		})
	}
}

// TestConfigFromBlueprintRoutesArePostOnly keeps a GET (a link, an image tag)
// from ever rendering or saving a config.
func TestConfigFromBlueprintRoutesArePostOnly(t *testing.T) {
	c := &configFormClient{}
	handler := configFormHandler(t, c)
	w := httptest.NewRecorder()
	handler.ServeHTTP(
		w,
		httptest.NewRequest(http.MethodGet, "http://localhost:5129/api/configs/from-blueprint/create", nil),
	)
	if len(c.previews) != 0 || len(c.created) != 0 {
		t.Fatalf("GET reached the daemon (status %d)", w.Code)
	}
}
