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
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type scopedClient struct {
	kukeonv1.FakeClient

	scope                  []string
	missing, empty, closed bool
	err                    error
	cell                   v1beta1.CellDoc
	container              v1beta1.ContainerSpec
}

func (c *scopedClient) Close() error { c.closed = true; return nil }
func (c *scopedClient) ListSpaces(_ context.Context, realm string) ([]v1beta1.SpaceDoc, error) {
	c.scope = []string{realm}
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.SpaceDoc{{}}, c.err
}

func (c *scopedClient) GetSpace(_ context.Context, doc v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
	c.scope = []string{doc.Spec.RealmID, doc.Metadata.Name}
	return kukeonv1.GetSpaceResult{Space: doc, MetadataExists: !c.missing}, c.err
}

func (c *scopedClient) ListStacks(_ context.Context, realm, space string) ([]v1beta1.StackDoc, error) {
	c.scope = []string{realm, space}
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.StackDoc{{}}, c.err
}

func (c *scopedClient) GetStack(_ context.Context, doc v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
	c.scope = []string{doc.Spec.RealmID, doc.Spec.SpaceID, doc.Metadata.Name}
	return kukeonv1.GetStackResult{Stack: doc, MetadataExists: !c.missing}, c.err
}

func (c *scopedClient) ListCells(_ context.Context, realm, space, stack string) ([]v1beta1.CellDoc, error) {
	c.scope = []string{realm, space, stack}
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.CellDoc{c.cell}, c.err
}

func (c *scopedClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	c.scope = []string{doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID, doc.Metadata.Name}
	c.cell.Metadata = doc.Metadata
	return kukeonv1.GetCellResult{Cell: c.cell, MetadataExists: !c.missing}, c.err
}

func (c *scopedClient) ListContainers(
	_ context.Context,
	realm, space, stack, cell string,
) ([]v1beta1.ContainerSpec, error) {
	c.scope = []string{realm, space, stack, cell}
	if c.empty {
		return nil, c.err
	}
	return []v1beta1.ContainerSpec{c.container}, c.err
}

func (c *scopedClient) GetContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
	c.scope = []string{doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID, doc.Spec.CellID, doc.Metadata.Name}
	doc.Spec.Env = c.container.Env
	return kukeonv1.GetContainerResult{
		Container:          doc,
		CellMetadataExists: !c.missing,
		ContainerExists:    !c.missing,
	}, c.err
}

func TestScopedResourceAPI(t *testing.T) {
	paths := []string{
		"/api/realms/r/spaces",
		"/api/realms/r/spaces/s/stacks",
		"/api/realms/r/spaces/s/stacks/t/cells",
		"/api/realms/r/spaces/s/stacks/t/cells/c/containers",
	}
	for depth, path := range paths {
		for _, mode := range []string{"list", "detail", "empty", "missing", "failed"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				c := &scopedClient{empty: mode == "empty", missing: mode == "missing"}
				if mode == "failed" {
					c.err = io.EOF
				}
				url := path
				detail := mode == "detail" || mode == "missing"
				if detail {
					url += "/target"
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
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+url, nil))
				expected := 200
				if mode == "missing" {
					expected = 404
				}
				if mode == "failed" {
					expected = 503
				}
				if w.Code != expected || !c.closed {
					t.Fatalf("status=%d closed=%v body=%s", w.Code, c.closed, w.Body)
				}
				scope := []string{"r", "s", "t", "c"}[:depth+1]
				if detail {
					scope = append(scope, "target")
				}
				if !reflect.DeepEqual(scope, c.scope) {
					t.Fatalf("scope=%v want %v", c.scope, scope)
				}
				if mode == "empty" && w.Body.String() != "[]\n" {
					t.Fatal(w.Body)
				}
				if mode == "detail" {
					var doc map[string]any
					if decodeErr := json.Unmarshal(w.Body.Bytes(), &doc); decodeErr != nil {
						t.Fatal(decodeErr)
					}
					for _, field := range []string{"metadata", "spec", "status", "apiVersion", "kind"} {
						if _, ok := doc[field]; !ok {
							t.Errorf("missing %s", field)
						}
					}
				}
			})
		}
	}
}

func TestScopedResponsesRedactInputs(t *testing.T) {
	c := &scopedClient{
		container: v1beta1.ContainerSpec{Env: []string{"PASSWORD=container-secret"}},
		cell: v1beta1.CellDoc{Spec: v1beta1.CellSpec{
			Containers: []v1beta1.ContainerSpec{{Env: []string{"PASSWORD=cell-secret"}}},
			RuntimeEnv: []string{"PASSWORD=runtime-secret"},
			Provenance: &v1beta1.CellProvenance{
				Params:       map[string]string{"password": "param-secret"},
				EnvOverrides: []string{"PASSWORD=override-secret"},
			},
		}},
	}
	handler, err := newHandler(
		"localhost:5129",
		"unix:///test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/realms/r/spaces/s/stacks/t/cells", "/api/realms/r/spaces/s/stacks/t/cells/c", "/api/realms/r/spaces/s/stacks/t/cells/c/containers", "/api/realms/r/spaces/s/stacks/t/cells/c/containers/n"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+path, nil))
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("%s: status=%d body=%s", path, w.Code, w.Body)
		}
	}
	if c.cell.Spec.Containers[0].Env[0] != "PASSWORD=cell-secret" ||
		c.container.Env[0] != "PASSWORD=container-secret" ||
		c.cell.Spec.Provenance.Params["password"] != "param-secret" {
		t.Fatal("redaction mutated client data")
	}
}

func TestContainerNotFoundWireErrors(t *testing.T) {
	for _, sentinel := range []error{errdefs.ErrCellNotFound, errdefs.ErrContainerNotFound} {
		wire := kukeonv1.ToAPIError(fmt.Errorf("lookup: %w", sentinel))
		c := &scopedClient{err: kukeonv1.FromAPIError(wire)}
		handler, err := newHandler(
			"localhost:5129",
			"unix:///test",
			func(context.Context) (kukeonv1.Client, error) { return c, nil },
		)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(
			w,
			httptest.NewRequest(
				http.MethodGet,
				"http://localhost:5129/api/realms/r/spaces/s/stacks/t/cells/c/containers/n",
				nil,
			),
		)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%v: status=%d body=%s", sentinel, w.Code, w.Body)
		}
	}
}
