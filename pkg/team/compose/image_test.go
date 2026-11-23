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

package compose_test

import (
	"testing"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

func TestImageSelectionRequiresCompleteCapabilities(t *testing.T) {
	catalog := &harness.ImageCatalog{
		APIVersion: "v1beta1", Kind: "HarnessImageCatalog",
		Metadata: model.Metadata{Name: "codex"},
		Spec: harness.CatalogSpec{Images: []harness.CatalogImage{
			{Image: "registry.test/root:v1", Capabilities: []string{"codex", "gh", "go", "root"}},
			{Image: "registry.test/go:v1", Capabilities: []string{"codex", "go"}},
			{Image: "registry.test/gh:v1", Capabilities: []string{"codex", "gh"}},
			{Image: "registry.test/second-go:v1", Capabilities: []string{"codex", "go"}},
		}},
	}
	if _, err := compose.SelectImage(catalog, "codex", []string{"go", "gh"}); err == nil {
		t.Fatal("combined capabilities or selected root without opt-in")
	}
	selected, err := compose.SelectImage(catalog, "codex", []string{"go"})
	if err != nil || selected.Image != "registry.test/go:v1" {
		t.Fatalf("selection order/root opt-in: %+v %v", selected, err)
	}
	selected, err = compose.SelectImage(catalog, "codex", []string{"go", "root"})
	if err != nil || selected.Image != "registry.test/root:v1" {
		t.Fatalf("root selection: %+v %v", selected, err)
	}
	if _, err = compose.SelectImage(catalog, "claude", nil); err == nil {
		t.Fatal("accepted another harness catalog")
	}
	for _, ref := range []string{"local-go", "codex:dev", "registry.test/go", "invalid::ref"} {
		catalog.Spec.Images[1].Image = ref
		if _, err = compose.SelectImage(catalog, "codex", []string{"go"}); err == nil {
			t.Fatalf("accepted nonpublished reference %q", ref)
		}
	}
	catalog.Spec.Images[1].Image = "registry.test/go:v1"
	catalog.Spec.Images[1].Capabilities = []string{"go"}
	if _, err = compose.SelectImage(catalog, "codex", []string{"go"}); err == nil {
		t.Fatal("harness capability not required")
	}
}
