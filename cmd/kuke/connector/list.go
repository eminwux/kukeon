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
	"slices"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
)

func newListCmd(catalog *connector.Catalog) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List connectors, their identities and the identity each Realm default names",
		Long: "List every connector kuke knows, the Secrets tagged as its identities (name, scope,\n" +
			"type) and, for each Realm, the identity its default names. Secret material is never read.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			secrets, err := client.ListSecrets(cmd.Context(), "", "", "", "")
			if err != nil {
				return err
			}
			realms, err := client.ListRealms(cmd.Context())
			if err != nil {
				return err
			}
			printIdentities(cmd, catalog, secrets)
			cmd.Println()
			printRealmDefaults(cmd, catalog, realms)
			return nil
		},
	}
}

// printIdentities prints one row per identity, grouped by connector in
// catalog order; a connector with none gets a row of dashes. Secrets tagged
// with a connector the catalog lacks follow, so none is hidden.
func printIdentities(cmd *cobra.Command, catalog *connector.Catalog, secrets []v1beta1.SecretDoc) {
	byConnector := map[string][]v1beta1.SecretDoc{}
	for _, secret := range secrets {
		if tag := strings.TrimSpace(secret.Spec.Connector); tag != "" {
			byConnector[tag] = append(byConnector[tag], secret)
		}
	}
	names := catalog.Names()
	var unknown []string
	for tag := range byConnector {
		if !slices.Contains(names, tag) {
			unknown = append(unknown, tag)
		}
	}
	sort.Strings(unknown)
	var rows [][]string
	for _, name := range append(slices.Clone(names), unknown...) {
		identities := byConnector[name]
		sort.Slice(identities, func(i, j int) bool {
			a, b := identities[i].Metadata, identities[j].Metadata
			return scopeName(a.Name, a.Realm, a.Space, a.Stack, a.Cell) <
				scopeName(b.Name, b.Realm, b.Space, b.Stack, b.Cell)
		})
		if len(identities) == 0 {
			rows = append(rows, []string{name, "-", "-", "-"})
			continue
		}
		for _, secret := range identities {
			md := secret.Metadata
			scope := scopePath(md.Realm, md.Space, md.Stack, md.Cell)
			secretType := secret.Spec.Type
			if secretType == "" {
				secretType = v1beta1.SecretTypeOpaque
			}
			rows = append(rows, []string{name, md.Name, scope, secretType})
		}
	}
	cmd.Println("IDENTITIES")
	shared.PrintTable(cmd, []string{"CONNECTOR", "IDENTITY", "SCOPE", "TYPE"}, rows)
}

// printRealmDefaults prints the identity each Realm's default names for each
// connector method that takes one.
func printRealmDefaults(cmd *cobra.Command, catalog *connector.Catalog, realms []v1beta1.RealmDoc) {
	sort.Slice(realms, func(i, j int) bool { return realms[i].Metadata.Name < realms[j].Metadata.Name })
	var rows [][]string
	for _, target := range scopedefaults.Targets(catalog) {
		for _, realm := range realms {
			ref := target.Read(realm.Spec.Defaults)
			if ref == nil {
				continue
			}
			resolved := *ref
			if strings.TrimSpace(resolved.Realm) == "" {
				resolved.Realm = realm.Metadata.Name
			}
			method := target.Method.Name
			if method == "" {
				method = "-"
			}
			rows = append(
				rows,
				[]string{target.Conn.Metadata.Name, method, realm.Metadata.Name, describeRef(&resolved)},
			)
		}
	}
	cmd.Println("REALM DEFAULTS")
	if len(rows) == 0 {
		cmd.Println("No realm sets a default identity.")
		return
	}
	shared.PrintTable(cmd, []string{"CONNECTOR", "METHOD", "REALM", "IDENTITY"}, rows)
}
