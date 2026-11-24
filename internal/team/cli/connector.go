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

	kukeconfig "github.com/eminwux/kukeon/cmd/config"
	getshared "github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
)

// The `kuke team connector` verbs (issue #1649): give a role of a stored team
// a connector the role does not ask for itself, take it away, and list what
// each Config of the team resolves.

func newConnectorCommand(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connector",
		Short: "Give a team's roles a connector, or take it away",
		Long: "A role's cells get a connector the role does not ask for itself (GitHub access for majordomo's " +
			"health sweep, for example) through the role's entry in the stored team. enable and disable edit " +
			"that entry and apply the team; list shows what each Config of the team resolves.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newConnectorSetCommand(deps, true), newConnectorSetCommand(deps, false),
		newConnectorListCommand(deps))
	return cmd
}

func newConnectorSetCommand(deps Deps, enable bool) *cobra.Command {
	var conn connection
	var roles []string
	var identity string
	var dryRun bool
	verb, short, long := "disable", "Take a connector away from roles of a stored team",
		"Removes the role entry's connectors entry for CONNECTOR and every needs.secrets name that selects it "+
			"(the older spelling), then applies the team. If a Config of the role would still render the "+
			"connector because the role's definition or the team's harness entry asks for it, nothing is written."
	if enable {
		verb, short, long = "enable", "Give roles of a stored team a connector",
			"Adds CONNECTOR to each role's entry in the stored team (spec.roles[ROLE].connectors) and applies "+
				"the team, exactly as kuke team apply -f of the edited declaration would. Without --identity a "+
				"new entry names no identity, so it resolves through the Stack, Space and Realm defaults; "+
				"--identity sets the entry's identity and --identity \"\" removes it."
	}
	cmd := &cobra.Command{
		Use:   verb + " TEAM CONNECTOR --role ROLE...",
		Short: short,
		Long: long + "\n--dry-run prints what would be applied and writes nothing. No cells are created or " +
			"started; a running cell keeps what it started with until kuke restart renders it again.",
		Args:              cobra.ExactArgs(2), //nolint:mnd // TEAM CONNECTOR
		ValidArgsFunction: completeTeamConnector,
		RunE: func(cmd *cobra.Command, args []string) error {
			change := initialize.ConnectorChange{Team: args[0], Connector: args[1], Roles: roles, Enable: enable}
			if enable && cmd.Flags().Changed("identity") {
				change.Identity = &identity
			}
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			out, err := initialize.SetRoleConnector(cmd.Context(), s.options(dryRun), change)
			if err != nil {
				return err
			}
			return printConnectorChange(cmd.OutOrStdout(), change, out)
		},
	}
	conn.register(cmd)
	cmd.Flags().StringArrayVar(&roles, "role", nil, "A role of the team (repeatable; required)")
	_ = cmd.MarkFlagRequired("role")
	if enable {
		cmd.Flags().StringVar(&identity, "identity", "",
			"The stored identity that fills the connector: name[@realm[/space[/stack]]]; \"\" removes it")
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be applied; write nothing")
	return cmd
}

// printConnectorChange is AC4: the change, what kuke team apply prints, and
// the running-cells line. A dry run prints the plan alone: nothing changed.
func printConnectorChange(w io.Writer, change initialize.ConnectorChange, out initialize.Outcome) error {
	if out.Result == nil {
		return PrintOutcome(w, out)
	}
	verb := "disabled"
	if change.Enable {
		verb = "enabled"
	}
	noun := "role"
	if len(change.Roles) > 1 {
		noun = "roles"
	}
	if _, err := fmt.Fprintf(w, "Connector %s %s for %s %s of team %s.\n",
		change.Connector, verb, noun, strings.Join(change.Roles, ", "), change.Team); err != nil {
		return err
	}
	if err := PrintOutcome(w, out); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "A running cell keeps what it started with; kuke restart <cell> renders it again.")
	return err
}

func newConnectorListCommand(deps Deps) *cobra.Command {
	var conn connection
	cmd := &cobra.Command{
		Use:   "list TEAM",
		Short: "List the connectors each Config of a stored team resolves",
		Long: "One row per Config of the team and connector it resolves, read from the stored team and the " +
			"Configs' status, with no fetch. SET BY is team when the team's role entry enables the connector " +
			"and definition when the role's definition or its harness asks for it. MISSING is a required " +
			"connector no identity fills yet.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: kukeconfig.CompleteTeamNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			rows, err := initialize.RoleConnectors(cmd.Context(), s.client, args[0])
			if err != nil {
				return err
			}
			table := make([][]string, 0, len(rows))
			for _, row := range rows {
				setBy := "definition"
				if row.SetByTeam {
					setBy = "team"
				}
				table = append(
					table,
					[]string{row.Role, row.Config, row.Connector, orDash(row.Method), row.Fill, setBy},
				)
			}
			getshared.PrintTable(cmd, []string{"ROLE", "CONFIG", "CONNECTOR", "METHOD", "IDENTITY", "SET BY"}, table)
			return nil
		},
	}
	conn.register(cmd)
	return cmd
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// completeTeamConnector completes TEAM from the stored teams, then
// CONNECTOR from the connectors a team can give a role.
func completeTeamConnector(
	cmd *cobra.Command, args []string, toComplete string,
) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return kukeconfig.CompleteTeamNames(cmd, args, toComplete)
	case 1:
		catalog, err := connector.Default()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, conn := range initialize.TogglableConnectors(catalog) {
			if strings.HasPrefix(conn.Metadata.Name, toComplete) {
				names = append(names, conn.Metadata.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}
