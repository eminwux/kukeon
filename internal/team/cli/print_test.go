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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/cli"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/team/config"
)

func TestPrintOutcomeResult(t *testing.T) {
	var out bytes.Buffer
	err := cli.PrintOutcome(&out, initialize.Outcome{Result: &initialize.Result{
		Team: "proj", Resources: 5, Pruned: []string{"proj-gone"},
		MissingSecrets: []string{"default/default/default/gh-token"}, Notes: []string{"a note"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "Applied 5 resources; stored team proj.\nPruned configs: proj-gone\nNo cells created or started.\n" +
		"Missing secrets (cells will not start until they exist):\n  default/default/default/gh-token\n" +
		"Notes:\n  a note\n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	out.Reset()
	if err = cli.PrintOutcome(&out, initialize.Outcome{Result: &initialize.Result{Team: "kuke", Resources: 2}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Notes:") || strings.Contains(out.String(), "Missing") ||
		strings.Contains(out.String(), "Pruned") {
		t.Fatalf("printed empty sections:\n%s", out.String())
	}
}

func TestPrintOutcomePlan(t *testing.T) {
	var out bytes.Buffer
	err := cli.PrintOutcome(&out, initialize.Outcome{Plan: &initialize.Plan{
		Origin: initialize.Origin{Kind: initialize.OriginStored},
		Team: &config.Team{
			APIVersion: config.APIVersion,
			Kind:       config.KindTeam,
			Metadata:   config.Metadata{Name: "proj"},
		},
		Sources: []initialize.PlannedSource{
			{Name: "kukeon", URL: "github.com/eminwux/kukeon", Ref: "branch main", Commit: "abc"},
			{Name: "project", URL: projectURL, Ref: "tag v1"},
		},
		Pairs: []initialize.PlannedPair{{
			Role: "lead", Harness: "codex", Config: "proj-lead-codex", Image: "img",
			Components: []string{"github"},
			Tasks: []initialize.PlannedTask{
				{Name: "sweep", Config: "proj-lead-codex-sweep", Schedule: "@daily", Suspend: true},
				{Name: "once", Config: "proj-lead-codex-once"},
			},
		}},
		Create: []string{"proj-lead-codex"}, Prune: []string{"proj-old"},
		CreateSecrets: []string{"gh-token"}, MissingSecrets: []string{"other"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Dry run: nothing was applied or created, and no secret value was read.",
		"Team proj, declaration from stored", "  name: proj",
		"  kukeon  github.com/eminwux/kukeon  branch main  abc", "project  " + projectURL + "  tag v1  unresolved",
		"lead × codex → config proj-lead-codex, image img", "connectors and components: github",
		"task sweep → config proj-lead-codex-sweep, @daily (suspended)", "task once → config proj-lead-codex-once, no schedule",
		"Configs to create: proj-lead-codex", "Configs to re-render: none", "Configs to prune: proj-old",
		"Secrets to create from their declared source:\n  gh-token", "Missing secrets (cells will not start until they exist):\n  other",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
}

func TestPrintInheriting(t *testing.T) {
	var out bytes.Buffer
	if err := cli.PrintInheriting(&out, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	want := "Teams inheriting the defaults (not recomposed): a, b\n  kuke team apply a\n  kuke team apply b\n"
	if out.String() != want {
		t.Fatalf("got %q", out.String())
	}
}

// An outcome with no plan (kuke team init --upgrade with nothing to apply)
// prints its notes only.
func TestPrintOutcomeNotesOnly(t *testing.T) {
	var out bytes.Buffer
	if err := cli.PrintOutcome(&out, initialize.Outcome{Notes: []string{"kuke team and defaults already at commit x"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "kuke team and defaults already at commit x\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
