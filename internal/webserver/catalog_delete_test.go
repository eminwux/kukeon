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
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type catalogDeleteClient struct {
	catalogClient

	deleteCalls int
	deleteScope []string
	deleteName  string
	deleteErr   error
}

func (c *catalogDeleteClient) recordDelete(name string, scope ...string) error {
	c.deleteCalls++
	c.deleteName = name
	c.deleteScope = scope
	return c.deleteErr
}

func (c *catalogDeleteClient) DeleteBlueprint(
	_ context.Context,
	doc v1beta1.CellBlueprintDoc,
) (kukeonv1.DeleteBlueprintResult, error) {
	md := doc.Metadata
	err := c.recordDelete(md.Name, md.Realm, md.Space, md.Stack)
	return kukeonv1.DeleteBlueprintResult{Blueprint: doc, Deleted: err == nil}, err
}

func (c *catalogDeleteClient) DeleteConfig(
	_ context.Context,
	doc v1beta1.CellConfigDoc,
) (kukeonv1.DeleteConfigResult, error) {
	md := doc.Metadata
	err := c.recordDelete(md.Name, md.Realm, md.Space, md.Stack)
	return kukeonv1.DeleteConfigResult{Config: doc, Deleted: err == nil, BackRefCells: []string{"r/s/t/cell-a"}}, err
}

func (c *catalogDeleteClient) DeleteVolume(
	_ context.Context,
	doc v1beta1.VolumeDoc,
) (kukeonv1.DeleteVolumeResult, error) {
	md := doc.Metadata
	err := c.recordDelete(md.Name, md.Realm, md.Space, md.Stack)
	return kukeonv1.DeleteVolumeResult{Volume: doc, Deleted: err == nil}, err
}

func (c *catalogDeleteClient) DeleteSecret(
	_ context.Context,
	doc v1beta1.SecretDoc,
) (kukeonv1.DeleteSecretResult, error) {
	md := doc.Metadata
	err := c.recordDelete(md.Name, md.Realm, md.Space, md.Stack, md.Cell)
	doc.Spec.Data = "must-never-leak"
	return kukeonv1.DeleteSecretResult{Secret: doc, Deleted: err == nil}, err
}

func catalogDeleteRequest(kind, query, origin, contentType, body string) *http.Request {
	req := httptest.NewRequest(
		http.MethodPost,
		"http://localhost:5129/api/catalog/"+kind+"/item/delete"+query,
		strings.NewReader(body),
	)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func serveCatalogDelete(t *testing.T, c *catalogDeleteClient, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := newHandler(
		"http://localhost:5129",
		"unix:///test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestCatalogDeleteRoutes(t *testing.T) {
	refusals := map[string]error{
		catalogBlueprints: fmt.Errorf("%w: daemon refused blueprint delete", errdefs.ErrDeleteBlueprint),
		catalogConfigs:    fmt.Errorf("%w: daemon refused config delete", errdefs.ErrDeleteConfig),
		catalogVolumes:    fmt.Errorf("%w: mounted by cell \"r/s/t/web\"", errdefs.ErrVolumeInUse),
		catalogSecrets: fmt.Errorf(
			"%w: secret \"item\" is referenced by 1 cell(s): r/s/t/web",
			errdefs.ErrSecretInUse,
		),
	}
	for _, kind := range []string{catalogBlueprints, catalogConfigs, catalogVolumes, catalogSecrets} {
		t.Run(kind+"/success", func(t *testing.T) {
			c := &catalogDeleteClient{}
			query := "?realm=r&space=s&stack=t"
			wantScope := []string{"r", "s", "t"}
			if kind == catalogSecrets {
				query += "&cell=c"
				wantScope = append(wantScope, "c")
			}
			w := serveCatalogDelete(t, c, catalogDeleteRequest(kind, query, "", jsonContentType, "{}"))
			if w.Code != http.StatusOK || c.deleteCalls != 1 || !c.closed {
				t.Fatalf("status=%d calls=%d closed=%v body=%s", w.Code, c.deleteCalls, c.closed, w.Body)
			}
			if c.deleteName != "item" || !reflect.DeepEqual(c.deleteScope, wantScope) {
				t.Fatalf("name=%q scope=%v", c.deleteName, c.deleteScope)
			}
			if strings.Contains(w.Body.String(), "must-never-leak") {
				t.Fatal("secret material leaked")
			}
			var result catalogDeleteResult
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.Deleted || result.Kind != kind || result.Name != "item" {
				t.Fatalf("result=%+v", result)
			}
			if kind == catalogConfigs && !reflect.DeepEqual(result.BackRefCells, []string{"r/s/t/cell-a"}) {
				t.Fatalf("back-refs=%v", result.BackRefCells)
			}
		})
		t.Run(kind+"/not-found", func(t *testing.T) {
			c := &catalogDeleteClient{catalogClient: catalogClient{missing: true}}
			w := serveCatalogDelete(t, c, catalogDeleteRequest(kind, "?realm=r", "", jsonContentType, "{}"))
			if w.Code != http.StatusNotFound || c.deleteCalls != 0 || !strings.Contains(w.Body.String(), "not_found") {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, c.deleteCalls, w.Body)
			}
		})
		t.Run(kind+"/daemon-refusal", func(t *testing.T) {
			refusal := refusals[kind]
			c := &catalogDeleteClient{deleteErr: refusal}
			w := serveCatalogDelete(t, c, catalogDeleteRequest(kind, "?realm=r", "", jsonContentType, "{}"))
			wantStatus := http.StatusServiceUnavailable
			if kind == catalogVolumes || kind == catalogSecrets {
				wantStatus = http.StatusConflict
			}
			if w.Code != wantStatus || c.deleteCalls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, c.deleteCalls, w.Body)
			}
			var body struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Message != refusal.Error() {
				t.Fatalf("message=%q want verbatim %q", body.Error.Message, refusal.Error())
			}
		})
		t.Run(kind+"/origin-rejected", func(t *testing.T) {
			c := &catalogDeleteClient{}
			w := serveCatalogDelete(
				t, c, catalogDeleteRequest(kind, "?realm=r", "http://evil.example", jsonContentType, "{}"),
			)
			if w.Code != http.StatusForbidden || c.deleteCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, c.deleteCalls, w.Body)
			}
		})
	}
}

func TestCatalogDeleteRejectsUnsafeRequests(t *testing.T) {
	const form = "application/x-www-form-urlencoded"
	j := jsonContentType
	cases := []struct {
		name, kind, query, contentType, body string
	}{
		{"form post", catalogConfigs, "?realm=r", form, "a=b"},
		{"missing content type", catalogConfigs, "?realm=r", "", ""},
		{"unknown field", catalogConfigs, "?realm=r", j, `{"force":true}`},
		{"two documents", catalogConfigs, "?realm=r", j, "{}{}"},
		{"missing realm", catalogBlueprints, "", j, "{}"},
		{"scope gap", catalogVolumes, "?realm=r&stack=t", j, "{}"},
		{"cell scope on config", catalogConfigs, "?realm=r&space=s&stack=t&cell=c", j, "{}"},
		{"unsafe realm", catalogSecrets, "?realm=..", j, "{}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &catalogDeleteClient{}
			w := serveCatalogDelete(t, c, catalogDeleteRequest(tc.kind, tc.query, "", tc.contentType, tc.body))
			if w.Code != http.StatusBadRequest || c.deleteCalls != 0 || c.scope != nil {
				t.Fatalf("status=%d calls=%d scope=%v body=%s", w.Code, c.deleteCalls, c.scope, w.Body)
			}
		})
	}
}

func TestCatalogDeleteAcceptsEmptyJSONBody(t *testing.T) {
	c := &catalogDeleteClient{}
	w := serveCatalogDelete(t, c, catalogDeleteRequest(catalogConfigs, "?realm=r", "", jsonContentType, ""))
	if w.Code != http.StatusOK || c.deleteCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, c.deleteCalls, w.Body)
	}
}

func TestCatalogDeleteRejectsUnsafeName(t *testing.T) {
	c := &catalogDeleteClient{}
	handler := catalogDeleteHandler(func(context.Context) (kukeonv1.Client, error) { return c, nil }, catalogConfigs)
	r := catalogDeleteRequest(catalogConfigs, "?realm=r", "", jsonContentType, "{}")
	r.SetPathValue("name", "../outside")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || c.deleteCalls != 0 || c.scope != nil {
		t.Fatalf("status=%d calls=%d scope=%v body=%s", w.Code, c.deleteCalls, c.scope, w.Body)
	}
}
