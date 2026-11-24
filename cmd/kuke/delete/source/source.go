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

// Package source implements `kuke delete source` (issue #1643).
package source

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	teamconfig "github.com/eminwux/kukeon/pkg/team/config"
	"github.com/spf13/cobra"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewSourceCmd builds `kuke delete source NAME`. kukeond refuses it while a
// default role or harness or a stored team references the Source, and names
// them; deleting a team never deletes a Source.
func NewSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "source NAME",
		Aliases:      []string{"sources"},
		Short:        "Delete a kind: Source no default and no stored team references",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			realm, _ := cmd.Flags().GetString("realm")
			if realm = strings.TrimSpace(realm); realm == "" {
				realm = teamconfig.DefaultRealm
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			result, err := client.DeleteSource(cmd.Context(), kukeonv1.SourceRef{Name: name, Realm: realm})
			if err != nil {
				return err
			}
			if !result.Deleted {
				return fmt.Errorf("%w: source %q in realm %q", errdefs.ErrSourceNotFound, name, realm)
			}
			cmd.Printf("Deleted source %s\n", name)
			return nil
		},
	}
	cmd.Flags().String("realm", "", "Realm of the source (default: default)")
	cmd.ValidArgsFunction = config.CompleteSourceNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}
