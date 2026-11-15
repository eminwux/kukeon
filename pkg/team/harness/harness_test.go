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

package harness_test

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/pkg/team/harness"
)

func TestInvalidHarnessSchema(t *testing.T) {
	original, err := os.ReadFile("../../../team/harnesses/codex/harness.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, old, value, want string }{
		{"duplicate", "name: codex-root", "name: codex", "duplicate or invalid image name"},
		{"invalid-name", "name: codex-root", "name: Invalid/Name", "invalid image name"},
		{"context", "context: harnesses/codex/context", "context: ../escape", "repository-relative"},
		{"dockerfile", "dockerfile: Dockerfile.root", "dockerfile: /escape", "context-relative"},
		{"capability", "[codex, git, gh, go, node, make, root]", "[git, root]", "include harness"},
		{"obsolete-ref", "    - name: codex-root", "    - ref: codex-root", "field ref not found"},
		{"extra-document", "kind: Harness", "kind: Harness\n---\nkind: Harness", "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := strings.Replace(string(original), test.old, test.value, 1)
			_, loadErr := harness.Load(fstest.MapFS{"custom/harness.yaml": {Data: []byte(raw)}}, "custom")
			if loadErr == nil || !strings.Contains(loadErr.Error(), "custom/harness.yaml") {
				t.Fatalf("missing path-specific error: %v", loadErr)
			}
			if test.want != "invalid" && !strings.Contains(loadErr.Error(), test.want) {
				t.Fatalf("want %q: %v", test.want, loadErr)
			}
		})
	}
}

func TestCatalogGeneration(t *testing.T) {
	h, err := harness.Load(os.DirFS("../../../team"), "harnesses/codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, registry := range []string{"ghcr.io/eminwux/kuketeams", ""} {
		c, genErr := harness.Generate(h, registry, "v0.2.1")
		if genErr != nil {
			t.Fatal(genErr)
		}
		assertGenerated(t, h, c, registry)
		first, marshalErr := harness.MarshalCatalog(c)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		second, marshalErr := harness.MarshalCatalog(c)
		if marshalErr != nil || !bytes.Equal(first, second) {
			t.Fatal("unstable generation")
		}
		if registry != "" {
			committed, readErr := os.ReadFile("../../../team/harnesses/codex/images.yaml")
			if readErr != nil || !bytes.Equal(first, committed) {
				t.Fatal("committed catalog drift")
			}
		}
	}
	for _, registry := range []string{"invalid registry", "registry/", "/bad"} {
		if _, genErr := harness.Generate(h, registry, "dev"); genErr == nil {
			t.Fatalf("accepted registry %q", registry)
		}
	}
}

func TestCatalogRejectsWrongShapeAndIdentity(t *testing.T) {
	original, err := os.ReadFile("../../../team/harnesses/codex/images.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, old, value string }{
		{"kind", "kind: HarnessImageCatalog", "kind: ImageCatalog"},
		{"identity", "name: codex", "name: claude"},
		{"obsolete-build", "  images:", "  build: {}\n  images:"},
		{"invalid-ref", "ghcr.io/eminwux/kuketeams/codex:v0.2.1", "bad::ref"},
		{"missing-capability", "        - codex\n", "        - wrong\n"},
		{"duplicate-image", "ghcr.io/eminwux/kuketeams/codex-root:v0.2.1", "ghcr.io/eminwux/kuketeams/codex:v0.2.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := strings.Replace(string(original), test.old, test.value, 1)
			if raw == string(original) {
				t.Fatalf("fixture did not replace %q", test.old)
			}
			_, loadErr := harness.LoadCatalog(
				fstest.MapFS{"custom/images.yaml": {Data: []byte(raw)}},
				"custom",
				"codex",
			)
			if loadErr == nil || !strings.Contains(loadErr.Error(), "custom/images.yaml") {
				t.Fatalf("accepted malformed catalog: %v", loadErr)
			}
		})
	}
}

func assertGenerated(t *testing.T, h *harness.Harness, c *harness.ImageCatalog, registry string) {
	t.Helper()
	if c.Metadata.Name != "codex" || len(c.Spec.Images) != 2 {
		t.Fatal("lost identity or variants")
	}
	for i, image := range c.Spec.Images {
		prefix := ""
		if registry != "" {
			prefix = registry + "/"
		}
		if image.Image != prefix+h.Spec.Images[i].Name+":v0.2.1" ||
			!reflect.DeepEqual(image.Capabilities, h.Spec.Images[i].Capabilities) {
			t.Fatalf("generation: %+v", image)
		}
	}
}
