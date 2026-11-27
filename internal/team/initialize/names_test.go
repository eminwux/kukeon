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
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/teamtest"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// collectPathNames walks a decoded YAML document and records every value that
// becomes a path segment: the name and scope coordinates of the document's own
// metadata and of every reference to a stored Blueprint, Secret or Volume, and
// container ids (each container gets its own metadata directory). Blueprint
// template placeholders are skipped.
func collectPathNames(node any, parentKey string, out map[string]string) {
	switch v := node.(type) {
	case map[string]any:
		switch parentKey {
		case "metadata", "blueprint", "secretRef", "volumeRef":
			for _, field := range []string{"name", "realm", "space", "stack", "cell"} {
				// A ${PARAM} in a blueprint template is filled by the Config
				// that materializes it, not stored as a name.
				if s, ok := v[field].(string); ok && s != "" && !strings.Contains(s, "${") {
					out[s] = parentKey + "." + field
				}
			}
		}
		if parentKey == "containers" {
			if id, ok := v["id"].(string); ok && id != "" {
				out[id] = "container id"
			}
		}
		for key, child := range v {
			collectPathNames(child, key, out)
		}
	case []any:
		for _, child := range v {
			collectPathNames(child, parentKey, out)
		}
	}
}

// TestBuiltInNamesPassSharedRule is #1719's guard that the shared name rule
// rejects nothing kukeon itself creates: the default realms and the daemon's
// scope, the web UI's admin cell, generated cell names, and every name the
// shipped kuketeam.yaml composes into Blueprints, Configs, tasks, Sources and
// Secret references.
func TestBuiltInNamesPassSharedRule(t *testing.T) {
	names := map[string]string{
		"default":      "default realm",
		"kuke-system":  "system realm",
		"kukeon":       "system space/stack",
		"kukeond":      "daemon cell",
		"web-ui-admin": "web UI admin cell",
	}
	for range 8 {
		generated, err := naming.GenerateCellName("kukeon-dev")
		if err != nil {
			t.Fatal(err)
		}
		names[generated] = "naming.GenerateCellName"
	}

	raw, err := os.ReadFile("../../../kuketeam.yaml")
	if err != nil {
		t.Fatal(err)
	}
	team, err := config.DecodeTeamDeclaration(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	team.Metadata.Realm = config.DefaultRealm
	names[team.Metadata.Name] = "team name"
	for _, src := range team.Spec.Sources {
		names[src.Name] = "source name"
	}
	project := config.Source{
		Name: config.ProjectSourceName, URL: "git@github.com:eminwux/kukeon", Branch: "main",
		ClonePath: "/home/user/kukeon",
	}
	team.Spec.ProjectSource = &project
	defaults := config.EmptyDefaults(config.DefaultRealm)
	defaults.Spec.Defaults = config.Defaults{
		Sources:   []config.Source{config.MajordomoSource()},
		Harnesses: config.MajordomoHarnesses(),
	}
	for _, src := range defaults.Spec.Defaults.Sources {
		names[src.Name] = "default source name"
	}
	kukeon, err := teamtest.PlainSource("../../..")
	if err != nil {
		t.Fatal(err)
	}
	result, err := compose.Compose(defaults, team, compose.Options{
		Sources: map[string]fs.FS{"kukeon": kukeon, "agents": fixtureAgentsTree(), "project": kukeon},
		Scope:   compose.Scope{Realm: config.DefaultRealm},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range result.DefaultSecrets {
		names[secret.Reference.Name] = "default secret"
	}

	var stream yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(renderComposed(t, result)))
	for dec.Decode(&stream) == nil {
		var doc any
		if err = stream.Decode(&doc); err != nil {
			t.Fatal(err)
		}
		collectPathNames(doc, "", names)
	}

	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	if len(keys) < 20 {
		t.Fatalf("collected only %d names, want the composed team's too: %v", len(keys), keys)
	}
	t.Logf("checked %d names: %v", len(keys), keys)
	for _, name := range keys {
		if !naming.IsValidName(name) {
			t.Errorf("%s %q fails the shared name rule %s", names[name], name, naming.NamePattern)
		}
	}
}
