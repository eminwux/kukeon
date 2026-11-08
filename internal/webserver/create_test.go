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

	created  int
	existing bool
	doc      v1beta1.CellDoc
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
	return kukeonv1.CreateCellResult{}, nil
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
		{name: "empty name", input: createConfigRequest{Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r"}}},
		{name: "scope gap", input: createConfigRequest{Name: "cell", Config: v1beta1.CellConfigMetadata{Name: "cfg", Realm: "r", Stack: "s"}}},
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
		})
	}
}
