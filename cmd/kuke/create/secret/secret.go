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
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewSecretCmd builds `kuke create secret` over the connector catalog embedded
// in kuke.
func NewSecretCmd() *cobra.Command {
	catalog, err := connector.Default()
	if err != nil {
		// The embedded catalog is validated by its own tests; a broken one
		// leaves the command usable for plain secrets.
		catalog = &connector.Catalog{}
	}
	return NewSecretCmdWithCatalog(catalog)
}

// NewSecretCmdWithCatalog builds `kuke create secret` over a connector
// catalog: `--connector` accepts its connectors and their identity fields
// become flags.
func NewSecretCmdWithCatalog(catalog *connector.Catalog) *cobra.Command {
	var fieldFlags []identityFlag
	cmd := &cobra.Command{
		Use:   "secret [name]",
		Short: "Create a secret within a scope",
		Long: "Create a secret within a scope. --type selects the kind of material:\n\n" +
			"  opaque (default) — raw bytes, injected through containers[].secrets[]:\n" +
			"    kuke create secret <name> --from-literal=KEY=VAL    (inline value, repeatable)\n" +
			"    kuke create secret <name> --from-file=<path>        (value read from a file)\n\n" +
			"  basic-auth — HTTPS git credential (username + password or token):\n" +
			"    kuke create secret <name> --type basic-auth --username <user> --password-stdin\n" +
			"    kuke create secret <name> --type basic-auth --username <user> --token-file <path>\n\n" +
			"  ssh-key — SSH git credential and signing key (any OpenSSH key type):\n" +
			"    kuke create secret <name> --type ssh-key --from-file ~/.ssh/id_ed25519 \\\n" +
			"        [--passphrase-stdin | --passphrase-file <path>] [--public-key-file <path>] \\\n" +
			"        [--certificate-file <path>] [--known-hosts-file <path>]\n\n" +
			"Typed secrets are referenced from git.credentials[] and git.signing; their material is\n" +
			"never echoed back, put in an environment variable, or embedded in a URL.\n\n" +
			"--connector tags the secret as an identity of a connector — a kind of access a cell\n" +
			"needs (a harness login, an API token, a git identity, a registry credential) — so it\n" +
			"can be chosen by name for a team, a CellConfig or a single cell. The connector's\n" +
			"readable identity fields are flags of their own, or --identity FIELD=VALUE:\n" +
			"    kuke create secret <name> --connector <connector> --from-file ./token\n" +
			"    kuke create secret <name> --connector <connector> --type ssh-key \\\n" +
			"        --from-file ~/.ssh/id_ed25519 --identity FIELD=VALUE\n" +
			"Connectors: " + strings.Join(catalog.Names(), ", ") + ".\n" +
			"The tag and the identity fields are readable (`kuke get secret`); the material never is.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreateSecret(cmd, args, catalog, fieldFlags)
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the secret")
	_ = viper.BindPFlag(config.KUKE_CREATE_SECRET_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the secret")
	_ = viper.BindPFlag(config.KUKE_CREATE_SECRET_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the secret")
	_ = viper.BindPFlag(config.KUKE_CREATE_SECRET_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().StringArray("from-literal", nil,
		"Specify a key-value pair as KEY=VAL; repeatable to provide multiple values (joined by newline)")
	cmd.Flags().String("from-file", "",
		"Read the secret value from a file (opaque: the value; basic-auth: the password; ssh-key: the private key)")
	cmd.Flags().String("type", v1beta1.SecretTypeOpaque, "Secret type: opaque, basic-auth or ssh-key")
	cmd.Flags().String("username", "", "basic-auth: the HTTP user")
	cmd.Flags().Bool("password-stdin", false, "basic-auth: read the password from stdin")
	cmd.Flags().Bool("token-stdin", false, "basic-auth: read a token from stdin")
	cmd.Flags().String("token-file", "", "basic-auth: read a token from a file")
	cmd.Flags().Bool("passphrase-stdin", false, "ssh-key: read the private key's passphrase from stdin")
	cmd.Flags().String("passphrase-file", "", "ssh-key: read the private key's passphrase from a file")
	cmd.Flags().String("public-key-file", "", "ssh-key: the matching public key file")
	cmd.Flags().String("certificate-file", "", "ssh-key: an OpenSSH user certificate for the key")
	cmd.Flags().String("known-hosts-file", "", "ssh-key: pinned host keys in known_hosts format")

	fieldFlags = registerConnectorFlags(cmd, catalog)

	// Register autocomplete functions for flags
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runCreateSecret(
	cmd *cobra.Command, args []string, catalog *connector.Catalog, fieldFlags []identityFlag,
) error {
	flags, err := parseCreateSecretFlags(cmd, args, catalog, fieldFlags)
	if err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	doc := v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSecret,
		Metadata: v1beta1.SecretMetadata{
			Name:  flags.name,
			Realm: flags.realm,
			Space: strings.TrimSpace(flags.space),
			Stack: strings.TrimSpace(flags.stack),
		},
		Spec: flags.spec,
	}

	result, err := client.CreateSecret(cmd.Context(), doc)
	if err != nil {
		return err
	}

	printSecretResult(cmd, result)
	return nil
}

type createSecretFlags struct {
	name  string
	realm string
	space string
	stack string
	spec  v1beta1.SecretSpec
}

func parseCreateSecretFlags(
	cmd *cobra.Command, args []string, catalog *connector.Catalog, fieldFlags []identityFlag,
) (createSecretFlags, error) {
	flags := createSecretFlags{}

	name, err := shared.RequireNameArgOrDefault(
		cmd,
		args,
		"secret",
		viper.GetString(config.KUKE_CREATE_SECRET_NAME.ViperKey),
	)
	if err != nil {
		return createSecretFlags{}, err
	}
	flags.name = name

	flags.realm = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_SECRET_REALM.ViperKey))
	if flags.realm == "" {
		flags.realm = strings.TrimSpace(config.KUKE_CREATE_SECRET_REALM.ValueOrDefault())
	}

	flags.space = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_SECRET_SPACE.ViperKey))
	if flags.space == "" {
		flags.space = strings.TrimSpace(config.KUKE_CREATE_SECRET_SPACE.ValueOrDefault())
	}

	flags.stack = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_SECRET_STACK.ViperKey))
	if flags.stack == "" {
		flags.stack = strings.TrimSpace(config.KUKE_CREATE_SECRET_STACK.ValueOrDefault())
	}

	choice, err := resolveConnector(cmd, catalog, fieldFlags)
	if err != nil {
		return createSecretFlags{}, err
	}
	secretType, err := cmd.Flags().GetString("type")
	if err != nil {
		return createSecretFlags{}, err
	}
	if secretType, err = choice.secretType(cmd, secretType); err != nil {
		return createSecretFlags{}, err
	}
	switch strings.TrimSpace(secretType) {
	case "", v1beta1.SecretTypeOpaque:
		flags.spec, err = opaqueSpec(cmd)
	case v1beta1.SecretTypeBasicAuth:
		flags.spec, err = basicAuthSpec(cmd)
	case v1beta1.SecretTypeSSHKey:
		flags.spec, err = sshKeySpec(cmd)
	default:
		err = fmt.Errorf("--type %q: want opaque, basic-auth or ssh-key", secretType)
	}
	if err != nil {
		return createSecretFlags{}, err
	}
	if err = choice.tag(cmd, fieldFlags, &flags.spec); err != nil {
		return createSecretFlags{}, err
	}
	return flags, nil
}

// rejectFlags errors when a flag that belongs to another secret type is set, so
// a mistyped --type never silently drops material.
func rejectFlags(cmd *cobra.Command, secretType string, names ...string) error {
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			return fmt.Errorf("--%s does not apply to --type %s", name, secretType)
		}
	}
	return nil
}

func opaqueSpec(cmd *cobra.Command) (v1beta1.SecretSpec, error) {
	if err := rejectFlags(cmd, v1beta1.SecretTypeOpaque,
		"username", "password-stdin", "token-stdin", "token-file", "passphrase-stdin",
		"passphrase-file", "public-key-file", "certificate-file", "known-hosts-file"); err != nil {
		return v1beta1.SecretSpec{}, err
	}
	literals, err := cmd.Flags().GetStringArray("from-literal")
	if err != nil {
		return v1beta1.SecretSpec{}, err
	}

	fromFile, err := cmd.Flags().GetString("from-file")
	if err != nil {
		return v1beta1.SecretSpec{}, err
	}

	if len(literals) == 0 && fromFile == "" {
		return v1beta1.SecretSpec{}, errors.New("at least one of --from-literal or --from-file is required")
	}

	var parts []string
	for _, lit := range literals {
		lit = strings.TrimSpace(lit)
		if lit == "" {
			continue
		}
		// Accept KEY=VAL format; store only the value part
		if idx := strings.Index(lit, "="); idx >= 0 {
			parts = append(parts, lit[idx+1:])
		} else {
			parts = append(parts, lit)
		}
	}

	var data string
	if fromFile != "" {
		raw, readErr := os.ReadFile(fromFile)
		if readErr != nil {
			return v1beta1.SecretSpec{}, fmt.Errorf("read --from-file %q: %w", fromFile, readErr)
		}
		data = strings.TrimRight(string(raw), "\n")
	}

	// Append literal values after file content (if both specified)
	if len(parts) > 0 {
		if data != "" {
			data += "\n"
		}
		data += strings.Join(parts, "\n")
	}

	return v1beta1.SecretSpec{Data: data}, nil
}

// basicAuthSpec builds a basic-auth secret from --username plus exactly one
// password or token source. The value never appears on the command line.
func basicAuthSpec(cmd *cobra.Command) (v1beta1.SecretSpec, error) {
	if err := rejectFlags(cmd, v1beta1.SecretTypeBasicAuth,
		"from-literal", "passphrase-stdin", "passphrase-file", "public-key-file",
		"certificate-file", "known-hosts-file"); err != nil {
		return v1beta1.SecretSpec{}, err
	}
	username, _ := cmd.Flags().GetString("username")
	if strings.TrimSpace(username) == "" {
		return v1beta1.SecretSpec{}, errors.New("--type basic-auth requires --username")
	}
	passwordFile, _ := cmd.Flags().GetString("from-file")
	tokenFile, _ := cmd.Flags().GetString("token-file")
	passwordStdin, _ := cmd.Flags().GetBool("password-stdin")
	tokenStdin, _ := cmd.Flags().GetBool("token-stdin")
	sources := 0
	for _, set := range []bool{passwordFile != "", tokenFile != "", passwordStdin, tokenStdin} {
		if set {
			sources++
		}
	}
	if sources != 1 {
		return v1beta1.SecretSpec{}, errors.New(
			"--type basic-auth requires exactly one of --password-stdin, --token-stdin, --from-file (password) or --token-file",
		)
	}
	auth := &v1beta1.SecretBasicAuth{Username: strings.TrimSpace(username)}
	var err error
	switch {
	case passwordStdin:
		auth.Password, err = readLine(cmd.InOrStdin(), "stdin")
	case tokenStdin:
		auth.Token, err = readLine(cmd.InOrStdin(), "stdin")
	case passwordFile != "":
		auth.Password, err = readLineFile("--from-file", passwordFile)
	default:
		auth.Token, err = readLineFile("--token-file", tokenFile)
	}
	if err != nil {
		return v1beta1.SecretSpec{}, err
	}
	return v1beta1.SecretSpec{Type: v1beta1.SecretTypeBasicAuth, BasicAuth: auth}, nil
}

// sshKeySpec builds an ssh-key secret from the private key file plus the
// optional passphrase, public key, certificate and pinned host keys.
func sshKeySpec(cmd *cobra.Command) (v1beta1.SecretSpec, error) {
	if err := rejectFlags(cmd, v1beta1.SecretTypeSSHKey,
		"from-literal", "username", "password-stdin", "token-stdin", "token-file"); err != nil {
		return v1beta1.SecretSpec{}, err
	}
	keyFile, _ := cmd.Flags().GetString("from-file")
	if keyFile == "" {
		return v1beta1.SecretSpec{}, errors.New("--type ssh-key requires --from-file <private key>")
	}
	private, err := os.ReadFile(keyFile)
	if err != nil {
		return v1beta1.SecretSpec{}, fmt.Errorf("read --from-file %q: %w", keyFile, err)
	}
	key := &v1beta1.SecretSSHKey{PrivateKey: string(private)}

	passphraseFile, _ := cmd.Flags().GetString("passphrase-file")
	passphraseStdin, _ := cmd.Flags().GetBool("passphrase-stdin")
	switch {
	case passphraseStdin && passphraseFile != "":
		return v1beta1.SecretSpec{}, errors.New("--passphrase-stdin and --passphrase-file are mutually exclusive")
	case passphraseStdin:
		key.Passphrase, err = readLine(cmd.InOrStdin(), "stdin")
	case passphraseFile != "":
		key.Passphrase, err = readLineFile("--passphrase-file", passphraseFile)
	}
	if err != nil {
		return v1beta1.SecretSpec{}, err
	}

	for _, optional := range []struct {
		flag string
		dst  *string
		line bool
	}{
		{"public-key-file", &key.PublicKey, true},
		{"certificate-file", &key.Certificate, true},
		{"known-hosts-file", &key.KnownHosts, false},
	} {
		path, _ := cmd.Flags().GetString(optional.flag)
		if path == "" {
			continue
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return v1beta1.SecretSpec{}, fmt.Errorf("read --%s %q: %w", optional.flag, path, readErr)
		}
		*optional.dst = string(raw)
		if optional.line {
			*optional.dst = strings.TrimSpace(string(raw))
		}
	}
	return v1beta1.SecretSpec{Type: v1beta1.SecretTypeSSHKey, SSHKey: key}, nil
}

// readLine reads one secret value, dropping the trailing line ending a shell
// pipe or an editor adds.
func readLine(r io.Reader, source string) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", source, err)
	}
	value := strings.TrimRight(string(raw), "\r\n")
	if value == "" {
		return "", fmt.Errorf("%s is empty", source)
	}
	return value, nil
}

func readLineFile(flag, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read %s %q: %w", flag, path, err)
	}
	defer func() { _ = f.Close() }()
	return readLine(f, flag+" "+path)
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

func printSecretResult(cmd *cobra.Command, result kukeonv1.CreateSecretResult) {
	cmd.Printf(
		"Secret %q (realm %q, space %q, stack %q)\n",
		result.Secret.Metadata.Name,
		result.Secret.Metadata.Realm,
		result.Secret.Metadata.Space,
		result.Secret.Metadata.Stack,
	)
	label := "data"
	if t := result.Secret.Spec.Type; t != "" && t != v1beta1.SecretTypeOpaque {
		label = "data (" + t + ")"
	}
	shared.PrintCreationOutcome(cmd, label, result.Secret.Spec.Data != "", result.Created)
	if tag := result.Secret.Spec.Connector; tag != "" {
		cmd.Printf("  - connector: %s\n", tag)
	}
}
