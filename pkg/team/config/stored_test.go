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

package config_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
)

// TestValidateSource is #1643 AC1's body rules.
func TestValidateSource(t *testing.T) {
	good := config.SourceSpec{URL: "github.com/eminwux/agents", Branch: "main"}
	if err := config.ValidateSource("agents", good); err != nil {
		t.Fatal(err)
	}
	identity := good
	identity.Git = &config.SourceGit{IdentityRef: &config.SecretRef{Name: "git-ssh", Realm: "default"}}
	if err := config.ValidateSource("agents", identity); err != nil {
		t.Fatal(err)
	}
	badIdentity := good
	badIdentity.Git = &config.SourceGit{IdentityRef: &config.SecretRef{Name: "git-ssh"}}
	for name, tc := range map[string]struct {
		name string
		spec config.SourceSpec
		want string
	}{
		"two refs":     {"agents", config.SourceSpec{URL: good.URL, Branch: "main", Tag: "v1"}, "exactly one"},
		"no ref":       {"agents", config.SourceSpec{URL: good.URL}, "exactly one"},
		"project":      {"project", good, "project is reserved"},
		"bad name":     {"Agents", good, "invalid source name"},
		"no url":       {"agents", config.SourceSpec{Branch: "main"}, "spec.url"},
		"bad identity": {"agents", badIdentity, "spec.git.identityRef"},
	} {
		err := config.ValidateSource(tc.name, tc.spec)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

// TestStoredDefaultsHoldNoSources is #1643 AC2: kukeond rejects a
// TeamDefaults listing sources, empty or not, and naming kuke create source;
// default roles and harnesses may name sources it does not know (D8).
func TestStoredDefaultsHoldNoSources(t *testing.T) {
	defaults := config.EmptyDefaults(config.DefaultRealm)
	defaults.Spec.Defaults.Harnesses = config.MajordomoHarnesses()
	defaults.Spec.Defaults.Roles = []config.Role{{Name: "dev", Source: "agents", Path: "dev"}}
	if err := config.ValidateStoredDefaults(defaults); err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(defaults); err == nil {
		t.Fatal("Validate accepted default roles naming an unlisted source")
	}
	for _, sources := range [][]config.Source{{}, {config.MajordomoSource()}} {
		defaults.Spec.Defaults.Sources = sources
		err := config.ValidateStoredDefaults(defaults)
		if err == nil || !strings.Contains(err.Error(), "kuke create source") {
			t.Fatalf("sources %v: err = %v", sources, err)
		}
	}
}

// TestTeamEntriesAreReferences is #1643 AC3: a stored Team's entries name
// their Source; one with url or git is a definition, which only a
// kuketeam.yaml may carry.
func TestTeamEntriesAreReferences(t *testing.T) {
	team := func(entry string) string {
		return "apiVersion: v1beta1\nkind: Team\nmetadata: {name: kukeon, realm: default}\nspec:\n" + entry
	}
	reference := team("  projectSource: {name: project, source: kukeon, branch: main, clonePath: /home/user/kukeon}\n" +
		"  sources:\n    - {name: agents, source: agents-fork, tag: v1, clonePath: /home/user/agents}\n")
	decoded, err := config.DecodeTeam(strings.NewReader(reference))
	if err != nil {
		t.Fatal(err)
	}
	if err = config.ValidateTeam(decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Spec.Sources[0].Stored; got != "agents-fork" {
		t.Fatalf("source = %q", got)
	}
	for name, tc := range map[string]struct{ doc, want string }{
		"entry url":   {team("  sources:\n    - {name: agents, url: github.com/eminwux/agents}\n"), "spec.sources[0].url"},
		"project url": {team("  projectSource: {url: github.com/eminwux/kukeon}\n"), "spec.projectSource.url"},
		"entry git": {
			team("  sources:\n    - {name: agents, git: {identityRef: {name: k, realm: default}}}\n"),
			"spec.sources[0].git",
		},
	} {
		definition, decodeErr := config.DecodeTeam(strings.NewReader(tc.doc))
		if decodeErr != nil {
			t.Fatalf("%s: a kuketeam.yaml may carry a definition: %v", name, decodeErr)
		}
		if err = config.ValidateTeam(definition); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	for name, tc := range map[string]struct{ doc, want string }{
		"two refs": {team("  sources:\n    - {name: agents, branch: main, tag: v1}\n"), "at most one"},
		"alias project": {
			team("  sources:\n    - {name: project}\n"), "project names spec.projectSource",
		},
		"bad source": {team("  sources:\n    - {name: agents, source: Bad}\n"), "spec.sources[0].source"},
		"duplicate":  {team("  sources:\n    - {name: agents}\n    - {name: agents}\n"), "duplicate name"},
	} {
		if _, decodeErr := config.DecodeTeam(strings.NewReader(tc.doc)); decodeErr == nil ||
			!strings.Contains(decodeErr.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, decodeErr, tc.want)
		}
	}
}
