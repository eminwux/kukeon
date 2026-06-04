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
	"errors"
	"fmt"
	"io"

	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

const (
	outputFormatJSON = "json"
	outputFormatYAML = "yaml"
)

// NewApplyCmd builds the `kuke apply` cobra command. `-f` reads a multi-document
// YAML stream from disk or stdin — the sole shape `apply` supports. The
// daemon-side reconcile-by-ref forms (`-b`/`-c`) were retired under #819; the
// equivalent operator workflow is `kuke restart <name>` (which sees
// OutOfSync on Config-lineage cells and reconciles implicitly).
func NewApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "apply -f <file>",
		Short:         "Apply resource definitions from a YAML file or stdin",
		Long:          "Apply resource definitions from a YAML file or stdin (-f).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runApply,
	}

	cmd.Flags().StringP("file", "f", "", "File to read YAML from (use - for stdin)")
	cmd.Flags().StringP("output", "o", "", "Output format: json, yaml (default: human-readable)")

	return cmd
}

// applyFlags is the validated bundle of flag values runApply consumes.
type applyFlags struct {
	file   string
	output string
}

func parseApplyFlags(cmd *cobra.Command) (applyFlags, error) {
	flags := applyFlags{}
	var err error
	if flags.file, err = cmd.Flags().GetString("file"); err != nil {
		return flags, err
	}
	if flags.output, err = cmd.Flags().GetString("output"); err != nil {
		return flags, err
	}

	if flags.output != "" && flags.output != outputFormatJSON && flags.output != outputFormatYAML {
		return flags, fmt.Errorf("invalid --output %q: want json or yaml", flags.output)
	}
	return flags, nil
}

func runApply(cmd *cobra.Command, _ []string) error {
	flags, err := parseApplyFlags(cmd)
	if err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	return runApplyFile(cmd, client, flags)
}

// runApplyFile is the `kuke apply -f` path: read YAML, send to the daemon's
// ApplyDocuments, print result.
func runApplyFile(cmd *cobra.Command, client kukeonv1.Client, flags applyFlags) error {
	if flags.file == "" {
		return errors.New("file flag is required (use -f <file> or -f - for stdin)")
	}

	reader, cleanup, err := kukshared.ReadFileOrStdin(flags.file)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()

	rawYAML, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	result, err := client.ApplyDocuments(cmd.Context(), rawYAML)
	if err != nil {
		return err
	}

	if flags.output == outputFormatJSON || flags.output == outputFormatYAML {
		return printApplyResultJSON(cmd, result, flags.output)
	}
	return printApplyResult(cmd, result)
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukshared.DaemonClientFromCmd(cmd)
}

func printApplyResult(cmd *cobra.Command, result kukeonv1.ApplyDocumentsResult) error {
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
			if resource.Error != "" {
				cmd.Printf("  Error: %s\n", resource.Error)
			}
		}
	}

	if hasFailures {
		return fmt.Errorf("%w: some resources failed to apply", errdefs.ErrConfig)
	}

	return nil
}

func printApplyResultJSON(cmd *cobra.Command, result kukeonv1.ApplyDocumentsResult, format string) error {
	output := struct {
		Resources []kukeonv1.ApplyResourceResult `json:"resources" yaml:"resources"`
	}{
		Resources: result.Resources,
	}
	return kukshared.PrintJSONOrYAML(cmd, output, format)
}
