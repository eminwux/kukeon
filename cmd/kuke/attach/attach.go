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

// Package attach implements the `kuke attach` thin sbsh client subcommand.
// The daemon validates the target's Attachable gate, dials the
// per-container sbsh control socket, and passes each connected socket back
// over the kukeond socket (ConnectContainerTerminal); this subcommand drives
// the interactive attach loop in-process via sbsh's pkg/attach library over
// those connections, so kuke needs neither an on-host `sb` binary nor access
// to the socket path. Bytes never traverse kukeond's RPC.
package attach

import (
	"context"
	"os"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/sbsh/pkg/attach"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// MockRunKey is used to inject a mock runFn via context in tests, so the
// real pkg/attach.Run (which would open a TTY and connect to a real
// control socket) is bypassed.
type MockRunKey struct{}

// runFn drives the in-process sbsh attach loop. Returns nil on a clean
// detach / context cancel and any unrecoverable controller error
// otherwise.
type runFn func(ctx context.Context, opts attach.Options) error

// NewAttachCmd builds the `kuke attach` cobra command.
func NewAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "attach <cell>",
		Aliases:       []string{"att"},
		Short:         "Attach to an Attachable container's sbsh terminal",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runAttach,
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_ATTACH_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", consts.KukeonDefaultSpaceName, "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_ATTACH_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", consts.KukeonDefaultStackName, "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_ATTACH_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("container", "",
		"Container within the cell to attach to (omit to auto-pick the only non-root attachable)")
	_ = viper.BindPFlag(config.KUKE_ATTACH_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("container", config.CompleteContainerNames)

	return cmd
}

func runAttach(cmd *cobra.Command, args []string) error {
	target := kukeshared.TerminalTarget{
		Cell:      args[0],
		Realm:     viper.GetString(config.KUKE_ATTACH_REALM.ViperKey),
		Space:     viper.GetString(config.KUKE_ATTACH_SPACE.ViperKey),
		Stack:     viper.GetString(config.KUKE_ATTACH_STACK.ViperKey),
		Container: viper.GetString(config.KUKE_ATTACH_CONTAINER.ViperKey),
	}.Trimmed()
	if err := target.Validate(); err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// Cell-level liveness guard (#683, #852) then the container picker:
	// refuse a missing / Stopped / diverged cell before the attach loop
	// dials a now-orphan socket inode.
	doc, err := kukeshared.ResolveTerminalTarget(cmd.Context(), client, target)
	if err != nil {
		return err
	}

	// The daemon dials the container's sbsh control socket and passes the
	// connection back (ConnectContainerTerminal), so this works from a cell
	// that can reach the kukeond socket but not the socket path. The
	// preflight connect enforces the Attachable / task-liveness gates
	// before the attach loop takes over the terminal.
	dialer := kukeshared.NewTerminalDialer(client, doc)
	defer dialer.Close()
	opts := attach.Options{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	if err = dialer.Prepare(cmd.Context(), &opts); err != nil {
		return kukeshared.WrapTerminalConnectError(err, doc)
	}

	run := resolveRun(cmd)
	runErr := run(cmd.Context(), opts)
	if runErr != nil && !kukeshared.IsCleanAttachExit(runErr) {
		return runErr
	}
	return nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

func resolveRun(cmd *cobra.Command) runFn {
	if mock, ok := cmd.Context().Value(MockRunKey{}).(runFn); ok {
		return mock
	}
	return attach.Run
}
