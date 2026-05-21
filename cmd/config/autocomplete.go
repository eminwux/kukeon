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
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/cellprofile"
	"github.com/eminwux/kukeon/internal/client/local"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in
// tests, matching the convention every other command package uses (see e.g.
// cmd/kuke/get/cell). Despite the historical name it now carries a
// kukeonv1.Client, not a controller.Exec.
type MockControllerKey struct{}

// completionTimeout bounds how long a completer waits on the daemon before
// degrading to an empty list. Shell completion must never block the prompt,
// so this is deliberately short: a slow, hung, or unreachable daemon yields
// no candidates rather than a frozen terminal (issue #634).
const completionTimeout = 2 * time.Second

// completionClient returns the kukeonv1.Client the completers resolve resource
// names through. It mirrors shared.ClientFromCmd's source selection so
// completion reflects the same authoritative view `kuke get …` shows the user
// running it: the `kukeon/noDaemon` viper key picks the in-process controller
// (local.New, direct on-disk metadata reads, requires privileges) versus the
// kukeond daemon (RPC over the configured KUKEON_HOST socket).
//
// The default is the daemon path. This is the fix for issue #634: the
// completers previously read /opt/kukeon/data directly via controller.Exec,
// which an unprivileged shell user cannot read on the standard root-owned
// install, so completion silently returned nothing while `kuke get …` (which
// routes through the group-accessible daemon socket) worked. Routing
// completion through the daemon makes it work for any user who can already
// run `kuke get …`.
//
// --no-daemon story: completion follows the same `kukeon/noDaemon` viper key
// the get verbs honor — there is no second selection path to keep in sync.
// One caveat is worth stating because it shapes what works at a live prompt:
// cobra's hidden `__complete` command does not run the root PersistentPreRunE,
// and it does not mark inherited *persistent flags* (`--host`, `--no-daemon`,
// `--run-path`) as changed for the completion request. So during real shell
// completion those flags do not reach viper; what does reach it is the env
// (KUKEON_HOST, KUKEON_NO_DAEMON, KUKEON_RUN_PATH, bound via config.DefineKV's
// viper.SetDefault plus the env bindings) and the built-in defaults. In
// practice that is exactly right for the standard install: completion dials
// the default kukeond socket (or KUKEON_HOST when the operator overrides it)
// and stays on the daemon path. The in-process branch remains reachable when
// the viper key is set directly — programmatically or via KUKEON_NO_DAEMON —
// which is also how the unit tests exercise it.
//
// The caller owns the returned Client and must Close it.
func completionClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	// Check for an injected mock client in context (for testing).
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}

	if viper.GetBool(KUKEON_ROOT_NO_DAEMON.ViperKey) {
		logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
		if !ok || logger == nil {
			return nil, errdefs.ErrLoggerNotFound
		}
		return local.New(cmd.Context(), logger, controller.Options{
			RunPath:          viper.GetString(KUKEON_ROOT_RUN_PATH.ViperKey),
			ContainerdSocket: viper.GetString(KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
		}), nil
	}

	host := viper.GetString(KUKEON_ROOT_HOST.ViperKey)
	if host == "" {
		host = KUKEON_ROOT_HOST.Default
	}
	return kukeonv1.Dial(cmd.Context(), host)
}

// getFlagValueWithDefault gets a flag value, falling back to Viper (which includes defaults) and then to a provided default.
// The ViperKey is determined from the command hierarchy (e.g., "kuke/create/space/realm").
func getFlagValueWithDefault(cmd *cobra.Command, flagName string, fallbackDefault string) string {
	// First, try to get the flag value directly
	flagValue, _ := cmd.Flags().GetString(flagName)
	if flagValue != "" {
		return strings.TrimSpace(flagValue)
	}

	// If flag is not set, try to determine the ViperKey from command hierarchy
	// Pattern: kuke/{operation}/{resource}/{flagName}
	// Command hierarchy: kuke (root) -> create/get/delete/etc (operation) -> space/stack/etc (resource)
	var operation string
	resource := cmd.Name()

	// Get the parent command (operation: create, get, delete, purge, start, stop, kill)
	if parent := cmd.Parent(); parent != nil {
		operation = parent.Name()
		// If parent is "kuke" (root), we need to go up one more level
		// This should only happen in edge cases, but handle it gracefully
		if operation == "kuke" {
			if grandParent := parent.Parent(); grandParent != nil {
				operation = grandParent.Name()
			} else {
				operation = "" // Can't determine operation
			}
		}
	}

	// Build ViperKey if we have both operation and resource
	if operation != "" && resource != "" {
		viperKey := "kuke/" + operation + "/" + resource + "/" + flagName
		viperValue := viper.GetString(viperKey)
		if viperValue != "" {
			return strings.TrimSpace(viperValue)
		}
	}

	// Finally, use the fallback default
	return strings.TrimSpace(fallbackDefault)
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

	client, err := completionClient(cmd)
	if err != nil {
		// Return empty completion on error (client unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()

	realms, err := client.ListRealms(ctx)
	if err != nil {
		// Return empty completion on error (daemon unreachable, timeout, etc.)
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

	client, err := completionClient(cmd)
	if err != nil {
		// Return empty completion on error (client unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	// Read realm flag with default fallback
	realmName := getFlagValueWithDefault(cmd, "realm", "default")

	// When used as ValidArgsFunction for positional args, require --realm flag to be set
	// Check if we're being called as ValidArgsFunction (not for flag completion)
	if cmd.ValidArgsFunction != nil && len(args) == 0 {
		// We're completing a positional argument and no args are provided yet
		// Require --realm flag to be set
		if realmName == "" {
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()

	// List spaces, optionally filtered by realm
	spaces, err := client.ListSpaces(ctx, realmName)
	if err != nil {
		// Return empty completion on error (daemon unreachable, timeout, etc.)
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

	client, err := completionClient(cmd)
	if err != nil {
		// Return empty completion on error (client unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	// Read realm and space flags with default fallback
	realmName := getFlagValueWithDefault(cmd, "realm", "default")
	spaceName := getFlagValueWithDefault(cmd, "space", "default")

	// When used as ValidArgsFunction for positional args, require --realm and --space flags to be set
	// Check if we're being called as ValidArgsFunction (not for flag completion)
	if cmd.ValidArgsFunction != nil && len(args) == 0 {
		// We're completing a positional argument and no args are provided yet
		// Require --realm and --space flags to be set
		if realmName == "" || spaceName == "" {
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()

	// List stacks, optionally filtered by realm and space
	stacks, err := client.ListStacks(ctx, realmName, spaceName)
	if err != nil {
		// Return empty completion on error (daemon unreachable, timeout, etc.)
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

	client, err := completionClient(cmd)
	if err != nil {
		// Return empty completion on error (client unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	// Read realm, space, and stack flags with default fallback
	realmName := getFlagValueWithDefault(cmd, "realm", "default")
	spaceName := getFlagValueWithDefault(cmd, "space", "default")
	stackName := getFlagValueWithDefault(cmd, "stack", "default")

	// When used as ValidArgsFunction for positional args, require --realm, --space, and --stack flags to be set
	// Check if we're being called as ValidArgsFunction (not for flag completion)
	if cmd.ValidArgsFunction != nil && len(args) == 0 {
		// We're completing a positional argument and no args are provided yet
		// Require --realm, --space, and --stack flags to be set
		if realmName == "" || spaceName == "" || stackName == "" {
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()

	// List cells, optionally filtered by realm, space, and stack
	cells, err := client.ListCells(ctx, realmName, spaceName, stackName)
	if err != nil {
		// Return empty completion on error (daemon unreachable, timeout, etc.)
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

	client, err := completionClient(cmd)
	if err != nil {
		// Return empty completion on error (client unavailable)
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	// Read required flags with default fallback (cell has no default)
	realmName := getFlagValueWithDefault(cmd, "realm", "default")
	spaceName := getFlagValueWithDefault(cmd, "space", "default")
	stackName := getFlagValueWithDefault(cmd, "stack", "default")
	cellName := getFlagValueWithDefault(cmd, "cell", "")

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

	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()

	// List containers filtered by realm, space, stack, and cell
	containers, err := client.ListContainers(ctx, realmName, spaceName, stackName, cellName)
	if err != nil {
		// Return empty completion on error (daemon unreachable, timeout, etc.)
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

// CompleteSecretNames provides shell completion for `kind: Secret` names by
// listing existing secrets (issue #622). It can be used as a ValidArgsFunction
// or for flag completion. Scope flags filter the candidate set: --realm
// defaults to "default", while --space/--stack/--cell are unset by default
// (an unset deeper coordinate means "list the whole subtree"). When used as a
// ValidArgsFunction it requires --realm to be set before completing.
func CompleteSecretNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 1 && toComplete == "" {
		if cmd.ValidArgsFunction != nil {
			testArgs := make([]string, len(args), len(args)+1)
			copy(testArgs, args)
			testArgs = append(testArgs, "test")
			if cmd.Args != nil {
				if err := cmd.Args(cmd, testArgs); err != nil {
					return []string{}, cobra.ShellCompDirectiveNoFileComp
				}
			}
		}
	}

	client, err := completionClient(cmd)
	if err != nil {
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	realmName := getFlagValueWithDefault(cmd, "realm", "default")
	spaceName := getFlagValueWithDefault(cmd, "space", "")
	stackName := getFlagValueWithDefault(cmd, "stack", "")
	cellName := getFlagValueWithDefault(cmd, "cell", "")

	if cmd.ValidArgsFunction != nil && len(args) == 0 {
		if realmName == "" {
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()

	secrets, err := client.ListSecrets(ctx, realmName, spaceName, stackName, cellName)
	if err != nil {
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}

	seen := make(map[string]bool)
	names := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		name := secret.Metadata.Name
		if toComplete == "" || strings.HasPrefix(name, toComplete) {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteProfileNames provides shell completion for `-p/--profile` by listing
// every CellProfile under the active profiles directory ($KUKE_PROFILES_DIR or
// $HOME/.kuke/profiles.d). Errors swallow to an empty list — a fresh shell with
// no profiles directory should not surface a noisy completion error.
func CompleteProfileNames(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	dir, err := cellprofile.ResolveDir()
	if err != nil {
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	profiles, err := cellprofile.List(dir)
	if err != nil {
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		name := p.Metadata.Name
		if toComplete == "" || strings.HasPrefix(name, toComplete) {
			names = append(names, name)
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
