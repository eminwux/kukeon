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
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

// tablePadding is the space between table columns.
const tablePadding = 2

func newSourceCommand(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Register the sources every team can use",
		Long: "Sources are registered in the realm's TeamDefaults (kuke get teamdefaults): a methodology " +
			"repository, for one. Its roles are named in a team as SOURCE:PATH.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newSourceAddCommand(deps), newSourceListCommand(deps), newSourceShowCommand(deps),
		newSourceRemoveCommand(deps))
	return cmd
}

func newSourceAddCommand(deps Deps) *cobra.Command {
	var conn connection
	var opts initialize.SourceOptions
	cmd := &cobra.Command{
		Use:   "add NAME --url URL",
		Short: "Register a source, or update a registered one",
		Long: "Check the URL, discover the default branch when no ref is given, fetch the source and add it " +
			"to the TeamDefaults. A source that offers roles is cloned into its role cells at --clone-path " +
			"(default /home/user/NAME).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			opts.Name, opts.Options = args[0], s.options(false)
			change, err := initialize.AddSource(cmd.Context(), opts)
			if err != nil {
				return err
			}
			c := change.Discovered
			what := fmt.Sprintf("Source %s %s: %s, %s", opts.Name, change.Action, c.Source.URL,
				strings.TrimPrefix(sourceRef(c.Source.Branch, c.Source.Tag, c.Source.Commit), " "))
			if c.Source.ClonePath != "" {
				what += ", cloned at " + c.Source.ClonePath
			}
			what += fmt.Sprintf("; offers %d roles and %d harnesses", len(c.Roles), len(c.Harnesses))
			return PrintDefaultsChange(cmd.OutOrStdout(), what, change)
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVar(&opts.URL, "url", "", "Git remote (SCP-style SSH, ssh://, or HTTPS)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Select a branch (default: the remote's default branch)")
	cmd.Flags().StringVar(&opts.Tag, "tag", "", "Select a tag")
	cmd.Flags().StringVar(&opts.Commit, "commit", "", "Select a full commit object ID")
	cmd.Flags().StringVar(&opts.ClonePath, "clone-path", "", "Where team cells clone the source")
	cmd.MarkFlagsMutuallyExclusive("branch", "tag", "commit")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newSourceListCommand(deps Deps) *cobra.Command {
	var conn connection
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the registered sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			defaults, err := initialize.Defaults(cmd.Context(), s.client)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, tablePadding, ' ', 0)
			fmt.Fprintln(w, "NAME\tURL\tREF\tCLONE PATH")
			for _, src := range defaults.Spec.Defaults.Sources {
				clone := src.ClonePath
				if clone == "" {
					clone = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", src.Name, src.URL,
					strings.TrimPrefix(sourceRef(src.Branch, src.Tag, src.Commit), " "), clone)
			}
			return w.Flush()
		},
	}
	conn.register(cmd)
	return cmd
}

func newSourceShowCommand(deps Deps) *cobra.Command {
	var conn connection
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Fetch a registered source and list the roles and harnesses it offers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			contents, err := initialize.ShowSource(cmd.Context(), s.options(false), args[0])
			if err != nil {
				return err
			}
			return printSourceContents(cmd.OutOrStdout(), args[0], contents)
		},
	}
	conn.register(cmd)
	return cmd
}

func printSourceContents(out io.Writer, name string, c *initialize.SourceContents) error {
	commit := c.Commit
	if commit == "" {
		commit = "unresolved"
	}
	if _, err := fmt.Fprintf(out, "Source %s: %s, %s at %s\n", name, c.Source.URL,
		strings.TrimPrefix(sourceRef(c.Source.Branch, c.Source.Tag, c.Source.Commit), " "), commit); err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 0, tablePadding, ' ', 0)
	fmt.Fprintln(w, "ROLE\tPATH\tHARNESSES\tTASKS\tDEFAULT")
	for _, role := range c.Roles {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", role.Name, role.Path, dash(role.Harnesses), dash(role.Tasks),
			yesNo(role.Default))
	}
	fmt.Fprintln(w, "\nHARNESS\tPATH\tDEFAULT")
	for _, h := range c.Harnesses {
		fmt.Fprintf(w, "%s\t%s\t%s\n", h.Name, h.Path, yesNo(h.Default))
	}
	return w.Flush()
}

func newSourceRemoveCommand(deps Deps) *cobra.Command {
	var conn connection
	cmd := &cobra.Command{
		Use:   "remove NAME",
		Short: "Unregister a source no default and no stored team references",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			change, err := initialize.RemoveSource(cmd.Context(), s.options(false), args[0])
			if err != nil {
				return err
			}
			return PrintDefaultsChange(cmd.OutOrStdout(), "Source "+args[0]+" removed", change)
		},
	}
	conn.register(cmd)
	return cmd
}

func sourceRef(branch, tag, commit string) string {
	switch {
	case branch != "":
		return " branch " + branch
	case tag != "":
		return " tag " + tag
	default:
		return " commit " + commit
	}
}

func dash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ",")
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
