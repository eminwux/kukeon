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

// Package serve implements the foreground local web server command.
package serve

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/webserver"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const defaultPort = 5129

func NewServeCmd() *cobra.Command {
	var listen string
	var port int
	cmd := &cobra.Command{
		Use: "serve", Short: "Serve the local browser UI in the foreground", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			host := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey)
			if host == "" {
				host = config.KUKEON_ROOT_HOST.Default
			}
			// Resolve transport errors before binding; Dial is lazy and does not require a running daemon.
			client, err := shared.DaemonClientFromCmd(cmd)
			if err != nil {
				return err
			}
			_ = client.Close()
			return webserver.Run(ctx, listen, port, host, func(ctx context.Context) (kukeonv1.Client, error) {
				return kukeonv1.Dial(ctx, host)
			}, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "localhost", "Loopback IP address or localhost")
	cmd.Flags().IntVar(&port, "port", defaultPort, "HTTP port (0 selects an available port)")
	return cmd
}
