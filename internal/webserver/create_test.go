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

type creationClient struct {
	catalogClient

	created            int
	existing           bool
	materializeErr     error
	materializeCreated bool
	unsafeBlueprintRef bool
	getBlueprintCalls  int
	getConfigCalls     int
	doc                v1beta1.CellDoc
	materializedCell   v1beta1.CellDoc
}

func (c *creationClient) GetConfig(ctx context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	c.getConfigCalls++
	result, err := c.catalogClient.GetConfig(ctx, doc)
	if c.unsafeBlueprintRef {
		result.Config.Spec.Blueprint.Name = ".."
	}
	return result, err
}

func (c *creationClient) GetBlueprint(
	ctx context.Context,
	doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	c.getBlueprintCalls++
	return c.catalogClient.GetBlueprint(ctx, doc)
}

func (c *creationClient) GetStack(_ context.Context, doc v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
	return kukeonv1.GetStackResult{Stack: doc, MetadataExists: true}, nil
}

func (c *creationClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	return kukeonv1.GetCellResult{Cell: doc, MetadataExists: c.existing}, nil
}

func (c *creationClient) MaterializeCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	c.created++
	c.doc = doc
	if c.materializeErr != nil {
		return kukeonv1.CreateCellResult{}, c.materializeErr
	}
	materialized := c.materializedCell
	if materialized.Metadata.Name == "" {
		materialized = doc
	}
	return kukeonv1.CreateCellResult{Created: c.materializeCreated, Cell: materialized}, nil
}

func TestConfigCreation(t *testing.T) {
	c := &creationClient{}
	handler, err := newHandler(
		"localhost:5129",
		"test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	input := createConfigRequest{
		Config: v1beta1.CellConfigMetadata{Name: "config", Realm: "r"},
		Name:   "new-cell",
		Env:    []string{"APP=value"},
	}
	request := func(action, origin string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(input)
		r := httptest.NewRequest(
			http.MethodPost,
			"http://localhost:5129/api/cells/from-config/"+action,
			strings.NewReader(string(data)),
		)
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request("preview", "http://localhost:5129")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var preview struct {
		Preview  string          `json:"preview"`
		Document v1beta1.CellDoc `json:"document"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if c.created != 0 || preview.Document.Spec.Provenance == nil || preview.Document.Spec.RealmID != "r" {
		t.Fatal("preview must materialize lineage without mutation")
	}
	if w = request("create", "http://localhost:5129"); w.Code < 400 {
		t.Fatal("unreviewed create accepted")
	}
	input.Preview = preview.Preview
	if w = request("create", ""); w.Code < 400 {
		t.Fatal("missing origin accepted")
	}
	c.materializeCreated = true
	if w = request("create", "http://localhost:5129"); w.Code != 200 || c.created != 1 ||
		c.doc.Metadata.Name != "new-cell" {
		t.Fatalf("create: %s", w.Body.String())
	}
	input.Name = "changed"
	if w = request("create", "http://localhost:5129"); w.Code != 400 || c.created != 1 {
		t.Fatal("changed inputs accepted")
	}
	c.existing = true
	if w = request("preview", "http://localhost:5129"); w.Code < 400 {
		t.Fatal("existing cell accepted")
	}
}

func TestConfigCreationValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   createConfigRequest
		missing bool
	}{
		{name: "scope gap", input: createConfigRequest{Name: "cell", Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r", Stack: "s"}}},
		{name: "unsafe cell name", input: createConfigRequest{Name: "../cell", Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r"}}},
		{name: "unsafe config realm", input: createConfigRequest{Name: "cell", Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: ".."}}},
		{name: "unsafe config name", input: createConfigRequest{Name: "cell", Config: v1beta1.CellConfigMetadata{Name: "nested/name", Realm: "r"}}},
		{name: "unsafe config space", input: createConfigRequest{Name: "cell", Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r", Space: `..\nested`}}},
		{name: "invalid env", input: createConfigRequest{Name: "cell", Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r"}, Env: []string{"bad"}}},
		{name: "conflicting env", input: createConfigRequest{Name: "cell", Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r"}, Env: []string{"A=one", "A=two"}}},
		{name: "missing config", input: createConfigRequest{Name: "cell", Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r"}}, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &creationClient{}
			c.missing = tc.missing
			if _, err := configCell(context.Background(), c, tc.input); err == nil {
				t.Fatal("expected validation failure")
			}
			if c.created != 0 {
				t.Fatal("validation mutated daemon")
			}
			if strings.HasPrefix(tc.name, "unsafe ") && c.getConfigCalls != 0 {
				t.Fatalf("unsafe input reached daemon lookup: %d calls", c.getConfigCalls)
			}
		})
	}
}

func TestConfigCellAutomaticNameUsesConfigPrefix(t *testing.T) {
	c := &creationClient{}
	doc, err := configCell(context.Background(), c, createConfigRequest{
		Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Metadata.Name, "cfg-") || doc.Spec.ID != doc.Metadata.Name {
		t.Fatalf("automatic name = %q (id %q), want cfg-<suffix> and matching ID", doc.Metadata.Name, doc.Spec.ID)
	}
	if doc.Spec.Provenance == nil || doc.Spec.Provenance.BindingKind != v1beta1.BindingKindConfig {
		t.Fatalf("automatic-name materialization lost Config lineage: %+v", doc.Spec.Provenance)
	}

	previewName := doc.Metadata.Name
	created, err := configCell(context.Background(), c, createConfigRequest{
		Config:       v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r"},
		ResolvedName: previewName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Metadata.Name != previewName {
		t.Fatalf("create name = %q, want reviewed generated name %q", created.Metadata.Name, previewName)
	}
}

func TestConfigCreationAutomaticNamePreviewMatchesCreatedCell(t *testing.T) {
	client := &creationClient{materializeCreated: true}
	handler := testCreateHandler(t, client)
	input := createConfigRequest{Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "realm"}}
	previewResponse := postCreate(t, handler, "/api/cells/from-config/preview", input)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview struct {
		Preview  string          `json:"preview"`
		Document v1beta1.CellDoc `json:"document"`
	}
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(preview.Document.Metadata.Name, "cfg-") {
		t.Fatalf("automatic preview name = %q, want cfg- prefix", preview.Document.Metadata.Name)
	}

	input.ResolvedName = preview.Document.Metadata.Name
	input.Preview = preview.Preview
	createdResponse := postCreate(t, handler, "/api/cells/from-config/create", input)
	if createdResponse.Code != http.StatusOK || client.created != 1 {
		t.Fatalf("create: %d %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created v1beta1.CellDoc
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Metadata.Name != preview.Document.Metadata.Name ||
		client.doc.Metadata.Name != preview.Document.Metadata.Name {
		t.Fatalf(
			"created name %q and materialized name %q differ from preview %q",
			created.Metadata.Name,
			client.doc.Metadata.Name,
			preview.Document.Metadata.Name,
		)
	}
}

func TestSafePathSegment(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{value: "", want: true},
		{value: "resource-name", want: true},
		{value: "foo..bar", want: true},
		{value: "..", want: false},
		{value: "../outside", want: false},
		{value: "nested/name", want: false},
		{value: `nested\\name`, want: false},
		{value: "bad\x00name", want: false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := safePathSegment(tc.value); got != tc.want {
				t.Fatalf("safePathSegment(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}

	t.Run("unsafe persisted blueprint ref", func(t *testing.T) {
		c := &creationClient{unsafeBlueprintRef: true}
		_, err := configCell(context.Background(), c, createConfigRequest{
			Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r"},
			Name:   "cell",
		})
		if err == nil || c.getBlueprintCalls != 0 {
			t.Fatalf("unsafe blueprint reference accepted: err=%v calls=%d", err, c.getBlueprintCalls)
		}
	})
}
