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

package version

import (
	"fmt"
	"os"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/spf13/cobra"
)

type VersionProvider interface {
	Version() string
}

// MockVersionProviderKey is used to inject mock version providers in tests via context.
type MockVersionProviderKey struct{}

type configVersionProvider struct{}

func (p *configVersionProvider) Version() string {
	return config.Version
}

func NewVersionCmd() *cobra.Command {
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, _ []string) {
			var provider VersionProvider
			if mockProvider, ok := cmd.Context().Value(MockVersionProviderKey{}).(VersionProvider); ok {
				provider = mockProvider
			} else {
				provider = &configVersionProvider{}
			}

			fmt.Fprintln(os.Stdout, provider.Version())
		},
	}
	return versionCmd
}
