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
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type RealmController interface {
	GetRealm(name string) (*v1beta1.RealmDoc, error)
	ListRealms() ([]*v1beta1.RealmDoc, error)
}

type realmController = RealmController // internal alias for backward compatibility

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewRealmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "realm [name]",
		Aliases:       []string{"realms"},
		Short:         "Get or list realm information",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			var ctrl realmController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(RealmController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

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
				// Get single realm
				realm, err := ctrl.GetRealm(name)
				if err != nil {
					if errors.Is(err, errdefs.ErrRealmNotFound) {
						return fmt.Errorf("realm %q not found", name)
					}
					return err
				}

				return printRealm(cmd, realm, outputFormat)
			}

			// List all realms
			realms, err := ctrl.ListRealms()
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

	return cmd
}

func printRealm(_ *cobra.Command, realm interface{}, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(realm)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(realm)
	case shared.OutputFormatTable:
		// For single resource, show full YAML by default
		return shared.PrintYAML(realm)
	default:
		return shared.PrintYAML(realm)
	}
}

func printRealms(cmd *cobra.Command, realms []*v1beta1.RealmDoc, format shared.OutputFormat) error {
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

		for _, r := range realms {
			state := (&r.Status.State).String()
			cgroup := r.Status.CgroupPath
			if cgroup == "" {
				cgroup = "-"
			}

			rows = append(rows, []string{
				r.Metadata.Name,
				r.Spec.Namespace,
				state,
				cgroup,
			})
		}

		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(realms)
	}
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) GetRealm(name string) (*v1beta1.RealmDoc, error) {
	return w.ctrl.GetRealm(name)
}

func (w *controllerWrapper) ListRealms() ([]*v1beta1.RealmDoc, error) {
	return w.ctrl.ListRealms()
}
