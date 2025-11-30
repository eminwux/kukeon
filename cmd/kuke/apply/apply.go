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

package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type applyController interface {
	ApplyDocuments(docs []parser.Document) (controller.ApplyResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "apply -f <file>",
		Short:         "Apply resource definitions from YAML file",
		Long:          "Apply resource definitions from a YAML file or stdin. Supports multi-document YAML separated by '---'.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			file, err := cmd.Flags().GetString("file")
			if err != nil {
				return err
			}
			if file == "" {
				return errors.New("file flag is required (use -f <file> or -f - for stdin)")
			}

			output, err := cmd.Flags().GetString("output")
			if err != nil {
				return err
			}

			// Read input
			var reader io.Reader
			if file == "-" {
				reader = os.Stdin
			} else {
				f, openErr := os.Open(file)
				if openErr != nil {
					return fmt.Errorf("failed to open file %q: %w", file, openErr)
				}
				defer f.Close()
				reader = f
			}

			// Parse YAML documents
			rawDocs, err := parser.ParseDocuments(reader)
			if err != nil {
				return fmt.Errorf("failed to parse YAML: %w", err)
			}

			// Parse and validate each document
			docs := make([]parser.Document, 0, len(rawDocs))
			var validationErrors []*parser.ValidationError

			for i, rawDoc := range rawDocs {
				doc, parseErr := parser.ParseDocument(i, rawDoc)
				if parseErr != nil {
					validationErrors = append(validationErrors, &parser.ValidationError{
						Index: i,
						Err:   parseErr,
					})
					continue
				}

				validationErr := parser.ValidateDocument(doc)
				if validationErr != nil {
					validationErrors = append(validationErrors, validationErr)
					continue
				}

				docs = append(docs, *doc)
			}

			// Report validation errors
			if len(validationErrors) > 0 {
				var errMsgs []string
				for _, validationErr := range validationErrors {
					errMsgs = append(errMsgs, validationErr.Error())
				}
				return fmt.Errorf("validation errors:\n  %s", strings.Join(errMsgs, "\n  "))
			}

			if len(docs) == 0 {
				return errors.New("no valid documents found in input")
			}

			// Get controller
			var ctrl applyController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(applyController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, ctrlErr := shared.ControllerFromCmd(cmd)
				if ctrlErr != nil {
					return ctrlErr
				}
				ctrl = realCtrl
			}

			// Apply documents
			result, err := ctrl.ApplyDocuments(docs)
			if err != nil {
				return fmt.Errorf("failed to apply documents: %w", err)
			}

			// Print results
			if output == "json" || output == "yaml" {
				return printApplyResultJSON(cmd, result, output)
			}
			return printApplyResult(cmd, result)
		},
	}

	cmd.Flags().StringP("file", "f", "", "File to read YAML from (use - for stdin)")
	_ = cmd.MarkFlagRequired("file")

	cmd.Flags().StringP("output", "o", "", "Output format: json, yaml (default: human-readable)")

	return cmd
}

func printApplyResult(cmd *cobra.Command, result controller.ApplyResult) error {
	hasFailures := false
	for _, resource := range result.Resources {
		switch resource.Action {
		case "created":
			cmd.Printf("%s %q: created\n", resource.Kind, resource.Name)
		case "updated":
			cmd.Printf("%s %q: updated\n", resource.Kind, resource.Name)
			for _, change := range resource.Changes {
				cmd.Printf("  - %s\n", change)
			}
		case "unchanged":
			cmd.Printf("%s %q: unchanged\n", resource.Kind, resource.Name)
		case "failed":
			hasFailures = true
			cmd.Printf("%s %q: failed\n", resource.Kind, resource.Name)
			if resource.Error != nil {
				cmd.Printf("  Error: %v\n", resource.Error)
			}
		}
	}

	if hasFailures {
		return fmt.Errorf("%w: some resources failed to apply", errdefs.ErrConfig)
	}

	return nil
}

func printApplyResultJSON(cmd *cobra.Command, result controller.ApplyResult, format string) error {
	// Simple JSON output (can be enhanced later)
	output := struct {
		Resources []controller.ResourceResult `json:"resources"`
	}{
		Resources: result.Resources,
	}

	var outputStr string
	var err error

	if format == "json" {
		var b []byte
		b, err = json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		outputStr = string(b)
	} else {
		var b []byte
		b, err = yaml.Marshal(output)
		if err != nil {
			return fmt.Errorf("failed to marshal YAML: %w", err)
		}
		outputStr = string(b)
	}

	cmd.Print(outputStr)
	return nil
}
