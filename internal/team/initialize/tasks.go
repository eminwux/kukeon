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
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// Per-harness task schedules (issue #1668): `kuke team task enable|disable`
// switches a task's schedule on or off in the task entry of a stored Team —
// for every harness of the role, or, with --harness, in the entry's
// harnesses.<harness> override only — and applies the team in one command.

// errNoSchedule is the composition refusal a switch meets on a task with no
// schedule to switch.
const errNoSchedule = "suspend is set but the task has no schedule"

// TaskChange is one `kuke team task enable|disable`.
type TaskChange struct {
	Team, Role, Task string
	// Harnesses names the harnesses whose override is switched; empty
	// switches the entry itself, for every harness.
	Harnesses []string
	// Enable switches the schedule on (suspend: false); else off.
	Enable bool
	// Schedule, on an enable, sets the schedule at the same level as the
	// switch; "" keeps the schedule.
	Schedule string
}

// command is the change as the CLI line that makes it, for rerun hints.
func (c TaskChange) command() string {
	verb := "disable"
	if c.Enable {
		verb = "enable"
	}
	parts := []string{"kuke team task", verb, c.Team, c.Task, "--role " + c.Role}
	for _, h := range c.Harnesses {
		parts = append(parts, "--harness "+h)
	}
	if c.Enable && c.Schedule != "" {
		parts = append(parts, fmt.Sprintf("--schedule %q", c.Schedule))
	}
	return strings.Join(parts, " ")
}

// SetTaskSchedule switches the schedule of one task of a role of a stored
// Team and applies the team, exactly as kuke team apply -f of the edited
// declaration would (AC5). With opts.DryRun it plans and writes nothing.
func SetTaskSchedule(ctx context.Context, opts Options, change TaskChange) (Outcome, error) {
	if err := opts.check(); err != nil {
		return Outcome{}, err
	}
	if change.Role == "" {
		return Outcome{}, fmt.Errorf("%w: name the role with --role", errdefs.ErrInvalidTeam)
	}
	if !change.Enable && change.Schedule != "" {
		return Outcome{}, fmt.Errorf("%w: --schedule is for enable only", errdefs.ErrInvalidTeam)
	}
	stored, err := StoredTeam(ctx, opts.Client, change.Team)
	if err != nil {
		return Outcome{}, err
	}
	if stored == nil {
		return Outcome{}, notStored(change.Team, config.DefaultRealm)
	}
	entry, err := roleEntry(stored, change.Role)
	if err != nil {
		return Outcome{}, err
	}
	defaults, _, err := readDefaults(ctx, opts.Client, stored.Metadata.Realm)
	if err != nil {
		return Outcome{}, err
	}
	req := request{
		command: change.command(), origin: Origin{Kind: OriginStored}, defaults: defaults,
		have: injectedSources(opts.Sources),
	}
	if entry.Tasks == nil {
		// An omitted `tasks` selects every task; listing one entry would
		// deselect the rest, so list every task the role renders first.
		before, cloneErr := config.Declaration(stored)
		if cloneErr != nil {
			return Outcome{}, cloneErr
		}
		unchanged := req
		unchanged.team = before
		p, planErr := plan(ctx, opts.Client, unchanged)
		if planErr != nil {
			return Outcome{}, withRerun(planErr, req.command)
		}
		tasks := []config.TaskSelection{}
		for _, name := range renderedTasks(p, change.Role) {
			tasks = append(tasks, config.TaskSelection{Name: name})
		}
		entry.Tasks = &tasks
	}
	if err = editTaskEntry(stored, entry, change); err != nil {
		return Outcome{}, err
	}
	req.team = stored
	p, err := plan(ctx, opts.Client, req)
	if err != nil {
		if change.Enable && change.Schedule == "" && strings.Contains(err.Error(), errNoSchedule) {
			return Outcome{}, fmt.Errorf("%w; give one with --schedule", err)
		}
		return Outcome{}, withRerun(err, req.command)
	}
	if err = checkTaskHarnesses(p, change); err != nil {
		return Outcome{}, err
	}
	if opts.DryRun {
		return Outcome{Plan: p}, nil
	}
	if err = ctx.Err(); err != nil {
		return Outcome{Plan: p}, err
	}
	result, err := commit(ctx, opts.Client, p)
	if err != nil {
		return Outcome{Plan: p}, withRerun(err, req.command)
	}
	return Outcome{Plan: p, Result: &result}, nil
}

// renderedTasks are the names of the tasks the role's pairs render, the
// union over its pairs in first-seen order.
func renderedTasks(p *Plan, role string) []string {
	var out []string
	for _, pair := range p.Pairs {
		if pair.Role != role {
			continue
		}
		for _, task := range pair.Tasks {
			if !slices.Contains(out, task.Name) {
				out = append(out, task.Name)
			}
		}
	}
	return out
}

// editTaskEntry applies change to the role entry's task entry (AC5). The
// entry must list the task: selecting a task is not this command's job (D4).
func editTaskEntry(team *config.Team, entry *config.Role, change TaskChange) error {
	tasks := *entry.Tasks
	i := slices.IndexFunc(tasks, func(t config.TaskSelection) bool { return t.Name == change.Task })
	if i < 0 {
		names := make([]string, 0, len(tasks))
		for _, task := range tasks {
			names = append(names, task.Name)
		}
		listed := strings.Join(names, ", ")
		if listed == "" {
			listed = "none"
		}
		return fmt.Errorf("%w: role %s of team %s renders no task %q; its tasks: %s",
			errdefs.ErrInvalidTeam, change.Role, team.Metadata.Name, change.Task, listed)
	}
	suspend := !change.Enable
	task := &tasks[i]
	if len(change.Harnesses) == 0 {
		task.Suspend = &suspend
		if change.Schedule != "" {
			task.Schedule = change.Schedule
		}
		for name, override := range task.Harnesses {
			override.Suspend = nil
			if override.Schedule == "" {
				delete(task.Harnesses, name)
				continue
			}
			task.Harnesses[name] = override
		}
		if len(task.Harnesses) == 0 {
			task.Harnesses = nil
		}
		return nil
	}
	if task.Harnesses == nil {
		task.Harnesses = map[string]config.TaskHarness{}
	}
	for _, name := range change.Harnesses {
		override := task.Harnesses[name]
		value := suspend
		override.Suspend = &value
		if change.Schedule != "" {
			override.Schedule = change.Schedule
		}
		task.Harnesses[name] = override
	}
	return nil
}

// checkTaskHarnesses fails when a named harness renders no Config of the task:
// composition refuses a key that is no harness of the team, but a harness the
// role does not pair with would otherwise be stored and switch nothing.
func checkTaskHarnesses(p *Plan, change TaskChange) error {
	var have []string
	for _, pair := range p.Pairs {
		if pair.Role != change.Role {
			continue
		}
		if slices.ContainsFunc(pair.Tasks, func(t PlannedTask) bool { return t.Name == change.Task }) {
			have = append(have, pair.Harness)
		}
	}
	for _, name := range change.Harnesses {
		if !slices.Contains(have, name) {
			return fmt.Errorf("%w: role %s of team %s renders task %s on no harness %q; its harnesses: %s",
				errdefs.ErrInvalidTeam, change.Role, change.Team, change.Task, name,
				strings.Join(have, ", "))
		}
	}
	return nil
}

// Task states of TeamTask.
const (
	TaskOn        = "on"
	TaskSuspended = "suspended"
	// TaskManual is a task Config with no schedule: it runs with kuke run.
	TaskManual = "manual"
)

// TeamTask is one task Config of a team (AC6): the row of kuke team task
// list.
type TeamTask struct {
	Role string
	Task string
	// Harness is the Config's kukeon.io/harness label; empty on a Config
	// rendered before the label existed (D7).
	Harness  string
	Config   string
	Schedule string
	State    string
	Next     *time.Time
}

// TeamTasks lists the task Configs of a stored team, from the stored Configs
// with no fetch, sorted by role, task and harness.
func TeamTasks(ctx context.Context, client Client, team string) ([]TeamTask, error) {
	stored, err := StoredTeam(ctx, client, team)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, notStored(team, config.DefaultRealm)
	}
	configs, err := client.ListConfigs(ctx, "", "", "")
	if err != nil {
		return nil, requestError("lookup", "configs", err)
	}
	return TeamTasksOf(team, configs), nil
}

// TeamTasksOf builds the rows of TeamTasks from configs (any Configs: only
// the task Configs of team are read).
func TeamTasksOf(team string, configs []api.CellConfigDoc) []TeamTask {
	out := []TeamTask{}
	for _, doc := range configs {
		labels := doc.Metadata.Labels
		if labels[api.LabelTeam] != team || labels[api.LabelTask] == "" {
			continue
		}
		row := TeamTask{
			Role: labels[api.LabelRole], Task: labels[api.LabelTask], Harness: labels[api.LabelHarness],
			Config: doc.Metadata.Name, State: TaskManual,
		}
		if s := doc.Spec.Schedule; s != nil {
			row.Schedule, row.State = s.Cron, TaskOn
			if s.Suspend {
				row.State = TaskSuspended
			}
		}
		if doc.Status != nil && doc.Status.Schedule != nil && row.State == TaskOn {
			row.Next = doc.Status.Schedule.NextScheduleTime
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Task != b.Task {
			return a.Task < b.Task
		}
		if a.Harness != b.Harness {
			return a.Harness < b.Harness
		}
		return a.Config < b.Config
	})
	return out
}
