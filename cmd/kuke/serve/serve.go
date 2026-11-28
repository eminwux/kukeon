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
	"path/filepath"
	"syscall"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cloudhost"
	"github.com/eminwux/kukeon/internal/webserver"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const defaultListen = "localhost:5129"

func NewServeCmd() *cobra.Command {
	var listen, browserURL, cloudLink string
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
			var link *webserver.CloudLink
			if cmd.Flags().Changed("cloud-link") {
				link = &webserver.CloudLink{URL: cloudLink}
				if config.HomeDirKnown() {
					link.StatusFile = filepath.Join(config.DefaultKukeDir(), cloudhost.StatusFileName)
				}
			}
			return webserver.Run(ctx, listen, browserURL, host, func(ctx context.Context) (kukeonv1.Client, error) {
				return kukeonv1.Dial(ctx, host)
			}, cmd.OutOrStdout(), cmd.ErrOrStderr(), link)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", defaultListen,
		"Bind address as host:port: localhost, a literal IPv4, or a bracketed IPv6 (port 0 selects an available port)")
	cmd.Flags().StringVar(&browserURL, "url", "",
		"The only browser origin accepted, e.g. http://localhost:5129 (default: accept any Host and Origin)")
	cmd.Flags().StringVar(&cloudLink, "cloud-link", "",
		"Also serve the API to a cloud relay over an outbound wss:// link (enroll first with `kuke cloud enroll`)")
	return cmd
}
