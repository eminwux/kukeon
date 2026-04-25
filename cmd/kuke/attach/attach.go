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
// The daemon validates the target's Attachable gate and returns the host
// path of the per-container sbsh control socket; this subcommand opens the
// socket via the on-host `sb` binary using syscall.Exec, so TTY/signal
// handling falls through to sb directly. Bytes never traverse kukeond's
// RPC.
package attach

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// MockExecKey is used to inject a mock execFn via context in tests, so the
// real syscall.Exec (which would replace the test binary) is bypassed.
type MockExecKey struct{}

// execFn replaces the running process with the named binary, à la syscall.Exec.
// Returning an error means the exec call itself failed; on success the
// function does not return.
type execFn func(argv0 string, argv []string, envv []string) error

// NewAttachCmd builds the `kuke attach` cobra command.
func NewAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "attach",
		Aliases:       []string{"att"},
		Short:         "Attach to an Attachable container's sbsh terminal",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runAttach,
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_ATTACH_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_ATTACH_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_ATTACH_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("cell", "", "Cell to attach into")
	_ = viper.BindPFlag(config.KUKE_ATTACH_CELL.ViperKey, cmd.Flags().Lookup("cell"))
	cmd.Flags().String("container", "",
		"Container within the cell to attach to (omit to auto-pick the only non-root attachable)")
	_ = viper.BindPFlag(config.KUKE_ATTACH_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))
	cmd.Flags().String("sb-binary", config.KUKE_ATTACH_SB_BINARY.Default,
		"Path to the on-host sb client binary (looked up on $PATH if not absolute)")
	_ = viper.BindPFlag(config.KUKE_ATTACH_SB_BINARY.ViperKey, cmd.Flags().Lookup("sb-binary"))

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)
	_ = cmd.RegisterFlagCompletionFunc("container", config.CompleteContainerNames)

	return cmd
}

func runAttach(cmd *cobra.Command, _ []string) error {
	realm := strings.TrimSpace(viper.GetString(config.KUKE_ATTACH_REALM.ViperKey))
	space := strings.TrimSpace(viper.GetString(config.KUKE_ATTACH_SPACE.ViperKey))
	stack := strings.TrimSpace(viper.GetString(config.KUKE_ATTACH_STACK.ViperKey))
	cell := strings.TrimSpace(viper.GetString(config.KUKE_ATTACH_CELL.ViperKey))
	container := strings.TrimSpace(viper.GetString(config.KUKE_ATTACH_CONTAINER.ViperKey))
	sbBinary := strings.TrimSpace(viper.GetString(config.KUKE_ATTACH_SB_BINARY.ViperKey))

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
	if sbBinary == "" {
		sbBinary = config.KUKE_ATTACH_SB_BINARY.Default
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
	result, err := client.AttachContainer(cmd.Context(), doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrAttachNotSupported) {
			return fmt.Errorf("container %q in cell %q is not attachable: %w", container, cell, err)
		}
		if errors.Is(err, errdefs.ErrContainerNotFound) {
			return fmt.Errorf("container %q not found in cell %q", container, cell)
		}
		return err
	}
	if result.HostSocketPath == "" {
		return fmt.Errorf("daemon returned empty HostSocketPath for container %q", container)
	}

	sbPath, err := resolveSbBinary(sbBinary)
	if err != nil {
		return err
	}

	exe := resolveExec(cmd)
	argv := []string{sbPath, "attach", "--socket", result.HostSocketPath}
	// On success exe replaces the current process and never returns.
	return exe(sbPath, argv, os.Environ())
}

// pickAttachableContainer enumerates the cell's containers and returns the
// single non-root attachable one. Errors with ErrAttachNoCandidate when
// none exist and ErrAttachAmbiguous (with the candidate list) when more
// than one exist.
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

// resolveSbBinary returns the absolute path of the sb client binary. If the
// caller supplied an absolute path it is used verbatim; otherwise the name
// is looked up on $PATH so operators can drop a binary anywhere.
func resolveSbBinary(name string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("locate sb client binary %q on PATH: %w", name, err)
	}
	return path, nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func resolveExec(cmd *cobra.Command) execFn {
	if mock, ok := cmd.Context().Value(MockExecKey{}).(execFn); ok {
		return mock
	}
	return syscall.Exec
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
