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
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/spf13/cobra"
)

// tablePadding is the space between table columns.
const tablePadding = 2

// NewCreateSourceCommand returns `kuke create source` (#1643, D12).
func NewCreateSourceCommand() *cobra.Command { return NewCreateSourceCommandWith(defaultDeps()) }

// NewCreateSourceCommandWith returns `kuke create source` with deps.
func NewCreateSourceCommandWith(deps Deps) *cobra.Command {
	var conn connection
	var opts initialize.SourceOptions
	var identity string
	cmd := &cobra.Command{
		Use:   "source NAME --url ADDRESS",
		Short: "Store a Source: a Git repository teams name for roles, harnesses and checkouts",
		Long: "Check the address, discover the default branch when no ref is given, fetch the source with its " +
			"git identity and store it as a kind: Source in realm default. A team names it in its sources " +
			"(or as its project) and roles name it as SOURCE:PATH.\n" +
			"create fails when the Source is stored unless --force; replacing one lists the teams that use it, " +
			"which see the change when each is recomposed.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Identity = nil
			if identity != "" {
				opts.Identity = &config.SecretRef{Name: identity, Realm: config.DefaultRealm}
			}
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			dryRun := opts.DryRun
			opts.Name, opts.Options = args[0], s.options(dryRun)
			change, err := initialize.CreateSource(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return printSourceChange(cmd.OutOrStdout(), opts.Name, change, dryRun)
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVar(&opts.URL, "url", "",
		"Git address (host/owner/repo, SCP-style SSH, ssh://, or HTTPS)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Select a branch (default: the remote's default branch)")
	cmd.Flags().StringVar(&opts.Tag, "tag", "", "Select a tag")
	cmd.Flags().StringVar(&opts.Commit, "commit", "", "Select a full commit object ID")
	cmd.Flags().StringVar(&identity, "identity", "",
		"The stored git identity (a Secret in realm default) to fetch and clone the source with")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Replace a stored Source of the same name")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Fetch and check the source; store nothing")
	cmd.MarkFlagsMutuallyExclusive("branch", "tag", "commit")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func printSourceChange(out io.Writer, name string, change initialize.SourceChange, dryRun bool) error {
	c := change.Discovered
	action := change.Action
	if dryRun {
		action = "checked (dry run, nothing stored)"
	}
	what := fmt.Sprintf("Source %s %s: %s, %s", name, action, gitcred.ShortForm(change.Source.URL),
		change.Source.Revision())
	if git := change.Source.Git; git != nil && git.IdentityRef != nil {
		what += ", identity " + git.IdentityRef.Name
	}
	if _, err := fmt.Fprintf(out, "%s; offers %d roles and %d harnesses.\n", what, len(c.Roles),
		len(c.Harnesses)); err != nil {
		return err
	}
	if len(change.Users) == 0 {
		return nil
	}
	users := make([]string, 0, len(change.Users))
	teams := map[string]bool{}
	for _, use := range change.Users {
		users = append(users, use.String())
		if use.Team != "" {
			teams[use.Team] = true
		}
	}
	if _, err := fmt.Fprintf(out, "Used by: %s\n", strings.Join(users, ", ")); err != nil {
		return err
	}
	for _, team := range slices.Sorted(maps.Keys(teams)) {
		if _, err := fmt.Fprintf(out, "  kuke team apply %s\n", team); err != nil {
			return err
		}
	}
	return nil
}

// NewSourceCommand returns `kuke source`, the verbs that read a stored
// Source's repository (#1643).
func NewSourceCommand() *cobra.Command { return NewSourceCommandWith(defaultDeps()) }

// NewSourceCommandWith returns `kuke source` with deps.
func NewSourceCommandWith(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "source",
		Short:        "Inspect stored Sources (kuke create source, kuke get sources)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	show := newSourceShowCommand(deps)
	silence(show)
	cmd.AddCommand(show)
	return cmd
}

func newSourceShowCommand(deps Deps) *cobra.Command {
	var conn connection
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Fetch a stored Source and list the roles and harnesses it offers",
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
	if _, err := fmt.Fprintf(out, "Source %s: %s, %s at %s\n", name, gitcred.ShortForm(c.Source.URL),
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
