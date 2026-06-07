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
// `kuke create secret --from-file`. The command reads the realm, upserts the
// credential keyed by --server, and re-applies the realm through the daemon's
// write-through apply path, which the reconciler converges as a compatible
// change (no realm recreate, no cell disruption).
package registrycredential

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
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
			"Upserts an entry on the realm's spec.registryCredentials keyed by --server\n" +
			"and re-applies the realm; the reconciler converges it as a compatible change\n" +
			"(no realm recreate). Re-running with the same --server updates the entry in\n" +
			"place rather than appending a duplicate.\n\n" +
			"The token is read from stdin or a file only — never from an argv flag:\n\n" +
			"  - kuke create registry-credential <realm> --server ghcr.io --username <u> --password-stdin\n" +
			"  - kuke create registry-credential <realm> --server ghcr.io --username <u> --from-file <path>\n\n" +
			"Exactly one of --password-stdin or --from-file is required.\n\n" +
			"--server is the registry host the credential applies to (e.g. ghcr.io). It\n" +
			"defaults to empty, which matches the registry extracted from the image\n" +
			"reference at pull time.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateRegistryCredential,
	}

	cmd.Flags().String("server", "", "Registry server address the credential applies to (e.g. ghcr.io); empty matches the registry from the image reference")
	cmd.Flags().String("username", "", "Registry username")
	cmd.Flags().Bool("password-stdin", false, "Read the registry token from stdin (docker login style)")
	cmd.Flags().String("from-file", "", "Read the registry token from a file")

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

	desired := current.Realm
	desired.APIVersion = v1beta1.APIVersionV1Beta1
	desired.Kind = v1beta1.KindRealm
	desired.Status = v1beta1.RealmStatus{}
	desired.Spec.RegistryCredentials = upsertCredential(
		desired.Spec.RegistryCredentials,
		v1beta1.RegistryCredentials{
			Username:      flags.username,
			Password:      flags.token,
			ServerAddress: flags.server,
		},
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

// upsertCredential replaces an existing entry whose ServerAddress matches the
// incoming credential's ServerAddress (username + password updated), or appends
// a new entry when no match is found. Matching is exact on ServerAddress so the
// empty-server default ("matches by image reference") upserts against itself.
func upsertCredential(existing []v1beta1.RegistryCredentials, cred v1beta1.RegistryCredentials) []v1beta1.RegistryCredentials {
	for i := range existing {
		if existing[i].ServerAddress == cred.ServerAddress {
			existing[i] = cred
			return existing
		}
	}
	return append(existing, cred)
}

type registryCredentialFlags struct {
	realm    string
	server   string
	username string
	token    string
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
				"Registry credential for %s (user %q) applied to realm %q: %s\n",
				server, flags.username, flags.realm, resource.Action,
			)
			return nil
		}
	}

	cmd.Printf(
		"Registry credential for %s (user %q) applied to realm %q\n",
		server, flags.username, flags.realm,
	)
	return nil
}
