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
	"strings"

	"github.com/eminwux/kukeon/cmd/kuke/purge/shared"
	"github.com/spf13/cobra"
)

func NewRealmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "realm [name]",
		Short:         "Purge a realm with comprehensive cleanup",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctrl, err := shared.ControllerFromCmd(cmd)
			if err != nil {
				return err
			}

			name := strings.TrimSpace(args[0])

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			result, err := ctrl.PurgeRealm(name, force, cascade)
			if err != nil {
				return err
			}

			cmd.Printf("Purged realm %q\n", result.RealmName)
			if len(result.Purged) > 0 {
				cmd.Printf("Additional resources purged: %v\n", result.Purged)
			}
			return nil
		},
	}

	return cmd
}
