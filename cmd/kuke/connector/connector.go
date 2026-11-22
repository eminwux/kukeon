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

// Package connector hosts `kuke connector` (issue #1591): list the connectors
// and their identities, import the operator's git identity from the host, and
// set the default identity a Realm gives every cell in it. The import and the
// default edit are the same code the web UI's Connectors page runs
// (internal/connector/gitimport and internal/connector/scopedefaults).
package connector

import (
	"context"
	"io"
	"strings"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/connector/gitimport"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
)

// MockControllerKey injects a Client via context for tests.
type MockControllerKey struct{}

// MockGitKey injects a gitimport.GitReader via context for tests.
type MockGitKey struct{}

// MockHomeKey injects the home directory the import reads, for tests.
type MockHomeKey struct{}

// Client is the part of the daemon client `kuke connector` uses.
type Client interface {
	io.Closer
	scopedefaults.Client
	ListRealms(context.Context) ([]v1beta1.RealmDoc, error)
	ListSecrets(ctx context.Context, realm, space, stack, cell string) ([]v1beta1.SecretDoc, error)
	CreateSecret(context.Context, v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error)
}

var _ Client = kukeonv1.Client(nil)

// NewConnectorCmd builds `kuke connector` over the connector catalog shipped
// with kuke.
func NewConnectorCmd() *cobra.Command {
	catalog, err := connector.Default()
	if err != nil {
		// The embedded catalog is validated by its own tests.
		catalog = &connector.Catalog{}
	}
	return NewConnectorCmdWithCatalog(catalog)
}

// NewConnectorCmdWithCatalog builds `kuke connector` over a connector catalog.
func NewConnectorCmdWithCatalog(catalog *connector.Catalog) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connector",
		Short: "List connectors, import a git identity and set default identities",
		Long: "A connector is a kind of access a cell needs — a harness login, an API token, a git\n" +
			"identity, a registry credential. Its identities are Secrets tagged with the connector,\n" +
			"and a Realm, Space or Stack default names the identity every cell in it uses.\n\n" +
			"  kuke connector list                         connectors, identities and realm defaults\n" +
			"  kuke connector import git [--default]       store your git identity from this host\n" +
			"  kuke connector default git <name[@realm]>   set a realm's default git identity",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}
	cmd.AddCommand(newListCmd(catalog))
	cmd.AddCommand(newImportCmd(catalog))
	cmd.AddCommand(newDefaultCmd(catalog))
	return cmd
}

// resolveClient returns the daemon client: an injected one in tests, else a
// dial of --host / KUKEON_HOST.
func resolveClient(cmd *cobra.Command) (Client, error) {
	if mock, ok := cmd.Context().Value(MockControllerKey{}).(Client); ok {
		return mock, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

// gitReader returns the global git configuration reader.
func gitReader(cmd *cobra.Command) gitimport.GitReader {
	if mock, ok := cmd.Context().Value(MockGitKey{}).(gitimport.GitReader); ok {
		return mock
	}
	return gitimport.CommandGit{}
}

// describeRef renders an identity reference, "none" for nil.
func describeRef(ref *v1beta1.ContainerSecretRef) string {
	if ref == nil {
		return "none"
	}
	return scopeName(ref.Name, ref.Realm, ref.Space, ref.Stack, ref.Cell)
}

// scopeName renders `name@realm[/space[/stack[/cell]]]`.
func scopeName(name, realm string, deeper ...string) string {
	return name + "@" + scopePath(realm, deeper...)
}

// scopePath renders `realm[/space[/stack[/cell]]]`, stopping at the first
// unset coordinate.
func scopePath(realm string, deeper ...string) string {
	parts := []string{realm}
	for _, part := range deeper {
		if part == "" {
			break
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "/")
}
