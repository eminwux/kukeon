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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type cellActionClient struct {
	kukeonv1.FakeClient

	config    v1beta1.CellConfigDoc
	blueprint v1beta1.CellBlueprintDoc
	created   v1beta1.CellDoc
	closed    bool
}

func (c *cellActionClient) Close() error { c.closed = true; return nil }
func (c *cellActionClient) ListConfigs(context.Context, string, string, string) ([]v1beta1.CellConfigDoc, error) {
	return []v1beta1.CellConfigDoc{c.config}, nil
}

func (c *cellActionClient) GetConfig(
	_ context.Context,
	lookup v1beta1.CellConfigDoc,
) (kukeonv1.GetConfigResult, error) {
	if lookup.Metadata.Name != c.config.Metadata.Name || lookup.Metadata.Realm != c.config.Metadata.Realm {
		return kukeonv1.GetConfigResult{}, errdefs.ErrConfigNotFound
	}
	return kukeonv1.GetConfigResult{Config: c.config, MetadataExists: true}, nil
}

func (c *cellActionClient) GetBlueprint(
	context.Context,
	v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	return kukeonv1.GetBlueprintResult{Blueprint: c.blueprint, MetadataExists: true}, nil
}

func (c *cellActionClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	if c.created.Metadata.Name != "" && c.created.Metadata.Name == doc.Metadata.Name {
		return kukeonv1.GetCellResult{Cell: c.created, MetadataExists: true}, nil
	}
	return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
}

func (c *cellActionClient) MaterializeCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	c.created = doc
	return kukeonv1.CreateCellResult{Cell: doc, Created: true}, nil
}

func TestConfigListOmitsValuesAndFills(t *testing.T) {
	client := &cellActionClient{config: v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{Name: "dev", Realm: "r"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "bp", Realm: "r"},
			Values:    map[string]string{"TOKEN": "private-value"},
			Repos:     map[string]v1beta1.CellConfigRepoFill{"source": {URL: "https://private.example/repo"}},
			Secrets:   map[string]v1beta1.CellConfigSecretFill{"auth": {}},
		},
	}}
	handler := configListHandler(func(context.Context) (kukeonv1.Client, error) { return client, nil })
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/configs?realm=r", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, sensitive := range []string{"private-value", "private.example", "secretRef"} {
		if strings.Contains(body, sensitive) {
			t.Errorf("config response leaked %q: %s", sensitive, body)
		}
	}
	for _, expected := range []string{"\"TOKEN\"", "\"source\"", "\"auth\"", "\"bp\""} {
		if !strings.Contains(body, expected) {
			t.Errorf("config summary missing %q: %s", expected, body)
		}
	}
	if !client.closed {
		t.Error("client was not closed")
	}
}

func TestCreateCellFromConfigMaterializesStoppedCell(t *testing.T) {
	config := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{Name: "web", Realm: "r", Space: "source"},
		Spec:     v1beta1.CellConfigSpec{Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web-template", Realm: "r"}},
	}
	blueprint := v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{Name: "web-template", Realm: "r"},
		Spec: v1beta1.CellBlueprintSpec{
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "app", Image: "example.invalid/app:latest", Attachable: true},
				},
			},
		},
	}
	client := &cellActionClient{config: config, blueprint: blueprint}
	handler := createCellFromConfigHandler(func(context.Context) (kukeonv1.Client, error) { return client, nil })
	request := `{"config":{"name":"web","realm":"r","space":"source"},"scope":{"realm":"r","space":"target","stack":"apps"},"start":false}`
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/cells/from-config", strings.NewReader(request)))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Cell    v1beta1.CellDoc `json:"cell"`
		Started bool            `json:"started"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Cell.Metadata.Name == "" || response.Cell.Metadata.Name == "web" {
		t.Fatalf("cell name = %q, want generated config-prefixed name", response.Cell.Metadata.Name)
	}
	if response.Cell.Spec.ID != response.Cell.Metadata.Name {
		t.Errorf("cell spec ID = %q; metadata name = %q", response.Cell.Spec.ID, response.Cell.Metadata.Name)
	}
	if response.Cell.Spec.RealmID != "r" || response.Cell.Spec.SpaceID != "target" ||
		response.Cell.Spec.StackID != "apps" {
		t.Errorf("cell scope = %+v", response.Cell.Spec)
	}
	if response.Started {
		t.Error("stopped-cell request reported started")
	}
	if response.Cell.Metadata.Labels["kukeon.io/config"] != "web" {
		t.Errorf("config lineage label = %q", response.Cell.Metadata.Labels["kukeon.io/config"])
	}
	if len(client.created.Spec.Containers) != 1 || !client.created.Spec.Containers[0].Attachable {
		t.Errorf("materialized containers = %+v", client.created.Spec.Containers)
	}
}

func TestTerminalRequiresExactBrowserOrigin(t *testing.T) {
	handler := terminalHandler(func(context.Context) (kukeonv1.Client, error) {
		t.Fatal("origin rejection must happen before daemon access")
		return nil, errors.New("unexpected dial")
	}, "localhost:5129")
	for _, origin := range []string{"", "http://evil.example", "https://localhost:5129"} {
		r := httptest.NewRequest(http.MethodGet, "http://localhost:5129/api/terminal", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("Origin %q: status = %d, want %d", origin, w.Code, http.StatusForbidden)
		}
	}
}
