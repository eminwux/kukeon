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

package harnessbuild_test

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/team/harnessbuild"
)

func buildSource(t *testing.T) fstest.MapFS {
	t.Helper()
	source := os.DirFS("../../../team")
	out := fstest.MapFS{}
	for _, pattern := range []string{"harnesses/*/harness.yaml", "harnesses/*/context/Dockerfile*", "harnesses/base/Dockerfile*"} {
		files, err := fs.Glob(source, pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, filename := range files {
			raw, readErr := fs.ReadFile(source, filename)
			if readErr != nil {
				t.Fatal(readErr)
			}
			out[filename] = &fstest.MapFile{Data: raw}
		}
	}
	return out
}

func TestBuildUsesAuthoredContextAndDockerfile(t *testing.T) {
	source := buildSource(t)
	filename := "harnesses/codex/harness.yaml"
	raw := strings.Replace(
		string(source[filename].Data),
		"context: harnesses/codex/context",
		"context: custom/context",
		1,
	)
	raw = strings.Replace(raw, "dockerfile: Dockerfile\n", "dockerfile: Customfile\n", 1)
	source[filename].Data = []byte(raw)
	source["custom/context/Customfile"] = &fstest.MapFile{Data: []byte("FROM ${IMAGE_PREFIX}base-user:${TAG}\n")}
	plan, err := harnessbuild.Build(source, "harnesses/codex", "", "dev", "linux/amd64", []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	target := plan.Target["codex"]
	if target.Context != "custom/context" || target.Dockerfile != "Customfile" ||
		!slices.Equal(target.Tags, []string{"codex:dev"}) {
		t.Fatalf("ignored authored build: %+v", target)
	}
	if target.Contexts["base-user:dev"] != "target:base-user" ||
		plan.Target["base-user"].Contexts["base:dev"] != "target:base" {
		t.Fatal("missing transitive base links")
	}
}

func TestBuildRejectsCrossHarnessDuplicateNames(t *testing.T) {
	source := buildSource(t)
	filename := "harnesses/codex/harness.yaml"
	source[filename].Data = []byte(
		strings.Replace(string(source[filename].Data), "name: codex-root", "name: claude", 1),
	)
	_, err := harnessbuild.Build(source, "", "registry.test/team", "v1", "linux/amd64", nil)
	if err == nil || !strings.Contains(err.Error(), filename) ||
		!strings.Contains(err.Error(), "duplicate image name") {
		t.Fatalf("duplicate build target accepted: %v", err)
	}
}
