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

package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/spf13/cobra"
)

// NewCreateTeamCommand returns `kuke create team` (#1643, D12).
func NewCreateTeamCommand() *cobra.Command { return NewCreateTeamCommandWith(defaultDeps()) }

// NewCreateTeamCommandWith returns `kuke create team` with deps.
//
//nolint:funlen // the help text and one line per flag
func NewCreateTeamCommandWith(deps Deps) *cobra.Command {
	var conn connection
	var opts initialize.AddOptions
	var name, file, identity string
	cmd := &cobra.Command{
		Use:   "team NAME --source ADDRESS|SOURCE | DIRECTORY",
		Short: "Create a team from a Git repository, a stored Source or a local checkout",
		Long: "Create a team: compose it and apply it to kukeond, which stores the Team with its Configs.\n\n" +
			"NAME --source ADDRESS fetches the repository and uses its kuketeam.yaml, read from the fetched " +
			"revision, when there is one; otherwise the team inherits the defaults. --source may name a stored " +
			"Source instead of an address. Without --source, -f FILE's spec.projectSource.url is the project. " +
			"DIRECTORY reads the local checkout's kuketeam.yaml, origin and branch.\n" +
			"The project and every source the declaration defines with an address are stored as Sources " +
			"(kuke get sources) unless one of the name is; the Team refers to them by name.\n" +
			"--role and --harness replace the corresponding list: a bare NAME picks the default of that name, " +
			"[NAME=]SOURCE:PATH names any role in a source the team lists or a stored Source. -f FILE replaces " +
			"the whole declaration and --no-repo-file ignores the repository's file.\n" +
			"create fails when the team is stored; use kuke team apply NAME to recompose it " +
			"or --force to replace its declaration (the recorded secret bindings are kept).\n" +
			"--dry-run prints what would be applied and writes nothing. No cells are created or started.",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, flag := range []string{"name", "source", "branch", "tag", "commit"} {
				if cmd.Flags().Changed(flag) {
					value, _ := cmd.Flags().GetString(flag)
					if strings.TrimSpace(value) == "" {
						return errors.New("create team: explicit name, source, and revision flags must not be empty")
					}
				}
			}
			opts.Directory = ""
			info, statErr := os.Stat(args[0])
			switch {
			case statErr == nil && info.IsDir():
				opts.Directory = args[0]
				opts.Name = name
			case statErr != nil && !errors.Is(statErr, os.ErrNotExist):
				return fmt.Errorf("create team: cannot inspect directory: %w", statErr)
			case strings.ContainsAny(args[0], `/\`) || args[0] == "." || args[0] == "..":
				return fmt.Errorf("create team: directory %q is unavailable or is not a directory", args[0])
			default:
				if opts.Source == "" && file == "" {
					return errors.New("create team: use an existing directory, NAME --source ADDRESS|SOURCE, " +
						"or NAME -f FILE with spec.projectSource.url")
				}
				opts.Name = args[0]
				if name != "" {
					opts.Name = name
				}
			}
			if !cmd.Flags().Changed("role") {
				opts.Roles = nil
			}
			if !cmd.Flags().Changed("harness") {
				opts.Harnesses = nil
			}
			if file != "" {
				raw, err := os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("create team: read %s: %w", file, err)
				}
				opts.File, opts.FileName = raw, file
			}
			opts.Identity = nil
			if identity != "" {
				opts.Identity = &config.SecretRef{Name: identity, Realm: config.DefaultRealm}
			}
			s, err := conn.open(cmd, deps, true)
			if err != nil {
				return err
			}
			defer s.close()
			dryRun := opts.DryRun
			opts.Options = s.options(dryRun)
			out, err := initialize.Add(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return PrintOutcome(cmd.OutOrStdout(), out)
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVar(&opts.Source, "source", "",
		"The project: a Git address (host/owner/repo, SCP-style SSH, ssh://, or HTTPS) or a stored Source's name")
	cmd.Flags().StringVar(&opts.Branch, "branch", "",
		"Select a branch (default: local HEAD for directories, remote HEAD for named sources)")
	cmd.Flags().StringVar(&opts.Tag, "tag", "", "Select a tag")
	cmd.Flags().StringVar(&opts.Commit, "commit", "", "Select a full commit object ID")
	cmd.Flags().StringVar(&name, "name", "",
		"Override the team name (default: the directory's kuketeam.yaml metadata.name, then its basename)")
	cmd.Flags().StringArrayVar(&opts.Roles, "role", nil,
		"Replace the roles: a default role's NAME or [NAME=]SOURCE:PATH (repeatable)")
	cmd.Flags().StringArrayVar(&opts.Harnesses, "harness", nil,
		"Replace the harnesses: a default harness's NAME or [NAME=]SOURCE:PATH (repeatable)")
	cmd.Flags().StringVarP(&file, "file", "f", "", "Use the Team document in FILE as the whole declaration")
	cmd.Flags().BoolVar(&opts.NoRepoFile, "no-repo-file", false, "Ignore the repository's kuketeam.yaml")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Replace a stored team of the same name")
	cmd.Flags().StringVar(&identity, "identity", "",
		"The stored git identity (a Secret in realm default) of the project's Source, when this command creates it")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Print what would be applied; write nothing")
	cmd.MarkFlagsMutuallyExclusive("branch", "tag", "commit")
	cmd.MarkFlagsMutuallyExclusive("file", "no-repo-file")
	return cmd
}
