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
	"io"
	"os"
	"sort"
	"strings"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/connector/githubtoken"
	"github.com/eminwux/kukeon/internal/connector/gitimport"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
)

// maxTokenBytes bounds a token read from stdin or a file.
const maxTokenBytes = 64 << 10

// flagGit is --git: it also stores the token as an identity of the git
// connector, hence its name.
const flagGit = intconnector.GitIdentityTag

// tokenEnv is the environment variable the github connector delivers its
// token as, read from the catalog.
func tokenEnv(catalog *connector.Catalog) string {
	conn, ok := catalog.Find(intconnector.GitHubIdentityTag)
	if !ok {
		return "its environment variable"
	}
	if method, _ := conn.Method(""); method.Secret != nil && method.Secret.EnvName != "" {
		return method.Secret.EnvName
	}
	return "its environment variable"
}

func newImportGitHubCmd(catalog *connector.Catalog) *cobra.Command {
	cmd := &cobra.Command{
		Use:   intconnector.GitHubIdentityTag,
		Short: "Store a GitHub token as a github identity and, with --git, as a git identity for github.com",
		Long: "Store a GitHub personal access token, read from --token-stdin or --token-file, as an\n" +
			"identity of the github connector (default name " + githubtoken.DefaultName + "): cells get it as " +
			tokenEnv(catalog) + ".\n\n" +
			"--git also stores the same token as a git identity (default name " + githubtoken.DefaultGitName + "): a\n" +
			"basic-auth Secret with username " + intconnector.GitHubTokenUsername + " that clones and pushes over HTTPS on\n" +
			"--hosts only (default " + intconnector.GitHubTokenHost + "). GitHub accepts any non-empty username with a token.\n" +
			"The git identity records the github identity it came from, so rerunning with --git\n" +
			"rotates both. Without --git, git is unchanged: the API token is never a git credential\n" +
			"unless asked for, and a git identity already linked to the github identity keeps its\n" +
			"previous token (the command warns). Signing needs an SSH key: use\n" +
			"`kuke connector import git` for that.\n\n" +
			"--default makes each stored identity its connector's default in --realm. --dry-run\n" +
			"prints what would be stored and stores nothing. The token is never printed.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runImportGitHub(cmd, catalog)
		},
	}
	flags := cmd.Flags()
	flags.Bool("token-stdin", false, "Read the token from standard input")
	flags.String("token-file", "", "Read the token from this file")
	flags.Bool(flagGit, false, "Also store the token as a git identity for --hosts")
	flags.String("author", "", "Commit author of the git identity, as `Name <email>`")
	flags.String("username", "", "Username of the git identity (default "+intconnector.GitHubTokenUsername+")")
	flags.StringSlice("hosts", nil, "Hosts or URL prefixes the git identity authenticates (default "+
		intconnector.GitHubTokenHost+")")
	flags.String("name", "", "github identity name (default "+githubtoken.DefaultName+")")
	flags.String("git-name", "", "git identity name (default: the linked one, else "+githubtoken.DefaultGitName+")")
	flags.String("realm", gitimport.DefaultRealm, "Realm the identities are stored in")
	flags.Bool("default", false, "Also make each identity its connector's default in the Realm")
	flags.Bool("dry-run", false, "Print what would be stored; store nothing")
	return cmd
}

// readToken reads the token from stdin or a file: exactly one is required.
func readToken(cmd *cobra.Command) (string, error) {
	fromStdin, _ := cmd.Flags().GetBool("token-stdin")
	file, _ := cmd.Flags().GetString("token-file")
	var reader io.Reader
	switch {
	case fromStdin && file != "":
		return "", fmt.Errorf("%w: use --token-stdin or --token-file, not both", errdefs.ErrGitHubToken)
	case fromStdin:
		reader = cmd.InOrStdin()
	case file != "":
		f, err := os.Open(file)
		if err != nil {
			return "", fmt.Errorf("%w: read the token file: %w", errdefs.ErrGitHubToken, err)
		}
		defer func() { _ = f.Close() }()
		reader = f
	default:
		return "", fmt.Errorf("%w: pass the token with --token-stdin or --token-file", errdefs.ErrGitHubToken)
	}
	raw, err := io.ReadAll(io.LimitReader(reader, maxTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: read the token: %w", errdefs.ErrGitHubToken, err)
	}
	if len(raw) > maxTokenBytes {
		return "", fmt.Errorf("%w: the token is longer than %d bytes", errdefs.ErrGitHubToken, maxTokenBytes)
	}
	return strings.TrimSpace(string(raw)), nil
}

// gitHubOptions reads the flags into a request.
func gitHubOptions(cmd *cobra.Command, token string) (githubtoken.Options, error) {
	flags := cmd.Flags()
	realm, _ := flags.GetString("realm")
	scope, err := scopedefaults.CheckScope(scopedefaults.Scope{Realm: realm})
	if err != nil {
		return githubtoken.Options{}, err
	}
	opts := githubtoken.Options{Scope: scope, Token: token}
	opts.Name, _ = flags.GetString("name")
	withGit, _ := flags.GetBool(flagGit)
	gitOnly := []string{"author", "username", "hosts", "git-name"}
	if !withGit {
		for _, name := range gitOnly {
			if flags.Changed(name) {
				return opts, fmt.Errorf("%w: --%s needs --git", errdefs.ErrGitHubToken, name)
			}
		}
		return opts, nil
	}
	git := &githubtoken.Git{}
	git.Name, _ = flags.GetString("git-name")
	git.Author, _ = flags.GetString("author")
	git.Username, _ = flags.GetString("username")
	git.Hosts, _ = flags.GetStringSlice("hosts")
	opts.Git = git
	return opts, nil
}

func runImportGitHub(cmd *cobra.Command, catalog *connector.Catalog) error {
	makeDefault, _ := cmd.Flags().GetBool("default")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	token, err := readToken(cmd)
	if err != nil {
		return err
	}
	opts, err := gitHubOptions(cmd, token)
	if err != nil {
		return err
	}
	if dryRun {
		// No daemon is reached: a linked git identity cannot be looked up, so
		// the dry run names the identity a first store would write.
		plan, buildErr := githubtoken.Build(catalog, opts, nil)
		if buildErr != nil {
			return buildErr
		}
		printTokenDryRun(cmd, plan, makeDefault)
		return nil
	}
	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	// With --git the linked git identity is rotated; without, it is looked up
	// only to warn that it keeps the old token (issue #1707), so a failed
	// lookup is reported and the token is stored all the same.
	existing, err := githubtoken.Existing(cmd.Context(), client, opts.Scope)
	if err != nil {
		if opts.Git != nil {
			return err
		}
		cmd.PrintErrf("Warning: could not look for a git identity linked to the github identity: %v\n", err)
	}
	plan, err := githubtoken.Build(catalog, opts, existing)
	if err != nil {
		return err
	}
	// What was stored is printed even when a later step fails: the error, a
	// *githubtoken.PartialError, names what was not.
	result, err := githubtoken.Store(cmd.Context(), client, catalog, plan, makeDefault)
	printTokenResult(cmd, result, tokenEnv(catalog))
	if stale := result.StaleGit; stale != nil {
		cmd.PrintErrf("Warning: git identity %s still holds the previous token: it is linked to github "+
			"identity %s and was not rotated. Rerun with --%s to rotate it too.\n",
			scopeName(stale.Name, stale.Realm), scopeName(plan.Token.Metadata.Name, plan.Scope.Realm), flagGit)
	}
	if err != nil {
		return redact(err, plan.Material())
	}
	if !makeDefault {
		for _, stored := range result.Secrets {
			cmd.Printf("Make it the default with: kuke connector default %s %s\n",
				stored.Connector, scopeName(stored.Name, stored.Scope.Realm))
		}
	}
	return nil
}

// redact masks material out of an error a daemon may have built from a
// request.
func redact(err error, material []string) error {
	message := err.Error()
	for _, secret := range material {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	if message == err.Error() {
		return err
	}
	return &redactedError{err: err, message: message}
}

type redactedError struct {
	err     error
	message string
}

func (e *redactedError) Error() string { return e.message }
func (e *redactedError) Unwrap() error { return e.err }

func printTokenResult(cmd *cobra.Command, result githubtoken.Result, env string) {
	for _, stored := range result.Secrets {
		cmd.Printf("Secret %q (realm %q): %s\n", stored.Name, stored.Scope.Realm, stored.Action)
		if stored.Connector == intconnector.GitIdentityTag {
			cmd.Printf("Git identity %s (basic-auth for %s, token from %s)\n",
				scopeName(stored.Name, stored.Scope.Realm), stored.Identity[intconnector.GitFieldHosts],
				stored.Identity[intconnector.IdentityTokenFrom])
		} else {
			cmd.Printf("GitHub identity %s (%s)\n", scopeName(stored.Name, stored.Scope.Realm), env)
		}
	}
	for _, set := range result.Defaults {
		printDefaultChange(cmd, scopedefaults.SetResult{
			Connector: set.Connector, Method: set.Method, Scope: set.Scope, Level: set.Level,
			Identity: set.Identity, Previous: set.Previous, Action: set.Action,
		})
	}
}

func printTokenDryRun(cmd *cobra.Command, plan githubtoken.Plan, makeDefault bool) {
	cmd.Println("Dry run: nothing is stored.")
	docs := []v1beta1.SecretDoc{plan.Token}
	if plan.Git != nil {
		docs = append(docs, *plan.Git)
	}
	for _, doc := range docs {
		secretType := doc.Spec.Type
		if secretType == "" {
			secretType = v1beta1.SecretTypeOpaque
		}
		cmd.Printf("Secret: %s (type %s, connector %s)\n",
			scopeName(doc.Metadata.Name, doc.Metadata.Realm), secretType, doc.Spec.Connector)
		if doc.Spec.BasicAuth != nil {
			cmd.Printf("  username: %s\n", doc.Spec.BasicAuth.Username)
		}
		fields := make([]string, 0, len(doc.Spec.Identity))
		for field := range doc.Spec.Identity {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			cmd.Printf("  %s: %s\n", field, doc.Spec.Identity[field])
		}
	}
	if plan.Git != nil {
		cmd.Println("Note: a git identity already linked to the github identity is rotated in place; " +
			"the dry run does not look it up.")
	}
	if makeDefault {
		for _, doc := range docs {
			cmd.Printf("Would set the realm %q default %s identity to %s.\n", plan.Scope.Realm,
				doc.Spec.Connector, scopeName(doc.Metadata.Name, doc.Metadata.Realm))
		}
	}
}
