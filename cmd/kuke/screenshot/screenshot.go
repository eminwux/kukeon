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

// Package screenshot implements `kuke screenshot`: print an Attachable
// container's current screen as sbsh's terminal emulator has rendered it.
// It takes `kuke attach`'s target shape and reaches the terminal the same
// way — kukeond connects to the container's sbsh control socket and passes
// the connection back (ConnectContainerTerminal) — then makes one sbsh
// Screenshot RPC. Terminal bytes never traverse kukeond.
package screenshot

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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

// Output formats accepted by -o.
const (
	FormatText = "text"
	FormatANSI = "ansi"
	FormatJSON = "json"
)

// Result is the `-o json` document: sbsh's ScreenshotResult, field for
// field. The JSON field names are a stable schema (documented in
// docs/site/cli/kuke-screenshot.md); new fields may be added, existing
// ones are not renamed or removed.
type Result struct {
	Cols          int    `json:"cols"`
	Rows          int    `json:"rows"`
	CursorX       int    `json:"cursorX"`
	CursorY       int    `json:"cursorY"`
	CursorVisible bool   `json:"cursorVisible"`
	AltScreen     bool   `json:"altScreen"`
	Text          string `json:"text"`
	ANSI          string `json:"ansi"`
}

// NewScreenshotCmd builds the `kuke screenshot` cobra command.
func NewScreenshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "screenshot <cell>",
		Short: "Print an Attachable container's current rendered screen",
		Long: `Print the current screen of an Attachable container's terminal, as sbsh's
terminal emulator has rendered it — the grid an attached client would see,
not the raw redraw bytes ` + "`kuke log`" + ` prints.

Output formats (-o):
  text  the plain character grid (default)
  ansi  the grid with SGR color sequences
  json  the full screenshot: size, cursor, alt-screen flag, text and ansi

The target is resolved like ` + "`kuke attach`" + `: the positional cell, --realm,
--space, --stack, and --container (omit it to pick the cell's only non-root
Attachable container).`,
		Example: `  kuke screenshot mycell
  kuke screenshot mycell -o ansi
  kuke screenshot mycell --container claude -o json`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runScreenshot,
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_SCREENSHOT_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", consts.KukeonDefaultSpaceName, "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_SCREENSHOT_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", consts.KukeonDefaultStackName, "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_SCREENSHOT_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("container", "",
		"Container within the cell (omit to auto-pick the only non-root attachable)")
	_ = viper.BindPFlag(config.KUKE_SCREENSHOT_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))
	cmd.Flags().StringP("output", "o", FormatText, "Output format: text, ansi, json")
	_ = viper.BindPFlag(config.KUKE_SCREENSHOT_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("container", config.CompleteContainerNames)
	_ = cmd.RegisterFlagCompletionFunc("output",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return []string{FormatText, FormatANSI, FormatJSON}, cobra.ShellCompDirectiveNoFileComp
		})

	return cmd
}

func runScreenshot(cmd *cobra.Command, args []string) error {
	format := strings.ToLower(strings.TrimSpace(viper.GetString(config.KUKE_SCREENSHOT_OUTPUT.ViperKey)))
	if format == "" {
		format = FormatText
	}
	if format != FormatText && format != FormatANSI && format != FormatJSON {
		return fmt.Errorf("%w: %q", errdefs.ErrInvalidScreenshotFormat, format)
	}

	target := kukeshared.TerminalTarget{
		Cell:      args[0],
		Realm:     viper.GetString(config.KUKE_SCREENSHOT_REALM.ViperKey),
		Space:     viper.GetString(config.KUKE_SCREENSHOT_SPACE.ViperKey),
		Stack:     viper.GetString(config.KUKE_SCREENSHOT_STACK.ViperKey),
		Container: viper.GetString(config.KUKE_SCREENSHOT_CONTAINER.ViperKey),
	}.Trimmed()
	if err := target.Validate(); err != nil {
		return err
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

	var res api.ScreenshotResult
	if err = rc.Screenshot(cmd.Context(), &api.ScreenshotArgs{}, &res); err != nil {
		return err
	}
	return render(cmd.OutOrStdout(), format, res)
}

// render writes res in format. text and ansi print the grid as sbsh
// returned it; json prints the Result document with a trailing newline.
func render(out io.Writer, format string, res api.ScreenshotResult) error {
	switch format {
	case FormatANSI:
		_, err := io.WriteString(out, res.ANSI)
		return err
	case FormatJSON:
		b, err := json.MarshalIndent(Result(res), "", "  ")
		if err != nil {
			return fmt.Errorf("marshal screenshot: %w", err)
		}
		_, err = fmt.Fprintln(out, string(b))
		return err
	default:
		_, err := io.WriteString(out, res.Text)
		return err
	}
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
