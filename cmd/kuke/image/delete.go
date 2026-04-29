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
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
)

// NewDeleteCmd builds the `kuke image delete` subcommand. The positional
// ref is required; not-found is surfaced with a friendly message that still
// unwraps to errdefs.ErrImageNotFound for callers using errors.Is.
func NewDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "delete <ref>",
		Aliases:       []string{"rm", "remove"},
		Short:         "Remove an image from a realm's containerd namespace",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			realm, err := cmd.Flags().GetString("realm")
			if err != nil {
				return err
			}
			realm = strings.TrimSpace(realm)
			if realm == "" {
				return errdefs.ErrRealmNameRequired
			}

			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errdefs.ErrImageNotFound
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			res, err := client.DeleteImage(cmd.Context(), realm, ref)
			if err != nil {
				if errors.Is(err, errdefs.ErrImageNotFound) {
					return fmt.Errorf("image %q not found in realm %q: %w", ref, realm, errdefs.ErrImageNotFound)
				}
				return err
			}

			cmd.Printf("deleted image %q from realm %q (namespace %q)\n", res.Ref, res.Realm, res.Namespace)
			return nil
		},
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Target realm; the lookup runs in <realm>.kukeon.io")

	return cmd
}
