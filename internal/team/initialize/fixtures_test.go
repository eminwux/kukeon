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

package initialize

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/team/teamtest"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// fixtureRoleYAML stands in for an agent role of the eminwux/agents
// repository: it clones the project and the agents source, as the real roles
// do, so the fixtures exercise the repo fills of both.
const fixtureRoleYAML = `apiVersion: v1beta1
kind: Role
metadata: { name: %s }
spec:
  harnesses:
    claude:
      settings: config/claude.settings.json
    codex: {}
  connectors: [github]
  needs:
    repos: [project, agents]
    params:
      - AGENT_NAME
      - { name: AGENTS_REPO, default: "${ROLE_SOURCE_REPO}" }
`

// fixturePipelineYAML stands in for the agents repository's pipeline role.
const fixturePipelineYAML = `apiVersion: v1beta1
kind: Role
metadata: { name: pipeline }
spec:
  harnesses:
    script:
      command: python3 "$ROLE_SOURCE_DIR/$ROLE_PATH/reactor.py"
  connectors: [github]
  needs:
    repos: [project, agents]
    params: [AGENT_NAME]
  tasks:
    - { name: tick, schedule: "*/5 * * * *", suspend: true }
`

// fixtureAgentsTree is the eminwux/agents roles both fixtures name.
func fixtureAgentsTree() fstest.MapFS {
	tree := fstest.MapFS{"pipeline/role.yaml": &fstest.MapFile{Data: []byte(fixturePipelineYAML)}}
	for _, role := range []string{"dev", "pm", "pr-reviewer", "meta-dev", "meta-reviewer", "meta-pm"} {
		tree[role+"/role.yaml"] = &fstest.MapFile{Data: fmt.Appendf(nil, fixtureRoleYAML, role)}
		tree[role+"/AGENTS.md"] = &fstest.MapFile{Data: []byte("# " + role + "\n")}
		tree[role+"/config/claude.settings.json"] = &fstest.MapFile{Data: []byte("{}\n")}
	}
	return tree
}

// teamFixture is one of the two repository files AC5 pins: the repository's
// address and the definition trees composition reads.
type teamFixture struct {
	name, file, url string
}

func teamFixtures() []teamFixture {
	return []teamFixture{
		{name: "kukeon", file: "kukeon.kuketeam.yaml", url: "git@github.com:eminwux/kukeon"},
		{name: "agents", file: "agents.kuketeam.yaml", url: "git@github.com:eminwux/agents"},
	}
}

// fixtureFile reads a fixture's kuketeam.yaml.
func fixtureFile(t *testing.T, fx teamFixture) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", fx.file))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fixtureSources are the definition trees a fixture composes from: the
// repository itself as the project (its kuketeam.yaml at the root), the
// agents roles and this repository's harnesses.
func fixtureSources(t *testing.T, fx teamFixture) map[string]fs.FS {
	t.Helper()
	kukeon, err := teamtest.PlainSource("../../..")
	if err != nil {
		t.Fatal(err)
	}
	agents := fixtureAgentsTree()
	project := fstest.MapFS{}
	base := map[string]fstest.MapFS{"kukeon": kukeon, "agents": agents}[fx.name]
	for name, file := range base {
		project[name] = file
	}
	project[teamFileName] = &fstest.MapFile{Data: fixtureFile(t, fx)}
	return map[string]fs.FS{"kukeon": kukeon, "agents": agents, "project": project}
}

// renderComposed writes the Blueprints and Configs of a composition as one
// YAML stream, in composition order.
func renderComposed(t *testing.T, result *compose.Result) []byte {
	t.Helper()
	var docs []any
	for _, bp := range result.Blueprints {
		docs = append(docs, bp)
	}
	for _, pair := range result.Pairs {
		docs = append(docs, pair.Config)
		for _, task := range pair.Tasks {
			docs = append(docs, task)
		}
	}
	var buf bytes.Buffer
	for i, doc := range docs {
		if i > 0 {
			buf.WriteString("---\n")
		}
		raw, err := yaml.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(raw)
	}
	return buf.Bytes()
}

// composeLegacyShape composes a fixture the way kuke did before sources were
// stored (#1643): compose reads the declaration as written, with the project
// the command line names, over defaults that carry the kukeon source. Compose
// keeps those inputs, so this is the reference AC5 compares against.
func composeLegacyShape(t *testing.T, fx teamFixture) *compose.Result {
	t.Helper()
	team, err := config.DecodeTeamDeclaration(bytes.NewReader(fixtureFile(t, fx)))
	if err != nil {
		t.Fatal(err)
	}
	team.Metadata = config.Metadata{Name: fx.name, Realm: config.DefaultRealm}
	project := config.Source{
		Name: config.ProjectSourceName, URL: fx.url, Branch: "main", ClonePath: "/home/user/" + fx.name,
	}
	if declared := team.Spec.ProjectSource; declared != nil && declared.ClonePath != "" {
		project.ClonePath = declared.ClonePath
	}
	team.Spec.ProjectSource = &project
	defaults := config.EmptyDefaults(config.DefaultRealm)
	defaults.Spec.Defaults = config.Defaults{
		Sources:   []config.Source{config.MajordomoSource()},
		Harnesses: config.MajordomoHarnesses(),
	}
	result, err := compose.Compose(defaults, team, compose.Options{
		Sources: fixtureSources(t, fx), Scope: compose.Scope{Realm: config.DefaultRealm},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestExistingTeamFilesComposeAsBefore is #1643 AC5: kukeon's and
// eminwux/agents' kuketeam.yaml, created as teams over the stored state kuke
// team init leaves (the kukeon Source and defaults with no sources), render
// the same Blueprints and Configs as they did before sources were stored.
func TestExistingTeamFilesComposeAsBefore(t *testing.T) {
	for _, fx := range teamFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			f := newFake()
			f.storeSource(config.KukeonSourceName, config.KukeonSourceSpec())
			storeDefaults(t, f, InitialDefaults(config.DefaultRealm))
			out, err := Add(t.Context(), AddOptions{
				Options: Options{Client: f, Sources: fixtureSources(t, fx), DryRun: true},
				Name:    fx.name, Source: fx.url, Branch: "main",
			})
			if err != nil {
				t.Fatal(err)
			}
			got, want := renderComposed(t, out.Plan.composed), renderComposed(t, composeLegacyShape(t, fx))
			if !bytes.Equal(got, want) {
				t.Fatalf("composition changed:\n%s", lineDiff(string(want), string(got)))
			}
		})
	}
}

// lineDiff lists the lines of want and got that differ, by line number.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out strings.Builder
	for i := range max(len(w), len(g)) {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			fmt.Fprintf(&out, "%d:\n  - %s\n  + %s\n", i+1, a, b)
		}
	}
	return out.String()
}
