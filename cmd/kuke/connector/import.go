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
	"fmt"
	"os"
	"sort"
	"strings"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/connector/gitimport"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
)

func newImportCmd(catalog *connector.Catalog) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Store an identity discovered on this host",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}
	cmd.AddCommand(newImportGitCmd(catalog))
	cmd.AddCommand(newImportGitHubCmd(catalog))
	return cmd
}

func newImportGitCmd(catalog *connector.Catalog) *cobra.Command {
	cmd := &cobra.Command{
		Use:   intconnector.GitIdentityTag,
		Short: "Store your git identity — SSH key and gitconfig author — as a git connector identity",
		Long: "Discover your git identity on this host and store it as one Secret tagged as an\n" +
			"identity of the git connector:\n\n" +
			"  - with an SSH key (--key, else the key git signs with, else ~/.ssh/id_ed25519,\n" +
			"    id_ecdsa, id_rsa): an ssh-key identity (default name git-ssh) that clones and\n" +
			"    pushes over --hosts (default github.com), carries the author and committer from\n" +
			"    the global gitconfig and, when commit.gpgsign / tag.gpgsign are set and\n" +
			"    user.signingkey names that key, signs. gpg.ssh.allowedSignersFile is stored as\n" +
			"    git-allowed-signers.\n" +
			"  - without one: an author-only identity (default name git-author) that sets the\n" +
			"    author and committer and authenticates nothing.\n\n" +
			"Rerunning updates the stored Secrets in place. --default also makes the identity the\n" +
			"Realm's default git identity, replacing any existing one. --dry-run prints what it\n" +
			"found and stores nothing. Key material is never printed.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runImportGit(cmd, catalog)
		},
	}
	cmd.Flags().
		String("key", "", "Private key to import (default: the key git signs with, else ~/.ssh/id_ed25519, id_ecdsa, id_rsa)")
	cmd.Flags().String("passphrase-file", "", "File holding the key's passphrase (required for an encrypted key)")
	cmd.Flags().
		String("known-hosts", "", "known_hosts file pinning host keys (default: built-in keys of github.com, gitlab.com, bitbucket.org)")
	cmd.Flags().StringSlice("hosts", nil, "Hosts or URL prefixes the key authenticates (default github.com)")
	cmd.Flags().String("name", "", "Identity secret name (default git-ssh, or git-author without a key)")
	cmd.Flags().String("realm", gitimport.DefaultRealm, "Realm the identity is stored in")
	cmd.Flags().Bool("default", false, "Also make the identity the Realm's default git identity")
	cmd.Flags().Bool("dry-run", false, "Print what discovery found; store nothing")
	return cmd
}

// importOptions reads the import's flags and the home directory to read.
func importOptions(cmd *cobra.Command) (gitimport.Options, scopedefaults.Scope, error) {
	flags := cmd.Flags()
	opts := gitimport.Options{Git: gitReader(cmd)}
	opts.Key, _ = flags.GetString("key")
	opts.PassphraseFile, _ = flags.GetString("passphrase-file")
	opts.KnownHostsFile, _ = flags.GetString("known-hosts")
	opts.Hosts, _ = flags.GetStringSlice("hosts")
	opts.Name, _ = flags.GetString("name")
	opts.Realm, _ = flags.GetString("realm")
	scope, err := scopedefaults.CheckScope(scopedefaults.Scope{Realm: opts.Realm})
	if err != nil {
		return opts, scope, err
	}
	opts.Realm = scope.Realm
	home, ok := cmd.Context().Value(MockHomeKey{}).(string)
	if !ok {
		if home, err = os.UserHomeDir(); err != nil {
			return opts, scope, fmt.Errorf("%w: find the home directory: %w", errdefs.ErrGitImport, err)
		}
	}
	opts.Home = home
	return opts, scope, nil
}

func runImportGit(cmd *cobra.Command, catalog *connector.Catalog) error {
	conn, ok := catalog.Find(intconnector.GitIdentityTag)
	if !ok {
		return fmt.Errorf("%w: the connector catalog has no git connector", errdefs.ErrConnectorInvalid)
	}
	makeDefault, _ := cmd.Flags().GetBool("default")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	opts, scope, err := importOptions(cmd)
	if err != nil {
		return err
	}
	result, err := gitimport.Import(cmd.Context(), opts)
	if err != nil {
		return err
	}
	method, _ := conn.Method("")
	if err = result.Check(conn.Descriptor(method)); err != nil {
		return err
	}
	if dryRun {
		printDryRun(cmd, result, makeDefault)
		return nil
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err = storeImport(cmd, client, result); err != nil {
		return err
	}
	if !makeDefault {
		cmd.Printf("Make it the default with: kuke connector default git %s\n", scopeName(result.Name, result.Realm))
		return nil
	}
	ref := result.Ref()
	set, err := scopedefaults.Set(cmd.Context(), client, catalog, conn, scope, "", &ref)
	if err != nil {
		return fmt.Errorf("identity stored but the realm default was not set: %w", err)
	}
	printDefaultChange(cmd, set)
	return nil
}

// storeImport stores the imported Secrets and reports each one.
func storeImport(cmd *cobra.Command, client Client, result gitimport.Result) error {
	for _, doc := range result.Secrets {
		created, err := client.CreateSecret(cmd.Context(), doc)
		if err != nil {
			return fmt.Errorf("store secret %s: %w", scopeName(doc.Metadata.Name, doc.Metadata.Realm), err)
		}
		action := "updated"
		if created.Created {
			action = "created"
		}
		cmd.Printf("Secret %q (realm %q): %s\n", doc.Metadata.Name, doc.Metadata.Realm, action)
	}
	cmd.Printf("Git identity %s (%s)\n", scopeName(result.Name, result.Realm), describeIdentity(result))
	for _, note := range result.Notes {
		cmd.Printf("Note: %s\n", note)
	}
	return nil
}

// describeIdentity is a one-line summary of what the identity gives a cell.
func describeIdentity(result gitimport.Result) string {
	if result.Type != "ssh-key" {
		return "author-only: " + result.Identity[intconnector.GitFieldAuthor]
	}
	parts := []string{"ssh-key for " + result.Identity[intconnector.GitFieldHosts]}
	if author := result.Identity[intconnector.GitFieldAuthor]; author != "" {
		parts = append(parts, "author "+author)
	}
	if sign := result.Identity[intconnector.GitFieldSign]; sign != "" {
		parts = append(parts, "signs "+sign)
	}
	return strings.Join(parts, ", ")
}

func printDryRun(cmd *cobra.Command, result gitimport.Result, makeDefault bool) {
	cmd.Println("Dry run: nothing is stored.")
	cmd.Printf("Home: %s\n", result.Home)
	cmd.Printf("Secret: %s (type %s, connector git)\n", scopeName(result.Name, result.Realm), result.Type)
	if result.KeyPath != "" {
		cmd.Printf("Key: %s\n", result.KeyPath)
	} else {
		cmd.Println("Key: none (author-only identity)")
	}
	if result.AllowedSignersPath != "" {
		cmd.Printf("Allowed signers: %s (secret %s)\n", result.AllowedSignersPath,
			scopeName(gitimport.SignersName, result.Realm))
	}
	cmd.Println("Identity:")
	fields := make([]string, 0, len(result.Identity))
	for field := range result.Identity {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		cmd.Printf("  %s: %s\n", field, result.Identity[field])
	}
	if makeDefault {
		cmd.Printf("Would set the realm %q default git identity to %s.\n", result.Realm,
			scopeName(result.Name, result.Realm))
	}
	for _, note := range result.Notes {
		cmd.Printf("Note: %s\n", note)
	}
}

// printDefaultChange reports a default written by scopedefaults.Set, naming
// the default it replaced.
func printDefaultChange(cmd *cobra.Command, set scopedefaults.SetResult) {
	switch {
	case set.Identity == nil:
		cmd.Printf("Realm %q default %s identity cleared (was %s)\n", set.Scope.Realm, set.Connector,
			describeRef(set.Previous))
	case set.Previous != nil && !intconnector.SameSecret(*set.Previous, *set.Identity):
		cmd.Printf("Realm %q default %s identity: %s (replaces %s)\n", set.Scope.Realm, set.Connector,
			describeRef(set.Identity), describeRef(set.Previous))
	default:
		cmd.Printf("Realm %q default %s identity: %s (was %s)\n", set.Scope.Realm, set.Connector,
			describeRef(set.Identity), describeRef(set.Previous))
	}
}
