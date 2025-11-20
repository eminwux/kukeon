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

package shared

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	createshared "github.com/eminwux/kukeon/cmd/kuke/create/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// OutputFormat represents the output format type.
type OutputFormat string

const (
	OutputFormatYAML  OutputFormat = "yaml"
	OutputFormatJSON  OutputFormat = "json"
	OutputFormatTable OutputFormat = "table"
)

// ParseOutputFormat parses and validates the --output flag from the command.
func ParseOutputFormat(cmd *cobra.Command) (OutputFormat, error) {
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return OutputFormatTable, err
	}
	if output == "" {
		output, _ = cmd.Flags().GetString("o")
	}
	if output == "" {
		return OutputFormatTable, nil
	}

	format := OutputFormat(strings.ToLower(strings.TrimSpace(output)))
	switch format {
	case OutputFormatYAML, OutputFormatJSON, OutputFormatTable:
		return format, nil
	default:
		return OutputFormatTable, fmt.Errorf("invalid output format: %s (supported: yaml, json, table)", output)
	}
}

// PrintYAML prints the resource as YAML.
func PrintYAML(doc interface{}) error {
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(doc)
}

// PrintJSON prints the resource as JSON.
func PrintJSON(doc interface{}) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(doc)
}

// PrintTable prints resources in a table format.
func PrintTable(cmd *cobra.Command, headers []string, rows [][]string) {
	if len(rows) == 0 {
		cmd.Println("No resources found.")
		return
	}

	// Calculate column widths
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print header
	headerRow := ""
	var headerRowSb100 strings.Builder
	for i, h := range headers {
		if i > 0 {
			headerRowSb100.WriteString("  ")
		}
		headerRowSb100.WriteString(fmt.Sprintf("%-*s", widths[i], h))
	}
	headerRow += headerRowSb100.String()
	cmd.Println(headerRow)

	// Print separator
	separator := ""
	var separatorSb110 strings.Builder
	for i, w := range widths {
		if i > 0 {
			separatorSb110.WriteString("  ")
		}
		separatorSb110.WriteString(strings.Repeat("-", w))
	}
	separator += separatorSb110.String()
	cmd.Println(separator)

	// Print rows
	for _, row := range rows {
		rowStr := ""
		var rowStrSb121 strings.Builder
		for i, cell := range row {
			if i < len(widths) {
				if i > 0 {
					rowStrSb121.WriteString("  ")
				}
				rowStrSb121.WriteString(fmt.Sprintf("%-*s", widths[i], cell))
			}
		}
		rowStr += rowStrSb121.String()
		cmd.Println(rowStr)
	}
}

// ControllerFromCmd reuses the controller helper from create/shared.
func ControllerFromCmd(cmd *cobra.Command) (*controller.Exec, error) {
	return createshared.ControllerFromCmd(cmd)
}

// LoggerFromCmd reuses the logger helper from create/shared.
func LoggerFromCmd(cmd *cobra.Command) (*slog.Logger, error) {
	return createshared.LoggerFromCmd(cmd)
}
