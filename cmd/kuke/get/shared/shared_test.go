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

package shared_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func TestParseOutputFormat(t *testing.T) {
	tests := []struct {
		name       string
		flags      map[string]string
		wantFormat shared.OutputFormat
		wantErr    string
	}{
		{
			name:       "yaml format",
			flags:      map[string]string{"output": "yaml"},
			wantFormat: shared.OutputFormatYAML,
		},
		{
			name:       "json format",
			flags:      map[string]string{"output": "json"},
			wantFormat: shared.OutputFormatJSON,
		},
		{
			name:       "table format",
			flags:      map[string]string{"output": "table"},
			wantFormat: shared.OutputFormatTable,
		},
		{
			name:       "default format when no flag",
			flags:      map[string]string{},
			wantFormat: shared.OutputFormatTable,
		},
		{
			name:       "uppercase format converted to lowercase",
			flags:      map[string]string{"output": "YAML"},
			wantFormat: shared.OutputFormatYAML,
		},
		{
			name:       "format with whitespace trimmed",
			flags:      map[string]string{"output": "  json  "},
			wantFormat: shared.OutputFormatJSON,
		},
		{
			name:       "short flag -o",
			flags:      map[string]string{"o": "yaml"},
			wantFormat: shared.OutputFormatYAML,
		},
		{
			name:       "output flag takes precedence over -o",
			flags:      map[string]string{"output": "json", "o": "yaml"},
			wantFormat: shared.OutputFormatJSON,
		},
		{
			name:    "invalid format",
			flags:   map[string]string{"output": "xml"},
			wantErr: "invalid output format",
		},
		{
			name:       "empty string after trimming",
			flags:      map[string]string{"output": "   "},
			wantFormat: shared.OutputFormatTable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().String("output", "", "Output format")
			cmd.Flags().StringP("o", "o", "", "Output format (short)")

			for name, value := range tt.flags {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatalf("failed to set flag %q: %v", name, err)
				}
			}

			format, err := shared.ParseOutputFormat(cmd)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if format != tt.wantFormat {
				t.Errorf("expected format %q, got %q", tt.wantFormat, format)
			}
		})
	}
}

func TestPrintYAML(t *testing.T) {
	tests := []struct {
		name       string
		doc        interface{}
		wantOutput string
		wantErr    string
	}{
		{
			name:       "simple map",
			doc:        map[string]string{"key": "value"},
			wantOutput: "key: value\n",
		},
		{
			name: "nested map",
			doc: map[string]interface{}{
				"name": "test",
				"nested": map[string]string{
					"key": "value",
				},
			},
			wantOutput: "name: test\nnested:\n  key: value\n",
		},
		{
			name:       "array",
			doc:        []string{"item1", "item2"},
			wantOutput: "- item1\n- item2\n",
		},
		{
			name:       "empty map",
			doc:        map[string]string{},
			wantOutput: "{}\n",
		},
		{
			name:       "nil value",
			doc:        map[string]interface{}{"key": nil},
			wantOutput: "key: null\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStdout := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create pipe: %v", err)
			}
			os.Stdout = w

			errChan := make(chan error, 1)
			go func() {
				printErr := shared.PrintYAML(tt.doc)
				w.Close()
				errChan <- printErr
			}()

			var buf bytes.Buffer
			io.Copy(&buf, r)
			os.Stdout = oldStdout
			r.Close()

			err = <-errChan
			output := buf.String()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify the output is valid YAML
			var decoded interface{}
			if unmarshalErr := yaml.Unmarshal([]byte(output), &decoded); unmarshalErr != nil {
				t.Errorf("output is not valid YAML: %v", unmarshalErr)
			}

			// Verify it matches expected content (loose check)
			if tt.wantOutput != "" {
				// Normalize line endings for comparison
				normalizedOutput := strings.ReplaceAll(output, "\r\n", "\n")
				if !strings.Contains(normalizedOutput, strings.ReplaceAll(tt.wantOutput, "\r\n", "\n")) {
					t.Errorf("output should contain expected content. Got: %q, Want: %q", output, tt.wantOutput)
				}
			}
		})
	}
}

func TestPrintJSON(t *testing.T) {
	tests := []struct {
		name       string
		doc        interface{}
		wantOutput string
		wantErr    string
	}{
		{
			name:       "simple map",
			doc:        map[string]string{"key": "value"},
			wantOutput: "{\n  \"key\": \"value\"\n}",
		},
		{
			name: "nested map",
			doc: map[string]interface{}{
				"name": "test",
				"nested": map[string]string{
					"key": "value",
				},
			},
			wantOutput: "{\n  \"name\": \"test\",\n  \"nested\": {\n    \"key\": \"value\"\n  }\n}",
		},
		{
			name:       "array",
			doc:        []string{"item1", "item2"},
			wantOutput: "[\n  \"item1\",\n  \"item2\"\n]",
		},
		{
			name:       "empty map",
			doc:        map[string]string{},
			wantOutput: "{}",
		},
		{
			name:       "nil value",
			doc:        map[string]interface{}{"key": nil},
			wantOutput: "{\n  \"key\": null\n}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStdout := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create pipe: %v", err)
			}
			os.Stdout = w

			errChan := make(chan error, 1)
			go func() {
				printErr := shared.PrintJSON(tt.doc)
				w.Close()
				errChan <- printErr
			}()

			var buf bytes.Buffer
			io.Copy(&buf, r)
			os.Stdout = oldStdout
			r.Close()

			err = <-errChan
			output := strings.TrimSpace(buf.String())

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify the output is valid JSON
			var decoded interface{}
			if unmarshalErr := json.Unmarshal([]byte(output), &decoded); unmarshalErr != nil {
				t.Errorf("output is not valid JSON: %v", unmarshalErr)
			}

			// Verify it matches expected content (normalize whitespace)
			if tt.wantOutput != "" {
				// Normalize for comparison by parsing and re-encoding
				var gotDecoded, wantDecoded interface{}
				var gotErr, wantErr error
				gotErr = json.Unmarshal([]byte(output), &gotDecoded)
				if gotErr == nil {
					wantErr = json.Unmarshal([]byte(tt.wantOutput), &wantDecoded)
					if wantErr == nil {
						gotJSON, _ := json.MarshalIndent(gotDecoded, "", "  ")
						wantJSON, _ := json.MarshalIndent(wantDecoded, "", "  ")
						if string(gotJSON) != string(wantJSON) {
							t.Errorf("output mismatch. Got: %q, Want: %q", output, tt.wantOutput)
						}
					}
				}
			}
		})
	}
}

func TestPrintTable(t *testing.T) {
	tests := []struct {
		name          string
		headers       []string
		rows          [][]string
		wantOutput    []string
		notWantOutput []string
	}{
		{
			name:    "simple table",
			headers: []string{"Name", "Status"},
			rows:    [][]string{{"item1", "active"}, {"item2", "inactive"}},
			wantOutput: []string{
				"Name",
				"Status",
				"item1",
				"active",
				"item2",
				"inactive",
			},
		},
		{
			name:    "empty rows",
			headers: []string{"Name", "Status"},
			rows:    [][]string{},
			wantOutput: []string{
				"No resources found.",
			},
			notWantOutput: []string{
				"Name",
				"Status",
			},
		},
		{
			name:    "single column",
			headers: []string{"Name"},
			rows:    [][]string{{"item1"}, {"item2"}},
			wantOutput: []string{
				"Name",
				"item1",
				"item2",
			},
		},
		{
			name:    "multiple columns with varying widths",
			headers: []string{"Short", "Very Long Column Name"},
			rows:    [][]string{{"a", "short"}, {"very long value", "b"}},
			wantOutput: []string{
				"Short",
				"Very Long Column Name",
				"a",
				"short",
				"very long value",
				"b",
			},
		},
		{
			name:    "rows with fewer columns than headers",
			headers: []string{"Name", "Status", "Type"},
			rows:    [][]string{{"item1", "active"}},
			wantOutput: []string{
				"Name",
				"Status",
				"Type",
				"item1",
				"active",
			},
		},
		{
			name:    "rows with more columns than headers",
			headers: []string{"Name", "Status"},
			rows:    [][]string{{"item1", "active", "extra"}},
			wantOutput: []string{
				"Name",
				"Status",
				"item1",
				"active",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			shared.PrintTable(cmd, tt.headers, tt.rows)
			output := buf.String()

			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing expected string %q. Got output: %q", want, output)
				}
			}

			for _, notWant := range tt.notWantOutput {
				if strings.Contains(output, notWant) {
					t.Errorf("output contains unexpected string %q. Got output: %q", notWant, output)
				}
			}

			// Verify separator line exists when there are rows
			if len(tt.rows) > 0 {
				lines := strings.Split(output, "\n")
				foundSeparator := false
				for _, line := range lines {
					// Check for separator line with dashes (at least 2 dashes to avoid false positives)
					if strings.Contains(line, "--") {
						foundSeparator = true
						break
					}
				}
				if !foundSeparator {
					t.Error("expected separator line with dashes, but not found")
				}
			}
		})
	}
}

// Test helpers

func newOutputCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}
