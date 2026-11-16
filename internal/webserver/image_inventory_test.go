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
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

type imageClient struct {
	kukeonv1.FakeClient

	realm  string
	ref    string
	images []kukeonv1.ImageInfo
	err    error
	closed bool
}

func (c *imageClient) Close() error { c.closed = true; return nil }

func (c *imageClient) ListImages(_ context.Context, realm string) (kukeonv1.ListImagesResult, error) {
	c.realm = realm
	if c.err != nil {
		return kukeonv1.ListImagesResult{}, c.err
	}
	return kukeonv1.ListImagesResult{Realm: realm, Namespace: realm + ".kukeon.io", Images: c.images}, nil
}

func (c *imageClient) GetImage(_ context.Context, realm, ref string) (kukeonv1.GetImageResult, error) {
	c.realm, c.ref = realm, ref
	if c.err != nil {
		return kukeonv1.GetImageResult{}, c.err
	}
	for _, img := range c.images {
		if img.Name == ref {
			return kukeonv1.GetImageResult{Realm: realm, Namespace: realm + ".kukeon.io", Image: img}, nil
		}
	}
	return kukeonv1.GetImageResult{}, fmt.Errorf("%w: %s", errdefs.ErrImageNotFound, ref)
}

func serveImages(t *testing.T, c *imageClient, url string) *httptest.ResponseRecorder {
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
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+url, nil))
	return w
}

func sampleImages() []kukeonv1.ImageInfo {
	return []kukeonv1.ImageInfo{
		{
			Name:      "docker.io/library/alpine:3.19",
			Size:      3_400_000,
			CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Digest:    "sha256:abc",
			MediaType: "application/vnd.oci.image.index.v1+json",
			Labels:    map[string]string{"io.kukeon/built": "true"},
		},
		{Name: "kukeon.internal/unsized:dev", Size: -1, Digest: "sha256:def"},
	}
}

func TestImageInventoryListScopesRealmAndNormalizesUnknowns(t *testing.T) {
	c := &imageClient{images: sampleImages()}
	w := serveImages(t, c, "/api/realms/dev/image-inventory")
	if w.Code != http.StatusOK || !c.closed || c.realm != "dev" {
		t.Fatalf("status=%d closed=%v realm=%q body=%s", w.Code, c.closed, c.realm, w.Body)
	}
	var body struct {
		Realm     string           `json:"realm"`
		Namespace string           `json:"namespace"`
		Images    []map[string]any `json:"images"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Realm != "dev" || body.Namespace != "dev.kukeon.io" || len(body.Images) != 2 {
		t.Fatalf("body=%s", w.Body)
	}
	sized, unsized := body.Images[0], body.Images[1]
	if sized["size"] != float64(3_400_000) || sized["createdAt"] != "2026-01-02T03:04:05Z" ||
		sized["mediaType"] != "application/vnd.oci.image.index.v1+json" {
		t.Fatalf("sized=%v", sized)
	}
	// An unresolvable size is null, never zero; a zero time and nil labels
	// are normalized so the browser need not special-case them.
	if v, ok := unsized["size"]; !ok || v != nil {
		t.Fatalf("unsized size=%v present=%v", v, ok)
	}
	if v, ok := unsized["createdAt"]; !ok || v != nil {
		t.Fatalf("unsized createdAt=%v", v)
	}
	if labels, ok := unsized["labels"].(map[string]any); !ok || len(labels) != 0 {
		t.Fatalf("unsized labels=%v", unsized["labels"])
	}
}

func TestImageInventoryListEmptyIsArray(t *testing.T) {
	w := serveImages(t, &imageClient{}, "/api/realms/dev/image-inventory")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if images, ok := body["images"].([]any); !ok || len(images) != 0 {
		t.Fatalf("images=%v", body["images"])
	}
}

func TestImageInventoryDetailTakesSlashedReference(t *testing.T) {
	for _, url := range []string{
		"/api/realms/dev/image-inventory/docker.io/library/alpine:3.19",
		"/api/realms/dev/image-inventory/docker.io%2Flibrary%2Falpine%3A3.19",
	} {
		t.Run(url, func(t *testing.T) {
			c := &imageClient{images: sampleImages()}
			w := serveImages(t, c, url)
			if w.Code != http.StatusOK || c.realm != "dev" || c.ref != "docker.io/library/alpine:3.19" {
				t.Fatalf("status=%d realm=%q ref=%q body=%s", w.Code, c.realm, c.ref, w.Body)
			}
			var body struct {
				Image map[string]any `json:"image"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Image["digest"] != "sha256:abc" ||
				body.Image["labels"].(map[string]any)["io.kukeon/built"] != "true" {
				t.Fatalf("body=%s", w.Body)
			}
		})
	}
}

func TestImageInventoryErrors(t *testing.T) {
	cases := []struct {
		name string
		url  string
		err  error
		code int
		api  string
	}{
		{"image not found", "/api/realms/dev/image-inventory/missing:1", nil, http.StatusNotFound, "not_found"},
		{
			"realm not found", "/api/realms/gone/image-inventory",
			fmt.Errorf("%w: gone", errdefs.ErrRealmNotFound), http.StatusNotFound, "not_found",
		},
		{"daemon failure", "/api/realms/dev/image-inventory", io.EOF, http.StatusServiceUnavailable, "unavailable"},
		{
			"permission", "/api/realms/dev/image-inventory",
			errors.Join(errdefs.ErrListImages, fs.ErrPermission), http.StatusForbidden, "permission_denied",
		},
		{
			"outdated daemon", "/api/realms/dev/image-inventory",
			fmt.Errorf("%w: KukeonV1.ListImages", errdefs.ErrDaemonMethodUnsupported),
			http.StatusNotImplemented, "daemon_outdated",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveImages(t, &imageClient{err: tc.err}, tc.url)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != tc.code || body.Error.Code != tc.api {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestImageInventoryRejectsUnsafeRealm(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, realm := range []string{"", ".."} {
		r.SetPathValue("realm", realm)
		if _, err := inventoryRealm(r); !errors.Is(err, errdefs.ErrConfig) {
			t.Fatalf("realm %q: got %v", realm, err)
		}
	}
}

// TestImageInventoryReportsCapabilities locks the field the create form
// filters its image picker on (#1472): an image's io.kukeon.capabilities, and
// an empty array — never null — for an image that declares none.
func TestImageInventoryReportsCapabilities(t *testing.T) {
	c := &imageClient{images: []kukeonv1.ImageInfo{
		{Name: "ghcr.io/eminwux/kukeon/claude:v1", Capabilities: []string{"claude", "git", "gh"}},
		{Name: "docker.io/library/alpine:3.19"},
	}}
	w := serveImages(t, c, "/api/realms/dev/image-inventory")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var body struct {
		Images []map[string]any `json:"images"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	labeled, ok := body.Images[0]["capabilities"].([]any)
	if !ok || len(labeled) != 3 || labeled[0] != "claude" || labeled[2] != "gh" {
		t.Fatalf("labeled capabilities=%v", body.Images[0]["capabilities"])
	}
	unlabeled, ok := body.Images[1]["capabilities"].([]any)
	if !ok || len(unlabeled) != 0 {
		t.Fatalf("unlabeled capabilities=%v, want []", body.Images[1]["capabilities"])
	}

	w = serveImages(t, c, "/api/realms/dev/image-inventory/ghcr.io/eminwux/kukeon/claude:v1")
	var detail struct {
		Image map[string]any `json:"image"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != http.StatusOK {
		t.Fatalf("detail status=%d err=%v body=%s", w.Code, err, w.Body)
	}
	if got, isList := detail.Image["capabilities"].([]any); !isList || len(got) != 3 {
		t.Fatalf("detail capabilities=%v", detail.Image["capabilities"])
	}
}
