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

package webui_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/webui"
)

// Exercise the compiled assets from a directory without frontend source files.
// This also works when the test executable is copied out of the checkout.
func TestEmbeddedAssets(t *testing.T) {
	t.Chdir(t.TempDir())
	assets, err := webui.Assets()
	if err != nil {
		t.Fatal(err)
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `id="root"`) {
		t.Fatal("missing React mount point")
	}
	refs := regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllSubmatch(index, -1)
	if len(refs) < 2 {
		t.Fatal("expected bundled JavaScript and stylesheet")
	}
	handler := http.FileServerFS(assets)
	for _, ref := range refs {
		path := string(ref[1])
		if !strings.HasPrefix(path, "/assets/") {
			t.Fatalf("asset is not self-contained: %s", path)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("asset %s: status %d, length %d", path, response.Code, response.Body.Len())
		}
	}
}

func TestNonAssetsNotServed(t *testing.T) {
	t.Parallel()
	assets, err := webui.Assets()
	if err != nil {
		t.Fatal(err)
	}
	handler := http.FileServerFS(assets)
	for _, path := range []string{"/frontend/package.json", "/src/main.tsx", "/missing.js"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("non-asset %s: status %d", path, response.Code)
		}
	}
}
