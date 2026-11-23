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
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const agentsRepoDefault = "    params:\n      - { name: AGENTS_REPO, default: \"${ROLE_SOURCE_REPO}\" }\n"

// withRoleParams composes the majordomo role with the given needs.params
// block, its source cloned from url.
func withRoleParams(t *testing.T, params, url string) (*config.TeamDefaults, compose.Options) {
	t.Helper()
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	role := string(source["team/roles/majordomo/role.yaml"].Data)
	role = strings.Replace(role, "  needs:\n", "  needs:\n"+params, 1)
	source["team/roles/majordomo/role.yaml"] = &fstest.MapFile{Data: []byte(role)}
	if url != "" {
		cfg.Spec.Defaults.Sources[0].URL = url
	}
	return cfg, opts
}

// A role-declared default needs no spec.parameters entry and reaches both the
// Config's values and the cell environment; ROLE_SOURCE_REPO reduces every
// clone URL form to <owner>/<repo>.
func TestRoleParamDefaultFromRoleSource(t *testing.T) {
	for _, url := range []string{
		"git@github.com:eminwux/agents",
		"git@github.com:eminwux/agents.git",
		"ssh://git@github.com/eminwux/agents",
		"ssh://git@github.com:2222/eminwux/agents.git",
		"https://github.com/eminwux/agents",
		"https://github.com/eminwux/agents.git",
		"github.com/eminwux/agents",
	} {
		t.Run(url, func(t *testing.T) {
			cfg, opts := withRoleParams(t, agentsRepoDefault, url)
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			pair := result.Pairs[0]
			if got := pair.Config.Spec.Values["AGENTS_REPO"]; got != "eminwux/agents" {
				t.Fatalf("AGENTS_REPO value = %q", got)
			}
			if got := envOf(work(t, pair, interactive))["AGENTS_REPO"]; got != "eminwux/agents" {
				t.Fatalf("AGENTS_REPO env = %q", got)
			}
			if _, fact := pair.Config.Spec.Values["ROLE_SOURCE_REPO"]; fact {
				t.Fatal("ROLE_SOURCE_REPO is a fact, not a Config value")
			}
			for _, note := range result.Notes {
				if strings.Contains(note, "AGENTS_REPO") {
					t.Fatalf("unexpected note: %s", note)
				}
			}
		})
	}
}

// A bare name keeps today's meaning: the operator supplies it, and it reaches
// the cell environment.
func TestRoleParamBareName(t *testing.T) {
	cfg, opts := withRoleParams(t, "    params: [REGION]\n", "")
	if _, err := compose.Compose(cfg, selectedTeam(cfg), opts); err == nil ||
		!strings.Contains(err.Error(), `missing required parameter "REGION"`) {
		t.Fatalf("err = %v", err)
	}
	cfg.Spec.Parameters = map[string]string{"REGION": "eu-test"}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := envOf(work(t, result.Pairs[0], interactive))["REGION"]; got != "eu-test" {
		t.Fatalf("REGION env = %q", got)
	}
}

// Defaults may be literal or interpolate any composed fact.
func TestRoleParamDefaultInterpolation(t *testing.T) {
	cfg, opts := withRoleParams(t,
		"    params:\n"+
			"      - { name: REGION, default: eu-west }\n"+
			"      - { name: WHERE, default: \"${ROLE_SOURCE_REPO}@${ROLE_PATH}\" }\n"+
			"      - { name: BLANK, default: \"\" }\n",
		"https://github.com/eminwux/agents.git")
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	values := result.Pairs[0].Config.Spec.Values
	if values["REGION"] != "eu-west" || values["WHERE"] != "eminwux/agents@team/roles/majordomo" {
		t.Fatalf("values = %v", values)
	}
	if _, set := values["BLANK"]; set {
		t.Fatal("an empty default is left to the blueprint")
	}
	for _, note := range result.Notes {
		if strings.Contains(note, "BLANK") {
			t.Fatalf("an explicitly empty default is noted: %s", note)
		}
	}
}

// An unreducible role-source URL leaves ROLE_SOURCE_REPO empty and notes the
// role and each param whose default resolved empty.
func TestRoleParamUnreducibleSource(t *testing.T) {
	cfg, opts := withRoleParams(t, agentsRepoDefault, "https://gitlab.example.com/group/sub/agents.git")
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, set := result.Pairs[0].Config.Spec.Values["AGENTS_REPO"]; set {
		t.Fatalf("AGENTS_REPO = %q, want unset", got)
	}
	if got := envOf(work(t, result.Pairs[0], interactive))["AGENTS_REPO"]; got != "" {
		t.Fatalf("AGENTS_REPO env = %q", got)
	}
	var noted bool
	for _, note := range result.Notes {
		noted = noted || strings.Contains(note, "role majordomo") && strings.Contains(note, "AGENTS_REPO") &&
			strings.Contains(note, "gitlab.example.com/group/sub/agents.git")
	}
	if !noted {
		t.Fatalf("notes = %v", result.Notes)
	}
	// The operator's value silences the note.
	cfg.Spec.Parameters = map[string]string{"AGENTS_REPO": "group/agents"}
	result, err = compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Pairs[0].Config.Spec.Values["AGENTS_REPO"]; got != "group/agents" {
		t.Fatalf("AGENTS_REPO = %q", got)
	}
	for _, note := range result.Notes {
		if strings.Contains(note, "AGENTS_REPO") {
			t.Fatalf("overridden default still noted: %s", note)
		}
	}
}

func TestRoleParamDefaultFailures(t *testing.T) {
	for _, tc := range []struct{ name, params, want string }{
		{
			"unknown-fact",
			"    params:\n      - { name: REGION, default: \"${NOT_A_FACT}\" }\n",
			`unknown fact "NOT_A_FACT"`,
		},
		{
			"blueprint-parameter",
			"    params:\n      - { name: AGENT_NAME, default: me }\n",
			"a role cannot default it",
		},
		{
			"reserved-environment",
			"    params:\n      - { name: PATH, default: /tmp }\n",
			`"PATH" is reserved in the cell environment`,
		},
		{
			"unknown-field",
			"    params:\n      - { name: REGION, value: eu }\n",
			`unknown field "value"`,
		},
		{
			"duplicate",
			"    params:\n      - { name: REGION, default: a }\n      - { name: REGION, default: b }\n",
			`duplicate parameter "REGION"`,
		},
		{"duplicate-default-then-bare", "    params:\n      - { name: REGION, default: a }\n      - REGION\n", `duplicate parameter "REGION"`},
		{"duplicate-bare-then-default", "    params:\n      - REGION\n      - { name: REGION, default: a }\n", `duplicate parameter "REGION"`},
		{"duplicate-bare-then-bare", "    params:\n      - REGION\n      - REGION\n", `duplicate parameter "REGION"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := withRoleParams(t, tc.params, "")
			if _, err := compose.Compose(cfg, selectedTeam(cfg), opts); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
