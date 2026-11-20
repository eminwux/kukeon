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

package secret

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
)

// Connector identities (issue #1548). `--connector <name>` tags the secret as
// an identity of a connector, and the connector's readable identity fields
// become flags: every field any connector in the catalog declares is one
// `--<field>` flag, generated from the catalog, so a new connector's fields
// need no code here. `--identity <field>=<value>` sets a field by name — the
// spelling for a field whose flag name is already taken by another flag.

// identityFlag maps a generated flag to the identity field it sets.
type identityFlag struct {
	flag  string
	field string
}

// fieldFlagName spells an identity field as a flag: camelCase becomes
// kebab-case (`allowedSignersRef` → `allowed-signers-ref`).
func fieldFlagName(field string) string {
	var b strings.Builder
	for i, r := range field {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// registerConnectorFlags adds --connector, --identity and one flag per
// identity field the catalog declares. It returns the generated field flags.
func registerConnectorFlags(cmd *cobra.Command, catalog *connector.Catalog) []identityFlag {
	names := catalog.Names()
	cmd.Flags().String("connector", "",
		"Tag the secret as an identity of a connector ("+strings.Join(names, ", ")+")")
	cmd.Flags().StringArray("identity", nil,
		"Set a readable identity field as FIELD=VALUE; repeatable (requires --connector)")
	_ = cmd.RegisterFlagCompletionFunc("connector",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return names, cobra.ShellCompDirectiveNoFileComp
		})

	// One flag per field; a field several connectors declare is one flag
	// whose help names them all.
	type fieldUse struct {
		help       string
		connectors []string
	}
	fields := map[string]*fieldUse{}
	var order []string
	if catalog != nil {
		for i := range catalog.Connectors {
			conn := &catalog.Connectors[i]
			for _, field := range conn.Spec.Identity.Fields {
				use, seen := fields[field.Name]
				if !seen {
					use = &fieldUse{help: field.Help}
					fields[field.Name] = use
					order = append(order, field.Name)
				}
				use.connectors = append(use.connectors, conn.Metadata.Name)
			}
		}
	}
	sort.Strings(order)
	var out []identityFlag
	for _, field := range order {
		flag := fieldFlagName(field)
		if cmd.Flags().Lookup(flag) != nil {
			continue // taken: the field is set with --identity
		}
		use := fields[field]
		help := strings.TrimSpace(use.help)
		if help != "" {
			help = ": " + help
		}
		cmd.Flags().String(flag, "",
			fmt.Sprintf("%s identity field %q%s", strings.Join(use.connectors, "/"), field, help))
		out = append(out, identityFlag{flag: flag, field: field})
	}
	return out
}

// connectorChoice is what --connector resolved to.
type connectorChoice struct {
	conn *connector.Connector
	desc v1beta1.BlueprintConnector
}

// resolveConnector reads --connector. Without it, an identity flag is an
// error: a field means nothing on an untagged secret.
func resolveConnector(
	cmd *cobra.Command, catalog *connector.Catalog, fieldFlags []identityFlag,
) (*connectorChoice, error) {
	name, err := cmd.Flags().GetString("connector")
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		if cmd.Flags().Changed("identity") {
			return nil, errors.New("--identity requires --connector")
		}
		for _, f := range fieldFlags {
			if cmd.Flags().Changed(f.flag) {
				return nil, fmt.Errorf("--%s requires --connector", f.flag)
			}
		}
		return nil, nil //nolint:nilnil // no connector is a valid choice, not an error
	}
	conn, ok := catalog.Find(name)
	if !ok {
		return nil, fmt.Errorf("--connector %q: unknown connector (known: %s)",
			name, strings.Join(catalog.Names(), ", "))
	}
	method, _ := conn.Method("")
	return &connectorChoice{conn: conn, desc: conn.Descriptor(method)}, nil
}

// secretType picks the secret type: --type when given, else the one type the
// connector accepts. A connector that accepts several types and not the
// default one needs --type.
func (c *connectorChoice) secretType(cmd *cobra.Command, fromFlag string) (string, error) {
	if c == nil || cmd.Flags().Changed("type") {
		return fromFlag, nil
	}
	accepted := c.desc.SecretTypes
	switch {
	case len(accepted) == 0 || slices.Contains(accepted, v1beta1.SecretTypeOpaque):
		return fromFlag, nil
	case len(accepted) == 1:
		return accepted[0], nil
	default:
		return "", fmt.Errorf("--connector %s requires --type (one of: %s)",
			c.desc.Name, strings.Join(accepted, ", "))
	}
}

// tag stamps the connector and its identity fields onto the secret, after
// checking them against the connector's definition: the type is one the
// connector accepts, every field is one it declares, every required field is
// set and every value reads as its declared type.
func (c *connectorChoice) tag(cmd *cobra.Command, fieldFlags []identityFlag, spec *v1beta1.SecretSpec) error {
	if c == nil {
		return nil
	}
	identity := map[string]string{}
	entries, err := cmd.Flags().GetStringArray("identity")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		field, value, ok := strings.Cut(entry, "=")
		field = strings.TrimSpace(field)
		if !ok || field == "" {
			return fmt.Errorf("--identity %q: want FIELD=VALUE", entry)
		}
		if _, dup := identity[field]; dup {
			return fmt.Errorf("--identity sets %q more than once", field)
		}
		identity[field] = strings.TrimSpace(value)
	}
	for _, f := range fieldFlags {
		if !cmd.Flags().Changed(f.flag) {
			continue
		}
		value, getErr := cmd.Flags().GetString(f.flag)
		if getErr != nil {
			return getErr
		}
		if _, dup := identity[f.field]; dup {
			return fmt.Errorf("--%s and --identity both set %q", f.flag, f.field)
		}
		identity[f.field] = strings.TrimSpace(value)
	}
	for field, value := range identity {
		if value == "" {
			delete(identity, field)
		}
	}
	spec.Connector = c.desc.Name
	if len(identity) > 0 {
		spec.Identity = identity
	}
	if checkErr := intconnector.CheckSecret(c.desc, *spec); checkErr != nil {
		return fmt.Errorf("--connector %s: %w", c.desc.Name, checkErr)
	}
	return nil
}
