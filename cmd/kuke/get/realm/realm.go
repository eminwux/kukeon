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

package realm

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewRealmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "realm [name]",
		Aliases:       []string{"realms", "r"},
		Short:         "Get or list realm information",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFormat, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}

			selector, err := shared.ParseLabelSelectorFlag(cmd)
			if err != nil {
				return err
			}

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_REALM_NAME.ViperKey))
			}

			if name != "" && !selector.Empty() {
				return errdefs.ErrSelectorWithName
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			if name != "" {
				doc := v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{Name: name},
				}
				result, err := client.GetRealm(cmd.Context(), doc)
				if err != nil {
					if errors.Is(err, errdefs.ErrRealmNotFound) {
						return fmt.Errorf("realm %q not found", name)
					}
					return err
				}
				if !result.MetadataExists {
					return fmt.Errorf("realm %q not found", name)
				}
				return printRealm(cmd, &result.Realm, outputFormat)
			}

			realms, err := client.ListRealms(cmd.Context())
			if err != nil {
				return err
			}
			realms = filterRealmsBySelector(realms, selector)
			return printRealms(cmd, realms, outputFormat)
		},
	}

	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, table for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	shared.RegisterLabelSelectorFlag(cmd)

	// `--no-daemon` is inherited as a persistent flag from the parent `get`
	// command (registered in cmd/kuke/get/get.go) — every `get <kind>`
	// keeps the flag per the user override on #222. The AGENTS.md
	// `make dev-init` regression guard runs `kuke get realms` vs
	// `kuke get realms --no-daemon` against that inherited flag.

	cmd.ValidArgsFunction = config.CompleteRealmNames
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

// filterRealmsBySelector returns the subset of realms whose
// Metadata.Labels satisfy selector. A nil or empty selector returns the
// input slice unmodified so the common no-flag path skips the allocation.
func filterRealmsBySelector(realms []v1beta1.RealmDoc, selector *shared.LabelSelector) []v1beta1.RealmDoc {
	if selector.Empty() {
		return realms
	}
	out := make([]v1beta1.RealmDoc, 0, len(realms))
	for i := range realms {
		if selector.Matches(realms[i].Metadata.Labels) {
			out = append(out, realms[i])
		}
	}
	return out
}

func printRealm(cmd *cobra.Command, realm *v1beta1.RealmDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, realm)
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, realm)
	default:
		// table / wide: render the single found element as a one-row table
		// with the same columns as the list view (kubectl parity).
		return printRealms(cmd, []v1beta1.RealmDoc{*realm}, format)
	}
}

func printRealms(
	cmd *cobra.Command,
	realms []v1beta1.RealmDoc,
	format shared.OutputFormat,
) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, realms)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, realms)
	case shared.OutputFormatTable, shared.OutputFormatWide:
		if len(realms) == 0 {
			cmd.Println("No realms found.")
			return nil
		}
		wide := format == shared.OutputFormatWide
		headers := []string{"NAME", "STATE", "AGE"}
		if wide {
			headers = append(headers, "NAMESPACE")
		}
		now := time.Now()
		rows := make([][]string, 0, len(realms))
		for i := range realms {
			r := &realms[i]
			state := (&r.Status.State).String()
			row := []string{r.Metadata.Name, state, shared.RenderAge(r.Status.CreatedAt, now)}
			if wide {
				row = append(row, r.Spec.Namespace)
			}
			rows = append(rows, row)
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(cmd, realms)
	}
}
