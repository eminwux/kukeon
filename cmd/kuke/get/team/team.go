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

// Package team implements `kuke get team(s)` (issue #1618).
package team

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	teamconfig "github.com/eminwux/kukeon/pkg/team/config"
	"github.com/spf13/cobra"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewTeamCmd builds `kuke get team [NAME]`. Without a name it lists the
// stored teams with their realm, project, roles and number of Configs; with
// one it shows the team's roles, harnesses and Configs, and `-o yaml` prints
// the stored document.
func NewTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "team [name]",
		Aliases:      []string{"teams"},
		Short:        "Get or list kind: Team",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}
			realm, _ := cmd.Flags().GetString("realm")
			realm = strings.TrimSpace(realm)
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			if len(args) == 0 {
				teams, listErr := client.ListTeams(cmd.Context(), realm)
				if listErr != nil {
					return listErr
				}
				return printTeams(cmd, teams, format)
			}
			name := strings.TrimSpace(args[0])
			if realm == "" {
				realm = teamconfig.DefaultRealm
			}
			got, err := client.GetTeam(cmd.Context(), kukeonv1.TeamRef{Name: name, Realm: realm})
			if err != nil {
				return err
			}
			if !got.Exists {
				return fmt.Errorf("team %q not found in realm %q; list the teams with kuke get teams", name, realm)
			}
			return printTeam(cmd, got, format)
		},
	}
	cmd.Flags().String("realm", "", "Realm of the teams (default: every realm; default for a named team)")
	cmd.Flags().StringP("output", "o", "", "Output format (yaml, json, table, wide)")
	cmd.ValidArgsFunction = config.CompleteTeamNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printTeams(cmd *cobra.Command, teams []kukeonv1.GetTeamResult, format shared.OutputFormat) error {
	switch format { //nolint:exhaustive // table and wide are the default
	case shared.OutputFormatYAML:
		return printDocuments(cmd, teams)
	case shared.OutputFormatJSON:
		summaries := make([]kukeonv1.TeamSummary, 0, len(teams))
		for _, t := range teams {
			summaries = append(summaries, t.Summary)
		}
		return shared.PrintJSON(cmd, summaries)
	default:
		if len(teams) == 0 {
			cmd.Println("No teams found.")
			return nil
		}
		headers := []string{"NAME", "REALM", "PROJECT", "REF", "ROLES", "CONFIGS"}
		rows := make([][]string, 0, len(teams))
		for _, t := range teams {
			s := t.Summary
			rows = append(rows, []string{
				s.Name, s.Realm, dash(s.ProjectURL), dash(s.ProjectRef),
				selection(s.Roles, s.RolesInherited), strconv.Itoa(len(s.Configs)),
			})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	}
}

func printTeam(cmd *cobra.Command, got kukeonv1.GetTeamResult, format shared.OutputFormat) error {
	switch format { //nolint:exhaustive // table and wide are the default
	case shared.OutputFormatYAML:
		_, err := cmd.OutOrStdout().Write(got.Document)
		return err
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, got.Summary)
	default:
		s := got.Summary
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Team:      %s\n", s.Name)
		fmt.Fprintf(out, "Realm:     %s\n", s.Realm)
		if s.ProjectURL != "" {
			fmt.Fprintf(out, "Project:   %s (%s)\n", s.ProjectURL, s.ProjectRef)
		}
		fmt.Fprintf(out, "Roles:     %s\n", selection(s.Roles, s.RolesInherited))
		fmt.Fprintf(out, "Harnesses: %s\n", selection(s.Harnesses, s.HarnessesInherited))
		if len(s.Configs) == 0 {
			fmt.Fprintln(out, "Configs:   none")
			return nil
		}
		fmt.Fprintln(out, "Configs:")
		for _, c := range s.Configs {
			fmt.Fprintf(out, "  %s\n", c)
		}
		return nil
	}
}

// printDocuments prints each stored document as one YAML document.
func printDocuments(cmd *cobra.Command, teams []kukeonv1.GetTeamResult) error {
	var buf bytes.Buffer
	for i, t := range teams {
		if i > 0 {
			buf.WriteString("---\n")
		}
		buf.Write(t.Document)
	}
	_, err := cmd.OutOrStdout().Write(buf.Bytes())
	return err
}

// selection renders a role or harness list, or that the list is inherited.
func selection(names []string, inherited bool) string {
	if inherited {
		return "(defaults)"
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
