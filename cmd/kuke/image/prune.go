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

package image

import (
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
)

// NewPruneCmd builds the `kuke image prune` subcommand. It reclaims dangling
// image layers and the orphaned containerd leases pinning them in the
// realm's namespace, leaving tagged images and the snapshots backing live
// cells untouched. Idempotent: a second run on a clean realm is a no-op.
func NewPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "prune",
		Short:         "Reclaim dangling image layers and orphaned leases in a realm",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			realm, err := cmd.Flags().GetString("realm")
			if err != nil {
				return err
			}
			realm = strings.TrimSpace(realm)
			if realm == "" {
				return errdefs.ErrRealmNameRequired
			}

			client := resolveClient(cmd)
			defer func() { _ = client.Close() }()

			res, err := client.PruneImages(cmd.Context(), realm)
			if err != nil {
				return err
			}

			cmd.Printf(
				"pruned realm %q (namespace %q): released %d lease(s), retained %d\n",
				res.Realm, res.Namespace, res.LeasesDeleted, res.LeasesRetained,
			)
			return nil
		},
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Target realm; the prune runs in <realm>.kukeon.io")

	return cmd
}
