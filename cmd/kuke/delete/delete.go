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

package deletecmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	blueprintdelete "github.com/eminwux/kukeon/cmd/kuke/delete/blueprint"
	"github.com/eminwux/kukeon/cmd/kuke/delete/cell"
	configdelete "github.com/eminwux/kukeon/cmd/kuke/delete/config"
	"github.com/eminwux/kukeon/cmd/kuke/delete/container"
	"github.com/eminwux/kukeon/cmd/kuke/delete/realm"
	secretdelete "github.com/eminwux/kukeon/cmd/kuke/delete/secret"
	"github.com/eminwux/kukeon/cmd/kuke/delete/shared"
	"github.com/eminwux/kukeon/cmd/kuke/delete/space"
	"github.com/eminwux/kukeon/cmd/kuke/delete/stack"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
// Name kept for source-compat with existing test helpers; the value is now a
// kukeonv1.Client (mirroring apply -f).
type MockControllerKey struct{}

// NewDeleteCmd builds the `kuke delete` parent command and registers all resource
// delete subcommands. Persistent flags defined on the root kuke command are
// inherited automatically via Cobra's command tree.
func NewDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete [name]",
		Aliases: []string{"d"},
		Short:   "Delete Kukeon resources (realm, space, stack, cell, container, secret, blueprint, config)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Check if -f flag is provided
			file, err := cmd.Flags().GetString("file")
			if err != nil {
				return err
			}

			if file != "" {
				// Handle file-based deletion
				return handleFileDeletion(cmd, file)
			}

			// Default behavior: show help
			_ = cmd.Help()
			return nil
		},
	}

	cmd.ValidArgsFunction = completeDeleteSubcommands

	// Add -f, --file flag for file-based deletion
	cmd.Flags().StringP("file", "f", "", "File to read YAML from (use - for stdin)")

	// Add --output flag for output format
	cmd.Flags().StringP("output", "o", "", "Output format: json, yaml (default: human-readable)")

	// Add persistent --cascade flag
	cmd.PersistentFlags().
		Bool("cascade", false, "Automatically delete child resources recursively (does not apply to containers)")
	_ = viper.BindPFlag(config.KUKE_DELETE_CASCADE.ViperKey, cmd.PersistentFlags().Lookup("cascade"))

	// Add persistent --force flag
	cmd.PersistentFlags().Bool("force", false, "Skip validation and attempt deletion anyway")
	_ = viper.BindPFlag(config.KUKE_DELETE_FORCE.ViperKey, cmd.PersistentFlags().Lookup("force"))

	cmd.AddCommand(
		realm.NewRealmCmd(),
		space.NewSpaceCmd(),
		stack.NewStackCmd(),
		cell.NewCellCmd(),
		container.NewContainerCmd(),
		secretdelete.NewSecretCmd(),
		blueprintdelete.NewBlueprintCmd(),
		configdelete.NewConfigCmd(),
	)

	return cmd
}

// completeDeleteSubcommands provides shell completion for delete subcommand names.
func completeDeleteSubcommands(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	subcommands := []string{"realm", "space", "stack", "cell", "container", "secret", "blueprint", "config"}

	if toComplete == "" {
		return subcommands, cobra.ShellCompDirectiveNoFileComp
	}

	matches := make([]string, 0, len(subcommands))
	for _, subcmd := range subcommands {
		if strings.HasPrefix(subcmd, toComplete) {
			matches = append(matches, subcmd)
		}
	}

	return matches, cobra.ShellCompDirectiveNoFileComp
}

// handleFileDeletion handles deletion from a YAML file by routing through the
// daemon-aware kukeonv1.Client — symmetric with `kuke apply -f`. The previous
// in-process `controller.Exec` shortcut was a #574-class bug: it bypassed
// `--host`/`KUKEON_HOST` and read /opt/kukeon directly even when a daemon
// was managing a different run path.
func handleFileDeletion(cmd *cobra.Command, file string) error {
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return err
	}

	// Read raw YAML; the client (local or RPC) owns parse/validate.
	reader, cleanup, err := kukshared.ReadFileOrStdin(file)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()

	rawYAML, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	cascade := shared.ParseCascadeFlag(cmd)
	force := shared.ParseForceFlag(cmd)

	result, err := client.DeleteDocuments(cmd.Context(), rawYAML, cascade, force)
	if err != nil {
		return err
	}

	if output == "json" || output == "yaml" {
		return printDeleteResultJSON(cmd, result, output)
	}
	return printDeleteResult(cmd, result)
}

// resolveClient picks the test-injected mock from context if present, else
// returns a real daemon-aware client via the shared resolver. Mirrors
// apply.resolveClient so both `-f` paths share the same seam.
func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukshared.DaemonClientFromCmd(cmd)
}

// printDeleteResult prints deletion results in human-readable format.
func printDeleteResult(cmd *cobra.Command, result kukeonv1.DeleteDocumentsResult) error {
	hasFailures := false
	for _, resource := range result.Resources {
		switch resource.Action {
		case actionDeleted:
			cmd.Printf("%s %q: deleted\n", resource.Kind, resource.Name)
			// Show cascaded resources
			if len(resource.Cascaded) > 0 {
				// Determine resource type from kind
				var resourceType string
				switch resource.Kind {
				case "Realm":
					resourceType = "space"
				case "Space":
					resourceType = "stack"
				case "Stack":
					resourceType = "cell"
				default:
					resourceType = "resource"
				}
				cmd.Printf("  - %d %s(s) deleted (cascade)\n", len(resource.Cascaded), resourceType)
			}
			// Show details
			for key, value := range resource.Details {
				cmd.Printf("  - %s: %s\n", key, value)
			}
		case actionNotFound:
			cmd.Printf("%s %q: not found\n", resource.Kind, resource.Name)
		case actionFailed:
			hasFailures = true
			cmd.Printf("%s %q: failed\n", resource.Kind, resource.Name)
			if resource.Error != "" {
				cmd.Printf("  Error: %s\n", resource.Error)
			}
		}
	}

	if hasFailures {
		return fmt.Errorf("%w: some resources failed to delete", errdefs.ErrConfig)
	}

	return nil
}

// printDeleteResultJSON prints deletion results in JSON or YAML format.
func printDeleteResultJSON(cmd *cobra.Command, result kukeonv1.DeleteDocumentsResult, format string) error {
	output := struct {
		Resources []kukeonv1.DeleteResourceResult `json:"resources" yaml:"resources"`
	}{
		Resources: result.Resources,
	}

	return kukshared.PrintJSONOrYAML(cmd, output, format)
}

const (
	actionDeleted  = "deleted"
	actionNotFound = "not found"
	actionFailed   = "failed"
)
