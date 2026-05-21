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

// Package build hosts the `kuke build` CLI shim. It is a thin wrapper that
// locates the standalone `kukebuild` binary on PATH and exec's it with the
// parsed flags. `kukebuild` embeds BuildKit as a library (issue #522); keeping
// it a separate binary — exec'd here rather than imported — is what keeps
// BuildKit's transitive moby / runc / grpc closure out of the `kuke` and
// `kukeond` import sets (the same rationale as kuketty). This package
// therefore imports none of BuildKit.
package build

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
)

// kukebuildBinary is the name `kuke build` looks up on PATH.
const kukebuildBinary = "kukebuild"

// lookPath / execHandoff are indirected as package vars so tests can drive the
// PATH-lookup and exec-handoff paths without a real kukebuild on disk. In
// production lookPath is exec.LookPath and execHandoff is syscall.Exec, which
// replaces the kuke process image with kukebuild so the operator sees
// kukebuild's progress + exit code directly (a true exec, per the issue).
//
//nolint:gochecknoglobals // test seams for the production PATH lookup + exec
var (
	lookPath    = exec.LookPath
	execHandoff = syscall.Exec
)

// NewBuildCmd builds the `kuke build` subcommand.
func NewBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [-f Dockerfile] [-t name:tag] [--realm <name>] [--build-arg K=V]... <context>",
		Short: "Build an OCI image from a Dockerfile into a realm's containerd namespace",
		Long: "Build an OCI image from a Dockerfile using kukeon's native builder.\n\n" +
			"`kuke build` is a thin shim: it locates the `kukebuild` binary on PATH and\n" +
			"exec's it. `kukebuild` embeds BuildKit, builds the context with the Dockerfile\n" +
			"frontend, and writes the resulting image into the containerd namespace mapped\n" +
			"to --realm (<realm>.kukeon.io), ready for `kuke image get` and `kuke create`.\n\n" +
			"No docker or standalone buildkitd is required — only a running host containerd.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runBuild,
	}

	cmd.Flags().StringP("file", "f", "", "Path to the Dockerfile (default <context>/Dockerfile)")
	cmd.Flags().StringP("tag", "t", "", "Image reference for the built image, name:tag (required)")
	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Target realm; the image lands in <realm>.kukeon.io")
	cmd.Flags().StringArray("build-arg", nil, "Set a build-time variable KEY=VALUE (repeatable)")
	cmd.Flags().String(
		"kukeond-config",
		"",
		"Path to kukeond.yaml; resolves the realm namespace suffix (default /etc/kukeon/kukeond.yaml)",
	)

	return cmd
}

// runBuild resolves kukebuild on PATH and exec's it with the forwarded flags.
// A missing kukebuild surfaces as a clear "install kukebuild" error and a
// non-zero exit; a found kukebuild replaces this process via execHandoff so
// its progress and exit code reach the operator unmediated.
func runBuild(cmd *cobra.Command, args []string) error {
	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return err
	}
	if strings.TrimSpace(tag) == "" {
		return errdefs.ErrImageTagRequired
	}

	binPath, err := lookPath(kukebuildBinary)
	if err != nil {
		return fmt.Errorf(
			"%w: `kuke build` shells out to `%s`, which was not found on PATH — "+
				"install it (e.g. `make kukebuild` then put it on PATH)",
			errdefs.ErrKukebuildNotFound, kukebuildBinary,
		)
	}

	argv, err := buildArgv(cmd, args)
	if err != nil {
		return err
	}

	// True exec hand-off: replace the kuke process with kukebuild so the
	// operator sees kukebuild's own stdout/stderr and exit code. execHandoff
	// only returns on failure (e.g. ENOEXEC); on success it never returns.
	return execHandoff(binPath, argv, os.Environ())
}

// buildArgv assembles the kukebuild argv from the parsed `kuke build` flags
// and the positional context directory. argv[0] is the binary name by exec
// convention. Flags are forwarded in kukebuild's long-flag form.
func buildArgv(cmd *cobra.Command, args []string) ([]string, error) {
	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return nil, err
	}
	file, err := cmd.Flags().GetString("file")
	if err != nil {
		return nil, err
	}
	realm, err := cmd.Flags().GetString("realm")
	if err != nil {
		return nil, err
	}
	buildArgs, err := cmd.Flags().GetStringArray("build-arg")
	if err != nil {
		return nil, err
	}
	kukeondConfig, err := cmd.Flags().GetString("kukeond-config")
	if err != nil {
		return nil, err
	}

	argv := []string{kukebuildBinary, "--tag", tag, "--realm", realm}
	if strings.TrimSpace(file) != "" {
		argv = append(argv, "--file", file)
	}
	if strings.TrimSpace(kukeondConfig) != "" {
		argv = append(argv, "--kukeond-config", kukeondConfig)
	}
	for _, ba := range buildArgs {
		argv = append(argv, "--build-arg", ba)
	}
	// Context directory is positional and validated by cobra.ExactArgs(1).
	argv = append(argv, args[0])
	return argv, nil
}
