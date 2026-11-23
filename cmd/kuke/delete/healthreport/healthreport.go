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

// Package healthreport implements `kuke delete healthreport` (issue #1619).
package healthreport

import (
	"strings"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewHealthReportCmd builds `kuke delete healthreport <name>`. It removes one
// stored report by metadata.name. latest is not resolved: a delete names the
// report it removes.
func NewHealthReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "healthreport <name>",
		Aliases:       []string{"hr"},
		Short:         "Delete a kind: HealthReport",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			if err = client.DeleteHealthReport(cmd.Context(), name); err != nil {
				return err
			}
			cmd.Printf("Deleted health report %q\n", name)
			return nil
		},
	}
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}
