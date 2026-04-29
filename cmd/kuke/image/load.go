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

package image

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewLoadCmd builds the `kuke image load` subcommand. Either a positional
// tarball path or `--from-docker <ref>` is required; passing both is a usage
// error.
func NewLoadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "load [tarball | -]",
		Short:         "Import an OCI/docker image tarball into a realm's containerd namespace",
		Long:          "Import an OCI/docker image tarball into the containerd namespace mapped to --realm. Pass a tarball path, '-' for stdin, or --from-docker <ref> to shell out to `docker save`.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			realm, err := cmd.Flags().GetString("realm")
			if err != nil {
				return err
			}
			realm = strings.TrimSpace(realm)
			if realm == "" {
				return errdefs.ErrRealmNameRequired
			}

			fromDocker, err := cmd.Flags().GetString("from-docker")
			if err != nil {
				return err
			}
			fromDocker = strings.TrimSpace(fromDocker)

			tarball, err := readTarball(cmd, args, fromDocker)
			if err != nil {
				return err
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.LoadImage(cmd.Context(), realm, tarball)
			if err != nil {
				return err
			}

			printLoadResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Target realm; the image lands in <realm>.kukeon.io")
	cmd.Flags().
		String("from-docker", "", "Image reference to pipe in via `docker save <ref>` (mutually exclusive with the positional tarball)")

	return cmd
}

// readTarball resolves the tarball bytes from one of three sources:
// positional path, '-' for stdin, or --from-docker shelling out to
// `docker save`. Exactly one source must be supplied.
func readTarball(cmd *cobra.Command, args []string, fromDocker string) ([]byte, error) {
	hasPositional := len(args) > 0 && args[0] != ""

	switch {
	case fromDocker != "" && hasPositional:
		return nil, errors.New("pass either a tarball path or --from-docker, not both")
	case fromDocker == "" && !hasPositional:
		return nil, errors.New("a tarball path, '-' for stdin, or --from-docker <ref> is required")
	case fromDocker != "":
		return readFromDocker(cmd, fromDocker)
	}

	if args[0] == "-" {
		tarball, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("failed to read tarball from stdin: %w", err)
		}
		if len(tarball) == 0 {
			return nil, errdefs.ErrTarballRequired
		}
		return tarball, nil
	}

	reader, cleanup, err := kukshared.ReadFileOrStdin(args[0])
	if err != nil {
		return nil, err
	}
	defer func() { _ = cleanup() }()

	tarball, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read tarball: %w", err)
	}
	if len(tarball) == 0 {
		return nil, errdefs.ErrTarballRequired
	}
	return tarball, nil
}

// readFromDocker shells out to `docker save <ref>` and captures the produced
// tarball. Stderr is forwarded to the cobra command's err writer so docker's
// own error messages reach the operator (e.g., "Error response from daemon:
// reference does not exist").
func readFromDocker(cmd *cobra.Command, ref string) ([]byte, error) {
	dockerCmd := exec.CommandContext(cmd.Context(), "docker", "save", ref)
	dockerCmd.Stderr = cmd.ErrOrStderr()
	out, err := dockerCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker save %s: %w", ref, err)
	}
	if len(out) == 0 {
		return nil, errdefs.ErrTarballRequired
	}
	return out, nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukshared.ClientFromCmd(cmd)
}

func printLoadResult(cmd *cobra.Command, result kukeonv1.LoadImageResult) {
	if len(result.Images) == 0 {
		cmd.Printf("loaded 0 images into realm %q (namespace %q)\n", result.Realm, result.Namespace)
		return
	}
	cmd.Printf("loaded %d image(s) into realm %q (namespace %q):\n", len(result.Images), result.Realm, result.Namespace)
	for _, name := range result.Images {
		cmd.Printf("  - %s\n", name)
	}
}
