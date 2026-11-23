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

// Package cli is the `kuke team` command tree: the verbs that fetch sources
// and compose teams. Reading and deleting teams use the generic verbs
// (`kuke get team`, `kuke delete team`).
package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

// Deps are what the command tree reaches outside itself: the operator's
// home directory and the daemon connection. Tests replace both.
type Deps struct {
	HomeDir func() (string, error)
	Connect func(context.Context, initialize.ConnectOptions) (initialize.Client, error)
	// Sources, when set, replaces fetching: each source name maps to its
	// definition tree (tests).
	Sources map[string]fs.FS
}

func defaultDeps() Deps {
	return Deps{
		HomeDir: os.UserHomeDir,
		Connect: func(ctx context.Context, opts initialize.ConnectOptions) (initialize.Client, error) {
			return initialize.Connect(ctx, opts)
		},
	}
}

// NewTeamCommand returns `kuke team`.
func NewTeamCommand() *cobra.Command { return NewTeamCommandWith(defaultDeps()) }

// NewTeamCommandWith returns `kuke team` with deps.
func NewTeamCommandWith(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "team",
		Short:             "Compose agent teams and apply them to kukeond",
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE:              func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	// Cobra honors SilenceUsage only on the executing command or the root, so
	// set it on each subcommand: a failed verb reports its error alone.
	for _, sub := range []*cobra.Command{
		newInitCommand(deps), newAddCommand(deps), newApplyCommand(deps),
		newSourceCommand(deps), newDefaultsCommand(deps),
	} {
		silence(sub)
		cmd.AddCommand(sub)
	}
	return cmd
}

func silence(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	for _, sub := range cmd.Commands() {
		silence(sub)
	}
}

// connection holds the endpoint flags every verb takes.
type connection struct {
	host, configuration string
}

func (c *connection) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&c.host, "host", "", "kukeond Unix endpoint (overrides KUKEON_HOST and kuke.yaml)")
	cmd.Flags().StringVar(&c.configuration, "configuration", "",
		"kuke ClientConfiguration path (default KUKE_CONFIGURATION or ~/.kuke/kuke.yaml)")
}

// session is one verb's view of the operator's environment.
type session struct {
	home    string
	client  initialize.Client
	sources map[string]fs.FS
}

// options are the team operation options for this session.
func (s *session) options(dryRun bool) initialize.Options {
	return initialize.Options{Client: s.client, Sources: s.sources, DryRun: dryRun}
}

// open resolves the home directory, prints the legacy-file note when the old
// files exist (when note is set), and dials kukeond. The caller closes the
// client.
func (c *connection) open(cmd *cobra.Command, deps Deps, note bool) (*session, error) {
	home, err := deps.HomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve operator home directory: %w", err)
	}
	if note {
		if err = printLegacyNote(cmd.OutOrStdout(), home); err != nil {
			return nil, err
		}
	}
	client, err := deps.Connect(cmd.Context(), initialize.ConnectOptions{
		Root: filepath.Join(home, ".kuke"), Host: c.host, Configuration: c.configuration,
	})
	if err != nil {
		return nil, err
	}
	return &session{home: home, client: client, sources: deps.Sources}, nil
}

func (s *session) close() { _ = s.client.Close() }
