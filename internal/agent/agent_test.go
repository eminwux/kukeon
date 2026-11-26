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

package agent_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/agent"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// fakeClient stores Configs and cells in memory, with errors to inject.
type fakeClient struct {
	kukeonv1.FakeClient

	configs   map[string]v1beta1.CellConfigDoc
	created   []v1beta1.CellConfigDoc
	cells     []kukeonv1.CellFromConfigRequest
	started   []v1beta1.CellDoc
	createErr error
	startErr  error
}

func newFake(templates ...string) *fakeClient {
	f := &fakeClient{configs: map[string]v1beta1.CellConfigDoc{}}
	for _, harness := range templates {
		f.configs["default-agent-"+harness] = template(harness)
	}
	return f
}

func template(harness string) v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name: "default-agent-" + harness, Realm: "default", Space: "default", Stack: "default",
			Labels: map[string]string{
				v1beta1.LabelTeam: "default", v1beta1.LabelRole: "agent",
				v1beta1.LabelHarness: harness, v1beta1.LabelTemplate: "true",
			},
		},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: harness, Realm: "default"},
			Values: map[string]string{
				"STATE_VOLUME": "default-agent-" + harness + "-state", "STATE_VOLUME_REALM": "default",
				"ROLE_PATH": "team/roles/agent",
			},
			Repos:      map[string]v1beta1.CellConfigRepoFill{"role-source": {}},
			Components: []string{harness + "-login"},
		},
	}
}

func (f *fakeClient) GetConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	got, ok := f.configs[doc.Metadata.Name]
	if !ok || got.Metadata.Realm != doc.Metadata.Realm || got.Metadata.Space != doc.Metadata.Space ||
		got.Metadata.Stack != doc.Metadata.Stack {
		return kukeonv1.GetConfigResult{}, nil
	}
	return kukeonv1.GetConfigResult{Config: got, MetadataExists: true}, nil
}

func (f *fakeClient) CreateConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
	if _, taken := f.configs[doc.Metadata.Name]; taken {
		return kukeonv1.CreateConfigResult{}, errdefs.ErrConfigExists
	}
	f.configs[doc.Metadata.Name] = doc
	f.created = append(f.created, doc)
	return kukeonv1.CreateConfigResult{Config: doc, Created: true}, nil
}

func (f *fakeClient) CreateCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.CreateCellFromConfigResult, error) {
	f.cells = append(f.cells, req)
	if f.createErr != nil {
		return kukeonv1.CreateCellFromConfigResult{}, f.createErr
	}
	cell := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: req.Config.Name + "-4d5e6f"},
		Spec:     v1beta1.CellSpec{RealmID: req.Config.Realm, SpaceID: req.Config.Space, StackID: req.Config.Stack},
	}
	return kukeonv1.CreateCellFromConfigResult{CreateCellResult: kukeonv1.CreateCellResult{Cell: cell}}, nil
}

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	f.started = append(f.started, doc)
	if f.startErr != nil {
		return kukeonv1.StartCellResult{}, f.startErr
	}
	doc.Status.State = v1beta1.CellStateReady
	return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
}

func TestCreateCopiesTheTemplate(t *testing.T) {
	f := newFake("claude", "codex")
	res, err := agent.Create(
		t.Context(),
		f,
		agent.Request{Harness: "claude", DisplayName: " Reviewer "},
		agent.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg := res.Config
	if !regexp.MustCompile(`^default-agent-claude-[0-9a-f]{6}$`).MatchString(cfg.Metadata.Name) {
		t.Fatalf("name = %q", cfg.Metadata.Name)
	}
	if cfg.Metadata.Realm != "default" || cfg.Metadata.Space != "default" || cfg.Metadata.Stack != "default" {
		t.Fatalf("scope = %+v, want the template's", cfg.Metadata)
	}
	wantLabels := map[string]string{v1beta1.LabelRole: "agent", v1beta1.LabelHarness: "claude"}
	if !reflect.DeepEqual(cfg.Metadata.Labels, wantLabels) {
		t.Fatalf("labels = %v, want %v", cfg.Metadata.Labels, wantLabels)
	}
	wantAnnotations := map[string]string{
		v1beta1.AnnotationDisplayName: "Reviewer", v1beta1.AnnotationAgentTemplate: "default-agent-claude",
	}
	if !reflect.DeepEqual(cfg.Metadata.Annotations, wantAnnotations) {
		t.Fatalf("annotations = %v, want %v", cfg.Metadata.Annotations, wantAnnotations)
	}
	// The spec is the template's, verbatim: the same state volume.
	if !reflect.DeepEqual(cfg.Spec, template("claude").Spec) {
		t.Fatalf("spec = %+v, want the template's", cfg.Spec)
	}
	if len(f.cells) != 1 || f.cells[0].Config.Name != cfg.Metadata.Name || f.cells[0].Name != "" || f.cells[0].Start {
		t.Fatalf("cell requests = %+v, want one stopped create with an automatic name", f.cells)
	}
	if len(f.started) != 1 || res.Cell.Status.State != v1beta1.CellStateReady {
		t.Fatalf("started = %+v cell = %+v", f.started, res.Cell)
	}
	// The template is untouched.
	if !reflect.DeepEqual(f.configs["default-agent-claude"], template("claude")) {
		t.Fatal("the template was modified")
	}
}

func TestCreateWithoutNameAndTwoAgentsShareTheStateVolume(t *testing.T) {
	f := newFake("codex")
	first, err := agent.Create(t.Context(), f, agent.Request{Harness: "codex"}, agent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := agent.Create(t.Context(), f, agent.Request{Harness: "codex"}, agent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, set := first.Config.Metadata.Annotations[v1beta1.AnnotationDisplayName]; set {
		t.Fatal("a display name was stored without one")
	}
	if first.Config.Metadata.Name == second.Config.Metadata.Name {
		t.Fatal("two agents got the same name")
	}
	if a, b := first.Config.Spec.Values["STATE_VOLUME"], second.Config.Spec.Values["STATE_VOLUME"]; a != b ||
		a != "default-agent-codex-state" {
		t.Fatalf("state volumes %q and %q, want the template's", a, b)
	}
}

func TestCreateRetriesATakenName(t *testing.T) {
	f := newFake("claude")
	f.configs["default-agent-claude-aaaaaa"] = v1beta1.CellConfigDoc{}
	names := []string{"default-agent-claude-aaaaaa", "default-agent-claude-bbbbbb"}
	calls := 0
	opts := agent.Options{Name: func(template string) (string, error) {
		if template != "default-agent-claude" {
			t.Fatalf("template = %q", template)
		}
		calls++
		return names[calls-1], nil
	}}
	res, err := agent.Create(t.Context(), f, agent.Request{Harness: "claude"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Metadata.Name != "default-agent-claude-bbbbbb" || calls != 2 {
		t.Fatalf("name = %q after %d attempts", res.Config.Metadata.Name, calls)
	}

	always := agent.Options{Name: func(string) (string, error) { return "default-agent-claude-aaaaaa", nil }}
	if _, err = agent.Create(t.Context(), f, agent.Request{Harness: "claude"}, always); !errors.Is(
		err,
		errdefs.ErrConfigExists,
	) {
		t.Fatalf("exhausted retries: %v", err)
	}
	bad := agent.Options{Name: func(string) (string, error) { return "bad_name", nil }}
	if _, err = agent.Create(t.Context(), f, agent.Request{Harness: "claude"}, bad); !errors.Is(
		err,
		errdefs.ErrConfig,
	) {
		t.Fatalf("invalid generated name: %v", err)
	}
}

func TestCreateRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		fake *fakeClient
		req  agent.Request
		want error
	}{
		"unknown harness":  {newFake("claude"), agent.Request{Harness: "opencode"}, errdefs.ErrConfig},
		"missing template": {newFake("claude"), agent.Request{Harness: "codex"}, errdefs.ErrAgentTemplateNotFound},
		"bad display name": {
			newFake("claude"), agent.Request{Harness: "claude", DisplayName: "a\nb"}, errdefs.ErrInvalidDisplayName,
		},
		"stack without space": {
			newFake("claude"), agent.Request{Harness: "claude", Realm: "default", Stack: "s"}, errdefs.ErrConfig,
		},
		"unsafe scope": {newFake("claude"), agent.Request{Harness: "claude", Realm: "../x"}, errdefs.ErrConfig},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := agent.Create(t.Context(), tc.fake, tc.req, agent.Options{}); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(tc.fake.created) != 0 {
				t.Fatalf("stored %d configs", len(tc.fake.created))
			}
		})
	}
	// A Config of the template's name that is not a template is refused.
	f := newFake("claude")
	plain := template("claude")
	delete(plain.Metadata.Labels, v1beta1.LabelTemplate)
	f.configs["default-agent-claude"] = plain
	if _, err := agent.Create(t.Context(), f, agent.Request{Harness: "claude"}, agent.Options{}); !errors.Is(
		err, errdefs.ErrAgentTemplateNotFound) {
		t.Fatalf("a non-template Config: %v", err)
	}
}

func TestCreateAtAnotherScope(t *testing.T) {
	f := newFake("claude")
	res, err := agent.Create(t.Context(), f, agent.Request{Harness: "claude", Realm: "work", Space: "team"},
		agent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	md := res.Config.Metadata
	if md.Realm != "work" || md.Space != "team" || md.Stack != "" ||
		res.Config.Spec.Values["STATE_VOLUME_REALM"] != "default" {
		t.Fatalf("metadata = %+v values = %v", md, res.Config.Spec.Values)
	}
}

func TestCreatePartialFailureKeepsTheConfig(t *testing.T) {
	cause := errors.New("disk full")
	for _, step := range []agent.Step{agent.StepCreateCell, agent.StepStartCell} {
		t.Run(string(step), func(t *testing.T) {
			f := newFake("claude")
			if step == agent.StepCreateCell {
				f.createErr = cause
			} else {
				f.startErr = cause
			}
			res, err := agent.Create(t.Context(), f, agent.Request{Harness: "claude"}, agent.Options{})
			var stepErr *agent.StepError
			if !errors.As(err, &stepErr) || stepErr.Step != step || !errors.Is(err, cause) {
				t.Fatalf("err = %v, want a %s StepError", err, step)
			}
			name := res.Config.Metadata.Name
			if _, kept := f.configs[name]; !kept || stepErr.Config.Name != name ||
				!strings.Contains(err.Error(), "config "+name+" stored") {
				t.Fatalf("config %q not kept or not named: %v", name, err)
			}
			if step == agent.StepStartCell && (stepErr.Cell == nil || stepErr.Cell.Metadata.Name != name+"-4d5e6f") {
				t.Fatalf("start failure does not name the cell: %+v", stepErr.Cell)
			}
		})
	}
}

func TestTemplates(t *testing.T) {
	f := newFake("codex")
	got, err := agent.Templates(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Harness != "claude" || got[0].Exists || got[1].Harness != "codex" || !got[1].Exists ||
		got[1].Config.Name != "default-agent-codex" {
		t.Fatalf("templates = %+v", got)
	}
}
