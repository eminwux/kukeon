// Copyright 2026 Emiliano Spinella (eminwux)
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

package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

func newApplyCommand(deps Deps) *cobra.Command {
	var conn connection
	var file string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "apply NAME | -f FILE",
		Short: "Recompose a stored team, or apply a Team document",
		Long: "kuke team apply NAME recomposes a team from its stored Team, without consulting its repository's " +
			"kuketeam.yaml, and prunes the Configs it no longer renders. kuke team apply kuke equals " +
			"kuke team init --reapply.\n" +
			"kuke team apply -f FILE composes the Team document in FILE and stores it, creating the team when it is " +
			"new. FILE is what kuke get team NAME -o yaml prints and what a repository's kuketeam.yaml is; when it " +
			"has no spec.projectSource, the stored one is kept.\n" +
			"--dry-run prints what would be applied and writes nothing. No cells are created or started.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1) == (file != "") {
				return errors.New("apply: name a stored team or -f FILE")
			}
			var raw []byte
			if file != "" {
				var err error
				if raw, err = os.ReadFile(file); err != nil {
					return fmt.Errorf("apply: read %s: %w", file, err)
				}
			}
			s, err := conn.open(cmd, deps, true)
			if err != nil {
				return err
			}
			defer s.close()
			opts := s.options(dryRun)
			var out initialize.Outcome
			if file != "" {
				out, err = initialize.ApplyFile(cmd.Context(), opts, raw, file)
			} else {
				out, err = initialize.ApplyStored(cmd.Context(), opts, args[0], "")
			}
			if err != nil {
				return err
			}
			return PrintOutcome(cmd.OutOrStdout(), out)
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVarP(&file, "file", "f", "", "Team document to compose and store")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be applied; write nothing")
	return cmd
}
