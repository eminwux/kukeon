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
	"context"
	"fmt"
	"io"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// daemonClient is the subset of kukeonv1.Client needed by the version command.
type daemonClient interface {
	PingVersion(ctx context.Context) (string, error)
	Close() error
}

// MockDaemonClientKey is used to inject mock daemon clients in tests via context.
type MockDaemonClientKey struct{}

func NewVersionCmd() *cobra.Command {
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientVersion := config.Version
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Client: %s\n", clientVersion)

			noDaemon, _ := cmd.Flags().GetBool("no-daemon")
			if noDaemon {
				return nil
			}

			var client daemonClient
			if mockClient, ok := cmd.Context().Value(MockDaemonClientKey{}).(daemonClient); ok {
				client = mockClient
			} else {
				host := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey)
				if host == "" {
					host = config.KUKEON_ROOT_HOST.Default
				}
				c, err := kukeonv1.Dial(cmd.Context(), host)
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: daemon unreachable: %v\n", err)
					return nil
				}
				client = &realDaemonClient{client: c}
			}
			defer func() {
				if client != nil {
					_ = client.Close()
				}
			}()

			daemonVersion, err := client.PingVersion(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: daemon unreachable: %v\n", err)
				return nil
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Daemon: %s\n", daemonVersion)

			if clientVersion != daemonVersion {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: version mismatch (client: %s, daemon: %s)\n", clientVersion, daemonVersion)
				strict, _ := cmd.Flags().GetBool("strict")
				if strict {
					return fmt.Errorf("version mismatch: client=%s daemon=%s", clientVersion, daemonVersion)
				}
			}

			return nil
		},
	}

	shared.RegisterNoDaemonFlag(versionCmd)
	versionCmd.Flags().Bool("strict", false, "exit with non-zero status on version mismatch")

	return versionCmd
}

// realDaemonClient wraps a kukeonv1.Client to satisfy the daemonClient interface.
type realDaemonClient struct {
	client kukeonv1.Client
}

func (r *realDaemonClient) PingVersion(ctx context.Context) (string, error) {
	return r.client.PingVersion(ctx)
}

func (r *realDaemonClient) Close() error {
	return r.client.Close()
}

// compile-time interface check
var _ daemonClient = (*realDaemonClient)(nil)
var _ io.Closer = (*realDaemonClient)(nil)
