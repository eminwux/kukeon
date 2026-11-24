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

package cli_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// kukeHarness is a harness whose store holds the kuke team as kuke team init
// stores it.
func kukeHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.store.storeSource(t, config.KukeonSourceName, config.KukeonSourceSpec())
	h.store.putTeam(t, config.Majordomo())
	return h
}

func TestConnectorEnable(t *testing.T) {
	h := kukeHarness(t)
	out, err := h.run(t, "connector", "enable", "kuke", "github", "--role", "majordomo")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines := strings.Split(out, "\n")
	if lines[0] != "Connector github enabled for role majordomo of team kuke." ||
		!strings.HasPrefix(lines[1], "Applied ") || !strings.Contains(lines[1], "stored team kuke") {
		t.Fatalf("output:\n%s", out)
	}
	for _, want := range []string{
		"No cells created or started.",
		"Missing secrets (cells will not start until they exist):",
		"kuke create secret gh-token",
		"A running cell keeps what it started with; kuke restart <cell> renders it again.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q not in:\n%s", want, out)
		}
	}
	if _, ok := h.store.storedTeam(t, "kuke").Spec.Roles[0].Connectors["github"]; !ok {
		t.Fatal("the stored team has no github entry")
	}
	if h.store.team != "kuke" {
		t.Fatalf("applied team = %q", h.store.team)
	}
}

func TestConnectorEnableIdentity(t *testing.T) {
	h := kukeHarness(t)
	if out, err := h.run(t, "connector", "enable", "kuke", "github", "--role", "majordomo",
		"--identity", "bot-token"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := h.store.storedTeam(t, "kuke").Spec.Roles[0].Connectors["github"].Identity; got != "bot-token" {
		t.Fatalf("identity = %q", got)
	}
	// An unset --identity keeps it; --identity "" removes it.
	if out, err := h.run(t, "connector", "enable", "kuke", "github", "--role", "majordomo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := h.store.storedTeam(t, "kuke").Spec.Roles[0].Connectors["github"].Identity; got != "bot-token" {
		t.Fatalf("identity after a plain enable = %q", got)
	}
	if out, err := h.run(t, "connector", "enable", "kuke", "github", "--role", "majordomo",
		"--identity", ""); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, ok := h.store.storedTeam(t, "kuke").Spec.Roles[0].Connectors["github"]; !ok || got.Identity != "" {
		t.Fatalf("entry after --identity \"\" = %+v, %v", got, ok)
	}
}

func TestConnectorDryRun(t *testing.T) {
	h := kukeHarness(t)
	before := slices.Clone(h.store.teams["kuke"])
	out, err := h.run(t, "connector", "enable", "kuke", "github", "--role", "majordomo", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "Dry run:") || !strings.Contains(out, "github: {}") {
		t.Fatalf("output:\n%s", out)
	}
	if h.store.writes != 0 || !bytes.Equal(before, h.store.teams["kuke"]) {
		t.Fatal("a dry run wrote")
	}
}

func TestConnectorDisable(t *testing.T) {
	h := kukeHarness(t)
	if out, err := h.run(t, "connector", "enable", "kuke", "github", "--role", "majordomo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := h.run(t, "connector", "disable", "kuke", "github", "--role", "majordomo")
	if err != nil || !strings.HasPrefix(out, "Connector github disabled for role majordomo of team kuke.\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	if h.store.storedTeam(t, "kuke").Spec.Roles[0].Connectors != nil {
		t.Fatal("the entry is still stored")
	}
}

func TestConnectorArguments(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no role":        {[]string{"enable", "kuke", "github"}, `"role" not set`},
		"one argument":   {[]string{"enable", "kuke", "--role", "majordomo"}, "accepts 2 arg(s)"},
		"disable flag":   {[]string{"disable", "kuke", "github", "--role", "x", "--identity", "a"}, "unknown flag"},
		"list arguments": {[]string{"list"}, "accepts 1 arg(s)"},
		"unknown connector": {
			[]string{"enable", "kuke", "gitlab", "--role", "majordomo"}, `unknown connector "gitlab"`,
		},
		"harness connector": {[]string{"enable", "kuke", "claude", "--role", "majordomo"}, "spec.connectors"},
		"unknown role":      {[]string{"enable", "kuke", "github", "--role", "dev"}, `no role "dev"`},
		"not stored":        {[]string{"enable", "other", "github", "--role", "dev"}, "kuke create team other"},
	} {
		t.Run(name, func(t *testing.T) {
			h := kukeHarness(t)
			before := slices.Clone(h.store.teams["kuke"])
			out, err := h.run(t, append([]string{"connector"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q\n%s", err, tc.want, out)
			}
			if h.store.writes != 0 || !bytes.Equal(before, h.store.teams["kuke"]) {
				t.Fatal("a refused change wrote")
			}
		})
	}
}

func TestConnectorList(t *testing.T) {
	h := kukeHarness(t)
	team := config.Majordomo()
	team.Spec.Roles[0].Connectors = map[string]config.ConnectorChoice{"github": {}}
	h.store.putTeam(t, team)
	labels := func(task string) map[string]string {
		out := map[string]string{api.LabelTeam: "kuke", api.LabelRole: "majordomo"}
		if task != "" {
			out[api.LabelTask] = task
		}
		return out
	}
	login := api.ConnectorStatus{Connector: "claude", Method: "login", State: api.ConnectorResolved}
	github := api.ConnectorStatus{
		Connector: "github", Method: "token", State: api.ConnectorResolved, Origin: "realm",
		Identity: &api.ContainerSecretRef{Name: "gh-token", Realm: "default"},
	}
	missing := api.ConnectorStatus{Connector: "github", Method: "token", State: api.ConnectorUnresolved, Required: true}
	h.store.configs = []api.CellConfigDoc{
		{
			Metadata: api.CellConfigMetadata{
				Name:   "kuke-majordomo-claude-health-sweep",
				Labels: labels("health-sweep"),
			},
			Status: &api.CellConfigStatus{Connectors: []api.ConnectorStatus{login, missing}},
		},
		{
			Metadata: api.CellConfigMetadata{Name: "kuke-majordomo-claude", Labels: labels("")},
			Status:   &api.CellConfigStatus{Connectors: []api.ConnectorStatus{login, github}},
		},
	}
	out, err := h.run(t, "connector", "list", "kuke")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"ROLE       CONFIG                              CONNECTOR  METHOD  IDENTITY          SET BY    ",
		"---------  ----------------------------------  ---------  ------  ----------------  ----------",
		"majordomo  kuke-majordomo-claude               claude     login   -                 definition",
		"majordomo  kuke-majordomo-claude               github     token   gh-token (realm)  team      ",
		"majordomo  kuke-majordomo-claude-health-sweep  claude     login   -                 definition",
		"majordomo  kuke-majordomo-claude-health-sweep  github     token   MISSING           team      ",
		"",
	}, "\n")
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
	if h.store.writes != 0 {
		t.Fatal("list wrote")
	}
}
