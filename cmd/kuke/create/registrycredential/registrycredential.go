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

// Package registrycredential implements `kuke create registry-credential` — an
// imperative path to attach pull credentials to an existing realm's
// spec.registryCredentials without round-tripping through a hand-edited YAML
// manifest. The token enters via stdin or a file only, never argv, mirroring
// `kuke create secret --from-file`.
//
// The credential is stored as a Secret (issue #1548): the command writes a
// realm-scoped basic-auth Secret tagged as a registry identity, reads the
// realm, binds --server to that Secret (replacing whatever the realm had for
// the server — which is how an inline password written by an older kuke moves
// into a Secret), and re-applies the realm through the daemon's write-through
// apply path, which the reconciler converges as a compatible change (no realm
// recreate, no cell disruption). The realm document never carries the token.
package registrycredential

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewRegistryCredentialCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "registry-credential <realm>",
		Aliases: []string{"registry-cred", "regcred"},
		Short:   "Attach pull credentials to a realm",
		Long: "Attach private-registry pull credentials to an existing realm.\n\n" +
			"Stores the credential as a basic-auth Secret of the realm (an identity of the\n" +
			"registry connector) and binds --server to it on the realm's\n" +
			"spec.registryCredentials; the realm document never carries the token. The\n" +
			"realm is re-applied and the reconciler converges it as a compatible change (no\n" +
			"realm recreate). Re-running with the same --server rotates the Secret and\n" +
			"updates the entry in place rather than appending a duplicate; an entry that\n" +
			"still carries an inline password is replaced by the Secret-backed one.\n\n" +
			"The token is read from stdin or a file only — never from an argv flag:\n\n" +
			"  - kuke create registry-credential <realm> --server ghcr.io --username <u> --password-stdin\n" +
			"  - kuke create registry-credential <realm> --server ghcr.io --username <u> --from-file <path>\n\n" +
			"Exactly one of --password-stdin or --from-file is required.\n\n" +
			"--server is the registry host the credential applies to (e.g. ghcr.io). It\n" +
			"defaults to empty, which matches the registry extracted from the image\n" +
			"reference at pull time.\n\n" +
			"The Secret is named registry-<server> (ghcr.io becomes registry-ghcr-io) unless\n" +
			"--secret-name says otherwise.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateRegistryCredential,
	}

	cmd.Flags().
		String("server", "", "Registry server address the credential applies to (e.g. ghcr.io); empty matches the registry from the image reference")
	cmd.Flags().String("username", "", "Registry username")
	cmd.Flags().Bool("password-stdin", false, "Read the registry token from stdin (docker login style)")
	cmd.Flags().String("from-file", "", "Read the registry token from a file")
	cmd.Flags().String("secret-name", "", "Name of the Secret that stores the credential (default: registry-<server>)")

	cmd.ValidArgsFunction = config.CompleteRealmNames

	return cmd
}

func runCreateRegistryCredential(cmd *cobra.Command, args []string) error {
	flags, err := parseRegistryCredentialFlags(cmd, args)
	if err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// Resolve the realm first so the upsert merges onto the live
	// registryCredentials list rather than replacing it — the apply diff
	// overwrites the field wholesale, so any existing entries must be carried
	// forward in the desired doc.
	current, err := client.GetRealm(cmd.Context(), v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{Name: flags.realm},
	})
	if err != nil {
		return err
	}
	if !current.MetadataExists {
		return fmt.Errorf("realm %q not found", flags.realm)
	}

	// The credential itself goes into a Secret of the realm; creating one
	// that exists replaces its value, so a re-run rotates the token.
	if _, err = client.CreateSecret(cmd.Context(), connector.RegistrySecret(
		flags.realm, flags.secretName, flags.username, flags.token,
	)); err != nil {
		return fmt.Errorf("store registry credential as secret %q: %w", flags.secretName, err)
	}

	desired := current.Realm
	desired.APIVersion = v1beta1.APIVersionV1Beta1
	desired.Kind = v1beta1.KindRealm
	desired.Status = v1beta1.RealmStatus{}
	desired.Spec.RegistryCredentials = connector.UpsertRegistryCredential(
		desired.Spec.RegistryCredentials, flags.server, flags.realm, flags.secretName,
	)

	rawYAML, err := yaml.Marshal(desired)
	if err != nil {
		return fmt.Errorf("marshal realm document: %w", err)
	}

	result, err := client.ApplyDocuments(cmd.Context(), rawYAML)
	if err != nil {
		return err
	}

	return printApplyResult(cmd, flags, result)
}

type registryCredentialFlags struct {
	realm    string
	server   string
	username string
	token    string
	// secretName is the Secret the credential is stored in.
	secretName string
}

func parseRegistryCredentialFlags(cmd *cobra.Command, args []string) (registryCredentialFlags, error) {
	flags := registryCredentialFlags{}

	flags.realm = strings.TrimSpace(args[0])
	if flags.realm == "" {
		return registryCredentialFlags{}, errors.New("realm name is required")
	}

	server, err := cmd.Flags().GetString("server")
	if err != nil {
		return registryCredentialFlags{}, err
	}
	flags.server = strings.TrimSpace(server)

	username, err := cmd.Flags().GetString("username")
	if err != nil {
		return registryCredentialFlags{}, err
	}
	flags.username = strings.TrimSpace(username)
	if flags.username == "" {
		return registryCredentialFlags{}, errors.New("--username is required")
	}

	passwordStdin, err := cmd.Flags().GetBool("password-stdin")
	if err != nil {
		return registryCredentialFlags{}, err
	}

	fromFile, err := cmd.Flags().GetString("from-file")
	if err != nil {
		return registryCredentialFlags{}, err
	}

	switch {
	case passwordStdin && fromFile != "":
		return registryCredentialFlags{}, errors.New("only one of --password-stdin or --from-file may be specified")
	case passwordStdin:
		flags.token, err = readToken(cmd.InOrStdin())
		if err != nil {
			return registryCredentialFlags{}, fmt.Errorf("read token from stdin: %w", err)
		}
	case fromFile != "":
		f, openErr := os.Open(fromFile)
		if openErr != nil {
			return registryCredentialFlags{}, fmt.Errorf("read --from-file %q: %w", fromFile, openErr)
		}
		defer func() { _ = f.Close() }()
		flags.token, err = readToken(f)
		if err != nil {
			return registryCredentialFlags{}, fmt.Errorf("read --from-file %q: %w", fromFile, err)
		}
	default:
		return registryCredentialFlags{}, errors.New("exactly one of --password-stdin or --from-file is required")
	}

	if flags.token == "" {
		return registryCredentialFlags{}, errors.New("registry token is empty")
	}

	secretName, err := cmd.Flags().GetString("secret-name")
	if err != nil {
		return registryCredentialFlags{}, err
	}
	flags.secretName = strings.TrimSpace(secretName)
	if flags.secretName == "" {
		flags.secretName = connector.RegistrySecretName(flags.server)
	}

	return flags, nil
}

// readToken reads a token from r and strips trailing newlines, mirroring
// `kuke create secret --from-file` handling so a `password\n` from a file or a
// piped heredoc lands without the trailing byte.
func readToken(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

func printApplyResult(cmd *cobra.Command, flags registryCredentialFlags, result kukeonv1.ApplyDocumentsResult) error {
	server := flags.server
	if server == "" {
		server = "(default: image reference)"
	}

	for _, resource := range result.Resources {
		if resource.Kind != string(v1beta1.KindRealm) {
			continue
		}
		switch resource.Action {
		case "failed":
			if resource.Error != "" {
				return fmt.Errorf("apply realm %q: %s", resource.Name, resource.Error)
			}
			return fmt.Errorf("apply realm %q failed", resource.Name)
		default:
			cmd.Printf(
				"Registry credential for %s (user %q) stored in secret %q and applied to realm %q: %s\n",
				server, flags.username, flags.secretName, flags.realm, resource.Action,
			)
			return nil
		}
	}

	cmd.Printf(
		"Registry credential for %s (user %q) stored in secret %q and applied to realm %q\n",
		server, flags.username, flags.secretName, flags.realm,
	)
	return nil
}
