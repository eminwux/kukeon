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
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret [name]",
		Short: "Create a secret within a scope",
		Long: "Create a secret within a scope. Two source modes:\n\n" +
			"  - `kuke create secret <name> --from-literal=KEY=VAL` — inline value, repeatable\n" +
			"  - `kuke create secret <name> --from-file=<path>` — read value from file\n\n" +
			"At least one of --from-literal or --from-file is required.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateSecret,
	}

	cmd.Flags().String("realm", "", "Realm that owns the secret")
	_ = viper.BindPFlag(config.KUKE_CREATE_SECRET_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the secret")
	_ = viper.BindPFlag(config.KUKE_CREATE_SECRET_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the secret")
	_ = viper.BindPFlag(config.KUKE_CREATE_SECRET_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().StringArray("from-literal", nil,
		"Specify a key-value pair as KEY=VAL; repeatable to provide multiple values (joined by newline)")
	cmd.Flags().String("from-file", "", "Read the secret value from a file")

	// Register autocomplete functions for flags
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runCreateSecret(cmd *cobra.Command, args []string) error {
	flags, err := parseCreateSecretFlags(cmd, args)
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
		Spec: v1beta1.SecretSpec{
			Data: flags.data,
		},
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
	data  string
}

func parseCreateSecretFlags(cmd *cobra.Command, args []string) (createSecretFlags, error) {
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

	literals, err := cmd.Flags().GetStringArray("from-literal")
	if err != nil {
		return createSecretFlags{}, err
	}

	fromFile, err := cmd.Flags().GetString("from-file")
	if err != nil {
		return createSecretFlags{}, err
	}

	if len(literals) == 0 && fromFile == "" {
		return createSecretFlags{}, errors.New("at least one of --from-literal or --from-file is required")
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

	if fromFile != "" {
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return createSecretFlags{}, fmt.Errorf("read --from-file %q: %w", fromFile, err)
		}
		flags.data = strings.TrimRight(string(data), "\n")
	}

	// Append literal values after file content (if both specified)
	if len(parts) > 0 {
		if flags.data != "" {
			flags.data += "\n"
		}
		flags.data += strings.Join(parts, "\n")
	}

	return flags, nil
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
	shared.PrintCreationOutcome(cmd, "data", result.Secret.Spec.Data != "", result.Created)
}
