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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

func newAddCommand(
	homeDir func() (string, error),
	run func(context.Context, initialize.AddOptions) (initialize.Result, error),
) *cobra.Command {
	var opts initialize.AddOptions
	var name string
	cmd := &cobra.Command{
		Use:   "add DIRECTORY | NAME --source REPOSITORY",
		Short: "Apply and register a project team from a directory or Git source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, name := range []string{"name", "source", "branch", "tag", "commit"} {
				if cmd.Flags().Changed(name) {
					value, _ := cmd.Flags().GetString(name)
					if strings.TrimSpace(value) == "" {
						return errors.New("add: explicit name, source, and revision flags must not be empty")
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
				return fmt.Errorf("add: cannot inspect directory: %w", statErr)
			case strings.ContainsAny(args[0], `/\`) || args[0] == "." || args[0] == "..":
				return fmt.Errorf("add: directory %q is unavailable or is not a directory", args[0])
			default:
				if opts.Source == "" {
					return errors.New("add: use an existing directory or NAME --source REPOSITORY")
				}
				opts.Name = args[0]
				if name != "" {
					opts.Name = name
				}
			}
			home, err := homeDir()
			if err != nil {
				return fmt.Errorf("resolve operator home directory: %w", err)
			}
			opts.Home, opts.Root = home, filepath.Join(home, ".kuke")
			result, err := run(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Applied %d resources; registered team %s: %s\nNo cells created or started.\n",
				result.Resources,
				strings.TrimSuffix(filepath.Base(result.EntryPath), ".yaml"),
				result.EntryPath,
			)
			if err == nil {
				err = printMissingSecrets(cmd.OutOrStdout(), result.MissingSecrets)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Source, "source", "", "Project Git remote (SCP-style SSH, ssh://, or HTTPS)")
	cmd.Flags().
		StringVar(&opts.Branch, "branch", "", "Select a branch (default: local HEAD for directories, remote HEAD for named sources)")
	cmd.Flags().StringVar(&opts.Tag, "tag", "", "Select a tag")
	cmd.Flags().StringVar(&opts.Commit, "commit", "", "Select a full commit object ID")
	cmd.Flags().StringVar(&opts.Host, "host", "", "kukeond Unix endpoint (overrides KUKEON_HOST and kuke.yaml)")
	cmd.Flags().StringVar(&opts.Configuration, "configuration", "", "kuke ClientConfiguration path")
	cmd.Flags().
		StringVar(&name, "name", "", "Override the team name (default: local Team metadata.name, then directory basename)")
	cmd.MarkFlagsMutuallyExclusive("branch", "tag", "commit")
	return cmd
}

func defaultAddCommand() *cobra.Command { return newAddCommand(os.UserHomeDir, initialize.Add) }

// printMissingSecrets reports runtime secrets the team needs but kukeon lacks;
// application already succeeded, so this is guidance, not a failure.
func printMissingSecrets(w io.Writer, missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Missing secrets (cells will not start until they exist):"); err != nil {
		return err
	}
	for _, line := range missing {
		if _, err := fmt.Fprintf(w, "  %s\n", line); err != nil {
			return err
		}
	}
	return nil
}
