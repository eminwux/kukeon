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

package uninstall

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey injects a mock controller via cmd.Context() for tests.
type MockControllerKey struct{}

func NewUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "uninstall",
		Short:        "Remove all kukeon runtime state from this host",
		Long:         uninstallLongDesc,
		RunE:         runUninstall,
		SilenceUsage: true,
	}

	if err := setupUninstallCmd(cmd); err != nil {
		// setupUninstallCmd only fails if viper.BindPFlag is handed a nil
		// flag — unreachable by construction here. Panic surfaces a future
		// flag-rename or typo at startup instead of letting nil flow into
		// cobra.AddCommand and crash the root command registration.
		panic(fmt.Sprintf("kukeon: uninstall command misconfigured: %v", err))
	}
	return cmd
}

const uninstallLongDesc = `Remove all kukeon runtime state from this host.

This is the global counterpart to "kuke purge" (which is per-resource). It:
  1. Purges every realm with --cascade (drains spaces, stacks, cells,
     containers and their containerd tasks/containers, and deletes the
     containerd namespaces created by kukeon).
  2. Removes /run/kukeon/ recursively.
  3. Removes the configured run path (default /opt/kukeon) recursively.
  4. Removes the kukeon system user and group (no-op if absent).

The /usr/local/bin/kuke binary and the kukeond symlink are NOT removed —
uninstalling runtime state is not the same as uninstalling the binary.

By default the command asks for interactive confirmation. Pass --yes/-y to
skip the prompt (use this in scripts).`

func setupUninstallCmd(cmd *cobra.Command) error {
	cmd.Flags().BoolP("yes", "y", false, "Skip the interactive confirmation prompt")
	if err := viper.BindPFlag(config.KUKE_UNINSTALL_YES.ViperKey, cmd.Flags().Lookup("yes")); err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}
	return nil
}

func runUninstall(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	socketPath := viper.GetString(config.KUKEOND_SOCKET.ViperKey)
	if socketPath == "" {
		socketPath = config.KUKEOND_SOCKET.Default
	}
	socketDir := socketDir(socketPath)
	runPath := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
	}

	if !viper.GetBool(config.KUKE_UNINSTALL_YES.ViperKey) {
		confirmed, err := confirm(cmd, runPath, socketDir)
		if err != nil {
			return err
		}
		if !confirmed {
			cmd.Println("Aborted.")
			return nil
		}
	}

	ctrl := resolveController(cmd, logger, runPath)
	defer func() { _ = ctrl.Close() }()

	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir: socketDir,
	})
	printReport(cmd, report)
	return err
}

func resolveController(cmd *cobra.Command, logger *slog.Logger, runPath string) controller.Controller {
	if mock, ok := cmd.Context().Value(MockControllerKey{}).(controller.Controller); ok {
		return mock
	}
	opts := controller.Options{
		RunPath:          runPath,
		ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	}
	return controller.NewControllerExec(cmd.Context(), logger, opts)
}

func confirm(cmd *cobra.Command, runPath, socketDir string) (bool, error) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "kuke uninstall will remove ALL kukeon runtime state on this host:")
	fmt.Fprintln(out, "  - every realm (cascade), including the kuke-system realm and the kukeond cell")
	fmt.Fprintln(out, "  - the kukeon containerd namespaces")
	fmt.Fprintf(out, "  - %s/ (kukeond socket dir)\n", socketDir)
	fmt.Fprintf(out, "  - %s/ (run path)\n", runPath)
	fmt.Fprintln(out, "  - the kukeon system user and group (if present)")
	fmt.Fprintln(out)

	in := cmd.InOrStdin()
	for {
		fmt.Fprint(out, "Are you sure? (yes/no) ")
		line, err := readLine(in)
		if err != nil {
			return false, fmt.Errorf("read confirmation: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "yes", "y":
			return true, nil
		case "no", "n", "":
			return false, nil
		}
	}
}

func readLine(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && (line == "" || err != io.EOF) {
		return line, err
	}
	return line, nil
}

func printReport(cmd *cobra.Command, report controller.UninstallReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "kuke uninstall:")
	for _, r := range report.Realms {
		switch {
		case r.Err != nil:
			fmt.Fprintf(out, "  - realm %q (namespace %q): FAILED: %v\n", r.Name, r.Namespace, r.Err)
		case r.Purged:
			fmt.Fprintf(out, "  - realm %q (namespace %q): purged\n", r.Name, r.Namespace)
		}
	}
	fmt.Fprintf(out, "  - %s: %s\n", report.SocketDir, dirOutcome(report.SocketDirExists, report.SocketDirRemove))
	fmt.Fprintf(out, "  - %s: %s\n", report.RunPath, dirOutcome(report.RunPathExists, report.RunPathRemove))
	fmt.Fprintf(out, "  - user %q: %s\n", report.UserName, accountOutcome(report.UserExisted, report.UserRemoved))
	fmt.Fprintf(out, "  - group %q: %s\n", report.GroupName, accountOutcome(report.GroupExisted, report.GroupRemoved))
}

func dirOutcome(existed, removed bool) string {
	switch {
	case removed:
		return "removed"
	case existed:
		return "remove failed"
	default:
		return "absent"
	}
}

func accountOutcome(existed, removed bool) string {
	switch {
	case removed:
		return "removed"
	case existed:
		return "remove failed"
	default:
		return "absent"
	}
}

// socketDir mirrors the helper in controller/bootstrap.go so the CLI does
// not need to reach into that package just to derive the socket parent.
func socketDir(socketPath string) string {
	for i := len(socketPath) - 1; i >= 0; i-- {
		if socketPath[i] == '/' {
			if i == 0 {
				return "/"
			}
			return socketPath[:i]
		}
	}
	return "/run/kukeon"
}
