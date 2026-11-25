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
	"strconv"
	"strings"
	"time"

	kukeconfig "github.com/eminwux/kukeon/cmd/config"
	getshared "github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

// The `kuke team task` verbs (issue #1668): switch the schedule of a role's
// task in a stored team on or off — for every harness of the role, or for
// the named ones only — and list the team's task Configs.

func newTaskCommand(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Switch a team task's schedule on or off, per harness",
		Long: "A team renders one task Config per role × harness. enable and disable switch a task's schedule " +
			"in the role's task entry of the stored team, for every harness of the role or, with --harness, " +
			"for the named harnesses only, and apply the team; list shows every task Config of the team.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newTaskSetCommand(deps, true), newTaskSetCommand(deps, false), newTaskListCommand(deps))
	return cmd
}

func newTaskSetCommand(deps Deps, enable bool) *cobra.Command {
	var conn connection
	var role, schedule string
	var harnesses []string
	var dryRun bool
	verb, short, use := "disable", "Switch a task's schedule off in a stored team", ""
	if enable {
		verb, short, use = "enable", "Switch a task's schedule on in a stored team", " [--schedule CRON]"
	}
	cmd := &cobra.Command{
		Use:   verb + " TEAM TASK --role ROLE [--harness HARNESS]..." + use,
		Short: short,
		Long: "Sets suspend: " + strconv.FormatBool(!enable) + " on TASK of ROLE in the stored team and " +
			"applies the team, exactly as kuke team apply -f of the edited declaration would. With --harness (repeatable) it sets " +
			"the task entry's harnesses.<harness> override and leaves the entry's own values alone; without, it " +
			"sets the entry's suspend and removes suspend from every override, so the switch covers every " +
			"harness. A role entry that omits tasks keeps every task selected. The task keeps its Config either " +
			"way and still runs with kuke run.\n--dry-run prints what would be applied and writes nothing. No " +
			"cells are created or started.",
		Args:              cobra.ExactArgs(2), //nolint:mnd // TEAM TASK
		ValidArgsFunction: completeTeamFirst,
		RunE: func(cmd *cobra.Command, args []string) error {
			change := initialize.TaskChange{
				Team: args[0], Task: args[1], Role: role, Harnesses: harnesses, Enable: enable, Schedule: schedule,
			}
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			out, err := initialize.SetTaskSchedule(cmd.Context(), s.options(dryRun), change)
			if err != nil {
				return err
			}
			return printTaskChange(cmd.OutOrStdout(), change, out)
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVar(&role, "role", "", "The role of the team whose task to switch (required)")
	_ = cmd.MarkFlagRequired("role")
	cmd.Flags().StringArrayVar(&harnesses, "harness", nil,
		"Switch the task on this harness of the team only (repeatable; default every harness)")
	if enable {
		cmd.Flags().StringVar(&schedule, "schedule", "",
			"Set the task's schedule at the same level: a five-field cron expression or @hourly/@daily/... (UTC)")
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be applied; write nothing")
	return cmd
}

// printTaskChange prints the switch, then what kuke team apply prints. A dry
// run prints the plan alone: nothing changed.
func printTaskChange(w io.Writer, change initialize.TaskChange, out initialize.Outcome) error {
	if out.Result == nil {
		return PrintOutcome(w, out)
	}
	state := "off"
	if change.Enable {
		state = "on"
	}
	on := ""
	if len(change.Harnesses) > 0 {
		on = " on harness " + strings.Join(change.Harnesses, ", ")
	}
	if _, err := fmt.Fprintf(w, "Schedule of task %s switched %s for role %s%s of team %s.\n",
		change.Task, state, change.Role, on, change.Team); err != nil {
		return err
	}
	return PrintOutcome(w, out)
}

func newTaskListCommand(deps Deps) *cobra.Command {
	var conn connection
	cmd := &cobra.Command{
		Use:   "list TEAM",
		Short: "List the task Configs of a stored team",
		Long: "One row per task Config of the team, read from the stored Configs with no fetch. STATE is on, " +
			"suspended, or manual for a task with no schedule (it runs with kuke run). NEXT is the next fire " +
			"time kukeond's scheduler reports. HARNESS is - on a Config rendered before the team's last apply " +
			"labelled it.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: kukeconfig.CompleteTeamNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			rows, err := initialize.TeamTasks(cmd.Context(), s.client, args[0])
			if err != nil {
				return err
			}
			table := make([][]string, 0, len(rows))
			for _, row := range rows {
				next := "-"
				if row.Next != nil && row.State == initialize.TaskOn {
					next = row.Next.UTC().Format(time.RFC3339)
				}
				table = append(table, []string{
					row.Role, row.Task, orDash(row.Harness), row.Config, orDash(row.Schedule), row.State, next,
				})
			}
			getshared.PrintTable(cmd,
				[]string{"ROLE", "TASK", "HARNESS", "CONFIG", "SCHEDULE", "STATE", "NEXT"}, table)
			return nil
		},
	}
	conn.register(cmd)
	return cmd
}

// completeTeamFirst completes TEAM from the stored teams.
func completeTeamFirst(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return kukeconfig.CompleteTeamNames(cmd, args, toComplete)
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}
