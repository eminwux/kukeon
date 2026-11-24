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

package compose

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/cronspec"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// headlessMode is the harness mode every task config is pinned to.
const headlessMode = "headless"

// selectTasks validates the role's declared tasks and returns the ones the
// team's role entry selects, in the role's declaration order, each with its
// schedule resolved.
//
// A task's name is a resource-name segment unique within the role. On a model
// harness its skill must be the base name of one of the role's spec.skills
// entries; on the script harness it names no skill, since the cell runs the
// role's command with the task's args appended. The entry's
// `tasks` selection selects every task when omitted, none when empty, and
// fails on a name the role does not declare.
//
// A task's cadence is a deployment decision (issue #1503): the role declares a
// default `schedule` and `suspend`, and each value the team's task entry sets
// overrides the corresponding default. The returned tasks carry the resolved
// values in their Schedule and Suspend fields.
func selectTasks(role *model.Role, selection config.Role, harnessName string) ([]model.RoleTask, error) {
	skills := map[string]bool{}
	for _, skill := range role.Spec.Skills {
		skills[path.Base(path.Clean(skill))] = true
	}
	declared := map[string]bool{}
	for i, task := range role.Spec.Tasks {
		if !config.ValidName(task.Name) {
			return nil, fmt.Errorf("role %s: spec.tasks[%d]: invalid task name %q", role.Metadata.Name, i, task.Name)
		}
		if declared[task.Name] {
			return nil, fmt.Errorf("role %s: spec.tasks[%d]: duplicate task name %q", role.Metadata.Name, i, task.Name)
		}
		declared[task.Name] = true
		switch {
		case harnessName == scriptHarness && task.Skill != "":
			return nil, fmt.Errorf(
				"role %s: task %s: the script harness runs the role's command; a task names no skill",
				role.Metadata.Name, task.Name,
			)
		case harnessName != scriptHarness && !skills[task.Skill]:
			return nil, fmt.Errorf(
				"role %s: task %s: skill %q is not one of the role's spec.skills",
				role.Metadata.Name, task.Name, task.Skill,
			)
		}
		if task.Schedule != "" {
			if _, err := cronspec.Parse(task.Schedule); err != nil {
				return nil, fmt.Errorf("role %s: task %s: schedule: %w", role.Metadata.Name, task.Name, err)
			}
		}
	}
	if selection.Tasks != nil {
		for _, entry := range *selection.Tasks {
			if !declared[entry.Name] {
				return nil, fmt.Errorf("roles %s: tasks: role %s declares no task %q",
					selection.Name, role.Metadata.Name, entry.Name)
			}
		}
	}
	var out []model.RoleTask
	for _, task := range role.Spec.Tasks {
		if !selection.SelectsTask(task.Name) {
			continue
		}
		resolved, err := resolveTaskSchedule(role.Metadata.Name, selection, task, harnessName)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// resolveTaskSchedule applies the team's task entry over the role's defaults,
// and the entry's override for the harness over the entry (issue #1668): a
// team `args` replaces the role's argument text, a team `schedule` replaces
// the role's, a team `suspend` — true or false — replaces the role's, and a
// value in `harnesses.<harness>` replaces the entry's for that harness's
// Config. A `suspend` on a task that ends up with no schedule is refused:
// there is nothing for it to switch.
func resolveTaskSchedule(
	roleName string, selection config.Role, task model.RoleTask, harnessName string,
) (model.RoleTask, error) {
	declared, _ := selection.TaskSelection(task.Name)
	override, overridden := declared.Harnesses[harnessName]
	entry := declared.ForHarness(harnessName)
	// at names the harness in an error once the entry overrides per harness:
	// the pairs of the role then resolve differently.
	at := ""
	if len(declared.Harnesses) > 0 {
		at = "harness " + harnessName + ": "
	}
	suspendSet := task.Suspend
	if entry.Args != "" {
		task.Args = entry.Args
	}
	if entry.Schedule != "" {
		if _, err := cronspec.Parse(entry.Schedule); err != nil {
			field := "schedule"
			if overridden && override.Schedule != "" {
				field = "harnesses: " + harnessName + ": schedule"
			}
			return task, fmt.Errorf("roles %s: tasks: %s: %s: %w", selection.Name, task.Name, field, err)
		}
		task.Schedule = entry.Schedule
	}
	if entry.Suspend != nil {
		task.Suspend = *entry.Suspend
		suspendSet = true
	}
	if suspendSet && task.Schedule == "" {
		return task, fmt.Errorf(
			"roles %s: tasks: %s: %ssuspend is set but the task has no schedule (role %s declares none)",
			selection.Name, task.Name, at, roleName,
		)
	}
	return task, nil
}

// checkTaskHarnesses refuses a task entry override keyed by a name that is
// not a harness of the team, or — on a role pinned to one harness — by any
// other harness (issue #1668). It runs against the effective harness list, as
// a team may inherit it from the TeamDefaults.
func checkTaskHarnesses(role config.Role, harnesses []config.Harness) error {
	if role.Tasks == nil {
		return nil
	}
	names := make([]string, 0, len(harnesses))
	known := map[string]bool{}
	for _, h := range harnesses {
		names = append(names, h.Name)
		known[h.Name] = true
	}
	for _, entry := range *role.Tasks {
		for _, name := range slices.Sorted(maps.Keys(entry.Harnesses)) {
			switch {
			case !known[name]:
				return fmt.Errorf("roles %s: tasks: %s: harnesses: the team has no harness %q; its harnesses: %s",
					role.Name, entry.Name, name, strings.Join(names, ", "))
			case role.Harness != "" && name != role.Harness:
				return fmt.Errorf("roles %s: tasks: %s: harnesses: %s: role is pinned to harness %q",
					role.Name, entry.Name, name, role.Harness)
			}
		}
	}
	return nil
}

// taskPrompt is the harness-neutral prompt a task config runs headless with.
func taskPrompt(task model.RoleTask) string {
	prompt := "Use the " + task.Skill + " skill"
	if task.Args != "" {
		prompt += " with arguments: " + task.Args
	}
	return prompt
}

// bindTasks renders one headless-only, self-deleting CellConfig per selected
// task of the pair. Each carries the pair's blueprint ref, values, repos,
// secrets, components and requires, plus the task's PROMPT (on the script
// harness, the task's ARGS instead) — and, for a task
// with a resolved schedule, the spec.schedule kukeond fires it on (issue
// #1503). A harness without a headless mode gets no task configs; the returned
// note says so.
func bindTasks(plan *pairPlan, base *api.CellConfigDoc) ([]*api.CellConfigDoc, string) {
	if len(plan.tasks) == 0 {
		return nil, ""
	}
	if _, ok := plan.definition.Spec.Modes[headlessMode]; !ok {
		return nil, fmt.Sprintf(
			"role %s: harness %s declares no %s mode; skipped its task configs",
			plan.selection.Name, plan.hsel.Name, headlessMode,
		)
	}
	out := make([]*api.CellConfigDoc, 0, len(plan.tasks))
	for _, task := range plan.tasks {
		cc := &api.CellConfigDoc{
			APIVersion: base.APIVersion, Kind: base.Kind,
			Metadata: base.Metadata,
			Spec: api.CellConfigSpec{
				Blueprint:  base.Spec.Blueprint,
				Values:     maps.Clone(base.Spec.Values),
				Repos:      maps.Clone(base.Spec.Repos),
				Secrets:    maps.Clone(base.Spec.Secrets),
				Requires:   slices.Clone(base.Spec.Requires),
				Components: slices.Clone(base.Spec.Components),
				Modes:      &api.CellConfigModes{Enabled: []string{headlessMode}, Default: headlessMode},
				AutoDelete: true,
			},
		}
		if base.Spec.Git != nil {
			git := *base.Spec.Git
			cc.Spec.Git = &git
		}
		cc.Metadata.Name = resourceName(base.Metadata.Name, task.Name)
		cc.Metadata.Labels = maps.Clone(base.Metadata.Labels)
		cc.Metadata.Labels[api.LabelTask] = task.Name
		if cc.Spec.Values == nil {
			cc.Spec.Values = map[string]string{}
		}
		switch {
		case plan.hsel.Name != scriptHarness:
			cc.Spec.Values["PROMPT"] = taskPrompt(task)
		case task.Args != "":
			cc.Spec.Values["ARGS"] = task.Args
		}
		if task.Schedule != "" {
			cc.Spec.Schedule = &api.CellConfigSchedule{Cron: task.Schedule, Suspend: task.Suspend}
		}
		out = append(out, cc)
	}
	return out, ""
}
