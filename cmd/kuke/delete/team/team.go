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

// Package team implements `kuke delete team` (issue #1618).
package team

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

// NewTeamCmd builds `kuke delete team NAME`: kukeond deletes the team's
// Configs, the shared blueprints no other Config references, and the Team.
// Cells and volumes stay: the command prints the cells still running from
// those Configs and the state volumes kept. The kuke system team is refused.
func NewTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "team NAME",
		Aliases:      []string{"teams"},
		Short:        "Delete a kind: Team with its Configs, leaving cells and volumes",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == teamconfig.SystemTeamName {
				return fmt.Errorf("team %s is a %w", name, errdefs.ErrTeamReserved)
			}
			realm, _ := cmd.Flags().GetString("realm")
			if realm = strings.TrimSpace(realm); realm == "" {
				realm = teamconfig.DefaultRealm
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			result, err := client.DeleteTeam(cmd.Context(), kukeonv1.TeamRef{Name: name, Realm: realm})
			for _, r := range result.Removed {
				if r.Error != "" {
					cmd.Printf("Failed to delete %s %s: %s\n", r.Kind, r.Name, r.Error)
					continue
				}
				cmd.Printf("Deleted %s %s\n", r.Kind, r.Name)
			}
			if err != nil {
				return err
			}
			cmd.Printf("Deleted team %s\n", name)
			for _, cell := range result.LiveCells {
				cmd.Printf("Still running from a deleted config: cell %s (kuke delete cell to remove it)\n", cell)
			}
			for _, volume := range result.KeptVolumes {
				cmd.Printf("Kept state volume %s (kuke delete volume to remove it)\n", volume)
			}
			return nil
		},
	}
	cmd.Flags().String("realm", "", "Realm of the team (default: default)")
	cmd.ValidArgsFunction = config.CompleteTeamNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}
