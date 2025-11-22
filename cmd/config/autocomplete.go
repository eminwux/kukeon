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

package config

import (
	"log/slog"
	"strings"

	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// controllerFromCmd creates a controller.Exec from the command context.
// This duplicates the logic from cmd/kuke/create/shared to avoid import cycles.
func controllerFromCmd(cmd *cobra.Command) (*controller.Exec, error) {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return nil, errdefs.ErrLoggerNotFound
	}

	opts := controller.Options{
		RunPath:          viper.GetString(KUKEON_ROOT_RUN_PATH.ViperKey),
		ContainerdSocket: viper.GetString(KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	}

	return controller.NewControllerExec(cmd.Context(), logger, opts), nil
}

// CompleteRealmNames provides shell completion for realm names by listing existing realms.
// This function can be used as a ValidArgsFunction or for flag completion in commands that accept realm names.
func CompleteRealmNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// When used as ValidArgsFunction for positional args, if an argument is already provided
	// and toComplete is empty (meaning we're at the end of the command line), don't suggest
	// more completions. This prevents the completion from being appended multiple times
	// when tab is pressed repeatedly after an argument is already present.
	// For flag completion, we should always allow completion even if positional args exist.
	// We can't easily distinguish between ValidArgsFunction and flag completion calls,
	// so we use a heuristic: only block if toComplete is empty AND we have args AND
	// the command's Args validator would reject adding another arg.
	// However, for flag completion, toComplete being empty just means the flag value is empty,
	// which is valid. So we only block if we're being used as ValidArgsFunction.
	// Since we can't detect that directly, we check if ValidArgsFunction is set and
	// if we've reached what seems like the max args (1 for most commands using this).
	if len(args) >= 1 && toComplete == "" {
		// Check if this command has a maximum args limit of 1
		// This is a heuristic - if ValidArgsFunction is set and we have 1 arg,
		// we're likely at the max for positional args
		if cmd.ValidArgsFunction != nil {
			// Try to validate if we can add another arg
			// If the command uses MaximumNArgs(1), adding another arg will fail
			testArgs := make([]string, len(args), len(args)+1)
			copy(testArgs, args)
			testArgs = append(testArgs, "test")
			if cmd.Args != nil {
				if err := cmd.Args(cmd, testArgs); err != nil {
					// Adding another arg would fail, so we're at max
					// This means we're completing positional args, not flags
					return []string{}, cobra.ShellCompDirectiveNoFileComp
				}
			}
		}
	}

	ctrl, err := controllerFromCmd(cmd)
	if err != nil {
		// Return empty completion on error (controller unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	realms, err := ctrl.ListRealms()
	if err != nil {
		// Return empty completion on error
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Use a map to track seen names for deduplication
	seen := make(map[string]bool)
	names := make([]string, 0, len(realms))
	for _, realm := range realms {
		name := realm.Metadata.Name
		// Filter by toComplete prefix if provided
		if toComplete == "" || strings.HasPrefix(name, toComplete) {
			// Only add if we haven't seen this name before
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteSpaceNames provides shell completion for space names by listing existing spaces.
// This function can be used as a ValidArgsFunction or for flag completion in commands that accept space names.
// It optionally filters by realm if --realm flag is set.
// When used as ValidArgsFunction, it requires --realm flag to be set before completing positional arguments.
func CompleteSpaceNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// When used as ValidArgsFunction for positional args, if an argument is already provided
	// and toComplete is empty (meaning we're at the end of the command line), don't suggest
	// more completions. This prevents the completion from being appended multiple times
	// when tab is pressed repeatedly after an argument is already present.
	// For flag completion, we should always allow completion even if positional args exist.
	if len(args) >= 1 && toComplete == "" {
		// Check if this command has reached its maximum args limit
		// This is a heuristic - if ValidArgsFunction is set and we can't add another arg,
		// we're likely at the max for positional args
		if cmd.ValidArgsFunction != nil {
			// Try to validate if we can add another arg
			testArgs := make([]string, len(args), len(args)+1)
			copy(testArgs, args)
			testArgs = append(testArgs, "test")
			if cmd.Args != nil {
				if err := cmd.Args(cmd, testArgs); err != nil {
					// Adding another arg would fail, so we're at max
					// This means we're completing positional args, not flags
					return []string{}, cobra.ShellCompDirectiveNoFileComp
				}
			}
		}
	}

	ctrl, err := controllerFromCmd(cmd)
	if err != nil {
		// Return empty completion on error (controller unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Read realm flag if available
	realmName, _ := cmd.Flags().GetString("realm")

	// Trim whitespace
	realmName = strings.TrimSpace(realmName)

	// When used as ValidArgsFunction for positional args, require --realm flag to be set
	// Check if we're being called as ValidArgsFunction (not for flag completion)
	if cmd.ValidArgsFunction != nil && len(args) == 0 {
		// We're completing a positional argument and no args are provided yet
		// Require --realm flag to be set
		if realmName == "" {
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	// List spaces, optionally filtered by realm
	spaces, err := ctrl.ListSpaces(realmName)
	if err != nil {
		// Return empty completion on error
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Use a map to track seen names for deduplication
	seen := make(map[string]bool)
	names := make([]string, 0, len(spaces))
	for _, space := range spaces {
		name := space.Metadata.Name
		// Filter by toComplete prefix if provided
		if toComplete == "" || strings.HasPrefix(name, toComplete) {
			// Only add if we haven't seen this name before
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteStackNames provides shell completion for stack names by listing existing stacks.
// This function can be used as a ValidArgsFunction or for flag completion in commands that accept stack names.
// It optionally filters by realm and space if --realm and --space flags are set.
// When used as ValidArgsFunction, it requires --realm and --space flags to be set before completing positional arguments.
func CompleteStackNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// When used as ValidArgsFunction for positional args, if an argument is already provided
	// and toComplete is empty (meaning we're at the end of the command line), don't suggest
	// more completions. This prevents the completion from being appended multiple times
	// when tab is pressed repeatedly after an argument is already present.
	// For flag completion, we should always allow completion even if positional args exist.
	if len(args) >= 1 && toComplete == "" {
		// Check if this command has reached its maximum args limit
		// This is a heuristic - if ValidArgsFunction is set and we can't add another arg,
		// we're likely at the max for positional args
		if cmd.ValidArgsFunction != nil {
			// Try to validate if we can add another arg
			testArgs := make([]string, len(args), len(args)+1)
			copy(testArgs, args)
			testArgs = append(testArgs, "test")
			if cmd.Args != nil {
				if err := cmd.Args(cmd, testArgs); err != nil {
					// Adding another arg would fail, so we're at max
					// This means we're completing positional args, not flags
					return []string{}, cobra.ShellCompDirectiveNoFileComp
				}
			}
		}
	}

	ctrl, err := controllerFromCmd(cmd)
	if err != nil {
		// Return empty completion on error (controller unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Read realm and space flags if available
	realmName, _ := cmd.Flags().GetString("realm")
	spaceName, _ := cmd.Flags().GetString("space")

	// Trim whitespace
	realmName = strings.TrimSpace(realmName)
	spaceName = strings.TrimSpace(spaceName)

	// When used as ValidArgsFunction for positional args, require --realm and --space flags to be set
	// Check if we're being called as ValidArgsFunction (not for flag completion)
	if cmd.ValidArgsFunction != nil && len(args) == 0 {
		// We're completing a positional argument and no args are provided yet
		// Require --realm and --space flags to be set
		if realmName == "" || spaceName == "" {
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	// List stacks, optionally filtered by realm and space
	stacks, err := ctrl.ListStacks(realmName, spaceName)
	if err != nil {
		// Return empty completion on error
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Use a map to track seen names for deduplication
	seen := make(map[string]bool)
	names := make([]string, 0, len(stacks))
	for _, stack := range stacks {
		name := stack.Metadata.Name
		// Filter by toComplete prefix if provided
		if toComplete == "" || strings.HasPrefix(name, toComplete) {
			// Only add if we haven't seen this name before
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteCellNames provides shell completion for cell names by listing existing cells.
// This function can be used as a ValidArgsFunction or for flag completion in commands that accept cell names.
// It optionally filters by realm, space, and stack if --realm, --space, and --stack flags are set.
// When used as ValidArgsFunction, it requires --realm, --space, and --stack flags to be set before completing positional arguments.
func CompleteCellNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// When used as ValidArgsFunction for positional args, if an argument is already provided
	// and toComplete is empty (meaning we're at the end of the command line), don't suggest
	// more completions. This prevents the completion from being appended multiple times
	// when tab is pressed repeatedly after an argument is already present.
	// For flag completion, we should always allow completion even if positional args exist.
	if len(args) >= 1 && toComplete == "" {
		// Check if this command has reached its maximum args limit
		// This is a heuristic - if ValidArgsFunction is set and we can't add another arg,
		// we're likely at the max for positional args
		if cmd.ValidArgsFunction != nil {
			// Try to validate if we can add another arg
			testArgs := make([]string, len(args), len(args)+1)
			copy(testArgs, args)
			testArgs = append(testArgs, "test")
			if cmd.Args != nil {
				if err := cmd.Args(cmd, testArgs); err != nil {
					// Adding another arg would fail, so we're at max
					// This means we're completing positional args, not flags
					return []string{}, cobra.ShellCompDirectiveNoFileComp
				}
			}
		}
	}

	ctrl, err := controllerFromCmd(cmd)
	if err != nil {
		// Return empty completion on error (controller unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Read realm, space, and stack flags if available
	realmName, _ := cmd.Flags().GetString("realm")
	spaceName, _ := cmd.Flags().GetString("space")
	stackName, _ := cmd.Flags().GetString("stack")

	// Trim whitespace
	realmName = strings.TrimSpace(realmName)
	spaceName = strings.TrimSpace(spaceName)
	stackName = strings.TrimSpace(stackName)

	// When used as ValidArgsFunction for positional args, require --realm, --space, and --stack flags to be set
	// Check if we're being called as ValidArgsFunction (not for flag completion)
	if cmd.ValidArgsFunction != nil && len(args) == 0 {
		// We're completing a positional argument and no args are provided yet
		// Require --realm, --space, and --stack flags to be set
		if realmName == "" || spaceName == "" || stackName == "" {
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	// List cells, optionally filtered by realm, space, and stack
	cells, err := ctrl.ListCells(realmName, spaceName, stackName)
	if err != nil {
		// Return empty completion on error
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Use a map to track seen names for deduplication
	seen := make(map[string]bool)
	names := make([]string, 0, len(cells))
	for _, cell := range cells {
		name := cell.Metadata.Name
		// Filter by toComplete prefix if provided
		if toComplete == "" || strings.HasPrefix(name, toComplete) {
			// Only add if we haven't seen this name before
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteContainerNames provides shell completion for container names by listing existing containers.
// This function can be used as a ValidArgsFunction or for flag completion in commands that accept container names.
// It requires --realm, --space, --stack, and --cell flags to be set to filter containers.
// When used as ValidArgsFunction, it requires --realm, --space, --stack, and --cell flags to be set before completing positional arguments.
func CompleteContainerNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// When used as ValidArgsFunction for positional args, if an argument is already provided
	// and toComplete is empty (meaning we're at the end of the command line), don't suggest
	// more completions. This prevents the completion from being appended multiple times
	// when tab is pressed repeatedly after an argument is already present.
	// For flag completion, we should always allow completion even if positional args exist.
	if len(args) >= 1 && toComplete == "" {
		// Check if this command has reached its maximum args limit
		// This is a heuristic - if ValidArgsFunction is set and we can't add another arg,
		// we're likely at the max for positional args
		if cmd.ValidArgsFunction != nil {
			// Try to validate if we can add another arg
			testArgs := make([]string, len(args), len(args)+1)
			copy(testArgs, args)
			testArgs = append(testArgs, "test")
			if cmd.Args != nil {
				if err := cmd.Args(cmd, testArgs); err != nil {
					// Adding another arg would fail, so we're at max
					// This means we're completing positional args, not flags
					return []string{}, cobra.ShellCompDirectiveNoFileComp
				}
			}
		}
	}

	ctrl, err := controllerFromCmd(cmd)
	if err != nil {
		// Return empty completion on error (controller unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Read required flags
	realmName, _ := cmd.Flags().GetString("realm")
	spaceName, _ := cmd.Flags().GetString("space")
	stackName, _ := cmd.Flags().GetString("stack")
	cellName, _ := cmd.Flags().GetString("cell")

	// Trim whitespace
	realmName = strings.TrimSpace(realmName)
	spaceName = strings.TrimSpace(spaceName)
	stackName = strings.TrimSpace(stackName)
	cellName = strings.TrimSpace(cellName)

	// When used as ValidArgsFunction for positional args, require all flags to be set
	// Check if we're being called as ValidArgsFunction (not for flag completion)
	if cmd.ValidArgsFunction != nil && len(args) == 0 {
		// We're completing a positional argument and no args are provided yet
		// Require --realm, --space, --stack, and --cell flags to be set
		if realmName == "" || spaceName == "" || stackName == "" || cellName == "" {
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	// For flag completion or when listing containers, return empty if required flags are not set
	if realmName == "" || spaceName == "" || stackName == "" || cellName == "" {
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// List containers filtered by realm, space, stack, and cell
	containers, err := ctrl.ListContainers(realmName, spaceName, stackName, cellName)
	if err != nil {
		// Return empty completion on error
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	// Use a map to track seen names for deduplication
	seen := make(map[string]bool)
	names := make([]string, 0, len(containers))
	for _, container := range containers {
		name := container.ID
		// Filter by toComplete prefix if provided
		if toComplete == "" || strings.HasPrefix(name, toComplete) {
			// Only add if we haven't seen this name before
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteOutputFormat provides shell completion for output format values (yaml, json, table).
// This function can be used for flag completion in commands that accept output format flags.
func CompleteOutputFormat(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	formats := []string{"yaml", "json", "table"}

	names := make([]string, 0, len(formats))
	for _, format := range formats {
		// Filter by toComplete prefix if provided
		if toComplete == "" || strings.HasPrefix(format, toComplete) {
			names = append(names, format)
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}
