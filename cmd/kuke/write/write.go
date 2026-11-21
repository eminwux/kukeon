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

// Package write implements `kuke write`: write bytes into an Attachable
// container's terminal input, as if typed. It takes `kuke attach`'s target
// shape and reaches the terminal the same way — kukeond connects to the
// container's sbsh control socket and passes the connection back
// (ConnectContainerTerminal) — then makes one sbsh Write RPC. Input rules
// match `sb write`: caret notation and \xHH escapes are expanded, shell
// escapes are not, `-` reads stdin, and no newline is added.
package write

import (
	"fmt"
	"io"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/sbsh/pkg/api"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey injects a kukeonv1.Client via context for tests.
type MockControllerKey struct{}

// maxArgs is the positional cell plus the optional text argument.
const maxArgs = 2

// NewWriteCmd builds the `kuke write` cobra command.
func NewWriteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "write <cell> [text|-]",
		Short: "Write bytes to an Attachable container's terminal input",
		Long: `Write bytes to an Attachable container's terminal input, as if typed.

Caret notation (^C, ^D, ^[, ^M, ...) and \xHH hex escapes are expanded; \\ is
a literal backslash. Shell-style escapes such as \n are NOT interpreted and
are rejected: use ^M for Enter, \x0A for a line feed, or --raw.

If the text argument is '-', stdin is read until EOF (caret notation still
applies unless --raw is set). No trailing newline is added: submit with an
explicit ^M. --raw sends the bytes verbatim.

The target is resolved like ` + "`kuke attach`" + `: the positional cell, --realm,
--space, --stack, and --container (omit it to pick the cell's only non-root
Attachable container).`,
		Example: `  kuke write mycell "echo hello^M"
  kuke write mycell "^C"
  kuke write mycell --raw "$(printf 'ls\r')"
  cat prompt.txt | kuke write mycell -`,
		Args:          cobra.RangeArgs(1, maxArgs),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runWrite,
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_WRITE_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", consts.KukeonDefaultSpaceName, "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_WRITE_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", consts.KukeonDefaultStackName, "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_WRITE_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("container", "",
		"Container within the cell (omit to auto-pick the only non-root attachable)")
	_ = viper.BindPFlag(config.KUKE_WRITE_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))
	cmd.Flags().Bool("raw", false, "Send bytes verbatim; disable caret and \\x escape expansion")
	_ = viper.BindPFlag(config.KUKE_WRITE_RAW.ViperKey, cmd.Flags().Lookup("raw"))

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("container", config.CompleteContainerNames)

	return cmd
}

func runWrite(cmd *cobra.Command, args []string) error {
	target := kukeshared.TerminalTarget{
		Cell:      args[0],
		Realm:     viper.GetString(config.KUKE_WRITE_REALM.ViperKey),
		Space:     viper.GetString(config.KUKE_WRITE_SPACE.ViperKey),
		Stack:     viper.GetString(config.KUKE_WRITE_STACK.ViperKey),
		Container: viper.GetString(config.KUKE_WRITE_CONTAINER.ViperKey),
	}.Trimmed()
	if err := target.Validate(); err != nil {
		return err
	}

	payload, err := resolvePayload(cmd.InOrStdin(), args[1:], viper.GetBool(config.KUKE_WRITE_RAW.ViperKey))
	if err != nil {
		return err
	}
	// Same as `sb write`: nothing to send is a no-op, not a round trip.
	if len(payload) == 0 {
		return nil
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	rc, release, err := kukeshared.ConnectTerminalClient(cmd, client, target)
	if err != nil {
		return err
	}
	defer release()
	defer func() { _ = rc.Close() }()

	return rc.Write(cmd.Context(), &api.WriteRequest{Data: payload})
}

// resolvePayload applies `sb write`'s input rules to the optional text
// argument: absent → empty, "-" → stdin until EOF, otherwise the argument.
// Unless raw, caret notation and \xHH escapes are expanded.
func resolvePayload(stdin io.Reader, rest []string, raw bool) ([]byte, error) {
	var in []byte
	switch {
	case len(rest) == 0:
		return nil, nil
	case rest[0] == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		in = data
	default:
		in = []byte(rest[0])
	}

	if raw {
		return in, nil
	}
	out, err := parseCaret(in)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrInvalidWritePayload, err)
	}
	return out, nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
