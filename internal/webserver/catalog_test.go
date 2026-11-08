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
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type catalogClient struct {
	kukeonv1.FakeClient

	scope                  []string
	name                   string
	missing, empty, closed bool
	err                    error
}

func (c *catalogClient) Close() error { c.closed = true; return nil }

func (c *catalogClient) ListBlueprints(
	_ context.Context,
	realm, space, stack string,
) ([]v1beta1.CellBlueprintDoc, error) {
	c.scope = []string{realm, space, stack}
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.CellBlueprintDoc{{Metadata: v1beta1.CellBlueprintMetadata{Name: "item", Realm: "r"}}}, c.err
}

func (c *catalogClient) GetBlueprint(
	_ context.Context,
	doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	c.scope = []string{doc.Metadata.Realm, doc.Metadata.Space, doc.Metadata.Stack}
	c.name = doc.Metadata.Name
	doc.Spec.Prefix = "keep-full-spec"
	return kukeonv1.GetBlueprintResult{Blueprint: doc, MetadataExists: !c.missing}, c.err
}

func (c *catalogClient) ListConfigs(_ context.Context, realm, space, stack string) ([]v1beta1.CellConfigDoc, error) {
	c.scope = []string{realm, space, stack}
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.CellConfigDoc{{Metadata: v1beta1.CellConfigMetadata{Name: "item", Realm: "r"}}}, c.err
}

func (c *catalogClient) GetConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	c.scope = []string{doc.Metadata.Realm, doc.Metadata.Space, doc.Metadata.Stack}
	c.name = doc.Metadata.Name
	doc.Spec.Blueprint = v1beta1.CellConfigBlueprintRef{Name: "template", Realm: "cross-realm"}
	return kukeonv1.GetConfigResult{Config: doc, MetadataExists: !c.missing}, c.err
}

func (c *catalogClient) ListVolumes(_ context.Context, realm, space, stack string) ([]v1beta1.VolumeDoc, error) {
	c.scope = []string{realm, space, stack}
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.VolumeDoc{{Metadata: v1beta1.VolumeMetadata{Name: "item", Realm: "r"}}}, c.err
}

func (c *catalogClient) GetVolume(_ context.Context, doc v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error) {
	c.scope = []string{doc.Metadata.Realm, doc.Metadata.Space, doc.Metadata.Stack}
	c.name = doc.Metadata.Name
	doc.Spec.ReclaimPolicy = v1beta1.ReclaimRetain
	return kukeonv1.GetVolumeResult{Volume: doc, MetadataExists: !c.missing}, c.err
}

func (c *catalogClient) ListSecrets(_ context.Context, realm, space, stack, cell string) ([]v1beta1.SecretDoc, error) {
	c.scope = []string{realm, space, stack, cell}
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.SecretDoc{
		{Metadata: v1beta1.SecretMetadata{Name: "item", Realm: "r"}, Spec: v1beta1.SecretSpec{Data: "must-never-leak"}},
	}, c.err
}

func (c *catalogClient) GetSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	c.scope = []string{doc.Metadata.Realm, doc.Metadata.Space, doc.Metadata.Stack, doc.Metadata.Cell}
	c.name = doc.Metadata.Name
	doc.Spec.Data = "must-never-leak"
	return kukeonv1.GetSecretResult{Secret: doc, MetadataExists: !c.missing}, c.err
}

func TestCatalogAPI(t *testing.T) {
	for _, kind := range []string{"blueprints", "configs", "volumes", "secrets"} {
		for _, mode := range []string{"global-list", "realm-list", "stack-list", "detail", "empty", "missing", "unavailable", "permission", "gap", "missing-realm", "cell", "cell-detail"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				c := &catalogClient{empty: mode == "empty", missing: mode == "missing"}
				if mode == "unavailable" {
					c.err = io.EOF
				}
				if mode == "permission" {
					c.err = fs.ErrPermission
				}
				path := "/api/catalog/" + kind
				detail := mode == "detail" || mode == "missing" || mode == "missing-realm" || mode == "cell-detail"
				if detail {
					path += "/item"
				}
				query := ""
				switch mode {
				case "detail", "missing", "stack-list":
					query = "?realm=r&space=s&stack=t"
				case "realm-list":
					query = "?realm=r"
				case "gap":
					query = "?realm=r&stack=t"
				case "cell", "cell-detail":
					query = "?realm=r&space=s&stack=t&cell=c"
				}
				handler, err := newHandler(
					"localhost:5129",
					"unix:///test",
					func(context.Context) (kukeonv1.Client, error) { return c, nil },
				)
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+path+query, nil))
				expected := catalogExpectedStatus(kind, mode)
				if w.Code != expected || !c.closed {
					t.Fatalf("status=%d closed=%v body=%s", w.Code, c.closed, w.Body)
				}
				if strings.Contains(w.Body.String(), "must-never-leak") {
					t.Fatal("secret material leaked")
				}
				if expected != 200 {
					return
				}
				scope := []string{"", "", ""}
				if kind == "secrets" {
					scope = append(scope, "")
				}
				if query != "" {
					scope[0] = "r"
				}
				if mode == "detail" || mode == "stack-list" {
					scope[1] = "s"
					scope[2] = "t"
				}
				if mode == "cell" || mode == "cell-detail" {
					scope = []string{"r", "s", "t", "c"}
				}
				if !reflect.DeepEqual(scope, c.scope) {
					t.Fatalf("scope=%v expected=%v", c.scope, scope)
				}
				if mode == "empty" && w.Body.String() != "[]\n" {
					t.Fatal(w.Body)
				}
				if detail {
					checkCatalogDetail(t, kind, c, w)
				}
			})
		}
	}
}

func checkCatalogDetail(t *testing.T, kind string, c *catalogClient, w *httptest.ResponseRecorder) {
	t.Helper()
	if c.name != "item" {
		t.Fatal(c.name)
	}
	var doc map[string]any
	if decodeErr := json.Unmarshal(w.Body.Bytes(), &doc); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	spec := doc["spec"].(map[string]any)
	switch kind {
	case "blueprints":
		if spec["prefix"] != "keep-full-spec" {
			t.Fatal(spec)
		}
	case "configs":
		if spec["blueprint"].(map[string]any)["realm"] != "cross-realm" {
			t.Fatal(spec)
		}
	case "volumes":
		if spec["reclaimPolicy"] != "Retain" {
			t.Fatal(spec)
		}
	case "secrets":
		if _, ok := spec["data"]; ok {
			t.Fatal("data key must be absent")
		}
	}
}

func catalogExpectedStatus(kind, mode string) int {
	expected := 200
	switch mode {
	case "gap", "missing-realm":
		expected = 400
	case "missing":
		expected = 404
	case "unavailable":
		expected = 503
	case "permission":
		expected = 403
	case "cell", "cell-detail":
		if kind != "secrets" {
			expected = 400
		}
	}
	return expected
}
