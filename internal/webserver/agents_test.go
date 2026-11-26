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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// agentClient stands in for kukeond behind the New agent and display-name
// routes (issue #1692).
type agentClient struct {
	kukeonv1.FakeClient

	templates map[string]bool
	configs   map[string]v1beta1.CellConfigDoc
	cells     []kukeonv1.CellFromConfigRequest
	started   []v1beta1.CellDoc
	renames   []kukeonv1.SetDisplayNameRequest
	startErr  error
	renameErr error
}

func newAgentClient(harnesses ...string) *agentClient {
	c := &agentClient{templates: map[string]bool{}, configs: map[string]v1beta1.CellConfigDoc{}}
	for _, h := range harnesses {
		c.templates[h] = true
	}
	return c
}

func (c *agentClient) Close() error { return nil }

func (c *agentClient) GetConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	for harness := range c.templates {
		if doc.Metadata.Name == "default-agent-"+harness {
			return kukeonv1.GetConfigResult{MetadataExists: true, Config: v1beta1.CellConfigDoc{
				Metadata: v1beta1.CellConfigMetadata{
					Name: doc.Metadata.Name, Realm: "default", Space: "default", Stack: "default",
					Labels: map[string]string{
						v1beta1.LabelTeam: "default", v1beta1.LabelTemplate: "true",
						v1beta1.LabelRole: "agent", v1beta1.LabelHarness: harness,
					},
				},
				Spec: v1beta1.CellConfigSpec{Values: map[string]string{"STATE_VOLUME": doc.Metadata.Name + "-state"}},
			}}, nil
		}
	}
	return kukeonv1.GetConfigResult{}, nil
}

func (c *agentClient) CreateConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
	c.configs[doc.Metadata.Name] = doc
	return kukeonv1.CreateConfigResult{Config: doc, Created: true}, nil
}

func (c *agentClient) CreateCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.CreateCellFromConfigResult, error) {
	c.cells = append(c.cells, req)
	return kukeonv1.CreateCellFromConfigResult{CreateCellResult: kukeonv1.CreateCellResult{Cell: v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: req.Config.Name + "-4d5e6f"},
		Spec:     v1beta1.CellSpec{RealmID: req.Config.Realm, SpaceID: req.Config.Space, StackID: req.Config.Stack},
	}}}, nil
}

func (c *agentClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	c.started = append(c.started, doc)
	if c.startErr != nil {
		return kukeonv1.StartCellResult{}, c.startErr
	}
	return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
}

func (c *agentClient) SetDisplayName(
	_ context.Context, req kukeonv1.SetDisplayNameRequest,
) (kukeonv1.SetDisplayNameResult, error) {
	c.renames = append(c.renames, req)
	if c.renameErr != nil {
		return kukeonv1.SetDisplayNameResult{}, c.renameErr
	}
	var annotations map[string]string
	if req.DisplayName != "" {
		annotations = map[string]string{v1beta1.AnnotationDisplayName: req.DisplayName}
	}
	return kukeonv1.SetDisplayNameResult{DisplayName: req.DisplayName, Annotations: annotations, Changed: true}, nil
}

func agentHandler(t *testing.T, c *agentClient) http.Handler {
	t.Helper()
	handler, err := newHandler("http://localhost:5129", "test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil })
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type agentErrorBody struct {
	Error struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Step    string   `json:"step"`
		Config  agentRef `json:"config"`
		Cell    agentRef `json:"cell"`
	} `json:"error"`
}

func decodeAgentBody(t *testing.T, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
}

func TestCreateAgentHappyPath(t *testing.T) {
	c := newAgentClient("claude", "codex")
	w := postCreate(t, agentHandler(t, c), "/api/agents", map[string]any{"harness": "codex", "displayName": "Docs bot"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got struct {
		Config agentRef `json:"config"`
		Cell   agentRef `json:"cell"`
	}
	decodeAgentBody(t, w, &got)
	if !regexp.MustCompile(`^default-agent-codex-[0-9a-f]{6}$`).MatchString(got.Config.Name) ||
		got.Config.Realm != "default" || got.Config.Space != "default" || got.Config.Stack != "default" {
		t.Fatalf("config = %+v", got.Config)
	}
	if got.Config.Annotations[v1beta1.AnnotationDisplayName] != "Docs bot" ||
		got.Config.Annotations[v1beta1.AnnotationAgentTemplate] != "default-agent-codex" ||
		got.Config.Labels[v1beta1.LabelTeam] != "" || got.Config.Labels[v1beta1.LabelTemplate] != "" {
		t.Fatalf("config metadata = %+v", got.Config)
	}
	if got.Cell.Name != got.Config.Name+"-4d5e6f" || got.Cell.Stack != "default" {
		t.Fatalf("cell = %+v", got.Cell)
	}
	if len(c.cells) != 1 || c.cells[0].Start || c.cells[0].Name != "" || len(c.started) != 1 {
		t.Fatalf("cell requests %+v, starts %d", c.cells, len(c.started))
	}
}

func TestCreateAgentRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"missing template": {`{"harness":"codex"}`, http.StatusNotFound, "template_not_found"},
		"unknown harness":  {`{"harness":"opencode"}`, http.StatusBadRequest, codeInvalidRequest},
		"bad name":         {`{"harness":"claude","displayName":"a\nb"}`, http.StatusBadRequest, codeInvalidRequest},
		"unknown field":    {`{"harness":"claude","team":"x"}`, http.StatusBadRequest, codeInvalidRequest},
		"bad scope":        {`{"harness":"claude","scope":{"realm":"r","stack":"s"}}`, http.StatusBadRequest, codeInvalidRequest},
	} {
		t.Run(name, func(t *testing.T) {
			c := newAgentClient("claude")
			w := postCreateRaw(t, agentHandler(t, c), "/api/agents", tc.body, "http://localhost:5129")
			var body agentErrorBody
			decodeAgentBody(t, w, &body)
			if w.Code != tc.status || body.Error.Code != tc.code {
				t.Fatalf("status %d code %q: %s", w.Code, body.Error.Code, w.Body)
			}
			if len(c.configs) != 0 {
				t.Fatal("a config was stored")
			}
		})
	}
}

// TestCreateAgentStartFailureKeepsTheConfig: a failure after the Config is
// stored names the step and what exists.
func TestCreateAgentStartFailureKeepsTheConfig(t *testing.T) {
	c := newAgentClient("claude")
	c.startErr = fmt.Errorf("%w: no room", errdefs.ErrInvalidCellState)
	w := postCreate(t, agentHandler(t, c), "/api/agents", map[string]any{"harness": "claude"})
	var body agentErrorBody
	decodeAgentBody(t, w, &body)
	if w.Code != http.StatusConflict || body.Error.Step != "start-cell" || body.Error.Config.Name == "" ||
		body.Error.Cell.Name != body.Error.Config.Name+"-4d5e6f" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if _, kept := c.configs[body.Error.Config.Name]; !kept {
		t.Fatal("the stored config is not the one reported")
	}
}

func TestAgentTemplates(t *testing.T) {
	c := newAgentClient("codex")
	request := httptest.NewRequest(http.MethodGet, "http://localhost:5129/api/agents/templates", nil)
	w := httptest.NewRecorder()
	agentHandler(t, c).ServeHTTP(w, request)
	var got struct {
		Templates []agentTemplateView `json:"templates"`
	}
	decodeAgentBody(t, w, &got)
	if w.Code != http.StatusOK || len(got.Templates) != 2 || got.Templates[0].Harness != "claude" ||
		got.Templates[0].Exists || !got.Templates[1].Exists || got.Templates[1].Name != "default-agent-codex" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

func TestDisplayNameRoutes(t *testing.T) {
	c := newAgentClient()
	handler := agentHandler(t, c)
	w := postCreate(t, handler, "/api/configs/display-name", map[string]any{
		"config":      map[string]string{"name": "default-agent-claude-1a2b3c", "realm": "default", "space": "default"},
		"displayName": "Reviewer",
	})
	var cfg struct {
		Config agentRef `json:"config"`
	}
	decodeAgentBody(t, w, &cfg)
	if w.Code != http.StatusOK || cfg.Config.Annotations[v1beta1.AnnotationDisplayName] != "Reviewer" {
		t.Fatalf("config rename %d: %s", w.Code, w.Body)
	}
	want := kukeonv1.DisplayNameTarget{
		Kind: kukeonv1.DisplayNameTargetConfig, Name: "default-agent-claude-1a2b3c", Realm: "default", Space: "default",
	}
	if len(c.renames) != 1 || c.renames[0].Target != want || c.renames[0].DisplayName != "Reviewer" {
		t.Fatalf("renames = %+v", c.renames)
	}

	path := "/api/realms/default/spaces/default/stacks/default/cells/default-agent-claude-1a2b3c-4d5e6f/display-name"
	w = postCreate(t, handler, path, map[string]any{"displayName": ""})
	var cell struct {
		Cell agentRef `json:"cell"`
	}
	decodeAgentBody(t, w, &cell)
	if w.Code != http.StatusOK || cell.Cell.Name != "default-agent-claude-1a2b3c-4d5e6f" ||
		cell.Cell.Annotations != nil {
		t.Fatalf("cell rename %d: %s", w.Code, w.Body)
	}
	if got := c.renames[1].Target; got.Kind != kukeonv1.DisplayNameTargetCell || got.Stack != "default" {
		t.Fatalf("cell target = %+v", got)
	}

	// The daemon's refusal of a bad name is a bad request; an unsafe target
	// never reaches it.
	c.renameErr = fmt.Errorf("%w: too long", errdefs.ErrInvalidDisplayName)
	if w = postCreate(t, handler, path, map[string]any{"displayName": "x"}); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid name: %d %s", w.Code, w.Body)
	}
	renames := len(c.renames)
	w = postCreate(t, handler, "/api/configs/display-name", map[string]any{
		"config": map[string]string{"name": "../x", "realm": "default"}, "displayName": "x",
	})
	if w.Code != http.StatusBadRequest || len(c.renames) != renames {
		t.Fatalf("unsafe config: %d %s", w.Code, w.Body)
	}
}
