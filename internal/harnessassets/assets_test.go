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

// Package harnessassets contains packaging checks, not a production compositor.
package harnessassets_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/harness"
)

const root = "../../harnesses"

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCatalogAndBuildClosure(t *testing.T) {
	expected := map[string]bool{
		"claude":         true,
		"claude-root":    true,
		"claude-android": true,
		"codex":          true,
		"codex-root":     true,
		"opencode":       true,
		"opencode-root":  true,
	}
	var tag string
	for _, name := range []string{"claude", "codex", "opencode"} {
		h, err := harness.Load(os.DirFS(root), name)
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := harness.LoadCatalog(os.DirFS(root), name, h.Metadata.Name)
		if err != nil {
			t.Fatal(err)
		}
		if len(h.Spec.Images) != len(catalog.Spec.Images) {
			t.Fatal("build/catalog count mismatch")
		}
		for i, image := range h.Spec.Images {
			if !expected[image.Name] {
				t.Fatalf("duplicate or unexpected image %s", image.Name)
			}
			delete(expected, image.Name)
			published := catalog.Spec.Images[i]
			prefix := "ghcr.io/eminwux/kuketeams/" + image.Name + ":"
			if !strings.HasPrefix(published.Image, prefix) {
				t.Fatalf("wrong namespace: %s", published.Image)
			}
			current := strings.TrimPrefix(published.Image, prefix)
			if tag != "" && tag != current {
				t.Fatal("mixed catalog tags")
			}
			tag = current
			if !slices.Equal(image.Capabilities, published.Capabilities) {
				t.Fatal("capability drift")
			}
			if image.Build.Context != "harnesses/"+name+"/context" {
				t.Fatal("context escapes harness directory")
			}
			checkDockerfile(t, name, filepath.Join(name, "context", image.Build.Dockerfile))
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing images: %v", expected)
	}
	for _, name := range []string{"root", "user"} {
		if !strings.Contains(string(read(t, "base/Dockerfile."+name)), "FROM ${IMAGE_PREFIX}base:${TAG}") {
			t.Fatal("broken base dependency")
		}
	}
	read(t, "base/Dockerfile")
}

func checkDockerfile(t *testing.T, harness, path string) {
	t.Helper()
	content := string(read(t, path))
	if !strings.Contains(content, "COPY Makefile.start /opt/kuketeams/harness/Makefile.start") {
		t.Fatal("missing startup adapter")
	}
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "COPY":
			for _, source := range fields[1 : len(fields)-1] {
				read(t, filepath.Join(harness, "context", source))
			}
		case "FROM":
			if !strings.HasPrefix(fields[1], "${IMAGE_PREFIX}base-") || !strings.HasSuffix(fields[1], ":${TAG}") {
				t.Fatalf("unpackaged base: %s", line)
			}
			base := strings.TrimSuffix(strings.TrimPrefix(fields[1], "${IMAGE_PREFIX}base-"), ":${TAG}")
			read(t, "base/Dockerfile."+base)
		}
	}
}

func TestSourceInventoryIsPackaged(t *testing.T) {
	for _, line := range strings.Split(strings.TrimSpace(string(read(t, "UPSTREAM.tsv"))), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			t.Fatalf("invalid upstream inventory row: %q", line)
		}
		// Keep the deleted central catalog's original blob in the historical
		// inventory, even though release catalogs now live beside each Harness.
		if fields[3] == "harnesses/images.yaml" {
			continue
		}
		// The image publication workflow stays in eminwux/kuketeams until
		// publishing moves to kukeon releases (#1430).
		if fields[3] == ".github/workflows/publish-harness-images.yml" {
			continue
		}
		asset := fields[3]
		if strings.HasSuffix(asset, "partials.tmpl.yaml") {
			asset = "renderers/kukeon/v1beta1/partials.tmpl.yaml"
		}
		if strings.HasSuffix(asset, "blueprint.tmpl.yaml") {
			asset = "renderers/kukeon/v1beta1/blueprint.tmpl.yaml"
		}
		for _, name := range []string{"claude", "codex", "opencode"} {
			prefix := "harnesses/" + name + "/"
			if strings.HasPrefix(asset, prefix) &&
				(strings.HasPrefix(strings.TrimPrefix(asset, prefix), "Dockerfile") || strings.Contains(asset, "/hooks/") || strings.HasSuffix(asset, "requirements.toml")) {
				asset = prefix + "context/" + strings.TrimPrefix(asset, prefix)
			}
		}
		path := filepath.Join("../..", asset)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fields[0] == "100755" && info.Mode()&0o111 == 0 {
			t.Fatalf("lost executable mode: %s", path)
		}
	}
}
