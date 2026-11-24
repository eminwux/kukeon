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

// Package teamdefaults implements `kuke get teamdefaults` (issue #1618).
package teamdefaults

import (
	"bytes"
	"fmt"
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

// NewTeamDefaultsCmd builds `kuke get teamdefaults`: the realm's registered
// sources and default roles and harnesses; `-o yaml` prints the stored
// document.
func NewTeamDefaultsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "teamdefaults",
		Aliases:      []string{"team-defaults"},
		Short:        "Get kind: TeamDefaults, the settings every team of a realm inherits",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}
			realm, _ := cmd.Flags().GetString("realm")
			if realm = strings.TrimSpace(realm); realm == "" {
				realm = teamconfig.DefaultRealm
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			got, err := client.GetTeamDefaults(cmd.Context(), realm)
			if err != nil {
				return err
			}
			if !got.Exists {
				if format == shared.OutputFormatYAML || format == shared.OutputFormatJSON {
					return fmt.Errorf("realm %q stores no TeamDefaults; kuke team init creates one", realm)
				}
				cmd.Printf("Realm %s stores no TeamDefaults; kuke team init creates one.\n", realm)
				return nil
			}
			if format == shared.OutputFormatYAML {
				_, err = cmd.OutOrStdout().Write(got.Document)
				return err
			}
			defaults, err := teamconfig.DecodeStoredDefaults(bytes.NewReader(got.Document))
			if err != nil {
				return fmt.Errorf("stored TeamDefaults: %w", err)
			}
			if format == shared.OutputFormatJSON {
				return shared.PrintJSON(cmd, summarize(defaults))
			}
			printDefaults(cmd, realm, defaults)
			return nil
		},
	}
	cmd.Flags().String("realm", "", "Realm whose TeamDefaults to show (default: default)")
	cmd.Flags().StringP("output", "o", "", "Output format (yaml, json, table, wide)")
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

// summary is the JSON form: a summary, never the declaration types, whose
// omitted-versus-empty distinction only YAML keeps. The sources roles name
// are stored Sources (#1643; kuke get sources).
type summary struct {
	Roles     []string `json:"roles"`
	Harnesses []string `json:"harnesses"`
}

func summarize(d *teamconfig.TeamDefaults) summary {
	out := summary{Roles: []string{}, Harnesses: []string{}}
	for _, r := range d.Spec.Defaults.Roles {
		out.Roles = append(out.Roles, r.Name+"="+r.Source+":"+r.Path)
	}
	for _, h := range d.Spec.Defaults.Harnesses {
		out.Harnesses = append(out.Harnesses, h.Name+"="+h.Source+":"+h.Path)
	}
	return out
}

func printDefaults(cmd *cobra.Command, realm string, d *teamconfig.TeamDefaults) {
	s := summarize(d)
	cmd.Printf("TeamDefaults of realm %s\n\n", realm)
	cmd.Printf("Default roles:     %s\n", list(s.Roles))
	cmd.Printf("Default harnesses: %s\n", list(s.Harnesses))
	cmd.Println("Sources: kuke get sources")
}

func list(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
