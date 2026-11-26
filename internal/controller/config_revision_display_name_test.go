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

package controller

import (
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestConfigRevisionIgnoresTheDisplayName (issue #1692): renaming an agent
// changes only its Config's display-name annotation, which must not change
// the config revision its cells are compared against; any other annotation
// still does, and a Config without a display name hashes as before.
func TestConfigRevisionIgnoresTheDisplayName(t *testing.T) {
	doc := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{Name: "default-agent-claude-1a2b3c", Realm: "default"},
		Spec:     v1beta1.CellConfigSpec{Values: map[string]string{"A": "1"}},
	}
	base := configRevision(doc)
	if base != hashJSON(doc) {
		t.Fatal("a Config without a display name must hash as before")
	}
	named := doc
	named.Metadata.Annotations = map[string]string{v1beta1.AnnotationDisplayName: "Reviewer"}
	if got := configRevision(named); got != base {
		t.Fatalf("display name changed the revision: %s != %s", got, base)
	}
	if named.Metadata.Annotations[v1beta1.AnnotationDisplayName] != "Reviewer" {
		t.Fatal("configRevision mutated the caller's annotations")
	}
	withTemplate := doc
	withTemplate.Metadata.Annotations = map[string]string{v1beta1.AnnotationAgentTemplate: "default-agent-claude"}
	both := doc
	both.Metadata.Annotations = map[string]string{
		v1beta1.AnnotationAgentTemplate: "default-agent-claude", v1beta1.AnnotationDisplayName: "Docs bot",
	}
	if configRevision(both) != configRevision(withTemplate) {
		t.Fatal("the display name changed the revision of an annotated Config")
	}
	if configRevision(withTemplate) == base {
		t.Fatal("another annotation no longer changes the revision")
	}
}
