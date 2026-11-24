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

// Package source implements `kuke get source(s)` (issue #1643).
package source

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

// NewSourceCmd builds `kuke get source [NAME]`. Without a name it lists the
// stored Sources with their address, ref and git identity; with one it shows
// that Source, and `-o yaml` prints the stored documents.
func NewSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "source [name]",
		Aliases:      []string{"sources"},
		Short:        "Get or list kind: Source",
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
				sources, listErr := client.ListSources(cmd.Context(), realm)
				if listErr != nil {
					return listErr
				}
				return printSources(cmd, sources, format)
			}
			name := strings.TrimSpace(args[0])
			if realm == "" {
				realm = teamconfig.DefaultRealm
			}
			got, err := client.GetSource(cmd.Context(), kukeonv1.SourceRef{Name: name, Realm: realm})
			if err != nil {
				return err
			}
			if !got.Exists {
				return fmt.Errorf("source %q not found in realm %q; list the sources with kuke get sources",
					name, realm)
			}
			return printSources(cmd, []kukeonv1.GetSourceResult{got}, format)
		},
	}
	cmd.Flags().String("realm", "", "Realm of the sources (default: every realm; default for a named source)")
	cmd.Flags().StringP("output", "o", "", "Output format (yaml, json, table, wide)")
	cmd.ValidArgsFunction = config.CompleteSourceNames
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

func printSources(cmd *cobra.Command, sources []kukeonv1.GetSourceResult, format shared.OutputFormat) error {
	switch format { //nolint:exhaustive // table and wide are the default
	case shared.OutputFormatYAML:
		var buf bytes.Buffer
		for i, s := range sources {
			if i > 0 {
				buf.WriteString("---\n")
			}
			buf.Write(s.Document)
		}
		_, err := cmd.OutOrStdout().Write(buf.Bytes())
		return err
	case shared.OutputFormatJSON:
		summaries := make([]kukeonv1.SourceSummary, 0, len(sources))
		for _, s := range sources {
			summaries = append(summaries, s.Summary)
		}
		return shared.PrintJSON(cmd, summaries)
	default:
		if len(sources) == 0 {
			cmd.Println("No sources found.")
			return nil
		}
		headers := []string{"NAME", "ADDRESS", "REF", "IDENTITY"}
		rows := make([][]string, 0, len(sources))
		for _, s := range sources {
			rows = append(rows, []string{
				s.Summary.Name, dash(s.Summary.URL), dash(s.Summary.Ref),
				dash(s.Summary.Identity),
			})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
