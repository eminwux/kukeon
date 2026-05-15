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

// Package log implements the `kuke log` subcommand. It mirrors `kuke
// attach`'s flag/arg shape but instead of opening an interactive sbsh
// session it prints the per-container output stream. By default it
// dumps the current capture/log file contents and exits; pass
// `-f`/`--follow` to tail until SIGINT, matching `kubectl logs` /
// `docker logs` conventions. Bytes never traverse the daemon RPC:
// the daemon resolves the host path of the relevant stream and this
// subcommand opens that path directly.
//
// Two paths exist depending on the target container's IO model:
//
//   - Attachable containers route output through sbsh, which writes a
//     tty byte stream to HostCapturePath. `kuke log` reads that file.
//   - Non-Attachable containers (including kukeond) have the containerd
//     runtime shim write stdout/stderr to HostLogPath via cio.LogFile.
//     `kuke log` reads that file. Implemented per issue #203.
package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey injects a kukeonv1.Client via context for tests.
type MockControllerKey struct{}

// MockTailKey injects a tailFn via context for tests, so the real follow
// loop (which would block on a real file) can be bypassed.
type MockTailKey struct{}

// tailFn opens path and copies its contents to out, then either returns
// immediately after the first dump (follow=false, the default) or keeps
// streaming new bytes until ctx is cancelled (follow=true). Real
// implementation: tailFile.
type tailFn func(ctx context.Context, path string, out io.Writer, follow bool) error

// pollInterval is how long the follow loop waits between EOF reads.
// Polling is intentional: avoiding fsnotify keeps the dependency surface
// small and behaves correctly for sbsh's append-only capture file.
const pollInterval = 200 * time.Millisecond

// TailFn is the exported alias for tailFile-shaped functions: open path,
// dump current contents to out, and optionally follow until ctx is
// cancelled. Sibling commands that share `kuke log`'s streaming semantics
// (e.g. `kuke daemon logs`) accept values of this type for injection.
type TailFn = tailFn

// TailFile re-exports tailFile so sibling commands that share the same
// log-streaming semantics (e.g. `kuke daemon logs`) can dispatch through
// the same dump-and-exit / follow loop instead of duplicating it.
func TailFile(ctx context.Context, path string, out io.Writer, follow bool) error {
	return tailFile(ctx, path, out, follow)
}

// NewLogCmd builds the `kuke log` cobra command.
func NewLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "log <cell>",
		Aliases:       []string{"logs"},
		Short:         "Print a container's stdout/stderr stream (use -f to follow)",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runLog,
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", consts.KukeonDefaultSpaceName, "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", consts.KukeonDefaultStackName, "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("container", "",
		"Container within the cell to read (omit to auto-pick the only non-root container)")
	_ = viper.BindPFlag(config.KUKE_LOG_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))
	cmd.Flags().BoolP("follow", "f", false, "Tail the file until SIGINT instead of printing current contents and exiting")
	_ = viper.BindPFlag(config.KUKE_LOG_FOLLOW.ViperKey, cmd.Flags().Lookup("follow"))

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("container", config.CompleteContainerNames)

	return cmd
}

func runLog(cmd *cobra.Command, args []string) error {
	cell := strings.TrimSpace(args[0])
	realm := strings.TrimSpace(viper.GetString(config.KUKE_LOG_REALM.ViperKey))
	space := strings.TrimSpace(viper.GetString(config.KUKE_LOG_SPACE.ViperKey))
	stack := strings.TrimSpace(viper.GetString(config.KUKE_LOG_STACK.ViperKey))
	container := strings.TrimSpace(viper.GetString(config.KUKE_LOG_CONTAINER.ViperKey))
	follow := viper.GetBool(config.KUKE_LOG_FOLLOW.ViperKey)

	if cell == "" {
		return fmt.Errorf("%w (positional cell)", errdefs.ErrCellNameRequired)
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if container == "" {
		container, err = kukeshared.PickContainer(cmd.Context(), client, realm, space, stack, cell,
			func(spec v1beta1.ContainerSpec) bool {
				// `kuke log` accepts non-Attachable too — sbsh-capture
				// for Attachable containers, cio.LogFile for the rest
				// (e.g. kukeond). Only the root container is excluded.
				return !spec.Root
			})
		if err != nil {
			return err
		}
	}

	doc := buildContainerDoc(container, realm, space, stack, cell)
	result, err := client.LogContainer(cmd.Context(), doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrContainerNotFound) {
			return fmt.Errorf("container %q not found in cell %q: %w", container, cell, err)
		}
		return err
	}
	streamPath := result.HostCapturePath
	if streamPath == "" {
		streamPath = result.HostLogPath
	}
	if streamPath == "" {
		return fmt.Errorf("daemon returned no stream path for container %q", container)
	}

	tail := resolveTail(cmd)
	if tailErr := tail(cmd.Context(), streamPath, cmd.OutOrStdout(), follow); tailErr != nil {
		if errors.Is(tailErr, os.ErrNotExist) {
			return fmt.Errorf(
				"cell %q container %q has no log file at %s (the runtime shim has not opened it yet — try again after the container produces output)",
				cell,
				container,
				streamPath,
			)
		}
		return tailErr
	}
	return nil
}

// tailFile opens path and streams its bytes to out. With follow=false
// (the default for `kuke log`) it dumps the current contents and
// returns. With follow=true it dumps and then polls for new bytes until
// ctx is cancelled (SIGINT/SIGTERM). On cancellation it returns nil so
// `kuke log -f` exits 0 — the user pressing Ctrl+C is a benign session
// end, not a failure.
func tailFile(ctx context.Context, path string, out io.Writer, follow bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, copyErr := io.Copy(out, f); copyErr != nil {
		return copyErr
	}
	if !follow {
		return nil
	}

	// Install a local cancel scope tied to SIGINT/SIGTERM so Ctrl+C
	// during follow drains the goroutine cleanly. We chain off the
	// caller's ctx so an upstream cancellation still wins.
	followCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-followCtx.Done():
			return nil
		case <-ticker.C:
			// io.Copy from the same file handle resumes at the
			// previous offset; sbsh appends to the capture file so
			// any new bytes since the last copy are now between EOF
			// and the new write boundary.
			if _, copyErr := io.Copy(out, f); copyErr != nil {
				return copyErr
			}
		}
	}
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func resolveTail(cmd *cobra.Command) tailFn {
	if mock, ok := cmd.Context().Value(MockTailKey{}).(tailFn); ok {
		return mock
	}
	return tailFile
}

func buildContainerDoc(name, realm, space, stack, cell string) v1beta1.ContainerDoc {
	return v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   name,
			Labels: make(map[string]string),
		},
		Spec: v1beta1.ContainerSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
			CellID:  cell,
		},
	}
}
