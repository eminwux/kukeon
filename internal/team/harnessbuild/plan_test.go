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

// TestBuildStampsCapabilityLabel locks step 9 of #1472: every harness image is
// built carrying its authored capabilities as io.kukeon.capabilities so the
// daemon can check a cell's `requires` against it; base images declare none.
func TestBuildStampsCapabilityLabel(t *testing.T) {
	plan, err := harnessbuild.Build(buildSource(t), "", "registry.test/team", "v1", "linux/amd64", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Target["claude-root"].Labels["io.kukeon.capabilities"]
	for _, capability := range []string{"claude", "git", "gh", "root"} {
		if !slices.Contains(strings.Split(got, ","), capability) {
			t.Errorf("claude-root label %q lacks capability %q", got, capability)
		}
	}
	if len(plan.Target["base"].Labels) != 0 {
		t.Errorf("base image must carry no capability label, got %v", plan.Target["base"].Labels)
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

// TestBuildSkipsSharedBaseEntries checks a harness whose image is a published
// shared base adds no Bake target and claims no image name.
func TestBuildSkipsSharedBaseEntries(t *testing.T) {
	source := buildSource(t)
	delete(source, "harnesses/script/harness.yaml")
	without, err := harnessbuild.Build(source, "", "registry.test/team", "v1", "linux/amd64", nil)
	if err != nil {
		t.Fatal(err)
	}
	source["harnesses/shared/harness.yaml"] = &fstest.MapFile{Data: []byte(`apiVersion: v1beta1
kind: Harness
metadata:
  name: shared
spec:
  renderer: kukeon/v1beta1
  shell: /bin/bash
  skillPath: $HOME/.agents/skills
  modes:
    headless:
      onInit:
        - script: exec true
  images:
    - name: base-user
      capabilities: [shared]
`)}
	if _, _, err = harnessbuild.Definitions(source); err != nil {
		t.Fatalf("shared base entry rejected: %v", err)
	}
	with, err := harnessbuild.Build(source, "", "registry.test/team", "v1", "linux/amd64", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(with.Group["default"].Targets, without.Group["default"].Targets) ||
		len(with.Target) != len(without.Target) {
		t.Fatalf("shared entry changed the plan: %v vs %v", with.Group, without.Group)
	}
	if len(with.Target["base-user"].Labels) != 0 {
		t.Fatalf("shared entry stamped base-user: %v", with.Target["base-user"].Labels)
	}
	// A built image still cannot take a shared base's name.
	source["harnesses/shared/harness.yaml"].Data = []byte(strings.Replace(
		string(source["harnesses/shared/harness.yaml"].Data),
		"      capabilities: [shared]",
		"      build: {context: harnesses/base, dockerfile: Dockerfile.user}\n      capabilities: [shared]",
		1,
	))
	if _, err = harnessbuild.Build(source, "", "registry.test/team", "v1", "linux/amd64", nil); err == nil ||
		!strings.Contains(err.Error(), "duplicate image name") {
		t.Fatalf("built image named base-user accepted: %v", err)
	}
}
