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
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/delete/cell"
	"github.com/eminwux/kukeon/cmd/kuke/delete/container"
	"github.com/eminwux/kukeon/cmd/kuke/delete/realm"
	"github.com/eminwux/kukeon/cmd/kuke/delete/shared"
	"github.com/eminwux/kukeon/cmd/kuke/delete/space"
	"github.com/eminwux/kukeon/cmd/kuke/delete/stack"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type deleteController interface {
	DeleteDocuments(docs []parser.Document, cascade, force bool) (controller.DeleteResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

// NewDeleteCmd builds the `kuke delete` parent command and registers all resource
// delete subcommands. Persistent flags defined on the root kuke command are
// inherited automatically via Cobra's command tree.
func NewDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete [name]",
		Aliases: []string{"d"},
		Short:   "Delete Kukeon resources (realm, space, stack, cell, container)",
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
	)

	return cmd
}

// completeDeleteSubcommands provides shell completion for delete subcommand names.
func completeDeleteSubcommands(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	subcommands := []string{"realm", "space", "stack", "cell", "container"}

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

// handleFileDeletion handles deletion from a YAML file.
func handleFileDeletion(cmd *cobra.Command, file string) error {
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return err
	}

	// Read input
	reader, cleanup, err := kukshared.ReadFileOrStdin(file)
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			// Log cleanup error but don't fail the operation
			_ = cleanupErr
		}
	}()

	// Parse and validate documents
	docs, validationErrors, err := kukshared.ParseAndValidateDocuments(reader)
	if err != nil {
		return err
	}

	// Report validation errors
	if len(validationErrors) > 0 {
		return kukshared.FormatValidationErrors(validationErrors)
	}

	if len(docs) == 0 {
		return errors.New("no valid documents found in input")
	}

	// Get controller
	var ctrl deleteController
	if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(deleteController); ok {
		ctrl = mockCtrl
	} else {
		realCtrl, ctrlErr := shared.ControllerFromCmd(cmd)
		if ctrlErr != nil {
			return ctrlErr
		}
		ctrl = realCtrl
	}

	// Get cascade and force flags
	cascade := shared.ParseCascadeFlag(cmd)
	force := shared.ParseForceFlag(cmd)

	// Delete documents
	result, err := ctrl.DeleteDocuments(docs, cascade, force)
	if err != nil {
		return fmt.Errorf("failed to delete documents: %w", err)
	}

	// Print results
	if output == "json" || output == "yaml" {
		return printDeleteResultJSON(cmd, result, output)
	}
	return printDeleteResult(cmd, result)
}

// printDeleteResult prints deletion results in human-readable format.
func printDeleteResult(cmd *cobra.Command, result controller.DeleteResult) error {
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
			if resource.Error != nil {
				cmd.Printf("  Error: %v\n", resource.Error)
			}
		}
	}

	if hasFailures {
		return fmt.Errorf("%w: some resources failed to delete", errdefs.ErrConfig)
	}

	return nil
}

// printDeleteResultJSON prints deletion results in JSON or YAML format.
func printDeleteResultJSON(cmd *cobra.Command, result controller.DeleteResult, format string) error {
	output := struct {
		Resources []controller.ResourceDeleteResult `json:"resources"`
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
