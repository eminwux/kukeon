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

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// headlessMode is the harness mode every task config is pinned to.
const headlessMode = "headless"

// selectTasks validates the role's declared tasks and returns the ones the
// team's role entry selects, in the role's declaration order.
//
// A task's name is a resource-name segment unique within the role; its skill
// must be the base name of one of the role's spec.skills entries. The entry's
// `tasks` selection selects every task when omitted, none when empty, and
// fails on a name the role does not declare.
func selectTasks(role *model.Role, selection config.Role) ([]model.RoleTask, error) {
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
		if !skills[task.Skill] {
			return nil, fmt.Errorf(
				"role %s: task %s: skill %q is not one of the role's spec.skills",
				role.Metadata.Name, task.Name, task.Skill,
			)
		}
	}
	if selection.Tasks != nil {
		for _, name := range *selection.Tasks {
			if !declared[name] {
				return nil, fmt.Errorf("roles %s: tasks: role %s declares no task %q",
					selection.Name, role.Metadata.Name, name)
			}
		}
	}
	var out []model.RoleTask
	for _, task := range role.Spec.Tasks {
		if selection.SelectsTask(task.Name) {
			out = append(out, task)
		}
	}
	return out, nil
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
// secrets, components and requires, plus the task's PROMPT. A harness without
// a headless mode gets no task configs; the returned note says so.
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
		cc.Metadata.Name = resourceName(base.Metadata.Name, task.Name)
		cc.Metadata.Labels = maps.Clone(base.Metadata.Labels)
		cc.Metadata.Labels[api.LabelTask] = task.Name
		if cc.Spec.Values == nil {
			cc.Spec.Values = map[string]string{}
		}
		cc.Spec.Values["PROMPT"] = taskPrompt(task)
		out = append(out, cc)
	}
	return out, ""
}
