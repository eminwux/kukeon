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

package connector

import (
	"errors"
	"fmt"
	"strings"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
)

// maxDefaultArgs is the connector and the identity.
const maxDefaultArgs = 2

func newDefaultCmd(catalog *connector.Catalog) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "default <connector> [<identity>[@realm[/space[/stack]]]]",
		Short: "Set or clear a Realm's default identity for a connector",
		Long: "Set the identity a Realm gives every Config in it that chooses none — what the\n" +
			"Connectors page's default picker does. The Secret must exist and be an identity of\n" +
			"the connector. --clear removes the default.\n\n" +
			"  kuke connector default git git-ssh              realm default (the identity's realm)\n" +
			"  kuke connector default git git-ssh --realm dev  realm dev, identity git-ssh@dev\n" +
			"  kuke connector default git --clear\n\n" +
			"The Realm is --realm, else the realm the identity names, else default. A connector\n" +
			"with several methods (a token or a key) needs --method.",
		Args:          cobra.RangeArgs(1, maxDefaultArgs),
		SilenceUsage:  true,
		SilenceErrors: false,
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return catalog.Names(), cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDefault(cmd, catalog, args)
		},
	}
	cmd.Flags().String("realm", "", "Realm whose default is set (default: the identity's realm, else default)")
	cmd.Flags().String("method", "", "Connector method the default is for, when it has several")
	cmd.Flags().Bool("clear", false, "Remove the Realm's default instead of setting one")
	return cmd
}

func runDefault(cmd *cobra.Command, catalog *connector.Catalog, args []string) error {
	name := strings.TrimSpace(args[0])
	conn, ok := catalog.Find(name)
	if !ok {
		return fmt.Errorf("%w: unknown connector %q (known: %s)",
			errdefs.ErrConnectorInvalid, name, strings.Join(catalog.Names(), ", "))
	}
	clearing, _ := cmd.Flags().GetBool("clear")
	realm, _ := cmd.Flags().GetString("realm")
	method, _ := cmd.Flags().GetString("method")
	realm = strings.TrimSpace(realm)

	var identity *v1beta1.ContainerSecretRef
	switch {
	case clearing && len(args) == maxDefaultArgs:
		return errors.New("--clear takes no identity")
	case !clearing && len(args) < maxDefaultArgs:
		return errors.New("name the identity to make the default, or pass --clear")
	case !clearing:
		base := realm
		if base == "" {
			base = consts.KukeonDefaultRealmName
		}
		ref, err := intconnector.ParseSecretRef(args[1], v1beta1.ContainerSecretRef{Realm: base})
		if err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
		}
		identity = &ref
		if realm == "" {
			realm = ref.Realm
		}
	}
	if realm == "" {
		realm = consts.KukeonDefaultRealmName
	}
	scope, err := scopedefaults.CheckScope(scopedefaults.Scope{Realm: realm})
	if err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	set, err := scopedefaults.Set(cmd.Context(), client, catalog, conn, scope, method, identity)
	if err != nil {
		return err
	}
	printDefaultChange(cmd, set)
	return nil
}
