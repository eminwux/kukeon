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
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			outputFormat, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_REALM_NAME.ViperKey))
			}

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
				return printRealm(&result.Realm, outputFormat)
			}

			realms, err := client.ListRealms(cmd.Context())
			if err != nil {
				return err
			}
			return printRealms(cmd, realms, outputFormat)
		},
	}

	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

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

func printRealm(realm *v1beta1.RealmDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(realm)
	default:
		return shared.PrintYAML(realm)
	}
}

func printRealms(cmd *cobra.Command, realms []v1beta1.RealmDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(realms)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(realms)
	case shared.OutputFormatTable:
		if len(realms) == 0 {
			cmd.Println("No realms found.")
			return nil
		}
		headers := []string{"NAME", "NAMESPACE", "STATE", "CGROUP"}
		rows := make([][]string, 0, len(realms))
		for i := range realms {
			r := &realms[i]
			state := (&r.Status.State).String()
			cgroup := r.Status.CgroupPath
			if cgroup == "" {
				cgroup = "-"
			}
			rows = append(rows, []string{r.Metadata.Name, r.Spec.Namespace, state, cgroup})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(realms)
	}
}
