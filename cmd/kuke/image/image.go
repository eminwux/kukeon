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

// Package image hosts the `kuke image` parent command and its subcommands:
// `load` (#200) and `delete` (#212). Image *listing* moved to the
// `kuke get image` leaf in #824 — see `cmd/kuke/get/image`.
//
// `kuke image *` is the canonical example of the "daemon-independent,
// in-process by design" command category captured in #217: every subcommand
// wraps containerd's image API directly. Whether `kukeond` is running has
// no effect on their semantics — they manipulate containerd content, not
// `/opt/kukeon/<realm>/` state — so they always construct a local
// in-process Client. `--no-daemon` is not accepted on these commands —
// the runtime ignore from #226 became a flag removal in #222.
package image

import (
	"context"
	"io"
	"log/slog"

	"github.com/eminwux/kukeon/cmd/config"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/client/local"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey injects a mock Client via context for tests. Shared by
// every `kuke image *` subcommand so a single per-test fake can wire up
// load/get/delete behavior together.
type MockControllerKey struct{}

// Client is the narrow surface every `kuke image *` subcommand uses. It is
// satisfied by `*local.Client` (the in-process containerd-backed client)
// and by per-test fakes injected via MockControllerKey. There is no RPC
// implementation by design — see the package doc.
type Client interface {
	io.Closer

	LoadImage(ctx context.Context, realm string, tarball []byte) (kukeonv1.LoadImageResult, error)
	DeleteImage(ctx context.Context, realm, ref string) (kukeonv1.DeleteImageResult, error)
}

// resolveClient returns the Client every `kuke image *` subcommand uses.
// Tests inject a fake via MockControllerKey; otherwise the result is a
// fresh in-process local.Client wired to the root persistent --run-path
// and --containerd-socket flags. `--no-daemon` is not on image commands
// after #222; even when set via env, image commands ignore it — they are
// in-process by design (#226).
//
// The logger fallback (a discard handler when LoggerFromCmd cannot find one
// in the command context) makes this safe to call in tests that drive the
// cobra cmd directly without seeding `types.CtxLogger`. There is no error
// return because every branch produces a usable Client.
func resolveClient(cmd *cobra.Command) Client {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(Client); ok {
		return mockClient
	}
	logger, err := kukshared.LoggerFromCmd(cmd)
	if err != nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return local.New(cmd.Context(), logger, controller.Options{
		RunPath:          viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey),
		ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	})
}

// NewImageCmd builds the `kuke image` parent command and registers its
// subcommands. Persistent flags on the root kuke command are inherited
// automatically.
func NewImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage container images in a realm's containerd namespace",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(NewLoadCmd())
	cmd.AddCommand(NewDeleteCmd())

	return cmd
}
