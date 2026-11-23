// Copyright 2025 Emiliano Spinella (eminwux)
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

// Package healthreport implements `kuke get healthreport(s)` (issue #1619).
package healthreport

import (
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	kukehealth "github.com/eminwux/kukeon/cmd/kuke/health"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/health"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewHealthReportCmd builds `kuke get healthreport(s) [name|latest]`. Without
// a name it lists the stored reports, newest first, exactly as `kuke health
// reports` does. With a name — or latest, the newest report — it shows one:
// a one-row table by default, the stored document with -o yaml or -o json.
// A report always lives in the kuke-system realm, so there are no scope flags.
func NewHealthReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "healthreport [name|latest]",
		Aliases:       []string{"healthreports", "hr"},
		Short:         "Get or list kind: HealthReport",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFormat, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			if len(args) > 0 {
				name := strings.TrimSpace(args[0])
				report, readErr := client.ReadHealthReport(cmd.Context(), name)
				if readErr != nil {
					return readErr
				}
				return printReport(cmd, &report, outputFormat)
			}

			reports, err := client.ListHealthReports(cmd.Context())
			if err != nil {
				return err
			}
			return printReports(cmd, reports, outputFormat)
		},
	}

	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printReport(cmd *cobra.Command, report *kukeonv1.HealthReport, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, report)
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, report)
	case shared.OutputFormatTable, shared.OutputFormatWide:
		// The single report as a one-row list table.
		return kukehealth.PrintReportList(cmd.OutOrStdout(),
			[]kukeonv1.HealthReportSummary{health.Summarize(report)})
	default:
		return shared.PrintYAML(cmd, report)
	}
}

// printReports prints the list rows. Like `kuke health reports`, a list is
// its summaries in every format; one report's document is `get <name>`.
func printReports(cmd *cobra.Command, reports []kukeonv1.HealthReportSummary, format shared.OutputFormat) error {
	if reports == nil {
		reports = []kukeonv1.HealthReportSummary{}
	}
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, reports)
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, reports)
	case shared.OutputFormatTable, shared.OutputFormatWide:
		return kukehealth.PrintReportList(cmd.OutOrStdout(), reports)
	default:
		return shared.PrintYAML(cmd, reports)
	}
}
