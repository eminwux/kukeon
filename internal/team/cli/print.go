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

package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"gopkg.in/yaml.v3"
)

// PrintOutcome reports a team application: the plan for a dry run, the result
// otherwise. `kuke apply -f` prints a Team document's outcome with it.
func PrintOutcome(w io.Writer, out initialize.Outcome) error {
	p := &printer{w: w}
	if out.Result == nil && out.Plan == nil {
		for _, note := range out.Notes {
			p.linef("%s", note)
		}
		return p.err
	}
	if out.Result == nil {
		p.plan(out.Plan)
		return p.err
	}
	r := out.Result
	p.linef("Applied %d resources; stored team %s.", r.Resources, r.Team)
	if len(r.Pruned) > 0 {
		p.linef("Pruned configs: %s", strings.Join(r.Pruned, ", "))
	}
	p.linef("No cells created or started.")
	p.list("Missing secrets (cells will not start until they exist):", r.MissingSecrets)
	p.list("Notes:", r.Notes)
	return p.err
}

// PrintDefaultsChange reports an edit of the TeamDefaults and the teams it
// reaches only when recomposed (AC14).
func PrintDefaultsChange(w io.Writer, what string, change initialize.DefaultsChange) error {
	p := &printer{w: w}
	p.linef("%s.", what)
	for _, note := range change.Notes {
		p.linef("Note: %s", note)
	}
	printInheriting(p, change.Inheriting)
	return p.err
}

// PrintInheriting tells which stored teams inherit the TeamDefaults and how
// to recompose them.
func PrintInheriting(w io.Writer, teams []string) error {
	p := &printer{w: w}
	printInheriting(p, teams)
	return p.err
}

func printInheriting(p *printer, teams []string) {
	if len(teams) == 0 {
		p.linef("No stored team inherits the defaults.")
		return
	}
	p.linef("Teams inheriting the defaults (not recomposed): %s", strings.Join(teams, ", "))
	for _, team := range teams {
		p.linef("  kuke team apply %s", team)
	}
}

type printer struct {
	w   io.Writer
	err error
}

func (p *printer) linef(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format+"\n", args...)
	}
}

func (p *printer) list(title string, items []string) {
	if len(items) == 0 {
		return
	}
	p.linef("%s", title)
	for _, item := range items {
		p.linef("  %s", item)
	}
}

func (p *printer) plan(plan *initialize.Plan) {
	p.linef("Dry run: nothing was applied or created, and no secret value was read.")
	p.linef("Team %s, declaration from %s.", plan.Team.Metadata.Name, plan.Origin)
	p.linef("Declaration:")
	raw, err := yaml.Marshal(plan.Team)
	if err != nil {
		p.err = err
		return
	}
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		p.linef("  %s", l)
	}
	p.linef("Sources:")
	for _, s := range plan.Sources {
		commit := s.Commit
		if commit == "" {
			commit = "unresolved"
		}
		p.linef("  %s  %s  %s  %s", s.Name, s.URL, s.Ref, commit)
	}
	p.linef("Pairs:")
	for _, pair := range plan.Pairs {
		p.linef("  %s × %s → config %s, image %s", pair.Role, pair.Harness, pair.Config, pair.Image)
		if len(pair.Components) > 0 {
			p.linef("    connectors and components: %s", strings.Join(pair.Components, ", "))
		}
		for _, task := range pair.Tasks {
			schedule := task.Schedule
			if schedule == "" {
				schedule = "no schedule"
			}
			if task.Suspend {
				schedule += " (suspended)"
			}
			p.linef("    task %s → config %s, %s", task.Name, task.Config, schedule)
		}
	}
	p.configs("Configs to create:", plan.Create)
	p.configs("Configs to re-render:", plan.Rerender)
	p.configs("Configs to prune:", plan.Prune)
	p.list("Secrets to create from their declared source:", plan.CreateSecrets)
	p.list("Missing secrets (cells will not start until they exist):", plan.MissingSecrets)
	p.list("Notes:", plan.Notes)
}

func (p *printer) configs(title string, names []string) {
	if len(names) == 0 {
		p.linef("%s none", title)
		return
	}
	p.linef("%s %s", title, strings.Join(names, ", "))
}
