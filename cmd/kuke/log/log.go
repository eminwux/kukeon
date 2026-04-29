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
// session it tails the per-container sbsh capture file. Bytes never
// traverse the daemon RPC: the daemon validates the Attachable gate and
// returns the host path of the capture file; this subcommand opens that
// path directly.
package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
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
// when ctx is cancelled (default follow mode) or returns immediately
// after the first dump (noFollow). Real implementation: tailFile.
type tailFn func(ctx context.Context, path string, out io.Writer, noFollow bool) error

// pollInterval is how long the follow loop waits between EOF reads.
// Polling is intentional: avoiding fsnotify keeps the dependency surface
// small and behaves correctly for sbsh's append-only capture file.
const pollInterval = 200 * time.Millisecond

// NewLogCmd builds the `kuke log` cobra command.
func NewLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "log",
		Aliases:       []string{"logs"},
		Short:         "Tail the sbsh capture file of an Attachable container",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runLog,
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_LOG_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("cell", "", "Cell whose container's capture file to tail")
	_ = viper.BindPFlag(config.KUKE_LOG_CELL.ViperKey, cmd.Flags().Lookup("cell"))
	cmd.Flags().String("container", "",
		"Container within the cell to read (omit to auto-pick the only non-root attachable)")
	_ = viper.BindPFlag(config.KUKE_LOG_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))
	cmd.Flags().Bool("no-follow", false, "Dump current capture file contents and exit (do not follow)")
	_ = viper.BindPFlag(config.KUKE_LOG_NO_FOLLOW.ViperKey, cmd.Flags().Lookup("no-follow"))

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)
	_ = cmd.RegisterFlagCompletionFunc("container", config.CompleteContainerNames)

	return cmd
}

func runLog(cmd *cobra.Command, _ []string) error {
	realm := strings.TrimSpace(viper.GetString(config.KUKE_LOG_REALM.ViperKey))
	space := strings.TrimSpace(viper.GetString(config.KUKE_LOG_SPACE.ViperKey))
	stack := strings.TrimSpace(viper.GetString(config.KUKE_LOG_STACK.ViperKey))
	cell := strings.TrimSpace(viper.GetString(config.KUKE_LOG_CELL.ViperKey))
	container := strings.TrimSpace(viper.GetString(config.KUKE_LOG_CONTAINER.ViperKey))
	noFollow := viper.GetBool(config.KUKE_LOG_NO_FOLLOW.ViperKey)

	if realm == "" {
		return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
	}
	if space == "" {
		return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
	}
	if stack == "" {
		return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
	}
	if cell == "" {
		return fmt.Errorf("%w (--cell)", errdefs.ErrCellNameRequired)
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if container == "" {
		container, err = pickAttachableContainer(cmd.Context(), client, realm, space, stack, cell)
		if err != nil {
			return err
		}
	}

	doc := buildContainerDoc(container, realm, space, stack, cell)
	result, err := client.LogContainer(cmd.Context(), doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrAttachNotSupported) {
			return fmt.Errorf("container %q in cell %q is not attachable: %w", container, cell, err)
		}
		if errors.Is(err, errdefs.ErrContainerNotFound) {
			return fmt.Errorf("container %q not found in cell %q", container, cell)
		}
		return err
	}
	if result.HostCapturePath == "" {
		return fmt.Errorf("daemon returned empty HostCapturePath for container %q", container)
	}

	tail := resolveTail(cmd)
	if tailErr := tail(cmd.Context(), result.HostCapturePath, cmd.OutOrStdout(), noFollow); tailErr != nil {
		if errors.Is(tailErr, os.ErrNotExist) {
			return fmt.Errorf(
				"cell %q container %q has no capture file at %s",
				cell, container, result.HostCapturePath,
			)
		}
		return tailErr
	}
	return nil
}

// tailFile opens path and streams its bytes to out. With noFollow it
// dumps the current contents and returns. Otherwise it dumps and then
// polls for new bytes until ctx is cancelled (SIGINT/SIGTERM). On
// cancellation it returns nil so `kuke log` exits 0 — the user
// pressing Ctrl+C is a benign session end, not a failure.
func tailFile(ctx context.Context, path string, out io.Writer, noFollow bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, copyErr := io.Copy(out, f); copyErr != nil {
		return copyErr
	}
	if noFollow {
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

// pickAttachableContainer enumerates the cell's containers and returns the
// single non-root attachable one. Same shape and semantics as the
// `kuke attach` picker — both subcommands target the same container set.
func pickAttachableContainer(
	ctx context.Context,
	client kukeonv1.Client,
	realm, space, stack, cell string,
) (string, error) {
	specs, err := client.ListContainers(ctx, realm, space, stack, cell)
	if err != nil {
		return "", err
	}

	candidates := make([]string, 0, len(specs))
	for i := range specs {
		spec := specs[i]
		if spec.Root || !spec.Attachable {
			continue
		}
		candidates = append(candidates, spec.ID)
	}
	sort.Strings(candidates)

	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("%w (cell %q)", errdefs.ErrAttachNoCandidate, cell)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("%w (cell %q): candidates: %s",
			errdefs.ErrAttachAmbiguous, cell, strings.Join(candidates, ", "))
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
